package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// AuditTrail manages cryptographic signing of security decisions.
type AuditTrail struct {
	sessionID  string
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	records    []*SecurityRecord
}

// NewAuditTrail creates a new audit trail with a fresh Ed25519 keypair.
func NewAuditTrail(sessionID string) (*AuditTrail, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	return &AuditTrail{
		sessionID:  sessionID,
		publicKey:  pub,
		privateKey: priv,
		records:    make([]*SecurityRecord, 0),
	}, nil
}

// PublicKey returns the base64-encoded public key for verification.
func (a *AuditTrail) PublicKey() string {
	return base64.StdEncoding.EncodeToString(a.publicKey)
}

// SessionID returns the session identifier.
func (a *AuditTrail) SessionID() string {
	return a.sessionID
}

// SecurityRecord represents a signed security decision.
type SecurityRecord struct {
	BlockID     string    `json:"block_id"`
	SessionID   string    `json:"session_id"`
	Timestamp   time.Time `json:"timestamp"`
	ContentHash string    `json:"content_hash"`
	Trust       string    `json:"trust"`
	Type        string    `json:"type"`
	Tier1Result string    `json:"tier1_result"`
	Tier2Result string    `json:"tier2_result"`
	Tier3Result string    `json:"tier3_result"`
	Signature   string    `json:"signature,omitempty"`
}

// RecordDecision creates and signs a security decision record.
func (a *AuditTrail) RecordDecision(block *Block, tier1, tier2, tier3 string) *SecurityRecord {
	record := &SecurityRecord{
		BlockID:     block.ID,
		SessionID:   a.sessionID,
		Timestamp:   time.Now().UTC(),
		ContentHash: hashContent(block.Content),
		Trust:       string(block.Trust),
		Type:        string(block.Type),
		Tier1Result: tier1,
		Tier2Result: tier2,
		Tier3Result: tier3,
	}

	// Sign the record
	record.Signature = a.signRecord(record)
	a.records = append(a.records, record)

	return record
}

// signRecord creates an Ed25519 signature for the record.
func (a *AuditTrail) signRecord(record *SecurityRecord) string {
	// Create canonical JSON (without signature field)
	canonical := a.canonicalJSON(record)

	// Hash with SHA-256
	hash := sha256.Sum256(canonical)

	// Sign
	sig := ed25519.Sign(a.privateKey, hash[:])

	return base64.StdEncoding.EncodeToString(sig)
}

// canonicalJSON creates a deterministic JSON representation.
// Fields are sorted alphabetically, no extra whitespace.
func (a *AuditTrail) canonicalJSON(record *SecurityRecord) []byte {
	// Create map for canonical ordering
	m := map[string]interface{}{
		"block_id":     record.BlockID,
		"content_hash": record.ContentHash,
		"session_id":   record.SessionID,
		"tier1_result": record.Tier1Result,
		"tier2_result": record.Tier2Result,
		"tier3_result": record.Tier3Result,
		"timestamp":    record.Timestamp.Format(time.RFC3339Nano),
		"trust":        record.Trust,
		"type":         record.Type,
	}

	// Sort keys
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build canonical JSON manually for consistency
	result := "{"
	for i, k := range keys {
		if i > 0 {
			result += ","
		}
		v, _ := json.Marshal(m[k])
		result += fmt.Sprintf(`"%s":%s`, k, string(v))
	}
	result += "}"

	return []byte(result)
}

// hashContent creates a SHA-256 hash of content.
func hashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return base64.StdEncoding.EncodeToString(hash[:])
}

// VerifyRecord verifies a security record's signature.
func VerifyRecord(record *SecurityRecord, publicKeyBase64 string) (bool, error) {
	// Decode public key
	pubKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return false, fmt.Errorf("invalid public key size: %d", len(pubKeyBytes))
	}

	publicKey := ed25519.PublicKey(pubKeyBytes)

	// Decode signature
	sigBytes, err := base64.StdEncoding.DecodeString(record.Signature)
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}

	// Recreate canonical JSON
	trail := &AuditTrail{} // Just for the method
	canonical := trail.canonicalJSON(record)

	// Hash
	hash := sha256.Sum256(canonical)

	// Verify
	return ed25519.Verify(publicKey, hash[:], sigBytes), nil
}

// Records returns all recorded security decisions.
func (a *AuditTrail) Records() []*SecurityRecord {
	return a.records
}

// SessionLog represents a complete session security log.
type SessionLog struct {
	SessionID       string            `json:"session_id"`
	StartedAt       time.Time         `json:"started_at"`
	PublicKey       string            `json:"public_key"`
	SecurityMode    string            `json:"security_mode"`
	SecurityRecords []*SecurityRecord `json:"security_records"`
}

// ExportLog exports the audit trail as a session log.
func (a *AuditTrail) ExportLog(securityMode string) *SessionLog {
	return &SessionLog{
		SessionID:       a.sessionID,
		StartedAt:       time.Now().UTC(),
		PublicKey:       a.PublicKey(),
		SecurityMode:    securityMode,
		SecurityRecords: a.records,
	}
}

// Destroy zeros out the private key from memory.
// Call this when the session ends.
func (a *AuditTrail) Destroy() {
	for i := range a.privateKey {
		a.privateKey[i] = 0
	}
	a.privateKey = nil
}
