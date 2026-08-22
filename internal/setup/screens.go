package setup

import (
	"slices"

	"github.com/vinayprograms/agentkit/credentials"
)

// Screen represents a setup wizard screen.
type Screen int

const (
	ScreenWelcome Screen = iota
	ScreenScenario
	ScreenProvider
	ScreenModel
	ScreenCustomModel // Text input for model name (Ollama Cloud, LiteLLM, Custom)
	ScreenAPIKey
	ScreenBaseURL
	ScreenThinking
	ScreenSmallLLM
	ScreenSmallLLMProvider
	ScreenSmallLLMModel
	ScreenWorkspace
	ScreenSecurity
	ScreenSecurityMode
	ScreenProfiles
	ScreenProfilesConfig
	ScreenFeatures
	ScreenMCPAdd
	ScreenMCPName
	ScreenMCPCommand
	ScreenMCPArgs
	ScreenMCPProbe
	ScreenMCPDenySelect
	ScreenCredentialMethod
	ScreenConfirm
	ScreenWriteFiles
	ScreenComplete
)

func (m Model) previousScreen() Screen {
	// Handle conditional step skipping when going back
	prev := m.screen - 1

	// Skip small LLM steps if not enabled
	if prev == ScreenSmallLLMModel && !m.config.SmallLLMEnabled {
		prev = ScreenSmallLLM
	}
	if prev == ScreenSmallLLMProvider && !m.config.SmallLLMEnabled {
		prev = ScreenSmallLLM
	}

	// Skip base URL for direct providers
	if prev == ScreenBaseURL && !m.needsBaseURL() {
		prev = ScreenAPIKey
	}

	// Skip profiles config if not using profiles
	if prev == ScreenProfilesConfig && !m.config.UseProfiles {
		prev = ScreenProfiles
	}

	return prev
}

func (m Model) maxCursorForScreen() int {
	switch m.screen {
	case ScreenScenario:
		return len(m.getScenarios()) - 1
	case ScreenProvider:
		return len(m.getProviders()) - 1
	case ScreenModel:
		return len(m.getModels()) - 1
	case ScreenThinking:
		return 4 // auto, off, low, medium, high
	case ScreenSmallLLM:
		return 1 // yes, no
	case ScreenSmallLLMProvider:
		return len(m.getProviders()) - 1 // reuses main provider list
	case ScreenSecurity:
		return 1 // default, strict
	case ScreenSecurityMode:
		return 1 // default, paranoid
	case ScreenProfiles:
		return 1 // yes, no
	case ScreenFeatures:
		return 3 // 4 features (0-3)
	case ScreenMCPAdd:
		return len(m.config.MCPServers) + 1 // edit options + add + done
	case ScreenMCPDenySelect:
		if len(m.probedTools) == 0 {
			return 0
		}
		return len(m.probedTools) - 1
	case ScreenCredentialMethod:
		return 1 // file, env
	case ScreenConfirm:
		return 1 // confirm, cancel
	default:
		return 100 // fallback high number
	}
}

func (m Model) isTextInputScreen() bool {
	switch m.screen {
	case ScreenAPIKey, ScreenBaseURL, ScreenWorkspace, ScreenSmallLLMModel,
		ScreenMCPName, ScreenMCPCommand, ScreenMCPArgs, ScreenCustomModel:
		return true
	}
	return false
}

func (m Model) needsCustomModelInput() bool {
	switch m.config.Provider {
	case ProviderOllamaCloud, ProviderOllamaLocal, ProviderLiteLLM, ProviderLMStudio, ProviderCustom:
		return true
	}
	return false
}

func (m Model) needsBaseURL() bool {
	switch m.config.Provider {
	case ProviderOllamaLocal, ProviderLiteLLM, ProviderLMStudio, ProviderOpenRouter, ProviderCustom:
		return true
	}
	return false
}

func (m Model) getDefaultBaseURL() string {
	switch m.config.Provider {
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

type scenarioOption struct {
	id   string
	name string
	desc string
}

func (m Model) getScenarios() []scenarioOption {
	return []scenarioOption{
		{ScenarioLocal, "Local Development", "Personal machine with Ollama, no API keys needed"},
		{ScenarioDev, "Cloud Development", "Development with cloud LLMs (Anthropic, OpenAI, etc.)"},
		{ScenarioTeam, "Team/Proxy", "Shared LLM proxy (LiteLLM, OpenRouter) with profiles"},
		{ScenarioProduction, "Production", "Full security, telemetry, and monitoring"},
		{ScenarioDocker, "Docker/Container", "Container deployment with env-based credentials"},
	}
}

// Helper functions to find current selection index for edit mode

func (m Model) findScenarioIndex() int {
	if m.config.Scenario == "" {
		return 0
	}
	scenarios := m.getScenarios()
	for i, s := range scenarios {
		if s.id == m.config.Scenario {
			return i
		}
	}
	return 0
}

func (m Model) findProviderIndex(provider string) int {
	if provider == "" {
		return 0
	}
	providers := m.getProviders()
	for i, p := range providers {
		if p.id == provider {
			return i
		}
	}
	return 0
}

func (m Model) findModelIndex() int {
	if m.config.Model == "" {
		return 0
	}
	models := m.getModels()
	for i, model := range models {
		if model.id == m.config.Model {
			return i
		}
	}
	return 0
}

func (m Model) findThinkingIndex() int {
	options := []string{"auto", "off", "low", "medium", "high"}
	for i, opt := range options {
		if opt == m.config.Thinking {
			return i
		}
	}
	return 0
}

type providerOption struct {
	id   string
	name string
	desc string
}

func (m Model) getProviders() []providerOption {
	return []providerOption{
		{ProviderAnthropic, "Anthropic", "Claude models (recommended)"},
		{ProviderOpenAI, "OpenAI", "GPT-4o, o3 models"},
		{ProviderGoogle, "Google", "Gemini models"},
		{ProviderGroq, "Groq", "Fast inference (Llama, Mixtral)"},
		{ProviderMistral, "Mistral", "Mistral models"},
		{ProviderXAI, "xAI", "Grok models"},
		{ProviderOpenRouter, "OpenRouter", "Multi-provider router"},
		{ProviderOllamaCloud, "Ollama Cloud", "Hosted Ollama (api.ollama.com)"},
		{ProviderOllamaLocal, "Ollama Local", "Local Ollama (free, requires install)"},
		{ProviderLiteLLM, "LiteLLM", "Self-hosted proxy (OpenAI-compatible)"},
		{ProviderLMStudio, "LM Studio", "Local models with UI"},
		{ProviderCustom, "Custom", "Custom OpenAI-compatible endpoint"},
	}
}

type modelOption struct {
	id   string
	name string
}

func (m Model) getModels() []modelOption {
	switch m.config.Provider {
	case ProviderAnthropic:
		return []modelOption{
			{"claude-sonnet-4-20250514", "Claude Sonnet 4 (recommended)"},
			{"claude-opus-4-20250514", "Claude Opus 4 (most capable)"},
			{"claude-3-5-haiku-20241022", "Claude 3.5 Haiku (fast)"},
		}
	case ProviderOpenAI:
		return []modelOption{
			{"gpt-4o", "GPT-4o (recommended)"},
			{"gpt-4o-mini", "GPT-4o Mini (fast)"},
			{"o3", "o3 (reasoning)"},
			{"o3-mini", "o3 Mini (fast reasoning)"},
		}
	case ProviderGoogle:
		return []modelOption{
			{"gemini-2.0-flash", "Gemini 2.0 Flash (recommended)"},
			{"gemini-2.0-pro", "Gemini 2.0 Pro"},
			{"gemini-1.5-pro", "Gemini 1.5 Pro"},
		}
	case ProviderGroq:
		return []modelOption{
			{"llama-3.3-70b-versatile", "Llama 3.3 70B (recommended)"},
			{"llama-3.1-8b-instant", "Llama 3.1 8B (fast)"},
			{"mixtral-8x7b-32768", "Mixtral 8x7B"},
		}
	case ProviderMistral:
		return []modelOption{
			{"mistral-large-latest", "Mistral Large (recommended)"},
			{"mistral-medium-latest", "Mistral Medium"},
			{"mistral-small-latest", "Mistral Small (fast)"},
		}
	case ProviderXAI:
		return []modelOption{
			{"grok-2", "Grok 2 (recommended)"},
			{"grok-2-mini", "Grok 2 Mini (fast)"},
		}
	case ProviderOllamaCloud, ProviderOllamaLocal:
		return []modelOption{
			{"llama3.2", "Llama 3.2 (recommended)"},
			{"llama3.2:1b", "Llama 3.2 1B (fast)"},
			{"codellama", "Code Llama"},
			{"mistral", "Mistral 7B"},
			{"phi3", "Phi-3"},
		}
	default:
		return []modelOption{
			{m.config.Model, "Default model"},
		}
	}
}

func getDefaultEnvVar(provider string) string {
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

// getCredentialMethods returns available credential methods for the current provider.
func (m Model) getCredentialMethods() []struct{ name, desc string } {
	methods := []struct{ name, desc string }{}

	// For Anthropic, check if Claude CLI credentials exist
	if m.config.Provider == ProviderAnthropic && hasClaudeCLICredentials() {
		methods = append(methods, struct{ name, desc string }{
			"claude-cli", "Use Claude CLI credentials (already authenticated)",
		})
	}

	methods = append(methods,
		struct{ name, desc string }{"file", "API key in ~/.config/grid/credentials.toml"},
		struct{ name, desc string }{"env", "Environment variables only"},
	)

	return methods
}

// hasClaudeCLICredentials reports whether the Claude Code CLI has a usable
// OAuth token (~/.claude/.credentials.json).
func hasClaudeCLICredentials() bool {
	_, ok := credentials.ClaudeCLICredentials().Resolve("anthropic")
	return ok
}

func (m Model) getSortedMCPServerNames() []string {
	names := make([]string, 0, len(m.config.MCPServers))
	for name := range m.config.MCPServers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
