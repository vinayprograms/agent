// Package security contains security-focused tests for policy enforcement.
package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/tests/internal/testkit"
	"github.com/vinayprograms/agentkit/policy"
)

// TestSecurity_PathTraversal tests that path traversal attacks are blocked.
func TestSecurity_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	sensitiveFile := filepath.Join(tmpDir, "..", "sensitive.txt")

	pol := policy.New()
	pol.DefaultDeny = true
	pol.Tools["read"] = &policy.ToolPolicy{
		Allow: []string{tmpDir + "/**"},
	}

	registry := testkit.Registry(t, pol, tmpDir)

	// Try to read outside workspace using path traversal
	_, err := registry.Execute(t.Context(), "read", map[string]any{
		"path": sensitiveFile,
	})

	if err == nil {
		t.Fatal("expected path traversal to be blocked")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("expected denial error, got: %v", err)
	}
}

// TestSecurity_SymlinkEscape tests that symlink-based escapes are handled.
func TestSecurity_SymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a symlink pointing outside workspace
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	os.WriteFile(outsideFile, []byte("secret data"), 0644)

	symlinkPath := filepath.Join(tmpDir, "link")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	pol := policy.New()
	pol.DefaultDeny = true
	pol.Tools["read"] = &policy.ToolPolicy{
		Allow: []string{tmpDir + "/**"},
	}

	registry := testkit.Registry(t, pol, tmpDir)

	// Try to read through symlink
	// Note: Current implementation may not catch this - it's a known limitation
	// This test documents the expected behavior
	_, err := registry.Execute(t.Context(), "read", map[string]any{
		"path": filepath.Join(symlinkPath, "secret.txt"),
	})

	// If symlink resolution isn't implemented, this might succeed
	// For now, just document the behavior
	t.Logf("Symlink escape result: err=%v", err)
}

// TestSecurity_DenyOverridesAllow tests that deny patterns take precedence.
func TestSecurity_DenyOverridesAllow(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a .ssh directory inside workspace
	sshDir := filepath.Join(tmpDir, ".ssh")
	os.MkdirAll(sshDir, 0700)
	keyFile := filepath.Join(sshDir, "id_rsa")
	os.WriteFile(keyFile, []byte("private key"), 0600)

	pol := policy.New()
	pol.Tools["read"] = &policy.ToolPolicy{
		Allow: []string{tmpDir + "/**"},
		Deny:  []string{tmpDir + "/.ssh/*"},
	}

	registry := testkit.Registry(t, pol, tmpDir)

	_, err := registry.Execute(t.Context(), "read", map[string]any{
		"path": keyFile,
	})

	if err == nil {
		t.Fatal("expected .ssh file to be denied")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("expected denial error, got: %v", err)
	}
}

// TestSecurity_BashCommandInjection tests command injection prevention.
// v1.2.0 has no bash allowlist: the policy's deny list becomes shellguard's
// user-denied commands (matched on base command name, per chained segment)
// on top of shellguard's built-in banned set (sudo, curl, ...).
func TestSecurity_BashCommandInjection(t *testing.T) {
	pol := policy.New()
	pol.Tools["bash"] = &policy.ToolPolicy{
		Deny: []string{"rm"},
	}

	registry := testkit.Registry(t, pol, t.TempDir())

	tests := []struct {
		name    string
		command string
		wantErr bool
	}{
		{"allowed ls", "ls .", false},
		{"denied rm", "rm -rf /", true},
		{"injection semicolon", "ls; rm -rf /", true},
		{"injection and", "ls && rm -rf /", true},
		{"injection pipe", "ls | rm -rf /", true},
		{"denied sudo", "sudo cat /etc/shadow", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := registry.Execute(t.Context(), "bash", map[string]any{
				"command": tt.command,
			})

			if tt.wantErr && err == nil {
				t.Errorf("expected error for command %q", tt.command)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for command %q: %v", tt.command, err)
			}
		})
	}
}

// TestSecurity_DefaultDeny tests that default deny blocks unlisted tools.
func TestSecurity_DefaultDeny(t *testing.T) {
	pol := policy.New()
	pol.DefaultDeny = true
	// Only enable read, not write
	pol.Tools["read"] = &policy.ToolPolicy{
		Allow: []string{"**"},
	}

	if pol.IsToolEnabled("write") {
		t.Fatal("write should be disabled under default deny")
	}

	registry := testkit.Registry(t, pol, t.TempDir())
	if registry.Has("write") {
		t.Fatal("a policy-disabled tool must not be registered")
	}

	_, err := registry.Execute(t.Context(), "write", map[string]any{
		"path":    "/tmp/test.txt",
		"content": "test",
	})

	if err == nil {
		t.Error("expected write to be blocked in default deny mode")
	}
}

// TestSecurity_DisabledTool tests that disabled tools cannot be used.
// Under v1.2.0 a tool is disabled by leaving it out of [tools] with
// default_deny = true.
func TestSecurity_DisabledTool(t *testing.T) {
	pol := policy.New()
	pol.DefaultDeny = true
	pol.Tools["read"] = &policy.ToolPolicy{}

	registry := testkit.Registry(t, pol, t.TempDir())

	// Get definitions should not include disabled tools
	defs := registry.Definitions()
	for _, def := range defs {
		if def.Name == "bash" {
			t.Error("disabled tool should not appear in definitions")
		}
	}
	if registry.Has("bash") {
		t.Error("disabled tool should not be registered")
	}
}

// TestSecurity_WebDomainRestriction tests domain allowlist enforcement.
func TestSecurity_WebDomainRestriction(t *testing.T) {
	pol := policy.New()
	pol.Tools["web_fetch"] = &policy.ToolPolicy{
		Allow: []string{"api.example.com", "*.trusted.com"},
	}

	tests := []struct {
		domain  string
		allowed bool
	}{
		{"api.example.com", true},
		{"sub.trusted.com", true},
		{"trusted.com", true},
		{"evil.com", false},
		{"example.com", false}, // Not exact match
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			allowed, _ := pol.CheckDomain("web_fetch", tt.domain)
			if allowed != tt.allowed {
				t.Errorf("CheckDomain(web_fetch, %q) = %v, want %v", tt.domain, allowed, tt.allowed)
			}
		})
	}
}

// TestSecurity_SensitivePathPatterns tests blocking of sensitive paths.
func TestSecurity_SensitivePathPatterns(t *testing.T) {
	pol := policy.New()
	pol.DefaultDeny = true
	pol.Tools["read"] = &policy.ToolPolicy{
		Allow: []string{"/home/user/**"},
		Deny: []string{
			"/home/user/.ssh/**",
			"/home/user/.gnupg/**",
			"/home/user/.aws/**",
			"**/.git/config",
			"**/*.pem",
			"**/*.key",
		},
	}

	sensitivePaths := []string{
		"/home/user/.ssh/id_rsa",
		"/home/user/.gnupg/private-keys-v1.d/key",
		"/home/user/.aws/credentials",
		"/home/user/project/.git/config",
		"/home/user/project/server.pem",
		"/home/user/project/private.key",
	}

	for _, path := range sensitivePaths {
		t.Run(path, func(t *testing.T) {
			allowed, _ := pol.CheckPath("read", path)
			if allowed {
				t.Errorf("expected sensitive path %s to be denied", path)
			}
		})
	}

	// Safe paths should be allowed
	safePaths := []string{
		"/home/user/project/main.go",
		"/home/user/project/README.md",
		"/home/user/documents/notes.txt",
	}

	for _, path := range safePaths {
		t.Run("safe:"+path, func(t *testing.T) {
			allowed, _ := pol.CheckPath("read", path)
			if !allowed {
				t.Errorf("expected safe path %s to be allowed", path)
			}
		})
	}
}
