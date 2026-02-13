package main

// Setup tests are in internal/setup/setup_test.go
// This file exists for coverage completeness.

import "testing"

func TestRunSetup_Integration(t *testing.T) {
	// Integration test would require mocking stdin/stdout
	// which is covered in internal/setup tests
	t.Skip("setup is tested in internal/setup package")
}
