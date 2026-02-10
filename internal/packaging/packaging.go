// Package packaging handles agent package creation, verification, and installation.
package packaging

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Manifest represents the public API of an agent package.
type Manifest struct {
	Format       int               `json:"format"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Description  string            `json:"description,omitempty"`
	Author       *Author           `json:"author,omitempty"`
	License      string            `json:"license,omitempty"`
	Inputs       map[string]Input  `json:"inputs,omitempty"`
	Outputs      map[string]Output `json:"outputs,omitempty"`
	Requires     *Requirements     `json:"requires,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
	CreatedAt    string            `json:"created_at"`
}

// Author represents package author information.
type Author struct {
	Name           string `json:"name,omitempty"`
	Email          string `json:"email,omitempty"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
}

// Input represents a package input parameter.
type Input struct {
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Output represents a package output.
type Output struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// Requirements represents runtime requirements.
type Requirements struct {
	Profiles []string `json:"profiles,omitempty"`
	Tools    []string `json:"tools,omitempty"`
}

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
	if _, err := os.Stat(agentfilePath); os.IsNotExist(err) {
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
		manifestHash := sha256.Sum256(manifestJSON)
		contentHash := sha256.Sum256(content)

		// Sign concatenation of both hashes
		toSign := append(manifestHash[:], contentHash[:]...)
		pkg.Signature = ed25519.Sign(opts.PrivateKey, toSign)
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

// loadOrCreateManifest loads manifest.json or creates one from Agentfile.
func loadOrCreateManifest(sourceDir string, opts PackOptions) (*Manifest, error) {
	manifestPath := filepath.Join(sourceDir, ManifestFile)

	var manifest *Manifest

	if data, err := os.ReadFile(manifestPath); err == nil {
		// Load existing manifest
		manifest = &Manifest{}
		if err := json.Unmarshal(data, manifest); err != nil {
			return nil, fmt.Errorf("invalid manifest.json: %w", err)
		}
		// Still extract data from Agentfile to merge (name, version, inputs, requires)
		extracted := &Manifest{Inputs: make(map[string]Input)}
		if err := extractManifestFromAgentfile(sourceDir, extracted); err == nil {
			// Use Agentfile NAME/VERSION if manifest doesn't have them
			if manifest.Name == "" {
				manifest.Name = extracted.Name
			}
			if manifest.Version == "" {
				manifest.Version = extracted.Version
			}
			// Merge inputs (Agentfile inputs fill in missing)
			if manifest.Inputs == nil {
				manifest.Inputs = extracted.Inputs
			} else {
				for name, input := range extracted.Inputs {
					if _, exists := manifest.Inputs[name]; !exists {
						manifest.Inputs[name] = input
					}
				}
			}
			// Merge requires (combine profiles)
			if extracted.Requires != nil && len(extracted.Requires.Profiles) > 0 {
				if manifest.Requires == nil {
					manifest.Requires = &Requirements{}
				}
				for _, profile := range extracted.Requires.Profiles {
					found := false
					for _, p := range manifest.Requires.Profiles {
						if p == profile {
							found = true
							break
						}
					}
					if !found {
						manifest.Requires.Profiles = append(manifest.Requires.Profiles, profile)
					}
				}
			}
		}
	} else {
		// Create from Agentfile
		manifest = &Manifest{
			Inputs:  make(map[string]Input),
			Outputs: make(map[string]Output),
		}

		if err := extractManifestFromAgentfile(sourceDir, manifest); err != nil {
			return nil, err
		}
	}

	manifest.Format = FormatVersion

	// Apply overrides from options
	if opts.Author != nil {
		manifest.Author = opts.Author
	}
	if opts.Description != "" {
		manifest.Description = opts.Description
	}
	if opts.License != "" {
		manifest.License = opts.License
	}

	return manifest, nil
}

// extractManifestFromAgentfile parses Agentfile to populate manifest.
func extractManifestFromAgentfile(sourceDir string, manifest *Manifest) error {
	agentfilePath := filepath.Join(sourceDir, "Agentfile")
	content, err := os.ReadFile(agentfilePath)
	if err != nil {
		return err
	}

	// Simple line-by-line parsing for NAME, INPUT, VERSION
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch fields[0] {
		case "NAME":
			manifest.Name = fields[1]
		case "VERSION":
			manifest.Version = fields[1]
		case "INPUT":
			name := fields[1]
			input := Input{Required: true, Type: "string"}

			for i := 2; i < len(fields); i++ {
				if fields[i] == "DEFAULT" && i+1 < len(fields) {
					input.Required = false
					input.Default = strings.Trim(fields[i+1], "\"")
					i++
				}
			}
			manifest.Inputs[name] = input
		case "AGENT":
			// Extract required profiles
			for i, f := range fields {
				if f == "REQUIRES" && i+1 < len(fields) {
					profile := strings.Trim(fields[i+1], "\"")
					if manifest.Requires == nil {
						manifest.Requires = &Requirements{}
					}
					// Dedupe
					found := false
					for _, p := range manifest.Requires.Profiles {
						if p == profile {
							found = true
							break
						}
					}
					if !found {
						manifest.Requires.Profiles = append(manifest.Requires.Profiles, profile)
					}
				}
			}
		}
	}

	if manifest.Name == "" {
		return fmt.Errorf("Agentfile missing NAME")
	}
	if manifest.Version == "" {
		manifest.Version = "0.0.0"
	}

	return nil
}

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
	sort.Strings(files)

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
		return nil, fmt.Errorf("package missing manifest.json")
	}
	if pkg.Content == nil {
		return nil, fmt.Errorf("package missing content.tar.gz")
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

// Verify verifies package signature.
func Verify(pkg *Package, publicKey ed25519.PublicKey) error {
	// Re-serialize manifest for hashing
	manifestJSON, err := json.MarshalIndent(pkg.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}

	// Calculate hashes
	manifestHash := sha256.Sum256(manifestJSON)
	contentHash := sha256.Sum256(pkg.Content)

	if publicKey == nil {
		// No key provided, skip signature verification
		return nil
	}

	if pkg.Signature == nil {
		return fmt.Errorf("package is not signed")
	}

	// Verify signature over concatenated hashes
	toVerify := append(manifestHash[:], contentHash[:]...)
	if !ed25519.Verify(publicKey, toVerify, pkg.Signature) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// InstallOptions configures package installation.
type InstallOptions struct {
	PackagePath string
	TargetDir   string // Default: ~/.agent/packages/
	PublicKey   ed25519.PublicKey
	NoDeps      bool
	DryRun      bool
}

// InstallResult contains installation results.
type InstallResult struct {
	Installed    []string // Package names installed
	Dependencies []string // Dependency names (for display)
	InstallPath  string   // Where package was installed
}

// Install installs a package.
func Install(opts InstallOptions) (*InstallResult, error) {
	pkg, err := Load(opts.PackagePath)
	if err != nil {
		return nil, err
	}

	// Verify
	if err := Verify(pkg, opts.PublicKey); err != nil {
		return nil, fmt.Errorf("verification failed: %w", err)
	}

	result := &InstallResult{
		Installed: []string{pkg.Manifest.Name},
	}

	// Collect dependencies
	if pkg.Manifest.Dependencies != nil && !opts.NoDeps {
		for dep := range pkg.Manifest.Dependencies {
			result.Dependencies = append(result.Dependencies, dep)
		}
	}

	if opts.DryRun {
		return result, nil
	}

	// Determine target directory
	targetDir := opts.TargetDir
	if targetDir == "" {
		home, _ := os.UserHomeDir()
		targetDir = filepath.Join(home, ".agent", "packages")
	}

	// Create package directory
	pkgDir := filepath.Join(targetDir, pkg.Manifest.Name, pkg.Manifest.Version)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create package directory: %w", err)
	}

	// Extract content
	if err := extractContent(pkg.Content, pkgDir); err != nil {
		return nil, fmt.Errorf("failed to extract content: %w", err)
	}

	result.InstallPath = pkgDir

	return result, nil
}

// extractContent extracts tar.gz content to a directory.
func extractContent(content []byte, targetDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		targetPath := filepath.Join(targetDir, header.Name)

		// Security: prevent path traversal
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(targetDir)) {
			return fmt.Errorf("invalid path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			f, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}

	return nil
}

// GenerateKeyPair generates a new Ed25519 key pair.
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// SavePrivateKey saves a private key to a PEM file.
func SavePrivateKey(path string, key ed25519.PrivateKey) error {
	block := &pem.Block{
		Type:  "ED25519 PRIVATE KEY",
		Bytes: key,
	}
	data := pem.EncodeToMemory(block)
	return os.WriteFile(path, data, 0600)
}

// LoadPrivateKey loads a private key from a PEM file.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	if block.Type != "ED25519 PRIVATE KEY" {
		return nil, fmt.Errorf("unexpected key type: %s", block.Type)
	}
	return ed25519.PrivateKey(block.Bytes), nil
}

// SavePublicKey saves a public key to a PEM file.
func SavePublicKey(path string, key ed25519.PublicKey) error {
	block := &pem.Block{
		Type:  "ED25519 PUBLIC KEY",
		Bytes: key,
	}
	data := pem.EncodeToMemory(block)
	return os.WriteFile(path, data, 0644)
}

// LoadPublicKey loads a public key from a PEM file.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	if block.Type != "ED25519 PUBLIC KEY" {
		return nil, fmt.Errorf("unexpected key type: %s", block.Type)
	}
	return ed25519.PublicKey(block.Bytes), nil
}

// LoadFromPath loads a package from a file path (convenience wrapper for Load).
func LoadFromPath(path string) (*Package, error) {
	return Load(path)
}

// LoadByName loads a package by name from a base directory.
func LoadByName(baseDir, name string) (*Package, error) {
	// Try with .agent extension
	path := filepath.Join(baseDir, name+".agent")
	if _, err := os.Stat(path); err == nil {
		return Load(path)
	}

	// Try as-is
	path = filepath.Join(baseDir, name)
	if _, err := os.Stat(path); err == nil {
		return Load(path)
	}

	return nil, fmt.Errorf("package not found: %s in %s", name, baseDir)
}

// ExtractToTemp extracts package contents to a temporary directory.
func (p *Package) ExtractToTemp() (string, error) {
	tmpDir, err := os.MkdirTemp("", "agent-"+p.Manifest.Name+"-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	if err := extractContent(p.Content, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to extract content: %w", err)
	}

	return tmpDir, nil
}

// GetFile reads a specific file from the package content.
func (p *Package) GetFile(name string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(p.Content))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
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

// GetAgentfile returns the Agentfile content from the package.
func (p *Package) GetAgentfile() ([]byte, error) {
	return p.GetFile("Agentfile")
}

// GetConfig returns the agent.json config from the package.
func (p *Package) GetConfig() ([]byte, error) {
	return p.GetFile("agent.json")
}

// GetPolicy returns the policy.toml from the package.
func (p *Package) GetPolicy() ([]byte, error) {
	return p.GetFile("policy.toml")
}

// validateAgentReferences checks that AGENT FROM doesn't reference .agent packages.
// Packages must be self-contained; inter-package dependencies use manifest.json.
func validateAgentReferences(agentfilePath string) error {
	content, err := os.ReadFile(agentfilePath)
	if err != nil {
		return fmt.Errorf("failed to read Agentfile: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	for lineNum, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		// Check AGENT ... FROM <path>
		if fields[0] == "AGENT" {
			for i, f := range fields {
				if f == "FROM" && i+1 < len(fields) {
					path := fields[i+1]
					// Remove quotes if present
					path = strings.Trim(path, "\"'")
					
					if strings.HasSuffix(path, ".agent") {
						return fmt.Errorf(
							"line %d: AGENT cannot reference .agent packages (%s). "+
								"Packages must be self-contained. Use manifest.json dependencies for inter-package relationships",
							lineNum+1, path,
						)
					}
				}
			}
		}
	}

	return nil
}
