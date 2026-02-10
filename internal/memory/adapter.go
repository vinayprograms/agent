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

// ToolsMemoryMeta mirrors tools.SemanticMemoryMeta
type ToolsMemoryMeta struct {
	Source     string
	Importance float32
	Tags       []string
}

// ToolsMemoryResult mirrors tools.SemanticMemoryResult
type ToolsMemoryResult struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Score   float32  `json:"score"`
	Tags    []string `json:"tags,omitempty"`
}

// Remember stores a memory.
func (a *ToolsAdapter) Remember(ctx context.Context, content string, meta ToolsMemoryMeta) error {
	return a.store.Remember(ctx, content, MemoryMetadata{
		Source:     meta.Source,
		Importance: meta.Importance,
		Tags:       meta.Tags,
	})
}

// Recall searches for relevant memories.
func (a *ToolsAdapter) Recall(ctx context.Context, query string, limit int) ([]ToolsMemoryResult, error) {
	results, err := a.store.Recall(ctx, query, RecallOpts{Limit: limit})
	if err != nil {
		return nil, err
	}

	out := make([]ToolsMemoryResult, len(results))
	for i, r := range results {
		out[i] = ToolsMemoryResult{
			ID:      r.ID,
			Content: r.Content,
			Score:   r.Score,
			Tags:    r.Tags,
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
