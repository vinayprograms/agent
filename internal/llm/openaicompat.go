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

// OpenAICompatProvider implements the Provider interface for OpenAI-compatible APIs.
// This includes Groq, Mistral, LiteLLM, OpenRouter, local Ollama, LMStudio, etc.
type OpenAICompatProvider struct {
	apiKey       string
	baseURL      string
	model        string
	maxTokens    int
	providerName string
	thinking     ThinkingConfig
	retry        RetryConfig
	client       *http.Client
}

// OpenAICompatConfig holds configuration for OpenAI-compatible providers.
type OpenAICompatConfig struct {
	APIKey       string
	BaseURL      string
	Model        string
	MaxTokens    int
	ProviderName string // For logging/identification
	Thinking     ThinkingConfig
	Retry        RetryConfig
}

// NewOpenAICompatProvider creates a new OpenAI-compatible provider.
func NewOpenAICompatProvider(cfg OpenAICompatConfig) (*OpenAICompatProvider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required for openai-compatible provider")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if cfg.MaxTokens == 0 {
		return nil, fmt.Errorf("max_tokens is required")
	}

	return &OpenAICompatProvider{
		apiKey:       cfg.APIKey,
		baseURL:      cfg.BaseURL,
		model:        cfg.Model,
		maxTokens:    cfg.MaxTokens,
		providerName: cfg.ProviderName,
		thinking:     cfg.Thinking,
		retry:        cfg.Retry,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// getRetryConfig returns effective retry settings with defaults.
func (p *OpenAICompatProvider) getRetryConfig() (maxRetries int, initBackoff, maxBackoff time.Duration) {
	maxRetries = p.retry.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	initBackoff = p.retry.InitBackoff
	if initBackoff <= 0 {
		initBackoff = defaultInitBackoff
	}
	maxBackoff = p.retry.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	return
}

// OpenAI-compatible request/response types

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string             `json:"type"`
	Function oaiToolDefinition  `json:"function"`
}

type oaiToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

type oaiResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int        `json:"index"`
		Message      oaiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// Chat implements the Provider interface.
func (p *OpenAICompatProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to OpenAI format
	messages := make([]oaiMessage, 0, len(req.Messages))

	for _, m := range req.Messages {
		msg := oaiMessage{
			Role:    m.Role,
			Content: m.Content,
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				msg.ToolCalls = append(msg.ToolCalls, oaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: oaiFunction{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}

		if m.Role == "tool" {
			msg.ToolCallID = m.ToolCallID
		}

		messages = append(messages, msg)
	}

	// Convert tools
	tools := make([]oaiTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, oaiTool{
			Type: "function",
			Function: oaiToolDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	maxTokens := p.maxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}

	oaiReq := oaiRequest{
		Model:     p.model,
		Messages:  messages,
		MaxTokens: maxTokens,
	}

	if len(tools) > 0 {
		oaiReq.Tools = tools
	}

	// Make request with retry
	maxRetries, initBackoff, maxBackoff := p.getRetryConfig()
	var resp *oaiResponse
	var err error
	backoff := initBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = p.doRequest(ctx, oaiReq)
		if err == nil {
			break
		}

		if isBillingError(err) {
			return nil, fmt.Errorf("billing/payment error (fatal): %w", err)
		}

		if !isRetryableError(err) {
			return nil, fmt.Errorf("%s request failed: %w", p.providerName, err)
		}

		if attempt == maxRetries {
			return nil, fmt.Errorf("%s request failed after %d retries: %w", p.providerName, maxRetries, err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		backoff = time.Duration(float64(backoff) * backoffFactor)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	// Convert response
	result := &ChatResponse{
		Model:        resp.Model,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		result.Content = choice.Message.Content
		result.StopReason = choice.FinishReason

		// Extract tool calls
		for _, tc := range choice.Message.ToolCalls {
			var args map[string]interface{}
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			})
		}
	}

	return result, nil
}

// doRequest makes the HTTP request.
func (p *OpenAICompatProvider) doRequest(ctx context.Context, req oaiRequest) (*oaiResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		if httpResp.StatusCode == 429 {
			return nil, fmt.Errorf("rate limit exceeded: %s", string(respBody))
		}
		if httpResp.StatusCode == 402 {
			return nil, fmt.Errorf("payment required: %s", string(respBody))
		}
		return nil, fmt.Errorf("API error (status %d): %s", httpResp.StatusCode, string(respBody))
	}

	var resp oaiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("API error: %s", resp.Error.Message)
	}

	return &resp, nil
}

// Provider-specific base URLs
const (
	GroqBaseURL    = "https://api.groq.com/openai/v1"
	MistralBaseURL = "https://api.mistral.ai/v1"
)

// NewGroqProvider creates a Groq provider (uses OpenAI-compatible API).
func NewGroqProvider(cfg OpenAICompatConfig) (*OpenAICompatProvider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = GroqBaseURL
	}
	if cfg.ProviderName == "" {
		cfg.ProviderName = "groq"
	}
	return NewOpenAICompatProvider(cfg)
}

// NewMistralProvider creates a Mistral provider (uses OpenAI-compatible API).
func NewMistralProvider(cfg OpenAICompatConfig) (*OpenAICompatProvider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = MistralBaseURL
	}
	if cfg.ProviderName == "" {
		cfg.ProviderName = "mistral"
	}
	return NewOpenAICompatProvider(cfg)
}
