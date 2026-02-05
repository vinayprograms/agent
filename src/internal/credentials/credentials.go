// Package credentials loads API keys from standard locations.
package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrInsecurePermissions is returned when credentials file has overly permissive permissions.
var ErrInsecurePermissions = fmt.Errorf("credentials file has insecure permissions")

// Credentials holds API keys loaded from credentials.toml
// Uses a generic map to support any provider without hardcoding.
type Credentials struct {
	// LLM is the generic LLM API key (used when provider-specific key not found)
	LLM *ProviderCreds `toml:"llm"`

	// Provider-specific sections (loaded dynamically)
	providers map[string]*ProviderCreds
}

// ProviderCreds holds credentials for a single provider
type ProviderCreds struct {
	APIKey string `toml:"api_key"`
}

// rawCredentials is used for TOML unmarshaling
type rawCredentials struct {
	LLM      *ProviderCreds            `toml:"llm"`
	Sections map[string]*ProviderCreds `toml:"-"`
}

// StandardPaths returns the standard credential file locations in order of priority
func StandardPaths() []string {
	paths := []string{}

	// 1. Current directory
	paths = append(paths, "credentials.toml")

	// 2. ~/.config/grid/credentials.toml
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "grid", "credentials.toml"))
	}

	// 3. ~/.grid/credentials.toml (fallback)
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".grid", "credentials.toml"))
	}

	return paths
}

// Load loads credentials from the first available standard location
func Load() (*Credentials, string, error) {
	for _, path := range StandardPaths() {
		if _, err := os.Stat(path); err == nil {
			creds, err := LoadFile(path)
			if err != nil {
				return nil, path, err
			}
			return creds, path, nil
		}
	}
	return nil, "", nil // No credentials file found (not an error)
}

// LoadFile loads credentials from a specific file.
// Returns ErrInsecurePermissions if file is readable by group or others.
func LoadFile(path string) (*Credentials, error) {
	// Check file permissions (Unix only)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		mode := info.Mode().Perm()
		// Credentials must be 0400 (owner read-only)
		if mode != 0400 {
			return nil, fmt.Errorf("%w: %s has mode %04o (must be 0400)",
				ErrInsecurePermissions, path, mode)
		}
	}

	// First pass: decode into a generic map to get all sections
	var rawData map[string]interface{}
	if _, err := toml.DecodeFile(path, &rawData); err != nil {
		return nil, err
	}

	creds := &Credentials{
		providers: make(map[string]*ProviderCreds),
	}

	// Extract provider sections
	for key, value := range rawData {
		section, ok := value.(map[string]interface{})
		if !ok {
			continue
		}

		apiKey, _ := section["api_key"].(string)
		if apiKey == "" {
			continue
		}

		provCreds := &ProviderCreds{APIKey: apiKey}

		if key == "llm" {
			creds.LLM = provCreds
		} else {
			creds.providers[key] = provCreds
		}
	}

	return creds, nil
}

// GetAPIKey returns the API key for a provider.
// Priority: [provider] section > [llm] section > environment variable
func (c *Credentials) GetAPIKey(provider string) string {
	if c != nil {
		// Normalize provider name (lowercase, no dashes)
		normalized := strings.ToLower(strings.ReplaceAll(provider, "-", ""))

		// Check provider-specific section first
		if creds, ok := c.providers[provider]; ok && creds.APIKey != "" {
			return creds.APIKey
		}
		// Try normalized name
		if creds, ok := c.providers[normalized]; ok && creds.APIKey != "" {
			return creds.APIKey
		}

		// Fall back to generic [llm] section
		if c.LLM != nil && c.LLM.APIKey != "" {
			return c.LLM.APIKey
		}
	}

	// Fallback to environment variable
	return os.Getenv(envVarForProvider(provider))
}

// envVarForProvider returns the environment variable name for a provider.
func envVarForProvider(provider string) string {
	// Known providers with standard env vars
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai", "openai-compat":
		return "OPENAI_API_KEY"
	case "google":
		return "GOOGLE_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "brave":
		return "BRAVE_API_KEY"
	case "tavily":
		return "TAVILY_API_KEY"
	default:
		// Generic: PROVIDER_API_KEY
		return strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"
	}
}
