package step

import (
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
)

func TestBuildGraph(t *testing.T) {
	tests := []struct {
		name  string
		steps []agentfile.Step
		want  string // expected Name() of the built graph
	}{
		{
			name:  "no steps",
			steps: nil,
			want:  "empty",
		},
		{
			name: "single run step",
			steps: []agentfile.Step{
				{Type: agentfile.StepRUN, Name: "build", UsingGoals: []string{"g1"}},
			},
			want: "build",
		},
		{
			name: "multiple run steps become a sequence",
			steps: []agentfile.Step{
				{Type: agentfile.StepRUN, Name: "build", UsingGoals: []string{"g1"}},
				{Type: agentfile.StepRUN, Name: "test", UsingGoals: []string{"g2"}},
			},
			want: "build...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := &agentfile.Workflow{Steps: tt.steps}
			got := BuildGraph(wf, &fakeGoalExecutor{})
			if got.Name() != tt.want {
				t.Errorf("BuildGraph(%+v).Name() = %q, want %q", tt.steps, got.Name(), tt.want)
			}
		})
	}
}
