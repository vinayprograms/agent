// Package replay provides session replay and visualization.
package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
	output         io.Writer
	verbosity      int // 0=normal, 1=verbose (-v), 2=very verbose (-vv)
	maxContentSize int // Maximum size for Content fields (0 = unlimited)
}

// ReplayerOption configures a Replayer.
type ReplayerOption func(*Replayer)

// WithMaxContentSize limits Content field size to avoid OOM on large sessions.
func WithMaxContentSize(size int) ReplayerOption {
	return func(r *Replayer) {
		r.maxContentSize = size
	}
}

// New creates a new Replayer.
// verbosity: 0=normal, 1=verbose (-v), 2=very verbose (-vv)
func New(output io.Writer, verbosity int, opts ...ReplayerOption) *Replayer {
	r := &Replayer{
		output:         output,
		verbosity:      verbosity,
		maxContentSize: 50 * 1024, // Default: 50KB per content field
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ReplayFile loads and replays a session from a file.
// Supports both JSONL (new) and JSON (legacy) formats.
func (r *Replayer) ReplayFile(path string) error {
	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}
	return r.Replay(sess)
}

// ReplayFileInteractive loads and replays with interactive pager.
func (r *Replayer) ReplayFileInteractive(path string) error {
	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}
	return r.ReplayInteractive(sess)
}

// ReplayInteractive outputs a formatted timeline using an interactive pager.
func (r *Replayer) ReplayInteractive(sess *session.Session) error {
	// Render to string buffer first
	var buf strings.Builder
	oldOutput := r.output
	r.output = &buf
	
	if err := r.Replay(sess); err != nil {
		r.output = oldOutput
		return err
	}
	r.output = oldOutput

	// Launch pager
	title := fmt.Sprintf("Session: %s", sess.ID)
	p := NewPager(title, buf.String())
	return p.Run(buf.String())
}

// ReplayFileLive loads and replays with live file watching.
func (r *Replayer) ReplayFileLive(path string) error {
	// Create render function that reloads the file
	renderFunc := func() (string, error) {
		sess, err := r.loadSession(path)
		if err != nil {
			return "", err
		}

		// Render to string
		var buf strings.Builder
		oldOutput := r.output
		r.output = &buf
		err = r.Replay(sess)
		r.output = oldOutput
		
		if err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	// Initial render to get session info for title
	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}

	title := fmt.Sprintf("Session: %s (LIVE)", sess.ID)
	p := NewPager(title, "")
	return p.RunLive(path, renderFunc)
}

// loadSession loads a session from a file, detecting format automatically.
func (r *Replayer) loadSession(path string) (*session.Session, error) {
	format, err := session.DetectFormat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to detect format: %w", err)
	}

	if format == "jsonl" {
		return r.loadJSONL(path)
	}
	return r.loadLegacyJSON(path)
}

// loadJSONL loads a session from JSONL format (streaming).
func (r *Replayer) loadJSONL(path string) (*session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}
	defer f.Close()

	sess := &session.Session{
		Inputs:  make(map[string]string),
		State:   make(map[string]interface{}),
		Outputs: make(map[string]string),
		Events:  []session.Event{},
	}

	// Use bufio.Reader instead of Scanner - no line length limits
	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				// Process final line if no trailing newline
				if len(line) > 0 {
					if parseErr := r.parseJSONLLine(line, sess); parseErr != nil {
						return nil, parseErr
					}
				}
				break
			}
			return nil, fmt.Errorf("error reading JSONL: %w", err)
		}

		// Skip empty lines
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if err := r.parseJSONLLine(line, sess); err != nil {
			return nil, err
		}
	}

	return sess, nil
}

// parseJSONLLine parses a single JSONL line into the session.
func (r *Replayer) parseJSONLLine(line []byte, sess *session.Session) error {
	var record session.JSONLRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return fmt.Errorf("failed to parse JSONL line: %w", err)
	}

	switch record.RecordType {
	case session.RecordTypeHeader:
		sess.ID = record.ID
		sess.WorkflowName = record.WorkflowName
		sess.Inputs = record.Inputs
		sess.CreatedAt = record.CreatedAt
		
	case session.RecordTypeEvent:
		if record.Event != nil {
			evt := *record.Event
			// Truncate large content to avoid OOM
			if r.maxContentSize > 0 && len(evt.Content) > r.maxContentSize {
				evt.Content = evt.Content[:r.maxContentSize] + fmt.Sprintf("\n... [truncated, %d bytes total]", len(record.Event.Content))
			}
			sess.Events = append(sess.Events, evt)
		}
		
	case session.RecordTypeFooter:
		sess.Status = record.Status
		sess.Result = record.Result
		sess.Error = record.Error
		sess.Outputs = record.Outputs
		sess.State = record.State
		sess.UpdatedAt = record.UpdatedAt
	}

	return nil
}

// loadLegacyJSON loads a session from legacy JSON format.
func (r *Replayer) loadLegacyJSON(path string) (*session.Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var sess session.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("failed to parse session: %w", err)
	}

	// Truncate large content fields to avoid OOM
	if r.maxContentSize > 0 {
		for i := range sess.Events {
			if len(sess.Events[i].Content) > r.maxContentSize {
				originalSize := len(sess.Events[i].Content)
				sess.Events[i].Content = sess.Events[i].Content[:r.maxContentSize] + 
					fmt.Sprintf("\n... [truncated, %d bytes total]", originalSize)
			}
		}
	}

	return &sess, nil
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
		if r.verbosity >= 1 && event.Content != "" {
			r.printContent(event.Content)
		}

	case session.EventSubAgentStart:
		if event.Meta != nil {
			model := ""
			if event.Meta.SubAgentModel != "" {
				model = dimStyle.Render(fmt.Sprintf(" [%s]", event.Meta.SubAgentModel))
			}
			fmt.Fprintf(r.output, "%s │ %s │ %s %s%s\n", seqNum, ts,
				subagentStyle.Render("SUBAGENT START:"),
				valueStyle.Render(event.Meta.SubAgentName),
				model)
			if r.verbosity >= 1 && event.Meta.SubAgentTask != "" {
				fmt.Fprintf(r.output, "      │          │   %s %s\n",
					dimStyle.Render("task:"),
					dimStyle.Render(truncateContent(event.Meta.SubAgentTask, 100)))
			}
		}

	case session.EventSubAgentEnd:
		if event.Meta != nil {
			status := successStyle.Render("complete")
			if event.Error != "" {
				status = errorStyle.Render("failed")
			}
			fmt.Fprintf(r.output, "%s │ %s │ %s %s %s %s\n", seqNum, ts,
				subagentStyle.Render("SUBAGENT END:"),
				valueStyle.Render(event.Meta.SubAgentName),
				status,
				dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
			if event.Error != "" {
				fmt.Fprintf(r.output, "      │          │   %s\n",
					errorStyle.Render(event.Error))
			} else if event.Meta.SubAgentOutput != "" {
				// Always show sub-agent output (this is the key visibility you need)
				fmt.Fprintf(r.output, "      │          │   %s\n",
					subagentDimStyle.Render("output:"))
				r.printSubAgentOutput(event.Meta.SubAgentOutput)
			}
		}

	case session.EventSystem:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, dimStyle.Render("SYSTEM"))
		if r.verbosity >= 1 && event.Content != "" {
			r.printContent(event.Content)
		}

	case session.EventUser:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("USER"))
		if r.verbosity >= 1 && event.Content != "" {
			r.printContent(event.Content)
		}

	case session.EventAssistant:
		fmt.Fprintf(r.output, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("ASSISTANT"))
		if r.verbosity >= 1 && event.Content != "" {
			r.printContent(event.Content)
		}
		// -vv: Show full LLM metadata
		if r.verbosity >= 2 && event.Meta != nil {
			r.printLLMMeta(event.Meta)
		}

	case session.EventToolCall:
		// Show agent attribution if present (for sub-agents)
		agentPrefix := r.getAgentPrefix(event)
		corr := ""
		if event.CorrelationID != "" {
			corr = dimStyle.Render(fmt.Sprintf(" [%s]", event.CorrelationID))
		}
		fmt.Fprintf(r.output, "%s │ %s │ %s%s %s%s\n", seqNum, ts,
			agentPrefix,
			toolStyle.Render("TOOL CALL:"),
			valueStyle.Render(event.Tool),
			corr)
		if r.verbosity >= 1 && len(event.Args) > 0 {
			r.printArgs(event.Args)
		}

	case session.EventToolResult:
		agentPrefix := r.getAgentPrefix(event)
		corr := ""
		if event.CorrelationID != "" {
			corr = dimStyle.Render(fmt.Sprintf(" [%s]", event.CorrelationID))
		}
		// Show key args inline for context (e.g., query for web_search, url for web_fetch)
		argsHint := r.getArgsHint(event.Tool, event.Args)
		if event.Error != "" {
			fmt.Fprintf(r.output, "%s │ %s │ %s%s %s%s %s%s\n", seqNum, ts,
				agentPrefix,
				toolStyle.Render("TOOL RESULT:"),
				errorStyle.Render(event.Tool+" FAILED"),
				argsHint,
				dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)),
				corr)
			r.printError(event.Error)
		} else {
			fmt.Fprintf(r.output, "%s │ %s │ %s%s %s%s %s%s\n", seqNum, ts,
				agentPrefix,
				toolStyle.Render("TOOL RESULT:"),
				valueStyle.Render(event.Tool),
				argsHint,
				dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)),
				corr)
			if r.verbosity >= 1 && event.Content != "" {
				r.printContent(event.Content)
			}
		}

	case session.EventPhaseCommit:
		fmt.Fprintf(r.output, "%s │ %s │ %s %s\n", seqNum, ts,
			flowStyle.Render("COMMIT"),
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
		if r.verbosity >= 1 && event.Meta != nil && event.Meta.Confidence != "" {
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
		if r.verbosity >= 1 && event.Meta != nil && len(event.Meta.Triggers) > 0 {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("triggers: %v", event.Meta.Triggers)))
		}

	case session.EventPhaseSupervise:
		verdict := "CONTINUE"
		supervisorType := "execution"
		if event.Meta != nil {
			if event.Meta.Verdict != "" {
				verdict = event.Meta.Verdict
			}
			if event.Meta.SupervisorType != "" {
				supervisorType = strings.ToLower(event.Meta.SupervisorType)
			}
		}
		verdictStyled := r.verdictStyle(verdict).Render(verdict)
		
		// Format based on supervisor type
		var label string
		var style lipgloss.Style
		if supervisorType == "security" {
			label = "SECURITY: supervisor"
			style = securitySupervisorStyle
		} else {
			label = "SUPERVISOR"
			style = execSupervisorStyle
		}
		
		fmt.Fprintf(r.output, "%s │ %s │ %s %s %s\n", seqNum, ts,
			style.Render(label),
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
			if r.verbosity >= 1 {
				r.printLLMDetails(event.Meta)
			}
		}

	case session.EventSecurityBlock:
		if event.Meta != nil {
			// For untrusted content, show block ID prominently (it's the key identifier)
			sourceInfo := ""
			if event.Meta.Source != "" {
				sourceInfo = dimStyle.Render(fmt.Sprintf(" ← %s", event.Meta.Source))
			} else if event.Tool != "" {
				sourceInfo = dimStyle.Render(fmt.Sprintf(" ← %s", event.Tool))
			}
			fmt.Fprintf(r.output, "%s │ %s │ %s %s %s%s\n", seqNum, ts,
				securityStyle.Render("SECURITY: untrusted content"),
				valueStyle.Render(event.Meta.BlockID),
				dimStyle.Render(fmt.Sprintf("(entropy=%.2f)", event.Meta.Entropy)),
				sourceInfo)
			// Show which blocks influenced this content (taint source)
			if len(event.Meta.RelatedBlocks) > 0 {
				fmt.Fprintf(r.output, "      │          │   %s %s\n",
					securityStyle.Render("tainted by:"),
					warnStyle.Render(strings.Join(event.Meta.RelatedBlocks, ", ")))
			}
		}

	case session.EventSecurityStatic:
		status := successStyle.Render("pass")
		if event.Meta != nil && !event.Meta.Pass {
			status = warnStyle.Render("flagged")
		}
		context := r.getSecurityContext(event)
		fmt.Fprintf(r.output, "%s │ %s │ %s %s%s\n", seqNum, ts,
			securityStyle.Render("SECURITY: static check"),
			status, context)
		if event.Meta != nil && len(event.Meta.Flags) > 0 {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("flags: %v", event.Meta.Flags)))
		}
		// Show related blocks if multiple contributed
		if event.Meta != nil && len(event.Meta.RelatedBlocks) > 1 {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("related: %v", event.Meta.RelatedBlocks)))
		}
		// Show taint lineage if available
		if event.Meta != nil && len(event.Meta.TaintLineage) > 0 {
			r.printTaintLineage(event.Meta.TaintLineage)
		}
		// Show skip reason (why escalation didn't happen)
		if event.Meta != nil && event.Meta.SkipReason != "" {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("no escalation: %s", event.Meta.SkipReason)))
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
		context := r.getSecurityContext(event)
		fmt.Fprintf(r.output, "%s │ %s │ %s %s%s%s\n", seqNum, ts,
			securityStyle.Render("SECURITY: triage"),
			status, model, context)
		// Show skip reason (why supervisor wasn't invoked)
		if event.Meta != nil && event.Meta.SkipReason != "" {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("no escalation: %s", event.Meta.SkipReason)))
		}
		if r.verbosity >= 1 && event.Meta != nil {
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
		context := r.getSecurityContext(event)
		fmt.Fprintf(r.output, "%s │ %s │ %s %s%s%s\n", seqNum, ts,
			securitySupervisorStyle.Render("SECURITY: supervisor"),
			actionStyled, model, context)
		if event.Meta != nil && event.Meta.Reason != "" {
			fmt.Fprintf(r.output, "      │          │   %s\n",
				dimStyle.Render(event.Meta.Reason))
		}
		if r.verbosity >= 1 && event.Meta != nil {
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
			context := r.getSecurityContext(event)
			fmt.Fprintf(r.output, "%s │ %s │ %s %s%s%s\n", seqNum, ts,
				securityStyle.Render("SECURITY: decision"),
				actionDisplay, pathStr, context)
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

// getSecurityContext returns a formatted string showing what the security check relates to.
func (r *Replayer) getSecurityContext(event *session.Event) string {
	var parts []string
	
	// Block ID is the most important identifier for forensic analysis
	if event.Meta != nil && event.Meta.BlockID != "" {
		parts = append(parts, event.Meta.BlockID)
	}
	
	// Add source/tool for additional context
	if event.Tool != "" {
		parts = append(parts, event.Tool)
	} else if event.Meta != nil && event.Meta.Source != "" {
		parts = append(parts, event.Meta.Source)
	} else if event.CorrelationID != "" {
		// Use correlation ID if nothing else
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
	
	// Truncate if too long
	if len(context) > 35 {
		context = context[:32] + "..."
	}
	
	return dimStyle.Render(fmt.Sprintf(" [%s]", context))
}

// getAgentPrefix returns a formatted agent name prefix for sub-agent attribution.
// Returns empty for main agent (role="main") to avoid clutter.
func (r *Replayer) getAgentPrefix(event *session.Event) string {
	if event.Agent == "" && event.AgentRole == "" {
		return ""
	}
	
	// Skip prefix for main agent
	if event.AgentRole == "main" {
		return ""
	}
	
	// Use role if available (more descriptive for dynamic agents)
	name := event.AgentRole
	if name == "" {
		name = event.Agent
	}
	
	// Truncate long names
	if len(name) > 20 {
		name = name[:17] + "..."
	}
	
	// Magenta for sub-agents (matches color scheme from MEMORY)
	subagentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("13")). // Magenta
		Bold(true)
	
	return subagentStyle.Render(fmt.Sprintf("[%s] ", name))
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

// getArgsHint returns a concise hint about key args for tool result display.
// Shows query for web_search, url for web_fetch, path for read/write, etc.
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
	// Remove newlines for single-line display
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// printContent prints verbose content.
func (r *Replayer) printContent(content string) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fmt.Fprintf(r.output, "      │          │   %s\n", line)
	}
}

// printSubAgentOutput prints sub-agent output with special formatting.
// Always shows first few lines, then truncates with line count.
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

// printLLMMeta prints LLM metadata (model, tokens, latency) and optionally full prompt/response.
func (r *Replayer) printLLMMeta(meta *session.EventMeta) {
	if meta == nil {
		return
	}

	// Always show summary in -vv mode
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

	// Show thinking if available
	if meta.Thinking != "" {
		fmt.Fprintf(r.output, "      │          │\n")
		fmt.Fprintf(r.output, "      │          │   %s\n", blockHeaderStyle.Render("── THINKING ──"))
		r.printContent(meta.Thinking)
	}

	// Show full prompt
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

	// Format: ● b0001 [untrusted] source (seq:42)
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

	// Print parent blocks (TaintedBy)
	for _, parent := range node.TaintedBy {
		r.printTaintNode(parent, depth+1)
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

// truncateContent truncates a string for display.
func truncateContent(s string, maxLen int) string {
	// Remove newlines for single-line display
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
