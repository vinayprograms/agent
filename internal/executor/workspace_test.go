package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildWorkspaceContext_NoWorkspace(t *testing.T) {
	if got := BuildWorkspaceContext(""); got != "" {
		t.Errorf("empty path = %q, want \"\"", got)
	}
	if got := BuildWorkspaceContext(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Errorf("missing dir = %q, want \"\"", got)
	}
	file := filepath.Join(t.TempDir(), "f.txt")
	write(t, file, "x")
	if got := BuildWorkspaceContext(file); got != "" {
		t.Errorf("plain file = %q, want \"\"", got)
	}
}

// A bare directory still yields a workspace element — just no project type,
// metadata, or tree.
func TestBuildWorkspaceContext_EmptyDir(t *testing.T) {
	ws := t.TempDir()
	got := BuildWorkspaceContext(ws)
	if !strings.HasPrefix(got, "<workspace path=") || !strings.HasSuffix(got, "</workspace>") {
		t.Fatalf("unexpected shape:\n%s", got)
	}
	for _, unwanted := range []string{"Project type:", "Directory structure:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("empty workspace should not report %q:\n%s", unwanted, got)
		}
	}
}

func TestBuildWorkspaceContext_GoProject(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, "go.mod"), "module example.com/thing\n\ngo 1.25\n")
	write(t, filepath.Join(ws, "Makefile"), "all:\n")
	write(t, filepath.Join(ws, "notes.txt"), "ignored")
	write(t, filepath.Join(ws, "internal", "deep", "x.go"), "package deep")
	write(t, filepath.Join(ws, "vendor", "v.go"), "package v")
	write(t, filepath.Join(ws, ".hidden", "h.go"), "package h")

	got := BuildWorkspaceContext(ws)
	for _, want := range []string{
		"Project type: Go module, Make-based project",
		"Go module: example.com/thing",
		"Go version: 1.25",
		"Directory structure:",
		"internal/",
		"  deep/",
		"go.mod",
		"Makefile",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"vendor/", ".hidden/", "notes.txt"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in:\n%s", unwanted, got)
		}
	}
}

func TestExtractProjectMeta(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "node",
			files: map[string]string{"package.json": `{"name": "my-app", "version": "1.0.0"}`},
			want:  []string{"Package: my-app"},
		},
		{
			name:  "rust",
			files: map[string]string{"Cargo.toml": "[package]\nname = \"my-crate\"\n"},
			want:  []string{"Crate: my-crate"},
		},
		{
			name:  "python",
			files: map[string]string{"pyproject.toml": "[project]\nname = 'my-proj'\n"},
			want:  []string{"Project: my-proj"},
		},
		{
			name:  "go without module or version directive",
			files: map[string]string{"go.mod": "// only a comment\n"},
			want:  nil,
		},
		{
			name:  "package.json without a name",
			files: map[string]string{"package.json": `{"version": "1.0.0"}`},
			want:  nil,
		},
		{
			name:  "manifests without a name key",
			files: map[string]string{"Cargo.toml": "[package]\n", "pyproject.toml": "[project]\n"},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := t.TempDir()
			for name, content := range tt.files {
				write(t, filepath.Join(ws, name), content)
			}
			got := extractProjectMeta(ws)
			if len(tt.want) == 0 {
				if got != "" {
					t.Errorf("extractProjectMeta = %q, want \"\"", got)
				}
				return
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("extractProjectMeta = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

// Truncated JSON exercises each early exit of the quote scanner.
func TestExtractProjectMeta_TruncatedPackageJSON(t *testing.T) {
	for _, content := range []string{`{"name"`, `{"name": `, `{"name": "my-app`} {
		ws := t.TempDir()
		write(t, filepath.Join(ws, "package.json"), content)
		if got := extractProjectMeta(ws); got != "" {
			t.Errorf("extractProjectMeta(%q) = %q, want \"\"", content, got)
		}
	}
}

func TestBuildTreeListing_Limits(t *testing.T) {
	ws := t.TempDir()
	for i := range 20 {
		write(t, filepath.Join(ws, fmt.Sprintf("d%02d", i), "f.txt"), "x")
	}
	got := buildTreeListing(ws, 2)
	if !strings.Contains(got, "... and 5 more directories") {
		t.Errorf("expected the overflow line in:\n%s", got)
	}
	if strings.Contains(got, "d15/") {
		t.Errorf("directories past the cap should be omitted:\n%s", got)
	}

	// Depth cut: dirs below maxDepth are not descended into.
	deep := t.TempDir()
	write(t, filepath.Join(deep, "a", "b", "c", "f.txt"), "x")
	shallow := buildTreeListing(deep, 1)
	if !strings.Contains(shallow, "b/") || strings.Contains(shallow, "c/") {
		t.Errorf("depth 1 listing = %q", shallow)
	}

	// Nothing to list, and an unreadable directory, both yield "".
	if got := buildTreeListing(t.TempDir(), 2); got != "" {
		t.Errorf("empty dir listing = %q", got)
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if got := buildTreeListing(locked, 2); got != "" {
		t.Errorf("unreadable dir listing = %q", got)
	}
}
