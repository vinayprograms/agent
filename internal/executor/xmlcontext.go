package executor

import (
	"fmt"
	"html"
	"strings"
)

// escapeXMLAttr escapes content for safe inclusion inside a double-quoted
// XML attribute value (e.g. id="..."). It must neutralize '"' as well as
// '&'/'<'/'>', since an unescaped '"' would close the attribute early and
// let the rest of the string be read as markup.
func escapeXMLAttr(s string) string {
	return html.EscapeString(s)
}

// escapeXMLBody escapes content for safe inclusion as XML element body text
// (e.g. the text between <goal id="...">...</goal>). Body text is not inside
// an attribute, so unlike escapeXMLAttr it only needs to neutralize the
// characters that can form markup: '&', '<', and '>'. Escaping '<' and '>'
// is sufficient to stop prior-goal output from forging a closing tag such as
// </goal>, opening a fake <goal id="x">, or otherwise being parsed as
// structure — no complete tag can survive with both angle brackets escaped.
//
// Deliberately NOT escaping '"' and '\” here: html.EscapeString escapes
// those too (-> &#34; / &#39;), which corrupts prior agent output containing
// source code, JSON, or shell quoting (e.g. Query().Get("password") becomes
// Query().Get(&#34;password&#34;)) without adding any protection, since
// quotes carry no structural meaning outside an attribute value.
func escapeXMLBody(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// GoalOutput represents a completed goal's output for context building.
type GoalOutput struct {
	ID     string // Goal identifier
	Output string // The LLM's response for this goal
}

// ConvergenceIteration represents a completed convergence iteration.
type ConvergenceIteration struct {
	N      int    // Iteration number (1-indexed)
	Output string // The output from this iteration
}

// brief accumulates the XML-structured goal briefing sent to the model.
// Every value written into it is escaped, so model- or supervisor-authored
// text cannot forge an element.
type brief struct {
	workflowName          string
	priorGoals            []GoalOutput
	convergenceIterations []ConvergenceIteration
	isConverge            bool
	currentGoal           struct {
		id          string
		description string
	}
	correction string
}

// newBrief starts a goal briefing for a workflow.
func newBrief(workflowName string) *brief {
	return &brief{workflowName: workflowName}
}

// AddPriorGoal adds a completed goal's output to the context.
func (b *brief) AddPriorGoal(id, output string) {
	b.priorGoals = append(b.priorGoals, GoalOutput{ID: id, Output: output})
}

// SetConvergenceMode enables convergence mode for the context builder.
func (b *brief) SetConvergenceMode() {
	b.isConverge = true
}

// AddConvergenceIteration adds a completed convergence iteration to the context.
func (b *brief) AddConvergenceIteration(n int, output string) {
	b.convergenceIterations = append(b.convergenceIterations, ConvergenceIteration{N: n, Output: output})
}

// SetCurrentGoal sets the current goal to be executed.
func (b *brief) SetCurrentGoal(id, description string) {
	b.currentGoal.id = id
	b.currentGoal.description = description
}

// SetCorrection sets the supervisor correction for the current goal.
func (b *brief) SetCorrection(correction string) {
	b.correction = correction
}

// String renders the briefing as XML.
func (b *brief) String() string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<workflow name=%q>\n", escapeXMLAttr(b.workflowName)))

	// Build context section if there are prior goals or convergence iterations
	if len(b.priorGoals) > 0 || len(b.convergenceIterations) > 0 {
		buf.WriteString("\n<context>\n")

		// Add prior goals
		for _, goal := range b.priorGoals {
			buf.WriteString(fmt.Sprintf("  <goal id=%q>\n", escapeXMLAttr(goal.ID)))
			escaped := escapeXMLBody(goal.Output)
			buf.WriteString(escaped)
			if !strings.HasSuffix(escaped, "\n") {
				buf.WriteString("\n")
			}
			buf.WriteString("  </goal>\n\n")
		}

		// Add convergence iterations
		if len(b.convergenceIterations) > 0 {
			buf.WriteString("  <convergence-history>\n")
			for _, iter := range b.convergenceIterations {
				buf.WriteString(fmt.Sprintf("    <iteration n=\"%d\">\n", iter.N))
				escaped := escapeXMLBody(iter.Output)
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
	buf.WriteString(fmt.Sprintf("<current-goal id=%q>\n", escapeXMLAttr(b.currentGoal.id)))
	descEscaped := escapeXMLBody(b.currentGoal.description)
	buf.WriteString(descEscaped)
	if !strings.HasSuffix(descEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</current-goal>\n")

	// Add convergence instruction if in convergence mode
	if b.isConverge {
		buf.WriteString("\n<convergence-instruction>\n")
		buf.WriteString("This is a convergence goal. Review your previous iterations in <convergence-history> and refine your output.\n")
		buf.WriteString("When you are confident that further refinement would not meaningfully improve the result, output your complete final result, then the word CONVERGED on a line of its own as the last line.\n")
		buf.WriteString("The final result must appear in the response itself even if you also wrote it to a file — it is what downstream goals receive.\n")
		buf.WriteString("Do not output CONVERGED prematurely. Only converge when the output is truly stable and complete.\n")
		buf.WriteString("</convergence-instruction>\n")
	}

	// Add correction if present (escape to prevent injection from supervisor LLM)
	if b.correction != "" {
		buf.WriteString("\n<correction source=\"supervisor\">\n")
		escaped := escapeXMLBody(b.correction)
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

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", escapeXMLAttr(role), escapeXMLAttr(parentGoal)))
	taskEscaped := escapeXMLBody(task)
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

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", escapeXMLAttr(role), escapeXMLAttr(parentGoal)))
	taskEscaped := escapeXMLBody(task)
	buf.WriteString(taskEscaped)
	if !strings.HasSuffix(taskEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</task>\n")

	buf.WriteString("\n<correction source=\"supervisor\">\n")
	corrEscaped := escapeXMLBody(correction)
	buf.WriteString(corrEscaped)
	if !strings.HasSuffix(corrEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</correction>")

	return buf.String()
}

// BuildTaskContextWithPriorGoals builds XML context for a sub-agent task including prior goal outputs.
// This ensures sub-agents have access to the workflow context, not just the raw task.
// All data content is escaped to prevent injection attacks.
func BuildTaskContextWithPriorGoals(role, parentGoal, task string, priorGoals []GoalOutput) string {
	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("<task role=%q parent-goal=%q>\n", escapeXMLAttr(role), escapeXMLAttr(parentGoal)))

	// Include prior goal outputs as context (all escaped)
	if len(priorGoals) > 0 {
		buf.WriteString("<context>\n")
		for _, goal := range priorGoals {
			buf.WriteString(fmt.Sprintf("<goal id=%q>\n", escapeXMLAttr(goal.ID)))
			escaped := escapeXMLBody(goal.Output)
			buf.WriteString(escaped)
			if !strings.HasSuffix(escaped, "\n") {
				buf.WriteString("\n")
			}
			buf.WriteString("</goal>\n")
		}
		buf.WriteString("</context>\n\n")
	}

	buf.WriteString("<objective>\n")
	taskEscaped := escapeXMLBody(task)
	buf.WriteString(taskEscaped)
	if !strings.HasSuffix(taskEscaped, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("</objective>\n")
	buf.WriteString("</task>")

	return buf.String()
}
