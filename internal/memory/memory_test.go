package memory

import (
	"context"
	"testing"
)

func TestMockEmbedder(t *testing.T) {
	embedder := NewMockEmbedder(384)

	embeddings, err := embedder.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("embed failed: %v", err)
	}

	if len(embeddings) != 2 {
		t.Errorf("expected 2 embeddings, got %d", len(embeddings))
	}

	if len(embeddings[0]) != 384 {
		t.Errorf("expected dimension 384, got %d", len(embeddings[0]))
	}

	// Same input should produce same embedding (deterministic)
	embeddings2, _ := embedder.Embed(context.Background(), []string{"hello"})
	for i := 0; i < len(embeddings[0]); i++ {
		if embeddings[0][i] != embeddings2[0][i] {
			t.Error("mock embedder should be deterministic")
			break
		}
	}
}

func TestInMemoryStore_RememberRecall(t *testing.T) {
	embedder := NewMockEmbedder(128)
	store := NewInMemoryStore(embedder)

	ctx := context.Background()

	// Remember something
	err := store.Remember(ctx, "The user prefers dark mode and vim keybindings", MemoryMetadata{
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

func TestInMemoryStore_KeyValue(t *testing.T) {
	embedder := NewMockEmbedder(128)
	store := NewInMemoryStore(embedder)

	// Set and get
	err := store.Set("user.name", "Alice")
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

func TestInMemoryStore_Forget(t *testing.T) {
	embedder := NewMockEmbedder(128)
	store := NewInMemoryStore(embedder)

	ctx := context.Background()

	// Remember something
	err := store.Remember(ctx, "Test memory to forget", MemoryMetadata{
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
	results, err = store.Recall(ctx, "forget", RecallOpts{Limit: 1})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	for _, r := range results {
		if r.ID == id {
			t.Error("memory should have been forgotten")
		}
	}
}

func TestInMemoryStore_TagFilter(t *testing.T) {
	embedder := NewMockEmbedder(128)
	store := NewInMemoryStore(embedder)

	ctx := context.Background()

	// Remember with tags
	err := store.Remember(ctx, "User prefers dark mode", MemoryMetadata{
		Source: "explicit",
		Tags:   []string{"preferences", "ui"},
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	err = store.Remember(ctx, "Database is PostgreSQL", MemoryMetadata{
		Source: "decision",
		Tags:   []string{"architecture", "database"},
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	// Recall with tag filter
	results, err := store.Recall(ctx, "user", RecallOpts{
		Limit: 10,
		Tags:  []string{"preferences"},
	})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}

	// Should only find the preferences entry
	for _, r := range results {
		found := false
		for _, tag := range r.Tags {
			if tag == "preferences" {
				found = true
				break
			}
		}
		if !found {
			t.Error("result should have 'preferences' tag")
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Test identical vectors
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	if sim < 0.999 {
		t.Errorf("identical vectors should have similarity ~1, got %f", sim)
	}

	// Test orthogonal vectors
	a = []float32{1, 0, 0}
	b = []float32{0, 1, 0}
	sim = cosineSimilarity(a, b)
	if sim > 0.001 {
		t.Errorf("orthogonal vectors should have similarity ~0, got %f", sim)
	}

	// Test opposite vectors
	a = []float32{1, 0, 0}
	b = []float32{-1, 0, 0}
	sim = cosineSimilarity(a, b)
	if sim > -0.999 {
		t.Errorf("opposite vectors should have similarity ~-1, got %f", sim)
	}
}
