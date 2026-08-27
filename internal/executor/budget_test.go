package executor

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/tools"
)

func TestBudget_Spend(t *testing.T) {
	tests := []struct {
		name    string
		limits  Budget
		rounds  []int // tool calls per spend
		wantErr string
	}{
		{name: "unlimited", rounds: []int{9, 9, 9}},
		{
			name:    "tool calls",
			limits:  Budget{MaxToolCalls: 5},
			rounds:  []int{2, 2, 2},
			wantErr: `goal "g" exceeded budget: 6 tool calls (max 5)`,
		},
		{
			name:    "turns",
			limits:  Budget{MaxTurns: 2},
			rounds:  []int{0, 0},
			wantErr: `goal "g" exceeded budget: 2 turns (max 2)`,
		},
		{name: "under both limits", limits: Budget{MaxTurns: 5, MaxToolCalls: 5}, rounds: []int{1, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &budget{goal: "g", limits: tt.limits, start: time.Now()}
			var err error
			for _, calls := range tt.rounds {
				if err = b.spend(calls); err != nil {
					break
				}
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("spend() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Errorf("spend() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// A nil budget — no budget in context — is unlimited.
func TestBudget_NilIsUnlimited(t *testing.T) {
	if err := budgetOf(context.Background()).spend(1000); err != nil {
		t.Errorf("spend on nil budget = %v, want nil", err)
	}
}

func TestBudget_MaxDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &budget{goal: "g", limits: Budget{MaxDuration: time.Minute}, start: time.Now()}
		if err := b.spend(0); err != nil {
			t.Fatalf("spend before the deadline = %v, want nil", err)
		}
		time.Sleep(time.Minute)
		err := b.spend(0)
		if err == nil || !strings.Contains(err.Error(), "ran for 1m0s (max 1m0s)") {
			t.Errorf("spend after the deadline = %v, want a duration budget error", err)
		}
	})
}

// The budget is per goal: a runaway tool loop ends that goal with whatever it
// produced, records a warning event, and lets the workflow continue.
func TestBudget_EndsGoalNotRun(t *testing.T) {
	var calls atomic.Int32
	loop := fakeTool{name: "search", run: func(context.Context, tools.Args) (string, error) {
		calls.Add(1)
		return "more results", nil
	}}
	reg, _ := newTestRegistry(t, t.TempDir(), loop)

	var mu sync.Mutex
	turn := 0
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		turn++
		// The second goal answers immediately; the first never stops.
		if strings.Contains(req.Messages[1].Content, "finish up") {
			return &llm.ChatResponse{Content: "wrapped up"}, nil
		}
		return &llm.ChatResponse{
			Content:   "still searching",
			ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "search", Args: map[string]any{}}},
		}, nil
	})

	wf := &agentfile.Workflow{
		Name:  "budget",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"research", "report"}}},
		Goals: []agentfile.Goal{
			{Name: "research", Outcome: "search forever"},
			{Name: "report", Outcome: "finish up"},
		},
	}
	sess := &session.Session{}
	exec := mustNew(t, Config{
		Workflow: wf,
		Model:    model,
		Registry: reg,
		Policy:   permissivePolicy(),
		Session:  sess,
		Budget:   Budget{MaxToolCalls: 4},
	})

	result, err := exec.Run(t.Context(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// "research" never stops calling tools, even on its continuation
	// retry, so it stays budget_exhausted; "report" is ok. One of two
	// goals ok makes the workflow partial, not complete.
	if result.Status != StatusPartial {
		t.Errorf("Status = %v, want %v", result.Status, StatusPartial)
	}
	if oc := result.Goals["research"]; oc.Outcome != OutcomeBudgetExhausted {
		t.Errorf("Goals[research].Outcome = %v, want %v", oc.Outcome, OutcomeBudgetExhausted)
	}
	if oc := result.Goals["report"]; oc.Outcome != OutcomeOK {
		t.Errorf("Goals[report].Outcome = %v, want %v", oc.Outcome, OutcomeOK)
	}
	if got := calls.Load(); got > 4+5 {
		t.Errorf("tool calls = %d, want at most the budget of 4 plus the 5-call retry budget", got)
	}
	if got := result.Outputs["report"]; got != "wrapped up" {
		t.Errorf("Outputs[report] = %q, want %q — later goals must still run", got, "wrapped up")
	}

	var warned string
	for _, evt := range sess.Snapshot() {
		if evt.Type == session.EventWarning && strings.Contains(evt.Content, "exceeded budget") {
			warned = evt.Content
		}
	}
	if !strings.Contains(warned, `goal "research" exceeded budget`) {
		t.Errorf("session warning = %q, want one naming the research goal's budget", warned)
	}
}

// A CONVERGE goal with USING agents shares its budget across every
// iteration's agents. Once it's spent, the goal must end — not swallow the
// budget error as a successful iteration and start another round (which
// re-trips the same exhausted budget and never stops). Regression test for
// the bug where spawnAgentWithPrompt's EXECUTE phase turned a *budgetError
// into a nil error, hiding it from the convergence loop.
func TestBudget_MultiAgentConvergeEndsGoal(t *testing.T) {
	loop := fakeTool{name: "search", run: func(context.Context, tools.Args) (string, error) {
		return "more results", nil
	}}
	reg, _ := newTestRegistry(t, t.TempDir(), loop)

	// Every agent, every turn, keeps calling the tool — never converges on
	// its own. Only the shared budget can stop this.
	model := modelFunc(func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{
			Content:   "still refining",
			ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "search", Args: map[string]any{}}},
		}, nil
	})

	limit := 25 // convergence iteration limit: budget must end this long before it's reached
	wf := &agentfile.Workflow{
		Name:  "budget-converge",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"refine"}}},
		Goals: []agentfile.Goal{{
			Name: "refine", Outcome: "Refine forever", IsConverge: true,
			WithinLimit: &limit, UsingAgent: []string{"researcher", "critic"},
		}},
		Agents: []agentfile.Agent{{Name: "researcher"}, {Name: "critic"}},
	}

	sess := &session.Session{}
	var logBuf bytes.Buffer
	exec := mustNew(t, Config{
		Workflow: wf,
		Model:    model,
		Registry: reg,
		Policy:   permissivePolicy(),
		Session:  sess,
		Budget:   Budget{MaxToolCalls: 3},
		Logger:   slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	result, err := exec.Run(t.Context(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The mock never stops calling tools, so the one-shot continuation
	// retry also exhausts its small budget: the goal never gets to ok, so
	// the workflow is failed (its only goal isn't ok), not complete.
	if result.Status != StatusFailed {
		t.Errorf("Status = %v, want %v", result.Status, StatusFailed)
	}
	if oc := result.Goals["refine"]; oc.Outcome != OutcomeBudgetExhausted || !oc.Retried {
		t.Errorf("Goals[refine] = %+v, want outcome=budget_exhausted retried=true", oc)
	}

	// The goal must have ended long before using all 25 iterations.
	if n := strings.Count(logBuf.String(), "convergence iteration"); n >= limit {
		t.Errorf("logged %d convergence iterations, want well under the limit of %d — the loop didn't stop on budget exhaustion", n, limit)
	}

	// A budget stop is not a convergence failure.
	if failures := exec.ConvergenceFailures(); failures["refine"] != 0 {
		t.Errorf("ConvergenceFailures()[refine] = %d, want 0 — budget exhaustion isn't a convergence failure", failures["refine"])
	}

	// Exactly one "goal budget exhausted" log line per attempt (the
	// original run and the one continuation retry), despite two agents
	// racing to spend the last of a shared budget across possibly several
	// iterations within each attempt.
	if n := strings.Count(logBuf.String(), "goal budget exhausted"); n != 2 {
		t.Errorf(`log contains %d "goal budget exhausted" lines, want exactly 2 (original + retry):\n%s`, n, logBuf.String())
	}

	// Exactly one session warning event per attempt for the same reason.
	warnings := 0
	for _, evt := range sess.Snapshot() {
		if evt.Type == session.EventWarning && strings.Contains(evt.Content, "exceeded budget") {
			warnings++
		}
	}
	if warnings != 2 {
		t.Errorf("session has %d budget-exceeded warning events, want exactly 2 (original + retry)", warnings)
	}
}

// remainingToolCalls reports the tool-call headroom left in the budget, or
// -1 when there is no cap. executeSimpleParallel uses this to carve a fair
// share of that headroom across parallel agents (3c).
func TestBudget_RemainingToolCalls(t *testing.T) {
	var nilBudget *budget
	if got := nilBudget.remainingToolCalls(); got != -1 {
		t.Errorf("nil budget remainingToolCalls() = %d, want -1", got)
	}

	unlimited := &budget{goal: "g", start: time.Now()}
	if got := unlimited.remainingToolCalls(); got != -1 {
		t.Errorf("unlimited budget remainingToolCalls() = %d, want -1", got)
	}

	b := &budget{goal: "g", limits: Budget{MaxToolCalls: 10}, start: time.Now()}
	if got := b.remainingToolCalls(); got != 10 {
		t.Errorf("fresh budget remainingToolCalls() = %d, want 10", got)
	}
	_ = b.spend(4)
	if got := b.remainingToolCalls(); got != 6 {
		t.Errorf("after spending 4, remainingToolCalls() = %d, want 6", got)
	}
	_ = b.spend(100) // overspend
	if got := b.remainingToolCalls(); got != 0 {
		t.Errorf("after overspending, remainingToolCalls() = %d, want 0 (never negative)", got)
	}
}

// A sub-agent given a localToolCap gets nudged to wrap up once its own tool
// calls would cross that share, and returns whatever content it has instead
// of erroring — even if it ignores the nudge and asks for more tools (3c).
func TestSubAgent_LocalBudgetNudgeReturnsPartialOutput(t *testing.T) {
	loop := fakeTool{name: "search", run: func(context.Context, tools.Args) (string, error) {
		return "more results", nil
	}}
	reg, _ := newTestRegistry(t, t.TempDir(), loop)

	var nudged bool
	turn := 0
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		turn++
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "user" && strings.Contains(last.Content, "allotted share") {
			nudged = true
			return &llm.ChatResponse{Content: "final answer after nudge"}, nil
		}
		// Keeps calling tools forever otherwise.
		return &llm.ChatResponse{
			Content:   "still working",
			ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "search", Args: map[string]any{}}},
		}, nil
	})

	exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, model, reg, permissivePolicy())
	ctx := exec.withBudget(t.Context(), "g") // unlimited goal budget; only the local cap should bind

	out, _, _, _, err := exec.subAgentExecutePhaseWithModel(ctx, model, "r", "sys", "task", 2)
	if err != nil {
		t.Fatalf("subAgentExecutePhaseWithModel: %v", err)
	}
	if !nudged {
		t.Error("expected the agent to receive the fair-share nudge")
	}
	if out != "final answer after nudge" {
		t.Errorf("output = %q, want the agent's post-nudge answer", out)
	}
	_ = turn
}

// A sub-agent that ignores the nudge and keeps asking for tools still ends
// gracefully with partial output (not an error) once it crosses its local
// cap a second time — this is what prevents "everyone fails with empty
// output" (3c / run 38).
func TestSubAgent_LocalBudgetHardStopIsGraceful(t *testing.T) {
	loop := fakeTool{name: "search", run: func(context.Context, tools.Args) (string, error) {
		return "more results", nil
	}}
	reg, _ := newTestRegistry(t, t.TempDir(), loop)

	model := modelFunc(func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
		// Never stops calling tools, even after the nudge.
		return &llm.ChatResponse{
			Content:   "keeps going regardless",
			ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "search", Args: map[string]any{}}},
		}, nil
	})

	exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, model, reg, permissivePolicy())
	ctx := exec.withBudget(t.Context(), "g")

	out, _, _, _, err := exec.subAgentExecutePhaseWithModel(ctx, model, "r", "sys", "task", 2)
	if err != nil {
		t.Fatalf("subAgentExecutePhaseWithModel returned an error instead of degrading gracefully: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("expected non-empty partial output on local-budget hard stop, got empty")
	}
}

// End-to-end: two parallel agents in a GOAL that never stop calling tools
// must not both burn the entire shared goal budget and come back empty —
// the fair-share cap should make at least one of them wrap up with real
// output. Regression for run 38 (two sub-agents hit 41/40 calls, both
// success=false, outputs all "").
func TestBudget_ParallelAgentsDontBothComeBackEmpty(t *testing.T) {
	loop := fakeTool{name: "search", run: func(context.Context, tools.Args) (string, error) {
		return "more results", nil
	}}
	reg, _ := newTestRegistry(t, t.TempDir(), loop)

	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "user" && strings.Contains(last.Content, "allotted share") {
			return &llm.ChatResponse{Content: "wrapped up early"}, nil
		}
		return &llm.ChatResponse{
			Content:   "still going",
			ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "search", Args: map[string]any{}}},
		}, nil
	})

	wf := &agentfile.Workflow{
		Name:  "budget-parallel",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "main", UsingGoals: []string{"work"}}},
		Goals: []agentfile.Goal{{
			Name: "work", Outcome: "do work", UsingAgent: []string{"a", "b"},
		}},
		Agents: []agentfile.Agent{{Name: "a"}, {Name: "b"}},
	}

	sess := &session.Session{}
	exec := mustNew(t, Config{
		Workflow: wf,
		Model:    model,
		Registry: reg,
		Policy:   permissivePolicy(),
		Session:  sess,
		Budget:   Budget{MaxToolCalls: 8},
	})

	result, err := exec.Run(t.Context(), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := result.Outputs["work"]
	if strings.TrimSpace(out) == "" {
		t.Error("expected non-empty combined output from the parallel goal, got empty — both agents starved each other")
	}
}
