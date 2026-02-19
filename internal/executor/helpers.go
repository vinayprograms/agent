// Utility functions for the executor.
package executor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
)

// truncateForLog truncates a string for logging purposes.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// buildStructuredOutputInstruction builds instructions for structured output.
func buildStructuredOutputInstruction(outputs []string) string {
	if len(outputs) == 0 {
		return ""
	}
	return "Return your response as JSON with the following fields: " + strings.Join(outputs, ", ")
}

// parseStructuredOutput parses JSON output into expected fields.
func parseStructuredOutput(content string, expectedFields []string) (map[string]string, error) {
	// Try to extract JSON from content
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		jsonStr = content
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		// If JSON parsing fails, try to extract fields from plain text
		result := make(map[string]string)
		for _, field := range expectedFields {
			result[field] = content
		}
		return result, nil
	}

	result := make(map[string]string)
	for _, field := range expectedFields {
		if val, ok := raw[field]; ok {
			switch v := val.(type) {
			case string:
				result[field] = v
			default:
				jsonBytes, _ := json.Marshal(v)
				result[field] = string(jsonBytes)
			}
		}
	}
	return result, nil
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
	// Replace input variables
	for name, value := range e.inputs {
		text = strings.ReplaceAll(text, "$"+name, value)
	}

	// Replace goal output variables
	for name, value := range e.outputs {
		text = strings.ReplaceAll(text, "$"+name, value)
	}

	// Track unresolved variables for warning
	var unresolved []string

	// Handle any remaining $var patterns
	re := regexp.MustCompile(`\$([a-zA-Z_][a-zA-Z0-9_]*)`)
	text = re.ReplaceAllStringFunc(text, func(match string) string {
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
		e.logger.Warn("unresolved variables in prompt (check Agentfile)", map[string]interface{}{
			"variables": unresolved,
			"hint":      "ensure prior goals output these variables with -> syntax",
		})
		// Also log to session for replay visibility
		e.logEvent(session.EventWarning, fmt.Sprintf("Unresolved variables: %v (ensure prior goals output these with -> syntax)", unresolved))
	}

	return text
}

// findGoal finds a goal by name.
func (e *Executor) findGoal(name string) *agentfile.Goal {
	for i := range e.workflow.Goals {
		if e.workflow.Goals[i].Name == name {
			return &e.workflow.Goals[i]
		}
	}
	return nil
}

// findAgent finds an agent by name.
func (e *Executor) findAgent(name string) *agentfile.Agent {
	for i := range e.workflow.Agents {
		if e.workflow.Agents[i].Name == name {
			return &e.workflow.Agents[i]
		}
	}
	return nil
}
