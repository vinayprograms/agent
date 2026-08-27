package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
)

// contentPreview computes a non-debug-safe summary of a piece of content
// that must not be logged in full outside debug mode: a short truncated
// preview, its full byte size, and a short hash of the full content. The
// hash lets forensic tooling confirm two truncated previews came from the
// same underlying output (or diff against a full copy captured elsewhere)
// without the runtime ever writing the full text to the non-debug JSONL.
const previewLen = 300

func contentPreview(s string) (preview string, size int, hash string) {
	if s == "" {
		return "", 0, ""
	}
	sum := sha256.Sum256([]byte(s))
	return truncateForLog(s, previewLen), len(s), hex.EncodeToString(sum[:])[:16]
}

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

// mergeAgentOutputs fills a goal's declared outputs from the outputs its
// sub-agents declared, for the fields the goal's own response did not
// supply.
//
// `GOAL scan "..." -> scan_results USING scanner` where
// `AGENT scanner "..." -> vulnerabilities, severity` means scan_results
// holds what the scanner produced. The scanner emits its own field names,
// so looking for a "scan_results" key in the goal's text never finds one —
// the goal was reported empty_output while its agents had done the work.
//
// Two ways a field gets filled, in order:
//
//   - by name: a declared goal output matching an agent's field name takes
//     that value directly, so `GOAL x -> severity USING scanner` works.
//   - by nesting: any goal output still empty is populated with the agents'
//     outputs as a JSON object, which is the `-> scan_results` case. With
//     one agent that is its fields; with several it is keyed by agent name,
//     so nothing is silently dropped when siblings share a field name.
//
// Values the goal itself produced always win: a goal that emitted its own
// outputs has said what it meant, and agent outputs are the fallback.
func mergeAgentOutputs(vars map[string]string, declared []string, agentVars map[string]map[string]string) map[string]string {
	if len(declared) == 0 || len(agentVars) == 0 {
		return vars
	}
	if vars == nil {
		vars = make(map[string]string, len(declared))
	}

	for _, field := range declared {
		if strings.TrimSpace(vars[field]) != "" {
			continue // the goal supplied this one
		}
		for _, av := range agentVars {
			if v, ok := av[field]; ok && strings.TrimSpace(v) != "" {
				vars[field] = v
				break
			}
		}
	}

	// Whatever is still empty gets the agents' outputs nested under it.
	var nested string
	for _, field := range declared {
		if strings.TrimSpace(vars[field]) != "" {
			continue
		}
		if nested == "" {
			if len(agentVars) == 1 {
				for _, av := range agentVars {
					nested = jsonStringify(av)
				}
			} else {
				nested = jsonStringify(agentVars)
			}
		}
		vars[field] = nested
	}
	return vars
}
