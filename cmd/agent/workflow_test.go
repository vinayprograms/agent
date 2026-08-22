package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/config"
)

func TestRunCmd_Defaults(t *testing.T) {
	cli, err := parseArgs([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.File != "Agentfile" {
		t.Errorf("expected default file 'Agentfile', got %q", cli.Run.File)
	}
}

func TestRunCmd_CustomFile(t *testing.T) {
	cli, err := parseArgs([]string{"run", "custom.agent"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.File != "custom.agent" {
		t.Errorf("expected 'custom.agent', got %q", cli.Run.File)
	}
}

func TestRunCmd_Inputs(t *testing.T) {
	cli, err := parseArgs([]string{"run", "-i", "key=value", "-i", "foo=bar"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.Input["key"] != "value" {
		t.Errorf("expected input key=value, got %v", cli.Run.Input)
	}
	if cli.Run.Input["foo"] != "bar" {
		t.Errorf("expected input foo=bar, got %v", cli.Run.Input)
	}
}

func TestRunCmd_AllFlags(t *testing.T) {
	cli, err := parseArgs([]string{
		"run",
		"--config", "/path/to/config.toml",
		"--policy", "/path/to/policy.toml",
		"--workspace", "/tmp/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.Config != "/path/to/config.toml" {
		t.Errorf("expected config path, got %q", cli.Run.Config)
	}
	if cli.Run.Policy != "/path/to/policy.toml" {
		t.Errorf("expected policy path, got %q", cli.Run.Policy)
	}
	if cli.Run.Workspace != "/tmp/workspace" {
		t.Errorf("expected workspace path, got %q", cli.Run.Workspace)
	}
}

// writePolicy writes content to <dir>/policy.toml and returns a workflow
// whose baseDir is dir (default policy path) with the workspace set.
func writePolicy(t *testing.T, content string) *workflow {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return &workflow{baseDir: dir, cfg: &config.Config{Agent: config.AgentConfig{Workspace: dir}}}
}

func TestLoadPolicy_LegacyKeysRejected(t *testing.T) {
	w := writePolicy(t, `
default_deny = true
[tools.bash]
enabled = true
allowlist = ["ls"]
denylist = ["rm"]
rate_limit = 5
sandbox = "none"
timeout = 30
[tools.web_fetch]
allow_domains = ["example.com"]
[mcp]
default_deny = true
allowed_tools = ["fs:read"]
[security]
extra_patterns = ["x:y"]
extra_keywords = ["k"]
[bogus]
thing = 1
`)
	err := w.loadPolicy()
	if err == nil {
		t.Fatal("expected legacy-key error")
	}
	msg := err.Error()
	for _, want := range []string{
		"policy validation",
		"tools.bash.enabled -> list the tool under [tools.<name>]",
		"tools.bash.allowlist -> (removed; use deny + LLM review)",
		"tools.bash.denylist -> deny",
		"tools.bash.rate_limit -> (removed)",
		"tools.web_fetch.allow_domains -> allow",
		"mcp.default_deny -> mcp.enabled",
		"mcp.allowed_tools -> mcp.allow",
		"security.extra_patterns -> content.security.patterns",
		"security.extra_keywords -> content.security.keywords",
		"bogus.thing -> unknown key",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
	for _, valid := range []string{"sandbox", "timeout"} {
		if strings.Contains(msg, "tools.bash."+valid) {
			t.Errorf("valid key %q reported as unknown:\n%s", valid, msg)
		}
	}
}

func TestLoadPolicy_CurrentSchema(t *testing.T) {
	w := writePolicy(t, `
default_deny = true
allowed_dirs = ["$WORKSPACE/sub"]
[tools.read]
[tools.bash]
deny = ["curl"]
[content.security]
patterns = ["name:regex"]
keywords = ["secret"]
`)
	if err := w.loadPolicy(); err != nil {
		t.Fatalf("loadPolicy: %v", err)
	}
	if !w.pol.IsToolEnabled("read") || w.pol.IsToolEnabled("write") {
		t.Error("tool enablement not honoured")
	}
	if got := w.pol.Tools["bash"].Deny; len(got) != 1 || got[0] != "curl" {
		t.Errorf("bash deny = %v", got)
	}
	ws := w.cfg.Agent.Workspace
	if len(w.pol.AllowedDirs) != 2 || w.pol.AllowedDirs[0] != filepath.Join(ws, "sub") || w.pol.AllowedDirs[1] != ws {
		t.Errorf("allowed_dirs = %v (want expanded sub dir + workspace)", w.pol.AllowedDirs)
	}
	if w.pol.Content.Security.Patterns[0] != "name:regex" || w.pol.Content.Security.Keywords[0] != "secret" {
		t.Errorf("content.security not loaded: %+v", w.pol.Content.Security)
	}
}

func TestLoadPolicy_InvalidTOML(t *testing.T) {
	w := writePolicy(t, "default_deny = [")
	if err := w.loadPolicy(); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadPolicy_MissingDefaultIsPermissive(t *testing.T) {
	dir := t.TempDir()
	w := &workflow{baseDir: dir, cfg: &config.Config{Agent: config.AgentConfig{Workspace: dir}}}
	if err := w.loadPolicy(); err != nil {
		t.Fatalf("loadPolicy: %v", err)
	}
	if w.pol.DefaultDeny || !w.pol.IsToolEnabled("bash") {
		t.Error("missing default policy should be permissive")
	}
	if len(w.pol.AllowedDirs) != 1 || w.pol.AllowedDirs[0] != dir {
		t.Errorf("allowed_dirs = %v, want [%s]", w.pol.AllowedDirs, dir)
	}
}

func TestLoadPolicy_MissingExplicitIsError(t *testing.T) {
	dir := t.TempDir()
	w := &workflow{
		baseDir:    dir,
		policyPath: filepath.Join(dir, "nope.toml"),
		cfg:        &config.Config{Agent: config.AgentConfig{Workspace: dir}},
	}
	if err := w.loadPolicy(); err == nil {
		t.Fatal("expected error for explicit missing policy path")
	}
}

func TestPolicyKeyReplacement(t *testing.T) {
	cases := map[string]string{
		"tools.read.enabled":      "list the tool under [tools.<name>]",
		"enabled":                 "list the tool under [tools.<name>]",
		"mcp.default_deny":        "mcp.enabled",
		"security.extra_keywords": "content.security.keywords",
		"workspace":               "unknown key (remove it)",
		"a.b.c":                   "unknown key (remove it)",
	}
	for key, want := range cases {
		if got := policyKeyReplacement(key); got != want {
			t.Errorf("%s: got %q want %q", key, got, want)
		}
	}
	if err := validatePolicyKeys("p.toml", nil); err != nil {
		t.Errorf("no unknown keys should be nil, got %v", err)
	}
}
