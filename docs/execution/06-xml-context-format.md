# XML Context Format

The headless agent uses XML-structured prompts to communicate with LLMs. This format provides clear boundaries between system context and LLM-generated content, avoiding confusion when markdown headers collide.

## Why XML?

When passing context from previous goals to the next goal, the agent needs to clearly separate:
1. **Prior goal outputs** (what previous goals produced)
2. **Current goal** (what the LLM should do now)
3. **Corrections** (supervisor feedback, if any)

Previously, this was done with markdown headers like `## Context from Previous Goals`. However, LLMs often use markdown headers in their responses, creating ambiguous nesting.

XML provides unambiguous structural boundaries while allowing markdown content inside the tags.

## Format

### Main Workflow Goals

```xml
<workflow name="recipe-creator">

<context>
  <goal id="brainstorm">
Here are 3 distinct dishes:

## 1. Classic South Indian Coconut Chutney
A fresh, vibrant accompaniment...

## 2. Kerala-Style Chammanthi
A bold, concentrated chutney...
  </goal>
</context>

<current-goal id="select">
Choose the best recipe based on: ingredient usage, flavor balance, and preparation simplicity.
</current-goal>

</workflow>
```

### First Goal (No Prior Context)

```xml
<workflow name="recipe-creator">

<current-goal id="brainstorm">
Brainstorm 3 possible dishes using coconut, curry leaves, green chillies.
</current-goal>

</workflow>
```

### Multiple Prior Goals

```xml
<workflow name="essay-writer">

<context>
  <goal id="outline">
# Essay Outline
1. Introduction...
  </goal>

  <goal id="draft">
# The Essay

Introduction paragraph...
  </goal>
</context>

<current-goal id="polish">
Review and improve the essay.
</current-goal>

</workflow>
```

### With Supervisor Correction

When the execution supervisor requests a course correction:

```xml
<workflow name="code-review">

<context>
  <goal id="scan">
Found 15 Go files in /src/...
  </goal>
</context>

<current-goal id="review">
Review code for bugs and security issues.
</current-goal>

<correction source="supervisor">
Focus specifically on SQL injection in /src/db/. Prioritize this over style issues.
</correction>

</workflow>
```

### Dynamic Sub-Agent Tasks

When the orchestrator spawns a dynamic sub-agent:

```xml
<task role="quantum-historian" parent-goal="research">
Research the history of quantum computing from 1980 to present.
Focus on key milestones and breakthrough papers.
</task>
```

With correction:

```xml
<task role="researcher" parent-goal="analyze">
Analyze the data thoroughly.
</task>

<correction source="supervisor">
Focus on outliers and anomalies.
</correction>
```

### Parallel Agent Outputs

When multiple agents work on the same goal, their outputs are labeled:

```xml
<workflow name="decision-analyzer">

<context>
  <goal id="frame">
Decision: Migrate to Kubernetes...
  </goal>

  <goal id="evaluate[optimist]">
## Opportunities
K8s will allow scaling...
  </goal>

  <goal id="evaluate[critic]">
## Concerns
Complexity overhead...
  </goal>
</context>

<current-goal id="synthesize">
Synthesize into a recommendation.
</current-goal>

</workflow>
```

## XML Elements Reference

| Element | Purpose | Attributes |
|---------|---------|------------|
| `<workflow>` | Container for goal execution | `name` - workflow name |
| `<context>` | Contains prior goal outputs | none |
| `<goal>` | A completed goal's output | `id` - goal identifier |
| `<current-goal>` | What to execute now | `id`, optional: `loop`, `iteration` |
| `<task>` | Dynamic sub-agent task | `role`, `parent-goal` |
| `<correction>` | Supervisor feedback | `source="supervisor"` |
| `<iteration>` | Loop iteration group | `n` - iteration number |

## LLM Response Format

LLMs respond with plain markdown/text — no XML wrapper needed. The agent captures the response and wraps it appropriately when building context for subsequent goals.

## Session Logs

The XML-structured prompts appear in session logs, making it easy to:
- Debug context flow between goals
- Identify supervisor corrections
- Trace sub-agent task delegation

## Compatibility

This format is compatible with all major LLM providers (OpenAI, Anthropic, Google, etc.). Models handle XML well and naturally understand it as structural markup separate from their markdown responses.
