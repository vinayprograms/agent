package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agentkit/memory"
)

// run executes the CLI in-process with args and returns stdout, stderr and
// an exit code derived from whether Execute returned an error (matching the
// hand-rolled CLI's os.Exit(1)-on-error convention).
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	root := NewRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		if err != errNoCommand {
			fmt.Fprintln(&errb, err)
		}
		code = 1
	}
	return out.String(), errb.String(), code
}

// seedStore creates a bleve store under dir with one observation per category.
func seedStore(t *testing.T, dir string) {
	t.Helper()
	store, err := memory.NewBleveStore(memory.BleveStoreConfig{BasePath: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.RememberFIL(ctx,
		[]string{"database uses postgres"},
		[]string{"postgres is a solid database choice"},
		[]string{"always benchmark the database"},
		"GOAL:step"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("k1", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("long", strings.Repeat("x", 150)); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantContains(t *testing.T, got string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q:\n%s", s, got)
		}
	}
}

func TestMainDispatch(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout []string
		stderr []string
	}{
		{name: "no args", code: 1, stdout: []string{"Usage:"}},
		{name: "help", args: []string{"help"}, stdout: []string{"agentmem - Memory investigation tool", "Usage:"}},
		{name: "-h", args: []string{"-h"}, stdout: []string{"Usage:"}},
		{name: "--help", args: []string{"--help"}, stdout: []string{"Usage:"}},
		{name: "unknown", args: []string{"bogus"}, code: 1, stdout: []string{"Usage:"}, stderr: []string{"Unknown command:", "bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errs, code := run(t, tt.args...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d", code, tt.code)
			}
			wantContains(t, out, tt.stdout...)
			wantContains(t, errs, tt.stderr...)
		})
	}
}

func TestList(t *testing.T) {
	seeded := t.TempDir()
	seedStore(t, seeded)
	empty := t.TempDir()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		args   []string
		code   int
		stdout []string
		absent []string
		stderr []string
	}{
		{name: "missing path", args: []string{"--limit=5"}, code: 1, stderr: []string{"storage path required"}},
		{name: "open error", args: []string{filepath.Join(file, "sub")}, code: 1, stderr: []string{"Error opening store"}},
		{name: "empty", args: []string{empty}, stdout: []string{"No observations found."}},
		{name: "all", args: []string{"--limit=50", seeded}, stdout: []string{
			"=== Findings (1) ===", "1. [", "] database uses postgres",
			"=== Insights (1) ===", "=== Lessons (1) ===",
		}},
		{name: "category", args: []string{"--category=lesson", seeded},
			stdout: []string{"=== Lessons (1) ===", "always benchmark"},
			absent: []string{"Findings", "Insights"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errs, code := run(t, append([]string{"list"}, tt.args...)...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d; stderr=%s", code, tt.code, errs)
			}
			wantContains(t, out, tt.stdout...)
			wantContains(t, errs, tt.stderr...)
			for _, s := range tt.absent {
				if strings.Contains(out, s) {
					t.Errorf("output should not contain %q:\n%s", s, out)
				}
			}
		})
	}
}

func TestSearch(t *testing.T) {
	seeded := t.TempDir()
	seedStore(t, seeded)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		args   []string
		code   int
		stdout []string
		stderr []string
	}{
		{name: "missing query", args: []string{}, code: 1, stderr: []string{"query and storage path required", "Usage: agentmem search"}},
		{name: "missing path", args: []string{"q"}, code: 1, stderr: []string{"query and storage path required"}},
		{name: "open error", args: []string{"q", filepath.Join(file, "sub")}, code: 1, stderr: []string{"Error opening store"}},
		{name: "hits", args: []string{"--limit=3", "database", seeded}, stdout: []string{
			`Search: "database"`, "=== Findings ===", "1. database uses postgres",
			"=== Insights ===", "1. postgres is a solid database choice",
			"=== Lessons ===", "1. always benchmark the database",
		}},
		{name: "no hits", args: []string{"zzzz", seeded}, stdout: []string{`Search: "zzzz"`, "No results found."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errs, code := run(t, append([]string{"search"}, tt.args...)...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d; stderr=%s", code, tt.code, errs)
			}
			wantContains(t, out, tt.stdout...)
			wantContains(t, errs, tt.stderr...)
		})
	}
}

func TestStats(t *testing.T) {
	seeded := t.TempDir()
	seedStore(t, seeded)
	writeJSON(t, filepath.Join(seeded, "semantic_graph.json"), map[string]any{
		"terms": map[string]any{"api": map[string]any{"related": []string{"rest"}}},
	})

	// A directory with malformed sidecar files: graph/scratchpad lines are
	// skipped silently and the store (which loads kv.json) cannot open.
	bad := t.TempDir()
	seedStore(t, bad)
	for _, name := range []string{"semantic_graph.json", "kv.json"} {
		if err := os.WriteFile(filepath.Join(bad, name), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A regular file as storage path: nothing found, store cannot open.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// observations.bleve as a regular file: exists but no size walk.
	flat := t.TempDir()
	if err := os.WriteFile(filepath.Join(flat, "observations.bleve"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		args   []string
		code   int
		stdout []string
		absent []string
		stderr []string
	}{
		{name: "missing path", args: []string{}, code: 1, stderr: []string{"storage path required"}},
		{name: "full", args: []string{seeded}, stdout: []string{
			"Storage path: " + seeded, "Bleve index: " + filepath.Join(seeded, "observations.bleve") + " (exists)",
			"Size: ", "Semantic graph: 1 terms", "Scratchpad: 2 keys",
			"--- Observation Counts ---", "Findings: 1", "Insights: 1", "Lessons: 1",
		}},
		{name: "malformed sidecars", args: []string{bad},
			stdout: []string{"(exists)"},
			absent: []string{"Semantic graph:", "Scratchpad:", "Observation Counts"}},
		{name: "nothing there", args: []string{file}, stdout: []string{
			"Bleve index: not found", "Semantic graph: not found", "Scratchpad: not found",
		}, absent: []string{"Observation Counts"}},
		{name: "bleve is a file", args: []string{flat}, stdout: []string{"(exists)"}, absent: []string{"Size:"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errs, code := run(t, append([]string{"stats"}, tt.args...)...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d; stderr=%s", code, tt.code, errs)
			}
			wantContains(t, out, tt.stdout...)
			wantContains(t, errs, tt.stderr...)
			for _, s := range tt.absent {
				if strings.Contains(out, s) {
					t.Errorf("output should not contain %q:\n%s", s, out)
				}
			}
		})
	}
}

func TestGraph(t *testing.T) {
	dir := t.TempDir()
	terms := map[string]any{"api": map[string]any{"related": []string{"rest", "http"}}}
	for i := range 60 {
		terms[fmt.Sprintf("t%02d", i)] = map[string]any{}
	}
	writeJSON(t, filepath.Join(dir, "semantic_graph.json"), map[string]any{
		"provider": "ollama", "model": "m1", "terms": terms,
	})
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "semantic_graph.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		args   []string
		code   int
		stdout []string
		stderr []string
	}{
		{name: "missing path", args: []string{"--term=x"}, code: 1, stderr: []string{"storage path required"}},
		{name: "read error", args: []string{t.TempDir()}, code: 1, stderr: []string{"Error reading graph"}},
		{name: "parse error", args: []string{bad}, code: 1, stderr: []string{"Error parsing graph"}},
		{name: "term found", args: []string{"--term=api", dir}, stdout: []string{
			"Semantic Graph", "Provider: ollama", "Model: m1", "Terms: 61", `Term: "api"`, "Related: [rest http]",
		}},
		{name: "term missing", args: []string{"--term=nope", dir}, stdout: []string{`Term "nope" not found in graph`}},
		{name: "list truncated", args: []string{dir}, stdout: []string{"... and 11 more"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errs, code := run(t, append([]string{"graph"}, tt.args...)...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d; stderr=%s", code, tt.code, errs)
			}
			wantContains(t, out, tt.stdout...)
			wantContains(t, errs, tt.stderr...)
		})
	}
}

func TestScratchpad(t *testing.T) {
	seeded := t.TempDir()
	seedStore(t, seeded)
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "kv.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		args   []string
		code   int
		stdout []string
		stderr []string
	}{
		{name: "missing path", args: []string{}, code: 1, stderr: []string{"storage path required"}},
		{name: "read error", args: []string{t.TempDir()}, code: 1, stderr: []string{"Error reading scratchpad"}},
		{name: "parse error", args: []string{bad}, code: 1, stderr: []string{"Error parsing scratchpad"}},
		{name: "dump", args: []string{seeded}, stdout: []string{
			"Scratchpad (2 keys)", "k1 = v1", "long = " + strings.Repeat("x", 100) + "...",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errs, code := run(t, append([]string{"scratchpad"}, tt.args...)...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d; stderr=%s", code, tt.code, errs)
			}
			wantContains(t, out, tt.stdout...)
			wantContains(t, errs, tt.stderr...)
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{3 << 30, "3.0 GB"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.in); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTitle(t *testing.T) {
	if got := title(""); got != "" {
		t.Errorf("title(\"\") = %q, want \"\"", got)
	}
	if got := title("finding"); got != "Finding" {
		t.Errorf("title(\"finding\") = %q, want \"Finding\"", got)
	}
}
