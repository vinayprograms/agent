// Package memory provides semantic memory storage with vector embeddings.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/google/uuid"
)

// BleveStore implements Store using Bleve for BM25 search and a semantic graph for query expansion.
type BleveStore struct {
	mu sync.RWMutex

	// Bleve index for full-text search
	index bleve.Index

	// Semantic graph for query expansion
	graph *SemanticGraph

	// KV store for photographic memory
	kv     map[string]string
	kvPath string

	// Base path for all storage
	basePath string

	// Embedder for semantic graph
	embedder EmbeddingProvider
}

// BleveStoreConfig configures the Bleve-based memory store.
type BleveStoreConfig struct {
	// BasePath is the directory for all storage files
	BasePath string

	// Embedder for semantic graph (nil to disable)
	Embedder EmbeddingProvider

	// Embedding config for graph metadata
	Provider string
	Model    string
	BaseURL  string
}

// ObservationDocument represents a stored observation in Bleve.
type ObservationDocument struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Source     string    `json:"source"`
	Importance float32   `json:"importance"`
	Tags       []string  `json:"tags"`
	Keywords   []string  `json:"keywords"`
	CreatedAt  time.Time `json:"created_at"`
	AccessedAt time.Time `json:"accessed_at"`
}

// NewBleveStore creates a new Bleve-based memory store.
func NewBleveStore(cfg BleveStoreConfig) (*BleveStore, error) {
	// Ensure base directory exists
	if err := os.MkdirAll(cfg.BasePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	indexPath := filepath.Join(cfg.BasePath, "observations.bleve")
	graphPath := filepath.Join(cfg.BasePath, "semantic_graph.json")
	kvPath := filepath.Join(cfg.BasePath, "kv.json")

	// Open or create Bleve index
	var index bleve.Index
	var err error

	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		// Create new index
		indexMapping := buildIndexMapping()
		index, err = bleve.New(indexPath, indexMapping)
		if err != nil {
			return nil, fmt.Errorf("failed to create bleve index: %w", err)
		}
	} else {
		// Open existing index
		index, err = bleve.Open(indexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open bleve index: %w", err)
		}
	}

	// Create semantic graph
	graph, err := NewSemanticGraph(SemanticGraphConfig{
		Path:     graphPath,
		Embedder: cfg.Embedder,
		Provider: cfg.Provider,
		Model:    cfg.Model,
		BaseURL:  cfg.BaseURL,
	})
	if err != nil {
		index.Close()
		return nil, fmt.Errorf("failed to create semantic graph: %w", err)
	}

	store := &BleveStore{
		index:    index,
		graph:    graph,
		kv:       make(map[string]string),
		kvPath:   kvPath,
		basePath: cfg.BasePath,
		embedder: cfg.Embedder,
	}

	// Load KV store
	if err := store.loadKV(); err != nil && !os.IsNotExist(err) {
		index.Close()
		return nil, fmt.Errorf("failed to load KV store: %w", err)
	}

	return store, nil
}

// buildIndexMapping creates the Bleve index mapping.
func buildIndexMapping() mapping.IndexMapping {
	// Create a document mapping for observations
	obsMapping := bleve.NewDocumentMapping()

	// Text field mapping (analyzed)
	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Analyzer = standard.Name

	// Keyword field mapping (not analyzed)
	keywordFieldMapping := bleve.NewKeywordFieldMapping()

	// Numeric field mapping
	numericFieldMapping := bleve.NewNumericFieldMapping()

	// Date field mapping
	dateFieldMapping := bleve.NewDateTimeFieldMapping()

	obsMapping.AddFieldMappingsAt("content", textFieldMapping)
	obsMapping.AddFieldMappingsAt("source", keywordFieldMapping)
	obsMapping.AddFieldMappingsAt("importance", numericFieldMapping)
	obsMapping.AddFieldMappingsAt("tags", keywordFieldMapping)
	obsMapping.AddFieldMappingsAt("keywords", textFieldMapping)
	obsMapping.AddFieldMappingsAt("created_at", dateFieldMapping)
	obsMapping.AddFieldMappingsAt("accessed_at", dateFieldMapping)

	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultMapping = obsMapping
	indexMapping.DefaultAnalyzer = standard.Name

	return indexMapping
}

// Remember stores a memory with its keywords in the semantic graph.
func (s *BleveStore) Remember(ctx context.Context, content string, meta MemoryMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.New().String()
	now := time.Now()

	importance := meta.Importance
	if importance == 0 {
		importance = 0.5
	}

	// Extract keywords from content (simple tokenization for now)
	keywords := extractKeywords(content)

	doc := ObservationDocument{
		ID:         id,
		Content:    content,
		Source:     meta.Source,
		Importance: importance,
		Tags:       meta.Tags,
		Keywords:   keywords,
		CreatedAt:  now,
		AccessedAt: now,
	}

	// Index in Bleve
	if err := s.index.Index(id, doc); err != nil {
		return fmt.Errorf("failed to index document: %w", err)
	}

	// Add keywords to semantic graph
	if s.graph != nil && len(keywords) > 0 {
		if err := s.graph.AddTerms(ctx, keywords); err != nil {
			// Log but don't fail - semantic graph is an enhancement
		}
		// Save graph periodically (every 10 new terms)
		if s.graph.TermCount()%10 == 0 {
			s.graph.Save()
		}
	}

	return nil
}

// Recall performs semantic search for relevant memories.
func (s *BleveStore) Recall(ctx context.Context, queryText string, opts RecallOpts) ([]MemoryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	// Tokenize query
	queryTerms := extractKeywords(queryText)

	// Expand query terms using semantic graph
	var searchQuery query.Query
	if s.graph != nil && len(queryTerms) > 0 {
		expandedTerms := s.graph.ExpandQuery(queryTerms)
		searchQuery = buildExpandedQuery(queryText, expandedTerms)
	} else {
		// Simple match query without expansion
		searchQuery = bleve.NewMatchQuery(queryText)
	}

	// Create search request
	searchReq := bleve.NewSearchRequest(searchQuery)
	searchReq.Size = limit * 2 // Fetch extra for filtering
	searchReq.Fields = []string{"*"}

	// Execute search
	searchResult, err := s.index.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert results
	var results []MemoryResult
	for _, hit := range searchResult.Hits {
		// Convert score to 0-1 range (BM25 scores can be > 1)
		score := float32(hit.Score)
		if score > 1 {
			score = 1 - (1 / (1 + score)) // Normalize high scores
		}

		if score < opts.MinScore {
			continue
		}

		// Extract fields from hit
		content, _ := hit.Fields["content"].(string)
		source, _ := hit.Fields["source"].(string)
		importance, _ := hit.Fields["importance"].(float64)

		// Handle tags (may be string or []interface{})
		var tags []string
		if tagsRaw, ok := hit.Fields["tags"]; ok {
			switch t := tagsRaw.(type) {
			case string:
				tags = []string{t}
			case []interface{}:
				for _, v := range t {
					if str, ok := v.(string); ok {
						tags = append(tags, str)
					}
				}
			}
		}

		// Apply tag filter
		if len(opts.Tags) > 0 && !hasAnyTag(tags, opts.Tags) {
			continue
		}

		result := MemoryResult{
			Memory: Memory{
				ID:         hit.ID,
				Content:    content,
				Source:     source,
				Importance: float32(importance),
				Tags:       tags,
			},
			Score: score,
		}
		results = append(results, result)

		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

// buildExpandedQuery creates a boolean query with expanded terms.
func buildExpandedQuery(originalQuery string, expandedTerms map[string][]string) query.Query {
	// If no expansion happened, use simple match
	if len(expandedTerms) == 0 {
		return bleve.NewMatchQuery(originalQuery)
	}

	// Collect all queries for a disjunction
	var allQueries []query.Query

	for _, relatedTerms := range expandedTerms {
		if len(relatedTerms) == 0 {
			continue
		}
		for _, term := range relatedTerms {
			matchQuery := bleve.NewMatchQuery(term)
			allQueries = append(allQueries, matchQuery)
		}
	}

	// If no terms were added, fall back to original query
	if len(allQueries) == 0 {
		return bleve.NewMatchQuery(originalQuery)
	}

	// Use disjunction (OR) for all expanded terms
	return bleve.NewDisjunctionQuery(allQueries...)
}

// Forget deletes a memory by ID.
func (s *BleveStore) Forget(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.index.Delete(id)
}

// Get retrieves a value by key (KV store).
func (s *BleveStore) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if val, ok := s.kv[key]; ok {
		return val, nil
	}
	return "", fmt.Errorf("key not found: %s", key)
}

// Set stores a key-value pair.
func (s *BleveStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.kv[key] = value
	return s.saveKV()
}

// List returns keys matching a prefix.
func (s *BleveStore) List(prefix string) ([]string, error) {
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

// Search performs substring search on KV store.
func (s *BleveStore) Search(queryStr string) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryLower := strings.ToLower(queryStr)
	var results []SearchResult
	for k, v := range s.kv {
		if strings.Contains(strings.ToLower(k), queryLower) ||
			strings.Contains(strings.ToLower(v), queryLower) {
			results = append(results, SearchResult{Key: k, Value: v})
		}
	}
	return results, nil
}

// ConsolidateSession extracts and stores insights from a session transcript.
func (s *BleveStore) ConsolidateSession(ctx context.Context, sessionID string, transcript []Message) error {
	if len(transcript) == 0 {
		return nil
	}

	// Extract key content from the session
	var insights []string
	for _, msg := range transcript {
		content := msg.Content
		lower := strings.ToLower(content)

		// Heuristic: messages containing decision/conclusion language
		if containsAny(lower, []string{
			"decided", "conclusion", "important", "remember",
			"note that", "key insight", "learned that",
			"will use", "should use", "agreed",
		}) {
			insights = append(insights, content)
		}
	}

	// Also include the last assistant message as a summary
	for i := len(transcript) - 1; i >= 0; i-- {
		if transcript[i].Role == "assistant" && len(transcript[i].Content) > 100 {
			insights = append(insights, transcript[i].Content)
			break
		}
	}

	// Store each insight
	for _, insight := range insights {
		if len(insight) < 50 {
			continue
		}
		if len(insight) > 2000 {
			insight = insight[:2000] + "..."
		}

		s.Remember(ctx, insight, MemoryMetadata{
			Source:     "session:" + sessionID,
			Importance: 0.6,
		})
	}

	return nil
}

// Close closes the store and saves state.
func (s *BleveStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Save semantic graph
	if s.graph != nil {
		s.graph.Save()
	}

	// Save KV store
	s.saveKV()

	// Close Bleve index
	return s.index.Close()
}

// loadKV loads the KV store from disk.
func (s *BleveStore) loadKV() error {
	data, err := os.ReadFile(s.kvPath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.kv)
}

// saveKV saves the KV store to disk.
func (s *BleveStore) saveKV() error {
	data, err := json.MarshalIndent(s.kv, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.kvPath, data, 0644)
}

// RebuildSemanticGraph rebuilds the semantic graph from all indexed documents.
func (s *BleveStore) RebuildSemanticGraph(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.graph == nil || s.embedder == nil {
		return nil
	}

	// Get all documents from index
	searchReq := bleve.NewSearchRequest(bleve.NewMatchAllQuery())
	searchReq.Size = 100000 // Get all
	searchReq.Fields = []string{"keywords"}

	result, err := s.index.Search(searchReq)
	if err != nil {
		return fmt.Errorf("failed to fetch documents: %w", err)
	}

	// Collect all unique keywords
	keywordSet := make(map[string]bool)
	for _, hit := range result.Hits {
		if kwRaw, ok := hit.Fields["keywords"]; ok {
			switch kw := kwRaw.(type) {
			case string:
				keywordSet[kw] = true
			case []interface{}:
				for _, v := range kw {
					if str, ok := v.(string); ok {
						keywordSet[str] = true
					}
				}
			}
		}
	}

	// Convert to slice
	var allKeywords []string
	for k := range keywordSet {
		allKeywords = append(allKeywords, k)
	}

	// Rebuild graph
	return s.graph.RebuildFromTerms(ctx, allKeywords)
}

// extractKeywords extracts keywords from text (simple tokenization).
func extractKeywords(text string) []string {
	// Simple word extraction - in production, use proper NLP
	text = strings.ToLower(text)

	// Replace punctuation with spaces
	for _, p := range []string{".", ",", "!", "?", ":", ";", "(", ")", "[", "]", "{", "}", "\"", "'", "-", "_", "/", "\\"} {
		text = strings.ReplaceAll(text, p, " ")
	}

	// Split and filter
	words := strings.Fields(text)
	var keywords []string

	// Stop words to filter out
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "as": true, "is": true, "was": true,
		"are": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true, "did": true,
		"will": true, "would": true, "could": true, "should": true, "may": true,
		"might": true, "must": true, "can": true, "this": true, "that": true,
		"these": true, "those": true, "it": true, "its": true, "i": true, "we": true,
		"you": true, "he": true, "she": true, "they": true, "them": true,
	}

	seen := make(map[string]bool)
	for _, word := range words {
		if len(word) < 3 {
			continue
		}
		if stopWords[word] {
			continue
		}
		if seen[word] {
			continue
		}
		seen[word] = true
		keywords = append(keywords, word)
	}

	return keywords
}

// containsAny checks if text contains any of the patterns.
func containsAny(text string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}
