// Package credentials loads API keys from standard locations.
package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// ErrInsecurePermissions is returned when credentials file has overly permissive permissions.
var ErrInsecurePermissions = fmt.Errorf("credentials file has insecure permissions")

// Credentials holds API keys loaded from credentials.toml
type Credentials struct {
	Anthropic  *ProviderCreds `toml:"anthropic"`
	OpenAI     *ProviderCreds `toml:"openai"`
	Google     *ProviderCreds `toml:"google"`
	Mistral    *ProviderCreds `toml:"mistral"`
	Groq       *ProviderCreds `toml:"groq"`
	Brave      *ProviderCreds `toml:"brave"`
	Tavily     *ProviderCreds `toml:"tavily"`
	OpenRouter *ProviderCreds `toml:"openrouter"`
	LiteLLM    *ProviderCreds `toml:"litellm"`
	Ollama     *ProviderCreds `toml:"ollama"`
	LMStudio   *ProviderCreds `toml:"lmstudio"`
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
		// Fail if group or others can read (should be 0600 or 0400)
		// Credentials must be 0400 (owner read-only)
		if mode != 0400 {
			return nil, fmt.Errorf("%w: %s has mode %04o (must be 0400)", 
				ErrInsecurePermissions, path, mode)
		}
	}

	var creds Credentials
	if _, err := toml.DecodeFile(path, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// GetAPIKey returns the API key for a provider.
// Priority: credentials file > environment variable.
func (c *Credentials) GetAPIKey(provider string) string {
	if c != nil {
		switch provider {
		case "anthropic":
			if c.Anthropic != nil && c.Anthropic.APIKey != "" {
				return c.Anthropic.APIKey
			}
		case "openai", "openai-compat":
			if c.OpenAI != nil && c.OpenAI.APIKey != "" {
				return c.OpenAI.APIKey
			}
		case "google":
			if c.Google != nil && c.Google.APIKey != "" {
				return c.Google.APIKey
			}
		case "mistral":
			if c.Mistral != nil && c.Mistral.APIKey != "" {
				return c.Mistral.APIKey
			}
		case "groq":
			if c.Groq != nil && c.Groq.APIKey != "" {
				return c.Groq.APIKey
			}
		case "brave":
			if c.Brave != nil && c.Brave.APIKey != "" {
				return c.Brave.APIKey
			}
		case "tavily":
			if c.Tavily != nil && c.Tavily.APIKey != "" {
				return c.Tavily.APIKey
			}
		case "openrouter":
			if c.OpenRouter != nil && c.OpenRouter.APIKey != "" {
				return c.OpenRouter.APIKey
			}
		case "litellm":
			if c.LiteLLM != nil && c.LiteLLM.APIKey != "" {
				return c.LiteLLM.APIKey
			}
		case "ollama":
			if c.Ollama != nil && c.Ollama.APIKey != "" {
				return c.Ollama.APIKey
			}
		case "lmstudio":
			if c.LMStudio != nil && c.LMStudio.APIKey != "" {
				return c.LMStudio.APIKey
			}
		}
	}

	// Fallback to environment variable
	return os.Getenv(envVarForProvider(provider))
}

// envVarForProvider returns the environment variable name for a provider.
func envVarForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
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
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "litellm":
		return "LITELLM_API_KEY"
	case "ollama":
		return "OLLAMA_API_KEY"
	case "lmstudio":
		return "LMSTUDIO_API_KEY"
	case "openai-compat":
		return "OPENAI_API_KEY" // Default to OpenAI env var for generic compat
	default:
		return ""
	}
}
