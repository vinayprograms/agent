package agentfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R1.3.1: Verify all agents referenced in USING clauses are defined
func TestValidation_UndefinedAgent(t *testing.T) {
	input := `NAME test
GOAL analyze "test" USING undefined_agent`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	err = Validate(wf)
	if err == nil {
		t.Fatal("expected validation error for undefined agent")
	}
	if !strings.Contains(err.Error(), "undefined_agent") {
		t.Errorf("error should mention undefined agent: %v", err)
	}
}

// R1.3.2: Verify all goals referenced in RUN/LOOP are defined
func TestValidation_UndefinedGoal(t *testing.T) {
	input := `NAME test
GOAL analyze "test"
RUN setup USING analyze, undefined_goal`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	err = Validate(wf)
	if err == nil {
		t.Fatal("expected validation error for undefined goal")
	}
	if !strings.Contains(err.Error(), "undefined_goal") {
		t.Errorf("error should mention undefined goal: %v", err)
	}
}

// R1.3.3: Verify goals are defined before use (in file order)
func TestValidation_GoalDefinedBeforeUse(t *testing.T) {
	// This is valid - goal defined before RUN
	validInput := `NAME test
GOAL analyze "test"
RUN setup USING analyze`

	wf, err := ParseString(validInput)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	err = Validate(wf)
	if err != nil {
		t.Errorf("should be valid: %v", err)
	}
}

// R1.3.6: Verify NAME is specified exactly once
func TestValidation_MissingName(t *testing.T) {
	input := `INPUT feature_request
GOAL analyze "test"
RUN setup USING analyze`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	err = Validate(wf)
	if err == nil {
		t.Fatal("expected validation error for missing NAME")
	}
	if !strings.Contains(err.Error(), "NAME") {
		t.Errorf("error should mention NAME: %v", err)
	}
}

// R1.3.7: Verify at least one RUN or LOOP step exists
func TestValidation_NoSteps(t *testing.T) {
	input := `NAME test
GOAL analyze "test"`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	err = Validate(wf)
	if err == nil {
		t.Fatal("expected validation error for no steps")
	}
	if !strings.Contains(err.Error(), "step") || !strings.Contains(err.Error(), "RUN") {
		t.Errorf("error should mention RUN/LOOP step: %v", err)
	}
}

// Test valid workflow passes validation
func TestValidation_ValidWorkflow(t *testing.T) {
	input := `NAME test
INPUT feature_request
AGENT creative FROM agents/creative.md
GOAL analyze "test" USING creative
GOAL run_tests "run tests"
RUN setup USING analyze
LOOP impl USING run_tests WITHIN 10`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// We skip FROM path validation in this test
	err = ValidateWithoutPaths(wf)
	if err != nil {
		t.Errorf("should be valid: %v", err)
	}
}

// R1.4.1: Load external prompt files
func TestFileLoading_LoadFromPaths(t *testing.T) {
	// Create temp directory with files
	tmpDir := t.TempDir()

	// Create agent prompt file
	agentPath := filepath.Join(tmpDir, "agents", "creative.md")
	os.MkdirAll(filepath.Dir(agentPath), 0755)
	os.WriteFile(agentPath, []byte("You are a creative agent."), 0644)

	// Create goal prompt file
	goalPath := filepath.Join(tmpDir, "goals", "analyze.md")
	os.MkdirAll(filepath.Dir(goalPath), 0755)
	os.WriteFile(goalPath, []byte("Analyze the input thoroughly."), 0644)

	// Create Agentfile
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	os.WriteFile(agentfilePath, []byte(`NAME test
AGENT creative FROM agents/creative.md
GOAL analyze FROM goals/analyze.md
RUN setup USING analyze`), 0644)

	// Load and resolve
	wf, err := LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	// Verify loaded content (this requires the loader to populate content)
	// For now, just verify it parsed correctly
	if wf.Name != "test" {
		t.Errorf("name wrong: %s", wf.Name)
	}
}

// R1.4.4: Report file not found errors with context
func TestFileLoading_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	os.WriteFile(agentfilePath, []byte(`NAME test
AGENT creative FROM agents/missing.md
GOAL analyze "test"
RUN setup USING analyze`), 0644)

	_, err := LoadFile(agentfilePath)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "missing.md") {
		t.Errorf("error should mention missing file: %v", err)
	}
}

// R1.4.2: Resolve paths relative to Agentfile location
func TestFileLoading_RelativePaths(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "workflows", "feature")
	os.MkdirAll(subDir, 0755)

	// Create agent file relative to workflow
	agentPath := filepath.Join(subDir, "agents", "creative.md")
	os.MkdirAll(filepath.Dir(agentPath), 0755)
	os.WriteFile(agentPath, []byte("Creative agent prompt"), 0644)

	// Create Agentfile in subdirectory
	agentfilePath := filepath.Join(subDir, "Agentfile")
	os.WriteFile(agentfilePath, []byte(`NAME test
AGENT creative FROM agents/creative.md
GOAL analyze "test"
RUN setup USING analyze`), 0644)

	wf, err := LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if wf.Name != "test" {
		t.Errorf("name wrong: %s", wf.Name)
	}
}
