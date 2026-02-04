// Package main is the entry point for the headless agent CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/openclaw/headless-agent/internal/agentfile"
	"github.com/openclaw/headless-agent/internal/config"
	"github.com/openclaw/headless-agent/internal/credentials"
	"github.com/openclaw/headless-agent/internal/executor"
	"github.com/openclaw/headless-agent/internal/llm"
	"github.com/openclaw/headless-agent/internal/packaging"
	"github.com/openclaw/headless-agent/internal/policy"
	"github.com/openclaw/headless-agent/internal/session"
	"github.com/openclaw/headless-agent/internal/telemetry"
	"github.com/openclaw/headless-agent/internal/tools"
)

const version = "0.1.0"

func init() {
	// Load credentials from standard locations (silent if not found)
	// Priority: env vars > .env > ~/.config/grid/credentials.toml
	
	// 1. Load credentials.toml first (lowest priority, can be overwritten)
	if creds, _, err := credentials.Load(); err == nil && creds != nil {
		creds.Apply()
	}
	
	// 2. Load .env (overwrites credentials.toml if both set)
	_ = godotenv.Load()
	
	// 3. Existing env vars have highest priority (Apply() won't overwrite them)
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
	case "version":
		fmt.Printf("agent version %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

// isPackageFile checks if a file is a zip package (not a text Agentfile)
func isPackageFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Check for zip magic bytes (PK\x03\x04)
	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil {
		return false
	}
	return magic[0] == 'P' && magic[1] == 'K' && magic[2] == 0x03 && magic[3] == 0x04
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
  version               Show version
  help                  Show this help

Agentfile Options:
  -f, --file <path>     Agentfile path (default: ./Agentfile)

Run Options:
  --input key=value     Provide input (repeatable)
  --config <path>       Config file path
  --policy <path>       Policy file path
  --workspace <path>    Workspace directory

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
		}
	}

	// Load configuration
	var cfg *config.Config
	var err error
	if configPath != "" {
		cfg, err = config.LoadFile(configPath)
	} else {
		cfg, err = config.LoadFile("agent.json")
		if os.IsNotExist(err) {
			cfg = config.Default()
			err = nil
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Override workspace if specified
	if workspacePath != "" {
		cfg.Agent.Workspace = workspacePath
	}
	if cfg.Agent.Workspace == "" {
		cfg.Agent.Workspace, _ = os.Getwd()
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

	// Create LLM provider
	var provider llm.Provider
	if cfg.LLM.Provider != "" {
		provider, err = llm.NewFantasyProvider(llm.FantasyConfig{
			Provider:  cfg.LLM.Provider,
			Model:     cfg.LLM.Model,
			APIKey:    cfg.GetAPIKey(),
			MaxTokens: cfg.LLM.MaxTokens,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating LLM provider: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "error: LLM provider not configured")
		os.Exit(1)
	}

	// Create tool registry
	registry := tools.NewRegistry(pol)

	// Set up summarizer for web_fetch if small_llm is configured
	if cfg.SmallLLM.Provider != "" && cfg.SmallLLM.Model != "" {
		// Resolve API key: explicit env var > default for provider
		apiKeyEnv := cfg.SmallLLM.APIKeyEnv
		if apiKeyEnv == "" {
			apiKeyEnv = config.DefaultAPIKeyEnv(cfg.SmallLLM.Provider)
		}
		smallProvider, err := llm.NewFantasyProvider(llm.FantasyConfig{
			Provider:  cfg.SmallLLM.Provider,
			Model:     cfg.SmallLLM.Model,
			APIKey:    os.Getenv(apiKeyEnv),
			MaxTokens: cfg.SmallLLM.MaxTokens,
		})
		if err == nil {
			registry.SetSummarizer(llm.NewSummarizer(smallProvider))
		}
	}

	// Resolve session path: default to ~/.local/grid/sessions/<workflow-name>/
	sessionPath := cfg.Session.Path
	if sessionPath == "" {
		home, _ := os.UserHomeDir()
		sessionPath = filepath.Join(home, ".local", "grid", "sessions", wf.Name)
	}

	// Create session manager
	var sessionMgr session.SessionManager
	if cfg.Session.Store == "sqlite" {
		sessionMgr, err = session.NewSQLiteManager(sessionPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating session manager: %v\n", err)
			os.Exit(1)
		}
	} else {
		sessionMgr = session.NewFileManager(sessionPath)
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
	exec.OnToolCall = func(name string, args map[string]interface{}, result interface{}) {
		fmt.Fprintf(os.Stderr, "  → Tool: %s\n", name)
		telem.LogEvent("tool_call", map[string]interface{}{"tool": name, "args": args})
	}
	exec.OnToolError = func(name string, args map[string]interface{}, err error) {
		fmt.Fprintf(os.Stderr, "  ✗ Tool error [%s]: %v\n", name, err)
		telem.LogEvent("tool_error", map[string]interface{}{"tool": name, "error": err.Error()})
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

	// Connect session to executor for detailed logging
	exec.SetSession(sess, sessionMgr)

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
func inspectWorkflow(args []string) {
	agentfilePath, _ := resolveAgentfile(args)
	
	if _, err := os.Stat(agentfilePath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: %s not found\n", agentfilePath)
		os.Exit(1)
	}

	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Workflow: %s\n\n", wf.Name)

	if len(wf.Inputs) > 0 {
		fmt.Println("Inputs:")
		for _, input := range wf.Inputs {
			if input.Default != nil {
				fmt.Printf("  - %s (default: %s)\n", input.Name, *input.Default)
			} else {
				fmt.Printf("  - %s (required)\n", input.Name)
			}
		}
		fmt.Println()
	}

	if len(wf.Agents) > 0 {
		fmt.Println("Agents:")
		for _, agent := range wf.Agents {
			fmt.Printf("  - %s", agent.Name)
			if agent.FromPath != "" {
				fmt.Printf(" (from %s)", agent.FromPath)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if len(wf.Goals) > 0 {
		fmt.Println("Goals:")
		for _, goal := range wf.Goals {
			fmt.Printf("  - %s", goal.Name)
			if len(goal.UsingAgent) > 0 {
				fmt.Printf(" [using: %s]", strings.Join(goal.UsingAgent, ", "))
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if len(wf.Steps) > 0 {
		fmt.Println("Steps:")
		for _, step := range wf.Steps {
			switch step.Type {
			case agentfile.StepRUN:
				fmt.Printf("  RUN %s: %s\n", step.Name, strings.Join(step.UsingGoals, ", "))
			case agentfile.StepLOOP:
				limit := "∞"
				if step.WithinLimit != nil {
					limit = fmt.Sprintf("%d", *step.WithinLimit)
				}
				fmt.Printf("  LOOP %s: %s (max %s)\n", step.Name, strings.Join(step.UsingGoals, ", "), limit)
			}
		}
	}
}

// inspectPackage shows the manifest of a package.
func inspectPackage(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: package path required")
		os.Exit(1)
	}

	pkgPath := args[0]
	pkg, err := packaging.Load(pkgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading package: %v\n", err)
		os.Exit(1)
	}

	m := pkg.Manifest
	fmt.Printf("Package: %s@%s\n", m.Name, m.Version)
	if m.Description != "" {
		fmt.Printf("Description: %s\n", m.Description)
	}
	if m.Author != nil {
		if m.Author.Email != "" {
			fmt.Printf("Author: %s <%s>\n", m.Author.Name, m.Author.Email)
		} else if m.Author.Name != "" {
			fmt.Printf("Author: %s\n", m.Author.Name)
		}
		if m.Author.KeyFingerprint != "" {
			fmt.Printf("Key fingerprint: %s\n", m.Author.KeyFingerprint)
		}
	}
	if m.License != "" {
		fmt.Printf("License: %s\n", m.License)
	}
	fmt.Println()

	if len(m.Inputs) > 0 {
		fmt.Println("Inputs:")
		for name, input := range m.Inputs {
			if input.Required {
				fmt.Printf("  - %s (required)", name)
			} else {
				fmt.Printf("  - %s (default: %s)", name, input.Default)
			}
			if input.Description != "" {
				fmt.Printf(" - %s", input.Description)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if len(m.Outputs) > 0 {
		fmt.Println("Outputs:")
		for name, output := range m.Outputs {
			fmt.Printf("  - %s", name)
			if output.Description != "" {
				fmt.Printf(": %s", output.Description)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if m.Requires != nil {
		if len(m.Requires.Profiles) > 0 {
			fmt.Printf("Required profiles: %s\n", strings.Join(m.Requires.Profiles, ", "))
		}
		if len(m.Requires.Tools) > 0 {
			fmt.Printf("Required tools: %s\n", strings.Join(m.Requires.Tools, ", "))
		}
		fmt.Println()
	}

	if len(m.Dependencies) > 0 {
		fmt.Println("Dependencies:")
		for name, version := range m.Dependencies {
			fmt.Printf("  - %s %s\n", name, version)
		}
		fmt.Println()
	}

	fmt.Printf("Created: %s\n", m.CreatedAt)
	if pkg.Signature != nil {
		fmt.Printf("Signed: yes (%d bytes)\n", len(pkg.Signature))
	} else {
		fmt.Println("Signed: no")
	}
}

// packAgent creates a signed agent package.
func packAgent(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: source directory required")
		os.Exit(1)
	}

	sourceDir := args[0]
	var outputPath, signKeyPath, authorName, authorEmail, license string

	// Parse flags
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "--output" || arg == "-o") && i+1 < len(args):
			i++
			outputPath = args[i]
		case strings.HasPrefix(arg, "--output="):
			outputPath = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			outputPath = strings.TrimPrefix(arg, "-o=")
		case arg == "--sign" && i+1 < len(args):
			i++
			signKeyPath = args[i]
		case strings.HasPrefix(arg, "--sign="):
			signKeyPath = strings.TrimPrefix(arg, "--sign=")
		case arg == "--author" && i+1 < len(args):
			i++
			authorName = args[i]
		case strings.HasPrefix(arg, "--author="):
			authorName = strings.TrimPrefix(arg, "--author=")
		case arg == "--email" && i+1 < len(args):
			i++
			authorEmail = args[i]
		case strings.HasPrefix(arg, "--email="):
			authorEmail = strings.TrimPrefix(arg, "--email=")
		case arg == "--license" && i+1 < len(args):
			i++
			license = args[i]
		case strings.HasPrefix(arg, "--license="):
			license = strings.TrimPrefix(arg, "--license=")
		}
	}

	opts := packaging.PackOptions{
		SourceDir: sourceDir,
		License:   license,
	}

	// Set author if provided
	if authorName != "" || authorEmail != "" {
		opts.Author = &packaging.Author{
			Name:  authorName,
			Email: authorEmail,
		}
	}

	// Load signing key if provided
	if signKeyPath != "" {
		privKey, err := packaging.LoadPrivateKey(signKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading signing key: %v\n", err)
			os.Exit(1)
		}
		opts.PrivateKey = privKey
	}

	// Create package (without writing yet to get manifest info)
	pkg, err := packaging.Pack(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating package: %v\n", err)
		os.Exit(1)
	}

	// Determine output path
	if outputPath == "" {
		outputPath = fmt.Sprintf("%s-%s.agent", pkg.Manifest.Name, pkg.Manifest.Version)
	}
	opts.OutputPath = outputPath

	// Pack again with output path
	pkg, err = packaging.Pack(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing package: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Created %s\n", outputPath)
	fmt.Printf("  Name: %s\n", pkg.Manifest.Name)
	fmt.Printf("  Version: %s\n", pkg.Manifest.Version)
	if signKeyPath != "" {
		fmt.Printf("  Signed: yes\n")
	} else {
		fmt.Printf("  Signed: no (use --sign to sign)\n")
	}
	if len(pkg.Manifest.Inputs) > 0 {
		fmt.Printf("  Inputs: %d\n", len(pkg.Manifest.Inputs))
	}
	if pkg.Manifest.Requires != nil && len(pkg.Manifest.Requires.Profiles) > 0 {
		fmt.Printf("  Requires profiles: %s\n", strings.Join(pkg.Manifest.Requires.Profiles, ", "))
	}
	if len(pkg.Manifest.Dependencies) > 0 {
		fmt.Printf("  Dependencies: %d\n", len(pkg.Manifest.Dependencies))
	}
}

// verifyPackage verifies a package signature.
func verifyPackage(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: package path required")
		os.Exit(1)
	}

	pkgPath := args[0]
	var pubKeyPath string

	// Parse flags
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--key" && i+1 < len(args):
			i++
			pubKeyPath = args[i]
		case strings.HasPrefix(arg, "--key="):
			pubKeyPath = strings.TrimPrefix(arg, "--key=")
		}
	}

	pkg, err := packaging.Load(pkgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading package: %v\n", err)
		os.Exit(1)
	}

	// Load public key if provided
	var pubKey []byte
	if pubKeyPath != "" {
		pubKey, err = packaging.LoadPublicKey(pubKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading public key: %v\n", err)
			os.Exit(1)
		}
	}

	// Verify
	if err := packaging.Verify(pkg, pubKey); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Verification failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Package verified: %s@%s\n", pkg.Manifest.Name, pkg.Manifest.Version)
	if pkg.Signature != nil {
		fmt.Println("  Signature: valid")
	} else {
		fmt.Println("  Signature: unsigned")
	}
	fmt.Println("  Content hash: valid")
}

// installPackage installs a package.
func installPackage(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: package path required")
		os.Exit(1)
	}

	pkgPath := args[0]
	var pubKeyPath, targetDir string
	noDeps := false
	dryRun := false

	// Parse flags
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--key" && i+1 < len(args):
			i++
			pubKeyPath = args[i]
		case strings.HasPrefix(arg, "--key="):
			pubKeyPath = strings.TrimPrefix(arg, "--key=")
		case arg == "--target" && i+1 < len(args):
			i++
			targetDir = args[i]
		case strings.HasPrefix(arg, "--target="):
			targetDir = strings.TrimPrefix(arg, "--target=")
		case arg == "--no-deps":
			noDeps = true
		case arg == "--dry-run":
			dryRun = true
		}
	}

	opts := packaging.InstallOptions{
		PackagePath: pkgPath,
		TargetDir:   targetDir,
		NoDeps:      noDeps,
		DryRun:      dryRun,
	}

	// Load public key if provided
	if pubKeyPath != "" {
		pubKey, err := packaging.LoadPublicKey(pubKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading public key: %v\n", err)
			os.Exit(1)
		}
		opts.PublicKey = pubKey
	}

	result, err := packaging.Install(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error installing package: %v\n", err)
		os.Exit(1)
	}

	if dryRun {
		fmt.Println("Dry run - would install:")
		for _, name := range result.Installed {
			fmt.Printf("  - %s\n", name)
		}
		if len(result.Dependencies) > 0 && !noDeps {
			fmt.Println("Dependencies:")
			for _, dep := range result.Dependencies {
				fmt.Printf("  - %s\n", dep)
			}
		}
		return
	}

	fmt.Printf("✓ Installed %s\n", strings.Join(result.Installed, ", "))
	fmt.Printf("  Location: %s\n", result.InstallPath)
	if len(result.Dependencies) > 0 {
		if noDeps {
			fmt.Println("  Dependencies (skipped, --no-deps):")
		} else {
			fmt.Println("  Dependencies (require manual install):")
		}
		for _, dep := range result.Dependencies {
			fmt.Printf("    - %s\n", dep)
		}
	}
}

// generateKeys generates a new signing key pair.
func generateKeys(args []string) {
	outputPrefix := "agent-key"

	// Parse flags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "--output" || arg == "-o") && i+1 < len(args):
			i++
			outputPrefix = args[i]
		case strings.HasPrefix(arg, "--output="):
			outputPrefix = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			outputPrefix = strings.TrimPrefix(arg, "-o=")
		}
	}

	privPath := outputPrefix + ".pem"
	pubPath := outputPrefix + ".pub"

	// Check if files exist
	if _, err := os.Stat(privPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists\n", privPath)
		os.Exit(1)
	}
	if _, err := os.Stat(pubPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists\n", pubPath)
		os.Exit(1)
	}

	pubKey, privKey, err := packaging.GenerateKeyPair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating key pair: %v\n", err)
		os.Exit(1)
	}

	if err := packaging.SavePrivateKey(privPath, privKey); err != nil {
		fmt.Fprintf(os.Stderr, "error saving private key: %v\n", err)
		os.Exit(1)
	}

	if err := packaging.SavePublicKey(pubPath, pubKey); err != nil {
		fmt.Fprintf(os.Stderr, "error saving public key: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Generated key pair\n")
	fmt.Printf("  Private key: %s (keep secret!)\n", privPath)
	fmt.Printf("  Public key:  %s (share for verification)\n", pubPath)
}
