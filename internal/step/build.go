package step

import "github.com/vinayprograms/agent/internal/agentfile"

// BuildGraph converts an Agentfile workflow into a composable step graph.
func BuildGraph(workflow *agentfile.Workflow, executor GoalExecutor) Step {
	var steps []Step
	for _, s := range workflow.Steps {
		if s.Type == agentfile.StepRUN {
			steps = append(steps, NewRunStep(s.Name, s.UsingGoals, executor))
		}
	}
	if len(steps) == 1 {
		return steps[0]
	}
	return Sequence(steps...)
}
