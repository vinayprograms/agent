// Runtime execution: setup reads as a checklist (provider, registry, memory,
// telemetry, executor), then run dispatches the workflow.
package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
	"github.com/vinayprograms/agentkit/telemetry"
	"github.com/vinayprograms/agentkit/tools"
)

// runtime handles the execution phase of a workflow.
type runtime struct {
	wf     *agentfile.Workflow
	cfg    *config.Config
	pol    *policy.Policy
	creds  *credentials.Credentials
	inputs map[string]string
	debug        bool
	sessionLabel string // Override session directory name

	// Components
	provider       llm.Provider
	smallLLM       llm.Provider
	registry       *tools.Registry
	bashLLMChecker *policy.SmallLLMChecker
	telem          telemetry.Exporter
	otelProvider   *telemetry.Provider // OpenTelemetry tracing provider
	exec           *executor.Executor
	mcpManager     *mcp.Manager
	sessionMgr     session.SessionManager
	sess           *session.Session
	secVerifier    *security.Verifier

	// Storage
	storagePath string
	sessionPath string
	bleveStore  *memory.BleveStore

	// Cleanup
	closers []func()
}

// newRuntime creates a runtime from loaded workflow configuration.
func newRuntime(w *workflow, creds *credentials.Credentials) *runtime {
	rt := &runtime{
		wf:           w.wf,
		cfg:          w.cfg,
		pol:          w.pol,
		creds:        creds,
		inputs:       w.inputs,
		debug:        w.debug,
		sessionLabel: w.sessionLabel,
	}
	rt.resolveStoragePath()
	return rt
}

// resolveStoragePath sets up storage and session paths.
func (rt *runtime) resolveStoragePath() {
	rt.storagePath = rt.cfg.State.Location
	if rt.storagePath == "" {
		home, _ := os.UserHomeDir()
		rt.storagePath = filepath.Join(home, ".local", "grid")
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
	rt.setupRegistry()
	if err := rt.setupMemory(); err != nil {
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

// createProvider creates the main LLM provider.
func (rt *runtime) createProvider() error {
	llmProvider := rt.cfg.LLM.Provider
	if llmProvider == "" {
		llmProvider = llm.InferProviderFromModel(rt.cfg.LLM.Model)
	}
	if llmProvider == "" && rt.cfg.LLM.Model == "" {
		return fmt.Errorf("LLM model not configured")
	}

	var err error
	cred := rt.creds.GetCredential(llmProvider)
	rt.provider, err = llm.NewProvider(llm.ProviderConfig{
		Provider:     llmProvider,
		Model:        rt.cfg.LLM.Model,
		APIKey:       cred.Key,
		IsOAuthToken: cred.IsOAuthToken,
		MaxTokens:    rt.cfg.LLM.MaxTokens,
		BaseURL:      rt.cfg.LLM.BaseURL,
		Thinking:     llm.ThinkingConfig{Level: llm.ThinkingLevel(rt.cfg.LLM.Thinking)},
		RetryConfig:  parseRetryConfig(rt.cfg.LLM.MaxRetries, rt.cfg.LLM.RetryBackoff),
	})
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}
	return nil
}

// createSmallLLM creates the small LLM for summarization and triage.
// Returns error if small_llm is configured but fails to create.
func (rt *runtime) createSmallLLM() error {
	if rt.cfg.SmallLLM.Model == "" {
		// Not configured - this is fine, proceed without it
		return nil
	}
	smallProvider := rt.cfg.SmallLLM.Provider
	if smallProvider == "" {
		smallProvider = llm.InferProviderFromModel(rt.cfg.SmallLLM.Model)
	}
	smallCred := rt.creds.GetCredential(smallProvider)
	var err error
	rt.smallLLM, err = llm.NewProvider(llm.ProviderConfig{
		Provider:     smallProvider,
		Model:        rt.cfg.SmallLLM.Model,
		APIKey:       smallCred.Key,
		IsOAuthToken: smallCred.IsOAuthToken,
		MaxTokens:    rt.cfg.SmallLLM.MaxTokens,
		BaseURL:      rt.cfg.SmallLLM.BaseURL,
	})
	if err != nil {
		return fmt.Errorf("failed to create small_llm (model=%s, provider=%s): %w", rt.cfg.SmallLLM.Model, smallProvider, err)
	}
	fmt.Fprintf(os.Stderr, "✓ Small LLM: %s via %s (for summarization and security triage)\n", rt.cfg.SmallLLM.Model, smallProvider)
	return nil
}

// setupRegistry creates and configures the tool registry.
func (rt *runtime) setupRegistry() {
	rt.registry = tools.NewRegistry(rt.pol)

	// Bash is controlled by policy — set up security if enabled
	if rt.pol.IsToolEnabled("bash") {
		fmt.Println("⚠️  bash enabled by policy")
		rt.setupBashChecker()
		if rt.smallLLM != nil {
			rt.bashLLMChecker = policy.NewSmallLLMChecker(policy.LLMProviderFromChatProvider(rt.smallLLM))
			rt.registry.SetBashLLMChecker(rt.bashLLMChecker)
		}
	}

	if rt.smallLLM != nil {
		rt.registry.SetSummarizer(llm.NewSummarizer(rt.smallLLM))
	}
	rt.registry.SetCredentials(rt.creds)
}

// setupBashChecker configures bash security with fail-close defaults.
func (rt *runtime) setupBashChecker() {
	bashPolicy := rt.pol.GetToolPolicy("bash")
	bashChecker := policy.NewBashChecker(rt.pol, bashPolicy.Denylist)
	rt.registry.SetBashChecker(bashChecker)
}

// setupMemory configures scratchpad and semantic memory.
// Design:
//   - Scratchpad: always ephemeral (session-scoped, agent-decided working memory)
//   - BM25 memory: always persistent (cross-session, "remember"/"recall" implies persistence)
func (rt *runtime) setupMemory() error {
	// Scratchpad: ephemeral (in-memory only, cleared each run)
	kvStore := tools.NewInMemoryStore()
	rt.registry.SetScratchpad(kvStore, false)

	// BM25 semantic memory: always persistent
	var err error
	rt.bleveStore, err = memory.NewBleveStore(memory.BleveStoreConfig{
		BasePath: rt.storagePath,
	})
	if err != nil {
		return fmt.Errorf("creating semantic memory store: %w", err)
	}
	rt.addCloser(func() { rt.bleveStore.Close() })
	semanticMemory := memory.NewToolsAdapter(rt.bleveStore)
	rt.registry.SetSemanticMemory(semanticMemory)

	fmt.Println("🧠 Memory: scratchpad (session) + BM25 (persistent)")
	return nil
}

// setupTelemetry creates the telemetry exporter and OTel provider.
func (rt *runtime) setupTelemetry() error {
	var err error

	// Legacy exporter (for backwards compatibility)
	if rt.cfg.Telemetry.Enabled && rt.cfg.Telemetry.Protocol != "grpc" && rt.cfg.Telemetry.Protocol != "http" {
		// Old-style protocol (file, http endpoint, etc.)
		rt.telem, err = telemetry.NewExporter(rt.cfg.Telemetry.Protocol, rt.cfg.Telemetry.Endpoint)
		if err != nil {
			return fmt.Errorf("creating telemetry exporter: %w", err)
		}
	} else {
		rt.telem = telemetry.NewNoopExporter()
	}
	rt.addCloser(func() { rt.telem.Close() })

	// OpenTelemetry tracing provider
	if rt.cfg.Telemetry.Enabled && rt.cfg.Telemetry.Endpoint != "" {
		protocol := rt.cfg.Telemetry.Protocol
		if protocol == "" {
			protocol = "grpc"
		}

		ctx := context.Background()
		rt.otelProvider, err = telemetry.InitProvider(ctx, telemetry.ProviderConfig{
			ServiceName:    "agent",
			ServiceVersion: version,
			Endpoint:       rt.cfg.Telemetry.Endpoint,
			Protocol:       protocol,
			Insecure:       rt.cfg.Telemetry.Insecure,
			Headers:        rt.cfg.Telemetry.Headers,
			Debug:          rt.debug,
		})
		if err != nil {
			// Log warning but don't fail - tracing is optional
			fmt.Fprintf(os.Stderr, "WARN: failed to initialize OpenTelemetry: %v\n", err)
		} else {
			rt.addCloser(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				rt.otelProvider.Shutdown(ctx)
			})
		}
	}

	return nil
}

// createExecutor builds an executor.Config, wiring up MCP, session, security,
// supervision, and observations, then creates the executor in one shot.
func (rt *runtime) createExecutor() error {
	// --- MCP ---
	var mcpMgr *mcp.Manager
	if len(rt.cfg.MCP.Servers) > 0 {
		mcpMgr = mcp.NewManager()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for name, serverCfg := range rt.cfg.MCP.Servers {
			err := mcpMgr.Connect(ctx, name, mcp.ServerConfig{
				Command: serverCfg.Command,
				Args:    serverCfg.Args,
				Env:     serverCfg.Env,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to connect MCP server %q: %v\n", name, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "✓ Connected MCP server: %s\n", name)
			if len(serverCfg.DeniedTools) > 0 {
				mcpMgr.SetDeniedTools(name, serverCfg.DeniedTools)
				fmt.Fprintf(os.Stderr, "  └─ Denied %d tools\n", len(serverCfg.DeniedTools))
			}
		}
		rt.mcpManager = mcpMgr
		rt.addCloser(func() { mcpMgr.Close() })
	}

	// --- Session ---
	rt.sessionMgr = session.NewFileManager(rt.sessionPath)
	var err error
	rt.sess, err = rt.sessionMgr.Create(rt.wf.Name)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	// --- Security ---
	var secVerifier *security.Verifier
	var secResearchScope string
	mode, scope, userTrust := rt.determineSecurityConfig()
	triageProvider := rt.createTriageProvider()

	verifier, verErr := security.NewVerifier(security.Config{
		Mode:               mode,
		ResearchScope:      scope,
		UserTrust:          userTrust,
		TriageProvider:     triageProvider,
		SupervisorProvider: rt.provider,
	}, rt.sess.ID)
	if verErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create security verifier: %v\n", verErr)
	} else {
		secVerifier = verifier
		rt.secVerifier = verifier
		rt.addCloser(func() { verifier.Destroy() })

		if mode == security.ModeResearch {
			fmt.Fprintf(os.Stderr, "🔓 Security: mode=research, scope=%q\n", scope)
			secResearchScope = scope
			if rt.bashLLMChecker != nil {
				rt.bashLLMChecker.SetSecurityScope(scope)
			}
		} else {
			fmt.Fprintf(os.Stderr, "🔒 Security: mode=%s, user_trust=%s\n", mode, userTrust)
		}
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
				Provider: rt.provider,
			})
			fmt.Fprintf(os.Stderr, "👁 Supervision: enabled (four-phase execution)\n")
		}
	}

	// --- Observation extraction ---
	var obsExtractor executor.ObservationExtractor
	var obsStore executor.ObservationStore
	if rt.smallLLM != nil && rt.bleveStore != nil {
		obsExtractor = memory.NewObservationExtractor(rt.smallLLM)
		obsStore = memory.NewBleveObservationStore(rt.bleveStore)
		fmt.Fprintf(os.Stderr, "🔍 Observations: enabled (extracting insights after each step)\n")
	}

	// --- Workspace context ---
	var wsCtx string
	workspace := rt.cfg.Agent.Workspace
	if wc := executor.BuildWorkspaceContext(workspace); wc != "" {
		wsCtx = wc
		fmt.Fprintf(os.Stderr, "📂 Workspace context: %s\n", workspace)
	}

	// --- Provider factory ---
	factory := &profileProviderFactory{
		cfg:      rt.cfg,
		creds:    rt.creds,
		fallback: rt.provider,
	}

	// --- Build Config & create executor ---
	cfg := executor.Config{
		Workflow:              rt.wf,
		ProviderFactory:       factory,
		Registry:              rt.registry,
		Policy:                rt.pol,
		Debug:                 rt.debug,
		MCPManager:            mcpMgr,
		Session:               rt.sess,
		SessionManager:        rt.sessionMgr,
		SecurityVerifier:      secVerifier,
		SecurityResearchScope: secResearchScope,
		TimeoutMCP:            rt.cfg.Timeouts.MCP,
		TimeoutWebSearch:      rt.cfg.Timeouts.WebSearch,
		TimeoutWebFetch:       rt.cfg.Timeouts.WebFetch,
		CheckpointStore:       checkpointStore,
		Supervisor:            supervisor,
		ObservationExtractor:  obsExtractor,
		ObservationStore:      obsStore,
		WorkspaceContext:      wsCtx,
	}
	rt.exec = executor.New(cfg)

	// Wire bash security callback (needs exec reference)
	rt.registry.SetBashSecurityCallback(rt.exec.LogBashSecurity)

	// Set HTTP client timeout to max of configured timeouts
	maxTimeout := rt.cfg.Timeouts.MCP
	if rt.cfg.Timeouts.WebSearch > maxTimeout {
		maxTimeout = rt.cfg.Timeouts.WebSearch
	}
	if rt.cfg.Timeouts.WebFetch > maxTimeout {
		maxTimeout = rt.cfg.Timeouts.WebFetch
	}
	if maxTimeout > 0 {
		tools.SetHTTPTimeout(time.Duration(maxTimeout) * time.Second)
	}

	return nil
}

// profileProviderFactory creates providers based on capability profiles.
type profileProviderFactory struct {
	mu       sync.Mutex
	cfg      *config.Config
	creds    *credentials.Credentials
	fallback llm.Provider
	cache    map[string]llm.Provider
}

// GetProvider returns a provider for the given profile name.
// It is safe for concurrent use by multiple goroutines (e.g. sub-agents).
func (f *profileProviderFactory) GetProvider(profile string) (llm.Provider, error) {
	if profile == "" {
		return f.fallback, nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cache != nil {
		if cached, ok := f.cache[profile]; ok {
			return cached, nil
		}
	}

	profileCfg := f.cfg.GetProfile(profile)
	if profileCfg.Model == "" {
		return f.fallback, nil
	}

	providerName := profileCfg.Provider
	if providerName == "" {
		providerName = llm.InferProviderFromModel(profileCfg.Model)
	}
	cred := f.creds.GetCredential(providerName)

	provider, err := llm.NewProvider(llm.ProviderConfig{
		Provider:     providerName,
		Model:        profileCfg.Model,
		APIKey:       cred.Key,
		IsOAuthToken: cred.IsOAuthToken,
		MaxTokens:    profileCfg.MaxTokens,
		BaseURL:      profileCfg.BaseURL,
		Thinking:     llm.ThinkingConfig{Level: llm.ThinkingLevel(profileCfg.Thinking)},
	})
	if err != nil {
		return nil, fmt.Errorf("creating provider for profile %q: %w", profile, err)
	}

	if f.cache == nil {
		f.cache = make(map[string]llm.Provider)
	}
	f.cache[profile] = provider

	return provider, nil
}

// determineSecurityConfig extracts security mode and settings.
func (rt *runtime) determineSecurityConfig() (security.Mode, string, security.TrustLevel) {
	mode := security.ModeDefault
	var scope string
	if rt.cfg.Security.Mode == "paranoid" || rt.wf.SecurityMode == "paranoid" {
		mode = security.ModeParanoid
	} else if rt.wf.SecurityMode == "research" {
		mode = security.ModeResearch
		scope = rt.wf.SecurityScope
	}

	userTrust := security.TrustUntrusted
	switch rt.cfg.Security.UserTrust {
	case "trusted":
		userTrust = security.TrustTrusted
	case "vetted":
		userTrust = security.TrustVetted
	}
	return mode, scope, userTrust
}

// createTriageProvider creates the LLM for security triage.
func (rt *runtime) createTriageProvider() llm.Provider {
	if rt.cfg.Security.TriageLLM != "" {
		triageCfg := rt.cfg.GetProfile(rt.cfg.Security.TriageLLM)
		providerName := triageCfg.Provider
		if providerName == "" {
			providerName = llm.InferProviderFromModel(triageCfg.Model)
		}
		triageCred := rt.creds.GetCredential(providerName)
		provider, _ := llm.NewProvider(llm.ProviderConfig{
			Provider:     providerName,
			Model:        triageCfg.Model,
			APIKey:       triageCred.Key,
			IsOAuthToken: triageCred.IsOAuthToken,
			MaxTokens:    triageCfg.MaxTokens,
		})
		return provider
	}
	return rt.smallLLM // May be nil
}

// setupCallbacks wires up telemetry and progress callbacks via hooks.
func (rt *runtime) setupCallbacks() {
	rt.exec.Hooks().On(hooks.SubAgentStart, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"].(string)
		fmt.Fprintf(os.Stderr, "  ⊕ Spawning sub-agent: %s\n", name)
		rt.telem.LogEvent("subagent_start", map[string]interface{}{"role": name})
	})
	rt.exec.Hooks().On(hooks.SubAgentComplete, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"].(string)
		fmt.Fprintf(os.Stderr, "  ⊖ Sub-agent complete: %s\n", name)
		rt.telem.LogEvent("subagent_complete", map[string]interface{}{"role": name})
	})
	rt.exec.Hooks().On(hooks.GoalStart, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"].(string)
		fmt.Fprintf(os.Stderr, "▶ Starting goal: %s\n", name)
		rt.telem.LogEvent("goal_started", map[string]interface{}{"goal": name})
	})
	rt.exec.Hooks().On(hooks.GoalComplete, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"].(string)
		fmt.Fprintf(os.Stderr, "✓ Completed goal: %s\n", name)
		rt.telem.LogEvent("goal_complete", map[string]interface{}{"goal": name})
	})
	rt.exec.Hooks().On(hooks.ToolCall, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"].(string)
		args, _ := evt.Data["args"].(map[string]interface{})
		agentRole, _ := evt.Data["agent_role"].(string)
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  → [%s] Tool: %s\n", agentRole, name)
		} else {
			fmt.Fprintf(os.Stderr, "  → Tool: %s\n", name)
		}
		rt.telem.LogEvent("tool_call", map[string]interface{}{"tool": name, "args": args, "agent": agentRole})
	})
	rt.exec.Hooks().On(hooks.ToolError, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"].(string)
		err := evt.Data["error"].(error)
		agentRole, _ := evt.Data["agent_role"].(string)
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  ✗ [%s] Tool error [%s]: %v\n", agentRole, name, err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✗ Tool error [%s]: %v\n", name, err)
		}
		rt.telem.LogEvent("tool_error", map[string]interface{}{"tool": name, "error": err.Error(), "agent": agentRole})
	})
	rt.exec.Hooks().On(hooks.MCPToolCall, func(_ context.Context, evt hooks.Event) {
		server := evt.Data["server"].(string)
		tool := evt.Data["tool"].(string)
		args, _ := evt.Data["args"].(map[string]interface{})
		fmt.Fprintf(os.Stderr, "  → MCP Tool: %s/%s\n", server, tool)
		rt.telem.LogEvent("mcp_tool_call", map[string]interface{}{"server": server, "tool": tool, "args": args})
	})
	rt.exec.Hooks().On(hooks.SkillLoaded, func(_ context.Context, evt hooks.Event) {
		name := evt.Data["name"].(string)
		fmt.Fprintf(os.Stderr, "  → Skill loaded: %s\n", name)
		rt.telem.LogEvent("skill_loaded", map[string]interface{}{"skill": name})
	})
	rt.exec.Hooks().On(hooks.SupervisionEvent, func(_ context.Context, evt hooks.Event) {
		stepID := evt.Data["step_id"].(string)
		phase := evt.Data["phase"].(string)
		fmt.Fprintf(os.Stderr, "  ⊙ Supervision [%s]: %s\n", stepID, phase)
		rt.telem.LogEvent("supervision_"+phase, map[string]interface{}{"step": stepID})
	})
	rt.exec.Hooks().On(hooks.LLMError, func(_ context.Context, evt hooks.Event) {
		err := evt.Data["error"].(error)
		fmt.Fprintf(os.Stderr, "  ✗ LLM error: %v\n", err)
		rt.telem.LogEvent("llm_error", map[string]interface{}{"error": err.Error()})
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
