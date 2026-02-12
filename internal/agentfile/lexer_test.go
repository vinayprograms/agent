package agentfile

import (
	"testing"
)

// R1.1.1: Tokenize keywords
func TestLexer_Keywords(t *testing.T) {
	// Test keywords individually to avoid path-parsing mode interference
	keywords := []struct {
		input    string
		expected TokenType
	}{
		{"NAME", TokenNAME},
		{"INPUT", TokenINPUT},
		{"AGENT", TokenAGENT},
		{"GOAL", TokenGOAL},
		{"RUN", TokenRUN},
		{"LOOP", TokenLOOP},
		{"FROM", TokenFROM},
		{"USING", TokenUSING},
		{"WITHIN", TokenWITHIN},
		{"DEFAULT", TokenDEFAULT},
	}

	for i, kw := range keywords {
		l := NewLexer(kw.input)
		tok := l.NextToken()
		if tok.Type != kw.expected {
			t.Errorf("keywords[%d] - tokentype wrong. expected=%q, got=%q for input %q", 
				i, kw.expected, tok.Type, kw.input)
		}
		if tok.Literal != kw.input {
			t.Errorf("keywords[%d] - literal wrong. expected=%q, got=%q", 
				i, kw.input, tok.Literal)
		}
	}
}

// R1.1.2: Tokenize identifiers
func TestLexer_Identifiers(t *testing.T) {
	input := `my_workflow feature_request max_iterations analyze123 _private`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenIdent, "my_workflow"},
		{TokenIdent, "feature_request"},
		{TokenIdent, "max_iterations"},
		{TokenIdent, "analyze123"},
		{TokenIdent, "_private"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

// R1.1.3: Tokenize strings with escape sequences
func TestLexer_Strings(t *testing.T) {
	tests := []struct {
		input           string
		expectedLiteral string
	}{
		{`"hello world"`, "hello world"},
		{`"with \"quotes\""`, `with "quotes"`},
		{`"line1\nline2"`, "line1\nline2"},
		{`"tab\there"`, "tab\there"},
		{`"back\\slash"`, "back\\slash"},
		{`""`, ""},
	}

	for i, tt := range tests {
		l := NewLexer(tt.input)
		tok := l.NextToken()
		if tok.Type != TokenString {
			t.Errorf("tests[%d] - tokentype wrong. expected=STRING, got=%q", i, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

// R1.1.4: Tokenize paths (for FROM clause)
func TestLexer_Paths(t *testing.T) {
	// Paths are recognized after FROM keyword
	input := `FROM agents/creative.md`

	l := NewLexer(input)
	tok := l.NextToken() // FROM
	if tok.Type != TokenFROM {
		t.Errorf("expected FROM, got %s", tok.Type)
	}

	tok = l.NextToken() // path
	if tok.Type != TokenPath {
		t.Errorf("expected PATH, got %s", tok.Type)
	}
	if tok.Literal != "agents/creative.md" {
		t.Errorf("expected 'agents/creative.md', got %q", tok.Literal)
	}
}

// R1.1.5: Tokenize variables ($identifier)
func TestLexer_Variables(t *testing.T) {
	input := `$feature_request $max_iterations $WORKSPACE`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenVar, "feature_request"},
		{TokenVar, "max_iterations"},
		{TokenVar, "WORKSPACE"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

// R1.1.6: Tokenize numbers
func TestLexer_Numbers(t *testing.T) {
	input := `10 123 0 999`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenNumber, "10"},
		{TokenNumber, "123"},
		{TokenNumber, "0"},
		{TokenNumber, "999"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

// R1.1.7: Tokenize commas
func TestLexer_Commas(t *testing.T) {
	input := `creative, devils_advocate`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenIdent, "creative"},
		{TokenComma, ","},
		{TokenIdent, "devils_advocate"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

// R1.1.8: Skip comments
func TestLexer_Comments(t *testing.T) {
	input := `NAME test # this is a comment
INPUT feature_request # another comment`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenNAME, "NAME"},
		{TokenIdent, "test"},
		{TokenNewline, "\n"},
		{TokenINPUT, "INPUT"},
		{TokenIdent, "feature_request"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

// R1.1.9: Skip empty lines
func TestLexer_EmptyLines(t *testing.T) {
	input := `NAME test


INPUT feature_request`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenNAME, "NAME"},
		{TokenIdent, "test"},
		{TokenNewline, "\n"},
		{TokenINPUT, "INPUT"},
		{TokenIdent, "feature_request"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
	}
}

// R1.1.10: Track line numbers for error reporting
func TestLexer_LineNumbers(t *testing.T) {
	input := `NAME test
INPUT feature_request
AGENT creative FROM agents/creative.md`

	expectedLines := []int{1, 1, 1, 2, 2, 2, 3, 3, 3, 3}

	l := NewLexer(input)
	for i, expectedLine := range expectedLines {
		tok := l.NextToken()
		if tok.Line != expectedLine {
			t.Errorf("token[%d] - line wrong. expected=%d, got=%d (token=%s)", i, expectedLine, tok.Line, tok.Literal)
		}
	}
}

// Test complete Agentfile tokenization
func TestLexer_CompleteAgentfile(t *testing.T) {
	input := `# Agentfile: Test-Driven Feature Implementation

NAME implement-feature

INPUT feature_request
INPUT max_iterations DEFAULT 10

AGENT creative FROM agents/creative.md
AGENT devils_advocate FROM agents/devils_advocate.md

GOAL analyze FROM goals/analyze.md USING creative, devils_advocate
GOAL run_tests "Run all tests and capture any failures"

RUN setup USING analyze
LOOP implementation USING run_tests WITHIN $max_iterations`

	l := NewLexer(input)
	tokenCount := 0
	for {
		tok := l.NextToken()
		tokenCount++
		if tok.Type == TokenEOF {
			break
		}
		if tok.Type == TokenIllegal {
			t.Errorf("unexpected illegal token at line %d: %q", tok.Line, tok.Literal)
		}
	}

	// Should have parsed many tokens without error
	if tokenCount < 40 {
		t.Errorf("expected at least 40 tokens, got %d", tokenCount)
	}
}

// Test illegal characters
func TestLexer_IllegalCharacters(t *testing.T) {
	input := `@illegal`

	l := NewLexer(input)
	tok := l.NextToken()
	
	// @illegal should produce an illegal token (@ is not valid)
	if tok.Type != TokenIllegal {
		t.Errorf("expected ILLEGAL for @, got %s with literal %q", tok.Type, tok.Literal)
	}
}

// Test string with unterminated quote
func TestLexer_UnterminatedString(t *testing.T) {
	input := `"unterminated`

	l := NewLexer(input)
	tok := l.NextToken()
	if tok.Type != TokenIllegal {
		t.Errorf("expected ILLEGAL for unterminated string, got %s", tok.Type)
	}
}

// Test column tracking
func TestLexer_ColumnNumbers(t *testing.T) {
	input := `NAME test`

	l := NewLexer(input)
	tok := l.NextToken() // NAME
	if tok.Column != 1 {
		t.Errorf("NAME column wrong. expected=1, got=%d", tok.Column)
	}

	tok = l.NextToken() // test
	if tok.Column != 6 {
		t.Errorf("test column wrong. expected=6, got=%d", tok.Column)
	}
}

func TestLexerRequires(t *testing.T) {
    input := `AGENT critic FROM agents/critic.md REQUIRES "reasoning-heavy"`
    l := NewLexer(input)
    
    expected := []struct {
        typ TokenType
        lit string
    }{
        {TokenAGENT, "AGENT"},
        {TokenIdent, "critic"},
        {TokenFROM, "FROM"},
        {TokenPath, "agents/critic.md"},
        {TokenREQUIRES, "REQUIRES"},
        {TokenString, "reasoning-heavy"},
        {TokenEOF, ""},
    }
    
    for i, exp := range expected {
        tok := l.NextToken()
        t.Logf("Token %d: type=%s lit=%q", i, tok.Type, tok.Literal)
        if tok.Type != exp.typ {
            t.Errorf("token %d: expected type %s, got %s", i, exp.typ, tok.Type)
        }
    }
}

func TestLexerPathWithRequires(t *testing.T) {
    input := "AGENT critic FROM agents/critic.md REQUIRES \"reasoning-heavy\""
    l := NewLexer(input)
    
    for {
        tok := l.NextToken()
        t.Logf("type=%-10s lit=%q", tok.Type, tok.Literal)
        if tok.Type == TokenEOF {
            break
        }
    }
}

func TestLexer_TripleQuotedString(t *testing.T) {
	input := `GOAL test """
First line
Second line
Third line
"""`

	l := NewLexer(input)
	
	// GOAL keyword
	tok := l.NextToken()
	if tok.Type != TokenGOAL {
		t.Fatalf("expected GOAL, got %s", tok.Type)
	}
	
	// identifier
	tok = l.NextToken()
	if tok.Type != TokenIdent || tok.Literal != "test" {
		t.Fatalf("expected ident 'test', got %s %q", tok.Type, tok.Literal)
	}
	
	// triple-quoted string
	tok = l.NextToken()
	if tok.Type != TokenString {
		t.Fatalf("expected TokenString, got %s", tok.Type)
	}
	
	expected := "First line\nSecond line\nThird line"
	if tok.Literal != expected {
		t.Errorf("triple-quoted string wrong.\nexpected: %q\ngot:      %q", expected, tok.Literal)
	}
}

func TestLexer_TripleQuotedStringInline(t *testing.T) {
	// Triple quotes on same line as content
	input := `GOAL test """inline content"""`

	l := NewLexer(input)
	l.NextToken() // GOAL
	l.NextToken() // test
	
	tok := l.NextToken()
	if tok.Type != TokenString {
		t.Fatalf("expected TokenString, got %s", tok.Type)
	}
	
	if tok.Literal != "inline content" {
		t.Errorf("expected %q, got %q", "inline content", tok.Literal)
	}
}

func TestLexer_TripleQuotedStringUnterminated(t *testing.T) {
	input := `GOAL test """
This string never ends
`

	l := NewLexer(input)
	l.NextToken() // GOAL
	l.NextToken() // test
	
	tok := l.NextToken()
	if tok.Type != TokenIllegal {
		t.Fatalf("expected TokenIllegal for unterminated triple-quote, got %s", tok.Type)
	}
}
