package security

import (
	"testing"
)

func TestGetTaintLineage_Simple(t *testing.T) {
	v, err := NewVerifier(Config{Mode: ModeDefault}, "test-session")
	if err != nil {
		t.Fatalf("failed to create verifier: %v", err)
	}
	defer v.Destroy()

	// Add a simple untrusted block
	block := v.AddBlockWithTaint(TrustUntrusted, TypeData, true, "malicious content", "tool:web_fetch", "", 42, nil)

	lineage := v.GetTaintLineage(block.ID)
	if lineage == nil {
		t.Fatal("expected non-nil lineage")
	}

	if lineage.BlockID != block.ID {
		t.Errorf("expected block ID %s, got %s", block.ID, lineage.BlockID)
	}
	if lineage.Trust != TrustUntrusted {
		t.Errorf("expected trust untrusted, got %s", lineage.Trust)
	}
	if lineage.Source != "tool:web_fetch" {
		t.Errorf("expected source tool:web_fetch, got %s", lineage.Source)
	}
	if lineage.EventSeq != 42 {
		t.Errorf("expected event seq 42, got %d", lineage.EventSeq)
	}
	if len(lineage.TaintedBy) != 0 {
		t.Errorf("expected no parents, got %d", len(lineage.TaintedBy))
	}
}

func TestGetTaintLineage_WithParents(t *testing.T) {
	v, err := NewVerifier(Config{Mode: ModeDefault}, "test-session")
	if err != nil {
		t.Fatalf("failed to create verifier: %v", err)
	}
	defer v.Destroy()

	// Add parent blocks (root sources)
	parent1 := v.AddBlockWithTaint(TrustUntrusted, TypeData, true, "content from web", "tool:web_fetch", "", 10, nil)
	parent2 := v.AddBlockWithTaint(TrustUntrusted, TypeData, true, "content from file", "tool:read", "", 15, nil)

	// Add a child block that was influenced by both parents
	child := v.AddBlockWithTaint(TrustVetted, TypeInstruction, false, "LLM response", "llm:response", "", 20, []string{parent1.ID, parent2.ID})

	lineage := v.GetTaintLineage(child.ID)
	if lineage == nil {
		t.Fatal("expected non-nil lineage")
	}

	if lineage.BlockID != child.ID {
		t.Errorf("expected block ID %s, got %s", child.ID, lineage.BlockID)
	}
	if len(lineage.TaintedBy) != 2 {
		t.Fatalf("expected 2 parents, got %d", len(lineage.TaintedBy))
	}

	// Check parents
	foundParent1, foundParent2 := false, false
	for _, p := range lineage.TaintedBy {
		if p.BlockID == parent1.ID {
			foundParent1 = true
			if p.Source != "tool:web_fetch" {
				t.Errorf("parent1 source mismatch: %s", p.Source)
			}
		}
		if p.BlockID == parent2.ID {
			foundParent2 = true
			if p.Source != "tool:read" {
				t.Errorf("parent2 source mismatch: %s", p.Source)
			}
		}
	}
	if !foundParent1 {
		t.Error("parent1 not found in lineage")
	}
	if !foundParent2 {
		t.Error("parent2 not found in lineage")
	}
}

func TestGetTaintLineage_DeepChain(t *testing.T) {
	v, err := NewVerifier(Config{Mode: ModeDefault}, "test-session")
	if err != nil {
		t.Fatalf("failed to create verifier: %v", err)
	}
	defer v.Destroy()

	// Create a chain: grandparent -> parent -> child
	grandparent := v.AddBlockWithTaint(TrustUntrusted, TypeData, true, "external data", "tool:web_fetch", "", 10, nil)
	parent := v.AddBlockWithTaint(TrustUntrusted, TypeData, true, "processed data", "llm:response", "", 20, []string{grandparent.ID})
	child := v.AddBlockWithTaint(TrustUntrusted, TypeData, true, "final output", "llm:response", "", 30, []string{parent.ID})

	lineage := v.GetTaintLineage(child.ID)
	if lineage == nil {
		t.Fatal("expected non-nil lineage")
	}

	// Check depth
	if lineage.Depth != 0 {
		t.Errorf("expected depth 0 for child, got %d", lineage.Depth)
	}
	if len(lineage.TaintedBy) != 1 {
		t.Fatalf("expected 1 parent for child, got %d", len(lineage.TaintedBy))
	}

	parentNode := lineage.TaintedBy[0]
	if parentNode.Depth != 1 {
		t.Errorf("expected depth 1 for parent, got %d", parentNode.Depth)
	}
	if len(parentNode.TaintedBy) != 1 {
		t.Fatalf("expected 1 grandparent, got %d", len(parentNode.TaintedBy))
	}

	grandparentNode := parentNode.TaintedBy[0]
	if grandparentNode.Depth != 2 {
		t.Errorf("expected depth 2 for grandparent, got %d", grandparentNode.Depth)
	}
	if grandparentNode.BlockID != grandparent.ID {
		t.Errorf("expected grandparent ID %s, got %s", grandparent.ID, grandparentNode.BlockID)
	}
}

func TestGetTaintLineage_NotFound(t *testing.T) {
	v, err := NewVerifier(Config{Mode: ModeDefault}, "test-session")
	if err != nil {
		t.Fatalf("failed to create verifier: %v", err)
	}
	defer v.Destroy()

	lineage := v.GetTaintLineage("nonexistent")
	if lineage != nil {
		t.Error("expected nil lineage for nonexistent block")
	}
}

func TestGetCurrentUntrustedBlockIDs(t *testing.T) {
	v, err := NewVerifier(Config{Mode: ModeDefault}, "test-session")
	if err != nil {
		t.Fatalf("failed to create verifier: %v", err)
	}
	defer v.Destroy()

	// Add mixed blocks
	v.AddBlock(TrustTrusted, TypeInstruction, false, "system prompt", "system")
	v.AddBlock(TrustUntrusted, TypeData, true, "external 1", "tool:web")
	v.AddBlock(TrustVetted, TypeInstruction, false, "user prompt", "user")
	v.AddBlock(TrustUntrusted, TypeData, true, "external 2", "tool:read")

	ids := v.GetCurrentUntrustedBlockIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 untrusted blocks, got %d", len(ids))
	}
}
