// Package executor provides workflow execution with XML-structured context.
package executor

import (
	"fmt"
	"strings"
)

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

// XMLContextBuilder builds XML-structured prompts for LLM communication.
type XMLContextBuilder struct {
	workflowName string
	priorGoals   []GoalOutput
	iterations   []LoopIteration
	currentGoal  struct {
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
func (b *XMLContextBuilder) Build() string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<workflow name=%q>\n", b.workflowName))

	// Build context section if there are prior goals or iterations
	if len(b.priorGoals) > 0 || len(b.iterations) > 0 {
		buf.WriteString("\n<context>\n")

		// Add prior goals (non-loop goals)
		for _, goal := range b.priorGoals {
			buf.WriteString(fmt.Sprintf("  <goal id=%q>\n", goal.ID))
			buf.WriteString(goal.Output)
			if !strings.HasSuffix(goal.Output, "\n") {
				buf.WriteString("\n")
			}
			buf.WriteString("  </goal>\n\n")
		}

		// Add loop iterations
		for _, iter := range b.iterations {
			buf.WriteString(fmt.Sprintf("  <iteration n=\"%d\">\n", iter.N))
			for _, goal := range iter.Goals {
				buf.WriteString(fmt.Sprintf("    <goal id=%q>\n", goal.ID))
				buf.WriteString(goal.Output)
				if !strings.HasSuffix(goal.Output, "\n") {
					buf.WriteString("\n")
				}
				buf.WriteString("    </goal>\n")
			}
			buf.WriteString("  </iteration>\n\n")
		}

		buf.WriteString("</context>\n")
	}

	// Build current goal
	buf.WriteString("\n")
	if b.currentGoal.loopName != "" {
		buf.WriteString(fmt.Sprintf("<current-goal id=%q loop=%q iteration=\"%d\">\n",
			b.currentGoal.id, b.currentGoal.loopName, b.currentGoal.loopIter))
	} else {
		buf.WriteString(fmt.Sprintf("<current-goal id=%q>\n", b.currentGoal.id))
	}
	buf.WriteString(b.currentGoal.description)
	if !strings.HasSuffix(b.currentGoal.description, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</current-goal>\n")

	// Add correction if present
	if b.correction != "" {
		buf.WriteString("\n<correction source=\"supervisor\">\n")
		buf.WriteString(b.correction)
		if !strings.HasSuffix(b.correction, "\n") {
			buf.WriteString("\n")
		}
		buf.WriteString("</correction>\n")
	}

	buf.WriteString("\n</workflow>")

	return buf.String()
}

// BuildTaskContext builds XML context for a dynamic sub-agent task.
func BuildTaskContext(role, parentGoal, task string) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", role, parentGoal))
	buf.WriteString(task)
	if !strings.HasSuffix(task, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</task>")

	return buf.String()
}

// BuildTaskContextWithCorrection builds XML context for a sub-agent task with supervisor correction.
func BuildTaskContextWithCorrection(role, parentGoal, task, correction string) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", role, parentGoal))
	buf.WriteString(task)
	if !strings.HasSuffix(task, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</task>\n")

	buf.WriteString("\n<correction source=\"supervisor\">\n")
	buf.WriteString(correction)
	if !strings.HasSuffix(correction, "\n") {
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
func BuildTaskContextWithPriorGoals(role, parentGoal, task string, priorGoals []GoalOutput) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", role, parentGoal))

	// Include prior goal outputs as context
	if len(priorGoals) > 0 {
		buf.WriteString("<context>\n")
		for _, goal := range priorGoals {
			buf.WriteString(fmt.Sprintf("<goal id=%q>\n", goal.ID))
			buf.WriteString(goal.Output)
			if !strings.HasSuffix(goal.Output, "\n") {
				buf.WriteString("\n")
			}
			buf.WriteString("</goal>\n")
		}
		buf.WriteString("</context>\n\n")
	}

	buf.WriteString("<objective>\n")
	buf.WriteString(task)
	if !strings.HasSuffix(task, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</objective>\n")
	buf.WriteString("</task>")

	return buf.String()
}
