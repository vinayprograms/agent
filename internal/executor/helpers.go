package executor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
)

// variableRef matches a $name reference left in a prompt after input and
// output substitution.
var variableRef = regexp.MustCompile(`\$([a-zA-Z_][a-zA-Z0-9_]*)`)

// truncateForLog truncates a string for logging purposes. The cut point
// backs off to the previous rune boundary when maxLen would otherwise land
// inside a multi-byte UTF-8 character, so the result is always valid UTF-8.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// buildStructuredOutputInstruction builds instructions for structured output.
func buildStructuredOutputInstruction(outputs []string) string {
	if len(outputs) == 0 {
		return ""
	}
	return "Return your response as JSON with the following fields: " + strings.Join(outputs, ", ")
}

// parseStructuredOutput parses JSON output into the expected fields. Content
// that is not JSON is not an error: the whole answer stands in for every
// field, which is what a model that ignored the format instruction meant.
func parseStructuredOutput(content string, expectedFields []string) map[string]string {
	// Try to extract JSON from content
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		jsonStr = content
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		// If JSON parsing fails, try to extract fields from plain text
		result := make(map[string]string)
		for _, field := range expectedFields {
			result[field] = content
		}
		return result
	}

	result := make(map[string]string)
	for _, field := range expectedFields {
		if val, ok := raw[field]; ok {
			switch v := val.(type) {
			case string:
				result[field] = v
			default:
				result[field] = jsonStringify(v)
			}
		}
	}
	return result
}

// jsonStringify re-marshals a non-string decoded JSON value back to its JSON
// text, for callers that need every field as a string.
func jsonStringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// extractJSON extracts a JSON object from content that may contain surrounding text.
func extractJSON(content string) string {
	// Find the first { and last } to extract JSON
	start := strings.Index(content, "{")
	if start == -1 {
		return ""
	}

	// Find matching closing brace
	depth := 0
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}
	return ""
}

// interpolate replaces variable placeholders in text.
// Warns about unresolved variables that might indicate Agentfile bugs.
func (e *Executor) interpolate(text string) string {
	// One pass over every $name: inputs first, then goal outputs. A value
	// that itself names a variable is left alone — expanding it would make
	// the result depend on map iteration order.
	var unresolved []string
	text = variableRef.ReplaceAllStringFunc(text, func(match string) string {
		varName := strings.TrimPrefix(match, "$")
		if val, ok := e.inputs[varName]; ok {
			return val
		}
		if val, ok := e.outputs[varName]; ok {
			return val
		}
		unresolved = append(unresolved, varName)
		return match // Leave unresolved variables as-is
	})

	// Warn about unresolved variables (both console and session for replay)
	if len(unresolved) > 0 {
		e.logger.Warn("unresolved variables in prompt (check Agentfile)",
			"variables", unresolved,
			"hint", "ensure prior goals output these variables with -> syntax")
		// Also log to session for replay visibility
		e.logEvent(session.EventWarning, fmt.Sprintf("Unresolved variables: %v (ensure prior goals output these with -> syntax)", unresolved))
	}

	return text
}

func (e *Executor) findGoal(name string) *agentfile.Goal {
	for i := range e.workflow.Goals {
		if e.workflow.Goals[i].Name == name {
			return &e.workflow.Goals[i]
		}
	}
	return nil
}

func (e *Executor) findAgent(name string) *agentfile.Agent {
	for i := range e.workflow.Agents {
		if e.workflow.Agents[i].Name == name {
			return &e.workflow.Agents[i]
		}
	}
	return nil
}
