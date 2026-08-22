package main

import (
	"context"
	"strings"
	"testing"
)

func TestStepGate_Answers(t *testing.T) {
	cases := []struct {
		name    string
		stdin   string
		want    []bool
		prompts int
	}{
		{"enter continues", "\n", []bool{true}, 1},
		{"y continues", "y\n", []bool{true}, 1},
		{"yes continues", "YES\n", []bool{true}, 1},
		{"n aborts", "n\n", []bool{false}, 1},
		{"no aborts", "No\n", []bool{false}, 1},
		{"garbage re-asks", "maybe\ny\n", []bool{true}, 2},
		{"eof aborts", "", []bool{false}, 1},
		{"second call aborts after eof", "y\n", []bool{true, false}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			gate := stepGate(strings.NewReader(tc.stdin), &out)
			for i, want := range tc.want {
				got, err := gate(context.Background(), "build", "g1")
				if err != nil {
					t.Fatalf("gate call %d: %v", i, err)
				}
				if got != want {
					t.Errorf("gate call %d = %v, want %v", i, got, want)
				}
			}
			if n := strings.Count(out.String(), "Continue? [Y/n]"); n != tc.prompts {
				t.Errorf("prompts = %d, want %d (%q)", n, tc.prompts, out.String())
			}
			if !strings.Contains(out.String(), `RUN build › goal "g1" finished.`) {
				t.Errorf("prompt = %q, want the step and goal named", out.String())
			}
		})
	}
}

// --step is refused unless both stdin and stderr are terminals, and reaches
// the runtime deps when they are.
func TestRunStep_RequiresTerminal(t *testing.T) {
	for _, tc := range []struct {
		name string
		tty  bool
	}{{"no tty", false}, {"tty", true}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.isTerminal = func(any) bool { return tc.tty }
			err := h.exec("run", "--goal", "hi", "--step", "--workspace", t.TempDir())
			if !tc.tty {
				if err == nil || !strings.Contains(err.Error(), "--step needs an interactive terminal") {
					t.Fatalf("run --step error = %v, want the terminal requirement", err)
				}
				if h.rt.ran {
					t.Error("the runtime must not run when --step is rejected")
				}
				return
			}
			if err != nil {
				t.Fatalf("run --step: %v", err)
			}
			if h.lastRun.StepGate == nil {
				t.Error("run --step must install a StepGate")
			}
		})
	}
}

// Without --step no gate is installed.
func TestRun_NoStepGateByDefault(t *testing.T) {
	h := newHarness(t)
	if err := h.exec("run", "--goal", "hi", "--workspace", t.TempDir()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if h.lastRun.StepGate != nil {
		t.Error("StepGate must be nil without --step")
	}
}
