package executor

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/tools"
)

// slowExtractor takes a measurable amount of time, so a Run that does not
// wait for the extraction goroutine is observable.
type slowExtractor struct{ delay time.Duration }

func (s slowExtractor) Extract(ctx context.Context, _ string, _ ...memory.ExtractOption) ([]string, []string, []string, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	}
	return []string{"f"}, nil, nil, nil
}

// countingStore records how many observation batches were stored.
type countingStore struct{ n atomic.Int32 }

func (c *countingStore) RememberFIL(context.Context, []string, []string, []string, string) ([]string, error) {
	c.n.Add(1)
	return []string{"id"}, nil
}

func oneGoalWorkflow() *agentfile.Workflow {
	return &agentfile.Workflow{
		Name:  "bg",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"work"}}},
		Goals: []agentfile.Goal{{Name: "work", Outcome: "do the work"}},
	}
}

func TestRun_WaitsForObservationExtraction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &countingStore{}
		exec := mustNew(t, Config{
			Workflow:             oneGoalWorkflow(),
			Model:                llmmock.New(),
			ObservationExtractor: slowExtractor{delay: time.Minute},
			ObservationStore:     store,
		})

		if _, err := exec.Run(t.Context(), RunOptions{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := store.n.Load(); got != 1 {
			t.Errorf("observations stored after Run returned = %d, want 1", got)
		}
	})
}

// A cancelled Run still finishes the observation write it started: the work
// is detached from the caller's context, and Run owns it to completion.
func TestRun_ObservationSurvivesCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &countingStore{}
		exec := mustNew(t, Config{
			Workflow:             oneGoalWorkflow(),
			Model:                llmmock.New(),
			ObservationExtractor: slowExtractor{delay: time.Minute},
			ObservationStore:     store,
		})

		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			synctest.Wait()
			cancel()
		}()
		if _, err := exec.Run(ctx, RunOptions{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := store.n.Load(); got != 1 {
			t.Errorf("observations stored = %d, want 1", got)
		}
	})
}

// Async (fire-and-forget) tools must also finish before Run returns —
// otherwise they log to a session the executor has already closed.
func TestRun_WaitsForAsyncTools(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var remembered atomic.Int32
		remember := fakeTool{name: "remember", run: func(context.Context, tools.Args) (string, error) {
			time.Sleep(time.Minute)
			remembered.Add(1)
			return "stored", nil
		}}
		reg, _ := newTestRegistry(t, t.TempDir(), remember)

		var turn atomic.Int32
		model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
			if turn.Add(1) == 1 {
				return &llm.ChatResponse{ToolCalls: []llm.ToolCallResponse{
					{ID: "1", Name: "remember", Args: map[string]any{}},
					{ID: "2", Name: "pwd", Args: map[string]any{}},
				}}, nil
			}
			return &llm.ChatResponse{Content: "done"}, nil
		})

		sess := &session.Session{}
		exec := mustNew(t, Config{
			Workflow: oneGoalWorkflow(),
			Model:    model,
			Registry: reg,
			Policy:   permissivePolicy(),
			Session:  sess,
		})

		if _, err := exec.Run(t.Context(), RunOptions{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := remembered.Load(); got != 1 {
			t.Errorf("async tool completions after Run returned = %d, want 1", got)
		}
	})
}

// hangingExtractor blocks until its context ends — a stuck LLM call.
type hangingExtractor struct{ waited chan time.Duration }

func (h hangingExtractor) Extract(ctx context.Context, _ string, _ ...memory.ExtractOption) ([]string, []string, []string, error) {
	start := time.Now()
	<-ctx.Done()
	h.waited <- time.Since(start)
	return nil, nil, nil, ctx.Err()
}

// Detaching background work from cancellation must not make it unbounded:
// Run waits for it, so it needs a deadline of its own.
func TestRun_BackgroundWorkHasDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		waited := make(chan time.Duration, 1)
		exec := mustNew(t, Config{
			Workflow:             oneGoalWorkflow(),
			Model:                llmmock.New(),
			ObservationExtractor: hangingExtractor{waited: waited},
			ObservationStore:     &countingStore{},
		})

		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			synctest.Wait()
			cancel()
		}()
		if _, err := exec.Run(ctx, RunOptions{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := <-waited; got != backgroundTimeout {
			t.Errorf("background work ran for %v, want the %v deadline", got, backgroundTimeout)
		}
	})
}
