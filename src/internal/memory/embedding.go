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

// GoogleEmbedder generates embeddings using Google's Gemini API.
type GoogleEmbedder struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// GoogleConfig configures the Google embedder.
type GoogleConfig struct {
	APIKey  string
	Model   string // default: text-embedding-004
	BaseURL string // default: https://generativelanguage.googleapis.com/v1beta
}

// NewGoogleEmbedder creates a new Google embedding provider.
func NewGoogleEmbedder(cfg GoogleConfig) *GoogleEmbedder {
	model := cfg.Model
	if model == "" {
		model = "text-embedding-004"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	return &GoogleEmbedder{
		apiKey:  cfg.APIKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type googleEmbedRequest struct {
	Model   string              `json:"model"`
	Content googleEmbedContent  `json:"content"`
}

type googleEmbedContent struct {
	Parts []googleEmbedPart `json:"parts"`
}

type googleEmbedPart struct {
	Text string `json:"text"`
}

type googleEmbedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

// Embed generates embeddings for the given texts.
func (e *GoogleEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	results := make([][]float32, len(texts))

	// Google embeds one at a time
	for i, text := range texts {
		reqBody := googleEmbedRequest{
			Model: "models/" + e.model,
			Content: googleEmbedContent{
				Parts: []googleEmbedPart{{Text: text}},
			},
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		url := fmt.Sprintf("%s/models/%s:embedContent?key=%s", e.baseURL, e.model, e.apiKey)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
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
			return nil, fmt.Errorf("google embedding error (status %d): %s", resp.StatusCode, string(body))
		}

		var embedResp googleEmbedResponse
		if err := json.Unmarshal(body, &embedResp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		results[i] = embedResp.Embedding.Values
	}

	return results, nil
}

// Dimension returns the embedding dimension for the model.
func (e *GoogleEmbedder) Dimension() int {
	switch e.model {
	case "text-embedding-004":
		return 768
	case "embedding-001":
		return 768
	default:
		return 768
	}
}

// MistralEmbedder generates embeddings using Mistral's API.
type MistralEmbedder struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// MistralConfig configures the Mistral embedder.
type MistralConfig struct {
	APIKey  string
	Model   string // default: mistral-embed
	BaseURL string // default: https://api.mistral.ai/v1
}

// NewMistralEmbedder creates a new Mistral embedding provider.
func NewMistralEmbedder(cfg MistralConfig) *MistralEmbedder {
	model := cfg.Model
	if model == "" {
		model = "mistral-embed"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.mistral.ai/v1"
	}
	return &MistralEmbedder{
		apiKey:  cfg.APIKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type mistralEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type mistralEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed generates embeddings for the given texts.
func (e *MistralEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := mistralEmbedRequest{
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
		return nil, fmt.Errorf("mistral embedding error (status %d): %s", resp.StatusCode, string(body))
	}

	var embedResp mistralEmbedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result := make([][]float32, len(texts))
	for _, d := range embedResp.Data {
		if d.Index < len(result) {
			result[d.Index] = d.Embedding
		}
	}

	return result, nil
}

// Dimension returns the embedding dimension.
func (e *MistralEmbedder) Dimension() int {
	return 1024 // mistral-embed dimension
}

// CohereEmbedder generates embeddings using Cohere's API.
type CohereEmbedder struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// CohereConfig configures the Cohere embedder.
type CohereConfig struct {
	APIKey  string
	Model   string // default: embed-english-v3.0
	BaseURL string // default: https://api.cohere.ai/v1
}

// NewCohereEmbedder creates a new Cohere embedding provider.
func NewCohereEmbedder(cfg CohereConfig) *CohereEmbedder {
	model := cfg.Model
	if model == "" {
		model = "embed-english-v3.0"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.cohere.ai/v1"
	}
	return &CohereEmbedder{
		apiKey:  cfg.APIKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type cohereEmbedRequest struct {
	Model     string   `json:"model"`
	Texts     []string `json:"texts"`
	InputType string   `json:"input_type"`
}

type cohereEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed generates embeddings for the given texts.
func (e *CohereEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := cohereEmbedRequest{
		Model:     e.model,
		Texts:     texts,
		InputType: "search_document", // or "search_query" for queries
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/embed", bytes.NewReader(jsonBody))
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
		return nil, fmt.Errorf("cohere embedding error (status %d): %s", resp.StatusCode, string(body))
	}

	var embedResp cohereEmbedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return embedResp.Embeddings, nil
}

// Dimension returns the embedding dimension.
func (e *CohereEmbedder) Dimension() int {
	switch e.model {
	case "embed-english-v3.0", "embed-multilingual-v3.0":
		return 1024
	case "embed-english-light-v3.0", "embed-multilingual-light-v3.0":
		return 384
	default:
		return 1024
	}
}

// VoyageEmbedder generates embeddings using Voyage AI's API.
type VoyageEmbedder struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// VoyageConfig configures the Voyage embedder.
type VoyageConfig struct {
	APIKey  string
	Model   string // default: voyage-2
	BaseURL string // default: https://api.voyageai.com/v1
}

// NewVoyageEmbedder creates a new Voyage AI embedding provider.
func NewVoyageEmbedder(cfg VoyageConfig) *VoyageEmbedder {
	model := cfg.Model
	if model == "" {
		model = "voyage-2"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.voyageai.com/v1"
	}
	return &VoyageEmbedder{
		apiKey:  cfg.APIKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type voyageEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type voyageEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed generates embeddings for the given texts.
func (e *VoyageEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := voyageEmbedRequest{
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
		return nil, fmt.Errorf("voyage embedding error (status %d): %s", resp.StatusCode, string(body))
	}

	var embedResp voyageEmbedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result := make([][]float32, len(texts))
	for _, d := range embedResp.Data {
		if d.Index < len(result) {
			result[d.Index] = d.Embedding
		}
	}

	return result, nil
}

// Dimension returns the embedding dimension.
func (e *VoyageEmbedder) Dimension() int {
	switch e.model {
	case "voyage-2", "voyage-large-2":
		return 1024
	case "voyage-code-2":
		return 1536
	case "voyage-lite-02-instruct":
		return 1024
	default:
		return 1024
	}
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
