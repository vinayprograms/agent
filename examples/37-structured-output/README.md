# Structured Output Example

Demonstrates the `->` syntax for structured output fields.

## Agentfile

```
NAME structured-analysis
INPUT topic

GOAL research "Research $topic thoroughly" -> findings, sources, confidence
GOAL critique "Critique $findings for bias and gaps" -> issues, recommendations
GOAL report "Write final report on $topic" -> summary, action_items

RUN analysis USING research, critique, report
```

## How it works

1. Each goal declares output fields after `->`
2. LLM receives JSON instruction: "Respond with JSON containing: findings, sources, confidence"
3. Response is parsed and fields become variables
4. `$findings` from `research` is available to `critique`
5. Variables accumulate through the pipeline

## Output

Each goal returns structured JSON:

```json
// research goal
{"findings": "...", "sources": ["..."], "confidence": 0.85}

// critique goal  
{"issues": ["..."], "recommendations": ["..."]}

// report goal
{"summary": "...", "action_items": ["..."]}
```

## Usage

```bash
agent run --input topic="quantum computing"
```
