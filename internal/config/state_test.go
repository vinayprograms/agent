package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	cases := map[string]string{
		"~/x":      "/home/u/x",
		"~":        "/home/u",
		"~other":   "~other", // a bare ~ or ~/ only; ~user is not a home we know
		"rel":      "rel",
		"/abs":     "/abs",
		"":         "",
		"a/~/b":    "a/~/b",
		"~/a/../b": "/home/u/b",
	}
	for in, want := range cases {
		if got := ExpandHome(in, "/home/u"); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ExpandHome("~/x", ""); got != "~/x" {
		t.Errorf("no home: %q", got)
	}
}

func TestConfigStateDir(t *testing.T) {
	cfg := New()
	cfg.State.Location = ""
	if got, want := cfg.StateDir("/home/u"), DefaultStateDir("/home/u"); got != want {
		t.Errorf("unset location = %q, want %q", got, want)
	}
	cfg.State.Location = "~/state"
	if got, want := cfg.StateDir("/home/u"), "/home/u/state"; got != want {
		t.Errorf("tilde location = %q, want %q", got, want)
	}
	cfg.State.Location = "/data"
	if got := cfg.StateDir("/home/u"); got != "/data" {
		t.Errorf("absolute location = %q", got)
	}
}

func TestStateDir(t *testing.T) {
	home := t.TempDir()

	// The override wins outright, tilde and all.
	if got, want := mustStateDir(t, "", "~/elsewhere", home), filepath.Join(home, "elsewhere"); got != want {
		t.Errorf("override = %q, want %q", got, want)
	}

	// Otherwise [state] location from the config file named by configPath.
	cfgPath := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(cfgPath, []byte("[state]\nlocation = \"/data/agent\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustStateDir(t, cfgPath, "", home); got != "/data/agent" {
		t.Errorf("config location = %q", got)
	}

	// With no config anywhere, the default under home.
	if got, want := mustStateDir(t, "", "", home), DefaultStateDir(home); got != want {
		t.Errorf("default = %q, want %q", got, want)
	}

	// An explicit config file that does not exist is an error.
	if _, err := StateDir(filepath.Join(t.TempDir(), "missing.toml"), "", home); err == nil {
		t.Error("expected an error for a missing --config file")
	}
}

func mustStateDir(t *testing.T, configPath, override, home string) string {
	t.Helper()
	dir, err := StateDir(configPath, override, home)
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	return dir
}
