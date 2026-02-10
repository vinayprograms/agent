package security

import (
	"testing"
)

func TestAuditTrail_SignAndVerify(t *testing.T) {
	trail, err := NewAuditTrail("test-session")
	if err != nil {
		t.Fatalf("NewAuditTrail() error = %v", err)
	}
	defer trail.Destroy()

	// Create a block
	block := NewBlock("b001", TrustUntrusted, TypeData, true, "test content", "test")

	// Record a decision
	record := trail.RecordDecision(block, "escalate", "pass", "skipped")

	if record.Signature == "" {
		t.Error("Expected signature to be set")
	}

	// Verify the record
	valid, err := VerifyRecord(record, trail.PublicKey())
	if err != nil {
		t.Fatalf("VerifyRecord() error = %v", err)
	}

	if !valid {
		t.Error("Expected record to be valid")
	}
}

func TestAuditTrail_TamperedRecord(t *testing.T) {
	trail, err := NewAuditTrail("test-session")
	if err != nil {
		t.Fatalf("NewAuditTrail() error = %v", err)
	}
	defer trail.Destroy()

	block := NewBlock("b001", TrustUntrusted, TypeData, true, "original content", "test")
	record := trail.RecordDecision(block, "escalate", "pass", "skipped")

	// Tamper with the record
	record.Tier1Result = "pass" // Changed from "escalate"

	// Verify should fail
	valid, err := VerifyRecord(record, trail.PublicKey())
	if err != nil {
		t.Fatalf("VerifyRecord() error = %v", err)
	}

	if valid {
		t.Error("Expected tampered record to be invalid")
	}
}

func TestAuditTrail_MultipleRecords(t *testing.T) {
	trail, err := NewAuditTrail("test-session")
	if err != nil {
		t.Fatalf("NewAuditTrail() error = %v", err)
	}
	defer trail.Destroy()

	// Record multiple decisions
	for i := 0; i < 5; i++ {
		block := NewBlock("b00"+string(rune('0'+i)), TrustUntrusted, TypeData, true, "content", "test")
		trail.RecordDecision(block, "escalate", "escalate", "ALLOW")
	}

	records := trail.Records()
	if len(records) != 5 {
		t.Errorf("Expected 5 records, got %d", len(records))
	}

	// Verify all records
	pubKey := trail.PublicKey()
	for i, record := range records {
		valid, err := VerifyRecord(record, pubKey)
		if err != nil {
			t.Errorf("Record %d: VerifyRecord() error = %v", i, err)
		}
		if !valid {
			t.Errorf("Record %d: expected valid", i)
		}
	}
}

func TestAuditTrail_ExportLog(t *testing.T) {
	trail, err := NewAuditTrail("test-session")
	if err != nil {
		t.Fatalf("NewAuditTrail() error = %v", err)
	}
	defer trail.Destroy()

	block := NewBlock("b001", TrustUntrusted, TypeData, true, "content", "test")
	trail.RecordDecision(block, "pass", "skipped", "skipped")

	log := trail.ExportLog("default")

	if log.SessionID != "test-session" {
		t.Errorf("SessionID = %v, want test-session", log.SessionID)
	}

	if log.SecurityMode != "default" {
		t.Errorf("SecurityMode = %v, want default", log.SecurityMode)
	}

	if log.PublicKey == "" {
		t.Error("Expected PublicKey to be set")
	}

	if len(log.SecurityRecords) != 1 {
		t.Errorf("Expected 1 record, got %d", len(log.SecurityRecords))
	}
}

func TestAuditTrail_Destroy(t *testing.T) {
	trail, err := NewAuditTrail("test-session")
	if err != nil {
		t.Fatalf("NewAuditTrail() error = %v", err)
	}

	// Destroy should zero the private key
	trail.Destroy()

	if trail.privateKey != nil {
		t.Error("Expected privateKey to be nil after Destroy")
	}
}
