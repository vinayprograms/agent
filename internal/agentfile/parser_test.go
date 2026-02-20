package agentfile

import (
	"strings"
	"testing"
)

// R1.2.1: Parse NAME statement
func TestParser_NameStatement(t *testing.T) {
	input := `NAME my-workflow`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if wf.Name != "my-workflow" {
		t.Errorf("workflow name wrong. expected=%q, got=%q", "my-workflow", wf.Name)
	}
}

// R1.2.2: Parse INPUT statement with optional DEFAULT
func TestParser_InputStatement(t *testing.T) {
	input := `NAME test
INPUT feature_request
INPUT max_iterations DEFAULT 10`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(wf.Inputs))
	}

	// First input has no default
	if wf.Inputs[0].Name != "feature_request" {
		t.Errorf("input[0] name wrong. expected=%q, got=%q", "feature_request", wf.Inputs[0].Name)
	}
	if wf.Inputs[0].Default != nil {
		t.Errorf("input[0] should have no default")
	}

	// Second input has default
	if wf.Inputs[1].Name != "max_iterations" {
		t.Errorf("input[1] name wrong. expected=%q, got=%q", "max_iterations", wf.Inputs[1].Name)
	}
	if wf.Inputs[1].Default == nil {
		t.Errorf("input[1] should have default")
	} else if *wf.Inputs[1].Default != "10" {
		t.Errorf("input[1] default wrong. expected=%q, got=%q", "10", *wf.Inputs[1].Default)
	}
}

// R1.2.3: Parse AGENT statement with FROM path
func TestParser_AgentStatement(t *testing.T) {
	input := `NAME test
AGENT creative FROM agents/creative.md
AGENT devils_advocate FROM agents/devils_advocate.md`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(wf.Agents))
	}

	if wf.Agents[0].Name != "creative" {
		t.Errorf("agent[0] name wrong. expected=%q, got=%q", "creative", wf.Agents[0].Name)
	}
	if wf.Agents[0].FromPath != "agents/creative.md" {
		t.Errorf("agent[0] path wrong. expected=%q, got=%q", "agents/creative.md", wf.Agents[0].FromPath)
	}

	if wf.Agents[1].Name != "devils_advocate" {
		t.Errorf("agent[1] name wrong. expected=%q, got=%q", "devils_advocate", wf.Agents[1].Name)
	}
}

// R1.2.4: Parse GOAL statement with inline string
func TestParser_GoalInlineString(t *testing.T) {
	input := `NAME test
GOAL run_tests "Run all tests and capture any failures"`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(wf.Goals))
	}

	if wf.Goals[0].Name != "run_tests" {
		t.Errorf("goal name wrong. expected=%q, got=%q", "run_tests", wf.Goals[0].Name)
	}
	if wf.Goals[0].Outcome != "Run all tests and capture any failures" {
		t.Errorf("goal outcome wrong. expected=%q, got=%q", "Run all tests and capture any failures", wf.Goals[0].Outcome)
	}
}

// R1.2.4: Parse GOAL statement with FROM path
func TestParser_GoalFromPath(t *testing.T) {
	input := `NAME test
GOAL analyze FROM goals/analyze.md`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(wf.Goals))
	}

	if wf.Goals[0].Name != "analyze" {
		t.Errorf("goal name wrong. expected=%q, got=%q", "analyze", wf.Goals[0].Name)
	}
	if wf.Goals[0].FromPath != "goals/analyze.md" {
		t.Errorf("goal path wrong. expected=%q, got=%q", "goals/analyze.md", wf.Goals[0].FromPath)
	}
}

// R1.2.5: Parse GOAL statement with USING clause
func TestParser_GoalWithUsing(t *testing.T) {
	input := `NAME test
GOAL analyze FROM goals/analyze.md USING creative, devils_advocate`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(wf.Goals))
	}

	if len(wf.Goals[0].UsingAgent) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(wf.Goals[0].UsingAgent))
	}
	if wf.Goals[0].UsingAgent[0] != "creative" {
		t.Errorf("agent[0] wrong. expected=%q, got=%q", "creative", wf.Goals[0].UsingAgent[0])
	}
	if wf.Goals[0].UsingAgent[1] != "devils_advocate" {
		t.Errorf("agent[1] wrong. expected=%q, got=%q", "devils_advocate", wf.Goals[0].UsingAgent[1])
	}
}

// R1.2.6: Parse RUN statement with USING identifier list
func TestParser_RunStatement(t *testing.T) {
	input := `NAME test
GOAL analyze "Analyze the input"
GOAL build "Build the project"
RUN setup USING analyze, build`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(wf.Steps))
	}

	step := wf.Steps[0]
	if step.Type != StepRUN {
		t.Errorf("step type wrong. expected=RUN, got=%s", step.Type)
	}
	if step.Name != "setup" {
		t.Errorf("step name wrong. expected=%q, got=%q", "setup", step.Name)
	}
	if len(step.UsingGoals) != 2 {
		t.Fatalf("expected 2 goals, got %d", len(step.UsingGoals))
	}
	if step.UsingGoals[0] != "analyze" {
		t.Errorf("goal[0] wrong. expected=%q, got=%q", "analyze", step.UsingGoals[0])
	}
	if step.UsingGoals[1] != "build" {
		t.Errorf("goal[1] wrong. expected=%q, got=%q", "build", step.UsingGoals[1])
	}
}

// R1.2.7: Parse LOOP statement with USING and WITHIN
func TestParser_LoopStatementLiteral(t *testing.T) {
	input := `NAME test
GOAL run_tests "Run tests"
LOOP implementation USING run_tests WITHIN 10`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(wf.Steps))
	}

	step := wf.Steps[0]
	if step.Type != StepLOOP {
		t.Errorf("step type wrong. expected=LOOP, got=%s", step.Type)
	}
	if step.Name != "implementation" {
		t.Errorf("step name wrong. expected=%q, got=%q", "implementation", step.Name)
	}
	if step.WithinLimit == nil {
		t.Fatalf("expected within limit to be set")
	}
	if *step.WithinLimit != 10 {
		t.Errorf("within limit wrong. expected=10, got=%d", *step.WithinLimit)
	}
}

// R1.2.8: Support variable references in WITHIN clause
func TestParser_LoopStatementVariable(t *testing.T) {
	input := `NAME test
INPUT max_iterations DEFAULT 10
GOAL run_tests "Run tests"
LOOP implementation USING run_tests WITHIN $max_iterations`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	step := wf.Steps[0]
	if step.WithinVar != "max_iterations" {
		t.Errorf("within var wrong. expected=%q, got=%q", "max_iterations", step.WithinVar)
	}
	if step.WithinLimit != nil {
		t.Errorf("within limit should be nil when using variable")
	}
}

// R1.2.9: Produce AST with all node types
func TestParser_CompleteAgentfile(t *testing.T) {
	input := `# Agentfile: Test-Driven Feature Implementation

NAME implement-feature

INPUT feature_request
INPUT max_iterations DEFAULT 10

AGENT creative FROM agents/creative.md
AGENT devils_advocate FROM agents/devils_advocate.md

GOAL analyze FROM goals/analyze.md USING creative, devils_advocate
GOAL system_tests FROM goals/system_tests.md
GOAL run_tests "Run all tests and capture any failures"

RUN setup USING analyze, system_tests
LOOP implementation USING run_tests WITHIN $max_iterations`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if wf.Name != "implement-feature" {
		t.Errorf("name wrong: %s", wf.Name)
	}
	if len(wf.Inputs) != 2 {
		t.Errorf("expected 2 inputs, got %d", len(wf.Inputs))
	}
	if len(wf.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(wf.Agents))
	}
	if len(wf.Goals) != 3 {
		t.Errorf("expected 3 goals, got %d", len(wf.Goals))
	}
	if len(wf.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(wf.Steps))
	}
}

// R1.2.10: Report syntax errors with line numbers
func TestParser_SyntaxErrors(t *testing.T) {
	tests := []struct {
		input         string
		expectedError string
	}{
		{
			input:         `AGENT creative`,
			expectedError: "line 1",
		},
		{
			input:         `NAME test
GOAL analyze`,
			expectedError: "line 2",
		},
		{
			input:         `NAME test
RUN setup`,
			expectedError: "line 2",
		},
		{
			input:         `NAME test
LOOP impl USING test`,
			expectedError: "line 2", // missing WITHIN
		},
	}

	for i, tt := range tests {
		p := NewParser(NewLexer(tt.input))
		_, err := p.Parse()
		if err == nil {
			t.Errorf("tests[%d] - expected error, got nil", i)
			continue
		}
		if !strings.Contains(err.Error(), tt.expectedError) {
			t.Errorf("tests[%d] - error should contain %q, got %q", i, tt.expectedError, err.Error())
		}
	}
}

// Test multiple RUN and LOOP in order
func TestParser_MultipleSteps(t *testing.T) {
	input := `NAME test
GOAL a "Goal A"
GOAL b "Goal B"
GOAL c "Goal C"
RUN first USING a
RUN second USING b
LOOP third USING c WITHIN 5`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(wf.Steps))
	}

	if wf.Steps[0].Name != "first" || wf.Steps[0].Type != StepRUN {
		t.Errorf("step[0] wrong")
	}
	if wf.Steps[1].Name != "second" || wf.Steps[1].Type != StepRUN {
		t.Errorf("step[1] wrong")
	}
	if wf.Steps[2].Name != "third" || wf.Steps[2].Type != StepLOOP {
		t.Errorf("step[2] wrong")
	}
}

// Test INPUT with string default
func TestParser_InputStringDefault(t *testing.T) {
	input := `NAME test
INPUT greeting DEFAULT "hello world"`

	p := NewParser(NewLexer(input))
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}

	if len(wf.Inputs) != 1 {
		t.Fatalf("expected 1 input, got %d", len(wf.Inputs))
	}

	if wf.Inputs[0].Default == nil {
		t.Fatal("expected default value")
	}
	if *wf.Inputs[0].Default != "hello world" {
		t.Errorf("default wrong. expected=%q, got=%q", "hello world", *wf.Inputs[0].Default)
	}
}

func TestParseAgentWithRequires(t *testing.T) {
    input := `NAME test
AGENT critic FROM agents/critic.md REQUIRES "reasoning-heavy"
GOAL test "Test goal" USING critic
RUN main USING test`

    wf, err := ParseString(input)
    if err != nil {
        t.Fatalf("ParseString failed: %v", err)
    }
    
    if len(wf.Agents) != 1 {
        t.Fatalf("expected 1 agent, got %d", len(wf.Agents))
    }
    
    agent := wf.Agents[0]
    if agent.Requires != "reasoning-heavy" {
        t.Errorf("expected Requires='reasoning-heavy', got %q", agent.Requires)
    }
}

// Test GOAL with structured output
func TestParser_GoalWithOutputs(t *testing.T) {
	input := `NAME test
GOAL assess "Assess code at $path" -> current_state, opportunities, priority
RUN main USING assess`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	if len(wf.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(wf.Goals))
	}

	goal := wf.Goals[0]
	if goal.Name != "assess" {
		t.Errorf("expected name 'assess', got %q", goal.Name)
	}
	if goal.Outcome != "Assess code at $path" {
		t.Errorf("expected outcome 'Assess code at $path', got %q", goal.Outcome)
	}
	if len(goal.Outputs) != 3 {
		t.Fatalf("expected 3 outputs, got %d", len(goal.Outputs))
	}
	expectedOutputs := []string{"current_state", "opportunities", "priority"}
	for i, expected := range expectedOutputs {
		if goal.Outputs[i] != expected {
			t.Errorf("output[%d]: expected %q, got %q", i, expected, goal.Outputs[i])
		}
	}
}

// Test GOAL with outputs and USING
func TestParser_GoalWithOutputsAndUsing(t *testing.T) {
	input := `NAME test
AGENT researcher "Research $topic" -> findings, sources
AGENT critic "Find biases" -> issues, concerns
GOAL analyze "Analyze $topic" -> summary, recommendations USING researcher, critic
RUN main USING analyze`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	if len(wf.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(wf.Goals))
	}

	goal := wf.Goals[0]
	if len(goal.Outputs) != 2 {
		t.Fatalf("expected 2 goal outputs, got %d", len(goal.Outputs))
	}
	if goal.Outputs[0] != "summary" || goal.Outputs[1] != "recommendations" {
		t.Errorf("unexpected goal outputs: %v", goal.Outputs)
	}
	if len(goal.UsingAgent) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(goal.UsingAgent))
	}
	if goal.UsingAgent[0] != "researcher" || goal.UsingAgent[1] != "critic" {
		t.Errorf("unexpected agents: %v", goal.UsingAgent)
	}
}

// Test AGENT with structured output
func TestParser_AgentWithOutputs(t *testing.T) {
	input := `NAME test
AGENT researcher "Research $topic thoroughly" -> findings, sources, confidence
GOAL analyze "Analyze" USING researcher
RUN main USING analyze`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	if len(wf.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(wf.Agents))
	}

	agent := wf.Agents[0]
	if agent.Name != "researcher" {
		t.Errorf("expected name 'researcher', got %q", agent.Name)
	}
	if agent.Prompt != "Research $topic thoroughly" {
		t.Errorf("expected prompt 'Research $topic thoroughly', got %q", agent.Prompt)
	}
	if len(agent.Outputs) != 3 {
		t.Fatalf("expected 3 outputs, got %d", len(agent.Outputs))
	}
	expectedOutputs := []string{"findings", "sources", "confidence"}
	for i, expected := range expectedOutputs {
		if agent.Outputs[i] != expected {
			t.Errorf("output[%d]: expected %q, got %q", i, expected, agent.Outputs[i])
		}
	}
}

// Test AGENT with outputs and REQUIRES
func TestParser_AgentWithOutputsAndRequires(t *testing.T) {
	input := `NAME test
AGENT critic FROM agents/critic.md -> issues, severity REQUIRES "reasoning-heavy"
GOAL analyze "Analyze" USING critic
RUN main USING analyze`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	agent := wf.Agents[0]
	if len(agent.Outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(agent.Outputs))
	}
	if agent.Outputs[0] != "issues" || agent.Outputs[1] != "severity" {
		t.Errorf("unexpected outputs: %v", agent.Outputs)
	}
	if agent.Requires != "reasoning-heavy" {
		t.Errorf("expected requires 'reasoning-heavy', got %q", agent.Requires)
	}
}

// Test GOAL FROM with outputs
func TestParser_GoalFromWithOutputs(t *testing.T) {
	input := `NAME test
GOAL assess FROM prompts/assess.md -> findings, recommendations
RUN main USING assess`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if goal.FromPath != "prompts/assess.md" {
		t.Errorf("expected FromPath 'prompts/assess.md', got %q", goal.FromPath)
	}
	if len(goal.Outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(goal.Outputs))
	}
	if goal.Outputs[0] != "findings" || goal.Outputs[1] != "recommendations" {
		t.Errorf("unexpected outputs: %v", goal.Outputs)
	}
}

// Test lexer arrow token
func TestLexer_ArrowToken(t *testing.T) {
	input := `-> output1, output2`
	l := NewLexer(input)

	tok := l.NextToken()
	if tok.Type != TokenArrow {
		t.Errorf("expected ARROW token, got %s", tok.Type)
	}
	if tok.Literal != "->" {
		t.Errorf("expected literal '->', got %q", tok.Literal)
	}

	tok = l.NextToken()
	if tok.Type != TokenIdent || tok.Literal != "output1" {
		t.Errorf("expected IDENT 'output1', got %s %q", tok.Type, tok.Literal)
	}
}

// Test global SUPERVISED keyword
func TestParser_GlobalSupervised(t *testing.T) {
	input := `SUPERVISED
NAME supervised-workflow
GOAL analyze "Analyze the data"
RUN main USING analyze`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	if !wf.Supervised {
		t.Error("expected workflow to be supervised")
	}
	if wf.HumanOnly {
		t.Error("expected HumanOnly to be false")
	}
}

// Test global SUPERVISED HUMAN
func TestParser_GlobalSupervisedHuman(t *testing.T) {
	input := `SUPERVISED HUMAN
NAME human-supervised-workflow
GOAL deploy "Deploy to production"
RUN main USING deploy`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	if !wf.Supervised {
		t.Error("expected workflow to be supervised")
	}
	if !wf.HumanOnly {
		t.Error("expected HumanOnly to be true")
	}
}

// Test GOAL with inline SUPERVISED
func TestParser_GoalSupervised(t *testing.T) {
	input := `NAME test
GOAL risky_operation "Perform risky operation" SUPERVISED
RUN main USING risky_operation`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if goal.Supervision != SupervisionEnabled {
		t.Error("expected goal to be supervised")
	}
	if goal.HumanOnly {
		t.Error("expected goal HumanOnly to be false")
	}
}

// Test GOAL with SUPERVISED HUMAN
func TestParser_GoalSupervisedHuman(t *testing.T) {
	input := `NAME test
GOAL deploy "Deploy to production" SUPERVISED HUMAN
RUN main USING deploy`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if goal.Supervision != SupervisionEnabled {
		t.Error("expected goal to be supervised")
	}
	if !goal.HumanOnly {
		t.Error("expected goal HumanOnly to be true")
	}
}

// Test GOAL with UNSUPERVISED
func TestParser_GoalUnsupervised(t *testing.T) {
	input := `SUPERVISED
NAME test
GOAL trivial "Do something trivial" UNSUPERVISED
RUN main USING trivial`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	if !wf.Supervised {
		t.Error("expected workflow to be supervised")
	}

	goal := wf.Goals[0]
	if goal.Supervision != SupervisionDisabled {
		t.Error("expected goal to be unsupervised")
	}
}

// Test AGENT with SUPERVISED
func TestParser_AgentSupervised(t *testing.T) {
	input := `NAME test
AGENT critic "Find issues" SUPERVISED
GOAL analyze "Analyze" USING critic
RUN main USING analyze`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	agent := wf.Agents[0]
	if agent.Supervision != SupervisionEnabled {
		t.Error("expected agent to be supervised")
	}
}

// Test AGENT with SUPERVISED HUMAN and REQUIRES
func TestParser_AgentSupervisedHumanWithRequires(t *testing.T) {
	input := `NAME test
AGENT deployer "Deploy changes" REQUIRES "production" SUPERVISED HUMAN
GOAL deploy "Deploy" USING deployer
RUN main USING deploy`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	agent := wf.Agents[0]
	if agent.Requires != "production" {
		t.Errorf("expected Requires 'production', got %q", agent.Requires)
	}
	if agent.Supervision != SupervisionEnabled {
		t.Error("expected agent to be supervised")
	}
	if !agent.HumanOnly {
		t.Error("expected agent HumanOnly to be true")
	}
}

// Test RUN with SUPERVISED
func TestParser_RunSupervised(t *testing.T) {
	input := `NAME test
GOAL analyze "Analyze"
RUN main USING analyze SUPERVISED`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	step := wf.Steps[0]
	if step.Supervision != SupervisionEnabled {
		t.Error("expected step to be supervised")
	}
}

// Test LOOP with SUPERVISED HUMAN
func TestParser_LoopSupervisedHuman(t *testing.T) {
	input := `NAME test
GOAL refine "Refine"
LOOP refine_loop USING refine WITHIN 10 SUPERVISED HUMAN`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	step := wf.Steps[0]
	if step.Type != StepLOOP {
		t.Error("expected LOOP step")
	}
	if step.Supervision != SupervisionEnabled {
		t.Error("expected step to be supervised")
	}
	if !step.HumanOnly {
		t.Error("expected step HumanOnly to be true")
	}
}

// Test mixed supervision levels
func TestParser_MixedSupervision(t *testing.T) {
	input := `SUPERVISED
NAME mixed-workflow
GOAL analyze "Analyze data"
GOAL trivial "Simple task" UNSUPERVISED
GOAL deploy "Deploy" SUPERVISED HUMAN
RUN phase1 USING analyze
RUN phase2 USING trivial
RUN phase3 USING deploy`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	// Workflow level
	if !wf.Supervised {
		t.Error("expected workflow to be supervised")
	}

	// Goal 1: inherits from workflow (supervised)
	if wf.Goals[0].Supervision != SupervisionInherit {
		t.Error("expected goal[0].Supervision to be Inherit")
	}

	// Goal 2: explicitly unsupervised
	if wf.Goals[1].Supervision != SupervisionDisabled {
		t.Error("expected goal[1] to be unsupervised")
	}

	// Goal 3: explicitly supervised with human
	if wf.Goals[2].Supervision != SupervisionEnabled {
		t.Error("expected goal[2] to be supervised")
	}
	if !wf.Goals[2].HumanOnly {
		t.Error("expected goal[2] HumanOnly to be true")
	}
}

// Test HasHumanRequiredSteps
func TestWorkflow_HasHumanRequiredSteps(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name: "no supervision",
			input: `NAME test
GOAL a "A"
RUN main USING a`,
			expected: false,
		},
		{
			name: "supervised but not human",
			input: `SUPERVISED
NAME test
GOAL a "A"
RUN main USING a`,
			expected: false,
		},
		{
			name: "global supervised human",
			input: `SUPERVISED HUMAN
NAME test
GOAL a "A"
RUN main USING a`,
			expected: true,
		},
		{
			name: "one step requires human",
			input: `NAME test
GOAL a "A"
GOAL b "B" SUPERVISED HUMAN
RUN main USING a, b`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, err := ParseString(tt.input)
			if err != nil {
				t.Fatalf("ParseString failed: %v", err)
			}

			got := wf.HasHumanRequiredSteps()
			if got != tt.expected {
				t.Errorf("HasHumanRequiredSteps() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test GetHumanRequiredStepNames
func TestWorkflow_GetHumanRequiredStepNames(t *testing.T) {
	input := `NAME test
GOAL analyze "Analyze"
GOAL deploy "Deploy" SUPERVISED HUMAN
GOAL cleanup "Cleanup" SUPERVISED HUMAN
RUN main USING analyze, deploy, cleanup`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	names := wf.GetHumanRequiredStepNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}

	// Check that deploy and cleanup are in the list
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["deploy"] || !found["cleanup"] {
		t.Errorf("expected deploy and cleanup, got %v", names)
	}
}

// TestParser_SecurityMode tests parsing SECURITY directive.
func TestParser_SecurityMode(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedMode string
		shouldError  bool
	}{
		{
			name:         "default mode",
			input:        "SECURITY default\nNAME test\nGOAL g \"do it\"\nRUN r USING g",
			expectedMode: "default",
		},
		{
			name:         "paranoid mode",
			input:        "SECURITY paranoid\nNAME test\nGOAL g \"do it\"\nRUN r USING g",
			expectedMode: "paranoid",
		},
		{
			name:        "invalid mode",
			input:       "SECURITY invalid\nNAME test",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser(NewLexer(tt.input))
			wf, err := p.Parse()

			if tt.shouldError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if wf.SecurityMode != tt.expectedMode {
				t.Errorf("expected SecurityMode=%q, got %q", tt.expectedMode, wf.SecurityMode)
			}
		})
	}
}

// TestParser_SecurityResearchMode tests parsing SECURITY research with scope.
func TestParser_SecurityResearchMode(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedMode  string
		expectedScope string
		shouldError   bool
	}{
		{
			name:          "research mode with scope",
			input:         `SECURITY research "authorized pentest of lab environment"` + "\nNAME test\nGOAL g \"do it\"\nRUN r USING g",
			expectedMode:  "research",
			expectedScope: "authorized pentest of lab environment",
		},
		{
			name:          "research mode with complex scope",
			input:         `SECURITY research "vulnerability assessment of internal network 192.168.1.0/24"` + "\nNAME test\nGOAL g \"do it\"\nRUN r USING g",
			expectedMode:  "research",
			expectedScope: "vulnerability assessment of internal network 192.168.1.0/24",
		},
		{
			name:        "research mode without scope",
			input:       "SECURITY research\nNAME test",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser(NewLexer(tt.input))
			wf, err := p.Parse()

			if tt.shouldError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if wf.SecurityMode != tt.expectedMode {
				t.Errorf("expected SecurityMode=%q, got %q", tt.expectedMode, wf.SecurityMode)
			}

			if wf.SecurityScope != tt.expectedScope {
				t.Errorf("expected SecurityScope=%q, got %q", tt.expectedScope, wf.SecurityScope)
			}
		})
	}
}

// TestParser_ConvergeStatement tests parsing CONVERGE with literal limit.
func TestParser_ConvergeStatement(t *testing.T) {
	input := `NAME test
CONVERGE refine "Refine the code until clean" WITHIN 10
RUN main USING refine`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	if len(wf.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(wf.Goals))
	}

	goal := wf.Goals[0]
	if goal.Name != "refine" {
		t.Errorf("expected name 'refine', got %q", goal.Name)
	}
	if goal.Outcome != "Refine the code until clean" {
		t.Errorf("expected outcome 'Refine the code until clean', got %q", goal.Outcome)
	}
	if !goal.IsConverge {
		t.Error("expected IsConverge to be true")
	}
	if goal.WithinLimit == nil || *goal.WithinLimit != 10 {
		t.Errorf("expected WithinLimit=10, got %v", goal.WithinLimit)
	}
}

// TestParser_ConvergeStatementVariable tests parsing CONVERGE with variable limit.
func TestParser_ConvergeStatementVariable(t *testing.T) {
	input := `NAME test
INPUT max_iterations DEFAULT 5
CONVERGE refine "Refine iteratively" WITHIN $max_iterations
RUN main USING refine`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if !goal.IsConverge {
		t.Error("expected IsConverge to be true")
	}
	if goal.WithinLimit != nil {
		t.Error("expected WithinLimit to be nil for variable reference")
	}
	if goal.WithinVar != "max_iterations" {
		t.Errorf("expected WithinVar='max_iterations', got %q", goal.WithinVar)
	}
}

// TestParser_ConvergeWithOutputs tests parsing CONVERGE with output variable.
func TestParser_ConvergeWithOutputs(t *testing.T) {
	input := `NAME test
CONVERGE refine "Refine until done" WITHIN 10 -> result
RUN main USING refine`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if len(goal.Outputs) != 1 || goal.Outputs[0] != "result" {
		t.Errorf("expected Outputs=['result'], got %v", goal.Outputs)
	}
}

// TestParser_ConvergeWithUsing tests parsing CONVERGE with USING clause.
func TestParser_ConvergeWithUsing(t *testing.T) {
	input := `NAME test
AGENT reviewer "You are a code reviewer"
CONVERGE polish "Polish the code" WITHIN 5 USING reviewer
RUN main USING polish`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if len(goal.UsingAgent) != 1 || goal.UsingAgent[0] != "reviewer" {
		t.Errorf("expected UsingAgent=['reviewer'], got %v", goal.UsingAgent)
	}
}

// TestParser_ConvergeSupervised tests parsing CONVERGE with SUPERVISED modifier.
func TestParser_ConvergeSupervised(t *testing.T) {
	input := `NAME test
CONVERGE refine "Refine carefully" WITHIN 10 SUPERVISED HUMAN
RUN main USING refine`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if goal.Supervision != SupervisionEnabled {
		t.Error("expected goal to be supervised")
	}
	if !goal.HumanOnly {
		t.Error("expected HumanOnly to be true")
	}
}

// TestParser_ConvergeFull tests parsing CONVERGE with all options.
func TestParser_ConvergeFull(t *testing.T) {
	input := `NAME test
AGENT critic "You critique code"
CONVERGE polish "Polish until perfect" WITHIN 8 -> refined_code USING critic SUPERVISED
RUN main USING polish`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}

	goal := wf.Goals[0]
	if goal.Name != "polish" {
		t.Errorf("expected name 'polish', got %q", goal.Name)
	}
	if !goal.IsConverge {
		t.Error("expected IsConverge to be true")
	}
	if goal.WithinLimit == nil || *goal.WithinLimit != 8 {
		t.Errorf("expected WithinLimit=8, got %v", goal.WithinLimit)
	}
	if len(goal.Outputs) != 1 || goal.Outputs[0] != "refined_code" {
		t.Errorf("expected Outputs=['refined_code'], got %v", goal.Outputs)
	}
	if len(goal.UsingAgent) != 1 || goal.UsingAgent[0] != "critic" {
		t.Errorf("expected UsingAgent=['critic'], got %v", goal.UsingAgent)
	}
	if goal.Supervision != SupervisionEnabled {
		t.Error("expected goal to be supervised")
	}
}

// TestParser_ConvergeMissingWithin tests that CONVERGE without WITHIN fails.
func TestParser_ConvergeMissingWithin(t *testing.T) {
	input := `NAME test
CONVERGE refine "Refine forever"
RUN main USING refine`

	_, err := ParseString(input)
	if err == nil {
		t.Error("expected error for CONVERGE without WITHIN, got nil")
	}
}
