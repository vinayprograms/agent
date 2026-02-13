package main

import (
	"fmt"
	"os"

	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/memory"
)

// createEmbeddingProvider creates an embedding provider based on config.
func createEmbeddingProvider(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	switch cfg.Provider {
	case "none", "disabled", "off":
		return nil, nil

	case "openai", "":
		return createOpenAIEmbedder(cfg, creds)

	case "google":
		return createGoogleEmbedder(cfg, creds)

	case "mistral":
		return createMistralEmbedder(cfg, creds)

	case "cohere":
		return createCohereEmbedder(cfg, creds)

	case "voyage":
		return createVoyageEmbedder(cfg, creds)

	case "ollama":
		return createOllamaEmbedder(cfg)

	case "ollama-cloud":
		return createOllamaCloudEmbedder(cfg, creds)

	case "litellm", "openai-compat":
		return createLiteLLMEmbedder(cfg, creds)

	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s (supported: openai, google, mistral, cohere, voyage, ollama, litellm, none)", cfg.Provider)
	}
}

func createOpenAIEmbedder(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	apiKey := creds.GetAPIKey("openai")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key not found for embeddings")
	}
	return memory.NewOpenAIEmbedder(memory.OpenAIConfig{
		APIKey:  apiKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
	}), nil
}

func createGoogleEmbedder(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	apiKey := creds.GetAPIKey("google")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Google API key not found for embeddings")
	}
	return memory.NewGoogleEmbedder(memory.GoogleConfig{
		APIKey:  apiKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
	}), nil
}

func createMistralEmbedder(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	apiKey := creds.GetAPIKey("mistral")
	if apiKey == "" {
		apiKey = os.Getenv("MISTRAL_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Mistral API key not found for embeddings")
	}
	return memory.NewMistralEmbedder(memory.MistralConfig{
		APIKey:  apiKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
	}), nil
}

func createCohereEmbedder(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	apiKey := creds.GetAPIKey("cohere")
	if apiKey == "" {
		apiKey = os.Getenv("COHERE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Cohere API key not found for embeddings")
	}
	return memory.NewCohereEmbedder(memory.CohereConfig{
		APIKey:  apiKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
	}), nil
}

func createVoyageEmbedder(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	apiKey := creds.GetAPIKey("voyage")
	if apiKey == "" {
		apiKey = os.Getenv("VOYAGE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Voyage AI API key not found for embeddings")
	}
	return memory.NewVoyageEmbedder(memory.VoyageConfig{
		APIKey:  apiKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
	}), nil
}

func createOllamaEmbedder(cfg config.EmbeddingConfig) (memory.EmbeddingProvider, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return memory.NewOllamaEmbedder(memory.OllamaConfig{
		BaseURL: baseURL,
		Model:   cfg.Model,
	}), nil
}

func createOllamaCloudEmbedder(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	apiKey := creds.GetAPIKey("ollama-cloud")
	if apiKey == "" {
		apiKey = os.Getenv("OLLAMA_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Ollama Cloud API key not found for embeddings")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://ollama.com"
	}
	return memory.NewOllamaCloudEmbedder(memory.OllamaCloudEmbedConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   cfg.Model,
	})
}

func createLiteLLMEmbedder(cfg config.EmbeddingConfig, creds *credentials.Credentials) (memory.EmbeddingProvider, error) {
	apiKey := creds.GetAPIKey("litellm")
	if apiKey == "" {
		apiKey = creds.GetAPIKey("llm")
	}
	if apiKey == "" {
		apiKey = os.Getenv("LITELLM_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("LiteLLM embedding requires base_url to be set")
	}
	return memory.NewOpenAIEmbedder(memory.OpenAIConfig{
		APIKey:  apiKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
	}), nil
}
