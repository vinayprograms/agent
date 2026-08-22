package swarm

import (
	"errors"
	"strings"
	"testing"

	"github.com/vinayprograms/agentkit/tools"
)

func newArgs(t *testing.T, tool tools.Tool, raw map[string]any) tools.Args {
	t.Helper()
	// Validate against an all-optional copy so Execute's own required checks are exercised.
	params := map[string]tools.Param{}
	for k, p := range tool.Parameters() {
		p.Required = false
		params[k] = p
	}
	args, err := tools.Validate(params, raw)
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func TestDispatchToolMetadata(t *testing.T) {
	var _ tools.Tool = (*DispatchTool)(nil)

	bare := NewDispatchTool(&fakeBus{}, "mgr", nil)
	if bare.Name() != "dispatch" {
		t.Errorf("Name() = %q", bare.Name())
	}
	if strings.Contains(bare.Description(), "Available capabilities") {
		t.Error("description should not list capabilities when none configured")
	}

	tool := NewDispatchTool(&fakeBus{}, "mgr", []WorkerCapability{{Name: "develop", Replicas: 2}})
	desc := tool.Description()
	if !strings.Contains(desc, "Dispatch a task to a worker capability channel") ||
		!strings.Contains(desc, `- "develop" (2 workers)`) {
		t.Errorf("Description() = %q", desc)
	}

	params := tool.Parameters()
	for _, name := range []string{"capability", "task"} {
		p, ok := params[name]
		if !ok || p.Type != tools.StringParam || !p.Required || p.Description == "" {
			t.Errorf("param %q = %+v", name, p)
		}
	}
}

func TestDispatchToolExecute(t *testing.T) {
	t.Run("missing capability", func(t *testing.T) {
		tool := NewDispatchTool(&fakeBus{}, "mgr", nil)
		_, err := tool.Execute(t.Context(), newArgs(t, tool, map[string]any{"task": "x"}))
		if err == nil || err.Error() != "capability is required" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("missing task", func(t *testing.T) {
		tool := NewDispatchTool(&fakeBus{}, "mgr", nil)
		_, err := tool.Execute(t.Context(), newArgs(t, tool, map[string]any{"capability": "develop"}))
		if err == nil || err.Error() != "task description is required" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("publish error", func(t *testing.T) {
		want := errors.New("down")
		tool := NewDispatchTool(&fakeBus{err: want}, "mgr", nil)
		_, err := tool.Execute(t.Context(), newArgs(t, tool, map[string]any{"capability": "develop", "task": "x"}))
		if !errors.Is(err, want) || !strings.HasPrefix(err.Error(), "publishing to work.develop.t-") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("success", func(t *testing.T) {
		bus := &fakeBus{}
		tool := NewDispatchTool(bus, "mgr", nil)
		out, err := tool.Execute(t.Context(), newArgs(t, tool, map[string]any{"capability": "develop", "task": "build it"}))
		if err != nil {
			t.Fatal(err)
		}
		msgs := bus.messages()
		if len(msgs) != 1 {
			t.Fatalf("published %d messages, want 1", len(msgs))
		}
		tm, err := UnmarshalTaskMessage(msgs[0].Data)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tm.TaskID, "t-") || len(tm.TaskID) != 10 {
			t.Errorf("TaskID = %q", tm.TaskID)
		}
		if msgs[0].Subject != "work.develop."+tm.TaskID {
			t.Errorf("subject = %q", msgs[0].Subject)
		}
		if tm.Capability != "develop" || tm.Inputs["task"] != "build it" || tm.Attempt != 1 ||
			tm.SubmittedBy != "mgr" || tm.SubmittedAt.IsZero() {
			t.Errorf("task envelope = %+v", tm)
		}
		if out != "Dispatched task "+tm.TaskID+` to capability "develop"` {
			t.Errorf("result text = %q", out)
		}
	})
}
