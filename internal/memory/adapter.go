package memory

import (
	"context"
)

// ToolsAdapter adapts memory.Store to the tools.SemanticMemory interface.
type ToolsAdapter struct {
	store Store
}

// NewToolsAdapter creates a new adapter for the tools package.
func NewToolsAdapter(store Store) *ToolsAdapter {
	return &ToolsAdapter{store: store}
}

// ToolsMemoryResult mirrors tools.SemanticMemoryResult
type ToolsMemoryResult struct {
	ID       string  `json:"id"`
	Content  string  `json:"content"`
	Category string  `json:"category"` // "finding" | "insight" | "lesson"
	Score    float32 `json:"score"`
}

// ToolsObservationItem mirrors tools.ObservationItem
type ToolsObservationItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Category string `json:"category"`
}

// RememberFIL stores multiple observations and returns their IDs.
func (a *ToolsAdapter) RememberFIL(ctx context.Context, findings, insights, lessons []string, source string) ([]string, error) {
	return a.store.RememberFIL(ctx, findings, insights, lessons, source)
}

// RetrieveByID gets a single observation by ID.
func (a *ToolsAdapter) RetrieveByID(ctx context.Context, id string) (*ToolsObservationItem, error) {
	item, err := a.store.RetrieveByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	return &ToolsObservationItem{
		ID:       item.ID,
		Content:  item.Content,
		Category: item.Category,
	}, nil
}

// RecallFIL searches and returns results grouped as FIL.
func (a *ToolsAdapter) RecallFIL(ctx context.Context, query string, limitPerCategory int) (*FILResult, error) {
	return a.store.RecallFIL(ctx, query, limitPerCategory)
}

// Recall searches for relevant memories (all categories).
func (a *ToolsAdapter) Recall(ctx context.Context, query string, limit int) ([]ToolsMemoryResult, error) {
	results, err := a.store.Recall(ctx, query, RecallOpts{Limit: limit})
	if err != nil {
		return nil, err
	}

	out := make([]ToolsMemoryResult, len(results))
	for i, r := range results {
		out[i] = ToolsMemoryResult{
			ID:       r.ID,
			Content:  r.Content,
			Category: r.Category,
			Score:    r.Score,
		}
	}
	return out, nil
}

// ConsolidateSession wraps the store's consolidation.
func (a *ToolsAdapter) ConsolidateSession(ctx context.Context, sessionID string, transcript []Message) error {
	return a.store.ConsolidateSession(ctx, sessionID, transcript)
}

// Close closes the underlying store.
func (a *ToolsAdapter) Close() error {
	return a.store.Close()
}
