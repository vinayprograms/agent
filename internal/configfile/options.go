// Package configfile holds the shape of a generated agent configuration and
// renders it as agent.toml / policy.toml text. Both `agent config init` and
// the interactive `agent setup` wizard build an Options and hand it here, so
// the two entry points can never drift apart.
package configfile

// Deployment scenarios.
const (
	ScenarioLocal      = "local"      // Personal machine, experimenting
	ScenarioDev        = "dev"        // Development/testing with cloud LLMs
	ScenarioTeam       = "team"       // Small team, shared proxy (LiteLLM)
	ScenarioProduction = "production" // Production with full features
	ScenarioDocker     = "docker"     // Container deployment
)

// LLM providers.
const (
	ProviderAnthropic   = "anthropic"
	ProviderOpenAI      = "openai"
	ProviderGoogle      = "google"
	ProviderGroq        = "groq"
	ProviderMistral     = "mistral"
	ProviderXAI         = "xai"
	ProviderOpenRouter  = "openrouter"
	ProviderOllamaCloud = "ollama-cloud"
	ProviderOllamaLocal = "ollama-local"
	ProviderLiteLLM     = "litellm"
	ProviderLMStudio    = "lmstudio"
	ProviderCustom      = "custom"
)

// Scenarios lists the deployment scenarios in presentation order.
func Scenarios() []string {
	return []string{ScenarioLocal, ScenarioDev, ScenarioTeam, ScenarioProduction, ScenarioDocker}
}

// Providers lists the known LLM providers in presentation order.
func Providers() []string {
	return []string{
		ProviderAnthropic, ProviderOpenAI, ProviderGoogle, ProviderGroq,
		ProviderMistral, ProviderXAI, ProviderOpenRouter, ProviderOllamaCloud,
		ProviderOllamaLocal, ProviderLiteLLM, ProviderLMStudio, ProviderCustom,
	}
}

// Options is the full description of a generated configuration. The zero
// value renders valid TOML; New supplies the defaults the wizard starts from.
type Options struct {
	// Deployment
	Scenario  string
	Workspace string
	ConfigDir string

	// Main LLM
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
	Thinking string

	// Small LLM (for summarization, triage)
	SmallLLMEnabled  bool
	SmallLLMProvider string
	SmallLLMModel    string
	SmallLLMBaseURL  string

	// Profiles
	UseProfiles bool
	Profiles    map[string]ProfileConfig

	// Security
	DefaultDeny  bool
	AllowBash    bool
	AllowWeb     bool
	SecurityMode string // "default" or "paranoid"

	// Features
	EnableMCP       bool
	EnableTelemetry bool
	EnableMemory    bool

	// MCP Servers
	MCPServers map[string]MCPServerSetup

	// Credentials
	CredentialMethod string // "file", "env", "claude-cli"
	// GeneratedBy names the command credited in the generated files'
	// header comment. Empty credits "agent setup".
	GeneratedBy string

	// APIKeyEnv overrides the env var written as llm.api_key_env when
	// CredentialMethod is "env". Empty uses DefaultEnvVar(Provider).
	APIKeyEnv string
}

// MCPServerSetup describes one MCP tool server.
type MCPServerSetup struct {
	Command     string
	Args        []string
	Env         map[string]string
	DeniedTools []string
	// DiscoveredTools are the tools the setup wizard probed from the server.
	// They drive the wizard's deny picker and are never written to TOML.
	DiscoveredTools []string
}

// ProfileConfig is one capability profile.
type ProfileConfig struct {
	Provider string
	Model    string
	BaseURL  string
	Thinking string
}

// ApplyScenario overwrites the security, provider and feature fields with the
// defaults for o.Scenario. An unknown scenario leaves o untouched.
func (o *Options) ApplyScenario() {
	switch o.Scenario {
	case ScenarioLocal:
		o.Provider = ProviderOllamaLocal
		o.DefaultDeny = false
		o.AllowBash = true
		o.AllowWeb = true
		o.SecurityMode = "default"
		o.SmallLLMEnabled = false

	case ScenarioDev:
		o.Provider = ProviderAnthropic
		o.DefaultDeny = false
		o.AllowBash = true
		o.AllowWeb = true
		o.SecurityMode = "default"
		o.SmallLLMEnabled = true

	case ScenarioTeam:
		o.Provider = ProviderLiteLLM
		o.DefaultDeny = true
		o.AllowBash = true
		o.AllowWeb = true
		o.SecurityMode = "default"
		o.SmallLLMEnabled = true
		o.UseProfiles = true

	case ScenarioProduction:
		o.Provider = ProviderLiteLLM
		o.DefaultDeny = true
		o.AllowBash = false
		o.AllowWeb = true
		o.SecurityMode = "paranoid"
		o.SmallLLMEnabled = true
		o.UseProfiles = true
		o.EnableTelemetry = true

	case ScenarioDocker:
		o.Provider = ProviderLiteLLM
		o.DefaultDeny = true
		o.AllowBash = true
		o.AllowWeb = true
		o.SecurityMode = "default"
		o.SmallLLMEnabled = true
		o.CredentialMethod = "env"
	}
}

// SetDefaultModel sets Model to the recommended model for o.Provider.
func (o *Options) SetDefaultModel() {
	switch o.Provider {
	case ProviderAnthropic:
		o.Model = "claude-sonnet-4-20250514"
	case ProviderOpenAI:
		o.Model = "gpt-4o"
	case ProviderGoogle:
		o.Model = "gemini-2.0-flash"
	case ProviderGroq:
		o.Model = "llama-3.3-70b-versatile"
	case ProviderMistral:
		o.Model = "mistral-large-latest"
	case ProviderXAI:
		o.Model = "grok-2"
	case ProviderOpenRouter:
		o.Model = "anthropic/claude-sonnet-4"
	case ProviderOllamaCloud:
		o.Model = "llama3.2"
	case ProviderOllamaLocal:
		o.Model = "llama3.2"
	case ProviderLiteLLM:
		o.Model = "claude-sonnet-4-20250514"
	case ProviderLMStudio:
		o.Model = "local-model"
	default:
		o.Model = ""
	}
}

// SetDefaultSmallModel sets SmallLLMModel to the recommended fast model for
// o.SmallLLMProvider, inheriting BaseURL when both LLMs share a provider.
func (o *Options) SetDefaultSmallModel() {
	switch o.SmallLLMProvider {
	case ProviderAnthropic:
		o.SmallLLMModel = "claude-3-5-haiku-20241022"
	case ProviderOpenAI:
		o.SmallLLMModel = "gpt-4o-mini"
	case ProviderGoogle:
		o.SmallLLMModel = "gemini-2.0-flash"
	case ProviderGroq:
		o.SmallLLMModel = "llama-3.1-8b-instant"
	case ProviderMistral:
		o.SmallLLMModel = "mistral-small-latest"
	case ProviderXAI:
		o.SmallLLMModel = "grok-2" // xAI doesn't have a small model yet
	case ProviderOllamaCloud, ProviderOllamaLocal:
		o.SmallLLMModel = "llama3.2:1b"
	case ProviderLiteLLM:
		o.SmallLLMModel = "claude-3-5-haiku-20241022"
	default:
		o.SmallLLMModel = o.Model
	}
	// Inherit base URL from main LLM if same provider type
	if o.SmallLLMProvider == o.Provider {
		o.SmallLLMBaseURL = o.BaseURL
	}
}

// ConfigureDefaultProfiles fills Profiles with reasonable capability profiles
// for o.Provider.
func (o *Options) ConfigureDefaultProfiles() {
	switch o.Provider {
	case ProviderAnthropic:
		o.Profiles["reasoning"] = ProfileConfig{
			Model:    "claude-opus-4-20250514",
			Thinking: "high",
		}
		o.Profiles["fast"] = ProfileConfig{
			Model:    "claude-3-5-haiku-20241022",
			Thinking: "off",
		}
		o.Profiles["balanced"] = ProfileConfig{
			Model:    "claude-sonnet-4-20250514",
			Thinking: "auto",
		}

	case ProviderOpenAI:
		o.Profiles["reasoning"] = ProfileConfig{
			Model:    "o3",
			Thinking: "high",
		}
		o.Profiles["fast"] = ProfileConfig{
			Model:    "gpt-4o-mini",
			Thinking: "off",
		}
		o.Profiles["balanced"] = ProfileConfig{
			Model:    "gpt-4o",
			Thinking: "auto",
		}

	case ProviderLiteLLM:
		// Generic profiles for proxy
		o.Profiles["reasoning"] = ProfileConfig{
			Model:    "claude-opus-4-20250514",
			Thinking: "high",
		}
		o.Profiles["fast"] = ProfileConfig{
			Model:    "claude-3-5-haiku-20241022",
			Thinking: "off",
		}

	default:
		// Simple fast/slow profiles
		o.Profiles["fast"] = ProfileConfig{
			Model:    o.Model,
			Thinking: "off",
		}
	}
}

// DefaultEnvVar names the environment variable a provider's API key is
// conventionally read from.
func DefaultEnvVar(provider string) string {
	switch provider {
	case ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case ProviderOpenAI:
		return "OPENAI_API_KEY"
	case ProviderGoogle:
		return "GOOGLE_API_KEY"
	case ProviderMistral:
		return "MISTRAL_API_KEY"
	case ProviderGroq:
		return "GROQ_API_KEY"
	default:
		return "API_KEY"
	}
}

// NeedsBaseURL reports whether a provider is reached through a configurable
// endpoint rather than a fixed vendor API.
func NeedsBaseURL(provider string) bool {
	switch provider {
	case ProviderOllamaLocal, ProviderLiteLLM, ProviderLMStudio, ProviderOpenRouter, ProviderCustom:
		return true
	}
	return false
}

// DefaultBaseURL is the conventional endpoint for a provider that needs one,
// or "" for providers reached at their vendor API.
func DefaultBaseURL(provider string) string {
	switch provider {
	case ProviderOllamaLocal:
		return "http://localhost:11434/v1"
	case ProviderLMStudio:
		return "http://localhost:1234/v1"
	case ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	case ProviderLiteLLM:
		return "http://localhost:4000/v1"
	default:
		return ""
	}
}
