// Package main is the entry point for the headless agent CLI.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
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
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("agent"),
		kong.Description("Headless agent for running AI workflows"),
		kong.UsageOnError(),
		kong.Vars{"version": version},
	)
	err := ctx.Run(&runContext{creds: globalCreds})
	ctx.FatalIfErrorf(err)
}

// runContext provides shared dependencies to commands.
type runContext struct {
	creds *credentials.Credentials
}

// Run executes the run command.
func (c *RunCmd) Run(ctx *runContext) error {
	w := &workflow{
		agentfilePath: c.File,
		inputs:        c.Input,
		configPath:    c.Config,
		policyPath:    c.Policy,
		workspacePath: c.Workspace,
		debug:         c.Debug,
	}

	// Handle persist memory flag
	w.persistMemory = c.PersistMemory

	if _, err := os.Stat(w.agentfilePath); os.IsNotExist(err) {
		return fmt.Errorf("%s not found", w.agentfilePath)
	}

	if err := w.load(); err != nil {
		return err
	}

	rt := newRuntime(w, ctx.creds)
	defer rt.cleanup()

	if err := rt.setup(); err != nil {
		return err
	}

	bgCtx := context.Background()
	code := rt.run(bgCtx)
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// Run executes the validate command.
func (c *ValidateCmd) Run(ctx *runContext) error {
	if _, err := os.Stat(c.File); os.IsNotExist(err) {
		return fmt.Errorf("%s not found", c.File)
	}

	_, err := agentfile.LoadFile(c.File)
	if err != nil {
		return err
	}

	fmt.Println("✓ Valid")
	return nil
}

// Run executes the inspect command.
func (c *InspectCmd) Run(ctx *runContext) error {
	if isPackageFile(c.Path) {
		return runInspectPackage(c.Path)
	}
	return runInspectWorkflow(c.Path)
}

// Run executes the pack command.
func (c *PackCmd) Run(ctx *runContext) error {
	return runPack(c)
}

// Run executes the verify command.
func (c *VerifyCmd) Run(ctx *runContext) error {
	return runVerify(c.Package, c.Key)
}

// Run executes the install command.
func (c *InstallCmd) Run(ctx *runContext) error {
	return runInstall(c)
}

// Run executes the keygen command.
func (c *KeygenCmd) Run(ctx *runContext) error {
	return runKeygen(c.Output)
}

// Run executes the setup command.
func (c *SetupCmd) Run(ctx *runContext) error {
	runSetup()
	return nil
}

// Run executes the replay command.
func (c *ReplayCmd) Run(ctx *runContext) error {
	return runReplay(c.Session, c.Verbose, c.NoPager, c.Cost)
}

// Run executes the version command.
func (c *VersionCmd) Run(ctx *runContext) error {
	fmt.Printf("agent version %s (commit: %s, built: %s)\n", version, commit, buildTime)
	return nil
}
