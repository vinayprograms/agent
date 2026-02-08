// Package llm provides LLM provider interfaces and implementations.
package llm

import (
	"context"
	"fmt"
)

// Message represents an LLM message.
type Message struct {
	Role       string             `json:"role"` // user, assistant, tool, system
	Content    string             `json:"content"`
	ToolCalls  []ToolCallResponse `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"` // For tool result messages
}

// ToolDef represents a tool definition for the LLM.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCallResponse represents a tool call from the LLM.
type ToolCallResponse struct {
	ID   string                 `json:"id"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// ChatRequest represents a chat request to the LLM.
type ChatRequest struct {
	Messages  []Message `json:"messages"`
	Tools     []ToolDef `json:"tools,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

// ChatResponse represents a chat response from the LLM.
type ChatResponse struct {
	Content      string             `json:"content"`
	Thinking     string             `json:"thinking,omitempty"`
	ToolCalls    []ToolCallResponse `json:"tool_calls,omitempty"`
	StopReason   string             `json:"stop_reason"`
	InputTokens  int                `json:"input_tokens"`
	OutputTokens int                `json:"output_tokens"`
	Model        string             `json:"model"`
}

// Provider is the interface for LLM providers.
type Provider interface {
	// Chat sends a chat request and returns the response.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// FantasyConfig holds configuration for the Fantasy adapter.
type FantasyConfig struct {
	Provider  string         `json:"provider"`  // anthropic, openai, google, groq, mistral, openai-compat
	Model     string         `json:"model"`
	APIKey    string         `json:"api_key"`
	MaxTokens int            `json:"max_tokens"`
	BaseURL   string         `json:"base_url"` // Custom API endpoint (for OpenRouter, LiteLLM, Ollama, LMStudio)
	Thinking  ThinkingConfig `json:"thinking"` // Thinking/reasoning configuration
}

// Validate validates the configuration.
func (c *FantasyConfig) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	if c.APIKey == "" {
		return fmt.Errorf("api key is required")
	}
	return nil
}

// ApplyDefaults applies default values.
func (c *FantasyConfig) ApplyDefaults() {
	if c.MaxTokens == 0 {
		c.MaxTokens = 4096
	}
}

// ProviderFactory creates providers based on configuration.
type ProviderFactory interface {
	// GetProvider returns a provider for the given profile name.
	// Empty profile name returns the default provider.
	GetProvider(profile string) (Provider, error)
}

// SingleProviderFactory wraps a single provider (for backward compatibility).
type SingleProviderFactory struct {
	provider Provider
}

// NewSingleProviderFactory creates a factory that always returns the same provider.
func NewSingleProviderFactory(p Provider) *SingleProviderFactory {
	return &SingleProviderFactory{provider: p}
}

// GetProvider returns the single provider regardless of profile.
func (f *SingleProviderFactory) GetProvider(profile string) (Provider, error) {
	return f.provider, nil
}

// --- Mock Provider for Testing ---

// MockProvider is a mock LLM provider for testing.
type MockProvider struct {
	response     string
	toolCalls    []ToolCallResponse
	stopReason   string
	inputTokens  int
	outputTokens int
	lastRequest  *ChatRequest
	err          error
	callCount    int
	
	// ChatFunc can be overridden for custom behavior
	ChatFunc func(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// NewMockProvider creates a new mock provider.
func NewMockProvider() *MockProvider {
	p := &MockProvider{
		stopReason: "end_turn",
	}
	return p
}

// SetResponse sets the response content.
func (p *MockProvider) SetResponse(content string) {
	p.response = content
}

// SetToolCall sets a single tool call response.
func (p *MockProvider) SetToolCall(name string, args map[string]interface{}) {
	p.toolCalls = []ToolCallResponse{
		{ID: "tc-1", Name: name, Args: args},
	}
}

// SetToolCalls sets multiple tool call responses.
func (p *MockProvider) SetToolCalls(calls []ToolCallResponse) {
	p.toolCalls = calls
}

// SetTokenCounts sets the token counts.
func (p *MockProvider) SetTokenCounts(input, output int) {
	p.inputTokens = input
	p.outputTokens = output
}

// SetStopReason sets the stop reason.
func (p *MockProvider) SetStopReason(reason string) {
	p.stopReason = reason
}

// SetError sets an error to return.
func (p *MockProvider) SetError(err error) {
	p.err = err
}

// LastRequest returns the last request.
func (p *MockProvider) LastRequest() *ChatRequest {
	return p.lastRequest
}

// CallCount returns the number of Chat calls made.
func (p *MockProvider) CallCount() int {
	return p.callCount
}

// Reset resets the call count.
func (p *MockProvider) Reset() {
	p.callCount = 0
}

// Chat implements the Provider interface.
func (p *MockProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
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
		return &ChatResponse{
			Content:      p.response,
			StopReason:   p.stopReason,
			InputTokens:  p.inputTokens,
			OutputTokens: p.outputTokens,
		}, nil
	}

	return &ChatResponse{
		Content:      p.response,
		ToolCalls:    p.toolCalls,
		StopReason:   p.stopReason,
		InputTokens:  p.inputTokens,
		OutputTokens: p.outputTokens,
	}, nil
}
