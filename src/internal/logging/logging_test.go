package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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

	// Parse the entry
	var entry Entry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse log entry: %v", err)
	}

	if entry.Level != LevelInfo {
		t.Errorf("expected level INFO, got %s", entry.Level)
	}
	if entry.Message != "info message" {
		t.Errorf("expected message 'info message', got %s", entry.Message)
	}
}

func TestLogger_WithComponent(t *testing.T) {
	var buf bytes.Buffer
	logger := New().WithComponent("executor")
	logger.SetOutput(&buf)

	logger.Info("test message")

	var entry Entry
	json.Unmarshal(buf.Bytes(), &entry)

	if entry.Component != "executor" {
		t.Errorf("expected component 'executor', got %s", entry.Component)
	}
}

func TestLogger_WithTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := New().WithTraceID("req-123")
	logger.SetOutput(&buf)

	logger.Info("test message")

	var entry Entry
	json.Unmarshal(buf.Bytes(), &entry)

	if entry.TraceID != "req-123" {
		t.Errorf("expected trace_id 'req-123', got %s", entry.TraceID)
	}
}

func TestLogger_Fields(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)

	logger.Info("tool call", map[string]interface{}{
		"tool": "bash",
		"args": map[string]string{"command": "ls"},
	})

	var entry Entry
	json.Unmarshal(buf.Bytes(), &entry)

	if entry.Fields["tool"] != "bash" {
		t.Errorf("expected tool 'bash', got %v", entry.Fields["tool"])
	}
}

func TestLogger_ToolCall(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)

	logger.ToolCall("read", map[string]interface{}{"path": "/tmp/test"})

	if !strings.Contains(buf.String(), `"tool":"read"`) {
		t.Error("tool call should include tool name")
	}
}

func TestLogger_SecurityWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetOutput(&buf)

	logger.SecurityWarning("MCP policy not configured", nil)

	var entry Entry
	json.Unmarshal(buf.Bytes(), &entry)

	if entry.Level != LevelWarn {
		t.Error("security warning should be WARN level")
	}
	if entry.Fields["security"] != true {
		t.Error("security warning should have security=true field")
	}
}
