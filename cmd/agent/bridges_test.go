package main

import (
	"context"
	"testing"

	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/policy"
)

// mockLLMProvider implements llm.Provider for testing.
type mockLLMProvider struct {
	response string
	err      error
}

func (m *mockLLMProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}

func (m *mockLLMProvider) Name() string { return "mock" }

func TestLLMProviderAdapter(t *testing.T) {
	mock := &mockLLMProvider{response: "test response"}
	adapter := policy.LLMProviderFromChatProvider(mock)

	result, err := adapter.Generate(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "test response" {
		t.Errorf("got %q, want %q", result.Content, "test response")
	}
}

func TestLLMProviderAdapter_Error(t *testing.T) {
	mock := &mockLLMProvider{err: context.Canceled}
	adapter := policy.LLMProviderFromChatProvider(mock)

	_, err := adapter.Generate(context.Background(), "test prompt")
	if err == nil {
		t.Error("expected error")
	}
}
