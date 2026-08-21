// Package llmmock provides an in-memory llm.Model for tests.
//
// agentkit v0.2.x shipped llm.MockProvider; v1.2.0 removed it. This package
// is a verbatim copy of that mock (OLD agentkit llm/provider.go, "Mock Provider
// for Testing" section), retyped against v1.2.0's llm.Model interface so the
// repo's tests can replace llm.NewMockProvider() with llmmock.New() mechanically.
// Behaviour is intentionally unchanged; see the migration ledger (A-C7).
package llmmock

import (
	"context"

	"github.com/vinayprograms/agentkit/llm"
)

var _ llm.Model = (*Model)(nil)

// Model is a mock LLM model for testing.
type Model struct {
	response     string
	toolCalls    []llm.ToolCallResponse
	stopReason   string
	inputTokens  int
	outputTokens int
	lastRequest  *llm.ChatRequest
	err          error
	callCount    int

	// ChatFunc can be overridden for custom behavior
	ChatFunc func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

// New creates a new mock model.
func New() *Model {
	p := &Model{
		stopReason: "end_turn",
	}
	return p
}

// SetResponse sets the response content.
func (p *Model) SetResponse(content string) {
	p.response = content
}

// SetToolCall sets a single tool call response.
func (p *Model) SetToolCall(name string, args map[string]any) {
	p.toolCalls = []llm.ToolCallResponse{
		{ID: "tc-1", Name: name, Args: args},
	}
}

// SetToolCalls sets multiple tool call responses.
func (p *Model) SetToolCalls(calls []llm.ToolCallResponse) {
	p.toolCalls = calls
}

// SetTokenCounts sets the token counts.
func (p *Model) SetTokenCounts(input, output int) {
	p.inputTokens = input
	p.outputTokens = output
}

// SetStopReason sets the stop reason.
func (p *Model) SetStopReason(reason string) {
	p.stopReason = reason
}

// SetError sets an error to return.
func (p *Model) SetError(err error) {
	p.err = err
}

// LastRequest returns the last request.
func (p *Model) LastRequest() *llm.ChatRequest {
	return p.lastRequest
}

// CallCount returns the number of Chat calls made.
func (p *Model) CallCount() int {
	return p.callCount
}

// Reset resets the call count.
func (p *Model) Reset() {
	p.callCount = 0
}

// Chat implements the llm.Model interface.
func (p *Model) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.callCount++
	p.lastRequest = &req

	// Use custom function if set
	if p.ChatFunc != nil {
		return p.ChatFunc(ctx, req)
	}

	if p.err != nil {
		return nil, p.err
	}

	// If toolCalls are set, return them only on first call per goal
	// After tool results come back (detected by tool messages), return content
	hasToolResult := false
	for _, msg := range req.Messages {
		if msg.Role == "tool" {
			hasToolResult = true
			break
		}
	}

	if hasToolResult {
		// Tool results received, complete the goal
		return &llm.ChatResponse{
			Content:      p.response,
			StopReason:   p.stopReason,
			InputTokens:  p.inputTokens,
			OutputTokens: p.outputTokens,
		}, nil
	}

	return &llm.ChatResponse{
		Content:      p.response,
		ToolCalls:    p.toolCalls,
		StopReason:   p.stopReason,
		InputTokens:  p.inputTokens,
		OutputTokens: p.outputTokens,
	}, nil
}
