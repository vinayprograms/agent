// Package config provides configuration loading and management.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config represents the agent configuration.
type Config struct {
	Agent      AgentConfig           `json:"agent"`
	LLM        LLMConfig             `json:"llm"`        // Default LLM settings
	Profiles   map[string]Profile    `json:"profiles"`   // Capability profiles
	Web        WebConfig             `json:"web"`
	Telemetry  TelemetryConfig       `json:"telemetry"`
	Session    SessionConfig         `json:"session"`
	MCP        MCPConfig             `json:"mcp"`        // MCP tool servers
	Skills     SkillsConfig          `json:"skills"`     // Agent Skills
}

// AgentConfig contains agent identification settings.
type AgentConfig struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
}

// LLMConfig contains LLM provider settings.
type LLMConfig struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env"`
	MaxTokens int    `json:"max_tokens"`
}

// Profile represents a capability profile mapping to a specific LLM configuration.
type Profile struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env"`
	MaxTokens int    `json:"max_tokens"`
}

// WebConfig contains Internet Gateway settings.
type WebConfig struct {
	GatewayURL      string `json:"gateway_url"`
	GatewayTokenEnv string `json:"gateway_token_env"`
}

// TelemetryConfig contains telemetry settings.
type TelemetryConfig struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Protocol string `json:"protocol"` // http, otlp, file, noop
}

// SessionConfig contains session storage settings.
type SessionConfig struct {
	Store string `json:"store"` // sqlite or file
	Path  string `json:"path"`
}

// MCPConfig contains MCP tool server configuration.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers"`
}

// MCPServerConfig configures an MCP server connection.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// SkillsConfig contains Agent Skills configuration.
type SkillsConfig struct {
	Paths []string `json:"paths"` // Directories to search for skills
}

// New creates a new config with defaults.
func New() *Config {
	return &Config{
		LLM: LLMConfig{
			MaxTokens: 4096,
		},
		Session: SessionConfig{
			Store: "file",
		},
		Telemetry: TelemetryConfig{
			Protocol: "noop",
		},
	}
}

// Default returns a default configuration.
func Default() *Config {
	return New()
}

// LoadFile loads configuration from a JSON file.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := New()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return cfg, nil
}

// LoadDefault loads configuration from agent.json in the current directory.
func LoadDefault() (*Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	return LoadFile(filepath.Join(cwd, "agent.json"))
}

// GetAPIKey returns the API key from the configured environment variable.
func (c *Config) GetAPIKey() string {
	if c.LLM.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(c.LLM.APIKeyEnv)
}

// GetGatewayToken returns the gateway token from the configured environment variable.
func (c *Config) GetGatewayToken() string {
	if c.Web.GatewayTokenEnv == "" {
		return ""
	}
	return os.Getenv(c.Web.GatewayTokenEnv)
}

// GetProfile returns the LLM config for a capability profile.
// Falls back to default LLM config if profile not found.
func (c *Config) GetProfile(name string) LLMConfig {
	if name == "" {
		return c.LLM
	}
	if profile, ok := c.Profiles[name]; ok {
		// Fill in defaults from main LLM config
		result := LLMConfig{
			Provider:  profile.Provider,
			Model:     profile.Model,
			APIKeyEnv: profile.APIKeyEnv,
			MaxTokens: profile.MaxTokens,
		}
		if result.Provider == "" {
			result.Provider = c.LLM.Provider
		}
		if result.APIKeyEnv == "" {
			result.APIKeyEnv = c.LLM.APIKeyEnv
		}
		if result.MaxTokens == 0 {
			result.MaxTokens = c.LLM.MaxTokens
		}
		return result
	}
	return c.LLM
}

// GetProfileAPIKey returns the API key for a specific profile.
func (c *Config) GetProfileAPIKey(profileName string) string {
	llmCfg := c.GetProfile(profileName)
	if llmCfg.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(llmCfg.APIKeyEnv)
}
