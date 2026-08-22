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
8. [x] U8 internal/executor — tools/llm/contentguard/memory/mcp/slog/spawn (largest)
Wave D:
9. [x] U9 cmd/agent — runtime wiring (toolset builder, guards, shellguard, contentguard, telemetry), serve (messaging/registry/second conn), workflow, tests
10. [x] U10 cmd/agentmem — memory API
Wave E:
11. [x] U11 tests/{integration,failure,performance,security,system} — llmmock, registry builder, policy files
12. [x] U12 examples/**/policy.toml + docs (security 06/07/10, configuration, usage) to new schema
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
### U12 examples policy.toml + docs — DONE (6203382 + 6c0a6c0, verifier FAIL→fixed)
5 policy files converted (enabled sets unchanged vs main; default_deny explicit everywhere); tests/system/policy_files_test.go walks repo and asserts zero unknown keys. Widened rm/chmod deny entries removed after review. Docs: security 05/06/07/10/README, design 01/02/04/05, execution/05, configuration/protocols, README, Makefile setup-dev, examples/agent/memory/README.
Flag for U9/U11: examples/agent/memory/simple-memory.agent references `memory_forget` (not a v1.2.0 tool); internal/setup/setup_test.go:1057 legacy literal is intentional (legacy-warning test).
### U8 internal/executor — DONE (799163e, af606c4, 4dc30cb; verifier FAIL→PASS)
API for U9/U11: `New(Config) (*Executor, error)` — NewExecutor/NewExecutorWithFactory DELETED. Config{Workflow, Model llm.Model, Resolver llm.Resolver, Registry *tools.Registry, Policy, SpawnBinder *tools.SpawnBinder, Logger *slog.Logger, Debug, MCPManager, SkillRefs, Session, SessionManager, PersistentSession, CheckpointStore, Supervisor, HumanAvailable, HumanInputChan, Security *SecurityConfig{Mode, Scope, Screener, Reviewer(REQUIRED), Patterns, Keywords}, TimeoutMCP/WebSearch/WebFetch, ObservationExtractor (=*memory.Extractor), ObservationStore (=BleveStore/InMemoryStore), MetricsCollector, InterruptBuffer, DiscussPublisher, WorkspaceContext, Hooks}. Executor builds contentguard (Skip = registry minus {bash,write,edit,web_fetch,spawn_agents,rm,mv,patch}); guard error ⇒ New error (fail-closed); Reviewer required in every mode. `(*Executor).LogBashSecurity` matches shellguard.Gate.OnDecision. Definitions filtered by policy + execution refused for disabled tools. Session meta keys unchanged; value spellings now kit's (`tool:X`, "no untrusted content", "skipped tool: X"). coverage executor 87.3%.
parked-smells: hand-rolled semaphore→errgroup.SetLimit; atomic.AddInt32→atomic.Int32; observation goroutine context.Background (→WithoutCancel + lifetime); fmt.Errorf '%s'→%q; bare contentguard error (prefix "security:"); TestExecutor_ToolExecution `ls ..`; workspace.go 0%, converge multi-agent paths uncovered.
### U11 tests/* — DONE (783c41e + 79c86cc, verifier PASS w/ fixes applied)
new tests/internal/testkit: Registry(t,pol,ws,extra...) with path guards ("<tool>: access denied: …") + shellguard bash gate; Executor(t,cfg); PermissivePolicy(). All tests re-pointed (llmmock, executor.New, registry.Execute, v1.2.0 policy literals, t.Context). BashCommandInjection now enforces && and | cases. TestSystem_CredentialsLoading deleted (asserted nothing). tests/system root resolution fixed; runtime tests need U9.
parked-smells: glob has no path guard (comment narrowed?); pathGuard CheckPath uses process cwd for relative paths; benchmarks use context.Background/b.N; getSrcDir alias; fixture write errors ignored; tests/system drives CLI via go run subprocess.
Flag: examples/agent/memory/simple-memory.agent references memory_forget (not a v1.2.0 tool) → fix in U9 or refactor phase.

## Phase 2 — idiomatic refactor + coverage (after U9 merge; source: diagnosis.md + parked-smells above)
Invariants carry over (CLI flags/commands, Agentfile, session JSONL, swarm wire/on-disk, security fail-close, policy enforcement). Each unit: worker → adversarial verifier (≤2 rounds) → merge. Boundary moves are sanctioned (user asked for better Go design).
Wave 1 (parallel, independent packages):
- [ ] R1 cross-cutting sweep: interface{}→any (X6); one DefaultStateDir/config-dir owner in config (X3/X4, bug 12); setup.Step→Screen (X7); dead-code delete list (diagnosis "Delete" table, re-verify zero callers); config.GetProfile BaseURL/Thinking bug (1); cmd/replay *.json→*.jsonl (6); agentmem Walk nil-deref (8).
- [x] R4 internal/config: Get-cluster → nouns/delete; deprecation warnings returned not printed; inject home/env; 100%.
- [x] R8 small packages: skills.ReadReference/ScriptPath traversal (7); packaging dead code + Get* → File/Agentfile/Config/Policy; agentfile.ValidateWithoutPaths; websearch limiter state onto Tool (+WithHTTPTimeout); supervision.Supervisor + checkpoint.CheckpointStore ifaces → consumers; hooks tests; each to 100%.
Wave 2:
- [x] R2 internal/session: single file-backed Recorder (NewX returns error, no half-built store, AddEvent-after-Close guard); replay/loader.go → session.ReadFile; delete Store/Manager/NewManager/Message/ToolCall; JSONL byte-identical (golden tests); 100%.
Wave 3:
- [ ] R3 internal/executor: goroutine ownership (bug 9: observation/async tools under WaitGroup/errgroup, WithoutCancel); Set*/Clear* → Config + RunOptions; errgroup.SetLimit; atomic.Int32; ctx threading (X5); SwarmContext/XMLContextBuilder dead parts; XMLContextBuilder naming gauntlet; U8 parked smells; coverage → 100% (workspace.go, converge multi-agent paths).
Wave 4 (parallel):
- [~] R5 DROPPED by user on 2026-08-22 ("let's not work on swarm") — cmd/swarm stays as migrated (U7); its parked bugs remain listed under U7. Was: cmd/swarm → internal/swarm/{taskdb (mutex + ID validation, traversal fix), manifest, web (httptest-able, server timeouts), launch}; bugs 3,4,5,10; capabilities counts agents; submit early-result race; replay uses dataDir; cobra root (X8); main thin; tests to 100% of non-TUI code.
- [ ] R6 cmd/agent → internal/runtime (Load+New+Run) + internal/serve (lifecycle type; status/currentTask sync; one executor per task or serialized; taskDone buffered); bug 2 (cleanup on exit), 11; output via cmd.OutOrStdout/ErrOrStderr (X9); os.Exit only in main (X10, typed exitError); main thin; toolset tests; simple-memory.agent memory_forget fix; 100% of non-TTY code.
Wave 5:
- [ ] R7 cmd/replay → delete (Makefile alias) or 5-line main over shared cobra factory; cmd/agentmem cobra; parseCostSpec → replay.ParsePricing; X1 truncate dedupe.
- [ ] R9 coverage push to 100%: internal/step, internal/hooks, internal/replay (2,420 lines, 0%), internal/setup remaining, cmd/* non-TTY; move tests/integration+failure into package tests where it removes `go run` subprocess tests; adopt synctest for timing tests.
- [ ] R10 system tests: run every examples/agent/*.agent that needs no MCP/network-only deps via agent.ollama.toml against testdata expected criteria; record results in test-results/; fix Agentfile drift.

## Checkpoint 2 (usage-limit pause)
Merged: U1–U8, U10–U12. Module builds; `go test -race ./...` green on branch mig/u9 (merged with main).
In flight: U9 `mig/u9` @14f8760 (+merge of main) — worker done (cmd/agent 55.8%); adversarial verifier running (security wiring, policy legacy error, credentials on fresh machine, serve lifecycle, smoke runs of examples 01/02 via agent.ollama.toml). On PASS → merge, record, then the full verify.md gate on main.
Phase 2 started early: R4 `ref/r4` (internal/config) and R8 `ref/r8` (skills/packaging/checkpoint/hooks/supervision/swarm/websearch) workers running off main (no cmd/agent overlap). Each needs a verifier before merge; expect call-site conflicts with mig/u9 in cmd/agent — merge U9 first, then rebase/merge R4, R8.
Remaining: R1, R2, R3, R5, R6, R7, R9, R10 per plan above.
### U9 cmd/agent — DONE (14f8760, f22b804; verifier PASS; smoke: 01-hello-world ran end-to-end on ollama-cloud, legacy policy rejected loudly)
toolset.go builds registry from policy (path/domain guards, bash only with shellguard gate, websearch instead of kit Search, SpawnBinder, scratchpad/memory); runtime wires executor.Config incl. SecurityConfig{Reviewer: provider}; credentials loaded inside run/serve (no init()); policy via FromTOMLWithUnknownKeys with replacement-naming error; no policy file ⇒ permissive + warning (old behaviour); serve on swarmkit messaging + registry, second nats conn for JetStream, wire identical. coverage 55.8%.
fix after review: diff/patch guard resolves relative paths against cwd (kit doesn't confine them); legacy-key report skips bare tables, maps memory_read/write.
parked-smells: grep/glob guarded under "read" (document); http.Server timeouts; status/currentTask races; RunCmd os.Exit; --state not forwarded from run; secTrust only printed; stdout/stderr mix for status lines.
## MIGRATION COMPLETE — module green (build/vet/test -race) at this merge. 14 pre-existing gofmt-dirty files in internal/* → R1.
### R4 internal/config — DONE (09ff2e2 + d154cb9, verifier PASS)
Profile(name) LLMConfig (bug fix: BaseURL/Thinking/retries now carried); Profiles map[string]LLMConfig; typed Protocol consts; LoadOptions{Home, Getenv}; Config.Deprecations (cmd/agent prints WARN); DefaultConfigDir=~/.config/grid, DefaultStateDir=~/.local/agent (user's explicit choice kept; semantic-memory.md updated); six dead funcs deleted; [storage] compat decoded once. coverage 100%.
Process note: ALWAYS merge from the main checkout (`git -C /Users/vinay/Documents/projects/agent merge …`), never from inside a worktree.
### R8 skills/hooks/checkpoint/supervision/packaging/swarm/websearch — DONE (7 commits, merged 9ea98e8; verifier PASS — old-binary package signatures still verify)
skills: traversal guard (ReadReference/ScriptPath reject abs/..), Discover returns invalid list; 100%. hooks 100%. checkpoint: Store (no stutter), consumer iface supervision.Store, Checkpoint(id)/Trail() copies, single upsert, PathEscape'd file names, Load deleted; 97.4%. supervision: Work{Commit,Execute,Post}, Event, <-chan string, decision struct, Supervisor iface kept (Pipeline is an in-package consumer); 100%. packaging: split by concern, os.Root zip-slip fix, TargetDir required, PEM checks, File/Agentfile/Config/Policy; 96.2%. swarm: dead Replay stack deleted, Logger on sender, MetricsCollector(MetadataSetter), Validate pure; 98.1%. websearch: state on Tool, WithHTTPTimeout, ErrNoProvider; 100%.
### R2 internal/session — DONE (8e50543, merged 2b27f68; verifier PASS — golden verified against old code byte-for-byte)
session.Open(dir, Sink) (*Recorder, error) with Create/Update/Get; Sink func(Event) set at construction (serve swaps publisher via atomic.Pointer outside session); ReadFile(path, ReadOptions) replaces replay/loader duplicate; ErrUnknownFormat; Store/FileStore/Manager/SessionManager/Message/ToolCall/EventSecurityTier* deleted; executor.Config.SessionManager deleted (executor no longer calls the recorder). AddEvent after Close appends in memory (never blocks), persisted by the final Update. Golden wire-format test TestRecorder_WireFormatGolden. coverage 100%.
pre-existing wire quirk documented: Event.Error never written for event records (footer field shadows it).
### R9a internal/agentfile + internal/step — DONE (861e6e6, 8cf8c34; verifier PASS w/ follow-ups)
agentfile: Validate returns errors.Join of *ValidationError{Line,Msg} (text preserved); ValidateWithoutPaths deleted; parser internals unexported (Lexer/Token* stay exported: tests/performance uses them); HumanRequiredStepNames. coverage 99.7%. step: 100%.
R8 follow-ups for R1: archive extract error wording ("extracting %q"); skills.Discover dead outside tests; Windows-safe step-ID escaping; ErrNoProvider trailing prose.
R9a follow-ups for R1: add errors.As/.Line assertions on Validate; delete two dead lexer branches (lexer.go:79-81, 159-161); decide on dropped "validation errors:" header (restore at LoadFile or CLI); TestLoadFile_SkillPathTildeExpansion writes to real $HOME → t.Setenv; drop TestNode_Marker coverage noise.
R2 follow-ups for R1: concurrent AddEvent+Flush/Close test; Session doc "fields are the caller's until Update"; runtime.go:373 wrap consistency.

User ruling 2026-08-22: no further work on swarm (cmd/swarm, internal/swarm beyond what is merged). R1 sweep must skip cmd/swarm; R6 keeps serve.go in place (no internal/serve extraction of swarm-facing code beyond cmd/agent cleanups); R7 excludes swarm CLI.
