package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// GoogleProvider implements the Provider interface using the official Google Gemini SDK.
type GoogleProvider struct {
	client    *genai.Client
	model     *genai.GenerativeModel
	modelName string
	maxTokens int
	thinking  ThinkingConfig
	retry     RetryConfig
}

// GoogleConfig holds configuration for the Google provider.
type GoogleConfig struct {
	APIKey    string
	Model     string
	MaxTokens int
	Thinking  ThinkingConfig
	Retry     RetryConfig
}

// NewGoogleProvider creates a new Google Gemini provider using the official SDK.
func NewGoogleProvider(cfg GoogleConfig) (*GoogleProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required for google")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required for google")
	}
	if cfg.MaxTokens == 0 {
		return nil, fmt.Errorf("max_tokens is required for google")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(cfg.APIKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create google client: %w", err)
	}

	model := client.GenerativeModel(cfg.Model)
	maxTokens := int32(cfg.MaxTokens)
	model.MaxOutputTokens = &maxTokens

	return &GoogleProvider{
		client:    client,
		model:     model,
		modelName: cfg.Model,
		maxTokens: cfg.MaxTokens,
		thinking:  cfg.Thinking,
		retry:     cfg.Retry,
	}, nil
}

// Close closes the underlying client.
func (p *GoogleProvider) Close() error {
	return p.client.Close()
}

// getRetryConfig returns effective retry settings with defaults.
func (p *GoogleProvider) getRetryConfig() (maxRetries int, initBackoff, maxBackoff time.Duration) {
	maxRetries = p.retry.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	initBackoff = p.retry.InitBackoff
	if initBackoff <= 0 {
		initBackoff = defaultInitBackoff
	}
	maxBackoff = p.retry.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	return
}

// Chat implements the Provider interface.
func (p *GoogleProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Set system instruction if present
	for _, m := range req.Messages {
		if m.Role == "system" {
			p.model.SystemInstruction = &genai.Content{
				Parts: []genai.Part{genai.Text(m.Content)},
			}
			break
		}
	}

	// Convert tools to Gemini format
	if len(req.Tools) > 0 {
		funcDecls := make([]*genai.FunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			// Convert parameters to Schema
			schema := convertToGeminiSchema(t.Parameters)
			funcDecls = append(funcDecls, &genai.FunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			})
		}
		p.model.Tools = []*genai.Tool{{FunctionDeclarations: funcDecls}}
	}

	// Build chat session with history
	cs := p.model.StartChat()
	var lastUserContent *genai.Content

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			// Already handled above
			continue
		case "user":
			lastUserContent = &genai.Content{
				Role:  "user",
				Parts: []genai.Part{genai.Text(m.Content)},
			}
			cs.History = append(cs.History, lastUserContent)
		case "assistant":
			content := &genai.Content{
				Role:  "model",
				Parts: []genai.Part{},
			}
			if m.Content != "" {
				content.Parts = append(content.Parts, genai.Text(m.Content))
			}
			for _, tc := range m.ToolCalls {
				content.Parts = append(content.Parts, genai.FunctionCall{
					Name: tc.Name,
					Args: tc.Args,
				})
			}
			cs.History = append(cs.History, content)
		case "tool":
			// Tool result
			cs.History = append(cs.History, &genai.Content{
				Role: "user",
				Parts: []genai.Part{
					genai.FunctionResponse{
						Name:     m.ToolCallID, // In our format, ToolCallID is the tool name for simplicity
						Response: map[string]interface{}{"result": m.Content},
					},
				},
			})
		}
	}

	// Remove last user message from history (will be sent as the prompt)
	var prompt string
	if len(cs.History) > 0 && cs.History[len(cs.History)-1].Role == "user" {
		lastContent := cs.History[len(cs.History)-1]
		cs.History = cs.History[:len(cs.History)-1]
		if len(lastContent.Parts) > 0 {
			if text, ok := lastContent.Parts[0].(genai.Text); ok {
				prompt = string(text)
			}
		}
	}

	// Make request with retry
	maxRetries, initBackoff, maxBackoff := p.getRetryConfig()
	var resp *genai.GenerateContentResponse
	var err error
	backoff := initBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = cs.SendMessage(ctx, genai.Text(prompt))
		if err == nil {
			break
		}

		if isBillingError(err) {
			return nil, fmt.Errorf("billing/payment error (fatal): %w", err)
		}

		if !isRetryableError(err) {
			return nil, fmt.Errorf("google request failed: %w", err)
		}

		if attempt == maxRetries {
			return nil, fmt.Errorf("google request failed after %d retries: %w", maxRetries, err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		backoff = time.Duration(float64(backoff) * backoffFactor)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	// Convert response
	result := &ChatResponse{
		Model: p.modelName,
	}

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.FinishReason != 0 {
			result.StopReason = candidate.FinishReason.String()
		}

		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				switch p := part.(type) {
				case genai.Text:
					result.Content += string(p)
				case genai.FunctionCall:
					result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
						ID:   fmt.Sprintf("call_%s", p.Name),
						Name: p.Name,
						Args: p.Args,
					})
				}
			}
		}
	}

	if resp.UsageMetadata != nil {
		result.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
		result.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}

	return result, nil
}

// convertToGeminiSchema converts a JSON Schema map to Gemini's Schema type.
func convertToGeminiSchema(params map[string]interface{}) *genai.Schema {
	schema := &genai.Schema{
		Type: genai.TypeObject,
	}

	if props, ok := params["properties"].(map[string]interface{}); ok {
		schema.Properties = make(map[string]*genai.Schema)
		for name, prop := range props {
			if propMap, ok := prop.(map[string]interface{}); ok {
				schema.Properties[name] = convertPropertyToSchema(propMap)
			}
		}
	}

	if required, ok := params["required"].([]interface{}); ok {
		for _, r := range required {
			if s, ok := r.(string); ok {
				schema.Required = append(schema.Required, s)
			}
		}
	}

	return schema
}

// convertPropertyToSchema converts a single property to Gemini Schema.
func convertPropertyToSchema(prop map[string]interface{}) *genai.Schema {
	schema := &genai.Schema{}

	if typ, ok := prop["type"].(string); ok {
		switch typ {
		case "string":
			schema.Type = genai.TypeString
		case "number":
			schema.Type = genai.TypeNumber
		case "integer":
			schema.Type = genai.TypeInteger
		case "boolean":
			schema.Type = genai.TypeBoolean
		case "array":
			schema.Type = genai.TypeArray
			if items, ok := prop["items"].(map[string]interface{}); ok {
				schema.Items = convertPropertyToSchema(items)
			}
		case "object":
			schema.Type = genai.TypeObject
			if props, ok := prop["properties"].(map[string]interface{}); ok {
				schema.Properties = make(map[string]*genai.Schema)
				for name, p := range props {
					if propMap, ok := p.(map[string]interface{}); ok {
						schema.Properties[name] = convertPropertyToSchema(propMap)
					}
				}
			}
		}
	}

	if desc, ok := prop["description"].(string); ok {
		schema.Description = desc
	}

	if enum, ok := prop["enum"].([]interface{}); ok {
		for _, e := range enum {
			if s, ok := e.(string); ok {
				schema.Enum = append(schema.Enum, s)
			}
		}
	}

	return schema
}
