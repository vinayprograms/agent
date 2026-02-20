package agentfile

import (
	"fmt"
	"strconv"
)

// Parser parses Agentfile tokens into an AST.
type Parser struct {
	l         *Lexer
	curToken  Token
	peekToken Token
	errors    []string
}

// NewParser creates a new parser for the given lexer.
func NewParser(l *Lexer) *Parser {
	p := &Parser{l: l}
	// Read two tokens to initialize curToken and peekToken
	p.nextToken()
	p.nextToken()
	return p
}

// nextToken advances to the next token.
func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

// Parse parses the input and returns the workflow AST.
func (p *Parser) Parse() (*Workflow, error) {
	wf := &Workflow{}

	for p.curToken.Type != TokenEOF {
		switch p.curToken.Type {
		case TokenNewline:
			p.nextToken()
			continue
		case TokenSUPERVISED:
			// Global supervision at the top level
			p.nextToken() // consume SUPERVISED
			wf.Supervised = true
			if p.curToken.Type == TokenHUMAN {
				wf.HumanOnly = true
				p.nextToken()
			}
			p.skipNewline()
		case TokenSECURITY:
			// Security mode: SECURITY default | paranoid | research "scope"
			p.nextToken() // consume SECURITY
			if p.curToken.Type == TokenIdent {
				mode := p.curToken.Literal
				if mode == "default" || mode == "paranoid" {
					wf.SecurityMode = mode
					p.nextToken()
				} else if mode == "research" {
					wf.SecurityMode = mode
					p.nextToken()
					// Research mode requires a scope string
					if p.curToken.Type == TokenString {
						wf.SecurityScope = p.curToken.Literal
						p.nextToken()
					} else {
						return nil, fmt.Errorf("line %d: SECURITY research requires a scope description string", p.curToken.Line)
					}
				} else {
					return nil, fmt.Errorf("line %d: invalid security mode %q, expected 'default', 'paranoid', or 'research'", p.curToken.Line, mode)
				}
			} else {
				return nil, fmt.Errorf("line %d: expected security mode after SECURITY, got %s", p.curToken.Line, p.curToken.Type)
			}
			p.skipNewline()
		case TokenNAME:
			name, err := p.parseNameStatement()
			if err != nil {
				return nil, err
			}
			wf.Name = name
		case TokenINPUT:
			input, err := p.parseInputStatement()
			if err != nil {
				return nil, err
			}
			wf.Inputs = append(wf.Inputs, *input)
		case TokenAGENT:
			agent, err := p.parseAgentStatement()
			if err != nil {
				return nil, err
			}
			wf.Agents = append(wf.Agents, *agent)
		case TokenGOAL:
			goal, err := p.parseGoalStatement()
			if err != nil {
				return nil, err
			}
			wf.Goals = append(wf.Goals, *goal)
		case TokenCONVERGE:
			goal, err := p.parseConvergeStatement()
			if err != nil {
				return nil, err
			}
			wf.Goals = append(wf.Goals, *goal)
		case TokenRUN:
			step, err := p.parseRunStatement()
			if err != nil {
				return nil, err
			}
			wf.Steps = append(wf.Steps, *step)
		case TokenLOOP:
			step, err := p.parseLoopStatement()
			if err != nil {
				return nil, err
			}
			wf.Steps = append(wf.Steps, *step)
		default:
			return nil, fmt.Errorf("line %d: unexpected token %s", p.curToken.Line, p.curToken.Type)
		}
	}

	return wf, nil
}

// parseNameStatement parses: NAME <identifier>
func (p *Parser) parseNameStatement() (string, error) {
	line := p.curToken.Line
	p.nextToken() // consume NAME

	if !p.isIdentifier() {
		return "", fmt.Errorf("line %d: expected identifier after NAME, got %s", line, p.curToken.Type)
	}

	name := p.curToken.Literal
	p.nextToken()
	p.skipNewline()
	return name, nil
}

// parseInputStatement parses: INPUT <identifier> [DEFAULT <value>]
func (p *Parser) parseInputStatement() (*Input, error) {
	line := p.curToken.Line
	p.nextToken() // consume INPUT

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after INPUT, got %s", line, p.curToken.Type)
	}

	input := &Input{
		Name: p.curToken.Literal,
		Line: line,
	}
	p.nextToken()

	// Check for optional DEFAULT
	if p.curToken.Type == TokenDEFAULT {
		p.nextToken() // consume DEFAULT
		if !p.isValue() {
			return nil, fmt.Errorf("line %d: expected value after DEFAULT, got %s", line, p.curToken.Type)
		}
		val := p.curToken.Literal
		input.Default = &val
		p.nextToken()
	}

	p.skipNewline()
	return input, nil
}

// parseAgentStatement parses: AGENT <identifier> (FROM <path> | <string>) [-> outputs] [REQUIRES <string>] [SUPERVISED [HUMAN] | UNSUPERVISED]
func (p *Parser) parseAgentStatement() (*Agent, error) {
	line := p.curToken.Line
	p.nextToken() // consume AGENT

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after AGENT, got %s", line, p.curToken.Type)
	}

	agent := &Agent{
		Name: p.curToken.Literal,
		Line: line,
	}
	p.nextToken()

	// Either string prompt or FROM path
	if p.curToken.Type == TokenString {
		agent.Prompt = p.curToken.Literal
		p.nextToken()
	} else if p.curToken.Type == TokenFROM {
		p.nextToken() // consume FROM
		if p.curToken.Type != TokenPath {
			return nil, fmt.Errorf("line %d: expected path after FROM, got %s", line, p.curToken.Type)
		}
		agent.FromPath = p.curToken.Literal
		p.nextToken()
	} else {
		return nil, fmt.Errorf("line %d: expected string or FROM after AGENT name, got %s", line, p.curToken.Type)
	}

	// Check for optional -> outputs
	if p.curToken.Type == TokenArrow {
		outputs, err := p.parseOutputList()
		if err != nil {
			return nil, err
		}
		agent.Outputs = outputs
	}

	// Check for optional REQUIRES clause
	if p.curToken.Type == TokenREQUIRES {
		p.nextToken() // consume REQUIRES
		if p.curToken.Type != TokenString {
			return nil, fmt.Errorf("line %d: expected string after REQUIRES, got %s", line, p.curToken.Type)
		}
		agent.Requires = p.curToken.Literal
		p.nextToken()
	}

	// Check for optional supervision modifiers
	if p.curToken.Type == TokenSUPERVISED {
		agent.Supervision = SupervisionEnabled
		p.nextToken()
		if p.curToken.Type == TokenHUMAN {
			agent.HumanOnly = true
			p.nextToken()
		}
	} else if p.curToken.Type == TokenUNSUPERVISED {
		agent.Supervision = SupervisionDisabled
		p.nextToken()
	}

	p.skipNewline()
	return agent, nil
}

// parseGoalStatement parses: GOAL <identifier> (<string> | FROM <path>) [-> outputs] [USING <identifier_list>] [SUPERVISED [HUMAN] | UNSUPERVISED]
func (p *Parser) parseGoalStatement() (*Goal, error) {
	line := p.curToken.Line
	p.nextToken() // consume GOAL

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after GOAL, got %s", line, p.curToken.Type)
	}

	goal := &Goal{
		Name: p.curToken.Literal,
		Line: line,
	}
	p.nextToken()

	// Either string or FROM path
	if p.curToken.Type == TokenString {
		goal.Outcome = p.curToken.Literal
		p.nextToken()
	} else if p.curToken.Type == TokenFROM {
		p.nextToken() // consume FROM
		if p.curToken.Type != TokenPath {
			return nil, fmt.Errorf("line %d: expected path after FROM, got %s", line, p.curToken.Type)
		}
		goal.FromPath = p.curToken.Literal
		p.nextToken()
	} else {
		return nil, fmt.Errorf("line %d: expected string or FROM after GOAL name, got %s", line, p.curToken.Type)
	}

	// Check for optional -> outputs
	if p.curToken.Type == TokenArrow {
		outputs, err := p.parseOutputList()
		if err != nil {
			return nil, err
		}
		goal.Outputs = outputs
	}

	// Check for optional USING clause
	if p.curToken.Type == TokenUSING {
		agents, err := p.parseIdentifierList()
		if err != nil {
			return nil, err
		}
		goal.UsingAgent = agents
	}

	// Check for optional supervision modifiers
	if p.curToken.Type == TokenSUPERVISED {
		goal.Supervision = SupervisionEnabled
		p.nextToken()
		if p.curToken.Type == TokenHUMAN {
			goal.HumanOnly = true
			p.nextToken()
		}
	} else if p.curToken.Type == TokenUNSUPERVISED {
		goal.Supervision = SupervisionDisabled
		p.nextToken()
	}

	p.skipNewline()
	return goal, nil
}

// parseConvergeStatement parses: CONVERGE <identifier> (<string> | FROM <path>) [-> outputs] [USING <identifier_list>] WITHIN (<number> | <variable>) [SUPERVISED [HUMAN] | UNSUPERVISED]
func (p *Parser) parseConvergeStatement() (*Goal, error) {
	line := p.curToken.Line
	p.nextToken() // consume CONVERGE

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after CONVERGE, got %s", line, p.curToken.Type)
	}

	goal := &Goal{
		Name:       p.curToken.Literal,
		IsConverge: true,
		Line:       line,
	}
	p.nextToken()

	// Either string or FROM path
	if p.curToken.Type == TokenString {
		goal.Outcome = p.curToken.Literal
		p.nextToken()
	} else if p.curToken.Type == TokenFROM {
		p.nextToken() // consume FROM
		if p.curToken.Type != TokenPath {
			return nil, fmt.Errorf("line %d: expected path after FROM, got %s", line, p.curToken.Type)
		}
		goal.FromPath = p.curToken.Literal
		p.nextToken()
	} else {
		return nil, fmt.Errorf("line %d: expected string or FROM after CONVERGE name, got %s", line, p.curToken.Type)
	}

	// Check for optional -> outputs (right after description)
	if p.curToken.Type == TokenArrow {
		outputs, err := p.parseOutputList()
		if err != nil {
			return nil, err
		}
		goal.Outputs = outputs
	}

	// Check for optional USING clause (before WITHIN)
	if p.curToken.Type == TokenUSING {
		agents, err := p.parseIdentifierList()
		if err != nil {
			return nil, err
		}
		goal.UsingAgent = agents
	}

	// WITHIN is mandatory for CONVERGE
	if p.curToken.Type != TokenWITHIN {
		return nil, fmt.Errorf("line %d: CONVERGE requires WITHIN clause, got %s", line, p.curToken.Type)
	}
	p.nextToken() // consume WITHIN

	// Either number or variable
	if p.curToken.Type == TokenNumber {
		val, _ := strconv.Atoi(p.curToken.Literal)
		goal.WithinLimit = &val
		p.nextToken()
	} else if p.curToken.Type == TokenVar {
		goal.WithinVar = p.curToken.Literal
		p.nextToken()
	} else {
		return nil, fmt.Errorf("line %d: expected number or variable after WITHIN, got %s", line, p.curToken.Type)
	}

	// Check for optional supervision modifiers
	if p.curToken.Type == TokenSUPERVISED {
		goal.Supervision = SupervisionEnabled
		p.nextToken()
		if p.curToken.Type == TokenHUMAN {
			goal.HumanOnly = true
			p.nextToken()
		}
	} else if p.curToken.Type == TokenUNSUPERVISED {
		goal.Supervision = SupervisionDisabled
		p.nextToken()
	}

	p.skipNewline()
	return goal, nil
}

// parseRunStatement parses: RUN <identifier> USING <identifier_list> [SUPERVISED [HUMAN] | UNSUPERVISED]
func (p *Parser) parseRunStatement() (*Step, error) {
	line := p.curToken.Line
	p.nextToken() // consume RUN

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after RUN, got %s", line, p.curToken.Type)
	}

	step := &Step{
		Type: StepRUN,
		Name: p.curToken.Literal,
		Line: line,
	}
	p.nextToken()

	if p.curToken.Type != TokenUSING {
		return nil, fmt.Errorf("line %d: expected USING after RUN name, got %s", line, p.curToken.Type)
	}

	goals, err := p.parseIdentifierList()
	if err != nil {
		return nil, err
	}
	step.UsingGoals = goals

	// Check for optional supervision modifiers
	if p.curToken.Type == TokenSUPERVISED {
		step.Supervision = SupervisionEnabled
		p.nextToken()
		if p.curToken.Type == TokenHUMAN {
			step.HumanOnly = true
			p.nextToken()
		}
	} else if p.curToken.Type == TokenUNSUPERVISED {
		step.Supervision = SupervisionDisabled
		p.nextToken()
	}

	p.skipNewline()
	return step, nil
}

// parseLoopStatement parses: LOOP <identifier> USING <identifier_list> WITHIN (<number> | <variable>) [SUPERVISED [HUMAN] | UNSUPERVISED]
func (p *Parser) parseLoopStatement() (*Step, error) {
	line := p.curToken.Line
	p.nextToken() // consume LOOP

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after LOOP, got %s", line, p.curToken.Type)
	}

	step := &Step{
		Type: StepLOOP,
		Name: p.curToken.Literal,
		Line: line,
	}
	p.nextToken()

	if p.curToken.Type != TokenUSING {
		return nil, fmt.Errorf("line %d: expected USING after LOOP name, got %s", line, p.curToken.Type)
	}

	goals, err := p.parseIdentifierList()
	if err != nil {
		return nil, err
	}
	step.UsingGoals = goals

	if p.curToken.Type != TokenWITHIN {
		return nil, fmt.Errorf("line %d: expected WITHIN after USING list, got %s", line, p.curToken.Type)
	}
	p.nextToken() // consume WITHIN

	// Either number or variable
	if p.curToken.Type == TokenNumber {
		val, _ := strconv.Atoi(p.curToken.Literal)
		step.WithinLimit = &val
		p.nextToken()
	} else if p.curToken.Type == TokenVar {
		step.WithinVar = p.curToken.Literal
		p.nextToken()
	} else {
		return nil, fmt.Errorf("line %d: expected number or variable after WITHIN, got %s", line, p.curToken.Type)
	}

	// Check for optional supervision modifiers
	if p.curToken.Type == TokenSUPERVISED {
		step.Supervision = SupervisionEnabled
		p.nextToken()
		if p.curToken.Type == TokenHUMAN {
			step.HumanOnly = true
			p.nextToken()
		}
	} else if p.curToken.Type == TokenUNSUPERVISED {
		step.Supervision = SupervisionDisabled
		p.nextToken()
	}

	p.skipNewline()
	return step, nil
}

// parseIdentifierList parses: USING <identifier> [, <identifier>]*
func (p *Parser) parseIdentifierList() ([]string, error) {
	line := p.curToken.Line
	p.nextToken() // consume USING

	var idents []string

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after USING, got %s", line, p.curToken.Type)
	}
	idents = append(idents, p.curToken.Literal)
	p.nextToken()

	// Continue while we see commas
	for p.curToken.Type == TokenComma {
		p.nextToken() // consume comma
		if !p.isIdentifier() {
			return nil, fmt.Errorf("line %d: expected identifier after comma, got %s", line, p.curToken.Type)
		}
		idents = append(idents, p.curToken.Literal)
		p.nextToken()
	}

	return idents, nil
}

// parseOutputList parses: -> <identifier> [, <identifier>]*
func (p *Parser) parseOutputList() ([]string, error) {
	line := p.curToken.Line
	p.nextToken() // consume ->

	var outputs []string

	if !p.isIdentifier() {
		return nil, fmt.Errorf("line %d: expected identifier after ->, got %s", line, p.curToken.Type)
	}
	outputs = append(outputs, p.curToken.Literal)
	p.nextToken()

	// Continue while we see commas
	for p.curToken.Type == TokenComma {
		p.nextToken() // consume comma
		if !p.isIdentifier() {
			return nil, fmt.Errorf("line %d: expected identifier after comma, got %s", line, p.curToken.Type)
		}
		outputs = append(outputs, p.curToken.Literal)
		p.nextToken()
	}

	return outputs, nil
}

// isIdentifier returns true if current token is an identifier (not a keyword used as value).
func (p *Parser) isIdentifier() bool {
	return p.curToken.Type == TokenIdent
}

// isValue returns true if current token can be a value (number, string, identifier).
func (p *Parser) isValue() bool {
	return p.curToken.Type == TokenNumber ||
		p.curToken.Type == TokenString ||
		p.curToken.Type == TokenIdent
}

// skipNewline skips newline tokens.
func (p *Parser) skipNewline() {
	for p.curToken.Type == TokenNewline {
		p.nextToken()
	}
}
