// Runtime execution: setup reads as a checklist (models, memory, security
// mode, tool set, telemetry, executor), then Run dispatches the workflow.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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

// Deps are the process-level collaborators the runtime needs but does not
// own: credentials, the streams it reports on, and the optional session
// event sink.
type Deps struct {
	Creds   credentials.Lookup
	Stdout  io.Writer    // workflow result; nil discards
	Stderr  io.Writer    // status lines and warnings; nil discards
	Version string       // reported to telemetry as the service version
	Sink    session.Sink // optional: session events (serve mode streams them to the bus)
	// Metrics receives LLM and supervision metrics. Optional; serve mode
	// supplies a forwarder because its heartbeat sender only exists once
	// the bus is up.
	Metrics executor.MetricsCollector
	// KeepSession leaves the session open across runs (serve mode): Run
	// flushes but does not close it.
	KeepSession bool
	// StepGate pauses the run after each goal; see
	// [executor.RunOptions.StepGate]. nil (serve mode always) disables it.
	StepGate func(ctx context.Context, step, goal string) (bool, error)
}

// Runtime executes a loaded workflow. New returns one ready to Run; Close
// releases everything it opened.
type Runtime struct {
	wf            *agentfile.Workflow
	cfg           *config.Config
	pol           *policy.Policy
	creds         credentials.Lookup
	inputs        map[string]string
	debug         bool
	agentfilePath string // absolute Agentfile path; empty for an inline goal
	sessionLabel  string // deployment label recorded in the session header
	home          string
	stdout        io.Writer
	stderr        io.Writer
	version       string

	// Components
	logger            *slog.Logger
	provider          llm.Model
	smallLLM          llm.Model
	registry          *tools.Registry
	spawn             *tools.SpawnBinder
	bashGate          *shellguard.Gate
	exec              *executor.Executor
	mcpManager        *mcp.Manager
	eventSink         session.Sink // optional; set before setup (serve mode)
	persistentSession bool
	stepGate          func(ctx context.Context, step, goal string) (bool, error)
	metrics           executor.MetricsCollector
	sessionMgr        *session.Recorder
	sess              *session.Session

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

// New wires every runtime component for the loaded workflow and returns a
// runtime ready to Run. The caller must Close it.
func New(ctx context.Context, l *Loaded, deps Deps) (*Runtime, error) {
	rt := newRuntime(l, deps)
	if err := rt.setup(ctx); err != nil {
		rt.Close()
		return nil, err
	}
	return rt, nil
}

// newRuntime builds the runtime shell — identity, paths and streams — with
// no component wired yet.
func newRuntime(l *Loaded, deps Deps) *Runtime {
	rt := &Runtime{
		wf:                l.Workflow,
		cfg:               l.Config,
		pol:               l.Policy,
		creds:             deps.Creds,
		metrics:           deps.Metrics,
		inputs:            l.Inputs,
		debug:             l.Debug,
		agentfilePath:     l.Agentfile,
		sessionLabel:      l.SessionLabel,
		home:              l.home,
		stdout:            orDiscard(deps.Stdout),
		stderr:            orDiscard(deps.Stderr),
		version:           deps.Version,
		eventSink:         deps.Sink,
		persistentSession: deps.KeepSession,
		stepGate:          deps.StepGate,
	}
	rt.logger = newLogger(l.Debug, rt.stderr)
	rt.resolveStoragePath()
	return rt
}

// orDiscard substitutes io.Discard for a nil writer.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// Executor is the executor the workflow runs on. Serve mode registers
// extra tools and runs tasks through it directly.
func (rt *Runtime) Executor() *executor.Executor { return rt.exec }

// Registry is the tool set the executor advertises.
func (rt *Runtime) Registry() *tools.Registry { return rt.registry }

// Policy is the loaded policy, which serve mode extends for the tools it
// registers itself.
func (rt *Runtime) Policy() *policy.Policy { return rt.pol }

// Session is the record of this run; its ID identifies the agent.
func (rt *Runtime) Session() *session.Session { return rt.sess }

// newLogger returns the process logger: text to stderr, Debug level when
// --debug is set.
func newLogger(debug bool, w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// resolveStoragePath sets up storage and session paths.
func (rt *Runtime) resolveStoragePath() {
	rt.storagePath = rt.cfg.StateDir(rt.home)
	rt.sessionPath = filepath.Join(rt.storagePath, "sessions")
}

// setup initializes all runtime components. Returns error on failure.
func (rt *Runtime) setup(ctx context.Context) error {
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
	if err := rt.setupTelemetry(ctx); err != nil {
		return err
	}
	if err := rt.createExecutor(ctx); err != nil {
		return err
	}
	rt.setupCallbacks()
	return nil
}

// newModel builds an llm.Model from a config profile. The service is
// inferred from the model name when unset, credentials are resolved through
// the credential lookup, and max_tokens falls back to the default model's
// value and then to defaultMaxTokens.
func (rt *Runtime) newModel(p config.LLMConfig, retry llm.RetryConfig) (llm.Model, error) {
	if p.Model == "" {
		return nil, errors.New("LLM model not configured")
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
func (rt *Runtime) createProvider() error {
	m, err := rt.newModel(rt.cfg.LLM, parseRetryConfig(rt.cfg.LLM.MaxRetries, rt.cfg.LLM.RetryBackoff))
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}
	rt.provider = m
	return nil
}

// createSmallLLM creates the small LLM for summarization and triage.
// Returns error if small_llm is configured but fails to create.
func (rt *Runtime) createSmallLLM() error {
	if rt.cfg.SmallLLM.Model == "" {
		// Not configured - this is fine, proceed without it
		return nil
	}
	m, err := rt.newModel(rt.cfg.SmallLLM, llm.RetryConfig{})
	if err != nil {
		return fmt.Errorf("creating small_llm (model=%s): %w", rt.cfg.SmallLLM.Model, err)
	}
	rt.smallLLM = m
	fmt.Fprintf(rt.stderr, "✓ Small LLM: %s (for summarization and security triage)\n", rt.cfg.SmallLLM.Model)
	return nil
}

// setupMemory configures scratchpad and semantic memory.
// Design:
//   - Scratchpad: always ephemeral (session-scoped, agent-decided working memory)
//   - BM25 memory: always persistent (cross-session, "remember"/"recall" implies persistence)
func (rt *Runtime) setupMemory() error {
	rt.scratchpad = memory.NewInMemoryStore()

	var err error
	rt.bleveStore, err = memory.NewBleveStore(memory.BleveStoreConfig{
		BasePath: rt.storagePath,
	})
	if err != nil {
		return fmt.Errorf("creating semantic memory store: %w", err)
	}
	rt.addCloser(func() { rt.bleveStore.Close() })

	fmt.Fprintln(rt.stderr, "🧠 Memory: scratchpad (session) + BM25 (persistent)")
	return nil
}

// setupRegistry builds the tool set from the policy, attaching the bash
// gate (shellguard) when the policy enables bash.
func (rt *Runtime) setupRegistry() error {
	workspace := rt.cfg.Agent.Workspace

	if rt.pol.IsToolEnabled("bash") {
		fmt.Fprintln(rt.stderr, "⚠️  bash enabled by policy")
		var denied []string
		if tp := rt.pol.GetToolPolicy("bash"); tp != nil {
			denied = tp.Deny
		}
		// Tell shellguard which filesystem tools policy has disabled, so
		// bash can't be used as a side door around them: a disabled
		// read/write/edit tool means bash data reads / writes are denied
		// too, not just ones outside allowed_dirs.
		var disabledTools []string
		for _, name := range []string{"read", "write", "edit"} {
			if !rt.pol.IsToolEnabled(name) {
				disabledTools = append(disabledTools, name)
			}
		}
		rt.bashGate = shellguard.NewGate(shellguard.Config{
			Shell:              shellguard.Bash(),
			Workspace:          workspace,
			AllowedDirs:        rt.pol.AllowedDirs,
			UserDeniedCommands: denied,
			DisabledTools:      disabledTools,
			Model:              rt.smallLLM,
			SecurityScope:      rt.secScope,
		})
	}

	var summarizer tools.Summarizer
	if rt.smallLLM != nil {
		summarizer = tools.NewSummarizer(rt.smallLLM)
	}

	rt.spawn = tools.NewSpawnBinder()
	reg, err := buildToolset(rt.toolsetConfig(workspace, summarizer))
	if err != nil {
		return err
	}
	rt.registry = reg
	return nil
}

// toolsetConfig assembles buildToolset's input from the runtime's config
// and already-built components. Split out of setupRegistry so a test can
// construct one through the real Runtime wiring path and inspect it field
// by field (see TestToolsetConfig_WiredFromConfig).
func (rt *Runtime) toolsetConfig(workspace string, summarizer tools.Summarizer) toolsetConfig {
	return toolsetConfig{
		Policy:           rt.pol,
		Workspace:        workspace,
		Creds:            rt.creds,
		Summarizer:       summarizer,
		HTTPTimeout:      rt.httpTimeout(),
		Scratchpad:       rt.scratchpad,
		Memory:           rt.bleveStore,
		BashGate:         rt.bashGate,
		Spawn:            rt.spawn,
		SearchCooldownMS: rt.cfg.Timeouts.SearchCooldownMS,
		SearXNGURL:       rt.cfg.Web.SearXNGURL,
		SearchProvider:   rt.cfg.Web.SearchProvider,
		FetchMaxChars:    rt.cfg.Web.FetchMaxChars,
	}
}

// httpTimeout is the HTTP client timeout for web tools: the largest of the
// configured network timeouts. Zero means the kit default.
func (rt *Runtime) httpTimeout() time.Duration {
	secs := max(rt.cfg.Timeouts.MCP, rt.cfg.Timeouts.WebSearch, rt.cfg.Timeouts.WebFetch)
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// setupTelemetry installs the OpenTelemetry tracer provider when telemetry
// is enabled with an endpoint. Failure is a warning: tracing is optional.
func (rt *Runtime) setupTelemetry(ctx context.Context) error {
	if !rt.cfg.Telemetry.Enabled || rt.cfg.Telemetry.Endpoint == "" {
		return nil
	}
	protocol := rt.cfg.Telemetry.Protocol
	if protocol == "" {
		protocol = config.ProtocolGRPC
	}
	shutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:    "agent",
		ServiceVersion: rt.version,
		Endpoint:       rt.cfg.Telemetry.Endpoint,
		Protocol:       string(protocol),
		Insecure:       rt.cfg.Telemetry.Insecure,
		Headers:        rt.cfg.Telemetry.Headers,
	})
	if err != nil {
		fmt.Fprintf(rt.stderr, "WARN: failed to initialize OpenTelemetry: %v\n", err)
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
func (rt *Runtime) connectMCP(ctx context.Context) *mcp.Manager {
	if len(rt.cfg.MCP.Servers) == 0 {
		return nil
	}
	mgr := mcp.NewManager()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for name, serverCfg := range rt.cfg.MCP.Servers {
		client, err := mcp.Stdio(ctx, mcp.ServerConfig{
			Command: serverCfg.Command,
			Args:    serverCfg.Args,
			Env:     serverCfg.Env,
		})
		if err != nil {
			fmt.Fprintf(rt.stderr, "warning: failed to connect MCP server %q: %v\n", name, err)
			continue
		}
		if err := mgr.Register(name, client); err != nil {
			client.Close()
			fmt.Fprintf(rt.stderr, "warning: failed to register MCP server %q: %v\n", name, err)
			continue
		}
		fmt.Fprintf(rt.stderr, "✓ Connected MCP server: %s\n", name)
		if len(serverCfg.DeniedTools) > 0 {
			mgr.Deny(name, serverCfg.DeniedTools)
			fmt.Fprintf(rt.stderr, "  └─ Denied %d tools\n", len(serverCfg.DeniedTools))
		}
	}
	rt.mcpManager = mgr
	rt.addCloser(func() { mgr.Close() })
	return mgr
}

// securityConfig builds the executor's content-trust configuration from the
// resolved mode, the triage model and the policy's [content.security] extras.
func (rt *Runtime) securityConfig() *executor.SecurityConfig {
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
		fmt.Fprintf(rt.stderr, "🔓 Security: mode=research, scope=%q\n", rt.secScope)
	} else {
		fmt.Fprintf(rt.stderr, "🔒 Security: mode=%s, user_trust=%s\n", rt.secMode, rt.secTrust)
	}
	return sec
}

// unmetRequirements lists the capability profiles the Agentfile's agents
// REQUIRE that the configuration does not define, in first-mention order.
// Such a profile silently falls back to the default model, so the run is
// nothing like what the Agentfile asked for; the caller reports them.
func (rt *Runtime) unmetRequirements() []string {
	var missing []string
	for _, agent := range rt.wf.Agents {
		if agent.Requires == "" || slices.Contains(missing, agent.Requires) {
			continue
		}
		if rt.cfg.Profile(agent.Requires).Model == "" {
			missing = append(missing, agent.Requires)
		}
	}
	return missing
}

// createExecutor builds an executor.Config, wiring up MCP, session, security,
// supervision, and observations, then creates the executor in one shot.
func (rt *Runtime) createExecutor(ctx context.Context) error {
	for _, profile := range rt.unmetRequirements() {
		fmt.Fprintf(rt.stderr, "⚠️  Profile %q is not configured — using the default model\n", profile)
	}

	mcpMgr := rt.connectMCP(ctx)

	// --- Session ---
	var err error
	rt.sessionMgr, err = session.Open(rt.sessionPath, rt.eventSink)
	if err != nil {
		return err
	}
	rt.sess, err = rt.sessionMgr.Create(session.Meta{
		Name:      rt.wf.Name,
		Agentfile: rt.agentfilePath,
		Label:     rt.sessionLabel,
		Inputs:    rt.inputs,
	})
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	// --- Supervision ---
	var checkpointStore supervision.Store
	var supervisor supervision.Supervisor
	if rt.wf.HasSupervisedGoals() {
		checkpointDir := filepath.Join(rt.storagePath, "checkpoints", rt.sess.ID)
		cs, csErr := checkpoint.NewStore(checkpointDir)
		if csErr != nil {
			fmt.Fprintf(rt.stderr, "warning: failed to create checkpoint store: %v\n", csErr)
		} else {
			checkpointStore = cs
			supervisor = supervision.NewLLMSupervisor(supervision.Config{
				Model:  rt.provider,
				Logger: rt.logger,
			})
			fmt.Fprintf(rt.stderr, "👁 Supervision: enabled (four-phase execution)\n")
		}
	}

	// --- Observation extraction ---
	var obsExtractor executor.ObservationExtractor
	var obsStore executor.ObservationStore
	if rt.smallLLM != nil && rt.bleveStore != nil {
		obsExtractor = memory.NewExtractor(rt.smallLLM)
		obsStore = rt.bleveStore
		fmt.Fprintf(rt.stderr, "🔍 Observations: enabled (extracting insights after each step)\n")
	}

	// --- Workspace context ---
	var wsCtx string
	workspace := rt.cfg.Agent.Workspace
	if wc := executor.BuildWorkspaceContext(workspace); wc != "" {
		wsCtx = wc
		fmt.Fprintf(rt.stderr, "📂 Workspace context: %s\n", workspace)
	}

	// --- Build Config & create executor ---
	cfg := executor.Config{
		Workflow:    rt.wf,
		Model:       rt.provider,
		Resolver:    &profileResolver{rt: rt, fallback: rt.provider},
		Registry:    rt.registry,
		Policy:      rt.pol,
		SpawnBinder: rt.spawn,
		Logger:      rt.logger,
		Debug:       rt.debug,
		MCPManager:  mcpMgr,
		Session:     rt.sess,
		Security:    rt.securityConfig(),
		Budget: executor.Budget{
			MaxToolCalls: rt.cfg.Limits.MaxToolCalls,
			MaxTurns:     rt.cfg.Limits.MaxTurns,
			MaxDuration:  rt.cfg.Limits.Duration(),
		},
		TimeoutMCP:           rt.cfg.Timeouts.MCP,
		TimeoutWebSearch:     rt.cfg.Timeouts.WebSearch,
		TimeoutWebFetch:      rt.cfg.Timeouts.WebFetch,
		CheckpointStore:      checkpointStore,
		Supervisor:           supervisor,
		ObservationExtractor: obsExtractor,
		ObservationStore:     obsStore,
		WorkspaceContext:     wsCtx,
		PersistentSession:    rt.persistentSession,
		MetricsCollector:     rt.metrics,
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
	rt       *Runtime
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
func (rt *Runtime) determineSecurityConfig() (executor.SecurityMode, string, string) {
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
func (rt *Runtime) createTriageProvider() llm.Model {
	if rt.cfg.Security.TriageLLM != "" {
		m, err := rt.newModel(rt.cfg.Profile(rt.cfg.Security.TriageLLM), llm.RetryConfig{})
		if err != nil {
			fmt.Fprintf(rt.stderr, "warning: triage_llm %q unavailable, using small_llm: %v\n", rt.cfg.Security.TriageLLM, err)
			return rt.smallLLM
		}
		return m
	}
	return rt.smallLLM
}

// setupCallbacks wires progress output to executor hooks.
func (rt *Runtime) setupCallbacks() {
	rt.exec.Hooks().On(hooks.SubAgentStart, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "  ⊕ Spawning sub-agent: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.SubAgentComplete, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "  ⊖ Sub-agent complete: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.GoalStart, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "▶ Starting goal: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.GoalComplete, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "✓ Completed goal: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.ToolCall, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"]
		agentRole, _ := evt.Data["agent_role"].(string)
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(rt.stderr, "  → [%s] Tool: %s\n", agentRole, name)
		} else {
			fmt.Fprintf(rt.stderr, "  → Tool: %s\n", name)
		}
	})
	rt.exec.Hooks().On(hooks.ToolError, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"]
		err := evt.Data["error"]
		agentRole, _ := evt.Data["agent_role"].(string)
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(rt.stderr, "  ✗ [%s] Tool error [%s]: %v\n", agentRole, name, err)
		} else {
			fmt.Fprintf(rt.stderr, "  ✗ Tool error [%s]: %v\n", name, err)
		}
	})
	rt.exec.Hooks().On(hooks.MCPToolCall, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "  → MCP Tool: %s/%s\n", evt.Data["server"], evt.Data["tool"])
	})
	rt.exec.Hooks().On(hooks.SkillLoaded, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "  → Skill loaded: %s\n", evt.Data["name"])
	})
	rt.exec.Hooks().On(hooks.SupervisionEvent, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "  ⊙ Supervision [%s]: %s\n", evt.Data["step_id"], evt.Data["phase"])
	})
	rt.exec.Hooks().On(hooks.LLMError, func(_ context.Context, evt hooks.Event) {
		fmt.Fprintf(rt.stderr, "  ✗ LLM error: %v\n", evt.Data["error"])
	})
}

// Run executes the workflow, records the outcome in the session and writes
// the result: raw outputs for an inline goal, the JSON result otherwise.
func (rt *Runtime) Run(ctx context.Context) error {
	fmt.Fprintf(rt.stderr, "Running workflow: %s (session: %s)\n\n", rt.wf.Name, rt.sess.ID)

	result, err := rt.exec.Run(ctx, executor.RunOptions{Inputs: rt.inputs, StepGate: rt.stepGate})
	if err != nil {
		rt.sess.Status = session.StatusFailed
		if errors.Is(err, executor.ErrAborted) {
			rt.sess.Status = session.StatusAborted
		}
		rt.sess.Error = err.Error()
		rt.sessionMgr.Update(rt.sess)
		return err
	}

	rt.sess.Status = string(result.Status)
	rt.sess.Outputs = result.Outputs
	rt.sessionMgr.Update(rt.sess)

	// Report every goal that did not end ok, so a headless caller (and a
	// human watching stderr) can tell a budget-exhausted or empty-output
	// goal from a genuinely completed one without grepping the JSONL.
	for name, oc := range result.Goals {
		if oc.Outcome == executor.OutcomeOK {
			continue
		}
		retried := ""
		if oc.Retried {
			retried = " (after retry)"
		}
		fmt.Fprintf(rt.stderr, "⚠ Goal %s: %s%s — %s\n", name, oc.Outcome, retried, oc.Reason)
	}

	switch result.Status {
	case executor.StatusComplete:
		fmt.Fprintf(rt.stderr, "\n✓ Workflow complete\n\n")
	case executor.StatusPartial:
		fmt.Fprintf(rt.stderr, "\n⚠ Workflow partial — some goals did not complete\n\n")
	default:
		fmt.Fprintf(rt.stderr, "\n✗ Workflow failed — no goal completed\n\n")
	}

	// For inline goals, print the output directly in a user-friendly format
	if rt.wf.Name == inlineGoalName && len(result.Outputs) > 0 {
		for _, output := range result.Outputs {
			fmt.Fprintln(rt.stdout, output)
		}
	} else {
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(rt.stdout, string(output))
	}

	if result.Status != executor.StatusComplete {
		return &statusExitError{status: result.Status}
	}
	return nil
}

// statusExitError signals the run command's exit code for a run that
// finished (no hard execution error) but not every goal was ok: 2 for
// partial, 1 for failed. It carries no alarming message of its own — the
// per-goal "⚠ Goal ..." lines above already explained why.
type statusExitError struct{ status executor.Status }

func (e *statusExitError) Error() string {
	return fmt.Sprintf("workflow ended with status %q", e.status)
}

// ExitCode implements the interface main.go checks to pick a process exit
// code other than the default 1.
func (e *statusExitError) ExitCode() int {
	if e.status == executor.StatusPartial {
		return 2
	}
	return 1
}

// Close runs every registered cleanup function, most recent first.
func (rt *Runtime) Close() {
	for i := len(rt.closers) - 1; i >= 0; i-- {
		rt.closers[i]()
	}
}

// addCloser registers a cleanup function.
func (rt *Runtime) addCloser(fn func()) {
	rt.closers = append(rt.closers, fn)
}

// parseRetryConfig converts config values to an llm.RetryConfig. An
// unparseable backoff leaves the kit default in place.
func parseRetryConfig(maxRetries int, backoffStr string) llm.RetryConfig {
	cfg := llm.RetryConfig{MaxRetries: maxRetries}
	if backoffStr != "" {
		if d, err := time.ParseDuration(backoffStr); err == nil {
			cfg.MaxBackoff = d
		}
	}
	return cfg
}
