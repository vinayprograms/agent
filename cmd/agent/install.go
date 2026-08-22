package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/packaging"
)

// installOptions are the install command's flags.
type installOptions struct {
	Package string
	Target  string
	Key     string
	NoDeps  bool
	DryRun  bool
}

// newInstallCmd installs a package into the target directory.
func newInstallCmd() *cobra.Command {
	var opts installOptions
	cmd := &cobra.Command{
		Use:   "install <package>",
		Short: "Install a package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Package = args[0]
			return runInstall(cmd.OutOrStdout(), &opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Target, "target", "", "Installation target directory")
	f.StringVar(&opts.Key, "key", "", "Public key path for verification")
	f.BoolVar(&opts.NoDeps, "no-deps", false, "Skip dependency installation")
	f.BoolVar(&opts.DryRun, "dry-run", false, "Show what would be installed")
	return cmd
}

// runInstall installs a package.
func runInstall(w io.Writer, c *installOptions) error {
	target := c.Target
	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolving default install directory: %w", err)
		}
		target = filepath.Join(home, ".agent", "packages")
	}
	opts := packaging.InstallOptions{
		PackagePath: c.Package,
		TargetDir:   target,
		NoDeps:      c.NoDeps,
		DryRun:      c.DryRun,
	}

	if c.Key != "" {
		pubKey, err := packaging.LoadPublicKey(c.Key)
		if err != nil {
			return fmt.Errorf("loading public key: %w", err)
		}
		opts.PublicKey = pubKey
	}

	result, err := packaging.Install(opts)
	if err != nil {
		return fmt.Errorf("installing package: %w", err)
	}

	printInstallResult(w, result, opts)
	return nil
}

func printInstallResult(w io.Writer, result *packaging.InstallResult, opts packaging.InstallOptions) {
	if opts.DryRun {
		fmt.Fprintln(w, "Dry run - would install:")
		for _, name := range result.Installed {
			fmt.Fprintf(w, "  - %s\n", name)
		}
		if len(result.Dependencies) > 0 && !opts.NoDeps {
			fmt.Fprintln(w, "Dependencies:")
			for _, dep := range result.Dependencies {
				fmt.Fprintf(w, "  - %s\n", dep)
			}
		}
		return
	}

	fmt.Fprintf(w, "✓ Installed %s\n", strings.Join(result.Installed, ", "))
	fmt.Fprintf(w, "  Location: %s\n", result.InstallPath)
	if len(result.Dependencies) > 0 {
		if opts.NoDeps {
			fmt.Fprintln(w, "  Dependencies (skipped, --no-deps):")
		} else {
			fmt.Fprintln(w, "  Dependencies (require manual install):")
		}
		for _, dep := range result.Dependencies {
			fmt.Fprintf(w, "    - %s\n", dep)
		}
	}
}
