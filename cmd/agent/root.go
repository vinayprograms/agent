// Package main's routing layer: the root command factory and the process
// dependencies every subcommand draws on.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/replaycmd"
	"github.com/vinayprograms/agent/internal/run"
	"github.com/vinayprograms/agent/internal/term"
	"github.com/vinayprograms/agentkit/credentials"
)

// runner is the slice of the agent runtime the run command needs. It is a
// field of deps so tests can execute the command without an LLM.
type runner interface {
	Run(ctx context.Context) error
	Close()
}

// deps are the process facilities the commands reach for: the environment,
// credentials, and the runtime factory.
type deps struct {
	home   string
	getenv func(string) string
	// credentials resolves the credential lookup; the argument is the
	// --credentials override path (empty uses the standard paths).
	credentials func(override string) (credentials.Lookup, error)
	newRuntime  func(context.Context, *run.Loaded, run.Deps) (runner, error)
	// isTerminal reports whether a stream is an interactive terminal; --step
	// needs one. Tests substitute it.
	isTerminal func(any) bool
}

// newDeps returns the real process dependencies.
func newDeps() deps {
	home, _ := os.UserHomeDir()
	return deps{
		home:   home,
		getenv: os.Getenv,
		credentials: func(override string) (credentials.Lookup, error) {
			return loadCredentials(home, override)
		},
		newRuntime: func(ctx context.Context, l *run.Loaded, d run.Deps) (runner, error) {
			return run.New(ctx, l, d)
		},
		isTerminal: term.IsTerminal,
	}
}

// loadCredentials composes the credential lookup from env, the first
// credentials.toml found via run.CredentialPaths (honoring an explicit
// --credentials override), and the Claude CLI token. It runs inside the
// commands that need it, so a broken credentials file does not break
// --help, validate, pack and friends.
func loadCredentials(home, override string) (credentials.Lookup, error) {
	paths, err := run.CredentialPaths(home, override)
	if err != nil {
		return nil, err
	}
	creds, _, err := credentials.Load(paths...)
	if err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}
	return creds, nil
}

// newRootCmd builds the command tree. Every subcommand owns its own flags
// and prints through the command's streams.
func newRootCmd(d deps) *cobra.Command {
	root := &cobra.Command{
		Use:           "agent",
		Short:         "Headless agent for running AI workflows",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(*cobra.Command, []string) {
			// .env supplies credentials when no credentials file exists.
			_ = godotenv.Load()
		},
	}
	root.AddCommand(
		newRunCmd(d),
		newServeCmd(d),
		newValidateCmd(),
		newInspectCmd(),
		newPackCmd(),
		newVerifyCmd(),
		newInstallCmd(),
		newKeygenCmd(),
		newSetupCmd(),
		replaycmd.New(replaycmd.Config{Version: version, Commit: commit, BuildTime: buildTime}),
		newVersionCmd(),
	)
	return root
}

// argOr returns the first positional argument, or def when there is none.
func argOr(args []string, def string) string {
	if len(args) > 0 {
		return args[0]
	}
	return def
}

// runOptions are the run command's flags.
type runOptions struct {
	inputs      map[string]string
	config      string
	policy      string
	credentials string
	workspace   string
	goal        string
	debug       bool
	step        bool
}

// newRunCmd runs a workflow once and exits.
func newRunCmd(d deps) *cobra.Command {
	var opts runOptions
	cmd := &cobra.Command{
		Use:   "run [file]",
		Short: "Run a workflow (one-shot, ephemeral)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var gate func(context.Context, string, string) (bool, error)
			if opts.step {
				if !d.isTerminal(cmd.InOrStdin()) || !d.isTerminal(cmd.ErrOrStderr()) {
					return errors.New("--step needs an interactive terminal")
				}
				gate = stepGate(cmd.InOrStdin(), cmd.ErrOrStderr())
			}
			creds, err := d.credentials(opts.credentials)
			if err != nil {
				return err
			}
			loaded, err := run.Load(run.LoadOptions{
				AgentfilePath: argOr(args, "Agentfile"),
				ConfigPath:    opts.config,
				PolicyPath:    opts.policy,
				Workspace:     opts.workspace,
				Goal:          opts.goal,
				Inputs:        opts.inputs,
				Debug:         opts.debug,
				Home:          d.home,
				Stderr:        cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			rt, err := d.newRuntime(cmd.Context(), loaded, run.Deps{
				Creds:    creds,
				Stdout:   cmd.OutOrStdout(),
				Stderr:   cmd.ErrOrStderr(),
				Version:  version,
				StepGate: gate,
			})
			if err != nil {
				return err
			}
			defer rt.Close()
			return rt.Run(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.StringToStringVarP(&opts.inputs, "input", "i", nil, "Input key=value (repeatable)")
	f.StringVar(&opts.config, "config", "", "Config file path")
	f.StringVar(&opts.policy, "policy", "", "Policy file path")
	f.StringVar(&opts.credentials, "credentials", "", "Credentials file path")
	f.StringVar(&opts.workspace, "workspace", "", "Workspace directory")
	f.StringVar(&opts.goal, "goal", "", "Inline goal description (skips Agentfile)")
	f.BoolVar(&opts.debug, "debug", false, "Enable verbose logging (prompts, responses, tool outputs)")
	f.BoolVar(&opts.step, "step", false, "Pause after each goal and ask whether to continue (interactive terminal only)")
	return cmd
}

// newValidateCmd checks that an Agentfile parses.
func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [file]",
		Short: "Validate Agentfile syntax",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := argOr(args, "Agentfile")
			if _, err := os.Stat(file); os.IsNotExist(err) {
				return fmt.Errorf("%s not found", file)
			}
			if _, err := agentfile.LoadFile(file); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "✓ Valid")
			return nil
		},
	}
}

// newVersionCmd reports the build identity.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "agent version %s (commit: %s, built: %s)\n", version, commit, buildTime)
		},
	}
}
