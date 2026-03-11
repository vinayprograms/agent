// Package main provides workflow configuration loading.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
)

// workflow handles the configuration phase of a run.
type workflow struct {
	// Parsed from CLI (populated by kong via RunCmd)
	agentfilePath string
	inputs        map[string]string
	configPath    string
	policyPath    string
	workspacePath string
	statePath     string // CLI --state override
	debug         bool
	sessionLabel  string // Override session directory name (default: Agentfile NAME)

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

	// Strict validation: workspace conflict
	if err := w.validateWorkspaceConfig(); err != nil {
		return err
	}

	// Strict validation: state location conflict
	if err := w.validateStateConfig(); err != nil {
		return err
	}

	// Apply CLI overrides (after validation)
	if w.workspacePath != "" {
		w.cfg.Agent.Workspace = w.workspacePath
	}
	if w.cfg.Agent.Workspace == "" {
		w.cfg.Agent.Workspace, _ = os.Getwd()
	}
	w.cfg.Agent.Workspace = expandAbsPath(w.cfg.Agent.Workspace)
	return nil
}

// expandAbsPath expands ~ and resolves to absolute path.
func expandAbsPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p, _ = filepath.Abs(p)
	}
	return p
}

// validateWorkspaceConfig checks for conflicting workspace settings.
func (w *workflow) validateWorkspaceConfig() error {
	if w.workspacePath == "" || w.cfg.Agent.Workspace == "" {
		return nil
	}
	cliResolved := expandAbsPath(w.workspacePath)
	cfgResolved := expandAbsPath(w.cfg.Agent.Workspace)
	if cliResolved != cfgResolved {
		return fmt.Errorf("workspace conflict: --workspace=%q resolves to %q, agent.toml has %q",
			w.workspacePath, cliResolved, cfgResolved)
	}
	return nil
}

// validateStateConfig checks for conflicting state location settings.
func (w *workflow) validateStateConfig() error {
	if w.statePath == "" || w.cfg.State.Location == "" {
		return nil
	}
	cliResolved := expandAbsPath(w.statePath)
	cfgResolved := expandAbsPath(w.cfg.State.Location)
	if cliResolved != cfgResolved {
		return fmt.Errorf("state location conflict: --state=%q resolves to %q, agent.toml has %q",
			w.statePath, cliResolved, cfgResolved)
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
	policyPath := w.policyPath
	if policyPath == "" {
		policyPath = filepath.Join(w.baseDir, "policy.toml")
	}

	// Validate policy keys before parsing
	if content, err := os.ReadFile(policyPath); err == nil {
		if err := policy.ValidateKeys(string(content)); err != nil {
			return fmt.Errorf("policy validation: %w", err)
		}
	} else if w.policyPath != "" {
		// Explicit path specified but can't read — error
		return fmt.Errorf("failed to read policy file: %w", err)
	}

	var err error
	w.pol, err = policy.LoadFile(policyPath)
	if os.IsNotExist(err) {
		w.pol = policy.New()
		err = nil
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
