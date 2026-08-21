# investigate.refactor — diagnosis (read-only), module github.com/vinayprograms/agent

Mode: **app** (internal/ + cmd/; no external consumers). Rubric: go skill rubric (principles, naming, api-design, responsibility, errors, concurrency, testing, idioms) + reference/packages.md + reference/cli.md. Prior rulings from DESIGN-DEBT.md are inherited verbatim (Get→noun accessors, OnX→X, With*=derivation only, ≤3 positional/else trailing Config, Executor ready-to-use, session→single Recorder, cmd/ holds domains, three `Step` types, hooks/agentfile/checkpoint/supervision/step/executor/packaging are agentcore deletion candidates — names inside them are NOT polished here).

Out of scope by instruction: everything the agentkit v1.2/swarmkit migration rewrites (llm.Provider→Model, security→contentguard/shellguard, bus/registry/tasks/heartbeat→swarmkit, logging→slog, telemetry→otel). Findings below are only what survives that migration.

Legend per item: **[pkg]** = fix stays inside the package boundary · **[MOVE]** = needs a boundary move (routes to extract/decompose) · size T/M/L · anchors are `file:line` as of this snapshot.

Coverage baseline: cmd/agent 18.8, agentfile 68.9, checkpoint 88.1, config 72.5, executor 45.7, packaging 56.4, session 50.9, skills 86.4, supervision 35.5, swarm 25.5, websearch 40.1; zero for step, hooks, replay, setup, cmd/swarm, cmd/agentmem, cmd/replay. Target 100 %/package.

---

## 0. Cross-cutting findings (apply to several packages)

| # | Smell | Fix | Anchors | Size |
|---|---|---|---|---|
| X1 | **Four copies of `truncate`** | one unexported helper per consumer is fine in Go, but these are identical; keep `executor.truncateForLog`, delete the three others after the cmd/swarm → domain move (they end up in one package) | cmd/agent/serve.go:651, cmd/swarm/main.go:1052, internal/executor/helpers.go:14, internal/replay/helpers.go:286/296 | T |
| X2 | **`parseCostSpec` + `isTerminal` duplicated across two binaries** | cmd/replay becomes `agent replay` (it already is: cmd/agent/replay.go) — delete cmd/replay, keep the Makefile target by aliasing, or make cmd/replay a 5-line `main` that calls the shared cobra command. Move `parseCostSpec` into internal/replay as `replay.ParsePricing(spec string) (model string, p ModelPricing, err error)` | cmd/agent/replay.go:35-61, cmd/replay/main.go:147-172,244 | M [MOVE] |
| X3 | **Three different default state dirs** | one constant owner. `config.New()` says `~/.local/agent` (config.go:173), runtime falls back to `~/.local/grid` (runtime.go:82), swarm uses `~/.local/share/swarm` (main.go:109, manifest.go:113, replay.go:61). Pick one, put it in `config`, have everyone call `config.DefaultStateDir(home)` | listed | T |
| X4 | **`~/.config/grid` vs `~/.config/agent`** | setup.getDefaultConfigDir returns `.config/agent` (setup.go:378) while config.globalConfigPath and credentials use `.config/grid` (config.go:310). Bug: the wizard’s ConfigDir points where nothing reads. Same owner as X3 | setup.go:376-381, config.go:305-311 | T |
| X5 | **`context.Background()` in library code (22 sites)** | thread `ctx` from the caller; the only legitimate ones are `main`/signal roots. Worst: executor.go:320 (hook fire inside pipeline callback), executor.go:379-383 (observation goroutine), setup.go:1927 | grep `context.Background()` | M |
| X6 | **`interface{}` (52 sites) / `map[string]interface{}`** | `any` (go 1.25 in go.mod) — mechanical | all | T |
| X7 | **Three unrelated `Step` types** (ruling #7) | `agentfile.Step` is the AST RUN node and the canonical meaning; `step.Step` is an execution-graph interface (agentcore deletion candidate — leave); rename **`setup.Step` → `setup.Screen`** (it is a wizard screen enum: `StepWelcome…StepComplete`, setup.go:137-168) — zero external call-sites, 1 file | setup.go:137 | T |
| X8 | **Two CLI frameworks** | cmd/agent = cobra, cmd/swarm = kong (main.go:20,100), cmd/agentmem and cmd/replay = hand-rolled `os.Args` loops. Standardise on cobra factories (reference/cli.md): each binary `main.go` = `NewRootCmd().ExecuteContext(ctx)`; drop the kong dependency | cmd/swarm/main.go:28-114, cmd/agentmem/main.go:22-49, cmd/replay/main.go:22-131 | L |
| X9 | **`fmt.Print*` to os.Stdout/Stderr inside commands and libraries** (>150 sites) | commands print through `cmd.OutOrStdout()/ErrOrStderr()`; libraries never print — return data or accept an `io.Writer` (config.go:269, manifest.go:64 print deprecation warnings from inside loaders) | cmd/agent/*.go, cmd/swarm/*.go, cmd/agentmem, config.go:269, manifest.go:64, runtime.go (29 sites) | L (mechanical) |
| X10 | **`os.Exit` outside `main`** (ruling #6) | only `main()` exits. cmd/agent/main.go:29 (init), main.go:95 (RunCmd.Run — **skips the deferred `rt.cleanup()` at main.go:87 → bleve store and OTel never flushed**), cli.go:408 (dead `printErrAndExit`), setup.go:14, cmd/agentmem ×13, cmd/replay ×12. Return `error`; for a non-1 exit code use a typed `exitError{code}` that `main` unwraps | listed | M |

---

## 1. cmd/agent (2,640 lines; 18.8 %)

### Smell → fix
1. **`init()` loads credentials and `os.Exit`s before cobra parses flags** — breaks `--help`, `version`, `validate` when credentials.toml is malformed; global `globalCreds` (main.go:22-38). Fix: delete `init()`; load credentials inside `RunCmd.Run`/`ServeCmd.Run` only (serve already does its own `credentials.Load()` at serve.go:124 — two loaders with different error policy). Single helper `loadCredentials() (*credentials.Credentials, error)` called by run/serve. `godotenv.Load()` moves to `PersistentPreRunE`. **[pkg] M**
2. **`RunCmd.Run` calls `os.Exit(code)`** (main.go:93-96) → bypasses `defer rt.cleanup()`; `runtime.run` returns an `int` (runtime.go:591). Fix: `run(ctx) error`; cobra `RunE` returns it; `main` maps to exit code. **[pkg] T**
3. **`runContext` is a one-field struct passed to every `Run(ctx *runContext)`** (main.go:47-49) and several commands ignore it (Validate/Inspect/Pack/Verify/Install/Keygen/Setup/Replay/Version). Fix: delete; after #1 only run/serve need creds. **[pkg] T**
4. **Builder indirection `buildXCmd(cli *CLI, action func() error)` + `parseTestRoot` duplicating `newRootCmd`** (cli.go:105-405). The `action`/nil split exists only so tests can parse without executing. Fix: cobra factory per reference/cli.md — `newRootCmd(deps) *cobra.Command`, each subcommand owns its flag struct locally, tests execute the real root with `SetArgs` + `SetOut` and a fake `deps` (fake credentials, in-memory fs). Delete `CLI` struct (a kong remnant; main_test.go:54 comment still says "Kong uses positional arguments"). **[pkg] M**
5. **No `cmd.Context()`/signal wiring in run mode** — `RunCmd.Run` uses `context.Background()` (main.go:92); Ctrl-C kills without flushing the session. Serve mode hand-rolls `signal.Notify` twice (serve.go:263-270, 458-459). Fix: `main` does `signal.NotifyContext`, `root.ExecuteContext(ctx)`, commands use `cmd.Context()`. **[pkg] T**
6. **cmd/agent holds three domains** (ruling #6): (a) `runtime` = executor wiring (runtime.go, 648 lines, 14 components + closers); (b) `serviceAgent` = a NATS/HTTP task-serving agent (serve.go, 1,082 lines: work loop, JetStream pull loop, heartbeat, registry, discuss publishing, drain); (c) `workflow` = config+Agentfile+policy loading with validation (workflow.go). Fix: **[MOVE]** → `internal/runtime` (or `agent`): `Load(opts) (*Loaded, error)` + `New(ctx, loaded, deps) (*Runtime, error)`; **[MOVE]** serve.go → `internal/serve` (`serve.Agent`, `agent.RunHTTP(ctx)`, `agent.RunBus(ctx)`) — much of serve.go is swarmkit-coupled and will be rewritten by the migration, so route the *structure* (package boundary, lifecycle, state machine) now and let the migration fill the bus seam. cmd/agent keeps only flag binding + `printXxx` rendering. **L**
7. **Data races in `serviceAgent`** (survives migration — it is the agent state machine): `status`, `currentTask` written in `executeTask` (serve.go:950-954) and read by HTTP handlers (serve.go:206,225) and the signal goroutine (`initiateShutdown`, serve.go:1007); HTTP mode calls `executeTask` **concurrently** from handler goroutines (serve.go:244) on one shared `*executor.Executor` whose `inputs/outputs` maps are unsynchronised (executor.go:122-123, 647-654). Fix: `atomic.Int32`/mutex for status; serialise task execution with a `sync.Mutex` or a single worker goroutine fed by a channel (as bus mode already does via `workCh`). **[pkg→MOVE with #6] M**
8. **`http.Server` without timeouts** (serve.go:254-257; also cmd/swarm/web.go:127) → slow-loris; `json.NewDecoder(r.Body)` unbounded (serve.go:232). Fix: `ReadHeaderTimeout`, `ReadTimeout`, `IdleTimeout`; `http.MaxBytesReader`. **[pkg] T**
9. **`taskDone` is unbuffered with a non-blocking send** (serve.go:181, 955-958) — if `pullWorkLoop` is not yet waiting (serve.go:788) the signal is dropped and the JetStream message is never acked → redelivery after `AckWait`. Fix: buffered `chan struct{}` of 1, or return a per-task `done` channel from `executeTask`. **[pkg] T**
10. **`handleInstanceMessage` drops corrections when idle** (serve.go:561-565) — silently discards operator input; at minimum log via the session, better: queue until next task. **[pkg] T**
11. **Dead code**: `generateShortID` (serve.go:1060), `printErrAndExit` (cli.go:408), `validateStateConfig` (always nil, workflow.go:113), `workflow.statePath` written never read (workflow.go:25, serve.go:76), `validateStateConfig` call (workflow.go:66), empty `defer func(){}()` in executor (see §8). Delete. **[pkg] T**
12. **`parseSwarmCapabilities` uses `fmt.Sscanf("%d")`** (serve.go:641) → `strconv.Atoi`; errors currently collapse to 1 silently. **[pkg] T**
13. **`resolveStoragePath` expands `~` by hand and ignores `UserHomeDir` errors** (runtime.go:80-87); `expandAbsPath` swallows `filepath.Abs` error (workflow.go:90). Fix: one `expandHome(p, home string)` in the new runtime package; inject `home` for tests. **[pkg] T**
14. **`setupCallbacks` type-asserts `evt.Data["name"].(string)` without comma-ok** (runtime.go:492-560) → panic on a malformed hook payload. Hooks is a deletion candidate (observe), but until then use comma-ok or typed event structs. **[pkg] T**
15. **Inline goal synthesises a fake `agentfile.Workflow` in the command** (main.go:63-72) — domain knowledge in cmd; move to `agentfile.InlineGoal(text) *Workflow` or the runtime package. **[MOVE] T**
16. **`profileProviderFactory` lives in cmd** (runtime.go:374-430) — migration rewrites it (`llm.Model`), but its *home* is the runtime package; flag for the move only.
17. **Naming**: `workflow` struct is a loader, not a workflow (`workflow.load()`; it holds `wf *agentfile.Workflow`) → `loader`/`Loaded`; `runtime` collides with stdlib `runtime` (rubric: don't shadow stdlib) → `agent.Runtime` in the new package. **[MOVE] T**
18. **`createTriageProvider` swallows the provider error** (runtime.go:451) `provider, _ := llm.NewProvider(...)` → returns nil silently; return the error. (Migration touches the call but not the policy.) **T**

### Preserve
- Commands + flags in docs/usage/cli-reference.md and Makefile: `run [file] --config --policy --workspace --goal --debug -i/--input`, `validate [file]`, `inspect [path]`, `pack <dir> -o --sign --author --email --license`, `verify <pkg> --key`, `install <pkg> --target --key --no-deps --dry-run`, `keygen -o`, `setup`, `serve [file] --config --policy --workspace --state --http --bus --queue-group --capability --session-label --type --capabilities`, `replay <session> -v --no-pager --cost`, `version`. Default positional `Agentfile`. (Doc drift to fix separately: cli-reference lists `-f <path>`, `agent help`, `make install-system` — none exist.)
- Env fallbacks `AGENT_TYPE`, `SWARM_CAPABILITIES` (serve.go:154,166) — cmd/swarm `up` sets them.
- Exit code 1 on run failure; JSON result printed to stdout for workflows, raw outputs for `--goal`; `✓ Valid` string (tests grep it).
- Session dir layout `<state>/sessions/<label>/<id>.jsonl`; `checkpoints/<sessID>/` under it.
- NATS subjects `work.<cap>.*`, `work.<instance>.*`, `done.<cap>.<id>`, `discuss.<id>`, `control.<id>.shutdown`, `events.<name>`, `heartbeat.<id>`; result JSON = `tasks.TaskResult` wire; discuss update JSON keys `instance_id,task_id,goal,content,timestamp`.

### Impact set
cmd/agent/* only (binary). Makefile `build`, `install`, `validate-examples`; tests/system (exec-based, see §17).

### Testability plan → 100 %
- Factory root + `SetOut/SetErr/SetArgs` executes every command in-process; delete the `go run` tests in main_test.go (≈ 6 × 2–4 s each).
- `deps` struct injected into the factory: `stdin io.Reader`, `stdout/stderr io.Writer`, `home string`, `getenv func(string) string`, `loadCredentials func() (*credentials.Credentials, error)`, `newRuntime func(...)`. Fake runtime for `run`/`serve` so the command layer is 100 % without an LLM.
- `serve` HTTP mode: `httptest.NewServer(agent.handler())` once the mux is a method; bus mode is swarmkit-coupled — test the state machine (`executeTask`, drain, `taskDone`) against a fake `bus` interface defined in the test.
- Untestable as written: `init()` (runs before any test can intercept), `os.Exit` paths, `fmt.Println` output, `isTerminal(os.Stdout)`, `signal.Notify` inside handlers, hard-coded `credentials.Load()`.

---

## 2. cmd/swarm (3,283 lines; 0 %)

### Smell → fix
1. **A JSON "task DB" named and commented as SQLite** (`taskDB`, "SQLite persistence", "swarm.db", main.go:1059-1084) is a domain package hiding in cmd (ruling #6). Fix **[MOVE]** → `internal/swarm/taskdb` (or `swarmstate`): `Open(dir) (*DB, error)`, `Record(task)`, `Complete(result)`, `Task(id)`, `Result(id)`, `Thread(id)`, `AppendThread(id, entry)`, `List(filter)`, `Stats()`. Rename `GetTask/GetResult/GetThread` → nouns (ruling #5). **L**
2. **Unsynchronised read-modify-write of `tasks.json`** — `InsertTask`/`UpdateResult` (main.go:1088-1161) are called from 7 concurrent NATS subscription callbacks in web.go:94-99/173-242 and from CLI commands in other processes → lost updates and torn files. Fix: mutex in the DB type + write-temp-and-rename; cross-process: a lock file or per-task files only (the per-task `.json` files are already the source of truth — drop `tasks.json` and derive `List/Stats` by scanning `tasks/`). **[MOVE with #1] M**
3. **Swallowed errors** (ruling #10): `loadRecords`/`loadPIDRecords` ignore `json.Unmarshal` (main.go:1229,1317); `nc.Publish` ignored (main.go:687,841,1419; web.go:732); `savePIDRecords` ignored (main.go:852); `os.Remove` (850); `nc.Flush` (842); `exec.Command(browser).Start()` (replay.go:211); `enc.Encode` (replay.go:89); all `s.db.*` calls in web.go:186-330; `sub.Unsubscribe` everywhere. Fix: return or log with the reason; a corrupt `pids.json` must surface, not become "no agents". **[pkg] M**
4. **Path traversal in web API**: `handleTaskDetail`, `handleThreadAPI`, `handleHumanReply` take `taskID` from the URL and build `filepath.Join(dir, taskID+".json")` (web.go:592-600, 631, 668) with no sanitisation — `../../` escapes `dataDir`. `handleSessionLogs` does sanitise (web.go:896-902) — inconsistent. Fix: one `validTaskID(s) bool` (`^t-[0-9a-f]{8}$`) at the DB boundary (rubric: untrusted input never decides a path; Go 1.24 `os.Root` for the directory). **[MOVE with #1] T**
5. **`replayWeb` panics on every run**: `result["duration_ms"].(int64)` (replay.go:186) — JSON numbers decode to `float64`. Also both replay funcs hard-code `~/.local/share/swarm` (replay.go:61,97) instead of `a.dataDir`. Fix: decode into `tasks.TaskResult`, pass `a.dataDir`. **[pkg] T**
6. **`tui.executeCmd` publishes `{"task": …}` raw** (tui.go:269-270) instead of a `tasks.TaskMessage` → workers reject it (`UnmarshalTaskMessage`/`Validate` in serve.go:805). Same code exists correctly in `SubmitCmd.Run`. Fix: one `submit(nc, capability, inputs)` in the domain package used by CLI, TUI and web (web.go:459-500 has a third copy). **[MOVE] M**
7. **`handleShutdownCommand` publishes `control.<pid.Name>.shutdown`** (web.go:731) but agents subscribe `control.<agentID>.shutdown` where `agentID = <capability>-<sessionID>` (serve.go:142,448) → never delivered. `DownCmd` gets it right (main.go:778). Reuse `discoverAgentsViaHeartbeat`. **[pkg] T**
8. **Input-parsing block duplicated 3×** (`--input`, `--file`, positional JSON: main.go:298-347, 417-461, plus chain) → `parseInputs(flags, file, positional) (map[string]string, error)`. **[pkg] T**
9. **`time.Sleep` as protocol** (main.go:729 10 s, 814 3 s, 844 200 ms, 690 500 ms) — replace with polling `isProcessAlive` on a ticker + deadline, `nc.Flush()` for propagation, and `cmd.Wait` with a timeout. **[pkg] M**
10. **`RestartCmd` is a stub** printing advice (main.go:859-864); `checkNATS` (manifest.go:180), `execCmd`, `expandPath`, `getUserHome` (main.go:1428-1449) are dead. Delete or implement `restart = down + up`. **[pkg] T**
11. **`UpCmd.Run` is 180 lines of process supervision** (spawn, pipe-prefix, crash-detect, PID file, SIGTERM/SIGKILL) — a `procs`/`launcher` domain: `Launch(ctx, spec) (*Proc, error)`, `Stop(procs, grace)`. **[MOVE] M**
12. **`webServer` is 1,037 lines mixing**: NATS→WS broadcast, persistence (#1), HTTP API, Tailscale serve, session-log browsing (`handleSessionLogs` 120 lines with a debug `listTree` walk logged on every miss, web.go:960-970). Fix **[MOVE]** → `internal/swarm/web` (handlers + cache) and keep Tailscale in cmd. Unbounded `go func` per client per message (web.go:164) → per-client send goroutine with a bounded channel, drop on overflow. `http.Server{Handler: mux}` without timeouts (web.go:127). **L**
13. **Global `log.Printf`** (web.go, main.go:900-920) alongside `fmt.Fprintf(os.Stderr)` — one logger (slog after migration) injected into the server. **T**
14. **`loadManifest` prints a deprecation to stderr** (manifest.go:64) and defaults to the home dir inside a parser. Return `Manifest.Deprecations []string`; take `home` as an argument. `Manifest` types are exported in `package main` — move with #1 to `internal/swarm/manifest` (`manifest.Load(path, home)`). **[MOVE] T**
15. **`waitForResult` polls with `NextMsg(1 ms)` in a busy loop** (wait.go:58-65) — use `select` on two `chan *nats.Msg` via `nc.ChanSubscribe` plus a `time.Ticker` for the heartbeat deadline; take `ctx`. **[pkg] T**

### Preserve
- Subcommand names/flags (README/docs): `status, agents, capabilities, submit <cap> [task] -i -f --nowait, discuss, result <id> -w, history -c -s -l, up [file] -a, down [agents], restart, ui -p -b --tui -f, replay <id> -w, chain <spec>, purge -f`, global `--nats`.
- **swarm task JSON on disk**: `tasks/<id>.input.json` (= `tasks.TaskMessage` wire), `tasks/<id>.json` (= `tasks.TaskResult`, indented), `tasks/<id>.thread.jsonl` (`threadEntry` fields `agent_id,capability,name,type,content,round,timestamp`), `tasks.json` (`taskRecord`), `pids.json` (`pidRecord` `name,pid,capability,started_at`). The web UI (static/) reads `wsMessage{type,subject,data}` and `history` records — keep field names.
- `swarm.yaml` schema incl. deprecated `storage.root` migration and the manager/replica validation rules.
- Child `agent serve` argv shape and `AGENT_TYPE`/`SWARM_CAPABILITIES` env (main.go:630-663).
- Log forwarding JSON `{agent,capability,line}` on `log.<name>`; `control.swarm.shutdown` `{event,agents}`.

### Impact set
cmd/swarm only; static/ JS reads the WS/JSON shapes; cmd/agent serve consumes the argv/env contract.

### Testability plan
- After the moves: `taskdb` 100 % with `t.TempDir()` (incl. concurrent writers with `-race`); `manifest` table tests (defaults, deprecation, invalid type, >1 manager, relative path resolution with an injected `home`); `web` handlers via `httptest.NewRecorder` + an in-test `publisher` interface replacing `*nats.Conn`; `launcher` with a fake `exec` (inject `commandContext func(ctx, name, args…) *exec.Cmd` and run `os.Args[0]` with a `-test.run=TestHelperProcess` helper).
- Cobra factory for the CLI; commands call the domain packages through small consumer-defined interfaces (`nats` operations: `Publish`, `SubscribeSync`, `JetStream`).
- Untestable as written: everything — `main` wires `*nats.Conn` directly, `taskDB` path is `os.UserHomeDir()`, TUI/web start real servers, `time.Sleep` protocol, `os.Args`/kong globals.

---

## 3. cmd/agentmem (403 lines; 0 %)

1. Hand-rolled `os.Args` parsing with `fmt.Sscanf` ignoring errors (main.go:84,141) and `os.Exit` in every `cmdX` (13 sites) → cobra factory; `--limit` via `IntVar`. **[pkg] M**
2. **`filepath.Walk` callback ignores `err` and dereferences `info`** (main.go:229-234) → nil-pointer panic on an unreadable entry. Use `filepath.WalkDir` and check `err`. **[pkg] T**
3. `strings.Title` is deprecated (main.go:274); `store.ListAll` errors ignored (273); `json.Unmarshal` ignored (326). **T**
4. Reads `semantic_graph.json`/`kv.json` by hand-coded paths — file-layout knowledge belongs to agentkit `memory` (consumer of a sibling’s private layout). After migration, ask `memory` for these; until then isolate in one `layout` struct. **[MOVE-ish] T**
5. Output via `fmt.Println` → `cmd.OutOrStdout()`.
Preserve: subcommand names `list/search/stats/graph/scratchpad`, `--category= --limit= --term=` spellings (docs/memory). Impact: none (leaf binary). Tests: cobra in-process + `t.TempDir()` with a real `memory.NewBleveStore` (it is file-backed, no network) → 100 % reachable once `os.Exit` is gone.

---

## 4. cmd/replay (250 lines; 0 %)

1. Duplicate of `agent replay` with a hand-rolled flag loop and 12 `os.Exit` sites (main.go:32-131). Fix: delete the parser; `main` = cobra root reusing the same `replayCmd` factory as cmd/agent (or keep the binary name via Makefile alias). **[MOVE → shared factory in internal/replay/cli or cmd/agent] M**
2. `expandPaths` returns only `*.json` for directories (main.go:231) but sessions are written as `*.jsonl` (session.go:534) → directory mode finds nothing. Bug. **T**
3. `--follow` only works with one file, `-vv` semantics differ from cobra `CountVarP` in cmd/agent (`-vv` vs `-v -v`). Preserve both spellings.
Preserve: `agent-replay [opts] <file|dir>…`, `-f/--follow/--live`, `-v`, `-vv`, `--no-pager`, `--cost`, `--version`. Tests: in-process root with `SetArgs`, golden output from internal/replay.

---

## 5. internal/agentfile (1,494 lines; 68.9 %) — agentcore deletion candidate: structure only

1. Validation builds a `[]string` then joins into one `fmt.Errorf` (loader.go:168-236) — callers cannot `errors.Is/As`. Fix: `errors.Join` of a typed `ValidationError{Line int, Msg string}`. **[pkg] T**
2. `ValidateWithoutPaths` == `Validate` (loader.go:244) — dead alias, delete. `LoadFile` vs `LoadFileWithOptions` → `FromFile(path, opts...)` is the ruling (“constructors encode the source”), but names are frozen by the deletion rule — only delete the dead one. **T**
3. `LookupIdent`, `NewLexer`, `NewParser`, `Lexer`, `Parser`, `Token*` exported with no consumer outside the package (only `ParseString` is used) — unexport. **[pkg] T**
4. `Workflow.GetHumanRequiredStepNames` (ast.go:151) → `HumanRequiredStepNames` (ruling #5; single call-site executor.go:556). **T**
5. Coverage gaps: `resolveAgentFrom` skill-path branches (loader.go:87-127), `loadAgentFromSkillDir` errors (131-164), `Validate` supervision-downgrade cases (214-233), lexer `readTripleQuoteString`/`readPath` edge cases. Plan: table tests over `testdata/*.agent` golden ASTs; `t.TempDir()` skill dirs; error-path cases asserting `ValidationError.Line`.
Preserve: Agentfile grammar and every error string prefix `line %d:` (tests and docs grep them); `Workflow` field names (executor, serve, inspect read them).

---

## 6. internal/checkpoint (249 lines; 88.1 %) — deletion candidate

1. `CheckpointStore` stutter + producer-side interface with one impl (ruling #3): the only consumers are executor/supervision → they declare what they need (supervision needs `SavePre/SavePost/SaveReconcile/SaveSupervise/GetDecisionTrail`; executor needs nothing beyond passing it through). **[MOVE of interface to consumer] T**
2. Four copy-pasted `Save*` (checkpoint.go:105-158) → one `upsert(stepID string, set func(*Checkpoint)) error`. **[pkg] T**
3. `Get`/`GetDecisionTrail` return internal `*Checkpoint` pointers (160-179) → callers can mutate the store (rubric: encapsulation) — return copies; rename `Get(id)`→`Checkpoint(id)`, `GetDecisionTrail`→`Trail()`. **T**
4. `flush` uses `stepID` as a file name unsanitised (checkpoint.go:189; IDs like `subagent:role`) — encode (`url.PathEscape`). **T**
5. `Load` silently skips unreadable/corrupt files (206-221) and is unused outside tests → delete or return `errors.Join`. **T**
6. Coverage: `flush` write error, `Load` skip paths; use a read-only dir (`os.Chmod 0o500`) and malformed JSON.
Preserve: on-disk JSON `{"pre","post","reconcile","supervise"}` per `<stepID>.json` (replay/forensics read it).

---

## 7. internal/config (387 lines; 72.5 %)

1. **Bug: `GetProfile` drops `BaseURL` and `Thinking`** (config.go:354-378 copies 4 of 6 fields) → `profileProviderFactory` (runtime.go:411-412) always sends empty BaseURL/Thinking for profiles. Fix: copy all fields (or make `Profile` embed `LLMConfig`). **[pkg] T — behavior fix, flag to user**
2. Get-cluster (ruling #5 settled): `GetAPIKey→APIKey`, `GetGatewayToken→GatewayToken`, `GetProfile→Profile`, `GetProfileAPIKey→ProfileAPIKey`. Except: `GetAPIKey`, `GetGatewayToken`, `GetProfileAPIKey`, `DefaultAPIKeyEnv`, `LoadDefault`, `Default` have **zero non-test callers** (credentials moved to agentkit) → delete rather than rename. **T**
3. `migrateStorageToState` decodes the file a second time, ignores both decode errors (246-247, 263), and prints to stderr (269). Fix: decode once into `map[string]any` alongside the struct (or `toml.MetaData.Undecoded()`), return `Config.Deprecations []string`; the command prints them. **[pkg] T**
4. `LoadWithPrecedence` reads `os.Getwd`, `os.Getenv`, `os.UserHomeDir` directly (207-236) → add `LoadOptions{Home, Getenv func(string) string}`; tests stop mutating `HOME`. **T**
5. `Config.State.Location` default `~/.local/agent` (173) — see X3. `Telemetry.Protocol: "noop"` default (176) is a magic string matched in runtime.go:205 — make it a typed const.
6. `Profile` vs `LLMConfig` are the same shape minus two fields → `type Profile = LLMConfig`? No — aliases are for migration; make `Profiles map[string]LLMConfig`. **T**
Preserve: TOML keys (docs/configuration), `[storage]→[state]` compat, precedence order global→project→env→CLI, `AGENT_CONFIG` env var. Impact: cmd/agent runtime.go/workflow.go/serve.go, setup (writes the TOML text, not the struct). Tests to 100 %: table over precedence with injected home/env; `mergeIfExists` stat-error branch (unreadable dir); deprecation warning assertion.

---

## 8. internal/executor (5,000 lines; 45.7 %) — deletion candidate (→ agentcore workflow); diagnose structure/safety only

1. **Constructor cluster** (ruling #1): `New`, `NewExecutor`, `NewExecutorWithFactory` (executor.go:256-351) + `SetMetricsCollector` (355), `SetPersistentSession` (526), `SetInterruptBuffer` (210), `SetDiscussPublisher`/`Clear…` (223-230), `SetEventPublisher`/`Clear…` (234-245). The two deprecated ctors are used only by tests/ (13 sites). Fix: delete them; all five `Set*` already have `Config` fields except the publishers’ *clear* semantics. For serve mode the per-task publishers are genuinely per-task state → pass them in `Run(ctx, inputs, RunOptions{Interrupts *InterruptBuffer, Discuss func(...)})`, not as executor mutation. **[pkg] M**
2. **Goroutines outlive `Run`**: `extractAndStoreObservations` (executor.go:378-384, `context.Background()`, no WaitGroup) and `executeAsyncTool` (tools.go:267,352) keep logging to the session after `Run` returned → `session.Close()` has stopped the writer, `AddEvent` then blocks forever once `eventCh` (256) fills, and `SetEventPublisher/Clear` races with them (session.OnEvent read at session.go:283). Also the bleve store is closed by `rt.cleanup()` while an extractor write may be in flight. Fix: `errgroup`/`sync.WaitGroup` owned by the executor, waited in `Run`’s defer; observation extraction bounded by `ctx` (`context.WithoutCancel(ctx)` if it must survive cancellation). **[pkg] M**
3. `asyncTools`/`serializeTools`/`isExternalTool` string tables (tools.go:145-213) — the executor decides scheduling by tool *name* (responsibility: the tool should declare it). Keep as data but move to one `toolClass(name)`; after migration ask the tool. **T**
4. `regexp.MustCompile` inside `interpolate` on every call (helpers.go:103) → package var. **T**
5. Empty `defer func(){}()` (executor.go:571-573) — delete. `getPipeline` allocates a passthrough `Pipeline` per goal (677-685) → build once in `New`. **T**
6. `logging.go`: 25 `log*` methods with `…WithDetails`/`…WithTaint` twins (303/298, 359/354, 441/436, 480/475) → one method each taking a small options struct; 595 lines → ~250. **[pkg] M**
7. `XMLContextBuilder` (ruling #4) and dead `AddDiscussContribution`/`SetDiscussTaskID` (xmlcontext.go:85-95, no callers) → delete the two; gauntlet the name later (deletion candidate). **T**
8. `SwarmContext` + `Get*` quartet (swarmctx.go:91-134; ruling #5) — **entire type is dead**: only `internal/swarm/replay.go` uses it, and `swarm.Replay`/`LastSequence` have no callers. Delete both (or wire them if the REPLAY phase is still planned — ask). **[MOVE/delete] T**
9. Hook payloads are `map[string]any` with implicit keys (executor.go:718, 1417, subagent.go:163) consumed by blind assertions in runtime.go → typed event structs (`hooks.GoalEvent{Name, Output}`) — hooks is a deletion candidate (observe), so do the minimum: comma-ok at the consumer.
10. `Executor` has 35 fields across 4 concerns; 1,449-line file. Split by concern is what agentcore does — do not polish.
11. `Run` returns `(*Result, error)` with `Result.Error string` duplicating `err` (executor.go:590-607) → return `Result` by value with no `Error` field, or keep the error only. **T**
12. Coverage (45.7 %): the big holes are `executePhase` tool-loop branches (interrupts, discuss publisher, skill activation, MCP tools, security verifier), `executeMultiAgentGoal`, `subAgentExecutePhaseWithProvider`, all `logSecurity*`. Plan: a test-local `fakeModel` that scripts tool calls then a final answer (converge_test.go already has `mockConvergeProvider`); a fake `tools.Tool`; in-memory `session.SessionManager` fake capturing events; table tests per branch; `-race` on the parallel paths. Untestable as written: observation goroutine (no join), `SetEventPublisher` races, `context.Background()` hook fire, `tools.SetHTTPTimeout` global (runtime.go:365, agentkit).
Preserve: `Result{Status,Outputs,Iterations}` (serve.go:976-991 and runtime.go:616 read it), `hooks.*` event names and `Data` keys until hooks is deleted, session event types/`Meta` fields (wire), `InterruptMessage`/`FormatInterruptsBlock` XML, prompt-prefix constants (behaviour).

---

## 9. internal/hooks (76 lines; 0 %) — deletion candidate (observe)

Only smell worth acting on: `Fire` holds no lock while calling handlers (correct) but `On` after `Fire` from another goroutine is fine; nothing to fix. Tests: 100 % in ~40 lines (`On/Fire`, nil receiver, ordering, no handlers). Do it because it is free and stays green until deletion.

---

## 10. internal/packaging (808 lines; 56.4 %) — deletion candidate (agentcore packaging)

1. One file, five concerns (manifest, tar/zip archive, ed25519 signing/keys, install, Agentfile reference validation). Structure only: split into `manifest.go`, `archive.go`, `sign.go`, `install.go` before/if it survives. **[pkg] M**
2. Dead: `LoadFromPath`, `LoadByName`, `ExtractToTemp` (692-726). Delete. **T**
3. `Get*` quartet → `File/Agentfile/Config/Policy` (settled; lands via agentcore) — skip.
4. `Install` default `~/.agent/packages` via `os.UserHomeDir` with ignored error (564) → `InstallOptions.TargetDir` required, default resolved by the command. **T**
5. `extractContent` zip-slip check uses `strings.HasPrefix(Clean(target), Clean(dir))` (604-606) — `dir="/a/b"` accepts `/a/bc/..`; use `filepath.Rel` + `..` check or `os.Root` (1.24). Files are created with `os.Create` ignoring tar mode and symlinks are skipped silently — document. **T**
6. `LoadPrivateKey/LoadPublicKey` `pem.Decode` nil-block not checked before use? (655,681 — `block, _ :=` then presumably `block.Bytes`) → check. **T**
7. Coverage: `Install` non-dry-run, `extractContent` error branches, `writePackage`, `LoadPrivateKey` malformed PEM, `validateAgentReferences` quote handling. All file-based → `t.TempDir()`; craft tar with `..` entries for the traversal test.
Preserve: `.agent` zip layout (`manifest.json`, `content.tar.gz`, `signature`), manifest JSON schema (docs/usage/packaging.md), deterministic packing (test exists), signature scheme.

---

## 11. internal/replay (2,420 lines; 0 %)

1. **Second replay stack**: `replay/session.go` (`replay.Event`, `LoadSession`) duplicates `session.Event`/`FileStore.loadJSONL` with a divergent schema (`_type` on the event itself) and has **no callers** → delete. `loader.go:28-105` also duplicates `session.FileStore.loadJSONL` line-for-line (session.go:633-713) → `session.ReadFile(path, opts)` exported once; replay calls it. **[MOVE: loader → session] M**
2. `max(a,b int)` helper (pager.go:478) shadows the 1.21 builtin → delete. **T**
3. `watchFile` polls with `time.Sleep(100ms)` (pager.go:146) while `fsnotify` is already a module dependency → use it, or accept a `clock`/`poll` interval injected for tests. **T**
4. `ReplayInteractive`/`ReplayFileLive` mutate `r.output` to capture (replayer.go:77-85, 100-104) → render into a `strings.Builder` via a `render(w io.Writer, sess)` method; `Replayer` keeps no writer state → also makes `Replayer` safe to reuse. **[pkg] T**
5. `Stats.PrintStats`/`PrintTokenUsage` take `io.Writer` (good) but `ComputeStats` walks 400 lines of `switch event.Type` with string keys into `Meta` — fine, but `Stats` has 30 fields; table-test it with a golden session.
6. `WithMaxContentSize` unused outside tests; options named `With*` are value constructors (ruling: bare nouns — `replay.MaxContentSize(n)`, `replay.Pricing(model, in, out)`). **T**
7. `styles.go` globals (lipgloss) are fine for a TUI; `pager` is unexported but `NewPager` is exported (pager.go:40) → unexport.
8. Coverage plan (0 → 100): `Replay(sess)` → golden files in `testdata/` for each event type at verbosity 0/1/2 (strip ANSI via `lipgloss.SetColorProfile(termenv.Ascii)` in `TestMain`); `ComputeStats` table; `loadSession` format detection/truncation/malformed line; `MultiReplayer` ordering; pager `Update` driven with `tea.KeyMsg` values (pure), `RunLive` needs the injected poll interval. Untestable as written: `p.Run()` (real terminal), `watchFile` sleep loop, `ReplayFileLive`.
Preserve: output text layout is consumed by humans and greps (`| grep SECURITY` in usage) — golden-file it before refactoring; JSONL loading must accept both the header/event/footer JSONL and legacy JSON.

---

## 12. internal/session (819 lines; 50.9 %)

1. **Five names, two ideas** (ruling #2) — and worse: `Manager` does **not** implement `SessionManager` (`Create(name, inputs)` vs `Create(name)`, session.go:419 vs 402), so `Store`, `Manager`, `NewManager`, `Manager.AddEvent`, `Message`, `ToolCall` (373-468) are dead except in session_test.go. Fix: delete them; keep one file-backed `Recorder` (`session.Open(dir) (*Recorder, error)`, `r.Create(name)`, `r.Update(s)`, `r.Get(id)`); rename `SessionManager`→ consumer-defined narrow interface in executor (`Update(*Session) error` is all it calls, executor.go:309 / session.go:362). **[pkg + interface MOVE to executor] M**
2. `NewFileManager` swallows `MkdirAll` error and returns a half-built store (741-748) → `(SessionManager, error)` (ruling: ready-to-use or error). **T**
3. `Session` mixes persisted JSON fields with writer plumbing (`eventCh`, `flushCh`, `stopCh`, `sessionMgr`, `closeOnce`, `OnEvent`, 96-111). Split: `Session` = data; `writer` = unexported batching goroutine owned by the Recorder. **[pkg] M**
4. **`AddEvent` after `Close` blocks forever** (276-298: `eventCh` non-nil, writer gone) — see executor #2. Fix: `closed atomic.Bool` → fall back to direct append, or make `AddEvent` return an error. **T**
5. `OnEvent` (108, ruling #8) → a `Sink` interface/func field set at construction, not mutated at runtime (serve.go:352 sets it concurrently with sub-agent goroutines calling `AddEvent`). **T**
6. `rand.Read` errors ignored (369, 473); `generateID` and `StartCorrelation` → `crypto/rand` is right, check the error (it only fails catastrophically — `panic` is acceptable there, document). **T**
7. `FileStore.Save` reads `sess.Events` without `sess.mu` (567) — safe today only because the single writer goroutine is the sole appender; document or read under lock. `writtenEvents` map never shrinks (511) — bounded by sessions per process; fine for run mode, leaks in serve mode over days → delete entry on Close. **T**
8. `DetectFormat` defaults to `"json"` for unknown content (818) — return an error (`ErrUnknownFormat`). **T**
9. Deprecated `EventSecurityTier1..3` aliases (70-72) — no callers → delete.
10. Coverage: writer loop batching (`batchSizeMax`, ticker), `Flush`, `Close` idempotency, `OnEvent` firing, legacy JSON fallback, `DetectFormat` sniffing. Use `testing/synctest` (1.25) for the ticker; fake `SessionManager` counting `Update` calls; `-race`.
Preserve **exactly**: JSONL wire format — header/event/footer records with `_type`, every `Event`/`EventMeta` JSON tag, “last footer wins”, `.jsonl` new / `.json` legacy read, append-only semantics (the swarm web UI `handleSessionLogs` and `agent replay` parse these).

---

## 13. internal/setup (2,187 lines; 0 %)

1. One 2,187-line file: enum, model, 40 `view*` funcs, 2 TOML generators, MCP probe, file writing. Split: `model.go` (state + Update), `screens.go` (enum + navigation), `views.go`, `generate.go` (pure TOML text), `write.go`. **[pkg] M**
2. `Step` → `Screen` (X7). **T**
3. `writeFiles` writes `agent.toml`/`policy.toml` **relative to CWD** with hard-coded names (1958, 1965) and `loadExistingConfig` reads `"agent.toml"` from CWD (289-296) while `Config.ConfigDir` exists but is unused for this → `Model.dir string` (injected; default `.`). **T**
4. `writeCredentials` ignores `credentials.Load()` errors (2171) and overwrites on any error. **T**
5. `generateAgentTOML`/`generatePolicyTOML` build TOML by string concatenation (1984-2166) → marshal a `config.Config`/`policy.Policy` struct with `BurntSushi/toml` — guarantees the wizard output stays in sync with the config schema (it currently writes keys `config` may not read). **[pkg] M**
6. `probeMCPServer` 30 s `context.Background()` (1927) → derive from a model-level ctx; `mcp.NewManager` direct → fine (agentkit).
7. `Run()` (2183) constructs `tea.NewProgram(New())` — add `Run(opts)` with `tea.WithInput/WithOutput` passthrough for tests.
8. Coverage plan: `Update` is a pure state machine — table-drive it with `tea.KeyMsg` sequences per screen and assert `m.config`; `generate*` → golden files; `writeFiles` with injected dir + `t.TempDir()`; `View()` smoke (non-empty, contains title) per screen. Untestable as written: CWD writes, `credentials.Load()` (home), `tea.NewProgram` without IO options, `probeMCPServer` (spawns a process — give it a fake `connector` interface).
Preserve: generated TOML keys and comments (users diff them), scenario/provider/model option lists (docs/configuration), credential methods.

---

## 14. internal/skills (253 lines; 86.4 %)

1. **Path traversal**: `ReadReference(name)` and `ScriptPath(name)` join caller-supplied names (221-252) — `name="../../etc/passwd"` escapes the skill dir; the name originates from LLM output (executor/skills.go). Fix: reject `filepath.IsAbs`/`..` (or `os.Root`). **[pkg] T**
2. `Discover` silently skips invalid skills (174) — return them as `Invalid []error` or log via an injected callback so a typo’d SKILL.md is not invisible. **T**
3. `parseRef` uses `bufio.Scanner` default 64 KB lines (191) — fine for frontmatter; note. `SkillRef` name fine.
4. Coverage: `ReadReference` missing file, `ListScripts` read-dir error (file in place of dir), `parseRef` open error, `Discover` non-NotExist error (permission). `t.TempDir()` + `os.Chmod`.
Preserve: SKILL.md frontmatter schema, name validation rules (spec).

---

## 15. internal/step (109 lines; 0 %) — deletion candidate

No naming changes. `sequence.Name()` returns `"first..."` (step.go:45) — odd but unused. `GoalExecutor` is correctly consumer-defined. Tests (free, 100 %): `BuildGraph` single vs multiple steps, `Sequence` stops on first error and on ctx cancel, `RunStep` ordering — fake `GoalExecutor` recording calls.

---

## 16. internal/supervision (671 lines; 35.5 %) — deletion candidate (agentcore supervise)

1. `Supervisor` + `LLMSupervisor` (ruling #3) — leave names; but `Config.HumanInputChan chan string` (53) is bidirectional → `<-chan string`; `SetHumanAvailable` (72) unsynchronised mutation after construction → a `Config` field only. **T**
2. `Pipeline.Run(ctx, req, commit, execute, post)` — three func params (pipeline.go:101-107) → `Work{Commit, Execute, Post}` struct (rubric: option struct for long param lists); `PipelineConfig.OnEvent` → `Event` (ruling #8). **T**
3. `parseSupervisionResponse` returns `(Verdict, string, string)` naked triple (327) → `decision{Verdict, Correction, Question}`; `Reconcile` dereferences `pre`/`post` without nil checks (78-83) while `Pipeline.Run` guards them — fine, document. **T**
4. `warn` swallows when `Logger` nil (257-261) — after migration slog default; ok.
5. Coverage 35 → 100: **`Pipeline.Run` has zero tests** and is pure given fakes — table: unsupervised passthrough, commit nil, save errors (fake store returning errors), reconcile no-trigger, supervise CONTINUE/REORIENT/PAUSE, supervise error, `HumanRequired` forcing supervise, event/phase-logger call order. `Supervise` human-input branches (176-201) use `time.After` → `testing/synctest`. Fake `llm` interface declared in the test.
Preserve: verdict strings `CONTINUE/REORIENT/PAUSE`, trigger names (session `Meta.Triggers` wire), supervisor prompts (behaviour).

---

## 17. internal/swarm (499 lines; 25.5 %)

1. `Replay`, `LastSequence`, `processReplay*` (replay.go, jetstream.go:78) — **no callers**; with them go `executor.SwarmContext` (§8 #8). Delete, or if the REPLAY phase is still intended, wire it in serve and keep. Ask. **[delete] T**
2. Bug in the dead code (if kept): `Timestamp: time.Now()` with comment "TaskMessage doesn't have timestamp" (replay.go:153) — it has `SubmittedAt` (used at serve.go:358). **T**
3. `DispatchTool.Execute` takes `args map[string]interface{}` (agentkit `tools.Tool` contract — migration). Fine.
4. Package mixes a tool, JetStream setup and a replay consumer — after #1 it is `dispatch.go` + `jetstream.go`; rename to what it provides (`swarmbus`?) only if it survives swarmkit.
5. Coverage: `DispatchTool` 100 % with a fake `bus.MessageBus` (test-local interface); `EnsureStream/EnsureWorkConsumer` need a NATS server → `nats-server/v2/test.RunServer` embedded (already an indirect dep via nats.go? no — add as test dep) or leave as integration-tagged.
Preserve: `work.<cap>.<t-xxxxxxxx>` subject/ID format, `TaskMessage` fields set by dispatch, stream name `SWARM`, consumer `work-<cap>` with `MaxDeliver 3`/`AckWait 10m`.

---

## 18. internal/tools/websearch (527 lines; 40.1 %)

1. **Package is unreferenced** — no `websearch.` import anywhere in cmd/ or internal/ (git status shows `?? internal/tools/` — new and never wired). Either register it in runtime.setupRegistry (the package doc says how) or delete. Decide first. **[MOVE: wiring belongs to runtime] T**
2. Package-level mutable globals: `httpClient` (102), `searchMutex/lastSearchTime/searchCooldown` (105-109) → fields on `Tool` (`client *http.Client`, `cooldown`, `last`, `now func() time.Time`). **[pkg] T**
3. `os.Getenv` inside `New`/`brave()`/`tavily()` (68, 113-128) — credential resolution belongs to the credentials owner; take the values in `Config{SearXNGURL, BraveKey, TavilyKey, Provider}` and let the runtime resolve env. **T**
4. Provider endpoints hard-coded (272, 322, duckduckgo.go) → `Config.BraveURL/TavilyURL/DDGURL` defaulting to prod — the only way to reach 100 % with `httptest`. **T**
5. Error strings with em-dashes/long prose (200-203) — keep the guidance but as a wrapped sentinel `ErrNoProvider`. **T**
6. Coverage: every provider success/4xx/malformed-JSON/ctx-cancel via `httptest.NewServer`; cooldown with injected clock; `toInt` table.
Preserve: `Name()=="web_search"`, parameter schema, cascade order, `SearchResult` JSON.

---

## 19. tests/{system,integration,failure,security,performance}

1. **tests/system is exec-based** (`go run ./cmd/agent` ×~40, 3–5 s each, no coverage) with stale `src/` assumptions: `buildAgent` → `getProjectRoot` returns *parent* of the go.mod dir + `"src"` (system_test.go:19-34 — wrong and `buildAgent` is unused); `getSrcDir` returns `../..` which is the repo root by accident (71-77). `TestSystem_CredentialsLoading` runs `go run ./internal/credentials -test` — the package does not exist (447-449) → permanently failing/ignored. Fix: delete tests/system; re-express as in-process cobra tests in cmd/agent (§1) — they then count toward cmd/agent coverage. **[MOVE] M**
2. tests/integration + tests/failure use the deprecated `executor.NewExecutor` (13 sites) and `context.Background()` → `executor.New(Config)` and `t.Context()`; these are really executor package tests — move into `internal/executor` as external test package `executor_test` so they count toward its coverage (external `tests/` dirs never raise per-package coverage without `-coverpkg`). **[MOVE] T**
3. tests/performance: classic `for i := 0; i < b.N; i++` ×7 → `for b.Loop()` (1.24). **T**
4. tests/security exercises agentkit `policy` (migration) — keep, retarget after migration.
5. No test in the module uses `t.Parallel`, `t.Context()`, `cmp.Diff`, `synctest`, or `httptest` (grep: zero hits) — adopt in the new tests; `os.CreateTemp` → `t.TempDir()` (cmd/agent/util_test.go:11,48).

---

## Summary tables

### Boundary moves (route to extract/decompose)
| From | To | Why |
|---|---|---|
| cmd/agent/runtime.go + workflow.go | `internal/runtime` (Load + New + Run) | cmd holds executor wiring/config precedence/policy merging |
| cmd/agent/serve.go | `internal/serve` | 1,082-line task-serving agent state machine; races fixable only once it is a type with a lifecycle |
| cmd/swarm `taskDB` + thread/pid persistence | `internal/swarm/taskdb` | JSON DB in cmd; concurrent-writer bug; traversal fix lives at this boundary |
| cmd/swarm manifest.go | `internal/swarm/manifest` | loader prints/defaults; needed by `up` and `ui` |
| cmd/swarm web.go handlers/cache | `internal/swarm/web` | 1,037 lines; httptest-able only as a package |
| cmd/swarm `UpCmd` process supervision | `internal/swarm/launch` | spawn/prefix/PID/SIGTERM domain |
| cmd/replay | delete → `agent replay` factory shared | duplicate binary |
| `replay/loader.go` JSONL reader | `session.ReadFile` | duplicate of session loader |
| `session.SessionManager` iface | consumer (executor) narrow iface | producer-side interface |
| `checkpoint.CheckpointStore` iface | consumer (supervision) | same |
| websearch wiring | runtime.setupRegistry (or delete pkg) | unreferenced package |
| tests/system, tests/integration, tests/failure | cmd/agent and internal/executor package tests | coverage + speed |

### Delete (dead code, verified zero non-test callers)
cmd/agent: `generateShortID`, `printErrAndExit`, `validateStateConfig`, `workflow.statePath` · cmd/swarm: `checkNATS`, `execCmd`, `expandPath`, `getUserHome`, `RestartCmd` stub · executor: `NewExecutor`, `NewExecutorWithFactory`, `SwarmContext`+tests, `XMLContextBuilder.AddDiscussContribution/SetDiscussTaskID`, empty defer · swarm: `Replay`, `LastSequence`, `processReplay*` · session: `Store`, `Manager`, `NewManager`, `Message`, `ToolCall`, `EventSecurityTier*` · replay: `session.go` (`Event`, `LoadSession`), `max`, `WithMaxContentSize` · config: `Default`, `LoadDefault`, `GetAPIKey`, `GetGatewayToken`, `GetProfileAPIKey`, `DefaultAPIKeyEnv` · packaging: `LoadFromPath`, `LoadByName`, `ExtractToTemp` · agentfile: `ValidateWithoutPaths` · websearch: whole package unless wired · tests/system `buildAgent`, `TestSystem_CredentialsLoading`.

### Behaviour bugs found incidentally (fix, but they are not refactors — surface to the user)
1. `config.GetProfile` drops `BaseURL`/`Thinking` (config.go:360-365).
2. `RunCmd.Run` `os.Exit` skips `rt.cleanup()` (main.go:87-96).
3. `replayWeb` panics on `duration_ms` float64→int64 assertion (cmd/swarm/replay.go:186).
4. `tui.executeCmd` publishes a non-`TaskMessage` payload (tui.go:269).
5. `handleShutdownCommand` targets the wrong subject (web.go:731).
6. cmd/replay directory mode globs `*.json` but sessions are `*.jsonl` (cmd/replay/main.go:231).
7. Path traversal in `/api/task/`, `/api/thread/`, `/api/reply/` and `skills.ReadReference/ScriptPath`.
8. `agentmem stats` nil-deref in `filepath.Walk` callback on error.
9. Goroutines outliving `Executor.Run` can block forever on the closed session writer.
10. `taskDone` unbuffered non-blocking send can drop the JetStream ack signal.
11. Concurrent `/task` HTTP requests run one `Executor` in parallel (unsynchronised maps).
12. Three different default state directories; wizard ConfigDir `.config/agent` vs everything else `.config/grid`.
