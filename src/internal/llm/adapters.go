package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
)

// Retry configuration
const (
	maxRetries     = 5
	initialBackoff = 1 * time.Second
	maxBackoff     = 60 * time.Second
	backoffFactor  = 2.0
)

// isRateLimitError checks if the error is a rate limit error.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "capacity")
}

// FantasyAdapter wraps a fantasy.LanguageModel to implement our Provider interface.
type FantasyAdapter struct {
	model     fantasy.LanguageModel
	maxTokens int
}

// NewFantasyAdapter creates a new adapter wrapping a fantasy LanguageModel.
func NewFantasyAdapter(model fantasy.LanguageModel, maxTokens int) *FantasyAdapter {
	return &FantasyAdapter{
		model:     model,
		maxTokens: maxTokens,
	}
}

// Chat implements the Provider interface using fantasy's Generate method.
func (a *FantasyAdapter) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to fantasy Prompt
	var prompt fantasy.Prompt

	for _, m := range req.Messages {
		var msg fantasy.Message

		switch m.Role {
		case "system":
			msg = fantasy.NewSystemMessage(m.Content)
		case "user":
			msg = fantasy.NewUserMessage(m.Content)
		case "assistant":
			var parts []fantasy.MessagePart
			if m.Content != "" {
				parts = append(parts, fantasy.TextPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				parts = append(parts, fantasy.ToolCallPart{
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Input:      string(argsJSON),
				})
			}
			msg = fantasy.Message{
				Role:    fantasy.MessageRoleAssistant,
				Content: parts,
			}
		case "tool":
			msg = fantasy.Message{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{
						ToolCallID: m.ToolCallID,
						Output:     fantasy.ToolResultOutputContentText{Text: m.Content},
					},
				},
			}
		default:
			continue
		}

		prompt = append(prompt, msg)
	}

	// Convert tools
	var tools []fantasy.Tool
	for _, t := range req.Tools {
		tools = append(tools, fantasy.FunctionTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	maxTokens := int64(a.maxTokens)
	if req.MaxTokens > 0 {
		maxTokens = int64(req.MaxTokens)
	}

	// Build call
	call := fantasy.Call{
		Prompt:          prompt,
		Tools:           tools,
		MaxOutputTokens: &maxTokens,
	}

	// Generate with retry and exponential backoff
	var resp *fantasy.Response
	var err error
	backoff := initialBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = a.model.Generate(ctx, call)
		if err == nil {
			break
		}

		// Check if it's a rate limit error
		if !isRateLimitError(err) {
			return nil, fmt.Errorf("fantasy generate failed: %w", err)
		}

		// Don't retry if we've exhausted attempts
		if attempt == maxRetries {
			return nil, fmt.Errorf("fantasy generate failed after %d retries (rate limited): %w", maxRetries, err)
		}

		// Wait before retrying
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		// Exponential backoff with cap
		backoff = time.Duration(float64(backoff) * backoffFactor)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	// Convert response
	result := &ChatResponse{
		StopReason:   string(resp.FinishReason),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
		Model:        a.model.Model(),
	}

	// Extract text and tool calls from response
	for _, content := range resp.Content {
		switch c := content.(type) {
		case *fantasy.TextContent:
			result.Content += c.Text
		case fantasy.TextContent:
			result.Content += c.Text
		case *fantasy.ToolCallContent:
			var args map[string]interface{}
			json.Unmarshal([]byte(c.Input), &args)
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:   c.ToolCallID,
				Name: c.ToolName,
				Args: args,
			})
		case fantasy.ToolCallContent:
			var args map[string]interface{}
			json.Unmarshal([]byte(c.Input), &args)
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:   c.ToolCallID,
				Name: c.ToolName,
				Args: args,
			})
		}
	}

	return result, nil
}

// InferProviderFromModel returns the provider name based on model name patterns.
// This allows users to just specify a model name without explicitly setting the provider.
func InferProviderFromModel(model string) string {
	model = strings.ToLower(model)

	// Anthropic models
	if strings.HasPrefix(model, "claude") {
		return "anthropic"
	}

	// OpenAI models
	if strings.HasPrefix(model, "gpt-") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "chatgpt") {
		return "openai"
	}

	// Google models
	if strings.HasPrefix(model, "gemini") ||
		strings.HasPrefix(model, "gemma") {
		return "google"
	}

	// Groq models (Llama, Mixtral on Groq)
	if strings.HasPrefix(model, "llama") ||
		strings.HasPrefix(model, "mixtral") && strings.Contains(model, "groq") {
		return "groq"
	}

	// Mistral models
	if strings.HasPrefix(model, "mistral") ||
		strings.HasPrefix(model, "mixtral") ||
		strings.HasPrefix(model, "codestral") ||
		strings.HasPrefix(model, "pixtral") {
		return "mistral"
	}

	return ""
}

// createFantasyProvider creates a Fantasy provider for the given provider name and API key.
func createFantasyProvider(providerName, apiKey string) (fantasy.Provider, error) {
	switch providerName {
	case "anthropic":
		return anthropic.New(anthropic.WithAPIKey(apiKey))
	case "openai":
		return openai.New(openai.WithAPIKey(apiKey))
	case "google":
		return google.New(google.WithGeminiAPIKey(apiKey))
	case "groq":
		return openaicompat.New(
			openaicompat.WithBaseURL("https://api.groq.com/openai/v1"),
			openaicompat.WithAPIKey(apiKey),
			openaicompat.WithName("groq"),
		)
	case "mistral":
		return openaicompat.New(
			openaicompat.WithBaseURL("https://api.mistral.ai/v1"),
			openaicompat.WithAPIKey(apiKey),
			openaicompat.WithName("mistral"),
		)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}

// NewProvider creates a provider based on the configuration using fantasy.
// If Provider is empty, it will be looked up from catwalk or inferred from the Model name.
func NewProvider(cfg FantasyConfig) (Provider, error) {
	// Try catwalk lookup first, then fall back to pattern inference
	if cfg.Provider == "" && cfg.Model != "" {
		providerID, modelInfo, err := FindModelProvider(context.Background(), cfg.Model)
		if err == nil && providerID != "" {
			cfg.Provider = providerID
			// Use model's default max tokens if not specified
			if cfg.MaxTokens == 0 && modelInfo != nil && modelInfo.DefaultMaxTokens > 0 {
				cfg.MaxTokens = int(modelInfo.DefaultMaxTokens)
			}
		} else {
			// Fall back to pattern-based inference
			cfg.Provider = InferProviderFromModel(cfg.Model)
		}

		if cfg.Provider == "" {
			return nil, fmt.Errorf("cannot determine provider for model %q; set provider explicitly", cfg.Model)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()

	fantasyProvider, err := createFantasyProvider(cfg.Provider, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s provider: %w", cfg.Provider, err)
	}

	model, err := fantasyProvider.LanguageModel(context.Background(), cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to get model %s: %w", cfg.Model, err)
	}

	return NewFantasyAdapter(model, cfg.MaxTokens), nil
}

// NewFantasyProvider is an alias for NewProvider.
func NewFantasyProvider(cfg FantasyConfig) (Provider, error) {
	return NewProvider(cfg)
}
