package run

import (
	"fmt"
	"os"
	"path/filepath"
)

// CredentialPaths returns the ordered credential file search paths for
// credentials.Load: an explicit --credentials override first (it must
// exist, checked here so a typo fails loudly instead of silently falling
// through), then the standard locations (./credentials.toml,
// ~/.config/agent/credentials.toml, ~/.agent/credentials.toml). home is
// used to build the standard locations; an empty home omits them.
func CredentialPaths(home, override string) ([]string, error) {
	var paths []string
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return nil, fmt.Errorf("credentials file %s: %w", override, err)
		}
		paths = append(paths, override)
	}
	paths = append(paths, "credentials.toml")
	if home != "" {
		paths = append(paths,
			filepath.Join(home, ".config", "agent", "credentials.toml"),
			filepath.Join(home, ".agent", "credentials.toml"),
		)
	}
	return paths, nil
}
