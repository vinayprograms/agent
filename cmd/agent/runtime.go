// Runtime execution: setup reads as a checklist (models, memory, security
// mode, tool set, telemetry, executor), then run dispatches the workflow.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agent/internal/telemetry"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/shellguard"
	"github.com/vinayprograms/agentkit/tools"
)

// defaultMaxTokens is applied when a model profile leaves max_tokens unset;
// llm.New rejects a zero value.
const defaultMaxTokens = 4096

// runtime handles the execution phase of a workflow.
type runtime struct {
	wf           *agentfile.Workflow
	cfg          *config.Config
	pol          *policy.Policy
	creds        credentials.Lookup
	inputs       map[string]string
	debug        bool
	sessionLabel string // Override session directory name

	// Components
	logger     *slog.Logger
	provider   llm.Model
	smallLLM   llm.Model
	registry   *tools.Registry
	spawn      *tools.SpawnBinder
	bashGate   *shellguard.Gate
	exec       *executor.Executor
	mcpManager *mcp.Manager
	eventSink  session.Sink // optional; set before setup (serve mode)
	sessionMgr *session.Recorder
	sess       *session.Session

	// Security (computed before the tool set so the bash gate gets the scope)
	secMode  executor.SecurityMode
	secScope string
	secTrust string

	// Storage
	storagePath string
	sessionPath string
	bleveStore  *memory.BleveStore
	scratchpad  *memory.InMemoryStore

	// Cleanup
	closers []func()
}

// newRuntime creates a runtime from loaded workflow configuration.
func newRuntime(w *workflow, creds credentials.Lookup) *runtime {
	rt := &runtime{
		wf:           w.wf,
		cfg:          w.cfg,
		pol:          w.pol,
		creds:        creds,
		inputs:       w.inputs,
		debug:        w.debug,
		sessionLabel: w.sessionLabel,
	}
	rt.logger = newLogger(w.debug)
	rt.resolveStoragePath()
	return rt
}

// newLogger returns the process logger: text to stderr, Debug level when
// --debug is set.
func newLogger(debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// resolveStoragePath sets up storage and session paths.
func (rt *runtime) resolveStoragePath() {
	rt.storagePath = rt.cfg.State.Location
	if rt.storagePath == "" {
		home, _ := os.UserHomeDir()
		rt.storagePath = config.DefaultStateDir(home)
	}
	if len(rt.storagePath) > 0 && rt.storagePath[0] == '~' {
		home, _ := os.UserHomeDir()
		rt.storagePath = filepath.Join(home, rt.storagePath[1:])
	}
	// Use sessionLabel if provided (swarm passes agent name), otherwise workflow name
	sessDir := rt.wf.Name
	if rt.sessionLabel != "" {
		sessDir = rt.sessionLabel
	}
	rt.sessionPath = filepath.Join(rt.storagePath, "sessions", sessDir)
}

// setup initializes all runtime components. Returns error on failure.
func (rt *runtime) setup() error {
	if err := os.MkdirAll(rt.storagePath, 0755); err != nil {
		return fmt.Errorf("creating storage directory: %w", err)
	}

	if err := rt.createProvider(); err != nil {
		return err
	}
	if err := rt.createSmallLLM(); err != nil {
		return err
	}
	if err := rt.setupMemory(); err != nil {
		return err
	}
	rt.secMode, rt.secScope, rt.secTrust = rt.determineSecurityConfig()
	if err := rt.setupRegistry(); err != nil {
		return err
	}
	if err := rt.setupTelemetry(); err != nil {
		return err
	}
	if err := rt.createExecutor(); err != nil {
		return err
	}
	rt.setupCallbacks()
	return nil
}

// newModel builds an llm.Model from a config profile. The service is
// inferred from the model name when unset, credentials are resolved through
// the credential lookup, and max_tokens falls back to the default model's
// value and then to defaultMaxTokens.
func (rt *runtime) newModel(p config.LLMConfig, retry llm.RetryConfig) (llm.Model, error) {
	if p.Model == "" {
		return nil, fmt.Errorf("LLM model not configured")
	}
	service := p.Provider
	if service == "" {
		service = llm.InferService(p.Model)
	}
	key, isOAuth := credentials.Resolve(rt.creds, service)
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = rt.cfg.LLM.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	return llm.New(llm.Config{
		Service:      service,
		Model:        p.Model,
		APIKey:       string(key),
		IsOAuthToken: isOAuth,
		MaxTokens:    maxTokens,
		BaseURL:      p.BaseURL,
		Thinking:     llm.ThinkingConfig{Level: llm.ThinkingLevel(p.Thinking)},
		Retry:        retry,
	})
}

// createProvider creates the main LLM model.
func (rt *runtime) createProvider() error {
	m, err := rt.newModel(rt.cfg.LLM, parseRetryConfig(rt.cfg.LLM.MaxRetries, rt.cfg.LLM.RetryBackoff))
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}
	rt.provider = m
	return nil
}

// createSmallLLM creates the small LLM for summarization and triage.
// Returns error if small_llm is configured but fails to create.
func (rt *runtime) createSmallLLM() error {
	if rt.cfg.SmallLLM.Model == "" {
		// Not configured - this is fine, proceed without it
		return nil
	}
	m, err := rt.newModel(rt.cfg.SmallLLM, llm.RetryConfig{})
	if err != nil {
		return fmt.Errorf("failed to create small_llm (model=%s): %w", rt.cfg.SmallLLM.Model, err)
	}
	rt.smallLLM = m
	fmt.Fprintf(os.Stderr, "✓ Small LLM: %s (for summarization and security triage)\n", rt.cfg.SmallLLM.Model)
	return nil
}

// setupMemory configures scratchpad and semantic memory.
// Design:
//   - Scratchpad: always ephemeral (session-scoped, agent-decided working memory)
//   - BM25 memory: always persistent (cross-session, "remember"/"recall" implies persistence)
func (rt *runtime) setupMemory() error {
	rt.scratchpad = memory.NewInMemoryStore()

	var err error
	rt.bleveStore, err = memory.NewBleveStore(memory.BleveStoreConfig{
		BasePath: rt.storagePath,
	})
	if err != nil {
		return fmt.Errorf("creating semantic memory store: %w", err)
	}
	rt.addCloser(func() { rt.bleveStore.Close() })

	fmt.Println("🧠 Memory: scratchpad (session) + BM25 (persistent)")
	return nil
}

// setupRegistry builds the tool set from the policy, attaching the bash
// gate (shellguard) when the policy enables bash.
func (rt *runtime) setupRegistry() error {
	workspace := rt.cfg.Agent.Workspace

	if rt.pol.IsToolEnabled("bash") {
		fmt.Println("⚠️  bash enabled by policy")
		var denied []string
		if tp := rt.pol.GetToolPolicy("bash"); tp != nil {
			denied = tp.Deny
		}
		rt.bashGate = shellguard.New(shellguard.Bash(), workspace, rt.pol.AllowedDirs, denied, rt.smallLLM, rt.secScope)
	}

	var summarizer tools.Summarizer
	if rt.smallLLM != nil {
		summarizer = tools.NewSummarizer(rt.smallLLM)
	}

	rt.spawn = tools.NewSpawnBinder()
	reg, err := buildToolset(toolsetConfig{
		Policy:      rt.pol,
		Workspace:   workspace,
		Creds:       rt.creds,
		Summarizer:  summarizer,
		HTTPTimeout: rt.httpTimeout(),
		Scratchpad:  rt.scratchpad,
		Memory:      rt.bleveStore,
		BashGate:    rt.bashGate,
		Spawn:       rt.spawn,
	})
	if err != nil {
		return err
	}
	rt.registry = reg
	return nil
}

// httpTimeout is the HTTP client timeout for web tools: the largest of the
// configured network timeouts. Zero means the kit default.
func (rt *runtime) httpTimeout() time.Duration {
	secs := max(rt.cfg.Timeouts.MCP, rt.cfg.Timeouts.WebSearch, rt.cfg.Timeouts.WebFetch)
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// setupTelemetry installs the OpenTelemetry tracer provider when telemetry
// is enabled with an endpoint. Failure is a warning: tracing is optional.
func (rt *runtime) setupTelemetry() error {
	if !rt.cfg.Telemetry.Enabled || rt.cfg.Telemetry.Endpoint == "" {
		return nil
	}
	protocol := rt.cfg.Telemetry.Protocol
	if protocol == "" {
		protocol = config.ProtocolGRPC
	}
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName:    "agent",
		ServiceVersion: version,
		Endpoint:       rt.cfg.Telemetry.Endpoint,
		Protocol:       string(protocol),
		Insecure:       rt.cfg.Telemetry.Insecure,
		Headers:        rt.cfg.Telemetry.Headers,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to initialize OpenTelemetry: %v\n", err)
		return nil
	}
	rt.addCloser(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			rt.logger.Warn("telemetry shutdown", "err", err)
		}
	})
	return nil
}

// connectMCP connects every configured MCP server (stdio transport) and
// applies the per-server denied tool lists. A server that fails to connect
// is skipped with a warning.
func (rt *runtime) connectMCP() *mcp.Manager {
	if len(rt.cfg.MCP.Servers) == 0 {
		return nil
	}
	mgr := mcp.NewManager()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for name, serverCfg := range rt.cfg.MCP.Servers {
		client, err := mcp.Stdio(ctx, mcp.ServerConfig{
			Command: serverCfg.Command,
			Args:    serverCfg.Args,
			Env:     serverCfg.Env,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to connect MCP server %q: %v\n", name, err)
			continue
		}
		if err := mgr.Register(name, client); err != nil {
			client.Close()
			fmt.Fprintf(os.Stderr, "warning: failed to register MCP server %q: %v\n", name, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "✓ Connected MCP server: %s\n", name)
		if len(serverCfg.DeniedTools) > 0 {
			mgr.Deny(name, serverCfg.DeniedTools)
			fmt.Fprintf(os.Stderr, "  └─ Denied %d tools\n", len(serverCfg.DeniedTools))
		}
	}
	rt.mcpManager = mgr
	rt.addCloser(func() { mgr.Close() })
	return mgr
}

// securityConfig builds the executor's content-trust configuration from the
// resolved mode, the triage model and the policy's [content.security] extras.
func (rt *runtime) securityConfig() *executor.SecurityConfig {
	sec := &executor.SecurityConfig{
		Mode:     rt.secMode,
		Scope:    rt.secScope,
		Screener: rt.createTriageProvider(),
		Reviewer: rt.provider,
	}
	if rt.pol.Content != nil && rt.pol.Content.Security != nil {
		sec.Patterns = rt.pol.Content.Security.Patterns
		sec.Keywords = rt.pol.Content.Security.Keywords
	}
	if rt.secMode == executor.SecurityResearch {
		fmt.Fprintf(os.Stderr, "🔓 Security: mode=research, scope=%q\n", rt.secScope)
	} else {
		fmt.Fprintf(os.Stderr, "🔒 Security: mode=%s, user_trust=%s\n", rt.secMode, rt.secTrust)
	}
	return sec
}

// createExecutor builds an executor.Config, wiring up MCP, session, security,
// supervision, and observations, then creates the executor in one shot.
func (rt *runtime) createExecutor() error {
	mcpMgr := rt.connectMCP()

	// --- Session ---
	var err error
	rt.sessionMgr, err = session.Open(rt.sessionPath, rt.eventSink)
	if err != nil {
		return err
	}
	rt.sess, err = rt.sessionMgr.Create(rt.wf.Name)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	// --- Supervision ---
	var checkpointStore checkpoint.CheckpointStore
	var supervisor supervision.Supervisor
	if rt.wf.HasSupervisedGoals() {
		checkpointDir := filepath.Join(rt.sessionPath, "checkpoints", rt.sess.ID)
		cs, csErr := checkpoint.NewStore(checkpointDir)
		if csErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to create checkpoint store: %v\n", csErr)
		} else {
			checkpointStore = cs
			supervisor = supervision.NewLLMSupervisor(supervision.Config{
				Model:  rt.provider,
				Logger: rt.logger,
			})
			fmt.Fprintf(os.Stderr, "👁 Supervision: enabled (four-phase execution)\n")
		}
	}

	// --- Observation extraction ---
	var obsExtractor executor.ObservationExtractor
	var obsStore executor.ObservationStore
	if rt.smallLLM != nil && rt.bleveStore != nil {
		obsExtractor = memory.NewExtractor(rt.smallLLM)
		obsStore = rt.bleveStore
		fmt.Fprintf(os.Stderr, "🔍 Observations: enabled (extracting insights after each step)\n")
	}

	// --- Workspace context ---
	var wsCtx string
	workspace := rt.cfg.Agent.Workspace
	if wc := executor.BuildWorkspaceContext(workspace); wc != "" {
		wsCtx = wc
		fmt.Fprintf(os.Stderr, "📂 Workspace context: %s\n", workspace)
	}

	// --- Build Config & create executor ---
	cfg := executor.Config{
		Workflow:             rt.wf,
		Model:                rt.provider,
		Resolver:             &profileResolver{rt: rt, fallback: rt.provider},
		Registry:             rt.registry,
		Policy:               rt.pol,
		SpawnBinder:          rt.spawn,
		Logger:               rt.logger,
		Debug:                rt.debug,
		MCPManager:           mcpMgr,
		Session:              rt.sess,
		Security:             rt.securityConfig(),
		TimeoutMCP:           rt.cfg.Timeouts.MCP,
		TimeoutWebSearch:     rt.cfg.Timeouts.WebSearch,
		TimeoutWebFetch:      rt.cfg.Timeouts.WebFetch,
		CheckpointStore:      checkpointStore,
		Supervisor:           supervisor,
		ObservationExtractor: obsExtractor,
		ObservationStore:     obsStore,
		WorkspaceContext:     wsCtx,
	}
	rt.exec, err = executor.New(cfg)
	if err != nil {
		return fmt.Errorf("creating executor: %w", err)
	}

	// Bash security decisions go to the session log (needs exec reference)
	if rt.bashGate != nil {
		rt.bashGate.OnDecision = rt.exec.LogBashSecurity
	}
	return nil
}

// profileResolver creates models based on capability profiles.
type profileResolver struct {
	mu       sync.Mutex
	rt       *runtime
	fallback llm.Model
	cache    map[string]llm.Model
}

// Model returns the model for the given profile name. Profiles without a
// model of their own resolve to the fallback. Safe for concurrent use by
// multiple goroutines (e.g. sub-agents).
func (f *profileResolver) Model(profile string) (llm.Model, error) {
	if profile == "" {
		return f.fallback, nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if cached, ok := f.cache[profile]; ok {
		return cached, nil
	}

	profileCfg := f.rt.cfg.Profile(profile)
	if profileCfg.Model == "" {
		return f.fallback, nil
	}

	m, err := f.rt.newModel(profileCfg, llm.RetryConfig{})
	if err != nil {
		return nil, fmt.Errorf("creating provider for profile %q: %w", profile, err)
	}

	if f.cache == nil {
		f.cache = make(map[string]llm.Model)
	}
	f.cache[profile] = m
	return m, nil
}

// determineSecurityConfig resolves the security mode, research scope and
// the user trust label (trust is reported at startup only).
func (rt *runtime) determineSecurityConfig() (executor.SecurityMode, string, string) {
	mode := executor.SecurityDefault
	var scope string
	if rt.cfg.Security.Mode == "paranoid" || rt.wf.SecurityMode == "paranoid" {
		mode = executor.SecurityParanoid
	} else if rt.wf.SecurityMode == "research" {
		mode = executor.SecurityResearch
		scope = rt.wf.SecurityScope
	}

	trust := "untrusted"
	switch rt.cfg.Security.UserTrust {
	case "trusted", "vetted":
		trust = rt.cfg.Security.UserTrust
	}
	return mode, scope, trust
}

// createTriageProvider creates the model for security triage: the configured
// triage profile, else the small LLM (may be nil).
func (rt *runtime) createTriageProvider() llm.Model {
	if rt.cfg.Security.TriageLLM != "" {
		m, err := rt.newModel(rt.cfg.Profile(rt.cfg.Security.TriageLLM), llm.RetryConfig{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: triage_llm %q unavailable, using small_llm: %v\n", rt.cfg.Security.TriageLLM, err)
			return rt.smallLLM
		}
		return m
	}
	return rt.smallLLM
}

// setupCallbacks wires progress output to executor hooks.
func (rt *runtime) setupCallbacks() {
	rt.exec.Hooks().On(hooks.SubAgentStart, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "  ⊕ Spawning sub-agent: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.SubAgentComplete, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "  ⊖ Sub-agent complete: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.GoalStart, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "▶ Starting goal: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.GoalComplete, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "✓ Completed goal: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.ToolCall, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"]
		agentRole, _ := evt.Data["agent_role"].(string)
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  → [%s] Tool: %s\n", agentRole, name)
		} else {
			fmt.Fprintf(os.Stderr, "  → Tool: %s\n", name)
		}
	})
	rt.exec.Hooks().On(hooks.ToolError, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"]
		err := evt.Data["error"]
		agentRole, _ := evt.Data["agent_role"].(string)
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  ✗ [%s] Tool error [%s]: %v\n", agentRole, name, err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✗ Tool error [%s]: %v\n", name, err)
		}
	})
	rt.exec.Hooks().On(hooks.MCPToolCall, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "  → MCP Tool: %s/%s\n", evt.Data["server"], evt.Data["tool"])
	})
	rt.exec.Hooks().On(hooks.SkillLoaded, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "  → Skill loaded: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.SupervisionEvent, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "  ⊙ Supervision [%s]: %s\n", evt.Data["step_id"], evt.Data["phase"])
	})
	rt.exec.Hooks().On(hooks.LLMError, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(os.Stderr, "  ✗ LLM error: %v\n", evt.Data["error"])
	})
}

// run executes the workflow and returns exit code.
func (rt *runtime) run(ctx context.Context) int {
	fmt.Fprintf(os.Stderr, "Running workflow: %s (session: %s)\n\n", rt.wf.Name, rt.sess.ID)

	result, err := rt.exec.Run(ctx, rt.inputs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		rt.sess.Status = "failed"
		rt.sess.Error = err.Error()
		rt.sessionMgr.Update(rt.sess)
		return 1
	}

	rt.sess.Status = string(result.Status)
	rt.sess.Outputs = result.Outputs
	rt.sessionMgr.Update(rt.sess)

	// Report convergence failures if any
	if failures := rt.exec.GetConvergenceFailures(); len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "\n⚠ Convergence warnings:\n")
		for goal, iterations := range failures {
			fmt.Fprintf(os.Stderr, "  • Goal %q did not converge (used all %d iterations)\n", goal, iterations)
		}
	}

	fmt.Fprintf(os.Stderr, "\n✓ Workflow complete\n\n")

	// For inline goals, print the output directly in a user-friendly format
	if rt.wf.Name == "inline-goal" && len(result.Outputs) > 0 {
		for _, output := range result.Outputs {
			fmt.Println(output)
		}
	} else {
		// For regular workflows, print full JSON result
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	}
	return 0
}

// cleanup runs all registered cleanup functions.
func (rt *runtime) cleanup() {
	for i := len(rt.closers) - 1; i >= 0; i-- {
		rt.closers[i]()
	}
}

// addCloser registers a cleanup function.
func (rt *runtime) addCloser(fn func()) {
	rt.closers = append(rt.closers, fn)
}
