# Multi-Agent Structured Output with Synthesis

Demonstrates structured output with parallel agents and automatic synthesis.

## Agentfile

```
NAME balanced-research
INPUT topic

AGENT researcher "Research $topic finding facts and evidence" -> findings, sources
AGENT critic "Identify biases, limitations, and counterarguments about $topic" -> issues, concerns

GOAL analyze "Analyze $topic from multiple perspectives" -> summary, recommendations, confidence USING researcher, critic

RUN main USING analyze
```

## How it works

1. `researcher` and `critic` agents run **in parallel**
2. Each returns structured JSON with their declared outputs
3. The **synthesizer** receives both outputs:
   ```
   ## researcher
   - findings: ...
   - sources: ...

   ## critic
   - issues: ...
   - concerns: ...
   ```
4. Synthesizer produces the goal's declared outputs: `summary`, `recommendations`, `confidence`
5. This is essentially a DSPy-style signature transformation:
   ```
   findings, sources, issues, concerns -> summary, recommendations, confidence
   ```

## Usage

```bash
agent run --input topic="artificial intelligence ethics"
```

## Output

```json
{
  "summary": "Balanced analysis of AI ethics...",
  "recommendations": ["Establish oversight boards", "Require transparency"],
  "confidence": 0.78
}
```
