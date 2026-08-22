package executor

import (
	"context"
	"testing"

	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
)

// modelFunc adapts a function to llm.Model. Unlike llmmock it keeps no
// state, so it is safe for tests that call it from parallel sub-agents.
type modelFunc func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)

func (f modelFunc) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return f(ctx, req)
}

// permissivePolicy returns a policy with every tool enabled (the old
// policy.New() default).
func permissivePolicy() *policy.Policy {
	pol := policy.New()
	pol.DefaultDeny = false
	return pol
}

// newTestRegistry builds a small real registry rooted at ws: read, ls, pwd,
// the spawn_agents binder, plus any extra tools.
func newTestRegistry(t *testing.T, ws string, extra ...tools.Tool) (*tools.Registry, *tools.SpawnBinder) {
	t.Helper()
	reg := tools.NewRegistry()
	binder := tools.NewSpawnBinder()
	all := append([]tools.Tool{tools.Read(ws), tools.Ls(ws), tools.Pwd(), binder.Tool()}, extra...)
	for _, tl := range all {
		if err := reg.Register(tools.New(tl)); err != nil {
			t.Fatalf("register %s: %v", tl.Name(), err)
		}
	}
	return reg, binder
}

// fakeTool is a minimal tools.Tool for tests.
type fakeTool struct {
	name   string
	params map[string]tools.Param
	run    func(ctx context.Context, args tools.Args) (string, error)
}

func (f fakeTool) Name() string                       { return f.name }
func (f fakeTool) Description() string                { return "fake " + f.name }
func (f fakeTool) Parameters() map[string]tools.Param { return f.params }
func (f fakeTool) Execute(ctx context.Context, args tools.Args) (string, error) {
	return f.run(ctx, args)
}
