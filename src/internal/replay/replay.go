// Package replay provides session replay and visualization.
package replay

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"encoding/json"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinayprograms/agent/internal/session"
)

// Component color scheme - each component has a distinct, consistent color
var (
	// Structural / metadata
	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // Gray - timestamps, metadata

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // Gray - labels

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")) // White - values

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")) // White bold - headers

	// Main agent flow - default/white
	flowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")) // White

	// Tools - Blue
	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")) // Blue

	// Execution supervisor - Yellow
	execSupervisorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("11")) // Yellow

	// Security (supervisor + checks) - Cyan
	securityStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")) // Cyan

	securitySupervisorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("14")) // Cyan bold

	// Sub-agents - Magenta
	subagentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")) // Magenta

	subagentDimStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("5")) // Magenta dim

	// Outcomes
	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")) // Green

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")) // Red

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")) // Yellow

	// Timeline
	seqStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Width(5).
			Align(lipgloss.Right)

	timeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	// Content blocks
	blockHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Italic(true)

	divider = lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Render(strings.Repeat("━", 60))
)

// Replayer reads and formats session events for forensic analysis.
type Replayer struct {
	output  io.Writer
	verbose bool
}

// New creates a new Replayer.
func New(output io.Writer, verbose bool) *Replayer {
	return &Replayer{
		output:  output,
		verbose: verbose,
	}
}

// ReplayFile loads and replays a session from a JSON file.
func (r *Replayer) ReplayFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read session file: %w", err)
	}

	var sess session.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return fmt.Errorf("failed to parse session: %w", err)
	}

	return r.Replay(&sess)
}

// Replay outputs a formatted timeline of session events.
func (r *Replayer) Replay(sess *session.Session) error {
	// Header
	fmt.Fprintln(r.output)
	fmt.Fprintf(r.output, "%s %s\n", titleStyle.Render("SESSION"), valueStyle.Render(sess.ID))
	fmt.Fprintln(r.output, divider)
	fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Workflow:"), valueStyle.Render(sess.WorkflowName))
	fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Status:  "), r.statusStyle(sess.Status).Render(sess.Status))
	fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Created: "), valueStyle.Render(sess.CreatedAt.Format(time.RFC3339)))
	if len(sess.Inputs) > 0 {
		fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Inputs:  "), valueStyle.Render(formatMap(sess.Inputs)))
	}
	fmt.Fprintln(r.output)

	// Events timeline
	fmt.Fprintf(r.output, "%s %s\n", titleStyle.Render("TIMELINE"), dimStyle.Render(fmt.Sprintf("(%d events)", len(sess.Events))))
	fmt.Fprintln(r.output, divider)

	var lastGoal string
	for i, event := range sess.Events {
		r.formatEvent(i+1, &event, &lastGoal)
	}

	// Summary
	fmt.Fprintln(r.output)
	fmt.Fprintln(r.output, divider)
	switch sess.Status {
	case session.StatusComplete:
		fmt.Fprintln(r.output, successStyle.Render("COMPLETED"))
	case session.StatusFailed:
		fmt.Fprintf(r.output, "%s %s\n", errorStyle.Render("FAILED:"), valueStyle.Render(sess.Error))
	default:
		fmt.Fprintln(r.output, warnStyle.Render("RUNNING"))
	}
	fmt.Fprintln(r.output)

	return nil
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

// formatEvent formats a single event for display.
func (r *Replayer) formatEvent(seq int, event *session.Event, lastGoal *string) {
	// Show goal transitions
	if event.Goal != "" && event.Goal != *lastGoal {
		fmt.Fprintln(r.output)
		fmt.Fprintf(r.output, "%s %s\n", flowStyle.Render("GOAL:"), valueStyle.Render(event.Goal))
		fmt.Fprintln(r.output)
		*lastGoal = event.Goal
	}

	// Time and sequence
	ts := timeStyle.Render(event.Timestamp.Format("15:04:05"))
	seqNum := seqStyle.Render(fmt.Sprintf("%d", seq))

	// Format based on event type
	switch event.Type {
	case session.EventWorkflowStart:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("WORKFLOW START"))

	case session.EventWorkflowEnd:
		fmt.Fprintf(r.output, "%s │ %s │ %s %s\n", seqNum, ts,
			flowStyle.Render("WORKFLOW END"),
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))

	case session.EventGoalStart:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts,
			flowStyle.Render("GOAL START"))

	case session.EventGoalEnd:
		fmt.Fprintf(r.output, "%s │ %s │ %s %s\n", seqNum, ts,
			flowStyle.Render("GOAL END"),
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
		if r.verbose && event.Content != "" {
			r.printContent(event.Content)
		}

	case session.EventSystem:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, dimStyle.Render("SYSTEM"))
		if r.verbose && event.Content != "" {
			r.printContent(event.Content)
		}

	case session.EventUser:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("USER"))
		if r.verbose && event.Content != "" {
			r.printContent(event.Content)
		}

	case session.EventAssistant:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("ASSISTANT"))
		if r.verbose && event.Content != "" {
			r.printContent(event.Content)
		}

	case session.EventToolCall:
		corr := ""
		if event.CorrelationID != "" {
			corr = dimStyle.Render(fmt.Sprintf(" [%s]", event.CorrelationID))
		}
		fmt.Fprintf(r.output, "%s │ %s │ %s %s%s\n", seqNum, ts,
			toolStyle.Render("TOOL CALL:"),
			valueStyle.Render(event.Tool),
			corr)
		if r.verbose && len(event.Args) > 0 {
			r.printArgs(event.Args)
		}

	case session.EventToolResult:
		if event.Error != "" {
			fmt.Fprintf(r.output, "%s │ %s │ %s %s %s\n", seqNum, ts,
				toolStyle.Render("TOOL RESULT:"),
				errorStyle.Render(event.Tool+" FAILED"),
				dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
			r.printError(event.Error)
		} else {
			fmt.Fprintf(r.output, "%s │ %s │ %s %s %s\n", seqNum, ts,
				toolStyle.Render("TOOL RESULT:"),
				valueStyle.Render(event.Tool),
				dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
			if r.verbose && event.Content != "" {
				r.printContent(event.Content)
			}
		}

	case session.EventPhaseCommit:
		fmt.Fprintf(r.output, "%s │ %s │ %s %s\n", seqNum, ts,
			flowStyle.Render("COMMIT"),
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
		if r.verbose && event.Meta != nil && event.Meta.Confidence != "" {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("confidence: %s", event.Meta.Confidence)))
		}

	case session.EventPhaseExecute:
		fmt.Fprintf(r.output, "%s │ %s │ %s %s\n", seqNum, ts,
			flowStyle.Render("EXECUTE"),
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))

	case session.EventPhaseReconcile:
		status := successStyle.Render("pass")
		if event.Meta != nil && event.Meta.Escalate {
			status = warnStyle.Render("ESCALATE")
		}
		fmt.Fprintf(r.output, "%s │ %s │ %s %s %s\n", seqNum, ts,
			flowStyle.Render("RECONCILE:"),
			status,
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
		if r.verbose && event.Meta != nil && len(event.Meta.Triggers) > 0 {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("triggers: %v", event.Meta.Triggers)))
		}

	case session.EventPhaseSupervise:
		verdict := "CONTINUE"
		supervisorType := "EXECUTION"
		if event.Meta != nil {
			if event.Meta.Verdict != "" {
				verdict = event.Meta.Verdict
			}
			if event.Meta.SupervisorType != "" {
				supervisorType = strings.ToUpper(event.Meta.SupervisorType)
			}
		}
		verdictStyled := r.verdictStyle(verdict).Render(verdict)
		// Use appropriate style based on supervisor type
		var supervStyle lipgloss.Style
		if supervisorType == "SECURITY" {
			supervStyle = securitySupervisorStyle
		} else {
			supervStyle = execSupervisorStyle
		}
		fmt.Fprintf(r.output, "%s │ %s │ %s %s %s\n", seqNum, ts,
			supervStyle.Render(fmt.Sprintf("SUPERVISOR [%s]:", supervisorType)),
			verdictStyled,
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
		if event.Meta != nil {
			if event.Meta.Model != "" {
				fmt.Fprintf(r.output, "      │          │   %s\n",
					dimStyle.Render(fmt.Sprintf("model: %s", event.Meta.Model)))
			}
			if event.Meta.Correction != "" {
				fmt.Fprintf(r.output, "      │          │   %s\n",
					dimStyle.Render(fmt.Sprintf("correction: %s", event.Meta.Correction)))
			}
			if r.verbose {
				r.printLLMDetails(event.Meta)
			}
		}

	case session.EventSecurityBlock:
		if event.Meta != nil {
			fmt.Fprintf(r.output, "%s │ %s │ %s %s %s\n", seqNum, ts,
				securityStyle.Render("UNTRUSTED CONTENT:"),
				valueStyle.Render(event.Meta.BlockID),
				dimStyle.Render(fmt.Sprintf("(trust=%s, entropy=%.2f)", event.Meta.Trust, event.Meta.Entropy)))
		}

	case session.EventSecurityStatic:
		status := successStyle.Render("pass")
		if event.Meta != nil && !event.Meta.Pass {
			status = warnStyle.Render("flagged")
		}
		fmt.Fprintf(r.output, "%s │ %s │ %s %s\n", seqNum, ts,
			securityStyle.Render("STATIC CHECK:"),
			status)
		if r.verbose && event.Meta != nil && len(event.Meta.Flags) > 0 {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("flags: %v", event.Meta.Flags)))
		}

	case session.EventSecurityTriage:
		status := successStyle.Render("benign")
		if event.Meta != nil && event.Meta.Suspicious {
			status = warnStyle.Render("suspicious - escalating")
		}
		model := ""
		if event.Meta != nil && event.Meta.Model != "" {
			model = dimStyle.Render(fmt.Sprintf(" [%s]", event.Meta.Model))
		}
		fmt.Fprintf(r.output, "%s │ %s │ %s %s%s\n", seqNum, ts,
			securityStyle.Render("TRIAGE:"),
			status, model)
		if r.verbose && event.Meta != nil {
			r.printLLMDetails(event.Meta)
		}

	case session.EventSecuritySupervisor:
		action := "allow"
		if event.Meta != nil {
			if event.Meta.Action != "" {
				action = event.Meta.Action
			}
			if event.Meta.Verdict != "" {
				action = event.Meta.Verdict
			}
		}
		actionStyled := r.actionStyle(action).Render(action)
		model := ""
		if event.Meta != nil && event.Meta.Model != "" {
			model = dimStyle.Render(fmt.Sprintf(" [%s]", event.Meta.Model))
		}
		fmt.Fprintf(r.output, "%s │ %s │ %s %s%s\n", seqNum, ts,
			securitySupervisorStyle.Render("SUPERVISOR [SECURITY]:"),
			actionStyled, model)
		if event.Meta != nil && event.Meta.Reason != "" {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(event.Meta.Reason))
		}
		if r.verbose && event.Meta != nil {
			r.printLLMDetails(event.Meta)
		}

	case session.EventSecurityDecision:
		if event.Meta != nil {
			action := event.Meta.Action
			actionDisplay := r.actionStyle(action).Render(strings.ToUpper(action))
			path := event.Meta.CheckPath
			if path == "" && event.Meta.Tiers != "" {
				path = event.Meta.Tiers
			}
			pathStr := ""
			if path != "" {
				pathStr = dimStyle.Render(fmt.Sprintf(" [%s]", path))
			}
			fmt.Fprintf(r.output, "%s │ %s │ %s %s%s\n", seqNum, ts,
				securityStyle.Render("DECISION:"),
				actionDisplay, pathStr)
			if event.Meta.Reason != "" {
				fmt.Fprintf(r.output, "      │          │   %s\n",
					dimStyle.Render(event.Meta.Reason))
			}
		}

	case session.EventCheckpoint:
		if event.Meta != nil {
			fmt.Fprintf(r.output, "%s │ %s │ %s %s\n", seqNum, ts,
				dimStyle.Render("CHECKPOINT:"),
				valueStyle.Render(event.Meta.CheckpointType))
		}

	default:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, dimStyle.Render(string(event.Type)))
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

// printContent prints verbose content.
func (r *Replayer) printContent(content string) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fmt.Fprintf(r.output, "      │          │   %s\n", line)
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

// formatMap formats a string map for display.
func formatMap(m map[string]string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}
