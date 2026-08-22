package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/tools/websearch"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/shellguard"
	"github.com/vinayprograms/agentkit/tools"
)

// permissivePolicy returns a policy with every tool enabled and the
// workspace as the only allowed directory.
func permissivePolicy(ws string) *policy.Policy {
	pol := policy.New()
	pol.DefaultDeny = false
	pol.AllowedDirs = []string{ws}
	return pol
}

func TestBuildToolset_RequiresPolicy(t *testing.T) {
	if _, err := buildToolset(toolsetConfig{}); err == nil {
		t.Fatal("expected error for nil policy")
	}
}

func TestBuildToolset_Permissive_RegistersEverything(t *testing.T) {
	ws := t.TempDir()
	reg, err := buildToolset(toolsetConfig{
		Policy:      permissivePolicy(ws),
		Workspace:   ws,
		Scratchpad:  memory.NewInMemoryStore(),
		Memory:      memory.NewInMemoryStore(),
		BashGate:    shellguard.New(shellguard.Bash(), ws, []string{ws}, nil, nil, ""),
		Spawn:       tools.NewSpawnBinder(),
		HTTPTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	want := []string{
		"read", "write", "edit", "grep", "glob", "ls", "head", "tail", "tree",
		"mkdir", "mv", "cp", "rm", "diff", "patch", "git",
		"pwd", "hostname", "whoami", "sysinfo", "datetime", "env", "which",
		"bash", "web_fetch", "web_search", "spawn_agents",
		"scratchpad_read", "scratchpad_write", "scratchpad_list", "scratchpad_search",
		"remember", "recall",
	}
	for _, name := range want {
		if !reg.Has(name) {
			t.Errorf("tool %q not registered", name)
		}
	}
	if got := len(reg.Definitions()); got != len(want) {
		t.Errorf("registered %d tools, want %d", got, len(want))
	}
	if _, ok := reg.Get("web_search").(*websearch.Tool); !ok {
		t.Errorf("web_search is %T, want the in-repo websearch tool", reg.Get("web_search"))
	}
}

func TestBuildToolset_DenyByDefault_OnlyListedTools(t *testing.T) {
	ws := t.TempDir()
	pol := policy.New() // DefaultDeny
	pol.Tools["read"] = &policy.ToolPolicy{}
	pol.Tools["pwd"] = &policy.ToolPolicy{}
	pol.Tools["bash"] = &policy.ToolPolicy{}

	reg, err := buildToolset(toolsetConfig{
		Policy:     pol,
		Workspace:  ws,
		Scratchpad: memory.NewInMemoryStore(),
		Memory:     memory.NewInMemoryStore(),
		Spawn:      tools.NewSpawnBinder(),
		// no BashGate: bash must not be registered even though enabled
	})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	for _, name := range []string{"read", "pwd"} {
		if !reg.Has(name) {
			t.Errorf("tool %q should be registered", name)
		}
	}
	for _, name := range []string{"write", "bash", "web_fetch", "web_search", "spawn_agents", "scratchpad_read", "remember"} {
		if reg.Has(name) {
			t.Errorf("tool %q should not be registered", name)
		}
	}
}

func TestBuildToolset_OptionalMembersSkipped(t *testing.T) {
	ws := t.TempDir()
	reg, err := buildToolset(toolsetConfig{Policy: permissivePolicy(ws), Workspace: ws})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	for _, name := range []string{"bash", "spawn_agents", "scratchpad_read", "remember", "recall"} {
		if reg.Has(name) {
			t.Errorf("tool %q should be skipped without its dependency", name)
		}
	}
	if !reg.Has("web_fetch") {
		t.Error("web_fetch should be registered without a summarizer")
	}
}

func TestBuildToolset_PathGuard(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(ws, "ok.txt")
	if err := os.WriteFile(inside, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	pol := permissivePolicy(ws)
	pol.Tools["read"] = &policy.ToolPolicy{Deny: []string{"**/*.key"}}
	reg, err := buildToolset(toolsetConfig{Policy: pol, Workspace: ws})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	ctx := context.Background()

	out, err := reg.Execute(ctx, "read", map[string]any{"path": inside})
	if err != nil || !strings.Contains(out, "hello") {
		t.Fatalf("read inside workspace: out=%q err=%v", out, err)
	}
	// Relative paths resolve against the workspace.
	if _, err := reg.Execute(ctx, "read", map[string]any{"path": "ok.txt"}); err != nil {
		t.Fatalf("relative read: %v", err)
	}

	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"read outside allowed dirs", "read", map[string]any{"path": outsideFile}},
		{"read deny pattern", "read", map[string]any{"path": filepath.Join(ws, "id.key")}},
		{"write outside", "write", map[string]any{"path": outsideFile, "content": "x"}},
		{"mv destination outside", "mv", map[string]any{"source": inside, "destination": filepath.Join(outside, "moved")}},
		{"cp source outside", "cp", map[string]any{"source": outsideFile, "destination": filepath.Join(ws, "copy")}},
		{"grep outside", "grep", map[string]any{"pattern": "x", "path": outside}},
		{"ls outside", "ls", map[string]any{"path": outside}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reg.Execute(ctx, tc.tool, tc.args)
			if err == nil {
				t.Fatal("expected denial")
			}
			if !strings.Contains(err.Error(), "denied") {
				t.Errorf("error %q should contain \"denied\"", err)
			}
		})
	}
	// The outside file must still be there: the guard ran before the tool.
	if _, err := os.Stat(outsideFile); err != nil {
		t.Errorf("outside file touched: %v", err)
	}
}

func TestBuildToolset_DomainGuard(t *testing.T) {
	ws := t.TempDir()
	pol := permissivePolicy(ws)
	pol.Tools["web_fetch"] = &policy.ToolPolicy{Allow: []string{"example.com"}}
	reg, err := buildToolset(toolsetConfig{Policy: pol, Workspace: ws, HTTPTimeout: time.Second})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	ctx := context.Background()

	for _, bad := range []string{"https://evil.example.org/x", "not a url", ""} {
		_, err := reg.Execute(ctx, "web_fetch", map[string]any{"url": bad, "question": "q"})
		if err == nil || !strings.Contains(err.Error(), "denied") {
			t.Errorf("url %q: want denied error, got %v", bad, err)
		}
	}
}

func TestDomainGuard_AllowsListedHost(t *testing.T) {
	pol := policy.New()
	pol.DefaultDeny = false
	pol.Tools["web_fetch"] = &policy.ToolPolicy{Allow: []string{"example.com"}}
	g := domainGuard{pol: pol, tool: "web_fetch"}
	args, err := tools.Validate(map[string]tools.Param{"url": {Type: tools.StringParam}}, map[string]any{"url": "https://example.com/page"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Check(context.Background(), args); err != nil {
		t.Errorf("listed host denied: %v", err)
	}
}

func TestPathGuard_SkipsAbsentArgs(t *testing.T) {
	ws := t.TempDir()
	g := pathGuard{pol: permissivePolicy(ws), base: ws, tool: "read", keys: []string{"path"}}
	args, err := tools.Validate(map[string]tools.Param{"path": {Type: tools.StringParam}}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Check(context.Background(), args); err != nil {
		t.Errorf("absent path should pass: %v", err)
	}
}

func TestBuildToolset_BashGate(t *testing.T) {
	ws := t.TempDir()
	pol := permissivePolicy(ws)
	var decisions []string
	gate := shellguard.New(shellguard.Bash(), ws, []string{ws}, []string{"git"}, nil, "")
	gate.OnDecision = func(command, step string, allowed bool, reason string, _ int64, _, _ int) {
		decisions = append(decisions, step)
	}
	reg, err := buildToolset(toolsetConfig{Policy: pol, Workspace: ws, BashGate: gate})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	ctx := context.Background()

	if _, err := reg.Execute(ctx, "bash", map[string]any{"command": "git status"}); err == nil {
		t.Error("user-denied command should be blocked")
	}
	if _, err := reg.Execute(ctx, "bash", map[string]any{"command": "sudo ls"}); err == nil {
		t.Error("banned command should be blocked")
	}
	out, err := reg.Execute(ctx, "bash", map[string]any{"command": "echo ok"})
	if err != nil || !strings.Contains(out, "ok") {
		t.Errorf("echo: out=%q err=%v", out, err)
	}
	if len(decisions) != 3 {
		t.Errorf("expected 3 OnDecision calls, got %d", len(decisions))
	}
}

func TestBuildToolset_SpawnBinderLateBound(t *testing.T) {
	ws := t.TempDir()
	binder := tools.NewSpawnBinder()
	reg, err := buildToolset(toolsetConfig{Policy: permissivePolicy(ws), Workspace: ws, Spawn: binder})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	ctx := context.Background()
	agents := map[string]any{"agents": []any{map[string]any{"role": "r", "task": "t"}}}

	if _, err := reg.Execute(ctx, "spawn_agents", agents); err == nil {
		t.Error("unbound spawn tool should fail")
	}
	binder.Bind(func(_ context.Context, role, task string, _ []string) (string, error) {
		return role + ":" + task, nil
	})
	out, err := reg.Execute(ctx, "spawn_agents", agents)
	if err != nil || out != "r:t" {
		t.Errorf("bound spawn: out=%q err=%v", out, err)
	}
}

func TestBuildToolset_MemoryTools(t *testing.T) {
	ws := t.TempDir()
	reg, err := buildToolset(toolsetConfig{
		Policy:     permissivePolicy(ws),
		Workspace:  ws,
		Scratchpad: memory.NewInMemoryStore(),
		Memory:     memory.NewInMemoryStore(),
	})
	if err != nil {
		t.Fatalf("buildToolset: %v", err)
	}
	ctx := context.Background()
	if _, err := reg.Execute(ctx, "scratchpad_write", map[string]any{"key": "k", "value": "v"}); err != nil {
		t.Fatalf("scratchpad_write: %v", err)
	}
	out, err := reg.Execute(ctx, "scratchpad_read", map[string]any{"key": "k"})
	if err != nil || !strings.Contains(out, "v") {
		t.Errorf("scratchpad_read: out=%q err=%v", out, err)
	}
	if _, err := reg.Execute(ctx, "remember", map[string]any{"findings": []any{"the sky is blue"}}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	out, err = reg.Execute(ctx, "recall", map[string]any{"query": "sky"})
	if err != nil || !strings.Contains(out, "blue") {
		t.Errorf("recall: out=%q err=%v", out, err)
	}
}

func TestPathGuard_CwdResolutionForUnconfinedTools(t *testing.T) {
	ws := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)
	pol := permissivePolicy(ws)
	pol.Tools["patch"] = &policy.ToolPolicy{Deny: []string{filepath.Join(cwd, "secret*")}}
	g := pathGuard{pol: pol, tool: "patch", keys: []string{"path"}}
	args, err := tools.Validate(map[string]tools.Param{"path": {Type: tools.StringParam}}, map[string]any{"path": "secret.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Check(t.Context(), args); err == nil {
		t.Fatal("relative path must be checked against the cwd for patch")
	}
}
