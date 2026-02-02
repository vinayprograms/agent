// Package acp provides Agent Client Protocol support.
// ACP standardizes communication between code editors and coding agents.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// Server implements an ACP agent server.
type Server struct {
	stdin   io.Reader
	stdout  io.Writer
	scanner *bufio.Scanner
	mu      sync.Mutex
	id      atomic.Int64

	// Handlers
	onPrompt func(ctx context.Context, req *PromptRequest) (*PromptResponse, error)

	// State
	session     *Session
	initialized bool
	info        AgentInfo
	caps        AgentCapabilities
}

// AgentInfo describes the agent.
type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// AgentCapabilities advertises agent features.
type AgentCapabilities struct {
	LoadSession       bool                `json:"loadSession,omitempty"`
	PromptCapabilities PromptCapabilities `json:"promptCapabilities,omitempty"`
}

// PromptCapabilities describes what prompts can contain.
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// Session represents an agent session.
type Session struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Request is a JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

// Notification is a JSON-RPC notification.
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// Error is a JSON-RPC error.
type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// InitializeRequest is the initialize request params.
type InitializeRequest struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
	Capabilities    ClientCapabilities `json:"capabilities"`
}

// ClientInfo describes the client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities describes client features.
type ClientCapabilities struct {
	Terminal      bool `json:"terminal,omitempty"`
	ReadTextFile  bool `json:"fs.readTextFile,omitempty"`
	WriteTextFile bool `json:"fs.writeTextFile,omitempty"`
}

// InitializeResponse is the initialize response.
type InitializeResponse struct {
	ProtocolVersion string            `json:"protocolVersion"`
	AgentInfo       AgentInfo         `json:"agentInfo"`
	Capabilities    AgentCapabilities `json:"capabilities"`
}

// NewSessionRequest creates a new session.
type NewSessionRequest struct {
	Metadata map[string]string `json:"metadata,omitempty"`
}

// NewSessionResponse returns the session.
type NewSessionResponse struct {
	Session Session `json:"session"`
}

// PromptRequest is a prompt turn request.
type PromptRequest struct {
	SessionID string        `json:"sessionId"`
	Prompt    []PromptPart  `json:"prompt"`
	Command   *CommandInput `json:"command,omitempty"`
}

// PromptPart is a part of a prompt.
type PromptPart struct {
	Type string `json:"type"` // "text", "image", "audio"
	Text string `json:"text,omitempty"`
	Data string `json:"data,omitempty"` // base64
	Mime string `json:"mimeType,omitempty"`
}

// CommandInput is a slash command.
type CommandInput struct {
	Name  string `json:"name"`
	Input string `json:"input,omitempty"`
}

// PromptResponse is the response to a prompt.
type PromptResponse struct {
	StopReason string `json:"stopReason"` // "endTurn", "cancelled", "error"
}

// SessionNotification notifies about session updates.
type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate is a session state update.
type SessionUpdate struct {
	Type string `json:"type"` // "messageChunk", "toolCall", "planUpdate"
	// For messageChunk
	Role  string `json:"role,omitempty"`
	Chunk string `json:"chunk,omitempty"`
	// For toolCall
	ToolCall *ToolCallUpdate `json:"toolCall,omitempty"`
}

// ToolCallUpdate is a tool call notification.
type ToolCallUpdate struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"` // "running", "completed", "error"
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
}

// NewServer creates a new ACP server.
func NewServer(info AgentInfo, caps AgentCapabilities) *Server {
	return &Server{
		stdin:  os.Stdin,
		stdout: os.Stdout,
		info:   info,
		caps:   caps,
	}
}

// OnPrompt sets the prompt handler.
func (s *Server) OnPrompt(handler func(ctx context.Context, req *PromptRequest) (*PromptResponse, error)) {
	s.onPrompt = handler
}

// Run starts the server loop.
func (s *Server) Run(ctx context.Context) error {
	s.scanner = bufio.NewScanner(s.stdin)

	for s.scanner.Scan() {
		line := s.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error", nil)
			continue
		}

		if err := s.handleRequest(ctx, &req); err != nil {
			s.sendError(req.ID, -32603, err.Error(), nil)
		}
	}

	return s.scanner.Err()
}

func (s *Server) handleRequest(ctx context.Context, req *Request) error {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "session/new":
		return s.handleNewSession(req)
	case "session/prompt":
		return s.handlePrompt(ctx, req)
	case "session/cancel":
		// Handle cancellation
		return s.sendResult(req.ID, map[string]interface{}{})
	default:
		return s.sendError(req.ID, -32601, "Method not found", nil)
	}
}

func (s *Server) handleInitialize(req *Request) error {
	var params InitializeRequest
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.sendError(req.ID, -32602, "Invalid params", nil)
		}
	}

	s.initialized = true

	return s.sendResult(req.ID, InitializeResponse{
		ProtocolVersion: "2025-01-01",
		AgentInfo:       s.info,
		Capabilities:    s.caps,
	})
}

func (s *Server) handleNewSession(req *Request) error {
	var params NewSessionRequest
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}

	s.session = &Session{
		ID:       fmt.Sprintf("session-%d", s.id.Add(1)),
		Metadata: params.Metadata,
	}

	return s.sendResult(req.ID, NewSessionResponse{
		Session: *s.session,
	})
}

func (s *Server) handlePrompt(ctx context.Context, req *Request) error {
	if s.onPrompt == nil {
		return s.sendError(req.ID, -32603, "No prompt handler", nil)
	}

	var params PromptRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.sendError(req.ID, -32602, "Invalid params", nil)
	}

	resp, err := s.onPrompt(ctx, &params)
	if err != nil {
		return s.sendError(req.ID, -32603, err.Error(), nil)
	}

	return s.sendResult(req.ID, resp)
}

// SendMessageChunk sends a message chunk to the client.
func (s *Server) SendMessageChunk(sessionID, role, chunk string) error {
	return s.sendNotification("session/update", SessionNotification{
		SessionID: sessionID,
		Update: SessionUpdate{
			Type:  "messageChunk",
			Role:  role,
			Chunk: chunk,
		},
	})
}

// SendToolCall sends a tool call update to the client.
func (s *Server) SendToolCall(sessionID string, call *ToolCallUpdate) error {
	return s.sendNotification("session/update", SessionNotification{
		SessionID: sessionID,
		Update: SessionUpdate{
			Type:     "toolCall",
			ToolCall: call,
		},
	})
}

func (s *Server) sendResult(id interface{}, result interface{}) error {
	return s.send(Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func (s *Server) sendError(id interface{}, code int, message string, data interface{}) error {
	return s.send(Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message, Data: data},
	})
}

func (s *Server) sendNotification(method string, params interface{}) error {
	return s.send(Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

func (s *Server) send(msg interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(s.stdout, "%s\n", data)
	return err
}
