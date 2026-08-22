package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/packaging"
)

// packOptions are the pack command's flags.
type packOptions struct {
	Dir     string
	Output  string
	Sign    string
	Author  string
	Email   string
	License string
}

// newPackCmd creates a signed agent package from a directory.
func newPackCmd() *cobra.Command {
	var opts packOptions
	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Create a signed agent package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Dir = args[0]
			return runPack(&opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.Output, "output", "o", "", "Output package path")
	f.StringVar(&opts.Sign, "sign", "", "Private key path for signing")
	f.StringVar(&opts.Author, "author", "", "Author name")
	f.StringVar(&opts.Email, "email", "", "Author email")
	f.StringVar(&opts.License, "license", "", "License (MIT, Apache-2.0, etc.)")
	return cmd
}

// runPack creates a signed agent package.
func runPack(c *packOptions) error {
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
