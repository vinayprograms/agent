package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vinayprograms/agent/internal/packaging"
)

// packAgent creates a signed agent package.
func packAgent(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: source directory required")
		os.Exit(1)
	}

	sourceDir := args[0]
	opts := parsePackArgs(args[1:], sourceDir)

	pkg, err := packaging.Pack(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating package: %v\n", err)
		os.Exit(1)
	}

	printPackResult(pkg, opts)
}

func parsePackArgs(args []string, sourceDir string) packaging.PackOptions {
	opts := packaging.PackOptions{SourceDir: sourceDir}
	var signKeyPath, authorName, authorEmail string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "--output" || arg == "-o") && i+1 < len(args):
			i++
			opts.OutputPath = args[i]
		case strings.HasPrefix(arg, "--output="):
			opts.OutputPath = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			opts.OutputPath = strings.TrimPrefix(arg, "-o=")
		case arg == "--sign" && i+1 < len(args):
			i++
			signKeyPath = args[i]
		case strings.HasPrefix(arg, "--sign="):
			signKeyPath = strings.TrimPrefix(arg, "--sign=")
		case arg == "--author" && i+1 < len(args):
			i++
			authorName = args[i]
		case strings.HasPrefix(arg, "--author="):
			authorName = strings.TrimPrefix(arg, "--author=")
		case arg == "--email" && i+1 < len(args):
			i++
			authorEmail = args[i]
		case strings.HasPrefix(arg, "--email="):
			authorEmail = strings.TrimPrefix(arg, "--email=")
		case arg == "--license" && i+1 < len(args):
			i++
			opts.License = args[i]
		case strings.HasPrefix(arg, "--license="):
			opts.License = strings.TrimPrefix(arg, "--license=")
		}
	}

	if authorName != "" || authorEmail != "" {
		opts.Author = &packaging.Author{
			Name:  authorName,
			Email: authorEmail,
		}
	}

	if signKeyPath != "" {
		privKey, err := packaging.LoadPrivateKey(signKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading signing key: %v\n", err)
			os.Exit(1)
		}
		opts.PrivateKey = privKey
	}

	return opts
}

func printPackResult(pkg *packaging.Package, opts packaging.PackOptions) {
	fmt.Printf("✓ Created %s\n", opts.OutputPath)
	fmt.Printf("  Name: %s\n", pkg.Manifest.Name)
	fmt.Printf("  Version: %s\n", pkg.Manifest.Version)
	if opts.PrivateKey != nil {
		fmt.Printf("  Signed: yes\n")
	} else {
		fmt.Printf("  Signed: no (use --sign to sign)\n")
	}
	if len(pkg.Manifest.Inputs) > 0 {
		fmt.Printf("  Inputs: %d\n", len(pkg.Manifest.Inputs))
	}
	if pkg.Manifest.Requires != nil && len(pkg.Manifest.Requires.Profiles) > 0 {
		fmt.Printf("  Requires profiles: %s\n", strings.Join(pkg.Manifest.Requires.Profiles, ", "))
	}
	if len(pkg.Manifest.Dependencies) > 0 {
		fmt.Printf("  Dependencies: %d\n", len(pkg.Manifest.Dependencies))
	}
}
