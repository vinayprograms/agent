package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// createContentArchive creates a deterministic tar.gz of the agent files.
func createContentArchive(sourceDir string) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Collect all files first for sorting
	var files []string
	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		name := info.Name()
		// Skip signature file and hidden files
		if name == SignatureFile {
			return nil
		}
		if strings.HasPrefix(name, ".") && name != "." {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relPath != "." {
			files = append(files, relPath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort for deterministic order
	slices.Sort(files)

	// Add files in sorted order with fixed timestamps
	for _, relPath := range files {
		fullPath := filepath.Join(sourceDir, relPath)
		info, err := os.Stat(fullPath)
		if err != nil {
			return nil, err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil, err
		}
		header.Name = relPath
		// Fixed timestamp for reproducibility
		header.ModTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""

		if err := tw.WriteHeader(header); err != nil {
			return nil, err
		}

		if !info.IsDir() {
			data, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, err
			}
			if _, err := tw.Write(data); err != nil {
				return nil, err
			}
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// extractContent extracts tar.gz content into targetDir. Entries are
// confined to targetDir via os.Root, so "..", absolute and symlink-escaping
// names fail instead of writing elsewhere. Only directories and regular
// files are extracted (symlinks and other types are skipped) and file modes
// are not preserved.
func extractContent(content []byte, targetDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return err
	}
	defer gr.Close()

	root, err := os.OpenRoot(targetDir)
	if err != nil {
		return err
	}
	defer root.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := extractEntry(root, header, tr); err != nil {
			return fmt.Errorf("invalid path in archive %q: %w", header.Name, err)
		}
	}
}

// extractEntry writes one directory or regular file entry under root.
func extractEntry(root *os.Root, header *tar.Header, body io.Reader) error {
	name := strings.TrimLeft(header.Name, "/")
	switch header.Typeflag {
	case tar.TypeDir:
		return root.MkdirAll(name, 0o755)
	case tar.TypeReg:
		if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		f, err := root.Create(name)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, body); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	return nil
}

// File reads one file from the package content by its archive name.
func (p *Package) File(name string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(p.Content))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == name || "./"+hdr.Name == name || hdr.Name == "./"+name {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("file not found in package: %s", name)
}

// Agentfile returns the Agentfile from the package.
func (p *Package) Agentfile() ([]byte, error) { return p.File("Agentfile") }

// Config returns the agent.toml from the package.
func (p *Package) Config() ([]byte, error) { return p.File("agent.toml") }

// Policy returns the policy.toml from the package.
func (p *Package) Policy() ([]byte, error) { return p.File("policy.toml") }
