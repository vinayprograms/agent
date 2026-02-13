// Package main is the entry point for the headless agent CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agentkit/credentials"
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
	w := parseWorkflow(args)
	if err := w.load(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	rt := newRuntime(w, globalCreds)
	defer rt.cleanup()

	if err := rt.setup(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	os.Exit(rt.run(ctx))
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
