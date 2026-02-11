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

	// Remember observations with categories - now returns ID
	_, err = store.RememberObservation(ctx, "The user prefers dark mode and vim keybindings", "finding", "explicit")
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	_, err = store.RememberObservation(ctx, "We decided to use PostgreSQL for the database", "insight", "session:123")
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	_, err = store.RememberObservation(ctx, "Avoid using deprecated APIs", "lesson", "session:123")
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
		if r.Score < 0 || r.Score > 1 {
			t.Errorf("score should be 0-1, got %f", r.Score)
		}
	}
}

func TestBleveStore_RecallFIL(t *testing.T) {
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

	// Store observations in each category
	store.RememberObservation(ctx, "API rate limit is 100 per minute", "finding", "GOAL:research")
	store.RememberObservation(ctx, "Authentication uses OAuth2", "finding", "GOAL:research")
	store.RememberObservation(ctx, "REST is better than GraphQL for this case", "insight", "GOAL:design")
	store.RememberObservation(ctx, "Avoid library X - no TypeScript support", "lesson", "GOAL:coding")
	store.RememberObservation(ctx, "Always validate API responses", "lesson", "GOAL:coding")

	// Recall as FIL
	fil, err := store.RecallFIL(ctx, "API", 5)
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

	// Check structure is correct
	t.Logf("Findings: %v", fil.Findings)
	t.Logf("Insights: %v", fil.Insights)
	t.Logf("Lessons: %v", fil.Lessons)
}

func TestBleveStore_RecallByCategory(t *testing.T) {
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

func TestBleveStore_RetrieveByID(t *testing.T) {
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

	// Remember something - now returns ID
	id, err := store.RememberObservation(ctx, "Test memory to retrieve later", "finding", "test")
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	// Retrieve by ID
	item, err := store.RetrieveByID(ctx, id)
	if err != nil {
		t.Fatalf("retrieve failed: %v", err)
	}
	if item == nil {
		t.Fatal("expected item")
	}

	if item.ID != id {
		t.Errorf("ID mismatch: got %s, want %s", item.ID, id)
	}
	if item.Content != "Test memory to retrieve later" {
		t.Errorf("content mismatch: got %s", item.Content)
	}
	if item.Category != "finding" {
		t.Errorf("category mismatch: got %s", item.Category)
	}

	// Retrieve non-existent
	item, err = store.RetrieveByID(ctx, "non-existent-id")
	if err != nil {
		t.Fatalf("retrieve should not error for missing: %v", err)
	}
	if item != nil {
		t.Error("expected nil for non-existent ID")
	}
}

func TestBleveStore_RememberFIL(t *testing.T) {
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

	// Remember using RememberFIL
	ids, err := store.RememberFIL(ctx,
		[]string{"API rate limit is 100 per minute", "Uses OAuth2"},
		[]string{"REST is better for this case"},
		[]string{"Always validate responses"},
		"test",
	)
	if err != nil {
		t.Fatalf("remember FIL failed: %v", err)
	}

	if len(ids) != 4 {
		t.Errorf("expected 4 IDs, got %d", len(ids))
	}

	// Verify each ID can be retrieved
	for _, id := range ids {
		item, err := store.RetrieveByID(ctx, id)
		if err != nil {
			t.Fatalf("retrieve failed for %s: %v", id, err)
		}
		if item == nil {
			t.Errorf("expected item for ID %s", id)
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

	_, err = store1.RememberObservation(ctx, "This should persist across restarts", "insight", "test")
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

func TestObservationStore_StoreFIL(t *testing.T) {
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

	ctx := context.Background()
	obsStore := NewBleveObservationStore(store)

	// Store an observation
	obs := &Observation{
		Findings: []string{"API uses REST", "Rate limit is 100/min"},
		Insights: []string{"REST is simpler than GraphQL"},
		Lessons:  []string{"Always check rate limits"},
		StepName: "research",
		StepType: "GOAL",
	}

	err = obsStore.StoreObservation(ctx, obs)
	if err != nil {
		t.Fatalf("store observation failed: %v", err)
	}

	// Query back as FIL
	results, err := obsStore.QueryRelevantObservations(ctx, "API", 5)
	if err != nil {
		t.Fatalf("query observations failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results")
	}

	// Should be an Observation
	resultObs, ok := results[0].(*Observation)
	if !ok {
		t.Fatal("expected Observation type")
	}

	if len(resultObs.Findings) == 0 {
		t.Error("expected findings")
	}

	t.Logf("Retrieved FIL: F=%d I=%d L=%d",
		len(resultObs.Findings), len(resultObs.Insights), len(resultObs.Lessons))
}
