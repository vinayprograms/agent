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

// RememberObservation stores an observation with its category.
func (a *ToolsAdapter) RememberObservation(ctx context.Context, content, category, source string) error {
	return a.store.RememberObservation(ctx, content, category, source)
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

// Forget deletes a memory by ID.
func (a *ToolsAdapter) Forget(ctx context.Context, id string) error {
	return a.store.Forget(ctx, id)
}

// ConsolidateSession wraps the store's consolidation.
func (a *ToolsAdapter) ConsolidateSession(ctx context.Context, sessionID string, transcript []Message) error {
	return a.store.ConsolidateSession(ctx, sessionID, transcript)
}

// Close closes the underlying store.
func (a *ToolsAdapter) Close() error {
	return a.store.Close()
}
