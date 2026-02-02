package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_Help(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI help failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "Usage:") {
		t.Error("expected usage in help output")
	}
	if !strings.Contains(string(output), "run") {
		t.Error("expected 'run' command in help")
	}
}

func TestCLI_Version(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI version failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "agent version") {
		t.Error("expected version output")
	}
}

func TestCLI_Validate(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid Agentfile
	agentfile := `NAME test
INPUT topic DEFAULT "golang"
GOAL analyze "Analyze $topic"
RUN main USING analyze
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfile), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	cmd := exec.Command("go", "run", ".", "validate", agentfilePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI validate failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "Valid") {
		t.Error("expected 'Valid' in output")
	}
}

func TestCLI_Inspect(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid Agentfile
	agentfile := `NAME test-workflow
INPUT topic
INPUT max DEFAULT 10
GOAL analyze "Analyze $topic"
GOAL summarize "Summarize results"
RUN step1 USING analyze
LOOP step2 USING summarize WITHIN $max
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfile), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	cmd := exec.Command("go", "run", ".", "inspect", agentfilePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI inspect failed: %v\n%s", err, output)
	}

	outStr := string(output)
	if !strings.Contains(outStr, "test-workflow") {
		t.Error("expected workflow name")
	}
	if !strings.Contains(outStr, "topic") {
		t.Error("expected input 'topic'")
	}
	if !strings.Contains(outStr, "analyze") {
		t.Error("expected goal 'analyze'")
	}
	if !strings.Contains(outStr, "RUN") {
		t.Error("expected RUN step")
	}
	if !strings.Contains(outStr, "LOOP") {
		t.Error("expected LOOP step")
	}
}

func TestCLI_ValidateInvalid(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an invalid Agentfile (missing NAME)
	agentfile := `GOAL analyze "Analyze something"
RUN main USING analyze
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfile), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	cmd := exec.Command("go", "run", ".", "validate", agentfilePath)
	output, _ := cmd.CombinedOutput()

	// Should fail (non-zero exit)
	if strings.Contains(string(output), "Valid") {
		t.Error("expected validation to fail for invalid Agentfile")
	}
}
