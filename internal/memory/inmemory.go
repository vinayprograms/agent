// Package memory provides semantic memory storage with vector embeddings.
package memory

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemoryStore is an in-memory implementation of Store for session-scoped memory.
// All data is lost when the process exits.
type InMemoryStore struct {
	mu       sync.RWMutex
	memories map[string]*Memory
	vectors  map[string][]float32
	kv       map[string]string
	embedder EmbeddingProvider
}

// NewInMemoryStore creates a new in-memory store.
func NewInMemoryStore(embedder EmbeddingProvider) *InMemoryStore {
	return &InMemoryStore{
		memories: make(map[string]*Memory),
		vectors:  make(map[string][]float32),
		kv:       make(map[string]string),
		embedder: embedder,
	}
}

// RememberObservation stores an observation with its category and returns the ID.
func (s *InMemoryStore) RememberObservation(ctx context.Context, content, category, source string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate embedding
	embeddings, err := s.embedder.Embed(ctx, []string{content})
	if err != nil {
		return "", err
	}

	id := uuid.New().String()
	now := time.Now()

	mem := &Memory{
		ID:        id,
		Content:   content,
		Category:  category,
		Source:    source,
		CreatedAt: now,
	}

	s.memories[id] = mem
	s.vectors[id] = embeddings[0]

	return id, nil
}

// RememberFIL stores multiple observations and returns their IDs.
func (s *InMemoryStore) RememberFIL(ctx context.Context, findings, insights, lessons []string, source string) ([]string, error) {
	var ids []string

	for _, f := range findings {
		id, err := s.RememberObservation(ctx, f, "finding", source)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}

	for _, i := range insights {
		id, err := s.RememberObservation(ctx, i, "insight", source)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}

	for _, l := range lessons {
		id, err := s.RememberObservation(ctx, l, "lesson", source)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// RetrieveByID gets a single observation by ID.
func (s *InMemoryStore) RetrieveByID(ctx context.Context, id string) (*ObservationItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mem, ok := s.memories[id]
	if !ok {
		return nil, nil
	}

	return &ObservationItem{
		ID:       mem.ID,
		Content:  mem.Content,
		Category: mem.Category,
	}, nil
}

// RecallByCategory searches for memories in a specific category.
func (s *InMemoryStore) RecallByCategory(ctx context.Context, query, category string, limit int) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.memories) == 0 {
		return nil, nil
	}

	if limit <= 0 {
		limit = 5
	}

	// Generate query embedding
	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	queryVec := embeddings[0]

	// Calculate similarity for memories in category
	type scored struct {
		content string
		score   float32
	}
	var results []scored

	for id, mem := range s.memories {
		if mem.Category != category {
			continue
		}

		vec, ok := s.vectors[id]
		if !ok {
			continue
		}

		score := cosineSimilarity(queryVec, vec)
		results = append(results, scored{content: mem.Content, score: score})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	// Extract content
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.content
	}

	return out, nil
}

// RecallFIL performs semantic search and returns results grouped as FIL.
func (s *InMemoryStore) RecallFIL(ctx context.Context, query string, limitPerCategory int) (*FILResult, error) {
	if limitPerCategory <= 0 {
		limitPerCategory = 5
	}

	findings, err := s.RecallByCategory(ctx, query, "finding", limitPerCategory)
	if err != nil {
		return nil, err
	}

	insights, err := s.RecallByCategory(ctx, query, "insight", limitPerCategory)
	if err != nil {
		return nil, err
	}

	lessons, err := s.RecallByCategory(ctx, query, "lesson", limitPerCategory)
	if err != nil {
		return nil, err
	}

	return &FILResult{
		Findings: findings,
		Insights: insights,
		Lessons:  lessons,
	}, nil
}

// Recall searches for memories similar to the query (all categories).
func (s *InMemoryStore) Recall(ctx context.Context, query string, opts RecallOpts) ([]MemoryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.memories) == 0 {
		return nil, nil
	}

	// Generate query embedding
	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	queryVec := embeddings[0]

	// Calculate similarity for all memories
	var results []MemoryResult
	for id, mem := range s.memories {
		vec, ok := s.vectors[id]
		if !ok {
			continue
		}

		score := cosineSimilarity(queryVec, vec)
		if score < opts.MinScore {
			continue
		}

		// Apply time filter
		if opts.TimeRange != nil {
			if mem.CreatedAt.Before(opts.TimeRange.Start) || mem.CreatedAt.After(opts.TimeRange.End) {
				continue
			}
		}

		results = append(results, MemoryResult{
			Memory: *mem,
			Score:  score,
		})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Apply limit
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// Get retrieves a value by key (KV store).
func (s *InMemoryStore) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if val, ok := s.kv[key]; ok {
		return val, nil
	}
	return "", nil
}

// Set stores a key-value pair.
func (s *InMemoryStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.kv[key] = value
	return nil
}

// List returns keys matching the prefix.
func (s *InMemoryStore) List(prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []string
	for k := range s.kv {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// Search performs a simple substring search on KV values.
func (s *InMemoryStore) Search(query string) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query = strings.ToLower(query)
	var results []SearchResult
	for k, v := range s.kv {
		if strings.Contains(strings.ToLower(k), query) ||
			strings.Contains(strings.ToLower(v), query) {
			results = append(results, SearchResult{Key: k, Value: v})
		}
	}
	return results, nil
}

// ConsolidateSession is a no-op for in-memory store.
func (s *InMemoryStore) ConsolidateSession(ctx context.Context, sessionID string, transcript []Message) error {
	// In-memory store doesn't persist, so consolidation is meaningless
	return nil
}

// Close is a no-op for in-memory store.
func (s *InMemoryStore) Close() error {
	return nil
}

// cosineSimilarity calculates the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return float32(dotProduct / (math.Sqrt(normA) * math.Sqrt(normB)))
}
