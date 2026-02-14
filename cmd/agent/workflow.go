// Package main provides workflow configuration loading.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
)

// workflow handles the configuration phase of a run.
type workflow struct {
	// Parsed from CLI (populated by kong via RunCmd)
	agentfilePath  string
	inputs         map[string]string
	configPath     string
	policyPath     string
	workspacePath  string
	persistMemory  bool // CLI flag: --persist-memory (default false)
	debug          bool

	// Loaded artifacts
	wf      *agentfile.Workflow
	cfg     *config.Config
	pol     *policy.Policy
	baseDir string
}

// load loads config, agentfile, and policy.
func (w *workflow) load() error {
	if err := w.loadConfig(); err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := w.loadAgentfile(); err != nil {
		return fmt.Errorf("loading Agentfile: %w", err)
	}
	if err := w.loadPolicy(); err != nil {
		return fmt.Errorf("loading policy: %w", err)
	}
	return nil
}

// loadConfig loads and applies configuration.
func (w *workflow) loadConfig() error {
	var err error
	if w.configPath != "" {
		w.cfg, err = config.LoadFile(w.configPath)
	} else {
		w.cfg, err = config.LoadFile("agent.toml")
		if os.IsNotExist(err) {
			w.cfg = config.Default()
			err = nil
		}
	}
	if err != nil {
		return err
	}

	// Apply CLI overrides
	if w.persistMemory {
		w.cfg.Storage.PersistMemory = true
	}
	if w.workspacePath != "" {
		w.cfg.Agent.Workspace = w.workspacePath
	}
	if w.cfg.Agent.Workspace == "" {
		w.cfg.Agent.Workspace, _ = os.Getwd()
	}
	if !filepath.IsAbs(w.cfg.Agent.Workspace) {
		w.cfg.Agent.Workspace, _ = filepath.Abs(w.cfg.Agent.Workspace)
	}
	return nil
}

// loadAgentfile parses and validates the Agentfile.
func (w *workflow) loadAgentfile() error {
	var err error
	w.wf, err = agentfile.LoadFile(w.agentfilePath)
	if err != nil {
		return err
	}
	w.baseDir = filepath.Dir(w.agentfilePath)
	return nil
}

// loadPolicy loads the policy file.
func (w *workflow) loadPolicy() error {
	var err error
	if w.policyPath != "" {
		w.pol, err = policy.LoadFile(w.policyPath)
	} else {
		defaultPath := filepath.Join(w.baseDir, "policy.toml")
		w.pol, err = policy.LoadFile(defaultPath)
		if os.IsNotExist(err) {
			w.pol = policy.New()
			err = nil
		}
	}
	if err != nil {
		return err
	}
	w.pol.Workspace = w.cfg.Agent.Workspace

	// Register custom security patterns
	w.registerSecurityExtensions()
	return nil
}

// registerSecurityExtensions registers custom patterns and keywords from policy.
func (w *workflow) registerSecurityExtensions() {
	if w.pol.Security == nil {
		return
	}
	if len(w.pol.Security.ExtraPatterns) > 0 {
		if err := security.RegisterCustomPatterns(w.pol.Security.ExtraPatterns); err != nil {
			fmt.Fprintf(os.Stderr, "warning: invalid security pattern in policy: %v\n", err)
		}
	}
	if len(w.pol.Security.ExtraKeywords) > 0 {
		security.RegisterCustomKeywords(w.pol.Security.ExtraKeywords)
	}
}
