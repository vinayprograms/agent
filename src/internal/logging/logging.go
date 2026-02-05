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
