package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoopExporter(t *testing.T) {
	exp := NewNoopExporter()

	// Should not panic
	exp.LogEvent("test", map[string]interface{}{"key": "value"})
	exp.LogMessage(Message{Content: "test"})

	if err := exp.Flush(); err != nil {
		t.Errorf("Flush() error = %v", err)
	}
	if err := exp.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestFileExporter(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "telemetry.jsonl")

	exp, err := NewFileExporter(path)
	if err != nil {
		t.Fatalf("NewFileExporter() error = %v", err)
	}
	defer exp.Close()

	// Log event
	exp.LogEvent("test_event", map[string]interface{}{"foo": "bar"})

	// Log message
	exp.LogMessage(Message{
		SessionID:    "sess-123",
		WorkflowName: "test",
		Goal:         "analyze",
		Role:         "assistant",
		Content:      "Hello world",
		Tokens:       TokenCount{Input: 100, Output: 50},
		Latency:      time.Second,
		Model:        "test-model",
	})

	exp.Flush()

	// Verify file exists and has content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty file")
	}

	// Should have two lines (event + message)
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("expected 2 lines, got %d", lines)
	}
}

func TestNewExporter(t *testing.T) {
	tests := []struct {
		protocol string
		wantErr  bool
	}{
		{"noop", false},
		{"", false},
		{"unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.protocol, func(t *testing.T) {
			exp, err := NewExporter(tt.protocol, "")
			if (err != nil) != tt.wantErr {
				t.Errorf("NewExporter() error = %v, wantErr %v", err, tt.wantErr)
			}
			if exp != nil {
				exp.Close()
			}
		})
	}
}
