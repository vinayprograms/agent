package executor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/supervision"
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
//
// Supervision flow:
// - COMMIT: Once at start (declares intent to converge)
// - EXECUTE: Multiple iterations (the convergence loop)
// - RECONCILE: Once at end (checks final output)
// - SUPERVISE: Once at end (if reconcile triggered or SUPERVISED HUMAN)
func (e *Executor) executeConvergeGoal(ctx context.Context, goal *agentfile.Goal) (*ConvergenceResult, error) {
	// Get max iterations from literal or variable
	maxIterations := e.getConvergeLimit(goal)
	if maxIterations <= 0 {
		return nil, fmt.Errorf("CONVERGE goal %q: WITHIN limit must be > 0", goal.Name)
	}

	e.logger.Info("starting convergence goal", map[string]any{
		"goal": goal.Name,
	})

	// Set current goal for logging
	e.currentGoal = goal.Name

	// Determine supervision status
	supervised := e.isSupervised(goal)
	humanRequired := e.requiresHuman(goal)
	e.currentGoalSupervised = supervised

	// Build initial prompt for COMMIT phase
	initialPrompt := e.buildConvergePrompt(goal, nil, 1)

	// State captured by the execute closure and used by the post-checkpoint closure
	var iterations []ConvergenceIteration
	var converged bool
	var iterationCount int

	// Run through the supervision pipeline
	pipelineResult, err := e.getPipeline().Run(
		ctx,
		supervision.PipelineRequest{
			StepID:        goal.Name,
			GoalName:      e.goalOutcome(goal.Name),
			Supervised:    supervised,
			HumanRequired: humanRequired,
		},
		// COMMIT: declare intent to converge
		func(ctx context.Context) *checkpoint.PreCheckpoint {
			return e.commitPhase(ctx, goal, initialPrompt)
		},
		// EXECUTE: convergence loop
		func(ctx context.Context) (*supervision.ExecuteResult, error) {
			var lastOutput string
			for i := 1; i <= maxIterations; i++ {
				e.logger.Debug("convergence iteration", map[string]any{
					"goal":      goal.Name,
					"iteration": i,
				})

				e.logEvent(session.EventSystem, fmt.Sprintf("Convergence iteration %d for goal %q", i, goal.Name))

				prompt := e.buildConvergePrompt(goal, iterations, i)

				output, iterErr := e.executeConvergeIteration(ctx, goal, prompt)
				if iterErr != nil {
					return nil, fmt.Errorf("convergence iteration %d failed: %w", i, iterErr)
				}

				trimmed := strings.TrimSpace(output)
				if trimmed == "CONVERGED" {
					e.logger.Info("convergence achieved", map[string]any{
						"goal":       goal.Name,
						"iterations": i,
					})
					e.logEvent(session.EventSystem, fmt.Sprintf("Goal %q converged after %d iterations", goal.Name, i))
					converged = true
					iterationCount = i
					break
				}

				iterations = append(iterations, ConvergenceIteration{N: i, Output: output})
				lastOutput = output
				iterationCount = i
			}

			if !converged {
				e.logger.Warn("convergence limit reached without converging", map[string]any{
					"goal":  goal.Name,
					"limit": maxIterations,
				})
				e.logEvent(session.EventWarning, fmt.Sprintf("Goal %q did not converge within limit (used all iterations)", goal.Name))
				e.trackConvergenceFailure(goal.Name, maxIterations)
			}

			// For convergence, we note iteration count instead of individual tools
			toolsUsed := []string{fmt.Sprintf("converge:%d_iterations", iterationCount)}
			if !converged {
				toolsUsed = append(toolsUsed, "converge:limit_reached")
			}

			return &supervision.ExecuteResult{Output: lastOutput, ToolsUsed: toolsUsed}, nil
		},
		// POST-CHECKPOINT: self-assessment on final output
		func(ctx context.Context, pre *checkpoint.PreCheckpoint, output string, toolsUsed []string) *checkpoint.PostCheckpoint {
			return e.createPostCheckpoint(ctx, goal, pre, output, toolsUsed)
		},
	)
	if err != nil {
		return nil, err
	}

	finalOutput := pipelineResult.Output

	// Handle supervision verdict
	switch pipelineResult.Verdict {
	case supervision.VerdictReorient:
		e.logger.Info("supervisor requested reorientation", map[string]any{
			"goal":       goal.Name,
			"correction": pipelineResult.Correction,
		})
		correctionPrompt := e.buildConvergePromptWithCorrection(goal, iterations, iterationCount+1, pipelineResult.Correction)
		correctedOutput, corrErr := e.executeConvergeIteration(ctx, goal, correctionPrompt)
		if corrErr != nil {
			return nil, fmt.Errorf("correction iteration failed: %w", corrErr)
		}
		finalOutput = correctedOutput

	case supervision.VerdictPause:
		return nil, fmt.Errorf("supervision paused: %s", pipelineResult.Question)
	}

	return &ConvergenceResult{
		Converged:  converged,
		Iterations: iterationCount,
		Output:     finalOutput,
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

// buildConvergePromptWithCorrection builds prompt with supervisor correction.
func (e *Executor) buildConvergePromptWithCorrection(goal *agentfile.Goal, iterations []ConvergenceIteration, currentIteration int, correction string) string {
	xmlBuilder := NewXMLContextBuilder(e.workflow.Name)
	xmlBuilder.SetConvergenceMode()

	for goalName, output := range e.outputs {
		xmlBuilder.AddPriorGoal(goalName, output)
	}

	for _, iter := range iterations {
		xmlBuilder.AddConvergenceIteration(iter.N, iter.Output)
	}

	goalDescription := e.interpolate(goal.Outcome)
	if len(goal.Outputs) > 0 {
		goalDescription += "\n\n" + buildStructuredOutputInstruction(goal.Outputs)
	}

	xmlBuilder.SetCurrentGoal(goal.Name, goalDescription)
	xmlBuilder.SetCorrection(correction)

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
	// Store convergence context so executeSimpleParallel can use it
	e.convergenceContext = prompt
	defer func() { e.convergenceContext = "" }()
	
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
