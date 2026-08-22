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
	"github.com/vinayprograms/agent/internal/configfile"
	"github.com/vinayprograms/agentkit/policy"
)

// The wizard's vocabulary is defined by internal/configfile, which owns the
// shape of a generated configuration and renders it as TOML. These aliases
// keep the wizard's own code (and its tests) reading in setup terms.
type (
	// Config is the configuration the wizard collects.
	Config = configfile.Options
	// MCPServerSetup holds MCP server configuration during setup.
	MCPServerSetup = configfile.MCPServerSetup
	// ProfileConfig holds a capability profile configuration.
	ProfileConfig = configfile.ProfileConfig
)

// Deployment scenarios
const (
	ScenarioLocal      = configfile.ScenarioLocal
	ScenarioDev        = configfile.ScenarioDev
	ScenarioTeam       = configfile.ScenarioTeam
	ScenarioProduction = configfile.ScenarioProduction
	ScenarioDocker     = configfile.ScenarioDocker
)

// Provider options
const (
	ProviderAnthropic   = configfile.ProviderAnthropic
	ProviderOpenAI      = configfile.ProviderOpenAI
	ProviderGoogle      = configfile.ProviderGoogle
	ProviderGroq        = configfile.ProviderGroq
	ProviderMistral     = configfile.ProviderMistral
	ProviderXAI         = configfile.ProviderXAI
	ProviderOpenRouter  = configfile.ProviderOpenRouter
	ProviderOllamaCloud = configfile.ProviderOllamaCloud
	ProviderOllamaLocal = configfile.ProviderOllamaLocal
	ProviderLiteLLM     = configfile.ProviderLiteLLM
	ProviderLMStudio    = configfile.ProviderLMStudio
	ProviderCustom      = configfile.ProviderCustom
)

// Model is the bubbletea model for the setup wizard
type Model struct {
	screen    Screen
	config    Config
	cursor    int
	textInput textinput.Model
	err       error
	width     int
	height    int

	// dir is the directory agent.toml/policy.toml are read from and written
	// to. Defaults to "." (the process cwd); inject with Dir for tests or
	// callers that don't want to rely on the process's working directory.
	dir string

	// ctx bounds operations that need one (currently the MCP probe). Set
	// from New's positional ctx parameter.
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

// Option configures a Model at construction. See Dir.
type Option func(*Model)

// Dir sets the directory agent.toml/policy.toml are read from and written
// to. Default is ".".
func Dir(dir string) Option {
	return func(m *Model) { m.dir = dir }
}

// New creates a new setup model. ctx bounds operations that need one
// (currently the MCP probe).
func New(ctx context.Context, opts ...Option) Model {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	m := Model{
		screen:    ScreenWelcome,
		textInput: ti,
		dir:       ".",
		ctx:       ctx,
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
		m.screen = ScreenMCPDenySelect
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
		m.screen = ScreenComplete
		return m, nil
	case errMsg:
		m.err = msg.error
		m.screen = ScreenComplete
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Handle text input steps first - let them capture all keys except ctrl+c and enter
		if m.isTextInputScreen() {
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
			if m.screen == ScreenComplete {
				return m, tea.Quit
			}
			if m.screen == ScreenWelcome {
				return m, tea.Quit
			}
			// Go back
			if m.screen > ScreenWelcome {
				m.screen = m.previousScreen()
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
			max := m.maxCursorForScreen()
			if m.cursor < max {
				m.cursor++
			}
			return m, nil

		case " ":
			// Toggle selection for multi-select steps
			if m.screen == ScreenFeatures || m.screen == ScreenMCPDenySelect {
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
	switch m.screen {
	case ScreenWelcome:
		m.screen = ScreenScenario
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
		m.screen = ScreenProvider
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
			m.screen = ScreenCustomModel
			m.textInput.SetValue(m.config.Model)
			m.textInput.Placeholder = "e.g., llama3.2, claude-sonnet-4"
			m.textInput.Focus()
		} else {
			m.screen = ScreenModel
			m.cursor = m.findModelIndex()
		}

	case ScreenCustomModel:
		model := strings.TrimSpace(m.textInput.Value())
		if model == "" {
			m.err = fmt.Errorf("model name is required")
		} else {
			m.err = nil
			m.config.Model = model
			m.screen = ScreenAPIKey
			m.textInput.SetValue("")
			m.textInput.Placeholder = "sk-... (leave empty to keep existing)"
			m.textInput.EchoMode = textinput.EchoPassword
		}

	case ScreenModel:
		models := m.getModels()
		if m.cursor >= 0 && m.cursor < len(models) {
			m.config.Model = models[m.cursor].id
		}
		m.screen = ScreenAPIKey
		m.textInput.SetValue("")
		m.textInput.Placeholder = "sk-... (leave empty to keep existing)"
		m.textInput.EchoMode = textinput.EchoPassword

	case ScreenAPIKey:
		if m.textInput.Value() != "" {
			m.config.APIKey = m.textInput.Value()
		}
		m.textInput.EchoMode = textinput.EchoNormal
		if m.needsBaseURL() {
			m.screen = ScreenBaseURL
			if m.editMode && m.config.BaseURL != "" {
				m.textInput.SetValue(m.config.BaseURL)
			} else {
				m.textInput.SetValue(m.getDefaultBaseURL())
			}
			m.textInput.Placeholder = "https://..."
		} else {
			m.screen = ScreenThinking
			m.cursor = m.findThinkingIndex()
		}

	case ScreenBaseURL:
		m.config.BaseURL = m.textInput.Value()
		m.screen = ScreenThinking
		m.cursor = m.findThinkingIndex()

	case ScreenThinking:
		thinkingOptions := []string{"auto", "off", "low", "medium", "high"}
		if m.cursor >= 0 && m.cursor < len(thinkingOptions) {
			m.config.Thinking = thinkingOptions[m.cursor]
		}
		m.screen = ScreenSmallLLM
		if m.config.SmallLLMEnabled {
			m.cursor = 0 // Yes
		} else {
			m.cursor = 1 // No
		}

	case ScreenSmallLLM:
		m.config.SmallLLMEnabled = m.cursor == 0 // Yes
		if m.config.SmallLLMEnabled {
			m.screen = ScreenSmallLLMProvider
			m.cursor = m.findProviderIndex(m.config.SmallLLMProvider)
		} else {
			m.screen = ScreenWorkspace
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
		m.screen = ScreenSmallLLMModel
		m.textInput.SetValue(m.config.SmallLLMModel)
		m.textInput.Placeholder = "model name"

	case ScreenSmallLLMModel:
		m.config.SmallLLMModel = m.textInput.Value()
		m.screen = ScreenWorkspace
		m.textInput.SetValue(m.config.Workspace)
		m.textInput.Placeholder = "/path/to/workspace"

	case ScreenWorkspace:
		m.config.Workspace = m.textInput.Value()
		if m.config.Workspace == "" {
			m.config.Workspace = "."
		}
		m.screen = ScreenSecurity
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
		m.screen = ScreenSecurityMode
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
		m.screen = ScreenProfiles
		if m.config.UseProfiles {
			m.cursor = 0 // Yes
		} else {
			m.cursor = 1 // No
		}

	case ScreenProfiles:
		m.config.UseProfiles = m.cursor == 0 // Yes
		if m.config.UseProfiles {
			m.screen = ScreenProfilesConfig
			m.cursor = 0
		} else {
			m.screen = ScreenFeatures
			m.cursor = 0
			m.initFeatureSelection()
		}

	case ScreenProfilesConfig:
		// Auto-configure profiles based on provider
		m.configureDefaultProfiles()
		m.screen = ScreenFeatures
		m.cursor = 0
		m.initFeatureSelection()

	case ScreenFeatures:
		m.applyFeatureSelection()
		if m.config.EnableMCP {
			m.screen = ScreenMCPAdd
			m.cursor = 0
		} else {
			m.screen = ScreenCredentialMethod
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
			m.screen = ScreenMCPProbe
			m.probeError = ""
			m.probedTools = nil
			return m, m.probeMCPServer()
		} else if m.cursor == numServers {
			// Add new server
			m.screen = ScreenMCPName
			m.textInput.SetValue("")
			m.textInput.Focus()
		} else {
			// Done
			m.screen = ScreenCredentialMethod
			m.cursor = 0
		}

	case ScreenMCPName:
		m.currentMCPName = strings.TrimSpace(m.textInput.Value())
		if m.currentMCPName == "" {
			m.err = fmt.Errorf("server name is required")
		} else {
			m.err = nil
			m.screen = ScreenMCPCommand
			m.textInput.SetValue("")
			m.textInput.Focus()
		}

	case ScreenMCPCommand:
		m.currentMCPCommand = strings.TrimSpace(m.textInput.Value())
		if m.currentMCPCommand == "" {
			m.err = fmt.Errorf("command is required")
		} else {
			m.err = nil
			m.screen = ScreenMCPArgs
			m.textInput.SetValue("")
			m.textInput.Focus()
		}

	case ScreenMCPArgs:
		m.currentMCPArgs = m.textInput.Value()
		m.screen = ScreenMCPProbe
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
		m.screen = ScreenMCPAdd
		m.cursor = 0

	case ScreenCredentialMethod:
		methods := m.getCredentialMethods()
		if m.cursor >= 0 && m.cursor < len(methods) {
			m.config.CredentialMethod = methods[m.cursor].name
		}
		m.screen = ScreenConfirm
		m.cursor = 0

	case ScreenConfirm:
		if m.cursor == 0 { // Confirm
			m.screen = ScreenWriteFiles
			return m, m.writeFiles()
		}
		// Cancel - go back to scenario
		m.screen = ScreenScenario
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

func (m *Model) applyScenarioDefaults() { m.config.ApplyScenario() }

func (m *Model) setDefaultModel() { m.config.SetDefaultModel() }

func (m *Model) setDefaultSmallModel() { m.config.SetDefaultSmallModel() }

func (m *Model) configureDefaultProfiles() { m.config.ConfigureDefaultProfiles() }

// Run starts the setup wizard against dir, the directory agent.toml and
// policy.toml are read from and written to (empty means the working
// directory). opts are passed through to tea.NewProgram, e.g.
// tea.WithInput/tea.WithOutput for tests.
func Run(ctx context.Context, dir string, opts ...tea.ProgramOption) error {
	var modelOpts []Option
	if dir != "" {
		modelOpts = append(modelOpts, Dir(dir))
	}
	p := tea.NewProgram(New(ctx, modelOpts...), opts...)
	_, err := p.Run()
	return err
}
