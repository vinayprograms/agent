package main

import (
	"context"
	"testing"

	"github.com/vinayprograms/agentkit/llm"
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

func TestLLMGenerateAdapter(t *testing.T) {
	mock := &mockLLMProvider{response: "test response"}
	adapter := &llmGenerateAdapter{provider: mock}

	result, err := adapter.Generate(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "test response" {
		t.Errorf("got %q, want %q", result.Content, "test response")
	}
}

func TestLLMGenerateAdapter_Error(t *testing.T) {
	mock := &mockLLMProvider{err: context.Canceled}
	adapter := &llmGenerateAdapter{provider: mock}

	_, err := adapter.Generate(context.Background(), "test prompt")
	if err == nil {
		t.Error("expected error")
	}
}

// Note: semanticMemoryBridge tests would require mocking memory.ToolsAdapter
// which is tested in the memory package itself.
