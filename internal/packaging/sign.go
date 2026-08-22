package packaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// Verify checks the package signature against publicKey. A nil key skips
// verification.
func Verify(pkg *Package, publicKey ed25519.PublicKey) error {
	if publicKey == nil {
		return nil
	}
	if pkg.Signature == nil {
		return errors.New("package is not signed")
	}
	manifestJSON, err := json.MarshalIndent(pkg.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}
	if !ed25519.Verify(publicKey, signingInput(manifestJSON, pkg.Content), pkg.Signature) {
		return errors.New("signature verification failed")
	}
	return nil
}

// signingInput is what gets signed: sha256(manifest JSON) || sha256(content).
func signingInput(manifestJSON, content []byte) []byte {
	manifestHash := sha256.Sum256(manifestJSON)
	contentHash := sha256.Sum256(content)
	return append(manifestHash[:], contentHash[:]...)
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
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

// LoadPrivateKey loads a private key from a PEM file.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, err := decodeKey(data, "ED25519 PRIVATE KEY", ed25519.PrivateKeySize)
	if err != nil {
		return nil, err
	}
	return ed25519.PrivateKey(block), nil
}

// SavePublicKey saves a public key to a PEM file.
func SavePublicKey(path string, key ed25519.PublicKey) error {
	block := &pem.Block{
		Type:  "ED25519 PUBLIC KEY",
		Bytes: key,
	}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o644)
}

// LoadPublicKey loads a public key from a PEM file.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, err := decodeKey(data, "ED25519 PUBLIC KEY", ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(block), nil
}

// decodeKey returns the bytes of the first PEM block in data, which must have
// the given type and size.
func decodeKey(data []byte, keyType string, size int) ([]byte, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if block.Type != keyType {
		return nil, fmt.Errorf("unexpected key type: %q", block.Type)
	}
	if len(block.Bytes) != size {
		return nil, fmt.Errorf("%s: %d bytes, want %d", keyType, len(block.Bytes), size)
	}
	return block.Bytes, nil
}
