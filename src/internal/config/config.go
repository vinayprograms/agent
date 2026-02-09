// Package config provides configuration loading and management.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config represents the agent configuration.
type Config struct {
	Agent     AgentConfig           `toml:"agent"`
	LLM       LLMConfig             `toml:"llm"`        // Default LLM settings
	SmallLLM  LLMConfig             `toml:"small_llm"`  // Fast/cheap model for summarization
	Embedding EmbeddingConfig       `toml:"embedding"`  // Embedding model for semantic memory
	Profiles  map[string]Profile    `toml:"profiles"`   // Capability profiles
	Web       WebConfig             `toml:"web"`
	Telemetry TelemetryConfig       `toml:"telemetry"`
	Storage   StorageConfig         `toml:"storage"`    // Persistent storage settings
	MCP       MCPConfig             `toml:"mcp"`        // MCP tool servers
	Skills    SkillsConfig          `toml:"skills"`     // Agent Skills
	Security  SecurityConfig        `toml:"security"`   // Security framework
}

// AgentConfig contains agent identification settings.
type AgentConfig struct {
	ID        string `toml:"id"`
	Workspace string `toml:"workspace"`
}

// LLMConfig contains LLM provider settings.
type LLMConfig struct {
	Provider     string `toml:"provider"`
	Model        string `toml:"model"`
	APIKeyEnv    string `toml:"api_key_env"`
	MaxTokens    int    `toml:"max_tokens"`
	BaseURL      string `toml:"base_url"`      // Custom API endpoint (OpenRouter, LiteLLM, Ollama, LMStudio)
	Thinking     string `toml:"thinking"`      // Thinking level: auto|off|low|medium|high
	MaxRetries   int    `toml:"max_retries"`   // Max retry attempts (default 5)
	RetryBackoff string `toml:"retry_backoff"` // Max backoff duration (default "60s")
}

// Profile represents a capability profile mapping to a specific LLM configuration.
type Profile struct {
	Provider  string `toml:"provider"`
	Model     string `toml:"model"`
	APIKeyEnv string `toml:"api_key_env"`
	MaxTokens int    `toml:"max_tokens"`
	BaseURL   string `toml:"base_url"` // Custom API endpoint
	Thinking  string `toml:"thinking"` // Thinking level: auto|off|low|medium|high
}

// WebConfig contains Internet Gateway settings.
type WebConfig struct {
	GatewayURL      string `toml:"gateway_url"`
	GatewayTokenEnv string `toml:"gateway_token_env"`
}

// TelemetryConfig contains telemetry settings.
type TelemetryConfig struct {
	Enabled  bool   `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
	Protocol string `toml:"protocol"` // http, otlp, file, noop
}

// StorageConfig contains persistent storage settings.
type StorageConfig struct {
	Path          string `toml:"path"`           // Base directory for all persistent data
	PersistMemory bool   `toml:"persist_memory"` // true = memory survives across runs, false = in-memory only
}

// EmbeddingConfig contains embedding provider settings.
//
// Supported providers:
//   - openai:  text-embedding-3-small, text-embedding-3-large, text-embedding-ada-002
//   - google:  text-embedding-004, embedding-001
//   - mistral: mistral-embed
//   - cohere:  embed-english-v3.0, embed-multilingual-v3.0, embed-english-light-v3.0
//   - voyage:  voyage-2, voyage-large-2, voyage-code-2
//   - ollama:  nomic-embed-text, mxbai-embed-large, all-minilm (local)
//   - none:    Disables semantic memory (KV memory still works)
//
// NOT supported (no embedding endpoints):
//   - anthropic (Claude) - use voyage instead (Anthropic's recommended partner)
//   - openrouter - chat completions only
//   - groq - chat completions only
type EmbeddingConfig struct {
	Provider string `toml:"provider"` // openai, google, mistral, cohere, voyage, ollama, none
	Model    string `toml:"model"`    // Model name (e.g., nomic-embed-text, text-embedding-3-small)
	BaseURL  string `toml:"base_url"` // Base URL (for ollama or custom endpoint)
}

// MCPConfig contains MCP tool server configuration.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `toml:"servers"`
}

// MCPServerConfig configures an MCP server connection.
type MCPServerConfig struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
}

// SkillsConfig contains Agent Skills configuration.
type SkillsConfig struct {
	Paths []string `toml:"paths"` // Directories to search for skills
}

// SecurityConfig contains security framework configuration.
type SecurityConfig struct {
	Mode       string `toml:"mode"`        // "default" or "paranoid"
	UserTrust  string `toml:"user_trust"`  // Trust level for user messages: "trusted", "vetted", "untrusted"
	TriageLLM  string `toml:"triage_llm"`  // Profile name for Tier 2 triage (cheap/fast model)
}

// New creates a new config with defaults.
func New() *Config {
	return &Config{
		LLM: LLMConfig{
			MaxTokens: 4096,
		},
		Storage: StorageConfig{
			Path:          "~/.local/grid",
			PersistMemory: true,
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

// LoadFile loads configuration from a TOML file.
func LoadFile(path string) (*Config, error) {
	cfg := New()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return cfg, nil
}

// LoadDefault loads configuration from agent.toml in the current directory.
func LoadDefault() (*Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	return LoadFile(filepath.Join(cwd, "agent.toml"))
}

// GetAPIKey returns the API key from the configured environment variable.
// If api_key_env is not set, uses the default env var for the provider.
func (c *Config) GetAPIKey() string {
	envVar := c.LLM.APIKeyEnv
	if envVar == "" {
		envVar = DefaultAPIKeyEnv(c.LLM.Provider)
	}
	if envVar == "" {
		return ""
	}
	return os.Getenv(envVar)
}

// DefaultAPIKeyEnv returns the default environment variable name for a provider.
func DefaultAPIKeyEnv(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "google":
		return "GOOGLE_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	default:
		return ""
	}
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
