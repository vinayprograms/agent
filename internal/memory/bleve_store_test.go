package memory

import (
	"context"
	"os"
	"testing"
)

func TestBleveStore_RememberRecall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	embedder := NewMockEmbedder(128)

	store, err := NewBleveStore(BleveStoreConfig{
		BasePath: tmpDir,
		Embedder: embedder,
		Provider: "mock",
		Model:    "mock-model",
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Remember something
	err = store.Remember(ctx, "The user prefers dark mode and vim keybindings", MemoryMetadata{
		Source:     "explicit",
		Importance: 0.8,
		Tags:       []string{"preferences"},
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	// Remember another thing
	err = store.Remember(ctx, "We decided to use PostgreSQL for the database", MemoryMetadata{
		Source:     "session:123",
		Importance: 0.7,
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	// Recall - should find both
	results, err := store.Recall(ctx, "user preferences", RecallOpts{Limit: 10})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}

	if len(results) < 1 {
		t.Error("expected at least 1 result")
	}

	// Verify the results have required fields
	for _, r := range results {
		if r.ID == "" {
			t.Error("result should have ID")
		}
		if r.Content == "" {
			t.Error("result should have content")
		}
		if r.Score < 0 || r.Score > 1 {
			t.Errorf("score should be 0-1, got %f", r.Score)
		}
	}
}

func TestBleveStore_KeyValue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewBleveStore(BleveStoreConfig{
		BasePath: tmpDir,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Set and get
	err = store.Set("user.name", "Alice")
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}

	value, err := store.Get("user.name")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if value != "Alice" {
		t.Errorf("expected 'Alice', got '%s'", value)
	}

	// List
	store.Set("user.email", "alice@example.com")
	store.Set("project.name", "MyProject")

	keys, err := store.List("user.")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}

	// Search
	results, err := store.Search("example.com")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestBleveStore_Forget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	embedder := NewMockEmbedder(128)

	store, err := NewBleveStore(BleveStoreConfig{
		BasePath: tmpDir,
		Embedder: embedder,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Remember something
	err = store.Remember(ctx, "Test memory to forget about later", MemoryMetadata{
		Source: "test",
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	// Recall to get the ID
	results, err := store.Recall(ctx, "forget", RecallOpts{Limit: 1})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	id := results[0].ID

	// Forget
	err = store.Forget(ctx, id)
	if err != nil {
		t.Fatalf("forget failed: %v", err)
	}

	// Should not find it anymore
	results, err = store.Recall(ctx, "forget", RecallOpts{Limit: 10})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	for _, r := range results {
		if r.ID == id {
			t.Error("memory should have been forgotten")
		}
	}
}

func TestBleveStore_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	embedder := NewMockEmbedder(128)
	ctx := context.Background()

	// Create store and add data
	store1, err := NewBleveStore(BleveStoreConfig{
		BasePath: tmpDir,
		Embedder: embedder,
		Provider: "mock",
		Model:    "mock-model",
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	err = store1.Remember(ctx, "This should persist across restarts", MemoryMetadata{
		Source: "test",
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	err = store1.Set("persistent.key", "persistent.value")
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}

	store1.Close()

	// Reopen store and verify data
	store2, err := NewBleveStore(BleveStoreConfig{
		BasePath: tmpDir,
		Embedder: embedder,
		Provider: "mock",
		Model:    "mock-model",
	})
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	defer store2.Close()

	// Check semantic memory
	results, err := store2.Recall(ctx, "persist", RecallOpts{Limit: 10})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected persisted memory to survive restart")
	}

	// Check KV store
	value, err := store2.Get("persistent.key")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if value != "persistent.value" {
		t.Errorf("expected 'persistent.value', got '%s'", value)
	}
}

func TestSemanticGraph_ExpandQuery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "graph-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	embedder := NewMockEmbedder(128)
	ctx := context.Background()

	graph, err := NewSemanticGraph(SemanticGraphConfig{
		Embedder:            embedder,
		SimilarityThreshold: 0.5, // Lower threshold for mock embeddings
	})
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}

	// Add related terms
	err = graph.AddTerms(ctx, []string{"fast", "speed", "performance"})
	if err != nil {
		t.Fatalf("add terms failed: %v", err)
	}

	// Expand query
	expanded := graph.ExpandQuery([]string{"fast"})

	if len(expanded["fast"]) < 1 {
		t.Error("expected at least the original term")
	}

	// Verify original term is included
	found := false
	for _, term := range expanded["fast"] {
		if term == "fast" {
			found = true
			break
		}
	}
	if !found {
		t.Error("original term should be in expansion")
	}
}

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		input    string
		expected int // minimum expected keywords
	}{
		{"The user prefers dark mode", 3}, // user, prefers, dark, mode
		{"PostgreSQL database decision", 3},
		{"a the an", 0}, // all stop words
		{"", 0},
	}

	for _, tc := range tests {
		keywords := extractKeywords(tc.input)
		if len(keywords) < tc.expected {
			t.Errorf("extractKeywords(%q): expected at least %d keywords, got %d: %v",
				tc.input, tc.expected, len(keywords), keywords)
		}
	}
}
