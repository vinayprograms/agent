package main

import (
	"testing"
)

func TestParseInstallArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		pkgPath    string
		wantTarget string
		wantNoDeps bool
		wantDryRun bool
	}{
		{
			name:    "minimal",
			args:    []string{},
			pkgPath: "test.agent",
		},
		{
			name:       "with target",
			args:       []string{"--target", "/install"},
			pkgPath:    "test.agent",
			wantTarget: "/install",
		},
		{
			name:       "no deps",
			args:       []string{"--no-deps"},
			pkgPath:    "test.agent",
			wantNoDeps: true,
		},
		{
			name:       "dry run",
			args:       []string{"--dry-run"},
			pkgPath:    "test.agent",
			wantDryRun: true,
		},
		{
			name:       "all flags",
			args:       []string{"--target=/install", "--no-deps", "--dry-run"},
			pkgPath:    "test.agent",
			wantTarget: "/install",
			wantNoDeps: true,
			wantDryRun: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := parseInstallArgs(tt.args, tt.pkgPath)
			if opts.PackagePath != tt.pkgPath {
				t.Errorf("PackagePath = %q, want %q", opts.PackagePath, tt.pkgPath)
			}
			if opts.TargetDir != tt.wantTarget {
				t.Errorf("TargetDir = %q, want %q", opts.TargetDir, tt.wantTarget)
			}
			if opts.NoDeps != tt.wantNoDeps {
				t.Errorf("NoDeps = %v, want %v", opts.NoDeps, tt.wantNoDeps)
			}
			if opts.DryRun != tt.wantDryRun {
				t.Errorf("DryRun = %v, want %v", opts.DryRun, tt.wantDryRun)
			}
		})
	}
}
