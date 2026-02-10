// Package agentfile provides lexer, parser, and AST for the Agentfile DSL.
package agentfile

// TokenType represents the type of a token.
type TokenType int

const (
	// Special tokens
	TokenEOF TokenType = iota
	TokenIllegal
	TokenNewline

	// Keywords
	TokenNAME
	TokenINPUT
	TokenAGENT
	TokenGOAL
	TokenRUN
	TokenLOOP
	TokenFROM
	TokenUSING
	TokenWITHIN
	TokenDEFAULT
	TokenREQUIRES
	TokenSUPERVISED
	TokenHUMAN
	TokenUNSUPERVISED
	TokenSECURITY

	// Literals
	TokenIdent   // identifier
	TokenString  // "quoted string"
	TokenNumber  // 123
	TokenPath    // path/to/file.md
	TokenVar     // $variable

	// Punctuation
	TokenComma // ,
	TokenArrow // ->
)

// String returns the string representation of the token type.
func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenIllegal:
		return "ILLEGAL"
	case TokenNewline:
		return "NEWLINE"
	case TokenNAME:
		return "NAME"
	case TokenINPUT:
		return "INPUT"
	case TokenAGENT:
		return "AGENT"
	case TokenGOAL:
		return "GOAL"
	case TokenRUN:
		return "RUN"
	case TokenLOOP:
		return "LOOP"
	case TokenFROM:
		return "FROM"
	case TokenUSING:
		return "USING"
	case TokenWITHIN:
		return "WITHIN"
	case TokenDEFAULT:
		return "DEFAULT"
	case TokenREQUIRES:
		return "REQUIRES"
	case TokenSUPERVISED:
		return "SUPERVISED"
	case TokenHUMAN:
		return "HUMAN"
	case TokenUNSUPERVISED:
		return "UNSUPERVISED"
	case TokenSECURITY:
		return "SECURITY"
	case TokenIdent:
		return "IDENT"
	case TokenString:
		return "STRING"
	case TokenNumber:
		return "NUMBER"
	case TokenPath:
		return "PATH"
	case TokenVar:
		return "VAR"
	case TokenComma:
		return "COMMA"
	case TokenArrow:
		return "ARROW"
	default:
		return "UNKNOWN"
	}
}

// Token represents a single token from the lexer.
type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Column  int
}

// keywords maps keyword strings to their token types.
var keywords = map[string]TokenType{
	"NAME":         TokenNAME,
	"INPUT":        TokenINPUT,
	"AGENT":        TokenAGENT,
	"GOAL":         TokenGOAL,
	"RUN":          TokenRUN,
	"LOOP":         TokenLOOP,
	"FROM":         TokenFROM,
	"USING":        TokenUSING,
	"WITHIN":       TokenWITHIN,
	"DEFAULT":      TokenDEFAULT,
	"REQUIRES":     TokenREQUIRES,
	"SUPERVISED":   TokenSUPERVISED,
	"HUMAN":        TokenHUMAN,
	"UNSUPERVISED": TokenUNSUPERVISED,
	"SECURITY":     TokenSECURITY,
}

// LookupIdent checks if an identifier is a keyword.
func LookupIdent(ident string) TokenType {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return TokenIdent
}
