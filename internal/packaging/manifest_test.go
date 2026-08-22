package packaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateManifest_MergesAgentfile(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "Agentfile"), []byte(`# comment

NAME merged
VERSION 2.0.0
INPUT a
INPUT b DEFAULT "x"
AGENT c FROM c.md REQUIRES "fast"
AGENT d FROM d.md REQUIRES "fast"
GOAL
`), 0o644)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`{"inputs":{"a":{"type":"int"}},"requires":{"profiles":["slow"]}}`), 0o644)

	m, err := loadOrCreateManifest(src, PackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "merged" || m.Version != "2.0.0" || m.Format != FormatVersion {
		t.Errorf("name/version/format = %q/%q/%d", m.Name, m.Version, m.Format)
	}
	if m.Inputs["a"].Type != "int" || m.Inputs["b"].Default != "x" || m.Inputs["b"].Required {
		t.Errorf("inputs = %+v, want manifest 'a' kept and Agentfile 'b' added", m.Inputs)
	}
	if strings.Join(m.Requires.Profiles, ",") != "slow,fast" {
		t.Errorf("profiles = %v, want slow,fast", m.Requires.Profiles)
	}
}

func TestLoadOrCreateManifest_ManifestWithoutInputsOrRequires(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "Agentfile"), []byte("NAME n\nINPUT a\nAGENT c FROM c.md REQUIRES \"fast\"\n"), 0o644)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`{"name":"keep","version":"9"}`), 0o644)
	m, err := loadOrCreateManifest(src, PackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "keep" || m.Version != "9" || !m.Inputs["a"].Required || m.Requires.Profiles[0] != "fast" {
		t.Errorf("manifest = %+v", m)
	}
}

func TestLoadOrCreateManifest_Errors(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, ManifestFile), []byte("{bad"), 0o644)
	if _, err := loadOrCreateManifest(src, PackOptions{}); err == nil {
		t.Error("bad manifest.json: want error")
	}
	src = t.TempDir()
	os.WriteFile(filepath.Join(src, "Agentfile"), []byte("VERSION 1\n"), 0o644)
	if _, err := loadOrCreateManifest(src, PackOptions{}); err == nil {
		t.Error("Agentfile without NAME: want error")
	}
	if err := extractManifestFromAgentfile(t.TempDir(), &Manifest{}); err == nil {
		t.Error("missing Agentfile: want error")
	}
}

func TestExtractManifest_DefaultVersion(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "Agentfile"), []byte("NAME only\n"), 0o644)
	m := &Manifest{Inputs: map[string]Input{}}
	if err := extractManifestFromAgentfile(src, m); err != nil || m.Version != "0.0.0" {
		t.Errorf("version = %q, err %v; want 0.0.0", m.Version, err)
	}
}

func TestValidateAgentReferences_Unreadable(t *testing.T) {
	if err := validateAgentReferences(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing Agentfile: want error")
	}
}
