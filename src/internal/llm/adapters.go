package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
)

// FantasyAdapter wraps a fantasy.LanguageModel to implement our Provider interface.
type FantasyAdapter struct {
	model     fantasy.LanguageModel
	maxTokens int
}

// NewFantasyAdapter creates a new adapter wrapping a fantasy LanguageModel.
func NewFantasyAdapter(model fantasy.LanguageModel, maxTokens int) *FantasyAdapter {
	return &FantasyAdapter{
		model:     model,
		maxTokens: maxTokens,
	}
}

// Chat implements the Provider interface using fantasy's Generate method.
func (a *FantasyAdapter) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to fantasy Prompt
	var prompt fantasy.Prompt

	for _, m := range req.Messages {
		var msg fantasy.Message

		switch m.Role {
		case "system":
			msg = fantasy.NewSystemMessage(m.Content)
		case "user":
			msg = fantasy.NewUserMessage(m.Content)
		case "assistant":
			var parts []fantasy.MessagePart
			if m.Content != "" {
				parts = append(parts, fantasy.TextPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				parts = append(parts, fantasy.ToolCallPart{
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Input:      string(argsJSON),
				})
			}
			msg = fantasy.Message{
				Role:    fantasy.MessageRoleAssistant,
				Content: parts,
			}
		case "tool":
			msg = fantasy.Message{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{
						ToolCallID: m.ToolCallID,
						Output:     fantasy.ToolResultOutputContentText{Text: m.Content},
					},
				},
			}
		default:
			continue
		}

		prompt = append(prompt, msg)
	}

	// Convert tools
	var tools []fantasy.Tool
	for _, t := range req.Tools {
		tools = append(tools, fantasy.FunctionTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	maxTokens := int64(a.maxTokens)
	if req.MaxTokens > 0 {
		maxTokens = int64(req.MaxTokens)
	}

	// Build call
	call := fantasy.Call{
		Prompt:          prompt,
		Tools:           tools,
		MaxOutputTokens: &maxTokens,
	}

	// Generate
	resp, err := a.model.Generate(ctx, call)
	if err != nil {
		return nil, fmt.Errorf("fantasy generate failed: %w", err)
	}

	// Convert response
	result := &ChatResponse{
		StopReason:   string(resp.FinishReason),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
		Model:        a.model.Model(),
	}

	// Extract text and tool calls from response
	for _, content := range resp.Content {
		switch c := content.(type) {
		case *fantasy.TextContent:
			result.Content += c.Text
		case fantasy.TextContent:
			result.Content += c.Text
		case *fantasy.ToolCallContent:
			var args map[string]interface{}
			json.Unmarshal([]byte(c.Input), &args)
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:   c.ToolCallID,
				Name: c.ToolName,
				Args: args,
			})
		case fantasy.ToolCallContent:
			var args map[string]interface{}
			json.Unmarshal([]byte(c.Input), &args)
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:   c.ToolCallID,
				Name: c.ToolName,
				Args: args,
			})
		}
	}

	return result, nil
}

// NewProvider creates a provider based on the configuration using fantasy.
func NewProvider(cfg FantasyConfig) (Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()

	var fantasyProvider fantasy.Provider
	var err error

	switch cfg.Provider {
	case "anthropic":
		fantasyProvider, err = anthropic.New(anthropic.WithAPIKey(cfg.APIKey))
	case "openai":
		fantasyProvider, err = openai.New(openai.WithAPIKey(cfg.APIKey))
	case "google":
		fantasyProvider, err = google.New(google.WithGeminiAPIKey(cfg.APIKey))
	case "groq":
		fantasyProvider, err = openaicompat.New(
			openaicompat.WithBaseURL("https://api.groq.com/openai/v1"),
			openaicompat.WithAPIKey(cfg.APIKey),
			openaicompat.WithName("groq"),
		)
	case "mistral":
		fantasyProvider, err = openaicompat.New(
			openaicompat.WithBaseURL("https://api.mistral.ai/v1"),
			openaicompat.WithAPIKey(cfg.APIKey),
			openaicompat.WithName("mistral"),
		)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create %s provider: %w", cfg.Provider, err)
	}

	model, err := fantasyProvider.LanguageModel(context.Background(), cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to get model %s: %w", cfg.Model, err)
	}

	return NewFantasyAdapter(model, cfg.MaxTokens), nil
}

// NewFantasyProvider is an alias for NewProvider.
func NewFantasyProvider(cfg FantasyConfig) (Provider, error) {
	return NewProvider(cfg)
}
