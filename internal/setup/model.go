// Package setup provides the interactive setup wizard for the agent.
package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/policy"
)

// Deployment scenarios
const (
	ScenarioLocal      = "local"      // Personal machine, experimenting
	ScenarioDev        = "dev"        // Development/testing with cloud LLMs
	ScenarioTeam       = "team"       // Small team, shared proxy (LiteLLM)
	ScenarioProduction = "production" // Production with full features
	ScenarioDocker     = "docker"     // Container deployment
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

// Model is the bubbletea model for the setup wizard
type Model struct {
	step      Screen
	config    Config
	cursor    int
	textInput textinput.Model
	err       error
	width     int
	height    int

	// dir is the directory agent.toml/policy.toml are read from and written
	// to. Defaults to "." (the process cwd); inject with WithDir for tests
	// or callers that don't want to rely on the process's working directory.
	dir string

	// ctx bounds operations that need one (currently the MCP probe). Defaults
	// to context.Background(); inject with WithContext to bound wizard
	// lifetime to a caller's context.
	ctx context.Context
	// probe is the seam used to discover an MCP server's tools. Defaults to
	// dialMCP (spawns a real process); tests substitute a fake.
	probe probeFunc

	// For multi-select
	selected map[int]bool

	// Edit mode - true if loading from existing config
	editMode     bool
	existingFile string
	// policyWarning is shown on the welcome screen when an existing policy.toml
	// carries keys the current schema does not understand.
	policyWarning string

	// MCP setup state
	currentMCPName    string   // Name of MCP server being configured
	currentMCPCommand string   // Command for current MCP
	currentMCPArgs    string   // Args as space-separated string
	probedTools       []string // Tools discovered from current MCP
	probeError        string   // Error from probing, if any

	// Results
	filesWritten []string
}

// Option configures a Model at construction. See WithDir and WithContext.
type Option func(*Model)

// WithDir sets the directory agent.toml/policy.toml are read from and
// written to. Default is ".".
func WithDir(dir string) Option {
	return func(m *Model) { m.dir = dir }
}

// WithContext bounds the wizard's operations (currently the MCP probe) to
// ctx. Default is context.Background().
func WithContext(ctx context.Context) Option {
	return func(m *Model) { m.ctx = ctx }
}

// New creates a new setup model
func New(opts ...Option) Model {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	m := Model{
		step:      ScreenWelcome,
		textInput: ti,
		dir:       ".",
		ctx:       context.Background(),
		probe:     dialMCP,
		config: Config{
			Workspace:        ".",
			ConfigDir:        getDefaultConfigDir(),
			Profiles:         make(map[string]ProfileConfig),
			MCPServers:       make(map[string]MCPServerSetup),
			AllowBash:        true,
			AllowWeb:         true,
			EnableMemory:     true,
			SecurityMode:     "default",
			Thinking:         "auto",
			CredentialMethod: "file",
		},
		selected: make(map[int]bool),
	}
	for _, opt := range opts {
		opt(&m)
	}

	// Try to load existing configuration
	if err := m.loadExistingConfig(); err == nil {
		m.editMode = true
	} else {
		// No agent.toml (fresh mode): still warn about a legacy policy.toml
		// before the wizard silently overwrites it.
		m.checkPolicyWarning()
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
		Path string `toml:"path"`
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

func (m *Model) loadExistingConfig() error {
	agentPath := filepath.Join(m.dir, "agent.toml")

	// Check for agent.toml
	if _, err := os.Stat(agentPath); os.IsNotExist(err) {
		return err
	}

	m.existingFile = "agent.toml"

	var cfg existingConfig
	if _, err := toml.DecodeFile(agentPath, &cfg); err != nil {
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

	// Try to load policy.toml too. A tool is enabled when listed under
	// [tools] or when default_deny is false; pattern expansion is irrelevant here.
	if raw, err := os.ReadFile(filepath.Join(m.dir, "policy.toml")); err == nil {
		pol, unknown, err := policy.FromTOMLWithUnknownKeys(string(raw), m.config.Workspace, "")
		if err == nil {
			m.config.DefaultDeny = pol.DefaultDeny
			m.config.AllowBash = pol.IsToolEnabled("bash")
			m.config.AllowWeb = pol.IsToolEnabled("web_search")
		}
		m.setPolicyWarning(unknown)
	}

	return nil
}

// checkPolicyWarning surfaces a legacy policy.toml warning even when there
// is no agent.toml (fresh mode) — without this, a stray legacy policy.toml
// would be silently overwritten by writeFiles with no warning shown.
func (m *Model) checkPolicyWarning() {
	raw, err := os.ReadFile(filepath.Join(m.dir, "policy.toml"))
	if err != nil {
		return
	}
	_, unknown, err := policy.FromTOMLWithUnknownKeys(string(raw), m.config.Workspace, "")
	if err != nil {
		return
	}
	m.setPolicyWarning(unknown)
}

func (m *Model) setPolicyWarning(unknown []string) {
	if len(unknown) > 0 {
		m.policyWarning = fmt.Sprintf("policy.toml has unrecognised keys (legacy schema?): %s — the wizard will rewrite it in the current schema",
			strings.Join(unknown, ", "))
	}
}

func getDefaultConfigDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return config.DefaultConfigDir(home)
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
	case mcpProbeResult:
		if msg.err != nil {
			m.probeError = msg.err.Error()
			m.probedTools = nil
		} else {
			m.probedTools = msg.tools
			m.probeError = ""
		}
		m.step = ScreenMCPDenySelect
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
		m.step = ScreenComplete
		return m, nil
	case errMsg:
		m.err = msg.error
		m.step = ScreenComplete
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
			if m.step == ScreenComplete {
				return m, tea.Quit
			}
			if m.step == ScreenWelcome {
				return m, tea.Quit
			}
			// Go back
			if m.step > ScreenWelcome {
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
			if m.step == ScreenFeatures || m.step == ScreenMCPDenySelect {
				m.selected[m.cursor] = !m.selected[m.cursor]
			}
			return m, nil

		case "tab":
			return m, nil
		}
	}

	return m, nil
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.step {
	case ScreenWelcome:
		m.step = ScreenScenario
		m.cursor = m.findScenarioIndex()

	case ScreenScenario:
		scenarios := m.getScenarios()
		if m.cursor >= 0 && m.cursor < len(scenarios) {
			m.config.Scenario = scenarios[m.cursor].id
			// Only apply defaults if not in edit mode
			if !m.editMode {
				m.applyScenarioDefaults()
			}
		}
		m.step = ScreenProvider
		m.cursor = m.findProviderIndex(m.config.Provider)

	case ScreenProvider:
		providers := m.getProviders()
		if m.cursor >= 0 && m.cursor < len(providers) {
			m.config.Provider = providers[m.cursor].id
			if !m.editMode {
				m.setDefaultModel()
			}
		}
		if m.needsCustomModelInput() {
			m.step = ScreenCustomModel
			m.textInput.SetValue(m.config.Model)
			m.textInput.Placeholder = "e.g., llama3.2, claude-sonnet-4"
			m.textInput.Focus()
		} else {
			m.step = ScreenModel
			m.cursor = m.findModelIndex()
		}

	case ScreenCustomModel:
		model := strings.TrimSpace(m.textInput.Value())
		if model == "" {
			m.err = fmt.Errorf("model name is required")
		} else {
			m.err = nil
			m.config.Model = model
			m.step = ScreenAPIKey
			m.textInput.SetValue("")
			m.textInput.Placeholder = "sk-... (leave empty to keep existing)"
			m.textInput.EchoMode = textinput.EchoPassword
		}

	case ScreenModel:
		models := m.getModels()
		if m.cursor >= 0 && m.cursor < len(models) {
			m.config.Model = models[m.cursor].id
		}
		m.step = ScreenAPIKey
		m.textInput.SetValue("")
		m.textInput.Placeholder = "sk-... (leave empty to keep existing)"
		m.textInput.EchoMode = textinput.EchoPassword

	case ScreenAPIKey:
		if m.textInput.Value() != "" {
			m.config.APIKey = m.textInput.Value()
		}
		m.textInput.EchoMode = textinput.EchoNormal
		if m.needsBaseURL() {
			m.step = ScreenBaseURL
			if m.editMode && m.config.BaseURL != "" {
				m.textInput.SetValue(m.config.BaseURL)
			} else {
				m.textInput.SetValue(m.getDefaultBaseURL())
			}
			m.textInput.Placeholder = "https://..."
		} else {
			m.step = ScreenThinking
			m.cursor = m.findThinkingIndex()
		}

	case ScreenBaseURL:
		m.config.BaseURL = m.textInput.Value()
		m.step = ScreenThinking
		m.cursor = m.findThinkingIndex()

	case ScreenThinking:
		thinkingOptions := []string{"auto", "off", "low", "medium", "high"}
		if m.cursor >= 0 && m.cursor < len(thinkingOptions) {
			m.config.Thinking = thinkingOptions[m.cursor]
		}
		m.step = ScreenSmallLLM
		if m.config.SmallLLMEnabled {
			m.cursor = 0 // Yes
		} else {
			m.cursor = 1 // No
		}

	case ScreenSmallLLM:
		m.config.SmallLLMEnabled = m.cursor == 0 // Yes
		if m.config.SmallLLMEnabled {
			m.step = ScreenSmallLLMProvider
			m.cursor = m.findProviderIndex(m.config.SmallLLMProvider)
		} else {
			m.step = ScreenWorkspace
			m.textInput.SetValue(m.config.Workspace)
			m.textInput.Placeholder = "/path/to/workspace"
		}

	case ScreenSmallLLMProvider:
		providers := m.getProviders()
		if m.cursor >= 0 && m.cursor < len(providers) {
			m.config.SmallLLMProvider = providers[m.cursor].id
			if !m.editMode || m.config.SmallLLMModel == "" {
				m.setDefaultSmallModel()
			}
		}
		m.step = ScreenSmallLLMModel
		m.textInput.SetValue(m.config.SmallLLMModel)
		m.textInput.Placeholder = "model name"

	case ScreenSmallLLMModel:
		m.config.SmallLLMModel = m.textInput.Value()
		m.step = ScreenWorkspace
		m.textInput.SetValue(m.config.Workspace)
		m.textInput.Placeholder = "/path/to/workspace"

	case ScreenWorkspace:
		m.config.Workspace = m.textInput.Value()
		if m.config.Workspace == "" {
			m.config.Workspace = "."
		}
		m.step = ScreenSecurity
		if m.config.DefaultDeny {
			m.cursor = 1 // Restrictive
		} else {
			m.cursor = 0 // Permissive
		}

	case ScreenSecurity:
		// Security stance: permissive (0) or restrictive (1)
		m.config.DefaultDeny = m.cursor == 1
		if m.cursor == 1 && !m.editMode {
			m.config.AllowBash = false
			m.config.AllowWeb = false
		}
		m.step = ScreenSecurityMode
		if m.config.SecurityMode == "paranoid" {
			m.cursor = 1
		} else {
			m.cursor = 0
		}

	case ScreenSecurityMode:
		modes := []string{"default", "paranoid"}
		if m.cursor >= 0 && m.cursor < len(modes) {
			m.config.SecurityMode = modes[m.cursor]
		}
		m.step = ScreenProfiles
		if m.config.UseProfiles {
			m.cursor = 0 // Yes
		} else {
			m.cursor = 1 // No
		}

	case ScreenProfiles:
		m.config.UseProfiles = m.cursor == 0 // Yes
		if m.config.UseProfiles {
			m.step = ScreenProfilesConfig
			m.cursor = 0
		} else {
			m.step = ScreenFeatures
			m.cursor = 0
			m.initFeatureSelection()
		}

	case ScreenProfilesConfig:
		// Auto-configure profiles based on provider
		m.configureDefaultProfiles()
		m.step = ScreenFeatures
		m.cursor = 0
		m.initFeatureSelection()

	case ScreenFeatures:
		m.applyFeatureSelection()
		if m.config.EnableMCP {
			m.step = ScreenMCPAdd
			m.cursor = 0
		} else {
			m.step = ScreenCredentialMethod
			m.cursor = 0
		}

	case ScreenMCPAdd:
		serverNames := m.getSortedMCPServerNames()
		numServers := len(serverNames)

		if m.cursor < numServers {
			// Edit existing server - re-probe and allow deny selection
			m.currentMCPName = serverNames[m.cursor]
			srv := m.config.MCPServers[m.currentMCPName]
			m.currentMCPCommand = srv.Command
			m.currentMCPArgs = strings.Join(srv.Args, " ")
			m.step = ScreenMCPProbe
			m.probeError = ""
			m.probedTools = nil
			return m, m.probeMCPServer()
		} else if m.cursor == numServers {
			// Add new server
			m.step = ScreenMCPName
			m.textInput.SetValue("")
			m.textInput.Focus()
		} else {
			// Done
			m.step = ScreenCredentialMethod
			m.cursor = 0
		}

	case ScreenMCPName:
		m.currentMCPName = strings.TrimSpace(m.textInput.Value())
		if m.currentMCPName == "" {
			m.err = fmt.Errorf("server name is required")
		} else {
			m.err = nil
			m.step = ScreenMCPCommand
			m.textInput.SetValue("")
			m.textInput.Focus()
		}

	case ScreenMCPCommand:
		m.currentMCPCommand = strings.TrimSpace(m.textInput.Value())
		if m.currentMCPCommand == "" {
			m.err = fmt.Errorf("command is required")
		} else {
			m.err = nil
			m.step = ScreenMCPArgs
			m.textInput.SetValue("")
			m.textInput.Focus()
		}

	case ScreenMCPArgs:
		m.currentMCPArgs = m.textInput.Value()
		m.step = ScreenMCPProbe
		m.probeError = ""
		m.probedTools = nil
		return m, m.probeMCPServer()

	case ScreenMCPProbe:
		// Handled by tea.Cmd from probeMCPServer
		// Just wait for result

	case ScreenMCPDenySelect:
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
		m.step = ScreenMCPAdd
		m.cursor = 0

	case ScreenCredentialMethod:
		methods := m.getCredentialMethods()
		if m.cursor >= 0 && m.cursor < len(methods) {
			m.config.CredentialMethod = methods[m.cursor].name
		}
		m.step = ScreenConfirm
		m.cursor = 0

	case ScreenConfirm:
		if m.cursor == 0 { // Confirm
			m.step = ScreenWriteFiles
			return m, m.writeFiles()
		}
		// Cancel - go back to scenario
		m.step = ScreenScenario
		m.cursor = 0

	case ScreenComplete:
		return m, tea.Quit
	}

	return m, nil
}

func (m *Model) initFeatureSelection() {
	m.selected = map[int]bool{
		0: m.config.EnableMCP,
		1: m.config.EnableMemory,
		2: m.config.EnableTelemetry,
	}
}

func (m *Model) applyFeatureSelection() {
	m.config.EnableMCP = m.selected[0]
	m.config.EnableMemory = m.selected[1]
	m.config.EnableTelemetry = m.selected[2]
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

// Run starts the setup wizard. opts are passed through to tea.NewProgram,
// e.g. tea.WithInput/tea.WithOutput for tests.
func Run(opts ...tea.ProgramOption) error {
	p := tea.NewProgram(New(), opts...)
	_, err := p.Run()
	return err
}
