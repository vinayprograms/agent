// Package main is the headless agent CLI: load credentials, parse CLI, dispatch.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/vinayprograms/agent/cmd/agent/subcommands"
	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/credentials"
)

// Build-time variables (set via ldflags)
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

// globalCreds holds loaded credentials (file > env fallback happens in GetAPIKey)
var globalCreds credentials.Store

// init runs before main and performs one-time startup wiring.
// Startup order is:
// 1) load .env so env-based config/credential references are available,
// 2) load credentials from standard locations,
// 3) register run subcommand factories.
//
// Config TOML precedence is resolved later (after CLI parsing) inside workflow
// loading, so --config can take highest priority over global/project/env files.
func init() {
	// Load .env first so env-provided config and key references are visible.
	godotenv.Load()

	// Load credentials from standard locations
	// Priority: CLI args > environment variables > current directory > home directory
	creds, path, err := loadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load credentials from %s: %v\n", path, err)
		os.Exit(1)
	}
	globalCreds = creds
	subcommands.SetRunWorkflowFactory(newRunWorkflow)
	subcommands.SetRunRuntimeFactory(newRunRuntime)
}

func main() {
	root, _ := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runContext provides shared dependencies to commands.
type runContext struct {
	creds credentials.Store
}

type runWorkflowAdapter struct {
	w *workflow
}

func newRunWorkflow(c *subcommands.RunCmd) (subcommands.RunWorkflow, error) {
	return &runWorkflowAdapter{w: &workflow{
		agentfilePath: c.File,
		inputs:        c.Input,
		configPath:    c.Config,
		policyPath:    c.Policy,
		workspacePath: c.Workspace,
		debug:         c.Debug,
	}}, nil
}

func (a *runWorkflowAdapter) AgentfilePath() string {
	return a.w.agentfilePath
}

func (a *runWorkflowAdapter) SetInlineGoal(goal string) {
	a.w.wf = &agentfile.Workflow{
		Name: "inline-goal",
		Goals: []agentfile.Goal{
			{Name: "goal", Outcome: goal},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, Name: "run-goal", UsingGoals: []string{"goal"}},
		},
	}
}

func (a *runWorkflowAdapter) Load() error {
	return a.w.load()
}

func (a *runWorkflowAdapter) LoadConfig() error {
	return a.w.loadConfig()
}

func (a *runWorkflowAdapter) LoadPolicy() error {
	return a.w.loadPolicy()
}

type runRuntimeAdapter struct {
	rt *runtime
}

func newRunRuntime(w subcommands.RunWorkflow, creds credentials.Store) (subcommands.RunRuntime, error) {
	wa, ok := w.(*runWorkflowAdapter)
	if !ok {
		return nil, fmt.Errorf("invalid run workflow adapter")
	}
	return &runRuntimeAdapter{rt: newRuntime(wa.w, creds)}, nil
}

func (a *runRuntimeAdapter) Setup() error {
	return a.rt.setup()
}

func (a *runRuntimeAdapter) Run(ctx context.Context) int {
	return a.rt.run(ctx)
}

func (a *runRuntimeAdapter) Cleanup() {
	a.rt.cleanup()
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
