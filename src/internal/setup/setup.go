// Package setup provides the interactive setup wizard for the agent.
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Deployment scenarios
const (
	ScenarioLocal      = "local"       // Personal machine, experimenting
	ScenarioDev        = "dev"         // Development/testing
	ScenarioTeam       = "team"        // Small team, shared configs
	ScenarioProduction = "production"  // Production deployment
	ScenarioEnterprise = "enterprise"  // Large-scale enterprise
	ScenarioDocker     = "docker"      // Container deployment
	ScenarioK8s        = "kubernetes"  // Kubernetes deployment
)

// Provider options
const (
	ProviderAnthropic   = "anthropic"
	ProviderOpenAI      = "openai"
	ProviderGoogle      = "google"
	ProviderGroq        = "groq"
	ProviderMistral     = "mistral"
	ProviderOpenRouter  = "openrouter"
	ProviderOllama      = "ollama"
	ProviderLiteLLM     = "litellm"
	ProviderLMStudio    = "lmstudio"
	ProviderCustom      = "custom"
)

// Config holds the setup configuration
type Config struct {
	// Deployment
	Scenario    string
	Workspace   string
	ConfigDir   string
	
	// LLM
	Provider    string
	Model       string
	APIKey      string
	BaseURL     string
	
	// Profiles
	UseProfiles bool
	Profiles    map[string]ProfileConfig
	
	// Security
	DefaultDeny  bool
	AllowBash    bool
	AllowWeb     bool
	
	// Features
	EnableMCP       bool
	EnableTelemetry bool
	EnableMemory    bool
	
	// Credentials
	CredentialMethod string // "file", "env", "vault", "k8s-secret"
}

// ProfileConfig holds a capability profile configuration
type ProfileConfig struct {
	Provider string
	Model    string
	BaseURL  string
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

	boxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("99")).
		Padding(1, 2)
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
	StepWorkspace
	StepSecurity
	StepProfiles
	StepCredentialMethod
	StepFeatures
	StepConfirm
	StepWriteFiles
	StepComplete
)

// Model is the bubbletea model for the setup wizard
type Model struct {
	step       Step
	config     Config
	cursor     int
	textInput  textinput.Model
	err        error
	width      int
	height     int
	
	// For multi-select
	selected   map[int]bool
	
	// Results
	filesWritten []string
}

// New creates a new setup model
func New() Model {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	return Model{
		step:      StepWelcome,
		textInput: ti,
		config: Config{
			Workspace:  ".",
			ConfigDir:  getDefaultConfigDir(),
			Profiles:   make(map[string]ProfileConfig),
			AllowBash:  true,
			AllowWeb:   true,
			EnableMemory: true,
		},
		selected: make(map[int]bool),
	}
}

func getDefaultConfigDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "grid")
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
			// Allow quitting at welcome
			if m.step == StepWelcome {
				return m, tea.Quit
			}
			// Go back
			if m.step > StepWelcome {
				m.step--
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
			// Move to next input field if applicable
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

func (m Model) isTextInputStep() bool {
	switch m.step {
	case StepModel, StepAPIKey, StepBaseURL, StepWorkspace:
		return true
	}
	return false
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.step {
	case StepWelcome:
		m.step = StepScenario
		m.cursor = 0

	case StepScenario:
		scenarios := m.getScenarioOptions()
		if m.cursor < len(scenarios) {
			m.config.Scenario = scenarios[m.cursor].value
			m.applyScenarioDefaults()
			m.step = StepProvider
			m.cursor = 0
		}

	case StepProvider:
		providers := m.getProviderOptions()
		if m.cursor < len(providers) {
			m.config.Provider = providers[m.cursor].value
			m.step = StepModel
			m.textInput.SetValue(m.getDefaultModel())
			m.textInput.Focus()
		}

	case StepModel:
		m.config.Model = m.textInput.Value()
		if m.config.Model == "" {
			m.config.Model = m.getDefaultModel()
		}
		m.step = StepAPIKey
		m.textInput.SetValue("")
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.Placeholder = "sk-..."
		m.textInput.Focus()

	case StepAPIKey:
		m.config.APIKey = m.textInput.Value()
		m.textInput.EchoMode = textinput.EchoNormal
		if m.needsBaseURL() {
			m.step = StepBaseURL
			m.textInput.SetValue(m.getDefaultBaseURL())
			m.textInput.Placeholder = "https://..."
		} else {
			m.step = StepWorkspace
			m.textInput.SetValue(m.config.Workspace)
			m.textInput.Placeholder = "/path/to/workspace"
		}
		m.textInput.Focus()

	case StepBaseURL:
		m.config.BaseURL = m.textInput.Value()
		m.step = StepWorkspace
		m.textInput.SetValue(m.config.Workspace)
		m.textInput.Placeholder = "/path/to/workspace"
		m.textInput.Focus()

	case StepWorkspace:
		m.config.Workspace = m.textInput.Value()
		if m.config.Workspace == "" {
			m.config.Workspace = "."
		}
		m.step = StepSecurity
		m.cursor = 0

	case StepSecurity:
		securityLevels := m.getSecurityOptions()
		if m.cursor < len(securityLevels) {
			m.applySecurityLevel(securityLevels[m.cursor].value)
			m.step = StepCredentialMethod
			m.cursor = 0
		}

	case StepCredentialMethod:
		methods := m.getCredentialMethods()
		if m.cursor < len(methods) {
			m.config.CredentialMethod = methods[m.cursor].value
			m.step = StepFeatures
			m.cursor = 0
			// Pre-select based on scenario
			m.selected[0] = m.config.EnableMemory
			m.selected[1] = m.config.EnableMCP
			m.selected[2] = m.config.EnableTelemetry
		}

	case StepFeatures:
		m.config.EnableMemory = m.selected[0]
		m.config.EnableMCP = m.selected[1]
		m.config.EnableTelemetry = m.selected[2]
		m.step = StepConfirm
		m.cursor = 0

	case StepConfirm:
		if m.cursor == 0 { // Yes, create files
			m.step = StepWriteFiles
			return m, m.writeConfigFiles
		} else { // No, go back
			m.step = StepScenario
			m.cursor = 0
		}

	case StepComplete:
		return m, tea.Quit
	}

	return m, nil
}

type option struct {
	label       string
	value       string
	description string
}

func (m Model) getScenarioOptions() []option {
	return []option{
		{"🧪 Local Experimenter", ScenarioLocal, "Personal machine, trying things out"},
		{"💻 Development", ScenarioDev, "Development and testing environment"},
		{"👥 Team", ScenarioTeam, "Small team with shared configurations"},
		{"🚀 Production", ScenarioProduction, "Production deployment with security"},
		{"🏢 Enterprise", ScenarioEnterprise, "Large-scale with compliance needs"},
		{"🐳 Docker", ScenarioDocker, "Container-based deployment"},
		{"☸️  Kubernetes", ScenarioK8s, "Kubernetes cluster deployment"},
	}
}

func (m Model) getProviderOptions() []option {
	return []option{
		{"Anthropic (Claude)", ProviderAnthropic, "claude-sonnet-4, claude-opus-4"},
		{"OpenAI (GPT)", ProviderOpenAI, "gpt-4o, gpt-4-turbo, o1"},
		{"Google (Gemini)", ProviderGoogle, "gemini-1.5-pro, gemini-2.0"},
		{"Groq (Fast)", ProviderGroq, "llama-3, mixtral (fast inference)"},
		{"Mistral", ProviderMistral, "mistral-large, codestral"},
		{"OpenRouter", ProviderOpenRouter, "Multi-provider gateway"},
		{"Ollama (Local)", ProviderOllama, "Self-hosted, local models"},
		{"LiteLLM (Proxy)", ProviderLiteLLM, "LLM proxy/gateway"},
		{"LM Studio (Local)", ProviderLMStudio, "Local GUI + server"},
		{"Custom Endpoint", ProviderCustom, "Any OpenAI-compatible API"},
	}
}

func (m Model) getSecurityOptions() []option {
	return []option{
		{"🟢 Permissive", "permissive", "All tools enabled, no restrictions"},
		{"🟡 Balanced", "balanced", "Most tools enabled, some restrictions"},
		{"🔴 Strict", "strict", "Default deny, explicit allowlist"},
		{"🔒 Paranoid", "paranoid", "Minimal tools, maximum restrictions"},
	}
}

func (m Model) getCredentialMethods() []option {
	methods := []option{
		{"📄 Config File", "file", "Store in ~/.config/grid/credentials.toml"},
		{"🌍 Environment Variables", "env", "Use PROVIDER_API_KEY env vars"},
	}
	
	// Add advanced options for enterprise scenarios
	if m.config.Scenario == ScenarioEnterprise || m.config.Scenario == ScenarioK8s {
		methods = append(methods,
			option{"🔐 HashiCorp Vault", "vault", "Fetch from Vault at runtime"},
			option{"☸️  Kubernetes Secrets", "k8s-secret", "Mount as K8s secret"},
		)
	}
	
	return methods
}

func (m *Model) applyScenarioDefaults() {
	switch m.config.Scenario {
	case ScenarioLocal:
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.EnableMCP = false
		m.config.EnableTelemetry = false
		m.config.EnableMemory = true
		m.config.CredentialMethod = "file"

	case ScenarioDev:
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.EnableMCP = true
		m.config.EnableTelemetry = false
		m.config.EnableMemory = true
		m.config.CredentialMethod = "env"

	case ScenarioTeam:
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
		m.config.EnableMCP = true
		m.config.EnableTelemetry = true
		m.config.EnableMemory = true
		m.config.CredentialMethod = "env"

	case ScenarioProduction:
		m.config.DefaultDeny = true
		m.config.AllowBash = false
		m.config.AllowWeb = true
		m.config.EnableMCP = true
		m.config.EnableTelemetry = true
		m.config.EnableMemory = true
		m.config.CredentialMethod = "env"

	case ScenarioEnterprise:
		m.config.DefaultDeny = true
		m.config.AllowBash = false
		m.config.AllowWeb = true
		m.config.EnableMCP = true
		m.config.EnableTelemetry = true
		m.config.EnableMemory = true
		m.config.CredentialMethod = "vault"

	case ScenarioDocker:
		m.config.DefaultDeny = true
		m.config.AllowBash = false
		m.config.AllowWeb = true
		m.config.EnableMCP = false
		m.config.EnableTelemetry = true
		m.config.EnableMemory = true
		m.config.CredentialMethod = "env"

	case ScenarioK8s:
		m.config.DefaultDeny = true
		m.config.AllowBash = false
		m.config.AllowWeb = true
		m.config.EnableMCP = true
		m.config.EnableTelemetry = true
		m.config.EnableMemory = true
		m.config.CredentialMethod = "k8s-secret"
	}
}

func (m *Model) applySecurityLevel(level string) {
	switch level {
	case "permissive":
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
	case "balanced":
		m.config.DefaultDeny = false
		m.config.AllowBash = true
		m.config.AllowWeb = true
	case "strict":
		m.config.DefaultDeny = true
		m.config.AllowBash = false
		m.config.AllowWeb = true
	case "paranoid":
		m.config.DefaultDeny = true
		m.config.AllowBash = false
		m.config.AllowWeb = false
	}
}

func (m Model) needsBaseURL() bool {
	switch m.config.Provider {
	case ProviderOpenRouter, ProviderOllama, ProviderLiteLLM, ProviderLMStudio, ProviderCustom:
		return true
	}
	return false
}

func (m Model) getDefaultModel() string {
	switch m.config.Provider {
	case ProviderAnthropic:
		return "claude-sonnet-4-20250514"
	case ProviderOpenAI:
		return "gpt-4o"
	case ProviderGoogle:
		return "gemini-1.5-pro"
	case ProviderGroq:
		return "llama-3.3-70b-versatile"
	case ProviderMistral:
		return "mistral-large-latest"
	case ProviderOpenRouter:
		return "anthropic/claude-3.5-sonnet"
	case ProviderOllama:
		return "llama3:70b"
	case ProviderLiteLLM:
		return "gpt-4"
	case ProviderLMStudio:
		return "local-model"
	default:
		return "gpt-4"
	}
}

func (m Model) getDefaultBaseURL() string {
	switch m.config.Provider {
	case ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	case ProviderOllama:
		return "http://localhost:11434/v1"
	case ProviderLiteLLM:
		return "http://localhost:4000"
	case ProviderLMStudio:
		return "http://localhost:1234/v1"
	default:
		return "https://api.example.com/v1"
	}
}

// View renders the UI
func (m Model) View() string {
	switch m.step {
	case StepWelcome:
		return m.viewWelcome()
	case StepScenario:
		return m.viewScenario()
	case StepProvider:
		return m.viewProvider()
	case StepModel:
		return m.viewModel()
	case StepAPIKey:
		return m.viewAPIKey()
	case StepBaseURL:
		return m.viewBaseURL()
	case StepWorkspace:
		return m.viewWorkspace()
	case StepSecurity:
		return m.viewSecurity()
	case StepCredentialMethod:
		return m.viewCredentialMethod()
	case StepFeatures:
		return m.viewFeatures()
	case StepConfirm:
		return m.viewConfirm()
	case StepWriteFiles:
		return m.viewWriting()
	case StepComplete:
		return m.viewComplete()
	}
	return ""
}

func (m Model) viewWelcome() string {
	s := titleStyle.Render("🤖 Headless Agent Setup Wizard")
	s += "\n\n"
	s += normalStyle.Render("This wizard will help you configure the headless agent for your environment.")
	s += "\n\n"
	s += dimStyle.Render("We'll set up:")
	s += "\n"
	s += dimStyle.Render("  • agent.toml      - Agent configuration")
	s += "\n"
	s += dimStyle.Render("  • credentials.toml - API keys (secure)")
	s += "\n"
	s += dimStyle.Render("  • policy.toml     - Security policy")
	s += "\n\n"
	s += infoStyle.Render("Press Enter to start, or q to quit")
	return boxStyle.Render(s)
}

func (m Model) viewScenario() string {
	s := titleStyle.Render("📦 Deployment Scenario")
	s += "\n"
	s += subtitleStyle.Render("How will you be using the agent?")
	s += "\n\n"

	options := m.getScenarioOptions()
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "▸ "
			style = selectedStyle
		}
		s += cursor + style.Render(opt.label)
		if i == m.cursor {
			s += dimStyle.Render("  " + opt.description)
		}
		s += "\n"
	}

	s += "\n" + dimStyle.Render("↑/↓ to navigate, Enter to select, q to go back")
	return boxStyle.Render(s)
}

func (m Model) viewProvider() string {
	s := titleStyle.Render("🧠 LLM Provider")
	s += "\n"
	s += subtitleStyle.Render("Which LLM provider will you use?")
	s += "\n\n"

	options := m.getProviderOptions()
	maxVisible := 8
	start := 0
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(options) {
		end = len(options)
	}

	if start > 0 {
		s += dimStyle.Render("  ↑ more above") + "\n"
	}

	for i := start; i < end; i++ {
		opt := options[i]
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "▸ "
			style = selectedStyle
		}
		s += cursor + style.Render(opt.label)
		if i == m.cursor {
			s += dimStyle.Render("  " + opt.description)
		}
		s += "\n"
	}

	if end < len(options) {
		s += dimStyle.Render("  ↓ more below") + "\n"
	}

	s += "\n" + dimStyle.Render("↑/↓ to navigate, Enter to select")
	return boxStyle.Render(s)
}

func (m Model) viewModel() string {
	s := titleStyle.Render("📝 Model Name")
	s += "\n"
	s += subtitleStyle.Render("Enter the model to use (or press Enter for default)")
	s += "\n\n"
	s += m.textInput.View()
	s += "\n\n"
	s += dimStyle.Render("Default: " + m.getDefaultModel())
	s += "\n\n" + dimStyle.Render("Enter to confirm")
	return boxStyle.Render(s)
}

func (m Model) viewAPIKey() string {
	s := titleStyle.Render("🔑 API Key")
	s += "\n"
	
	if m.config.Provider == ProviderOllama || m.config.Provider == ProviderLMStudio {
		s += subtitleStyle.Render("API key (optional for local models, press Enter to skip)")
	} else {
		s += subtitleStyle.Render("Enter your API key")
	}
	s += "\n\n"
	s += m.textInput.View()
	s += "\n\n"

	envVar := m.getEnvVarName()
	s += dimStyle.Render("You can also set via: " + envVar)
	s += "\n\n" + dimStyle.Render("Enter to confirm")
	return boxStyle.Render(s)
}

func (m Model) getEnvVarName() string {
	switch m.config.Provider {
	case ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case ProviderOpenAI:
		return "OPENAI_API_KEY"
	case ProviderGoogle:
		return "GOOGLE_API_KEY"
	case ProviderGroq:
		return "GROQ_API_KEY"
	case ProviderMistral:
		return "MISTRAL_API_KEY"
	case ProviderOpenRouter:
		return "OPENROUTER_API_KEY"
	default:
		return strings.ToUpper(m.config.Provider) + "_API_KEY"
	}
}

func (m Model) viewBaseURL() string {
	s := titleStyle.Render("🌐 API Base URL")
	s += "\n"
	s += subtitleStyle.Render("Enter the API endpoint URL")
	s += "\n\n"
	s += m.textInput.View()
	s += "\n\n" + dimStyle.Render("Enter to confirm")
	return boxStyle.Render(s)
}

func (m Model) viewWorkspace() string {
	s := titleStyle.Render("📁 Workspace Directory")
	s += "\n"
	s += subtitleStyle.Render("Where will the agent work? (relative or absolute path)")
	s += "\n\n"
	s += m.textInput.View()
	s += "\n\n" + dimStyle.Render("Enter to confirm")
	return boxStyle.Render(s)
}

func (m Model) viewSecurity() string {
	s := titleStyle.Render("🔒 Security Level")
	s += "\n"
	s += subtitleStyle.Render("How strict should security be?")
	s += "\n\n"

	options := m.getSecurityOptions()
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "▸ "
			style = selectedStyle
		}
		s += cursor + style.Render(opt.label)
		if i == m.cursor {
			s += dimStyle.Render("  " + opt.description)
		}
		s += "\n"
	}

	s += "\n" + dimStyle.Render("↑/↓ to navigate, Enter to select")
	return boxStyle.Render(s)
}

func (m Model) viewCredentialMethod() string {
	s := titleStyle.Render("🗝️ Credential Storage")
	s += "\n"
	s += subtitleStyle.Render("How should API keys be stored/accessed?")
	s += "\n\n"

	options := m.getCredentialMethods()
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "▸ "
			style = selectedStyle
		}
		s += cursor + style.Render(opt.label)
		if i == m.cursor {
			s += dimStyle.Render("  " + opt.description)
		}
		s += "\n"
	}

	s += "\n" + dimStyle.Render("↑/↓ to navigate, Enter to select")
	return boxStyle.Render(s)
}

func (m Model) viewFeatures() string {
	s := titleStyle.Render("⚙️ Features")
	s += "\n"
	s += subtitleStyle.Render("Enable/disable optional features")
	s += "\n\n"

	features := []struct {
		name string
		desc string
	}{
		{"Memory (persistence)", "Remember context across sessions"},
		{"MCP Servers", "Connect to external tool servers"},
		{"Telemetry", "Usage metrics and tracing"},
	}

	for i, f := range features {
		cursor := "  "
		style := normalStyle
		check := "[ ]"
		if m.selected[i] {
			check = "[✓]"
		}
		if i == m.cursor {
			cursor = "▸ "
			style = selectedStyle
		}
		s += cursor + style.Render(check + " " + f.name)
		if i == m.cursor {
			s += dimStyle.Render("  " + f.desc)
		}
		s += "\n"
	}

	s += "\n" + dimStyle.Render("Space to toggle, Enter to confirm")
	return boxStyle.Render(s)
}

func (m Model) viewConfirm() string {
	s := titleStyle.Render("✅ Configuration Summary")
	s += "\n\n"

	s += normalStyle.Render("Scenario:    ") + selectedStyle.Render(m.config.Scenario) + "\n"
	s += normalStyle.Render("Provider:    ") + selectedStyle.Render(m.config.Provider) + "\n"
	s += normalStyle.Render("Model:       ") + selectedStyle.Render(m.config.Model) + "\n"
	if m.config.BaseURL != "" {
		s += normalStyle.Render("Base URL:    ") + selectedStyle.Render(m.config.BaseURL) + "\n"
	}
	s += normalStyle.Render("Workspace:   ") + selectedStyle.Render(m.config.Workspace) + "\n"
	s += normalStyle.Render("Credentials: ") + selectedStyle.Render(m.config.CredentialMethod) + "\n"
	
	security := "Permissive"
	if m.config.DefaultDeny {
		security = "Strict"
	}
	s += normalStyle.Render("Security:    ") + selectedStyle.Render(security) + "\n"

	s += "\n" + normalStyle.Render("Files to create:") + "\n"
	s += dimStyle.Render("  • agent.toml") + "\n"
	if m.config.CredentialMethod == "file" && m.config.APIKey != "" {
		s += dimStyle.Render("  • ~/.config/grid/credentials.toml") + "\n"
	}
	s += dimStyle.Render("  • policy.toml") + "\n"

	s += "\n"
	options := []string{"Yes, create files", "No, go back"}
	for i, opt := range options {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "▸ "
			style = selectedStyle
		}
		s += cursor + style.Render(opt) + "\n"
	}

	return boxStyle.Render(s)
}

func (m Model) viewWriting() string {
	s := titleStyle.Render("📝 Writing Configuration...")
	s += "\n\n"
	s += dimStyle.Render("Creating files...")
	return boxStyle.Render(s)
}

func (m Model) viewComplete() string {
	s := successStyle.Render("✅ Setup Complete!")
	s += "\n\n"
	s += normalStyle.Render("Created files:") + "\n"
	for _, f := range m.filesWritten {
		s += successStyle.Render("  ✓ ") + normalStyle.Render(f) + "\n"
	}

	s += "\n" + normalStyle.Render("Next steps:") + "\n"
	
	if m.config.CredentialMethod == "env" {
		s += dimStyle.Render(fmt.Sprintf("  1. Set %s environment variable", m.getEnvVarName())) + "\n"
		s += dimStyle.Render("  2. Create an Agentfile") + "\n"
		s += dimStyle.Render("  3. Run: agent run Agentfile") + "\n"
	} else {
		s += dimStyle.Render("  1. Create an Agentfile") + "\n"
		s += dimStyle.Render("  2. Run: agent run Agentfile") + "\n"
	}

	s += "\n" + dimStyle.Render("Press q to exit")
	return boxStyle.Render(s)
}

// writeConfigFiles creates the configuration files
func (m Model) writeConfigFiles() tea.Msg {
	var files []string

	// Create agent.toml
	agentContent := m.generateAgentTOML()
	if err := os.WriteFile("agent.toml", []byte(agentContent), 0644); err != nil {
		return errMsg{err}
	}
	files = append(files, "agent.toml")

	// Create policy.toml
	policyContent := m.generatePolicyTOML()
	if err := os.WriteFile("policy.toml", []byte(policyContent), 0644); err != nil {
		return errMsg{err}
	}
	files = append(files, "policy.toml")

	// Create credentials.toml if using file method and key provided
	if m.config.CredentialMethod == "file" && m.config.APIKey != "" {
		configDir := m.config.ConfigDir
		if err := os.MkdirAll(configDir, 0700); err != nil {
			return errMsg{err}
		}
		credPath := filepath.Join(configDir, "credentials.toml")
		credContent := m.generateCredentialsTOML()
		if err := os.WriteFile(credPath, []byte(credContent), 0400); err != nil {
			return errMsg{err}
		}
		files = append(files, credPath)
	}

	return filesWrittenMsg{files}
}

type errMsg struct{ error }
type filesWrittenMsg struct{ files []string }

func (m Model) generateAgentTOML() string {
	var sb strings.Builder

	sb.WriteString("# Agent Configuration\n")
	sb.WriteString("# Generated by: agent setup\n\n")

	sb.WriteString("[agent]\n")
	sb.WriteString(fmt.Sprintf("id = \"%s-agent\"\n", m.config.Scenario))
	sb.WriteString(fmt.Sprintf("workspace = \"%s\"\n\n", m.config.Workspace))

	sb.WriteString("[llm]\n")
	if m.config.Provider != "" {
		sb.WriteString(fmt.Sprintf("provider = \"%s\"\n", m.config.Provider))
	}
	sb.WriteString(fmt.Sprintf("model = \"%s\"\n", m.config.Model))
	if m.config.BaseURL != "" {
		sb.WriteString(fmt.Sprintf("base_url = \"%s\"\n", m.config.BaseURL))
	}
	sb.WriteString("max_tokens = 4096\n\n")

	sb.WriteString("[session]\n")
	sb.WriteString("store = \"file\"\n")
	sb.WriteString("path = \"./sessions\"\n\n")

	if m.config.EnableMemory {
		sb.WriteString("[memory]\n")
		sb.WriteString("enabled = true\n")
		sb.WriteString("path = \"./memory\"\n\n")
	}

	if m.config.EnableTelemetry {
		sb.WriteString("[telemetry]\n")
		sb.WriteString("enabled = true\n")
		sb.WriteString("protocol = \"otlp\"\n")
		sb.WriteString("# endpoint = \"http://localhost:4317\"\n\n")
	}

	if m.config.EnableMCP {
		sb.WriteString("# MCP Server Configuration\n")
		sb.WriteString("# [mcp.servers.memory]\n")
		sb.WriteString("# command = \"npx\"\n")
		sb.WriteString("# args = [\"-y\", \"@modelcontextprotocol/server-memory\"]\n\n")
	}

	// Add profiles for non-local scenarios
	if m.config.Scenario != ScenarioLocal {
		sb.WriteString("# Capability Profiles\n")
		sb.WriteString("[profiles.reasoning-heavy]\n")
		sb.WriteString("model = \"claude-opus-4-20250514\"\n\n")
		sb.WriteString("[profiles.fast]\n")
		sb.WriteString("model = \"claude-haiku-20240307\"\n\n")
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
	if m.config.AllowBash && m.config.DefaultDeny {
		sb.WriteString("allowlist = [\"ls *\", \"cat *\", \"grep *\", \"find *\", \"head *\", \"tail *\"]\n")
		sb.WriteString("denylist = [\"rm -rf *\", \"sudo *\", \"curl * | bash\", \"chmod 777 *\"]\n")
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
	p := tea.NewProgram(New(), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	// Handle final messages
	if m, ok := finalModel.(Model); ok {
		if m.err != nil {
			return m.err
		}
	}

	return nil
}
