package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agentkit/llm"
)

// mockConvergeProvider simulates LLM responses for convergence testing.
type mockConvergeProvider struct {
	responses []string // responses to return in order
	callCount int
}

func (m *mockConvergeProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.callCount < len(m.responses) {
		resp := m.responses[m.callCount]
		m.callCount++
		return &llm.ChatResponse{Content: resp}, nil
	}
	return &llm.ChatResponse{Content: "CONVERGED"}, nil
}

func (m *mockConvergeProvider) ChatStream(ctx context.Context, req llm.ChatRequest, callback func(string)) (*llm.ChatResponse, error) {
	return m.Chat(ctx, req)
}

func (m *mockConvergeProvider) Name() string { return "mock-converge" }

func TestConvergeGoal_Converges(t *testing.T) {
	limit := 10
	wf := &agentfile.Workflow{
		Name: "converge-test",
		Goals: []agentfile.Goal{
			{
				Name:        "refine",
				Outcome:     "Refine the code",
				IsConverge:  true,
				WithinLimit: &limit,
			},
		},
	}

	// Mock provider returns 2 refinements then CONVERGED
	provider := &mockConvergeProvider{
		responses: []string{
			"First refinement attempt",
			"Second refinement attempt",
			"CONVERGED",
		},
	}

	exec := NewExecutor(wf, provider, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}

	if !result.Converged {
		t.Error("expected goal to converge")
	}
	if result.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", result.Iterations)
	}
	if result.Output != "Second refinement attempt" {
		t.Errorf("expected last substantive output, got %q", result.Output)
	}
}

func TestConvergeGoal_HitsLimit(t *testing.T) {
	limit := 3
	wf := &agentfile.Workflow{
		Name: "converge-test",
		Goals: []agentfile.Goal{
			{
				Name:        "refine",
				Outcome:     "Refine forever",
				IsConverge:  true,
				WithinLimit: &limit,
			},
		},
	}

	// Mock provider never says CONVERGED
	provider := &mockConvergeProvider{
		responses: []string{
			"Attempt 1",
			"Attempt 2",
			"Attempt 3",
			"Attempt 4", // won't reach this due to limit
		},
	}

	exec := NewExecutor(wf, provider, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}

	if result.Converged {
		t.Error("expected goal NOT to converge (hit limit)")
	}
	if result.Iterations != 3 {
		t.Errorf("expected 3 iterations (limit), got %d", result.Iterations)
	}
	if result.Output != "Attempt 3" {
		t.Errorf("expected last output 'Attempt 3', got %q", result.Output)
	}

	// Check convergence failure was tracked
	failures := exec.GetConvergenceFailures()
	if failures == nil || failures["refine"] != 3 {
		t.Errorf("expected convergence failure tracked, got %v", failures)
	}
}

func TestConvergeGoal_ContextBuilding(t *testing.T) {
	limit := 5
	wf := &agentfile.Workflow{
		Name: "converge-test",
		Goals: []agentfile.Goal{
			{
				Name:        "refine",
				Outcome:     "Refine iteratively",
				IsConverge:  true,
				WithinLimit: &limit,
			},
		},
	}

	// Track what prompts are sent
	var prompts []string
	provider := &mockConvergeProvider{
		responses: []string{"First", "CONVERGED"},
	}

	exec := NewExecutor(wf, provider, nil, nil)
	
	// Override to capture prompts (we can test the context building separately)
	iterations := []ConvergenceIteration{}
	prompt := exec.buildConvergePrompt(&wf.Goals[0], iterations, 1)
	prompts = append(prompts, prompt)

	// First iteration should have convergence-instruction
	if !strings.Contains(prompt, "<convergence-instruction>") {
		t.Error("expected convergence instruction in prompt")
	}
	if !strings.Contains(prompt, "CONVERGED") {
		t.Error("expected CONVERGED keyword instruction in prompt")
	}

	// Second iteration should have first output in history
	iterations = append(iterations, ConvergenceIteration{N: 1, Output: "First iteration output"})
	prompt = exec.buildConvergePrompt(&wf.Goals[0], iterations, 2)

	if !strings.Contains(prompt, "<convergence-history>") {
		t.Error("expected convergence-history in prompt")
	}
	if !strings.Contains(prompt, "First iteration output") {
		t.Error("expected previous iteration output in context")
	}
}

func TestConvergeGoal_VariableLimit(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "converge-test",
		Inputs: []agentfile.Input{
			{Name: "max_iter"},
		},
		Goals: []agentfile.Goal{
			{
				Name:       "refine",
				Outcome:    "Refine with variable limit",
				IsConverge: true,
				WithinVar:  "max_iter",
			},
		},
	}

	provider := &mockConvergeProvider{
		responses: []string{"One", "Two", "CONVERGED"},
	}

	exec := NewExecutor(wf, provider, nil, nil)
	
	// Initialize inputs map and set the variable
	if exec.inputs == nil {
		exec.inputs = make(map[string]string)
	}
	exec.inputs["max_iter"] = "5"

	limit := exec.getConvergeLimit(&wf.Goals[0])
	if limit != 5 {
		t.Errorf("expected limit=5 from variable, got %d", limit)
	}
}
