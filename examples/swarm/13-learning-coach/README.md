# 13 — Learning Coach

Two agents forming a teach-then-test loop. Teacher creates a lesson, quizzer generates an assessment.

## Agents

- **teacher** — creates structured lessons with Feynman technique
- **quizzer** — generates assessments from lesson content

## Usage

```bash
swarm up
swarm chain teach '{"task": "Dijkstra shortest path algorithm", "level": "intermediate"}' -> quiz
swarm down
```

## Pattern: Generate-Then-Validate

```
teach → quiz
```

The quizzer validates that the lesson content is testable and complete. If the quiz can't be answered from the lesson alone, the lesson has gaps. Human reviews both outputs to judge quality.
