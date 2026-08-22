package setup

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinayprograms/agent/internal/configfile"
)

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

// View renders the current step
func (m Model) View() string {
	var s strings.Builder

	switch m.screen {
	case ScreenWelcome:
		s.WriteString(m.viewWelcome())
	case ScreenScenario:
		s.WriteString(m.viewScenario())
	case ScreenProvider:
		s.WriteString(m.viewProvider())
	case ScreenModel:
		s.WriteString(m.viewModel())
	case ScreenCustomModel:
		s.WriteString(m.viewCustomModel())
	case ScreenAPIKey:
		s.WriteString(m.viewAPIKey())
	case ScreenBaseURL:
		s.WriteString(m.viewBaseURL())
	case ScreenThinking:
		s.WriteString(m.viewThinking())
	case ScreenSmallLLM:
		s.WriteString(m.viewSmallLLM())
	case ScreenSmallLLMProvider:
		s.WriteString(m.viewSmallLLMProvider())
	case ScreenSmallLLMModel:
		s.WriteString(m.viewSmallLLMModel())
	case ScreenWorkspace:
		s.WriteString(m.viewWorkspace())
	case ScreenSecurity:
		s.WriteString(m.viewSecurity())
	case ScreenSecurityMode:
		s.WriteString(m.viewSecurityMode())
	case ScreenProfiles:
		s.WriteString(m.viewProfiles())
	case ScreenProfilesConfig:
		s.WriteString(m.viewProfilesConfig())
	case ScreenFeatures:
		s.WriteString(m.viewFeatures())
	case ScreenMCPAdd:
		s.WriteString(m.viewMCPAdd())
	case ScreenMCPName:
		s.WriteString(m.viewMCPName())
	case ScreenMCPCommand:
		s.WriteString(m.viewMCPCommand())
	case ScreenMCPArgs:
		s.WriteString(m.viewMCPArgs())
	case ScreenMCPProbe:
		s.WriteString(m.viewMCPProbe())
	case ScreenMCPDenySelect:
		s.WriteString(m.viewMCPDenySelect())
	case ScreenCredentialMethod:
		s.WriteString(m.viewCredentialMethod())
	case ScreenConfirm:
		s.WriteString(m.viewConfirm())
	case ScreenWriteFiles:
		s.WriteString(m.viewWriting())
	case ScreenComplete:
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
	if m.policyWarning != "" {
		s.WriteString(errorStyle.Render("⚠ " + m.policyWarning))
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
	case configfile.ProviderOllamaCloud, configfile.ProviderOllamaLocal:
		s.WriteString(subtitleStyle.Render("Enter the Ollama model to use") + "\n\n")
		s.WriteString(dimStyle.Render("Examples: llama3.2, codellama, mistral, phi3, qwen2.5") + "\n")
		s.WriteString(dimStyle.Render("Run 'ollama list' to see your downloaded models") + "\n\n")
	case configfile.ProviderLMStudio:
		s.WriteString(subtitleStyle.Render("Enter the model name from LM Studio") + "\n\n")
		s.WriteString(dimStyle.Render("Check LM Studio UI for available model names") + "\n\n")
	case configfile.ProviderLiteLLM:
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
	s.WriteString(dimStyle.Render("This will be stored in credentials.toml (mode 0600)"))
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
		{"Semantic Memory", "Remember insights across conversations (always persistent)"},
		{"Telemetry", "OpenTelemetry observability"},
	}

	for i, f := range features {
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

	// Show hint about Claude CLI if Anthropic is selected but no CLI credentials found
	if m.config.Provider == configfile.ProviderAnthropic && !hasClaudeCLICredentials() {
		s.WriteString("\n" + dimStyle.Render("💡 Tip: Install Claude CLI and run 'claude login' for easier auth"))
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
	return (titleStyle.Render("Writing Files...") + "\n\n" +
		normalStyle.Render("Creating configuration files..."))
}

func (m Model) viewComplete() string {
	if m.err != nil {
		return (errorStyle.Render("Error") + "\n\n" +
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
		envVar := configfile.DefaultEnvVar(m.config.Provider)
		s.WriteString(dimStyle.Render("  2. Set "+envVar+" environment variable") + "\n")
		s.WriteString(dimStyle.Render("  3. Run: agent run your-workflow.agent") + "\n")
	} else {
		s.WriteString(dimStyle.Render("  2. Run: agent run your-workflow.agent") + "\n")
	}

	s.WriteString("\n" + dimStyle.Render("Press q to exit"))
	return (s.String())
}
