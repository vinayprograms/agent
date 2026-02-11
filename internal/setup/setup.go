// Package setup provides the interactive setup wizard for the agent.
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Deployment scenarios
const (
	ScenarioLocal      = "local"       // Personal machine, experimenting
	ScenarioDev        = "dev"         // Development/testing with cloud LLMs
	ScenarioTeam       = "team"        // Small team, shared proxy (LiteLLM)
	ScenarioProduction = "production"  // Production with full features
	ScenarioDocker     = "docker"      // Container deployment
)

// Provider options
const (
	ProviderAnthropic  = "anthropic"
	ProviderOpenAI     = "openai"
	ProviderGoogle     = "google"
	ProviderGroq       = "groq"
	ProviderMistral    = "mistral"
	ProviderOpenRouter = "openrouter"
	ProviderOllama     = "ollama"
	ProviderLiteLLM    = "litellm"
	ProviderLMStudio   = "lmstudio"
	ProviderCustom     = "custom"
)

// Embedding provider options
const (
	EmbeddingOpenAI  = "openai"
	EmbeddingGoogle  = "google"
	EmbeddingMistral = "mistral"
	EmbeddingCohere  = "cohere"
	EmbeddingVoyage  = "voyage"
	EmbeddingOllama  = "ollama"
	EmbeddingLiteLLM = "litellm"
	EmbeddingNone    = "none"
)

// Config holds the setup configuration
type Config struct {
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

	// Embedding (for semantic memory)
	EmbeddingProvider string
	EmbeddingModel    string
	EmbeddingBaseURL  string

	// Profiles
	UseProfiles bool
	Profiles    map[string]ProfileConfig

	// Security
	DefaultDeny   bool
	AllowBash     bool
	AllowWeb      bool
	SecurityMode  string // "default" or "paranoid"

	// Features
	EnableMCP       bool
	EnableTelemetry bool
	EnableMemory    bool
	PersistMemory   bool

	// Credentials
	CredentialMethod string // "file", "env"
}

// ProfileConfig holds a capability profile configuration
type ProfileConfig struct {
	Provider string
	Model    string
	BaseURL  string
	Thinking string
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99")).
			MarginBottom(1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginBottom(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("170")).
			Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39"))
)

// Step represents a setup wizard step
type Step int

const (
	StepWelcome Step = iota
	StepScenario
	StepProvider
	StepModel
	StepAPIKey
	StepBaseURL
	StepThinking
	StepSmallLLM
	StepSmallLLMProvider
	StepSmallLLMModel
	StepEmbedding
	StepEmbeddingModel
	StepWorkspace
	StepSecurity
	StepSecurityMode
	StepProfiles
	StepProfilesConfig
	StepFeatures
	StepCredentialMethod
	StepConfirm
	StepWriteFiles
	StepComplete
)

// Model is the bubbletea model for the setup wizard
type Model struct {
	step      Step
	config    Config
	cursor    int
	textInput textinput.Model
	err       error
	width     int
	height    int

	// For multi-select
	selected map[int]bool

	// Edit mode - true if loading from existing config
	editMode     bool
	existingFile string

	// Results
	filesWritten []string
}

// New creates a new setup model
func New() Model {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	m := Model{
		step:      StepWelcome,
		textInput: ti,
		config: Config{
			Workspace:         ".",
			ConfigDir:         getDefaultConfigDir(),
			Profiles:          make(map[string]ProfileConfig),
			AllowBash:         true,
			AllowWeb:          true,
			EnableMemory:      true,
			PersistMemory:     true,
			SecurityMode:      "default",
			Thinking:          "auto",
			CredentialMethod:  "file",
			EmbeddingProvider: "none",
		},
		selected: make(map[int]bool),
	}

	// Try to load existing configuration
	if err := m.loadExistingConfig(); err == nil {
		m.editMode = true
	}

	return m
}

// existingConfig mirrors the structure in internal/config for loading
type existingConfig struct {
	Agent struct {
		ID        string `toml:"id"`
		Workspace string `toml:"workspace"`
	} `toml:"agent"`
	LLM struct {
		Provider  string `toml:"provider"`
		Model     string `toml:"model"`
		MaxTokens int    `toml:"max_tokens"`
		BaseURL   string `toml:"base_url"`
		Thinking  string `toml:"thinking"`
		APIKeyEnv string `toml:"api_key_env"`
	} `toml:"llm"`
	SmallLLM struct {
		Provider  string `toml:"provider"`
		Model     string `toml:"model"`
		MaxTokens int    `toml:"max_tokens"`
		BaseURL   string `toml:"base_url"`
	} `toml:"small_llm"`
	Embedding struct {
		Provider string `toml:"provider"`
		Model    string `toml:"model"`
		BaseURL  string `toml:"base_url"`
	} `toml:"embedding"`
	Profiles map[string]struct {
		Provider string `toml:"provider"`
		Model    string `toml:"model"`
		BaseURL  string `toml:"base_url"`
		Thinking string `toml:"thinking"`
	} `toml:"profiles"`
	Storage struct {
		Path          string `toml:"path"`
		PersistMemory bool   `toml:"persist_memory"`
	} `toml:"storage"`
	Security struct {
		Mode string `toml:"mode"`
	} `toml:"security"`
	Telemetry struct {
		Enabled bool `toml:"enabled"`
	} `toml:"telemetry"`
	MCP struct {
		Servers map[string]interface{} `toml:"servers"`
	} `toml:"mcp"`
}

type existingPolicy struct {
	DefaultDeny bool   `toml:"default_deny"`
	Workspace   string `toml:"workspace"`
	Tools       struct {
		Bash struct {
			Enabled bool `toml:"enabled"`
		} `toml:"bash"`
		WebSearch struct {
			Enabled bool `toml:"enabled"`
		} `toml:"web_search"`
	} `toml:"tools"`
}

func (m *Model) loadExistingConfig() error {
	// Check for agent.toml
	if _, err := os.Stat("agent.toml"); os.IsNotExist(err) {
		return err
	}

	m.existingFile = "agent.toml"

	var cfg existingConfig
	if _, err := toml.DecodeFile("agent.toml", &cfg); err != nil {
		return err
	}

	// Populate config from loaded file
	if cfg.Agent.Workspace != "" {
		m.config.Workspace = cfg.Agent.Workspace
	}

	// Main LLM
	if cfg.LLM.Provider != "" {
		m.config.Provider = cfg.LLM.Provider
	}
	if cfg.LLM.Model != "" {
		m.config.Model = cfg.LLM.Model
	}
	if cfg.LLM.BaseURL != "" {
		m.config.BaseURL = cfg.LLM.BaseURL
	}
	if cfg.LLM.Thinking != "" {
		m.config.Thinking = cfg.LLM.Thinking
	}
	if cfg.LLM.APIKeyEnv != "" {
		m.config.CredentialMethod = "env"
	}

	// Small LLM
	if cfg.SmallLLM.Provider != "" {
		m.config.SmallLLMEnabled = true
		m.config.SmallLLMProvider = cfg.SmallLLM.Provider
		m.config.SmallLLMModel = cfg.SmallLLM.Model
		m.config.SmallLLMBaseURL = cfg.SmallLLM.BaseURL
	}

	// Embedding
	if cfg.Embedding.Provider != "" {
		m.config.EmbeddingProvider = cfg.Embedding.Provider
		m.config.EmbeddingModel = cfg.Embedding.Model
		m.config.EmbeddingBaseURL = cfg.Embedding.BaseURL
	}

	// Profiles
	if len(cfg.Profiles) > 0 {
		m.config.UseProfiles = true
		m.config.Profiles = make(map[string]ProfileConfig)
		for name, p := range cfg.Profiles {
			m.config.Profiles[name] = ProfileConfig{
				Provider: p.Provider,
				Model:    p.Model,
				BaseURL:  p.BaseURL,
				Thinking: p.Thinking,
			}
		}
	}

	// Storage
	m.config.PersistMemory = cfg.Storage.PersistMemory

	// Security
	if cfg.Security.Mode != "" {
		m.config.SecurityMode = cfg.Security.Mode
	}

	// Telemetry
	m.config.EnableTelemetry = cfg.Telemetry.Enabled

	// MCP
	m.config.EnableMCP = len(cfg.MCP.Servers) > 0

	// Memory enabled if embedding provider is not none
	m.config.EnableMemory = cfg.Embedding.Provider != "" && cfg.Embedding.Provider != "none"

	// Try to load policy.toml too
	if _, err := os.Stat("policy.toml"); err == nil {
		var policy existingPolicy
		if _, err := toml.DecodeFile("policy.toml", &policy); err == nil {
			m.config.DefaultDeny = policy.DefaultDeny
			m.config.AllowBash = policy.Tools.Bash.Enabled
			m.config.AllowWeb = policy.Tools.WebSearch.Enabled
			if policy.Workspace != "" {
				m.config.Workspace = policy.Workspace
			}
		}
	}

	return nil
}

func getDefaultConfigDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "agent")
	}
	return "."
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case filesWrittenMsg:
		m.filesWritten = msg.files
		m.step = StepComplete
		return m, nil
	case errMsg:
		m.err = msg.error
		m.step = StepComplete
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.step == StepComplete {
				return m, tea.Quit
			}
			if m.step == StepWelcome {
				return m, tea.Quit
			}
			// Go back
			if m.step > StepWelcome {
				m.step = m.previousStep()
				m.cursor = 0
			}
			return m, nil

		case "enter":
			return m.handleEnter()

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case "down", "j":
			m.cursor++
			return m, nil

		case " ":
			// Toggle selection for multi-select steps
			if m.step == StepFeatures {
				m.selected[m.cursor] = !m.selected[m.cursor]
			}
			return m, nil

		case "tab":
			return m, nil
		}
	}

	// Update text input
	if m.isTextInputStep() {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) previousStep() Step {
	// Handle conditional step skipping when going back
	prev := m.step - 1

	// Skip small LLM steps if not enabled
	if prev == StepSmallLLMModel && !m.config.SmallLLMEnabled {
		prev = StepSmallLLM
	}
	if prev == StepSmallLLMProvider && !m.config.SmallLLMEnabled {
		prev = StepSmallLLM
	}

	// Skip embedding model if provider is none
	if prev == StepEmbeddingModel && m.config.EmbeddingProvider == "none" {
		prev = StepEmbedding
	}

	// Skip base URL for direct providers
	if prev == StepBaseURL && !m.needsBaseURL() {
		prev = StepAPIKey
	}

	// Skip profiles config if not using profiles
	if prev == StepProfilesConfig && !m.config.UseProfiles {
		prev = StepProfiles
	}

	return prev
}

func (m Model) isTextInputStep() bool {
	switch m.step {
	case StepAPIKey, StepBaseURL, StepWorkspace, StepSmallLLMModel, StepEmbeddingModel:
		return true
	}
	return false
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.step {
	case StepWelcome:
		m.step = StepScenario
		m.cursor = m.findScenarioIndex()

	case StepScenario:
		scenarios := m.getScenarios()
		if m.cursor >= 0 && m.cursor < len(scenarios) {
			m.config.Scenario = scenarios[m.cursor].id
			// Only apply defaults if not in edit mode
			if !m.editMode {
				m.applyScenarioDefaults()
			}
		}
		m.step = StepProvider
		m.cursor = m.findProviderIndex(m.config.Provider)

	case StepProvider:
		providers := m.getProviders()
		if m.cursor >= 0 && m.cursor < len(providers) {
			m.config.Provider = providers[m.cursor].id
			if !m.editMode {
				m.setDefaultModel()
			}
		}
		m.step = StepModel
		m.cursor = m.findModelIndex()

	case StepModel:
		models := m.getModels()
		if m.cursor >= 0 && m.cursor < len(models) {
			m.config.Model = models[m.cursor].id
		}
		m.step = StepAPIKey
		m.textInput.SetValue("")
		m.textInput.Placeholder = "sk-... (leave empty to keep existing)"
		m.textInput.EchoMode = textinput.EchoPassword

	case StepAPIKey:
		if m.textInput.Value() != "" {
			m.config.APIKey = m.textInput.Value()
		}
		m.textInput.EchoMode = textinput.EchoNormal
		if m.needsBaseURL() {
			m.step = StepBaseURL
			if m.editMode && m.config.BaseURL != "" {
				m.textInput.SetValue(m.config.BaseURL)
			} else {
				m.textInput.SetValue(m.getDefaultBaseURL())
			}
			m.textInput.Placeholder = "https://..."
		} else {
			m.step = StepThinking
			m.cursor = m.findThinkingIndex()
		}

	case StepBaseURL:
		m.config.BaseURL = m.textInput.Value()
		m.step = StepThinking
		m.cursor = m.findThinkingIndex()

	case StepThinking:
		thinkingOptions := []string{"auto", "off", "low", "medium", "high"}
		if m.cursor >= 0 && m.cursor < len(thinkingOptions) {
			m.config.Thinking = thinkingOptions[m.cursor]
		}
		m.step = StepSmallLLM
		if m.config.SmallLLMEnabled {
			m.cursor = 0 // Yes
		} else {
			m.cursor = 1 // No
		}

	case StepSmallLLM:
		m.config.SmallLLMEnabled = m.cursor == 0 // Yes
		if m.config.SmallLLMEnabled {
			m.step = StepSmallLLMProvider
			m.cursor = m.findProviderIndex(m.config.SmallLLMProvider)
		} else {
			m.step = StepEmbedding
			m.cursor = m.findEmbeddingIndex()
		}

	case StepSmallLLMProvider:
		providers := m.getProviders()
		if m.cursor >= 0 && m.cursor < len(providers) {
			m.config.SmallLLMProvider = providers[m.cursor].id
			if !m.editMode || m.config.SmallLLMModel == "" {
				m.setDefaultSmallModel()
			}
		}
		m.step = StepSmallLLMModel
		m.textInput.SetValue(m.config.SmallLLMModel)
		m.textInput.Placeholder = "model name"

	case StepSmallLLMModel:
		m.config.SmallLLMModel = m.textInput.Value()
		m.step = StepEmbedding
		m.cursor = m.findEmbeddingIndex()

	case StepEmbedding:
		embeddingProviders := m.getEmbeddingProviders()
		if m.cursor >= 0 && m.cursor < len(embeddingProviders) {
			m.config.EmbeddingProvider = embeddingProviders[m.cursor].id
			if !m.editMode || m.config.EmbeddingModel == "" {
				m.setDefaultEmbeddingModel()
			}
		}
		if m.config.EmbeddingProvider != "none" {
			m.step = StepEmbeddingModel
			m.textInput.SetValue(m.config.EmbeddingModel)
			m.textInput.Placeholder = "model name"
		} else {
			m.step = StepWorkspace
			m.textInput.SetValue(m.config.Workspace)
			m.textInput.Placeholder = "/path/to/workspace"
		}

	case StepEmbeddingModel:
		m.config.EmbeddingModel = m.textInput.Value()
		m.step = StepWorkspace
		m.textInput.SetValue(m.config.Workspace)
		m.textInput.Placeholder = "/path/to/workspace"

	case StepWorkspace:
		m.config.Workspace = m.textInput.Value()
		if m.config.Workspace == "" {
			m.config.Workspace = "."
		}
		m.step = StepSecurity
		if m.config.DefaultDeny {
			m.cursor = 1 // Restrictive
		} else {
			m.cursor = 0 // Permissive
		}

	case StepSecurity:
		// Security stance: permissive (0) or restrictive (1)
		m.config.DefaultDeny = m.cursor == 1
		if m.cursor == 1 && !m.editMode {
			m.config.AllowBash = false
			m.config.AllowWeb = false
		}
		m.step = StepSecurityMode
		if m.config.SecurityMode == "paranoid" {
			m.cursor = 1
		} else {
			m.cursor = 0
		}

	case StepSecurityMode:
		modes := []string{"default", "paranoid"}
		if m.cursor >= 0 && m.cursor < len(modes) {
			m.config.SecurityMode = modes[m.cursor]
		}
		m.step = StepProfiles
		if m.config.UseProfiles {
			m.cursor = 0 // Yes
		} else {
			m.cursor = 1 // No
		}

	case StepProfiles:
		m.config.UseProfiles = m.cursor == 0 // Yes
		if m.config.UseProfiles {
			m.step = StepProfilesConfig
			m.cursor = 0
		} else {
			m.step = StepFeatures
			m.cursor = 0
			m.initFeatureSelection()
		}

	case StepProfilesConfig:
		// Auto-configure profiles based on provider
		m.configureDefaultProfiles()
		m.step = StepFeatures
		m.cursor = 0
		m.initFeatureSelection()

	case StepFeatures:
		m.applyFeatureSelection()
		m.step = StepCredentialMethod
		m.cursor = 0

	case StepCredentialMethod:
		methods := []string{"file", "env"}
		if m.cursor >= 0 && m.cursor < len(methods) {
			m.config.CredentialMethod = methods[m.cursor]
		}
		m.step = StepConfirm
		m.cursor = 0

	case StepConfirm:
		if m.cursor == 0 { // Confirm
			m.step = StepWriteFiles
			return m, m.writeFiles()
		}
		// Cancel - go back to scenario
		m.step = StepScenario
		m.cursor = 0

	case StepComplete:
		return m, tea.Quit
	}

	return m, nil
}

func (m *Model) initFeatureSelection() {
	m.selected = map[int]bool{
		0: m.config.EnableMCP,
		1: m.config.EnableMemory,
		2: m.config.PersistMemory,
		3: m.config.EnableTelemetry,
	}
}

func (m *Model) applyFeatureSelection() {
	m.config.EnableMCP = m.selected[0]
	m.config.EnableMemory = m.selected[1]
	m.config.PersistMemory = m.selected[2]
	m.config.EnableTelemetry = m.selected[3]
}

func (m Model) needsBaseURL() bool {
	switch m.config.Provider {
	case ProviderOllama, ProviderLiteLLM, ProviderLMStudio, ProviderOpenRouter, ProviderCustom:
		return true
	}
	return false
}

func (m Model) getDefaultBaseURL() string {
	switch m.config.Provider {
	case ProviderOllama:
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

func (m *Model) applyScenarioDefaults() {
	switch m.config.Scenario {
	case ScenarioLocal:
		m.config.Provider = ProviderOllama
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.SecurityMode = "default"
		m.config.EmbeddingProvider = "ollama"
		m.config.SmallLLMEnabled = false
		m.config.PersistMemory = false

	case ScenarioDev:
		m.config.Provider = ProviderAnthropic
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.SecurityMode = "default"
		m.config.SmallLLMEnabled = true
		m.config.EmbeddingProvider = "voyage"

	case ScenarioTeam:
		m.config.Provider = ProviderLiteLLM
		m.config.DefaultDeny = true
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.SecurityMode = "default"
		m.config.SmallLLMEnabled = true
		m.config.UseProfiles = true

	case ScenarioProduction:
		m.config.Provider = ProviderLiteLLM
		m.config.DefaultDeny = true
		m.config.AllowBash = false
		m.config.AllowWeb = true
		m.config.SecurityMode = "paranoid"
		m.config.SmallLLMEnabled = true
		m.config.UseProfiles = true
		m.config.EnableTelemetry = true
		m.config.EmbeddingProvider = "voyage"

	case ScenarioDocker:
		m.config.Provider = ProviderLiteLLM
		m.config.DefaultDeny = true
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.SecurityMode = "default"
		m.config.SmallLLMEnabled = true
		m.config.CredentialMethod = "env"
	}
}

func (m *Model) setDefaultModel() {
	switch m.config.Provider {
	case ProviderAnthropic:
		m.config.Model = "claude-sonnet-4-20250514"
	case ProviderOpenAI:
		m.config.Model = "gpt-4o"
	case ProviderGoogle:
		m.config.Model = "gemini-2.0-flash"
	case ProviderGroq:
		m.config.Model = "llama-3.3-70b-versatile"
	case ProviderMistral:
		m.config.Model = "mistral-large-latest"
	case ProviderOpenRouter:
		m.config.Model = "anthropic/claude-sonnet-4"
	case ProviderOllama:
		m.config.Model = "llama3.2"
	case ProviderLiteLLM:
		m.config.Model = "claude-sonnet-4-20250514"
	case ProviderLMStudio:
		m.config.Model = "local-model"
	default:
		m.config.Model = ""
	}
}

func (m *Model) setDefaultSmallModel() {
	switch m.config.SmallLLMProvider {
	case ProviderAnthropic:
		m.config.SmallLLMModel = "claude-3-5-haiku-20241022"
	case ProviderOpenAI:
		m.config.SmallLLMModel = "gpt-4o-mini"
	case ProviderGoogle:
		m.config.SmallLLMModel = "gemini-2.0-flash"
	case ProviderGroq:
		m.config.SmallLLMModel = "llama-3.1-8b-instant"
	case ProviderMistral:
		m.config.SmallLLMModel = "mistral-small-latest"
	case ProviderOllama:
		m.config.SmallLLMModel = "llama3.2:1b"
	case ProviderLiteLLM:
		m.config.SmallLLMModel = "claude-3-5-haiku-20241022"
	default:
		m.config.SmallLLMModel = m.config.Model
	}
	// Inherit base URL from main LLM if same provider type
	if m.config.SmallLLMProvider == m.config.Provider {
		m.config.SmallLLMBaseURL = m.config.BaseURL
	}
}

func (m *Model) setDefaultEmbeddingModel() {
	switch m.config.EmbeddingProvider {
	case EmbeddingOpenAI:
		m.config.EmbeddingModel = "text-embedding-3-small"
	case EmbeddingGoogle:
		m.config.EmbeddingModel = "text-embedding-004"
	case EmbeddingMistral:
		m.config.EmbeddingModel = "mistral-embed"
	case EmbeddingCohere:
		m.config.EmbeddingModel = "embed-english-v3.0"
	case EmbeddingVoyage:
		m.config.EmbeddingModel = "voyage-3-lite"
	case EmbeddingOllama:
		m.config.EmbeddingModel = "nomic-embed-text"
	case EmbeddingLiteLLM:
		m.config.EmbeddingModel = "text-embedding-3-small"
	default:
		m.config.EmbeddingModel = ""
	}
}

func (m *Model) configureDefaultProfiles() {
	// Create reasonable default profiles based on main provider
	switch m.config.Provider {
	case ProviderAnthropic:
		m.config.Profiles["reasoning"] = ProfileConfig{
			Model:    "claude-opus-4-20250514",
			Thinking: "high",
		}
		m.config.Profiles["fast"] = ProfileConfig{
			Model:    "claude-3-5-haiku-20241022",
			Thinking: "off",
		}
		m.config.Profiles["balanced"] = ProfileConfig{
			Model:    "claude-sonnet-4-20250514",
			Thinking: "auto",
		}

	case ProviderOpenAI:
		m.config.Profiles["reasoning"] = ProfileConfig{
			Model:    "o3",
			Thinking: "high",
		}
		m.config.Profiles["fast"] = ProfileConfig{
			Model:    "gpt-4o-mini",
			Thinking: "off",
		}
		m.config.Profiles["balanced"] = ProfileConfig{
			Model:    "gpt-4o",
			Thinking: "auto",
		}

	case ProviderLiteLLM:
		// Generic profiles for proxy
		m.config.Profiles["reasoning"] = ProfileConfig{
			Model:    "claude-opus-4-20250514",
			Thinking: "high",
		}
		m.config.Profiles["fast"] = ProfileConfig{
			Model:    "claude-3-5-haiku-20241022",
			Thinking: "off",
		}

	default:
		// Simple fast/slow profiles
		m.config.Profiles["fast"] = ProfileConfig{
			Model:    m.config.Model,
			Thinking: "off",
		}
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

func (m Model) findEmbeddingIndex() int {
	if m.config.EmbeddingProvider == "" {
		return 0 // none
	}
	providers := m.getEmbeddingProviders()
	for i, p := range providers {
		if p.id == m.config.EmbeddingProvider {
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
		{ProviderOpenRouter, "OpenRouter", "Multi-provider router"},
		{ProviderOllama, "Ollama", "Local models (free)"},
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
	case ProviderOllama:
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

type embeddingOption struct {
	id   string
	name string
	desc string
}

func (m Model) getEmbeddingProviders() []embeddingOption {
	return []embeddingOption{
		{EmbeddingNone, "None", "Disable semantic memory (use scratchpad only)"},
		{EmbeddingOpenAI, "OpenAI", "text-embedding-3-small (recommended)"},
		{EmbeddingVoyage, "Voyage", "Anthropic's recommended partner"},
		{EmbeddingGoogle, "Google", "text-embedding-004"},
		{EmbeddingMistral, "Mistral", "mistral-embed"},
		{EmbeddingCohere, "Cohere", "embed-english-v3.0"},
		{EmbeddingOllama, "Ollama", "nomic-embed-text (local, free)"},
		{EmbeddingLiteLLM, "LiteLLM", "Proxy to any embedding provider"},
	}
}

// View renders the current step
func (m Model) View() string {
	var s strings.Builder

	switch m.step {
	case StepWelcome:
		s.WriteString(m.viewWelcome())
	case StepScenario:
		s.WriteString(m.viewScenario())
	case StepProvider:
		s.WriteString(m.viewProvider())
	case StepModel:
		s.WriteString(m.viewModel())
	case StepAPIKey:
		s.WriteString(m.viewAPIKey())
	case StepBaseURL:
		s.WriteString(m.viewBaseURL())
	case StepThinking:
		s.WriteString(m.viewThinking())
	case StepSmallLLM:
		s.WriteString(m.viewSmallLLM())
	case StepSmallLLMProvider:
		s.WriteString(m.viewSmallLLMProvider())
	case StepSmallLLMModel:
		s.WriteString(m.viewSmallLLMModel())
	case StepEmbedding:
		s.WriteString(m.viewEmbedding())
	case StepEmbeddingModel:
		s.WriteString(m.viewEmbeddingModel())
	case StepWorkspace:
		s.WriteString(m.viewWorkspace())
	case StepSecurity:
		s.WriteString(m.viewSecurity())
	case StepSecurityMode:
		s.WriteString(m.viewSecurityMode())
	case StepProfiles:
		s.WriteString(m.viewProfiles())
	case StepProfilesConfig:
		s.WriteString(m.viewProfilesConfig())
	case StepFeatures:
		s.WriteString(m.viewFeatures())
	case StepCredentialMethod:
		s.WriteString(m.viewCredentialMethod())
	case StepConfirm:
		s.WriteString(m.viewConfirm())
	case StepWriteFiles:
		s.WriteString(m.viewWriting())
	case StepComplete:
		s.WriteString(m.viewComplete())
	}

	return s.String()
}

func (m Model) viewWelcome() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("🤖 Headless Agent Setup"))
	s.WriteString("\n\n")
	if m.editMode {
		s.WriteString(infoStyle.Render("Found existing configuration: " + m.existingFile))
		s.WriteString("\n\n")
		s.WriteString(normalStyle.Render("This wizard will help you edit your configuration."))
		s.WriteString("\n")
		s.WriteString(normalStyle.Render("Current values will be pre-filled."))
		s.WriteString("\n\n")
	} else {
		s.WriteString(normalStyle.Render("This wizard will help you configure your agent."))
		s.WriteString("\n\n")
	}
	s.WriteString(dimStyle.Render("Press Enter to continue, q to quit"))
	return s.String()
}

func (m Model) viewScenario() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Deployment Scenario") + "\n")
	s.WriteString(subtitleStyle.Render("How will you use the agent?") + "\n\n")

	scenarios := m.getScenarios()
	for i, sc := range scenarios {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(sc.name) + "\n")
		s.WriteString("    " + dimStyle.Render(sc.desc) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select, q to go back"))
	return s.String()
}

func (m Model) viewProvider() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("LLM Provider") + "\n")
	s.WriteString(subtitleStyle.Render("Select your main LLM provider") + "\n\n")

	providers := m.getProviders()
	for i, p := range providers {
		if m.cursor >= len(providers) {
			m.cursor = len(providers) - 1
		}
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(p.name) + " " + dimStyle.Render(p.desc) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) viewModel() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Model Selection") + "\n")
	s.WriteString(subtitleStyle.Render("Select the model to use") + "\n\n")

	models := m.getModels()
	for i, model := range models {
		if m.cursor >= len(models) {
			m.cursor = len(models) - 1
		}
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(model.name) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) viewAPIKey() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("API Key") + "\n")
	s.WriteString(subtitleStyle.Render("Enter your API key for "+m.config.Provider) + "\n\n")
	s.WriteString(m.textInput.View() + "\n\n")
	s.WriteString(dimStyle.Render("This will be stored in credentials.toml (mode 0400)"))
	return s.String()
}

func (m Model) viewBaseURL() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Base URL") + "\n")
	s.WriteString(subtitleStyle.Render("Enter the API endpoint URL") + "\n\n")
	s.WriteString(m.textInput.View() + "\n\n")
	s.WriteString(dimStyle.Render("For custom or self-hosted endpoints"))
	return s.String()
}

func (m Model) viewThinking() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Thinking Level") + "\n")
	s.WriteString(subtitleStyle.Render("Configure extended thinking for complex reasoning") + "\n\n")

	options := []struct {
		id   string
		desc string
	}{
		{"auto", "Auto-detect based on task complexity (recommended)"},
		{"off", "Disabled - fastest responses"},
		{"low", "Light reasoning (4K budget)"},
		{"medium", "Moderate reasoning (8K budget)"},
		{"high", "Deep reasoning (16K budget)"},
	}

	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt.id) + " - " + dimStyle.Render(opt.desc) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) viewSmallLLM() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Small LLM") + "\n")
	s.WriteString(subtitleStyle.Render("Configure a fast/cheap model for summarization and triage?") + "\n\n")

	options := []string{"Yes (recommended)", "No"}
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("Used for context summarization, security triage, memory extraction"))
	return s.String()
}

func (m Model) viewSmallLLMProvider() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Small LLM Provider") + "\n")
	s.WriteString(subtitleStyle.Render("Select provider for fast/cheap model") + "\n\n")

	providers := m.getProviders()
	for i, p := range providers {
		if m.cursor >= len(providers) {
			m.cursor = len(providers) - 1
		}
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(p.name) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) viewSmallLLMModel() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Small LLM Model") + "\n")
	s.WriteString(subtitleStyle.Render("Enter the model name for fast/cheap operations") + "\n\n")
	s.WriteString(m.textInput.View() + "\n\n")
	s.WriteString(dimStyle.Render("e.g., claude-3-5-haiku-20241022, gpt-4o-mini"))
	return s.String()
}

func (m Model) viewEmbedding() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Embedding Provider") + "\n")
	s.WriteString(subtitleStyle.Render("Select provider for semantic memory") + "\n\n")

	providers := m.getEmbeddingProviders()
	for i, p := range providers {
		if m.cursor >= len(providers) {
			m.cursor = len(providers) - 1
		}
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(p.name) + " - " + dimStyle.Render(p.desc) + "\n")
	}

	s.WriteString("\n" + infoStyle.Render("Note: Anthropic/Groq don't offer embeddings. Use Voyage (Anthropic partner) or OpenAI."))
	return s.String()
}

func (m Model) viewEmbeddingModel() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Embedding Model") + "\n")
	s.WriteString(subtitleStyle.Render("Enter the embedding model name") + "\n\n")
	s.WriteString(m.textInput.View() + "\n\n")
	s.WriteString(dimStyle.Render("e.g., text-embedding-3-small, nomic-embed-text"))
	return s.String()
}

func (m Model) viewWorkspace() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Workspace Directory") + "\n")
	s.WriteString(subtitleStyle.Render("Where will the agent work?") + "\n\n")
	s.WriteString(m.textInput.View() + "\n\n")
	s.WriteString(dimStyle.Render("The agent will have access to files in this directory"))
	return s.String()
}

func (m Model) viewSecurity() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Security Stance") + "\n")
	s.WriteString(subtitleStyle.Render("Choose security posture") + "\n\n")

	options := []struct {
		name string
		desc string
	}{
		{"Permissive", "Allow most operations (good for development)"},
		{"Restrictive", "Deny by default, explicit allowlists (good for production)"},
	}

	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt.name) + "\n")
		s.WriteString("    " + dimStyle.Render(opt.desc) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) viewSecurityMode() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Security Framework Mode") + "\n")
	s.WriteString(subtitleStyle.Render("How should untrusted content be verified?") + "\n\n")

	options := []struct {
		name string
		desc string
	}{
		{"default", "Smart escalation - verify suspicious content only"},
		{"paranoid", "Verify all untrusted content (higher latency, more secure)"},
	}

	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt.name) + " - " + dimStyle.Render(opt.desc) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) viewProfiles() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Capability Profiles") + "\n")
	s.WriteString(subtitleStyle.Render("Create profiles for different task types?") + "\n\n")

	options := []string{"Yes (recommended for teams)", "No"}
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("Profiles allow using different models for reasoning, fast tasks, etc."))
	return s.String()
}

func (m Model) viewProfilesConfig() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Configuring Profiles") + "\n\n")
	s.WriteString(normalStyle.Render("Creating default profiles based on your provider:\n\n"))

	for name, profile := range m.config.Profiles {
		s.WriteString(selectedStyle.Render("  "+name) + ": " + dimStyle.Render(profile.Model) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("Press Enter to continue (you can edit agent.toml later)"))
	return s.String()
}

func (m Model) viewFeatures() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Features") + "\n")
	s.WriteString(subtitleStyle.Render("Select features to enable (Space to toggle)") + "\n\n")

	features := []struct {
		name string
		desc string
	}{
		{"MCP Tools", "External tool servers via Model Context Protocol"},
		{"Semantic Memory", "Remember insights across conversations (FIL model)"},
		{"Persist Memory", "Save memory to disk between runs"},
		{"Telemetry", "OpenTelemetry observability"},
	}

	for i, f := range features {
		if m.cursor >= len(features) {
			m.cursor = len(features) - 1
		}
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		check := "[ ]"
		if m.selected[i] {
			check = "[✓]"
		}
		s.WriteString(cursor + check + " " + normalStyle.Render(f.name) + " - " + dimStyle.Render(f.desc) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("Space to toggle, Enter to continue"))
	return s.String()
}

func (m Model) viewCredentialMethod() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Credential Storage") + "\n")
	s.WriteString(subtitleStyle.Render("How should credentials be stored?") + "\n\n")

	options := []struct {
		name string
		desc string
	}{
		{"file", "credentials.toml file (mode 0400)"},
		{"env", "Environment variables only"},
	}

	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt.name) + " - " + dimStyle.Render(opt.desc) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) viewConfirm() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Configuration Summary") + "\n\n")

	s.WriteString(normalStyle.Render("Scenario: ") + selectedStyle.Render(m.config.Scenario) + "\n")
	s.WriteString(normalStyle.Render("Provider: ") + selectedStyle.Render(m.config.Provider) + "\n")
	s.WriteString(normalStyle.Render("Model: ") + selectedStyle.Render(m.config.Model) + "\n")
	s.WriteString(normalStyle.Render("Thinking: ") + selectedStyle.Render(m.config.Thinking) + "\n")
	if m.config.BaseURL != "" {
		s.WriteString(normalStyle.Render("Base URL: ") + selectedStyle.Render(m.config.BaseURL) + "\n")
	}

	if m.config.SmallLLMEnabled {
		s.WriteString(normalStyle.Render("Small LLM: ") + selectedStyle.Render(m.config.SmallLLMModel) + "\n")
	}

	if m.config.EmbeddingProvider != "none" {
		s.WriteString(normalStyle.Render("Embedding: ") + selectedStyle.Render(m.config.EmbeddingProvider+"/"+m.config.EmbeddingModel) + "\n")
	}

	s.WriteString(normalStyle.Render("Workspace: ") + selectedStyle.Render(m.config.Workspace) + "\n")
	s.WriteString(normalStyle.Render("Security: ") + selectedStyle.Render(m.config.SecurityMode) + "\n")
	s.WriteString(normalStyle.Render("Credentials: ") + selectedStyle.Render(m.config.CredentialMethod) + "\n")

	s.WriteString("\n" + normalStyle.Render("Files to create:") + "\n")
	s.WriteString(dimStyle.Render("  - agent.toml\n"))
	s.WriteString(dimStyle.Render("  - policy.toml\n"))
	if m.config.CredentialMethod == "file" {
		s.WriteString(dimStyle.Render("  - credentials.toml\n"))
	}

	s.WriteString("\n")
	options := []string{"Create files", "Go back"}
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt) + "\n")
	}

	return s.String()
}

func (m Model) viewWriting() string {
	return (
		titleStyle.Render("Writing Files...") + "\n\n" +
			normalStyle.Render("Creating configuration files..."))
}

func (m Model) viewComplete() string {
	if m.err != nil {
		return (
			errorStyle.Render("Error") + "\n\n" +
				normalStyle.Render(m.err.Error()) + "\n\n" +
				dimStyle.Render("Press q to exit"))
	}

	var s strings.Builder
	s.WriteString(successStyle.Render("✓ Setup Complete!") + "\n\n")
	s.WriteString(normalStyle.Render("Created files:") + "\n")
	for _, f := range m.filesWritten {
		s.WriteString(dimStyle.Render("  - "+f) + "\n")
	}

	s.WriteString("\n" + normalStyle.Render("Next steps:") + "\n")
	s.WriteString(dimStyle.Render("  1. Review agent.toml and policy.toml") + "\n")
	if m.config.CredentialMethod == "env" {
		envVar := getDefaultEnvVar(m.config.Provider)
		s.WriteString(dimStyle.Render("  2. Set "+envVar+" environment variable") + "\n")
		s.WriteString(dimStyle.Render("  3. Run: agent run your-workflow.agent") + "\n")
	} else {
		s.WriteString(dimStyle.Render("  2. Run: agent run your-workflow.agent") + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("Press q to exit"))
	return (s.String())
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

// Messages
type filesWrittenMsg struct {
	files []string
}

type errMsg struct {
	error error
}

func (m Model) writeFiles() tea.Cmd {
	return func() tea.Msg {
		var files []string

		// Write agent.toml
		agentTOML := m.generateAgentTOML()
		if err := os.WriteFile("agent.toml", []byte(agentTOML), 0644); err != nil {
			return errMsg{err}
		}
		files = append(files, "agent.toml")

		// Write policy.toml
		policyTOML := m.generatePolicyTOML()
		if err := os.WriteFile("policy.toml", []byte(policyTOML), 0644); err != nil {
			return errMsg{err}
		}
		files = append(files, "policy.toml")

		// Write credentials.toml if using file method
		if m.config.CredentialMethod == "file" && m.config.APIKey != "" {
			credsTOML := m.generateCredentialsTOML()
			if err := os.WriteFile("credentials.toml", []byte(credsTOML), 0400); err != nil {
				return errMsg{err}
			}
			files = append(files, "credentials.toml")
		}

		return filesWrittenMsg{files}
	}
}

func (m Model) generateAgentTOML() string {
	var sb strings.Builder

	sb.WriteString("# Agent Configuration\n")
	sb.WriteString("# Generated by: agent setup\n\n")

	// Agent section
	sb.WriteString("[agent]\n")
	sb.WriteString(fmt.Sprintf("workspace = \"%s\"\n\n", m.config.Workspace))

	// Main LLM
	sb.WriteString("# Main LLM\n")
	sb.WriteString("[llm]\n")
	sb.WriteString(fmt.Sprintf("provider = \"%s\"\n", m.config.Provider))
	sb.WriteString(fmt.Sprintf("model = \"%s\"\n", m.config.Model))
	sb.WriteString("max_tokens = 4096\n")
	if m.config.BaseURL != "" {
		sb.WriteString(fmt.Sprintf("base_url = \"%s\"\n", m.config.BaseURL))
	}
	sb.WriteString(fmt.Sprintf("thinking = \"%s\"\n", m.config.Thinking))
	if m.config.CredentialMethod == "env" {
		sb.WriteString(fmt.Sprintf("api_key_env = \"%s\"\n", getDefaultEnvVar(m.config.Provider)))
	}
	sb.WriteString("\n")

	// Small LLM
	if m.config.SmallLLMEnabled {
		sb.WriteString("# Fast/cheap model for summarization, triage, memory extraction\n")
		sb.WriteString("[small_llm]\n")
		sb.WriteString(fmt.Sprintf("provider = \"%s\"\n", m.config.SmallLLMProvider))
		sb.WriteString(fmt.Sprintf("model = \"%s\"\n", m.config.SmallLLMModel))
		sb.WriteString("max_tokens = 1024\n")
		if m.config.SmallLLMBaseURL != "" {
			sb.WriteString(fmt.Sprintf("base_url = \"%s\"\n", m.config.SmallLLMBaseURL))
		}
		sb.WriteString("\n")
	}

	// Embedding
	if m.config.EmbeddingProvider != "none" {
		sb.WriteString("# Embedding model for semantic memory\n")
		sb.WriteString("[embedding]\n")
		sb.WriteString(fmt.Sprintf("provider = \"%s\"\n", m.config.EmbeddingProvider))
		sb.WriteString(fmt.Sprintf("model = \"%s\"\n", m.config.EmbeddingModel))
		if m.config.EmbeddingBaseURL != "" {
			sb.WriteString(fmt.Sprintf("base_url = \"%s\"\n", m.config.EmbeddingBaseURL))
		}
		sb.WriteString("\n")
	}

	// Profiles
	if m.config.UseProfiles && len(m.config.Profiles) > 0 {
		sb.WriteString("# Capability Profiles\n")
		for name, profile := range m.config.Profiles {
			sb.WriteString(fmt.Sprintf("[profiles.%s]\n", name))
			sb.WriteString(fmt.Sprintf("model = \"%s\"\n", profile.Model))
			if profile.Thinking != "" {
				sb.WriteString(fmt.Sprintf("thinking = \"%s\"\n", profile.Thinking))
			}
			sb.WriteString("\n")
		}
	}

	// Storage
	sb.WriteString("# Storage\n")
	sb.WriteString("[storage]\n")
	sb.WriteString(fmt.Sprintf("persist_memory = %t\n", m.config.PersistMemory))
	sb.WriteString("\n")

	// Security
	sb.WriteString("# Security Framework\n")
	sb.WriteString("[security]\n")
	sb.WriteString(fmt.Sprintf("mode = \"%s\"\n", m.config.SecurityMode))
	sb.WriteString("\n")

	// Telemetry
	if m.config.EnableTelemetry {
		sb.WriteString("# Telemetry\n")
		sb.WriteString("[telemetry]\n")
		sb.WriteString("enabled = true\n")
		sb.WriteString("protocol = \"otlp\"\n")
		sb.WriteString("# endpoint = \"http://localhost:4317\"\n")
		sb.WriteString("\n")
	}

	// MCP placeholder
	if m.config.EnableMCP {
		sb.WriteString("# MCP Tool Servers\n")
		sb.WriteString("# [mcp.servers.memory]\n")
		sb.WriteString("# command = \"npx\"\n")
		sb.WriteString("# args = [\"-y\", \"@modelcontextprotocol/server-memory\"]\n\n")
	}

	return sb.String()
}

func (m Model) generatePolicyTOML() string {
	var sb strings.Builder

	sb.WriteString("# Security Policy\n")
	sb.WriteString("# Generated by: agent setup\n\n")

	sb.WriteString(fmt.Sprintf("default_deny = %t\n", m.config.DefaultDeny))
	sb.WriteString(fmt.Sprintf("workspace = \"%s\"\n\n", m.config.Workspace))

	// File tools
	sb.WriteString("[tools.read]\n")
	sb.WriteString("enabled = true\n")
	sb.WriteString("allow = [\"$WORKSPACE/**\"]\n")
	sb.WriteString("deny = [\"**/.env\", \"**/*.key\", \"**/credentials.toml\"]\n\n")

	sb.WriteString("[tools.write]\n")
	sb.WriteString("enabled = true\n")
	sb.WriteString("allow = [\"$WORKSPACE/**\"]\n")
	sb.WriteString("deny = [\"agent.toml\", \"policy.toml\", \"credentials.toml\"]\n\n")

	// Bash
	sb.WriteString("[tools.bash]\n")
	sb.WriteString(fmt.Sprintf("enabled = %t\n", m.config.AllowBash))
	if m.config.AllowBash {
		sb.WriteString("# Commands run from workspace directory; absolute paths outside workspace are blocked\n")
		if m.config.DefaultDeny {
			sb.WriteString("allowlist = [\"ls *\", \"cat *\", \"grep *\", \"find *\", \"head *\", \"tail *\", \"wc *\", \"git *\", \"go *\", \"make *\"]\n")
			sb.WriteString("denylist = [\"rm -rf *\", \"sudo *\", \"curl * | bash\", \"chmod 777 *\", \"../*\"]\n")
		} else {
			sb.WriteString("# Recommended: add denylist for dangerous commands\n")
			sb.WriteString("# denylist = [\"rm -rf *\", \"sudo *\", \"curl * | bash\", \"../*\"]\n")
		}
	}
	sb.WriteString("\n")

	// Web tools
	sb.WriteString("[tools.web_search]\n")
	sb.WriteString(fmt.Sprintf("enabled = %t\n", m.config.AllowWeb))
	sb.WriteString("\n")

	sb.WriteString("[tools.web_fetch]\n")
	sb.WriteString(fmt.Sprintf("enabled = %t\n", m.config.AllowWeb))
	if m.config.DefaultDeny && m.config.AllowWeb {
		sb.WriteString("# allow_domains = [\"github.com\", \"*.github.io\", \"docs.python.org\"]\n")
	}
	sb.WriteString("\n")

	// MCP policy
	if m.config.EnableMCP && m.config.DefaultDeny {
		sb.WriteString("[mcp]\n")
		sb.WriteString("default_deny = true\n")
		sb.WriteString("# allowed_tools = [\"memory:*\", \"filesystem:read_file\"]\n\n")
	}

	return sb.String()
}

func (m Model) generateCredentialsTOML() string {
	var sb strings.Builder

	sb.WriteString("# API Credentials\n")
	sb.WriteString("# Generated by: agent setup\n")
	sb.WriteString("# Permissions: 0400 (owner read-only)\n\n")

	sb.WriteString("[llm]\n")
	sb.WriteString(fmt.Sprintf("api_key = \"%s\"\n", m.config.APIKey))

	return sb.String()
}

// Run starts the setup wizard
func Run() error {
	p := tea.NewProgram(New())
	_, err := p.Run()
	return err
}
