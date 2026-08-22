package run

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/config"
)

// writePolicy writes content to <dir>/policy.toml and returns a workflow
// rooted at dir (the default policy path) with the workspace set.
func writePolicy(t *testing.T, content string) (*Loaded, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Loaded{Config: &config.Config{Agent: config.AgentConfig{Workspace: dir}}}, dir
}

func TestLoadPolicy_LegacyKeysRejected(t *testing.T) {
	w, dir := writePolicy(t, `
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
	err := w.loadPolicy("", dir, io.Discard)
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
	w, dir := writePolicy(t, `
default_deny = true
allowed_dirs = ["$WORKSPACE/sub"]
[tools.read]
[tools.bash]
deny = ["curl"]
[content.security]
patterns = ["name:regex"]
keywords = ["secret"]
`)
	if err := w.loadPolicy("", dir, io.Discard); err != nil {
		t.Fatalf("loadPolicy: %v", err)
	}
	if !w.Policy.IsToolEnabled("read") || w.Policy.IsToolEnabled("write") {
		t.Error("tool enablement not honoured")
	}
	if got := w.Policy.Tools["bash"].Deny; len(got) != 1 || got[0] != "curl" {
		t.Errorf("bash deny = %v", got)
	}
	ws := w.Config.Agent.Workspace
	if len(w.Policy.AllowedDirs) != 2 || w.Policy.AllowedDirs[0] != filepath.Join(ws, "sub") || w.Policy.AllowedDirs[1] != ws {
		t.Errorf("allowed_dirs = %v (want expanded sub dir + workspace)", w.Policy.AllowedDirs)
	}
	if w.Policy.Content.Security.Patterns[0] != "name:regex" || w.Policy.Content.Security.Keywords[0] != "secret" {
		t.Errorf("content.security not loaded: %+v", w.Policy.Content.Security)
	}
}

func TestLoadPolicy_InvalidTOML(t *testing.T) {
	w, dir := writePolicy(t, "default_deny = [")
	if err := w.loadPolicy("", dir, io.Discard); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadPolicy_MissingDefaultIsPermissive(t *testing.T) {
	dir := t.TempDir()
	w := &Loaded{Config: &config.Config{Agent: config.AgentConfig{Workspace: dir}}}
	if err := w.loadPolicy("", dir, io.Discard); err != nil {
		t.Fatalf("loadPolicy: %v", err)
	}
	if w.Policy.DefaultDeny || !w.Policy.IsToolEnabled("bash") {
		t.Error("missing default policy should be permissive")
	}
	if len(w.Policy.AllowedDirs) != 1 || w.Policy.AllowedDirs[0] != dir {
		t.Errorf("allowed_dirs = %v, want [%s]", w.Policy.AllowedDirs, dir)
	}
}

func TestLoadPolicy_MissingExplicitIsError(t *testing.T) {
	dir := t.TempDir()
	w := &Loaded{Config: &config.Config{Agent: config.AgentConfig{Workspace: dir}}}
	if err := w.loadPolicy(filepath.Join(dir, "nope.toml"), dir, io.Discard); err == nil {
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

const minimalAgentfile = `NAME pkg-test
INPUT topic DEFAULT "golang"
GOAL analyze "Analyze $topic" -> summary
RUN main USING analyze
`

// writeAgentDir writes a minimal Agentfile into a temp dir and returns it.
func writeAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Agentfile"), []byte(minimalAgentfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad(t *testing.T) {
	src := writeAgentDir(t)
	cfgPath := filepath.Join(src, "agent.toml")
	if err := os.WriteFile(cfgPath, []byte("[agent]\nworkspace = \""+src+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := LoadOptions{AgentfilePath: filepath.Join(src, "Agentfile"), ConfigPath: cfgPath, Workspace: src}

	l, err := Load(opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if l.Workflow.Name != "pkg-test" || l.Config.Agent.Workspace != src || l.Policy == nil {
		t.Errorf("loaded: wf=%v ws=%q pol=%v", l.Workflow, l.Config.Agent.Workspace, l.Policy)
	}

	// Conflicting --workspace vs agent.toml is an error.
	conflict := opts
	conflict.Workspace = t.TempDir()
	if _, err := Load(conflict); err == nil || !strings.Contains(err.Error(), "workspace conflict") {
		t.Errorf("expected workspace conflict, got %v", err)
	}

	// Missing config path and missing Agentfile are errors.
	badCfg := opts
	badCfg.ConfigPath = "/nonexistent.toml"
	if _, err := Load(badCfg); err == nil {
		t.Error("expected config error")
	}
	if _, err := Load(LoadOptions{AgentfilePath: "/nonexistent/Agentfile"}); err == nil {
		t.Error("expected Agentfile error")
	}
	if _, err := Load(LoadOptions{AgentfilePath: filepath.Join(src, "bad")}); err == nil {
		t.Error("expected not-found error")
	}

	// Unset workspace defaults to the working directory.
	l3, err := Load(LoadOptions{AgentfilePath: filepath.Join(src, "Agentfile")})
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if l3.Config.Agent.Workspace != cwd {
		t.Errorf("workspace = %q want %q", l3.Config.Agent.Workspace, cwd)
	}
}

func TestLoad_InlineGoalSkipsAgentfile(t *testing.T) {
	l, err := Load(LoadOptions{AgentfilePath: "/nonexistent/Agentfile", Goal: "summarise the news", Debug: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if l.Workflow.Name != inlineGoalName || l.Workflow.Goals[0].Outcome != "summarise the news" {
		t.Errorf("inline goal workflow = %+v", l.Workflow)
	}
	if len(l.Workflow.Steps) != 1 || l.Workflow.Steps[0].UsingGoals[0] != "goal" {
		t.Errorf("inline goal steps = %+v", l.Workflow.Steps)
	}
}

func TestLoad_WarningsReachStderr(t *testing.T) {
	src := writeAgentDir(t)
	var buf strings.Builder
	if _, err := Load(LoadOptions{AgentfilePath: filepath.Join(src, "Agentfile"), Workspace: src, Stderr: &buf}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no policy file at") {
		t.Errorf("missing policy warning: %q", buf.String())
	}
}

func TestExpandHome(t *testing.T) {
	cases := map[string]string{"~/x": "/home/u/x", "~": "/home/u", "rel": "rel", "/abs": "/abs", "": ""}
	for in, want := range cases {
		if got := expandHome(in, "/home/u"); got != want {
			t.Errorf("expandHome(%q) = %q want %q", in, got, want)
		}
	}
	if got := expandHome("~/x", ""); got != "~/x" {
		t.Errorf("no home: %q", got)
	}
}

func TestAbsPath(t *testing.T) {
	l := &Loaded{home: "/home/u"}
	if got := l.absPath("~/x"); got != "/home/u/x" {
		t.Errorf("tilde: %q", got)
	}
	if got := l.absPath("rel"); !filepath.IsAbs(got) {
		t.Errorf("relative: %q", got)
	}
	if got := l.absPath("/abs"); got != "/abs" {
		t.Errorf("absolute: %q", got)
	}
}

func TestEnsureWorkspaceInAllowedDirs(t *testing.T) {
	ws := "/ws/project"
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{ws}},
		{"exact", []string{ws}, []string{ws}},
		{"parent", []string{"/ws"}, []string{"/ws"}},
		{"placeholder", []string{"$WORKSPACE"}, []string{"$WORKSPACE"}},
		{"other", []string{"/tmp"}, []string{"/tmp", ws}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := testWorkflow(t, func(c *config.Config) { c.Agent.Workspace = ws })
			l.Policy.AllowedDirs = tc.in
			l.ensureWorkspaceInAllowedDirs()
			if strings.Join(l.Policy.AllowedDirs, ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v want %v", l.Policy.AllowedDirs, tc.want)
			}
		})
	}
	l := testWorkflow(t, func(c *config.Config) { c.Agent.Workspace = "" })
	l.Policy.AllowedDirs = nil
	l.ensureWorkspaceInAllowedDirs()
	if l.Policy.AllowedDirs != nil {
		t.Errorf("empty workspace should not add dirs: %v", l.Policy.AllowedDirs)
	}
}
