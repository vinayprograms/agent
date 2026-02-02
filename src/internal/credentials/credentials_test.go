package credentials

import (
	"os"
	"path/filepath"
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

func TestApply(t *testing.T) {
	// Clear any existing env vars
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")

	creds := &Credentials{
		Anthropic: &ProviderCreds{APIKey: "test-anthropic"},
		OpenAI:    &ProviderCreds{APIKey: "test-openai"},
	}

	creds.Apply()

	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "test-anthropic" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want %q", got, "test-anthropic")
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "test-openai" {
		t.Errorf("OPENAI_API_KEY = %q, want %q", got, "test-openai")
	}

	// Clean up
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
}

func TestApply_DoesNotOverwrite(t *testing.T) {
	// Set existing env var
	os.Setenv("ANTHROPIC_API_KEY", "existing-value")

	creds := &Credentials{
		Anthropic: &ProviderCreds{APIKey: "new-value"},
	}

	creds.Apply()

	// Should keep existing value
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "existing-value" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want %q (should not overwrite)", got, "existing-value")
	}

	// Clean up
	os.Unsetenv("ANTHROPIC_API_KEY")
}

func TestApply_NilCredentials(t *testing.T) {
	var creds *Credentials
	// Should not panic
	creds.Apply()
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
