package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
)

// Outcome classifies how a goal ended. Every goal result carries one, so a
// caller never has to infer success from an empty string or a missing
// field.
type Outcome string

const (
	// OutcomeOK is a goal that produced its declared outputs (or, when it
	// declares none, non-empty content or at least one tool call) without
	// running out of budget or iterations.
	OutcomeOK Outcome = "ok"
	// OutcomeNotConverged is a CONVERGE goal that used its whole WITHIN
	// limit without the model emitting the convergence marker.
	OutcomeNotConverged Outcome = "not_converged"
	// OutcomeBudgetExhausted is a goal stopped by a tool-call, turn, or
	// duration budget before it finished.
	OutcomeBudgetExhausted Outcome = "budget_exhausted"
	// OutcomeEmptyOutput is a goal that finished — no budget or iteration
	// limit stopped it — but produced nothing: every declared `-> var` is
	// empty, or (when the goal declares none) its content is empty and it
	// made no tool calls.
	OutcomeEmptyOutput Outcome = "empty_output"
	// OutcomeError is a goal that failed outright (an LLM or tool error
	// with no usable partial result).
	OutcomeError Outcome = "error"
)

// Hard reports whether outcome should never be retried. The soft outcomes
// (budget_exhausted, not_converged) get exactly one continuation retry; hard
// outcomes (error, empty_output) do not, since more budget would not have
// helped: the model already had a normal turn and produced nothing, or
// broke outright.
func (o Outcome) Hard() bool {
	return o == OutcomeError || o == OutcomeEmptyOutput
}

// GoalOutcome is the full record of how one goal ended, carried in the
// workflow Result and logged on the goal_end session event.
type GoalOutcome struct {
	Outcome    Outcome `json:"outcome"`
	Reason     string  `json:"reason,omitempty"`
	Iterations int     `json:"iterations,omitempty"` // populated for CONVERGE goals
	Retried    bool    `json:"retried,omitempty"`    // set once the soft-failure retry ran
}

// continuationNudge is appended to a goal's outcome text for its single
// retry after a soft failure, asking the model to wrap up with whatever it
// already has instead of continuing to explore.
const continuationNudge = "\n\nNOTE: You ran out of budget before finishing this goal. Stop exploring and, using only what you already have, finish now and produce the declared outputs immediately."

// retryBudget is the small fresh allowance given to a goal's one
// continuation retry after a soft failure.
var retryBudget = Budget{MaxToolCalls: 5, MaxTurns: 3}

// asBudgetError extracts a *budgetError from err, if it is one.
func asBudgetError(err error) *budgetError {
	var spent *budgetError
	if errors.As(err, &spent) {
		return spent
	}
	return nil
}

// declaredOutputsEmpty reports whether every one of a goal's declared
// `-> var` outputs is empty. Called only when the goal declares at least
// one.
func declaredOutputsEmpty(declared []string, vars map[string]string) bool {
	for _, name := range declared {
		if strings.TrimSpace(vars[name]) != "" {
			return false
		}
	}
	return true
}

// classifyOutcome is the single function that turns a goal's raw execution
// result into an explicit Outcome. spent is the budget error the goal
// stopped on, if any; notConverged marks a CONVERGE goal that used its
// whole WITHIN limit without converging.
func classifyOutcome(output string, toolCallsMade bool, declared []string, vars map[string]string, spent *budgetError, notConverged bool, convergeLimit int) GoalOutcome {
	switch {
	case spent != nil:
		return GoalOutcome{Outcome: OutcomeBudgetExhausted, Reason: spent.Error()}
	case notConverged:
		return GoalOutcome{
			Outcome:    OutcomeNotConverged,
			Reason:     fmt.Sprintf("did not converge within %d iteration(s)", convergeLimit),
			Iterations: convergeLimit,
		}
	case len(declared) > 0 && declaredOutputsEmpty(declared, vars):
		return GoalOutcome{Outcome: OutcomeEmptyOutput, Reason: "goal finished but every declared output is empty"}
	case len(declared) == 0 && strings.TrimSpace(output) == "" && !toolCallsMade:
		return GoalOutcome{Outcome: OutcomeEmptyOutput, Reason: "goal finished with empty content and no tool calls"}
	default:
		return GoalOutcome{Outcome: OutcomeOK}
	}
}

// nudgedGoal returns a shallow copy of goal whose Outcome text carries the
// continuation nudge, for a single retry attempt after a soft failure.
func nudgedGoal(goal *agentfile.Goal) *agentfile.Goal {
	g := *goal
	g.Outcome = goal.Outcome + continuationNudge
	return &g
}

// retryContext returns ctx with a fresh, small budget for goal's one
// continuation retry, independent of the exhausted budget the goal ran on.
func (e *Executor) retryContext(ctx context.Context, goalName string) context.Context {
	return context.WithValue(ctx, budgetKey{}, &budget{goal: goalName, limits: retryBudget, start: time.Now()})
}

// maybeRetry re-runs fn once, against a small fresh budget and a
// continuation nudge appended to the goal's outcome text, when outcome is
// soft (budget_exhausted or not_converged). Hard outcomes (error,
// empty_output) and outcome==ok pass through untouched, and fn is never
// called. This is the single place the soft-failure retry policy lives;
// every goal-execution path (plain, multi-agent, converge) calls it with a
// closure that re-runs just that path's single-shot execution and returns
// its own output/outcome.
func (e *Executor) maybeRetry(ctx context.Context, goal *agentfile.Goal, outcome GoalOutcome, fn func(retryCtx context.Context, goal *agentfile.Goal) GoalOutcome) GoalOutcome {
	if outcome.Outcome != OutcomeBudgetExhausted && outcome.Outcome != OutcomeNotConverged {
		return outcome
	}
	e.logger.Info("retrying goal after soft failure", "goal", goal.Name, "outcome", string(outcome.Outcome), "reason", outcome.Reason)
	e.logEvent(session.EventSystem, fmt.Sprintf("Goal %q %s; retrying once with a continuation nudge", goal.Name, outcome.Outcome))
	retryCtx := e.retryContext(ctx, goal.Name)
	next := fn(retryCtx, nudgedGoal(goal))
	next.Retried = true
	return next
}
