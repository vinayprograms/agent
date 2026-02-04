// Package logging provides structured, standards-compliant logging.
package logging

import (
	"encoding/json"
	"io"
	"os"
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

// Entry represents a structured log entry (RFC 5424 inspired).
type Entry struct {
	Timestamp string                 `json:"timestamp"`          // ISO 8601
	Level     Level                  `json:"level"`              // DEBUG, INFO, WARN, ERROR
	Message   string                 `json:"message"`            // Human-readable message
	Component string                 `json:"component,omitempty"` // e.g., "executor", "tool:bash"
	TraceID   string                 `json:"trace_id,omitempty"` // Request/session correlation
	Fields    map[string]interface{} `json:"fields,omitempty"`   // Additional structured data
}

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

// log writes a structured log entry.
func (l *Logger) log(level Level, msg string, fields ...map[string]interface{}) {
	if levelPriority[level] < levelPriority[l.minLevel] {
		return
	}

	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		Message:   msg,
		Component: l.component,
		TraceID:   l.traceID,
	}

	if len(fields) > 0 && fields[0] != nil {
		entry.Fields = fields[0]
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		// Fallback to simple format if JSON fails
		l.output.Write([]byte(msg + "\n"))
		return
	}
	l.output.Write(append(data, '\n'))
}

// ToolCall logs a tool invocation.
func (l *Logger) ToolCall(tool string, args map[string]interface{}) {
	l.Info("tool_call", map[string]interface{}{
		"tool": tool,
		"args": args,
	})
}

// ToolResult logs a tool result.
func (l *Logger) ToolResult(tool string, durationMs int64, err error) {
	fields := map[string]interface{}{
		"tool":        tool,
		"duration_ms": durationMs,
	}
	if err != nil {
		fields["error"] = err.Error()
		l.Error("tool_error", fields)
	} else {
		l.Info("tool_result", fields)
	}
}

// LLMCall logs an LLM API call.
func (l *Logger) LLMCall(model string, inputTokens, outputTokens int) {
	l.Info("llm_call", map[string]interface{}{
		"model":         model,
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
	})
}

// SecurityWarning logs a security-related warning.
func (l *Logger) SecurityWarning(msg string, fields map[string]interface{}) {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["security"] = true
	l.Warn(msg, fields)
}

// Default is the global default logger.
var Default = New()

// Convenience functions using Default logger.
func Debug(msg string, fields ...map[string]interface{}) { Default.Debug(msg, fields...) }
func Info(msg string, fields ...map[string]interface{})  { Default.Info(msg, fields...) }
func Warn(msg string, fields ...map[string]interface{})  { Default.Warn(msg, fields...) }
func Error(msg string, fields ...map[string]interface{}) { Default.Error(msg, fields...) }
