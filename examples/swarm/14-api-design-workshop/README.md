# 14 — API Design Workshop

Full API lifecycle: design the spec, implement it, test it. Three agents with different expertise.

## Agents

- **api-designer** — creates OpenAPI 3.0 specs from requirements
- **implementer** — builds Go server from spec
- **api-tester** — writes integration test suite

## Usage

```bash
swarm up
swarm chain design-api "A bookmarks API: CRUD for bookmarks with tags, search by tag, bulk import/export" -> implement-api -> test-api
swarm down
```

## Pattern: Spec-Driven Development

```
design-api → implement-api → test-api
```

The spec acts as a contract between agents. Tests validate against the spec, not just the implementation. If tests fail, the bug could be in implementation OR spec.
