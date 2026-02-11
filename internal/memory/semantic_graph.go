// Package memory provides semantic memory storage with vector embeddings.
package memory

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"sync"
	"time"
)

// SemanticGraph stores term relationships and embeddings for query expansion.
type SemanticGraph struct {
	mu sync.RWMutex

	// Metadata for detecting config changes
	Meta GraphMeta `json:"meta"`

	// Terms maps each term to its data
	Terms map[string]*TermData `json:"terms"`

	// Path to persist the graph
	path string

	// Embedder for generating embeddings
	embedder EmbeddingProvider

	// Similarity threshold for creating relationships
	similarityThreshold float32
}

// GraphMeta stores metadata about the embedding configuration.
type GraphMeta struct {
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	BaseURL    string    `json:"base_url,omitempty"`
	Dimensions int       `json:"dimensions"`
	BuiltAt    time.Time `json:"built_at"`
}

// TermData stores a term's embedding and related terms.
type TermData struct {
	Embedding []float32 `json:"embedding"`
	Related   []string  `json:"related"`
}

// SemanticGraphConfig configures the semantic graph.
type SemanticGraphConfig struct {
	Path                string
	Embedder            EmbeddingProvider
	Provider            string
	Model               string
	BaseURL             string
	SimilarityThreshold float32 // Default: 0.7
}

// NewSemanticGraph creates or loads a semantic graph.
func NewSemanticGraph(cfg SemanticGraphConfig) (*SemanticGraph, error) {
	threshold := cfg.SimilarityThreshold
	if threshold == 0 {
		threshold = 0.7
	}

	graph := &SemanticGraph{
		Terms:               make(map[string]*TermData),
		path:                cfg.Path,
		embedder:            cfg.Embedder,
		similarityThreshold: threshold,
	}

	// Try to load existing graph
	if cfg.Path != "" {
		if err := graph.load(); err == nil {
			// Check if config changed
			if graph.Meta.Provider != cfg.Provider ||
				graph.Meta.Model != cfg.Model ||
				graph.Meta.BaseURL != cfg.BaseURL {
				// Config changed, need to rebuild
				graph.Terms = make(map[string]*TermData)
				graph.Meta = GraphMeta{
					Provider: cfg.Provider,
					Model:    cfg.Model,
					BaseURL:  cfg.BaseURL,
				}
			}
		} else {
			// New graph
			graph.Meta = GraphMeta{
				Provider: cfg.Provider,
				Model:    cfg.Model,
				BaseURL:  cfg.BaseURL,
			}
			if cfg.Embedder != nil {
				graph.Meta.Dimensions = cfg.Embedder.Dimension()
			}
		}
	}

	return graph, nil
}

// load loads the graph from disk.
func (g *SemanticGraph) load() error {
	data, err := os.ReadFile(g.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, g)
}

// Save persists the graph to disk.
func (g *SemanticGraph) Save() error {
	if g.path == "" {
		return nil
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	g.Meta.BuiltAt = time.Now()

	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(g.path, data, 0644)
}

// AddTerms adds new terms and establishes relationships.
func (g *SemanticGraph) AddTerms(ctx context.Context, terms []string) error {
	if g.embedder == nil {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Filter to new terms only
	var newTerms []string
	for _, t := range terms {
		if _, exists := g.Terms[t]; !exists {
			newTerms = append(newTerms, t)
		}
	}

	if len(newTerms) == 0 {
		return nil
	}

	// Generate embeddings for new terms
	embeddings, err := g.embedder.Embed(ctx, newTerms)
	if err != nil {
		return err
	}

	// Add new terms
	for i, term := range newTerms {
		g.Terms[term] = &TermData{
			Embedding: embeddings[i],
			Related:   []string{},
		}
	}

	// Find relationships between new terms and all existing terms
	for _, newTerm := range newTerms {
		newData := g.Terms[newTerm]
		for existingTerm, existingData := range g.Terms {
			if existingTerm == newTerm {
				continue
			}

			// Calculate similarity
			sim := cosineSimilarity(newData.Embedding, existingData.Embedding)
			if sim >= g.similarityThreshold {
				// Add bidirectional relationship
				if !contains(newData.Related, existingTerm) {
					newData.Related = append(newData.Related, existingTerm)
				}
				if !contains(existingData.Related, newTerm) {
					existingData.Related = append(existingData.Related, newTerm)
				}
			}
		}
	}

	return nil
}

// ExpandQuery expands query terms with related terms (single hop).
func (g *SemanticGraph) ExpandQuery(terms []string) map[string][]string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make(map[string][]string)
	for _, term := range terms {
		if data, exists := g.Terms[term]; exists {
			result[term] = append([]string{term}, data.Related...)
		} else {
			result[term] = []string{term}
		}
	}
	return result
}

// GetRelated returns related terms for a single term.
func (g *SemanticGraph) GetRelated(term string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if data, exists := g.Terms[term]; exists {
		return data.Related
	}
	return nil
}

// TermCount returns the number of terms in the graph.
func (g *SemanticGraph) TermCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.Terms)
}

// Clear removes all terms from the graph.
func (g *SemanticGraph) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Terms = make(map[string]*TermData)
}

// RebuildFromTerms rebuilds the graph from a list of terms.
func (g *SemanticGraph) RebuildFromTerms(ctx context.Context, terms []string) error {
	g.Clear()
	return g.AddTerms(ctx, terms)
}

// cosineSimilarityFloat32 calculates cosine similarity between two float32 vectors.
func cosineSimilarityFloat32(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
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

// contains checks if a slice contains a string.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
