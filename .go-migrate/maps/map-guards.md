# Migration map: `security` / `logging` / `telemetry` (agentkit v0.2.1-fbf217a → v1.2.0)

Scope: the removed/replaced agentkit packages. `auth`, `transport`, `types` are **not imported anywhere** in the repo (grep of all `*.go` incl. tests, `cmd/swarm`, `cmd/agentmem`, `cmd/replay`) — nothing to do for those three.

Consumer packages affected (only these import the three packages):

| Repo package | `security` | `logging` | `telemetry` |
|---|---|---|---|
| `cmd/agent` (`runtime.go`, `workflow.go`, `runtime_test.go`) | yes | – | yes |
| `internal/executor` (`executor.go`, `config.go`, `logging.go`, `tracing.go`) | yes | yes | yes |
| `internal/supervision` (`supervisor.go`, `pipeline.go`) | – | yes | – |

Also in scope because it moved **out of `policy`/`tools` into `shellguard`**: the bash checker wiring in `cmd/agent/runtime.go` (`policy.NewBashChecker`, `policy.NewSmallLLMChecker`, `policy.LLMProviderFromChatProvider`, `registry.SetBashChecker`, `SetBashLLMChecker`, `SetBashSecurityCallback`) and `cmd/agent/bridges_test.go`.

Source references:
- OLD: `/Users/vinay/go/pkg/mod/github.com/vinayprograms/agentkit@v0.2.1-0.20260324114043-fbf217a606af/`
- NEW: `/Users/vinay/go/pkg/mod/github.com/vinayprograms/agentkit@v1.2.0/`

---

## 0. New-API cheat sheet (v1.2.0)

### `contentguard` (replaces `security.Verifier` + triage + supervisor + patterns + entropy)

```go
type Config struct {
    Context  map[string]string // flows to stages; key "scope" = research scope
    Patterns []string          // custom "name:regex"
    Keywords []string          // custom keywords
    Skip     []string          // tool names that skip verification
}
func New(stages []Stage, workflow Workflow, cfg Config) (*Guard, error)
func (g *Guard) Check(ctx, toolName string, args map[string]any, originalGoal string) (*Result, error)
func (g *Guard) Ingest(trust Trust, kind Kind, mutable bool, text, source string) *Content
func (g *Guard) IngestWithLineage(trust, kind, mutable, text, source string, originIDs []string) *Content
func (g *Guard) UntrustedIDs() []string
func (g *Guard) Find(id string) *Content
func (g *Guard) ClearContext()
func (g *Guard) Close()           // no-op
func Escalatory() Workflow        // stop on first allow/deny/modify; all-escalate => Deny
func Paranoid() Workflow          // run ALL stages; any deny/modify => stop; else Allow
func NewScreener(m llm.Model) *Screener   // YES/NO triage → Escalate/Allow
func NewReviewer(m llm.Model) *Reviewer   // ALLOW/DENY/MODIFY
func ShannonEntropy(s string) float64
func IsHighEntropy(s string) bool

type Trust string   // Trusted="trusted", Vetted="vetted", Untrusted="untrusted"
type Kind string    // Instruction="instruction", Data="data"
type Verdict string // Allow, Deny, Modify, Escalate (Escalate only inside Finding)
type Content struct { ID string; Trust Trust; Kind Kind; Mutable bool; Text, Source string; Origins []*Content }
type Finding struct { Verdict Verdict; Rationale, Source string; Latency time.Duration }
type Result  struct { Verdict Verdict; Rationale, ToolName string; Findings []*Finding; Related []RelatedContent }
type RelatedContent struct { ID string; Trust Trust }
type Stage interface { Evaluate(ctx, Request) (*Finding, error) }
type Request struct { ToolName string; ToolArgs map[string]any; Untrusted []*Content; OriginalGoal string; PriorFindings []*Finding; Context map[string]string }
```

### `shellguard` (replaces `policy.BashChecker` + `policy.SmallLLMChecker`)

```go
func New(shell Shell, workspace string, allowedDirs, userDeniedCommands []string, model llm.Model, securityScope string) *Gate
func (g *Gate) Check(ctx context.Context, args tools.Args) error  // implements tools.Guard
func (g *Gate) CheckDeterministic(command string) (bool, string)
// field:
OnDecision func(command, step string, allowed bool, reason string, durationMs int64, inputTokens, outputTokens int)
func Bash() Shell  // also Posix(), Fish()  — NOTE README says BashShell{}; the source exports Bash()
var BannedCommands []string; var BannedSubcommandPatterns []BannedSubcommand; var DangerousPipePatterns []*regexp.Regexp
```
Wired as `reg.Register(tools.New(tools.Bash(workspace)).With(gate))`.

---

## 1. `cmd/agent`

### 1.1 `runtime.go` — security verifier (lines 52, 318–348, 488–506)

| Old symbol | Classification | New |
|---|---|---|
| `*security.Verifier` (field `secVerifier`) | RE-POINT | `*contentguard.Guard` |
| `security.NewVerifier(security.Config{Mode, ResearchScope, UserTrust, TriageProvider, SupervisorProvider}, sessionID)` | RE-POINT (shape change) | `contentguard.New(stages, workflow, contentguard.Config{...})` — **no sessionID, no mode enum, no user-trust** |
| `security.Mode`, `ModeDefault/ModeParanoid/ModeResearch` | DROPPED (no enum) | Mode becomes: workflow choice (`Escalatory()` vs `Paranoid()`) + `Config.Context["scope"]` for research. Keep a repo-local enum in `cmd/agent` (or `internal/config`) — see decision D1. |
| `security.TrustLevel`, `TrustUntrusted/TrustTrusted/TrustVetted` | RE-POINT | `contentguard.Trust`, `contentguard.Untrusted/Trusted/Vetted` |
| `security.Config.UserTrust` | DROPPED | Old verifier stored `userTrust` but **never read it** (`verifier.go:48,82` only). Behavioural no-op → keep parsing it in `determineSecurityConfig` only if you want to preserve the startup log line, otherwise delete. |
| `verifier.Destroy()` (closer at line 335) | RE-POINT | `guard.Close()` (no-op). If the audit trail is re-homed in-repo (D3), call its `Destroy()` here instead. |
| `verifier.AuditTrail()` | not used by repo | — |

Before (runtime.go:318–348):
```go
verifier, verErr := security.NewVerifier(security.Config{
    Mode: mode, ResearchScope: scope, UserTrust: userTrust,
    TriageProvider: triageProvider, SupervisorProvider: rt.provider,
}, rt.sess.ID)
...
rt.addCloser(func() { verifier.Destroy() })
```
After:
```go
var stages []contentguard.Stage
if triageProvider != nil && mode != secModeParanoid { // old: paranoid skipped tier-2
    stages = append(stages, contentguard.NewScreener(triageProvider))
}
if rt.provider != nil {
    stages = append(stages, contentguard.NewReviewer(rt.provider))
}
wf := contentguard.Escalatory()
if mode == secModeParanoid { wf = contentguard.Paranoid() }   // see semantic note S2
cfg := contentguard.Config{
    Skip: []string{ /* low-risk tools */ "read","glob","grep","list_files","memory_read", ...}, // see S1
}
if mode == secModeResearch && scope != "" {
    cfg.Context = map[string]string{"scope": scope}
}
// custom patterns/keywords come from policy at construction time (was a global registration — see 1.2)
if rt.pol.Content != nil && rt.pol.Content.Security != nil {
    cfg.Patterns = rt.pol.Content.Security.Patterns
    cfg.Keywords = rt.pol.Content.Security.Keywords
}
guard, err := contentguard.New(stages, wf, cfg)
...
rt.addCloser(guard.Close)
```
`llm.Provider` → `llm.Model` is the llm-package agent's concern; `NewScreener/NewReviewer` take `llm.Model` (`Chat(ctx, ChatRequest) (*ChatResponse, error)`).

`determineSecurityConfig()` (lines 488–506) and `runtime_test.go:56–120`: keep the function, change its return type to a **repo-local** mode enum + `contentguard.Trust` (or drop trust). Tests re-point `security.ModeDefault` → local const, `security.TrustUntrusted` → `contentguard.Untrusted`.

### 1.2 `workflow.go:197–210` — `registerSecurityExtensions`

| Old | Classification | New |
|---|---|---|
| `security.RegisterCustomPatterns([]string) error` (process-global) | RE-POINT → `contentguard.Config.Patterns` | same `"name:regex"` format, same error text; validation now happens in `contentguard.New` |
| `security.RegisterCustomKeywords([]string)` | RE-POINT → `contentguard.Config.Keywords` | |

The function body goes away; the values flow into `contentguard.New` in `runtime.go` (1.1). Note the policy field moved too: old `pol.Security.ExtraPatterns/ExtraKeywords` (`[security] extra_patterns`) → new `pol.Content.Security.Patterns/Keywords` (`[content.security] patterns/keywords`). v1.2.0 `FromTOML` **silently drops** the legacy `[security].extra_*` keys (`policy.go:108-111`); use `FromTOMLWithUnknownKeys` to warn users. (Policy-key migration itself belongs to the policy agent; flagged here because it feeds the guard.)

### 1.3 `runtime.go` — bash checker → `shellguard` (lines 45, 186–203, 340–342, 413)

| Old | Classification | New |
|---|---|---|
| `policy.NewBashChecker(rt.pol, bashPolicy.Denylist)` | RE-POINT | `shellguard.New(shellguard.Bash(), workspace, rt.pol.GetAllowedDirs(), denied, model, scope)` |
| `policy.NewSmallLLMChecker(policy.LLMProviderFromChatProvider(rt.smallLLM))` | DROPPED (folded in) | pass `rt.smallLLM` as the `model` arg (`llm.Model`); `nil` = deterministic only |
| `policy.LLMProviderFromChatProvider` (also `bridges_test.go:28,41`) | DROPPED | delete the adapter tests or retarget them at `shellguard` with a fake `llm.Model` |
| `bashLLMChecker.SetSecurityScope(scope)` (line 341) | DROPPED | scope is a **constructor arg** → build the gate *after* `determineSecurityConfig()` (today the registry is built at line 181 before security at 318 — reorder) |
| `registry.SetBashChecker / SetBashLLMChecker` | DROPPED | `reg.Register(tools.New(tools.Bash(ws)).With(gate))` |
| `registry.SetBashSecurityCallback(rt.exec.LogBashSecurity)` (line 413) | RE-POINT | `gate.OnDecision = rt.exec.LogBashSecurity` — identical signature |
| `bashPolicy.Denylist` (`[tools.bash] denylist`) | DROPPED from policy | v1.2.0 `FromTOML` ignores `denylist`. Repo must read it itself (e.g. via `FromTOMLWithUnknownKeys` + own struct) and pass as `userDeniedCommands`. |

Semantic deltas for bash (see S7): old skipped the LLM step when `len(allowedDirs)==0`; new always runs the LLM if `model != nil`. Old fallback to `policy.CheckCommand` when no checker — new has no fallback: if you don't attach a gate, bash is unguarded.

### 1.4 `runtime.go` — telemetry (lines 46–47, 231–276, 534–594)

| Old | Classification | New |
|---|---|---|
| `telemetry.Exporter` / `NewExporter(protocol, endpoint)` / `NewNoopExporter()` / `.LogEvent(name, map)` / `.Close()` | IN-SOURCE or DROPPED | Used for 12 `LogEvent` calls (subagent_start/complete, goal_started/complete, tool_call/error, mcp_tool_call, skill_loaded, supervision_*, llm_error). Options: (a) DROP — these are already session events + OTel spans; (b) copy `OLD/telemetry/telemetry.go` (`Exporter` iface, `HTTPExporter` 74–159, `FileExporter` 161–211, `NoopExporter` 214–224, ~220 lines, stdlib only) into `internal/telemetry/exporter.go`. The code comment already calls it "Legacy exporter (for backwards compatibility)". → decision D4. |
| `*telemetry.Provider`, `telemetry.InitProvider(ctx, ProviderConfig{ServiceName, ServiceVersion, Endpoint, Protocol, Insecure, Headers, Debug})`, `.Shutdown(ctx)` | IN-SOURCE | copy `OLD/telemetry/provider.go:21–190` into `internal/telemetry/provider.go` (drop the `NewTracer/SetGlobalTracer` lines 160–163; keep `otel.SetTracerProvider` + `SetTextMapPropagator`). Needs `go.opentelemetry.io/otel/sdk`, `sdk/resource`, `semconv`, `exporters/otlp/otlptrace/otlptracegrpc` and `otlptracehttp` promoted from `// indirect` to direct in `go.mod` (they're already in the graph at v1.40.0). |
| `ProviderConfig.Debug` | IN-SOURCE | the only consumer is `Tracer.Debug()` in `internal/executor/tracing.go` → keep a `debug bool` on the repo's tracer wrapper or pass `debug` into `executor.Config`. |

Before (runtime.go:255–264): `telemetry.InitProvider(ctx, telemetry.ProviderConfig{...})` → After: `otelinit.Init(ctx, otelinit.Config{...})` in `internal/telemetry` (same fields). The v1.2.0 kit packages (`contentguard`, `shellguard`, `llm`, `tools`, `mcp`) emit spans via `otel.Tracer(...)` so they pick up the global provider automatically — nothing else to wire.

---

## 2. `internal/executor`

### 2.1 `config.go:50` / `executor.go:136,293`

`SecurityVerifier *security.Verifier` → `ContentGuard *contentguard.Guard` (RE-POINT). Consider the consumer-defined-interface pattern the kit now favours:
```go
type contentGuard interface {
    Check(ctx context.Context, tool string, args map[string]any, goal string) (*contentguard.Result, error)
    IngestWithLineage(trust contentguard.Trust, kind contentguard.Kind, mutable bool, text, source string, origins []string) *contentguard.Content
    Find(id string) *contentguard.Content
}
```

### 2.2 `executor.go:387–470` `verifyToolCall`

| Old | New |
|---|---|
| `v.VerifyToolCall(ctx, tool, args, goal, agentContext) (*VerificationResult, error)` | `g.Check(ctx, tool, args, goal) (*Result, error)` — **no agentContext** (S4) |
| `result.Allowed bool` | `result.Verdict == contentguard.Allow` |
| `result.DenyReason` / `result.Modification` | `result.Rationale` (Modify verdict: rationale *is* the suggestion) |
| `result.Tier1.{Pass, Reasons, SkipReason, Block, RelatedBlocks}` | `result.Findings[0]` (always deterministic, `Source=="deterministic"`): `Pass` ⇔ `Verdict==Allow`; `Reasons` ⇔ `strings.Split(Rationale, ", ")` (`"tool:bash, pattern:override, keyword:password, encoded_content, suspicious_args"`); `SkipReason` ⇔ Rationale when Allow (`"no untrusted content"` / `"skipped tool: X"`); `Block` — **gone** (no primary-block selection; S5); `RelatedBlocks` ⇔ `result.Related` (all untrusted content, not arg-correlated; S5) |
| `result.Tier2.{Suspicious, LatencyMs, InputTokens, OutputTokens}` | finding with `Source=="screener"`: `Suspicious` ⇔ `Verdict==Escalate`; `LatencyMs` ⇔ `Latency.Milliseconds()`; **tokens gone** (S6) |
| `result.Tier3.{Verdict, Reason, LatencyMs, tokens}` | finding with `Source=="reviewer"`: `Verdict` (lower-case `allow/deny/modify` vs old `ALLOW/DENY/MODIFY` — session JSON changes unless you upper-case), `Reason` ⇔ `Rationale`; tokens gone |
| `result.TaintLineage []*TaintLineageNode` | **gone** — rebuild in-repo from `result.Related` + `g.Find(id).Origins` (see 2.4) |
| `checkPath` strings (`static→triage→supervisor`) | derive from `Findings[i].Source` sequence |

Sketch:
```go
res, err := e.guard.Check(ctx, toolName, args, e.currentGoal)
det := res.Findings[0]
var related []string
for _, r := range res.Related { related = append(related, r.ID) }
e.logSecurityStatic(toolName, "", related, det.Verdict == contentguard.Allow, splitReasons(det), skipReason(det), e.lineageFor(related))
for _, f := range res.Findings[1:] {
    switch f.Source {
    case "screener": e.logSecurityTriage(toolName, "", f.Verdict == contentguard.Escalate, "triage", f.Latency.Milliseconds(), 0, 0, ternary(f.Verdict==contentguard.Allow, "triage_benign", ""))
    case "reviewer": e.logSecuritySupervisor(toolName, "", strings.ToUpper(string(f.Verdict)), f.Rationale, "supervisor", f.Latency.Milliseconds(), 0, 0)
    case "error":    // new: stage error → Deny finding
    }
}
if res.Verdict != contentguard.Allow { ... fmt.Errorf("security: %s", res.Rationale) }
```

### 2.3 `executor.go:472–511` `AddUntrustedContentWithTaint`

| Old | New |
|---|---|
| `v.AddBlockWithTaint(security.TrustUntrusted, security.TypeData, true, content, source, agentContext, eventSeq, taintedBy) *Block` | `g.IngestWithLineage(contentguard.Untrusted, contentguard.Data, true, content, source, taintedBy) *Content` — **no agentContext, no eventSeq** |
| `block.ID` | `c.ID` (same `b%04d` scheme; dedupe still links duplicate untrusted text to the first copy via `Origins`) |
| `security.ShannonEntropy([]byte(content))` | `contentguard.ShannonEntropy(content)` (string) |

`eventSeq` (used for `TaintLineageNode.EventSeq` → `session.TaintNode.EventSeq`) and `agentContext` must be kept in a repo-side map `contentID → {eventSeq, agentRole}` if the session log is to keep those fields. → decision D2.

### 2.4 `logging.go:386–434` `logSecurityStatic` / `convertTaintLineage` / `convertTaintNode`

`security.TaintLineageNode{BlockID, Trust, Source, EventSeq, Depth, TaintedBy}` → IN-SOURCE. Build `session.TaintNode` directly from `*contentguard.Content` (`ID`, `Trust`, `Source`, `Origins` recursion with a visited set; `Depth` = recursion depth; `EventSeq` from the repo-side map in 2.3). Reference impl: `OLD/security/verifier.go:531–560` `buildLineageTree`. Dedupe note: old marked `DedupeHit` and linked to the original ID in `TaintedBy`; new links via `Origins` — same tree shape.

### 2.5 `executor.go:102,279` + all `e.logger.*` calls — `logging.Logger` → `log/slog`

IN-SOURCE. `OLD/logging/logging.go` is ~250 lines with **no deps**. Two paths:

(a) **slog directly**: `logger *slog.Logger`; `logging.New().WithComponent("executor")` → `slog.Default().With("component", "executor")`; `Info/Warn/Debug/Error(msg, map[string]any{...})` → `slog.Info(msg, "k", v, ...)` (or a tiny `fields(map) []any` helper to keep the existing map literals — there are ~25 call sites in executor + 6 in supervision). Domain helpers used by the repo: `ExecutionStart/Complete`, `PhaseStart/Complete`, `ToolResult`, `ReconcilePhase`, `SupervisePhase`, `SupervisorVerdict` — recreate as package-level funcs in a small `internal/logging` (or methods on a thin wrapper around `*slog.Logger`). Handler: old format was `LEVEL 2006-01-02T15:04:05.000Z [component] msg k=v` to **stdout**, min level INFO. Closest stdlib: `slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})`; exact old format needs a custom handler (copy `log()` at `logging.go:118–140`). Note the repo writes user-facing output to stdout/stderr with `fmt`; decide whether slog goes to **stderr** to keep stdout clean (the old lib wrote to stdout).

(b) **Copy the package verbatim** to `internal/logging` — zero call-site churn, fastest. Against kit direction ("consumer-specific, use slog") but it *is* consumer code now.

→ decision D5.

### 2.6 `tracing.go` (whole file) — `telemetry.GetTracer()`

IN-SOURCE, small: replace with a package-level
```go
var tracer = otel.Tracer("github.com/vinayprograms/agent/internal/executor")
func (e *Executor) startWorkflowSpan(ctx, name) (context.Context, trace.Span) {
    ctx, span := tracer.Start(ctx, "workflow.run") ...
```
`tracer.Debug()` (used in `endGoalSpan`, `endSubAgentSpan` to gate output attributes) → `e.debug` field fed from `executor.Config`/runtime `rt.debug`. `GetTracer()` returned a no-op tracer when unset; `otel.Tracer` does the same when no provider is installed — behaviour preserved. Kit packages now produce their own child spans (`contentguard.check`, `contentguard.stage`, `shellguard.check`, with span events `deterministic.evaluated`, `workflow.started`, `llm.blocked`, …) under whatever span is in `ctx` — the repo's `workflow.run → goal.X` spans will get these for free.

---

## 3. `internal/supervision`

`supervisor.go:39,64` (`logger *logging.Logger`, `logging.New().WithComponent("supervisor")`, calls at 121–219: `ReconcilePhase`, `PhaseComplete`, `PhaseStart`, `Error`, `SupervisePhase`, `SupervisorVerdict`, `Info`, `Warn`) and `pipeline.go:45,258–259` (exported `Config.Logger *logging.Logger`, `.Warn(msg, fields)`).

Same treatment as 2.5; the exported `pipeline.Config.Logger` field becomes `*slog.Logger` (or the repo wrapper). `executor.go:317,683` pass `e.logger` into it, so both packages must switch together.

---

## 4. Semantic differences (old `security.Verifier` vs `contentguard`) — cross-checked against `docs/security/*.md`

| # | Old behaviour (and what docs promise) | v1.2.0 behaviour | Impact / what the repo must do |
|---|---|---|---|
| S1 | **High-risk tool gating** (`HighRiskTools = bash, write, web_fetch, spawn_agent`; docs 05 "Check 2"). Low-risk tools with untrusted context PASS at tier 1. | No risk classification. Any tool call while untrusted content exists escalates to the LLM stages unless the tool is in `Config.Skip`. | Without `Skip`, every `read/grep/glob/memory_*` call after the first web fetch costs a screener call (docs 05 estimates 80% tier-1 pass; that collapses). Repo must pass a `Skip` allow-list = complement of the old high-risk set. Invert the model: old = default-pass + high-risk list; new = default-check + skip list. Unknown MCP tools are now checked (arguably safer). |
| S2 | **Paranoid** = skip triage, go straight to supervisor (`verifier.go:243`); docs 07 says "Tier 2: Always, Tier 3: Always". | `Paranoid()` runs **every** stage; screener Escalate/Allow both continue; any Deny/Modify stops. | If you put Screener+Reviewer in the stage list with `Paranoid()`, you now pay for both LLM calls (matches docs 07 table, not old code). If you want old code behaviour, build `[]Stage{Reviewer}` only. Decide which is canonical (D1). |
| S3 | **Research mode**: triage gets scope appended, supervisor uses a research system prompt. | Same, via `Config.Context["scope"]`; screener prepends "SECURITY RESEARCH CONTEXT", reviewer swaps system prompt (`reviewer.go:46`). | Equivalent. Scope also needed for `shellguard.New(..., securityScope)`. |
| S4 | **Agent-context filtering**: untrusted blocks filtered by `agentContext` (same agent or shared) for tier 1 and the reviewer saw *all* blocks. | No agent context at all; every check sees all untrusted content. | Multi-agent runs: a sub-agent's tool call is now judged against content fetched by *other* agents (more escalations; more context in prompts). Options: one `Guard` per agent role (clean, but loses cross-agent lineage), or accept. (D2) |
| S5 | **Block correlation**: tier 1 found blocks whose text/URLs appear in the args (`argsContainBlockData`), reported `Tier1.Block` (primary) and `RelatedBlocks`; fell back to most-recent block. | `Result.Related` = *all* untrusted content in context, no correlation; no primary block. | Session events `security_static.block_id` will be empty and `related_blocks` grows to "everything". Taint propagation into the next tool-result block (`relatedBlocks` return from `verifyToolCall`) now taints with every untrusted block — lineage trees become wider. If fidelity matters, re-implement `argsContainBlockData` in-repo (`verifier.go:331–357`, ~25 lines) over `g.Find(id).Text`. |
| S6 | Tier-2/3 results carried `InputTokens/OutputTokens`; session events `security_triage`/`security_supervisor` record them. | `Finding` has `Latency` only; `ChatResponse` token counts are discarded inside Screener/Reviewer. | Token accounting for security LLM calls is lost. Either accept zeros, wrap the `llm.Model` handed to the stages with a counting decorator (recommended: a 10-line `countingModel` in-repo), or write custom `Stage`s. |
| S7 | Tier-2 error → continue to tier 3. Tier-3 error → **deny**. No supervisor configured → deny ("no security supervisor configured"). | Screener error → `Escalate` finding (continues) — same. Reviewer error → `Deny` finding — same. **No stages configured → Deny** ("no verification stages configured") — same fail-close. Stage returning a Go `error` (not a verdict) → Deny with `Source:"error"`. All-escalate → Deny. | Fail-close preserved. One new corner: if *only* a Screener is configured and it says NO → Allow (old also allowed on triage clear). |
| S8 | Old tier-1 scanned args with `HasSuspiciousPatterns`; dedupe of reasons across blocks. | Same checks (`patterns.go:90–115` builtins are identical incl. `curl_pipe_bash`, keywords identical), reasons **not** deduped across blocks; `"tool:<name>"` replaces `"high_risk_tool:<name>"`. `DetectEncoding`/`SegmentEntropy`/`EncodingType` no longer exported — only `hasEncodedContent` (base64/base64url/hex + entropy 4.8) used internally. | Docs 04 behaviour (encoded content always escalates) preserved. `flags` strings in session events change spelling. |
| S9 | **Audit trail**: per-session Ed25519 keypair, signed `SecurityRecord` per decision, `ExportLog`, `VerifyRecord` (docs 06; docs 07 invariant 3 "All security decisions are signed"). | **Nothing.** No signing, no records. | The repo never called `AuditTrail()/ExportLog()` (grep: only `internal/packaging` uses ed25519 for package signing), so today the records are generated and thrown away at `Destroy()`. To honour docs 06 you must copy `OLD/security/audit.go` (210 lines, stdlib crypto only) into e.g. `internal/security/audit.go` and call `RecordDecision` from `verifyToolCall`; or rewrite docs 06 to say the session JSON is the trail. (D3) |
| S10 | `Block` had `AgentContext`, `CreatedAtSeq`, `DedupeHit`, `ContentHash`, `IsImmutable/CanOverride/PropagatedTrust` helpers. | `Content{ID, Trust, Kind, Mutable, Text, Source, Origins}` only. | Repo only consumed `ID`; the `event_seq` in lineage needs the side-map (2.3). |
| S11 | Startup logged `security verifier initialized` via the kit logger. | Silent. | Repo already prints its own `🔒 Security: mode=…` line; fine. |
| S12 | Bash: `len(allowedDirs)==0` ⇒ LLM step skipped; no model ⇒ deterministic only; `SetSecurityScope` mutable. | `allowedDirs` empty ⇒ LLM still runs (unconstrained prompt); scope immutable; `Gate` only guards a tool registered `.With(gate)`. | Deterministic checks are byte-for-byte the same (`denylist.go` vs `bash.go`), LLM prompt identical, `OnDecision` identical. Main change is wiring order (gate needs scope ⇒ build after security config) and policy `denylist` key loss. |

---

## 5. Decisions needed

- **D1 — Mode semantics.** Keep `default/paranoid/research` as a repo enum and map to `(stages, workflow, context)`. For paranoid, choose docs-07 semantics (`Paranoid()` + Screener + Reviewer, both always run) or old-code semantics (`Escalatory()`/`Paranoid()` + Reviewer only). Recommendation: docs-07 semantics — that's what the docs promise and what the kit's `Paranoid()` is designed for.
- **D2 — Agent context & event seq.** Accept loss of per-agent block filtering and keep one guard (simplest), or one `Guard` per agent role. Keep a repo-side `map[contentID]struct{seq uint64; agent string}` to preserve `event_seq` in `session.TaintNode`, or drop the field (it's `omitempty`).
- **D3 — Audit trail.** Copy `audit.go` in-repo (and finally *use* it: export on session close) vs. drop and amend `docs/security/06-audit-trail.md` + `07` invariant 3. The old code never exported the log, so dropping changes no observable output.
- **D4 — Legacy `telemetry.Exporter`.** Drop the 12 `LogEvent` hooks (they duplicate session events and OTel spans) vs copy `telemetry.go` into `internal/telemetry`. Recommendation: drop; `TelemetryConfig.Protocol` then only means `grpc|http`.
- **D5 — Logging.** slog with a `fields(map)→[]any` shim and a handful of domain helper funcs, vs. verbatim copy of the old package into `internal/logging`. Also: stdout (old) or stderr (conventional) for the handler.
- **D6 — Block correlation.** Re-implement `argsContainBlockData` in-repo to keep `block_id`/`related_blocks` narrow (and taint trees small), or accept "all untrusted content is related".
- **D7 — Token counting for security LLM calls.** Counting decorator around `llm.Model`, or accept zeros in `security_triage`/`security_supervisor` events.
- **D8 — Skip list.** Exact set of tools to put in `contentguard.Config.Skip` (inverse of old `HighRiskTools`). Note new tool names registered by the v1.2.0 `tools` package may differ (`list_files`?) — align with the tools-package agent.

## 6. Files to touch (summary)

- `cmd/agent/runtime.go` — security (1.1), bash gate (1.3), telemetry (1.4)
- `cmd/agent/workflow.go` — delete `registerSecurityExtensions`, move values to guard config (1.2)
- `cmd/agent/runtime_test.go`, `cmd/agent/bridges_test.go` — re-point / delete
- `internal/executor/{config.go, executor.go, logging.go, tracing.go}` — 2.1–2.6
- `internal/supervision/{supervisor.go, pipeline.go}` — 3
- NEW `internal/telemetry/provider.go` (copied `InitProvider`), optionally `exporter.go`
- NEW `internal/logging/` (or slog shim), optionally `internal/security/audit.go`
- `go.mod` — promote otel sdk/exporter modules to direct deps
