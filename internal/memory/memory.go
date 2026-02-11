// Package memory provides semantic memory storage with vector embeddings.
package memory

import (
	"context"
	"time"
)

// Memory represents a stored memory with metadata.
type Memory struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Category  string    `json:"category,omitempty"` // "finding" | "insight" | "lesson"
	Source    string    `json:"source"`             // "GOAL:step-name", "session:xyz", etc.
	CreatedAt time.Time `json:"created_at"`
	Tags      []string  `json:"tags,omitempty"` // Deprecated: use Category
}

// MemoryResult is a memory with relevance score from search.
type MemoryResult struct {
	Memory
	Score float32 `json:"score"` // similarity score 0-1
}

// MemoryMetadata holds metadata for creating a memory.
// Deprecated: Use RememberObservation(content, category, source) instead.
type MemoryMetadata struct {
	Source     string   // "GOAL:step-name", "session:xyz", etc.
	Importance float32  // Deprecated: category implies importance
	Tags       []string // Deprecated: first tag used as category for compatibility
}

// RecallOpts configures memory recall.
type RecallOpts struct {
	Limit     int        // max results, default 10
	MinScore  float32    // minimum similarity score, default 0.0
	TimeRange *TimeRange // optional time filter
	Tags      []string   // Deprecated: use RecallByCategory or RecallFIL
}

// TimeRange represents a time window for filtering.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Message represents a conversation message for consolidation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SearchResult is for key-based search (legacy compatibility).
type SearchResult struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Store is the interface for memory storage.
type Store interface {
	// Semantic memory operations
	Remember(ctx context.Context, content string, meta MemoryMetadata) error
	Recall(ctx context.Context, query string, opts RecallOpts) ([]MemoryResult, error)
	Forget(ctx context.Context, id string) error

	// Key-value operations (backward compatibility)
	Get(key string) (string, error)
	Set(key, value string) error
	List(prefix string) ([]string, error)
	Search(query string) ([]SearchResult, error)

	// Session consolidation
	ConsolidateSession(ctx context.Context, sessionID string, transcript []Message) error

	// Lifecycle
	Close() error
}

// EmbeddingProvider generates vector embeddings for text.
type EmbeddingProvider interface {
	// Embed generates embeddings for the given texts.
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the embedding dimension.
	Dimension() int
}

// Consolidator extracts insights from session transcripts.
type Consolidator interface {
	// Extract extracts key insights from a transcript.
	Extract(ctx context.Context, transcript []Message) ([]string, error)
}
