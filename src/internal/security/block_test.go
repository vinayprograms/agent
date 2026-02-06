package security

import (
	"testing"
)

func TestNewBlock_EnforcesInvariants(t *testing.T) {
	tests := []struct {
		name         string
		trust        TrustLevel
		typ          BlockType
		mutable      bool
		wantType     BlockType
		wantMutable  bool
	}{
		{
			name:        "trusted instruction immutable - unchanged",
			trust:       TrustTrusted,
			typ:         TypeInstruction,
			mutable:     false,
			wantType:    TypeInstruction,
			wantMutable: false,
		},
		{
			name:        "vetted instruction mutable - unchanged",
			trust:       TrustVetted,
			typ:         TypeInstruction,
			mutable:     true,
			wantType:    TypeInstruction,
			wantMutable: true,
		},
		{
			name:        "untrusted instruction - forced to data",
			trust:       TrustUntrusted,
			typ:         TypeInstruction,
			mutable:     false,
			wantType:    TypeData,
			wantMutable: true,
		},
		{
			name:        "untrusted immutable - forced to mutable",
			trust:       TrustUntrusted,
			typ:         TypeData,
			mutable:     false,
			wantType:    TypeData,
			wantMutable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := NewBlock("test", tt.trust, tt.typ, tt.mutable, "content", "test")

			if block.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", block.Type, tt.wantType)
			}

			if block.Mutable != tt.wantMutable {
				t.Errorf("Mutable = %v, want %v", block.Mutable, tt.wantMutable)
			}
		})
	}
}

func TestBlock_CanOverride(t *testing.T) {
	immutable := NewBlock("sys", TrustTrusted, TypeInstruction, false, "system", "")
	mutableTrusted := NewBlock("commit", TrustTrusted, TypeInstruction, true, "commit", "")
	vetted := NewBlock("goal", TrustVetted, TypeInstruction, true, "goal", "")
	untrusted := NewBlock("file", TrustUntrusted, TypeData, true, "file content", "")

	tests := []struct {
		name   string
		a      *Block
		b      *Block
		canA   bool
	}{
		{"untrusted cannot override immutable", untrusted, immutable, false},
		{"untrusted cannot override mutable trusted", untrusted, mutableTrusted, false},
		{"untrusted cannot override vetted", untrusted, vetted, false},
		{"vetted cannot override immutable", vetted, immutable, false},
		{"trusted can override mutable vetted", mutableTrusted, vetted, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.CanOverride(tt.b); got != tt.canA {
				t.Errorf("a.CanOverride(b) = %v, want %v", got, tt.canA)
			}
		})
	}
}

func TestPropagatedTrust(t *testing.T) {
	tests := []struct {
		a, b TrustLevel
		want TrustLevel
	}{
		{TrustTrusted, TrustTrusted, TrustTrusted},
		{TrustTrusted, TrustVetted, TrustVetted},
		{TrustTrusted, TrustUntrusted, TrustUntrusted},
		{TrustVetted, TrustUntrusted, TrustUntrusted},
		{TrustVetted, TrustVetted, TrustVetted},
	}

	for _, tt := range tests {
		t.Run(string(tt.a)+"+"+string(tt.b), func(t *testing.T) {
			if got := PropagatedTrust(tt.a, tt.b); got != tt.want {
				t.Errorf("PropagatedTrust(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
