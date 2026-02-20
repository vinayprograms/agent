// Package executor provides convergence goal execution.
package executor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
)

// ConvergenceResult tracks the outcome of a convergence goal.
type ConvergenceResult struct {
	Converged  bool   // true if the goal converged before hitting the limit
	Iterations int    // number of iterations executed
	Output     string // final output (last substantive iteration)
}

// executeConvergeGoal executes a CONVERGE goal with iterative refinement.
// It runs the goal repeatedly, feeding previous outputs back as context,
// until the LLM outputs "CONVERGED" or the WITHIN limit is reached.
func (e *Executor) executeConvergeGoal(ctx context.Context, goal *agentfile.Goal) (*ConvergenceResult, error) {
	// Get max iterations from literal or variable
	maxIterations := e.getConvergeLimit(goal)
	if maxIterations <= 0 {
		return nil, fmt.Errorf("CONVERGE goal %q: WITHIN limit must be > 0", goal.Name)
	}

	e.logger.Info("starting convergence goal", map[string]interface{}{
		"goal": goal.Name,
		// Note: we intentionally do NOT log maxIterations to keep it hidden from LLM logs
	})

	var iterations []ConvergenceIteration
	var lastOutput string

	for i := 1; i <= maxIterations; i++ {
		e.logger.Debug("convergence iteration", map[string]interface{}{
			"goal":      goal.Name,
			"iteration": i,
		})

		// Log iteration start
		e.logEvent(session.EventSystem, fmt.Sprintf("Convergence iteration %d for goal %q", i, goal.Name))

		// Build prompt with convergence context
		prompt := e.buildConvergePrompt(goal, iterations, i)

		// Execute one iteration
		output, err := e.executeConvergeIteration(ctx, goal, prompt)
		if err != nil {
			return nil, fmt.Errorf("convergence iteration %d failed: %w", i, err)
		}

		// Check for convergence signal
		trimmed := strings.TrimSpace(output)
		if trimmed == "CONVERGED" {
			e.logger.Info("convergence achieved", map[string]interface{}{
				"goal":       goal.Name,
				"iterations": i,
			})
			e.logEvent(session.EventSystem, fmt.Sprintf("Goal %q converged after %d iterations", goal.Name, i))

			return &ConvergenceResult{
				Converged:  true,
				Iterations: i,
				Output:     lastOutput,
			}, nil
		}

		// Store this iteration
		iterations = append(iterations, ConvergenceIteration{N: i, Output: output})
		lastOutput = output
	}

	// Hit limit without converging
	e.logger.Warn("convergence limit reached without converging", map[string]interface{}{
		"goal":  goal.Name,
		"limit": maxIterations,
	})
	e.logEvent(session.EventWarning, fmt.Sprintf("Goal %q did not converge within limit (used all iterations)", goal.Name))

	// Track for replay warning coloring
	e.trackConvergenceFailure(goal.Name, maxIterations)

	return &ConvergenceResult{
		Converged:  false,
		Iterations: maxIterations,
		Output:     lastOutput,
	}, nil
}

// getConvergeLimit returns the max iterations for a CONVERGE goal.
func (e *Executor) getConvergeLimit(goal *agentfile.Goal) int {
	if goal.WithinLimit != nil {
		return *goal.WithinLimit
	}
	if goal.WithinVar != "" {
		// Look up variable value
		if val, ok := e.outputs[goal.WithinVar]; ok {
			if n, err := strconv.Atoi(val); err == nil {
				return n
			}
		}
		// Also check inputs
		if val, ok := e.inputs[goal.WithinVar]; ok {
			if n, err := strconv.Atoi(val); err == nil {
				return n
			}
		}
	}
	return 0
}

// buildConvergePrompt builds the XML prompt for a convergence iteration.
func (e *Executor) buildConvergePrompt(goal *agentfile.Goal, iterations []ConvergenceIteration, currentIteration int) string {
	xmlBuilder := NewXMLContextBuilder(e.workflow.Name)
	xmlBuilder.SetConvergenceMode()

	// Add prior goal outputs to context
	for goalName, output := range e.outputs {
		xmlBuilder.AddPriorGoal(goalName, output)
	}

	// Add previous convergence iterations
	for _, iter := range iterations {
		xmlBuilder.AddConvergenceIteration(iter.N, iter.Output)
	}

	// Set current goal with interpolated description
	goalDescription := e.interpolate(goal.Outcome)

	// Add structured output instruction if outputs are declared
	if len(goal.Outputs) > 0 {
		goalDescription += "\n\n" + buildStructuredOutputInstruction(goal.Outputs)
	}

	xmlBuilder.SetCurrentGoal(goal.Name, goalDescription)

	return xmlBuilder.Build()
}

// executeConvergeIteration executes a single iteration of a convergence goal.
// This handles both single-agent and multi-agent execution.
func (e *Executor) executeConvergeIteration(ctx context.Context, goal *agentfile.Goal, prompt string) (string, error) {
	// Check for multi-agent execution
	if len(goal.UsingAgent) > 0 {
		// For multi-agent convergence, we need to run the multi-agent flow
		// but with the convergence-aware prompt
		return e.executeConvergeMultiAgent(ctx, goal, prompt)
	}

	// Single-agent execution
	e.currentGoal = goal.Name

	// Use executePhase which handles tools, thinking, etc.
	output, _, _, err := e.executePhase(ctx, goal, prompt)
	if err != nil {
		return "", err
	}

	return output, nil
}

// executeConvergeMultiAgent handles multi-agent execution within a convergence loop.
func (e *Executor) executeConvergeMultiAgent(ctx context.Context, goal *agentfile.Goal, prompt string) (string, error) {
	// For now, delegate to the standard multi-agent execution
	// The convergence context is already in the prompt
	return e.executeMultiAgentGoal(ctx, goal)
}

// trackConvergenceFailure records a convergence failure for replay warning.
func (e *Executor) trackConvergenceFailure(goalName string, iterations int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.convergenceFailures == nil {
		e.convergenceFailures = make(map[string]int)
	}
	e.convergenceFailures[goalName] = iterations
}

// GetConvergenceFailures returns goals that failed to converge.
func (e *Executor) GetConvergenceFailures() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.convergenceFailures == nil {
		return nil
	}
	// Return a copy
	result := make(map[string]int)
	for k, v := range e.convergenceFailures {
		result[k] = v
	}
	return result
}
