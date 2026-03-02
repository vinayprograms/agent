# 22 — Collaborative Learning Coach

Same agents as [13-learning-coach](../13-learning-coach/), but using `discuss` mode. Teacher and quizzer self-organize.

**Compare with:** [13-learning-coach](../13-learning-coach/) (explicit chain)

## Agents

- **teacher** — creates structured lessons
- **quizzer** — generates assessments

## Usage

```bash
swarm up
swarm submit --mode discuss "teach me Dijkstra's shortest path algorithm at an intermediate level, and create a quiz to test my understanding"
swarm history
swarm down
```

## How It Works

Both agents see the task. The teacher should EXECUTE (lesson creation is explicitly requested). The quizzer should also EXECUTE (quiz is explicitly requested). But the quizzer creates a quiz from the task description alone — not from the teacher's lesson.

## Pattern: Independent vs Dependent

```
         ┌─ teacher [EXECUTE — create lesson]
discuss ─┤
         └─ quizzer [EXECUTE — create quiz]
```

Key difference from chain (13): in the chain, the quizzer sees the lesson and creates questions answerable only from lesson content. In collaboration, the quizzer creates a quiz from the topic description. The quiz might test broader knowledge rather than specifically what the lesson covered.

This is a case where **chaining produces better results** — the quiz should validate the lesson, not just the topic.
