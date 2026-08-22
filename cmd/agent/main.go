// Package main is the headless agent CLI: parse CLI, load what the command needs, dispatch.
package main

import (
	"context"
	"fmt"
	"os"

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

func main() {
	// Load .env for any additional env vars (credentials are read from env
	// when no credentials file is present).
	_ = godotenv.Load()

	root, _ := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// loadCredentials composes the credential lookup from env, the first
// credentials.toml in the standard locations, and the Claude CLI token.
// It runs inside the commands that need it, so a broken credentials file
// does not break --help, validate, pack and friends.
func loadCredentials() (credentials.Lookup, error) {
	creds, _, err := credentials.Load(credentials.StandardPaths("grid")...)
	if err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}
	return creds, nil
}

// Run executes the run command.
func (c *RunCmd) Run() error {
	creds, err := loadCredentials()
	if err != nil {
		return err
	}

	w := &workflow{
		agentfilePath: c.File,
		inputs:        c.Input,
		configPath:    c.Config,
		policyPath:    c.Policy,
		workspacePath: c.Workspace,
		debug:         c.Debug,
	}

	// Handle inline goal (skip Agentfile if provided)
	if c.Goal != "" {
		w.wf = &agentfile.Workflow{
			Name: "inline-goal",
			Goals: []agentfile.Goal{
				{Name: "goal", Outcome: c.Goal},
			},
			Steps: []agentfile.Step{
				{Type: agentfile.StepRUN, Name: "run-goal", UsingGoals: []string{"goal"}},
			},
		}
		// Still load config and policy, but skip Agentfile
		if err := w.loadConfig(); err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if err := w.loadPolicy(); err != nil {
			return fmt.Errorf("loading policy: %w", err)
		}
	} else {
		if _, err := os.Stat(w.agentfilePath); os.IsNotExist(err) {
			return fmt.Errorf("%s not found", w.agentfilePath)
		}

		if err := w.load(); err != nil {
			return err
		}
	}

	rt := newRuntime(w, creds)
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
func (c *ValidateCmd) Run() error {
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
func (c *InspectCmd) Run() error {
	if isPackageFile(c.Path) {
		return runInspectPackage(c.Path)
	}
	return runInspectWorkflow(c.Path)
}

// Run executes the pack command.
func (c *PackCmd) Run() error {
	return runPack(c)
}

// Run executes the verify command.
func (c *VerifyCmd) Run() error {
	return runVerify(c.Package, c.Key)
}

// Run executes the install command.
func (c *InstallCmd) Run() error {
	return runInstall(c)
}

// Run executes the keygen command.
func (c *KeygenCmd) Run() error {
	return runKeygen(c.Output)
}

// Run executes the setup command.
func (c *SetupCmd) Run() error {
	runSetup()
	return nil
}

// Run executes the replay command.
func (c *ReplayCmd) Run() error {
	return runReplay(c.Session, c.Verbose, c.NoPager, c.Cost)
}

// Run executes the version command.
func (c *VersionCmd) Run() error {
	fmt.Printf("agent version %s (commit: %s, built: %s)\n", version, commit, buildTime)
	return nil
}
