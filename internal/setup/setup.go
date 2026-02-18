// Package setup provides the interactive setup wizard for the agent.
package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinayprograms/agentkit/auth"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/mcp"
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

	// MCP Servers
	MCPServers map[string]MCPServerSetup

	// Credentials
	CredentialMethod string // "file", "env", "oauth"

	// OAuth (populated during OAuth flow)
	OAuthToken *credentials.OAuthToken
}

// MCPServerSetup holds MCP server configuration during setup
type MCPServerSetup struct {
	Command     string
	Args        []string
	Env         map[string]string
	DeniedTools []string
	// Discovered tools (not persisted, used during setup)
	DiscoveredTools []string
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
	StepCustomModel // Text input for model name (Ollama Cloud, LiteLLM, Custom)
	StepAPIKey
	StepBaseURL
	StepThinking
	StepSmallLLM
	StepSmallLLMProvider
	StepSmallLLMModel
	StepWorkspace
	StepSecurity
	StepSecurityMode
	StepProfiles
	StepProfilesConfig
	StepFeatures
	StepMCPAdd
	StepMCPName
	StepMCPCommand
	StepMCPArgs
	StepMCPProbe
	StepMCPDenySelect
	StepCredentialMethod
	StepOAuthFlow
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

	// MCP setup state
	currentMCPName    string   // Name of MCP server being configured
	currentMCPCommand string   // Command for current MCP
	currentMCPArgs    string   // Args as space-separated string
	probedTools       []string // Tools discovered from current MCP
	probeError        string   // Error from probing, if any

	// OAuth state
	oauthStatus    string // Status message during OAuth flow
	oauthUserCode  string // Code to show user
	oauthVerifyURI string // URL for user to visit
	oauthError     string // Error during OAuth, if any
	oauthComplete  bool   // True when OAuth flow completed

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
			MCPServers:        make(map[string]MCPServerSetup),
			AllowBash:         true,
			AllowWeb:          true,
			EnableMemory:      true,
			PersistMemory:     true,
			SecurityMode:      "default",
			Thinking:          "auto",
			CredentialMethod:  "file",
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
		Servers map[string]struct {
			Command     string   `toml:"command"`
			Args        []string `toml:"args"`
			DeniedTools []string `toml:"denied_tools"`
		} `toml:"servers"`
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

	// MCP - load existing servers
	m.config.EnableMCP = len(cfg.MCP.Servers) > 0
	for name, srv := range cfg.MCP.Servers {
		m.config.MCPServers[name] = MCPServerSetup{
			Command:     srv.Command,
			Args:        srv.Args,
			DeniedTools: srv.DeniedTools,
			// DiscoveredTools will be populated when user edits
		}
	}

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

// mcpProbeResult is the message sent after probing an MCP server
type mcpProbeResult struct {
	tools []string
	err   error
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case oauthResultMsg:
		if msg.err != nil {
			m.oauthError = msg.err.Error()
			m.oauthComplete = false
		} else {
			m.config.OAuthToken = msg.token
			m.oauthComplete = true
			m.oauthError = ""
		}
		m.step = StepConfirm
		m.cursor = 0
		return m, nil

	case mcpProbeResult:
		if msg.err != nil {
			m.probeError = msg.err.Error()
			m.probedTools = nil
		} else {
			m.probedTools = msg.tools
			m.probeError = ""
		}
		m.step = StepMCPDenySelect
		m.cursor = 0
		
		// Pre-select previously denied tools (for edit mode)
		m.selected = make(map[int]bool)
		if existingSrv, exists := m.config.MCPServers[m.currentMCPName]; exists {
			deniedSet := make(map[string]bool)
			for _, t := range existingSrv.DeniedTools {
				deniedSet[t] = true
			}
			for i, tool := range m.probedTools {
				if deniedSet[tool] {
					m.selected[i] = true
				}
			}
		}
		return m, nil

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
		// Handle text input steps first - let them capture all keys except ctrl+c and enter
		if m.isTextInputStep() {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				return m.handleEnter()
			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		}

		// Non-text-input steps - navigation keys work
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
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
			max := m.maxCursorForStep()
			if m.cursor < max {
				m.cursor++
			}
			return m, nil

		case " ":
			// Toggle selection for multi-select steps
			if m.step == StepFeatures || m.step == StepMCPDenySelect {
				m.selected[m.cursor] = !m.selected[m.cursor]
			}
			return m, nil

		case "tab":
			return m, nil
		}
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

func (m Model) maxCursorForStep() int {
	switch m.step {
	case StepScenario:
		return len(m.getScenarios()) - 1
	case StepProvider:
		return len(m.getProviders()) - 1
	case StepModel:
		return len(m.getModels()) - 1
	case StepThinking:
		return 4 // auto, off, low, medium, high
	case StepSmallLLM:
		return 1 // yes, no
	case StepSmallLLMProvider:
		return len(m.getProviders()) - 1 // reuses main provider list
	case StepSecurity:
		return 1 // default, strict
	case StepSecurityMode:
		return 1 // default, paranoid
	case StepProfiles:
		return 1 // yes, no
	case StepFeatures:
		return 3 // 4 features (0-3)
	case StepMCPAdd:
		return len(m.config.MCPServers) + 1 // edit options + add + done
	case StepMCPDenySelect:
		if len(m.probedTools) == 0 {
			return 0
		}
		return len(m.probedTools) - 1
	case StepCredentialMethod:
		return 1 // file, env
	case StepConfirm:
		return 1 // confirm, cancel
	default:
		return 100 // fallback high number
	}
}

func (m Model) isTextInputStep() bool {
	switch m.step {
	case StepAPIKey, StepBaseURL, StepWorkspace, StepSmallLLMModel,
		StepMCPName, StepMCPCommand, StepMCPArgs, StepCustomModel:
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
		if m.needsCustomModelInput() {
			m.step = StepCustomModel
			m.textInput.SetValue(m.config.Model)
			m.textInput.Placeholder = "e.g., llama3.2, claude-sonnet-4"
			m.textInput.Focus()
		} else {
			m.step = StepModel
			m.cursor = m.findModelIndex()
		}

	case StepCustomModel:
		model := strings.TrimSpace(m.textInput.Value())
		if model == "" {
			m.err = fmt.Errorf("model name is required")
		} else {
			m.config.Model = model
			m.step = StepAPIKey
			m.textInput.SetValue("")
			m.textInput.Placeholder = "sk-... (leave empty to keep existing)"
			m.textInput.EchoMode = textinput.EchoPassword
		}

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
			m.step = StepWorkspace
			m.textInput.SetValue(m.config.Workspace)
			m.textInput.Placeholder = "/path/to/workspace"
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
		if m.config.EnableMCP {
			m.step = StepMCPAdd
			m.cursor = 0
		} else {
			m.step = StepCredentialMethod
			m.cursor = 0
		}

	case StepMCPAdd:
		serverNames := m.getSortedMCPServerNames()
		numServers := len(serverNames)
		
		if m.cursor < numServers {
			// Edit existing server - re-probe and allow deny selection
			m.currentMCPName = serverNames[m.cursor]
			srv := m.config.MCPServers[m.currentMCPName]
			m.currentMCPCommand = srv.Command
			m.currentMCPArgs = strings.Join(srv.Args, " ")
			m.step = StepMCPProbe
			m.probeError = ""
			m.probedTools = nil
			return m, m.probeMCPServer()
		} else if m.cursor == numServers {
			// Add new server
			m.step = StepMCPName
			m.textInput.SetValue("")
			m.textInput.Focus()
		} else {
			// Done
			m.step = StepCredentialMethod
			m.cursor = 0
		}

	case StepMCPName:
		m.currentMCPName = strings.TrimSpace(m.textInput.Value())
		if m.currentMCPName == "" {
			m.err = fmt.Errorf("server name is required")
		} else {
			m.step = StepMCPCommand
			m.textInput.SetValue("")
			m.textInput.Focus()
		}

	case StepMCPCommand:
		m.currentMCPCommand = strings.TrimSpace(m.textInput.Value())
		if m.currentMCPCommand == "" {
			m.err = fmt.Errorf("command is required")
		} else {
			m.step = StepMCPArgs
			m.textInput.SetValue("")
			m.textInput.Focus()
		}

	case StepMCPArgs:
		m.currentMCPArgs = m.textInput.Value()
		m.step = StepMCPProbe
		m.probeError = ""
		m.probedTools = nil
		return m, m.probeMCPServer()

	case StepMCPProbe:
		// Handled by tea.Cmd from probeMCPServer
		// Just wait for result

	case StepMCPDenySelect:
		// Apply selected denied tools
		var deniedTools []string
		for i, tool := range m.probedTools {
			if m.selected[i] {
				deniedTools = append(deniedTools, tool)
			}
		}
		
		// Parse args
		var args []string
		if m.currentMCPArgs != "" {
			args = strings.Fields(m.currentMCPArgs)
		}
		
		// Save server config
		m.config.MCPServers[m.currentMCPName] = MCPServerSetup{
			Command:         m.currentMCPCommand,
			Args:            args,
			DeniedTools:     deniedTools,
			DiscoveredTools: m.probedTools,
		}
		
		// Reset state and go back to add more
		m.currentMCPName = ""
		m.currentMCPCommand = ""
		m.currentMCPArgs = ""
		m.probedTools = nil
		m.selected = make(map[int]bool)
		m.step = StepMCPAdd
		m.cursor = 0

	case StepCredentialMethod:
		methods := m.getCredentialMethods()
		if m.cursor >= 0 && m.cursor < len(methods) {
			m.config.CredentialMethod = methods[m.cursor].name
		}

		if m.config.CredentialMethod == "oauth" {
			// Start OAuth flow
			m.step = StepOAuthFlow
			return m, m.startOAuthFlow()
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

// oauthResultMsg is sent when OAuth completes
type oauthResultMsg struct {
	token *credentials.OAuthToken
	err   error
}

// startOAuthFlow initiates the OAuth device flow
func (m *Model) startOAuthFlow() tea.Cmd {
	return func() tea.Msg {
		cfg := auth.GetProviderConfig(m.config.Provider, "")
		if cfg == nil {
			return oauthResultMsg{err: fmt.Errorf("OAuth not supported for provider: %s", m.config.Provider)}
		}

		// Use default callbacks which print to stdout
		callbacks := auth.DefaultCallbacks()

		// Run OAuth flow (blocking - will print prompts to stdout)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		token, err := auth.DeviceAuth(ctx, *cfg, callbacks)
		if err != nil {
			return oauthResultMsg{err: err}
		}

		return oauthResultMsg{token: token}
	}
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

func (m *Model) applyScenarioDefaults() {
	switch m.config.Scenario {
	case ScenarioLocal:
		m.config.Provider = ProviderOllamaLocal
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.SecurityMode = "default"
		m.config.SmallLLMEnabled = false
		m.config.PersistMemory = false

	case ScenarioDev:
		m.config.Provider = ProviderAnthropic
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.SecurityMode = "default"
		m.config.SmallLLMEnabled = true

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
	case ProviderXAI:
		m.config.Model = "grok-2"
	case ProviderOpenRouter:
		m.config.Model = "anthropic/claude-sonnet-4"
	case ProviderOllamaCloud:
		m.config.Model = "llama3.2"
	case ProviderOllamaLocal:
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
	case ProviderXAI:
		m.config.SmallLLMModel = "grok-2" // xAI doesn't have a small model yet
	case ProviderOllamaCloud, ProviderOllamaLocal:
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
	case StepCustomModel:
		s.WriteString(m.viewCustomModel())
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
	case StepMCPAdd:
		s.WriteString(m.viewMCPAdd())
	case StepMCPName:
		s.WriteString(m.viewMCPName())
	case StepMCPCommand:
		s.WriteString(m.viewMCPCommand())
	case StepMCPArgs:
		s.WriteString(m.viewMCPArgs())
	case StepMCPProbe:
		s.WriteString(m.viewMCPProbe())
	case StepMCPDenySelect:
		s.WriteString(m.viewMCPDenySelect())
	case StepCredentialMethod:
		s.WriteString(m.viewCredentialMethod())
	case StepOAuthFlow:
		s.WriteString(m.viewOAuthFlow())
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
		// Compact: name and desc on same line
		s.WriteString(cursor + style.Render(sc.name) + " - " + dimStyle.Render(sc.desc) + "\n")
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

func (m Model) viewCustomModel() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Model Name") + "\n")
	
	switch m.config.Provider {
	case ProviderOllamaCloud, ProviderOllamaLocal:
		s.WriteString(subtitleStyle.Render("Enter the Ollama model to use") + "\n\n")
		s.WriteString(dimStyle.Render("Examples: llama3.2, codellama, mistral, phi3, qwen2.5") + "\n")
		s.WriteString(dimStyle.Render("Run 'ollama list' to see your downloaded models") + "\n\n")
	case ProviderLMStudio:
		s.WriteString(subtitleStyle.Render("Enter the model name from LM Studio") + "\n\n")
		s.WriteString(dimStyle.Render("Check LM Studio UI for available model names") + "\n\n")
	case ProviderLiteLLM:
		s.WriteString(subtitleStyle.Render("Enter the model name (as configured in LiteLLM)") + "\n\n")
		s.WriteString(dimStyle.Render("Examples: claude-sonnet-4, gpt-4o, gemini-2.0-flash") + "\n\n")
	default:
		s.WriteString(subtitleStyle.Render("Enter the model name") + "\n\n")
	}

	s.WriteString(m.textInput.View() + "\n\n")
	s.WriteString(dimStyle.Render("Enter to continue"))
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
	s.WriteString(subtitleStyle.Render("Toggle features with Space, then Enter to continue") + "\n\n")

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

	// Show selected count
	count := 0
	for _, v := range m.selected {
		if v {
			count++
		}
	}
	s.WriteString("\n" + infoStyle.Render(fmt.Sprintf("%d selected", count)))
	s.WriteString("\n" + dimStyle.Render("Space = toggle, Enter = continue"))
	return s.String()
}

func (m Model) viewMCPAdd() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("MCP Tool Servers") + "\n")
	s.WriteString(subtitleStyle.Render("Manage external tool servers") + "\n\n")

	// Build menu options
	var options []string
	
	// Add existing servers as editable options
	serverNames := m.getSortedMCPServerNames()
	for _, name := range serverNames {
		srv := m.config.MCPServers[name]
		deniedCount := len(srv.DeniedTools)
		if deniedCount > 0 {
			options = append(options, fmt.Sprintf("Edit %s (%d tools denied)", name, deniedCount))
		} else {
			options = append(options, fmt.Sprintf("Edit %s (all tools allowed)", name))
		}
	}
	
	options = append(options, "Add new MCP server")
	options = append(options, "Done - continue to next step")

	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		s.WriteString(cursor + style.Render(opt) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("↑/↓ to move, Enter to select"))
	return s.String()
}

func (m Model) getSortedMCPServerNames() []string {
	names := make([]string, 0, len(m.config.MCPServers))
	for name := range m.config.MCPServers {
		names = append(names, name)
	}
	// Sort for consistent ordering
	for i := 0; i < len(names)-1; i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

func (m Model) viewMCPName() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("MCP Server Name") + "\n")
	s.WriteString(subtitleStyle.Render("Enter a short name for this server (e.g., 'memory', 'filesystem')") + "\n\n")
	s.WriteString(m.textInput.View() + "\n")
	if m.err != nil {
		s.WriteString("\n" + errorStyle.Render(m.err.Error()) + "\n")
	}
	s.WriteString("\n" + dimStyle.Render("Enter to continue, Esc to go back"))
	return s.String()
}

func (m Model) viewMCPCommand() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("MCP Server Command") + "\n")
	s.WriteString(subtitleStyle.Render("Enter the command to start the server") + "\n\n")
	s.WriteString(infoStyle.Render("Server: "+m.currentMCPName) + "\n\n")
	s.WriteString("Examples:\n")
	s.WriteString(dimStyle.Render("  npx, uvx, node, python") + "\n\n")
	s.WriteString(m.textInput.View() + "\n")
	if m.err != nil {
		s.WriteString("\n" + errorStyle.Render(m.err.Error()) + "\n")
	}
	s.WriteString("\n" + dimStyle.Render("Enter to continue"))
	return s.String()
}

func (m Model) viewMCPArgs() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("MCP Server Arguments") + "\n")
	s.WriteString(subtitleStyle.Render("Enter command arguments (space-separated)") + "\n\n")
	s.WriteString(infoStyle.Render(fmt.Sprintf("Server: %s | Command: %s", m.currentMCPName, m.currentMCPCommand)) + "\n\n")
	s.WriteString("Examples:\n")
	s.WriteString(dimStyle.Render("  -y @modelcontextprotocol/server-memory") + "\n")
	s.WriteString(dimStyle.Render("  /path/to/server.js") + "\n\n")
	s.WriteString(m.textInput.View() + "\n")
	s.WriteString("\n" + dimStyle.Render("Enter to probe server (leave empty if no args)"))
	return s.String()
}

func (m Model) viewMCPProbe() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Probing MCP Server...") + "\n\n")
	s.WriteString(normalStyle.Render(fmt.Sprintf("Connecting to %s (%s)...", m.currentMCPName, m.currentMCPCommand)) + "\n")
	s.WriteString(dimStyle.Render("This may take a few seconds."))
	return s.String()
}

func (m Model) viewMCPDenySelect() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Select Tools to Deny") + "\n")
	s.WriteString(subtitleStyle.Render(fmt.Sprintf("Server: %s", m.currentMCPName)) + "\n\n")

	if m.probeError != "" {
		s.WriteString(errorStyle.Render("Error probing server: "+m.probeError) + "\n\n")
		s.WriteString(normalStyle.Render("Server will be added without tool filtering.") + "\n")
		s.WriteString(normalStyle.Render("You can manually edit denied_tools in agent.toml later.") + "\n\n")
		s.WriteString(dimStyle.Render("Press Enter to continue"))
		return s.String()
	}

	if len(m.probedTools) == 0 {
		s.WriteString(normalStyle.Render("No tools discovered from this server.") + "\n\n")
		s.WriteString(dimStyle.Render("Press Enter to continue"))
		return s.String()
	}

	s.WriteString(fmt.Sprintf("Found %d tools. Select tools to DENY (blocked from LLM):\n\n", len(m.probedTools)))

	for i, tool := range m.probedTools {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		check := "[ ]"
		if m.selected[i] {
			check = "[✗]" // X to indicate deny
		}
		s.WriteString(cursor + check + " " + normalStyle.Render(tool) + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("Space to toggle deny, Enter to continue (unselected = allowed)"))
	return s.String()
}

func (m Model) viewCredentialMethod() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Credential Storage") + "\n")
	s.WriteString(subtitleStyle.Render("How should credentials be stored?") + "\n\n")

	options := m.getCredentialMethods()

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

// getCredentialMethods returns available credential methods for the current provider.
func (m Model) getCredentialMethods() []struct{ name, desc string } {
	methods := []struct{ name, desc string }{
		{"file", "API key in credentials.toml (mode 0600)"},
		{"env", "Environment variables only"},
	}

	// Add OAuth option for supported providers
	if auth.SupportsOAuth(m.config.Provider) {
		methods = append([]struct{ name, desc string }{
			{"oauth", "OAuth2 login (browser-based, no API key needed)"},
		}, methods...)
	}

	return methods
}

func (m Model) viewOAuthFlow() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("OAuth Authentication") + "\n\n")

	if m.oauthError != "" {
		s.WriteString(errorStyle.Render("✗ Error: "+m.oauthError) + "\n\n")
		s.WriteString(dimStyle.Render("Press Enter to continue with API key instead"))
		return s.String()
	}

	if m.oauthComplete {
		s.WriteString(successStyle.Render("✓ Authentication successful!") + "\n\n")
		s.WriteString(dimStyle.Render("Token will be saved to ~/.config/grid/credentials.toml"))
		return s.String()
	}

	// Show waiting state
	s.WriteString("🔐 " + selectedStyle.Render("Authenticating with "+m.config.Provider) + "\n\n")
	s.WriteString(dimStyle.Render("Please complete authentication in your browser...") + "\n\n")
	s.WriteString(dimStyle.Render("This may take a moment."))

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

// probeMCPServer connects to an MCP server and discovers its tools
func (m Model) probeMCPServer() tea.Cmd {
	return func() tea.Msg {
		// Parse args
		var args []string
		if m.currentMCPArgs != "" {
			args = strings.Fields(m.currentMCPArgs)
		}

		// Create MCP manager and connect
		manager := mcp.NewManager()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := manager.Connect(ctx, m.currentMCPName, mcp.ServerConfig{
			Command: m.currentMCPCommand,
			Args:    args,
		})
		if err != nil {
			return mcpProbeResult{err: fmt.Errorf("failed to connect: %w", err)}
		}
		defer manager.Disconnect(m.currentMCPName)

		// Get all tools from this server
		allTools := manager.AllTools()
		var toolNames []string
		for _, t := range allTools {
			if t.Server == m.currentMCPName {
				toolNames = append(toolNames, t.Tool.Name)
			}
		}

		return mcpProbeResult{tools: toolNames}
	}
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

		// Write credentials to ~/.config/grid/credentials.toml
		if m.config.CredentialMethod == "file" && m.config.APIKey != "" {
			if err := m.writeCredentials(); err != nil {
				return errMsg{err}
			}
			files = append(files, credentials.DefaultPath())
		}

		// Save OAuth token to ~/.config/grid/credentials.toml
		if m.config.CredentialMethod == "oauth" && m.config.OAuthToken != nil {
			if err := m.writeOAuthCredentials(); err != nil {
				return errMsg{err}
			}
			files = append(files, credentials.DefaultPath())
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

	// MCP servers
	if m.config.EnableMCP && len(m.config.MCPServers) > 0 {
		sb.WriteString("# MCP Tool Servers\n")
		for name, srv := range m.config.MCPServers {
			sb.WriteString(fmt.Sprintf("[mcp.servers.%s]\n", name))
			sb.WriteString(fmt.Sprintf("command = \"%s\"\n", srv.Command))
			if len(srv.Args) > 0 {
				// Format args as TOML array
				quotedArgs := make([]string, len(srv.Args))
				for i, arg := range srv.Args {
					quotedArgs[i] = fmt.Sprintf("\"%s\"", arg)
				}
				sb.WriteString(fmt.Sprintf("args = [%s]\n", strings.Join(quotedArgs, ", ")))
			}
			if len(srv.DeniedTools) > 0 {
				quotedTools := make([]string, len(srv.DeniedTools))
				for i, tool := range srv.DeniedTools {
					quotedTools[i] = fmt.Sprintf("\"%s\"", tool)
				}
				sb.WriteString(fmt.Sprintf("denied_tools = [%s]\n", strings.Join(quotedTools, ", ")))
			}
			sb.WriteString("\n")
		}
	} else if m.config.EnableMCP {
		// Placeholder if MCP enabled but no servers configured
		sb.WriteString("# MCP Tool Servers\n")
		sb.WriteString("# [mcp.servers.memory]\n")
		sb.WriteString("# command = \"npx\"\n")
		sb.WriteString("# args = [\"-y\", \"@modelcontextprotocol/server-memory\"]\n")
		sb.WriteString("# denied_tools = []  # Tools to block from LLM\n\n")
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
		// Always include allowed_dirs (defaults to workspace + /tmp)
		sb.WriteString("allowed_dirs = [\"$WORKSPACE\", \"/tmp\"]\n")
		
		// Recommend sandbox for production/team scenarios
		if m.config.Scenario == ScenarioProduction || m.config.Scenario == ScenarioTeam {
			sb.WriteString("# Sandbox: restrict bash to workspace only (requires bwrap or docker)\n")
			sb.WriteString("# sandbox = \"bwrap\"  # bubblewrap - lightweight, no root needed\n")
			sb.WriteString("# sandbox = \"docker\" # run in container - more isolation\n")
		}
		if m.config.DefaultDeny {
			sb.WriteString("allowlist = [\"ls *\", \"cat *\", \"grep *\", \"find . *\", \"head *\", \"tail *\", \"wc *\", \"git *\", \"go *\", \"make *\"]\n")
			sb.WriteString("denylist = [\"rm -rf *\", \"sudo *\", \"curl * | bash\", \"chmod 777 *\", \"../*\", \"/*\"]\n")
		} else {
			sb.WriteString("# Recommended for security:\n")
			sb.WriteString("# sandbox = \"bwrap\"  # Restricts filesystem access to workspace only\n")
			sb.WriteString("# denylist = [\"docker\", \"kubectl\"]  # Add commands to block\n")
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

	// MCP policy - auto-generate allowed_tools from configured servers
	if m.config.EnableMCP && len(m.config.MCPServers) > 0 {
		sb.WriteString("[mcp]\n")
		if m.config.DefaultDeny {
			sb.WriteString("default_deny = true\n")
			// Auto-allow all tools from configured servers (denied_tools in agent.toml handles filtering)
			sb.WriteString("allowed_tools = [")
			serverNames := m.getSortedMCPServerNames()
			for i, name := range serverNames {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("\"%s:*\"", name))
			}
			sb.WriteString("]\n")
		} else {
			sb.WriteString("default_deny = false\n")
		}
		sb.WriteString("# Note: per-server denied_tools in agent.toml filter tools from LLM\n")
		sb.WriteString("# This policy.toml [mcp] section is runtime defense-in-depth\n\n")
	} else if m.config.EnableMCP && m.config.DefaultDeny {
		sb.WriteString("[mcp]\n")
		sb.WriteString("default_deny = true\n")
		sb.WriteString("# allowed_tools = [\"memory:*\", \"filesystem:read_file\"]\n\n")
	}

	return sb.String()
}

// writeCredentials saves API key to ~/.config/grid/credentials.toml
func (m Model) writeCredentials() error {
	// Load existing credentials or create new
	creds, _, _ := credentials.Load()
	if creds == nil {
		creds = &credentials.Credentials{}
	}

	// Set the API key for the provider
	creds.SetAPIKey(m.config.Provider, m.config.APIKey)

	return creds.Save()
}

// writeOAuthCredentials saves OAuth token to ~/.config/grid/credentials.toml
func (m Model) writeOAuthCredentials() error {
	// Load existing credentials or create new
	creds, _, _ := credentials.Load()
	if creds == nil {
		creds = &credentials.Credentials{}
	}

	// Set the OAuth token for the provider
	creds.SetOAuthToken(m.config.Provider, m.config.OAuthToken)

	return creds.Save()
}

// Run starts the setup wizard
func Run() error {
	p := tea.NewProgram(New())
	_, err := p.Run()
	return err
}
