# Chapter 7: Efficiency Guidelines

## Overview

Agents should be frugal by default. Every LLM call, tool invocation, and web search costs time and money. Efficient agents produce better results faster and cheaper.

## Core Principles

### 1. Minimal Tool Calls

**Do:** Use the minimum tools needed to accomplish the goal.

**Don't:** Speculatively call tools hoping something useful emerges.

```
# Bad: Scatter-shot approach
- glob("**/*")
- read every file found
- grep for every possible pattern
- web_search for background on every concept

# Good: Targeted approach
- glob for specific patterns relevant to the task
- read only files likely to contain what's needed
- search only when local information is insufficient
```

### 2. Web Search Discipline

Web searches are expensive (API costs, latency, token consumption from results). Use them sparingly:

| Situation | Approach |
|-----------|----------|
| Task is self-contained | No web search needed |
| Need current data (prices, news) | 1-2 targeted searches |
| Research task | Max 3-5 searches, refine queries |
| Code/technical questions | Prefer local docs, search only for specific APIs |

**Never:**
- Search for general knowledge the LLM already has
- Run multiple searches for the same concept
- Search without a specific information need

### 3. Concise Output

Agents produce **results**, not essays. Output should be:

- **Direct:** Lead with the answer, not preamble
- **Dense:** Every sentence adds information
- **Actionable:** Reader knows what to do next

```
# Bad
"I've carefully analyzed the codebase and after thorough consideration 
of multiple factors, I believe there may be some issues worth noting..."

# Good
"Found 3 issues:
1. SQL injection in handler.go:42 (critical)
2. Missing error check in db.go:18 (high)
3. Unused import in main.go:5 (low)"
```

### 4. Iteration Efficiency

For CONVERGE goals:

- **Start strong:** First iteration should be close to final
- **Targeted fixes:** Each iteration addresses specific issues
- **Know when to stop:** Diminishing returns = time to converge

```
# Bad convergence pattern
Iteration 1: Rough draft
Iteration 2: Slightly better
Iteration 3: Minor tweaks
Iteration 4: Different minor tweaks
Iteration 5: Revert some tweaks
...

# Good convergence pattern
Iteration 1: Solid first attempt
Iteration 2: Address specific gaps
Iteration 3: CONVERGED (or one more targeted fix)
```

### 5. Context Efficiency

Don't bloat context with unnecessary content:

- **File reads:** Read only what's needed, use line ranges when possible
- **Tool outputs:** Process results, don't just accumulate them
- **History:** Reference previous work, don't repeat it

## Quantitative Guidelines

These are guidelines, not hard limits. Adjust based on task complexity.

| Metric | Simple Task | Medium Task | Complex Task |
|--------|-------------|-------------|--------------|
| Tool calls | 1-5 | 5-15 | 15-30 |
| Web searches | 0 | 1-2 | 3-5 |
| File reads | 1-3 | 3-10 | 10-20 |
| CONVERGE iterations | 1-2 | 2-3 | 3-5 |
| Output length | 100-300 words | 300-800 words | 800-2000 words |

## Anti-Patterns

### The Kitchen Sink

Reading every file, searching for everything, producing massive output "just in case."

**Fix:** Ask "what specific information do I need?" before each tool call.

### The Perfectionist Loop

Endless iterations making marginal improvements.

**Fix:** Define "good enough" upfront. When criteria are met, stop.

### The Narrator

Explaining every thought and action instead of doing the work.

**Fix:** Actions speak. Narrate only when it aids understanding.

### The Hoarder

Accumulating context "for later" without using it.

**Fix:** Use or discard. Don't carry forward unused information.

## Applying to Agentfiles

### Goal Descriptions

Be specific about scope:

```
# Vague (invites scope creep)
GOAL analyze "Analyze the codebase"

# Specific (constrains effort)
GOAL analyze "Find SQL injection vulnerabilities in handler files"
```

### CONVERGE Limits

Set realistic WITHIN values:

```
# Too generous (wastes iterations)
CONVERGE refine "Polish the code" WITHIN 20

# Realistic (forces efficiency)
CONVERGE refine "Polish the code" WITHIN 5
```

### Agent Prompts

Include efficiency guidance in complex agent prompts:

```markdown
# Code Reviewer

Review the provided code for critical issues.

## Efficiency
- Focus on high-impact issues only
- Skip style nitpicks unless egregious
- Limit to top 5 findings
- No web searches needed
```

## Measuring Efficiency

Good indicators:
- Task completed with minimal tool calls
- Output is dense and actionable
- CONVERGE goals finish well under WITHIN limit
- No redundant or speculative operations

Warning signs:
- Many tool calls with little progress
- Large outputs with low information density
- CONVERGE hitting WITHIN limit regularly
- Repeated searches for similar information

---

Next: [Architecture](01-architecture.md) | Previous: [Packaging](06-packaging.md)
