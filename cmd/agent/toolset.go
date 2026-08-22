// Tool set wiring: registers every builtin the policy enables and attaches
// the policy guards to it. The kit provides the blocks; this file decides
// which tool gets which guard (A-C2).
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/vinayprograms/agent/internal/tools/websearch"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
)

// toolsetConfig carries everything buildToolset needs. Optional members
// (nil or zero) cause the tools that need them to be skipped:
//   - Memory nil  -> remember/recall are not registered
//   - BashGate nil -> bash is not registered even when the policy enables it
//     (fail closed: bash never runs unguarded)
//   - Spawn nil   -> spawn_agents is not registered
type toolsetConfig struct {
	Policy      *policy.Policy
	Workspace   string
	Creds       credentials.Lookup
	Summarizer  tools.Summarizer // nil: web_fetch returns full page text
	HTTPTimeout time.Duration    // zero: kit default
	Scratchpad  tools.Scratchpad
	Memory      tools.Memory
	BashGate    tools.Guard
	Spawn       *tools.SpawnBinder

	// Web search configuration ([web].searxng_url / search_provider); both
	// may be empty, in which case credentials and env decide.
	SearXNGURL     string
	SearchProvider string
}

// buildToolset registers every builtin tool that the policy enables.
// Filesystem tools get policy.AllowedDirs as extra roots and a path guard;
// web_fetch gets a domain guard; bash gets the shellguard gate; web_search is
// the in-repo websearch tool (registered instead of the kit's Search).
func buildToolset(c toolsetConfig) (*tools.Registry, error) {
	if c.Policy == nil {
		return nil, fmt.Errorf("toolset: policy is required")
	}
	reg := tools.NewRegistry()
	ws := c.Workspace
	roots := c.Policy.AllowedDirs

	add := func(t tools.Tool, guards ...tools.Guard) error {
		if !c.Policy.IsToolEnabled(t.Name()) {
			return nil
		}
		e := tools.New(t)
		for _, g := range guards {
			e = e.With(g)
		}
		return reg.Register(e)
	}
	paths := func(tool string, keys ...string) tools.Guard {
		return pathGuard{pol: c.Policy, base: ws, tool: tool, keys: keys}
	}
	cwdPaths := func(tool string, keys ...string) tools.Guard {
		return pathGuard{pol: c.Policy, tool: tool, keys: keys}
	}

	var webOpts []tools.WebOption
	if c.HTTPTimeout > 0 {
		webOpts = append(webOpts, tools.WithHTTPTimeout(c.HTTPTimeout))
	}

	steps := []func() error{
		// Filesystem tools: kit confinement (workspace + extra roots) plus
		// the policy path guard (allow/deny patterns, protected files).
		func() error { return add(tools.Read(ws, roots...), paths("read", "path")) },
		func() error { return add(tools.Write(ws, roots...), paths("write", "path")) },
		func() error { return add(tools.Edit(ws, roots...), paths("edit", "path")) },
		func() error { return add(tools.Grep(ws, roots...), paths("read", "path")) },
		func() error { return add(tools.Glob(ws), paths("read", "pattern")) },
		func() error { return add(tools.Ls(ws, roots...), paths("ls", "path")) },
		func() error { return add(tools.Head(ws, roots...), paths("head", "path")) },
		func() error { return add(tools.Tail(ws, roots...), paths("tail", "path")) },
		func() error { return add(tools.Tree(ws), paths("tree", "path")) },
		func() error { return add(tools.Mkdir(ws, roots...), paths("mkdir", "path")) },
		func() error { return add(tools.Mv(ws, roots...), paths("mv", "source", "destination")) },
		func() error { return add(tools.Cp(ws, roots...), paths("cp", "source", "destination")) },
		func() error { return add(tools.Rm(ws, roots...), paths("rm", "path")) },
		// diff/patch are not workspace-confined by the kit: they resolve
		// relative paths against the process cwd, so the guard must too.
		func() error { return add(tools.Diff(), cwdPaths("diff", "file_a", "file_b")) },
		func() error { return add(tools.Patch(), cwdPaths("patch", "path")) },
		func() error { return add(tools.Git(ws), paths("git", "cwd")) },

		// Process/system tools: no path argument to guard.
		func() error { return add(tools.Pwd()) },
		func() error { return add(tools.Hostname()) },
		func() error { return add(tools.Whoami()) },
		func() error { return add(tools.Sysinfo()) },
		func() error { return add(tools.Datetime()) },
		func() error { return add(tools.Env()) },
		func() error { return add(tools.Which()) },

		// Shell: only behind the shellguard gate.
		func() error {
			if c.BashGate == nil {
				return nil
			}
			return add(tools.Bash(ws), c.BashGate)
		},

		// Web: domain guard on fetch; in-repo search tool.
		func() error {
			return add(tools.Fetch(c.Summarizer, webOpts...), domainGuard{pol: c.Policy, tool: "web_fetch"})
		},
		func() error { return add(websearch.New(c.Creds, c.SearXNGURL, c.SearchProvider)) },

		// Sub-agents (late-bound by the executor).
		func() error {
			if c.Spawn == nil {
				return nil
			}
			return add(c.Spawn.Tool())
		},

		// Memory.
		func() error {
			if c.Scratchpad == nil {
				return nil
			}
			for _, t := range []tools.Tool{
				tools.ScratchpadRead(c.Scratchpad),
				tools.ScratchpadWrite(c.Scratchpad),
				tools.ScratchpadList(c.Scratchpad),
				tools.ScratchpadSearch(c.Scratchpad),
			} {
				if err := add(t); err != nil {
					return err
				}
			}
			return nil
		},
		func() error {
			if c.Memory == nil {
				return nil
			}
			if err := add(tools.Remember(c.Memory)); err != nil {
				return err
			}
			return add(tools.Recall(c.Memory))
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, fmt.Errorf("toolset: %w", err)
		}
	}
	return reg, nil
}

// pathGuard checks every named path argument against the policy. Relative
// paths are resolved against base — the workspace for the kit's confined
// file tools, the process cwd for diff/patch which the kit does not confine.
// An empty base means "resolve against the cwd at check time". Absent or
// empty arguments are skipped (optional paths such as grep's default
// directory).
type pathGuard struct {
	pol  policy.Lookup
	base string
	tool string
	keys []string
}

func (g pathGuard) Check(_ context.Context, args tools.Args) error {
	for _, key := range g.keys {
		p, err := args.String(key)
		if err != nil || p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			base := g.base
			if base == "" {
				base, _ = os.Getwd()
			}
			p = filepath.Join(base, p)
		}
		if ok, why := g.pol.CheckPath(g.tool, p); !ok {
			return fmt.Errorf("denied: %s", why)
		}
	}
	return nil
}

// domainGuard checks the "url" argument's host against the policy's domain
// allow list for the tool.
type domainGuard struct {
	pol  policy.Lookup
	tool string
}

func (g domainGuard) Check(_ context.Context, args tools.Args) error {
	raw, err := args.String("url")
	if err != nil {
		return fmt.Errorf("denied: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("denied: invalid url %q", raw)
	}
	if ok, why := g.pol.CheckDomain(g.tool, u.Hostname()); !ok {
		return fmt.Errorf("denied: %s", why)
	}
	return nil
}
