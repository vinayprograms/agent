package main

import (
	"os"
	"testing"

	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/credentials"
)

func TestCreateEmbeddingProvider_None(t *testing.T) {
	cfg := config.EmbeddingConfig{Provider: "none"}
	creds := &credentials.Credentials{}

	provider, err := createEmbeddingProvider(cfg, creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != nil {
		t.Error("expected nil provider for 'none'")
	}
}

func TestCreateEmbeddingProvider_Disabled(t *testing.T) {
	cfg := config.EmbeddingConfig{Provider: "disabled"}
	creds := &credentials.Credentials{}

	provider, err := createEmbeddingProvider(cfg, creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != nil {
		t.Error("expected nil provider for 'disabled'")
	}
}

func TestCreateEmbeddingProvider_Unsupported(t *testing.T) {
	cfg := config.EmbeddingConfig{Provider: "unknown-provider"}
	creds := &credentials.Credentials{}

	_, err := createEmbeddingProvider(cfg, creds)
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestCreateEmbeddingProvider_OpenAI_NoKey(t *testing.T) {
	// Clear env var
	old := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", old)

	cfg := config.EmbeddingConfig{Provider: "openai"}
	creds := &credentials.Credentials{}

	_, err := createEmbeddingProvider(cfg, creds)
	if err == nil {
		t.Error("expected error when API key missing")
	}
}

func TestCreateEmbeddingProvider_LiteLLM_NoBaseURL(t *testing.T) {
	cfg := config.EmbeddingConfig{Provider: "litellm"}
	creds := &credentials.Credentials{}

	_, err := createEmbeddingProvider(cfg, creds)
	if err == nil {
		t.Error("expected error when base_url missing for litellm")
	}
}

func TestCreateOllamaEmbedder_DefaultURL(t *testing.T) {
	cfg := config.EmbeddingConfig{Provider: "ollama", Model: "nomic-embed-text"}

	provider, err := createOllamaEmbedder(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Error("expected non-nil provider")
	}
}

func TestCreateOllamaEmbedder_CustomURL(t *testing.T) {
	cfg := config.EmbeddingConfig{
		Provider: "ollama",
		Model:    "nomic-embed-text",
		BaseURL:  "http://custom:11434",
	}

	provider, err := createOllamaEmbedder(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider == nil {
		t.Error("expected non-nil provider")
	}
}
