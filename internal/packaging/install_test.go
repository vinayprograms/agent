package packaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallErrors(t *testing.T) {
	dir := t.TempDir()
	goodPkg := packFixture(t, dir, "NAME inst\nVERSION 1.0.0\n", nil)
	pub, _, _ := GenerateKeyPair()

	evil := filepath.Join(dir, "evil.agent")
	evilContent := gzipTar(t, []tarEntry{{name: "../evil.txt", body: "x"}}, 0)
	if err := writePackage(evil, []byte(`{"name":"evil","version":"1"}`), &Package{Content: evilContent}); err != nil {
		t.Fatal(err)
	}
	fileAsTarget := filepath.Join(dir, "file")
	os.WriteFile(fileAsTarget, nil, 0o644)

	tests := []struct {
		name    string
		opts    InstallOptions
		wantErr string
	}{
		{"no target", InstallOptions{PackagePath: goodPkg}, "target directory is required"},
		{"missing package", InstallOptions{PackagePath: filepath.Join(dir, "nope.agent"), TargetDir: dir}, "open package"},
		{"unsigned with key", InstallOptions{PackagePath: goodPkg, TargetDir: dir, PublicKey: pub}, "verification failed"},
		{"target is a file", InstallOptions{PackagePath: goodPkg, TargetDir: fileAsTarget}, "package directory"},
		{"traversal in content", InstallOptions{PackagePath: evil, TargetDir: filepath.Join(dir, "evil-target")}, "extracting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Install(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Install() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Error("traversal entry escaped the install directory")
	}
}

func TestInstallNoDeps(t *testing.T) {
	dir := t.TempDir()
	pkg := packFixture(t, dir, "NAME deps\nVERSION 1.0.0\n", map[string]string{
		"manifest.json": `{"dependencies":{"b":"1","a":"2"}}`,
	})
	res, err := Install(InstallOptions{PackagePath: pkg, TargetDir: filepath.Join(dir, "out"), NoDeps: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Dependencies != nil {
		t.Errorf("Dependencies = %v, want none with NoDeps", res.Dependencies)
	}
	res, _ = Install(InstallOptions{PackagePath: pkg, DryRun: true})
	if strings.Join(res.Dependencies, ",") != "a,b" {
		t.Errorf("Dependencies = %v, want sorted a,b", res.Dependencies)
	}
}

// packFixture writes an agent source dir with the given Agentfile and extra
// files, packs it and returns the .agent path.
func packFixture(t *testing.T, dir, agentfile string, extra map[string]string) string {
	t.Helper()
	src := filepath.Join(dir, "src-"+strings.ReplaceAll(t.Name(), "/", "_"))
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "Agentfile"), []byte(agentfile), 0o644)
	for name, body := range extra {
		os.WriteFile(filepath.Join(src, name), []byte(body), 0o644)
	}
	out := filepath.Join(dir, filepath.Base(src)+".agent")
	if _, err := Pack(PackOptions{SourceDir: src, OutputPath: out}); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return out
}
