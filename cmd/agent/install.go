package main

import (
	"fmt"
	"strings"

	"github.com/vinayprograms/agent/internal/packaging"
)

// runInstall installs a package.
func runInstall(c *InstallCmd) error {
	opts := packaging.InstallOptions{
		PackagePath: c.Package,
		TargetDir:   c.Target,
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

	printInstallResult(result, opts)
	return nil
}

func printInstallResult(result *packaging.InstallResult, opts packaging.InstallOptions) {
	if opts.DryRun {
		fmt.Println("Dry run - would install:")
		for _, name := range result.Installed {
			fmt.Printf("  - %s\n", name)
		}
		if len(result.Dependencies) > 0 && !opts.NoDeps {
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
		if opts.NoDeps {
			fmt.Println("  Dependencies (skipped, --no-deps):")
		} else {
			fmt.Println("  Dependencies (require manual install):")
		}
		for _, dep := range result.Dependencies {
			fmt.Printf("    - %s\n", dep)
		}
	}
}
