// Package main defines the CLI structure using cobra.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// CLI holds the parsed state for all subcommands.
// Tests can call parseArgs to populate it.
type CLI struct {
	Run      RunCmd
	Serve    ServeCmd
	Validate ValidateCmd
	Inspect  InspectCmd
	Pack     PackCmd
	Verify   VerifyCmd
	Install  InstallCmd
	Keygen   KeygenCmd
	Setup    SetupCmd
	Replay   ReplayCmd
	Version  VersionCmd
}

// RunCmd executes a workflow from an Agentfile.
type RunCmd struct {
	Input     map[string]string
	Config    string
	Policy    string
	Workspace string
	Goal      string
	Debug     bool
	File      string
}

// ServeCmd runs the agent as a long-running service.
type ServeCmd struct {
	File      string
	Config    string
	Policy    string
	Workspace string
	State     string

	// Transport options
	HTTP string
	Bus  string

	// Service options
	QueueGroup   string
	Capability   string
	SessionLabel string

	// Swarm integration
	Type         string
	Capabilities string
}

// ValidateCmd validates an Agentfile.
type ValidateCmd struct {
	File string
}

// InspectCmd shows workflow or package structure.
type InspectCmd struct {
	Path string
}

// PackCmd creates a signed agent package.
type PackCmd struct {
	Dir     string
	Output  string
	Sign    string
	Author  string
	Email   string
	License string
}

// VerifyCmd verifies a package signature.
type VerifyCmd struct {
	Package string
	Key     string
}

// InstallCmd installs a package.
type InstallCmd struct {
	Package string
	Target  string
	Key     string
	NoDeps  bool
	DryRun  bool
}

// KeygenCmd generates a signing key pair.
type KeygenCmd struct {
	Output string
}

// SetupCmd runs the interactive setup wizard.
type SetupCmd struct{}

// ReplayCmd replays a session for analysis.
type ReplayCmd struct {
	Session string
	Verbose int
	NoPager bool
	Cost    []string
}

// VersionCmd shows version information.
type VersionCmd struct{}

// setPositionalArgs sets positional args on a CLI from cobra args in RunE callbacks.
// Each command builder calls this pattern: set struct field from args[0] if present.

// buildRunCmd creates the run subcommand and binds flags to cli.Run.
func buildRunCmd(cli *CLI, action func() error) *cobra.Command {
	cli.Run.File = "Agentfile"
	cmd := &cobra.Command{
		Use:   "run [file]",
		Short: "Run a workflow (one-shot, ephemeral)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				cli.Run.File = args[0]
			}
			if action != nil {
				return action()
			}
			return nil
		},
	}
	cmd.Flags().StringToStringVarP(&cli.Run.Input, "input", "i", nil, "Input key=value (repeatable)")
	cmd.Flags().StringVar(&cli.Run.Config, "config", "", "Config file path")
	cmd.Flags().StringVar(&cli.Run.Policy, "policy", "", "Policy file path")
	cmd.Flags().StringVar(&cli.Run.Workspace, "workspace", "", "Workspace directory")
	cmd.Flags().StringVar(&cli.Run.Goal, "goal", "", "Inline goal description (skips Agentfile)")
	cmd.Flags().BoolVar(&cli.Run.Debug, "debug", false, "Enable verbose logging (prompts, responses, tool outputs)")
	return cmd
}

// buildServeCmd creates the serve subcommand and binds flags to cli.Serve.
func buildServeCmd(cli *CLI, action func() error) *cobra.Command {
	cli.Serve.File = "Agentfile"
	cmd := &cobra.Command{
		Use:   "serve [file]",
		Short: "Run as a service agent (long-running)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				cli.Serve.File = args[0]
			}
			if action != nil {
				return action()
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cli.Serve.Config, "config", "", "Config file path")
	cmd.Flags().StringVar(&cli.Serve.Policy, "policy", "", "Policy file path")
	cmd.Flags().StringVar(&cli.Serve.Workspace, "workspace", "", "Workspace directory")
	cmd.Flags().StringVar(&cli.Serve.State, "state", "", "Override state location (isolate per-agent when needed)")
	cmd.Flags().StringVar(&cli.Serve.HTTP, "http", "", "Run HTTP server on this address (e.g., :8080)")
	cmd.Flags().StringVar(&cli.Serve.Bus, "bus", "", "Message bus URL (e.g., nats://localhost:4222)")
	cmd.Flags().StringVar(&cli.Serve.QueueGroup, "queue-group", "", "Queue group name for load balancing")
	cmd.Flags().StringVar(&cli.Serve.Capability, "capability", "", "Capability name (default: Agentfile NAME)")
	cmd.Flags().StringVar(&cli.Serve.SessionLabel, "session-label", "", "Label for session directory (default: Agentfile NAME)")
	cmd.Flags().StringVar(&cli.Serve.Type, "type", "", "Agent type: worker or manager (default: worker)")
	cmd.Flags().StringVar(&cli.Serve.Capabilities, "capabilities", "", "Worker capabilities for manager dispatch (format: cap1:n,cap2:n)")
	return cmd
}

// buildValidateCmd creates the validate subcommand.
func buildValidateCmd(cli *CLI, action func() error) *cobra.Command {
	cli.Validate.File = "Agentfile"
	cmd := &cobra.Command{
		Use:   "validate [file]",
		Short: "Validate Agentfile syntax",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				cli.Validate.File = args[0]
			}
			if action != nil {
				return action()
			}
			return nil
		},
	}
	return cmd
}

// buildInspectCmd creates the inspect subcommand.
func buildInspectCmd(cli *CLI, action func() error) *cobra.Command {
	cli.Inspect.Path = "Agentfile"
	cmd := &cobra.Command{
		Use:   "inspect [path]",
		Short: "Show workflow or package structure",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				cli.Inspect.Path = args[0]
			}
			if action != nil {
				return action()
			}
			return nil
		},
	}
	return cmd
}

// buildPackCmd creates the pack subcommand.
func buildPackCmd(cli *CLI, action func() error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Create a signed agent package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli.Pack.Dir = args[0]
			if action != nil {
				return action()
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&cli.Pack.Output, "output", "o", "", "Output package path")
	cmd.Flags().StringVar(&cli.Pack.Sign, "sign", "", "Private key path for signing")
	cmd.Flags().StringVar(&cli.Pack.Author, "author", "", "Author name")
	cmd.Flags().StringVar(&cli.Pack.Email, "email", "", "Author email")
	cmd.Flags().StringVar(&cli.Pack.License, "license", "", "License (MIT, Apache-2.0, etc.)")
	return cmd
}

// buildVerifyCmd creates the verify subcommand.
func buildVerifyCmd(cli *CLI, action func() error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <package>",
		Short: "Verify package signature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli.Verify.Package = args[0]
			if action != nil {
				return action()
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cli.Verify.Key, "key", "", "Public key path for verification")
	return cmd
}

// buildInstallCmd creates the install subcommand.
func buildInstallCmd(cli *CLI, action func() error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <package>",
		Short: "Install a package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli.Install.Package = args[0]
			if action != nil {
				return action()
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cli.Install.Target, "target", "", "Installation target directory")
	cmd.Flags().StringVar(&cli.Install.Key, "key", "", "Public key path for verification")
	cmd.Flags().BoolVar(&cli.Install.NoDeps, "no-deps", false, "Skip dependency installation")
	cmd.Flags().BoolVar(&cli.Install.DryRun, "dry-run", false, "Show what would be installed")
	return cmd
}

// buildKeygenCmd creates the keygen subcommand.
func buildKeygenCmd(cli *CLI, action func() error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate signing key pair",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if action != nil {
				return action()
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&cli.Keygen.Output, "output", "o", "agent-key", "Output path prefix (creates .pem and .pub)")
	return cmd
}

// buildSetupCmd creates the setup subcommand.
func buildSetupCmd(cli *CLI, action func() error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if action != nil {
				return action()
			}
			return nil
		},
	}
	return cmd
}

// buildReplayCmd creates the replay subcommand.
func buildReplayCmd(cli *CLI, action func() error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replay <session>",
		Short: "Replay session for forensic analysis",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli.Replay.Session = args[0]
			if action != nil {
				return action()
			}
			return nil
		},
	}
	cmd.Flags().CountVarP(&cli.Replay.Verbose, "verbose", "v", "Verbosity level (-v, -vv)")
	cmd.Flags().BoolVar(&cli.Replay.NoPager, "no-pager", false, "Disable pager for output")
	cmd.Flags().StringSliceVar(&cli.Replay.Cost, "cost", nil, "Model pricing: model:input,output (per 1M tokens). Repeatable.")
	return cmd
}

// buildVersionCmd creates the version subcommand.
func buildVersionCmd(cli *CLI, action func() error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if action != nil {
				return action()
			}
			return nil
		},
	}
	return cmd
}

// newRootCmd constructs the root command with all subcommands wired to real actions.
func newRootCmd() (*cobra.Command, *CLI) {
	cli := &CLI{}
	rctx := &runContext{creds: globalCreds}

	root := &cobra.Command{
		Use:           "agent",
		Short:         "Headless agent for running AI workflows",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		buildRunCmd(cli, func() error { return cli.Run.Run(rctx) }),
		buildServeCmd(cli, func() error { return cli.Serve.Run() }),
		buildValidateCmd(cli, func() error { return cli.Validate.Run(rctx) }),
		buildInspectCmd(cli, func() error { return cli.Inspect.Run(rctx) }),
		buildPackCmd(cli, func() error { return cli.Pack.Run(rctx) }),
		buildVerifyCmd(cli, func() error { return cli.Verify.Run(rctx) }),
		buildInstallCmd(cli, func() error { return cli.Install.Run(rctx) }),
		buildKeygenCmd(cli, func() error { return cli.Keygen.Run(rctx) }),
		buildSetupCmd(cli, func() error { return cli.Setup.Run(rctx) }),
		buildReplayCmd(cli, func() error { return cli.Replay.Run(rctx) }),
		buildVersionCmd(cli, func() error { return cli.Version.Run(rctx) }),
	)
	return root, cli
}

// parseTestRoot constructs a root command for parse-only testing (no actions executed).
func parseTestRoot() (*cobra.Command, *CLI) {
	cli := &CLI{}
	root := &cobra.Command{
		Use:           "agent",
		Short:         "Headless agent for running AI workflows",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		buildRunCmd(cli, nil),
		buildServeCmd(cli, nil),
		buildValidateCmd(cli, nil),
		buildInspectCmd(cli, nil),
		buildPackCmd(cli, nil),
		buildVerifyCmd(cli, nil),
		buildInstallCmd(cli, nil),
		buildKeygenCmd(cli, nil),
		buildSetupCmd(cli, nil),
		buildReplayCmd(cli, nil),
		buildVersionCmd(cli, nil),
	)
	return root, cli
}

// parseArgs parses command-line args for testing. Returns the populated CLI struct.
func parseArgs(args []string) (CLI, error) {
	root, cli := parseTestRoot()
	root.SetArgs(args)
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	err := root.Execute()
	return *cli, err
}

// printErrAndExit prints an error message and exits with code 1.
func printErrAndExit(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
