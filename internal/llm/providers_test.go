package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// =============================================================================
// Anthropic Provider Tests
// =============================================================================

func TestAnthropicProvider_Creation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     AnthropicConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: AnthropicConfig{
				APIKey:    "test-key",
				Model:     "claude-3-5-sonnet-20241022",
				MaxTokens: 4096,
			},
			wantErr: false,
		},
		{
			name: "missing api key",
			cfg: AnthropicConfig{
				Model:     "claude-3-5-sonnet-20241022",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing model",
			cfg: AnthropicConfig{
				APIKey:    "test-key",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing max_tokens",
			cfg: AnthropicConfig{
				APIKey: "test-key",
				Model:  "claude-3-5-sonnet-20241022",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAnthropicProvider(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewAnthropicProvider() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAnthropicProvider_RetryConfig(t *testing.T) {
	p := &AnthropicProvider{
		retry: RetryConfig{
			MaxRetries:  3,
			InitBackoff: 2 * time.Second,
			MaxBackoff:  30 * time.Second,
		},
	}

	maxRetries, initBackoff, maxBackoff := p.getRetryConfig()
	if maxRetries != 3 {
		t.Errorf("expected maxRetries 3, got %d", maxRetries)
	}
	if initBackoff != 2*time.Second {
		t.Errorf("expected initBackoff 2s, got %v", initBackoff)
	}
	if maxBackoff != 30*time.Second {
		t.Errorf("expected maxBackoff 30s, got %v", maxBackoff)
	}
}

func TestAnthropicProvider_RetryConfigDefaults(t *testing.T) {
	p := &AnthropicProvider{} // No retry config set

	maxRetries, initBackoff, maxBackoff := p.getRetryConfig()
	if maxRetries != defaultMaxRetries {
		t.Errorf("expected default maxRetries %d, got %d", defaultMaxRetries, maxRetries)
	}
	if initBackoff != time.Duration(defaultInitBackoff) {
		t.Errorf("expected default initBackoff, got %v", initBackoff)
	}
	if maxBackoff != time.Duration(defaultMaxBackoff) {
		t.Errorf("expected default maxBackoff, got %v", maxBackoff)
	}
}

// =============================================================================
// OpenAI Provider Tests
// =============================================================================

func TestOpenAIProvider_Creation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     OpenAIConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: OpenAIConfig{
				APIKey:    "test-key",
				Model:     "gpt-4o",
				MaxTokens: 4096,
			},
			wantErr: false,
		},
		{
			name: "missing api key",
			cfg: OpenAIConfig{
				Model:     "gpt-4o",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing model",
			cfg: OpenAIConfig{
				APIKey:    "test-key",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing max_tokens",
			cfg: OpenAIConfig{
				APIKey: "test-key",
				Model:  "gpt-4o",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewOpenAIProvider(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewOpenAIProvider() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsReasoningModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"o1-preview", true},
		{"o1-mini", true},
		{"o3-mini", true},
		{"o3", true},
		{"gpt-4o", false},
		{"gpt-4", false},
		{"claude-3-5-sonnet", false},
		{"a", false}, // too short
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := isReasoningModel(tt.model)
			if got != tt.want {
				t.Errorf("isReasoningModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Google Provider Tests
// =============================================================================

func TestGoogleProvider_Creation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     GoogleConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: GoogleConfig{
				APIKey:    "test-key",
				Model:     "gemini-1.5-pro",
				MaxTokens: 4096,
			},
			wantErr: false,
		},
		{
			name: "missing api key",
			cfg: GoogleConfig{
				Model:     "gemini-1.5-pro",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing model",
			cfg: GoogleConfig{
				APIKey:    "test-key",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing max_tokens",
			cfg: GoogleConfig{
				APIKey: "test-key",
				Model:  "gemini-1.5-pro",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewGoogleProvider(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewGoogleProvider() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConvertPropertyToSchema(t *testing.T) {
	tests := []struct {
		name     string
		prop     map[string]interface{}
		wantType string
	}{
		{
			name:     "string type",
			prop:     map[string]interface{}{"type": "string"},
			wantType: "TypeString",
		},
		{
			name:     "number type",
			prop:     map[string]interface{}{"type": "number"},
			wantType: "TypeNumber",
		},
		{
			name:     "integer type",
			prop:     map[string]interface{}{"type": "integer"},
			wantType: "TypeInteger",
		},
		{
			name:     "boolean type",
			prop:     map[string]interface{}{"type": "boolean"},
			wantType: "TypeBoolean",
		},
		{
			name:     "array type",
			prop:     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			wantType: "TypeArray",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := convertPropertyToSchema(tt.prop)
			if schema.Type.String() != tt.wantType {
				t.Errorf("convertPropertyToSchema() type = %v, want %v", schema.Type.String(), tt.wantType)
			}
		})
	}
}

// =============================================================================
// OpenAI-Compatible Provider Tests
// =============================================================================

func TestOpenAICompatProvider_Creation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     OpenAICompatConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: OpenAICompatConfig{
				APIKey:       "test-key",
				BaseURL:      "https://api.example.com/v1",
				Model:        "model-1",
				MaxTokens:    4096,
				ProviderName: "test",
			},
			wantErr: false,
		},
		{
			name: "missing base url",
			cfg: OpenAICompatConfig{
				APIKey:    "test-key",
				Model:     "model-1",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing model",
			cfg: OpenAICompatConfig{
				BaseURL:   "https://api.example.com/v1",
				MaxTokens: 4096,
			},
			wantErr: true,
		},
		{
			name: "missing max_tokens",
			cfg: OpenAICompatConfig{
				BaseURL: "https://api.example.com/v1",
				Model:   "model-1",
			},
			wantErr: true,
		},
		{
			name: "api key optional for local",
			cfg: OpenAICompatConfig{
				BaseURL:   "http://localhost:11434/v1",
				Model:     "llama3",
				MaxTokens: 4096,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewOpenAICompatProvider(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewOpenAICompatProvider() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOpenAICompatProvider_MockServer(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}

		// Return mock response
		resp := map[string]interface{}{
			"id":    "test-id",
			"model": "test-model",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Hello from mock server!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatProvider(OpenAICompatConfig{
		BaseURL:      server.URL,
		Model:        "test-model",
		MaxTokens:    4096,
		ProviderName: "test",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}

	if resp.Content != "Hello from mock server!" {
		t.Errorf("expected 'Hello from mock server!', got %s", resp.Content)
	}
	if resp.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 5 {
		t.Errorf("expected 5 output tokens, got %d", resp.OutputTokens)
	}
}

func TestOpenAICompatProvider_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"id":    "test-id",
			"model": "test-model",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]interface{}{
							{
								"id":   "call_123",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "read",
									"arguments": `{"path": "/test.txt"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, _ := NewOpenAICompatProvider(OpenAICompatConfig{
		BaseURL:   server.URL,
		Model:     "test-model",
		MaxTokens: 4096,
	})

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Read the file"}},
		Tools: []ToolDef{{
			Name:        "read",
			Description: "Read a file",
			Parameters:  map[string]interface{}{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "read" {
		t.Errorf("expected tool name 'read', got %s", resp.ToolCalls[0].Name)
	}
	if resp.ToolCalls[0].Args["path"] != "/test.txt" {
		t.Errorf("expected path '/test.txt', got %v", resp.ToolCalls[0].Args["path"])
	}
}

func TestOpenAICompatProvider_RateLimitRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error": "rate limit exceeded"}`))
			return
		}
		// Success on third call
		resp := map[string]interface{}{
			"id":    "test-id",
			"model": "test-model",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Success after retry!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, _ := NewOpenAICompatProvider(OpenAICompatConfig{
		BaseURL:   server.URL,
		Model:     "test-model",
		MaxTokens: 4096,
		Retry: RetryConfig{
			MaxRetries:  5,
			InitBackoff: 10 * time.Millisecond,
			MaxBackoff:  100 * time.Millisecond,
		},
	})

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}

	if calls != 3 {
		t.Errorf("expected 3 calls (2 retries), got %d", calls)
	}
	if resp.Content != "Success after retry!" {
		t.Errorf("expected 'Success after retry!', got %s", resp.Content)
	}
}

func TestGroqProvider_Creation(t *testing.T) {
	cfg := OpenAICompatConfig{
		APIKey:    "test-key",
		Model:     "llama-3.1-70b-versatile",
		MaxTokens: 4096,
	}

	provider, err := NewGroqProvider(cfg)
	if err != nil {
		t.Fatalf("NewGroqProvider() error: %v", err)
	}
	if provider == nil {
		t.Error("expected non-nil provider")
	}
	// Check that BaseURL was set to Groq's default
	if provider.baseURL != GroqBaseURL {
		t.Errorf("expected base URL %s, got %s", GroqBaseURL, provider.baseURL)
	}
}

func TestMistralProvider_Creation(t *testing.T) {
	cfg := OpenAICompatConfig{
		APIKey:    "test-key",
		Model:     "mistral-large-latest",
		MaxTokens: 4096,
	}

	provider, err := NewMistralProvider(cfg)
	if err != nil {
		t.Fatalf("NewMistralProvider() error: %v", err)
	}
	if provider == nil {
		t.Error("expected non-nil provider")
	}
	// Check that BaseURL was set to Mistral's default
	if provider.baseURL != MistralBaseURL {
		t.Errorf("expected base URL %s, got %s", MistralBaseURL, provider.baseURL)
	}
}

// =============================================================================
// Error Classification Tests
// =============================================================================

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"rate limit exceeded", true},
		{"too many requests", true},
		{"error: 429", true},
		{"server overloaded", true},
		{"at capacity", true},
		{"internal server error", false},
		{"invalid api key", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError{msg: tt.errMsg}
			}
			got := isRateLimitError(err)
			if got != tt.want {
				t.Errorf("isRateLimitError(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestIsServerError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"internal server error", true},
		{"bad gateway", true},
		{"service unavailable", true},
		{"gateway timeout", true},
		{"error: 500", true},
		{"error: 502", true},
		{"error: 503", true},
		{"error: 504", true},
		{"temporarily unavailable", true},
		{"rate limit exceeded", false},
		{"invalid api key", false},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			got := isServerError(&testError{msg: tt.errMsg})
			if got != tt.want {
				t.Errorf("isServerError(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestIsBillingError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"billing issue", true},
		{"payment required", true},
		{"insufficient credits", true},
		{"quota exceeded", true},
		{"subscription expired", true},
		{"error: 402", true},
		{"rate limit exceeded", false},
		{"internal server error", false},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			got := isBillingError(&testError{msg: tt.errMsg})
			if got != tt.want {
				t.Errorf("isBillingError(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		// Rate limit errors - retryable
		{"rate limit exceeded", true},
		{"429", true},
		// Server errors - retryable
		{"500 internal server error", true},
		{"503 service unavailable", true},
		// Non-retryable
		{"invalid api key", false},
		{"billing issue", false},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			got := isRetryableError(&testError{msg: tt.errMsg})
			if got != tt.want {
				t.Errorf("isRetryableError(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

// Helper type for testing error classification
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
