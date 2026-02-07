// Package replay provides session replay and visualization.
package replay

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/session"
)

// Replayer reads and formats session events for forensic analysis.
type Replayer struct {
	output io.Writer
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
	fmt.Fprintf(r.output, "╔══════════════════════════════════════════════════════════════════════╗\n")
	fmt.Fprintf(r.output, "║ SESSION: %-60s ║\n", sess.ID)
	fmt.Fprintf(r.output, "╠══════════════════════════════════════════════════════════════════════╣\n")
	fmt.Fprintf(r.output, "║ Workflow: %-59s ║\n", sess.WorkflowName)
	fmt.Fprintf(r.output, "║ Status:   %-59s ║\n", sess.Status)
	fmt.Fprintf(r.output, "║ Created:  %-59s ║\n", sess.CreatedAt.Format(time.RFC3339))
	if len(sess.Inputs) > 0 {
		fmt.Fprintf(r.output, "║ Inputs:   %-59s ║\n", formatMap(sess.Inputs))
	}
	fmt.Fprintf(r.output, "╚══════════════════════════════════════════════════════════════════════╝\n\n")

	// Events timeline
	fmt.Fprintf(r.output, "TIMELINE (%d events)\n", len(sess.Events))
	fmt.Fprintf(r.output, "─────────────────────────────────────────────────────────────────────────\n")

	var lastGoal string
	for i, event := range sess.Events {
		r.formatEvent(i+1, &event, &lastGoal)
	}

	// Summary
	fmt.Fprintf(r.output, "\n─────────────────────────────────────────────────────────────────────────\n")
	if sess.Status == session.StatusComplete {
		fmt.Fprintf(r.output, "✓ COMPLETED\n")
	} else if sess.Status == session.StatusFailed {
		fmt.Fprintf(r.output, "✗ FAILED: %s\n", sess.Error)
	} else {
		fmt.Fprintf(r.output, "⋯ RUNNING\n")
	}

	return nil
}

// formatEvent formats a single event for display.
func (r *Replayer) formatEvent(seq int, event *session.Event, lastGoal *string) {
	// Show goal transitions
	if event.Goal != "" && event.Goal != *lastGoal {
		fmt.Fprintf(r.output, "\n▶ GOAL: %s\n", event.Goal)
		*lastGoal = event.Goal
	}

	// Time and sequence
	ts := event.Timestamp.Format("15:04:05.000")
	
	// Format based on event type
	switch event.Type {
	case session.EventWorkflowStart:
		fmt.Fprintf(r.output, "%4d │ %s │ ▶ WORKFLOW START\n", seq, ts)
		
	case session.EventWorkflowEnd:
		fmt.Fprintf(r.output, "%4d │ %s │ ◼ WORKFLOW END (%dms)\n", seq, ts, event.DurationMs)

	case session.EventGoalStart:
		fmt.Fprintf(r.output, "%4d │ %s │ ┌─ GOAL START: %s\n", seq, ts, event.Goal)

	case session.EventGoalEnd:
		fmt.Fprintf(r.output, "%4d │ %s │ └─ GOAL END (%dms)\n", seq, ts, event.DurationMs)
		if r.verbose && event.Content != "" {
			r.printIndented("     │ ", truncate(event.Content, 200))
		}

	case session.EventSystem:
		fmt.Fprintf(r.output, "%4d │ %s │ 📋 SYSTEM\n", seq, ts)
		if r.verbose {
			r.printIndented("     │ ", truncate(event.Content, 500))
		}

	case session.EventUser:
		fmt.Fprintf(r.output, "%4d │ %s │ 👤 USER\n", seq, ts)
		if r.verbose {
			r.printIndented("     │ ", truncate(event.Content, 500))
		}

	case session.EventAssistant:
		fmt.Fprintf(r.output, "%4d │ %s │ 🤖 ASSISTANT\n", seq, ts)
		if r.verbose {
			r.printIndented("     │ ", truncate(event.Content, 500))
		}

	case session.EventToolCall:
		corr := ""
		if event.CorrelationID != "" {
			corr = fmt.Sprintf(" [%s]", event.CorrelationID)
		}
		fmt.Fprintf(r.output, "%4d │ %s │ 🔧 TOOL CALL: %s%s\n", seq, ts, event.Tool, corr)
		if r.verbose && len(event.Args) > 0 {
			r.printIndented("     │ ", formatArgs(event.Args))
		}

	case session.EventToolResult:
		status := "✓"
		if event.Error != "" {
			status = "✗"
		}
		fmt.Fprintf(r.output, "%4d │ %s │ %s TOOL RESULT: %s (%dms)\n", seq, ts, status, event.Tool, event.DurationMs)
		if event.Error != "" {
			r.printIndented("     │ ", "ERROR: "+event.Error)
		} else if r.verbose && event.Content != "" {
			r.printIndented("     │ ", truncate(event.Content, 200))
		}

	case session.EventPhaseCommit:
		fmt.Fprintf(r.output, "%4d │ %s │ 📝 COMMIT (%dms)\n", seq, ts, event.DurationMs)
		if r.verbose && event.Meta != nil {
			r.printIndented("     │ ", fmt.Sprintf("confidence=%s", event.Meta.Confidence))
		}

	case session.EventPhaseExecute:
		fmt.Fprintf(r.output, "%4d │ %s │ ⚙️  EXECUTE (%dms)\n", seq, ts, event.DurationMs)

	case session.EventPhaseReconcile:
		escalate := "pass"
		if event.Meta != nil && event.Meta.Escalate {
			escalate = "ESCALATE"
		}
		fmt.Fprintf(r.output, "%4d │ %s │ 🔍 RECONCILE: %s (%dms)\n", seq, ts, escalate, event.DurationMs)
		if r.verbose && event.Meta != nil && len(event.Meta.Triggers) > 0 {
			r.printIndented("     │ ", fmt.Sprintf("triggers=%v", event.Meta.Triggers))
		}

	case session.EventPhaseSupervise:
		verdict := "CONTINUE"
		if event.Meta != nil && event.Meta.Verdict != "" {
			verdict = event.Meta.Verdict
		}
		fmt.Fprintf(r.output, "%4d │ %s │ 👁️  SUPERVISE: %s (%dms)\n", seq, ts, verdict, event.DurationMs)
		if r.verbose && event.Meta != nil && event.Meta.Correction != "" {
			r.printIndented("     │ ", "correction: "+truncate(event.Meta.Correction, 100))
		}

	case session.EventSecurityBlock:
		if event.Meta != nil {
			fmt.Fprintf(r.output, "%4d │ %s │ 🔒 UNTRUSTED CONTENT: %s (trust=%s, entropy=%.2f)\n", 
				seq, ts, event.Meta.BlockID, event.Meta.Trust, event.Meta.Entropy)
		}

	case session.EventSecurityStatic:
		pass := true
		if event.Meta != nil {
			pass = event.Meta.Pass
		}
		status := "✓ pass"
		if !pass {
			status = "✗ flagged"
		}
		fmt.Fprintf(r.output, "%4d │ %s │ 🛡️  STATIC CHECK: %s\n", seq, ts, status)
		if r.verbose && event.Meta != nil && len(event.Meta.Flags) > 0 {
			r.printIndented("     │ ", fmt.Sprintf("flags=%v", event.Meta.Flags))
		}

	case session.EventSecurityTriage:
		suspicious := false
		if event.Meta != nil {
			suspicious = event.Meta.Suspicious
		}
		status := "✓ benign"
		if suspicious {
			status = "⚠ suspicious → escalating"
		}
		fmt.Fprintf(r.output, "%4d │ %s │ 🔍 LLM TRIAGE: %s\n", seq, ts, status)

	case session.EventSecuritySupervisor:
		action := "allow"
		reason := ""
		if event.Meta != nil {
			if event.Meta.Action != "" {
				action = event.Meta.Action
			}
			reason = event.Meta.Reason
		}
		fmt.Fprintf(r.output, "%4d │ %s │ 👁️  SUPERVISOR: %s", seq, ts, action)
		if reason != "" {
			fmt.Fprintf(r.output, " (%s)", reason)
		}
		fmt.Fprintf(r.output, "\n")

	case session.EventSecurityDecision:
		if event.Meta != nil {
			action := event.Meta.Action
			// Make action more readable
			actionDisplay := action
			switch action {
			case "allow":
				actionDisplay = "✓ ALLOW"
			case "deny":
				actionDisplay = "✗ DENY"
			case "modify":
				actionDisplay = "⚠ MODIFY"
			}
			path := event.Meta.CheckPath
			if path == "" && event.Meta.Tiers != "" {
				path = event.Meta.Tiers // fallback to old format
			}
			fmt.Fprintf(r.output, "%4d │ %s │ ⚖️  DECISION: %s", seq, ts, actionDisplay)
			if event.Meta.Reason != "" {
				fmt.Fprintf(r.output, " - %s", event.Meta.Reason)
			}
			if path != "" {
				fmt.Fprintf(r.output, " [%s]", path)
			}
			fmt.Fprintf(r.output, "\n")
		}

	case session.EventCheckpoint:
		if event.Meta != nil {
			fmt.Fprintf(r.output, "%4d │ %s │ 💾 CHECKPOINT: %s\n", seq, ts, event.Meta.CheckpointType)
		}

	default:
		fmt.Fprintf(r.output, "%4d │ %s │ ⬛ %s\n", seq, ts, event.Type)
	}
}

// printIndented prints text with indentation prefix.
func (r *Replayer) printIndented(prefix string, text string) {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if line != "" {
			fmt.Fprintf(r.output, "%s    %s\n", prefix, line)
		}
	}
}

// truncate truncates a string to max length.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// formatMap formats a string map for display.
func formatMap(m map[string]string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}

// formatArgs formats tool arguments for display.
func formatArgs(args map[string]interface{}) string {
	var parts []string
	for k, v := range args {
		// Truncate long values
		s := fmt.Sprintf("%v", v)
		if len(s) > 50 {
			s = s[:47] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, s))
	}
	return strings.Join(parts, ", ")
}
