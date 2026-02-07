package logging

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestLogger_Levels(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)
	logger.SetLevel(LevelInfo)

	// Debug should be filtered
	logger.Debug("debug message")
	if buf.Len() > 0 {
		t.Error("debug message should be filtered at INFO level")
	}

	// Info should pass
	logger.Info("info message")
	if buf.Len() == 0 {
		t.Error("info message should be logged")
	}

	output := buf.String()
	if !strings.Contains(output, "INFO") {
		t.Error("log should contain INFO level")
	}
	if !strings.Contains(output, "info message") {
		t.Error("log should contain the message")
	}
}

func TestLogger_WithComponent(t *testing.T) {
	var buf bytes.Buffer
	logger := New().WithComponent("executor")
	logger.SetOutput(&buf)

	logger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, "[executor]") {
		t.Errorf("expected component 'executor' in log, got: %s", output)
	}
}

func TestLogger_WithTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := New().WithTraceID("req-123")
	logger.SetOutput(&buf)

	logger.Info("test message")

	// TraceID is stored but not shown in simple format
	// Just ensure logging works
	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Error("log should contain the message")
	}
}

func TestLogger_Fields(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)

	logger.Info("tool call", map[string]interface{}{
		"tool": "bash",
	})

	output := buf.String()
	if !strings.Contains(output, "tool=bash") {
		t.Errorf("expected field 'tool=bash' in log, got: %s", output)
	}
}

func TestLogger_ToolCall(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)
	logger.SetLevel(LevelDebug) // ToolCall logs at Debug level

	logger.ToolCall("read", map[string]interface{}{"path": "/tmp/test"})

	output := buf.String()
	if !strings.Contains(output, "tool=read") {
		t.Errorf("tool call should include tool name, got: %s", output)
	}
}

func TestLogger_SecurityWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)

	logger.SecurityWarning("MCP policy not configured", nil)

	output := buf.String()
	if !strings.Contains(output, "WARN") {
		t.Error("security warning should be WARN level")
	}
	if !strings.Contains(output, "security=true") {
		t.Error("security warning should have security=true field")
	}
}

func TestLogger_Format(t *testing.T) {
	var buf bytes.Buffer
	logger := New().WithComponent("test")
	logger.SetOutput(&buf)

	logger.Info("hello world", map[string]interface{}{"key": "value"})

	output := buf.String()
	// Format: LEVEL TIMESTAMP [component] message key=value
	// Example: INFO  2026-02-05T04:00:00.000Z [test] hello world key=value
	if !strings.HasPrefix(output, "INFO ") {
		t.Errorf("expected line to start with 'INFO ', got: %s", output)
	}
	if !strings.Contains(output, "[test]") {
		t.Errorf("expected component [test], got: %s", output)
	}
	if !strings.Contains(output, "hello world") {
		t.Errorf("expected message, got: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected key=value, got: %s", output)
	}
}

func TestLogger_ExecutionTiming(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)

	logger.ExecutionStart("test-workflow")
	time.Sleep(10 * time.Millisecond)
	logger.ExecutionComplete("test-workflow", 10*time.Millisecond, "complete")

	output := buf.String()
	if !strings.Contains(output, "execution_start") {
		t.Error("expected execution_start log")
	}
	if !strings.Contains(output, "execution_complete") {
		t.Error("expected execution_complete log")
	}
	if !strings.Contains(output, "duration=") {
		t.Error("expected duration in log")
	}
}
