package packaging

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackAndLoad(t *testing.T) {
	// Create temp directory with test agent
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(agentDir, 0755)

	// Create Agentfile
	agentfile := `NAME test-agent
VERSION 1.0.0
INPUT path
INPUT format DEFAULT "json"
GOAL process "Process the input at $path"
RUN main USING process`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	// Generate key pair
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Pack
	outputPath := filepath.Join(tmpDir, "test-agent-1.0.0.agent")
	pkg, err := Pack(PackOptions{
		SourceDir:  agentDir,
		OutputPath: outputPath,
		PrivateKey: privKey,
		Author: &Author{
			Name:  "Test Author",
			Email: "test@example.com",
		},
		Description: "A test agent",
		License:     "MIT",
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	// Verify manifest
	if pkg.Manifest.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got %q", pkg.Manifest.Name)
	}
	if pkg.Manifest.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", pkg.Manifest.Version)
	}
	if len(pkg.Manifest.Inputs) != 2 {
		t.Errorf("expected 2 inputs, got %d", len(pkg.Manifest.Inputs))
	}
	if pkg.Manifest.Inputs["path"].Required != true {
		t.Error("expected 'path' input to be required")
	}
	if pkg.Manifest.Inputs["format"].Default != "json" {
		t.Errorf("expected 'format' default 'json', got %q", pkg.Manifest.Inputs["format"].Default)
	}

	// Verify signature exists
	if pkg.Signature == nil {
		t.Fatal("expected signature to be set")
	}
	if len(pkg.Signature) != 64 {
		t.Errorf("expected 64-byte signature, got %d", len(pkg.Signature))
	}

	// Load
	loaded, err := Load(outputPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Manifest.Name != pkg.Manifest.Name {
		t.Error("loaded manifest doesn't match")
	}

	// Verify
	if err := Verify(loaded, pubKey); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME tamper-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	pubKey, privKey, _ := GenerateKeyPair()

	outputPath := filepath.Join(tmpDir, "tamper-test.agent")
	_, err := Pack(PackOptions{
		SourceDir:  agentDir,
		OutputPath: outputPath,
		PrivateKey: privKey,
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	// Load and tamper
	pkg, _ := Load(outputPath)
	pkg.Content = []byte("tampered content")

	// Verify should fail
	err = Verify(pkg, pubKey)
	if err == nil {
		t.Error("expected verification to fail for tampered content")
	}
}

func TestVerifyWrongKey(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME key-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	_, privKey, _ := GenerateKeyPair()
	wrongPubKey, _, _ := GenerateKeyPair()

	outputPath := filepath.Join(tmpDir, "key-test.agent")
	Pack(PackOptions{
		SourceDir:  agentDir,
		OutputPath: outputPath,
		PrivateKey: privKey,
	})

	pkg, _ := Load(outputPath)

	// Verify with wrong key should fail
	err := Verify(pkg, wrongPubKey)
	if err == nil {
		t.Error("expected verification to fail with wrong key")
	}
}

func TestInstall(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME install-test
VERSION 2.0.0
GOAL test "Test"
RUN main USING test`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)
	os.WriteFile(filepath.Join(agentDir, "README.md"), []byte("# Test"), 0644)

	outputPath := filepath.Join(tmpDir, "install-test.agent")
	Pack(PackOptions{
		SourceDir:  agentDir,
		OutputPath: outputPath,
	})

	installDir := filepath.Join(tmpDir, "installed")
	result, err := Install(InstallOptions{
		PackagePath: outputPath,
		TargetDir:   installDir,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if len(result.Installed) != 1 || result.Installed[0] != "install-test" {
		t.Errorf("unexpected installed: %v", result.Installed)
	}

	// Verify files were extracted
	expectedPath := filepath.Join(installDir, "install-test", "2.0.0", "Agentfile")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Agentfile not found at %s", expectedPath)
	}
}

func TestInstallDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME dryrun-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	// Create manifest with dependencies
	manifest := `{
		"name": "dryrun-test",
		"version": "1.0.0",
		"dependencies": {
			"dep-a": "^1.0.0",
			"dep-b": ">=2.0.0"
		}
	}`
	os.WriteFile(filepath.Join(agentDir, "manifest.json"), []byte(manifest), 0644)

	outputPath := filepath.Join(tmpDir, "dryrun-test.agent")
	Pack(PackOptions{
		SourceDir:  agentDir,
		OutputPath: outputPath,
	})

	installDir := filepath.Join(tmpDir, "should-not-exist")
	result, err := Install(InstallOptions{
		PackagePath: outputPath,
		TargetDir:   installDir,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Install dry run: %v", err)
	}

	if len(result.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(result.Dependencies))
	}

	// Directory should not exist
	if _, err := os.Stat(installDir); !os.IsNotExist(err) {
		t.Error("dry run should not create directory")
	}
}

func TestKeyPairSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	privPath := filepath.Join(tmpDir, "key.pem")
	pubPath := filepath.Join(tmpDir, "key.pub")

	pubKey, privKey, _ := GenerateKeyPair()

	if err := SavePrivateKey(privPath, privKey); err != nil {
		t.Fatalf("SavePrivateKey: %v", err)
	}
	if err := SavePublicKey(pubPath, pubKey); err != nil {
		t.Fatalf("SavePublicKey: %v", err)
	}

	loadedPriv, err := LoadPrivateKey(privPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	loadedPub, err := LoadPublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadPublicKey: %v", err)
	}

	// Verify keys work
	message := []byte("test message")
	sig := ed25519.Sign(loadedPriv, message)
	if !ed25519.Verify(loadedPub, message, sig) {
		t.Error("loaded keys don't work together")
	}
}

func TestExtractRequiresProfiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(agentDir, 0755)
	os.MkdirAll(filepath.Join(agentDir, "agents"), 0755)

	agentfile := `NAME profile-test
VERSION 1.0.0
AGENT critic FROM agents/critic.md REQUIRES "reasoning-heavy"
AGENT helper FROM agents/helper.md REQUIRES "fast"
GOAL review "Review" USING critic, helper
RUN main USING review`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)
	os.WriteFile(filepath.Join(agentDir, "agents/critic.md"), []byte("Critic"), 0644)
	os.WriteFile(filepath.Join(agentDir, "agents/helper.md"), []byte("Helper"), 0644)

	outputPath := filepath.Join(tmpDir, "profile-test.agent")
	pkg, err := Pack(PackOptions{
		SourceDir:  agentDir,
		OutputPath: outputPath,
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if pkg.Manifest.Requires == nil {
		t.Fatal("expected Requires to be set")
	}
	if len(pkg.Manifest.Requires.Profiles) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(pkg.Manifest.Requires.Profiles))
	}
}

func TestDeterministicPacking(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "test-agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME deterministic-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	// Pack twice
	pkg1, _ := Pack(PackOptions{SourceDir: agentDir})
	pkg2, _ := Pack(PackOptions{SourceDir: agentDir})

	// Content should be identical
	if string(pkg1.Content) != string(pkg2.Content) {
		t.Error("packing is not deterministic - content differs")
	}
}

func TestValidateAgentReferences_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	agentfile := `NAME test
AGENT critic FROM agents/critic.md
AGENT helper FROM skills/helper/SKILL.md
GOAL main "Test"
RUN main USING main
`
	path := filepath.Join(tmpDir, "Agentfile")
	os.WriteFile(path, []byte(agentfile), 0644)

	err := validateAgentReferences(path)
	if err != nil {
		t.Errorf("expected no error for valid references, got: %v", err)
	}
}

func TestValidateAgentReferences_RejectsAgentPackage(t *testing.T) {
	tmpDir := t.TempDir()
	agentfile := `NAME test
AGENT helper FROM other-agent.agent
GOAL main "Test"
RUN main USING main
`
	path := filepath.Join(tmpDir, "Agentfile")
	os.WriteFile(path, []byte(agentfile), 0644)

	err := validateAgentReferences(path)
	if err == nil {
		t.Error("expected error for .agent reference")
	}
	if !strings.Contains(err.Error(), ".agent packages") {
		t.Errorf("expected error about .agent packages, got: %v", err)
	}
}

func TestValidateAgentReferences_RejectsPathWithAgentExtension(t *testing.T) {
	tmpDir := t.TempDir()
	agentfile := `NAME test
AGENT coder FROM packages/coder-go.agent REQUIRES "fast"
GOAL main "Test"
RUN main USING main
`
	path := filepath.Join(tmpDir, "Agentfile")
	os.WriteFile(path, []byte(agentfile), 0644)

	err := validateAgentReferences(path)
	if err == nil {
		t.Error("expected error for .agent path reference")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("expected error on line 2, got: %v", err)
	}
}

func TestValidateAgentReferences_AllowsQuotedPaths(t *testing.T) {
	tmpDir := t.TempDir()
	agentfile := `NAME test
AGENT critic FROM "agents/my critic.md"
GOAL main "Test"
RUN main USING main
`
	path := filepath.Join(tmpDir, "Agentfile")
	os.WriteFile(path, []byte(agentfile), 0644)

	err := validateAgentReferences(path)
	if err != nil {
		t.Errorf("expected no error for quoted .md path, got: %v", err)
	}
}
