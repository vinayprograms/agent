package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// StateDir resolves the directory a command reads and writes persistent
// state in: override (a --state flag) when set, otherwise [state]
// location from the standard config precedence, with configPath as the
// --config override. A leading ~ is expanded against home; an empty home
// asks the OS.
func StateDir(configPath, override, home string) (string, error) {
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
	}
	if override != "" {
		return ExpandHome(override, home), nil
	}
	cfg, err := LoadWithPrecedence(LoadOptions{CLIPath: configPath, Home: home})
	if err != nil {
		return "", err
	}
	return cfg.StateDir(home), nil
}

// StateDir is [state] location with a leading ~ expanded against home,
// falling back to [DefaultStateDir] when the setting is empty.
func (c *Config) StateDir(home string) string {
	loc := c.State.Location
	if loc == "" {
		loc = DefaultStateDir(home)
	}
	return ExpandHome(loc, home)
}

// ExpandHome replaces a leading ~ in p with home.
func ExpandHome(p, home string) string {
	if home == "" || p == "" || p[0] != '~' {
		return p
	}
	return filepath.Join(home, p[1:])
}
