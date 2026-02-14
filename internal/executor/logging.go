// Session event logging functions for the executor.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/security"
)

// logEvent logs a generic event to the session.
func (e *Executor) logEvent(eventType, content string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      eventType,
		Goal:      e.currentGoal,
		Content:   content,
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// LogBashSecurity logs a bash security decision to the session.
// This is called by the bash security checker callback.
func (e *Executor) LogBashSecurity(command, step string, allowed bool, reason string, durationMs int64, inputTokens, outputTokens int) {
	if e.session == nil || e.sessionManager == nil {
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

	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)

	// Also log to structured logger
	e.logger.Debug("bash security check", map[string]interface{}{
		"step":    step,
		"allowed": allowed,
		"command": command,
		"reason":  reason,
	})
}

// logToolCall logs a tool call event to the session.
// Returns a correlation ID that should be passed to logToolResult.
func (e *Executor) logToolCall(ctx context.Context, name string, args map[string]interface{}) string {
	corrID := fmt.Sprintf("tool-%d", time.Now().UnixNano())

	if e.session == nil || e.sessionManager == nil {
		return corrID
	}

	// Get agent identity from context (thread-safe for parallel execution)
	agentID := getAgentIdentity(ctx)

	e.session.Events = append(e.session.Events, session.Event{
		Type:          session.EventToolCall,
		CorrelationID: corrID,
		Goal:          e.currentGoal,
		Tool:          name,
		Args:          args,
		Agent:         agentID.Name,
		AgentRole:     agentID.Role,
		Timestamp:     time.Now(),
	})
	e.sessionManager.Update(e.session)
	return corrID
}

// logToolResult logs a tool result event to the session.
func (e *Executor) logToolResult(ctx context.Context, name string, args map[string]interface{}, corrID string, result interface{}, err error, duration time.Duration) {
	// Structured logging to stdout
	e.logger.ToolResult(name, duration, err)

	if e.session == nil || e.sessionManager == nil {
		return
	}

	// Get agent identity from context (thread-safe for parallel execution)
	agentID := getAgentIdentity(ctx)

	// Only include tool output in debug mode (PII protection)
	var content string
	if e.debug {
		switch v := result.(type) {
		case string:
			content = v
		case []byte:
			content = string(v)
		default:
			if b, err := json.Marshal(result); err == nil {
				content = string(b)
			} else {
				content = fmt.Sprintf("%v", result)
			}
		}
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
	if err != nil {
		event.Error = err.Error()
	}
	e.session.Events = append(e.session.Events, event)
	e.sessionManager.Update(e.session)
}

// logLLMCall logs an LLM call with full prompt/response for forensics.
func (e *Executor) logLLMCall(ctx context.Context, eventType string, messages []llm.Message, resp *llm.ChatResponse, duration time.Duration) {
	if e.session == nil || e.sessionManager == nil {
		return
	}

	agentID := getAgentIdentity(ctx)

	// Build meta with model/token info (always logged)
	meta := &session.EventMeta{
		Model:     resp.Model,
		TokensIn:  resp.InputTokens,
		TokensOut: resp.OutputTokens,
	}

	// Only log content in debug mode (PII protection)
	var content string
	if e.debug {
		var promptParts []string
		for _, msg := range messages {
			promptParts = append(promptParts, fmt.Sprintf("[%s] %s", msg.Role, truncateForLog(msg.Content, 500)))
		}
		content = truncateForLog(resp.Content, 1000)
		meta.Prompt = truncateForLog(fmt.Sprintf("%v", promptParts), 2000)
		meta.Response = truncateForLog(resp.Content, 2000)
		meta.Thinking = truncateForLog(resp.Thinking, 2000)
	}

	e.session.Events = append(e.session.Events, session.Event{
		Type:       eventType,
		Goal:       e.currentGoal,
		Content:    content,
		DurationMs: duration.Milliseconds(),
		Agent:      agentID.Name,
		AgentRole:  agentID.Role,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
	e.sessionManager.Update(e.session)
}

// logGoalStart logs the start of a goal execution.
func (e *Executor) logGoalStart(goalName string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.currentGoal = goalName
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventGoalStart,
		Goal:      goalName,
		Content:   fmt.Sprintf("Starting goal: %s", goalName),
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// logGoalEnd logs the end of a goal execution.
func (e *Executor) logGoalEnd(goalName, output string) {
	if e.session == nil || e.sessionManager == nil {
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

	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventGoalEnd,
		Goal:      goalName,
		Content:   content,
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// logPhaseCommit logs the COMMIT phase of goal execution.
func (e *Executor) logPhaseCommit(goal, commitment, confidence string, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}

	// Only include commitment content in debug mode (PII protection)
	var content, commitmentMeta string
	if e.debug {
		content = commitment
		commitmentMeta = commitment
	}

	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)
}

// logPhaseExecute logs the EXECUTE phase of goal execution.
func (e *Executor) logPhaseExecute(goal, result string, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}

	// Only include result content in debug mode (PII protection)
	var content, resultMeta string
	if e.debug {
		content = truncateForLog(result, 1000)
		resultMeta = truncateForLog(result, 2000)
	}

	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)
}

// logPhaseReconcile logs the RECONCILE phase of goal execution.
func (e *Executor) logPhaseReconcile(goal, step string, triggers []string, escalate bool, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)
}

// logPhaseSupervise logs the SUPERVISE phase of goal execution.
func (e *Executor) logPhaseSupervise(goal, step, verdict, guidance string, humanRequired bool, durationMs int64) {
	e.logPhaseSuperviseWithDetails(goal, step, verdict, guidance, humanRequired, durationMs, "", "", "", "", "")
}

// logPhaseSuperviseWithDetails logs supervisor review with full LLM details.
func (e *Executor) logPhaseSuperviseWithDetails(goal, step, verdict, guidance string, humanRequired bool, durationMs int64, supervisorType, model, prompt, response, thinking string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}

	meta := &session.EventMeta{
		Phase:          "SUPERVISE",
		SupervisorType: supervisorType,
		Verdict:        verdict,
		HumanRequired:  humanRequired,
		Model:          model,
	}

	// Only include LLM content in debug mode (PII protection)
	if e.debug {
		meta.Guidance = guidance
		meta.Prompt = prompt
		meta.Response = response
		meta.Thinking = thinking
	}

	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseSupervise,
		Goal:       goal,
		Step:       step,
		Content:    fmt.Sprintf("Verdict: %s", verdict),
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
	e.sessionManager.Update(e.session)
}

// logCheckpoint logs a checkpoint creation.
func (e *Executor) logCheckpoint(checkpointType, goal, step, checkpointID string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)
}

// logSecurityBlock logs when a content block is registered for security tracking.
func (e *Executor) logSecurityBlock(blockID, trust, blockType, source, xmlBlock string, entropy float64) {
	e.logSecurityBlockWithTaint(blockID, trust, blockType, source, xmlBlock, entropy, nil)
}

// logSecurityBlockWithTaint logs a content block with taint lineage.
func (e *Executor) logSecurityBlockWithTaint(blockID, trust, blockType, source, xmlBlock string, entropy float64, taintedBy []string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}

	// Only include XML content in debug mode (PII protection)
	var content string
	if e.debug {
		content = truncateForLog(xmlBlock, 500)
	}

	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)
}

// logSecurityStatic logs a static security check result.
func (e *Executor) logSecurityStatic(tool, blockID string, relatedBlockIDs []string, pass bool, flags []string, skipReason string, taintLineage []*security.TaintLineageNode) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
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
			TaintLineage:  convertTaintLineage(taintLineage),
		},
	})
	e.sessionManager.Update(e.session)
}

// convertTaintLineage converts security taint lineage to session format.
func convertTaintLineage(nodes []*security.TaintLineageNode) []session.TaintNode {
	if len(nodes) == 0 {
		return nil
	}
	result := make([]session.TaintNode, len(nodes))
	for i, n := range nodes {
		result[i] = convertTaintNode(n)
	}
	return result
}

// convertTaintNode converts a single taint node.
func convertTaintNode(n *security.TaintLineageNode) session.TaintNode {
	node := session.TaintNode{
		BlockID: n.BlockID,
		Trust:   string(n.Trust),
		Source:  n.Source,
	}
	if len(n.TaintedBy) > 0 {
		node.TaintedBy = make([]session.TaintNode, len(n.TaintedBy))
		for i, child := range n.TaintedBy {
			node.TaintedBy[i] = convertTaintNode(child)
		}
	}
	return node
}

// logSecurityTriage logs LLM triage check to session.
func (e *Executor) logSecurityTriage(tool, blockID string, suspicious bool, model string, latencyMs int64, inputTokens, outputTokens int, skipReason string) {
	e.logSecurityTriageWithDetails(tool, blockID, suspicious, model, latencyMs, inputTokens, outputTokens, "", "", "", skipReason)
}

// logSecurityTriageWithDetails logs LLM triage with full details.
func (e *Executor) logSecurityTriageWithDetails(tool, blockID string, suspicious bool, model string, latencyMs int64, inputTokens, outputTokens int, prompt, response, thinking, skipReason string) {
	if e.session == nil || e.sessionManager == nil {
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

	// Only include LLM content in debug mode (PII protection)
	if e.debug {
		meta.Prompt = prompt
		meta.Response = response
		meta.Thinking = thinking
	}

	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSecurityTriage,
		Tool:       tool,
		Goal:       e.currentGoal,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
	e.sessionManager.Update(e.session)
}

// logSecuritySupervisor logs supervisor review to session.
func (e *Executor) logSecuritySupervisor(tool, blockID, verdict, reason, model string, latencyMs int64, inputTokens, outputTokens int) {
	e.logSecuritySupervisorWithDetails(tool, blockID, verdict, reason, model, latencyMs, inputTokens, outputTokens, "", "", "")
}

// logSecuritySupervisorWithDetails logs supervisor review with full LLM details.
func (e *Executor) logSecuritySupervisorWithDetails(tool, blockID, verdict, reason, model string, latencyMs int64, inputTokens, outputTokens int, prompt, response, thinking string) {
	if e.session == nil || e.sessionManager == nil {
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
		meta.Prompt = prompt
		meta.Response = response
		meta.Thinking = thinking
	}

	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSecuritySupervisor,
		Tool:       tool,
		Goal:       e.currentGoal,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta:       meta,
	})
	e.sessionManager.Update(e.session)
}

// logSecurityDecision logs final security decision to session.
func (e *Executor) logSecurityDecision(tool, action, reason, trust, checkPath string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)
}

// logSubAgentStart logs the start of a sub-agent execution.
func (e *Executor) logSubAgentStart(name, role, model, task string, inputs map[string]string) {
	if e.session == nil || e.sessionManager == nil {
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

	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSubAgentStart,
		Goal:      e.currentGoal,
		Agent:     name,
		AgentRole: role,
		Timestamp: time.Now(),
		Meta:      meta,
	})
	e.sessionManager.Update(e.session)
}

// logSubAgentEnd logs the end of a sub-agent execution.
func (e *Executor) logSubAgentEnd(name, role, model, output string, durationMs int64, err error) {
	if e.session == nil || e.sessionManager == nil {
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

	e.session.Events = append(e.session.Events, session.Event{
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
	e.sessionManager.Update(e.session)
}
