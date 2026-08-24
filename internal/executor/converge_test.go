package executor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
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

	exec := mustNewExecutor(t, wf, provider, nil, nil)
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

	exec := mustNewExecutor(t, wf, provider, nil, nil)
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
	failures := exec.ConvergenceFailures()
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

	exec := mustNewExecutor(t, wf, provider, nil, nil)

	// Override to capture prompts (we can test the context building separately)
	iterations := []ConvergenceIteration{}
	prompt := exec.buildConvergePrompt(&wf.Goals[0], iterations, "")
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
	prompt = exec.buildConvergePrompt(&wf.Goals[0], iterations, "")

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

	exec := mustNewExecutor(t, wf, provider, nil, nil)

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

func convergeWorkflow(limit *int, within string, agents ...string) *agentfile.Workflow {
	wf := &agentfile.Workflow{
		Name:       "converge-test",
		Supervised: true,
		Steps:      []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"refine"}}},
		Goals: []agentfile.Goal{{
			Name: "refine", Outcome: "Refine the work", IsConverge: true,
			WithinLimit: limit, WithinVar: within, UsingAgent: agents,
		}},
	}
	for _, a := range agents {
		wf.Agents = append(wf.Agents, agentfile.Agent{Name: a})
	}
	return wf
}

func TestGetConvergeLimit(t *testing.T) {
	limit := 7
	tests := []struct {
		name    string
		goal    agentfile.Goal
		inputs  map[string]string
		outputs map[string]string
		want    int
	}{
		{name: "literal", goal: agentfile.Goal{WithinLimit: &limit}, want: 7},
		{name: "no limit at all", goal: agentfile.Goal{}, want: 0},
		{name: "from outputs", goal: agentfile.Goal{WithinVar: "n"}, outputs: map[string]string{"n": "4"}, want: 4},
		{name: "from inputs", goal: agentfile.Goal{WithinVar: "n"}, inputs: map[string]string{"n": "3"}, want: 3},
		{name: "outputs win over inputs", goal: agentfile.Goal{WithinVar: "n"}, outputs: map[string]string{"n": "4"}, inputs: map[string]string{"n": "3"}, want: 4},
		{name: "unparsable output falls through to inputs", goal: agentfile.Goal{WithinVar: "n"}, outputs: map[string]string{"n": "many"}, inputs: map[string]string{"n": "3"}, want: 3},
		{name: "unparsable everywhere", goal: agentfile.Goal{WithinVar: "n"}, inputs: map[string]string{"n": "many"}, want: 0},
		{name: "variable not bound", goal: agentfile.Goal{WithinVar: "n"}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
			exec.inputs, exec.outputs = tt.inputs, tt.outputs
			if got := exec.getConvergeLimit(&tt.goal); got != tt.want {
				t.Errorf("getConvergeLimit() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConvergeGoal_RejectsNonPositiveLimit(t *testing.T) {
	zero := 0
	wf := convergeWorkflow(&zero, "")
	exec := mustNewExecutor(t, wf, llmmock.New(), nil, nil)
	if _, err := exec.executeConvergeGoal(t.Context(), &wf.Goals[0]); err == nil || !strings.Contains(err.Error(), "WITHIN limit must be > 0") {
		t.Fatalf("expected limit rejection, got %v", err)
	}
}

func TestConvergeGoal_IterationFailure(t *testing.T) {
	limit := 3
	wf := convergeWorkflow(&limit, "")
	model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return nil, errors.New("model down")
	})
	exec := mustNewExecutor(t, wf, model, nil, nil)
	_, err := exec.executeConvergeGoal(t.Context(), &wf.Goals[0])
	if err == nil || !strings.Contains(err.Error(), "convergence iteration 1 failed") {
		t.Fatalf("expected iteration failure, got %v", err)
	}
}

// A supervisor reorientation runs one more iteration with the correction in
// the prompt, and that iteration's output is the goal's result.
func TestConvergeGoal_SupervisorVerdicts(t *testing.T) {
	limit := 2
	newModel := func(correctedErr error) llm.Model {
		return modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			user := req.Messages[len(req.Messages)-1].Content
			switch {
			case strings.Contains(user, "declare your intent"):
				return &llm.ChatResponse{Content: `{"interpretation":"i","approach":"a","confidence":"high"}`}, nil
			case strings.Contains(user, "Assess your work"):
				return &llm.ChatResponse{Content: `{"met_commitment":true}`}, nil
			case strings.Contains(user, "do better"):
				if correctedErr != nil {
					return nil, correctedErr
				}
				return &llm.ChatResponse{Content: "corrected"}, nil
			}
			return &llm.ChatResponse{Content: "draft"}, nil
		})
	}

	wf := convergeWorkflow(&limit, "")
	exec, _ := newSupervisedExecutor(t, wf, newModel(nil), fakeSupervisor{supervise: true, verdict: "REORIENT"})
	res, err := exec.executeConvergeGoal(t.Context(), &wf.Goals[0])
	if err != nil || res.Output != "corrected" {
		t.Fatalf("reorient: %+v %v", res, err)
	}

	exec, _ = newSupervisedExecutor(t, wf, newModel(errors.New("model down")), fakeSupervisor{supervise: true, verdict: "REORIENT"})
	if _, err := exec.executeConvergeGoal(t.Context(), &wf.Goals[0]); err == nil || !strings.Contains(err.Error(), "correction iteration failed") {
		t.Fatalf("expected correction failure, got %v", err)
	}

	exec, _ = newSupervisedExecutor(t, wf, newModel(nil), fakeSupervisor{supervise: true, verdict: "PAUSE"})
	if _, err := exec.executeConvergeGoal(t.Context(), &wf.Goals[0]); err == nil || !strings.Contains(err.Error(), "supervision paused") {
		t.Fatalf("expected pause, got %v", err)
	}
}

// A CONVERGE goal with USING AGENT runs the multi-agent path each iteration,
// handing the agents the convergence-aware prompt.
func TestConvergeGoal_MultiAgent(t *testing.T) {
	limit := 2
	wf := convergeWorkflow(&limit, "", "researcher")
	var tasks []string
	var mu sync.Mutex
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		mu.Lock()
		tasks = append(tasks, user)
		n := len(tasks)
		mu.Unlock()
		if n > 1 {
			return &llm.ChatResponse{Content: "CONVERGED"}, nil
		}
		return &llm.ChatResponse{Content: "first pass"}, nil
	})
	exec := mustNewExecutor(t, wf, model, nil, nil)

	res, err := exec.executeConvergeGoal(t.Context(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal: %v", err)
	}
	if !res.Converged || res.Output != "first pass" {
		t.Fatalf("got %+v", res)
	}
	if !strings.Contains(tasks[0], "convergence-instruction") {
		t.Errorf("agents did not get the convergence prompt: %q", tasks[0])
	}
}

func TestSplitConvergence(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantContent string
		wantDone    bool
	}{
		{"bare marker", "CONVERGED", "", true},
		{"marker with surrounding space", "  CONVERGED\n", "", true},
		{"content then marker", "Final answer.\n\nCONVERGED", "Final answer.", true},
		{"no marker", "Still refining", "Still refining", false},
		{"marker not on its own line", "The build CONVERGED early", "The build CONVERGED early", false},
		{"marker first is not terminal", "CONVERGED\nmore work", "CONVERGED\nmore work", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, done := splitConvergence(tt.output)
			if content != tt.wantContent || done != tt.wantDone {
				t.Errorf("splitConvergence(%q) = (%q, %v), want (%q, %v)",
					tt.output, content, done, tt.wantContent, tt.wantDone)
			}
		})
	}
}

// A CONVERGED marker may carry the final content on the lines before it; that
// content — not an empty string — is the goal's output.
func TestConvergeGoal_MarkerCarriesFinalOutput(t *testing.T) {
	limit := 3
	wf := &agentfile.Workflow{
		Name: "converge-test",
		Goals: []agentfile.Goal{{
			Name:        "summarize",
			Outcome:     "Summarize",
			IsConverge:  true,
			WithinLimit: &limit,
		}},
	}
	provider := &mockConvergeProvider{responses: []string{"The final summary.\n\nCONVERGED"}}

	exec := mustNewExecutor(t, wf, provider, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}
	if !result.Converged {
		t.Error("Converged = false, want true")
	}
	if result.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1", result.Iterations)
	}
	if result.Output != "The final summary." {
		t.Errorf("Output = %q, want %q", result.Output, "The final summary.")
	}
}

// A bare CONVERGED on a later iteration keeps the last substantive output.
func TestConvergeGoal_BareMarkerKeepsLastOutput(t *testing.T) {
	limit := 5
	wf := &agentfile.Workflow{
		Name: "converge-test",
		Goals: []agentfile.Goal{{
			Name:        "summarize",
			Outcome:     "Summarize",
			IsConverge:  true,
			WithinLimit: &limit,
		}},
	}
	provider := &mockConvergeProvider{responses: []string{"Draft one", "CONVERGED"}}

	exec := mustNewExecutor(t, wf, provider, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}
	if result.Output != "Draft one" {
		t.Errorf("Output = %q, want %q", result.Output, "Draft one")
	}
}

// goal.complete fires exactly once per goal, whatever the goal's shape —
// not once per convergence iteration, and never zero times.
func TestGoalComplete_FiresOncePerGoal(t *testing.T) {
	limit := 3
	tests := []struct {
		name string
		goal agentfile.Goal
	}{
		{"plain", agentfile.Goal{Name: "g", Outcome: "Do it"}},
		{"multi-agent", agentfile.Goal{Name: "g", Outcome: "Do it", UsingAgent: []string{"worker"}}},
		{"converge", agentfile.Goal{Name: "g", Outcome: "Do it", IsConverge: true, WithinLimit: &limit}},
		{"converge multi-agent", agentfile.Goal{
			Name: "g", Outcome: "Do it", IsConverge: true, WithinLimit: &limit,
			UsingAgent: []string{"worker"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := &agentfile.Workflow{
				Name:   "hooks-test",
				Agents: []agentfile.Agent{{Name: "worker", Prompt: "work"}},
				Goals:  []agentfile.Goal{tt.goal},
				Steps:  []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"g"}}},
			}
			// Two substantive iterations, then converge.
			var mu sync.Mutex
			turn := 0
			provider := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
				mu.Lock()
				defer mu.Unlock()
				turn++
				if turn >= 3 {
					return &llm.ChatResponse{Content: "CONVERGED"}, nil
				}
				return &llm.ChatResponse{Content: "draft"}, nil
			})

			exec := mustNewExecutor(t, wf, provider, nil, nil)
			completions := 0
			exec.Hooks().On(hooks.GoalComplete, func(_ context.Context, evt hooks.Event) {
				mu.Lock()
				defer mu.Unlock()
				if evt.Data["name"] == "g" {
					completions++
				}
			})

			if _, err := exec.Run(t.Context(), RunOptions{}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if completions != 1 {
				t.Errorf("goal.complete fired %d times, want 1", completions)
			}
		})
	}
}

// TestConvergeGoal_Pipeline_LastAgentDecides verifies that a CONVERGE goal's
// USING agents run sequentially (b sees a's output) and that ONLY the last
// agent's decision matters: the first agent (agentA) never emits CONVERGED,
// yet the goal still converges after one iteration because the last agent
// (agentB) does. There is no synthesis call: the last agent's output IS the
// goal's output.
func TestConvergeGoal_Pipeline_LastAgentDecides(t *testing.T) {
	limit := 5
	wf := &agentfile.Workflow{
		Name: "converge-multi",
		Agents: []agentfile.Agent{
			{Name: "agentA"},
			{Name: "agentB"},
		},
		Goals: []agentfile.Goal{
			{
				Name:        "refine",
				Outcome:     "Refine the proposal",
				IsConverge:  true,
				WithinLimit: &limit,
				UsingAgent:  []string{"agentA", "agentB"},
			},
		},
	}

	var mu sync.Mutex
	var sawAInB bool
	provider := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		sys := req.Messages[0].Content
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(sys, "agentA"):
			// agentA never converges on its own.
			return &llm.ChatResponse{Content: "Position A is a draft."}, nil
		case strings.Contains(sys, "agentB"):
			mu.Lock()
			sawAInB = strings.Contains(user, "Position A is a draft")
			mu.Unlock()
			return &llm.ChatResponse{Content: "Position B is final.\nCONVERGED"}, nil
		}
		return &llm.ChatResponse{Content: "unexpected caller"}, nil
	})

	exec := mustNewExecutor(t, wf, provider, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}

	if !result.Converged {
		t.Error("expected goal to converge on the last agent's decision alone")
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.Output != "Position B is final." {
		t.Errorf("expected the last agent's output as the goal output (no synthesis), got %q", result.Output)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawAInB {
		t.Error("expected agentB to see agentA's output (sequential pipeline)")
	}
}

// TestConvergeGoal_Pipeline_FirstAgentMarkerIgnored verifies that a marker
// from a non-last pipeline agent does NOT converge the goal — only the last
// agent's decision counts.
func TestConvergeGoal_Pipeline_FirstAgentMarkerIgnored(t *testing.T) {
	limit := 2
	wf := &agentfile.Workflow{
		Name: "converge-multi-partial",
		Agents: []agentfile.Agent{
			{Name: "agentA"},
			{Name: "critic"},
		},
		Goals: []agentfile.Goal{
			{
				Name:        "refine",
				Outcome:     "Refine the proposal",
				IsConverge:  true,
				WithinLimit: &limit,
				UsingAgent:  []string{"agentA", "critic"},
			},
		},
	}

	provider := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		sys := req.Messages[0].Content
		switch {
		case strings.Contains(sys, "agentA"):
			// agentA emits the marker, but it's not the last agent — it must
			// not converge the goal.
			return &llm.ChatResponse{Content: "Position A is solid.\nCONVERGED"}, nil
		case strings.Contains(sys, "critic"):
			// The critic (last agent) never converges: it always sees a gap.
			return &llm.ChatResponse{Content: "Still missing evidence for claim X."}, nil
		}
		return &llm.ChatResponse{Content: "unexpected caller"}, nil
	})

	exec := mustNewExecutor(t, wf, provider, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}

	if result.Converged {
		t.Error("expected goal NOT to converge: only the last agent's decision counts, and the critic never converges")
	}
	if result.Iterations != 2 {
		t.Errorf("expected to run out the 2-iteration limit, got %d", result.Iterations)
	}
}
