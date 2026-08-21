# Migration + Refactor Ledger — github.com/vinayprograms/agent

Unattended run (go skill `migrate.plan` → `migrate.iterate`). Committed so assumptions ride with history.
Maps: `maps/map-core.md` (llm/tools/policy/mcp/memory/credentials), `maps/map-guards.md`
(security→contentguard/shellguard, logging→slog, telemetry→otel), `maps/map-swarm.md` (bus/registry/tasks/heartbeat→swarmkit).

## Dependency change
- agentkit `v0.2.1-0.20260324114043-fbf217a606af` → `v1.2.0`
- add swarmkit `v1.0.0` (messaging, registry; heartbeat/task envelopes are IN-SOURCED, see A-S2)
- otel SDK + exporters promoted to direct deps (telemetry init in-repo)

## Behaviour invariants (must hold when the module is green again)
- CLI commands/flags unchanged (docs/usage/cli-reference.md); `agent validate|inspect|pack|verify|install|keygen|replay|version` behave identically.
- Agentfile language unchanged; all `examples/agent/*.agent` validate.
- Session JSONL event schema unchanged (internal/session); `agent replay` reads old sessions.
- Swarm wire formats byte-identical: task/result/heartbeat JSON field names, NATS subjects, JetStream stream `SWARM`, on-disk `~/.swarm/tasks/*.json`.
- Security fail-close semantics preserved (no stages → deny; reviewer error → deny; encoded content escalates).
- Policy enforcement: tools disabled by policy are neither advertised to the LLM nor executable; path/domain denials return errors containing "denied" (tests/security).
- Existing unit/integration/system tests pass again (adapted only where the agentkit type they mocked changed shape).

## Decisions / assumptions (no user present — recorded, revertable)
### core (map-core §8)
- A-C1 policy.toml: CONVERT to the v1.2.0 schema (repo files + `internal/setup` generator). Legacy keys fail loudly via `policy.FromTOMLWithUnknownKeys` with a message naming the new key. `default_deny` absent ⇒ v1.2.0 default (true); repo example files set it explicitly. Rationale: one schema, no translator to maintain; loud failure beats silent tool-disablement.
- A-C2 enforcement: per-tool `tools.Guard`s attached at registration (path guards for fs tools, domain guard for web_fetch, shellguard.Gate for bash) + executor-side `IsToolEnabled` filtering of definitions. `policy.AllowedDirs` become fs-tool `extraRoots`. Tool set builder lives in `cmd/agent` wiring (app-side), in one file.
- A-C3 bash: keep `denylist` (→ shellguard user-denied commands); DROP `allowlist` and bwrap/docker sandboxing (no kit support; doc 10 to be amended). Legacy keys error loudly.
- A-C4 credentials: use `credentials.Lookup` + `credentials.Load(StandardPaths("grid")...)` + `Resolve`; drop provider-name normalisation and the generic `[llm]` fallback (documented in CHANGELOG-style note in final report). SEARXNG_URL etc. keep working via os.Getenv where the tool already reads env.
- A-C5 executor observation seam reshaped to `(findings, insights, lessons)` / `RememberFIL` — no adapters.
- A-C6 spawn: runtime registers `tools.NewSpawnBinder().Tool()`; executor binds. `spawn_agent` (singular) removed from prompts/tests.
- A-C7 `llm.MockProvider` copied VERBATIM to `internal/testutil/llmmock` (typed against `llm.Model`).
### guards (map-guards §5)
- A-G1 modes: default = Escalatory(Screener, Reviewer); paranoid = Paranoid(Screener, Reviewer) (docs-07 semantics); research = default + Context["scope"].
- A-G2 one Guard per run (no per-agent filtering); `event_seq` dropped from TaintNode (omitempty).
- A-G3 audit trail DROPPED (never exported); docs/security/06 + 07-invariant-3 amended to point at session JSONL.
- A-G4 legacy `telemetry.Exporter.LogEvent` hooks DROPPED; `InitProvider` copied verbatim to `internal/telemetry`.
- A-G5 logging → `log/slog` to stderr; small in-repo helpers for the domain events (PhaseStart, SupervisorVerdict…).
- A-G6 block correlation (`argsContainBlockData`, ~25 lines) re-implemented in-repo so `block_id`/`related_blocks` stay narrow.
- A-G7 token-counting decorator around the security `llm.Model` so session events keep token counts.
- A-G8 contentguard Skip list = all registered tools except {bash, write, edit, web_fetch, spawn_agents, rm, mv, patch} (old high-risk set + destructive fs tools).
### swarm (map-swarm §12)
- A-S1 keep the repo's JetStream layer (stream `SWARM`, in-repo pull consumer); do NOT adopt swarmkit `task.Dispatcher/Worker`.
- A-S2 task/result/heartbeat envelopes + `MetricsCollector` IN-SOURCED VERBATIM into `internal/swarm` (wire byte-identical). swarmkit used for `messaging` (bus) and `registry` only.
- A-S3 raw `*nats.Conn` for JetStream: open a second `nats.Connect` in serve (swarmkit packages each dial their own).
- A-S4 registry: `registry.New` + Register at start, Deregister on shutdown; Touch/re-register-on-TTL machinery dropped (no TTL in swarmkit `AGENTS`).

## Red baseline
- baseline-sha (green, pinned agentkit): e8b42f5
- red-baseline error count (`go build -gcflags=-e ./... 2>&1 | grep -c '\.go:[0-9]'`): 7 at sha after dep bump
- error count must only shrink; previously-green packages must not regress.

## Units (consumer package granularity) and waves
Wave A (independent, parallel, each green in isolation):
1. [ ] U1 internal/testutil/llmmock — copy old llm/mock.go verbatim (A-C7)
2. [x] U2 internal/telemetry — copy InitProvider verbatim (A-G4); otel deps
3. [ ] U3 internal/swarm — in-source envelopes/metrics (A-S2); dispatch tool → new tools.Tool; replay test
4. [ ] U4 internal/tools/websearch — new tools.Tool shape, credentials.Lookup
5. [ ] U5 internal/setup — policy generator to new schema (A-C1), credentials FileStore
Wave B:
6. [ ] U6 internal/supervision — llm.Model, slog (A-G5), tests on llmmock
7. [ ] U7 cmd/swarm — tasks→internal/swarm envelopes, messaging re-point
Wave C:
8. [ ] U8 internal/executor — tools/llm/contentguard/memory/mcp/slog/spawn (largest)
Wave D:
9. [ ] U9 cmd/agent — runtime wiring (toolset builder, guards, shellguard, contentguard, telemetry), serve (messaging/registry/second conn), workflow, tests
10. [ ] U10 cmd/agentmem — memory API
Wave E:
11. [ ] U11 tests/{integration,failure,performance,security,system} — llmmock, registry builder, policy files
12. [ ] U12 examples/**/policy.toml + docs (security 06/07/10, configuration, usage) to new schema
Final: verify.md gate (gofmt/vet/build/test -race), then parked-smells cleanup, then the idiomatic refactor + 100% coverage phases (separate ledger section below).

## Gate protocol per unit
worker (migrate.unit.md) → adversarial verifier (critique.md lens + functional check vs invariants) → worker fixes → re-verify, bounded 2 rounds → orchestrator mechanical check (error count shrank, scope guard, unit tests green) → commit (unit + ledger).

## Per-unit records (append-only)
### U2 internal/telemetry — DONE (fd3040e + schema fix, verifier PASS)
API: Config{ServiceName,ServiceVersion,Endpoint,Protocol,Insecure,Headers,BatchTimeout,ExportTimeout}; Init(ctx, Config) (shutdown, error). No GetTracer — executor uses otel.Tracer directly (U8).
DEVIATION (verified by verifier): old kit InitProvider ALWAYS failed (semconv v1.26.0 schema conflict with sdk resource.Default) — tracing never worked; now uses resource.Default().SchemaURL(). coverage 97.8% — sole uncovered stmt is resource.Merge error return, unreachable by construction (accepted).
parked-smells: ServiceName doc says required but defaults; fmt.Errorf without verbs; batch/export timeouts not surfaced in internal/config.
