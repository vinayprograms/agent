package llm

import (
	"fmt"
	"strings"
)

// NewProvider creates a provider based on the configuration.
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

	// Route to the appropriate provider implementation
	switch cfg.Provider {
	case "anthropic":
		return NewAnthropicProvider(AnthropicConfig{
			APIKey:    cfg.APIKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
			Thinking:  cfg.Thinking,
			Retry:     cfg.RetryConfig,
		})

	case "openai":
		return NewOpenAIProvider(OpenAIConfig{
			APIKey:    cfg.APIKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
			Thinking:  cfg.Thinking,
			Retry:     cfg.RetryConfig,
		})

	case "google":
		return NewGoogleProvider(GoogleConfig{
			APIKey:    cfg.APIKey,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
			Thinking:  cfg.Thinking,
			Retry:     cfg.RetryConfig,
		})

	case "groq":
		return NewGroqProvider(OpenAICompatConfig{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ProviderName: "groq",
			Thinking:     cfg.Thinking,
			Retry:        cfg.RetryConfig,
		})

	case "mistral":
		return NewMistralProvider(OpenAICompatConfig{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ProviderName: "mistral",
			Thinking:     cfg.Thinking,
			Retry:        cfg.RetryConfig,
		})

	case "ollama-cloud":
		return NewOllamaCloudProvider(OllamaCloudConfig{
			APIKey:    cfg.APIKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
			Thinking:  cfg.Thinking,
			Retry:     cfg.RetryConfig,
		})

	case "xai":
		return NewXAIProvider(OpenAICompatConfig{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ProviderName: "xai",
			Thinking:     cfg.Thinking,
			Retry:        cfg.RetryConfig,
		})

	case "openrouter":
		return NewOpenRouterProvider(OpenAICompatConfig{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ProviderName: "openrouter",
			Thinking:     cfg.Thinking,
			Retry:        cfg.RetryConfig,
		})

	case "ollama-local", "ollama":
		return NewOllamaLocalProvider(OpenAICompatConfig{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ProviderName: "ollama-local",
			Thinking:     cfg.Thinking,
			Retry:        cfg.RetryConfig,
		})

	case "lmstudio":
		return NewLMStudioProvider(OpenAICompatConfig{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ProviderName: "lmstudio",
			Thinking:     cfg.Thinking,
			Retry:        cfg.RetryConfig,
		})

	case "openai-compat", "litellm":
		// Generic OpenAI-compatible endpoint
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("base_url is required for provider %s", cfg.Provider)
		}
		return NewOpenAICompatProvider(OpenAICompatConfig{
			APIKey:       cfg.APIKey,
			BaseURL:      cfg.BaseURL,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ProviderName: cfg.Provider,
			Thinking:     cfg.Thinking,
			Retry:        cfg.RetryConfig,
		})

	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}
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

	// xAI (Grok) models
	if strings.HasPrefix(model, "grok") {
		return "xai"
	}

	return ""
}

// Retry configuration defaults
const (
	defaultMaxRetries = 5
	defaultInitBackoff = 1 * 1e9  // 1 second in nanoseconds
	defaultMaxBackoff  = 60 * 1e9 // 60 seconds in nanoseconds
	backoffFactor      = 2.0
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
