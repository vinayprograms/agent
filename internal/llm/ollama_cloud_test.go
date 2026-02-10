package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaCloudProvider_Config(t *testing.T) {
	// Test missing API key
	_, err := NewOllamaCloudProvider(OllamaCloudConfig{
		Model: "gpt-oss:120b",
	})
	if err == nil {
		t.Error("expected error for missing API key")
	}

	// Test missing model
	_, err = NewOllamaCloudProvider(OllamaCloudConfig{
		APIKey: "test-key",
	})
	if err == nil {
		t.Error("expected error for missing model")
	}

	// Test valid config
	p, err := NewOllamaCloudProvider(OllamaCloudConfig{
		APIKey: "test-key",
		Model:  "gpt-oss:120b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL != "https://ollama.com" {
		t.Errorf("expected default baseURL, got %s", p.baseURL)
	}
	if p.maxTokens != 4096 {
		t.Errorf("expected default maxTokens 4096, got %d", p.maxTokens)
	}
}

func TestOllamaCloudProvider_Chat(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected path /api/chat, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer auth, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected JSON content type, got %s", r.Header.Get("Content-Type"))
		}

		// Parse request
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.Model != "gpt-oss:120b" {
			t.Errorf("expected model gpt-oss:120b, got %s", req.Model)
		}
		if len(req.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(req.Messages))
		}

		// Send response
		resp := ollamaChatResponse{
			Model: "gpt-oss:120b",
			Message: ollamaMessage{
				Role:    "assistant",
				Content: "The sky is blue due to Rayleigh scattering.",
			},
			Done:            true,
			DoneReason:      "stop",
			PromptEvalCount: 10,
			EvalCount:       15,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create provider with mock server
	p, err := NewOllamaCloudProvider(OllamaCloudConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Model:   "gpt-oss:120b",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// Make request
	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Why is the sky blue?"},
		},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	if resp.Content != "The sky is blue due to Rayleigh scattering." {
		t.Errorf("unexpected content: %s", resp.Content)
	}
	if resp.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 15 {
		t.Errorf("expected 15 output tokens, got %d", resp.OutputTokens)
	}
}

func TestOllamaCloudProvider_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{
			Model: "gpt-oss:120b",
			Message: ollamaMessage{
				Role: "assistant",
				ToolCalls: []ollamaToolCall{
					{
						Function: ollamaFunction{
							Name:      "get_weather",
							Arguments: map[string]interface{}{"location": "NYC"},
						},
					},
				},
			},
			Done:       true,
			DoneReason: "tool_calls",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p, _ := NewOllamaCloudProvider(OllamaCloudConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Model:   "gpt-oss:120b",
	})

	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "What's the weather in NYC?"},
		},
		Tools: []ToolDef{
			{Name: "get_weather", Description: "Get weather", Parameters: map[string]interface{}{}},
		},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", resp.ToolCalls[0].Name)
	}
}
