package system

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agentkit/policy"
)

// TestPolicy_FilesUseCurrentSchema walks the repository for policy*.toml
// files and asserts each one parses under the agentkit v1.2.0 policy schema
// with no unrecognized keys. Legacy keys (enabled, allowlist, denylist,
// allow_domains, rate_limit, mcp.default_deny, mcp.allowed_tools,
// security.extra_*) are silently ignored by policy.FromTOML, so this is the
// only place a stale example file would be caught.
func TestPolicy_FilesUseCurrentSchema(t *testing.T) {
	root := getSrcDir(t)

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "storage", "test-results":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "policy") && strings.HasSuffix(name, ".toml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("no policy*.toml files found under %s", root)
	}

	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("rel %s: %v", path, err)
		}
		t.Run(rel, func(t *testing.T) {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			_, unknown, err := policy.FromTOMLWithUnknownKeys(string(content), "/workspace", "/home/user")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(unknown) > 0 {
				t.Errorf("unknown keys (legacy schema?): %v", unknown)
			}
		})
	}
}
