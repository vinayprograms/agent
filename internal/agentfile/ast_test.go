package agentfile

import "testing"

func TestStep_IsSupervised(t *testing.T) {
	tests := []struct {
		name       string
		supervised SupervisionMode
		wfDefault  bool
		want       bool
	}{
		{"inherit falls back to workflow true", SupervisionInherit, true, true},
		{"inherit falls back to workflow false", SupervisionInherit, false, false},
		{"explicit enabled overrides false default", SupervisionEnabled, false, true},
		{"explicit disabled overrides true default", SupervisionDisabled, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Step{Supervision: tt.supervised}
			wf := &Workflow{Supervised: tt.wfDefault}
			if got := s.IsSupervised(wf); got != tt.want {
				t.Errorf("Step.IsSupervised() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStep_RequiresHuman(t *testing.T) {
	tests := []struct {
		name string
		step Step
		wf   Workflow
		want bool
	}{
		{"explicit supervised human", Step{Supervision: SupervisionEnabled, HumanOnly: true}, Workflow{}, true},
		{"explicit supervised not human", Step{Supervision: SupervisionEnabled, HumanOnly: false}, Workflow{}, false},
		{"inherit, workflow supervised human", Step{Supervision: SupervisionInherit}, Workflow{Supervised: true, HumanOnly: true}, true},
		{"inherit, step human overrides", Step{Supervision: SupervisionInherit, HumanOnly: true}, Workflow{Supervised: true}, true},
		{"inherit, workflow not supervised", Step{Supervision: SupervisionInherit}, Workflow{Supervised: false}, false},
		{"explicit disabled", Step{Supervision: SupervisionDisabled}, Workflow{Supervised: true, HumanOnly: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.step.RequiresHuman(&tt.wf); got != tt.want {
				t.Errorf("Step.RequiresHuman() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGoal_IsSupervised(t *testing.T) {
	g := &Goal{Supervision: SupervisionEnabled}
	if !g.IsSupervised(&Workflow{}) {
		t.Error("Goal.IsSupervised() = false, want true")
	}
	g2 := &Goal{Supervision: SupervisionInherit}
	if g2.IsSupervised(&Workflow{Supervised: false}) {
		t.Error("Goal.IsSupervised() = true, want false")
	}
}

func TestGoal_RequiresHuman(t *testing.T) {
	tests := []struct {
		name string
		goal Goal
		wf   Workflow
		want bool
	}{
		{"explicit supervised human", Goal{Supervision: SupervisionEnabled, HumanOnly: true}, Workflow{}, true},
		{"explicit supervised not human", Goal{Supervision: SupervisionEnabled}, Workflow{}, false},
		{"inherit, workflow supervised human", Goal{Supervision: SupervisionInherit}, Workflow{Supervised: true, HumanOnly: true}, true},
		{"inherit, workflow not supervised", Goal{Supervision: SupervisionInherit}, Workflow{}, false},
		{"explicit disabled", Goal{Supervision: SupervisionDisabled}, Workflow{Supervised: true, HumanOnly: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.goal.RequiresHuman(&tt.wf); got != tt.want {
				t.Errorf("Goal.RequiresHuman() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgent_IsSupervised(t *testing.T) {
	a := &Agent{Supervision: SupervisionDisabled}
	if a.IsSupervised(&Workflow{Supervised: true}) {
		t.Error("Agent.IsSupervised() = true, want false")
	}
}

func TestAgent_RequiresHuman(t *testing.T) {
	tests := []struct {
		name  string
		agent Agent
		wf    Workflow
		want  bool
	}{
		{"explicit supervised human", Agent{Supervision: SupervisionEnabled, HumanOnly: true}, Workflow{}, true},
		{"explicit supervised not human", Agent{Supervision: SupervisionEnabled}, Workflow{}, false},
		{"inherit, workflow supervised human", Agent{Supervision: SupervisionInherit}, Workflow{Supervised: true, HumanOnly: true}, true},
		{"inherit, workflow not supervised", Agent{Supervision: SupervisionInherit}, Workflow{}, false},
		{"explicit disabled", Agent{Supervision: SupervisionDisabled}, Workflow{Supervised: true, HumanOnly: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.agent.RequiresHuman(&tt.wf); got != tt.want {
				t.Errorf("Agent.RequiresHuman() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWorkflow_HasHumanRequiredSteps_ByField exercises the struct-literal
// branches (agent required-human) not covered by the parser-driven table in
// parser_test.go's TestWorkflow_HasHumanRequiredSteps.
func TestWorkflow_HasHumanRequiredSteps_ByField(t *testing.T) {
	wf := Workflow{Agents: []Agent{{Supervision: SupervisionEnabled, HumanOnly: true}}}
	if !wf.HasHumanRequiredSteps() {
		t.Error("Workflow.HasHumanRequiredSteps() = false, want true (agent requires human)")
	}
}

func TestWorkflow_HasHumanRequiredSteps_StepField(t *testing.T) {
	wf := Workflow{Steps: []Step{{Name: "s", Supervision: SupervisionEnabled, HumanOnly: true}}}
	if !wf.HasHumanRequiredSteps() {
		t.Error("Workflow.HasHumanRequiredSteps() = false, want true (step requires human)")
	}
}

func TestWorkflow_HumanRequiredStepNames_StepField(t *testing.T) {
	wf := Workflow{Steps: []Step{{Name: "s", Supervision: SupervisionEnabled, HumanOnly: true}}}
	got := wf.HumanRequiredStepNames()
	if len(got) != 1 || got[0] != "s" {
		t.Errorf("HumanRequiredStepNames() = %v, want [s]", got)
	}
}

func TestWorkflow_HumanRequiredStepNames_None(t *testing.T) {
	wf := Workflow{}
	if got := wf.HumanRequiredStepNames(); got != nil {
		t.Errorf("HumanRequiredStepNames() = %v, want nil", got)
	}
}

func TestWorkflow_HasSupervisedGoals(t *testing.T) {
	tests := []struct {
		name string
		wf   Workflow
		want bool
	}{
		{"workflow-level supervised", Workflow{Supervised: true}, true},
		{"nothing supervised", Workflow{}, false},
		{"goal supervised", Workflow{Goals: []Goal{{Supervision: SupervisionEnabled}}}, true},
		{"step supervised", Workflow{Steps: []Step{{Supervision: SupervisionEnabled}}}, true},
		{"agent supervised", Workflow{Agents: []Agent{{Supervision: SupervisionEnabled}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.wf.HasSupervisedGoals(); got != tt.want {
				t.Errorf("Workflow.HasSupervisedGoals() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNode_Marker exercises the unexported node() marker methods that only
// exist to satisfy the Node interface (no observable behavior beyond that).
func TestNode_Marker(t *testing.T) {
	var nodes = []Node{
		&Workflow{},
		&Input{},
		&Agent{},
		&Goal{},
		&Step{},
	}
	for _, n := range nodes {
		n.node() // must not panic
	}
}
