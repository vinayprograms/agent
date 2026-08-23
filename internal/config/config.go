// Package config provides configuration loading and management.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// EnvConfigPath is an optional env var that points to a TOML config file.
	EnvConfigPath = "AGENT_CONFIG"
)

// DefaultConfigDir is the user-level config directory (~/.config/agent) that
// holds agent.toml and credentials.toml.
func DefaultConfigDir(home string) string {
	return filepath.Join(home, ".config", "agent")
}

// DefaultStateDir is the default base directory for persistent state
// (~/.local/agent), used when [state] location is unset.
func DefaultStateDir(home string) string {
	return filepath.Join(home, ".local", "agent")
}

// LoadOptions controls layered config loading.
type LoadOptions struct {
	// ProjectDir is where project-local agent.toml is searched.
	// Empty means the current working directory.
	ProjectDir string

	// EnvVar is the environment variable that may contain a config path.
	// Defaults to EnvConfigPath when empty.
	EnvVar string

	// CLIPath is the highest-priority config file path (for --config).
	CLIPath string

	// Home is the user's home directory, where the global config lives.
	// Empty means os.UserHomeDir.
	Home string

	// Getenv looks up environment variables. Nil means os.Getenv.
	Getenv func(string) string
}

// Config represents the agent configuration.
type Config struct {
	Agent     AgentConfig          `toml:"agent"`
	LLM       LLMConfig            `toml:"llm"`       // Default LLM settings
	SmallLLM  LLMConfig            `toml:"small_llm"` // Fast/cheap model for summarization
	Profiles  map[string]LLMConfig `toml:"profiles"`  // Capability profiles
	Web       WebConfig            `toml:"web"`
	Telemetry TelemetryConfig      `toml:"telemetry"`
	State     StateConfig          `toml:"state"`     // Persistent state settings
	MCP       MCPConfig            `toml:"mcp"`       // MCP tool servers
	Skills    SkillsConfig         `toml:"skills"`    // Agent Skills
	Security  SecurityConfig       `toml:"security"`  // Security framework
	Timeouts  TimeoutsConfig       `toml:"timeouts"`  // Network operation timeouts
	Limits    LimitsConfig         `toml:"limits"`    // Per-goal execution budget
	Embedding EmbeddingConfig      `toml:"embedding"` // Embedding provider for resume vectors
	Service   ServiceConfig        `toml:"service"`   // Service agent settings (for `agent serve`)

	// Deprecations lists legacy settings found while loading (e.g. [storage]).
	// They were honoured; callers decide whether to warn the user.
	Deprecations []string `toml:"-"`

	// UnknownKeys lists TOML keys present in a loaded file that this struct
	// does not decode — almost always a typo, since every key it does
	// support has a `toml` tag above. Each entry is formatted
	// "<file>: unknown key <dotted.key>"; callers decide whether to warn or
	// fail on them.
	UnknownKeys []string `toml:"-"`
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

// WebConfig contains Internet Gateway settings.
type WebConfig struct {
	GatewayURL      string `toml:"gateway_url"`
	GatewayTokenEnv string `toml:"gateway_token_env"`

	// SearchProvider pins web_search to one provider ("auto", "searxng",
	// "brave", "tavily", "duckduckgo"), or "" for the default cascade.
	SearchProvider string `toml:"search_provider"`
	// SearXNGURL is the SearXNG instance to query; takes precedence over
	// the [searxng] credential and the SEARXNG_URL env var.
	SearXNGURL string `toml:"searxng_url"`
}

// validWebSearchProviders are the accepted [web] search_provider values.
var validWebSearchProviders = map[string]bool{
	"":           true,
	"auto":       true,
	"searxng":    true,
	"brave":      true,
	"tavily":     true,
	"duckduckgo": true,
}

// Protocol selects the telemetry exporter.
type Protocol string

const (
	ProtocolNoop Protocol = "noop" // no exporter
	ProtocolGRPC Protocol = "grpc"
	ProtocolHTTP Protocol = "http"
)

// TelemetryConfig contains telemetry settings.
type TelemetryConfig struct {
	Enabled  bool              `toml:"enabled"`
	Endpoint string            `toml:"endpoint"` // OTLP endpoint (e.g., localhost:4317)
	Protocol Protocol          `toml:"protocol"` // grpc, http, or noop
	Insecure bool              `toml:"insecure"` // Disable TLS (default false)
	Headers  map[string]string `toml:"headers"`  // Auth headers (e.g., DD-API-KEY, x-honeycomb-team)
}

// StateConfig contains persistent state settings.
type StateConfig struct {
	Location string `toml:"location"` // Base directory for persistent data (BM25 memory)
}

// EmbeddingConfig holds embedding provider settings for resume vectors.
type EmbeddingConfig struct {
	// Provider name: "openai", "google", "openai-compat", "litellm", "none"
	Provider string `toml:"provider"`
	// Model name (e.g., "text-embedding-3-small", "text-embedding-004")
	Model string `toml:"model"`
	// APIKey for the embedding provider (or use credentials.toml)
	APIKey string `toml:"api_key"`
	// BaseURL for OpenAI-compatible endpoints (Ollama, LiteLLM, etc.)
	BaseURL string `toml:"base_url"`
}

// MCPConfig contains MCP tool server configuration.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `toml:"servers"`
}

// MCPServerConfig configures an MCP server connection.
type MCPServerConfig struct {
	Command     string            `toml:"command"`
	Args        []string          `toml:"args,omitempty"`
	Env         map[string]string `toml:"env,omitempty"`
	DeniedTools []string          `toml:"denied_tools,omitempty"` // Tools to exclude from LLM
}

// SkillsConfig contains Agent Skills configuration.
type SkillsConfig struct {
	Paths []string `toml:"paths"` // Directories to search for skills
}

// SecurityConfig contains security framework configuration.
type SecurityConfig struct {
	Mode      string `toml:"mode"`       // "default" or "paranoid"
	UserTrust string `toml:"user_trust"` // Trust level for user messages: "trusted", "vetted", "untrusted"
	TriageLLM string `toml:"triage_llm"` // Profile name for Tier 2 triage (cheap/fast model)
}

// TimeoutsConfig contains timeout settings for network operations.
type TimeoutsConfig struct {
	MCP              int `toml:"mcp"`                // MCP tool call timeout in seconds (default 60)
	WebSearch        int `toml:"web_search"`         // web_search timeout in seconds (default 30)
	WebFetch         int `toml:"web_fetch"`          // web_fetch timeout in seconds (default 60)
	SearchCooldownMS int `toml:"search_cooldown_ms"` // minimum ms between DDG queries (default 2000)
}

// LimitsConfig bounds what a single goal may consume before the executor
// stops it. Zero (the default) means unlimited.
type LimitsConfig struct {
	MaxToolCalls int    `toml:"max_tool_calls"` // tool calls per goal
	MaxTurns     int    `toml:"max_turns"`      // LLM turns per goal
	MaxDuration  string `toml:"max_duration"`   // wall-clock per goal, e.g. "10m"
}

// Duration parses MaxDuration. An unset value is zero (unlimited). An
// unparseable value is rejected at load time (see mergeFile), so here it
// can only be empty or valid.
func (l LimitsConfig) Duration() time.Duration {
	d, _ := time.ParseDuration(l.MaxDuration)
	return d
}

// ServiceConfig contains settings for service agent mode (`agent serve`).
type ServiceConfig struct {
	// BusURL is the message bus URL for swarm mode (e.g., "nats://localhost:4222").
	// If empty, agent runs in local HTTP mode.
	BusURL string `toml:"bus_url"`

	// HTTPAddr is the HTTP server address for local mode (e.g., ":8080").
	// Only used if BusURL is empty.
	HTTPAddr string `toml:"http_addr"`

	// QueueGroup for load balancing across multiple instances.
	// Defaults to capability name if not set.
	QueueGroup string `toml:"queue_group"`

	// HeartbeatInterval between heartbeat messages.
	// Default: "5s"
	HeartbeatInterval string `toml:"heartbeat_interval"`

	// DrainTimeout is how long to wait for current task during shutdown.
	// Default: "30s"
	DrainTimeout string `toml:"drain_timeout"`

	// Capability override. If empty, capabilities are inferred from Agentfile.
	Capability string `toml:"capability"`
}

// New creates a new config with defaults.
func New() *Config {
	return &Config{
		LLM: LLMConfig{
			MaxTokens: 4096,
		},
		State: StateConfig{
			Location: DefaultStateDir("~"),
		},
		Telemetry: TelemetryConfig{
			Protocol: ProtocolNoop,
		},
		Timeouts: TimeoutsConfig{
			MCP:              60,   // 60 seconds for MCP calls
			WebSearch:        30,   // 30 seconds for web search
			WebFetch:         60,   // 60 seconds for web fetch
			SearchCooldownMS: 2000, // 2 seconds between DDG queries
		},
	}
}

// LoadFile loads configuration from a TOML file.
// Supports backwards-compatible [storage] → [state] migration.
func LoadFile(path string) (*Config, error) {
	cfg := New()
	if err := mergeFile(cfg, path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadWithPrecedence loads layered config with this priority order:
// global file, project file, env-provided file, and finally CLI file.
// Missing optional files are ignored; explicitly provided env/CLI files must exist.
func LoadWithPrecedence(opts LoadOptions) (*Config, error) {
	cfg := New()

	home := opts.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return nil, fmt.Errorf("resolving home directory: %w", err)
		}
	}
	if err := mergeIfExists(cfg, filepath.Join(DefaultConfigDir(home), "agent.toml")); err != nil {
		return nil, err
	}

	if err := mergeIfExists(cfg, filepath.Join(opts.ProjectDir, "agent.toml")); err != nil {
		return nil, err
	}

	envVar := opts.EnvVar
	if envVar == "" {
		envVar = EnvConfigPath
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if envPath := getenv(envVar); envPath != "" {
		if err := mergeFile(cfg, envPath); err != nil {
			return nil, fmt.Errorf("failed to load config from %s (%s): %w", envVar, envPath, err)
		}
	}

	if opts.CLIPath != "" {
		if err := mergeFile(cfg, opts.CLIPath); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

func mergeIfExists(cfg *Config, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return mergeFile(cfg, path)
}

// mergeFile decodes path over cfg. A legacy [storage] section is honoured as
// [state] and recorded in cfg.Deprecations; both sections together is an error.
func mergeFile(cfg *Config, path string) error {
	file := struct {
		*Config
		Storage *struct {
			Path     string `toml:"path"`
			Location string `toml:"location"`
		} `toml:"storage"`
	}{Config: cfg}
	md, err := toml.DecodeFile(path, &file)
	if err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}
	if v := file.Limits.MaxDuration; v != "" {
		if _, err := time.ParseDuration(v); err != nil {
			return fmt.Errorf("%s: [limits] max_duration %q: %w", path, v, err)
		}
	}
	if v := file.Web.SearchProvider; !validWebSearchProviders[v] {
		return fmt.Errorf("%s: [web] search_provider %q: not a valid provider (want one of auto, searxng, brave, tavily, duckduckgo)", path, v)
	}
	for _, key := range md.Undecoded() {
		cfg.UnknownKeys = append(cfg.UnknownKeys, fmt.Sprintf("%s: unknown key %s", path, key.String()))
	}
	if file.Storage == nil {
		return nil
	}
	if md.IsDefined("state") {
		return fmt.Errorf("config has both [state] and [storage]; remove deprecated [storage] section")
	}
	loc := file.Storage.Location
	if loc == "" {
		loc = file.Storage.Path
	}
	if loc != "" {
		cfg.Deprecations = append(cfg.Deprecations, fmt.Sprintf("[storage] is deprecated, rename to [state] with location = %q", loc))
		cfg.State.Location = loc
	}
	return nil
}

// Profile returns the LLM config for a capability profile, inheriting
// provider, api_key_env and max_tokens from [llm] when the profile omits them.
// Unknown or empty names return the default [llm] config.
func (c *Config) Profile(name string) LLMConfig {
	p, ok := c.Profiles[name]
	if name == "" || !ok {
		return c.LLM
	}
	if p.Provider == "" {
		p.Provider = c.LLM.Provider
	}
	if p.APIKeyEnv == "" {
		p.APIKeyEnv = c.LLM.APIKeyEnv
	}
	if p.MaxTokens == 0 {
		p.MaxTokens = c.LLM.MaxTokens
	}
	return p
}
