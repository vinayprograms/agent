package llm

import (
	"context"
	"fmt"
	"os"
	"sync"

	"charm.land/catwalk/pkg/catwalk"
)

var (
	providersCache []catwalk.Provider
	providersMu    sync.RWMutex
	cacheLoaded    bool
)

// GetProviders returns all available providers from catwalk.
// It caches the result after first fetch.
func GetProviders(ctx context.Context) ([]catwalk.Provider, error) {
	providersMu.RLock()
	if cacheLoaded {
		defer providersMu.RUnlock()
		return providersCache, nil
	}
	providersMu.RUnlock()

	providersMu.Lock()
	defer providersMu.Unlock()

	// Double-check after acquiring write lock
	if cacheLoaded {
		return providersCache, nil
	}

	client := catwalk.New()
	providers, err := client.GetProviders(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch providers from catwalk: %w", err)
	}

	providersCache = providers
	cacheLoaded = true
	return providers, nil
}

// GetModels returns all models for a specific provider.
func GetModels(ctx context.Context, providerID string) ([]catwalk.Model, error) {
	providers, err := GetProviders(ctx)
	if err != nil {
		return nil, err
	}

	for _, p := range providers {
		if string(p.ID) == providerID {
			return p.Models, nil
		}
	}

	return nil, fmt.Errorf("provider %q not found", providerID)
}

// FindModelProvider finds which provider a model belongs to.
// Returns the provider ID and the model, or an error if not found.
func FindModelProvider(ctx context.Context, modelID string) (string, *catwalk.Model, error) {
	providers, err := GetProviders(ctx)
	if err != nil {
		// If catwalk is unavailable, fall back to inference
		inferred := InferProviderFromModel(modelID)
		if inferred != "" {
			return inferred, nil, nil
		}
		return "", nil, fmt.Errorf("model %q not found and cannot infer provider", modelID)
	}

	for _, p := range providers {
		for i, m := range p.Models {
			if m.ID == modelID {
				return string(p.ID), &p.Models[i], nil
			}
		}
	}

	// Model not in catwalk, try inference
	inferred := InferProviderFromModel(modelID)
	if inferred != "" {
		return inferred, nil, nil
	}

	return "", nil, fmt.Errorf("model %q not found in any provider", modelID)
}

// GetProviderAPIKey returns the API key for a provider, checking:
// 1. Environment variable (from provider config or default)
// 2. credentials.toml (handled by caller)
func GetProviderAPIKey(provider catwalk.Provider) string {
	// Provider config specifies env var like "$ANTHROPIC_API_KEY"
	if provider.APIKey != "" && provider.APIKey[0] == '$' {
		envVar := provider.APIKey[1:]
		if val := os.Getenv(envVar); val != "" {
			return val
		}
	}
	return ""
}

// ListAllModels returns a flat list of all models across all providers.
func ListAllModels(ctx context.Context) ([]ModelInfo, error) {
	providers, err := GetProviders(ctx)
	if err != nil {
		return nil, err
	}

	var models []ModelInfo
	for _, p := range providers {
		for _, m := range p.Models {
			models = append(models, ModelInfo{
				ID:            m.ID,
				Name:          m.Name,
				Provider:      string(p.ID),
				ContextWindow: m.ContextWindow,
				CostPer1MIn:   m.CostPer1MIn,
				CostPer1MOut:  m.CostPer1MOut,
				CanReason:     m.CanReason,
				SupportsImages: m.SupportsImages,
			})
		}
	}
	return models, nil
}

// ModelInfo is a simplified model representation for listing.
type ModelInfo struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Provider       string  `json:"provider"`
	ContextWindow  int64   `json:"context_window"`
	CostPer1MIn    float64 `json:"cost_per_1m_in"`
	CostPer1MOut   float64 `json:"cost_per_1m_out"`
	CanReason      bool    `json:"can_reason"`
	SupportsImages bool    `json:"supports_images"`
}
