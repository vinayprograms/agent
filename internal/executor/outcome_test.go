package executor

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/tools"
)

// A plain goal that would exhaust its budget on the first attempt, but
// finishes cleanly once the continuation retry's nudge tells it to stop
// exploring, ends up ok with Retried set.
func TestOutcome_BudgetExhaustedRetrySucceeds(t *testing.T) {
	var calls atomic.Int32
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		n := calls.Add(1)
		if n == 1 {
			// First attempt: keep calling tools forever, tripping the
			// goal's tiny budget.
			return &llm.ChatResponse{
				Content:   "still working",
				ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "search", Args: map[string]any{}}},
			}, nil
		}
		// Retry attempt (small fresh budget): answer immediately.
		return &llm.ChatResponse{Content: "done"}, nil
	})

	loop := fakeTool{name: "search", run: func(context.Context, tools.Args) (string, error) {
		return "result", nil
	}}
	reg, _ := newTestRegistry(t, t.TempDir(), loop)

	wf := &agentfile.Workflow{
		Name:  "budget-retry",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"research"}}},
		Goals: []agentfile.Goal{{Name: "research", Outcome: "search forever"}},
	}
	exec := mustNew(t, Config{
		Workflow: wf,
		Model:    model,
		Registry: reg,
		Policy:   permissivePolicy(),
		Budget:   Budget{MaxToolCalls: 1},
	})

	result, err := exec.Run(t.Context(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	oc := result.Goals["research"]
	if oc.Outcome != OutcomeOK {
		t.Errorf("Outcome = %v, want %v", oc.Outcome, OutcomeOK)
	}
	if !oc.Retried {
		t.Errorf("Retried = false, want true")
	}
	if result.Status != StatusComplete {
		t.Errorf("Status = %v, want %v", result.Status, StatusComplete)
	}
	if got := result.Outputs["research"]; got != "done" {
		t.Errorf("Outputs[research] = %q, want %q", got, "done")
	}
}

// A goal that finishes normally (no budget/iteration limit hit) but
// produces empty content and made no tool calls is a hard empty_output
// failure: no retry.
func TestOutcome_EmptyOutputIsHardNoRetry(t *testing.T) {
	var calls atomic.Int32
	model := modelFunc(func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
		calls.Add(1)
		return &llm.ChatResponse{Content: ""}, nil
	})

	wf := &agentfile.Workflow{
		Name:  "empty",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"g"}}},
		Goals: []agentfile.Goal{{Name: "g", Outcome: "say something"}},
	}
	exec := mustNewExecutor(t, wf, model, nil, nil)

	result, err := exec.Run(t.Context(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	oc := result.Goals["g"]
	if oc.Outcome != OutcomeEmptyOutput {
		t.Errorf("Outcome = %v, want %v", oc.Outcome, OutcomeEmptyOutput)
	}
	if oc.Retried {
		t.Errorf("Retried = true, want false — empty_output is hard, no retry")
	}
	if result.Status != StatusFailed {
		t.Errorf("Status = %v, want %v", result.Status, StatusFailed)
	}
	// 2, not 1: the turn-level continuation retry (P0 #1) fires once on the
	// first empty turn (stop_reason=="" here counts as empty content, no
	// tool calls) before classifyOutcome ever sees it; that retry also
	// comes back empty, so the goal is empty_output. That is a different
	// layer from the goal-level maybeRetry this test is really about — the
	// goal itself is never re-run (oc.Retried stays false, checked above).
	if n := calls.Load(); n != 2 {
		t.Errorf("model called %d times, want exactly 2 (one turn-level continuation retry, no goal-level retry)", n)
	}
}

// A CONVERGE goal that uses its whole WITHIN limit without the model ever
// emitting the convergence marker is not_converged, with Iterations
// populated to the limit it used.
func TestOutcome_ConvergeNotConverged(t *testing.T) {
	model := modelFunc(func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "still refining"}, nil // never emits CONVERGED
	})

	limit := 2
	wf := &agentfile.Workflow{
		Name:  "converge-fail",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"refine"}}},
		Goals: []agentfile.Goal{{
			Name: "refine", Outcome: "Refine", IsConverge: true, WithinLimit: &limit,
		}},
	}
	exec := mustNewExecutor(t, wf, model, nil, nil)

	result, err := exec.Run(t.Context(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	oc := result.Goals["refine"]
	if oc.Outcome != OutcomeNotConverged {
		t.Errorf("Outcome = %v, want %v", oc.Outcome, OutcomeNotConverged)
	}
	if oc.Iterations != limit {
		t.Errorf("Iterations = %d, want %d", oc.Iterations, limit)
	}
	if result.Iterations["refine"] != limit {
		t.Errorf("result.Iterations[refine] = %d, want %d", result.Iterations["refine"], limit)
	}
	if result.Status != StatusFailed {
		t.Errorf("Status = %v, want %v", result.Status, StatusFailed)
	}
}

// A CONVERGE goal that does converge still reports its iteration count
// (previously always null).
func TestOutcome_ConvergeSuccessReportsIterations(t *testing.T) {
	var calls atomic.Int32
	model := modelFunc(func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
		n := calls.Add(1)
		if n >= 2 {
			return &llm.ChatResponse{Content: "final answer\nCONVERGED"}, nil
		}
		return &llm.ChatResponse{Content: "iterating"}, nil
	})

	limit := 5
	wf := &agentfile.Workflow{
		Name:  "converge-ok",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"refine"}}},
		Goals: []agentfile.Goal{{
			Name: "refine", Outcome: "Refine", IsConverge: true, WithinLimit: &limit,
		}},
	}
	exec := mustNewExecutor(t, wf, model, nil, nil)

	result, err := exec.Run(t.Context(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	oc := result.Goals["refine"]
	if oc.Outcome != OutcomeOK {
		t.Errorf("Outcome = %v, want %v", oc.Outcome, OutcomeOK)
	}
	if oc.Iterations != 2 {
		t.Errorf("Iterations = %d, want 2", oc.Iterations)
	}
	if result.Iterations["refine"] != 2 {
		t.Errorf("result.Iterations[refine] = %d, want 2", result.Iterations["refine"])
	}
	if result.Status != StatusComplete {
		t.Errorf("Status = %v, want %v", result.Status, StatusComplete)
	}
}

// Workflow status is partial when some goals are ok and some are not, and
// failed when none are.
func TestGoalsStatus_PartialAndFailed(t *testing.T) {
	partial := map[string]GoalOutcome{
		"a": {Outcome: OutcomeOK},
		"b": {Outcome: OutcomeEmptyOutput},
	}
	if got := goalsStatus(partial); got != StatusPartial {
		t.Errorf("goalsStatus(partial mix) = %v, want %v", got, StatusPartial)
	}

	failed := map[string]GoalOutcome{
		"a": {Outcome: OutcomeError},
		"b": {Outcome: OutcomeEmptyOutput},
	}
	if got := goalsStatus(failed); got != StatusFailed {
		t.Errorf("goalsStatus(all bad) = %v, want %v", got, StatusFailed)
	}

	complete := map[string]GoalOutcome{
		"a": {Outcome: OutcomeOK},
		"b": {Outcome: OutcomeOK},
	}
	if got := goalsStatus(complete); got != StatusComplete {
		t.Errorf("goalsStatus(all ok) = %v, want %v", got, StatusComplete)
	}
}

func TestOutcome_HardVsSoft(t *testing.T) {
	hard := []Outcome{OutcomeError, OutcomeEmptyOutput}
	for _, o := range hard {
		if !o.Hard() {
			t.Errorf("%v.Hard() = false, want true", o)
		}
	}
	soft := []Outcome{OutcomeBudgetExhausted, OutcomeNotConverged, OutcomeOK}
	for _, o := range soft {
		if o.Hard() {
			t.Errorf("%v.Hard() = true, want false", o)
		}
	}
}

func TestClassifyOutcome_DeclaredOutputsEmpty(t *testing.T) {
	declared := []string{"summary", "confidence"}
	vars := map[string]string{"summary": "", "confidence": "  "}
	oc := classifyOutcome("some raw text", true, declared, vars, nil, false, 0)
	if oc.Outcome != OutcomeEmptyOutput {
		t.Errorf("Outcome = %v, want %v", oc.Outcome, OutcomeEmptyOutput)
	}

	vars["summary"] = "not empty"
	oc = classifyOutcome("some raw text", true, declared, vars, nil, false, 0)
	if oc.Outcome != OutcomeOK {
		t.Errorf("Outcome = %v, want %v (one declared output non-empty)", oc.Outcome, OutcomeOK)
	}
}
