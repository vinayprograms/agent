package executor

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agentkit/llm"
)

// TestSplitConvergence_LenientFallback exercises the lenient prose fallback
// matching rule: the last non-empty line, after trimming whitespace,
// markdown fences, and trailing punctuation, must equal CONVERGED
// case-insensitively. It must not fire when the word appears mid-prose.
func TestSplitConvergence_LenientFallback(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantContent string
		wantDone    bool
	}{
		{"fenced marker on its own line", "Final answer.\n\n```CONVERGED```", "Final answer.", true},
		{"backtick-wrapped marker", "Done.\n`CONVERGED`", "Done.", true},
		{"trailing punctuation", "Done.\nCONVERGED.", "Done.", true},
		{"trailing punctuation with colon", "Done.\nCONVERGED:", "Done.", true},
		{"lowercase", "Done.\nconverged", "Done.", true},
		{"indented marker", "Done.\n   CONVERGED   ", "Done.", true},
		{"trailing blank lines after marker", "Done.\nCONVERGED\n\n", "Done.", true},
		{"word mid-prose is not terminal", "The build CONVERGED early and kept going", "The build CONVERGED early and kept going", false},
		{"marker not on last line", "CONVERGED\nmore work follows", "CONVERGED\nmore work follows", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, done := splitConvergence(tt.output)
			if content != tt.wantContent || done != tt.wantDone {
				t.Errorf("splitConvergence(%q) = (%q, %v), want (%q, %v)",
					tt.output, content, done, tt.wantContent, tt.wantDone)
			}
		})
	}
}

// TestConvergeGoal_ConvergedViaTool verifies that a single-agent CONVERGE
// goal converges via the converged tool call (the primary channel), and
// that the reason it reports ends up in the ConvergenceResult (and from
// there, the goal outcome).
func TestConvergeGoal_ConvergedViaTool(t *testing.T) {
	limit := 5
	wf := &agentfile.Workflow{
		Name: "converge-tool",
		Goals: []agentfile.Goal{{
			Name: "refine", Outcome: "Refine the thing", IsConverge: true, WithinLimit: &limit,
		}},
	}
	var calls int
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		calls++
		if calls == 1 {
			return &llm.ChatResponse{Content: "First draft"}, nil
		}
		// Second iteration: call the converged tool instead of writing prose.
		return &llm.ChatResponse{ToolCalls: []llm.ToolCallResponse{
			{ID: "1", Name: convergedToolName, Args: map[string]any{"reason": "the draft is complete"}},
		}}, nil
	})

	exec := mustNewExecutor(t, wf, model, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}
	if !result.Converged {
		t.Fatal("expected goal to converge")
	}
	if !result.ViaTool {
		t.Error("expected convergence to be reported via the converged tool")
	}
	if result.Reason != "the draft is complete" {
		t.Errorf("Reason = %q, want %q", result.Reason, "the draft is complete")
	}
	if result.Output != "First draft" {
		t.Errorf("Output = %q, want %q (tool call carries no new content)", result.Output, "First draft")
	}
}

// TestConvergeGoal_EmitOutputsViaTool verifies that a CONVERGE goal with
// declared `-> outputs` gets them bundled into the converged tool call, and
// that they land in ConvergenceResult.Vars without needing
// parseStructuredOutput.
func TestConvergeGoal_EmitOutputsViaTool(t *testing.T) {
	limit := 3
	wf := &agentfile.Workflow{
		Name: "converge-outputs",
		Goals: []agentfile.Goal{{
			Name: "refine", Outcome: "Refine the thing", IsConverge: true, WithinLimit: &limit,
			Outputs: []string{"summary"},
		}},
	}
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{ToolCalls: []llm.ToolCallResponse{
			{ID: "1", Name: convergedToolName, Args: map[string]any{
				"reason":  "done",
				"summary": "the final summary text",
			}},
		}}, nil
	})

	exec := mustNewExecutor(t, wf, model, nil, nil)
	result, err := exec.executeConvergeGoal(context.Background(), &wf.Goals[0])
	if err != nil {
		t.Fatalf("executeConvergeGoal() error = %v", err)
	}
	if !result.Converged || !result.ViaTool {
		t.Fatalf("expected convergence via tool, got %+v", result)
	}
	if result.Vars["summary"] != "the final summary text" {
		t.Errorf("Vars[summary] = %q, want %q", result.Vars["summary"], "the final summary text")
	}
}

// TestExecutePhase_EmitOutputsViaToolAndFallback covers both paths a plain
// GOAL's declared `-> outputs` can arrive by: the emit_outputs tool call
// (primary), and JSON-in-prose (parseStructuredOutput, the live fallback
// for a provider that ignores the offered tool).
func TestExecutePhase_EmitOutputsViaToolAndFallback(t *testing.T) {
	goal := &agentfile.Goal{Name: "g", Outcome: "Do the thing", Outputs: []string{"result"}}

	t.Run("via tool", func(t *testing.T) {
		model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{ToolCalls: []llm.ToolCallResponse{
				{ID: "1", Name: emitOutputsToolName, Args: map[string]any{"result": "the answer"}},
			}}, nil
		})
		exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, model, nil, nil)
		output, _, _, decision, err := exec.executePhase(context.Background(), goal, "do it", emitOutputsTool(goal.Outputs))
		if err != nil {
			t.Fatalf("executePhase: %v", err)
		}
		vars, viaTool := resolveOutputVars(output, decision, goal.Outputs)
		if !viaTool {
			t.Error("expected outputs to resolve via the emit_outputs tool")
		}
		if vars["result"] != "the answer" {
			t.Errorf("vars[result] = %q, want %q", vars["result"], "the answer")
		}
	})

	t.Run("fallback to prose JSON", func(t *testing.T) {
		model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: `{"result": "the answer via prose"}`}, nil
		})
		exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, model, nil, nil)
		output, _, _, decision, err := exec.executePhase(context.Background(), goal, "do it", emitOutputsTool(goal.Outputs))
		if err != nil {
			t.Fatalf("executePhase: %v", err)
		}
		vars, viaTool := resolveOutputVars(output, decision, goal.Outputs)
		if viaTool {
			t.Error("expected outputs to resolve via the prose fallback, not the tool")
		}
		if vars["result"] != "the answer via prose" {
			t.Errorf("vars[result] = %q, want %q", vars["result"], "the answer via prose")
		}
	})
}

// TestGoalFanOut_RunsConcurrently is a regression guard: GOAL's USING
// fan-out must stay parallel (unlike CONVERGE, which became a sequential
// pipeline). Two agents that each sleep briefly must overlap in time.
func TestGoalFanOut_RunsConcurrently(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "fanout",
		Agents: []agentfile.Agent{
			{Name: "agentA"},
			{Name: "agentB"},
		},
		Goals: []agentfile.Goal{{
			Name: "g", Outcome: "Do the thing", UsingAgent: []string{"agentA", "agentB"},
		}},
	}

	var mu sync.Mutex
	var starts []time.Time
	const sleep = 50 * time.Millisecond
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		sys := req.Messages[0].Content
		if strings.Contains(sys, "agentA") || strings.Contains(sys, "agentB") {
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			time.Sleep(sleep)
			return &llm.ChatResponse{Content: "done"}, nil
		}
		return &llm.ChatResponse{Content: "synthesized"}, nil
	})

	exec := mustNewExecutor(t, wf, model, nil, nil)
	if _, err := exec.executeMultiAgentGoal(context.Background(), &wf.Goals[0]); err != nil {
		t.Fatalf("executeMultiAgentGoal: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 2 {
		t.Fatalf("expected 2 agent starts, got %d", len(starts))
	}
	gap := starts[1].Sub(starts[0])
	if gap < 0 {
		gap = -gap
	}
	if gap >= sleep {
		t.Errorf("agents did not overlap: start gap %v >= sleep duration %v (fan-out looks sequential)", gap, sleep)
	}
}
