package agentfile

import (
	"strings"
)

// Lexer tokenizes Agentfile input.
type Lexer struct {
	input        string
	position     int  // current position in input (points to current char)
	readPosition int  // current reading position in input (after current char)
	ch           byte // current char under examination
	line         int  // current line number (1-indexed)
	column       int  // current column number (1-indexed)
	startColumn  int  // column at start of current token
	afterFrom    bool // true if previous token was FROM (for path parsing)
}

// NewLexer creates a new lexer for the given input.
func NewLexer(input string) *Lexer {
	l := &Lexer{
		input:  input,
		line:   1,
		column: 0,
	}
	l.readChar()
	return l
}

// readChar reads the next character and advances the position.
func (l *Lexer) readChar() {
	if l.readPosition >= len(l.input) {
		l.ch = 0
	} else {
		l.ch = l.input[l.readPosition]
	}
	l.position = l.readPosition
	l.readPosition++
	l.column++

	// Handle newline - column resets on next char
	if l.ch == '\n' {
		// Newline will be processed, line increments after token is created
	}
}

// peekChar returns the next character without advancing.
func (l *Lexer) peekChar() byte {
	if l.readPosition >= len(l.input) {
		return 0
	}
	return l.input[l.readPosition]
}

// NextToken returns the next token from the input.
func (l *Lexer) NextToken() Token {
	var tok Token

	l.skipWhitespace()

	// Skip comment-only lines and pure empty lines
	for l.ch == '#' || (l.ch == '\n' && l.isEmptyLineAhead()) {
		if l.ch == '#' {
			l.skipComment()
			l.skipWhitespace()
		} else if l.ch == '\n' && l.isEmptyLineAhead() {
			l.readChar()
			l.line++
			l.column = 1
			l.skipWhitespace()
		} else {
			break
		}
	}

	l.startColumn = l.column

	switch l.ch {
	case 0:
		tok = l.newToken(TokenEOF, "")
	case '\n':
		tok = l.newToken(TokenNewline, "\n")
		l.readChar()
		l.line++
		l.column = 1
	case ',':
		tok = l.newToken(TokenComma, ",")
		l.readChar()
	case '-':
		if l.peekChar() == '>' {
			l.readChar() // consume -
			l.readChar() // consume >
			tok = l.newToken(TokenArrow, "->")
		} else {
			tok = l.newToken(TokenIllegal, string(l.ch))
			l.readChar()
		}
	case '"':
		tok = l.readString()
	case '$':
		tok = l.readVariable()
	default:
		if l.afterFrom {
			// After FROM keyword, read a path
			tok = l.readPath()
			l.afterFrom = false
		} else if isLetter(l.ch) || l.ch == '_' {
			tok = l.readIdentifier()
			// Check if it's FROM to set path parsing mode
			if tok.Type == TokenFROM {
				l.afterFrom = true
			}
		} else if isDigit(l.ch) {
			tok = l.readNumber()
		} else {
			tok = l.newToken(TokenIllegal, string(l.ch))
			l.readChar()
		}
	}

	return tok
}

// newToken creates a new token with the current line/column.
func (l *Lexer) newToken(tokenType TokenType, literal string) Token {
	return Token{
		Type:    tokenType,
		Literal: literal,
		Line:    l.line,
		Column:  l.startColumn,
	}
}

// skipWhitespace skips spaces and tabs (but not newlines).
func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\r' {
		l.readChar()
	}
}

// skipComment skips from # to end of line.
func (l *Lexer) skipComment() {
	for l.ch != '\n' && l.ch != 0 {
		l.readChar()
	}
}

// isEmptyLineAhead returns true if we're at a newline and the next line is empty or whitespace-only.
func (l *Lexer) isEmptyLineAhead() bool {
	if l.ch != '\n' {
		return false
	}
	// Look ahead to see if next line is empty
	pos := l.readPosition
	for pos < len(l.input) {
		ch := l.input[pos]
		if ch == '\n' {
			return true // next line is empty
		}
		if ch == '#' {
			return true // next line is comment-only, treat as empty
		}
		if ch != ' ' && ch != '\t' && ch != '\r' {
			return false // next line has content
		}
		pos++
	}
	return true // EOF counts as empty
}

// readIdentifier reads an identifier or keyword.
func (l *Lexer) readIdentifier() Token {
	l.startColumn = l.column
	position := l.position
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' || l.ch == '-' {
		l.readChar()
	}
	literal := l.input[position:l.position]
	tokenType := LookupIdent(literal)
	return Token{
		Type:    tokenType,
		Literal: literal,
		Line:    l.line,
		Column:  l.startColumn,
	}
}

// readNumber reads a number literal.
func (l *Lexer) readNumber() Token {
	l.startColumn = l.column
	position := l.position
	for isDigit(l.ch) {
		l.readChar()
	}
	return Token{
		Type:    TokenNumber,
		Literal: l.input[position:l.position],
		Line:    l.line,
		Column:  l.startColumn,
	}
}

// readString reads a quoted string with escape sequences.
func (l *Lexer) readString() Token {
	l.startColumn = l.column
	var sb strings.Builder

	l.readChar() // skip opening quote

	for l.ch != '"' && l.ch != 0 && l.ch != '\n' {
		if l.ch == '\\' {
			l.readChar() // skip backslash
			switch l.ch {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(l.ch)
			}
		} else {
			sb.WriteByte(l.ch)
		}
		l.readChar()
	}

	if l.ch != '"' {
		return Token{
			Type:    TokenIllegal,
			Literal: "unterminated string",
			Line:    l.line,
			Column:  l.startColumn,
		}
	}

	l.readChar() // skip closing quote

	return Token{
		Type:    TokenString,
		Literal: sb.String(),
		Line:    l.line,
		Column:  l.startColumn,
	}
}

// readVariable reads a variable ($identifier).
func (l *Lexer) readVariable() Token {
	l.startColumn = l.column
	l.readChar() // skip $

	position := l.position
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}

	return Token{
		Type:    TokenVar,
		Literal: l.input[position:l.position],
		Line:    l.line,
		Column:  l.startColumn,
	}
}

// readPath reads a file path (used after FROM).
func (l *Lexer) readPath() Token {
	l.skipWhitespace()
	l.startColumn = l.column
	position := l.position

	// Path continues until whitespace, newline, or special chars
	for l.ch != ' ' && l.ch != '\t' && l.ch != '\n' && l.ch != '\r' && l.ch != 0 && l.ch != '#' {
		l.readChar()
	}

	return Token{
		Type:    TokenPath,
		Literal: l.input[position:l.position],
		Line:    l.line,
		Column:  l.startColumn,
	}
}

// isLetter returns true if the byte is a letter.
func isLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

// isIdentChar returns true if the byte can be part of an identifier (after first char).
func isIdentChar(ch byte) bool {
	return isLetter(ch) || isDigit(ch) || ch == '_' || ch == '-'
}

// isDigit returns true if the byte is a digit.
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
