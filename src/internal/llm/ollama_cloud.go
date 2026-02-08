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

// OllamaCloudProvider implements the Provider interface for Ollama's cloud API.
// This uses Ollama's native /api/chat endpoint, not the OpenAI-compatible endpoint.
type OllamaCloudProvider struct {
	apiKey    string
	baseURL   string
	model     string
	maxTokens int
	client    *http.Client
}

// OllamaCloudConfig holds configuration for the Ollama Cloud provider.
type OllamaCloudConfig struct {
	APIKey    string
	BaseURL   string // defaults to https://ollama.com
	Model     string
	MaxTokens int
}

// NewOllamaCloudProvider creates a new Ollama Cloud provider.
func NewOllamaCloudProvider(cfg OllamaCloudConfig) (*OllamaCloudProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required for ollama-cloud")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required for ollama-cloud")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://ollama.com"
	}

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	return &OllamaCloudProvider{
		apiKey:    cfg.APIKey,
		baseURL:   baseURL,
		model:     cfg.Model,
		maxTokens: maxTokens,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// ollamaMessage represents a message in Ollama's API format.
type ollamaMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

// ollamaToolCall represents a tool call in Ollama's format.
type ollamaToolCall struct {
	Function ollamaFunction `json:"function"`
}

// ollamaFunction represents a function call in Ollama's format.
type ollamaFunction struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ollamaTool represents a tool definition in Ollama's format.
type ollamaTool struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

// ollamaToolFunction represents a function definition in Ollama's format.
type ollamaToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ollamaChatRequest represents a chat request to Ollama's API.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

// ollamaOptions represents generation options.
type ollamaOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
}

// ollamaChatResponse represents a response from Ollama's API.
type ollamaChatResponse struct {
	Model     string        `json:"model"`
	Message   ollamaMessage `json:"message"`
	Done      bool          `json:"done"`
	DoneReason string       `json:"done_reason,omitempty"`
	
	// Token counts
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

// Chat implements the Provider interface.
func (p *OllamaCloudProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to Ollama format
	messages := make([]ollamaMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		
		// Convert tool calls if present
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, ollamaToolCall{
					Function: ollamaFunction{
						Name:      tc.Name,
						Arguments: tc.Args,
					},
				})
			}
		}
		
		messages = append(messages, msg)
	}

	// Convert tools to Ollama format
	var tools []ollamaTool
	for _, t := range req.Tools {
		tools = append(tools, ollamaTool{
			Type: "function",
			Function: ollamaToolFunction{
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

	ollamaReq := ollamaChatRequest{
		Model:    p.model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
		Options: &ollamaOptions{
			NumPredict: maxTokens,
		},
	}

	// Make request with retry
	var resp *ollamaChatResponse
	var err error
	backoff := initialBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = p.doRequest(ctx, ollamaReq)
		if err == nil {
			break
		}

		if isBillingError(err) {
			return nil, fmt.Errorf("billing/payment error (fatal): %w", err)
		}

		if !isRateLimitError(err) {
			return nil, fmt.Errorf("ollama cloud request failed: %w", err)
		}

		if attempt == maxRetries {
			return nil, fmt.Errorf("ollama cloud request failed after %d retries (rate limited): %w", maxRetries, err)
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
		Content:      resp.Message.Content,
		StopReason:   resp.DoneReason,
		InputTokens:  resp.PromptEvalCount,
		OutputTokens: resp.EvalCount,
		Model:        resp.Model,
	}

	// Convert tool calls
	for i, tc := range resp.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
			ID:   fmt.Sprintf("call_%d", i),
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	return result, nil
}

// doRequest makes the HTTP request to Ollama's API.
func (p *OllamaCloudProvider) doRequest(ctx context.Context, req ollamaChatRequest) (*ollamaChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

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
		// Check for specific error types
		if httpResp.StatusCode == 429 {
			return nil, fmt.Errorf("rate limit exceeded: %s", string(respBody))
		}
		if httpResp.StatusCode == 402 {
			return nil, fmt.Errorf("payment required: %s", string(respBody))
		}
		return nil, fmt.Errorf("ollama API error (status %d): %s", httpResp.StatusCode, string(respBody))
	}

	var resp ollamaChatResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}
