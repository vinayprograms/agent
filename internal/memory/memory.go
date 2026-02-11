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
	Category  string    `json:"category"` // "finding" | "insight" | "lesson"
	Source    string    `json:"source"`   // "GOAL:step-name", "session:xyz", etc.
	CreatedAt time.Time `json:"created_at"`
}

// MemoryResult is a memory with relevance score from search.
type MemoryResult struct {
	Memory
	Score float32 `json:"score"` // similarity score 0-1
}

// TimeRange represents a time window for filtering.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// RecallOpts configures memory recall.
type RecallOpts struct {
	Limit     int        // max results, default 10
	MinScore  float32    // minimum similarity score, default 0.0
	TimeRange *TimeRange // optional time filter
}

// Message represents a conversation message for consolidation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SearchResult is for key-based search.
type SearchResult struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Store is the interface for memory storage.
type Store interface {
	// Observation storage (primary API)
	RememberObservation(ctx context.Context, content, category, source string) error
	RecallByCategory(ctx context.Context, query, category string, limit int) ([]string, error)
	RecallFIL(ctx context.Context, query string, limitPerCategory int) (*FILResult, error)

	// Generic recall (returns all categories mixed)
	Recall(ctx context.Context, query string, opts RecallOpts) ([]MemoryResult, error)
	Forget(ctx context.Context, id string) error

	// Key-value operations
	Get(key string) (string, error)
	Set(key, value string) error
	List(prefix string) ([]string, error)
	Search(query string) ([]SearchResult, error)

	// Session consolidation
	ConsolidateSession(ctx context.Context, sessionID string, transcript []Message) error

	// Lifecycle
	Close() error
}

// FILResult holds categorized observation results.
type FILResult struct {
	Findings []string `json:"findings"`
	Insights []string `json:"insights"`
	Lessons  []string `json:"lessons"`
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
