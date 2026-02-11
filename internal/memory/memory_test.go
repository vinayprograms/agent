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

func TestInMemoryStore_RememberObservation(t *testing.T) {
	embedder := NewMockEmbedder(128)
	store := NewInMemoryStore(embedder)

	ctx := context.Background()

	// Remember observations
	err := store.RememberObservation(ctx, "The user prefers dark mode", "finding", "explicit")
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	err = store.RememberObservation(ctx, "PostgreSQL is best for JSON", "insight", "session:123")
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	err = store.RememberObservation(ctx, "Always validate input", "lesson", "session:123")
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	// Recall - should find results
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
		if r.Category == "" {
			t.Error("result should have category")
		}
		if r.Score < 0 || r.Score > 1 {
			t.Errorf("score should be 0-1, got %f", r.Score)
		}
	}
}

func TestInMemoryStore_RecallFIL(t *testing.T) {
	embedder := NewMockEmbedder(128)
	store := NewInMemoryStore(embedder)

	ctx := context.Background()

	// Store observations in each category
	store.RememberObservation(ctx, "API rate limit is 100 per minute", "finding", "test")
	store.RememberObservation(ctx, "REST is simpler than GraphQL", "insight", "test")
	store.RememberObservation(ctx, "Always check rate limits", "lesson", "test")

	// Recall as FIL
	fil, err := store.RecallFIL(ctx, "API rate", 5)
	if err != nil {
		t.Fatalf("recall FIL failed: %v", err)
	}

	if fil == nil {
		t.Fatal("expected FIL result")
	}

	// Should have findings about API
	if len(fil.Findings) == 0 {
		t.Error("expected at least 1 finding")
	}

	t.Logf("Findings: %v", fil.Findings)
	t.Logf("Insights: %v", fil.Insights)
	t.Logf("Lessons: %v", fil.Lessons)
}

func TestInMemoryStore_RecallByCategory(t *testing.T) {
	embedder := NewMockEmbedder(128)
	store := NewInMemoryStore(embedder)

	ctx := context.Background()

	// Store mixed observations
	store.RememberObservation(ctx, "Database uses PostgreSQL", "finding", "test")
	store.RememberObservation(ctx, "Database should be indexed", "lesson", "test")
	store.RememberObservation(ctx, "Database performance is good", "insight", "test")

	// Recall only findings
	findings, err := store.RecallByCategory(ctx, "database", "finding", 5)
	if err != nil {
		t.Fatalf("recall by category failed: %v", err)
	}

	if len(findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(findings))
	}

	// Recall only lessons
	lessons, err := store.RecallByCategory(ctx, "database", "lesson", 5)
	if err != nil {
		t.Fatalf("recall by category failed: %v", err)
	}

	if len(lessons) != 1 {
		t.Errorf("expected 1 lesson, got %d", len(lessons))
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
	err := store.RememberObservation(ctx, "Test memory to forget", "finding", "test")
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
