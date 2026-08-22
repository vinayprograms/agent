package replay

import (
	"fmt"
	"io"
	"strings"

	"github.com/vinayprograms/agent/internal/session"
)

// formatEvent formats a single event for display.
func (r *Replayer) formatEvent(w io.Writer, seq int, event *session.Event, lastGoal *string) {
	// Show goal transitions
	if event.Goal != "" && event.Goal != *lastGoal {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s %s\n", flowStyle.Render("GOAL:"), valueStyle.Render(event.Goal))
		fmt.Fprintln(w)
		*lastGoal = event.Goal
	}

	ts := timeStyle.Render(event.Timestamp.Format("15:04:05"))
	seqNum := seqStyle.Render(fmt.Sprintf("%d", seq))

	switch event.Type {
	case session.EventWorkflowStart:
		r.fmtWorkflowStart(w, seqNum, ts)
	case session.EventWorkflowEnd:
		r.fmtWorkflowEnd(w, seqNum, ts, event)
	case session.EventGoalStart:
		r.fmtGoalStart(w, seqNum, ts)
	case session.EventGoalEnd:
		r.fmtGoalEnd(w, seqNum, ts, event)
	case session.EventSubAgentStart:
		r.fmtSubAgentStart(w, seqNum, ts, event)
	case session.EventSubAgentEnd:
		r.fmtSubAgentEnd(w, seqNum, ts, event)
	case session.EventSystem:
		r.fmtSystem(w, seqNum, ts, event)
	case session.EventUser:
		r.fmtUser(w, seqNum, ts, event)
	case session.EventAssistant:
		r.fmtAssistant(w, seqNum, ts, event)
	case session.EventToolCall:
		r.fmtToolCall(w, seqNum, ts, event)
	case session.EventToolResult:
		r.fmtToolResult(w, seqNum, ts, event)
	case session.EventPhaseCommit:
		r.fmtPhaseCommit(w, seqNum, ts, event)
	case session.EventPhaseExecute:
		r.fmtPhaseExecute(w, seqNum, ts, event)
	case session.EventPhaseReconcile:
		r.fmtPhaseReconcile(w, seqNum, ts, event)
	case session.EventPhaseSupervise:
		r.fmtPhaseSupervise(w, seqNum, ts, event)
	case session.EventSecurityBlock:
		r.fmtSecurityBlock(w, seqNum, ts, event)
	case session.EventSecurityStatic:
		r.fmtSecurityStatic(w, seqNum, ts, event)
	case session.EventSecurityTriage:
		r.fmtSecurityTriage(w, seqNum, ts, event)
	case session.EventSecuritySupervisor:
		r.fmtSecuritySupervisor(w, seqNum, ts, event)
	case session.EventSecurityDecision:
		r.fmtSecurityDecision(w, seqNum, ts, event)
	case session.EventCheckpoint:
		r.fmtCheckpoint(w, seqNum, ts, event)
	case session.EventBashSecurity:
		r.fmtBashSecurity(w, seqNum, ts, event)
	case session.EventWarning:
		r.fmtWarning(w, seqNum, ts, event)
	default:
		fmt.Fprintf(w, "%s │ %s │ %s\n", seqNum, ts, dimStyle.Render(string(event.Type)))
	}
}

func (r *Replayer) fmtWorkflowStart(w io.Writer, seqNum, ts string) {
	fmt.Fprintf(w, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("WORKFLOW START"))
}

func (r *Replayer) fmtWorkflowEnd(w io.Writer, seqNum, ts string, event *session.Event) {
	fmt.Fprintf(w, "%s │ %s │ %s %s\n", seqNum, ts,
		flowStyle.Render("WORKFLOW END"),
		dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
}

func (r *Replayer) fmtGoalStart(w io.Writer, seqNum, ts string) {
	fmt.Fprintf(w, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("GOAL START"))
}

func (r *Replayer) fmtGoalEnd(w io.Writer, seqNum, ts string, event *session.Event) {
	fmt.Fprintf(w, "%s │ %s │ %s %s\n", seqNum, ts,
		flowStyle.Render("GOAL END"),
		dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
	if r.verbosity >= 1 && event.Content != "" {
		r.printContent(w, event.Content)
	}
}

func (r *Replayer) fmtSubAgentStart(w io.Writer, seqNum, ts string, event *session.Event) {
	if event.Meta == nil {
		return
	}
	model := ""
	if event.Meta.SubAgentModel != "" {
		model = dimStyle.Render(fmt.Sprintf(" [%s]", event.Meta.SubAgentModel))
	}
	fmt.Fprintf(w, "%s │ %s │ %s %s%s\n", seqNum, ts,
		subagentStyle.Render("SUBAGENT START:"),
		valueStyle.Render(event.Meta.SubAgentName),
		model)
	if r.verbosity >= 1 && event.Meta.SubAgentTask != "" {
		fmt.Fprintf(w, "      │          │   %s %s\n",
			dimStyle.Render("task:"),
			dimStyle.Render(truncateContent(event.Meta.SubAgentTask, 100)))
	}
}

func (r *Replayer) fmtSubAgentEnd(w io.Writer, seqNum, ts string, event *session.Event) {
	if event.Meta == nil {
		return
	}
	status := successStyle.Render("complete")
	if event.Error != "" {
		status = errorStyle.Render("failed")
	}
	fmt.Fprintf(w, "%s │ %s │ %s %s %s %s\n", seqNum, ts,
		subagentStyle.Render("SUBAGENT END:"),
		valueStyle.Render(event.Meta.SubAgentName),
		status,
		dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
	if event.Error != "" {
		fmt.Fprintf(w, "      │          │   %s\n", errorStyle.Render(event.Error))
	} else if event.Meta.SubAgentOutput != "" {
		fmt.Fprintf(w, "      │          │   %s\n", subagentDimStyle.Render("output:"))
		r.printSubAgentOutput(w, event.Meta.SubAgentOutput)
	}
}

func (r *Replayer) fmtSystem(w io.Writer, seqNum, ts string, event *session.Event) {
	fmt.Fprintf(w, "%s │ %s │ %s\n", seqNum, ts, dimStyle.Render("SYSTEM"))
	if r.verbosity >= 1 && event.Content != "" {
		r.printContent(w, event.Content)
	}
}

func (r *Replayer) fmtWarning(w io.Writer, seqNum, ts string, event *session.Event) {
	fmt.Fprintf(w, "%s │ %s │ %s %s\n", seqNum, ts,
		warnStyle.Render("⚠ WARNING:"),
		warnStyle.Render(event.Content))
}

func (r *Replayer) fmtUser(w io.Writer, seqNum, ts string, event *session.Event) {
	fmt.Fprintf(w, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("USER"))
	if r.verbosity >= 1 && event.Content != "" {
		r.printContent(w, event.Content)
	}
}

func (r *Replayer) fmtAssistant(w io.Writer, seqNum, ts string, event *session.Event) {
	fmt.Fprintf(w, "%s │ %s │ %s\n", seqNum, ts, flowStyle.Render("ASSISTANT"))
	if r.verbosity >= 1 && event.Content != "" {
		r.printContent(w, event.Content)
	}
	if r.verbosity >= 2 && event.Meta != nil {
		r.printLLMMeta(w, event.Meta)
	}
}

func (r *Replayer) fmtToolCall(w io.Writer, seqNum, ts string, event *session.Event) {
	agentPrefix := r.getAgentPrefix(event)
	corr := ""
	if event.CorrelationID != "" {
		corr = dimStyle.Render(fmt.Sprintf(" [%s]", event.CorrelationID))
	}
	fmt.Fprintf(w, "%s │ %s │ %s%s %s%s\n", seqNum, ts,
		agentPrefix,
		toolStyle.Render("TOOL CALL:"),
		valueStyle.Render(event.Tool),
		corr)
	if r.verbosity >= 1 && len(event.Args) > 0 {
		r.printArgs(w, event.Args)
	}
}

func (r *Replayer) fmtToolResult(w io.Writer, seqNum, ts string, event *session.Event) {
	agentPrefix := r.getAgentPrefix(event)
	corr := ""
	if event.CorrelationID != "" {
		corr = dimStyle.Render(fmt.Sprintf(" [%s]", event.CorrelationID))
	}
	argsHint := r.getArgsHint(event.Tool, event.Args)

	if event.Error != "" {
		fmt.Fprintf(w, "%s │ %s │ %s%s %s%s %s%s\n", seqNum, ts,
			agentPrefix,
			toolStyle.Render("TOOL RESULT:"),
			errorStyle.Render(event.Tool+" FAILED"),
			argsHint,
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)),
			corr)
		r.printError(w, event.Error)
	} else {
		fmt.Fprintf(w, "%s │ %s │ %s%s %s%s %s%s\n", seqNum, ts,
			agentPrefix,
			toolStyle.Render("TOOL RESULT:"),
			valueStyle.Render(event.Tool),
			argsHint,
			dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)),
			corr)
		if r.verbosity >= 1 && event.Content != "" {
			r.printContent(w, event.Content)
		}
	}
}

func (r *Replayer) fmtPhaseCommit(w io.Writer, seqNum, ts string, event *session.Event) {
	fmt.Fprintf(w, "%s │ %s │ %s %s\n", seqNum, ts,
		flowStyle.Render("COMMIT"),
		dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))

	if r.verbosity >= 1 && event.Meta != nil {
		if event.Meta.Confidence != "" {
			fmt.Fprintf(w, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("confidence: %s", event.Meta.Confidence)))
		}
		if event.Meta.Commitment != "" {
			summary := truncateContent(event.Meta.Commitment, 80)
			fmt.Fprintf(w, "      │          │   %s %s\n",
				dimStyle.Render("intent:"),
				dimStyle.Render(summary))
		}
	}
	if r.verbosity >= 2 && event.Meta != nil && event.Meta.Commitment != "" {
		if len(event.Meta.Commitment) > 80 {
			fmt.Fprintf(w, "      │          │\n")
			fmt.Fprintf(w, "      │          │   %s\n", blockHeaderStyle.Render("── COMMITMENT ──"))
			r.printContent(w, event.Meta.Commitment)
		}
	}
}

func (r *Replayer) fmtPhaseExecute(w io.Writer, seqNum, ts string, event *session.Event) {
	result := ""
	if event.Meta != nil && event.Meta.Result != "" {
		result = dimStyle.Render(fmt.Sprintf(" [%s]", event.Meta.Result))
	}
	fmt.Fprintf(w, "%s │ %s │ %s%s %s\n", seqNum, ts,
		flowStyle.Render("EXECUTE"),
		result,
		dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
}

func (r *Replayer) fmtPhaseReconcile(w io.Writer, seqNum, ts string, event *session.Event) {
	status := successStyle.Render("pass")
	if event.Meta != nil && event.Meta.Escalate {
		status = warnStyle.Render("ESCALATE")
	}
	fmt.Fprintf(w, "%s │ %s │ %s %s %s\n", seqNum, ts,
		flowStyle.Render("RECONCILE:"),
		status,
		dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))
	if r.verbosity >= 1 && event.Meta != nil && len(event.Meta.Triggers) > 0 {
		fmt.Fprintf(w, "      │          │   %s\n",
			dimStyle.Render(fmt.Sprintf("triggers: %v", event.Meta.Triggers)))
	}
}

func (r *Replayer) fmtPhaseSupervise(w io.Writer, seqNum, ts string, event *session.Event) {
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

	var label string
	var style = execSupervisorStyle
	if supervisorType == "security" {
		label = "SECURITY: supervisor"
		style = securitySupervisorStyle
	} else {
		label = "SUPERVISOR"
	}

	fmt.Fprintf(w, "%s │ %s │ %s %s %s\n", seqNum, ts,
		style.Render(label),
		verdictStyled,
		dimStyle.Render(fmt.Sprintf("(%dms)", event.DurationMs)))

	if event.Meta != nil {
		if event.Meta.Model != "" {
			fmt.Fprintf(w, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("model: %s", event.Meta.Model)))
		}
		if event.Meta.Correction != "" {
			fmt.Fprintf(w, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("correction: %s", event.Meta.Correction)))
		}
		if r.verbosity >= 1 {
			r.printLLMDetails(w, event.Meta)
		}
	}
}

func (r *Replayer) fmtSecurityBlock(w io.Writer, seqNum, ts string, event *session.Event) {
	if event.Meta == nil {
		return
	}
	sourceInfo := ""
	if event.Meta.Source != "" {
		sourceInfo = dimStyle.Render(fmt.Sprintf(" ← %s", event.Meta.Source))
	} else if event.Tool != "" {
		sourceInfo = dimStyle.Render(fmt.Sprintf(" ← %s", event.Tool))
	}
	fmt.Fprintf(w, "%s │ %s │ %s %s %s%s\n", seqNum, ts,
		securityStyle.Render("SECURITY: untrusted content"),
		valueStyle.Render(event.Meta.BlockID),
		dimStyle.Render(fmt.Sprintf("(entropy=%.2f)", event.Meta.Entropy)),
		sourceInfo)
	if len(event.Meta.RelatedBlocks) > 0 {
		fmt.Fprintf(w, "      │          │   %s %s\n",
			securityStyle.Render("tainted by:"),
			warnStyle.Render(strings.Join(event.Meta.RelatedBlocks, ", ")))
	}
}

func (r *Replayer) fmtSecurityStatic(w io.Writer, seqNum, ts string, event *session.Event) {
	status := successStyle.Render("pass")
	if event.Meta != nil && !event.Meta.Pass {
		status = warnStyle.Render("flagged")
	}
	context := r.getSecurityContext(event)
	fmt.Fprintf(w, "%s │ %s │ %s %s%s\n", seqNum, ts,
		securityStyle.Render("SECURITY: static check"),
		status, context)

	if event.Meta != nil {
		if len(event.Meta.Flags) > 0 {
			fmt.Fprintf(w, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("flags: %v", event.Meta.Flags)))
		}
		if len(event.Meta.RelatedBlocks) > 1 {
			fmt.Fprintf(w, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("related: %v", event.Meta.RelatedBlocks)))
		}
		if len(event.Meta.TaintLineage) > 0 {
			r.printTaintLineage(w, event.Meta.TaintLineage)
		}
		if event.Meta.SkipReason != "" {
			fmt.Fprintf(w, "      │          │   %s\n",
				dimStyle.Render(fmt.Sprintf("no escalation: %s", event.Meta.SkipReason)))
		}
	}
}

func (r *Replayer) fmtSecurityTriage(w io.Writer, seqNum, ts string, event *session.Event) {
	status := successStyle.Render("benign")
	if event.Meta != nil && event.Meta.Suspicious {
		status = warnStyle.Render("suspicious - escalating")
	}
	model := ""
	if event.Meta != nil && event.Meta.Model != "" {
		model = dimStyle.Render(fmt.Sprintf(" [%s]", event.Meta.Model))
	}
	context := r.getSecurityContext(event)
	fmt.Fprintf(w, "%s │ %s │ %s %s%s%s\n", seqNum, ts,
		securityStyle.Render("SECURITY: triage"),
		status, model, context)

	if event.Meta != nil && event.Meta.SkipReason != "" {
		fmt.Fprintf(w, "      │          │   %s\n",
			dimStyle.Render(fmt.Sprintf("no escalation: %s", event.Meta.SkipReason)))
	}
	if r.verbosity >= 1 && event.Meta != nil {
		r.printLLMDetails(w, event.Meta)
	}
}

func (r *Replayer) fmtSecuritySupervisor(w io.Writer, seqNum, ts string, event *session.Event) {
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
	fmt.Fprintf(w, "%s │ %s │ %s %s%s%s\n", seqNum, ts,
		securitySupervisorStyle.Render("SECURITY: supervisor"),
		actionStyled, model, context)
	if event.Meta != nil && event.Meta.Reason != "" {
		fmt.Fprintf(w, "      │          │   %s\n",
			dimStyle.Render(event.Meta.Reason))
	}
	if r.verbosity >= 1 && event.Meta != nil {
		r.printLLMDetails(w, event.Meta)
	}
}

func (r *Replayer) fmtSecurityDecision(w io.Writer, seqNum, ts string, event *session.Event) {
	if event.Meta == nil {
		return
	}
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
	fmt.Fprintf(w, "%s │ %s │ %s %s%s%s\n", seqNum, ts,
		securityStyle.Render("SECURITY: decision"),
		actionDisplay, pathStr, context)
	if event.Meta.Reason != "" {
		fmt.Fprintf(w, "      │          │   %s\n",
			dimStyle.Render(event.Meta.Reason))
	}
}

func (r *Replayer) fmtCheckpoint(w io.Writer, seqNum, ts string, event *session.Event) {
	if event.Meta != nil {
		fmt.Fprintf(w, "%s │ %s │ %s %s\n", seqNum, ts,
			dimStyle.Render("CHECKPOINT:"),
			valueStyle.Render(event.Meta.CheckpointType))
	}
}

func (r *Replayer) fmtBashSecurity(w io.Writer, seqNum, ts string, event *session.Event) {
	step := "check"
	action := "allow"
	command := ""
	reason := ""
	durationMs := event.DurationMs

	if event.Meta != nil {
		step = event.Meta.CheckName
		if event.Meta.Pass {
			action = "allow"
		} else {
			action = "deny"
		}
		if event.Meta.Source != "" {
			command = event.Meta.Source
		}
		reason = event.Meta.Reason
	}

	actionStyled := r.actionStyle(action).Render(strings.ToUpper(action))
	stepLabel := dimStyle.Render(fmt.Sprintf("[%s]", step))

	// Non-verbose: Only show denials
	if r.verbosity == 0 {
		if action == "deny" {
			fmt.Fprintf(w, "%s │ %s │ %s %s %s\n", seqNum, ts,
				bashStyle.Render("BASH:"),
				stepLabel,
				actionStyled)
		}
		return
	}

	// Verbosity >= 1
	cmdHint := ""
	if command != "" {
		cmdHint = dimStyle.Render(fmt.Sprintf(" %s", truncateContent(command, 50)))
	}
	timing := ""
	if durationMs > 0 {
		timing = dimStyle.Render(fmt.Sprintf(" (%dms)", durationMs))
	}

	fmt.Fprintf(w, "%s │ %s │ %s %s %s%s%s\n", seqNum, ts,
		bashStyle.Render("BASH:"),
		stepLabel,
		actionStyled,
		cmdHint,
		timing)

	// Verbosity >= 2
	if r.verbosity >= 2 {
		if command != "" && len(command) > 50 {
			fmt.Fprintf(w, "      │          │   %s %s\n",
				dimStyle.Render("command:"),
				valueStyle.Render(command))
		}
		if reason != "" {
			fmt.Fprintf(w, "      │          │   %s %s\n",
				dimStyle.Render("reason:"),
				dimStyle.Render(reason))
		}
	}
}
