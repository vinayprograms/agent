package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStandardPaths(t *testing.T) {
	paths := StandardPaths()
	if len(paths) < 2 {
		t.Errorf("expected at least 2 standard paths, got %d", len(paths))
	}
	if paths[0] != "credentials.toml" {
		t.Errorf("first path should be credentials.toml, got %s", paths[0])
	}
}

func TestLoadFile(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[anthropic]
api_key = "sk-ant-test123"

[openai]
api_key = "sk-openai-test456"
`
	os.WriteFile(credPath, []byte(content), 0600)

	creds, err := LoadFile(credPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if creds.Anthropic == nil || creds.Anthropic.APIKey != "sk-ant-test123" {
		t.Errorf("anthropic key not loaded correctly")
	}
	if creds.OpenAI == nil || creds.OpenAI.APIKey != "sk-openai-test456" {
		t.Errorf("openai key not loaded correctly")
	}
}

func TestLoadFile_AllProviders(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[anthropic]
api_key = "anthropic-key"

[openai]
api_key = "openai-key"

[google]
api_key = "google-key"

[mistral]
api_key = "mistral-key"

[groq]
api_key = "groq-key"
`
	os.WriteFile(credPath, []byte(content), 0600)

	creds, err := LoadFile(credPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name string
		got  *ProviderCreds
		want string
	}{
		{"anthropic", creds.Anthropic, "anthropic-key"},
		{"openai", creds.OpenAI, "openai-key"},
		{"google", creds.Google, "google-key"},
		{"mistral", creds.Mistral, "mistral-key"},
		{"groq", creds.Groq, "groq-key"},
	}

	for _, tt := range tests {
		if tt.got == nil || tt.got.APIKey != tt.want {
			t.Errorf("%s: expected %q, got %v", tt.name, tt.want, tt.got)
		}
	}
}

func TestLoadFile_InsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check not applicable on Windows")
	}

	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[anthropic]
api_key = "secret-key"
`
	// Write with world-readable permissions (0644)
	os.WriteFile(credPath, []byte(content), 0644)

	_, err := LoadFile(credPath)
	if err == nil {
		t.Fatal("expected error for insecure permissions")
	}
	if !errors.Is(err, ErrInsecurePermissions) {
		t.Errorf("expected ErrInsecurePermissions, got %v", err)
	}
}

func TestLoadFile_SecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check not applicable on Windows")
	}

	tmpDir := t.TempDir()

	content := `
[anthropic]
api_key = "secret-key"
`
	// Test 0600 (owner read-write)
	credPath600 := filepath.Join(tmpDir, "credentials-600.toml")
	os.WriteFile(credPath600, []byte(content), 0600)

	creds, err := LoadFile(credPath600)
	if err != nil {
		t.Fatalf("0600 should be allowed: %v", err)
	}
	if creds.Anthropic.APIKey != "secret-key" {
		t.Error("expected api_key to be loaded")
	}

	// Test 0400 (owner read-only) - should also be allowed
	credPath400 := filepath.Join(tmpDir, "credentials-400.toml")
	os.WriteFile(credPath400, []byte(content), 0400)

	creds, err = LoadFile(credPath400)
	if err != nil {
		t.Fatalf("0400 should be allowed: %v", err)
	}
	if creds.Anthropic.APIKey != "secret-key" {
		t.Error("expected api_key to be loaded")
	}
}

func TestGetAPIKey_FromCredentials(t *testing.T) {
	creds := &Credentials{
		Anthropic: &ProviderCreds{APIKey: "creds-anthropic"},
		OpenAI:    &ProviderCreds{APIKey: "creds-openai"},
	}

	if got := creds.GetAPIKey("anthropic"); got != "creds-anthropic" {
		t.Errorf("GetAPIKey(anthropic) = %q, want %q", got, "creds-anthropic")
	}
	if got := creds.GetAPIKey("openai"); got != "creds-openai" {
		t.Errorf("GetAPIKey(openai) = %q, want %q", got, "creds-openai")
	}
}

func TestGetAPIKey_FallbackToEnv(t *testing.T) {
	// Set env var but no credentials
	os.Setenv("ANTHROPIC_API_KEY", "env-anthropic")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	creds := &Credentials{} // No anthropic key set

	if got := creds.GetAPIKey("anthropic"); got != "env-anthropic" {
		t.Errorf("GetAPIKey(anthropic) = %q, want %q (from env)", got, "env-anthropic")
	}
}

func TestGetAPIKey_CredentialsTakesPriority(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "env-value")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	creds := &Credentials{
		Anthropic: &ProviderCreds{APIKey: "creds-value"},
	}

	if got := creds.GetAPIKey("anthropic"); got != "creds-value" {
		t.Errorf("GetAPIKey(anthropic) = %q, want %q (creds should take priority)", got, "creds-value")
	}
}

func TestGetAPIKey_NilCredentials(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "env-openai")
	defer os.Unsetenv("OPENAI_API_KEY")

	var creds *Credentials

	if got := creds.GetAPIKey("openai"); got != "env-openai" {
		t.Errorf("GetAPIKey(openai) = %q, want %q (from env with nil creds)", got, "env-openai")
	}
}

func TestGetAPIKey_AllProviders(t *testing.T) {
	creds := &Credentials{
		Anthropic: &ProviderCreds{APIKey: "anthropic-key"},
		OpenAI:    &ProviderCreds{APIKey: "openai-key"},
		Google:    &ProviderCreds{APIKey: "google-key"},
		Mistral:   &ProviderCreds{APIKey: "mistral-key"},
		Groq:      &ProviderCreds{APIKey: "groq-key"},
		Brave:     &ProviderCreds{APIKey: "brave-key"},
		Tavily:    &ProviderCreds{APIKey: "tavily-key"},
	}

	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "anthropic-key"},
		{"openai", "openai-key"},
		{"google", "google-key"},
		{"mistral", "mistral-key"},
		{"groq", "groq-key"},
		{"brave", "brave-key"},
		{"tavily", "tavily-key"},
	}

	for _, tt := range tests {
		if got := creds.GetAPIKey(tt.provider); got != tt.want {
			t.Errorf("GetAPIKey(%s) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestLoad_NoFile(t *testing.T) {
	// Change to temp dir where no credentials file exists
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	creds, path, err := Load()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if creds != nil {
		t.Error("expected nil credentials when no file exists")
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

func TestLoad_FromCurrentDir(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	content := `
[anthropic]
api_key = "from-current-dir"
`
	os.WriteFile("credentials.toml", []byte(content), 0600)

	creds, path, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected credentials to be loaded")
	}
	if creds.Anthropic.APIKey != "from-current-dir" {
		t.Errorf("unexpected api key: %s", creds.Anthropic.APIKey)
	}
	if path != "credentials.toml" {
		t.Errorf("expected path 'credentials.toml', got %q", path)
	}
}
