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

	// Truncate the command for logging: shellguard can be handed arbitrarily
	// long commands (heredocs, base64 blobs) and the command must still be
	// present regardless of debug mode for the decision to be auditable
	// (#1) — but an unbounded command shouldn't blow up Content/meta.source.
	truncatedCmd := truncateForLog(command, 2000)

	content := fmt.Sprintf("[%s] %s: %s", step, verdict, truncatedCmd)
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
			// step distinguishes which stage decided and whether it could
			// decide at all: "deterministic", "path-precheck", "llm", or —
			// when the LLM stage could not produce a verdict — "llm-timeout"
			// / "llm-error", where Pass records the deterministic fallback
			// rather than a judgement the reviewer never made. Auditing a
			// denial means nothing if an outage looks identical to a block.
			CheckName: step,
			Pass:      allowed,
			Action:    action,
			Reason:    reason,
			LatencyMs: durationMs,
			Source:    truncatedCmd,
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
	// A nanosecond timestamp alone collides when parallel tool calls land in
	// the same tick; append a monotonic counter so corr_id is always unique
	// under concurrency (#7).
	corrID := fmt.Sprintf("tool-%d-%d", time.Now().UnixNano(), e.toolCallSeq.Add(1))

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

	// Content policy (#2, #9): a session must be replayable/diagnosable
	// without --debug. Debug mode logs the full tool result (Content +
	// meta.Result); otherwise Content still carries a truncated preview and
	// meta carries the full result's byte size + hash, so a run can be
	// audited for e.g. an oversized web_fetch payload (~70k-token
	// injection, suspected in 37) without needing a --debug rerun.
	preview, size, hash := contentPreview(result)
	content := preview
	success := err == nil

	event := session.Event{
		Type:          session.EventToolResult,
		CorrelationID: corrID,
		Goal:          e.currentGoal,
		Tool:          name,
		Args:          args,
		Content:       content,
		Success:       &success,
		DurationMs:    duration.Milliseconds(),
		Agent:         agentID.Name,
		AgentRole:     agentID.Role,
		Timestamp:     time.Now(),
	}
	// Event.Error does not survive persistence (see EventMeta.Error's doc
	// comment), so a failed tool's error text is duplicated into meta.error
	// here, unconditionally — an error carries no arbitrary tool output, so
	// it is not subject to the same PII rule as Content/meta.result below.
	meta := &session.EventMeta{
		ContentSize: size,
		ContentHash: hash,
	}
	if e.debug {
		content = result
		event.Content = content
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

	// Build meta with model/token info (always logged). StopReason and
	// thinking length are logged unconditionally too (not just -vv/debug)
	// since they're what a headless caller needs to diagnose a
	// stop_reason=="length"-with-empty-content turn (P0 #1) without
	// re-running with --debug.
	meta := &session.EventMeta{
		Model:         resp.Model,
		TokensIn:      resp.InputTokens,
		TokensOut:     resp.OutputTokens,
		StopReason:    resp.StopReason,
		ThinkingChars: len(resp.Thinking),
	}

	// Content policy (#2, #9): a session must be diagnosable and roughly
	// replayable even without --debug, but full LLM content is
	// PII-sensitive. Debug mode logs it in full (Event.Content +
	// meta.Prompt/Response/Thinking); otherwise every assistant event still
	// carries a truncated content preview plus the full response's byte
	// size and hash, so "empty output", "truncated output", and "huge
	// output" are all distinguishable from the JSONL alone.
	preview, size, hash := contentPreview(resp.Content)
	meta.ContentSize = size
	meta.ContentHash = hash
	content := preview
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

// logGoalEnd logs the end of a goal execution, including its explicit
// outcome and reason so a headless caller can tell a budget-exhausted or
// empty-output goal from a genuinely completed one without grepping stderr.
func (e *Executor) logGoalEnd(goalName, output string, outcome GoalOutcome) {
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

	ok := outcome.Outcome == OutcomeOK

	// Reason explains every outcome, including a successful convergence.
	// Error must stay empty on success, or log consumers read a healthy
	// goal as a failed one.
	errText := ""
	if !ok {
		errText = outcome.Reason
	}
	e.session.AddEvent(session.Event{
		Type:      session.EventGoalEnd,
		Goal:      goalName,
		Content:   content,
		Success:   &ok,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			Result:     string(outcome.Outcome),
			Reason:     outcome.Reason,
			Error:      errText,
			Retried:    outcome.Retried,
			Iterations: outcome.Iterations,
		},
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

// logObservation logs the result of an observation-extraction attempt
// (findings/insights/lessons pulled from a completed goal or sub-agent's
// output for semantic memory). Counts are always logged, even on zero
// extractions or failure, so "Observations: enabled" in run.log is backed
// by session evidence either way (#11: the extractor previously ran with
// no corresponding event, so 49 real runs showed zero observation events
// despite the banner). Content is debug-only (PII protection); failErr, if
// set, is a store/extract error and is always logged (needed to diagnose a
// silent no-op without --debug).
func (e *Executor) logObservation(source string, findings, insights, lessons []string, failErr string) {
	if e.session == nil {
		return
	}

	meta := &session.EventMeta{
		ObservationSource:   source,
		ObservationCount:    len(findings) + len(insights) + len(lessons),
		ObservationFindings: len(findings),
		ObservationInsights: len(insights),
		ObservationLessons:  len(lessons),
	}
	if failErr != "" {
		meta.ObservationStoreError = failErr
	}

	var content string
	if e.debug {
		content = truncateForLog(fmt.Sprintf("findings=%v insights=%v lessons=%v", findings, insights, lessons), 2000)
	}

	success := failErr == ""
	e.session.AddEvent(session.Event{
		Type:      session.EventObservation,
		Goal:      e.currentGoal,
		Content:   content,
		Success:   &success,
		Timestamp: time.Now(),
		Meta:      meta,
	})
}

// logSubAgentStart logs the start of a sub-agent execution. model is the
// resolved model name (e.g. "deepseek-v4-pro:cloud") when already known at
// spawn time, empty otherwise — it is not resolved until the first LLM
// call, so logSubAgentEnd is the reliable place to find it (#7).
func (e *Executor) logSubAgentStart(name, role, profile, model, task string, inputs map[string]string) {
	if e.session == nil {
		return
	}

	meta := &session.EventMeta{
		SubAgentName:    name,
		SubAgentRole:    role,
		SubAgentProfile: profile,
		SubAgentModel:   model,
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

// logSubAgentEnd logs the end of a sub-agent execution. model should be the
// resolved model name observed from the sub-agent's own LLM responses
// (falls back to profile when the sub-agent never got a response, e.g. an
// immediate error) — profiles like "reasoning-heavy" are not model names
// (#7).
func (e *Executor) logSubAgentEnd(name, role, profile, model, output string, durationMs int64, err error) {
	if e.session == nil {
		return
	}
	errStr := ""
	success := true
	if err != nil {
		errStr = err.Error()
		success = false
	}
	if model == "" {
		model = profile
	}

	meta := &session.EventMeta{
		SubAgentName:    name,
		SubAgentRole:    role,
		SubAgentProfile: profile,
		SubAgentModel:   model,
	}

	// Event.Error does not survive persistence (see EventMeta.Error's doc
	// comment: the footer-level "error" field of the same JSON name wins
	// over the embedded Event.Error on marshal), so a failed sub-agent's
	// error text must also land in meta.error or it never reaches the
	// JSONL file at all (#8/#12).
	if errStr != "" {
		meta.Error = errStr
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
