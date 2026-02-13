package main

import (
	"os"
	"testing"
	"time"
)

func TestIsTerminal(t *testing.T) {
	// Create a temp file - definitely not a terminal
	f, err := os.CreateTemp("", "test-terminal-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if isTerminal(f) {
		t.Error("expected temp file to not be a terminal")
	}
}

func TestIsPackageFile(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected bool
	}{
		{
			name:     "zip file",
			content:  []byte{'P', 'K', 0x03, 0x04, 0x00, 0x00},
			expected: true,
		},
		{
			name:     "text file",
			content:  []byte("WORKFLOW test\nGOAL main\n"),
			expected: false,
		},
		{
			name:     "empty file",
			content:  []byte{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "test-*.bin")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())

			if _, err := f.Write(tt.content); err != nil {
				t.Fatal(err)
			}
			f.Close()

			got := isPackageFile(f.Name())
			if got != tt.expected {
				t.Errorf("isPackageFile() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsPackageFile_NonExistent(t *testing.T) {
	if isPackageFile("/nonexistent/path/file.zip") {
		t.Error("expected false for non-existent file")
	}
}

func TestParseRetryConfig(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries int
		backoffStr string
		wantMax    int
		wantBackoff time.Duration
	}{
		{
			name:       "defaults",
			maxRetries: 3,
			backoffStr: "",
			wantMax:    3,
			wantBackoff: 0,
		},
		{
			name:       "with backoff",
			maxRetries: 5,
			backoffStr: "30s",
			wantMax:    5,
			wantBackoff: 30 * time.Second,
		},
		{
			name:       "invalid backoff",
			maxRetries: 2,
			backoffStr: "invalid",
			wantMax:    2,
			wantBackoff: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseRetryConfig(tt.maxRetries, tt.backoffStr)
			if cfg.MaxRetries != tt.wantMax {
				t.Errorf("MaxRetries = %v, want %v", cfg.MaxRetries, tt.wantMax)
			}
			if cfg.MaxBackoff != tt.wantBackoff {
				t.Errorf("MaxBackoff = %v, want %v", cfg.MaxBackoff, tt.wantBackoff)
			}
		})
	}
}
