// Package main is the entry point for the headless agent CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agentkit/telemetry"
	"github.com/vinayprograms/agentkit/tools"
)

// Build-time variables (set via ldflags)
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

// globalCreds holds loaded credentials (file > env fallback happens in GetAPIKey)
var globalCreds *credentials.Credentials

func init() {
	// Load credentials from standard locations
	// Priority: credentials.toml > env vars (handled by GetAPIKey)
	if creds, _, err := credentials.Load(); err == nil && creds != nil {
		globalCreds = creds
	}
	
	// Load .env for any additional env vars
	_ = godotenv.Load()
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "run":
		runWorkflow(args)
	case "validate":
		validateWorkflow(args)
	case "inspect":
		// Check if it's a package (zip) or Agentfile (text)
		if len(args) > 0 && isPackageFile(args[0]) {
			inspectPackage(args)
		} else {
			inspectWorkflow(args)
		}
	case "pack":
		packAgent(args)
	case "verify":
		verifyPackage(args)
	case "install":
		installPackage(args)
	case "keygen":
		generateKeys(args)
	case "setup":
		runSetup()
	case "replay":
		replaySession(args)
	case "version":
		fmt.Printf("agent version %s (commit: %s, built: %s)\n", version, commit, buildTime)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Usage: agent <command> [options]

Commands:
  run                   Run a workflow
  validate              Validate syntax
  inspect               Show workflow structure
  pack <dir>            Create a signed package
  verify <pkg>          Verify package signature
  install <pkg>         Install a package
  keygen                Generate signing key pair
  setup                 Interactive setup wizard
  replay <session.json> Replay session for forensic analysis
  version               Show version
  help                  Show this help

Agentfile Options:
  -f, --file <path>     Agentfile path (default: ./Agentfile)

Run Options:
  --input key=value     Provide input (repeatable)
  --config <path>       Config file path
  --policy <path>       Policy file path
  --workspace <path>    Workspace directory
  --persist-memory      Enable persistent memory (overrides config)
  --no-persist-memory   Disable persistent memory (overrides config)

Pack Options:
  --output, -o <path>   Output package path
  --sign <key.pem>      Sign with private key
  --author <name>       Author name
  --email <email>       Author email
  --license <license>   License (MIT, Apache-2.0, etc.)

Install Options:
  --no-deps             Skip dependency installation
  --dry-run             Show what would be installed
  --key <key.pub>       Verify with public key

Replay Options:
  -v, --verbose         Show full message and result content

Keygen Options:
  --output, -o <path>   Output path prefix (creates .pem and .pub)`)
}

// resolveAgentfile finds the Agentfile path from args.
// Supports: -f <path>, --file <path>, --file=<path>, or defaults to ./Agentfile
func resolveAgentfile(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "-f" || arg == "--file") && i+1 < len(args):
			path := args[i+1]
			remaining := append(args[:i], args[i+2:]...)
			return path, remaining
		case strings.HasPrefix(arg, "--file="):
			path := strings.TrimPrefix(arg, "--file=")
			remaining := append(args[:i], args[i+1:]...)
			return path, remaining
		case strings.HasPrefix(arg, "-f="):
			path := strings.TrimPrefix(arg, "-f=")
			remaining := append(args[:i], args[i+1:]...)
			return path, remaining
		}
	}
	// Default to Agentfile in current directory
	return "Agentfile", args
}

// runWorkflow executes a workflow from an Agentfile.
func runWorkflow(args []string) {
	agentfilePath, args := resolveAgentfile(args)
	
	if _, err := os.Stat(agentfilePath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: %s not found\n", agentfilePath)
		fmt.Fprintln(os.Stderr, "Use -f <path> to specify a different Agentfile")
		os.Exit(1)
	}

	inputs := make(map[string]string)
	var configPath, policyPath, workspacePath string
	var persistMemoryOverride *bool // nil = no override, use config

	// Parse flags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--input" && i+1 < len(args):
			i++
			parts := strings.SplitN(args[i], "=", 2)
			if len(parts) == 2 {
				inputs[parts[0]] = parts[1]
			}
		case strings.HasPrefix(arg, "--input="):
			parts := strings.SplitN(strings.TrimPrefix(arg, "--input="), "=", 2)
			if len(parts) == 2 {
				inputs[parts[0]] = parts[1]
			}
		case arg == "--config" && i+1 < len(args):
			i++
			configPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--policy" && i+1 < len(args):
			i++
			policyPath = args[i]
		case strings.HasPrefix(arg, "--policy="):
			policyPath = strings.TrimPrefix(arg, "--policy=")
		case arg == "--workspace" && i+1 < len(args):
			i++
			workspacePath = args[i]
		case strings.HasPrefix(arg, "--workspace="):
			workspacePath = strings.TrimPrefix(arg, "--workspace=")
		case arg == "--persist-memory":
			t := true
			persistMemoryOverride = &t
		case arg == "--no-persist-memory":
			f := false
			persistMemoryOverride = &f
		}
	}

	// Load configuration
	var cfg *config.Config
	var err error
	if configPath != "" {
		cfg, err = config.LoadFile(configPath)
	} else {
		cfg, err = config.LoadFile("agent.toml")
		if os.IsNotExist(err) {
			cfg = config.Default()
			err = nil
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Apply CLI overrides
	if persistMemoryOverride != nil {
		cfg.Storage.PersistMemory = *persistMemoryOverride
	}

	// Override workspace if specified
	if workspacePath != "" {
		cfg.Agent.Workspace = workspacePath
	}
	if cfg.Agent.Workspace == "" {
		cfg.Agent.Workspace, _ = os.Getwd()
	}
	// Make workspace absolute
	if !filepath.IsAbs(cfg.Agent.Workspace) {
		cfg.Agent.Workspace, _ = filepath.Abs(cfg.Agent.Workspace)
	}

	// Parse and load Agentfile (includes loading FROM files and validation)
	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading Agentfile: %v\n", err)
		os.Exit(1)
	}

	baseDir := filepath.Dir(agentfilePath)

	// Load policy
	var pol *policy.Policy
	if policyPath != "" {
		pol, err = policy.LoadFile(policyPath)
	} else {
		defaultPolicyPath := filepath.Join(baseDir, "policy.toml")
		pol, err = policy.LoadFile(defaultPolicyPath)
		if os.IsNotExist(err) {
			pol = policy.New()
			err = nil
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading policy: %v\n", err)
		os.Exit(1)
	}
	pol.Workspace = cfg.Agent.Workspace

	// Register custom security patterns and keywords from policy
	if pol.Security != nil {
		if len(pol.Security.ExtraPatterns) > 0 {
			if err := security.RegisterCustomPatterns(pol.Security.ExtraPatterns); err != nil {
				fmt.Fprintf(os.Stderr, "warning: invalid security pattern in policy: %v\n", err)
			}
		}
		if len(pol.Security.ExtraKeywords) > 0 {
			security.RegisterCustomKeywords(pol.Security.ExtraKeywords)
		}
	}

	// Create LLM provider
	var provider llm.Provider
	
	// Determine provider from model if not set
	llmProvider := cfg.LLM.Provider
	if llmProvider == "" {
		llmProvider = llm.InferProviderFromModel(cfg.LLM.Model)
	}
	
	if llmProvider != "" || cfg.LLM.Model != "" {
		provider, err = llm.NewProvider(llm.ProviderConfig{
			Provider:    llmProvider,
			Model:       cfg.LLM.Model,
			APIKey:      globalCreds.GetAPIKey(llmProvider),
			MaxTokens:   cfg.LLM.MaxTokens,
			BaseURL:     cfg.LLM.BaseURL,
			Thinking:    llm.ThinkingConfig{Level: llm.ThinkingLevel(cfg.LLM.Thinking)},
			RetryConfig: parseRetryConfig(cfg.LLM.MaxRetries, cfg.LLM.RetryBackoff),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating LLM provider: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "error: LLM model not configured")
		os.Exit(1)
	}

	// Create tool registry
	registry := tools.NewRegistry(pol)

	// Set up bash security checker with denylist + LLM policy check
	{
		bashPolicy := pol.GetToolPolicy("bash")
		allowedDirs := bashPolicy.AllowedDirs
		if len(allowedDirs) == 0 {
			// Fail-close: if no allowed_dirs configured, restrict to workspace only (or cwd)
			if pol.Workspace != "" {
				allowedDirs = []string{pol.Workspace}
			} else {
				// Last resort: current directory only
				if cwd, err := os.Getwd(); err == nil {
					allowedDirs = []string{cwd}
				} else {
					allowedDirs = []string{"."}
				}
			}
			fmt.Printf("⚠️  No allowed_dirs configured for bash - defaulting to: %v (fail-close)\n", allowedDirs)
		}
		bashChecker := policy.NewBashChecker(pol.Workspace, allowedDirs, bashPolicy.Denylist)
		registry.SetBashChecker(bashChecker)
	}

	// Set up summarizer for web_fetch if small_llm is configured
	var smallLLM llm.Provider // Keep reference for observation extraction
	if cfg.SmallLLM.Model != "" {
		smallProvider := cfg.SmallLLM.Provider
		if smallProvider == "" {
			smallProvider = llm.InferProviderFromModel(cfg.SmallLLM.Model)
		}
		var err error
		smallLLM, err = llm.NewProvider(llm.ProviderConfig{
			Provider:  smallProvider,
			Model:     cfg.SmallLLM.Model,
			APIKey:    globalCreds.GetAPIKey(smallProvider),
			MaxTokens: cfg.SmallLLM.MaxTokens,
		})
		if err == nil {
			registry.SetSummarizer(llm.NewSummarizer(smallLLM))
			// Also set up LLM-based bash policy checker (Step 2)
			registry.SetBashLLMChecker(policy.NewSmallLLMChecker(&llmGenerateAdapter{smallLLM}))
		} else {
			smallLLM = nil // Clear on error
		}
	}

	// Set up credentials for web_search tool
	registry.SetCredentials(globalCreds)

	// Resolve storage path: default to ~/.local/grid/
	storagePath := cfg.Storage.Path
	if storagePath == "" {
		home, _ := os.UserHomeDir()
		storagePath = filepath.Join(home, ".local", "grid")
	}
	// Expand ~ if present
	if len(storagePath) > 0 && storagePath[0] == '~' {
		home, _ := os.UserHomeDir()
		storagePath = filepath.Join(home, storagePath[1:])
	}

	// Ensure storage directory exists
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating storage directory: %v\n", err)
		os.Exit(1)
	}

	// Session path is a subdirectory
	sessionPath := filepath.Join(storagePath, "sessions", wf.Name)

	// Create session manager (always file-based now)
	sessionMgr := session.NewFileManager(sessionPath)

	// Set up memory stores
	// Scratchpad (KV): always available, persisted based on config
	kvPath := filepath.Join(storagePath, "kv.json")
	var kvStore tools.MemoryStore
	persistMemory := cfg.Storage.PersistMemory
	if persistMemory {
		kvStore = tools.NewFileMemoryStore(kvPath)
	} else {
		kvStore = tools.NewInMemoryStore()
	}
	registry.SetScratchpad(kvStore, persistMemory)

	// Semantic memory: available if embedding provider is configured
	var semanticMemory *memory.ToolsAdapter
	var bleveStore *memory.BleveStore // Keep reference for observation extraction
	embedder, err := createEmbeddingProvider(cfg.Embedding, globalCreds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating embedding provider: %v\n", err)
		os.Exit(1)
	}

	if embedder == nil {
		// Semantic memory disabled (provider = "none")
		if persistMemory {
			fmt.Println("🧠 Memory: scratchpad persistent, semantic disabled")
		} else {
			fmt.Println("🧠 Memory: scratchpad ephemeral, semantic disabled")
		}
	} else if persistMemory {
		// Persistent semantic memory using BM25 + semantic graph
		var storeErr error
		bleveStore, storeErr = memory.NewBleveStore(memory.BleveStoreConfig{
			BasePath: storagePath,
			Embedder: embedder,
			Provider: cfg.Embedding.Provider,
			Model:    cfg.Embedding.Model,
			BaseURL:  cfg.Embedding.BaseURL,
		})
		if storeErr != nil {
			fmt.Fprintf(os.Stderr, "error creating semantic memory store: %v\n", storeErr)
			os.Exit(1)
		}
		defer bleveStore.Close()

		semanticMemory = memory.NewToolsAdapter(bleveStore)
		registry.SetSemanticMemory(&semanticMemoryBridge{semanticMemory})
		fmt.Println("🧠 Memory: persistent (scratchpad + semantic)")
	} else {
		// In-memory semantic store for ephemeral mode
		memStore := memory.NewInMemoryStore(embedder)
		semanticMemory = memory.NewToolsAdapter(memStore)
		registry.SetSemanticMemory(&semanticMemoryBridge{semanticMemory})
		fmt.Println("🧠 Memory: ephemeral (scratchpad + semantic)")
	}

	// Create telemetry exporter
	var telem telemetry.Exporter
	if cfg.Telemetry.Enabled {
		telem, err = telemetry.NewExporter(cfg.Telemetry.Protocol, cfg.Telemetry.Endpoint)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating telemetry exporter: %v\n", err)
			os.Exit(1)
		}
	} else {
		telem = telemetry.NewNoopExporter()
	}
	defer telem.Close()

	// Create executor
	exec := executor.NewExecutor(wf, provider, registry, pol)

	// Initialize MCP servers if configured
	var mcpManager *mcp.Manager
	if len(cfg.MCP.Servers) > 0 {
		mcpManager = mcp.NewManager()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		for name, serverCfg := range cfg.MCP.Servers {
			err := mcpManager.Connect(ctx, name, mcp.ServerConfig{
				Command: serverCfg.Command,
				Args:    serverCfg.Args,
				Env:     serverCfg.Env,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to connect MCP server %q: %v\n", name, err)
			} else {
				fmt.Fprintf(os.Stderr, "✓ Connected MCP server: %s\n", name)
				// Apply denied tools filter
				if len(serverCfg.DeniedTools) > 0 {
					mcpManager.SetDeniedTools(name, serverCfg.DeniedTools)
					fmt.Fprintf(os.Stderr, "  └─ Denied %d tools\n", len(serverCfg.DeniedTools))
				}
			}
		}
		cancel()
		exec.SetMCPManager(mcpManager)
		defer mcpManager.Close()
	}

	// Set up bash security logging callback
	registry.SetBashSecurityCallback(exec.LogBashSecurity)

	// Set up security verifier if configured or if workflow has security mode
	securityMode := security.ModeDefault
	researchScope := ""
	if cfg.Security.Mode == "paranoid" || wf.SecurityMode == "paranoid" {
		securityMode = security.ModeParanoid
	} else if wf.SecurityMode == "research" {
		securityMode = security.ModeResearch
		researchScope = wf.SecurityScope
	}
	
	// Determine user trust level
	userTrust := security.TrustUntrusted // Default to untrusted
	switch cfg.Security.UserTrust {
	case "trusted":
		userTrust = security.TrustTrusted
	case "vetted":
		userTrust = security.TrustVetted
	}
	
	// Create triage provider for Tier 2 (use small_llm if configured, otherwise skip T2)
	var triageProvider llm.Provider
	if cfg.Security.TriageLLM != "" {
		triageCfg := cfg.GetProfile(cfg.Security.TriageLLM)
		triageProviderName := triageCfg.Provider
		if triageProviderName == "" {
			triageProviderName = llm.InferProviderFromModel(triageCfg.Model)
		}
		triageProvider, _ = llm.NewProvider(llm.ProviderConfig{
			Provider:  triageProviderName,
			Model:     triageCfg.Model,
			APIKey:    globalCreds.GetAPIKey(triageProviderName),
			MaxTokens: triageCfg.MaxTokens,
		})
	} else if cfg.SmallLLM.Model != "" {
		// Fall back to small_llm for triage
		smallProviderName := cfg.SmallLLM.Provider
		if smallProviderName == "" {
			smallProviderName = llm.InferProviderFromModel(cfg.SmallLLM.Model)
		}
		triageProvider, _ = llm.NewProvider(llm.ProviderConfig{
			Provider:  smallProviderName,
			Model:     cfg.SmallLLM.Model,
			APIKey:    globalCreds.GetAPIKey(smallProviderName),
			MaxTokens: cfg.SmallLLM.MaxTokens,
		})
	}
	
	// Set up sub-agent callbacks
	exec.OnSubAgentStart = func(name string, input map[string]string) {
		fmt.Fprintf(os.Stderr, "  ⊕ Spawning sub-agent: %s\n", name)
		telem.LogEvent("subagent_start", map[string]interface{}{"role": name})
	}
	exec.OnSubAgentComplete = func(name, output string) {
		fmt.Fprintf(os.Stderr, "  ⊖ Sub-agent complete: %s\n", name)
		telem.LogEvent("subagent_complete", map[string]interface{}{"role": name})
	}
	
	// Set up callbacks
	exec.OnGoalStart = func(name string) {
		fmt.Fprintf(os.Stderr, "▶ Starting goal: %s\n", name)
		telem.LogEvent("goal_started", map[string]interface{}{"goal": name})
	}
	exec.OnGoalComplete = func(name, output string) {
		fmt.Fprintf(os.Stderr, "✓ Completed goal: %s\n", name)
		telem.LogEvent("goal_complete", map[string]interface{}{"goal": name})
	}
	exec.OnToolCall = func(name string, args map[string]interface{}, result interface{}, agentRole string) {
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  → [%s] Tool: %s\n", agentRole, name)
		} else {
			fmt.Fprintf(os.Stderr, "  → Tool: %s\n", name)
		}
		telem.LogEvent("tool_call", map[string]interface{}{"tool": name, "args": args, "agent": agentRole})
	}
	exec.OnToolError = func(name string, args map[string]interface{}, err error, agentRole string) {
		if agentRole != "" && agentRole != "main" {
			fmt.Fprintf(os.Stderr, "  ✗ [%s] Tool error [%s]: %v\n", agentRole, name, err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✗ Tool error [%s]: %v\n", name, err)
		}
		telem.LogEvent("tool_error", map[string]interface{}{"tool": name, "error": err.Error(), "agent": agentRole})
	}
	exec.OnLLMError = func(err error) {
		fmt.Fprintf(os.Stderr, "  ✗ LLM error: %v\n", err)
		telem.LogEvent("llm_error", map[string]interface{}{"error": err.Error()})
	}

	// Create session
	sess, err := sessionMgr.Create(wf.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating session: %v\n", err)
		os.Exit(1)
	}

	// Create and attach security verifier
	securityVerifier, err := security.NewVerifier(security.Config{
		Mode:               securityMode,
		ResearchScope:      researchScope,
		UserTrust:          userTrust,
		TriageProvider:     triageProvider,
		SupervisorProvider: provider, // Use main provider for Tier 3 supervision
	}, sess.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create security verifier: %v\n", err)
	} else {
		exec.SetSecurityVerifier(securityVerifier)
		defer securityVerifier.Destroy()
		if securityMode == security.ModeResearch {
			fmt.Fprintf(os.Stderr, "🔓 Security: mode=research, scope=%q\n", researchScope)
		} else {
			fmt.Fprintf(os.Stderr, "🔒 Security: mode=%s, user_trust=%s\n", securityMode, userTrust)
		}
	}

	// Set security research scope for defensive framing in prompts
	if researchScope != "" {
		exec.SetSecurityResearchScope(researchScope)
	}

	// Set up execution supervision if workflow has supervised goals
	if wf.HasSupervisedGoals() {
		checkpointDir := filepath.Join(sessionPath, "checkpoints", sess.ID)
		checkpointStore, err := checkpoint.NewStore(checkpointDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to create checkpoint store: %v\n", err)
		} else {
			// Use main provider for supervision LLM (could be configurable)
			exec.SetSupervision(checkpointStore, provider, false, nil)
			fmt.Fprintf(os.Stderr, "👁 Supervision: enabled (four-phase execution)\n")
		}
	}

	// Connect session to executor for detailed logging
	exec.SetSession(sess, sessionMgr)

	// Set up observation extraction for semantic memory (requires small_llm and persistent bleve store)
	if smallLLM != nil && bleveStore != nil {
		obsExtractor := memory.NewObservationExtractor(smallLLM)
		obsStore := memory.NewBleveObservationStore(bleveStore)
		exec.SetObservationExtraction(obsExtractor, obsStore)
		fmt.Fprintf(os.Stderr, "🔍 Observations: enabled (extracting insights after each step)\n")
	}

	fmt.Fprintf(os.Stderr, "Running workflow: %s (session: %s)\n\n", wf.Name, sess.ID)

	// Run workflow
	ctx := context.Background()
	result, err := exec.Run(ctx, inputs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		sess.Status = "failed"
		sess.Error = err.Error()
		sessionMgr.Update(sess)
		os.Exit(1)
	}

	// Update session
	sess.Status = string(result.Status)
	sess.Outputs = result.Outputs
	sessionMgr.Update(sess)

	// Output result
	fmt.Fprintf(os.Stderr, "\n✓ Workflow complete\n")
	output, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(output))
}

// validateWorkflow validates an Agentfile.
func validateWorkflow(args []string) {
	agentfilePath, _ := resolveAgentfile(args)
	
	if _, err := os.Stat(agentfilePath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "✗ Error: %s not found\n", agentfilePath)
		os.Exit(1)
	}

	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error: %v\n", err)
		os.Exit(1)
	}

	_ = wf
	fmt.Println("✓ Valid")
}

// inspectWorkflow shows the structure of an Agentfile.
