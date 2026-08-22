package packaging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyUnsigned(t *testing.T) {
	pub, _, _ := GenerateKeyPair()
	pkg := &Package{Manifest: &Manifest{Name: "x"}, Content: []byte("c")}
	if err := Verify(pkg, nil); err != nil {
		t.Errorf("Verify with nil key = %v, want nil", err)
	}
	if err := Verify(pkg, pub); err == nil {
		t.Error("Verify unsigned package with key = nil, want error")
	}
}

func TestLoadKeyErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o600)
		return p
	}
	tests := []struct {
		name string
		path string
	}{
		{"missing", filepath.Join(dir, "nope.pem")},
		{"not pem", write("junk.pem", "not pem at all")},
		{"wrong type", write("wrong.pem", "-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----\n")},
		{"wrong size private", write("short.pem", "-----BEGIN ED25519 PRIVATE KEY-----\nAAAA\n-----END ED25519 PRIVATE KEY-----\n")},
		{"wrong size public", write("shortpub.pem", "-----BEGIN ED25519 PUBLIC KEY-----\nAAAA\n-----END ED25519 PUBLIC KEY-----\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadPrivateKey(tt.path); err == nil {
				t.Errorf("LoadPrivateKey(%s) = nil error, want error", tt.name)
			}
			if _, err := LoadPublicKey(tt.path); err == nil {
				t.Errorf("LoadPublicKey(%s) = nil error, want error", tt.name)
			}
		})
	}
}
