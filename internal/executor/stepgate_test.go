package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/step"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
)

// gateWorkflow is two RUN steps, the first with two goals, so a gate sees
// three (step, goal) pairs in a deterministic order.
func gateWorkflow() *agentfile.Workflow {
	return &agentfile.Workflow{
		Name: "wf",
		Goals: []agentfile.Goal{
			{Name: "g1", Outcome: "one"},
			{Name: "g2", Outcome: "two"},
			{Name: "g3", Outcome: "three"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, Name: "first", UsingGoals: []string{"g1", "g2"}},
			{Type: agentfile.StepRUN, Name: "second", UsingGoals: []string{"g3"}},
		},
	}
}

func gateExecutor(t *testing.T) *Executor {
	t.Helper()
	provider := llmmock.New()
	provider.SetResponse("done")
	return mustNewExecutor(t, gateWorkflow(), provider, nil, nil)
}

func TestRun_StepGate_ContinuesAndSeesEveryGoal(t *testing.T) {
	var seen []string
	res, err := gateExecutor(t).Run(t.Context(), RunOptions{
		StepGate: func(_ context.Context, step, goal string) (bool, error) {
			seen = append(seen, step+"/"+goal)
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != StatusComplete {
		t.Errorf("Status = %q, want %q", res.Status, StatusComplete)
	}
	want := []string{"first/g1", "first/g2", "second/g3"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("gate calls = %v, want %v", seen, want)
	}
}

func TestRun_StepGate_AbortsAfterFirstGoal(t *testing.T) {
	var calls int
	res, err := gateExecutor(t).Run(t.Context(), RunOptions{
		StepGate: func(context.Context, string, string) (bool, error) {
			calls++
			return false, nil
		},
	})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Run error = %v, want ErrAborted", err)
	}
	for _, want := range []string{`"first"`, `"g1"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run error = %q, want it to mention %s", err, want)
		}
	}
	if calls != 1 {
		t.Errorf("gate calls = %d, want 1", calls)
	}
	if res.Status != StatusAborted {
		t.Errorf("Status = %q, want %q", res.Status, StatusAborted)
	}
}

func TestRun_StepGate_Error(t *testing.T) {
	wantErr := errors.New("terminal exploded")
	res, err := gateExecutor(t).Run(t.Context(), RunOptions{
		StepGate: func(context.Context, string, string) (bool, error) {
			return true, wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}
	if errors.Is(err, ErrAborted) {
		t.Errorf("Run error = %v, want a gate failure, not ErrAborted", err)
	}
	if res.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", res.Status, StatusFailed)
	}
}

func TestRun_StepGate_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	_, err := gateExecutor(t).Run(ctx, RunOptions{
		StepGate: func(ctx context.Context, _, _ string) (bool, error) {
			cancel()
			return false, ctx.Err()
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

// A gate still runs after a goal that failed, so the operator sees the
// failure before deciding; both errors survive.
func TestRun_StepGate_RunsAfterFailedGoal(t *testing.T) {
	goalErr := errors.New("model down")
	provider := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return nil, goalErr
	})
	exec := mustNewExecutor(t, gateWorkflow(), provider, nil, nil)
	var calls int
	_, err := exec.Run(t.Context(), RunOptions{
		StepGate: func(context.Context, string, string) (bool, error) {
			calls++
			return false, nil
		},
	})
	if calls != 1 {
		t.Fatalf("gate calls = %d, want 1", calls)
	}
	if !errors.Is(err, ErrAborted) || !strings.Contains(err.Error(), "model down") {
		t.Errorf("Run error = %v, want both the goal failure and ErrAborted", err)
	}
}

// The nil gate is the default: nothing is called, nothing changes.
func TestRun_NilStepGate_Unchanged(t *testing.T) {
	res, err := gateExecutor(t).Run(t.Context(), RunOptions{})
	if err != nil || res.Status != StatusComplete {
		t.Fatalf("Run = %+v, %v; want complete", res, err)
	}
}

// A goal no RUN step claims is labelled with its own name.
func TestExecuteGoal_StepGate_UnclaimedGoal(t *testing.T) {
	wf := &agentfile.Workflow{Name: "wf", Goals: []agentfile.Goal{{Name: "solo", Outcome: "x"}}}
	provider := llmmock.New()
	provider.SetResponse("done")
	exec := mustNewExecutor(t, wf, provider, nil, nil)
	var gotStep string
	exec.stepGate = func(_ context.Context, step, _ string) (bool, error) {
		gotStep = step
		return true, nil
	}
	if err := exec.ExecuteGoal(t.Context(), "solo", step.NewState(nil)); err != nil {
		t.Fatalf("ExecuteGoal: %v", err)
	}
	if gotStep != "solo" {
		t.Errorf("gate step = %q, want the goal name as the label", gotStep)
	}
}
