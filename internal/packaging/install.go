package packaging

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// InstallOptions configures package installation.
type InstallOptions struct {
	PackagePath string
	TargetDir   string // Required unless DryRun; the package lands in <TargetDir>/<name>/<version>.
	PublicKey   ed25519.PublicKey
	NoDeps      bool
	DryRun      bool
}

// InstallResult contains installation results.
type InstallResult struct {
	Installed    []string // Package names installed
	Dependencies []string // Dependency names (for display)
	InstallPath  string   // Where package was installed
}

// Install verifies a package and extracts it under opts.TargetDir.
func Install(opts InstallOptions) (*InstallResult, error) {
	if opts.TargetDir == "" && !opts.DryRun {
		return nil, errors.New("install target directory is required")
	}
	pkg, err := Load(opts.PackagePath)
	if err != nil {
		return nil, err
	}

	// Verify
	if err := Verify(pkg, opts.PublicKey); err != nil {
		return nil, fmt.Errorf("verification failed: %w", err)
	}

	result := &InstallResult{
		Installed: []string{pkg.Manifest.Name},
	}

	if !opts.NoDeps {
		result.Dependencies = slices.Sorted(maps.Keys(pkg.Manifest.Dependencies))
	}

	if opts.DryRun {
		return result, nil
	}

	pkgDir := filepath.Join(opts.TargetDir, pkg.Manifest.Name, pkg.Manifest.Version)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create package directory: %w", err)
	}

	// Extract content
	if err := extractContent(pkg.Content, pkgDir); err != nil {
		return nil, fmt.Errorf("extracting %q: %w", pkgDir, err)
	}

	result.InstallPath = pkgDir

	return result, nil
}
