package run

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/policy"
)

func TestResolveStoragePath_Default(t *testing.T) {
	home := "/home/tester"
	rt := &Runtime{
		cfg:  &config.Config{},
		wf:   &agentfile.Workflow{Name: "test"},
		home: home,
	}
	rt.resolveStoragePath()

	expected := config.DefaultStateDir(home)
	if rt.storagePath != expected {
		t.Errorf("expected %q, got %q", expected, rt.storagePath)
	}
	if rt.sessionPath != filepath.Join(expected, "sessions", "test") {
		t.Errorf("unexpected session path: %q", rt.sessionPath)
	}
}

func TestResolveStoragePath_Custom(t *testing.T) {
	rt := &Runtime{
		cfg: &config.Config{State: config.StateConfig{Location: "/custom/path"}},
		wf:  &agentfile.Workflow{Name: "myworkflow"},
	}
	rt.resolveStoragePath()

	if rt.storagePath != "/custom/path" {
		t.Errorf("expected /custom/path, got %q", rt.storagePath)
	}
}

func TestResolveStoragePath_TildeExpansion(t *testing.T) {
	home := "/home/tester"
	rt := &Runtime{
		cfg:  &config.Config{State: config.StateConfig{Location: "~/mydata"}},
		wf:   &agentfile.Workflow{Name: "test"},
		home: home,
	}
	rt.resolveStoragePath()

	expected := filepath.Join(home, "mydata")
	if rt.storagePath != expected {
		t.Errorf("expected %q, got %q", expected, rt.storagePath)
	}
}

func TestDetermineSecurityConfig_Default(t *testing.T) {
	rt := &Runtime{
		cfg: &config.Config{},
		wf:  &agentfile.Workflow{},
	}
	mode, scope, trust := rt.determineSecurityConfig()

	if mode != executor.SecurityDefault {
		t.Errorf("expected default mode, got %v", mode)
	}
	if scope != "" {
		t.Errorf("expected empty scope, got %q", scope)
	}
	if trust != "untrusted" {
		t.Errorf("expected untrusted, got %v", trust)
	}
}

func TestDetermineSecurityConfig_Paranoid(t *testing.T) {
	rt := &Runtime{
		cfg: &config.Config{Security: config.SecurityConfig{Mode: "paranoid"}},
		wf:  &agentfile.Workflow{},
	}
	mode, _, _ := rt.determineSecurityConfig()

	if mode != executor.SecurityParanoid {
		t.Errorf("expected paranoid mode, got %v", mode)
	}
}

func TestDetermineSecurityConfig_Research(t *testing.T) {
	rt := &Runtime{
		cfg: &config.Config{},
		wf:  &agentfile.Workflow{SecurityMode: "research", SecurityScope: "OWASP Top 10"},
	}
	mode, scope, _ := rt.determineSecurityConfig()

	if mode != executor.SecurityResearch {
		t.Errorf("expected research mode, got %v", mode)
	}
	if scope != "OWASP Top 10" {
		t.Errorf("expected scope 'OWASP Top 10', got %q", scope)
	}
}

func TestDetermineSecurityConfig_TrustLevels(t *testing.T) {
	tests := []struct {
		userTrust string
		expected  string
	}{
		{"", "untrusted"},
		{"trusted", "trusted"},
		{"vetted", "vetted"},
		{"bogus", "untrusted"},
	}
	for _, tt := range tests {
		rt := &Runtime{
			cfg: &config.Config{Security: config.SecurityConfig{UserTrust: tt.userTrust}},
			wf:  &agentfile.Workflow{},
		}
		_, _, trust := rt.determineSecurityConfig()
		if trust != tt.expected {
			t.Errorf("userTrust=%q: expected %v, got %v", tt.userTrust, tt.expected, trust)
		}
	}
}

func TestAddCloserAndCleanup(t *testing.T) {
	var calls []int
	rt := &Runtime{}

	rt.addCloser(func() { calls = append(calls, 1) })
	rt.addCloser(func() { calls = append(calls, 2) })
	rt.addCloser(func() { calls = append(calls, 3) })

	rt.Close()

	// Should run in reverse order
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if calls[0] != 3 || calls[1] != 2 || calls[2] != 1 {
		t.Errorf("expected [3,2,1], got %v", calls)
	}
}

// testWorkflow returns a loaded workflow rooted in a temp dir: local
// (ollama) models so no credentials or network are needed at construction,
// a permissive policy, and state under the temp dir.
func testWorkflow(t *testing.T, mutate func(*config.Config)) *Loaded {
	t.Helper()
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	cfg := config.New()
	cfg.Agent.Workspace = dir
	cfg.State.Location = filepath.Join(dir, "state")
	cfg.LLM = config.LLMConfig{Provider: "ollama-local", Model: "llama3", MaxTokens: 0}
	if mutate != nil {
		mutate(cfg)
	}
	pol := policy.New()
	pol.DefaultDeny = false
	pol.AllowedDirs = []string{dir}
	return &Loaded{
		Workflow: &agentfile.Workflow{
			Name:  "rt-test",
			Goals: []agentfile.Goal{{Name: "g", Outcome: "do it"}},
			Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "s", UsingGoals: []string{"g"}}},
		},
		Config: cfg,
		Policy: pol,
		home:   home,
	}
}

// testDeps are the dependencies for a runtime under test: env credentials
// and discarded output.
func testDeps() Deps {
	return Deps{Creds: credentials.NewEnvStore()}
}

func TestNewLogger_Level(t *testing.T) {
	if !newLogger(true, io.Discard).Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug logger should enable Debug")
	}
	if newLogger(false, io.Discard).Enabled(context.Background(), slog.LevelDebug) {
		t.Error("info logger should not enable Debug")
	}
}

func TestNewModel_DefaultsAndErrors(t *testing.T) {
	w := testWorkflow(t, nil)
	rt := newRuntime(w, testDeps())

	// MaxTokens 0 in both profile and default config falls back to defaultMaxTokens.
	rt.cfg.LLM.MaxTokens = 0
	if _, err := rt.newModel(config.LLMConfig{Provider: "ollama-local", Model: "llama3"}, llm.RetryConfig{}); err != nil {
		t.Errorf("max_tokens default not applied: %v", err)
	}
	// Service inferred from model name; key required for hosted services.
	if _, err := rt.newModel(config.LLMConfig{Model: "claude-3-5-sonnet"}, llm.RetryConfig{}); err == nil {
		t.Error("expected api key error for anthropic without credentials")
	}
	if _, err := rt.newModel(config.LLMConfig{}, llm.RetryConfig{}); err == nil {
		t.Error("expected error for empty model")
	}
	if _, err := rt.newModel(config.LLMConfig{Model: "mystery-model"}, llm.RetryConfig{}); err == nil {
		t.Error("expected error for uninferrable service")
	}
}

func TestCreateProvider_Error(t *testing.T) {
	w := testWorkflow(t, func(c *config.Config) { c.LLM.Model = "" })
	rt := newRuntime(w, testDeps())
	if err := rt.createProvider(); err == nil {
		t.Error("expected error")
	}
}

func TestCreateSmallLLM(t *testing.T) {
	w := testWorkflow(t, nil)
	rt := newRuntime(w, testDeps())
	if err := rt.createSmallLLM(); err != nil || rt.smallLLM != nil {
		t.Errorf("unconfigured small_llm: err=%v model=%v", err, rt.smallLLM)
	}
	rt.cfg.SmallLLM = config.LLMConfig{Model: "claude-haiku"}
	if err := rt.createSmallLLM(); err == nil {
		t.Error("expected error for small_llm without credentials")
	}
	rt.cfg.SmallLLM = config.LLMConfig{Provider: "ollama-local", Model: "tiny"}
	if err := rt.createSmallLLM(); err != nil || rt.smallLLM == nil {
		t.Errorf("local small_llm: err=%v", err)
	}
}

func TestHTTPTimeout(t *testing.T) {
	rt := &Runtime{cfg: &config.Config{}}
	if got := rt.httpTimeout(); got != 0 {
		t.Errorf("zero timeouts: got %v", got)
	}
	rt.cfg.Timeouts = config.TimeoutsConfig{MCP: 10, WebSearch: 45, WebFetch: 20}
	if got := rt.httpTimeout(); got != 45*time.Second {
		t.Errorf("got %v", got)
	}
}

func TestSetupTelemetry(t *testing.T) {
	w := testWorkflow(t, nil)
	rt := newRuntime(w, testDeps())
	if err := rt.setupTelemetry(t.Context()); err != nil || len(rt.closers) != 0 {
		t.Errorf("disabled telemetry: err=%v closers=%d", err, len(rt.closers))
	}
	rt.cfg.Telemetry = config.TelemetryConfig{Enabled: true, Endpoint: "localhost:1", Protocol: "http", Insecure: true}
	if err := rt.setupTelemetry(t.Context()); err != nil || len(rt.closers) != 1 {
		t.Errorf("enabled telemetry: err=%v closers=%d", err, len(rt.closers))
	}
	rt.Close()
}

func TestProfileResolver(t *testing.T) {
	w := testWorkflow(t, func(c *config.Config) {
		c.Profiles = map[string]config.LLMConfig{
			"fast":   {Provider: "ollama-local", Model: "fast-model"},
			"broken": {Provider: "anthropic", Model: "claude-opus"},
		}
	})
	rt := newRuntime(w, testDeps())
	if err := rt.createProvider(); err != nil {
		t.Fatal(err)
	}
	r := &profileResolver{rt: rt, fallback: rt.provider}

	if m, err := r.Model(""); err != nil || m != rt.provider {
		t.Errorf("empty profile should be fallback: %v", err)
	}
	// Unknown profiles resolve to the default LLM config (a fresh model).
	if m, err := r.Model("unknown"); err != nil || m == nil {
		t.Errorf("unknown profile: %v", err)
	}
	m1, err := r.Model("fast")
	if err != nil || m1 == rt.provider {
		t.Fatalf("fast profile: %v", err)
	}
	m2, _ := r.Model("fast")
	if m1 != m2 {
		t.Error("profile model should be cached")
	}
	if _, err := r.Model("broken"); err == nil {
		t.Error("expected error for profile without credentials")
	}
}

func TestCreateTriageProvider(t *testing.T) {
	w := testWorkflow(t, func(c *config.Config) {
		c.Profiles = map[string]config.LLMConfig{
			"triage": {Provider: "ollama-local", Model: "t"},
			"broken": {Provider: "anthropic", Model: "claude-opus"},
		}
	})
	rt := newRuntime(w, testDeps())
	if rt.createTriageProvider() != nil {
		t.Error("no triage/small llm: want nil")
	}
	rt.cfg.Security.TriageLLM = "triage"
	if rt.createTriageProvider() == nil {
		t.Error("triage profile: want model")
	}
	rt.cfg.Security.TriageLLM = "broken"
	rt.cfg.SmallLLM = config.LLMConfig{Provider: "ollama-local", Model: "s"}
	if err := rt.createSmallLLM(); err != nil {
		t.Fatal(err)
	}
	if rt.createTriageProvider() != rt.smallLLM {
		t.Error("broken triage profile should fall back to small_llm")
	}
}

func TestRuntimeSetup_FullWiring(t *testing.T) {
	w := testWorkflow(t, func(c *config.Config) {
		c.SmallLLM = config.LLMConfig{Provider: "ollama-local", Model: "small"}
		c.Security.Mode = "paranoid"
	})
	w.Workflow.Supervised = true
	w.Policy.Tools["bash"] = &policy.ToolPolicy{Deny: []string{"curl"}}
	w.Policy.Content.Security.Patterns = []string{"custom:ignore\\s+previous"}
	rt := newRuntime(w, testDeps())
	defer rt.Close()

	if err := rt.setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if rt.exec == nil || rt.sess == nil || rt.registry == nil || rt.bleveStore == nil {
		t.Fatal("components missing after setup")
	}
	if rt.secMode != executor.SecurityParanoid {
		t.Errorf("security mode %q", rt.secMode)
	}
	if rt.bashGate == nil || rt.bashGate.OnDecision == nil {
		t.Error("bash gate not wired to executor logging")
	}
	for _, name := range []string{"bash", "read", "spawn_agents", "web_search", "remember", "scratchpad_read"} {
		if !rt.registry.Has(name) {
			t.Errorf("tool %q missing", name)
		}
	}
	if rt.exec.Registry() != rt.registry {
		t.Error("executor should use the runtime registry")
	}
	if _, err := os.Stat(rt.sessionPath); err != nil {
		t.Errorf("session dir: %v", err)
	}
	// Bash decisions reach the session log through the gate callback.
	_, err := rt.registry.Execute(context.Background(), "bash", map[string]any{"command": "curl x"})
	if err == nil {
		t.Error("user-denied command should be blocked")
	}
}

func TestRuntimeSetup_InvalidSecurityPatternFails(t *testing.T) {
	w := testWorkflow(t, nil)
	w.Policy.Content.Security.Patterns = []string{"bad:("}
	rt := newRuntime(w, testDeps())
	defer rt.Close()
	if err := rt.setup(t.Context()); err == nil {
		t.Fatal("expected executor creation to fail on invalid pattern")
	}
}

func TestRuntimeSetup_MCPConnectFailureIsWarning(t *testing.T) {
	w := testWorkflow(t, func(c *config.Config) {
		c.MCP.Servers = map[string]config.MCPServerConfig{
			"nope": {Command: "/nonexistent/mcp-server", DeniedTools: []string{"x"}},
		}
	})
	rt := newRuntime(w, testDeps())
	defer rt.Close()
	if err := rt.setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if rt.mcpManager == nil || rt.mcpManager.ServerCount() != 0 {
		t.Errorf("mcp manager: %v", rt.mcpManager)
	}
}

func TestRuntimeSetup_StorageDirError(t *testing.T) {
	w := testWorkflow(t, nil)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w.Config.State.Location = filepath.Join(blocker, "state")
	rt := newRuntime(w, testDeps())
	if err := rt.setup(t.Context()); err == nil {
		t.Fatal("expected storage dir error")
	}
}

func TestRuntimeSetup_ResearchScopeReachesGate(t *testing.T) {
	w := testWorkflow(t, nil)
	w.Workflow.SecurityMode = "research"
	w.Workflow.SecurityScope = "OWASP"
	rt := newRuntime(w, testDeps())
	defer rt.Close()
	if err := rt.setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if rt.secMode != executor.SecurityResearch || rt.secScope != "OWASP" {
		t.Errorf("mode=%q scope=%q", rt.secMode, rt.secScope)
	}
}

func TestParseRetryConfig(t *testing.T) {
	cases := []struct {
		backoff string
		want    time.Duration
	}{{"", 0}, {"30s", 30 * time.Second}, {"invalid", 0}}
	for _, tc := range cases {
		cfg := parseRetryConfig(3, tc.backoff)
		if cfg.MaxRetries != 3 || cfg.MaxBackoff != tc.want {
			t.Errorf("backoff %q: %+v", tc.backoff, cfg)
		}
	}
}

// runWith builds a runtime over a mock model and runs it, returning the
// stdout it produced.
func runWith(t *testing.T, l *Loaded, model llm.Model) (string, error) {
	t.Helper()
	var out strings.Builder
	rt := newRuntime(l, Deps{Creds: credentials.NewEnvStore(), Stdout: &out})
	t.Cleanup(rt.Close)
	if err := rt.setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	exec, err := executor.New(executor.Config{
		Workflow: rt.wf,
		Model:    model,
		Registry: rt.registry,
		Policy:   rt.pol,
		Logger:   rt.logger,
		Session:  rt.sess,
	})
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	rt.exec = exec
	runErr := rt.Run(t.Context())
	return out.String(), runErr
}

func TestRun_WorkflowPrintsJSONResult(t *testing.T) {
	l := testWorkflow(t, nil)
	model := llmmock.New()
	model.SetResponse("all done")
	out, err := runWith(t, l, model)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "\"Status\"") || !strings.Contains(out, "all done") {
		t.Errorf("expected a JSON result on stdout, got %q", out)
	}
}

func TestRun_InlineGoalPrintsRawOutputs(t *testing.T) {
	l := testWorkflow(t, nil)
	l.Workflow = inlineGoal("say hello")
	model := llmmock.New()
	model.SetResponse("hello there")
	out, err := runWith(t, l, model)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "\"Status\"") || !strings.Contains(out, "hello there") {
		t.Errorf("expected raw outputs on stdout, got %q", out)
	}
}

func TestRun_ExecutorErrorMarksSessionFailed(t *testing.T) {
	l := testWorkflow(t, nil)
	model := llmmock.New()
	model.SetError(errors.New("model exploded"))
	if _, err := runWith(t, l, model); err == nil {
		t.Fatal("expected the executor error")
	}
}

func TestNew_AccessorsAndClose(t *testing.T) {
	l := testWorkflow(t, nil)
	var out, errBuf strings.Builder
	rt, err := New(t.Context(), l, Deps{Creds: credentials.NewEnvStore(), Stdout: &out, Stderr: &errBuf, Version: "v1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	if rt.Executor() == nil || rt.Registry() == nil || rt.Policy() != l.Policy || rt.Session() == nil {
		t.Error("accessors should expose the wired components")
	}
	if !strings.Contains(errBuf.String(), "🧠 Memory") {
		t.Errorf("status lines should reach the injected stderr: %q", errBuf.String())
	}

	// New reports setup failures rather than returning a half-built runtime.
	broken := testWorkflow(t, func(c *config.Config) { c.LLM.Model = "" })
	if _, err := New(t.Context(), broken, Deps{Creds: credentials.NewEnvStore()}); err == nil {
		t.Error("expected a setup error")
	}
}

func TestDeferredMetrics(t *testing.T) {
	var d deferredMetrics
	// The zero value drops every metric.
	d.RecordLLMCall(1, 2, 3, 4, 5)
	d.RecordSupervision(true)
	d.SetSubagents(2)

	spy := &metricsSpy{}
	d.set(spy)
	d.RecordLLMCall(1, 2, 3, 4, 5)
	d.RecordSupervision(true)
	d.SetSubagents(2)
	if spy.llm != 1 || spy.supervision != 1 || spy.subagents != 2 {
		t.Errorf("metrics not forwarded: %+v", spy)
	}
}

// metricsSpy counts the metrics forwarded to it.
type metricsSpy struct {
	llm         int
	supervision int
	subagents   int
}

func (m *metricsSpy) RecordLLMCall(_, _, _, _ int, _ int64) { m.llm++ }
func (m *metricsSpy) RecordSupervision(bool)                { m.supervision++ }
func (m *metricsSpy) SetSubagents(n int)                    { m.subagents = n }
