package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialPaths_NoOverride(t *testing.T) {
	got, err := CredentialPaths("/home/u", "")
	if err != nil {
		t.Fatalf("CredentialPaths: %v", err)
	}
	want := []string{
		"credentials.toml",
		filepath.Join("/home/u", ".config", "agent", "credentials.toml"),
		filepath.Join("/home/u", ".agent", "credentials.toml"),
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("CredentialPaths = %v, want %v", got, want)
	}
}

func TestCredentialPaths_EmptyHomeOmitsHomePaths(t *testing.T) {
	got, err := CredentialPaths("", "")
	if err != nil {
		t.Fatalf("CredentialPaths: %v", err)
	}
	if len(got) != 1 || got[0] != "credentials.toml" {
		t.Errorf("CredentialPaths = %v, want [credentials.toml]", got)
	}
}

func TestCredentialPaths_OverrideTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "creds.toml")
	if err := os.WriteFile(override, []byte("[anthropic]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := CredentialPaths("/home/u", override)
	if err != nil {
		t.Fatalf("CredentialPaths: %v", err)
	}
	if len(got) == 0 || got[0] != override {
		t.Errorf("CredentialPaths[0] = %v, want override first: %v", got, override)
	}
	if len(got) != 4 {
		t.Errorf("CredentialPaths = %v, want override + 3 standard paths", got)
	}
}

func TestCredentialPaths_OverrideMissingIsError(t *testing.T) {
	_, err := CredentialPaths("/home/u", "/nonexistent/creds.toml")
	if err == nil {
		t.Fatal("expected error for missing override file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/creds.toml") {
		t.Errorf("error should name the path: %v", err)
	}
}
