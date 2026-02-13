package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vinayprograms/agent/internal/packaging"
)

// installPackage installs a package.
func installPackage(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: package path required")
		os.Exit(1)
	}

	pkgPath := args[0]
	opts := parseInstallArgs(args[1:], pkgPath)

	result, err := packaging.Install(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error installing package: %v\n", err)
		os.Exit(1)
	}

	printInstallResult(result, opts)
}

func parseInstallArgs(args []string, pkgPath string) packaging.InstallOptions {
	opts := packaging.InstallOptions{PackagePath: pkgPath}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--key" && i+1 < len(args):
			i++
			pubKey, err := packaging.LoadPublicKey(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error loading public key: %v\n", err)
				os.Exit(1)
			}
			opts.PublicKey = pubKey
		case strings.HasPrefix(arg, "--key="):
			pubKey, err := packaging.LoadPublicKey(strings.TrimPrefix(arg, "--key="))
			if err != nil {
				fmt.Fprintf(os.Stderr, "error loading public key: %v\n", err)
				os.Exit(1)
			}
			opts.PublicKey = pubKey
		case arg == "--target" && i+1 < len(args):
			i++
			opts.TargetDir = args[i]
		case strings.HasPrefix(arg, "--target="):
			opts.TargetDir = strings.TrimPrefix(arg, "--target=")
		case arg == "--no-deps":
			opts.NoDeps = true
		case arg == "--dry-run":
			opts.DryRun = true
		}
	}

	return opts
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
