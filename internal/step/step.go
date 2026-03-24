// Package step provides composable workflow step interfaces.
//
// Each step in a workflow implements the Step interface. Steps can be
// composed using Sequence to build a complete workflow graph.
package step

import "context"

// State carries data between workflow steps.
// Inputs are provided at the start; outputs accumulate as steps execute.
type State struct {
	Inputs  map[string]string
	Outputs map[string]string
}

// NewState creates a State with the given inputs.
func NewState(inputs map[string]string) *State {
	return &State{
		Inputs:  inputs,
		Outputs: make(map[string]string),
	}
}

// Step is a single unit of workflow execution.
type Step interface {
	// Name returns the step's identifier.
	Name() string
	// Execute runs the step, reading from and writing to state.
	Execute(ctx context.Context, state *State) error
}

// Sequence runs steps in order. If any step fails, execution stops.
func Sequence(steps ...Step) Step {
	return &sequence{steps: steps}
}

type sequence struct {
	steps []Step
}

func (s *sequence) Name() string {
	if len(s.steps) == 0 {
		return "empty"
	}
	return s.steps[0].Name() + "..."
}

func (s *sequence) Execute(ctx context.Context, state *State) error {
	for _, step := range s.steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := step.Execute(ctx, state); err != nil {
			return err
		}
	}
	return nil
}
