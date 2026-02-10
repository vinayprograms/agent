// Package integration contains integration tests that verify
// multiple packages working together.
package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/llm"
	"github.com/vinayprograms/agent/internal/policy"
	"github.com/vinayprograms/agent/internal/tools"
)

// TestParserToExecutor tests the full flow from parsing to execution.
func TestParserToExecutor(t *testing.T) {
	tmpDir := t.TempDir()

	// Create Agentfile
	agentfileContent := `NAME integration-test
INPUT topic DEFAULT "testing"
GOAL analyze "Analyze $topic and provide insights"
GOAL summarize "Summarize the analysis"
RUN main USING analyze, summarize
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfileContent), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	// Parse
	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if wf.Name != "integration-test" {
		t.Errorf("expected name 'integration-test', got %s", wf.Name)
	}

	// Setup mock provider
	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		if callCount == 1 {
			return &llm.ChatResponse{Content: "Analysis: testing is important"}, nil
		}
		return &llm.ChatResponse{Content: "Summary: tests matter"}, nil
	}

	// Execute
	pol := policy.New()
	pol.Workspace = tmpDir
	registry := tools.NewRegistry(pol)

	exec := executor.NewExecutor(wf, provider, registry, pol)
	result, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Status != executor.StatusComplete {
		t.Errorf("expected status Complete, got %s", result.Status)
	}
	if callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", callCount)
	}
	if result.Outputs["analyze"] != "Analysis: testing is important" {
		t.Errorf("unexpected analyze output: %s", result.Outputs["analyze"])
	}
}

// TestToolExecution tests that tools are executed correctly during workflow.
func TestToolExecution(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Create Agentfile
	agentfileContent := `NAME tool-test
GOAL read_file "Read the file at ` + testFile + `"
RUN main USING read_file
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfileContent), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	// Mock provider that uses read tool
	toolCalled := false
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		// First call: request tool
		for _, msg := range req.Messages {
			if msg.Role == "tool" {
				toolCalled = true
				return &llm.ChatResponse{Content: "File content: " + msg.Content}, nil
			}
		}
		return &llm.ChatResponse{
			ToolCalls: []llm.ToolCallResponse{
				{ID: "tc1", Name: "read", Args: map[string]interface{}{"path": testFile}},
			},
		}, nil
	}

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.Tools["read"] = &policy.ToolPolicy{Enabled: true, Allow: []string{"**"}}
	registry := tools.NewRegistry(pol)

	exec := executor.NewExecutor(wf, provider, registry, pol)
	_, err = exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !toolCalled {
		t.Error("expected tool to be called")
	}
}

// TestPolicyEnforcement tests that policy blocks unauthorized operations.
func TestPolicyEnforcement(t *testing.T) {
	tmpDir := t.TempDir()

	agentfileContent := `NAME policy-test
GOAL write_sensitive "Write to /etc/passwd"
RUN main USING write_sensitive
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfileContent), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	// Mock provider that tries to write to /etc/passwd
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		for _, msg := range req.Messages {
			if msg.Role == "tool" {
				// Tool result should contain "denied"
				if msg.Content == "" || msg.Content[:5] != "Error" {
					t.Error("expected policy denial error")
				}
				return &llm.ChatResponse{Content: "Policy blocked the write"}, nil
			}
		}
		return &llm.ChatResponse{
			ToolCalls: []llm.ToolCallResponse{
				{ID: "tc1", Name: "write", Args: map[string]interface{}{
					"path":    "/etc/passwd",
					"content": "malicious",
				}},
			},
		}, nil
	}

	pol := policy.New()
	pol.DefaultDeny = true
	pol.Workspace = tmpDir
	pol.Tools["write"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
		Deny:    []string{"/etc/*"},
	}
	registry := tools.NewRegistry(pol)

	exec := executor.NewExecutor(wf, provider, registry, pol)
	_, err = exec.Run(context.Background(), nil)
	// Should complete (policy error is reported to LLM, not fatal)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

// TestMultiAgentIntegration tests multi-agent goal execution.
func TestMultiAgentIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agent files
	os.MkdirAll(filepath.Join(tmpDir, "agents"), 0755)
	if err := os.WriteFile(filepath.Join(tmpDir, "agents", "critic.md"), []byte("You are a critic"), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "agents", "optimist.md"), []byte("You are an optimist"), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	agentfileContent := `NAME multi-agent-test
AGENT critic FROM agents/critic.md
AGENT optimist FROM agents/optimist.md
GOAL review "Review the code" USING critic, optimist
RUN main USING review
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfileContent), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	// Track agent calls
	agentCalls := make(map[string]bool)
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		for _, msg := range req.Messages {
			if msg.Role == "system" {
				if msg.Content == "You are a critic" {
					agentCalls["critic"] = true
					return &llm.ChatResponse{Content: "Needs improvement"}, nil
				}
				if msg.Content == "You are an optimist" {
					agentCalls["optimist"] = true
					return &llm.ChatResponse{Content: "Looks great!"}, nil
				}
			}
		}
		// Synthesis call
		return &llm.ChatResponse{Content: "Mixed feedback"}, nil
	}

	exec := executor.NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !agentCalls["critic"] {
		t.Error("critic agent was not called")
	}
	if !agentCalls["optimist"] {
		t.Error("optimist agent was not called")
	}
	if result.Status != executor.StatusComplete {
		t.Errorf("expected Complete, got %s", result.Status)
	}
}

// TestLoopConvergence tests that loops converge correctly.
func TestLoopConvergence(t *testing.T) {
	tmpDir := t.TempDir()

	agentfileContent := `NAME loop-test
GOAL refine "Refine until perfect"
LOOP main USING refine WITHIN 5
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfileContent), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	// Converge after 3 iterations (same output)
	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		if callCount < 3 {
			return &llm.ChatResponse{Content: "Iteration " + string(rune('0'+callCount))}, nil
		}
		return &llm.ChatResponse{Content: "Perfect"}, nil
	}

	exec := executor.NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Status != executor.StatusComplete {
		t.Errorf("expected Complete, got %s", result.Status)
	}
	// Should have converged before max iterations
	if result.Iterations["refine"] > 5 {
		t.Errorf("expected at most 5 iterations, got %d", result.Iterations["refine"])
	}
}

// TestMultiAgentToolAccess tests that AGENT entries have tool access.
func TestMultiAgentToolAccess(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agent file with persona
	os.MkdirAll(filepath.Join(tmpDir, "agents"), 0755)
	if err := os.WriteFile(filepath.Join(tmpDir, "agents", "researcher.md"), []byte("You are a researcher. Use available tools to complete your task."), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	agentfileContent := `NAME agent-tools-test
AGENT researcher FROM agents/researcher.md
GOAL research "Research the topic" USING researcher
RUN main USING research
`
	agentfilePath := filepath.Join(tmpDir, "Agentfile")
	if err := os.WriteFile(agentfilePath, []byte(agentfileContent), 0644); err != nil {
		t.Fatalf("failed to write Agentfile: %v", err)
	}

	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	// Track tool calls from sub-agent
	var toolsReceived []string
	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		// First call should receive tools (sub-agent execution)
		if callCount == 1 && len(req.Tools) > 0 {
			for _, tool := range req.Tools {
				toolsReceived = append(toolsReceived, tool.Name)
			}
			// Sub-agent completes without using tools
			return &llm.ChatResponse{Content: "Research complete"}, nil
		}
		return &llm.ChatResponse{Content: "Done"}, nil
	}

	// Create tool registry with a test tool
	pol := policy.New()
	registry := tools.NewRegistry(pol)

	exec := executor.NewExecutor(wf, provider, registry, nil)
	result, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Status != executor.StatusComplete {
		t.Errorf("expected Complete, got %s", result.Status)
	}

	// Verify sub-agent received tools (but not spawn_agent/spawn_agents)
	if len(toolsReceived) == 0 {
		t.Error("sub-agent did not receive any tools")
	}
	for _, name := range toolsReceived {
		if name == "spawn_agent" || name == "spawn_agents" {
			t.Errorf("sub-agent should not have access to %s", name)
		}
	}
}
