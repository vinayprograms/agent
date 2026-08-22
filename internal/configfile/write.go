package configfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// fileMode is the permission for the generated files. They hold no secrets —
// credentials live in credentials.toml, which its own store writes at 0600.
const fileMode = 0644

// Write renders o and writes agent.toml and policy.toml into dir, creating
// dir if needed, and returns the paths written in that order. Unless force is
// set, an existing file is never overwritten: Write reports every file that
// is in the way and writes nothing.
func Write(dir string, o Options, force bool) ([]string, error) {
	files := []struct {
		path string
		body string
	}{
		{filepath.Join(dir, "agent.toml"), AgentTOML(o)},
		{filepath.Join(dir, "policy.toml"), PolicyTOML(o)},
	}

	if !force {
		var blocked []error
		for _, f := range files {
			if _, err := os.Stat(f.path); err == nil {
				blocked = append(blocked, fmt.Errorf("%s already exists", f.path))
			}
		}
		if len(blocked) > 0 {
			return nil, fmt.Errorf("%w (use --force to overwrite)", errors.Join(blocked...))
		}
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	var written []string
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.body), fileMode); err != nil {
			return written, fmt.Errorf("write %s: %w", f.path, err)
		}
		written = append(written, f.path)
	}
	return written, nil
}
