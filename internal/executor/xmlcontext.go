// Package executor provides workflow execution with XML-structured context.
package executor

import (
	"fmt"
	"html"
	"strings"
)

// escapeXML escapes content for safe inclusion in XML elements.
// Prevents injection attacks where LLM output could contain </goal> or similar.
func escapeXML(s string) string {
	return html.EscapeString(s)
}

// GoalOutput represents a completed goal's output for context building.
type GoalOutput struct {
	ID     string // Goal identifier
	Output string // The LLM's response for this goal
}

// LoopIteration represents a completed loop iteration.
type LoopIteration struct {
	N     int          // Iteration number (1-indexed)
	Goals []GoalOutput // Goals completed in this iteration
}

// ConvergenceIteration represents a completed convergence iteration.
type ConvergenceIteration struct {
	N      int    // Iteration number (1-indexed)
	Output string // The output from this iteration
}

// XMLContextBuilder builds XML-structured prompts for LLM communication.
type XMLContextBuilder struct {
	workflowName            string
	priorGoals              []GoalOutput
	iterations              []LoopIteration
	convergenceIterations   []ConvergenceIteration
	isConverge              bool
	currentGoal             struct {
		id          string
		description string
		loopName    string
		loopIter    int
	}
	correction string
}

// NewXMLContextBuilder creates a new context builder for a workflow.
func NewXMLContextBuilder(workflowName string) *XMLContextBuilder {
	return &XMLContextBuilder{
		workflowName: workflowName,
		priorGoals:   make([]GoalOutput, 0),
		iterations:   make([]LoopIteration, 0),
	}
}

// AddPriorGoal adds a completed goal's output to the context.
func (b *XMLContextBuilder) AddPriorGoal(id, output string) {
	b.priorGoals = append(b.priorGoals, GoalOutput{ID: id, Output: output})
}

// AddIteration adds a completed loop iteration to the context.
func (b *XMLContextBuilder) AddIteration(n int, goals []GoalOutput) {
	b.iterations = append(b.iterations, LoopIteration{N: n, Goals: goals})
}

// SetConvergenceMode enables convergence mode for the context builder.
func (b *XMLContextBuilder) SetConvergenceMode() {
	b.isConverge = true
}

// AddConvergenceIteration adds a completed convergence iteration to the context.
func (b *XMLContextBuilder) AddConvergenceIteration(n int, output string) {
	b.convergenceIterations = append(b.convergenceIterations, ConvergenceIteration{N: n, Output: output})
}

// SetCurrentGoal sets the current goal to be executed.
func (b *XMLContextBuilder) SetCurrentGoal(id, description string) {
	b.currentGoal.id = id
	b.currentGoal.description = description
	b.currentGoal.loopName = ""
	b.currentGoal.loopIter = 0
}

// SetCurrentGoalInLoop sets the current goal within a loop iteration.
func (b *XMLContextBuilder) SetCurrentGoalInLoop(id, description, loopName string, iteration int) {
	b.currentGoal.id = id
	b.currentGoal.description = description
	b.currentGoal.loopName = loopName
	b.currentGoal.loopIter = iteration
}

// SetCorrection sets the supervisor correction for the current goal.
func (b *XMLContextBuilder) SetCorrection(correction string) {
	b.correction = correction
}

// Build generates the XML-structured prompt.
// All data content is escaped to prevent injection attacks.
func (b *XMLContextBuilder) Build() string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<workflow name=%q>\n", escapeXML(b.workflowName)))

	// Build context section if there are prior goals, iterations, or convergence iterations
	if len(b.priorGoals) > 0 || len(b.iterations) > 0 || len(b.convergenceIterations) > 0 {
		buf.WriteString("\n<context>\n")

		// Add prior goals (non-loop goals)
		for _, goal := range b.priorGoals {
			buf.WriteString(fmt.Sprintf("  <goal id=%q>\n", escapeXML(goal.ID)))
			escaped := escapeXML(goal.Output)
			buf.WriteString(escaped)
			if !strings.HasSuffix(escaped, "\n") {
				buf.WriteString("\n")
			}
			buf.WriteString("  </goal>\n\n")
		}

		// Add loop iterations
		for _, iter := range b.iterations {
			buf.WriteString(fmt.Sprintf("  <iteration n=\"%d\">\n", iter.N))
			for _, goal := range iter.Goals {
				buf.WriteString(fmt.Sprintf("    <goal id=%q>\n", escapeXML(goal.ID)))
				escaped := escapeXML(goal.Output)
				buf.WriteString(escaped)
				if !strings.HasSuffix(escaped, "\n") {
					buf.WriteString("\n")
				}
				buf.WriteString("    </goal>\n")
			}
			buf.WriteString("  </iteration>\n\n")
		}

		// Add convergence iterations
		if len(b.convergenceIterations) > 0 {
			buf.WriteString("  <convergence-history>\n")
			for _, iter := range b.convergenceIterations {
				buf.WriteString(fmt.Sprintf("    <iteration n=\"%d\">\n", iter.N))
				escaped := escapeXML(iter.Output)
				buf.WriteString(escaped)
				if !strings.HasSuffix(escaped, "\n") {
					buf.WriteString("\n")
				}
				buf.WriteString("    </iteration>\n")
			}
			buf.WriteString("  </convergence-history>\n\n")
		}

		buf.WriteString("</context>\n")
	}

	// Build current goal (all fields escaped)
	buf.WriteString("\n")
	if b.currentGoal.loopName != "" {
		buf.WriteString(fmt.Sprintf("<current-goal id=%q loop=%q iteration=\"%d\">\n",
			escapeXML(b.currentGoal.id), escapeXML(b.currentGoal.loopName), b.currentGoal.loopIter))
	} else {
		buf.WriteString(fmt.Sprintf("<current-goal id=%q>\n", escapeXML(b.currentGoal.id)))
	}
	descEscaped := escapeXML(b.currentGoal.description)
	buf.WriteString(descEscaped)
	if !strings.HasSuffix(descEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</current-goal>\n")

	// Add convergence instruction if in convergence mode
	if b.isConverge {
		buf.WriteString("\n<convergence-instruction>\n")
		buf.WriteString("This is a convergence goal. Review your previous iterations in <convergence-history> and refine your output.\n")
		buf.WriteString("When you are confident that further refinement would not meaningfully improve the result, output ONLY the word: CONVERGED\n")
		buf.WriteString("Do not output CONVERGED prematurely. Only converge when the output is truly stable and complete.\n")
		buf.WriteString("</convergence-instruction>\n")
	}

	// Add correction if present (escape to prevent injection from supervisor LLM)
	if b.correction != "" {
		buf.WriteString("\n<correction source=\"supervisor\">\n")
		escaped := escapeXML(b.correction)
		buf.WriteString(escaped)
		if !strings.HasSuffix(escaped, "\n") {
			buf.WriteString("\n")
		}
		buf.WriteString("</correction>\n")
	}

	buf.WriteString("\n</workflow>")

	return buf.String()
}

// BuildTaskContext builds XML context for a dynamic sub-agent task.
// All data content is escaped to prevent injection attacks.
func BuildTaskContext(role, parentGoal, task string) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", escapeXML(role), escapeXML(parentGoal)))
	taskEscaped := escapeXML(task)
	buf.WriteString(taskEscaped)
	if !strings.HasSuffix(taskEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</task>")

	return buf.String()
}

// BuildTaskContextWithCorrection builds XML context for a sub-agent task with supervisor correction.
// All data content is escaped to prevent injection attacks.
func BuildTaskContextWithCorrection(role, parentGoal, task, correction string) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", escapeXML(role), escapeXML(parentGoal)))
	taskEscaped := escapeXML(task)
	buf.WriteString(taskEscaped)
	if !strings.HasSuffix(taskEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</task>\n")

	buf.WriteString("\n<correction source=\"supervisor\">\n")
	corrEscaped := escapeXML(correction)
	buf.WriteString(corrEscaped)
	if !strings.HasSuffix(corrEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</correction>")

	return buf.String()
}

// BuildParallelGoalContext builds XML context when multiple agents work on the same goal.
// The agentLabel is appended to the goal ID like "evaluate[optimist]".
func (b *XMLContextBuilder) AddPriorGoalWithAgent(id, agentLabel, output string) {
	labeledID := fmt.Sprintf("%s[%s]", id, agentLabel)
	b.priorGoals = append(b.priorGoals, GoalOutput{ID: labeledID, Output: output})
}

// BuildTaskContextWithPriorGoals builds XML context for a sub-agent task including prior goal outputs.
// This ensures sub-agents have access to the workflow context, not just the raw task.
// All data content is escaped to prevent injection attacks.
func BuildTaskContextWithPriorGoals(role, parentGoal, task string, priorGoals []GoalOutput) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", escapeXML(role), escapeXML(parentGoal)))

	// Include prior goal outputs as context (all escaped)
	if len(priorGoals) > 0 {
		buf.WriteString("<context>\n")
		for _, goal := range priorGoals {
			buf.WriteString(fmt.Sprintf("<goal id=%q>\n", escapeXML(goal.ID)))
			escaped := escapeXML(goal.Output)
			buf.WriteString(escaped)
			if !strings.HasSuffix(escaped, "\n") {
				buf.WriteString("\n")
			}
			buf.WriteString("</goal>\n")
		}
		buf.WriteString("</context>\n\n")
	}

	buf.WriteString("<objective>\n")
	taskEscaped := escapeXML(task)
	buf.WriteString(taskEscaped)
	if !strings.HasSuffix(taskEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</objective>\n")
	buf.WriteString("</task>")

	return buf.String()
}
