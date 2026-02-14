// Package main defines the CLI structure using kong.
package main

import "github.com/alecthomas/kong"

// CLI defines the command-line interface.
type CLI struct {
	Run      RunCmd      `cmd:"" help:"Run a workflow"`
	Validate ValidateCmd `cmd:"" help:"Validate Agentfile syntax"`
	Inspect  InspectCmd  `cmd:"" help:"Show workflow or package structure"`
	Pack     PackCmd     `cmd:"" help:"Create a signed agent package"`
	Verify   VerifyCmd   `cmd:"" help:"Verify package signature"`
	Install  InstallCmd  `cmd:"" help:"Install a package"`
	Keygen   KeygenCmd   `cmd:"" help:"Generate signing key pair"`
	Setup    SetupCmd    `cmd:"" help:"Interactive setup wizard"`
	Replay   ReplayCmd   `cmd:"" help:"Replay session for forensic analysis"`
	Version  VersionCmd  `cmd:"" help:"Show version information"`
}

// RunCmd executes a workflow from an Agentfile.
type RunCmd struct {
	File            string            `short:"f" default:"Agentfile" help:"Agentfile path"`
	Input           map[string]string `short:"i" help:"Input key=value (repeatable)"`
	Config          string            `help:"Config file path"`
	Policy          string            `help:"Policy file path"`
	Workspace       string            `help:"Workspace directory"`
	PersistMemory   bool              `help:"Enable persistent memory (overrides config)"`
	NoPersistMemory bool              `help:"Disable persistent memory (overrides config)"`
}

// ValidateCmd validates an Agentfile.
type ValidateCmd struct {
	File string `arg:"" optional:"" default:"Agentfile" help:"Agentfile path"`
}

// InspectCmd shows workflow or package structure.
type InspectCmd struct {
	Path string `arg:"" optional:"" default:"Agentfile" help:"Agentfile or package path"`
}

// PackCmd creates a signed agent package.
type PackCmd struct {
	Dir     string `arg:"" help:"Directory containing Agentfile"`
	Output  string `short:"o" help:"Output package path"`
	Sign    string `help:"Private key path for signing"`
	Author  string `help:"Author name"`
	Email   string `help:"Author email"`
	License string `help:"License (MIT, Apache-2.0, etc.)"`
}

// VerifyCmd verifies a package signature.
type VerifyCmd struct {
	Package string `arg:"" help:"Package file to verify"`
	Key     string `help:"Public key path for verification"`
}

// InstallCmd installs a package.
type InstallCmd struct {
	Package string `arg:"" help:"Package file to install"`
	Target  string `help:"Installation target directory"`
	Key     string `help:"Public key path for verification"`
	NoDeps  bool   `help:"Skip dependency installation"`
	DryRun  bool   `help:"Show what would be installed"`
}

// KeygenCmd generates a signing key pair.
type KeygenCmd struct {
	Output string `short:"o" default:"agent-key" help:"Output path prefix (creates .pem and .pub)"`
}

// SetupCmd runs the interactive setup wizard.
type SetupCmd struct{}

// ReplayCmd replays a session for analysis.
type ReplayCmd struct {
	Session string   `arg:"" help:"Session file(s) to replay (supports glob patterns)"`
	Verbose int      `short:"v" type:"counter" help:"Verbosity level (-v, -vv)"`
	NoPager bool     `help:"Disable pager for output"`
	Cost    []string `help:"Model pricing: model:input,output (per 1M tokens). Repeatable." placeholder:"MODEL:IN,OUT"`
}

// VersionCmd shows version information.
type VersionCmd struct{}

// kongVars returns variables for kong (version info).
func kongVars() kong.Vars {
	return kong.Vars{
		"version": version,
	}
}
