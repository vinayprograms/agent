// Package security provides prompt injection defense through trust-tagged content blocks,
// tiered verification, and cryptographic audit trails.
package security

// TrustLevel represents the origin-based authenticity of content.
type TrustLevel string

const (
	// TrustTrusted is for framework-generated content (system prompt, supervisor messages).
	TrustTrusted TrustLevel = "trusted"
	// TrustVetted is for human-authored content (Agentfile goals, signed packages).
	TrustVetted TrustLevel = "vetted"
	// TrustUntrusted is for external content (tool results, file reads, web fetches).
	TrustUntrusted TrustLevel = "untrusted"
)

// BlockType represents how content should be interpreted.
type BlockType string

const (
	// TypeInstruction means content contains executable instructions.
	TypeInstruction BlockType = "instruction"
	// TypeData means content is data only, never to be interpreted as instructions.
	TypeData BlockType = "data"
)

// Block represents a piece of content with security metadata.
type Block struct {
	// ID is a unique identifier for taint tracking.
	ID string `json:"id"`

	// Trust indicates who created the content.
	Trust TrustLevel `json:"trust"`

	// Type indicates how to interpret the content.
	Type BlockType `json:"type"`

	// Mutable indicates whether later content can override this block.
	// Immutable blocks have precedence immunity.
	Mutable bool `json:"mutable"`

	// Content is the actual text content.
	Content string `json:"content"`

	// Source describes where the content came from (for debugging).
	Source string `json:"source,omitempty"`

	// AgentContext identifies which agent/sub-agent created this block.
	// Used to filter blocks during security checks in multi-agent scenarios.
	AgentContext string `json:"agent_context,omitempty"`

	// TaintedBy lists IDs of blocks that influenced this block.
	TaintedBy []string `json:"tainted_by,omitempty"`

	// CreatedAtSeq is the session event sequence when this block was created.
	// Used to correlate blocks with session events in forensic analysis.
	CreatedAtSeq uint64 `json:"created_at_seq,omitempty"`
}

// NewBlock creates a new block with the given properties.
// It enforces security invariants:
// - Untrusted content is always type=data
// - Untrusted content is always mutable=true
func NewBlock(id string, trust TrustLevel, typ BlockType, mutable bool, content, source string) *Block {
	// Enforce invariants
	if trust == TrustUntrusted {
		typ = TypeData     // Untrusted content is NEVER instruction
		mutable = true     // Untrusted content cannot claim precedence immunity
	}

	return &Block{
		ID:      id,
		Trust:   trust,
		Type:    typ,
		Mutable: mutable,
		Content: content,
		Source:  source,
	}
}

// IsInstruction returns true if this block contains executable instructions.
func (b *Block) IsInstruction() bool {
	return b.Type == TypeInstruction
}

// IsData returns true if this block contains data only.
func (b *Block) IsData() bool {
	return b.Type == TypeData
}

// IsImmutable returns true if this block has precedence immunity.
func (b *Block) IsImmutable() bool {
	return !b.Mutable
}

// CanOverride returns true if this block can override the other block.
// Higher trust + immutable beats lower trust + mutable.
func (b *Block) CanOverride(other *Block) bool {
	// Immutable blocks cannot be overridden
	if other.IsImmutable() {
		return false
	}

	// Lower or equal trust cannot override higher trust
	if b.trustRank() <= other.trustRank() {
		return false
	}

	return true
}

// trustRank returns a numeric rank for trust level comparison.
func (b *Block) trustRank() int {
	switch b.Trust {
	case TrustTrusted:
		return 3
	case TrustVetted:
		return 2
	case TrustUntrusted:
		return 1
	default:
		return 0
	}
}

// PropagatedTrust returns the trust level when combining this block with another.
// The result is the lowest (least trusted) of the two.
func PropagatedTrust(a, b TrustLevel) TrustLevel {
	rankA := trustLevelRank(a)
	rankB := trustLevelRank(b)
	if rankA < rankB {
		return a
	}
	return b
}

func trustLevelRank(t TrustLevel) int {
	switch t {
	case TrustTrusted:
		return 3
	case TrustVetted:
		return 2
	case TrustUntrusted:
		return 1
	default:
		return 0
	}
}
