package main

import (
	"fmt"
	"strings"

	"github.com/vinayprograms/agent/internal/packaging"
)

// runPack creates a signed agent package.
func runPack(c *PackCmd) error {
	opts := packaging.PackOptions{
		SourceDir:  c.Dir,
		OutputPath: c.Output,
		License:    c.License,
	}

	if c.Author != "" || c.Email != "" {
		opts.Author = &packaging.Author{
			Name:  c.Author,
			Email: c.Email,
		}
	}

	if c.Sign != "" {
		privKey, err := packaging.LoadPrivateKey(c.Sign)
		if err != nil {
			return fmt.Errorf("loading signing key: %w", err)
		}
		opts.PrivateKey = privKey
	}

	pkg, err := packaging.Pack(opts)
	if err != nil {
		return fmt.Errorf("creating package: %w", err)
	}

	printPackResult(pkg, opts)
	return nil
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
