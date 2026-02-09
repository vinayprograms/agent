package memory

import (
	"context"
	"os"
	"path/filepath"
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

func TestSQLiteStore_RememberRecall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "memory-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "memory.db")
	embedder := NewMockEmbedder(128)

	store, err := NewSQLiteStore(SQLiteConfig{
		Path:     dbPath,
		Embedder: embedder,
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

func TestSQLiteStore_KeyValue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "memory-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "memory.db")
	embedder := NewMockEmbedder(128)

	store, err := NewSQLiteStore(SQLiteConfig{
		Path:     dbPath,
		Embedder: embedder,
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

func TestSQLiteStore_Forget(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "memory-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "memory.db")
	embedder := NewMockEmbedder(128)

	store, err := NewSQLiteStore(SQLiteConfig{
		Path:     dbPath,
		Embedder: embedder,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Remember something
	err = store.Remember(ctx, "Test memory to forget", MemoryMetadata{
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

func TestSQLiteStore_Consolidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "memory-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "memory.db")
	embedder := NewMockEmbedder(128)

	store, err := NewSQLiteStore(SQLiteConfig{
		Path:     dbPath,
		Embedder: embedder,
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create a transcript with important content
	transcript := []Message{
		{Role: "user", Content: "What database should we use?"},
		{Role: "assistant", Content: "I recommend PostgreSQL for this use case because it has excellent JSON support and is widely used."},
		{Role: "user", Content: "OK, let's go with that. Remember that we decided on PostgreSQL."},
		{Role: "assistant", Content: "Noted. We have decided to use PostgreSQL as our database. This is an important architectural decision that affects our data layer design."},
	}

	// Consolidate
	err = store.ConsolidateSession(ctx, "test-session-123", transcript)
	if err != nil {
		t.Fatalf("consolidation failed: %v", err)
	}

	// Should be able to recall the decision
	results, err := store.Recall(ctx, "database decision", RecallOpts{Limit: 5})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}

	// Should have extracted some insights
	if len(results) == 0 {
		t.Error("expected at least 1 consolidated memory")
	}
}
