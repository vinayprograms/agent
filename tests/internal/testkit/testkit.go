// Package testkit builds the tool registry and executor shapes the tests/*
// packages share. It mirrors the cmd/agent runtime wiring intent: every
// enabled filesystem tool is guarded by the policy's path checks, bash by a
// shellguard gate, and tools the policy disables are never registered.
package testkit

import (
	"context"
	"fmt"
	"testing"

	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/shellguard"
	"github.com/vinayprograms/agentkit/tools"
)

// pathGuard adapts policy.CheckPath into a tools.Guard. Denials carry the
// word "denied" so tests can assert on it regardless of the policy reason.
type pathGuard struct {
	pol  *policy.Policy
	tool string
	keys []string
}

func (g pathGuard) Check(_ context.Context, args tools.Args) error {
	for _, key := range g.keys {
		if !args.Has(key) {
			continue
		}
		path, err := args.String(key)
		if err != nil {
			return fmt.Errorf("%s: %w", g.tool, err)
		}
		if ok, reason := g.pol.CheckPath(g.tool, path); !ok {
			return fmt.Errorf("%s: access denied: %s", g.tool, reason)
		}
	}
	return nil
}

// PermissivePolicy returns a policy with every tool enabled and no path
// restrictions (the old policy.New() default).
func PermissivePolicy() *policy.Policy {
	pol := policy.New()
	pol.DefaultDeny = false
	return pol
}

// Registry builds a registry rooted at ws containing read, write, edit, ls,
// glob, grep, mv, cp, rm, mkdir and bash — each only if pol enables it.
// Filesystem tools are wrapped in a path guard over pol; bash is gated by
// shellguard with pol's bash deny list as user-denied commands. extra tools
// are registered unguarded.
func Registry(t testing.TB, pol *policy.Policy, ws string, extra ...tools.Tool) *tools.Registry {
	t.Helper()
	if pol == nil {
		pol = PermissivePolicy()
	}
	reg := tools.NewRegistry()
	roots := pol.AllowedDirs

	register := func(entry *tools.Entry, name string) {
		if !pol.IsToolEnabled(name) {
			return
		}
		if err := reg.Register(entry); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	guarded := func(tl tools.Tool, keys ...string) {
		register(tools.New(tl).With(pathGuard{pol: pol, tool: tl.Name(), keys: keys}), tl.Name())
	}

	guarded(tools.Read(ws, roots...), "path")
	guarded(tools.Write(ws, roots...), "path")
	guarded(tools.Edit(ws, roots...), "path")
	guarded(tools.Ls(ws, roots...), "path")
	guarded(tools.Glob(ws)) // pattern-based; workspace confinement only
	guarded(tools.Grep(ws, roots...), "path")
	guarded(tools.Mv(ws, roots...), "source", "destination")
	guarded(tools.Cp(ws, roots...), "source", "destination")
	guarded(tools.Rm(ws, roots...), "path")
	guarded(tools.Mkdir(ws, roots...), "path")

	var denied []string
	if bp := pol.GetToolPolicy("bash"); bp != nil {
		denied = bp.Deny
	}
	gate := shellguard.New(shellguard.Bash(), ws, roots, denied, nil, "")
	register(tools.New(tools.Bash(ws)).With(gate), "bash")

	for _, tl := range extra {
		register(tools.New(tl), tl.Name())
	}
	return reg
}

// Executor constructs an executor from cfg or fails the test.
func Executor(t testing.TB, cfg executor.Config) *executor.Executor {
	t.Helper()
	e, err := executor.New(cfg)
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	return e
}
