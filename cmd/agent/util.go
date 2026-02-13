package main

import (
	"os"
	"time"

	"github.com/vinayprograms/agentkit/llm"
)

// isTerminal checks if the given file is a terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// isPackageFile checks if a file is a zip package (not a text Agentfile).
func isPackageFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Check for zip magic bytes (PK\x03\x04)
	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil {
		return false
	}
	return magic[0] == 'P' && magic[1] == 'K' && magic[2] == 0x03 && magic[3] == 0x04
}

// parseRetryConfig converts config values to RetryConfig.
func parseRetryConfig(maxRetries int, backoffStr string) llm.RetryConfig {
	cfg := llm.RetryConfig{
		MaxRetries: maxRetries,
	}
	if backoffStr != "" {
		if d, err := time.ParseDuration(backoffStr); err == nil {
			cfg.MaxBackoff = d
		}
	}
	return cfg
}
