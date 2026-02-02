// Package executor provides workflow and goal execution.
package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openclaw/headless-agent/internal/agentfile"
	"github.com/openclaw/headless-agent/internal/llm"
	"github.com/openclaw/headless-agent/internal/policy"
	"github.com/openclaw/headless-agent/internal/skills"
	"github.com/openclaw/headless-agent/internal/tools"
)

func intPtr(i int) *int { return &i }
func strPtr(s string) *string { return &s }

// R2.1.1: Accept inputs as key-value pairs
func TestExecutor_InputBinding(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Inputs: []agentfile.Input{
			{Name: "topic"}, // no Default means required
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"analyze"}},
		},
		Goals: []agentfile.Goal{
			{Name: "analyze", Outcome: "Analyze $topic"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Analysis complete")

	exec := NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), map[string]string{
		"topic": "golang",
	})

	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if result.Status != StatusComplete {
		t.Errorf("expected status Complete, got %s", result.Status)
	}
}

// R2.1.2: Apply DEFAULT values for missing inputs
func TestExecutor_DefaultValues(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Inputs: []agentfile.Input{
			{Name: "lang", Default: strPtr("go")},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"check"}},
		},
		Goals: []agentfile.Goal{
			{Name: "check", Outcome: "Check $lang code"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := NewExecutor(wf, provider, nil, nil)
	_, err := exec.Run(context.Background(), nil) // No inputs provided

	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	// Verify default was applied
	req := provider.LastRequest()
	if !strings.Contains(req.Messages[1].Content, "go") {
		t.Error("expected default value 'go' in prompt")
	}
}

// R2.1.3: Error on missing required inputs
func TestExecutor_MissingRequiredInput(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Inputs: []agentfile.Input{
			{Name: "topic"}, // no Default = required
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"analyze"}},
		},
		Goals: []agentfile.Goal{
			{Name: "analyze", Outcome: "Analyze $topic"},
		},
	}

	exec := NewExecutor(wf, llm.NewMockProvider(), nil, nil)
	_, err := exec.Run(context.Background(), nil)

	if err == nil {
		t.Error("expected error for missing required input")
	}
	if !strings.Contains(err.Error(), "required input") {
		t.Errorf("error should mention required input: %v", err)
	}
}

// R2.2.1: Execute RUN/LOOP steps in file order
func TestExecutor_StepOrder(t *testing.T) {
	var executionOrder []string
	
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"first"}},
			{Type: agentfile.StepRUN, UsingGoals: []string{"second"}},
			{Type: agentfile.StepRUN, UsingGoals: []string{"third"}},
		},
		Goals: []agentfile.Goal{
			{Name: "first", Outcome: "First goal"},
			{Name: "second", Outcome: "Second goal"},
			{Name: "third", Outcome: "Third goal"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := NewExecutor(wf, provider, nil, nil)
	exec.OnGoalStart = func(name string) {
		executionOrder = append(executionOrder, name)
	}

	exec.Run(context.Background(), nil)

	expected := []string{"first", "second", "third"}
	if len(executionOrder) != 3 {
		t.Fatalf("expected 3 goals executed, got %d", len(executionOrder))
	}
	for i, name := range expected {
		if executionOrder[i] != name {
			t.Errorf("expected goal %d to be %s, got %s", i, name, executionOrder[i])
		}
	}
}

// R2.2.5: Support variable interpolation in goal prompts
func TestExecutor_VariableInterpolation(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Inputs: []agentfile.Input{
			{Name: "name", Default: strPtr("")},
			{Name: "count", Default: strPtr("")},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"greet"}},
		},
		Goals: []agentfile.Goal{
			{Name: "greet", Outcome: "Hello $name, you have $count items"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := NewExecutor(wf, provider, nil, nil)
	exec.Run(context.Background(), map[string]string{
		"name":  "Alice",
		"count": "5",
	})

	req := provider.LastRequest()
	if !strings.Contains(req.Messages[1].Content, "Hello Alice") {
		t.Error("expected interpolated name")
	}
	if !strings.Contains(req.Messages[1].Content, "5 items") {
		t.Error("expected interpolated count")
	}
}

// R2.3.2: Detect implicit convergence (no tool calls made)
func TestExecutor_LoopConvergence_NoToolCalls(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepLOOP, UsingGoals: []string{"refine"}, WithinLimit: intPtr(10)},
		},
		Goals: []agentfile.Goal{
			{Name: "refine", Outcome: "Refine the code"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Code is already perfect") // No tool calls

	exec := NewExecutor(wf, provider, nil, nil)
	result, _ := exec.Run(context.Background(), nil)

	if result.Iterations["refine"] > 1 {
		t.Errorf("expected convergence after 1 iteration, got %d", result.Iterations["refine"])
	}
}

// R2.3.5: Exit loop when WITHIN iteration limit reached
func TestExecutor_LoopMaxIterations(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepLOOP, UsingGoals: []string{"iterate"}, WithinLimit: intPtr(3)},
		},
		Goals: []agentfile.Goal{
			{Name: "iterate", Outcome: "Keep iterating"},
		},
	}

	// Use ChatFunc to always return tool calls, simulating never-converging behavior
	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		// Check if this is a response after tool results
		hasToolResult := false
		for _, msg := range req.Messages {
			if msg.Role == "tool" {
				hasToolResult = true
				break
			}
		}
		
		if hasToolResult {
			// After tool result, complete this goal iteration with unique output
			return &llm.ChatResponse{Content: fmt.Sprintf("Iteration result %d", callCount)}, nil
		}
		
		// First call in goal: return tool call
		return &llm.ChatResponse{
			ToolCalls: []llm.ToolCallResponse{
				{ID: fmt.Sprintf("tc-%d", callCount), Name: "ls", Args: map[string]interface{}{"path": "."}},
			},
		}, nil
	}

	pol := policy.New()
	tmpDir := t.TempDir()
	pol.Workspace = tmpDir
	reg := tools.NewRegistry(pol)

	exec := NewExecutor(wf, provider, reg, pol)
	result, _ := exec.Run(context.Background(), nil)

	if result.Iterations["iterate"] != 3 {
		t.Errorf("expected 3 iterations, got %d", result.Iterations["iterate"])
	}
}

// R2.4.2: Make prior goal outputs available via $goal_name
func TestExecutor_GoalOutputReference(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"analyze", "summarize"}},
		},
		Goals: []agentfile.Goal{
			{Name: "analyze", Outcome: "Analyze the code"},
			{Name: "summarize", Outcome: "Summarize: $analyze"},
		},
	}

	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		if callCount == 1 {
			return &llm.ChatResponse{Content: "Analysis result: good code"}, nil
		}
		return &llm.ChatResponse{Content: "Summary complete"}, nil
	}

	exec := NewExecutor(wf, provider, nil, nil)
	exec.Run(context.Background(), nil)

	req := provider.LastRequest()
	if !strings.Contains(req.Messages[1].Content, "Analysis result: good code") {
		t.Error("expected prior goal output in prompt")
	}
}

// R3.1.6: Loop until LLM signals goal complete
func TestExecutor_GoalLoop(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"multi_step"}},
		},
		Goals: []agentfile.Goal{
			{Name: "multi_step", Outcome: "Complete the multi-step task"},
		},
	}

	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		switch callCount {
		case 1:
			return &llm.ChatResponse{
				ToolCalls: []llm.ToolCallResponse{
					{ID: "tc1", Name: "ls", Args: map[string]interface{}{"path": "."}},
				},
			}, nil
		case 2:
			return &llm.ChatResponse{
				ToolCalls: []llm.ToolCallResponse{
					{ID: "tc2", Name: "ls", Args: map[string]interface{}{"path": ".."}},
				},
			}, nil
		default:
			return &llm.ChatResponse{Content: "Task complete"}, nil
		}
	}

	pol := policy.New()
	pol.Workspace = t.TempDir()
	reg := tools.NewRegistry(pol)

	exec := NewExecutor(wf, provider, reg, pol)
	exec.Run(context.Background(), nil)

	if callCount != 3 {
		t.Errorf("expected 3 LLM calls, got %d", callCount)
	}
}

// R3.2.1-R3.2.5: Multi-agent orchestration
func TestExecutor_MultiAgent(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Agents: []agentfile.Agent{
			{Name: "critic", Prompt: "You are a code critic"},
			{Name: "optimist", Prompt: "You are an optimistic reviewer"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"review"}},
		},
		Goals: []agentfile.Goal{
			{Name: "review", Outcome: "Review this code", UsingAgent: []string{"critic", "optimist"}},
		},
	}

	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		// First two calls are parallel agent calls
		if callCount <= 2 {
			if strings.Contains(req.Messages[0].Content, "critic") {
				return &llm.ChatResponse{Content: "Code needs improvement"}, nil
			}
			return &llm.ChatResponse{Content: "Code looks great!"}, nil
		}
		// Third call is synthesis
		return &llm.ChatResponse{Content: "Synthesized: Mixed reviews"}, nil
	}

	exec := NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), nil)

	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	// Should have at least 2 agent calls + 1 synthesis
	if callCount < 3 {
		t.Errorf("expected at least 3 LLM calls for multi-agent, got %d", callCount)
	}

	_ = result
}

// R3.3.3: Interpolate variables in prompts
func TestExecutor_PromptInterpolation(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Inputs: []agentfile.Input{
			{Name: "file_path", Default: strPtr("")},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"process"}},
		},
		Goals: []agentfile.Goal{
			{Name: "process", Outcome: "Process file at $file_path"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := NewExecutor(wf, provider, nil, nil)
	exec.Run(context.Background(), map[string]string{
		"file_path": "/data/input.json",
	})

	req := provider.LastRequest()
	if !strings.Contains(req.Messages[1].Content, "/data/input.json") {
		t.Error("expected file_path interpolated in prompt")
	}
}

// Test execution result contains goal outputs
func TestExecutor_ResultContainsOutputs(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"goal1", "goal2"}},
		},
		Goals: []agentfile.Goal{
			{Name: "goal1", Outcome: "First"},
			{Name: "goal2", Outcome: "Second"},
		},
	}

	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		if callCount == 1 {
			return &llm.ChatResponse{Content: "Output 1"}, nil
		}
		return &llm.ChatResponse{Content: "Output 2"}, nil
	}

	exec := NewExecutor(wf, provider, nil, nil)
	result, _ := exec.Run(context.Background(), nil)

	if result.Outputs["goal1"] != "Output 1" {
		t.Errorf("expected goal1 output 'Output 1', got %s", result.Outputs["goal1"])
	}
	if result.Outputs["goal2"] != "Output 2" {
		t.Errorf("expected goal2 output 'Output 2', got %s", result.Outputs["goal2"])
	}
}

// Test MCP manager integration
func TestExecutor_SetMCPManager(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"goal1"}},
		},
		Goals: []agentfile.Goal{
			{Name: "goal1", Outcome: "Test"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := NewExecutor(wf, provider, nil, nil)
	
	// Should not panic with nil
	exec.SetMCPManager(nil)
	
	result, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if result.Status != StatusComplete {
		t.Errorf("expected status Complete, got %s", result.Status)
	}
}

// Test skills integration
func TestExecutor_SetSkills(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"goal1"}},
		},
		Goals: []agentfile.Goal{
			{Name: "goal1", Outcome: "Test"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := NewExecutor(wf, provider, nil, nil)
	
	// Set some skill refs
	exec.SetSkills([]skills.SkillRef{
		{Name: "code-review", Description: "Review code", Path: "/path/to/skill"},
		{Name: "testing", Description: "Write tests", Path: "/path/to/testing"},
	})
	
	result, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if result.Status != StatusComplete {
		t.Errorf("expected status Complete, got %s", result.Status)
	}
	
	// Verify skills are included in system message
	req := provider.LastRequest()
	if len(exec.skillRefs) > 0 && !strings.Contains(req.Messages[0].Content, "code-review") {
		t.Error("expected skill to be mentioned in system message")
	}
}

// Test getAllToolDefinitions with no MCP
func TestExecutor_GetAllToolDefinitions(t *testing.T) {
	wf := &agentfile.Workflow{Name: "test"}
	provider := llm.NewMockProvider()
	pol := policy.New()
	registry := tools.NewRegistry(pol)
	
	exec := NewExecutor(wf, provider, registry, pol)
	
	defs := exec.getAllToolDefinitions()
	
	// Should have built-in tools
	if len(defs) == 0 {
		t.Error("expected some tool definitions")
	}
	
	// Check that built-in tools are present
	hasRead := false
	for _, d := range defs {
		if d.Name == "read" {
			hasRead = true
			break
		}
	}
	if !hasRead {
		t.Error("expected 'read' tool to be defined")
	}
}

// Test skill activation check
func TestExecutor_CheckSkillActivation(t *testing.T) {
	wf := &agentfile.Workflow{Name: "test"}
	provider := llm.NewMockProvider()
	exec := NewExecutor(wf, provider, nil, nil)
	
	exec.SetSkills([]skills.SkillRef{
		{Name: "code-review", Description: "Review code", Path: "/nonexistent"},
	})
	
	// No activation
	skill := exec.checkSkillActivation("Just a normal response")
	if skill != nil {
		t.Error("expected no skill activation")
	}
	
	// Activation pattern but skill doesn't exist
	skill = exec.checkSkillActivation("Let me [use-skill:unknown-skill] for this")
	if skill != nil {
		t.Error("expected no skill for unknown skill")
	}
}

// Test callbacks for skill loading
func TestExecutor_OnSkillLoadedCallback(t *testing.T) {
	wf := &agentfile.Workflow{Name: "test"}
	provider := llm.NewMockProvider()
	exec := NewExecutor(wf, provider, nil, nil)
	
	// Set callback
	callbackCalled := false
	exec.OnSkillLoaded = func(name string) {
		callbackCalled = true
	}
	
	// Verify callback can be set
	if exec.OnSkillLoaded == nil {
		t.Error("expected OnSkillLoaded callback to be set")
	}
	_ = callbackCalled // would be used in real skill loading test
}

// Test MCP tool call parsing
func TestExecutor_MCPToolNameParsing(t *testing.T) {
	wf := &agentfile.Workflow{Name: "test"}
	provider := llm.NewMockProvider()
	exec := NewExecutor(wf, provider, nil, nil)
	
	// Without MCP manager, should error
	_, err := exec.executeMCPTool(context.Background(), llm.ToolCallResponse{
		ID:   "call_1",
		Name: "mcp_filesystem_read_file",
		Args: map[string]interface{}{"path": "/tmp/test"},
	})
	
	if err == nil {
		t.Error("expected error without MCP manager")
	}
	if !strings.Contains(err.Error(), "no MCP manager") {
		t.Errorf("expected 'no MCP manager' error, got: %v", err)
	}
}

// Test callbacks
func TestExecutor_AllCallbacks(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"goal1"}},
		},
		Goals: []agentfile.Goal{
			{Name: "goal1", Outcome: "Test"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := NewExecutor(wf, provider, nil, nil)
	
	var goalStarted, goalCompleted string
	exec.OnGoalStart = func(name string) {
		goalStarted = name
	}
	exec.OnGoalComplete = func(name, output string) {
		goalCompleted = name
	}
	
	exec.Run(context.Background(), nil)
	
	if goalStarted != "goal1" {
		t.Errorf("expected goalStarted 'goal1', got %q", goalStarted)
	}
	if goalCompleted != "goal1" {
		t.Errorf("expected goalCompleted 'goal1', got %q", goalCompleted)
	}
}
