package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agentkit/llm"
)

// Structured-log helpers. Message strings and keys mirror the old agentkit
// logging domain events so existing log consumers keep working (A-G5).

// logExecutionComplete logs the completion of workflow execution.
func (e *Executor) logExecutionComplete(workflow string, start time.Time, status string) {
	e.logger.Info("execution_complete",
		"workflow", workflow,
		"duration", time.Since(start).String(),
		"status", status)
}

// logPhaseStart logs the start of an execution phase.
func (e *Executor) logPhaseStart(phase, goal, step string) {
	e.logger.Debug("phase_start", "phase", phase, "goal", goal, "step", step)
}

// logPhaseComplete logs the completion of an execution phase.
func (e *Executor) logPhaseComplete(phase, goal, step string, start time.Time, result string) {
	e.logger.Debug("phase_complete",
		"phase", phase,
		"goal", goal,
		"step", step,
		"duration", time.Since(start).String(),
		"result", result)
}

// logToolOutcome logs a tool result (real-time output).
func (e *Executor) logToolOutcome(tool string, duration time.Duration, err error) {
	if err != nil {
		e.logger.Error("tool_error", "tool", tool, "duration", duration.String(), "error", err.Error())
		return
	}
	e.logger.Debug("tool_result", "tool", tool, "duration", duration.String())
}

// logEvent logs a generic event to the session.
func (e *Executor) logEvent(eventType, content string) {
	if e.session == nil {
		return
	}
	e.session.AddEvent(session.Event{
		Type:      eventType,
		Goal:      e.currentGoal,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// LogBashSecurity logs a bash security decision to the session.
// Its signature matches shellguard.Gate.OnDecision; the runtime wires it
// onto the gate attached to the bash tool.
func (e *Executor) LogBashSecurity(command, step string, allowed bool, reason string, durationMs int64, inputTokens, outputTokens int) {
	if e.session == nil {
		return
	}

	verdict := "ALLOW"
	action := "allow"
	if !allowed {
		verdict = "BLOCK"
		action = "deny"
	}

	content := fmt.Sprintf("[%s] %s: %s", step, verdict, command)
	if reason != "" {
		content += fmt.Sprintf(" | reason: %s", reason)
	}

	e.session.AddEvent(session.Event{
		Type:       session.EventBashSecurity,
		Goal:       e.currentGoal,
		Content:    content,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			CheckName: step, // "deterministic" or "llm"
			Pass:      allowed,
			Action:    action,
			Reason:    reason,
			LatencyMs: durationMs,
			Source:    command,
			TokensIn:  inputTokens,
			TokensOut: outputTokens,
		},
	})

	// Also log to structured logger
	e.logger.Debug("bash security check", "step", step, "allowed", allowed, "command", command, "reason", reason)
}

// logToolCall logs a tool call event to the session.
// Returns a correlation ID that should be passed to logToolResult.
func (e *Executor) logToolCall(ctx context.Context, name string, args map[string]any) string {
	corrID := fmt.Sprintf("tool-%d", time.Now().UnixNano())

	if e.session == nil {
		return corrID
	}

	// Get agent identity from context (thread-safe for parallel execution)
	agentID := getAgentIdentity(ctx)

	e.session.AddEvent(session.Event{
		Type:          session.EventToolCall,
		CorrelationID: corrID,
		Goal:          e.currentGoal,
		Tool:          name,
		Args:          args,
		Agent:         agentID.Name,
		AgentRole:     agentID.Role,
		Timestamp:     time.Now(),
	})
	return corrID
}

// logToolResult logs a tool result event to the session.
func (e *Executor) logToolResult(ctx context.Context, name string, args map[string]any, corrID string, result string, err error, duration time.Duration) {
	e.logToolOutcome(name, duration, err)

	if e.session == nil {
		return
	}

	// Get agent identity from context (thread-safe for parallel execution)
	agentID := getAgentIdentity(ctx)

	// Only include tool output in debug mode (PII protection)
	var content string
	if e.debug {
		content = result
	}

	event := session.Event{
		Type:          session.EventToolResult,
		CorrelationID: corrID,
		Goal:          e.currentGoal,
		Tool:          name,
		Args:          args,
		Content:       content,
		DurationMs:    duration.Milliseconds(),
		Agent:         agentID.Name,
		AgentRole:     agentID.Role,
		Timestamp:     time.Now(),
	}
	// Event.Error does not survive persistence (see EventMeta.Error's doc
	// comment), so a failed tool's error text is duplicated into meta.error
	// here, unconditionally — an error carries no arbitrary tool output, so
	// it is not subject to the same PII rule as Content/meta.result below.
	meta := &session.EventMeta{}
	if e.debug {
		meta.Result = truncateForLog(result, 500)
	}
	if err != nil {
		event.Error = err.Error()
		meta.Error = err.Error()
	}
	event.Meta = meta
	e.session.AddEvent(event)
}

// logLLMCall logs an LLM call with full prompt/response for forensics.
func (e *Executor) logLLMCall(ctx context.Context, eventType string, messages []llm.Message, resp *llm.ChatResponse, duration time.Duration) {
	if e.session == nil {
		return
	}

	agentID := getAgentIdentity(ctx)

	// Build meta with model/token info (always logged)
	meta := &session.EventMeta{
		Model:     resp.Model,
		TokensIn:  resp.InputTokens,
		TokensOut: resp.OutputTokens,
	}

	// Content only logged in debug mode (PII/data protection)
	var content string
	if e.debug {
		content = resp.Content
		var promptParts []string
		for _, msg := range messages {
			promptParts = append(promptParts, fmt.Sprintf("[%s] %s", msg.Role, truncateForLog(msg.Content, 500)))
		}
		meta.Prompt = truncateForLog(fmt.Sprintf("%v", promptParts), 2000)
		meta.Response = truncateForLog(resp.Content, 2000)
		meta.Thinking = truncateForLog(resp.Thinking, 2000)
	}

	e.session.AddEvent(session.Event{
		Type:       eventType,
		Goal:       e.currentGoal,
		Content:    content,
		DurationMs: duration.Milliseconds(),
		Agent:      agentID.Name,
		AgentRole:  agentID.Role,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
}

// logGoalStart logs the start of a goal execution.
func (e *Executor) logGoalStart(goalName string) {
	if e.session == nil {
		return
	}
	e.currentGoal = goalName
	e.session.AddEvent(session.Event{
		Type:      session.EventGoalStart,
		Goal:      goalName,
		Content:   fmt.Sprintf("Starting goal: %s", goalName),
		Timestamp: time.Now(),
	})
}

// logGoalEnd logs the end of a goal execution.
func (e *Executor) logGoalEnd(goalName, output string) {
	if e.session == nil {
		return
	}

	// Only include output content in debug mode (PII protection)
	var content string
	if e.debug {
		const maxLen = 2000
		if len(output) > maxLen {
			content = output[:maxLen] + "... (truncated)"
		} else {
			content = output
		}
	}

	e.session.AddEvent(session.Event{
		Type:      session.EventGoalEnd,
		Goal:      goalName,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// logPhaseCommit logs the COMMIT phase of goal execution.
func (e *Executor) logPhaseCommit(goal, commitment, confidence string, durationMs int64) {
	if e.session == nil {
		return
	}

	// Only include commitment content in debug mode (PII protection)
	var content, commitmentMeta string
	if e.debug {
		content = commitment
		commitmentMeta = commitment
	}

	e.session.AddEvent(session.Event{
		Type:       session.EventPhaseCommit,
		Goal:       goal,
		Content:    content,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:      "COMMIT",
			Commitment: commitmentMeta,
			Confidence: confidence,
		},
	})
}

// logPhaseExecute logs the EXECUTE phase of goal execution.
func (e *Executor) logPhaseExecute(goal, result string, durationMs int64) {
	if e.session == nil {
		return
	}

	// Only include result content in debug mode (PII protection)
	var content, resultMeta string
	if e.debug {
		content = truncateForLog(result, 1000)
		resultMeta = truncateForLog(result, 2000)
	}

	e.session.AddEvent(session.Event{
		Type:       session.EventPhaseExecute,
		Goal:       goal,
		Content:    content,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:  "EXECUTE",
			Result: resultMeta,
		},
	})
}

// logPhaseReconcile logs the RECONCILE phase of goal execution.
func (e *Executor) logPhaseReconcile(goal, step string, triggers []string, escalate bool, durationMs int64) {
	if e.session == nil {
		return
	}
	e.session.AddEvent(session.Event{
		Type:       session.EventPhaseReconcile,
		Goal:       goal,
		Step:       step,
		Content:    fmt.Sprintf("Reconcile: triggers=%v, escalate=%v", triggers, escalate),
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:    "RECONCILE",
			Triggers: triggers,
			Escalate: escalate,
		},
	})
}

// logPhaseSupervise logs the SUPERVISE phase of goal execution.
func (e *Executor) logPhaseSupervise(goal, step, verdict, guidance string, humanRequired bool, durationMs int64) {
	if e.session == nil {
		return
	}

	meta := &session.EventMeta{
		Phase:         "SUPERVISE",
		Verdict:       verdict,
		HumanRequired: humanRequired,
	}

	// Only include LLM content in debug mode (PII protection)
	if e.debug {
		meta.Guidance = guidance
	}

	e.session.AddEvent(session.Event{
		Type:       session.EventPhaseSupervise,
		Goal:       goal,
		Step:       step,
		Content:    fmt.Sprintf("Verdict: %s", verdict),
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
}

// logCheckpoint logs a checkpoint creation.
func (e *Executor) logCheckpoint(checkpointType, goal, step, checkpointID string) {
	if e.session == nil {
		return
	}
	e.session.AddEvent(session.Event{
		Type:      session.EventCheckpoint,
		Goal:      goal,
		Step:      step,
		Content:   fmt.Sprintf("%s checkpoint: %s", checkpointType, checkpointID),
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			CheckpointType: checkpointType,
			CheckpointID:   checkpointID,
		},
	})
}

// logSecurityBlock logs when a content block is registered for security tracking.
func (e *Executor) logSecurityBlock(blockID, trust, blockType, source, xmlBlock string, entropy float64) {
	if e.session == nil {
		return
	}

	// Only include XML content in debug mode (PII protection)
	var content string
	if e.debug {
		content = truncateForLog(xmlBlock, 500)
	}

	e.session.AddEvent(session.Event{
		Type:      session.EventSecurityBlock,
		Goal:      e.currentGoal,
		Content:   content,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			BlockID:   blockID,
			Trust:     trust,
			BlockType: blockType,
			Source:    source,
			Entropy:   entropy,
		},
	})
}

// logSecurityStatic logs a static security check result.
func (e *Executor) logSecurityStatic(tool, blockID string, relatedBlockIDs []string, pass bool, flags []string, skipReason string, taintLineage []session.TaintNode) {
	if e.session == nil {
		return
	}
	e.session.AddEvent(session.Event{
		Type:      session.EventSecurityStatic,
		Tool:      tool,
		Goal:      e.currentGoal,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			CheckName:     "static",
			BlockID:       blockID,
			RelatedBlocks: relatedBlockIDs,
			Pass:          pass,
			Flags:         flags,
			SkipReason:    skipReason,
			TaintLineage:  taintLineage,
		},
	})
}

// logSecurityTriage logs LLM triage check to session.
func (e *Executor) logSecurityTriage(tool, blockID string, suspicious bool, model string, latencyMs int64, inputTokens, outputTokens int, skipReason string) {
	if e.session == nil {
		return
	}

	meta := &session.EventMeta{
		CheckName:  "triage",
		BlockID:    blockID,
		Suspicious: suspicious,
		Model:      model,
		LatencyMs:  latencyMs,
		TokensIn:   inputTokens,
		TokensOut:  outputTokens,
		SkipReason: skipReason,
	}

	e.session.AddEvent(session.Event{
		Type:       session.EventSecurityTriage,
		Tool:       tool,
		Goal:       e.currentGoal,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
}

// logSecuritySupervisor logs supervisor review to session.
func (e *Executor) logSecuritySupervisor(tool, blockID, verdict, reason, model string, latencyMs int64, inputTokens, outputTokens int) {
	if e.session == nil {
		return
	}

	meta := &session.EventMeta{
		SupervisorType: "security",
		CheckName:      "supervisor",
		BlockID:        blockID,
		Verdict:        verdict,
		Model:          model,
		LatencyMs:      latencyMs,
		TokensIn:       inputTokens,
		TokensOut:      outputTokens,
	}

	// Only include LLM content in debug mode (PII protection)
	if e.debug {
		meta.Reason = reason
	}

	e.session.AddEvent(session.Event{
		Type:       session.EventSecuritySupervisor,
		Tool:       tool,
		Goal:       e.currentGoal,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
}

// logSecurityDecision logs final security decision to session.
func (e *Executor) logSecurityDecision(tool, action, reason, trust, checkPath string) {
	if e.session == nil {
		return
	}
	e.session.AddEvent(session.Event{
		Type:      session.EventSecurityDecision,
		Tool:      tool,
		Goal:      e.currentGoal,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			Action:    action,
			Reason:    reason,
			Trust:     trust,
			CheckPath: checkPath,
		},
	})
}

// logSubAgentStart logs the start of a sub-agent execution.
func (e *Executor) logSubAgentStart(name, role, model, task string, inputs map[string]string) {
	if e.session == nil {
		return
	}

	meta := &session.EventMeta{
		SubAgentName:  name,
		SubAgentRole:  role,
		SubAgentModel: model,
	}

	// Only include task and inputs in debug mode (PII protection)
	if e.debug {
		meta.SubAgentTask = task
		meta.SubAgentInputs = inputs
	}

	e.session.AddEvent(session.Event{
		Type:      session.EventSubAgentStart,
		Goal:      e.currentGoal,
		Agent:     name,
		AgentRole: role,
		Timestamp: time.Now(),
		Meta:      meta,
	})
}

// logSubAgentEnd logs the end of a sub-agent execution.
func (e *Executor) logSubAgentEnd(name, role, model, output string, durationMs int64, err error) {
	if e.session == nil {
		return
	}
	errStr := ""
	success := true
	if err != nil {
		errStr = err.Error()
		success = false
	}

	meta := &session.EventMeta{
		SubAgentName:  name,
		SubAgentRole:  role,
		SubAgentModel: model,
	}

	// Only include output in debug mode (PII protection)
	if e.debug {
		meta.SubAgentOutput = truncateForLog(output, 2000)
	}

	e.session.AddEvent(session.Event{
		Type:       session.EventSubAgentEnd,
		Goal:       e.currentGoal,
		Agent:      name,
		AgentRole:  role,
		DurationMs: durationMs,
		Success:    &success,
		Error:      errStr,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
}
