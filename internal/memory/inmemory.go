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
	mu        sync.RWMutex
	memories  map[string]*Memory
	vectors   map[string][]float32
	kv        map[string]string
	embedder  EmbeddingProvider
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

// Remember stores content with its embedding.
func (s *InMemoryStore) Remember(ctx context.Context, content string, meta MemoryMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate embedding
	embeddings, err := s.embedder.Embed(ctx, []string{content})
	if err != nil {
		return err
	}

	id := uuid.New().String()
	now := time.Now()

	importance := meta.Importance
	if importance == 0 {
		importance = 0.5
	}

	mem := &Memory{
		ID:          id,
		Content:     content,
		Source:      meta.Source,
		Importance:  importance,
		CreatedAt:   now,
		AccessedAt:  now,
		AccessCount: 0,
		Tags:        meta.Tags,
	}

	s.memories[id] = mem
	s.vectors[id] = embeddings[0]

	return nil
}

// Recall searches for memories similar to the query.
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

		// Apply tag filter
		if len(opts.Tags) > 0 && !hasAnyTag(mem.Tags, opts.Tags) {
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

	// Update access times
	s.mu.RUnlock()
	s.mu.Lock()
	for _, r := range results {
		if mem, ok := s.memories[r.ID]; ok {
			mem.AccessedAt = time.Now()
			mem.AccessCount++
		}
	}
	s.mu.Unlock()
	s.mu.RLock()

	return results, nil
}

// Forget removes a memory by ID.
func (s *InMemoryStore) Forget(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.memories, id)
	delete(s.vectors, id)
	return nil
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

// hasAnyTag checks if the memory has any of the filter tags.
func hasAnyTag(memTags, filterTags []string) bool {
	tagSet := make(map[string]bool)
	for _, t := range memTags {
		tagSet[t] = true
	}
	for _, t := range filterTags {
		if tagSet[t] {
			return true
		}
	}
	return false
}
