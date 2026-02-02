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
