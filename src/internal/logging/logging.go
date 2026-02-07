// Package logging provides structured, standards-compliant logging.
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Level represents log severity.
type Level string

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

// Logger provides structured logging to stdout.
type Logger struct {
	mu        sync.Mutex
	output    io.Writer
	minLevel  Level
	component string
	traceID   string
}

// levelPriority maps levels to numeric priority for filtering.
var levelPriority = map[Level]int{
	LevelDebug: 0,
	LevelInfo:  1,
	LevelWarn:  2,
	LevelError: 3,
}

// New creates a new Logger.
func New() *Logger {
	return &Logger{
		output:   os.Stdout,
		minLevel: LevelInfo,
	}
}

// WithComponent returns a new logger with the given component name.
func (l *Logger) WithComponent(component string) *Logger {
	return &Logger{
		output:    l.output,
		minLevel:  l.minLevel,
		component: component,
		traceID:   l.traceID,
	}
}

// WithTraceID returns a new logger with the given trace ID.
func (l *Logger) WithTraceID(traceID string) *Logger {
	return &Logger{
		output:    l.output,
		minLevel:  l.minLevel,
		component: l.component,
		traceID:   traceID,
	}
}

// SetLevel sets the minimum log level.
func (l *Logger) SetLevel(level Level) {
	l.minLevel = level
}

// SetOutput sets the output writer (default: stdout).
func (l *Logger) SetOutput(w io.Writer) {
	l.output = w
}

// Debug logs a debug message.
func (l *Logger) Debug(msg string, fields ...map[string]interface{}) {
	l.log(LevelDebug, msg, fields...)
}

// Info logs an info message.
func (l *Logger) Info(msg string, fields ...map[string]interface{}) {
	l.log(LevelInfo, msg, fields...)
}

// Warn logs a warning message.
func (l *Logger) Warn(msg string, fields ...map[string]interface{}) {
	l.log(LevelWarn, msg, fields...)
}

// Error logs an error message.
func (l *Logger) Error(msg string, fields ...map[string]interface{}) {
	l.log(LevelError, msg, fields...)
}

// formatFields formats a map of fields as key=value pairs.
func formatFields(fields map[string]interface{}) string {
	if len(fields) == 0 {
		return ""
	}
	var parts []string
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return " " + strings.Join(parts, " ")
}

// log writes a log entry in traditional format: LEVEL TIMESTAMP [component] message key=value ...
func (l *Logger) log(level Level, msg string, fields ...map[string]interface{}) {
	if levelPriority[level] < levelPriority[l.minLevel] {
		return
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	var fieldStr string
	if len(fields) > 0 && fields[0] != nil {
		fieldStr = formatFields(fields[0])
	}

	var line string
	if l.component != "" {
		line = fmt.Sprintf("%-5s %s [%s] %s%s\n", level, timestamp, l.component, msg, fieldStr)
	} else {
		line = fmt.Sprintf("%-5s %s %s%s\n", level, timestamp, msg, fieldStr)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.output.Write([]byte(line))
}

// ToolCall logs a tool invocation.
func (l *Logger) ToolCall(tool string, args map[string]interface{}) {
	// Don't log args to avoid PII - just log tool name
	l.Info("tool_call", map[string]interface{}{
		"tool": tool,
	})
}

// ToolResult logs a tool result.
func (l *Logger) ToolResult(tool string, duration time.Duration, err error) {
	fields := map[string]interface{}{
		"tool":     tool,
		"duration": duration.String(),
	}
	if err != nil {
		fields["error"] = err.Error()
		l.Error("tool_error", fields)
	} else {
		l.Debug("tool_result", fields)
	}
}

// SecurityWarning logs a security-related warning.
func (l *Logger) SecurityWarning(msg string, fields map[string]interface{}) {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["security"] = true
	l.Warn(msg, fields)
}

// GoalStart logs the start of a goal execution.
func (l *Logger) GoalStart(name string) {
	l.Info("goal_start", map[string]interface{}{
		"goal": name,
	})
}

// GoalComplete logs the completion of a goal.
func (l *Logger) GoalComplete(name string, duration time.Duration) {
	l.Info("goal_complete", map[string]interface{}{
		"goal":     name,
		"duration": duration.String(),
	})
}

// ExecutionStart logs the start of workflow execution.
func (l *Logger) ExecutionStart(workflow string) {
	l.Info("execution_start", map[string]interface{}{
		"workflow": workflow,
	})
}

// ExecutionComplete logs the completion of workflow execution.
func (l *Logger) ExecutionComplete(workflow string, duration time.Duration, status string) {
	l.Info("execution_complete", map[string]interface{}{
		"workflow": workflow,
		"duration": duration.String(),
		"status":   status,
	})
}

// --- Forensic Logging for Supervision ---

// PhaseStart logs the start of an execution phase (COMMIT, EXECUTE, RECONCILE, SUPERVISE).
func (l *Logger) PhaseStart(phase, goal, step string) {
	l.Info("phase_start", map[string]interface{}{
		"phase": phase,
		"goal":  goal,
		"step":  step,
	})
}

// PhaseComplete logs the completion of an execution phase.
func (l *Logger) PhaseComplete(phase, goal, step string, duration time.Duration, result string) {
	l.Info("phase_complete", map[string]interface{}{
		"phase":    phase,
		"goal":     goal,
		"step":     step,
		"duration": duration.String(),
		"result":   result,
	})
}

// CommitPhase logs the COMMIT phase details.
func (l *Logger) CommitPhase(goal, step string, commitment string) {
	l.Info("commit_phase", map[string]interface{}{
		"phase":      "COMMIT",
		"goal":       goal,
		"step":       step,
		"commitment": commitment,
	})
}

// ReconcilePhase logs the RECONCILE phase details.
func (l *Logger) ReconcilePhase(goal, step string, triggers []string, escalate bool) {
	l.Info("reconcile_phase", map[string]interface{}{
		"phase":    "RECONCILE",
		"goal":     goal,
		"step":     step,
		"triggers": strings.Join(triggers, ","),
		"escalate": escalate,
	})
}

// SupervisePhase logs the SUPERVISE phase details.
func (l *Logger) SupervisePhase(goal, step string, verdict, reason string) {
	l.Info("supervise_phase", map[string]interface{}{
		"phase":   "SUPERVISE",
		"goal":    goal,
		"step":    step,
		"verdict": verdict,
		"reason":  reason,
	})
}

// SupervisorVerdict logs supervisor decisions for forensic analysis.
func (l *Logger) SupervisorVerdict(goal, step, verdict, guidance string, humanRequired bool) {
	l.Info("supervisor_verdict", map[string]interface{}{
		"goal":           goal,
		"step":           step,
		"verdict":        verdict,
		"guidance":       guidance,
		"human_required": humanRequired,
	})
}

// --- Forensic Logging for Security ---

// SecurityBlockAdded logs when untrusted content is registered.
func (l *Logger) SecurityBlockAdded(blockID, trust, blockType, source string, entropy float64) {
	l.Info("security_block_added", map[string]interface{}{
		"block_id":   blockID,
		"trust":      trust,
		"type":       blockType,
		"source":     source,
		"entropy":    fmt.Sprintf("%.2f", entropy),
		"security":   true,
	})
}

// SecurityTier1 logs Tier 1 deterministic check results.
func (l *Logger) SecurityTier1(blockID string, pass bool, flags []string) {
	l.Info("security_tier1", map[string]interface{}{
		"block_id": blockID,
		"pass":     pass,
		"flags":    strings.Join(flags, ","),
		"security": true,
	})
}

// SecurityTier2 logs Tier 2 triage results.
func (l *Logger) SecurityTier2(blockID string, escalate bool, confidence string, model string, latencyMs int64) {
	l.Info("security_tier2", map[string]interface{}{
		"block_id":   blockID,
		"escalate":   escalate,
		"confidence": confidence,
		"model":      model,
		"latency_ms": latencyMs,
		"security":   true,
	})
}

// SecurityTier3 logs Tier 3 supervisor verdict.
func (l *Logger) SecurityTier3(blockID string, verdict, reason, model string, latencyMs int64) {
	l.Info("security_tier3", map[string]interface{}{
		"block_id":   blockID,
		"verdict":    verdict,
		"reason":     reason,
		"model":      model,
		"latency_ms": latencyMs,
		"security":   true,
	})
}

// SecurityDecision logs a final security decision.
func (l *Logger) SecurityDecision(tool, action, reason string, trust string, tiers string) {
	l.Info("security_decision", map[string]interface{}{
		"tool":     tool,
		"action":   action,
		"reason":   reason,
		"trust":    trust,
		"tiers":    tiers,
		"security": true,
	})
}

// SecurityDeny logs when a tool call is denied.
func (l *Logger) SecurityDeny(tool, reason string, tier int) {
	l.Warn("security_deny", map[string]interface{}{
		"tool":     tool,
		"reason":   reason,
		"tier":     tier,
		"security": true,
	})
}

// CheckpointSaved logs when a checkpoint is saved.
func (l *Logger) CheckpointSaved(checkpointType, goal, step, checkpointID string) {
	l.Debug("checkpoint_saved", map[string]interface{}{
		"type":          checkpointType,
		"goal":          goal,
		"step":          step,
		"checkpoint_id": checkpointID,
	})
}
