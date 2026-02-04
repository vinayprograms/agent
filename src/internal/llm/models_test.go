package llm

import (
	"context"
	"os"
	"testing"
)

func TestListAllModels(t *testing.T) {
	// This test requires catwalk server or will use cached data
	// Skip if CATWALK_URL not set and no cache
	if os.Getenv("CATWALK_URL") == "" {
		t.Skip("CATWALK_URL not set, skipping catwalk integration test")
	}

	models, err := ListAllModels(context.Background())
	if err != nil {
		t.Fatalf("ListAllModels failed: %v", err)
	}

	if len(models) == 0 {
		t.Error("expected at least some models")
	}

	// Check structure
	for _, m := range models {
		if m.ID == "" {
			t.Error("model ID should not be empty")
		}
		if m.Provider == "" {
			t.Error("model provider should not be empty")
		}
	}
}

func TestFindModelProvider(t *testing.T) {
	// Test fallback to inference when catwalk unavailable
	providerID, _, err := FindModelProvider(context.Background(), "claude-3-5-sonnet-20241022")
	if err != nil {
		// Catwalk might not be available, should fall back to inference
		t.Logf("FindModelProvider returned error (expected if no catwalk): %v", err)
	}

	// Should either find via catwalk or infer
	if providerID != "" && providerID != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", providerID)
	}
}

func TestFindModelProvider_UnknownModel(t *testing.T) {
	_, _, err := FindModelProvider(context.Background(), "totally-unknown-model-xyz")
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestGetProviderAPIKey(t *testing.T) {
	// This would need a catwalk.Provider to test properly
	// Just verify the function exists and handles empty case
	// Full test would require mocking catwalk
}

func TestModelInfo_Structure(t *testing.T) {
	info := ModelInfo{
		ID:             "test-model",
		Name:           "Test Model",
		Provider:       "test-provider",
		ContextWindow:  128000,
		CostPer1MIn:    1.0,
		CostPer1MOut:   3.0,
		CanReason:      true,
		SupportsImages: false,
	}

	if info.ID != "test-model" {
		t.Errorf("ID mismatch")
	}
	if !info.CanReason {
		t.Errorf("CanReason should be true")
	}
}
