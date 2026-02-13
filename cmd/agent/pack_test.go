package main

import (
	"testing"
)

func TestParsePackArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sourceDir string
		wantOut   string
		wantLic   string
	}{
		{
			name:      "minimal",
			args:      []string{},
			sourceDir: "/src",
		},
		{
			name:      "with output",
			args:      []string{"--output", "pkg.agent"},
			sourceDir: "/src",
			wantOut:   "pkg.agent",
		},
		{
			name:      "with output equals",
			args:      []string{"--output=pkg.agent"},
			sourceDir: "/src",
			wantOut:   "pkg.agent",
		},
		{
			name:      "short output",
			args:      []string{"-o", "pkg.agent"},
			sourceDir: "/src",
			wantOut:   "pkg.agent",
		},
		{
			name:      "with license",
			args:      []string{"--license", "MIT"},
			sourceDir: "/src",
			wantLic:   "MIT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := parsePackArgs(tt.args, tt.sourceDir)
			if opts.SourceDir != tt.sourceDir {
				t.Errorf("SourceDir = %q, want %q", opts.SourceDir, tt.sourceDir)
			}
			if opts.OutputPath != tt.wantOut {
				t.Errorf("OutputPath = %q, want %q", opts.OutputPath, tt.wantOut)
			}
			if opts.License != tt.wantLic {
				t.Errorf("License = %q, want %q", opts.License, tt.wantLic)
			}
		})
	}
}
