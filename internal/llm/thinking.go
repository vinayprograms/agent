package llm

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ThinkingLevel represents the thinking/reasoning effort level.
type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
	ThinkingAuto   ThinkingLevel = "auto"
)

// ThinkingConfig holds thinking configuration.
type ThinkingConfig struct {
	// Level: "auto", "off", "low", "medium", "high"
	// Auto uses heuristic classifier to determine level per-request.
	Level ThinkingLevel
	
	// BudgetTokens for Anthropic extended thinking (optional, 0 = provider default)
	BudgetTokens int64
}

// InferThinkingLevel analyzes the request and returns an appropriate thinking level.
// This is a zero-cost heuristic classifier - no LLM calls, just pattern matching.
func InferThinkingLevel(messages []Message, tools []ToolDef) ThinkingLevel {
	text := extractUserContent(messages)
	textLen := utf8.RuneCountInString(text)
	toolCount := len(tools)
	
	// Check for high complexity indicators
	if detectHighComplexity(text, textLen, toolCount) {
		return ThinkingHigh
	}
	
	// Check for medium complexity indicators
	if detectMediumComplexity(text, textLen, toolCount) {
		return ThinkingMedium
	}
	
	// Check for low complexity that still benefits from some reasoning
	if detectLowComplexity(text, textLen, toolCount) {
		return ThinkingLow
	}
	
	// Simple queries - no thinking needed
	return ThinkingOff
}

// extractUserContent extracts all user message content for analysis.
func extractUserContent(messages []Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "user" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, " ")
}

// detectHighComplexity checks for patterns that indicate complex reasoning needs.
func detectHighComplexity(text string, textLen, toolCount int) bool {
	lower := strings.ToLower(text)
	
	// Math and logic patterns
	mathPatterns := []string{
		"prove", "derive", "calculate", "solve for",
		"equation", "theorem", "formula",
		"probability", "statistics",
	}
	for _, p := range mathPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	
	// Contains mathematical expressions
	if containsMathExpression(text) {
		return true
	}
	
	// Architecture and design patterns
	archPatterns := []string{
		"architect", "design system", "system design",
		"trade-off", "tradeoff", "trade off",
		"compare and contrast", "pros and cons",
		"security analysis", "threat model",
		"debug", "why is", "root cause",
	}
	for _, p := range archPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	
	// Very long context with constraints
	if textLen > 3000 {
		return true
	}
	
	// Many tools available (complex decision space)
	if toolCount > 10 {
		return true
	}
	
	return false
}

// detectMediumComplexity checks for moderate complexity patterns.
func detectMediumComplexity(text string, textLen, toolCount int) bool {
	lower := strings.ToLower(text)
	
	// Planning and analysis patterns
	planPatterns := []string{
		"plan", "strategy", "approach",
		"analyze", "analyse", "review",
		"implement", "create", "build", "develop",
		"refactor", "optimize", "improve",
		"explain how", "explain why",
		"step by step", "steps to",
	}
	for _, p := range planPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	
	// Code-related patterns
	codePatterns := []string{
		"function", "class", "method",
		"algorithm", "data structure",
		"api", "endpoint", "schema",
		"test", "unit test", "integration",
	}
	for _, p := range codePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	
	// Moderate context length
	if textLen > 1000 {
		return true
	}
	
	// Multiple tools (some reasoning about which to use)
	if toolCount > 5 {
		return true
	}
	
	return false
}

// detectLowComplexity checks for low but non-trivial complexity.
func detectLowComplexity(text string, textLen, toolCount int) bool {
	lower := strings.ToLower(text)
	
	// Some thought required
	lowPatterns := []string{
		"how to", "what is the best",
		"should i", "would you recommend",
		"difference between",
		"summarize", "summary",
		"list", "enumerate",
	}
	for _, p := range lowPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	
	// Has some tools
	if toolCount > 2 {
		return true
	}
	
	// Moderate length
	if textLen > 300 {
		return true
	}
	
	return false
}

// containsMathExpression checks for mathematical expressions.
var mathExpressionRegex = regexp.MustCompile(`[\d\s]*[+\-*/^=<>]+[\d\s]*[+\-*/^=<>]*[\d\s]*`)
var fractionRegex = regexp.MustCompile(`\d+/\d+`)
var exponentRegex = regexp.MustCompile(`\d+\^\d+|\d+\*\*\d+`)

func containsMathExpression(text string) bool {
	// Check for fractions
	if fractionRegex.MatchString(text) {
		return true
	}
	
	// Check for exponents
	if exponentRegex.MatchString(text) {
		return true
	}
	
	// Check for expressions with operators
	if mathExpressionRegex.MatchString(text) {
		// Verify it's not just comparison operators in prose
		matches := mathExpressionRegex.FindAllString(text, -1)
		for _, m := range matches {
			// Must have at least one digit and one operator
			hasDigit := strings.ContainsAny(m, "0123456789")
			hasOp := strings.ContainsAny(m, "+-*/^")
			if hasDigit && hasOp && len(strings.TrimSpace(m)) > 2 {
				return true
			}
		}
	}
	
	return false
}

// ResolveThinkingLevel resolves the thinking level for a request.
// If config is Auto, it uses the heuristic classifier.
func ResolveThinkingLevel(config ThinkingConfig, messages []Message, tools []ToolDef) ThinkingLevel {
	if config.Level == ThinkingAuto || config.Level == "" {
		return InferThinkingLevel(messages, tools)
	}
	return config.Level
}

// ThinkingLevelToBool converts a thinking level to a boolean for providers that only support on/off.
func ThinkingLevelToBool(level ThinkingLevel) bool {
	return level != ThinkingOff && level != ""
}

// ThinkingLevelToAnthropicBudget converts a thinking level to Anthropic budget tokens.
// Returns 0 for off, or a reasonable default for each level.
func ThinkingLevelToAnthropicBudget(level ThinkingLevel, configBudget int64) int64 {
	if configBudget > 0 {
		return configBudget
	}
	
	switch level {
	case ThinkingHigh:
		return 16000 // ~4 pages of reasoning
	case ThinkingMedium:
		return 8000
	case ThinkingLow:
		return 4000
	default:
		return 0
	}
}
