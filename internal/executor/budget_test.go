package executor

import (
	"context"
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
	if result.Status != StatusComplete {
		t.Errorf("Status = %v, want %v", result.Status, StatusComplete)
	}
	if got := calls.Load(); got > 4 {
		t.Errorf("tool calls = %d, want at most the budget of 4", got)
	}
	if got := result.Outputs["report"]; got != "wrapped up" {
		t.Errorf("Outputs[report] = %q, want %q — later goals must still run", got, "wrapped up")
	}

	var warned string
	for _, evt := range sess.Events {
		if evt.Type == session.EventWarning && strings.Contains(evt.Content, "exceeded budget") {
			warned = evt.Content
		}
	}
	if !strings.Contains(warned, `goal "research" exceeded budget`) {
		t.Errorf("session warning = %q, want one naming the research goal's budget", warned)
	}
}
