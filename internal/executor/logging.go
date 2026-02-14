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

	// Convert result to string for content
	var content string
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

	// Build prompt from messages
	var promptParts []string
	for _, msg := range messages {
		promptParts = append(promptParts, fmt.Sprintf("[%s] %s", msg.Role, truncateForLog(msg.Content, 500)))
	}

	e.session.Events = append(e.session.Events, session.Event{
		Type:       eventType,
		Goal:       e.currentGoal,
		Content:    truncateForLog(resp.Content, 1000),
		DurationMs: duration.Milliseconds(),
		Agent:      agentID.Name,
		AgentRole:  agentID.Role,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Model:     resp.Model,
			TokensIn:  resp.InputTokens,
			TokensOut: resp.OutputTokens,
			Prompt:    truncateForLog(fmt.Sprintf("%v", promptParts), 2000),
			Response:  truncateForLog(resp.Content, 2000),
			Thinking:  truncateForLog(resp.Thinking, 2000),
		},
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

	// Truncate long outputs
	truncatedOutput := output
	const maxLen = 2000
	if len(output) > maxLen {
		truncatedOutput = output[:maxLen] + "... (truncated)"
	}

	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventGoalEnd,
		Goal:      goalName,
		Content:   truncatedOutput,
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// logPhaseCommit logs the COMMIT phase of goal execution.
func (e *Executor) logPhaseCommit(goal, commitment, confidence string, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseCommit,
		Goal:       goal,
		Content:    commitment,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:      "COMMIT",
			Commitment: commitment,
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
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseExecute,
		Goal:       goal,
		Content:    truncateForLog(result, 1000),
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:  "EXECUTE",
			Result: truncateForLog(result, 2000),
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
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseSupervise,
		Goal:       goal,
		Step:       step,
		Content:    fmt.Sprintf("Verdict: %s", verdict),
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:          "SUPERVISE",
			SupervisorType: supervisorType,
			Verdict:        verdict,
			Guidance:       guidance,
			HumanRequired:  humanRequired,
			Model:          model,
			Prompt:         prompt,
			Response:       response,
			Thinking:       thinking,
		},
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
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSecurityBlock,
		Goal:      e.currentGoal,
		Content:   truncateForLog(xmlBlock, 500),
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
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSecurityTriage,
		Tool:       tool,
		Goal:       e.currentGoal,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			CheckName:  "triage",
			BlockID:    blockID,
			Suspicious: suspicious,
			Model:      model,
			LatencyMs:  latencyMs,
			TokensIn:   inputTokens,
			TokensOut:  outputTokens,
			Prompt:     prompt,
			Response:   response,
			Thinking:   thinking,
			SkipReason: skipReason,
		},
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
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSecuritySupervisor,
		Tool:       tool,
		Goal:       e.currentGoal,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			SupervisorType: "security",
			CheckName:      "supervisor",
			BlockID:        blockID,
			Verdict:        verdict,
			Reason:         reason,
			Model:          model,
			LatencyMs:      latencyMs,
			TokensIn:       inputTokens,
			TokensOut:      outputTokens,
			Prompt:         prompt,
			Response:       response,
			Thinking:       thinking,
		},
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
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSubAgentStart,
		Goal:      e.currentGoal,
		Agent:     name,
		AgentRole: role,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			SubAgentName:   name,
			SubAgentRole:   role,
			SubAgentModel:  model,
			SubAgentTask:   task,
			SubAgentInputs: inputs,
		},
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
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSubAgentEnd,
		Goal:       e.currentGoal,
		Agent:      name,
		AgentRole:  role,
		DurationMs: durationMs,
		Success:    &success,
		Error:      errStr,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			SubAgentName:   name,
			SubAgentRole:   role,
			SubAgentModel:  model,
			SubAgentOutput: truncateForLog(output, 2000),
		},
	})
	e.sessionManager.Update(e.session)
}
