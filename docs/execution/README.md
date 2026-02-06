# Execution Model Documentation

This documentation describes the agent execution architecture, supervision system, and workflow processing.

## Contents

| Chapter | Title | Description |
|---------|-------|-------------|
| 1 | [Four-Phase Execution](01-four-phase-execution.md) | COMMIT → EXECUTE → RECONCILE → SUPERVISE |
| 2 | [Reconciliation & Supervision](02-reconciliation-supervision.md) | Drift detection and correction |
| 3 | [Supervision Modes](03-supervision-modes.md) | UNSUPERVISED, SUPERVISED, SUPERVISED HUMAN |
| 4 | [Workflow Processing](04-workflow-processing.md) | Agentfile parsing and execution |
| 5 | [Supervisor Verdicts](05-supervisor-verdicts.md) | CONTINUE, REORIENT, PAUSE |

## Overview

The headless agent uses a four-phase execution model with optional supervision:

![Execution Overview](images/00-execution-overview.png)

## Core Principles

1. **Declare before acting** — Agent commits to an approach before execution
2. **Self-assessment** — Agent evaluates its own work after completion
3. **Tiered oversight** — Static checks first, LLM supervision only when needed
4. **Human escalation** — Critical steps can require human approval

## Quick Reference

| Phase | Purpose | Cost |
|-------|---------|------|
| COMMIT | Agent declares intent | 1 LLM call |
| EXECUTE | Do the actual work | N LLM calls + tools |
| RECONCILE | Static pattern checks | ~0 (no LLM) |
| SUPERVISE | LLM-based judgment | 1 LLM call (when triggered) |

## When to Use Supervision

| Scenario | Recommendation |
|----------|----------------|
| Development/testing | UNSUPERVISED |
| Internal tools, trusted data | UNSUPERVISED |
| Production, trusted users | SUPERVISED (auto) |
| Critical operations | SUPERVISED |
| Sensitive data, compliance | SUPERVISED HUMAN |
| Public-facing agents | SUPERVISED + paranoid security |

See [Supervision Modes](03-supervision-modes.md) for configuration details.
