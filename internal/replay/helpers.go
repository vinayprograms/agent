package replay

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinayprograms/agent/internal/session"
)

// printContent prints verbose content with timeline indentation.
func (r *Replayer) printContent(content string) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fmt.Fprintf(r.output, "      │          │   %s\n", line)
	}
}

// printSubAgentOutput prints sub-agent output with special formatting.
func (r *Replayer) printSubAgentOutput(content string) {
	lines := strings.Split(content, "\n")
	maxLines := 10
	if r.verbosity >= 1 {
		maxLines = 50
	}

	for i, line := range lines {
		if i >= maxLines {
			remaining := len(lines) - maxLines
			if remaining > 0 {
				fmt.Fprintf(r.output, "      │          │     %s\n",
					subagentDimStyle.Render(fmt.Sprintf("... (%d more lines)", remaining)))
			}
			break
		}
		fmt.Fprintf(r.output, "      │          │     %s\n", subagentDimStyle.Render(line))
	}
}

// printArgs prints tool arguments.
func (r *Replayer) printArgs(args map[string]interface{}) {
	for k, v := range args {
		fmt.Fprintf(r.output, "      │          │   %s: %v\n",
			labelStyle.Render(k), v)
	}
}

// printError prints an error.
func (r *Replayer) printError(err string) {
	fmt.Fprintf(r.output, "      │          │   %s\n", errorStyle.Render(err))
}

// printLLMMeta prints LLM metadata (model, tokens, latency).
func (r *Replayer) printLLMMeta(meta *session.EventMeta) {
	if meta == nil {
		return
	}

	fmt.Fprintf(r.output, "      │          │   %s %s",
		labelStyle.Render("model:"), valueStyle.Render(meta.Model))
	if meta.TokensIn > 0 || meta.TokensOut > 0 {
		fmt.Fprintf(r.output, "  %s %d→%d",
			labelStyle.Render("tokens:"), meta.TokensIn, meta.TokensOut)
	}
	if meta.LatencyMs > 0 {
		fmt.Fprintf(r.output, "  %s %dms",
			labelStyle.Render("latency:"), meta.LatencyMs)
	}
	fmt.Fprintf(r.output, "\n")

	if meta.Thinking != "" {
		fmt.Fprintf(r.output, "      │          │\n")
		fmt.Fprintf(r.output, "      │          │   %s\n", blockHeaderStyle.Render("── THINKING ──"))
		r.printContent(meta.Thinking)
	}

	if meta.Prompt != "" {
		fmt.Fprintf(r.output, "      │          │\n")
		fmt.Fprintf(r.output, "      │          │   %s\n", blockHeaderStyle.Render("── FULL PROMPT ──"))
		r.printContent(meta.Prompt)
	}
}

// printLLMDetails prints full LLM interaction details.
func (r *Replayer) printLLMDetails(meta *session.EventMeta) {
	if meta == nil {
		return
	}

	if meta.Thinking != "" {
		fmt.Fprintf(r.output, "      │          │\n")
		fmt.Fprintf(r.output, "      │          │   %s\n", blockHeaderStyle.Render("── THINKING ──"))
		r.printContent(meta.Thinking)
	}

	if meta.Prompt != "" {
		fmt.Fprintf(r.output, "      │          │\n")
		fmt.Fprintf(r.output, "      │          │   %s\n", blockHeaderStyle.Render("── PROMPT ──"))
		r.printContent(meta.Prompt)
	}

	if meta.Response != "" {
		fmt.Fprintf(r.output, "      │          │\n")
		fmt.Fprintf(r.output, "      │          │   %s\n", blockHeaderStyle.Render("── RESPONSE ──"))
		r.printContent(meta.Response)
	}
}

// printTaintLineage prints the taint dependency tree.
func (r *Replayer) printTaintLineage(lineage []session.TaintNode) {
	if len(lineage) == 0 {
		return
	}
	fmt.Fprintf(r.output, "      │          │   %s\n", securityStyle.Render("taint lineage:"))
	for _, node := range lineage {
		r.printTaintNode(node, 0)
	}
}

// printTaintNode recursively prints a taint tree node with indentation.
func (r *Replayer) printTaintNode(node session.TaintNode, depth int) {
	indent := strings.Repeat("  ", depth)
	prefix := "└─"
	if depth == 0 {
		prefix = "●"
	}

	trustColor := dimStyle
	if node.Trust == "untrusted" {
		trustColor = warnStyle
	}

	seqInfo := ""
	if node.EventSeq > 0 {
		seqInfo = dimStyle.Render(fmt.Sprintf(" (seq:%d)", node.EventSeq))
	}

	fmt.Fprintf(r.output, "      │          │     %s%s %s %s %s%s\n",
		indent,
		securityStyle.Render(prefix),
		securityStyle.Render(node.BlockID),
		trustColor.Render(fmt.Sprintf("[%s]", node.Trust)),
		dimStyle.Render(node.Source),
		seqInfo)

	for _, parent := range node.TaintedBy {
		r.printTaintNode(parent, depth+1)
	}
}

// statusStyle returns appropriate style for status.
func (r *Replayer) statusStyle(status string) lipgloss.Style {
	switch status {
	case session.StatusComplete:
		return successStyle
	case session.StatusFailed:
		return errorStyle
	default:
		return warnStyle
	}
}

// verdictStyle returns style for supervision verdicts.
func (r *Replayer) verdictStyle(verdict string) lipgloss.Style {
	switch strings.ToUpper(verdict) {
	case "CONTINUE":
		return successStyle
	case "REORIENT":
		return warnStyle
	case "PAUSE":
		return errorStyle
	default:
		return valueStyle
	}
}

// actionStyle returns style for security actions.
func (r *Replayer) actionStyle(action string) lipgloss.Style {
	switch strings.ToLower(action) {
	case "allow":
		return successStyle
	case "deny":
		return errorStyle
	case "modify":
		return warnStyle
	default:
		return valueStyle
	}
}

// getSecurityContext returns a formatted string showing what the security check relates to.
func (r *Replayer) getSecurityContext(event *session.Event) string {
	var parts []string

	if event.Meta != nil && event.Meta.BlockID != "" {
		parts = append(parts, event.Meta.BlockID)
	}

	if event.Tool != "" {
		parts = append(parts, event.Tool)
	} else if event.Meta != nil && event.Meta.Source != "" {
		parts = append(parts, event.Meta.Source)
	} else if event.CorrelationID != "" {
		corr := event.CorrelationID
		if len(corr) > 12 && strings.Contains(corr, "-") {
			corr = corr[:8] + "..."
		}
		parts = append(parts, corr)
	}

	if len(parts) == 0 {
		return ""
	}

	context := strings.Join(parts, " ")
	if len(context) > 35 {
		context = context[:32] + "..."
	}

	return dimStyle.Render(fmt.Sprintf(" [%s]", context))
}

// getAgentPrefix returns a formatted agent name prefix for sub-agent attribution.
func (r *Replayer) getAgentPrefix(event *session.Event) string {
	if event.Agent == "" && event.AgentRole == "" {
		return ""
	}

	if event.AgentRole == "main" {
		return ""
	}

	name := event.AgentRole
	if name == "" {
		name = event.Agent
	}

	if len(name) > 20 {
		name = name[:17] + "..."
	}

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("13")).
		Bold(true)

	return style.Render(fmt.Sprintf("[%s] ", name))
}

// getArgsHint returns a concise hint about key args for tool result display.
func (r *Replayer) getArgsHint(toolName string, args map[string]interface{}) string {
	if args == nil {
		return ""
	}

	var hint string
	switch toolName {
	case "web_search":
		if q, ok := args["query"].(string); ok {
			hint = truncateHint(q, 60)
		}
	case "web_fetch":
		if u, ok := args["url"].(string); ok {
			hint = truncateHint(u, 80)
		}
	case "read", "write", "glob", "ls":
		if p, ok := args["path"].(string); ok {
			hint = truncateHint(p, 80)
		}
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			hint = truncateHint(cmd, 60)
		}
	case "spawn_agent", "spawn_agents":
		if task, ok := args["task"].(string); ok {
			hint = truncateHint(task, 60)
		}
	}

	if hint == "" {
		return ""
	}
	return dimStyle.Render(fmt.Sprintf(" [%s]", hint))
}

// truncateHint truncates a string to maxLen, adding ... if needed.
func truncateHint(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// truncateContent truncates a string for display.
func truncateContent(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatMap formats a string map for display.
func formatMap(m map[string]string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}
