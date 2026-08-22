package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vinayprograms/agent/internal/session"
)

// Budget bounds what one goal may consume before the executor stops it. A
// zero field is unlimited, so the zero Budget lets a goal run as long as it
// needs — the historical behaviour.
//
// The budget covers a whole goal: every convergence iteration and every
// sub-agent it spawns draw on the same allowance.
type Budget struct {
	MaxToolCalls int           // tool calls across the goal
	MaxTurns     int           // LLM turns across the goal
	MaxDuration  time.Duration // wall-clock time from the goal's start
}

// budgetError reports that a goal ran out of budget. It ends that goal, not
// the run, so callers recognise it with errors.As.
type budgetError struct {
	goal  string
	limit string // the limit that was hit, e.g. "40 tool calls (max 40)"
}

func (e *budgetError) Error() string {
	return fmt.Sprintf("goal %q exceeded budget: %s", e.goal, e.limit)
}

// budget counts one goal's consumption. Sub-agents of a goal run
// concurrently and share their goal's budget, so it is safe for concurrent
// use. A nil *budget is unlimited.
type budget struct {
	goal   string
	limits Budget
	start  time.Time

	mu     sync.Mutex
	turns  int
	tools  int
	warned bool // one budget-exhausted warning per goal, even with parallel sub-agents
}

// spend records one LLM turn and the tool calls it requested, returning a
// *budgetError once the goal has reached any of its limits.
func (b *budget) spend(toolCalls int) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.turns++
	b.tools += toolCalls
	return b.limitReachedLocked()
}

// exhausted reports whether the goal has already reached any of its limits,
// without spending anything new. Callers use it to avoid starting another
// round of work (a convergence iteration, a fresh batch of sub-agents)
// against a budget that's already spent. A nil budget is unlimited.
func (b *budget) exhausted() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limitReachedLocked()
}

// limitReachedLocked checks the current counters against the configured
// limits. Callers must hold b.mu.
func (b *budget) limitReachedLocked() error {
	switch {
	case b.limits.MaxTurns > 0 && b.turns >= b.limits.MaxTurns:
		return &budgetError{b.goal, fmt.Sprintf("%d turns (max %d)", b.turns, b.limits.MaxTurns)}
	case b.limits.MaxToolCalls > 0 && b.tools >= b.limits.MaxToolCalls:
		return &budgetError{b.goal, fmt.Sprintf("%d tool calls (max %d)", b.tools, b.limits.MaxToolCalls)}
	case b.limits.MaxDuration > 0 && time.Since(b.start) >= b.limits.MaxDuration:
		return &budgetError{b.goal, fmt.Sprintf("ran for %s (max %s)", time.Since(b.start).Round(time.Second), b.limits.MaxDuration)}
	}
	return nil
}

type budgetKey struct{}

// withBudget starts a fresh budget for goal and puts it in ctx, so every
// sub-agent and convergence iteration of that goal draws on the same one.
func (e *Executor) withBudget(ctx context.Context, goal string) context.Context {
	return context.WithValue(ctx, budgetKey{}, &budget{goal: goal, limits: e.budget, start: time.Now()})
}

// budgetOf returns the budget of the goal being executed, or nil (unlimited).
func budgetOf(ctx context.Context) *budget {
	b, _ := ctx.Value(budgetKey{}).(*budget)
	return b
}

// noteBudget reports whether err is a spent budget, recording it as a warning
// and a session event. A spent budget ends its goal with whatever it produced
// so far; the run continues. Parallel sub-agents share one goal's budget and
// can all hit the limit at once, so only the first to notice logs it.
func (e *Executor) noteBudget(ctx context.Context, err error) bool {
	var spent *budgetError
	if !errors.As(err, &spent) {
		return false
	}
	if b := budgetOf(ctx); b != nil {
		b.mu.Lock()
		alreadyWarned := b.warned
		b.warned = true
		b.mu.Unlock()
		if alreadyWarned {
			return true
		}
	}
	e.logger.Warn("goal budget exhausted", "goal", spent.goal, "limit", spent.limit)
	e.logEvent(session.EventWarning, spent.Error())
	return true
}
