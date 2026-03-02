# Swarm Task Resilience

## Context

When `swarm restart` is called, the current behavior is simple: drain → kill → start fresh. In-flight tasks are abandoned. This is sufficient for v1 but leaves room for improvement.

## Future Scenarios

### Resume After Restart

Agent picks up where it left off after restart. Requires:
- Checkpointing conversation state (messages, tool results) to disk
- Resuming LLM conversation from checkpoint
- Detecting which tools already executed (side effects)

Challenge: LLM conversations aren't easily resumable mid-turn. Tool side effects (file writes, bash commands) may not be idempotent.

### Idempotent Resubmission

Same task resubmitted with same `idempotency_key` produces same result. Requires:
- Deduplication at the agent level (check if task already completed)
- Result caching keyed by idempotency_key
- Handling partial completion (task started but didn't finish)

The `TaskMessage.IdempotencyKey` field already exists in the wire format but is not enforced.

### Automatic Retry

swarm detects failed/abandoned tasks and resubmits:
- Configurable max retries per capability
- Exponential backoff between retries
- Dead letter queue for permanently failed tasks

### Rewind

Roll back to a previous checkpoint within a multi-goal workflow:
- Requires goal-level checkpointing (already exists in supervision system)
- swarm could trigger rewind to specific goal
- Complex interaction with side effects from tools

## Priority

Low. Current simple restart is sufficient for personal swarm use cases. These become important at Hive scale or for long-running workflows where restart cost is high.
