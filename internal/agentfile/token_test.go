package agentfile

import "testing"

func TestTokenType_String(t *testing.T) {
	tests := []struct {
		typ  TokenType
		want string
	}{
		{TokenEOF, "EOF"},
		{TokenIllegal, "ILLEGAL"},
		{TokenNewline, "NEWLINE"},
		{TokenNAME, "NAME"},
		{TokenINPUT, "INPUT"},
		{TokenAGENT, "AGENT"},
		{TokenGOAL, "GOAL"},
		{TokenCONVERGE, "CONVERGE"},
		{TokenRUN, "RUN"},
		{TokenFROM, "FROM"},
		{TokenUSING, "USING"},
		{TokenWITHIN, "WITHIN"},
		{TokenDEFAULT, "DEFAULT"},
		{TokenREQUIRES, "REQUIRES"},
		{TokenSUPERVISED, "SUPERVISED"},
		{TokenHUMAN, "HUMAN"},
		{TokenUNSUPERVISED, "UNSUPERVISED"},
		{TokenSECURITY, "SECURITY"},
		{TokenIdent, "IDENT"},
		{TokenString, "STRING"},
		{TokenNumber, "NUMBER"},
		{TokenPath, "PATH"},
		{TokenVar, "VAR"},
		{TokenComma, "COMMA"},
		{TokenArrow, "ARROW"},
		{TokenType(999), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.want {
				t.Errorf("TokenType(%d).String() = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

func TestLookupIdent(t *testing.T) {
	tests := []struct {
		ident string
		want  TokenType
	}{
		{"NAME", TokenNAME},
		{"RUN", TokenRUN},
		{"not_a_keyword", TokenIdent},
	}
	for _, tt := range tests {
		t.Run(tt.ident, func(t *testing.T) {
			if got := lookupIdent(tt.ident); got != tt.want {
				t.Errorf("lookupIdent(%q) = %v, want %v", tt.ident, got, tt.want)
			}
		})
	}
}
