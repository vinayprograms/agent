// Package failure contains tests that verify graceful handling of failures.
package failure

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/headless-agent/internal/agentfile"
	"github.com/openclaw/headless-agent/internal/executor"
	"github.com/openclaw/headless-agent/internal/llm"
	"github.com/openclaw/headless-agent/internal/policy"
	"github.com/openclaw/headless-agent/internal/tools"
)

// TestFailure_LLMError tests handling of LLM API errors.
func TestFailure_LLMError(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "llm-error-test",
		Goals: []agentfile.Goal{
			{Name: "task", Outcome: "Do something"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"task"}},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetError(errors.New("API rate limit exceeded"))

	exec := executor.NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), nil)

	if err == nil {
		t.Error("expected error from LLM failure")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
	if result.Status != executor.StatusFailed {
		t.Errorf("expected Failed status, got %s", result.Status)
	}
}

// TestFailure_ToolError tests handling of tool execution errors.
func TestFailure_ToolError(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "tool-error-test",
		Goals: []agentfile.Goal{
			{Name: "read_file", Outcome: "Read a file"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"read_file"}},
		},
	}

	// Provider that requests reading a non-existent file
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		for _, msg := range req.Messages {
			if msg.Role == "tool" {
				// Should contain error message
				if !strings.Contains(msg.Content, "Error") {
					t.Error("expected error in tool result")
				}
				return &llm.ChatResponse{Content: "File not found, handled gracefully"}, nil
			}
		}
		return &llm.ChatResponse{
			ToolCalls: []llm.ToolCallResponse{
				{ID: "tc1", Name: "read", Args: map[string]interface{}{
					"path": "/nonexistent/file.txt",
				}},
			},
		}, nil
	}

	pol := policy.New()
	pol.Workspace = t.TempDir()
	pol.Tools["read"] = &policy.ToolPolicy{Enabled: true, Allow: []string{"**"}}
	registry := tools.NewRegistry(pol)

	exec := executor.NewExecutor(wf, provider, registry, pol)
	result, err := exec.Run(context.Background(), nil)

	// Should complete despite tool error (error reported to LLM)
	if err != nil {
		t.Errorf("unexpected fatal error: %v", err)
	}
	if result.Status != executor.StatusComplete {
		t.Errorf("expected Complete (tool errors are recoverable), got %s", result.Status)
	}
}

// TestFailure_ContextCancellation tests handling of context cancellation.
func TestFailure_ContextCancellation(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "cancel-test",
		Goals: []agentfile.Goal{
			{Name: "slow_task", Outcome: "Do something slow"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"slow_task"}},
		},
	}

	// Provider that blocks until context is cancelled
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return &llm.ChatResponse{Content: "Done"}, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	exec := executor.NewExecutor(wf, provider, nil, nil)
	_, err := exec.Run(ctx, nil)

	if err == nil {
		t.Error("expected context cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context error, got: %v", err)
	}
}

// TestFailure_MissingInput tests handling of missing required inputs.
func TestFailure_MissingInput(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "input-test",
		Inputs: []agentfile.Input{
			{Name: "required_param"}, // No default = required
		},
		Goals: []agentfile.Goal{
			{Name: "task", Outcome: "Use $required_param"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"task"}},
		},
	}

	provider := llm.NewMockProvider()
	exec := executor.NewExecutor(wf, provider, nil, nil)

	_, err := exec.Run(context.Background(), nil) // No inputs provided

	if err == nil {
		t.Error("expected error for missing input")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("expected 'required' in error, got: %v", err)
	}
}

// TestFailure_InvalidAgentfile tests handling of invalid Agentfile.
func TestFailure_InvalidAgentfile(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		content string
	}{
		{"syntax error", "NAME test\nGOAL broken \"unclosed string\n"},
		{"invalid keyword", "NAME test\nINVALID foo\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, "Agentfile-"+tt.name)
			os.WriteFile(path, []byte(tt.content), 0644)

			_, err := agentfile.LoadFile(path)
			if err == nil {
				t.Error("expected parse error")
			}
		})
	}
}

// TestFailure_FileSystemError tests handling of filesystem errors.
func TestFailure_FileSystemError(t *testing.T) {
	// Test reading from non-existent file
	_, err := agentfile.LoadFile("/nonexistent/path/Agentfile")
	if err == nil {
		t.Error("expected error for non-existent file")
	}

	// Test with permission denied (if possible)
	tmpDir := t.TempDir()
	restrictedFile := filepath.Join(tmpDir, "restricted.txt")
	os.WriteFile(restrictedFile, []byte("test"), 0644)

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.DefaultDeny = true // No allow patterns = all denied
	registry := tools.NewRegistry(pol)
	readTool := registry.Get("read")

	_, err = readTool.Execute(context.Background(), map[string]interface{}{
		"path": restrictedFile,
	})
	if err == nil {
		t.Error("expected policy denial")
	}
}

// TestFailure_RecoveryFromPartialExecution tests recovery behavior.
func TestFailure_RecoveryFromPartialExecution(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "recovery-test",
		Goals: []agentfile.Goal{
			{Name: "step1", Outcome: "First step"},
			{Name: "step2", Outcome: "Second step (fails)"},
			{Name: "step3", Outcome: "Third step (never reached)"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"step1", "step2", "step3"}},
		},
	}

	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		if callCount == 2 {
			return nil, errors.New("simulated failure on step 2")
		}
		return &llm.ChatResponse{Content: "Done"}, nil
	}

	exec := executor.NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), nil)

	if err == nil {
		t.Error("expected error from step 2")
	}
	if result.Status != executor.StatusFailed {
		t.Errorf("expected Failed status, got %s", result.Status)
	}
	
	// Step 3 should not have been reached
	if _, ok := result.Outputs["step3"]; ok {
		t.Error("step3 should not have been executed")
	}
	
	// Verify exactly 2 LLM calls were made (step1 succeeded, step2 failed)
	if callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", callCount)
	}
}

// TestFailure_EmptyWorkflow tests handling of empty workflow.
func TestFailure_EmptyWorkflow(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:  "empty",
		Steps: []agentfile.Step{}, // No steps
	}

	err := agentfile.Validate(wf)
	if err == nil {
		t.Error("expected validation error for empty workflow")
	}
}

// TestFailure_GracefulDegradation tests that the system degrades gracefully.
func TestFailure_GracefulDegradation(t *testing.T) {
	// Test with nil registry (no tools available)
	wf := &agentfile.Workflow{
		Name: "no-tools-test",
		Goals: []agentfile.Goal{
			{Name: "task", Outcome: "Do something"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"task"}},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done without tools")

	// nil registry should work (no tools available)
	exec := executor.NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), nil)

	if err != nil {
		t.Errorf("unexpected error with nil registry: %v", err)
	}
	if result.Status != executor.StatusComplete {
		t.Errorf("expected Complete, got %s", result.Status)
	}
}
