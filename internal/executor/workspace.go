// Package executor provides workflow execution.
package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// projectSignature maps marker files to their project type description.
var projectSignatures = []struct {
	file string
	desc string
}{
	{"go.mod", "Go module"},
	{"Cargo.toml", "Rust (Cargo)"},
	{"package.json", "Node.js / JavaScript"},
	{"pyproject.toml", "Python (pyproject)"},
	{"setup.py", "Python (setup.py)"},
	{"requirements.txt", "Python"},
	{"pom.xml", "Java (Maven)"},
	{"build.gradle", "Java (Gradle)"},
	{"Gemfile", "Ruby"},
	{"mix.exs", "Elixir"},
	{"CMakeLists.txt", "C/C++ (CMake)"},
	{"Makefile", "Make-based project"},
	{"docker-compose.yml", "Docker Compose"},
	{"docker-compose.yaml", "Docker Compose"},
	{"Dockerfile", "Docker"},
	{".terraform", "Terraform"},
	{"helm", "Helm chart"},
}

// BuildWorkspaceContext scans the workspace directory and produces a concise
// context string describing the project layout, type, and key files.
// This is injected into the system prompt so agents don't waste cycles
// discovering the project structure.
func BuildWorkspaceContext(workspace string) string {
	if workspace == "" {
		return ""
	}

	// Resolve to absolute path
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}

	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return ""
	}

	var sections []string

	// Header
	sections = append(sections, fmt.Sprintf("<workspace path=%q>", abs))

	// Detect project type(s)
	var projectTypes []string
	for _, sig := range projectSignatures {
		path := filepath.Join(abs, sig.file)
		if _, err := os.Stat(path); err == nil {
			projectTypes = append(projectTypes, sig.desc)
		}
	}
	if len(projectTypes) > 0 {
		sections = append(sections, fmt.Sprintf("Project type: %s", strings.Join(projectTypes, ", ")))
	}

	// Extract key metadata from project files
	if meta := extractProjectMeta(abs); meta != "" {
		sections = append(sections, meta)
	}

	// List directory structure (dirs to depth 2, files only at root)
	if tree := buildTreeListing(abs, 2); tree != "" {
		sections = append(sections, "Directory structure:\n"+tree)
	}

	sections = append(sections, "</workspace>")

	return strings.Join(sections, "\n")
}

// extractProjectMeta reads key project files for metadata.
func extractProjectMeta(root string) string {
	var parts []string

	// Go: extract module name from go.mod
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				parts = append(parts, "Go module: "+strings.TrimPrefix(line, "module "))
				break
			}
		}
		// Extract Go version
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "go ") {
				parts = append(parts, "Go version: "+strings.TrimPrefix(line, "go "))
				break
			}
		}
	}

	// Node: extract name from package.json
	if data, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		// Simple extraction without JSON parsing to keep it lightweight
		content := string(data)
		if idx := strings.Index(content, `"name"`); idx >= 0 {
			rest := content[idx:]
			if start := strings.Index(rest, `"`); start >= 0 {
				rest = rest[start+1:]
				if start = strings.Index(rest, `"`); start >= 0 {
					rest = rest[start+1:]
					if end := strings.Index(rest, `"`); end >= 0 {
						parts = append(parts, "Package: "+rest[:end])
					}
				}
			}
		}
	}

	// Rust: extract package name from Cargo.toml
	if data, err := os.ReadFile(filepath.Join(root, "Cargo.toml")); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "name") && strings.Contains(line, "=") {
				val := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
				val = strings.Trim(val, `"'`)
				parts = append(parts, "Crate: "+val)
				break
			}
		}
	}

	// Python: extract project name from pyproject.toml
	if data, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "name") && strings.Contains(line, "=") {
				val := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
				val = strings.Trim(val, `"'`)
				parts = append(parts, "Project: "+val)
				break
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// buildTreeListing produces a compact directory tree listing.
// Only includes directories and key files, skipping hidden dirs and vendored deps.
func buildTreeListing(root string, maxDepth int) string {
	var lines []string
	buildTreeLines(root, root, 0, maxDepth, &lines)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// skipDirs are directories we never descend into.
var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	".git":         true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".tox":         true,
	"target":       true, // Rust build output
	"dist":         true,
	"build":        true,
	".next":        true,
	".cache":       true,
}

// keyFiles are files worth listing even when we're not listing all files.
var keyFiles = map[string]bool{
	"go.mod":              true,
	"go.sum":              true,
	"Cargo.toml":          true,
	"package.json":        true,
	"pyproject.toml":      true,
	"setup.py":            true,
	"requirements.txt":    true,
	"Makefile":            true,
	"Dockerfile":          true,
	"docker-compose.yml":  true,
	"docker-compose.yaml": true,
	".env.example":        true,
	"README.md":           true,
	"LICENSE":             true,
}

func buildTreeLines(root, dir string, depth, maxDepth int, lines *[]string) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Separate dirs and files, sort alphabetically
	var dirs []os.DirEntry
	var files []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && name != ".env.example" {
			continue // skip hidden files/dirs (except .env.example)
		}
		if e.IsDir() {
			if !skipDirs[name] {
				dirs = append(dirs, e)
			}
		} else {
			files = append(files, e)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	indent := strings.Repeat("  ", depth)

	// List directories (cap at 15 per level to keep output concise)
	maxShow := 15
	for i, d := range dirs {
		if i >= maxShow {
			*lines = append(*lines, fmt.Sprintf("%s... and %d more directories", indent, len(dirs)-maxShow))
			break
		}
		*lines = append(*lines, fmt.Sprintf("%s%s/", indent, d.Name()))
		buildTreeLines(root, filepath.Join(dir, d.Name()), depth+1, maxDepth, lines)
	}

	// Only list key files at root level; skip files at deeper levels
	if depth == 0 {
		for _, f := range files {
			if keyFiles[f.Name()] {
				*lines = append(*lines, fmt.Sprintf("%s%s", indent, f.Name()))
			}
		}
	}
}
