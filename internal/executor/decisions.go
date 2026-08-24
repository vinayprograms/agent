package executor

import (
	"github.com/vinayprograms/agentkit/llm"
)

// resolveOutputVars returns a goal's declared `-> outputs` fields, preferring
// the structured emit_outputs tool call (decision) when the model made one,
// and falling back to scraping JSON out of output (parseStructuredOutput)
// otherwise — the live path for providers that can't force tool_choice.
// viaTool reports which path was used, for logging.
func resolveOutputVars(output string, decision *llm.ToolCallResponse, fields []string) (vars map[string]string, viaTool bool) {
	if decision != nil && decision.Name == emitOutputsToolName {
		return decisionArgsToVars(decision.Args, fields), true
	}
	return parseStructuredOutput(output, fields), false
}

// convergedToolName is the tool a convergence-deciding agent (the single
// agent in single-agent CONVERGE, or the last agent in a CONVERGE pipeline)
// calls to report that the work has converged. Offering this tool replaces
// the old prose "CONVERGED" marker as the primary channel; splitConvergence
// remains as the lenient fallback for providers that ignore the tool (e.g.
// Ollama Cloud, which cannot force tool_choice).
const convergedToolName = "converged"

// convergedTool is the tool definition offered to a convergence-deciding
// agent.
var convergedTool = llm.ToolDef{
	Name:        convergedToolName,
	Description: "Call this when you judge the work converged/complete and no further iteration is needed. Do not write the word CONVERGED as text — call this tool instead.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "One-sentence reason the work has converged.",
			},
		},
		"required": []string{"reason"},
	},
}

// convergedToolFor builds the converged tool for a CONVERGE goal, folding in
// the goal's declared `-> outputs` fields (if any) as optional properties
// alongside reason. Bundling them into one tool call avoids a second,
// separate emit_outputs decision on the same turn: a convergence-deciding
// agent only gets one decision call before the phase ends (see
// executePhase's doc comment), so a CONVERGE goal with declared outputs
// reports them the moment it reports convergence, not on a later turn.
func convergedToolFor(outputFields []string) llm.ToolDef {
	if len(outputFields) == 0 {
		return convergedTool
	}
	props := map[string]any{
		"reason": map[string]any{
			"type":        "string",
			"description": "One-sentence reason the work has converged.",
		},
	}
	for _, f := range outputFields {
		props[f] = map[string]any{"type": "string"}
	}
	required := append([]string{"reason"}, outputFields...)
	return llm.ToolDef{
		Name:        convergedToolName,
		Description: "Call this when you judge the work converged/complete: report why, and your declared output fields. Do not write the word CONVERGED as text — call this tool instead.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
	}
}

// convergedInstruction is appended to the prompt of whichever agent is
// actually offered convergedTool (the single agent in single-agent
// CONVERGE, or the last agent in a pipeline) so a provider that can't force
// tool_choice (Ollama Cloud) is still nudged toward the structured path.
// Only that agent should see this — earlier pipeline agents don't have the
// tool, so telling them to call it would be misleading.
const convergedInstruction = "\n\nWhen you judge the work complete, call the `converged` tool with a one-sentence reason. Do NOT write the word CONVERGED as text — call the tool."

// emitOutputsToolName is the tool a goal with declared `-> outputs` calls to
// report its structured fields, replacing the old "return JSON in prose"
// convention that parseStructuredOutput had to scrape back out.
const emitOutputsToolName = "emit_outputs"

// emitOutputsTool builds the emit_outputs tool definition from a goal's
// declared output field names. Each field is a plain string parameter.
func emitOutputsTool(fields []string) llm.ToolDef {
	props := make(map[string]any, len(fields))
	for _, f := range fields {
		props[f] = map[string]any{"type": "string"}
	}
	return llm.ToolDef{
		Name:        emitOutputsToolName,
		Description: "Call this to return your declared output fields, instead of writing JSON in your reply.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   fields,
		},
	}
}

// emitOutputsInstruction is appended to the goal description whenever the
// goal declares `-> outputs`, nudging providers that can't force
// tool_choice toward the structured path.
const emitOutputsInstruction = "\n\nReturn these fields by calling the `emit_outputs` tool, not by writing JSON in your reply."

// decisionArgsToVars converts an emit_outputs tool call's Args into the
// map[string]string shape the rest of the executor expects (matching
// parseStructuredOutput's contract: non-string values are re-marshaled to
// their JSON text).
func decisionArgsToVars(args map[string]any, fields []string) map[string]string {
	result := make(map[string]string, len(fields))
	for _, f := range fields {
		if v, ok := args[f]; ok {
			if s, ok := v.(string); ok {
				result[f] = s
			} else {
				result[f] = jsonStringify(v)
			}
		}
	}
	return result
}
