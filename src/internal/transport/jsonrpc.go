// Package transport provides JSON-RPC 2.0 stdio transport.
package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Request represents a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

// Error represents a JSON-RPC 2.0 error.
type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard error codes
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// Notification represents a JSON-RPC 2.0 notification (no ID).
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// Handler handles JSON-RPC requests.
type Handler interface {
	Handle(ctx context.Context, method string, params json.RawMessage) (interface{}, error)
}

// HandlerFunc is a function adapter for Handler.
type HandlerFunc func(ctx context.Context, method string, params json.RawMessage) (interface{}, error)

func (f HandlerFunc) Handle(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
	return f(ctx, method, params)
}

// Server is a JSON-RPC 2.0 server over stdio.
type Server struct {
	reader  *bufio.Reader
	writer  io.Writer
	handler Handler
	mu      sync.Mutex

	// NotifyFunc is called to send notifications
	NotifyFunc func(method string, params interface{})
}

// NewServer creates a new JSON-RPC server.
func NewServer(r io.Reader, w io.Writer, handler Handler) *Server {
	s := &Server{
		reader:  bufio.NewReader(r),
		writer:  w,
		handler: handler,
	}
	s.NotifyFunc = s.notify
	return s
}

// Serve reads and handles requests until EOF or error.
func (s *Server) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, ParseError, "Parse error", err.Error())
			continue
		}

		if req.JSONRPC != "2.0" {
			s.sendError(req.ID, InvalidRequest, "Invalid Request", "jsonrpc must be 2.0")
			continue
		}

		// Handle request
		result, err := s.handler.Handle(ctx, req.Method, req.Params)
		if err != nil {
			s.sendError(req.ID, InternalError, "Internal error", err.Error())
			continue
		}

		// Send response (only if ID is present - otherwise it's a notification)
		if req.ID != nil {
			s.sendResult(req.ID, result)
		}
	}
}

// sendResult sends a successful response.
func (s *Server) sendResult(id interface{}, result interface{}) {
	s.send(Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

// sendError sends an error response.
func (s *Server) sendError(id interface{}, code int, message string, data interface{}) {
	s.send(Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
	})
}

// notify sends a notification.
func (s *Server) notify(method string, params interface{}) {
	s.send(Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

// Notify sends a notification to the client.
func (s *Server) Notify(method string, params interface{}) {
	s.NotifyFunc(method, params)
}

// send writes a JSON message to the output.
func (s *Server) send(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	_, err = s.writer.Write(append(data, '\n'))
	return err
}

// --- Run Params and Result ---

// RunParams are the parameters for the "run" method.
type RunParams struct {
	File   string            `json:"file"`
	Inputs map[string]string `json:"inputs,omitempty"`
}

// RunResult is the result of the "run" method.
type RunResult struct {
	SessionID  string            `json:"session_id"`
	Status     string            `json:"status"`
	Outputs    map[string]string `json:"outputs,omitempty"`
	Iterations map[string]int    `json:"iterations,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// Event types for notifications
const (
	EventGoalStarted   = "goal_started"
	EventGoalComplete  = "goal_complete"
	EventToolCall      = "tool_call"
	EventLoopIteration = "loop_iteration"
	EventError         = "error"
)

// GoalStartedParams are params for goal_started event.
type GoalStartedParams struct {
	SessionID string `json:"session_id"`
	Goal      string `json:"goal"`
}

// GoalCompleteParams are params for goal_complete event.
type GoalCompleteParams struct {
	SessionID string `json:"session_id"`
	Goal      string `json:"goal"`
	Output    string `json:"output"`
}

// ToolCallParams are params for tool_call event.
type ToolCallParams struct {
	SessionID string                 `json:"session_id"`
	Goal      string                 `json:"goal"`
	Tool      string                 `json:"tool"`
	Args      map[string]interface{} `json:"args"`
	Result    interface{}            `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
}
