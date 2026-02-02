package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AnthropicAdapter implements the Provider interface for Anthropic's Claude.
type AnthropicAdapter struct {
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
	baseURL   string
}

// NewAnthropicAdapter creates a new Anthropic adapter.
func NewAnthropicAdapter(apiKey, model string, maxTokens int) *AnthropicAdapter {
	return &AnthropicAdapter{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{Timeout: 120 * time.Second},
		baseURL:   "https://api.anthropic.com/v1",
	}
}

// anthropicRequest represents an Anthropic API request.
type anthropicRequest struct {
	Model     string            `json:"model"`
	Messages  []anthropicMsg    `json:"messages"`
	MaxTokens int               `json:"max_tokens"`
	Tools     []anthropicTool   `json:"tools,omitempty"`
	System    string            `json:"system,omitempty"`
}

type anthropicMsg struct {
	Role    string        `json:"role"`
	Content interface{}   `json:"content"` // string or []anthropicContent
}

type anthropicContent struct {
	Type        string                 `json:"type"`
	Text        string                 `json:"text,omitempty"`
	ID          string                 `json:"id,omitempty"`
	Name        string                 `json:"name,omitempty"`
	Input       map[string]interface{} `json:"input,omitempty"`
	ToolUseID   string                 `json:"tool_use_id,omitempty"`
	Content     string                 `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// anthropicResponse represents an Anthropic API response.
type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Chat implements the Provider interface.
func (a *AnthropicAdapter) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages
	var msgs []anthropicMsg
	var systemPrompt string

	for _, m := range req.Messages {
		if m.Role == "system" {
			systemPrompt = m.Content
			continue
		}

		msg := anthropicMsg{Role: m.Role}

		// Handle tool result messages
		if m.Role == "tool" {
			msg.Role = "user"
			msg.Content = []anthropicContent{
				{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				},
			}
			msgs = append(msgs, msg)
			continue
		}

		// Handle assistant messages with tool calls
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var content []anthropicContent
			if m.Content != "" {
				content = append(content, anthropicContent{
					Type: "text",
					Text: m.Content,
				})
			}
			for _, tc := range m.ToolCalls {
				content = append(content, anthropicContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: tc.Args,
				})
			}
			msg.Content = content
			msgs = append(msgs, msg)
			continue
		}

		// Regular text message
		msg.Content = m.Content
		msgs = append(msgs, msg)
	}

	// Convert tools
	var tools []anthropicTool
	for _, t := range req.Tools {
		tools = append(tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	maxTokens := a.maxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}

	apiReq := anthropicRequest{
		Model:     a.model,
		Messages:  msgs,
		MaxTokens: maxTokens,
		Tools:     tools,
		System:    systemPrompt,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert response
	result := &ChatResponse{
		StopReason:   apiResp.StopReason,
		InputTokens:  apiResp.Usage.InputTokens,
		OutputTokens: apiResp.Usage.OutputTokens,
		Model:        apiResp.Model,
	}

	for _, c := range apiResp.Content {
		switch c.Type {
		case "text":
			result.Content += c.Text
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:   c.ID,
				Name: c.Name,
				Args: c.Input,
			})
		}
	}

	return result, nil
}

// OpenAIAdapter implements the Provider interface for OpenAI.
type OpenAIAdapter struct {
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
	baseURL   string
}

// NewOpenAIAdapter creates a new OpenAI adapter.
func NewOpenAIAdapter(apiKey, model string, maxTokens int) *OpenAIAdapter {
	return &OpenAIAdapter{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{Timeout: 120 * time.Second},
		baseURL:   "https://api.openai.com/v1",
	}
}

// openaiRequest represents an OpenAI API request.
type openaiRequest struct {
	Model     string        `json:"model"`
	Messages  []openaiMsg   `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Tools     []openaiTool  `json:"tools,omitempty"`
}

type openaiMsg struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Parameters  map[string]interface{} `json:"parameters"`
	} `json:"function"`
}

// openaiResponse represents an OpenAI API response.
type openaiResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openaiToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Chat implements the Provider interface.
func (a *OpenAIAdapter) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages
	var msgs []openaiMsg
	for _, m := range req.Messages {
		msg := openaiMsg{
			Role:    m.Role,
			Content: m.Content,
		}

		// Handle tool result messages
		if m.Role == "tool" {
			msg.ToolCallID = m.ToolCallID
		}

		// Handle assistant messages with tool calls
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				msg.ToolCalls = append(msg.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}

		msgs = append(msgs, msg)
	}

	// Convert tools
	var tools []openaiTool
	for _, t := range req.Tools {
		tool := openaiTool{Type: "function"}
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = t.Parameters
		tools = append(tools, tool)
	}

	maxTokens := a.maxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}

	apiReq := openaiRequest{
		Model:     a.model,
		Messages:  msgs,
		MaxTokens: maxTokens,
		Tools:     tools,
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]

	result := &ChatResponse{
		Content:      choice.Message.Content,
		StopReason:   choice.FinishReason,
		InputTokens:  apiResp.Usage.PromptTokens,
		OutputTokens: apiResp.Usage.CompletionTokens,
		Model:        apiResp.Model,
	}

	// Convert tool calls
	for _, tc := range choice.Message.ToolCalls {
		var args map[string]interface{}
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}

	return result, nil
}

// NewProvider creates a new provider based on the configuration.
func NewProvider(cfg FantasyConfig) (Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()

	switch cfg.Provider {
	case "anthropic":
		return NewAnthropicAdapter(cfg.APIKey, cfg.Model, cfg.MaxTokens), nil
	case "openai":
		return NewOpenAIAdapter(cfg.APIKey, cfg.Model, cfg.MaxTokens), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}
}

// NewFantasyProvider is an alias for NewProvider (for compatibility).
func NewFantasyProvider(cfg FantasyConfig) (Provider, error) {
	return NewProvider(cfg)
}
