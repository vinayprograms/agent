// Package telemetry provides telemetry export functionality.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// Exporter is the interface for telemetry exporters.
type Exporter interface {
	// LogEvent logs an event with the given name and data.
	LogEvent(name string, data map[string]interface{})
	// LogMessage logs an LLM message.
	LogMessage(msg Message)
	// Flush sends any buffered data.
	Flush() error
	// Close closes the exporter.
	Close() error
}

// Message represents an LLM message for telemetry.
type Message struct {
	SessionID    string                 `json:"session_id"`
	WorkflowName string                 `json:"workflow_name"`
	Goal         string                 `json:"goal"`
	Role         string                 `json:"role"`
	Agent        string                 `json:"agent,omitempty"`
	Content      string                 `json:"content"`
	ToolCalls    []interface{}          `json:"tool_calls,omitempty"`
	Tokens       TokenCount             `json:"tokens"`
	Latency      time.Duration          `json:"latency"`
	Model        string                 `json:"model"`
	Iteration    int                    `json:"iteration,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
	Extra        map[string]interface{} `json:"extra,omitempty"`
}

// TokenCount represents token usage.
type TokenCount struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// Event represents a telemetry event.
type Event struct {
	Name      string                 `json:"name"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// NewExporter creates a new exporter based on protocol.
func NewExporter(protocol, endpoint string) (Exporter, error) {
	switch protocol {
	case "http":
		return NewHTTPExporter(endpoint), nil
	case "file":
		return NewFileExporter(endpoint)
	case "noop", "":
		return NewNoopExporter(), nil
	default:
		return nil, fmt.Errorf("unknown telemetry protocol: %s", protocol)
	}
}

// --- HTTP Exporter ---

// HTTPExporter sends telemetry to an HTTP endpoint.
type HTTPExporter struct {
	endpoint string
	client   *http.Client
	buffer   []interface{}
	mu       sync.Mutex
}

// NewHTTPExporter creates a new HTTP exporter.
func NewHTTPExporter(endpoint string) *HTTPExporter {
	return &HTTPExporter{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		buffer: make([]interface{}, 0, 100),
	}
}

func (e *HTTPExporter) LogEvent(name string, data map[string]interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.buffer = append(e.buffer, Event{
		Name:      name,
		Timestamp: time.Now(),
		Data:      data,
	})
	if len(e.buffer) >= 100 {
		e.flush()
	}
}

func (e *HTTPExporter) LogMessage(msg Message) {
	msg.Timestamp = time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.buffer = append(e.buffer, msg)
	if len(e.buffer) >= 100 {
		e.flush()
	}
}

func (e *HTTPExporter) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.flush()
}

func (e *HTTPExporter) flush() error {
	if len(e.buffer) == 0 {
		return nil
	}

	data, err := json.Marshal(e.buffer)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", e.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telemetry endpoint returned %d", resp.StatusCode)
	}

	e.buffer = e.buffer[:0]
	return nil
}

func (e *HTTPExporter) Close() error {
	return e.Flush()
}

// --- File Exporter ---

// FileExporter writes telemetry to a file.
type FileExporter struct {
	file *os.File
	mu   sync.Mutex
}

// NewFileExporter creates a new file exporter.
func NewFileExporter(path string) (*FileExporter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open telemetry file: %w", err)
	}
	return &FileExporter{file: file}, nil
}

func (e *FileExporter) LogEvent(name string, data map[string]interface{}) {
	event := Event{
		Name:      name,
		Timestamp: time.Now(),
		Data:      data,
	}
	e.write(event)
}

func (e *FileExporter) LogMessage(msg Message) {
	msg.Timestamp = time.Now()
	e.write(msg)
}

func (e *FileExporter) write(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.file.Write(data)
	e.file.Write([]byte("\n"))
}

func (e *FileExporter) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.file.Sync()
}

func (e *FileExporter) Close() error {
	e.Flush()
	return e.file.Close()
}

// --- Noop Exporter ---

// NoopExporter discards all telemetry.
type NoopExporter struct{}

// NewNoopExporter creates a new noop exporter.
func NewNoopExporter() *NoopExporter {
	return &NoopExporter{}
}

func (e *NoopExporter) LogEvent(name string, data map[string]interface{}) {}
func (e *NoopExporter) LogMessage(msg Message)                            {}
func (e *NoopExporter) Flush() error                                      { return nil }
func (e *NoopExporter) Close() error                                      { return nil }
