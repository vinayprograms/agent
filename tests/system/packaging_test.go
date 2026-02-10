// Package system contains end-to-end system tests for packaging.
package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackaging_KeygenCommand tests key generation.
func TestPackaging_KeygenCommand(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test-key")

	cmd := exec.Command("go", "run", "./cmd/agent", "keygen", "-o", keyPath)
	cmd.Dir = getSrcDir(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("keygen failed: %v\n%s", err, output)
	}

	// Check files were created
	if _, err := os.Stat(keyPath + ".pem"); os.IsNotExist(err) {
		t.Error("private key not created")
	}
	if _, err := os.Stat(keyPath + ".pub"); os.IsNotExist(err) {
		t.Error("public key not created")
	}

	if !strings.Contains(string(output), "Generated") {
		t.Errorf("expected 'Generated' message, got: %s", output)
	}
}

// TestPackaging_PackCommand tests package creation.
func TestPackaging_PackCommand(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "my-agent")
	os.MkdirAll(agentDir, 0755)

	// Create Agentfile
	agentfile := `NAME pack-test
VERSION 1.2.3
INPUT query
INPUT limit DEFAULT "10"
GOAL search "Search for $query"
RUN main USING search
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	outputPath := filepath.Join(tmpDir, "pack-test.agent")

	cmd := exec.Command("go", "run", "./cmd/agent", "pack", agentDir, "-o", outputPath)
	cmd.Dir = getSrcDir(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pack failed: %v\n%s", err, output)
	}

	// Check package was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Error("package not created")
	}

	outStr := string(output)
	if !strings.Contains(outStr, "Created") {
		t.Errorf("expected 'Created' message, got: %s", outStr)
	}
	if !strings.Contains(outStr, "pack-test") {
		t.Errorf("expected package name, got: %s", outStr)
	}
	if !strings.Contains(outStr, "1.2.3") {
		t.Errorf("expected version, got: %s", outStr)
	}
}

// TestPackaging_PackWithSigning tests signed package creation.
func TestPackaging_PackWithSigning(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate keys
	keyPath := filepath.Join(tmpDir, "key")
	cmd := exec.Command("go", "run", "./cmd/agent", "keygen", "-o", keyPath)
	cmd.Dir = getSrcDir(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("keygen failed: %v\n%s", err, output)
	}

	// Create agent
	agentDir := filepath.Join(tmpDir, "signed-agent")
	os.MkdirAll(agentDir, 0755)
	agentfile := `NAME signed-test
VERSION 2.0.0
GOAL test "Test"
RUN main USING test
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	outputPath := filepath.Join(tmpDir, "signed-test.agent")

	// Pack with signing
	cmd = exec.Command("go", "run", "./cmd/agent", "pack", agentDir,
		"--sign", keyPath+".pem",
		"-o", outputPath)
	cmd.Dir = getSrcDir(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pack with signing failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "Signed: yes") {
		t.Errorf("expected 'Signed: yes', got: %s", output)
	}
}

// TestPackaging_VerifyCommand tests package verification.
func TestPackaging_VerifyCommand(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate keys
	keyPath := filepath.Join(tmpDir, "key")
	cmd := exec.Command("go", "run", "./cmd/agent", "keygen", "-o", keyPath)
	cmd.Dir = getSrcDir(t)
	cmd.CombinedOutput()

	// Create and pack agent
	agentDir := filepath.Join(tmpDir, "verify-agent")
	os.MkdirAll(agentDir, 0755)
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(`NAME verify-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test
`), 0644)

	pkgPath := filepath.Join(tmpDir, "verify-test.agent")
	cmd = exec.Command("go", "run", "./cmd/agent", "pack", agentDir,
		"--sign", keyPath+".pem", "-o", pkgPath)
	cmd.Dir = getSrcDir(t)
	cmd.CombinedOutput()

	// Verify
	cmd = exec.Command("go", "run", "./cmd/agent", "verify", pkgPath,
		"--key", keyPath+".pub")
	cmd.Dir = getSrcDir(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify failed: %v\n%s", err, output)
	}

	outStr := string(output)
	if !strings.Contains(outStr, "verified") {
		t.Errorf("expected 'verified', got: %s", outStr)
	}
	if !strings.Contains(outStr, "Signature: valid") {
		t.Errorf("expected 'Signature: valid', got: %s", outStr)
	}
}

// TestPackaging_VerifyWrongKey tests verification with wrong key fails.
func TestPackaging_VerifyWrongKey(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate two key pairs
	key1Path := filepath.Join(tmpDir, "key1")
	key2Path := filepath.Join(tmpDir, "key2")
	srcDir := getSrcDir(t)

	exec.Command("go", "run", "./cmd/agent", "keygen", "-o", key1Path).Run()
	cmd := exec.Command("go", "run", "./cmd/agent", "keygen", "-o", key2Path)
	cmd.Dir = srcDir
	cmd.Run()

	// Create and pack with key1
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(`NAME wrong-key-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test
`), 0644)

	pkgPath := filepath.Join(tmpDir, "test.agent")
	cmd = exec.Command("go", "run", "./cmd/agent", "pack", agentDir,
		"--sign", key1Path+".pem", "-o", pkgPath)
	cmd.Dir = srcDir
	cmd.CombinedOutput()

	// Verify with key2 - should fail
	cmd = exec.Command("go", "run", "./cmd/agent", "verify", pkgPath,
		"--key", key2Path+".pub")
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Error("expected verification to fail with wrong key")
	}
	if !strings.Contains(string(output), "failed") {
		t.Errorf("expected 'failed' message, got: %s", output)
	}
}

// TestPackaging_InspectCommand tests package inspection.
func TestPackaging_InspectCommand(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME inspect-test
VERSION 3.1.4
INPUT source
INPUT format DEFAULT "json"
GOAL process "Process"
RUN main USING process
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	pkgPath := filepath.Join(tmpDir, "inspect.agent")
	srcDir := getSrcDir(t)

	cmd := exec.Command("go", "run", "./cmd/agent", "pack", agentDir, "-o", pkgPath)
	cmd.Dir = srcDir
	cmd.CombinedOutput()

	// Inspect
	cmd = exec.Command("go", "run", "./cmd/agent", "inspect", pkgPath)
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\n%s", err, output)
	}

	outStr := string(output)
	if !strings.Contains(outStr, "inspect-test@3.1.4") {
		t.Errorf("expected 'inspect-test@3.1.4', got: %s", outStr)
	}
	if !strings.Contains(outStr, "source") {
		t.Errorf("expected input 'source', got: %s", outStr)
	}
	if !strings.Contains(outStr, "format") {
		t.Errorf("expected input 'format', got: %s", outStr)
	}
}

// TestPackaging_InstallCommand tests package installation.
func TestPackaging_InstallCommand(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME install-cli-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)
	os.WriteFile(filepath.Join(agentDir, "README.md"), []byte("# Test Agent"), 0644)

	pkgPath := filepath.Join(tmpDir, "install.agent")
	installDir := filepath.Join(tmpDir, "installed")
	srcDir := getSrcDir(t)

	cmd := exec.Command("go", "run", "./cmd/agent", "pack", agentDir, "-o", pkgPath)
	cmd.Dir = srcDir
	cmd.CombinedOutput()

	// Install
	cmd = exec.Command("go", "run", "./cmd/agent", "install", pkgPath, "--target", installDir)
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, output)
	}

	// Check files extracted
	agentfilePath := filepath.Join(installDir, "install-cli-test", "1.0.0", "Agentfile")
	if _, err := os.Stat(agentfilePath); os.IsNotExist(err) {
		t.Errorf("Agentfile not installed at %s", agentfilePath)
	}

	readmePath := filepath.Join(installDir, "install-cli-test", "1.0.0", "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		t.Errorf("README.md not installed at %s", readmePath)
	}
}

// TestPackaging_InstallDryRun tests dry-run installation.
func TestPackaging_InstallDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)

	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(`NAME dryrun-cli-test
VERSION 1.0.0
GOAL test "Test"
RUN main USING test
`), 0644)

	pkgPath := filepath.Join(tmpDir, "dryrun.agent")
	installDir := filepath.Join(tmpDir, "should-not-exist")
	srcDir := getSrcDir(t)

	cmd := exec.Command("go", "run", "./cmd/agent", "pack", agentDir, "-o", pkgPath)
	cmd.Dir = srcDir
	cmd.CombinedOutput()

	// Install with dry-run
	cmd = exec.Command("go", "run", "./cmd/agent", "install", pkgPath,
		"--target", installDir, "--dry-run")
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install dry-run failed: %v\n%s", err, output)
	}

	// Directory should NOT exist
	if _, err := os.Stat(installDir); !os.IsNotExist(err) {
		t.Error("dry-run should not create install directory")
	}

	if !strings.Contains(string(output), "Dry run") {
		t.Errorf("expected 'Dry run' message, got: %s", output)
	}
}

// TestPackaging_RequiresProfiles tests that REQUIRES profiles are extracted.
func TestPackaging_RequiresProfiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(filepath.Join(agentDir, "agents"), 0755)

	agentfile := `NAME profile-cli-test
VERSION 1.0.0
AGENT thinker FROM agents/thinker.md REQUIRES "reasoning-heavy"
AGENT fast FROM agents/fast.md REQUIRES "fast"
GOAL analyze "Analyze" USING thinker, fast
RUN main USING analyze
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)
	os.WriteFile(filepath.Join(agentDir, "agents/thinker.md"), []byte("Deep thinker"), 0644)
	os.WriteFile(filepath.Join(agentDir, "agents/fast.md"), []byte("Fast responder"), 0644)

	pkgPath := filepath.Join(tmpDir, "profiles.agent")
	srcDir := getSrcDir(t)

	cmd := exec.Command("go", "run", "./cmd/agent", "pack", agentDir, "-o", pkgPath)
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pack failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "Requires profiles:") {
		t.Errorf("expected 'Requires profiles:' message, got: %s", output)
	}
}

// TestPackaging_ManifestMerge tests that manifest.json is merged with Agentfile.
func TestPackaging_ManifestMerge(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)

	// Agentfile with basic info
	agentfile := `NAME manifest-merge-test
VERSION 2.0.0
INPUT query
GOAL search "Search"
RUN main USING search
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	// manifest.json with additional metadata
	manifest := `{
  "description": "A test agent for manifest merging",
  "license": "Apache-2.0",
  "author": {
    "name": "Test Author",
    "email": "test@example.com"
  },
  "dependencies": {
    "helper-agent": "^1.0.0"
  }
}`
	os.WriteFile(filepath.Join(agentDir, "manifest.json"), []byte(manifest), 0644)

	pkgPath := filepath.Join(tmpDir, "merged.agent")
	srcDir := getSrcDir(t)

	cmd := exec.Command("go", "run", "./cmd/agent", "pack", agentDir, "-o", pkgPath)
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pack failed: %v\n%s", err, output)
	}

	// Inspect to verify merge
	cmd = exec.Command("go", "run", "./cmd/agent", "inspect", pkgPath)
	cmd.Dir = srcDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\n%s", err, output)
	}

	outStr := string(output)
	// Should have NAME/VERSION from Agentfile
	if !strings.Contains(outStr, "manifest-merge-test@2.0.0") {
		t.Errorf("expected name/version from Agentfile, got: %s", outStr)
	}
	// Should have description from manifest.json
	if !strings.Contains(outStr, "test agent for manifest merging") {
		t.Errorf("expected description from manifest.json, got: %s", outStr)
	}
	// Should have dependencies from manifest.json
	if !strings.Contains(outStr, "helper-agent") {
		t.Errorf("expected dependencies from manifest.json, got: %s", outStr)
	}
}
