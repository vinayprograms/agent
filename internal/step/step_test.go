package step

import (
	"context"
	"errors"
	"testing"
)

// fakeStep is a minimal Step for testing composition.
type fakeStep struct {
	name string
	err  error
	fn   func(ctx context.Context, state *State) error
	runs *[]string
}

func (f *fakeStep) Name() string { return f.name }

func (f *fakeStep) Execute(ctx context.Context, state *State) error {
	if f.runs != nil {
		*f.runs = append(*f.runs, f.name)
	}
	if f.fn != nil {
		return f.fn(ctx, state)
	}
	return f.err
}

func TestNewState(t *testing.T) {
	inputs := map[string]string{"a": "1"}
	s := NewState(inputs)

	if got := s.Inputs["a"]; got != "1" {
		t.Errorf("NewState(%v).Inputs[a] = %q, want %q", inputs, got, "1")
	}
	if s.Outputs == nil {
		t.Error("NewState(...).Outputs = nil, want initialized map")
	}
	if len(s.Outputs) != 0 {
		t.Errorf("NewState(...).Outputs = %v, want empty", s.Outputs)
	}
}

func TestSequence_Name(t *testing.T) {
	tests := []struct {
		name  string
		steps []Step
		want  string
	}{
		{name: "empty", steps: nil, want: "empty"},
		{name: "single", steps: []Step{&fakeStep{name: "a"}}, want: "a..."},
		{name: "multiple", steps: []Step{&fakeStep{name: "a"}, &fakeStep{name: "b"}}, want: "a..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sequence(tt.steps...).Name()
			if got != tt.want {
				t.Errorf("Sequence(%v).Name() = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestSequence_Execute_Order(t *testing.T) {
	var runs []string
	seq := Sequence(
		&fakeStep{name: "a", runs: &runs},
		&fakeStep{name: "b", runs: &runs},
		&fakeStep{name: "c", runs: &runs},
	)

	if err := seq.Execute(t.Context(), NewState(nil)); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}
	want := []string{"a", "b", "c"}
	if len(runs) != len(want) {
		t.Fatalf("Execute() ran steps %v, want %v", runs, want)
	}
	for i := range want {
		if runs[i] != want[i] {
			t.Errorf("Execute() ran steps %v, want %v", runs, want)
		}
	}
}

func TestSequence_Execute_StopsOnFirstError(t *testing.T) {
	var runs []string
	wantErr := errors.New("boom")
	seq := Sequence(
		&fakeStep{name: "a", runs: &runs},
		&fakeStep{name: "b", runs: &runs, err: wantErr},
		&fakeStep{name: "c", runs: &runs},
	)

	err := seq.Execute(t.Context(), NewState(nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() = %v, want %v", err, wantErr)
	}
	want := []string{"a", "b"}
	if len(runs) != len(want) {
		t.Fatalf("Execute() ran steps %v, want stop after %v", runs, want)
	}
}

func TestSequence_Execute_StopsOnContextCancel(t *testing.T) {
	var runs []string
	ctx, cancel := context.WithCancel(t.Context())
	seq := Sequence(
		&fakeStep{name: "a", runs: &runs, fn: func(ctx context.Context, state *State) error {
			cancel()
			return nil
		}},
		&fakeStep{name: "b", runs: &runs},
	)

	err := seq.Execute(ctx, NewState(nil))
	if err == nil {
		t.Fatal("Execute() = nil, want context canceled error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Execute() = %v, want context.Canceled", err)
	}
	want := []string{"a"}
	if len(runs) != len(want) {
		t.Fatalf("Execute() ran steps %v, want stop after %v", runs, want)
	}
}

func TestSequence_Execute_Empty(t *testing.T) {
	if err := Sequence().Execute(t.Context(), NewState(nil)); err != nil {
		t.Errorf("Execute() on empty sequence = %v, want nil", err)
	}
}
