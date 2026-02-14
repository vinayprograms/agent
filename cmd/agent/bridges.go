package main

import (
	"context"

	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
)

// semanticMemoryBridge bridges memory.ToolsAdapter to tools.SemanticMemory interface.
type semanticMemoryBridge struct {
	adapter *memory.ToolsAdapter
}

func (b *semanticMemoryBridge) RememberFIL(ctx context.Context, findings, insights, lessons []string, source string) ([]string, error) {
	return b.adapter.RememberFIL(ctx, findings, insights, lessons, source)
}

func (b *semanticMemoryBridge) RetrieveByID(ctx context.Context, id string) (*tools.ObservationItem, error) {
	item, err := b.adapter.RetrieveByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	return &tools.ObservationItem{
		ID:       item.ID,
		Content:  item.Content,
		Category: item.Category,
	}, nil
}

func (b *semanticMemoryBridge) RecallFIL(ctx context.Context, query string, limitPerCategory int) (*tools.FILResult, error) {
	result, err := b.adapter.RecallFIL(ctx, query, limitPerCategory)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return &tools.FILResult{
		Findings: result.Findings,
		Insights: result.Insights,
		Lessons:  result.Lessons,
	}, nil
}

func (b *semanticMemoryBridge) Recall(ctx context.Context, query string, limit int) ([]tools.SemanticMemoryResult, error) {
	results, err := b.adapter.Recall(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]tools.SemanticMemoryResult, len(results))
	for i, r := range results {
		out[i] = tools.SemanticMemoryResult{
			ID:       r.ID,
			Content:  r.Content,
			Category: r.Category,
			Score:    r.Score,
		}
	}
	return out, nil
}

// llmGenerateAdapter adapts llm.Provider to policy.LLMProvider for bash policy checking.
type llmGenerateAdapter struct {
	provider llm.Provider
}

func (a *llmGenerateAdapter) Generate(ctx context.Context, prompt string) (*policy.GenerateResult, error) {
	resp, err := a.provider.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, err
	}
	return &policy.GenerateResult{
		Content:      resp.Content,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}
