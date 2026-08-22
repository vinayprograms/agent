package executor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
)

// fakeMCPClient is an in-memory mcp.Client.
type fakeMCPClient struct {
	tools []mcp.Tool
	call  func(name string, args map[string]any) (*mcp.Result, error)
}

func (c *fakeMCPClient) Tools() []mcp.Tool { return c.tools }
func (c *fakeMCPClient) CallTool(_ context.Context, name string, args map[string]any) (*mcp.Result, error) {
	return c.call(name, args)
}
func (c *fakeMCPClient) Close() error { return nil }

func echoTool(name string) fakeTool {
	return fakeTool{
		name:   name,
		params: map[string]tools.Param{"text": {Type: tools.StringParam, Required: true}},
		run: func(_ context.Context, args tools.Args) (string, error) {
			s, _ := args.String("text")
			return s, nil
		},
	}
}

func newToolExecutor(t *testing.T, extra ...tools.Tool) (*Executor, *session.Session) {
	t.Helper()
	sess := &session.Session{}
	reg, _ := newTestRegistry(t, t.TempDir(), extra...)
	exec := mustNew(t, Config{
		Workflow: &agentfile.Workflow{Name: "tools"},
		Model:    llmmock.New(),
		Registry: reg,
		Policy:   permissivePolicy(),
		Session:  sess,
		Debug:    true,
	})
	return exec, sess
}

func TestExecuteTool_Success(t *testing.T) {
	exec, sess := newToolExecutor(t, echoTool("echo"))
	var hookResult any
	exec.Hooks().On(hooks.ToolCall, func(_ context.Context, evt hooks.Event) { hookResult = evt.Data["result"] })

	out, err := exec.executeTool(context.Background(), llm.ToolCallResponse{ID: "1", Name: "echo", Args: map[string]any{"text": "hi"}})
	if err != nil || out != "hi" {
		t.Fatalf("got %q, %v", out, err)
	}
	if hookResult != "hi" {
		t.Errorf("ToolCall hook result = %v", hookResult)
	}
	calls := eventsOfType(sess, session.EventToolCall)
	results := eventsOfType(sess, session.EventToolResult)
	if len(calls) != 1 || len(results) != 1 || calls[0].CorrelationID != results[0].CorrelationID {
		t.Fatalf("expected correlated call/result events: %+v %+v", calls, results)
	}
	if results[0].Content != "hi" || results[0].Tool != "echo" {
		t.Errorf("unexpected result event %+v", results[0])
	}
}

func TestExecuteTool_UnknownTool(t *testing.T) {
	exec, _ := newToolExecutor(t)
	_, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "nope"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "read") {
		t.Fatalf("expected unknown-tool error listing tools, got %v", err)
	}
}

func TestExecuteTool_NoRegistry(t *testing.T) {
	exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	if _, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "read"}); err == nil || !strings.Contains(err.Error(), "no tool registry") {
		t.Fatalf("expected no-registry error, got %v", err)
	}
}

func TestExecuteTool_ArgValidationAndToolError(t *testing.T) {
	failing := fakeTool{name: "fail", run: func(context.Context, tools.Args) (string, error) { return "", errors.New("kaboom") }}
	exec, sess := newToolExecutor(t, echoTool("echo"), failing)
	var hookErr error
	exec.Hooks().On(hooks.ToolError, func(_ context.Context, evt hooks.Event) { hookErr = evt.Data["error"].(error) })

	// Missing required arg is rejected by the registry before execution.
	if _, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "echo", Args: map[string]any{}}); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "fail"}); err == nil || hookErr == nil {
		t.Fatalf("expected tool error and ToolError hook, got %v / %v", err, hookErr)
	}
	results := eventsOfType(sess, session.EventToolResult)
	if len(results) != 2 || results[1].Error != "kaboom" {
		t.Errorf("expected error recorded on result event: %+v", results)
	}
}

func TestExecuteTool_ExternalResultRegisteredAsUntrusted(t *testing.T) {
	fetch := fakeTool{name: "web_fetch", run: func(context.Context, tools.Args) (string, error) { return "page body", nil }}
	empty := fakeTool{name: "web_search", run: func(context.Context, tools.Args) (string, error) { return "", nil }}
	sess := &session.Session{}
	reg, _ := newTestRegistry(t, t.TempDir(), fetch, empty)
	exec := mustNew(t, Config{
		Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(), Registry: reg,
		Session: sess, Security: &SecurityConfig{Reviewer: denyingReviewer()},
	})
	if _, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "web_fetch"}); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "web_search"}); err != nil {
		t.Fatal(err)
	}
	blocks := eventsOfType(sess, session.EventSecurityBlock)
	if len(blocks) != 1 || blocks[0].Meta.Source != "tool:web_fetch" {
		t.Fatalf("expected one block from web_fetch only, got %+v", blocks)
	}
	if ids := exec.guard.UntrustedIDs(); len(ids) != 1 {
		t.Errorf("expected one untrusted block in guard, got %v", ids)
	}
	// Security now gates the next high-risk call (reviewer denies).
	if _, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "web_fetch"}); err == nil || !strings.Contains(err.Error(), "security:") {
		t.Fatalf("expected security denial, got %v", err)
	}
}

func TestExecuteTool_MCP(t *testing.T) {
	mgr := mcp.NewManager()
	client := &fakeMCPClient{
		tools: []mcp.Tool{{Name: "read_file", Description: "reads", InputSchema: map[string]any{"type": "object"}}},
		call: func(name string, args map[string]any) (*mcp.Result, error) {
			if name == "boom" {
				return nil, errors.New("server error")
			}
			return &mcp.Result{Content: []mcp.Content{{Type: "text", Text: "file:" + args["path"].(string)}, {Type: "image"}}}, nil
		},
	}
	if err := mgr.Register("fs", client); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{}
	exec := mustNew(t, Config{
		Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(), MCPManager: mgr,
		Policy: permissivePolicy(), Session: sess, Security: &SecurityConfig{Reviewer: denyingReviewer()},
	})

	defs := exec.getAllToolDefinitions()
	if len(defs) != 1 || defs[0].Name != "mcp_fs_read_file" || !strings.HasPrefix(defs[0].Description, "[MCP:fs]") {
		t.Fatalf("unexpected MCP definitions: %+v", defs)
	}

	var hookFired bool
	exec.Hooks().On(hooks.MCPToolCall, func(context.Context, hooks.Event) { hookFired = true })
	out, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "mcp_fs_read_file", Args: map[string]any{"path": "/a"}})
	if err != nil || out != "file:/a" || !hookFired {
		t.Fatalf("got %q %v hook=%v", out, err, hookFired)
	}
	// MCP results are untrusted content.
	if len(exec.guard.UntrustedIDs()) != 1 {
		t.Error("expected MCP result registered as untrusted")
	}

	if _, err := exec.executeTool(context.Background(), llm.ToolCallResponse{Name: "mcp_fs_boom"}); err == nil {
		t.Error("expected server error")
	}
	if _, err := exec.executeMCPTool(context.Background(), llm.ToolCallResponse{Name: "mcp_fsonly"}); err == nil || !strings.Contains(err.Error(), "invalid MCP tool name") {
		t.Errorf("expected invalid name error, got %v", err)
	}

	// Policy: MCP disabled => denied.
	exec.policy.MCP = &policy.MCPPolicy{Enabled: false}
	if _, err := exec.executeMCPTool(context.Background(), llm.ToolCallResponse{Name: "mcp_fs_read_file", Args: map[string]any{"path": "/a"}}); err == nil || !strings.Contains(err.Error(), "policy denied") {
		t.Errorf("expected policy denial, got %v", err)
	}
}

func TestExecuteToolsParallel(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(name string) fakeTool {
		return fakeTool{name: name, run: func(context.Context, tools.Args) (string, error) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return "ok:" + name, nil
		}}
	}
	asyncDone := make(chan struct{})
	remember := fakeTool{name: "remember", run: func(context.Context, tools.Args) (string, error) {
		defer close(asyncDone)
		return "", errors.New("async failure is logged, not returned")
	}}
	exec, _ := newToolExecutor(t, record("a"), record("b"), record("write"), record("bash"), remember)

	if got := exec.executeToolsParallel(context.Background(), nil); got != nil {
		t.Fatal("expected nil for no calls")
	}

	// Single call path.
	single := exec.executeToolsParallel(context.Background(), []llm.ToolCallResponse{{ID: "s", Name: "a"}})
	if len(single) != 1 || single[0].Content != "ok:a" || single[0].ToolCallID != "s" || single[0].Role != "tool" {
		t.Fatalf("unexpected single result %+v", single)
	}
	errMsg := exec.executeToolsParallel(context.Background(), []llm.ToolCallResponse{{ID: "e", Name: "missing"}})
	if !strings.HasPrefix(errMsg[0].Content, "Error:") {
		t.Errorf("expected error content, got %q", errMsg[0].Content)
	}

	order = nil
	calls := []llm.ToolCallResponse{
		{ID: "1", Name: "bash"},     // serialized
		{ID: "2", Name: "a"},        // parallel
		{ID: "3", Name: "remember"}, // async
		{ID: "4", Name: "write"},    // serialized
		{ID: "5", Name: "b"},        // parallel
	}
	msgs := exec.executeToolsParallel(context.Background(), calls)
	<-asyncDone
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	want := map[string]string{"1": "ok:bash", "2": "ok:a", "3": "OK", "4": "ok:write", "5": "ok:b"}
	for i, m := range msgs {
		if m.ToolCallID != calls[i].ID || m.Content != want[m.ToolCallID] {
			t.Errorf("msg %d = %+v", i, m)
		}
	}
	// Serialized tools run after parallel ones, in request order.
	mu.Lock()
	defer mu.Unlock()
	idx := func(n string) int {
		for i, o := range order {
			if o == n {
				return i
			}
		}
		return -1
	}
	if idx("bash") > idx("write") || idx("a") > idx("bash") || idx("b") > idx("bash") {
		t.Errorf("unexpected execution order %v", order)
	}
}

func TestExecuteAsyncTool_RecoversPanic(t *testing.T) {
	panicky := fakeTool{name: "remember", run: func(context.Context, tools.Args) (string, error) { panic("oops") }}
	exec, _ := newToolExecutor(t, panicky)
	exec.executeAsyncTool(context.Background(), llm.ToolCallResponse{Name: "remember"}) // must not propagate
}

func TestToolClassification(t *testing.T) {
	if !isAsyncTool("remember") || !isAsyncTool("scratchpad_write") || isAsyncTool("read") {
		t.Error("async classification")
	}
	if !isSerializeTool("bash") || !isSerializeTool("spawn_agents") || isSerializeTool("read") {
		t.Error("serialize classification")
	}
	if !isExternalTool("web_fetch") || isExternalTool("read") {
		t.Error("external classification")
	}
}

func TestApplyToolTimeout(t *testing.T) {
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(), TimeoutMCP: 5, TimeoutWebFetch: 5})
	bg := context.Background()

	if _, cancel := exec.applyToolTimeout(bg, "read"); cancel != nil {
		t.Error("non-network tools get no timeout")
	}
	if _, cancel := exec.applyToolTimeout(bg, "web_search"); cancel != nil {
		t.Error("unconfigured timeout must be a no-op")
	}
	ctx, cancel := exec.applyToolTimeout(bg, "mcp_x_y")
	if cancel == nil {
		t.Fatal("expected timeout for mcp tool")
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Error("expected deadline")
	}
	cancel()

	short, c := context.WithTimeout(bg, time.Second)
	defer c()
	if _, cancel := exec.applyToolTimeout(short, "web_fetch"); cancel != nil {
		t.Error("existing shorter deadline must win")
	}
	long, c2 := context.WithTimeout(bg, time.Hour)
	defer c2()
	if _, cancel := exec.applyToolTimeout(long, "web_fetch"); cancel == nil {
		t.Error("longer existing deadline must be tightened")
	} else {
		cancel()
	}
}
