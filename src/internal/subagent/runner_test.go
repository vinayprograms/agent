// Package subagent tests.
package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/headless-agent/internal/agentfile"
	"github.com/openclaw/headless-agent/internal/llm"
)

func TestNewRunner(t *testing.T) {
	factory := llm.NewSingleProviderFactory(llm.NewMockProvider())
	runner := NewRunner(factory, []string{"/tmp/packages"})

	if runner == nil {
		t.Fatal("expected runner to be created")
	}
	if runner.providerFactory == nil {
		t.Error("expected provider factory to be set")
	}
	if len(runner.packagePaths) != 1 {
		t.Error("expected package paths to be set")
	}
}

func TestRunner_Callbacks(t *testing.T) {
	factory := llm.NewSingleProviderFactory(llm.NewMockProvider())
	runner := NewRunner(factory, nil)

	var startCalled, errorCalled bool
	runner.OnSubAgentStart = func(name string, input map[string]string) {
		startCalled = true
	}
	runner.OnSubAgentError = func(name string, err error) {
		errorCalled = true
	}

	// Try to spawn with non-existent package - will error
	agent := &agentfile.Agent{
		Name:     "test-agent",
		FromPath: "nonexistent-package",
	}

	result, _ := runner.SpawnOne(context.Background(), agent, nil)

	if !startCalled {
		t.Error("expected OnSubAgentStart to be called")
	}
	if !errorCalled {
		t.Error("expected OnSubAgentError to be called")
	}
	if result.Error == nil {
		t.Error("expected error for non-existent package")
	}
}

func TestRunner_SpawnParallel(t *testing.T) {
	factory := llm.NewSingleProviderFactory(llm.NewMockProvider())
	runner := NewRunner(factory, nil)

	agents := []*agentfile.Agent{
		{Name: "agent1", FromPath: "pkg1"},
		{Name: "agent2", FromPath: "pkg2"},
		{Name: "agent3", FromPath: "pkg3"},
	}

	results, _ := runner.SpawnParallel(context.Background(), agents, nil)

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// All should error since packages don't exist
	for _, r := range results {
		if r.Error == nil {
			t.Errorf("expected error for agent %s", r.Name)
		}
	}
}

func TestIsolatedEnv_Cleanup(t *testing.T) {
	cleanupCalled := 0
	env := &IsolatedEnv{
		cleanupFuncs: []func(){
			func() { cleanupCalled++ },
			func() { cleanupCalled++ },
		},
	}

	env.Cleanup()

	if cleanupCalled != 2 {
		t.Errorf("expected 2 cleanup calls, got %d", cleanupCalled)
	}
}

func TestReplaceVar(t *testing.T) {
	tests := []struct {
		input    string
		name     string
		value    string
		expected string
	}{
		{"Hello $name", "name", "world", "Hello world"},
		{"$a and $a", "a", "x", "x and x"},
		{"No vars", "x", "y", "No vars"},
		{"$topic is $topic", "topic", "Go", "Go is Go"},
	}

	for _, tt := range tests {
		result := replaceVar(tt.input, tt.name, tt.value)
		if result != tt.expected {
			t.Errorf("replaceVar(%q, %q, %q) = %q, want %q",
				tt.input, tt.name, tt.value, result, tt.expected)
		}
	}
}

func TestSplitMCPToolName(t *testing.T) {
	tests := []struct {
		name     string
		expected []string
	}{
		{"mcp_filesystem_read_file", []string{"filesystem", "read_file"}},
		{"mcp_memory_store", []string{"memory", "store"}},
		{"mcp_x_y", []string{"x", "y"}},
		{"mcp_nounder", nil},
	}

	for _, tt := range tests {
		result := splitMCPToolName(tt.name)
		if tt.expected == nil {
			if result != nil {
				t.Errorf("splitMCPToolName(%q) = %v, want nil", tt.name, result)
			}
		} else if len(result) != len(tt.expected) {
			t.Errorf("splitMCPToolName(%q) = %v, want %v", tt.name, result, tt.expected)
		} else {
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("splitMCPToolName(%q)[%d] = %q, want %q",
						tt.name, i, result[i], tt.expected[i])
				}
			}
		}
	}
}

func TestRunner_LoadPackage(t *testing.T) {
	runner := NewRunner(nil, []string{"/tmp/nonexistent"})

	_, err := runner.loadPackage("test-pkg")
	if err == nil {
		t.Error("expected error for non-existent package")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestIsolatedEnv_Execute_NoGoals(t *testing.T) {
	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	env := &IsolatedEnv{
		workflow: &agentfile.Workflow{
			Name:  "empty",
			Goals: []agentfile.Goal{}, // No goals
		},
		provider: provider,
	}

	_, err := env.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for workflow with no goals")
	}
	if !strings.Contains(err.Error(), "no goals") {
		t.Errorf("expected 'no goals' error, got: %v", err)
	}
}

func TestIsolatedEnv_Execute_Simple(t *testing.T) {
	provider := llm.NewMockProvider()
	provider.SetResponse("Task completed successfully")

	env := &IsolatedEnv{
		workflow: &agentfile.Workflow{
			Name: "simple",
			Goals: []agentfile.Goal{
				{Name: "main", Outcome: "Complete the task for $topic"},
			},
		},
		provider: provider,
	}

	result, err := env.Execute(context.Background(), map[string]string{"topic": "testing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Task completed successfully" {
		t.Errorf("expected 'Task completed successfully', got: %q", result)
	}

	// Verify input was interpolated
	req := provider.LastRequest()
	if !strings.Contains(req.Messages[1].Content, "testing") {
		t.Error("expected topic to be interpolated in task")
	}
}

func TestSubAgentResult(t *testing.T) {
	result := &SubAgentResult{
		Name:   "test-agent",
		Output: "result output",
		Error:  nil,
	}

	if result.Name != "test-agent" {
		t.Error("expected name to match")
	}
	if result.Output != "result output" {
		t.Error("expected output to match")
	}
}

// Integration test with real package structure
func TestRunner_WithRealPackage(t *testing.T) {
	// Create a minimal package structure
	tmpDir := t.TempDir()

	// Create agent package directory
	pkgDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(pkgDir, 0755)

	// Write Agentfile
	agentfile := `NAME test-agent
GOAL main "Complete the task: $task"
RUN main USING main
`
	os.WriteFile(filepath.Join(pkgDir, "Agentfile"), []byte(agentfile), 0644)

	// Write minimal config
	config := `{"llm": {"provider": "mock"}}`
	os.WriteFile(filepath.Join(pkgDir, "agent.json"), []byte(config), 0644)

	// Note: This test would need an actual .agent package file to work fully
	// Here we just verify the structure is recognized
	_, err := os.Stat(filepath.Join(pkgDir, "Agentfile"))
	if err != nil {
		t.Fatal("failed to create test package")
	}
}
