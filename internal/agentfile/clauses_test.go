package agentfile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// permutations returns every ordering of the given clauses.
func permutations(clauses []string) [][]string {
	if len(clauses) <= 1 {
		return [][]string{slices.Clone(clauses)}
	}
	var out [][]string
	for i := range clauses {
		rest := slices.Concat(clauses[:i:i], clauses[i+1:])
		for _, p := range permutations(rest) {
			out = append(out, append([]string{clauses[i]}, p...))
		}
	}
	return out
}

// Goal clauses are order-free: every permutation parses to the same goal.
func TestParser_GoalClauseOrderFree(t *testing.T) {
	for _, perm := range permutations([]string{"-> report", "USING analyst", "SUPERVISED HUMAN"}) {
		src := "NAME t\nAGENT analyst \"a\"\nGOAL check \"Check it\" " + strings.Join(perm, " ")
		t.Run(strings.Join(perm, "|"), func(t *testing.T) {
			wf, err := newParser(NewLexer(src)).Parse()
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", src, err)
			}
			g := wf.Goals[0]
			if !slices.Equal(g.Outputs, []string{"report"}) {
				t.Errorf("Outputs = %v, want [report]", g.Outputs)
			}
			if !slices.Equal(g.UsingAgent, []string{"analyst"}) {
				t.Errorf("UsingAgent = %v, want [analyst]", g.UsingAgent)
			}
			if g.Supervision != SupervisionEnabled || !g.HumanOnly {
				t.Errorf("Supervision = %v, HumanOnly = %v, want enabled/true", g.Supervision, g.HumanOnly)
			}
		})
	}
}

// CONVERGE clauses — including WITHIN — are order-free too.
func TestParser_ConvergeClauseOrderFree(t *testing.T) {
	for _, perm := range permutations([]string{"-> polished", "USING coder", "WITHIN 4", "UNSUPERVISED"}) {
		src := "NAME t\nAGENT coder \"c\"\nCONVERGE polish \"Polish it\" " + strings.Join(perm, " ")
		t.Run(strings.Join(perm, "|"), func(t *testing.T) {
			wf, err := newParser(NewLexer(src)).Parse()
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", src, err)
			}
			g := wf.Goals[0]
			if !g.IsConverge || g.WithinLimit == nil || *g.WithinLimit != 4 {
				t.Errorf("IsConverge = %v, WithinLimit = %v, want true/4", g.IsConverge, g.WithinLimit)
			}
			if !slices.Equal(g.Outputs, []string{"polished"}) {
				t.Errorf("Outputs = %v, want [polished]", g.Outputs)
			}
			if !slices.Equal(g.UsingAgent, []string{"coder"}) {
				t.Errorf("UsingAgent = %v, want [coder]", g.UsingAgent)
			}
			if g.Supervision != SupervisionDisabled {
				t.Errorf("Supervision = %v, want disabled", g.Supervision)
			}
		})
	}
}

func TestParser_GoalClauseErrors(t *testing.T) {
	tests := []struct {
		name, src, wantErr string
	}{
		{"duplicate outputs", "GOAL g \"x\" -> a -> b", "line 1: duplicate -> clause"},
		{"duplicate using", "GOAL g \"x\" USING a USING b", "line 1: duplicate USING clause"},
		{"conflicting supervision", "GOAL g \"x\" SUPERVISED UNSUPERVISED", "line 1: duplicate supervision clause"},
		{"duplicate supervised", "GOAL g \"x\" SUPERVISED SUPERVISED", "line 1: duplicate supervision clause"},
		{"within on plain goal", "GOAL g \"x\" WITHIN 3", "line 1: WITHIN is only valid on CONVERGE"},
		{"converge without within", "CONVERGE g \"x\" -> a", "requires WITHIN"},
		{"duplicate within", "CONVERGE g \"x\" WITHIN 3 WITHIN 4", "line 1: duplicate WITHIN clause"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newParser(NewLexer(tt.src)).Parse()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse(%q) error = %v, want containing %q", tt.src, err, tt.wantErr)
			}
		})
	}
}

// Every shipped example must parse and validate — they are the documentation.
func TestExamples_ParseAndValidate(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "agent")
	var files []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".agent" {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .agent examples found under %s", root)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			wf, err := LoadFile(f)
			if err != nil {
				t.Fatalf("LoadFile(%q) error = %v", f, err)
			}
			if err := Validate(wf); err != nil {
				t.Errorf("Validate(%q) error = %v", f, err)
			}
		})
	}
}
