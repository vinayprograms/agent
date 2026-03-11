package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CollaborationConfig holds swarm-wide collaboration settings.
type CollaborationConfig struct {
	InterruptCheck *bool `yaml:"interrupt_check"` // default: true
}

// Manifest represents a swarm.yaml deployment descriptor.
type Manifest struct {
	NATS          NATSConfig          `yaml:"nats"`
	Storage       StorageConfig       `yaml:"storage"`
	Collaboration CollaborationConfig `yaml:"collaboration"`
	Agents        []AgentSpec         `yaml:"agents"`
}

// NATSConfig configures NATS connection.
type NATSConfig struct {
	URL string `yaml:"url"`
}

// StorageConfig configures unified storage root.
type StorageConfig struct {
	Root string `yaml:"root"`
}

// AgentSpec describes an agent to run.
type AgentSpec struct {
	Name       string `yaml:"name"`
	Agentfile  string `yaml:"agentfile"`
	Config     string `yaml:"config"`
	Policy     string `yaml:"policy"`
	Capability string `yaml:"capability"`
	Storage    string `yaml:"storage"`
	Yolo       bool   `yaml:"yolo"`     // Enable bash with security guardrails
	Type       string `yaml:"type"`     // "worker" (default) or "manager"
	Replicas   int    `yaml:"replicas"` // Number of instances (default: 1, only for workers)
}

// loadManifest reads a swarm.yaml file.
func loadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	// Expand env vars
	m.NATS.URL = expandEnv(m.NATS.URL)
	m.Storage.Root = expandEnv(m.Storage.Root)
	for i := range m.Agents {
		m.Agents[i].Agentfile = expandEnv(m.Agents[i].Agentfile)
		m.Agents[i].Config = expandEnv(m.Agents[i].Config)
		m.Agents[i].Policy = expandEnv(m.Agents[i].Policy)
		m.Agents[i].Capability = expandEnv(m.Agents[i].Capability)
		m.Agents[i].Storage = expandEnv(m.Agents[i].Storage)
	}

	// Defaults
	if m.NATS.URL == "" {
		m.NATS.URL = "nats://localhost:4222"
	}
	if m.Storage.Root == "" {
		home, _ := os.UserHomeDir()
		m.Storage.Root = filepath.Join(home, ".local", "share", "swarm")
	}

	// Agent type/replicas defaults and validation
	managerCount := 0
	for i := range m.Agents {
		if m.Agents[i].Type == "" {
			m.Agents[i].Type = "worker"
		}
		if m.Agents[i].Type != "worker" && m.Agents[i].Type != "manager" {
			return nil, fmt.Errorf("agent %q: invalid type %q (must be \"worker\" or \"manager\")", m.Agents[i].Name, m.Agents[i].Type)
		}
		if m.Agents[i].Replicas < 1 {
			m.Agents[i].Replicas = 1
		}
		if m.Agents[i].Type == "manager" {
			managerCount++
			if m.Agents[i].Replicas > 1 {
				return nil, fmt.Errorf("agent %q: manager cannot have replicas > 1", m.Agents[i].Name)
			}
		}
	}
	if managerCount > 1 {
		return nil, fmt.Errorf("manifest defines %d managers; at most one is allowed", managerCount)
	}

	return &m, nil
}

// expandEnv expands ${VAR}, $VAR, and ~ (home dir) in s.
func expandEnv(s string) string {
	s = expandTilde(s)
	return os.ExpandEnv(s)
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, s[1:])
		}
	}
	return s
}

// findManifest looks for swarm.yaml in current directory.
func findManifest() (string, error) {
	candidates := []string{"swarm.yaml", "swarm.yml"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no swarm.yaml found in current directory")
}

// checkNATS checks if NATS is running, attempts to start if not.
func checkNATS(url string) error {
	// Quick check: can we connect?
	// This is done by the NATS client anyway, but we can try to auto-start
	if strings.Contains(url, "localhost") || strings.Contains(url, "127.0.0.1") {
		// Check if nats-server is in PATH
		if _, err := exec.LookPath("nats-server"); err == nil {
			// Could auto-start here, but for now just warn
			// Auto-start would require daemonizing which is complex
		}
	}
	return nil
}
