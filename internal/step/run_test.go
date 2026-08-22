package step

import (
	"context"
	"errors"
	"testing"
)

// fakeGoalExecutor is a fake GoalExecutor recording the goals it was asked to run.
type fakeGoalExecutor struct {
	ran     []string
	failAt  string
	failErr error
}

func (f *fakeGoalExecutor) ExecuteGoal(ctx context.Context, goalName string, state *State) error {
	f.ran = append(f.ran, goalName)
	if goalName == f.failAt {
		return f.failErr
	}
	return nil
}

func TestRunStep_Name(t *testing.T) {
	r := NewRunStep("build", []string{"g1"}, &fakeGoalExecutor{})
	if got := r.Name(); got != "build" {
		t.Errorf("Name() = %q, want %q", got, "build")
	}
}

func TestRunStep_Execute_Order(t *testing.T) {
	exec := &fakeGoalExecutor{}
	r := NewRunStep("build", []string{"g1", "g2", "g3"}, exec)

	if err := r.Execute(t.Context(), NewState(nil)); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	want := []string{"g1", "g2", "g3"}
	if len(exec.ran) != len(want) {
		t.Fatalf("Execute() ran goals %v, want %v", exec.ran, want)
	}
	for i := range want {
		if exec.ran[i] != want[i] {
			t.Errorf("Execute() ran goals %v, want %v", exec.ran, want)
		}
	}
}

func TestRunStep_Execute_StopsOnFirstError(t *testing.T) {
	wantErr := errors.New("boom")
	exec := &fakeGoalExecutor{failAt: "g2", failErr: wantErr}
	r := NewRunStep("build", []string{"g1", "g2", "g3"}, exec)

	err := r.Execute(t.Context(), NewState(nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() = %v, want %v", err, wantErr)
	}
	want := []string{"g1", "g2"}
	if len(exec.ran) != len(want) {
		t.Fatalf("Execute() ran goals %v, want stop after %v", exec.ran, want)
	}
}

func TestRunStep_Execute_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	exec := &fakeGoalExecutor{}
	r := NewRunStep("build", []string{"g1", "g2"}, exec)

	err := r.Execute(ctx, NewState(nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() = %v, want context.Canceled", err)
	}
	if len(exec.ran) != 0 {
		t.Errorf("Execute() ran goals %v, want none (context already canceled)", exec.ran)
	}
}
