package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIEmbedder generates embeddings using OpenAI's API.
type OpenAIEmbedder struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// OpenAIConfig configures the OpenAI embedder.
type OpenAIConfig struct {
	APIKey  string
	Model   string // default: text-embedding-3-small
	BaseURL string // default: https://api.openai.com/v1
}

// NewOpenAIEmbedder creates a new OpenAI embedding provider.
func NewOpenAIEmbedder(cfg OpenAIConfig) *OpenAIEmbedder {
	model := cfg.Model
	if model == "" {
		model = "text-embedding-3-small"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIEmbedder{
		apiKey:  cfg.APIKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed generates embeddings for the given texts.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := openAIEmbedRequest{
		Model: e.model,
		Input: texts,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/embeddings", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API error (status %d): %s", resp.StatusCode, string(body))
	}

	var embedResp openAIEmbedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Sort by index to maintain order
	result := make([][]float32, len(texts))
	for _, d := range embedResp.Data {
		if d.Index < len(result) {
			result[d.Index] = d.Embedding
		}
	}

	return result, nil
}

// Dimension returns the embedding dimension for the model.
func (e *OpenAIEmbedder) Dimension() int {
	switch e.model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 1536 // default
	}
}

// OllamaEmbedder generates embeddings using Ollama's API.
type OllamaEmbedder struct {
	baseURL   string
	model     string
	dimension int
	client    *http.Client
}

// OllamaConfig configures the Ollama embedder.
type OllamaConfig struct {
	BaseURL   string // default: http://localhost:11434
	Model     string // e.g., nomic-embed-text, mxbai-embed-large
	Dimension int    // embedding dimension (model-specific)
}

// NewOllamaEmbedder creates a new Ollama embedding provider.
func NewOllamaEmbedder(cfg OllamaConfig) *OllamaEmbedder {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := cfg.Model
	if model == "" {
		model = "nomic-embed-text"
	}
	dimension := cfg.Dimension
	if dimension == 0 {
		// Default dimensions for common models
		switch model {
		case "nomic-embed-text":
			dimension = 768
		case "mxbai-embed-large":
			dimension = 1024
		case "all-minilm":
			dimension = 384
		default:
			dimension = 768
		}
	}
	return &OllamaEmbedder{
		baseURL:   baseURL,
		model:     model,
		dimension: dimension,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed generates embeddings for the given texts.
func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	results := make([][]float32, len(texts))

	// Ollama embeds one at a time (or we can batch with newer API)
	for i, text := range texts {
		reqBody := ollamaEmbedRequest{
			Model: e.model,
			Input: text,
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embed", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		resp, err := e.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("embedding request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ollama embedding error (status %d): %s", resp.StatusCode, string(body))
		}

		var embedResp ollamaEmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		if len(embedResp.Embeddings) > 0 {
			results[i] = embedResp.Embeddings[0]
		}
	}

	return results, nil
}

// Dimension returns the embedding dimension.
func (e *OllamaEmbedder) Dimension() int {
	return e.dimension
}

// OllamaCloudEmbedder generates embeddings using Ollama Cloud's API.
type OllamaCloudEmbedder struct {
	apiKey    string
	baseURL   string
	model     string
	dimension int
	client    *http.Client
}

// OllamaCloudConfig configures the Ollama Cloud embedder.
type OllamaCloudEmbedConfig struct {
	APIKey    string // Required
	BaseURL   string // default: https://ollama.com
	Model     string // e.g., nomic-embed-text, mxbai-embed-large
	Dimension int    // embedding dimension (model-specific)
}

// NewOllamaCloudEmbedder creates a new Ollama Cloud embedding provider.
func NewOllamaCloudEmbedder(cfg OllamaCloudEmbedConfig) (*OllamaCloudEmbedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required for ollama-cloud embeddings")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://ollama.com"
	}
	model := cfg.Model
	if model == "" {
		model = "nomic-embed-text"
	}
	dimension := cfg.Dimension
	if dimension == 0 {
		switch model {
		case "nomic-embed-text":
			dimension = 768
		case "mxbai-embed-large":
			dimension = 1024
		case "all-minilm":
			dimension = 384
		default:
			dimension = 768
		}
	}
	return &OllamaCloudEmbedder{
		apiKey:    cfg.APIKey,
		baseURL:   baseURL,
		model:     model,
		dimension: dimension,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

// Embed generates embeddings for the given texts.
func (e *OllamaCloudEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	results := make([][]float32, len(texts))

	for i, text := range texts {
		reqBody := ollamaEmbedRequest{
			Model: e.model,
			Input: text,
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embed", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)

		resp, err := e.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("embedding request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ollama-cloud embedding error (status %d): %s", resp.StatusCode, string(body))
		}

		var embedResp ollamaEmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		if len(embedResp.Embeddings) > 0 {
			results[i] = embedResp.Embeddings[0]
		}
	}

	return results, nil
}

// Dimension returns the embedding dimension.
func (e *OllamaCloudEmbedder) Dimension() int {
	return e.dimension
}

// MockEmbedder is a mock embedding provider for testing.
type MockEmbedder struct {
	dimension int
}

// NewMockEmbedder creates a mock embedder.
func NewMockEmbedder(dimension int) *MockEmbedder {
	return &MockEmbedder{dimension: dimension}
}

// Embed returns deterministic fake embeddings based on text hash.
func (e *MockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		embedding := make([]float32, e.dimension)
		// Generate deterministic embedding based on text
		for j := 0; j < e.dimension && j < len(text); j++ {
			embedding[j] = float32(text[j%len(text)]) / 256.0
		}
		results[i] = embedding
	}
	return results, nil
}

// Dimension returns the embedding dimension.
func (e *MockEmbedder) Dimension() int {
	return e.dimension
}
