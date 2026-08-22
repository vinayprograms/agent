package hooks

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFireOrderAndPayload(t *testing.T) {
	r := NewRegistry()
	var got []string
	for _, id := range []string{"a", "b", "c"} {
		r.On(GoalStart, func(_ context.Context, e Event) {
			got = append(got, id+":"+e.Type+":"+e.Data["goal"].(string))
		})
	}
	r.On(GoalComplete, func(context.Context, Event) { t.Error("GoalComplete hook fired for GoalStart") })

	r.Fire(t.Context(), GoalStart, map[string]any{"goal": "g"})

	want := []string{"a:goal.start:g", "b:goal.start:g", "c:goal.start:g"}
	if !slices.Equal(got, want) {
		t.Errorf("Fire order = %v, want %v", got, want)
	}
}

func TestFireNoHandlers(t *testing.T) {
	NewRegistry().Fire(t.Context(), ToolCall, nil) // must not panic
}

func TestFireNilRegistry(t *testing.T) {
	var r *Registry
	r.Fire(t.Context(), ToolCall, nil) // must not panic
}

func TestConcurrentOnAndFire(t *testing.T) {
	r := NewRegistry()
	var fired atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r.On(ToolCall, func(context.Context, Event) { fired.Add(1) })
			r.Fire(t.Context(), ToolCall, nil)
		})
	}
	wg.Wait()
	r.Fire(t.Context(), ToolCall, nil)
	if n := fired.Load(); n < 8 {
		t.Errorf("hooks fired %d times, want at least 8", n)
	}
}
