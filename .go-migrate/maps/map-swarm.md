# Migration map: agentkit swarm primitives -> swarmkit v1.0.0

Scope: everything `github.com/vinayprograms/agent` imports from agentkit `bus`, `registry`,
`tasks`, `heartbeat`. The repo uses **none** of `results`, `state`, `resume`, `ratelimit`
(grep confirmed) — nothing to do for those.

Versions:
- Old: `agentkit@v0.2.1-0.20260324114043-fbf217a606af` (has bus/registry/tasks/heartbeat/…)
- New: `agentkit@v1.2.0` — the packages `bus`, `heartbeat`, `registry`, `tasks`, `results`,
  `state`, `resume`, `ratelimit`, `telemetry`, `transport`, `types`, `acp`, `auth`, `logging`,
  `security` are **gone** from the module (v1.2.0 ships only contentguard, credentials,
  embedding, errors, llm, mcp, memory, policy, shellguard, shutdown, tools).
- swarmkit: `go list -m -versions github.com/vinayprograms/swarmkit` -> **v1.0.0 only**
  (module cache also has pseudo-versions 20260411..20260419 and a `v1.0.1-0.20260419021047-95ebeae51fda`
  `.info`, but the proxy advertises only v1.0.0). Use v1.0.0.
- swarmkit requires `nats.go v1.50.0` (repo has v1.49.0 — will bump) and otel v1.43.0 (repo has
  v1.40.0 — will bump). swarmkit also pulls `nats-server/v2` as a *direct* dep (used in tests
  only, but it lands in the module graph).

Consumers (only these files import the old packages):
```
cmd/agent/serve.go            bus, heartbeat, registry, tasks   (+ nats.go directly)
cmd/swarm/main.go             tasks                              (+ nats.go directly)
cmd/swarm/wait.go             tasks                              (+ nats.go directly)
cmd/swarm/web.go              tasks                              (+ nats.go directly)
cmd/swarm/tui.go              (nats.go only, parses heartbeat JSON by hand)
internal/swarm/dispatch.go    bus, tasks
internal/swarm/replay.go      heartbeat, tasks                   (+ nats.go JetStream)
internal/swarm/replay_test.go heartbeat, tasks
internal/swarm/jetstream.go   (nats.go JetStream only)
internal/executor/swarmctx.go (no agentkit import; only comments mention heartbeat)
```

Package-name mapping:

| agentkit (old) | swarmkit (new) |
|---|---|
| `agentkit/bus` | `swarmkit/messaging` |
| `agentkit/tasks` (TaskMessage/TaskResult) | `swarmkit/task` (Message/Result) |
| `agentkit/heartbeat` | `swarmkit/heartbeat` |
| `agentkit/registry` | `swarmkit/registry` |

---

## 0. The three cross-cutting problems (read first)

### P1. swarmkit `messaging.Bus` does not expose the raw `*nats.Conn`
Old `bus.NATSBus.Conn()` is used by serve.go for (a) `swarm.EnsureStream(natsBus.Conn())`
(JetStream) and (b) `registry.NewNATSRegistry(conn, …)`. swarmkit's `messaging.NATS(cfg)`
returns the `Bus` interface; the concrete `natsBus` is unexported and has no `Conn()`.
There is also no `NewNATSBusFromConn` equivalent.

Options:
- **(A) Two connections**: `nats.Connect(url)` yourself for JetStream (+ keep `swarm.EnsureStream`
  / `EnsureWorkConsumer` / `Replay` on it), and `messaging.NATS(cfg)` for pub/sub + heartbeat.
  swarmkit's own `task` / `registry` each dial their own connection anyway, so "N connections per
  agent" is the swarmkit norm. Simplest; recommended.
- **(B) Write an in-repo `messaging.Bus` adapter over a `*nats.Conn` you own** (copy
  `agentkit/bus/nats.go` ~250 lines into `internal/swarm/natsbus.go`, implementing swarmkit's
  `Bus`/`Joiner`/`Subscription` so `heartbeat.NewSender` accepts it). One connection, more code.
- **(C) Drop `messaging.Bus` entirely** and publish via `*nats.Conn` directly; implement the
  heartbeat sender in-repo too. Maximum control, zero swarmkit `messaging` dependency.

### P2. JetStream stream overlap: repo `SWARM` vs swarmkit `TASKS`
Repo (`internal/swarm/jetstream.go`) owns stream **`SWARM`** with subjects
`discuss.>`, `work.>`, `done.>`, `heartbeat.>`, MemoryStorage, MaxAge 24h, LimitsPolicy.
swarmkit `task` owns stream **`TASKS`** with subjects `work.>`, `done.>`, FileStorage.
NATS forbids two streams claiming the same subject. **If you ever construct a
`task.NewWorker`/`task.NewDispatcher` against a server where `SWARM` exists, `ensureStream` fails
with "subjects overlap with an existing stream"** (and vice-versa). You cannot run both.
Decision needed: keep the repo's own JetStream layer (stream `SWARM`, RE-POINT nothing in
`task` beyond the envelope types) — or adopt swarmkit `task` and give up `SWARM`
(which would also kill `Replay`, since `TASKS` does not capture `discuss.>`/`heartbeat.>`).
**Recommendation: keep `SWARM` + in-repo pull consumer; use swarmkit `task` only for the
`Message`/`Result` envelope types (or not at all, see P3).**

### P3. Wire-format changes (JSON) — `agent serve` workers and `swarm` CLI/UI must agree
swarmkit renamed fields. Both sides live in this repo, so a coordinated switch is possible, but
there are hand-written JSON parsers (swarm CLI, TUI, web UI JS) that must change together.

Task message (`tasks.TaskMessage` -> `task.Message`):

| old JSON | new JSON | note |
|---|---|---|
| `task_id` | `id` | **rename** |
| `idempotency_key` | `idempotency` | rename |
| `parent_task_id` / `root_task_id` | `parent` / `root` | rename |
| `capability` | **gone** | routing is by subject only; repo reads `task.Capability` in serve.go, web.go, main.go (taskDB records) |
| `reply_to` | **gone** | serve.go honors `task.ReplyTo` for result subject |
| `timeout_seconds` | `timeout` | rename |
| `prior_outputs` | `prior` | rename |
| — | `version` (int, =1) | new, stamped by `NewMessage` |
| `max_attempts`,`attempt`,`inputs`,`created_at`,`submitted_at`,`submitted_by`,`tags`,`metadata`,`correlation_id` | same | unchanged |

Task result (`tasks.TaskResult` -> `task.Result`):

| old JSON | new JSON |
|---|---|
| `task_id` | `id` |
| `agent_id` | `agent` |
| `duration_ms` | `duration` |
| — | `version` |
| `status` (`success`/`failed`/`timeout`), `outputs`, `error`, `attempt`, `completed_at`, `correlation_id`, `metadata` | same |

Heartbeat (`heartbeat.Heartbeat` -> `heartbeat.Message`):

| old JSON | new JSON |
|---|---|
| `agent_id` | `agent` |
| `timestamp`,`status`,`load`,`metadata` | same |

Hand-rolled parsers that depend on the OLD names (must be updated if you adopt swarmkit types,
or must stay if you keep the old schema in-source):
- `cmd/swarm/main.go:1378` `discoverAgentsViaHeartbeat` — anonymous struct `json:"agent_id"`
- `cmd/swarm/tui.go:209` — anonymous struct `json:"agent_id"`
- `cmd/swarm/static/index.html` — 8 uses of `agent_id`, 2 of `task_id`, 6 of `duration_ms`
- `cmd/swarm/web.go:254` uses `result.AgentID != ""` as "is this a TaskResult?" discriminator
- `internal/swarm/replay.go:124` same discriminator on `result.AgentID`
- `cmd/swarm/main.go` taskDB `tasks/<id>.json` / `.input.json` files on disk use the old schema
  (existing `~/.swarm` state becomes unreadable after a schema switch — low stakes, but note it)

**Decision needed (biggest one):** adopt swarmkit envelope types and do the rename sweep
(Go + JS + on-disk), or copy `agentkit/tasks/message.go` + `agentkit/heartbeat/heartbeat.go`
into `internal/swarm/` and keep the existing wire schema byte-for-byte. The latter is ~300
lines of copied code and zero behavior risk; the former aligns with swarmkit but buys nothing
functionally because the repo does not use swarmkit `Dispatcher`/`Worker` (P2).

---

## 1. cmd/agent/serve.go

### 1.1 `bus` package

| Old symbol | Used at | Class | New |
|---|---|---|---|
| `bus.NATSConfig{URL, Name}` | :292 | RE-POINT | `messaging.NATSConfig{URL, Name}` — identical fields (`Config{BufferSize}`, URL, Name, Token, User, Password, ReconnectWait, MaxReconnects, ConnectTimeout). Zero-value `ReconnectWait`/`MaxReconnects`/`ConnectTimeout` are passed straight to nats options, so either start from `messaging.NATSDefaults()` and set URL/Name, or accept nats defaults of 0 (MaxReconnects=0 means **no reconnects** — note `DefaultNATSConfig()` in old bus was likewise not used here, so behavior is unchanged, but consider `NATSDefaults()`). |
| `bus.NewNATSBus(cfg)` -> `*bus.NATSBus` | :296 | RE-POINT | `messaging.NATS(cfg)` -> `messaging.Bus` (interface, not concrete) |
| `bus.MessageBus` (field `a.bus`) | :55 | RE-POINT | `messaging.Bus` |
| `bus.Subscription` (`taskSubs`, `instanceSub`, `discussSub`, `controlSub`) | :59-64 | RE-POINT | `messaging.Subscription` — same `Messages() <-chan *Message` / `Unsubscribe() error`, plus new `Dropped() uint64` |
| `bus.Message{Subject, Data}` | :482,499,505,511,559,595,733,772,803 | RE-POINT | `messaging.Message{Subject, Data, Reply}` — identical shape |
| `natsBus.Subscribe(subject)` | :428,438,449 | RE-POINT | `bus.Subscribe(subject)` — same |
| `natsBus.QueueSubscribe(subject, queue)` | :407 | RE-POINT (shape change) | `bus.Join(queue).Subscribe(subject)`. Queue group name is preserved verbatim (NATS queue group == pool name), so `cfg.Service.QueueGroup` / `"<cap>-workers"` stays wire-compatible with any old worker. |
| `natsBus.Publish(subject, data)` | :356,856,896 | RE-POINT | same on `messaging.Bus` |
| `natsBus.Close()` | :302 | RE-POINT | same (also `Shutdown(ctx)`) |
| `natsBus.Conn()` | :335 (`swarm.EnsureStream(natsBus.Conn())`), :660 (`registerWithRegistry`) | **DROPPED** | See P1. Open a second `*nats.Conn` (`nats.Connect(cfg.Service.BusURL, nats.Name(...))`) for JetStream (`swarm.EnsureStream`, `EnsureWorkConsumer`, `pullWorkLoop`) — or adapter (P1-B). |

Before / after:
```go
// before
cfg := bus.NATSConfig{URL: a.wf.cfg.Service.BusURL, Name: "agent-"+a.agentID}
natsBus, err := bus.NewNATSBus(cfg)
a.bus = natsBus
js, jsErr := swarm.EnsureStream(natsBus.Conn())
workSub, err := natsBus.QueueSubscribe("work.<cap>.*", a.queueGroup)

// after (option P1-A)
cfg := messaging.NATSDefaults()
cfg.URL, cfg.Name = a.wf.cfg.Service.BusURL, "agent-"+a.agentID
b, err := messaging.NATS(cfg)               // pub/sub + heartbeat
a.bus = b
nc, err := nats.Connect(cfg.URL, nats.Name(cfg.Name+"-js"))  // JetStream + replay
js, jsErr := swarm.EnsureStream(nc)
workSub, err := b.Join(a.queueGroup).Subscribe("work.<cap>.*")
```

### 1.2 `heartbeat` package

| Old symbol | Used at | Class | New |
|---|---|---|---|
| `*heartbeat.BusSender` (field) | :57 | RE-POINT | `heartbeat.Sender` (interface) |
| `heartbeat.NewBusSender(heartbeat.SenderConfig{Bus, AgentID, Interval, InitialStatus})` | :328 | RE-POINT (field renames) | `heartbeat.NewSender(heartbeat.SenderConfig{Bus: b, Agent: a.agentID, Interval: heartbeatInterval, Status: "idle"})`. **Starts immediately** on construction (no `Start(ctx)`), publishes first beat at once. |
| `hbSender.Start(ctx)` | :374 | DROPPED | implicit in `NewSender` — move `NewSender` to where you previously called `Start`, or accept that beats begin before the registry/JetStream setup finishes (old code started the loop after registration + metadata set; if you construct early, the first beat goes out with status "idle" and no metadata — the UI tolerates that). |
| `hbSender.Stop()` | :377 | RE-POINT | `sender.Close()` (or `Shutdown(ctx)`) |
| `SetStatus`, `SetLoad`, `SetMetadata` | :338-343, :815-818, :838-841, :907 | RE-POINT | identical signatures |
| `hbSender.SetCallback(func())` | :363 | **DROPPED** | No per-tick hook in swarmkit. Used for registry `Touch` + re-register on TTL expiry. Replacement: either (a) run your own `time.Ticker(heartbeatInterval)` goroutine doing the touch/re-register, or (b) drop it — swarmkit registry has **no TTL** (entries persist until `Deregister`), so there is nothing to touch (see 1.3). Recommend (b) + ensure `Deregister` on every exit path, plus consider wiring `heartbeat.Monitor.Deaths()` -> `Deregister` in the swarm CLI for crash cleanup. |
| `heartbeat.NewMetricsCollector(hbSender)` -> `*MetricsCollector` | :346 | **IN-SOURCE** | Gone from swarmkit. Copy `agentkit@old/heartbeat/metrics.go` (85 lines, only depends on an interface with `SetMetadata(k,v)`) to `internal/swarm/metrics.go` as `swarm.NewMetricsCollector(sender heartbeat.Sender)`. It already satisfies `executor.MetricsCollector` (`RecordLLMCall`, `RecordSupervision`, `SetSubagents`). Metadata keys emitted (`tokens_in`, `tokens_out`, `cache_creation_tokens`, `cache_read_tokens`, `llm_calls`, `subagents`, `sup_approved`, `sup_denied`, `avg_latency_ms`) are consumed by the web UI — keep them identical. |
| Heartbeat wire subject | — | same | both publish to `heartbeat.<agent>`; swarmkit Monitor subscribes `heartbeat.*` (repo uses `heartbeat.>` — equivalent for single-token IDs). |
| Heartbeat wire JSON | — | **CHANGED** | `agent_id` -> `agent` (P3). Affects swarm CLI/TUI/web UI parsers and `replay.go`. |
| Default interval | — | same | 5s both. |

### 1.3 `registry` package

| Old symbol | Used at | Class | New |
|---|---|---|---|
| `registry.Registry` (field `a.reg`) | :58 | RE-POINT | `registry.Registry` (interface: `Register(Agent)`, `Deregister(id)`, `Get(id)`, `List(*Filter)`, `Shutdown`, `Close`) |
| `registry.DefaultNATSRegistryConfig()` + `registry.NewNATSRegistry(conn, cfg)` | :665-666 | RE-POINT (constructor shape) | `registry.New(registry.Config{NATS: messaging.NATSConfig{URL: ..., Name: ...}})` — dials its **own** connection; no `*nats.Conn` parameter. Removes the need for `natsBus.Conn()` here. |
| `registry.AgentInfo{ID, Name, Capabilities []string, Status, Load, Metadata, LastSeen}` | :62, :673, :695 | RE-POINT (shape change) | `registry.Agent{ID, Name, Description, Version, Skills []Skill, Metadata, LastSeen}`. `Capabilities []string` -> `Skills: []registry.Skill{{ID: cap, Name: cap}}`. `Status`/`Load` have **no home** — swarmkit says runtime state lives in heartbeat. Move `version` from Metadata to `Agent.Version` (or keep both). |
| `registry.StatusIdle` | :677 | DROPPED | no status in registry; heartbeat carries it already. |
| `registry.ErrNotFound` | :366 | RE-POINT | `registry.ErrNotFound` exists, but the `Touch` path that produced it is gone. |
| `reg.Touch(id)` | :364 | **DROPPED** | swarmkit registry has no TTL (`CreateOrUpdateKeyValue{Bucket:"AGENTS", Replicas:1}`, no TTL) and no `Touch`. Either drop the whole touch/re-register machinery (`a.agentInfo`, `reRegister()`, the SetCallback block) or re-`Register` periodically from your own ticker (Register is an upsert, updates LastSeen). |
| `reg.Register(info)` / `reg.Deregister(id)` | :689, :913 | RE-POINT | same names; `Register` takes `registry.Agent` |
| `registry.CapabilitySchema{Name, Description, Inputs, Outputs}` / `registry.FieldSchema{Name, Required, Default, Type}` | :40, :1026-1060, HTTP `/capability` handler :213 | RE-POINT (rename) or IN-SOURCE | swarmkit `registry.Skill{ID, Name, Description, Tags, InputModes, OutputModes, Inputs []Param, Outputs []Param}` / `registry.Param{Name, Required, Default, Type, Description}` — same field set for Param. `extractCapabilitySchema` becomes `extractSkill` returning `registry.Skill{ID: name, Name: name, Description: ..., Inputs: ..., Outputs: ...}`. **Side effect**: the HTTP `/capability` JSON now includes `id` and omits `version`; anyone scraping that endpoint sees a different document (nothing in-repo consumes it). Alternative: copy `agentkit@old/registry/capability.go` (~100 lines) to `internal/swarm/capability.go` to keep the endpoint identical. |
| KV bucket name | — | **CHANGED** | old default `agent-registry` (TTL 30s); new hard-coded **`AGENTS`**, no TTL. Old bucket is simply orphaned on existing servers; not an overlap problem (KV buckets are streams named `KV_<bucket>` with subjects `$KV.<bucket>.>`). |
| `DefaultNATSRegistryConfig().TTL = 30s` semantics | — | **SEMANTIC** | Old: entries self-expire after 30s without Touch (crash = auto-cleanup). New: entries **persist forever** until `Deregister`. A worker killed with SIGKILL leaves a stale `AGENTS` entry. Nothing in-repo calls `reg.List` today (`swarm` CLI discovers via heartbeats), so the practical impact is low, but decide whether to (a) wire `heartbeat.Monitor.Deaths()` -> `Deregister` somewhere (swarm CLI `up`/`ui`), or (b) drop registry registration from `agent serve` entirely — it is write-only in this repo. **Decision needed.** |

Before / after:
```go
// before
regCfg := registry.DefaultNATSRegistryConfig()
natsReg, err := registry.NewNATSRegistry(natsBus.Conn(), regCfg)
info := registry.AgentInfo{ID: a.agentID, Name: a.capability.Name,
    Capabilities: a.getCapabilities(), Status: registry.StatusIdle, Load: 0,
    Metadata: map[string]string{"version": version, "instance_id": a.instanceID, "type": a.agentType}}
natsReg.Register(info)
// ... later, via heartbeat callback:
a.reg.Touch(a.agentID) // re-register on ErrNotFound

// after
reg, err := registry.New(registry.Config{NATS: cfg}) // cfg is the messaging.NATSConfig
a.reg = reg
skill := a.capability // now a registry.Skill, see extractSkill
reg.Register(registry.Agent{ID: a.agentID, Name: a.displayName, Version: version,
    Skills: []registry.Skill{skill},
    Metadata: map[string]string{"instance_id": a.instanceID, "type": a.agentType, "capability": skill.ID}})
// no Touch; Deregister on shutdown (already done in initiateBusShutdown)
```

### 1.4 `tasks` package

| Old symbol | Used at | Class | New |
|---|---|---|---|
| `tasks.TaskMessage` | :47, :231, :571, :716, :805, :948 | RE-POINT or IN-SOURCE (P3) | `task.Message` |
| `.TaskID` | many | rename | `.ID` |
| `.Capability` | :718 (`buildTaskText`) | **DROPPED** | not on `task.Message`. Derive from subject (`work.<cap>.<id>` — `extractTaskIDFromSubject` sibling) or carry in `Metadata["capability"]`. |
| `.ReplyTo` | :853 | **DROPPED** | not on `task.Message`. Default `done.<cap>.<id>` stays; if you want explicit reply routing keep it in `Metadata["reply_to"]`. |
| `.Inputs`, `.Metadata`, `.SubmittedBy`, `.Attempt`, `.CorrelationID` | various | same | same |
| `task.Validate()` | :237 | RE-POINT | `msg.Validate()` (ID non-empty; nil Inputs -> empty map). Old also required `Capability` non-empty — that check is gone. |
| `tasks.UnmarshalTaskMessage(data)` | :571, :805 | RE-POINT | `task.UnmarshalMessage(data)` |
| `tasks.TaskResult` | :606, :948 | RE-POINT | `task.Result` |
| `tasks.NewTaskResult(taskID, agentID, tasks.ResultSuccess)` | :961 | RE-POINT | `task.NewResult(id, agent, task.StatusSuccess)` |
| `tasks.ResultFailed` / `ResultSuccess` / `ResultStatus` | :248, :863, :869, :978, :982 | RE-POINT | `task.StatusFailed` / `task.StatusSuccess` / `task.Status` (string values `failed`/`success`/`timeout` unchanged) |
| `result.AgentID`, `.TaskID`, `.DurationMs` | :609, :873 | rename | `.Agent`, `.ID`, `.Duration` |
| `result.Marshal()` | :847 | same | same |

Result subject: repo publishes to `done.<capability>.<task_id>` (3 tokens). swarmkit `task.Worker`
would publish to `done.<task_id>` (2 tokens) and its Dispatcher subscribes `done.>`. Since you are
not using `task.Worker`, keep `done.<cap>.<id>`; the swarm CLI subscribes `done.*.<id>` and the
web UI parses `parts[len-1]`, so both remain correct.

### 1.5 nats.go direct usage in serve.go (unchanged, RE-POINT nothing)
`nats.JetStreamContext`, `*nats.Subscription`, `Fetch(1, nats.MaxWait(5s))`, `nats.ErrTimeout`,
`nats.ErrSubscriptionClosed`, `Ack()`, `Nak()` — all remain valid with nats.go v1.50.0.
Only change: the `*nats.Conn` must come from somewhere other than `natsBus.Conn()` (P1).

---

## 2. internal/swarm/dispatch.go

| Old | Class | New |
|---|---|---|
| `bus.MessageBus` field/param in `NewDispatchTool(b bus.MessageBus, ...)` | RE-POINT | `messaging.Bus` |
| `tasks.TaskMessage{TaskID, Capability, Inputs, Attempt, SubmittedBy, SubmittedAt}` | RE-POINT | `task.NewMessage(taskID, inputs)` then set `Attempt=1`, `SubmittedBy`, `SubmittedAt`; `Capability` field gone — put it in `Metadata["capability"]` if `buildTaskText` still wants it (subject already carries it). |
| `taskMsg.Marshal()` / `t.bus.Publish(subject, data)` | same | same |

Note: the manager publishes with core NATS `Publish`, not `js.Publish`; since `SWARM` captures
`work.>`, JetStream still stores it. Unchanged.

Alternative considered and rejected: replace `DispatchTool` internals with
`task.NewDispatcher(...).Start(ctx, cap, msg)` — blocked by P2 (stream overlap) and by the
`done.<id>` vs `done.<cap>.<id>` mismatch with `agent serve` workers.

---

## 3. internal/swarm/replay.go + replay_test.go

| Old | Class | New |
|---|---|---|
| `heartbeat.Unmarshal(data)` -> `*heartbeat.Heartbeat` | RE-POINT | `heartbeat.Unmarshal(data)` -> `*heartbeat.Message`; `hb.AgentID` -> `hb.Agent`. **Wire**: now expects `"agent"` key; old workers' `agent_id` heartbeats will unmarshal with empty Agent. |
| `heartbeat.Heartbeat{AgentID, Timestamp, Status, Metadata}` (tests) | RE-POINT | `heartbeat.Message{Agent, Timestamp, Status, Metadata}`; `hb.Marshal()` same |
| `tasks.TaskResult` + `json.Unmarshal` + `result.AgentID != ""` discriminator | RE-POINT | `task.Result` / `task.UnmarshalResult`; discriminator becomes `result.Agent != ""` |
| `result.CompletedAt`, `.Outputs`, `.Metadata["signal"]` | same | same |
| `tasks.UnmarshalTaskMessage` | RE-POINT | `task.UnmarshalMessage` |
| `task.SubmittedBy`, `task.Inputs["goal"]` | same | same |
| `nats.JetStreamContext`, `js.SubscribeSync(">", nats.DeliverAll(), nats.AckNone(), nats.BindStream(StreamName))` | unchanged | unchanged; still needs a `*nats.Conn` (P1) |

If you choose IN-SOURCE for envelopes (P3-B), replay.go/test just re-point the import to
`internal/swarm` types and nothing else changes.

---

## 4. internal/swarm/jetstream.go (no agentkit import; but semantically affected)

Unchanged code. Semantic notes vs swarmkit `task`:
- Stream `SWARM` (MemoryStorage, 24h, subjects discuss/work/done/heartbeat) vs swarmkit `TASKS`
  (FileStorage, no MaxAge, work/done). Overlap -> mutually exclusive (P2).
- Durable consumer name `work-<cap>` with `AckExplicit, MaxDeliver(3), AckWait(10m), DeliverNew`
  — swarmkit uses the **same consumer name and same ack settings** but **without `DeliverNew`**
  (it will replay backlog on first bind). Not an interaction issue since streams differ.
- `LastSequence` / `Replay` have no swarmkit equivalent — keep.

---

## 5. cmd/swarm/main.go

| Old | Lines | Class | New |
|---|---|---|---|
| `tasks.TaskMessage{TaskID, Capability, Inputs, Attempt, SubmittedAt}` (SubmitCmd, pipeline) | :353, :467, :980 | RE-POINT | `task.NewMessage(taskID, inputs)` + `Attempt=1`, `SubmittedAt`; `Capability` dropped from envelope (still used for subject + `taskRecord.Capability` — take it from the local variable) |
| `task.Marshal()` | :361, :475, :986 | same | same |
| `tasks.TaskResult` + `json.Unmarshal` | :1016, :1170 | RE-POINT | `task.Result` / `task.UnmarshalResult` |
| `result.Status == tasks.ResultFailed` | :1021 | RE-POINT | `task.StatusFailed` |
| `printResult(r *tasks.TaskResult)` reads `.TaskID`, `.Status`, `.Outputs`, `.Error`, `.DurationMs` | :521-535 | rename | `.ID`, `.Duration`. The printed JSON (`task_id`, `duration_ms`) is the CLI's own output struct — keep those output keys for users. |
| `taskDB.InsertTask(*tasks.TaskMessage)`, `GetTask`, `UpdateResult(*tasks.TaskResult)`, `GetResult` | :1088-1175 | RE-POINT | swap types; `task.TaskID`->`ID`, `task.Capability` -> pass capability explicitly (signature change: `InsertTask(task *task.Message, capability, status string)`), `result.TaskID`->`ID`, `result.DurationMs`->`Duration`. On-disk `tasks/<id>.input.json` / `<id>.json` schema changes (P3). |
| `tasks.UnmarshalTaskMessage` | :1129 | RE-POINT | `task.UnmarshalMessage` |
| `discoverAgentsViaHeartbeat` anonymous struct `json:"agent_id"` | :1378 | **WIRE** | change to `json:"agent"` (or use `heartbeat.Unmarshal` and read `.Agent`). |
| `control.<id>.shutdown`, `control.swarm.shutdown`, `log.<name>` | :778, :841, :1405 | unchanged | no swarmkit concept; keep raw `nc.Publish` |
| `nats.Connect`, `SubscribeSync`, `NextMsgWithContext` | various | unchanged | nats.go v1.50 compatible |

Could `SubmitCmd` use `task.NewDispatcher().Run`? No — P2 (stream overlap with `SWARM`) and
`done.<id>` vs `done.<cap>.<id>`. Keep raw publish + `waitForResult`.

---

## 6. cmd/swarm/wait.go

| Old | Class | New |
|---|---|---|
| `tasks.TaskResult` + `json.Unmarshal(msg.Data, &result)` | RE-POINT | `task.UnmarshalResult(msg.Data)` |
| heartbeat liveness by draining `heartbeat.>` raw | optional RE-POINT | could become `heartbeat.NewMonitor(MonitorConfig{Bus, Timeout: 30s})` + `Deaths()`; but that needs a `messaging.Bus` while the rest of the CLI holds a `*nats.Conn` (P1 again). Simplest: leave as-is (it never decodes the payload). |

---

## 7. cmd/swarm/web.go

| Old | Lines | Class | New |
|---|---|---|---|
| `tasks.UnmarshalTaskMessage` | :181, :295 | RE-POINT | `task.UnmarshalMessage`; `tm.Inputs`, `tm.Metadata` same |
| `tasks.TaskResult` + `json.Unmarshal` | :204, :254 | RE-POINT | `task.UnmarshalResult`; `result.TaskID`->`ID`, `result.AgentID`->`Agent` (incl. the `!= ""` discriminator at :255) |
| `&tasks.TaskMessage{TaskID, Capability, Inputs, Attempt, SubmittedAt[, Metadata, SubmittedBy]}` | :484, :512, :703 | RE-POINT | `task.NewMessage(id, inputs)` + fields; `Capability` dropped |
| `s.db.InsertTask(tm, status)` | :186, :494, :522 | follows main.go signature change |
| `cacheMessage`/`sendInitialState` forward raw bytes to the browser | :365+ | **WIRE** | `static/index.html` parses `agent_id` (8x), `task_id` (2x), `duration_ms` (6x) from raw NATS payloads — must be updated to `agent`/`id`/`duration` if envelopes change (P3). |
| Subjects subscribed: `heartbeat.>`, `work.>`, `done.>`, `discuss.>`, `control.>`, `log.>`, `events.>` | :91 | unchanged | swarmkit has no opinion on `discuss/control/log/events` |

---

## 8. cmd/swarm/tui.go

No agentkit import. Line :209 hand-parses heartbeat with `json:"agent_id"` -> `json:"agent"`
under P3-A. Everything else is raw nats.go.

---

## 9. internal/executor/swarmctx.go

No agentkit import; only comments reference heartbeat. `executor.MetricsCollector` interface is
the repo's own — the in-sourced `MetricsCollector` (1.2) satisfies it. No change.

---

## 10. Subject / naming scheme summary (repo vs swarmkit)

| Concern | repo today | swarmkit v1.0.0 | Verdict |
|---|---|---|---|
| work subject | `work.<cap>.<taskID>` | `work.<cap>.<taskID>` | same |
| result subject | `done.<cap>.<taskID>` | `done.<taskID>` | **differs**; keep repo's (not using task.Worker) |
| heartbeat subject | `heartbeat.<agentID>` | `heartbeat.<agent>` | same |
| discuss / control / log / events | `discuss.<id>`, `control.<id>.shutdown`, `log.<name>`, `events.<name>` | n/a | repo-owned, keep |
| JetStream stream | `SWARM` (mem, 24h, 4 subject families) | `TASKS` (file, work/done) | **conflict** (P2); keep `SWARM` |
| pull consumer | `work-<cap>`, AckExplicit/MaxDeliver 3/AckWait 10m/DeliverNew | `work-<cap>`, same minus DeliverNew | keep repo's |
| queue group fallback | `<cap>-workers` (or cfg) | `Join(pool)` = same NATS queue group | RE-POINT, wire-identical |
| registry bucket | `agent-registry`, TTL 30s, Touch | `AGENTS`, no TTL, no Touch | **semantic change** |
| heartbeat interval / monitor timeout | 5s / CLI uses 30s | 5s / Monitor default 15s | same defaults |
| task JSON | `task_id`,`capability`,`reply_to`,... | `id`, no capability/reply_to, `version` | **wire change** (P3) |
| result JSON | `task_id`,`agent_id`,`duration_ms` | `id`,`agent`,`duration`,`version` | **wire change** (P3) |
| heartbeat JSON | `agent_id` | `agent` | **wire change** (P3) |

---

## 11. IN-SOURCE candidates (what to copy, where from, where to)

| Capability | Copy from (old agentkit) | Suggested home | Size |
|---|---|---|---|
| `MetricsCollector` (always needed) | `heartbeat/metrics.go` | `internal/swarm/metrics.go` | 85 lines |
| Task envelope w/ old wire schema (if P3-B) | `tasks/message.go` (`TaskMessage`, `TaskResult`, `ResultStatus`, `NewTaskMessage`, `NewTaskResult`, `Unmarshal*`, `Marshal`, `Validate`) | `internal/swarm/message.go` | ~150 lines |
| Heartbeat envelope w/ old wire schema (if P3-B) | `heartbeat/heartbeat.go` (`Heartbeat`, `Marshal`, `Unmarshal`, `Subject`) | `internal/swarm/heartbeat.go` | ~40 lines |
| Heartbeat sender with `SetCallback` (only if keeping Touch semantics or going P1-C) | `heartbeat/sender.go` (`BusSender`) | `internal/swarm/heartbeat.go` | ~190 lines |
| `CapabilitySchema`/`FieldSchema` (only if you want `/capability` JSON unchanged) | `registry/capability.go` | `internal/swarm/capability.go` | ~100 lines |
| NATS bus adapter exposing `Conn()` (only if P1-B) | `bus/nats.go` | `internal/swarm/natsbus.go` | ~250 lines |
| Registry w/ TTL+Touch (only if you insist on auto-expiry) | `registry/nats.go` | `internal/swarm/registry.go` | ~300 lines; probably not worth it |

---

## 12. Decisions needed (ordered)

1. **Envelope types**: adopt swarmkit `task.Message/Result` + `heartbeat.Message` (rename sweep
   across Go + `static/index.html` + on-disk task files; `capability`/`reply_to` fields lost) —
   or in-source the old `tasks`/`heartbeat` envelope code and keep the wire identical (P3).
2. **JetStream**: keep repo-owned `SWARM` stream/pull consumer/replay (recommended) — swarmkit
   `task.Worker/Dispatcher` cannot coexist with it (P2).
3. **Connection topology**: second `*nats.Conn` for JetStream (P1-A, recommended) vs in-repo
   `messaging.Bus` adapter over one conn (P1-B) vs drop `messaging` (P1-C).
4. **Registry**: keep registering with swarmkit registry (bucket `AGENTS`, no TTL, stale entries on
   crash, drop Touch/re-register machinery) — or remove registry from `agent serve` (it is
   write-only in this repo) — or wire `heartbeat.Monitor.Deaths()` -> `Deregister` in the CLI.
5. **`/capability` HTTP endpoint** shape: switch to `registry.Skill` JSON or in-source the old
   `CapabilitySchema`.
6. Go deps: accept nats.go 1.49 -> 1.50 and otel 1.40 -> 1.43 bumps (plus `nats-server/v2` in the
   graph) that come with swarmkit.
