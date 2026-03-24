package step

import "context"

// GoalExecutor is the interface that the executor provides to run individual goals.
type GoalExecutor interface {
	ExecuteGoal(ctx context.Context, goalName string, state *State) error
}

// RunStep executes a named group of goals in sequence.
type RunStep struct {
	name      string
	goalNames []string
	executor  GoalExecutor
}

// NewRunStep creates a step that runs the given goals in sequence.
func NewRunStep(name string, goalNames []string, executor GoalExecutor) *RunStep {
	return &RunStep{name: name, goalNames: goalNames, executor: executor}
}

func (r *RunStep) Name() string { return r.name }

func (r *RunStep) Execute(ctx context.Context, state *State) error {
	for _, goalName := range r.goalNames {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.executor.ExecuteGoal(ctx, goalName, state); err != nil {
			return err
		}
	}
	return nil
}
