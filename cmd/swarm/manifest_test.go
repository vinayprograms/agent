package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadManifestDefaultsAndPaths(t *testing.T) {
	t.Setenv("SWARM_TEST_CAP", "summarize")
	path := writeManifest(t, `
state:
  location: data
agents:
  - name: w1
    agentfile: agents/w1.agent
    config: cfg.toml
    policy: /abs/policy.toml
    capability: ${SWARM_TEST_CAP}
    state: w1-state
  - name: mgr
    type: manager
`)
	m, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if m.NATS.URL != "nats://localhost:4222" {
		t.Errorf("default NATS URL: %q", m.NATS.URL)
	}
	if m.State.Location != filepath.Join(dir, "data") {
		t.Errorf("state location not resolved: %q", m.State.Location)
	}
	w := m.Agents[0]
	if w.Agentfile != filepath.Join(dir, "agents/w1.agent") || w.Config != filepath.Join(dir, "cfg.toml") {
		t.Errorf("relative paths not resolved: %+v", w)
	}
	if w.Policy != "/abs/policy.toml" {
		t.Errorf("absolute path changed: %q", w.Policy)
	}
	if w.Capability != "summarize" {
		t.Errorf("env not expanded: %q", w.Capability)
	}
	if w.State != filepath.Join(dir, "w1-state") {
		t.Errorf("agent state not resolved: %q", w.State)
	}
	if w.Type != "worker" || w.Replicas != 1 {
		t.Errorf("defaults: type=%q replicas=%d", w.Type, w.Replicas)
	}
	if m.Agents[1].Type != "manager" || m.Agents[1].Replicas != 1 {
		t.Errorf("manager defaults: %+v", m.Agents[1])
	}
}

func TestLoadManifestDefaultStateLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m, err := loadManifest(writeManifest(t, "agents: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "share", "swarm"); m.State.Location != want {
		t.Errorf("default state: %q want %q", m.State.Location, want)
	}
}

func TestLoadManifestLegacyStorage(t *testing.T) {
	m, err := loadManifest(writeManifest(t, "storage:\n  root: /old/root\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.State.Location != "/old/root" || m.Storage != nil {
		t.Errorf("legacy storage not migrated: %+v", m)
	}
}

func TestLoadManifestErrors(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"both state and storage": {"state:\n  location: /a\nstorage:\n  root: /b\n", "both"},
		"invalid type":           {"agents:\n  - name: x\n    type: boss\n", "invalid type"},
		"manager replicas":       {"agents:\n  - name: x\n    type: manager\n    replicas: 2\n", "replicas"},
		"two managers":           {"agents:\n  - name: a\n    type: manager\n  - name: b\n    type: manager\n", "at most one"},
		"bad yaml":               {"agents: [\n", "parse manifest"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadManifest(writeManifest(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
	if _, err := loadManifest(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "read manifest") {
		t.Errorf("missing file: %v", err)
	}
}

func TestPathHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := resolveRelPath("/base", ""); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := resolveRelPath("/base", "/abs"); got != "/abs" {
		t.Errorf("abs: %q", got)
	}
	if got := resolveRelPath("/base", "rel"); got != "/base/rel" {
		t.Errorf("rel: %q", got)
	}
	if got := expandTilde("~"); got != home {
		t.Errorf("tilde: %q", got)
	}
	if got := expandTilde("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("tilde path: %q", got)
	}
	if got := expandTilde("plain"); got != "plain" {
		t.Errorf("plain: %q", got)
	}
	t.Setenv("SWARM_TEST_V", "v")
	if got := expandEnv("~/$SWARM_TEST_V"); got != filepath.Join(home, "v") {
		t.Errorf("expandEnv: %q", got)
	}
	if got := expandPath("~/y"); got != filepath.Join(getUserHome(), "y") {
		t.Errorf("expandPath: %q", got)
	}
	if got := expandPath("/z"); got != "/z" {
		t.Errorf("expandPath abs: %q", got)
	}
	if getUserHome() == "" {
		t.Error("getUserHome empty")
	}
}

func TestFindManifest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := findManifest(); err == nil {
		t.Error("expected error with no manifest")
	}
	os.WriteFile(filepath.Join(dir, "swarm.yml"), []byte("agents: []"), 0o644)
	if got, err := findManifest(); err != nil || got != "swarm.yml" {
		t.Errorf("got %q, %v", got, err)
	}
	os.WriteFile(filepath.Join(dir, "swarm.yaml"), []byte("agents: []"), 0o644)
	if got, _ := findManifest(); got != "swarm.yaml" {
		t.Errorf("yaml should win: %q", got)
	}
}

func TestCheckNATS(t *testing.T) {
	for _, u := range []string{"nats://localhost:4222", "nats://127.0.0.1:4222", "nats://remote:4222"} {
		if err := checkNATS(u); err != nil {
			t.Errorf("checkNATS(%q) = %v", u, err)
		}
	}
}
