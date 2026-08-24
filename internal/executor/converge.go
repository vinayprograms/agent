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
	"github.com/vinayprograms/agentkit/llm"
)

// ConvergenceResult tracks the outcome of a convergence goal.
type ConvergenceResult struct {
	Converged  bool   // true if the goal converged before hitting the limit
	Iterations int    // number of iterations executed
	Output     string // final output (last substantive iteration)
	// Reason is the convergence reason reported by the converged tool, when
	// Converged and ViaTool are true. Empty when convergence came via the
	// lenient prose fallback (no reason to extract) or didn't happen.
	Reason string
	// ViaTool reports whether convergence was decided via the converged
	// tool call, as opposed to the lenient splitConvergence prose fallback.
	ViaTool bool
	// Vars holds the goal's declared `-> outputs` fields when they arrived
	// via the converged tool call's bundled fields; empty otherwise (the
	// caller falls back to parseStructuredOutput on Output).
	Vars map[string]string
	// BudgetErr is set when the goal stopped because its budget (tool
	// calls, turns, or duration) ran out mid-iteration, rather than by
	// exhausting its WITHIN limit without converging.
	BudgetErr error
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

	e.logger.Info("starting convergence goal", "goal", goal.Name)

	// Set current goal for logging
	e.currentGoal = goal.Name

	// Determine supervision status
	supervised := e.isSupervised(goal)
	humanRequired := e.requiresHuman(goal)
	e.currentGoalSupervised = supervised

	// Build initial prompt for COMMIT phase
	initialPrompt := e.buildConvergePrompt(goal, nil, "")

	// State captured by the execute closure and used by the post-checkpoint closure
	var iterations []ConvergenceIteration
	var converged bool
	var iterationCount int
	var budgetStopped bool
	var budgetErr error
	var convergedReason string
	var convergedViaTool bool
	var finalVars map[string]string

	// Run through the supervision pipeline
	pipelineResult, err := e.pipeline.Run(
		ctx,
		supervision.PipelineRequest{
			StepID:        goal.Name,
			GoalName:      goal.Name,
			Outcome:       e.goalOutcome(goal.Name),
			Supervised:    supervised,
			HumanRequired: humanRequired,
		},
		supervision.Work{
			// COMMIT: declare intent to converge
			Commit: func(ctx context.Context) *checkpoint.PreCheckpoint {
				return e.commitPhase(ctx, goal, initialPrompt)
			},
			// EXECUTE: convergence loop
			Execute: func(ctx context.Context) (*supervision.ExecuteResult, error) {
				var lastOutput string
				for i := 1; i <= maxIterations; i++ {
					// The budget may already be spent from the previous
					// iteration (e.g. MaxDuration ticking over between
					// iterations, or a multi-agent iteration that hit its
					// limit). Don't start another round against it.
					if exhErr := budgetOf(ctx).exhausted(); e.noteBudget(ctx, exhErr) {
						budgetStopped = true
						budgetErr = exhErr
						break
					}

					e.logger.Debug("convergence iteration", "goal", goal.Name, "iteration", i)

					e.logEvent(session.EventSystem, fmt.Sprintf("Convergence iteration %d for goal %q", i, goal.Name))

					prompt := e.buildConvergePrompt(goal, iterations, "")

					iter, iterErr := e.executeConvergeIteration(ctx, goal, prompt)
					content, done := iter.Content, iter.Done
					if e.noteBudget(ctx, iterErr) {
						// Out of budget: keep this iteration's partial output
						// and stop refining.
						iterationCount = i
						budgetStopped = true
						budgetErr = iterErr
						if trimmed := strings.TrimSpace(content); trimmed != "" {
							lastOutput = trimmed
						}
						break
					}
					if iterErr != nil {
						return nil, fmt.Errorf("convergence iteration %d failed: %w", i, iterErr)
					}

					iterationCount = i
					// A marker-only response carries no new content, so the
					// previous iteration's output stays final.
					if content != "" {
						iterations = append(iterations, ConvergenceIteration{N: i, Output: content})
						lastOutput = content
					}
					if done {
						convergedReason = iter.Reason
						convergedViaTool = iter.ViaTool
						finalVars = iter.Vars
						source := "prose fallback"
						if iter.ViaTool {
							source = "converged tool"
						}
						e.logger.Info("convergence achieved", "goal", goal.Name, "iterations", i, "via", source, "reason", iter.Reason)
						e.logEvent(session.EventSystem, fmt.Sprintf("Goal %q converged after %d iterations (via %s): %s", goal.Name, i, source, iter.Reason))
						converged = true
						break
					}
				}

				switch {
				case converged:
					// no-op: already logged above
				case budgetStopped:
					// noteBudget already logged the reason; this is not the
					// same as exhausting the WITHIN limit, so don't record
					// it as a convergence failure.
					e.logger.Warn("goal stopped early: budget exhausted before converging", "goal", goal.Name)
				default:
					e.logger.Warn("convergence limit reached without converging", "goal", goal.Name, "limit", maxIterations)
					e.logEvent(session.EventWarning, fmt.Sprintf("Goal %q did not converge within limit (used all iterations)", goal.Name))
					e.trackConvergenceFailure(goal.Name, maxIterations)
				}

				// For convergence, we note iteration count instead of individual tools
				toolsUsed := []string{fmt.Sprintf("converge:%d_iterations", iterationCount)}
				switch {
				case budgetStopped:
					toolsUsed = append(toolsUsed, "converge:budget_exhausted")
				case !converged:
					toolsUsed = append(toolsUsed, "converge:limit_reached")
				}

				return &supervision.ExecuteResult{Output: lastOutput, ToolsUsed: toolsUsed}, nil
			},
			// POST-CHECKPOINT: self-assessment on final output
			Post: func(ctx context.Context, pre *checkpoint.PreCheckpoint, output string, toolsUsed []string) *checkpoint.PostCheckpoint {
				return e.createPostCheckpoint(ctx, goal, pre, output, toolsUsed)
			},
		},
	)
	if err != nil {
		return nil, err
	}

	finalOutput := pipelineResult.Output

	// Handle supervision verdict
	switch pipelineResult.Verdict {
	case supervision.VerdictReorient:
		e.logger.Info("supervisor requested reorientation", "goal", goal.Name, "correction", pipelineResult.Correction)
		correctionPrompt := e.buildConvergePrompt(goal, iterations, pipelineResult.Correction)
		corrIter, corrErr := e.executeConvergeIteration(ctx, goal, correctionPrompt)
		if corrErr != nil {
			return nil, fmt.Errorf("correction iteration failed: %w", corrErr)
		}
		finalOutput = corrIter.Content
		if corrIter.Done {
			convergedReason = corrIter.Reason
			convergedViaTool = corrIter.ViaTool
			finalVars = corrIter.Vars
		}

	case supervision.VerdictPause:
		return nil, fmt.Errorf("supervision paused: %s", pipelineResult.Question)
	}

	return &ConvergenceResult{
		Converged:  converged,
		Iterations: iterationCount,
		Output:     finalOutput,
		Reason:     convergedReason,
		ViaTool:    convergedViaTool,
		Vars:       finalVars,
		BudgetErr:  budgetErr,
	}, nil
}

// convergenceMarker terminates a convergence loop when the model puts it on a
// line of its own; anything before it is that iteration's final content.
//
// This is the LENIENT prose fallback, live for any provider that ignores
// the offered convergedTool (notably Ollama Cloud, which cannot force
// tool_choice — see converged tool in decisions.go, the primary channel).
// splitConvergence matches when the LAST NON-EMPTY line, after trimming
// whitespace, markdown fences, and trailing punctuation, equals CONVERGED
// (case-insensitive) — deliberately lenient, since a strict exact-line match
// caused false negatives that each cost a full iteration.
const convergenceMarker = "CONVERGED"

// splitConvergence separates a convergence iteration's substantive content
// from a trailing convergenceMarker, reporting whether the loop should end.
// See convergenceMarker for the matching rule.
func splitConvergence(output string) (content string, converged bool) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return trimmed, false
	}
	lines := strings.Split(trimmed, "\n")
	lastIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastIdx = i
			break
		}
	}
	if lastIdx == -1 {
		return trimmed, false
	}
	if normalizeConvergenceMarker(lines[lastIdx]) != convergenceMarker {
		return trimmed, false
	}
	return strings.TrimSpace(strings.Join(lines[:lastIdx], "\n")), true
}

// normalizeConvergenceMarker trims whitespace, common markdown fence/quote/
// heading markers, and trailing punctuation from a candidate marker line,
// then upper-cases it for a case-insensitive compare.
func normalizeConvergenceMarker(line string) string {
	s := strings.TrimSpace(line)
	s = strings.Trim(s, "`*_#> \t")
	s = strings.TrimRight(s, ".!:; \t")
	return strings.ToUpper(strings.TrimSpace(s))
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
// A non-empty correction is a supervisor reorientation for the next one.
func (e *Executor) buildConvergePrompt(goal *agentfile.Goal, iterations []ConvergenceIteration, correction string) string {
	b := newBrief(e.workflow.Name)
	b.SetConvergenceMode()

	// Prior goal outputs, then the iterations so far.
	for goalName, output := range e.outputs {
		b.AddPriorGoal(goalName, output)
	}
	for _, iter := range iterations {
		b.AddConvergenceIteration(iter.N, iter.Output)
	}

	goalDescription := e.interpolate(goal.Outcome)
	if len(goal.Outputs) > 0 {
		goalDescription += "\n\n" + buildStructuredOutputInstruction(goal.Outputs)
	}
	b.SetCurrentGoal(goal.Name, goalDescription)

	if correction != "" {
		b.SetCorrection(correction)
	}
	return b.String()
}

// convergeIterationResult is one iteration's outcome, from either the
// single-agent path or the last stage of a CONVERGE pipeline.
type convergeIterationResult struct {
	Content string            // substantive content, marker/tool-call stripped
	Done    bool              // true if this iteration converged
	Reason  string            // convergence reason, when Done and reported via tool
	Vars    map[string]string // declared `-> outputs` fields, when reported via tool
	ViaTool bool              // true if the convergence decision came from the converged tool (vs. the lenient prose fallback)
}

// executeConvergeIteration executes a single iteration of a convergence
// goal. For a single agent, convergence is decided by that agent calling
// the converged tool (or, as a lenient fallback for providers that can't
// force tool_choice, the trailing CONVERGED marker). For USING agents,
// convergence is now a sequential pipeline (executeConvergePipeline): each
// agent sees the previous agent's output, and only the LAST agent's
// decision matters.
func (e *Executor) executeConvergeIteration(ctx context.Context, goal *agentfile.Goal, prompt string) (convergeIterationResult, error) {
	if len(goal.UsingAgent) > 0 {
		return e.executeConvergePipeline(ctx, goal, prompt)
	}

	// Single-agent execution.
	e.currentGoal = goal.Name

	extraTools := []llm.ToolDef{convergedToolFor(goal.Outputs)}
	// The single agent is the one deciding convergence, so it gets the
	// tool-call instruction (a provider that can't force tool_choice, e.g.
	// Ollama Cloud, still needs the prompt-level nudge).
	agentPrompt := prompt + convergedInstruction
	if len(goal.Outputs) > 0 {
		agentPrompt += fmt.Sprintf(" Include your declared output fields (%s) in that same call.", strings.Join(goal.Outputs, ", "))
	}
	// Use executePhase which handles tools, thinking, etc. The output is
	// returned even on error: a budget stop keeps its partial result.
	output, _, _, decision, err := e.executePhase(ctx, goal, agentPrompt, extraTools...)
	if err != nil {
		return convergeIterationResult{Content: output}, err
	}
	return decideConvergence(output, decision, goal.Outputs), nil
}

// decideConvergence turns a single agent's turn (its final content, plus
// any decision-tool call it made) into a convergeIterationResult: it prefers
// the structured converged tool call, falling back to the lenient
// splitConvergence prose marker when the model didn't call it — the live
// path for a provider (e.g. Ollama Cloud) that can't force tool_choice.
func decideConvergence(output string, decision *llm.ToolCallResponse, outputFields []string) convergeIterationResult {
	if decision != nil && decision.Name == convergedToolName {
		reason, _ := decision.Args["reason"].(string)
		result := convergeIterationResult{
			Content: strings.TrimSpace(output),
			Done:    true,
			Reason:  reason,
			ViaTool: true,
		}
		if len(outputFields) > 0 {
			result.Vars = decisionArgsToVars(decision.Args, outputFields)
		}
		return result
	}
	content, done := splitConvergence(output)
	return convergeIterationResult{Content: content, Done: done}
}

// executeConvergePipeline runs a CONVERGE goal's USING agents SEQUENTIALLY,
// each seeing the previous agent's output within the same iteration — this
// replaces the old parallel fan-out (still used by GOAL, unchanged) for
// CONVERGE goals specifically. Because the pipeline is sequential, the last
// agent's output IS the goal's output: there is no synthesis call to merge
// (synthesis existed only to merge independent parallel outputs). The LAST
// agent alone decides convergence, via the converged tool (or the lenient
// prose fallback) — this replaces the old unanimity rule where every agent
// had to emit the marker.
func (e *Executor) executeConvergePipeline(ctx context.Context, goal *agentfile.Goal, prompt string) (convergeIterationResult, error) {
	var agents []*agentfile.Agent
	for _, name := range goal.UsingAgent {
		agent := e.findAgent(name)
		if agent == nil {
			return convergeIterationResult{}, fmt.Errorf("agent not found: %s", name)
		}
		agents = append(agents, agent)
	}
	if len(agents) == 0 {
		return convergeIterationResult{}, fmt.Errorf("CONVERGE goal %q: no USING agents resolved", goal.Name)
	}

	priorGoals := e.buildPriorGoalsContext()
	task := prompt // the full convergence-aware XML prompt (buildConvergePrompt)

	var lastOutput string
	for i, agent := range agents {
		last := i == len(agents)-1

		role := agent.Name
		systemPrompt := e.interpolate(agent.Prompt)
		if systemPrompt == "" {
			systemPrompt = fmt.Sprintf("You are a %s. Complete the task and return your findings.", role)
		}
		systemPrompt = InformationProcessingGuidance + TersenessGuidance + systemPrompt

		agentTask := task
		var extraTools []llm.ToolDef
		if last {
			extraTools = []llm.ToolDef{convergedToolFor(goal.Outputs)}
			agentTask += convergedInstruction
			if len(goal.Outputs) > 0 {
				agentTask += fmt.Sprintf(" Include your declared output fields (%s) in that same call.", strings.Join(goal.Outputs, ", "))
			}
		}

		// No local tool-call cap: CONVERGE is a sequential pipeline (one
		// agent at a time), so there are no siblings to starve and the
		// agent should be free to use the goal's full remaining budget.
		output, decision, err := e.spawnAgentWithPrompt(ctx, role, systemPrompt, agentTask, nil, agent.Requires, priorGoals, agent.IsSupervised(e.workflow), 0, extraTools...)
		if err != nil {
			// Preserve whatever partial output came back (e.g. a spent
			// budget) as the pipeline's output so far.
			result := convergeIterationResult{Content: strings.TrimSpace(lastOutput)}
			if strings.TrimSpace(output) != "" {
				result.Content = strings.TrimSpace(output)
			}
			return result, err
		}

		if !last {
			lastOutput = output
			priorGoals = append(priorGoals, GoalOutput{ID: "agent:" + agent.Name, Output: output})
			continue
		}

		return decideConvergence(output, decision, goal.Outputs), nil
	}

	// Unreachable: the loop always returns on the last agent.
	return convergeIterationResult{Content: lastOutput}, nil
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

// recordGoalOutcome records how goalName ended, for the workflow Result's
// Goals map and Status computation.
func (e *Executor) recordGoalOutcome(goalName string, outcome GoalOutcome) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.goalOutcomes == nil {
		e.goalOutcomes = make(map[string]GoalOutcome)
	}
	e.goalOutcomes[goalName] = outcome
}

// GoalOutcomes returns a copy of every goal outcome recorded so far.
func (e *Executor) GoalOutcomes() map[string]GoalOutcome {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make(map[string]GoalOutcome, len(e.goalOutcomes))
	for k, v := range e.goalOutcomes {
		result[k] = v
	}
	return result
}

// ConvergenceFailures returns goals that failed to converge, with the
// iteration limit each one exhausted.
func (e *Executor) ConvergenceFailures() map[string]int {
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
