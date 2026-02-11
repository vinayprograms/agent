package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// OpenAIProvider implements the Provider interface using the official OpenAI SDK.
type OpenAIProvider struct {
	client    *openai.Client
	model     string
	maxTokens int
	thinking  ThinkingConfig
	retry     RetryConfig
}

// OpenAIConfig holds configuration for the OpenAI provider.
type OpenAIConfig struct {
	APIKey    string
	BaseURL   string // Optional custom endpoint
	Model     string
	MaxTokens int
	Thinking  ThinkingConfig
	Retry     RetryConfig
}

// NewOpenAIProvider creates a new OpenAI provider using the official SDK.
func NewOpenAIProvider(cfg OpenAIConfig) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required for openai")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required for openai")
	}
	if cfg.MaxTokens == 0 {
		return nil, fmt.Errorf("max_tokens is required for openai")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	client := openai.NewClient(opts...)

	return &OpenAIProvider{
		client:    &client,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		thinking:  cfg.Thinking,
		retry:     cfg.Retry,
	}, nil
}

// getRetryConfig returns effective retry settings with defaults.
func (p *OpenAIProvider) getRetryConfig() (maxRetries int, initBackoff, maxBackoff time.Duration) {
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

// Chat implements the Provider interface.
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to OpenAI format
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			messages = append(messages, openai.SystemMessage(m.Content))
		case "user":
			messages = append(messages, openai.UserMessage(m.Content))
		case "assistant":
			if len(m.ToolCalls) > 0 {
				// Assistant message with tool calls
				toolCalls := make([]openai.ChatCompletionMessageToolCallParam, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					argsJSON, _ := json.Marshal(tc.Args)
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: string(argsJSON),
						},
					})
				}
				messages = append(messages, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Content:   openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(m.Content)},
						ToolCalls: toolCalls,
					},
				})
			} else {
				messages = append(messages, openai.AssistantMessage(m.Content))
			}
		case "tool":
			messages = append(messages, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}

	// Convert tools to OpenAI format
	tools := make([]openai.ChatCompletionToolParam, 0, len(req.Tools))
	for _, t := range req.Tools {
		schemaJSON, _ := json.Marshal(t.Parameters)
		var schema shared.FunctionParameters
		json.Unmarshal(schemaJSON, &schema)

		tools = append(tools, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  schema,
			},
		})
	}

	maxTokens := int64(p.maxTokens)
	if req.MaxTokens > 0 {
		maxTokens = int64(req.MaxTokens)
	}

	// Build request params
	params := openai.ChatCompletionNewParams{
		Model:     shared.ChatModel(p.model),
		Messages:  messages,
		MaxTokens: openai.Int(maxTokens),
	}

	if len(tools) > 0 {
		params.Tools = tools
	}

	// Add reasoning effort for o1/o3 models
	thinkingLevel := ResolveThinkingLevel(p.thinking, req.Messages, req.Tools)
	if thinkingLevel != ThinkingOff && isReasoningModel(p.model) {
		var effort shared.ReasoningEffort
		switch thinkingLevel {
		case ThinkingHigh:
			effort = shared.ReasoningEffortHigh
		case ThinkingMedium:
			effort = shared.ReasoningEffortMedium
		case ThinkingLow:
			effort = shared.ReasoningEffortLow
		}
		params.ReasoningEffort = effort
	}

	// Make request with retry
	maxRetries, initBackoff, maxBackoff := p.getRetryConfig()
	var resp *openai.ChatCompletion
	var err error
	backoff := initBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = p.client.Chat.Completions.New(ctx, params)
		if err == nil {
			break
		}

		if isBillingError(err) {
			return nil, fmt.Errorf("billing/payment error (fatal): %w", err)
		}

		if !isRetryableError(err) {
			return nil, fmt.Errorf("openai request failed: %w", err)
		}

		if attempt == maxRetries {
			return nil, fmt.Errorf("openai request failed after %d retries: %w", maxRetries, err)
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
		Model: resp.Model,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		result.Content = choice.Message.Content
		result.StopReason = string(choice.FinishReason)

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

	result.InputTokens = int(resp.Usage.PromptTokens)
	result.OutputTokens = int(resp.Usage.CompletionTokens)

	return result, nil
}

// isReasoningModel checks if the model supports reasoning effort (o1, o3 models).
func isReasoningModel(model string) bool {
	return len(model) >= 2 && (model[:2] == "o1" || model[:2] == "o3")
}
