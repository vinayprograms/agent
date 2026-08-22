package setup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/policy"
)

// TestMain doubles as a minimal stdio MCP server when re-executed with
// SETUP_FAKE_MCP set, so probeMCPServer can be exercised end-to-end without
// an external binary.
func TestMain(m *testing.M) {
	if os.Getenv("SETUP_FAKE_MCP") != "" {
		runFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}

func runFakeMCPServer() {
	type req struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var r req
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil || r.ID == 0 {
			continue // notification or garbage
		}
		var result any
		switch r.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "read_file", "description": "read"},
				{"name": "delete_file", "description": "delete"},
			}}
		default:
			result = map[string]any{}
		}
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": result})
		fmt.Fprintf(os.Stdout, "%s\n", out)
	}
}

// isolate runs the test in an empty cwd with a throwaway HOME so that
// New(context.Background()) finds no existing config and nothing touches the real user files.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)
	return dir
}

func key(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func send(t *testing.T, m Model, msgs ...tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(Model)
	}
	return m, cmd
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	m, _ = send(t, m, runes(s))
	return m
}

// ---------------------------------------------------------------------------
// Generators

func TestGeneratePolicyTOML_RoundTripsWithNoUnknownKeys(t *testing.T) {
	isolate(t)
	scenarios := []string{ScenarioLocal, ScenarioDev, ScenarioTeam, ScenarioProduction, ScenarioDocker}
	for _, sc := range scenarios {
		for _, deny := range []bool{false, true} {
			for _, bash := range []bool{false, true} {
				for _, web := range []bool{false, true} {
					for _, mcpMode := range []string{"off", "empty", "servers"} {
						name := fmt.Sprintf("%s/deny=%t/bash=%t/web=%t/mcp=%s", sc, deny, bash, web, mcpMode)
						t.Run(name, func(t *testing.T) {
							m := New(context.Background())
							m.config.Scenario = sc
							m.config.DefaultDeny = deny
							m.config.AllowBash = bash
							m.config.AllowWeb = web
							m.config.EnableMCP = mcpMode != "off"
							if mcpMode == "servers" {
								m.config.MCPServers["memory"] = MCPServerSetup{Command: "npx"}
								m.config.MCPServers["fs"] = MCPServerSetup{Command: "uvx"}
							}
							assertPolicyMatchesConfig(t, m)
						})
					}
				}
			}
		}
	}
}

func assertPolicyMatchesConfig(t *testing.T, m Model) {
	t.Helper()
	content := m.generatePolicyTOML()
	pol, unknown, err := policy.FromTOMLWithUnknownKeys(content, "/ws", "/home/u")
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, content)
	}
	if len(unknown) != 0 {
		t.Fatalf("unknown keys %v in:\n%s", unknown, content)
	}
	cfg := m.config
	if pol.DefaultDeny != cfg.DefaultDeny {
		t.Errorf("default_deny = %t, want %t", pol.DefaultDeny, cfg.DefaultDeny)
	}
	if got := pol.GetAllowedDirs(); len(got) != 2 || got[0] != "/ws" || got[1] != "/tmp" {
		t.Errorf("allowed_dirs = %v", got)
	}
	for _, tool := range []string{"read", "write"} {
		if !pol.IsToolEnabled(tool) || len(pol.GetToolPolicy(tool).Deny) == 0 {
			t.Errorf("%s must be enabled with a deny list", tool)
		}
	}
	// The v1.2.0 schema has no per-tool off switch: a tool is disabled only
	// when it is unlisted AND default_deny is set.
	wantBash := cfg.AllowBash || !cfg.DefaultDeny
	if pol.IsToolEnabled("bash") != wantBash {
		t.Errorf("bash enabled = %t, want %t", !wantBash, wantBash)
	}
	if cfg.AllowBash && cfg.DefaultDeny && len(pol.GetToolPolicy("bash").Deny) == 0 {
		t.Error("restrictive bash must carry a deny list")
	}
	wantWeb := cfg.AllowWeb || !cfg.DefaultDeny
	for _, tool := range []string{"web_search", "web_fetch"} {
		if pol.IsToolEnabled(tool) != wantWeb {
			t.Errorf("%s enabled = %t, want %t", tool, !wantWeb, wantWeb)
		}
	}
	switch {
	case cfg.EnableMCP && len(cfg.MCPServers) > 0:
		if pol.MCP == nil || !pol.MCP.Enabled {
			t.Fatal("mcp must be enabled")
		}
		if want := []string{"fs:*", "memory:*"}; strings.Join(pol.MCP.Allow, ",") != strings.Join(want, ",") {
			t.Errorf("mcp.allow = %v, want %v (sorted)", pol.MCP.Allow, want)
		}
		if ok, reason, _ := pol.CheckMCPTool("memory", "store"); !ok {
			t.Errorf("memory:store denied: %s", reason)
		}
	case cfg.EnableMCP:
		if pol.MCP == nil || !pol.MCP.Enabled || len(pol.MCP.Allow) != 0 {
			t.Errorf("mcp = %+v, want enabled with no allow list", pol.MCP)
		}
	default:
		if pol.MCP != nil {
			t.Errorf("mcp section must be absent, got %+v", pol.MCP)
		}
	}
}

func TestGeneratePolicyTOML_LegacyKeysAbsent(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.config.DefaultDeny = true
	m.config.EnableMCP = true
	m.config.MCPServers["memory"] = MCPServerSetup{}
	content := m.generatePolicyTOML()
	for _, legacy := range []string{"enabled = true\n[", "allowlist", "denylist", "allowed_tools", "mcp.default_deny", "sandbox", "[security]"} {
		if strings.Contains(content, legacy) {
			t.Errorf("legacy key %q present:\n%s", legacy, content)
		}
	}
}

func TestGenerateAgentTOML(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.config.Provider = ProviderOpenAI
	m.config.Model = "gpt-4o"
	m.config.BaseURL = "http://proxy/v1"
	m.config.CredentialMethod = "env"
	m.config.SmallLLMEnabled = true
	m.config.SmallLLMProvider = ProviderOpenAI
	m.config.SmallLLMModel = "gpt-4o-mini"
	m.config.SmallLLMBaseURL = "http://proxy/v1"
	m.config.UseProfiles = true
	m.config.Profiles["fast"] = ProfileConfig{Model: "gpt-4o-mini", Thinking: "off"}
	m.config.Profiles["plain"] = ProfileConfig{Model: "gpt-4o"}
	m.config.SecurityMode = "paranoid"
	m.config.EnableTelemetry = true
	m.config.EnableMCP = true
	m.config.MCPServers["memory"] = MCPServerSetup{
		Command: "npx", Args: []string{"-y", "srv"}, DeniedTools: []string{"delete"},
	}

	var cfg existingConfig
	if _, err := toml.Decode(m.generateAgentTOML(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderOpenAI || cfg.LLM.APIKeyEnv != "OPENAI_API_KEY" || cfg.LLM.BaseURL != "http://proxy/v1" {
		t.Errorf("llm = %+v", cfg.LLM)
	}
	if cfg.SmallLLM.Model != "gpt-4o-mini" || cfg.SmallLLM.BaseURL != "http://proxy/v1" {
		t.Errorf("small_llm = %+v", cfg.SmallLLM)
	}
	if cfg.Profiles["fast"].Thinking != "off" || cfg.Profiles["plain"].Model != "gpt-4o" {
		t.Errorf("profiles = %+v", cfg.Profiles)
	}
	if cfg.Security.Mode != "paranoid" || !cfg.Telemetry.Enabled {
		t.Errorf("security/telemetry = %+v %+v", cfg.Security, cfg.Telemetry)
	}
	srv := cfg.MCP.Servers["memory"]
	if srv.Command != "npx" || len(srv.Args) != 2 || len(srv.DeniedTools) != 1 {
		t.Errorf("mcp server = %+v", srv)
	}

	// MCP enabled with no servers emits a commented placeholder only.
	m.config.MCPServers = map[string]MCPServerSetup{}
	m.config.CredentialMethod = "file"
	out := m.generateAgentTOML()
	if !strings.Contains(out, "# [mcp.servers.memory]") || strings.Contains(out, "api_key_env") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Defaults and lookups

func TestApplyScenarioDefaults(t *testing.T) {
	isolate(t)
	tests := []struct {
		scenario string
		provider string
		deny     bool
		bash     bool
		cred     string
	}{
		{ScenarioLocal, ProviderOllamaLocal, false, true, "file"},
		{ScenarioDev, ProviderAnthropic, false, true, "file"},
		{ScenarioTeam, ProviderLiteLLM, true, true, "file"},
		{ScenarioProduction, ProviderLiteLLM, true, false, "file"},
		{ScenarioDocker, ProviderLiteLLM, true, true, "env"},
	}
	for _, tt := range tests {
		m := New(context.Background())
		m.config.Scenario = tt.scenario
		m.applyScenarioDefaults()
		if m.config.Provider != tt.provider || m.config.DefaultDeny != tt.deny ||
			m.config.AllowBash != tt.bash || m.config.CredentialMethod != tt.cred {
			t.Errorf("%s: %+v", tt.scenario, m.config)
		}
	}
}

func TestDefaultModels(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	for _, p := range m.getProviders() {
		m.config.Provider = p.id
		m.setDefaultModel()
		if p.id != ProviderCustom && m.config.Model == "" {
			t.Errorf("%s: no default model", p.id)
		}
		if len(m.getModels()) == 0 {
			t.Errorf("%s: no model options", p.id)
		}
		m.config.SmallLLMProvider = p.id
		m.config.BaseURL = "http://x"
		m.setDefaultSmallModel()
		if m.config.SmallLLMModel == "" && p.id != ProviderCustom {
			t.Errorf("%s: no default small model", p.id)
		}
		if m.config.SmallLLMBaseURL != "http://x" {
			t.Errorf("%s: base URL not inherited", p.id)
		}
	}
	m.config.Provider = ProviderCustom
	m.config.Model = "mine"
	m.config.SmallLLMProvider = ProviderCustom
	m.setDefaultSmallModel()
	if m.config.SmallLLMModel != "mine" {
		t.Errorf("custom small model = %q", m.config.SmallLLMModel)
	}
}

func TestConfigureDefaultProfiles(t *testing.T) {
	isolate(t)
	for provider, want := range map[string]int{ProviderAnthropic: 3, ProviderOpenAI: 3, ProviderLiteLLM: 2, ProviderGroq: 1} {
		m := New(context.Background())
		m.config.Provider = provider
		m.configureDefaultProfiles()
		if len(m.config.Profiles) != want {
			t.Errorf("%s: %d profiles, want %d", provider, len(m.config.Profiles), want)
		}
	}
}

func TestFindIndexes(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	if m.findScenarioIndex() != 0 || m.findProviderIndex("") != 0 || m.findModelIndex() != 0 {
		t.Error("empty values must map to index 0")
	}
	m.config.Scenario = ScenarioTeam
	m.config.Provider = ProviderGoogle
	m.config.Model = "gemini-1.5-pro"
	m.config.Thinking = "high"
	if m.findScenarioIndex() != 2 || m.findProviderIndex(ProviderGoogle) != 2 || m.findModelIndex() != 2 || m.findThinkingIndex() != 4 {
		t.Errorf("indexes: %d %d %d %d", m.findScenarioIndex(), m.findProviderIndex(ProviderGoogle), m.findModelIndex(), m.findThinkingIndex())
	}
	m.config.Scenario, m.config.Model, m.config.Thinking = "nope", "nope", "nope"
	if m.findScenarioIndex() != 0 || m.findProviderIndex("nope") != 0 || m.findModelIndex() != 0 || m.findThinkingIndex() != 0 {
		t.Error("unknown values must map to index 0")
	}
}

func TestProviderHelpers(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	urls := map[string]string{
		ProviderOllamaLocal: "http://localhost:11434/v1",
		ProviderLMStudio:    "http://localhost:1234/v1",
		ProviderOpenRouter:  "https://openrouter.ai/api/v1",
		ProviderLiteLLM:     "http://localhost:4000/v1",
		ProviderAnthropic:   "",
	}
	for p, want := range urls {
		m.config.Provider = p
		if got := m.getDefaultBaseURL(); got != want {
			t.Errorf("%s base url = %q", p, got)
		}
	}
	m.config.Provider = ProviderAnthropic
	if m.needsCustomModelInput() || m.needsBaseURL() {
		t.Error("anthropic needs neither custom model nor base URL")
	}
	m.config.Provider = ProviderCustom
	if !m.needsCustomModelInput() || !m.needsBaseURL() {
		t.Error("custom needs both")
	}
	for p, want := range map[string]string{ProviderAnthropic: "ANTHROPIC_API_KEY", ProviderOpenAI: "OPENAI_API_KEY",
		ProviderGoogle: "GOOGLE_API_KEY", ProviderMistral: "MISTRAL_API_KEY", ProviderGroq: "GROQ_API_KEY", ProviderXAI: "API_KEY"} {
		if got := getDefaultEnvVar(p); got != want {
			t.Errorf("%s env = %q", p, got)
		}
	}
}

func TestGetDefaultConfigDir(t *testing.T) {
	home := isolate(t)
	if got := getDefaultConfigDir(); got != filepath.Join(home, ".config", "agent") {
		t.Errorf("config dir = %q", got)
	}
	t.Setenv("HOME", "")
	if got := getDefaultConfigDir(); got != "." {
		t.Errorf("config dir without HOME = %q", got)
	}
}

func TestGetSortedMCPServerNames(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	for _, n := range []string{"zeta", "alpha", "mid"} {
		m.config.MCPServers[n] = MCPServerSetup{}
	}
	if got := strings.Join(m.getSortedMCPServerNames(), ","); got != "alpha,mid,zeta" {
		t.Errorf("sorted = %s", got)
	}
}

// ---------------------------------------------------------------------------
// Existing-config loading (edit mode)

func TestLoadExistingConfig(t *testing.T) {
	isolate(t)
	agentTOML := `
[agent]
workspace = "/ws"
[llm]
provider = "openai"
model = "gpt-4o"
base_url = "http://proxy"
thinking = "low"
api_key_env = "OPENAI_API_KEY"
[small_llm]
provider = "openai"
model = "gpt-4o-mini"
[profiles.fast]
model = "gpt-4o-mini"
thinking = "off"
[security]
mode = "paranoid"
[telemetry]
enabled = true
[mcp.servers.memory]
command = "npx"
args = ["-y", "srv"]
denied_tools = ["delete"]
`
	policyTOML := `
default_deny = true
[tools.read]
[tools.bash]
`
	os.WriteFile("agent.toml", []byte(agentTOML), 0644)
	os.WriteFile("policy.toml", []byte(policyTOML), 0644)

	m := New(context.Background())
	if !m.editMode || m.existingFile != "agent.toml" {
		t.Fatal("expected edit mode")
	}
	c := m.config
	if c.Workspace != "/ws" || c.Provider != "openai" || c.Model != "gpt-4o" || c.BaseURL != "http://proxy" ||
		c.Thinking != "low" || c.CredentialMethod != "env" {
		t.Errorf("llm config = %+v", c)
	}
	if !c.SmallLLMEnabled || c.SmallLLMModel != "gpt-4o-mini" || !c.UseProfiles || c.Profiles["fast"].Thinking != "off" {
		t.Errorf("small llm/profiles = %+v", c)
	}
	if c.SecurityMode != "paranoid" || !c.EnableTelemetry || !c.EnableMCP || c.MCPServers["memory"].DeniedTools[0] != "delete" {
		t.Errorf("security/telemetry/mcp = %+v", c)
	}
	if !c.DefaultDeny || !c.AllowBash || c.AllowWeb {
		t.Errorf("policy: deny=%t bash=%t web=%t", c.DefaultDeny, c.AllowBash, c.AllowWeb)
	}
}

func TestLoadExistingConfig_Errors(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	if m.editMode {
		t.Fatal("no agent.toml must not enter edit mode")
	}
	os.WriteFile("agent.toml", []byte("not = [valid"), 0644)
	m = New(context.Background())
	if m.editMode {
		t.Fatal("malformed agent.toml must not enter edit mode")
	}
}

// ---------------------------------------------------------------------------
// Wizard flow via Update

func TestUpdate_Navigation(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.width != 80 || m.height != 24 {
		t.Error("window size not recorded")
	}
	if _, cmd := send(t, m, key(tea.KeyCtrlC)); cmd == nil {
		t.Error("ctrl+c must quit")
	}
	if _, cmd := send(t, m, runes("q")); cmd == nil {
		t.Error("q on welcome must quit")
	}

	m, _ = send(t, m, key(tea.KeyEnter)) // -> scenario
	if m.screen != ScreenScenario {
		t.Fatalf("step = %d", m.screen)
	}
	m, _ = send(t, m, key(tea.KeyUp)) // clamps at 0
	m, _ = send(t, m, key(tea.KeyDown), runes("j"), runes("k"))
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
	for range 10 {
		m, _ = send(t, m, key(tea.KeyDown))
	}
	if m.cursor != len(m.getScenarios())-1 {
		t.Errorf("cursor = %d, must clamp", m.cursor)
	}
	m, _ = send(t, m, key(tea.KeyTab), key(tea.KeySpace)) // no-ops here
	m, _ = send(t, m, runes("q"))                         // back
	if m.screen != ScreenWelcome {
		t.Errorf("q must go back, step = %d", m.screen)
	}
	m, _ = send(t, m, "unknown message")
	if m.screen != ScreenWelcome {
		t.Error("unknown message must be ignored")
	}
}

func TestUpdate_FullFlow_RestrictiveWithMCP(t *testing.T) {
	home := isolate(t)
	m := New(context.Background())
	m, _ = send(t, m, key(tea.KeyEnter))                   // welcome -> scenario
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // dev -> provider (anthropic)
	if m.config.Scenario != ScenarioDev || m.screen != ScreenProvider || m.config.Provider != ProviderAnthropic {
		t.Fatalf("after scenario: %+v step=%d", m.config, m.screen)
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // anthropic -> model list
	if m.screen != ScreenModel || m.config.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("after provider: step=%d model=%s", m.screen, m.config.Model)
	}
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // opus -> api key
	if m.screen != ScreenAPIKey || m.config.Model != "claude-opus-4-20250514" {
		t.Fatalf("after model: step=%d model=%s", m.screen, m.config.Model)
	}
	m = typeText(t, m, "sk-test")
	m, _ = send(t, m, key(tea.KeyEnter)) // -> thinking (anthropic has no base URL)
	if m.screen != ScreenThinking || m.config.APIKey != "sk-test" {
		t.Fatalf("after api key: step=%d", m.screen)
	}
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)) // low -> small llm
	if m.config.Thinking != "low" || m.screen != ScreenSmallLLM || m.cursor != 0 {
		t.Fatalf("after thinking: %+v", m.config)
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // yes -> small provider
	m, _ = send(t, m, key(tea.KeyEnter)) // anthropic -> small model
	if m.screen != ScreenSmallLLMModel || m.textInput.Value() != "claude-3-5-haiku-20241022" {
		t.Fatalf("after small provider: step=%d value=%s", m.screen, m.textInput.Value())
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // -> workspace
	m = typeText(t, m, "/ws")
	m, _ = send(t, m, key(tea.KeyEnter)) // -> security (cursor 0 = permissive)
	if m.screen != ScreenSecurity || m.config.Workspace != "./ws" && m.config.Workspace != "/ws" {
		t.Fatalf("after workspace: step=%d ws=%s", m.screen, m.config.Workspace)
	}
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // restrictive -> security mode
	if !m.config.DefaultDeny || m.config.AllowBash || m.config.AllowWeb {
		t.Fatalf("restrictive must clear bash/web: %+v", m.config)
	}
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // paranoid -> profiles
	if m.config.SecurityMode != "paranoid" || m.screen != ScreenProfiles || m.cursor != 1 {
		t.Fatalf("after security mode: %+v step=%d", m.config, m.screen)
	}
	m, _ = send(t, m, key(tea.KeyUp), key(tea.KeyEnter)) // yes -> profiles config
	if m.screen != ScreenProfilesConfig || !m.config.UseProfiles {
		t.Fatalf("step = %d", m.screen)
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // -> features
	if m.screen != ScreenFeatures || len(m.config.Profiles) != 3 {
		t.Fatalf("step = %d profiles=%d", m.screen, len(m.config.Profiles))
	}
	m, _ = send(t, m, key(tea.KeySpace), key(tea.KeyEnter)) // toggle MCP on -> mcp add
	if !m.config.EnableMCP || m.screen != ScreenMCPAdd {
		t.Fatalf("mcp=%t step=%d", m.config.EnableMCP, m.screen)
	}

	// Add a server: name, command, args, probe (fake result), deny selection.
	m, _ = send(t, m, key(tea.KeyEnter)) // "Add new" -> name
	m, _ = send(t, m, key(tea.KeyEnter)) // empty name -> error
	if m.err == nil || m.screen != ScreenMCPName {
		t.Fatal("empty name must error")
	}
	m = typeText(t, m, "memory")
	m, _ = send(t, m, key(tea.KeyEnter)) // -> command
	m, _ = send(t, m, key(tea.KeyEnter)) // empty command -> error
	if m.screen != ScreenMCPCommand {
		t.Fatal("empty command must error")
	}
	m = typeText(t, m, "npx")
	m, _ = send(t, m, key(tea.KeyEnter)) // -> args
	m = typeText(t, m, "-y srv")
	m, cmd := send(t, m, key(tea.KeyEnter)) // -> probe
	if m.screen != ScreenMCPProbe || cmd == nil {
		t.Fatalf("probe not started: step=%d", m.screen)
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // enter during probe is a no-op
	m, _ = send(t, m, mcpProbeResult{tools: []string{"read", "delete"}})
	if m.screen != ScreenMCPDenySelect || len(m.probedTools) != 2 {
		t.Fatalf("after probe: step=%d tools=%v", m.screen, m.probedTools)
	}
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeySpace), key(tea.KeyEnter)) // deny "delete"
	srv := m.config.MCPServers["memory"]
	if m.screen != ScreenMCPAdd || srv.Command != "npx" || len(srv.Args) != 2 || strings.Join(srv.DeniedTools, ",") != "delete" {
		t.Fatalf("server = %+v step=%d", srv, m.screen)
	}

	// Edit the existing server: re-probe, pre-selects previously denied tools.
	m, cmd = send(t, m, key(tea.KeyEnter)) // cursor 0 = edit memory
	if m.screen != ScreenMCPProbe || cmd == nil || m.currentMCPArgs != "-y srv" {
		t.Fatalf("edit must re-probe: step=%d args=%q", m.screen, m.currentMCPArgs)
	}
	m, _ = send(t, m, mcpProbeResult{tools: []string{"read", "delete"}})
	if !m.selected[1] || m.selected[0] {
		t.Errorf("previously denied tool must be pre-selected: %v", m.selected)
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // keep -> mcp add

	// Probe failure path.
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // add new
	m = typeText(t, m, "broken")
	m, _ = send(t, m, key(tea.KeyEnter))
	m = typeText(t, m, "nope")
	m, _ = send(t, m, key(tea.KeyEnter), key(tea.KeyEnter)) // no args -> probe
	m, _ = send(t, m, mcpProbeResult{err: fmt.Errorf("boom")})
	if m.probeError != "boom" || m.probedTools != nil {
		t.Fatalf("probe error not recorded: %q", m.probeError)
	}
	if !strings.Contains(m.View(), "boom") {
		t.Error("deny view must show probe error")
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // add without filtering
	if _, ok := m.config.MCPServers["broken"]; !ok {
		t.Fatal("server must be added despite probe error")
	}

	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)) // done -> credentials
	if m.screen != ScreenCredentialMethod {
		t.Fatalf("step = %d", m.screen)
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // file -> confirm
	if m.config.CredentialMethod != "file" || m.screen != ScreenConfirm {
		t.Fatalf("cred=%s step=%d", m.config.CredentialMethod, m.screen)
	}
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // go back -> scenario
	if m.screen != ScreenScenario {
		t.Fatalf("cancel must return to scenario, step=%d", m.screen)
	}
	m.screen = ScreenConfirm
	m.cursor = 0
	m.err = nil                            // parked smell: validation errors are never cleared and would show on the Complete screen
	m, cmd = send(t, m, key(tea.KeyEnter)) // create files
	if m.screen != ScreenWriteFiles || cmd == nil {
		t.Fatal("confirm must start writing")
	}
	m, _ = send(t, m, cmd())
	if m.screen != ScreenComplete || m.err != nil {
		t.Fatalf("write failed: %v", m.err)
	}
	credPath := filepath.Join(home, ".config", "agent", "credentials.toml")
	if strings.Join(m.filesWritten, ",") != "agent.toml,policy.toml,"+credPath {
		t.Errorf("files = %v", m.filesWritten)
	}
	store, err := credentials.NewFileStore(credPath)
	if err != nil || store.Get(ProviderAnthropic) != "sk-test" {
		t.Errorf("credentials not persisted: %v %v", err, store)
	}
	if _, err := policy.FromFile("policy.toml", "/ws", home); err != nil {
		t.Errorf("written policy must parse: %v", err)
	}
	if !strings.Contains(m.View(), "Setup Complete") {
		t.Error("complete view")
	}
	if _, cmd := send(t, m, key(tea.KeyEnter)); cmd == nil {
		t.Error("enter on complete must quit")
	}
	if _, cmd := send(t, m, runes("q")); cmd == nil {
		t.Error("q on complete must quit")
	}
}

func TestUpdate_CustomProviderFlow(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.screen = ScreenScenario
	m.cursor = 0
	m, _ = send(t, m, key(tea.KeyEnter)) // local -> provider (ollama-local)
	m, _ = send(t, m, key(tea.KeyEnter)) // ollama-local -> custom model input
	if m.screen != ScreenCustomModel || m.textInput.Value() != "llama3.2" {
		t.Fatalf("step=%d value=%s", m.screen, m.textInput.Value())
	}
	m.textInput.SetValue("")
	m, _ = send(t, m, key(tea.KeyEnter)) // empty -> error
	if m.err == nil || m.screen != ScreenCustomModel {
		t.Fatal("empty model must error")
	}
	m = typeText(t, m, "phi3")
	m, _ = send(t, m, key(tea.KeyEnter)) // -> api key
	m, _ = send(t, m, key(tea.KeyEnter)) // empty key keeps existing -> base URL (default)
	if m.screen != ScreenBaseURL || m.textInput.Value() != "http://localhost:11434/v1" {
		t.Fatalf("step=%d value=%s", m.screen, m.textInput.Value())
	}
	m, _ = send(t, m, key(tea.KeyEnter)) // -> thinking
	if m.config.BaseURL != "http://localhost:11434/v1" || m.screen != ScreenThinking {
		t.Fatalf("base url = %s step=%d", m.config.BaseURL, m.screen)
	}
	m, _ = send(t, m, key(tea.KeyEnter))                   // auto -> small llm (cursor 1: no)
	m, _ = send(t, m, key(tea.KeyEnter))                   // no -> workspace
	m, _ = send(t, m, key(tea.KeyEnter))                   // "." default -> security
	m, _ = send(t, m, key(tea.KeyEnter))                   // permissive -> mode
	m, _ = send(t, m, key(tea.KeyEnter))                   // default -> profiles
	m, _ = send(t, m, key(tea.KeyEnter))                   // no -> features
	m, _ = send(t, m, key(tea.KeyEnter))                   // no MCP -> credentials
	m, _ = send(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // env -> confirm
	if m.screen != ScreenConfirm || m.config.CredentialMethod != "env" || m.config.Workspace != "." {
		t.Fatalf("step=%d cred=%s ws=%s", m.screen, m.config.CredentialMethod, m.config.Workspace)
	}
	m.err = nil // parked smell: stale validation error, see TestUpdate_FullFlow_RestrictiveWithMCP
	m, cmd := send(t, m, key(tea.KeyEnter))
	m, _ = send(t, m, cmd())
	if m.err != nil || len(m.filesWritten) != 2 {
		t.Fatalf("files=%v err=%v", m.filesWritten, m.err)
	}
	if !strings.Contains(m.View(), "API_KEY environment variable") {
		t.Error("complete view must mention env var")
	}
}

func TestUpdate_EditModeBaseURLAndPreviousStep(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.editMode = true
	m.config.Provider = ProviderLiteLLM
	m.config.BaseURL = "http://existing"
	m.config.SmallLLMEnabled = true
	m.config.SmallLLMProvider = ProviderLiteLLM
	m.config.UseProfiles = true
	m.screen = ScreenScenario
	m, _ = send(t, m, key(tea.KeyEnter)) // edit mode keeps provider
	if m.config.Provider != ProviderLiteLLM {
		t.Fatal("edit mode must not override provider")
	}
	m.screen = ScreenAPIKey
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.textInput.Value() != "http://existing" {
		t.Errorf("edit mode must prefill base URL, got %q", m.textInput.Value())
	}
	m.screen = ScreenSmallLLMProvider
	m.config.SmallLLMModel = "keep-me"
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.config.SmallLLMModel != "keep-me" {
		t.Error("edit mode must keep existing small model")
	}
	m.screen = ScreenSecurity
	m.cursor = 1
	m.config.AllowBash = true
	m, _ = send(t, m, key(tea.KeyEnter))
	if !m.config.AllowBash {
		t.Error("edit mode must keep AllowBash on restrictive")
	}

	// previousScreen skips conditional steps.
	m.config.SmallLLMEnabled = false
	m.screen = ScreenWorkspace
	if m.previousScreen() != ScreenSmallLLM {
		t.Error("back from workspace skips small-llm model when disabled")
	}
	m.screen = ScreenSmallLLMModel
	if m.previousScreen() != ScreenSmallLLM {
		t.Error("back from small-llm model skips provider when disabled")
	}
	m.config.Provider = ProviderAnthropic
	m.screen = ScreenThinking
	if m.previousScreen() != ScreenAPIKey {
		t.Error("back from thinking skips base URL for direct providers")
	}
	m.config.UseProfiles = false
	m.screen = ScreenFeatures
	if m.previousScreen() != ScreenProfiles {
		t.Error("back from features skips profile config")
	}
	m.screen = ScreenModel
	if m.previousScreen() != ScreenProvider {
		t.Error("plain back")
	}
}

func TestMaxCursorForStep(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.config.MCPServers["a"] = MCPServerSetup{}
	want := map[Screen]int{
		ScreenScenario: 4, ScreenProvider: 11, ScreenModel: 0, ScreenThinking: 4, ScreenSmallLLM: 1,
		ScreenSmallLLMProvider: 11, ScreenSecurity: 1, ScreenSecurityMode: 1, ScreenProfiles: 1,
		ScreenFeatures: 3, ScreenMCPAdd: 2, ScreenMCPDenySelect: 0, ScreenCredentialMethod: 1,
		ScreenConfirm: 1, ScreenWelcome: 100,
	}
	for step, n := range want {
		m.screen = step
		if got := m.maxCursorForScreen(); got != n {
			t.Errorf("step %d: max %d, want %d", step, got, n)
		}
	}
	m.screen = ScreenMCPDenySelect
	m.probedTools = []string{"a", "b"}
	if m.maxCursorForScreen() != 1 {
		t.Error("deny select max")
	}
}

// ---------------------------------------------------------------------------
// Views

func TestView_AllSteps(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.config.Provider = ProviderAnthropic
	m.config.Model = "claude-sonnet-4-20250514"
	m.config.BaseURL = "http://x"
	m.config.SmallLLMEnabled = true
	m.config.SmallLLMModel = "haiku"
	m.config.Profiles["fast"] = ProfileConfig{Model: "haiku"}
	m.config.MCPServers["denied"] = MCPServerSetup{DeniedTools: []string{"x"}}
	m.config.MCPServers["open"] = MCPServerSetup{}
	m.selected = map[int]bool{0: true}
	m.probedTools = []string{"read", "write"}
	m.currentMCPName = "srv"
	m.currentMCPCommand = "npx"
	m.err = fmt.Errorf("oops")
	m.cursor = 50 // out of range: views must clamp without panicking

	for step := ScreenWelcome; step <= ScreenComplete; step++ {
		m.screen = step
		if out := m.View(); out == "" {
			t.Errorf("step %d renders empty", step)
		}
	}

	m.screen = ScreenCustomModel
	for _, p := range []string{ProviderOllamaLocal, ProviderLMStudio, ProviderLiteLLM, ProviderCustom} {
		m.config.Provider = p
		if !strings.Contains(m.View(), "Model Name") {
			t.Errorf("%s custom model view", p)
		}
	}

	m.screen = ScreenMCPDenySelect
	m.probeError = ""
	m.probedTools = nil
	if !strings.Contains(m.View(), "No tools discovered") {
		t.Error("empty tools view")
	}

	m.screen = ScreenComplete
	if !strings.Contains(m.View(), "oops") {
		t.Error("error view must show the error")
	}
	m.err = nil
	m.config.CredentialMethod = "file"
	if !strings.Contains(m.View(), "Setup Complete") {
		t.Error("success view")
	}

	m.editMode = true
	m.existingFile = "agent.toml"
	m.screen = ScreenWelcome
	if !strings.Contains(m.View(), "Found existing configuration") {
		t.Error("edit-mode welcome")
	}
	m.screen = ScreenConfirm
	m.config.CredentialMethod = "env"
	if strings.Contains(m.View(), "credentials.toml") {
		t.Error("env method must not list credentials.toml")
	}
}

func TestCredentialMethods_ClaudeCLI(t *testing.T) {
	home := isolate(t)
	m := New(context.Background())
	m.config.Provider = ProviderAnthropic
	if len(m.getCredentialMethods()) != 2 || !strings.Contains(m.viewCredentialMethod(), "Tip") {
		t.Fatal("without Claude CLI: file+env and a tip")
	}

	cli := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"r","expiresAt":%d}}`,
		time.Now().Add(time.Hour).UnixMilli())
	os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(cli), 0600)
	methods := m.getCredentialMethods()
	if len(methods) != 3 || methods[0].name != "claude-cli" || strings.Contains(m.viewCredentialMethod(), "Tip") {
		t.Errorf("with Claude CLI: %v", methods)
	}
	m.screen = ScreenCredentialMethod
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.config.CredentialMethod != "claude-cli" {
		t.Errorf("method = %s", m.config.CredentialMethod)
	}
	m.config.Provider = ProviderOpenAI
	if len(m.getCredentialMethods()) != 2 {
		t.Error("claude-cli is anthropic-only")
	}
}

// ---------------------------------------------------------------------------
// File writing

func TestWriteCredentials(t *testing.T) {
	home := isolate(t)
	path := filepath.Join(home, ".config", "agent", "credentials.toml")
	m := New(context.Background())
	m.config.Provider = ProviderOpenAI
	m.config.APIKey = "first"
	if got, err := m.writeCredentials(); err != nil || got != path {
		t.Fatalf("path=%s err=%v", got, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}

	// Second write preserves other providers.
	m.config.Provider = ProviderGroq
	m.config.APIKey = "second"
	if _, err := m.writeCredentials(); err != nil {
		t.Fatal(err)
	}
	store, err := credentials.NewFileStore(path)
	if err != nil || store.Get(ProviderOpenAI) != "first" || store.Get(ProviderGroq) != "second" {
		t.Errorf("store = %v err=%v", store, err)
	}

	// Insecure existing file is refused rather than overwritten.
	os.Chmod(path, 0644)
	if _, err := m.writeCredentials(); err == nil {
		t.Error("insecure file must be refused")
	}

	t.Setenv("HOME", "")
	if _, err := m.writeCredentials(); err == nil {
		t.Error("missing home must error")
	}
}

func TestWriteFiles_Errors(t *testing.T) {
	dir := isolate(t)
	m := New(context.Background())
	m.config.CredentialMethod = "file"
	m.config.APIKey = "k"

	os.Mkdir(filepath.Join(dir, "agent.toml"), 0755) // directory blocks the file write
	if msg := m.writeFiles()(); msg.(errMsg).error == nil {
		t.Error("agent.toml failure must surface")
	}
	os.Remove(filepath.Join(dir, "agent.toml"))

	os.Mkdir(filepath.Join(dir, "policy.toml"), 0755)
	if msg := m.writeFiles()(); msg.(errMsg).error == nil {
		t.Error("policy.toml failure must surface")
	}
	os.Remove(filepath.Join(dir, "policy.toml"))

	t.Setenv("HOME", "")
	if msg := m.writeFiles()(); msg.(errMsg).error == nil {
		t.Error("credentials failure must surface")
	}
}

// ---------------------------------------------------------------------------
// MCP probing against the fake server (this test binary re-executed)

func TestProbeMCPServer(t *testing.T) {
	isolate(t)
	exe, err := os.Executable()
	if err != nil {
		t.Skip("cannot locate test binary:", err)
	}
	t.Setenv("SETUP_FAKE_MCP", "1")
	m := New(context.Background())
	m.currentMCPName = "fake"
	m.currentMCPCommand = exe
	m.currentMCPArgs = "-test.run=NONE"
	res := m.probeMCPServer()().(mcpProbeResult)
	if res.err != nil {
		t.Fatalf("probe: %v", res.err)
	}
	if strings.Join(res.tools, ",") != "read_file,delete_file" {
		t.Errorf("tools = %v", res.tools)
	}

	m.currentMCPCommand = filepath.Join(t.TempDir(), "missing-server")
	m.currentMCPArgs = ""
	res = m.probeMCPServer()().(mcpProbeResult)
	if res.err == nil || !strings.Contains(res.err.Error(), "failed to connect") {
		t.Errorf("expected connect failure, got %v", res.err)
	}
}

func TestInit(t *testing.T) {
	isolate(t)
	if New(context.Background()).Init() == nil {
		t.Error("Init must return the blink command")
	}
}

func TestUpdate_RemainingBranches(t *testing.T) {
	isolate(t)
	m := New(context.Background())

	m, _ = send(t, m, errMsg{fmt.Errorf("write failed")})
	if m.screen != ScreenComplete || m.err == nil {
		t.Error("errMsg must land on the complete screen with the error")
	}

	m.screen = ScreenAPIKey // text-input step
	if _, cmd := send(t, m, key(tea.KeyCtrlC)); cmd == nil {
		t.Error("ctrl+c must quit from text input too")
	}

	// Pre-set cursors reflect current config when entering a step.
	m.screen = ScreenWorkspace
	m.textInput.SetValue("")
	m.config.DefaultDeny = true
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.config.Workspace != "." || m.cursor != 1 {
		t.Errorf("workspace=%q cursor=%d", m.config.Workspace, m.cursor)
	}
	m.config.SecurityMode = "paranoid"
	m.editMode = true
	m, _ = send(t, m, key(tea.KeyEnter)) // security -> mode, cursor on paranoid
	if m.cursor != 1 {
		t.Errorf("mode cursor = %d", m.cursor)
	}
	m.config.UseProfiles = true
	m, _ = send(t, m, key(tea.KeyEnter)) // mode -> profiles, cursor on yes
	if m.cursor != 0 {
		t.Errorf("profiles cursor = %d", m.cursor)
	}
}

func TestView_CursorHighlight(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.probedTools = []string{"a"}
	m.config.MCPServers["x"] = MCPServerSetup{}
	m.cursor = 0
	for _, step := range []Screen{ScreenScenario, ScreenProvider, ScreenModel, ScreenThinking, ScreenSmallLLM,
		ScreenSmallLLMProvider, ScreenSecurity, ScreenSecurityMode, ScreenProfiles, ScreenFeatures,
		ScreenMCPAdd, ScreenMCPDenySelect, ScreenConfirm} {
		m.screen = step
		if !strings.Contains(m.View(), "> ") {
			t.Errorf("step %d: cursor row not highlighted", step)
		}
	}
}

func TestWriteCredentials_SaveFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := isolate(t)
	dir := filepath.Join(home, ".config", "agent")
	os.MkdirAll(dir, 0700)
	os.Chmod(dir, 0500) // exists but unwritable: NewFileStore succeeds, Save fails
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	m := New(context.Background())
	m.config.Provider = ProviderOpenAI
	m.config.APIKey = "k"
	if _, err := m.writeCredentials(); err == nil || !strings.Contains(err.Error(), "save credentials") {
		t.Errorf("expected save failure, got %v", err)
	}
}

func TestLoadExistingConfig_LegacyPolicyKeysWarn(t *testing.T) {
	isolate(t)
	os.WriteFile("agent.toml", []byte("[llm]\nmodel = \"gpt-4o\"\n"), 0644)
	os.WriteFile("policy.toml", []byte("default_deny = false\n[bash]\nenabled = false\n"), 0644)
	m := New(context.Background())
	if !strings.Contains(m.policyWarning, "bash") {
		t.Fatalf("legacy keys must be surfaced, got %q", m.policyWarning)
	}
	if !strings.Contains(m.viewWelcome(), "unrecognised keys") {
		t.Error("welcome screen must show the policy warning")
	}
}

func TestWriteCredentials_EmptyExistingFile(t *testing.T) {
	home := isolate(t)
	path := filepath.Join(home, ".config", "agent", "credentials.toml")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, nil, 0600)
	m := New(context.Background())
	m.config.Provider = ProviderOpenAI
	m.config.APIKey = "k"
	if _, err := m.writeCredentials(); err != nil {
		t.Fatalf("empty credentials file must not fail: %v", err)
	}
	store, err := credentials.NewFileStore(path)
	if err != nil || store["openai"].APIKey != "k" {
		t.Errorf("key not saved: %v %+v", err, store)
	}
}

func TestHasClaudeCLICredentials_ExpiredToken(t *testing.T) {
	home := isolate(t)
	cli := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"r","expiresAt":%d}}`,
		time.Now().Add(-time.Hour).UnixMilli())
	os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(cli), 0600)
	if hasClaudeCLICredentials() {
		t.Error("an expired Claude CLI token must not count as usable credentials")
	}
}

// ---------------------------------------------------------------------------
// Fresh-mode legacy policy.toml warning (no agent.toml yet)

func TestFreshMode_LegacyPolicyWarn(t *testing.T) {
	isolate(t)
	os.WriteFile("policy.toml", []byte("default_deny = false\n[bash]\nenabled = false\n"), 0644)
	m := New(context.Background())
	if m.editMode {
		t.Fatal("no agent.toml present: must not be edit mode")
	}
	if !strings.Contains(m.policyWarning, "bash") {
		t.Fatalf("legacy keys must be surfaced even without agent.toml, got %q", m.policyWarning)
	}
	if !strings.Contains(m.viewWelcome(), "unrecognised keys") {
		t.Error("welcome screen must show the policy warning in fresh mode too")
	}
}

func TestFreshMode_MalformedPolicyDoesNotWarnOrPanic(t *testing.T) {
	isolate(t)
	os.WriteFile("policy.toml", []byte("not valid toml [[["), 0644)
	m := New(context.Background())
	if m.policyWarning != "" {
		t.Errorf("malformed policy.toml must not produce a warning, got %q", m.policyWarning)
	}
}

// ---------------------------------------------------------------------------
// dir/ctx injection (WithDir, WithContext)

func TestWithDir_ReadsAndWritesRelativeToInjectedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	sub := filepath.Join(root, "workspace")
	os.MkdirAll(sub, 0755)
	os.WriteFile(filepath.Join(sub, "agent.toml"), []byte("[llm]\nmodel = \"gpt-4o\"\n"), 0644)

	m := New(context.Background(), Dir(sub))
	if !m.editMode || m.config.Model != "gpt-4o" {
		t.Fatalf("WithDir must load agent.toml from the injected dir, got editMode=%v model=%q", m.editMode, m.config.Model)
	}

	m.config.APIKey = ""
	msg := m.writeFiles()()
	if _, ok := msg.(filesWrittenMsg); !ok {
		t.Fatalf("writeFiles must succeed, got %#v", msg)
	}
	if _, err := os.Stat(filepath.Join(sub, "policy.toml")); err != nil {
		t.Errorf("policy.toml must be written under the injected dir: %v", err)
	}
}

func TestWithContext_BoundsProbe(t *testing.T) {
	isolate(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := New(ctx)
	m.currentMCPCommand = "irrelevant"
	res := m.probeMCPServer()().(mcpProbeResult)
	if res.err == nil {
		t.Error("a canceled model context must fail the probe")
	}
}

// ---------------------------------------------------------------------------
// Run passthrough

func TestRun_OptionsPassthrough(t *testing.T) {
	isolate(t)
	in := strings.NewReader("q")
	var out bytes.Buffer
	if err := Run(context.Background(), tea.WithInput(in), tea.WithOutput(&out), tea.WithoutSignals()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// ---------------------------------------------------------------------------
// m.err cleared after a valid retry (was previously left stale)

func TestHandleEnter_ErrClearedAfterValidRetry(t *testing.T) {
	isolate(t)
	m := New(context.Background())
	m.screen = ScreenMCPName
	m.textInput.SetValue("")
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.err == nil {
		t.Fatal("empty server name must set an error")
	}
	m.textInput.SetValue("ok")
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.err != nil {
		t.Errorf("a valid retry must clear the stale error, got %v", m.err)
	}

	m.screen = ScreenMCPCommand
	m.textInput.SetValue("")
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.err == nil {
		t.Fatal("empty command must set an error")
	}
	m.textInput.SetValue("npx")
	m, _ = send(t, m, key(tea.KeyEnter))
	if m.err != nil {
		t.Errorf("a valid retry must clear the stale error, got %v", m.err)
	}
}
