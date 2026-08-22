package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name string
	flag byte
	body string
}

// gzipTar builds a tar.gz from entries; truncate > 0 drops that many bytes
// off the end of the tar before compressing (a cut-off body).
func gzipTar(t *testing.T, entries []tarEntry, truncate int) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, e := range entries {
		flag := e.flag
		if flag == 0 {
			flag = tar.TypeReg
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: flag, Size: int64(len(e.body)), Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if truncate == 0 {
		tw.Close()
	}
	data := raw.Bytes()
	data = data[:len(data)-truncate]
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	gw.Write(data)
	gw.Close()
	return out.Bytes()
}

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	gw.Write(b)
	gw.Close()
	return out.Bytes()
}

func TestCreateContentArchive_SkipsHiddenAndSignature(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "Agentfile"), []byte("NAME x"), 0o644)
	os.WriteFile(filepath.Join(src, SignatureFile), []byte("sig"), 0o644)
	os.WriteFile(filepath.Join(src, ".hidden"), []byte("h"), 0o644)
	os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o755)
	os.MkdirAll(filepath.Join(src, "agents"), 0o755)
	os.WriteFile(filepath.Join(src, "agents", "a.md"), []byte("a"), 0o644)

	content, err := createContentArchive(src)
	if err != nil {
		t.Fatalf("createContentArchive: %v", err)
	}
	pkg := &Package{Content: content}
	for _, name := range []string{"Agentfile", "agents", "agents/a.md"} {
		if _, err := pkg.File(name); err != nil {
			t.Errorf("File(%q): %v", name, err)
		}
	}
	for _, name := range []string{SignatureFile, ".hidden", ".git", ".git/objects"} {
		if _, err := pkg.File(name); err == nil {
			t.Errorf("File(%q) found, want skipped", name)
		}
	}
}

func TestCreateContentArchive_Errors(t *testing.T) {
	if _, err := createContentArchive(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing source dir: want error")
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "secret"), []byte("x"), 0)
	if _, err := createContentArchive(src); err == nil {
		t.Error("unreadable file: want error")
	}
}

func TestExtractContent(t *testing.T) {
	good := gzipTar(t, []tarEntry{
		{name: "dir", flag: tar.TypeDir},
		{name: "dir/file.txt", body: "hello"},
		{name: "/abs.txt", body: "abs"},
		{name: "link", flag: tar.TypeSymlink},
	}, 0)
	target := t.TempDir()
	if err := extractContent(good, target); err != nil {
		t.Fatalf("extractContent: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "dir", "file.txt")); string(b) != "hello" {
		t.Errorf("dir/file.txt = %q, want hello", b)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "abs.txt")); string(b) != "abs" {
		t.Errorf("abs.txt = %q, want abs", b)
	}
	if _, err := os.Lstat(filepath.Join(target, "link")); err == nil {
		t.Error("symlink extracted, want skipped")
	}
}

func TestExtractContent_Traversal(t *testing.T) {
	tests := []struct {
		name    string
		entries []tarEntry
		setup   func(target string)
	}{
		{"dotdot file", []tarEntry{{name: "../evil.txt", body: "x"}}, nil},
		{"dotdot dir", []tarEntry{{name: "../evil", flag: tar.TypeDir}}, nil},
		{"nested dotdot", []tarEntry{{name: "a/../../evil.txt", body: "x"}}, nil},
		{"symlink escape", []tarEntry{{name: "out/evil.txt", body: "x"}}, func(target string) {
			os.Symlink(t.TempDir(), filepath.Join(target, "out"))
		}},
		{"file over directory", []tarEntry{{name: "Agentfile", body: "x"}}, func(target string) {
			os.Mkdir(filepath.Join(target, "Agentfile"), 0o755)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "target")
			os.Mkdir(target, 0o755)
			if tt.setup != nil {
				tt.setup(target)
			}
			if err := extractContent(gzipTar(t, tt.entries, 0), target); err == nil {
				t.Fatal("extractContent = nil error, want traversal rejection")
			}
			if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
				t.Error("file escaped the target directory")
			}
		})
	}
}

func TestExtractContent_Malformed(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		target  string
	}{
		{"not gzip", []byte("nope"), t.TempDir()},
		{"not tar", gzipBytes(t, []byte("definitely not a tar stream")), t.TempDir()},
		{"truncated body", gzipTar(t, []tarEntry{{name: "f", body: strings.Repeat("x", 2000)}}, 1500), t.TempDir()},
		{"missing target", gzipTar(t, nil, 0), filepath.Join(t.TempDir(), "missing")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := extractContent(tt.content, tt.target); err == nil {
				t.Error("extractContent = nil error, want error")
			}
		})
	}
}

func TestPackageFile(t *testing.T) {
	pkg := &Package{Content: gzipTar(t, []tarEntry{
		{name: "./Agentfile", body: "NAME x"},
		{name: "agent.toml", body: "[model]"},
		{name: "policy.toml", body: "[tools]"},
	}, 0)}

	for name, want := range map[string]string{"Agentfile": "NAME x", "./agent.toml": "[agent.toml]"} {
		got, err := pkg.File(name)
		if name == "./agent.toml" {
			want = "[model]"
		}
		if err != nil || string(got) != want {
			t.Errorf("File(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	if a, err := pkg.Agentfile(); err != nil || string(a) != "NAME x" {
		t.Errorf("Agentfile() = %q, %v", a, err)
	}
	if c, err := pkg.Config(); err != nil || string(c) != "[model]" {
		t.Errorf("Config() = %q, %v", c, err)
	}
	if p, err := pkg.Policy(); err != nil || string(p) != "[tools]" {
		t.Errorf("Policy() = %q, %v", p, err)
	}
	if _, err := pkg.File("missing"); err == nil {
		t.Error("File(missing) = nil error, want error")
	}
	if _, err := (&Package{Content: []byte("bad")}).File("x"); err == nil {
		t.Error("File on non-gzip content = nil error, want error")
	}
	if _, err := (&Package{Content: gzipBytes(t, []byte("not a tar"))}).File("x"); err == nil {
		t.Error("File on non-tar content = nil error, want error")
	}
}

func TestCreateContentArchive_Symlinks(t *testing.T) {
	t.Run("dangling", func(t *testing.T) {
		src := t.TempDir()
		os.Symlink(filepath.Join(src, "missing"), filepath.Join(src, "link"))
		if _, err := createContentArchive(src); err == nil {
			t.Error("dangling symlink: want error")
		}
	})
	t.Run("to file is dereferenced", func(t *testing.T) {
		src := t.TempDir()
		os.WriteFile(filepath.Join(src, "real"), []byte("data"), 0o644)
		os.Symlink(filepath.Join(src, "real"), filepath.Join(src, "link"))
		content, err := createContentArchive(src)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := (&Package{Content: content}).File("link"); err != nil || string(got) != "data" {
			t.Errorf("File(link) = %q, %v; want the target's content", got, err)
		}
	})
}
