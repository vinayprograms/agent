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
	os.WriteFile(credPath, []byte(content), 0400)

	creds, err := LoadFile(credPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := creds.GetAPIKey("anthropic"); got != "sk-ant-test123" {
		t.Errorf("anthropic key = %q, want %q", got, "sk-ant-test123")
	}
	if got := creds.GetAPIKey("openai"); got != "sk-openai-test456" {
		t.Errorf("openai key = %q, want %q", got, "sk-openai-test456")
	}
}

func TestLoadFile_GenericLLMSection(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[llm]
api_key = "generic-llm-key"
`
	os.WriteFile(credPath, []byte(content), 0400)

	creds, err := LoadFile(credPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Any provider should get the generic key
	if got := creds.GetAPIKey("anthropic"); got != "generic-llm-key" {
		t.Errorf("anthropic key = %q, want %q (from [llm])", got, "generic-llm-key")
	}
	if got := creds.GetAPIKey("openrouter"); got != "generic-llm-key" {
		t.Errorf("openrouter key = %q, want %q (from [llm])", got, "generic-llm-key")
	}
	if got := creds.GetAPIKey("my-custom-provider"); got != "generic-llm-key" {
		t.Errorf("my-custom-provider key = %q, want %q (from [llm])", got, "generic-llm-key")
	}
}

func TestLoadFile_ProviderOverridesLLM(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[llm]
api_key = "generic-key"

[anthropic]
api_key = "anthropic-specific-key"
`
	os.WriteFile(credPath, []byte(content), 0400)

	creds, err := LoadFile(credPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Specific provider should use its own key
	if got := creds.GetAPIKey("anthropic"); got != "anthropic-specific-key" {
		t.Errorf("anthropic key = %q, want %q", got, "anthropic-specific-key")
	}
	// Other providers should use generic
	if got := creds.GetAPIKey("openai"); got != "generic-key" {
		t.Errorf("openai key = %q, want %q (from [llm])", got, "generic-key")
	}
}

func TestLoadFile_InsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check not applicable on Windows")
	}

	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[llm]
api_key = "secret-key"
`
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
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[llm]
api_key = "secret-key"
`
	os.WriteFile(credPath, []byte(content), 0400)

	creds, err := LoadFile(credPath)
	if err != nil {
		t.Fatalf("0400 should be allowed: %v", err)
	}
	if creds.GetAPIKey("any-provider") != "secret-key" {
		t.Error("expected api_key to be loaded")
	}
}

func TestLoadFile_RejectWritablePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check not applicable on Windows")
	}

	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[llm]
api_key = "secret-key"
`
	os.WriteFile(credPath, []byte(content), 0600)

	_, err := LoadFile(credPath)
	if err == nil {
		t.Fatal("expected error for 0600 permissions (writable)")
	}
	if !errors.Is(err, ErrInsecurePermissions) {
		t.Errorf("expected ErrInsecurePermissions, got %v", err)
	}
}

func TestGetAPIKey_FallbackToEnv(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "env-anthropic")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	creds := &Credentials{providers: make(map[string]*ProviderCreds)}

	if got := creds.GetAPIKey("anthropic"); got != "env-anthropic" {
		t.Errorf("GetAPIKey(anthropic) = %q, want %q (from env)", got, "env-anthropic")
	}
}

func TestGetAPIKey_CredentialsTakesPriority(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "env-value")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	creds := &Credentials{
		providers: map[string]*ProviderCreds{
			"anthropic": {APIKey: "creds-value"},
		},
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

func TestGetAPIKey_GenericEnvVar(t *testing.T) {
	// Unknown provider should generate PROVIDER_API_KEY env var
	os.Setenv("MYCUSTOM_API_KEY", "custom-env-value")
	defer os.Unsetenv("MYCUSTOM_API_KEY")

	creds := &Credentials{providers: make(map[string]*ProviderCreds)}

	if got := creds.GetAPIKey("mycustom"); got != "custom-env-value" {
		t.Errorf("GetAPIKey(mycustom) = %q, want %q", got, "custom-env-value")
	}
}

func TestLoad_NoFile(t *testing.T) {
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
[llm]
api_key = "from-current-dir"
`
	os.WriteFile("credentials.toml", []byte(content), 0400)

	creds, path, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected credentials to be loaded")
	}
	if creds.GetAPIKey("any") != "from-current-dir" {
		t.Errorf("unexpected api key: %s", creds.GetAPIKey("any"))
	}
	if path != "credentials.toml" {
		t.Errorf("expected path 'credentials.toml', got %q", path)
	}
}

func TestLoadFile_AnyProviderSection(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")

	content := `
[openrouter]
api_key = "openrouter-key"

[my-custom-llm]
api_key = "custom-key"
`
	os.WriteFile(credPath, []byte(content), 0400)

	creds, err := LoadFile(credPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := creds.GetAPIKey("openrouter"); got != "openrouter-key" {
		t.Errorf("openrouter key = %q, want %q", got, "openrouter-key")
	}
	if got := creds.GetAPIKey("my-custom-llm"); got != "custom-key" {
		t.Errorf("my-custom-llm key = %q, want %q", got, "custom-key")
	}
}
