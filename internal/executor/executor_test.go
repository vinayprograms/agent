// Package executor provides workflow and goal execution.
package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agentkit/tools"
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

// Test spawn_agent tool is registered by default
func TestExecutor_SpawnAgentToolRegistered(t *testing.T) {
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
	registry := tools.NewRegistry(nil)

	exec := NewExecutor(wf, provider, registry, nil)
	_ = exec // executor created

	// Verify spawn_agent is in the registry
	if !registry.Has("spawn_agent") {
		t.Error("expected spawn_agent tool to be registered")
	}
}

// Test orchestrator prompt injection when spawn_agent is available
func TestExecutor_OrchestratorPromptInjected(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"goal1"}},
		},
		Goals: []agentfile.Goal{
			{Name: "goal1", Outcome: "Research topic"},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")
	pol := policy.New()
	registry := tools.NewRegistry(pol)

	exec := NewExecutor(wf, provider, registry, pol)
	exec.Run(context.Background(), nil)

	// Check that the system message contains orchestrator guidance
	messages := provider.LastRequest().Messages
	if len(messages) == 0 {
		t.Fatal("expected messages in request")
	}

	systemMsg := ""
	for _, m := range messages {
		if m.Role == "system" {
			systemMsg = m.Content
			break
		}
	}

	if !strings.Contains(systemMsg, "spawn sub-agents") {
		t.Error("expected orchestrator guidance in system prompt")
	}
}

// Test sub-agent depth=1 enforcement (spawn_agent not available to sub-agents)
func TestExecutor_SubAgentCannotSpawn(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"goal1"}},
		},
		Goals: []agentfile.Goal{
			{Name: "goal1", Outcome: "Research"},
		},
	}

	// Track what tools are available to sub-agent
	var subAgentTools []string
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		// Capture tool names from request
		for _, tool := range req.Tools {
			subAgentTools = append(subAgentTools, tool.Name)
		}
		return &llm.ChatResponse{
			Content:    "Done",
			StopReason: "end_turn",
		}, nil
	}

	pol := policy.New()
	registry := tools.NewRegistry(pol)
	exec := NewExecutor(wf, provider, registry, pol)

	// Manually trigger sub-agent spawn to inspect tool filtering
	_, err := exec.spawnDynamicAgent(context.Background(), "researcher", "test task", nil)
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}

	// spawn_agent should NOT be in sub-agent's tool list
	for _, name := range subAgentTools {
		if name == "spawn_agent" {
			t.Error("spawn_agent should not be available to sub-agents")
		}
	}
}

// Test structured output instruction generation
func TestBuildStructuredOutputInstruction(t *testing.T) {
	outputs := []string{"findings", "sources", "confidence"}
	instruction := buildStructuredOutputInstruction(outputs)

	if !strings.Contains(instruction, "findings") {
		t.Error("expected instruction to contain 'findings'")
	}
	if !strings.Contains(instruction, "sources") {
		t.Error("expected instruction to contain 'sources'")
	}
	if !strings.Contains(instruction, "JSON") {
		t.Error("expected instruction to mention JSON")
	}
}

// Test JSON extraction from various formats
func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "raw json",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "json code block",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "generic code block",
			input:    "```\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "json with surrounding text",
			input:    "Here is the result:\n{\"key\": \"value\"}\nDone.",
			expected: `{"key": "value"}`,
		},
		{
			name:     "nested json",
			input:    `{"outer": {"inner": "value"}}`,
			expected: `{"outer": {"inner": "value"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractJSON(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// Test structured output parsing
func TestParseStructuredOutput(t *testing.T) {
	content := `{"findings": "test result", "sources": ["a", "b"], "count": 42}`
	fields := []string{"findings", "sources", "count"}

	result, err := parseStructuredOutput(content, fields)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["findings"] != "test result" {
		t.Errorf("expected findings='test result', got %q", result["findings"])
	}
	if result["sources"] != `["a","b"]` {
		t.Errorf("expected sources as JSON array, got %q", result["sources"])
	}
	if result["count"] != "42" {
		t.Errorf("expected count='42', got %q", result["count"])
	}
}

// Test goal with structured output adds JSON instruction
func TestExecutor_GoalWithStructuredOutput(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "test",
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"analyze"}},
		},
		Goals: []agentfile.Goal{
			{
				Name:    "analyze",
				Outcome: "Analyze the topic",
				Outputs: []string{"summary", "recommendations"},
			},
		},
	}

	provider := llm.NewMockProvider()
	provider.SetResponse(`{"summary": "Test summary", "recommendations": ["Do X", "Do Y"]}`)
	pol := policy.New()
	registry := tools.NewRegistry(pol)

	exec := NewExecutor(wf, provider, registry, pol)
	_, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	// Check that the prompt contained JSON instruction
	req := provider.LastRequest()
	found := false
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, "JSON") && strings.Contains(msg.Content, "summary") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected JSON instruction in prompt")
	}

	// Check that outputs were stored as variables
	if exec.outputs["summary"] != "Test summary" {
		t.Errorf("expected summary='Test summary', got %q", exec.outputs["summary"])
	}
}


// Test supervision helpers
func boolPtr(b bool) *bool { return &b }

// Test isSupervised with goal-level override
func TestExecutor_IsSupervised_GoalOverride(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "test",
		Supervised: true, // workflow is supervised
		Goals: []agentfile.Goal{
			{Name: "normal", Outcome: "Do something"},
			{Name: "unsupervised", Outcome: "Quick task", Supervised: boolPtr(false)},
		},
	}

	exec := NewExecutor(wf, nil, nil, nil)

	// Normal goal inherits from workflow
	if !exec.isSupervised(&wf.Goals[0]) {
		t.Error("expected normal goal to be supervised (inherited)")
	}

	// Unsupervised goal overrides
	if exec.isSupervised(&wf.Goals[1]) {
		t.Error("expected unsupervised goal to NOT be supervised (overridden)")
	}
}

// Test isSupervised with workflow default
func TestExecutor_IsSupervised_WorkflowDefault(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "test",
		Supervised: false, // workflow is NOT supervised
		Goals: []agentfile.Goal{
			{Name: "normal", Outcome: "Do something"},
			{Name: "supervised", Outcome: "Critical task", Supervised: boolPtr(true)},
		},
	}

	exec := NewExecutor(wf, nil, nil, nil)

	// Normal goal inherits from workflow (not supervised)
	if exec.isSupervised(&wf.Goals[0]) {
		t.Error("expected normal goal to NOT be supervised (inherited)")
	}

	// Supervised goal overrides
	if !exec.isSupervised(&wf.Goals[1]) {
		t.Error("expected supervised goal to be supervised (overridden)")
	}
}

// Test requiresHuman
func TestExecutor_RequiresHuman(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "test",
		Supervised: true,
		HumanOnly:  false,
		Goals: []agentfile.Goal{
			{Name: "auto", Outcome: "Auto supervised"},
			{Name: "human", Outcome: "Needs human", Supervised: boolPtr(true), HumanOnly: true},
		},
	}

	exec := NewExecutor(wf, nil, nil, nil)

	if exec.requiresHuman(&wf.Goals[0]) {
		t.Error("expected auto goal to NOT require human")
	}

	if !exec.requiresHuman(&wf.Goals[1]) {
		t.Error("expected human goal to require human")
	}
}

// Test PreFlight with no human required
func TestExecutor_PreFlight_NoHumanRequired(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "test",
		Supervised: true,
		HumanOnly:  false,
		Goals: []agentfile.Goal{
			{Name: "analyze", Outcome: "Analyze data"},
		},
	}

	exec := NewExecutor(wf, nil, nil, nil)
	err := exec.PreFlight()

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// Test PreFlight with human required but available
func TestExecutor_PreFlight_HumanAvailable(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "test",
		Supervised: true,
		HumanOnly:  true, // workflow requires human
		Goals: []agentfile.Goal{
			{Name: "deploy", Outcome: "Deploy to prod"},
		},
	}

	exec := NewExecutor(wf, nil, nil, nil)
	exec.humanAvailable = true

	err := exec.PreFlight()
	if err != nil {
		t.Errorf("expected no error when human available, got: %v", err)
	}
}

// Test PreFlight with human required but NOT available
func TestExecutor_PreFlight_HumanNotAvailable(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "test",
		Supervised: true,
		HumanOnly:  true, // workflow requires human
		Goals: []agentfile.Goal{
			{Name: "deploy", Outcome: "Deploy to prod"},
		},
	}

	exec := NewExecutor(wf, nil, nil, nil)
	exec.humanAvailable = false

	err := exec.PreFlight()
	if err == nil {
		t.Error("expected error when human required but not available")
	}
	if !strings.Contains(err.Error(), "human supervision") {
		t.Errorf("expected error about human supervision, got: %v", err)
	}
}

// Test PreFlight with goal-level human requirement
func TestExecutor_PreFlight_GoalRequiresHuman(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "test",
		Supervised: false, // workflow not supervised
		Goals: []agentfile.Goal{
			{Name: "analyze", Outcome: "Analyze data"},
			{Name: "deploy", Outcome: "Deploy to prod", Supervised: boolPtr(true), HumanOnly: true},
		},
	}

	exec := NewExecutor(wf, nil, nil, nil)
	exec.humanAvailable = false

	err := exec.PreFlight()
	if err == nil {
		t.Error("expected error when goal requires human but not available")
	}
	if !strings.Contains(err.Error(), "deploy") {
		t.Errorf("expected error to mention 'deploy', got: %v", err)
	}
}
