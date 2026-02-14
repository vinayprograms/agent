// Package main provides runtime execution for workflows.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
	"github.com/vinayprograms/agent/internal/session"
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

	// Components
	provider       llm.Provider
	smallLLM       llm.Provider
	registry       *tools.Registry
	bashLLMChecker *policy.SmallLLMChecker
	telem          telemetry.Exporter
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
		wf:     w.wf,
		cfg:    w.cfg,
		pol:    w.pol,
		creds:  creds,
		inputs: w.inputs,
	}
	rt.resolveStoragePath()
	return rt
}

// resolveStoragePath sets up storage and session paths.
func (rt *runtime) resolveStoragePath() {
	rt.storagePath = rt.cfg.Storage.Path
	if rt.storagePath == "" {
		home, _ := os.UserHomeDir()
		rt.storagePath = filepath.Join(home, ".local", "grid")
	}
	if len(rt.storagePath) > 0 && rt.storagePath[0] == '~' {
		home, _ := os.UserHomeDir()
		rt.storagePath = filepath.Join(home, rt.storagePath[1:])
	}
	rt.sessionPath = filepath.Join(rt.storagePath, "sessions", rt.wf.Name)
}

// setup initializes all runtime components. Returns error on failure.
func (rt *runtime) setup() error {
	if err := os.MkdirAll(rt.storagePath, 0755); err != nil {
		return fmt.Errorf("creating storage directory: %w", err)
	}

	if err := rt.createProvider(); err != nil {
		return err
	}
	rt.createSmallLLM()
	rt.setupRegistry()
	if err := rt.setupMemory(); err != nil {
		return err
	}
	if err := rt.setupTelemetry(); err != nil {
		return err
	}
	rt.createExecutor()
	rt.setupMCP()
	if err := rt.setupSession(); err != nil {
		return err
	}
	rt.setupSecurity()
	rt.setupSupervision()
	rt.setupObservations()
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
	rt.provider, err = llm.NewProvider(llm.ProviderConfig{
		Provider:    llmProvider,
		Model:       rt.cfg.LLM.Model,
		APIKey:      rt.creds.GetAPIKey(llmProvider),
		MaxTokens:   rt.cfg.LLM.MaxTokens,
		BaseURL:     rt.cfg.LLM.BaseURL,
		Thinking:    llm.ThinkingConfig{Level: llm.ThinkingLevel(rt.cfg.LLM.Thinking)},
		RetryConfig: parseRetryConfig(rt.cfg.LLM.MaxRetries, rt.cfg.LLM.RetryBackoff),
	})
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}
	return nil
}

// createSmallLLM creates the small LLM for summarization and triage.
func (rt *runtime) createSmallLLM() {
	if rt.cfg.SmallLLM.Model == "" {
		return
	}
	smallProvider := rt.cfg.SmallLLM.Provider
	if smallProvider == "" {
		smallProvider = llm.InferProviderFromModel(rt.cfg.SmallLLM.Model)
	}
	var err error
	rt.smallLLM, err = llm.NewProvider(llm.ProviderConfig{
		Provider:  smallProvider,
		Model:     rt.cfg.SmallLLM.Model,
		APIKey:    rt.creds.GetAPIKey(smallProvider),
		MaxTokens: rt.cfg.SmallLLM.MaxTokens,
	})
	if err != nil {
		rt.smallLLM = nil
	}
}

// setupRegistry creates and configures the tool registry.
func (rt *runtime) setupRegistry() {
	rt.registry = tools.NewRegistry(rt.pol)
	rt.setupBashChecker()
	if rt.smallLLM != nil {
		rt.registry.SetSummarizer(llm.NewSummarizer(rt.smallLLM))
		rt.bashLLMChecker = policy.NewSmallLLMChecker(&llmGenerateAdapter{rt.smallLLM})
		rt.registry.SetBashLLMChecker(rt.bashLLMChecker)
	}
	rt.registry.SetCredentials(rt.creds)
}

// setupBashChecker configures bash security with fail-close defaults.
func (rt *runtime) setupBashChecker() {
	bashPolicy := rt.pol.GetToolPolicy("bash")
	allowedDirs := bashPolicy.AllowedDirs
	if len(allowedDirs) == 0 {
		if rt.pol.Workspace != "" {
			allowedDirs = []string{rt.pol.Workspace}
		} else if cwd, err := os.Getwd(); err == nil {
			allowedDirs = []string{cwd}
		} else {
			allowedDirs = []string{"."}
		}
		fmt.Printf("⚠️  No allowed_dirs configured for bash - defaulting to: %v (fail-close)\n", allowedDirs)
	}
	bashChecker := policy.NewBashChecker(rt.pol.Workspace, allowedDirs, bashPolicy.Denylist)
	rt.registry.SetBashChecker(bashChecker)
}

// setupMemory configures scratchpad and semantic memory.
func (rt *runtime) setupMemory() error {
	kvPath := filepath.Join(rt.storagePath, "kv.json")
	persist := rt.cfg.Storage.PersistMemory

	var kvStore tools.MemoryStore
	if persist {
		kvStore = tools.NewFileMemoryStore(kvPath)
	} else {
		kvStore = tools.NewInMemoryStore()
	}
	rt.registry.SetScratchpad(kvStore, persist)

	embedder, err := createEmbeddingProvider(rt.cfg.Embedding, rt.creds)
	if err != nil {
		return fmt.Errorf("creating embedding provider: %w", err)
	}

	if embedder == nil {
		rt.printMemoryStatus(persist, false)
		return nil
	}

	if persist {
		rt.bleveStore, err = memory.NewBleveStore(memory.BleveStoreConfig{
			BasePath: rt.storagePath,
			Embedder: embedder,
			Provider: rt.cfg.Embedding.Provider,
			Model:    rt.cfg.Embedding.Model,
			BaseURL:  rt.cfg.Embedding.BaseURL,
		})
		if err != nil {
			return fmt.Errorf("creating semantic memory store: %w", err)
		}
		rt.addCloser(func() { rt.bleveStore.Close() })
		semanticMemory := memory.NewToolsAdapter(rt.bleveStore)
		rt.registry.SetSemanticMemory(&semanticMemoryBridge{semanticMemory})
	} else {
		memStore := memory.NewInMemoryStore(embedder)
		semanticMemory := memory.NewToolsAdapter(memStore)
		rt.registry.SetSemanticMemory(&semanticMemoryBridge{semanticMemory})
	}

	rt.printMemoryStatus(persist, true)
	return nil
}

// printMemoryStatus prints memory configuration status.
func (rt *runtime) printMemoryStatus(persist, semantic bool) {
	switch {
	case persist && semantic:
		fmt.Println("🧠 Memory: persistent (scratchpad + semantic)")
	case persist:
		fmt.Println("🧠 Memory: scratchpad persistent, semantic disabled")
	case semantic:
		fmt.Println("🧠 Memory: ephemeral (scratchpad + semantic)")
	default:
		fmt.Println("🧠 Memory: scratchpad ephemeral, semantic disabled")
	}
}

// setupTelemetry creates the telemetry exporter.
func (rt *runtime) setupTelemetry() error {
	var err error
	if rt.cfg.Telemetry.Enabled {
		rt.telem, err = telemetry.NewExporter(rt.cfg.Telemetry.Protocol, rt.cfg.Telemetry.Endpoint)
		if err != nil {
			return fmt.Errorf("creating telemetry exporter: %w", err)
		}
	} else {
		rt.telem = telemetry.NewNoopExporter()
	}
	rt.addCloser(func() { rt.telem.Close() })
	return nil
}

// createExecutor creates the workflow executor.
func (rt *runtime) createExecutor() {
	rt.exec = executor.NewExecutor(rt.wf, rt.provider, rt.registry, rt.pol)
}

// setupMCP initializes MCP servers.
func (rt *runtime) setupMCP() {
	if len(rt.cfg.MCP.Servers) == 0 {
		return
	}

	rt.mcpManager = mcp.NewManager()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for name, serverCfg := range rt.cfg.MCP.Servers {
		err := rt.mcpManager.Connect(ctx, name, mcp.ServerConfig{
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
			rt.mcpManager.SetDeniedTools(name, serverCfg.DeniedTools)
			fmt.Fprintf(os.Stderr, "  └─ Denied %d tools\n", len(serverCfg.DeniedTools))
		}
	}
	rt.exec.SetMCPManager(rt.mcpManager)
	rt.addCloser(func() { rt.mcpManager.Close() })
}

// setupSession creates the session and session manager.
func (rt *runtime) setupSession() error {
	rt.sessionMgr = session.NewFileManager(rt.sessionPath)
	var err error
	rt.sess, err = rt.sessionMgr.Create(rt.wf.Name)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	rt.exec.SetSession(rt.sess, rt.sessionMgr)
	rt.registry.SetBashSecurityCallback(rt.exec.LogBashSecurity)
	return nil
}

// setupSecurity configures the security verifier.
func (rt *runtime) setupSecurity() {
	mode, scope, userTrust := rt.determineSecurityConfig()
	triageProvider := rt.createTriageProvider()

	verifier, err := security.NewVerifier(security.Config{
		Mode:               mode,
		ResearchScope:      scope,
		UserTrust:          userTrust,
		TriageProvider:     triageProvider,
		SupervisorProvider: rt.provider,
	}, rt.sess.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create security verifier: %v\n", err)
		return
	}

	rt.secVerifier = verifier
	rt.exec.SetSecurityVerifier(verifier)
	rt.addCloser(func() { verifier.Destroy() })

	if mode == security.ModeResearch {
		fmt.Fprintf(os.Stderr, "🔓 Security: mode=research, scope=%q\n", scope)
		rt.exec.SetSecurityResearchScope(scope)
		// Also set scope on bash LLM checker so it knows about research context
		if rt.bashLLMChecker != nil {
			rt.bashLLMChecker.SetSecurityScope(scope)
		}
	} else {
		fmt.Fprintf(os.Stderr, "🔒 Security: mode=%s, user_trust=%s\n", mode, userTrust)
	}
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
		provider, _ := llm.NewProvider(llm.ProviderConfig{
			Provider:  providerName,
			Model:     triageCfg.Model,
			APIKey:    rt.creds.GetAPIKey(providerName),
			MaxTokens: triageCfg.MaxTokens,
		})
		return provider
	}
	return rt.smallLLM // May be nil
}

// setupSupervision configures checkpoint-based supervision.
func (rt *runtime) setupSupervision() {
	if !rt.wf.HasSupervisedGoals() {
		return
	}
	checkpointDir := filepath.Join(rt.sessionPath, "checkpoints", rt.sess.ID)
	checkpointStore, err := checkpoint.NewStore(checkpointDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create checkpoint store: %v\n", err)
		return
	}
	rt.exec.SetSupervision(checkpointStore, rt.provider, false, nil)
	fmt.Fprintf(os.Stderr, "👁 Supervision: enabled (four-phase execution)\n")
}

// setupObservations configures memory observation extraction.
func (rt *runtime) setupObservations() {
	if rt.smallLLM == nil || rt.bleveStore == nil {
		return
	}
	obsExtractor := memory.NewObservationExtractor(rt.smallLLM)
	obsStore := memory.NewBleveObservationStore(rt.bleveStore)
	rt.exec.SetObservationExtraction(obsExtractor, obsStore)
	fmt.Fprintf(os.Stderr, "🔍 Observations: enabled (extracting insights after each step)\n")
}

// setupCallbacks wires up telemetry and progress callbacks.
func (rt *runtime) setupCallbacks() {
	rt.exec.OnSubAgentStart = func(name string, input map[string]string) {
		fmt.Fprintf(os.Stderr, "  ⊕ Spawning sub-agent: %s\n", name)
		rt.telem.LogEvent("subagent_start", map[string]interface{}{"role": name})
	}
	rt.exec.OnSubAgentComplete = func(name, output string) {
		fmt.Fprintf(os.Stderr, "  ⊖ Sub-agent complete: %s\n", name)
		rt.telem.LogEvent("subagent_complete", map[string]interface{}{"role": name})
	}
	rt.exec.OnGoalStart = func(name string) {
		fmt.Fprintf(os.Stderr, "▶ Starting goal: %s\n", name)
		rt.telem.LogEvent("goal_started", map[string]interface{}{"goal": name})
	}
	rt.exec.OnGoalComplete = func(name, output string) {
		fmt.Fprintf(os.Stderr, "✓ Completed goal: %s\n", name)
		rt.telem.LogEvent("goal_complete", map[string]interface{}{"goal": name})
	}
	rt.exec.OnToolCall = func(name string, args map[string]interface{}, result interface{}, agentRole string) {
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  → [%s] Tool: %s\n", agentRole, name)
		} else {
			fmt.Fprintf(os.Stderr, "  → Tool: %s\n", name)
		}
		rt.telem.LogEvent("tool_call", map[string]interface{}{"tool": name, "args": args, "agent": agentRole})
	}
	rt.exec.OnToolError = func(name string, args map[string]interface{}, err error, agentRole string) {
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  ✗ [%s] Tool error [%s]: %v\n", agentRole, name, err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✗ Tool error [%s]: %v\n", name, err)
		}
		rt.telem.LogEvent("tool_error", map[string]interface{}{"tool": name, "error": err.Error(), "agent": agentRole})
	}
	rt.exec.OnLLMError = func(err error) {
		fmt.Fprintf(os.Stderr, "  ✗ LLM error: %v\n", err)
		rt.telem.LogEvent("llm_error", map[string]interface{}{"error": err.Error()})
	}
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

	fmt.Fprintf(os.Stderr, "\n✓ Workflow complete\n")
	output, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(output))
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
