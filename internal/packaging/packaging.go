// Package packaging handles agent package creation, verification, and installation.
package packaging

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Package represents a loaded agent package.
type Package struct {
	Manifest  *Manifest
	Content   []byte // tar.gz of agent files
	Signature []byte // Raw Ed25519 signature (64 bytes)
	Path      string
}

const (
	ManifestFile  = "manifest.json"
	ContentFile   = "content.tar.gz"
	SignatureFile = "signature"
	FormatVersion = 1
)

// PackOptions configures package creation.
type PackOptions struct {
	SourceDir   string
	OutputPath  string
	PrivateKey  ed25519.PrivateKey
	Author      *Author
	Description string
	License     string
}

// Pack creates an agent package from a directory.
func Pack(opts PackOptions) (*Package, error) {
	// Load and parse the Agentfile to extract metadata
	agentfilePath := filepath.Join(opts.SourceDir, "Agentfile")
	if _, err := os.Stat(agentfilePath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("Agentfile not found in %s", opts.SourceDir)
	}

	// Validate Agentfile doesn't reference external .agent packages
	if err := validateAgentReferences(agentfilePath); err != nil {
		return nil, err
	}

	// Read existing manifest or create from Agentfile
	manifest, err := loadOrCreateManifest(opts.SourceDir, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifest: %w", err)
	}

	manifest.CreatedAt = time.Now().UTC().Format(time.RFC3339)

	// Set author key fingerprint if signing
	if opts.PrivateKey != nil && manifest.Author != nil {
		pubKey := opts.PrivateKey.Public().(ed25519.PublicKey)
		fingerprint := sha256.Sum256(pubKey)
		manifest.Author.KeyFingerprint = hex.EncodeToString(fingerprint[:8])
	}

	// Serialize manifest
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize manifest: %w", err)
	}

	// Create content tar.gz (deterministic)
	content, err := createContentArchive(opts.SourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create content archive: %w", err)
	}

	pkg := &Package{
		Manifest: manifest,
		Content:  content,
	}

	// Sign if private key provided
	if opts.PrivateKey != nil {
		pkg.Signature = ed25519.Sign(opts.PrivateKey, signingInput(manifestJSON, content))
	}

	// Write package file
	if opts.OutputPath != "" {
		if err := writePackage(opts.OutputPath, manifestJSON, pkg); err != nil {
			return nil, fmt.Errorf("failed to write package: %w", err)
		}
		pkg.Path = opts.OutputPath
	}

	return pkg, nil
}

// writePackage writes the package to a zip file (uncompressed/stored).
func writePackage(path string, manifestJSON []byte, pkg *Package) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	// Write manifest (stored, not compressed)
	if err := writeZipFileStored(zw, ManifestFile, manifestJSON); err != nil {
		return err
	}

	// Write content (already gzipped, store as-is)
	if err := writeZipFileStored(zw, ContentFile, pkg.Content); err != nil {
		return err
	}

	// Write signature (raw bytes)
	if pkg.Signature != nil {
		if err := writeZipFileStored(zw, SignatureFile, pkg.Signature); err != nil {
			return err
		}
	}

	return zw.Close()
}

func writeZipFileStored(zw *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{
		Name:   name,
		Method: zip.Store, // No compression
	}
	header.Modified = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// Load loads a package from a .agent file.
func Load(path string) (*Package, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open package: %w", err)
	}
	defer zr.Close()

	pkg := &Package{Path: path}

	for _, f := range zr.File {
		data, err := readZipFile(f)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", f.Name, err)
		}

		switch f.Name {
		case ManifestFile:
			pkg.Manifest = &Manifest{}
			if err := json.Unmarshal(data, pkg.Manifest); err != nil {
				return nil, fmt.Errorf("invalid manifest: %w", err)
			}
		case ContentFile:
			pkg.Content = data
		case SignatureFile:
			pkg.Signature = data
		}
	}

	if pkg.Manifest == nil {
		return nil, errors.New("package missing manifest.json")
	}
	if pkg.Content == nil {
		return nil, errors.New("package missing content.tar.gz")
	}

	return pkg, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
