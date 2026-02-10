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

// Retry configuration defaults
const (
	defaultMaxRetries   = 5
	defaultInitBackoff  = 1 * time.Second
	defaultMaxBackoff   = 60 * time.Second
	backoffFactor       = 2.0
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

// isServerError checks if the error is a transient server error (5xx).
func isServerError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "internal server error") ||
		strings.Contains(errStr, "bad gateway") ||
		strings.Contains(errStr, "service unavailable") ||
		strings.Contains(errStr, "gateway timeout") ||
		strings.Contains(errStr, "temporarily unavailable")
}

// isRetryableError checks if the error is retryable (rate limit or server error).
func isRetryableError(err error) bool {
	return isRateLimitError(err) || isServerError(err)
}

// isBillingError checks if the error is a billing/payment/quota error (fatal, no retry).
func isBillingError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "billing") ||
		strings.Contains(errStr, "payment") ||
		strings.Contains(errStr, "credits") ||
		strings.Contains(errStr, "quota exceeded") ||
		strings.Contains(errStr, "insufficient") ||
		strings.Contains(errStr, "402") ||
		strings.Contains(errStr, "subscription") ||
		strings.Contains(errStr, "expired")
}

// FantasyAdapter wraps a fantasy.LanguageModel to implement our Provider interface.
type FantasyAdapter struct {
	model        fantasy.LanguageModel
	maxTokens    int
	providerName string
	thinking     ThinkingConfig
	retry        RetryConfig
}

// NewFantasyAdapter creates a new adapter wrapping a fantasy LanguageModel.
func NewFantasyAdapter(model fantasy.LanguageModel, maxTokens int) *FantasyAdapter {
	return &FantasyAdapter{
		model:     model,
		maxTokens: maxTokens,
	}
}

// NewFantasyAdapterWithOptions creates a new adapter with full configuration.
func NewFantasyAdapterWithOptions(model fantasy.LanguageModel, maxTokens int, providerName string, thinking ThinkingConfig, retry RetryConfig) *FantasyAdapter {
	return &FantasyAdapter{
		model:        model,
		maxTokens:    maxTokens,
		providerName: providerName,
		thinking:     thinking,
		retry:        retry,
	}
}

// NewFantasyAdapterWithThinking creates a new adapter with thinking support (legacy).
func NewFantasyAdapterWithThinking(model fantasy.LanguageModel, maxTokens int, providerName string, thinking ThinkingConfig) *FantasyAdapter {
	return NewFantasyAdapterWithOptions(model, maxTokens, providerName, thinking, RetryConfig{})
}

// getRetryConfig returns effective retry settings with defaults.
func (a *FantasyAdapter) getRetryConfig() (maxRetries int, initBackoff, maxBackoff time.Duration) {
	maxRetries = a.retry.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	initBackoff = a.retry.InitBackoff
	if initBackoff <= 0 {
		initBackoff = defaultInitBackoff
	}
	maxBackoff = a.retry.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	return
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

	// Add thinking/reasoning options if configured (auto is the default)
	thinkingLevel := ResolveThinkingLevel(a.thinking, req.Messages, req.Tools)
	if thinkingLevel != ThinkingOff {
		call.ProviderOptions = a.buildThinkingOptions(thinkingLevel)
	}

	// Generate with retry and exponential backoff
	maxRetries, initBackoff, maxBackoff := a.getRetryConfig()
	var resp *fantasy.Response
	var err error
	backoff := initBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = a.model.Generate(ctx, call)
		if err == nil {
			break
		}

		// Billing errors are fatal - no retry
		if isBillingError(err) {
			return nil, fmt.Errorf("billing/payment error (fatal): %w", err)
		}

		// Only retry transient errors (rate limits, 5xx)
		if !isRetryableError(err) {
			return nil, fmt.Errorf("fantasy generate failed: %w", err)
		}

		// Don't retry if we've exhausted attempts
		if attempt == maxRetries {
			return nil, fmt.Errorf("fantasy generate failed after %d retries: %w", maxRetries, err)
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
		case *fantasy.ReasoningContent:
			result.Thinking += c.Text
		case fantasy.ReasoningContent:
			result.Thinking += c.Text
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

// buildThinkingOptions creates provider-specific thinking options.
func (a *FantasyAdapter) buildThinkingOptions(level ThinkingLevel) fantasy.ProviderOptions {
	switch a.providerName {
	case "anthropic":
		budget := ThinkingLevelToAnthropicBudget(level, a.thinking.BudgetTokens)
		if budget > 0 {
			return anthropic.NewProviderOptions(&anthropic.ProviderOptions{
				Thinking: &anthropic.ThinkingProviderOption{
					BudgetTokens: budget,
				},
			})
		}
	case "openai":
		// Map our levels to OpenAI reasoning effort
		var effort openai.ReasoningEffort
		switch level {
		case ThinkingHigh:
			effort = openai.ReasoningEffortHigh
		case ThinkingMedium:
			effort = openai.ReasoningEffortMedium
		case ThinkingLow:
			effort = openai.ReasoningEffortLow
		default:
			effort = openai.ReasoningEffortMinimal
		}
		return openai.NewProviderOptions(&openai.ProviderOptions{
			ReasoningEffort: &effort,
		})
	}

	return nil
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

// createFantasyProvider creates a Fantasy provider for the given provider name, API key, and optional base URL.
func createFantasyProvider(providerName, apiKey, baseURL string) (fantasy.Provider, error) {
	switch providerName {
	case "anthropic":
		if baseURL != "" {
			return openaicompat.New(
				openaicompat.WithBaseURL(baseURL),
				openaicompat.WithAPIKey(apiKey),
				openaicompat.WithName("anthropic"),
			)
		}
		return anthropic.New(anthropic.WithAPIKey(apiKey))
	case "openai":
		if baseURL != "" {
			return openaicompat.New(
				openaicompat.WithBaseURL(baseURL),
				openaicompat.WithAPIKey(apiKey),
				openaicompat.WithName("openai"),
			)
		}
		return openai.New(openai.WithAPIKey(apiKey))
	case "google":
		return google.New(google.WithGeminiAPIKey(apiKey))
	case "groq":
		url := "https://api.groq.com/openai/v1"
		if baseURL != "" {
			url = baseURL
		}
		return openaicompat.New(
			openaicompat.WithBaseURL(url),
			openaicompat.WithAPIKey(apiKey),
			openaicompat.WithName("groq"),
		)
	case "mistral":
		url := "https://api.mistral.ai/v1"
		if baseURL != "" {
			url = baseURL
		}
		return openaicompat.New(
			openaicompat.WithBaseURL(url),
			openaicompat.WithAPIKey(apiKey),
			openaicompat.WithName("mistral"),
		)
	case "openai-compat", "openrouter", "litellm", "ollama", "lmstudio":
		// Generic OpenAI-compatible endpoint
		if baseURL == "" {
			return nil, fmt.Errorf("base_url is required for provider %s", providerName)
		}
		return openaicompat.New(
			openaicompat.WithBaseURL(baseURL),
			openaicompat.WithAPIKey(apiKey),
			openaicompat.WithName(providerName),
		)
	case "ollama-cloud":
		// Ollama Cloud uses a native provider, not fantasy - return nil here
		// The caller (NewProvider) handles this case specially
		return nil, fmt.Errorf("ollama-cloud requires native provider")
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}

// NewProvider creates a provider based on the configuration using fantasy.
// If Provider is empty, it will be inferred from the Model name.
func NewProvider(cfg FantasyConfig) (Provider, error) {
	// Infer provider from model name if not specified
	if cfg.Provider == "" && cfg.Model != "" {
		cfg.Provider = InferProviderFromModel(cfg.Model)

		if cfg.Provider == "" {
			return nil, fmt.Errorf("cannot determine provider for model %q; set provider explicitly", cfg.Model)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()

	// Handle ollama-cloud specially - it uses a native provider, not fantasy
	if cfg.Provider == "ollama-cloud" {
		return NewOllamaCloudProvider(OllamaCloudConfig{
			APIKey:    cfg.APIKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
			Thinking:  cfg.Thinking,
			Retry:     cfg.RetryConfig,
		})
	}

	fantasyProvider, err := createFantasyProvider(cfg.Provider, cfg.APIKey, cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s provider: %w", cfg.Provider, err)
	}

	model, err := fantasyProvider.LanguageModel(context.Background(), cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to get model %s: %w", cfg.Model, err)
	}

	// Use full options adapter (auto thinking is default when empty)
	thinkingLevel := cfg.Thinking.Level
	if thinkingLevel == "" {
		thinkingLevel = ThinkingAuto // Default to auto-detection
	}
	thinkingCfg := cfg.Thinking
	thinkingCfg.Level = thinkingLevel
	
	return NewFantasyAdapterWithOptions(model, cfg.MaxTokens, cfg.Provider, thinkingCfg, cfg.RetryConfig), nil
}

// NewFantasyProvider is an alias for NewProvider.
func NewFantasyProvider(cfg FantasyConfig) (Provider, error) {
	return NewProvider(cfg)
}
