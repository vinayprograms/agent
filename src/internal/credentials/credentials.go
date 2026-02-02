// Package credentials loads API keys from standard locations.
package credentials

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Credentials holds API keys loaded from credentials.toml
type Credentials struct {
	Anthropic *ProviderCreds `toml:"anthropic"`
	OpenAI    *ProviderCreds `toml:"openai"`
	Google    *ProviderCreds `toml:"google"`
	Mistral   *ProviderCreds `toml:"mistral"`
	Groq      *ProviderCreds `toml:"groq"`
}

// ProviderCreds holds credentials for a single provider
type ProviderCreds struct {
	APIKey string `toml:"api_key"`
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

// LoadFile loads credentials from a specific file
func LoadFile(path string) (*Credentials, error) {
	var creds Credentials
	if _, err := toml.DecodeFile(path, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// Apply sets environment variables from loaded credentials (if not already set)
func (c *Credentials) Apply() {
	if c == nil {
		return
	}

	if c.Anthropic != nil && c.Anthropic.APIKey != "" {
		setIfEmpty("ANTHROPIC_API_KEY", c.Anthropic.APIKey)
	}
	if c.OpenAI != nil && c.OpenAI.APIKey != "" {
		setIfEmpty("OPENAI_API_KEY", c.OpenAI.APIKey)
	}
	if c.Google != nil && c.Google.APIKey != "" {
		setIfEmpty("GOOGLE_API_KEY", c.Google.APIKey)
	}
	if c.Mistral != nil && c.Mistral.APIKey != "" {
		setIfEmpty("MISTRAL_API_KEY", c.Mistral.APIKey)
	}
	if c.Groq != nil && c.Groq.APIKey != "" {
		setIfEmpty("GROQ_API_KEY", c.Groq.APIKey)
	}
}

func setIfEmpty(key, value string) {
	if os.Getenv(key) == "" {
		os.Setenv(key, value)
	}
}
