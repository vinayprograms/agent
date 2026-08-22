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
1. [x] U1 internal/testutil/llmmock — copy old llm/mock.go verbatim (A-C7)
2. [x] U2 internal/telemetry — copy InitProvider verbatim (A-G4); otel deps
3. [x] U3 internal/swarm — in-source envelopes/metrics (A-S2); dispatch tool → new tools.Tool; replay test
4. [x] U4 internal/tools/websearch — new tools.Tool shape, credentials.Lookup
5. [x] U5 internal/setup — policy generator to new schema (A-C1), credentials FileStore
Wave B:
6. [x] U6 internal/supervision — llm.Model, slog (A-G5), tests on llmmock
7. [x] U7 cmd/swarm — tasks→internal/swarm envelopes, messaging re-point
Wave C:
8. [ ] U8 internal/executor — tools/llm/contentguard/memory/mcp/slog/spawn (largest)
Wave D:
9. [ ] U9 cmd/agent — runtime wiring (toolset builder, guards, shellguard, contentguard, telemetry), serve (messaging/registry/second conn), workflow, tests
10. [x] U10 cmd/agentmem — memory API
Wave E:
11. [ ] U11 tests/{integration,failure,performance,security,system} — llmmock, registry builder, policy files
12. [ ] U12 examples/**/policy.toml + docs (security 06/07/10, configuration, usage) to new schema
Final: verify.md gate (gofmt/vet/build/test -race), then parked-smells cleanup, then the idiomatic refactor + 100% coverage phases (separate ledger section below).

## Gate protocol per unit
worker (migrate.unit.md) → adversarial verifier (critique.md lens + functional check vs invariants) → worker fixes → re-verify, bounded 2 rounds → orchestrator mechanical check (error count shrank, scope guard, unit tests green) → commit (unit + ledger).

## Per-unit records (append-only)
### U1 internal/testutil/llmmock — DONE (e0963c1, verifier PASS)
copied verbatim from OLD llm/provider.go:135-249 (not mock.go). API: llmmock.New() *Model, method set unchanged. coverage 100%.
parked-smells: no mutex on callCount/lastRequest; receiver `p` fossil; exported ChatFunc field + setters mix; Reset only clears callCount; stringly "tool"/"end_turn"; SetToolCall hard-codes "tc-1".
### U2 internal/telemetry — DONE (fd3040e + schema fix, verifier PASS)
API: Config{ServiceName,ServiceVersion,Endpoint,Protocol,Insecure,Headers,BatchTimeout,ExportTimeout}; Init(ctx, Config) (shutdown, error). No GetTracer — executor uses otel.Tracer directly (U8).
DEVIATION (verified by verifier): old kit InitProvider ALWAYS failed (semconv v1.26.0 schema conflict with sdk resource.Default) — tracing never worked; now uses resource.Default().SchemaURL(). coverage 97.8% — sole uncovered stmt is resource.Merge error return, unreachable by construction (accepted).
parked-smells: ServiceName doc says required but defaults; fmt.Errorf without verbs; batch/export timeouts not surfaced in internal/config.
### U4 internal/tools/websearch — DONE (224f059, verifier PASS)
API: websearch.New(creds credentials.Lookup, searxngURL, provider string) *Tool; register via tools.New(...) INSTEAD of tools.Search. Params query/count identical to v1.2.0 built-in. creds resolved at construction (config > lookup > env). coverage 100%.
parked-smells: rate-limiter state is package-global (t.Parallel hazard) → move onto Tool in refactor phase; no WithHTTPTimeout option; Referer uses const not t.ddgURL; package-doc wording on env fallback.

## Checkpoint 2026-08-21 (usage-limit pause)
Merged to main: U1, U2, U4 (records above). In flight on branches (worktrees under ../agent-wt/):
- U3 `mig/u3` @774f1e2 — worker done (98.3% cov, 4 listed unreachable blocks); adversarial verifier was running, result not yet recorded → re-run verifier, then merge + record.
- U5 `mig/u5` @cec0391 — worker done (99.7% cov); verifier running with an explicit check of assumption (a) "v1.2.0 policy can't disable a single tool when default_deny=false" and of whether the restrictive policy now leaves edit/grep/ls disabled → re-run verifier, then merge + record.
- U6 `mig/u6` — worker running (supervision → llm.Model + slog, 100% cov target); not yet committed.
Next after these: U7 cmd/swarm (needs U3 merged), U8 internal/executor (needs U1,U3,U6), U9 cmd/agent, U10 agentmem, U11 tests/*, U12 examples policy.toml + docs; then verify.md gate, parked-smells cleanup, refactor phase per diagnosis.md, 100% coverage phase, system tests with examples/agent via agent.ollama.toml.
Process note: append ledger records on main only (worktree-side edits conflict on merge).
### U3 internal/swarm — DONE (774f1e2, verifier PASS)
in-sourced verbatim: task.go (TaskMessage/TaskResult…), heartbeat.go (Heartbeat/BusSender on messaging.Bus), metrics.go (MetricsCollector). Re-point map: tasks.X→swarm.X, heartbeat.X→swarm.X, heartbeat.Unmarshal→swarm.UnmarshalHeartbeat. JSON tags pinned by TestTaskWireTags. DispatchTool on v1.2.0 tools.Tool. coverage 98.3% (4 unreachable marshal/JetStream error returns, verified). nats-server/v2 test dep (already in graph).
parked-smells: BusSender.run swallows initial-beat error; Replay swallows NextMsg errors + hard-coded 5s drain (make injectable post-green); Validate mutates receiver; Sprintf("%d")→strconv; Sender iface could narrow; tests use Sleep polling + context.Background.
### U5 internal/setup — DONE (cec0391 + 8caa743, verifier FAIL→PASS after 3 fixes)
generator emits v1.2.0 policy schema (bare [tools.X] tables = enabled; deny/allow; [mcp] enabled/allow; [content.security]); round-trip test vs FromTOMLWithUnknownKeys for all scenarios. credentials via NewFileStore/SetAPIKey/Save at ~/.config/grid/credentials.toml; MCP probe via mcp.Stdio+Tools(). coverage 99.7% (Run() needs a TTY).
fixes after round 1: loud legacy-key warning in loadExistingConfig (policyWarning on welcome); nil FileStore guard for empty credentials file (panic); expired Claude CLI token no longer offered.
Confirmed: v1.2.0 policy cannot disable a single tool when default_deny=false (generator notes it). Pre-existing: restrictive policy leaves edit/ls/grep unlisted (same as old generator) → follow-up in refactor phase.
parked-smells: 2180-line file; m.err never cleared; hand-rolled bubble sort; value-receiver cursor mutation; "mode 0400" hint vs 0600 file; ~/.config/agent vs ~/.config/grid; fresh-mode legacy policy.toml overwritten without warning.
### U6 internal/supervision — DONE (e536eb0, verifier PASS)
API: Config.Provider→Config.Model llm.Model; Config.Logger/PipelineConfig.Logger *slog.Logger (nil ⇒ slog.Default(), component=supervisor). Log messages/keys preserved (+additive `step` key on phase_start/phase_complete). coverage 100%.
NOTE for U8: phase_start/phase_complete/supervisor_verdict helpers are private here; U8 must reuse the same key set (phase, goal, step, duration, result) — consider a tiny shared helper in refactor phase.
parked-smells: goal always logged ""; PAUSE with HumanAvailable && nil chan has no error; Supervisor iface is producer-side (consumer = executor); log-and-return at supervisor_llm_error; Pipeline.warn nil-logger now writes (was no-op).
### U10 cmd/agentmem — DONE (14dd3fa, verifier PASS)
memory API unchanged in v1.2.0 (bleve store layout identical, stores stay readable). main.go gofmt-only. New TestMain re-exec tests pin commands/flags/exit codes. coverage 98.1% (ListAll/RecallFIL error returns unreachable without seam).
parked-smells: Walk nil-info deref; silent Sscanf on --limit; strings.Title; swallowed ListAll error in stats; AGENTMEM_CHILD env leak guard; exec without context; --limit/--category cases under-tested.
### U7 cmd/swarm — DONE (8a785b0, verifier PASS)
re-points only (tasks.X→swarm.X, 22 sites); wire/on-disk formats pinned by new tests (reply_to pinned by U3). coverage 64.3% (uncovered: process spawning, TUI loop, tailscale, replayWeb, 30s const).
parked-smells / BUGS for refactor phase: replay --web panics (float64 vs int64); submit loses early results (second subscription) → 30s hang; capabilities counts heartbeats not agents; taskDB unlocked RMW; replay ignores dataDir; http.Server no timeouts; taskID path traversal in web handlers; duplicated heartbeat struct ×3; checkNATS no-op; ignored errors; tests use Sleep sequencing + unchecked fixture writes.
- A-C1 ownership note: loud rejection of legacy policy keys (via policy.FromTOMLWithUnknownKeys, replacing the vanished policy.ValidateKeys call in cmd/agent/workflow.go) is implemented by U9. `sandbox`/`timeout` are valid v1.2.0 keys that this agent does not enforce.
- A-C3 refinement (U12 verifier): shellguard deny matches base command names only; legacy glob entries (rm -rf *, chmod 777 *) are NOT widened to bare commands — dropped, LLM review covers them.
