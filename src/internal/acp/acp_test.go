package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentInfo(t *testing.T) {
	info := AgentInfo{
		Name:    "test-agent",
		Version: "1.0.0",
	}

	if info.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got %q", info.Name)
	}
}

func TestAgentCapabilities(t *testing.T) {
	caps := AgentCapabilities{
		LoadSession: true,
		PromptCapabilities: PromptCapabilities{
			Image: true,
			Audio: false,
		},
	}

	if !caps.LoadSession {
		t.Error("expected LoadSession to be true")
	}
	if !caps.PromptCapabilities.Image {
		t.Error("expected Image to be true")
	}
}

func TestSession(t *testing.T) {
	session := Session{
		ID: "session-123",
		Metadata: map[string]string{
			"workDir": "/project",
		},
	}

	if session.ID != "session-123" {
		t.Errorf("expected ID 'session-123', got %q", session.ID)
	}
}

func TestPromptRequest(t *testing.T) {
	req := PromptRequest{
		SessionID: "session-1",
		Prompt: []PromptPart{
			{Type: "text", Text: "Hello, agent!"},
		},
	}

	if len(req.Prompt) != 1 {
		t.Errorf("expected 1 prompt part, got %d", len(req.Prompt))
	}
	if req.Prompt[0].Text != "Hello, agent!" {
		t.Errorf("unexpected prompt text")
	}
}

func TestPromptRequestWithCommand(t *testing.T) {
	req := PromptRequest{
		SessionID: "session-1",
		Command: &CommandInput{
			Name:  "search",
			Input: "find all TODO comments",
		},
	}

	if req.Command == nil {
		t.Fatal("expected command to be set")
	}
	if req.Command.Name != "search" {
		t.Errorf("expected command 'search', got %q", req.Command.Name)
	}
}

func TestSessionUpdate(t *testing.T) {
	update := SessionUpdate{
		Type:  "messageChunk",
		Role:  "assistant",
		Chunk: "Here's my response...",
	}

	if update.Type != "messageChunk" {
		t.Errorf("expected type 'messageChunk', got %q", update.Type)
	}
}

func TestToolCallUpdate(t *testing.T) {
	update := ToolCallUpdate{
		ID:     "call-1",
		Name:   "read_file",
		Status: "running",
		Input:  "/path/to/file",
	}

	if update.Status != "running" {
		t.Errorf("expected status 'running', got %q", update.Status)
	}
}

func TestNewServer(t *testing.T) {
	info := AgentInfo{Name: "test", Version: "1.0.0"}
	caps := AgentCapabilities{}

	server := NewServer(info, caps)
	if server == nil {
		t.Fatal("expected server to be created")
	}
}

func TestServerInitialize(t *testing.T) {
	info := AgentInfo{Name: "test-agent", Version: "1.0.0"}
	caps := AgentCapabilities{LoadSession: true}

	var output bytes.Buffer
	server := &Server{
		stdin:  strings.NewReader(""),
		stdout: &output,
		info:   info,
		caps:   caps,
	}

	// Simulate initialize request
	req := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	reqBytes, _ := json.Marshal(req)

	// Use strings.NewReader to provide input
	server.stdin = strings.NewReader(string(reqBytes) + "\n")
	server.handleRequest(context.Background(), &req)

	// Check output
	var resp Response
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		// Might have multiple lines, try first line
		lines := strings.Split(output.String(), "\n")
		if len(lines) > 0 {
			json.Unmarshal([]byte(lines[0]), &resp)
		}
	}

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

func TestServerNewSession(t *testing.T) {
	info := AgentInfo{Name: "test-agent", Version: "1.0.0"}
	caps := AgentCapabilities{}

	var output bytes.Buffer
	server := &Server{
		stdin:       strings.NewReader(""),
		stdout:      &output,
		info:        info,
		caps:        caps,
		initialized: true,
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "session/new",
		Params:  json.RawMessage(`{"metadata": {"workDir": "/test"}}`),
	}

	server.handleRequest(context.Background(), &req)

	if server.session == nil {
		t.Error("expected session to be created")
	}
	if server.session.Metadata["workDir"] != "/test" {
		t.Error("expected workDir metadata to be set")
	}
}

func TestErrorCodes(t *testing.T) {
	err := Error{
		Code:    -32601,
		Message: "Method not found",
	}

	if err.Code != -32601 {
		t.Errorf("expected code -32601, got %d", err.Code)
	}
}

func TestInitializeRequest(t *testing.T) {
	req := InitializeRequest{
		ProtocolVersion: "2025-01-01",
		ClientInfo: ClientInfo{
			Name:    "vscode",
			Version: "1.85.0",
		},
		Capabilities: ClientCapabilities{
			Terminal:      true,
			ReadTextFile:  true,
			WriteTextFile: true,
		},
	}

	if req.ProtocolVersion != "2025-01-01" {
		t.Errorf("unexpected protocol version")
	}
	if !req.Capabilities.Terminal {
		t.Error("expected terminal capability")
	}
}

func TestNotificationMarshaling(t *testing.T) {
	notif := Notification{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params: SessionNotification{
			SessionID: "session-1",
			Update: SessionUpdate{
				Type:  "messageChunk",
				Chunk: "Hello",
			},
		},
	}

	data, err := json.Marshal(notif)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if !strings.Contains(string(data), "session/update") {
		t.Error("expected method in JSON")
	}
	if !strings.Contains(string(data), "messageChunk") {
		t.Error("expected update type in JSON")
	}
}
