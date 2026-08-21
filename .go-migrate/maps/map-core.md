# Migration map — agentkit core packages (llm, tools, policy, mcp, memory, credentials, embedding, errors, shutdown)

Old: `/Users/vinay/go/pkg/mod/github.com/vinayprograms/agentkit@v0.2.1-0.20260324114043-fbf217a606af/` (OLD)
New: `/Users/vinay/go/pkg/mod/github.com/vinayprograms/agentkit@v1.2.0/` (NEW)

Legend: **RE-POINT** = same capability, new name/shape. **IN-SOURCE** = capability removed from kit; repo must absorb it. **DROPPED** = gone and not needed.

`embedding`, `errors`, `shutdown`: the repo imports none of them (the `errors.Is/New` hits in `tests/failure` are stdlib). Nothing to do. (`errors` v1 is unchanged in shape; `shutdown.ErrAlreadyShutdown` is deprecated but still present.)

---

## 0. Big-picture semantic shifts (read first)

| Area | OLD | NEW (v1.2.0) | Consequence |
|---|---|---|---|
| LLM | `llm.Provider` iface, `llm.NewProvider(llm.ProviderConfig{Provider:…, RetryConfig:…})` | `llm.Model` iface (same single `Chat` method), `llm.New(llm.Config{Service:…, Retry:…})` | Pure rename for consumers of the interface; constructor fields renamed. `Chat` signature unchanged. |
| LLM factory | `llm.ProviderFactory{GetProvider(profile)}`, `llm.NewSingleProviderFactory` | `llm.Resolver{Model(profile) (Model, error)}`; no single-model helper | Rename + write a 5-line single resolver in repo. |
| LLM mock | `llm.NewMockProvider()` (+ `SetResponse/ChatFunc/LastRequest/SetError`) | **none** | IN-SOURCE: copy OLD `llm/mock.go` into a repo test helper package. |
| LLM thinking | `ChatRequest{Messages,Tools,MaxTokens}` | `ChatRequest` gains `Thinking ThinkingLevel` per-call override; `ResolveThinkingLevel(config, req)` | Additive; nothing to change. |
| LLM response | `ChatResponse.TTFTMs` | field removed | Repo never reads it. DROPPED. |
| Tools registry | `tools.NewRegistry(pol)` pre-registers ~27 builtins, policy-aware `Definitions`/`Execute`, `Set*` wiring, per-tool `policy.CheckPath` inside tools | `tools.NewRegistry()` **empty**; `Register(tools.New(tool).With(guard))` returns error on dup; `Definitions()` returns ALL tools (not policy-aware); tools only enforce workspace confinement | **Biggest change.** Repo must (a) build the builtin set itself, (b) filter definitions by policy itself, (c) attach `Guard`s for policy path/domain checks, (d) attach `shellguard.Gate` for bash. |
| Tool interface | `Execute(ctx, map[string]any) (any, error)`, `Parameters() map[string]any` (JSON schema) | `Execute(ctx, tools.Args) (string, error)`, `Parameters() map[string]tools.Param`; `Definition.JSONSchema()` gives the LLM schema | Every custom tool (swarm `DispatchTool`, `websearch.Tool`, test tools) must be rewritten. Executor must call `registry.Execute(ctx, name, rawArgs)` (which validates) instead of `tool.Execute(ctx, tc.Args)`. |
| Tool results | structured `any` → executor `json.Marshal`s | `string` | Simplify `logToolResult`/message building; no Marshal needed. |
| Spawn | two tools `spawn_agent` + `spawn_agents`, `registry.SetSpawner` | one tool `spawn_agents` via `tools.Spawn(fn)` or `tools.NewSpawnBinder()` (late bind) | Executor's `Has("spawn_agent")`, sub-agent exclusion list, and tests referencing `spawn_agent` must change to `spawn_agents`. |
| Scratchpad / memory tools | `registry.SetScratchpad(tools.NewInMemoryStore(), persisted)`, `registry.SetSemanticMemory(memory.NewToolsAdapter(store))` | `tools.ScratchpadRead/Write/List/Search(store tools.Scratchpad)`, `tools.Remember/Recall(mem tools.Memory)`; `memory.InMemoryStore`/`BleveStore` satisfy both directly | `tools.NewInMemoryStore`, `tools.FileMemoryStore`, `memory.ToolsAdapter` DROPPED; use `memory.NewInMemoryStore()` for scratchpad. |
| Web | `registry.SetSummarizer(llm.NewSummarizer(p))`, `registry.SetCredentials(creds)`, `tools.SetHTTPTimeout(d)` (global) | `tools.Fetch(tools.NewSummarizer(model), tools.WithHTTPTimeout(d))`, `tools.Search(creds credentials.Lookup, tools.WithHTTPTimeout(d))` | Per-tool option instead of global. |
| Bash security | `policy.BashChecker` + `policy.SmallLLMChecker` + `policy.LLMProviderFromChatProvider`, wired via `registry.SetBashChecker/SetBashLLMChecker/SetBashSecurityCallback`; policy `allowlist`/`denylist`/`CheckCommand`; sandbox bwrap/docker inside bash tool | `shellguard.New(shell, workspace, allowedDirs, userDeniedCommands, model llm.Model, securityScope) *Gate` is a `tools.Guard`; `gate.OnDecision` callback | `policy.CheckCommand`/allowlist DROPPED (only a denylist survives as `userDeniedCommands`); sandbox DROPPED (IN-SOURCE if still wanted — copy from OLD `tools/registry.go` bashTool lines ~805-900); `SetSecurityScope` becomes a constructor arg (gate must be built after security mode is known, or rebuilt). |
| Policy model | `Policy{DefaultDeny(false), Workspace, HomeDir, ConfigDir, AllowedDirs, Tools, MCP{DefaultDeny,AllowedTools}, Security{ExtraPatterns,ExtraKeywords}}`; `ToolPolicy{Enabled, Allow, Deny, Allowlist, Denylist, AllowDomains, RateLimit, Sandbox, Timeout}` | `Policy{DefaultDeny(**true**), ProtectedFiles, AllowedDirs, Tools, MCP{Enabled, Allow}, Content{Security{Patterns,Keywords}}}`; `ToolPolicy{Allow, Deny, Sandbox, Timeout}`; **presence in Tools == enabled** | No `Workspace` field (workspace passed to `FromFile`/used for expansion only); no `Enabled` flag; `AllowDomains`→`Allow`; `Denylist` gone. |
| Policy file | top-level `[read] enabled=true …`, `[mcp] default_deny/allowed_tools`, `[security] extra_*` | `[tools.read] allow=…`, `[mcp] enabled/allow`, `[content.security] patterns/keywords`; legacy keys silently ignored (`FromTOMLWithUnknownKeys` reports them) | **All repo policy.toml files and `setup.go`'s generator are in the legacy format → under NEW every tool is disabled (default_deny=true + nothing recognised under `[tools]`).** Design decision required (see §6). |
| Policy loading | `policy.LoadFile(path)`, `policy.Parse`, `policy.ValidateKeys`, `policy.NewRestrictive()` | `policy.FromFile(path, workspace, homeDir)`, `FromTOML`, `FromTOMLWithUnknownKeys`; `New()` is already restrictive | `ValidateKeys` IN-SOURCE (reimplement on top of `FromTOMLWithUnknownKeys`). |
| MCP | `mgr.Connect(ctx, name, ServerConfig)`, `mgr.SetDeniedTools`, `*mcp.Client` struct, `ToolCallResult` | `client, err := mcp.Stdio(ctx, cfg)` / `mcp.HTTP(ctx, HTTPConfig)`; `mgr.Register(name, client)`; `mgr.Deny(name, tools)`; `mcp.Client` is an interface; `mcp.Result` | Two-step connect; rest identical (`AllTools`, `CallTool`, `FindTool`, `Close`, `Disconnect`, `ServerCount`, `Servers`). |
| Memory | `memory.NewObservationExtractor(provider)` → `Extract(ctx, stepName, stepType, output) (any, error)`; `memory.NewBleveObservationStore(store)`; `Store.Search` returns `[]SearchResult` | `memory.NewExtractor(model)` → `Extract(ctx, text, WithSource(label)) (findings, insights, lessons []string, err)`; no observation store wrapper; `Search` returns `map[string]string` | Executor's `ObservationExtractor`/`ObservationStore` interfaces need thin repo adapters (or be reshaped). |
| Credentials | `*credentials.Credentials` struct; `credentials.Load() (*Credentials, path, err)`; `GetCredential(p) Credential{Key, IsOAuthToken}`; `GetAPIKey`; `SetAPIKey`+`Save()`; `DefaultPath()`; `HasClaudeCliCredentials()`; generic `[llm]` fallback; provider-name normalisation (`-` stripped, lowercased) | `credentials.Lookup` iface; `credentials.Load(paths...) (Lookup, FileStore, err)`; `credentials.Resolve(lookup, p) (Credential string, isOAuth bool)`; `FileStore.SetAPIKey` + `Save(path)`; `StandardPaths(app)`; `ClaudeCLICredentials() FileStore`; env fallback built into Load | `*Credentials` type disappears everywhere (runtime, serve, setup, websearch). `[llm]` generic fallback, name normalisation, `DefaultPath`, `HasClaudeCliCredentials` are IN-SOURCE (tiny). |

---

## 1. Package `llm`

### Symbols used → mapping

| Used symbol (where) | Class | NEW equivalent |
|---|---|---|
| `llm.Provider` (cmd/agent/runtime.go, bridges_test.go; executor/{config,executor,subagent}.go; supervision/supervisor.go) | RE-POINT | `llm.Model` — `interface{ Chat(ctx, ChatRequest) (*ChatResponse, error) }` (identical method). |
| `llm.ProviderConfig{Provider, Model, APIKey, IsOAuthToken, MaxTokens, BaseURL, Thinking, RetryConfig}` (runtime.go ×4) | RE-POINT | `llm.Config{Service, Model, APIKey, IsOAuthToken, MaxTokens, BaseURL, Thinking, Retry}`. Field renames: `Provider`→`Service`, `RetryConfig`→`Retry`. Note `Config.Validate()` now **requires `MaxTokens != 0`** and `APIKey != ""` unless service ∈ {ollama, ollama-local, lmstudio}; OLD `ApplyDefaults()` is gone — the repo must default MaxTokens itself (check `internal/config` defaults). Service names accepted: anthropic, openai, google, ollama-cloud, groq, mistral, xai, openrouter, ollama-local, ollama, lmstudio, cerebras, openai-compat, litellm. |
| `llm.NewProvider(cfg) (Provider, error)` (runtime.go ×4) | RE-POINT | `llm.New(cfg Config) (Model, error)`. Infers Service from Model if empty (errors if it can't). Result is already OTel-instrumented (`WithTracing` gone). |
| `llm.InferProviderFromModel(model) string` (runtime.go ×4) | RE-POINT | `llm.InferService(model) string`. |
| `llm.ThinkingConfig{Level, BudgetTokens}`, `llm.ThinkingLevel` + consts (runtime.go) | unchanged | same. |
| `llm.RetryConfig{MaxRetries, MaxBackoff, InitBackoff}` (cmd/agent/util.go) | unchanged | same type; assign to `Config.Retry`. Defaults 5 / 60s / 1s applied internally when zero. |
| `llm.NewSummarizer(provider) *Summarizer` (runtime.go) | RE-POINT (moved) | `tools.NewSummarizer(model llm.Model) tools.Summarizer`. |
| `llm.ProviderFactory` (executor/config.go, executor.go, runtime.go's `profileProviderFactory`) | RE-POINT | `llm.Resolver{ Model(profile string) (Model, error) }`. Rename method `GetProvider`→`Model`. |
| `llm.NewSingleProviderFactory(p)` (executor.go:262) | IN-SOURCE | 5 lines: `type singleResolver struct{ m llm.Model }; func (s singleResolver) Model(string) (llm.Model, error) { return s.m, nil }`. |
| `llm.NewMockProvider()` + `.SetResponse/.ChatFunc/.LastRequest/.SetError` (executor tests ×22, tests/failure ×6, tests/integration ×5, tests/performance ×2) | IN-SOURCE | Copy OLD `llm/mock.go` (`MockProvider` with `ChatFunc`, `CallCount`, `LastRequest`, `Reset`, `SetError`, `SetResponse`, `SetStopReason`, `SetTokenCounts`, `SetToolCall`, `SetToolCalls`) into e.g. `internal/testutil/llmmock` (exported as `llmmock.New()`), typed against `llm.Model`. Path: OLD `…/llm/mock.go`. |
| `llm.ChatRequest`, `llm.ChatResponse`, `llm.Message`, `llm.ToolDef`, `llm.ToolCallResponse` (everywhere) | unchanged | Same fields (`map[string]interface{}`→`map[string]any`, cosmetic). `ChatRequest.Thinking` is new/optional. `ChatResponse.TTFTMs` removed (unused). |

### Before → after

```go
// cmd/agent/runtime.go createProvider
// OLD
cred := rt.creds.GetCredential(llmProvider)
rt.provider, err = llm.NewProvider(llm.ProviderConfig{
    Provider: llmProvider, Model: rt.cfg.LLM.Model,
    APIKey: cred.Key, IsOAuthToken: cred.IsOAuthToken,
    MaxTokens: rt.cfg.LLM.MaxTokens, BaseURL: rt.cfg.LLM.BaseURL,
    Thinking: llm.ThinkingConfig{Level: llm.ThinkingLevel(rt.cfg.LLM.Thinking)},
    RetryConfig: parseRetryConfig(...),
})
// NEW
key, isOAuth := credentials.Resolve(rt.creds, llmProvider)   // rt.creds is credentials.Lookup
rt.model, err = llm.New(llm.Config{
    Service: llmProvider, Model: rt.cfg.LLM.Model,
    APIKey: string(key), IsOAuthToken: isOAuth,
    MaxTokens: rt.cfg.LLM.MaxTokens, BaseURL: rt.cfg.LLM.BaseURL,
    Thinking: llm.ThinkingConfig{Level: llm.ThinkingLevel(rt.cfg.LLM.Thinking)},
    Retry: parseRetryConfig(...),
})
```

```go
// profileProviderFactory → implements llm.Resolver
func (f *profileResolver) Model(profile string) (llm.Model, error) { ... llm.New(llm.Config{Service: ..., ...}) }
```

---

## 2. Package `tools`

### Symbols used → mapping

| Used symbol (where) | Class | NEW equivalent |
|---|---|---|
| `tools.NewRegistry(pol *policy.Policy)` (runtime.go; executor_test ×7; tests/* ×14; websearch.go comment) | RE-POINT + IN-SOURCE | `tools.NewRegistry()` — empty. The builtin set OLD auto-registered must be rebuilt in repo (see "buildRegistry" below). |
| `*tools.Registry` methods used: `Get`, `Has`, `Definitions`, `Register`, `SetSpawner`, `SetSummarizer`, `SetCredentials`, `SetScratchpad`, `SetSemanticMemory`, `SetBashChecker`, `SetBashLLMChecker`, `SetBashSecurityCallback` | mixed | `Get(name) Tool`, `Has(name) bool` unchanged. `Definitions() []Definition` (was `[]ToolDefinition`; `Definition.Parameters` is now `map[string]Param` → call `def.JSONSchema()` to get the LLM schema; **not policy-filtered**). `Register(entry *Entry) error` (was `Register(t Tool)`; **errors on duplicate name** — OLD silently overwrote, which `websearch` relied on). All `Set*` DROPPED → replaced by constructor args at registration (below). New: `Execute(ctx, name, rawArgs map[string]any) (string, error)` validates args then runs; `Subset(names) (*Registry, error)`. |
| `tools.Tool` (websearch.go, swarm/dispatch.go implement it; executor calls `tool.Execute(ctx, tc.Args)`) | RE-POINT | `Tool{ Name(); Description(); Parameters() map[string]Param; Execute(ctx, Args) (string, error) }`. |
| `tools.SetHTTPTimeout(d)` (runtime.go:424) | RE-POINT | `tools.WithHTTPTimeout(d) WebOption` passed to `tools.Fetch(...)`/`tools.Search(...)` at construction. |
| `tools.NewInMemoryStore()` (runtime.go:212, for scratchpad) | RE-POINT | `memory.NewInMemoryStore()` (satisfies `tools.Scratchpad`). |
| `tools.SpawnFunc` (executor.go:518 via SetSpawner) | unchanged type | `tools.Spawn(fn) Tool` or `tools.NewSpawnBinder()` → `.Tool()` register now, `.Bind(fn)` later (matches executor's `initSpawner` ordering where registry exists before executor). |
| `tools.Args.Raw` | DROPPED | `Args` is now an opaque validated struct (`String/Int/Bool/Float/StringSlice` + `*Or`, `Has`). |

### What OLD `NewRegistry(pol)` registered (must be rebuilt in-repo)

From OLD `tools/registry.go:135-161` + Set* methods: read, write, edit, glob, grep, ls, bash, mkdir, mv, cp, rm, head, tail, diff, tree, patch, git, pwd, hostname, whoami, sysinfo, datetime, web_fetch, web_search, spawn_agent, spawn_agents; plus (via Set*) scratchpad_read/write/list/search, remember, recall.

NEW constructors (all return `tools.Tool`): `Read/Write/Edit/Grep/Head/Tail/Ls/Mkdir/Mv/Cp/Rm(workspace, extraRoots...)`, `Glob(workspace)`, `Tree(workspace)`, `Git(workspace)`, `Bash(workspace)`, `Diff()`, `Patch()`, `Pwd()`, `Hostname()`, `Whoami()`, `Sysinfo()`, `Datetime()`, `Env()`, `Which()`, `Fetch(summarizer, opts...)`, `Search(creds credentials.Lookup, opts...)`, `Spawn(fn)`/`NewSpawnBinder()`, `ScratchpadRead/Write/List/Search(store)`, `Remember/Recall(mem)`. Tool names are identical to OLD except `spawn_agent` (gone; only `spawn_agents`), and `env`/`which` are new.

Suggested repo helper (new file, e.g. `internal/toolset/toolset.go`):

```go
// Build registers every builtin the policy enables, attaching policy guards.
func Build(pol *policy.Policy, ws string, extraRoots []string, sum tools.Summarizer,
           creds credentials.Lookup, httpTimeout time.Duration, scratch tools.Scratchpad,
           mem tools.Memory, bashGate tools.Guard, spawn *tools.SpawnBinder) (*tools.Registry, error) {
    reg := tools.NewRegistry()
    add := func(t tools.Tool, guards ...tools.Guard) error {
        if !pol.IsToolEnabled(t.Name()) { return nil }       // policy filtering lives HERE now
        e := tools.New(t); for _, g := range guards { e = e.With(g) }
        return reg.Register(e)
    }
    add(tools.Read(ws, extraRoots...), pathGuard{pol, "read", "path"})
    add(tools.Write(ws, extraRoots...), pathGuard{pol, "write", "path"})
    // ... edit, grep, head, tail, ls, mkdir, mv(src,dst), cp(src,dst), rm, glob, tree, diff, patch, git ...
    add(tools.Bash(ws), bashGate)                              // shellguard.Gate
    add(tools.Fetch(sum, tools.WithHTTPTimeout(httpTimeout)), domainGuard{pol, "web_fetch"})
    add(tools.Search(creds, tools.WithHTTPTimeout(httpTimeout)))
    add(spawn.Tool())
    add(tools.ScratchpadRead(scratch)); ... ; add(tools.Remember(mem)); add(tools.Recall(mem))
    return reg, nil
}

type pathGuard struct{ pol policy.Lookup; tool, key string }
func (g pathGuard) Check(_ context.Context, a tools.Args) error {
    p, _ := a.String(g.key)
    if ok, why := g.pol.CheckPath(g.tool, p); !ok { return errors.New("denied: " + why) }
    return nil
}
```
(OLD enforcement points to mirror: `tools/registry.go` read:408, write:461-467, edit:533-539, glob:601, grep:657/683; `tools/tool_fs.go` 46,99-102,153-156,253,319,378; `tools/tool_code.go` 53-56,203,301. mv/cp check both `source` and `destination`; grep/glob used `CheckPath("read", …)`.) Tests in `tests/security` assert the error text contains `"denied"` — keep that word in guard errors.

### Executor call-site changes (internal/executor/tools.go)

```go
// OLD
tool := e.registry.Get(tc.Name); ...; result, err := tool.Execute(ctx, tc.Args)   // result any
// NEW — validation + guards only run through the registry
if !e.registry.Has(tc.Name) { ...unknown tool... }
result, err := e.registry.Execute(ctx, tc.Name, tc.Args)                         // result string
```
`getAllToolDefinitions` / subagent tool-def building: `Parameters: def.JSONSchema()` instead of `def.Parameters`; exclude `"spawn_agents"` only. `logToolResult(result any)` can take `string`; drop the `json.Marshal` branch (logging.go:119-122).

---

## 3. Package `policy`

### Symbols used → mapping

| Used symbol (where) | Class | NEW equivalent |
|---|---|---|
| `policy.Policy` (runtime.go, workflow.go, executor/{config,executor}.go) | RE-POINT | `*policy.Policy` still exists; consider typing executor against `policy.Lookup` iface. Removed fields: `Workspace`, `HomeDir`, `ConfigDir`, `Security`; `MCP` shape changed; new `ProtectedFiles`, `Content`. |
| `policy.New()` (workflow.go, runtime_test, executor_test ×5, tests ×17) | RE-POINT (semantics!) | `policy.New()` now `DefaultDeny=true`, `ProtectedFiles=DefaultProtectedFiles()`. OLD default was permissive (`DefaultDeny=false`, all tools enabled). Tests that do `policy.New()` + `tools.NewRegistry(pol)` and expect `read`/`ls`/`spawn_agent` available must either set `pol.DefaultDeny=false` or list tools. |
| `policy.NewRestrictive()` (executor_test:475) | RE-POINT | `policy.New()` (already restrictive). |
| `policy.LoadFile(path)` (workflow.go:148) | RE-POINT | `policy.FromFile(path, workspace, homeDir)` — also does `$WORKSPACE`/`~` expansion (OLD expanded lazily via `Policy.Workspace/HomeDir`). `os.IsNotExist(err)` check still works (error wraps `os.ReadFile`'s error with `%w`). |
| `policy.ValidateKeys(content)` (workflow.go:139) | IN-SOURCE | Reimplement with `policy.FromTOMLWithUnknownKeys(content, ws, home)`: reject `"workspace"` and any unknown non-table keys; optionally warn on legacy keys (`*.enabled`, `*.allowlist`, `*.denylist`, `*.allow_domains`, `*.rate_limit`, `mcp.default_deny`, `mcp.allowed_tools`, `security.*`). OLD source: `policy/policy.go:203-232` (`knownTopLevelKeys`). |
| `pol.Workspace = …` (workflow.go:156; tests ×16) | DROPPED field | Pass workspace to `FromFile` and to the `tools.*` constructors. Tests: replace `pol.Workspace = dir` with passing `dir` to tool constructors. |
| `pol.AllowedDirs` (workflow.go `ensureWorkspaceInAllowedDirs`) | unchanged | same field. |
| `pol.Security.ExtraPatterns/ExtraKeywords` (workflow.go `registerSecurityExtensions`) | RE-POINT | `pol.Content.Security.Patterns` / `.Keywords` (`Content` and `Security` are pointers; `New()` pre-allocates both). Consumer of these (`security.RegisterCustomPatterns`) is in the removed `security` package — other agent's domain (contentguard). |
| `pol.GetToolPolicy("bash")` → `.Denylist` (runtime.go:201-202) | RE-POINT | `GetToolPolicy` now returns **nil** when the tool is not configured (OLD returned a synthesized `{Enabled:…}`); `Denylist` gone → feed user-denied commands to `shellguard.New(..., userDeniedCommands, ...)` from a repo-defined source (e.g. `tp.Deny` for bash, or a config field). Design decision: what does `[tools.bash].deny` mean now? |
| `pol.IsToolEnabled(name)` (runtime.go:184, executor/tools.go:117) | unchanged | same; semantics now "listed in Tools or !DefaultDeny". |
| `pol.CheckMCPTool(server, tool) (bool, string, string)` (executor/tools.go:387) | unchanged | same signature; now driven by `MCP.Enabled` + `MCP.Allow` (warning when `MCP == nil`). |
| `pol.CheckDomain`, `pol.CheckPath` (tests) | unchanged sig | `CheckDomain` uses `ToolPolicy.Allow` (not `AllowDomains`). `CheckPath` now also enforces `IsProtectedFile` for write/edit and `AllowedDirs`. |
| `policy.ToolPolicy{Enabled, Allow, Deny, Allowlist, Denylist, AllowDomains}` (tests ×14 literals; setup.go reads `.Enabled`) | RE-POINT | `ToolPolicy{Allow, Deny, Sandbox, Timeout}`. `Enabled: true` → just insert into `pol.Tools`; `Enabled: false` → delete from map (tests/security:188). `AllowDomains` → `Allow`. `Allowlist/Denylist` → gone (bash allowlist test `TestSecurity_BashCommandInjection` must be rewritten around `shellguard`). |
| `policy.DefaultDeny`, `policy.Tools` (internal/setup/setup.go:367-369) | n/a | These are fields of setup.go's *own* local struct decoded from policy.toml (`policy.Tools.Bash.Enabled`), not agentkit — but they read the legacy schema; update alongside the file-format decision. |
| `policy.NewBashChecker(pol, denylist)`, `*policy.SmallLLMChecker`, `policy.NewSmallLLMChecker(...)`, `policy.LLMProviderFromChatProvider(p)`, `.SetSecurityScope(scope)` (runtime.go:45,188,202,341; bridges_test.go) | RE-POINT (new pkg) | `shellguard.New(shellguard.Bash(), workspace, pol.GetAllowedDirs(), userDenied, smallModel /*nil = deterministic only*/, securityScope) *shellguard.Gate`; register with `tools.New(tools.Bash(ws)).With(gate)`; `gate.OnDecision = rt.exec.LogBashSecurity` replaces `SetBashSecurityCallback` (identical func signature). `SetSecurityScope` → constructor arg: build the gate **after** `determineSecurityConfig()` (reorder `setupRegistry` after security mode is known, or compute mode earlier). `bridges_test.go` tests the removed adapter → delete or retarget at `shellguard` (e.g. `gate.CheckDeterministic`). |
| `policy.CheckCommand` / bash `allowlist` | DROPPED | No allowlist concept in NEW. If the product needs a bash allowlist, IN-SOURCE as an extra `tools.Guard` (OLD matcher: `policy/policy.go:416-464, 599-645`). |
| bash `Sandbox` (bwrap/docker) | DROPPED from tool | `ToolPolicy.Sandbox` still parses but `tools.Bash` ignores it. IN-SOURCE if needed: OLD `tools/registry.go` bashTool.Execute ~805-900. |

### Before → after (workflow.go loadPolicy)

```go
// OLD
if content, err := os.ReadFile(policyPath); err == nil { policy.ValidateKeys(string(content)) ... }
w.pol, err = policy.LoadFile(policyPath); if os.IsNotExist(err) { w.pol = policy.New() }
w.pol.Workspace = w.cfg.Agent.Workspace
// NEW
home, _ := os.UserHomeDir()
content, err := os.ReadFile(policyPath)
switch {
case os.IsNotExist(err) && w.policyPath == "": w.pol = policy.New()
case err != nil: return err
default:
    pol, unknown, err := policy.FromTOMLWithUnknownKeys(string(content), w.cfg.Agent.Workspace, home)
    if err != nil { return err }
    if err := validatePolicyKeys(unknown); err != nil { return fmt.Errorf("policy validation: %w", err) }
    w.pol = pol
}
w.pol.ProtectedFiles = append(w.pol.ProtectedFiles, configPath, policyPath)  // replaces OLD ConfigDir
```

---

## 4. Package `mcp`

| Used symbol (where) | Class | NEW equivalent |
|---|---|---|
| `mcp.Manager`, `mcp.NewManager()` (runtime.go, executor/{config,executor}.go, setup.go) | unchanged | same. |
| `mcp.ServerConfig{Command, Args, Env}` (runtime.go:290, setup.go:1930) | unchanged | same (stdio). New `mcp.HTTPConfig{Endpoint}` available. |
| `mgr.Connect(ctx, name, cfg) error` (runtime.go:290, setup.go:1930) | RE-POINT | `client, err := mcp.Stdio(ctx, cfg); err = mgr.Register(name, client)`. |
| `mgr.SetDeniedTools(name, tools)` (runtime.go:301) | RE-POINT | `mgr.Deny(name, tools)`. |
| `mgr.AllTools() []ToolWithServer{Server, Tool{Name,Description,InputSchema}}` (executor ×2, setup.go) | unchanged | same. |
| `mgr.CallTool(ctx, server, tool, args) (*ToolCallResult, error)` → `.Content[].Type/.Text` (executor/tools.go:399) | RE-POINT (type rename) | returns `*mcp.Result{Content []Content, IsError}` — identical fields. |
| `mgr.Close()`, `mgr.Disconnect(name)` | unchanged | same. |

```go
// runtime.go createExecutor
// OLD
err := mcpMgr.Connect(ctx, name, mcp.ServerConfig{...}); ...; mcpMgr.SetDeniedTools(name, serverCfg.DeniedTools)
// NEW
client, err := mcp.Stdio(ctx, mcp.ServerConfig{Command: c.Command, Args: c.Args, Env: c.Env})
if err == nil { err = mcpMgr.Register(name, client) }
...
mcpMgr.Deny(name, serverCfg.DeniedTools)
```

---

## 5. Package `memory`

| Used symbol (where) | Class | NEW equivalent |
|---|---|---|
| `memory.BleveStore`, `memory.BleveStoreConfig{BasePath}`, `memory.NewBleveStore(cfg)` (runtime.go, cmd/agentmem/main.go) | unchanged | same. Method set same except `Search(q) (map[string]string, error)` (was `[]SearchResult`; agentmem does not call it). |
| `store.ListAll`, `store.RecallFIL`, `store.Close`, `memory.ObservationItem` (agentmem) | unchanged | same (`ObservationItem` now defined in `memory`, not `types`). |
| `memory.NewToolsAdapter(store)` (runtime.go:224) | DROPPED | Pass `rt.bleveStore` directly to `tools.Remember(store)` / `tools.Recall(store)` (`*BleveStore` satisfies `tools.Memory`). |
| `memory.NewObservationExtractor(provider)` (runtime.go:369) → executor's `ObservationExtractor{Extract(ctx, stepName, stepType, output) (any, error)}` | RE-POINT + IN-SOURCE adapter | `memory.NewExtractor(model llm.Model)`; `Extract(ctx, text, memory.WithSource(stepType+":"+stepName)) (findings, insights, lessons []string, err)`. Repo must adapt: either change executor's interfaces to `Extract(ctx, stepName, stepType, output) (findings, insights, lessons []string, err)` + `Store{RememberFIL}`, or write an adapter type in cmd/agent. |
| `memory.NewBleveObservationStore(store)` (runtime.go:370) → executor's `ObservationStore{StoreObservation(ctx, any) error; QueryRelevantObservations(...)}` | IN-SOURCE | Replace with `store.RememberFIL(ctx, f, i, l, source)`. `QueryRelevantObservations` is declared in executor but never called → drop it. OLD source to mirror: `memory/observation.go` (`BleveObservationStore.StoreObservation` = loop over `RememberObservation`; source = `"<stepType>:<stepName>"`). |

Suggested executor reshape (internal/executor/executor.go:36-44, 372-385):

```go
type ObservationExtractor interface {
    Extract(ctx context.Context, text string, opts ...memory.ExtractOption) (findings, insights, lessons []string, err error)
}
type ObservationStore interface {
    RememberFIL(ctx context.Context, findings, insights, lessons []string, source string) ([]string, error)
}
// extractAndStoreObservations:
f, i, l, err := e.observationExtractor.Extract(ctx, output, memory.WithSource(stepType+":"+stepName))
if err == nil && len(f)+len(i)+len(l) > 0 { e.observationStore.RememberFIL(ctx, f, i, l, stepType+":"+stepName) }
```
`*memory.Extractor` and `*memory.BleveStore` then satisfy these with no adapter.

---

## 6. Package `credentials`

| Used symbol (where) | Class | NEW equivalent |
|---|---|---|
| `*credentials.Credentials` (main.go, serve.go, runtime.go, setup.go, websearch.go comment) | RE-POINT | `credentials.Lookup` (iface: `Get(provider) Credential`, `Providers()`), optionally also `credentials.OAuthResolver`. Store in `runtime.creds credentials.Lookup`. |
| `credentials.Load() (*Credentials, path, error)` (main.go:27, serve.go:124, setup.go:2171) | RE-POINT | `credentials.Load(credentials.StandardPaths("grid")...) (Lookup, FileStore, error)`. Already composes env < file < Claude-CLI. Note: OLD `StandardPaths` — check OLD `credentials.go:72-91` for exact order; NEW is `./credentials.toml`, `~/.config/<app>/…`, `~/.<app>/…`. Error message no longer reports the path → keep your own loop if you need it. |
| `creds.GetCredential(p) Credential{Key, IsOAuthToken}` (runtime.go ×4) | RE-POINT | `key, isOAuth := credentials.Resolve(lookup, p)`; `key` is `credentials.Credential` (string). **Lost behaviours (IN-SOURCE if wanted):** (1) provider-name normalisation (`strings.ToLower`, strip `-`) — OLD `credentials.go:232-236`; NEW env map keys are exact (`openai-compat` is mapped, `ollama-cloud` etc. are not); (2) generic `[llm] api_key` fallback for LLM providers (OLD `:257-262`, `isLLMProvider :417`); (3) env var fallback for arbitrary providers via `envVarForProvider` (OLD `:427`, e.g. `SEARXNG_URL`, `XAI_API_KEY`…) — NEW `EnvStore` only knows anthropic/openai/openai-compat/google/mistral/groq/brave/tavily. Wrap the NEW Lookup in a small repo `Lookup` decorator if these matter. |
| `creds.GetAPIKey(p)` (websearch.go `CredProvider`) | RE-POINT | `string(lookup.Get(p))`. |
| `creds.SetAPIKey(p, key)` + `creds.Save()` (setup.go:2177-2179) | RE-POINT | `fs := credentials.FileStore{}` (or the `FileStore` returned by `Load`, nil → `FileStore{}`); `fs.SetAPIKey(p, key)`; `fs.Save(path)` — path is now explicit (creates parent dir 0700, file 0600). |
| `credentials.DefaultPath()` (setup.go:1975) | IN-SOURCE | `filepath.Join(home, ".config", "grid", "credentials.toml")` = `credentials.StandardPaths("grid")[1]`. |
| `credentials.HasClaudeCliCredentials()` (setup.go:1788,1801) | RE-POINT | `credentials.ClaudeCLICredentials() != nil`. |
| `globalCreds` nil-safety: OLD methods tolerated a nil `*Credentials` (env fallback) | note | NEW `Load` always returns a non-nil `UnionStore` (env store included), so nil checks go away. |

```go
// cmd/agent/main.go init()
// OLD
creds, path, err := credentials.Load()
// NEW
creds, _, err := credentials.Load(credentials.StandardPaths("grid")...)
```

---

## 7. Findings grouped by CONSUMER PACKAGE

### cmd/agent  (runtime.go, util.go, main.go, serve.go, workflow.go, bridges_test.go, runtime_test.go)
- **runtime.go** (heaviest file):
  - struct fields: `provider, smallLLM llm.Provider` → `llm.Model`; `creds *credentials.Credentials` → `credentials.Lookup`; `bashLLMChecker *policy.SmallLLMChecker` → `bashGate *shellguard.Gate`.
  - `createProvider/createSmallLLM/createTriageProvider/profileProviderFactory.GetProvider`: `llm.NewProvider(ProviderConfig)` → `llm.New(Config)` (field renames `Provider→Service`, `RetryConfig→Retry`); `InferProviderFromModel` → `InferService`; `GetCredential` → `credentials.Resolve`. `profileProviderFactory` → implements `llm.Resolver.Model`. Make sure `MaxTokens` is non-zero (NEW validates).
  - `setupRegistry/setupBashChecker/setupMemory`: rewrite entirely (§2). Order change: the shellguard gate needs `securityScope` at construction → compute `determineSecurityConfig()` before building the registry (or rebuild/replace the bash entry later). `SetSummarizer` → `tools.Fetch(tools.NewSummarizer(rt.smallLLM), tools.WithHTTPTimeout(maxTimeout))`; `SetCredentials` → `tools.Search(rt.creds, …)`; scratchpad → `memory.NewInMemoryStore()` + 4 `tools.Scratchpad*` tools; semantic memory → `tools.Remember/Recall(rt.bleveStore)`; spawn → `tools.NewSpawnBinder()` registered here, `Bind` from executor. `SetBashSecurityCallback(rt.exec.LogBashSecurity)` → `gate.OnDecision = rt.exec.LogBashSecurity`. `tools.SetHTTPTimeout` (runtime.go:424) → per-tool option, so compute `maxTimeout` *before* registry build.
  - `createExecutor`: MCP `Connect`→`Stdio`+`Register`, `SetDeniedTools`→`Deny`; observations → `memory.NewExtractor(rt.smallLLM)` + `rt.bleveStore` (after executor interface reshape, §5); `ProviderFactory: factory` → `Resolver`. `security.*` usage is the other agent's domain but note `SupervisorProvider: rt.provider` / `TriageProvider` will need `llm.Model` typing.
  - Legacy `telemetry.*` also in this file — other agent's domain.
- **util.go**: `parseRetryConfig` unchanged type; only the field it is assigned to renames (`Retry`).
- **main.go / serve.go**: `credentials.Load()` → `credentials.Load(credentials.StandardPaths("grid")...)`; `*credentials.Credentials` → `credentials.Lookup`. serve.go:307 `registry.Register(dispatchTool)` → `registry.Register(tools.New(dispatchTool))` and handle error; `DispatchTool` itself lives in `internal/swarm` (see below).
- **workflow.go**: `policy.LoadFile/ValidateKeys/New` (§3 snippet); drop `w.pol.Workspace = …`; `registerSecurityExtensions` reads `w.pol.Content.Security.Patterns/Keywords`; `ensureWorkspaceInAllowedDirs` unchanged.
- **bridges_test.go**: entirely about `policy.LLMProviderFromChatProvider` → DROPPED; delete or rewrite against `shellguard.Gate.CheckDeterministic`.
- **runtime_test.go**: `policy.New()` still compiles; the smoke test references `registry` field only.

### internal/executor  (config.go, executor.go, tools.go, subagent.go, logging.go, *_test.go)
- `config.go`: `Provider llm.Provider`→`llm.Model`; `ProviderFactory llm.ProviderFactory`→`Resolver llm.Resolver`; `Registry *tools.Registry` ok; `Policy *policy.Policy` (or `policy.Lookup`); `MCPManager *mcp.Manager` ok; `ObservationExtractor/ObservationStore` reshaped (§5). (`SecurityVerifier *security.Verifier` — other agent.)
- `executor.go`: `llm.NewSingleProviderFactory` → in-source `singleResolver`; `factory.GetProvider("")` → `resolver.Model("")`; `initSpawner` → `cfg.SpawnBinder.Bind(...)` (add `SpawnBinder *tools.SpawnBinder` to Config) or have executor register `tools.Spawn(fn)` itself (Register errors if already present); `Has("spawn_agent")` → `Has("spawn_agents")`; `getAllToolDefinitions`: **apply policy filter here** (`e.policy.IsToolEnabled(def.Name)`) since `Definitions()` no longer filters, and `Parameters: def.JSONSchema()`; `NewExecutor/NewExecutorWithFactory` signatures take `llm.Model`/`llm.Resolver`.
- `tools.go`: `executeTool` → `e.registry.Execute(ctx, tc.Name, tc.Args)` (string result); `executeMCPTool` result type `*mcp.Result` (fields same); `isExternalTool`/untrusted registration now receives `string`.
- `subagent.go`: `provider llm.Provider`→`llm.Model`; `e.providerFactory.GetProvider(profile)`→`e.resolver.Model(profile)`; tool-def building as above, exclude `"spawn_agents"`.
- `logging.go`: `logLLMCall(..., resp *llm.ChatResponse)` unchanged; `logToolResult(result any)` → `string` (drop Marshal branch).
- `*_test.go`: `llm.NewMockProvider` → in-source mock (§1); `tools.NewRegistry(pol)` → repo `toolset.Build(...)`/`tools.NewRegistry()` + explicit registrations; `policy.New()` now default-deny → set `pol.DefaultDeny = false` where tests expect all tools; `policy.NewRestrictive()`→`policy.New()`; `ToolPolicy{Enabled:true}` → map presence; `pol.Workspace = dir` → pass `dir` to tool constructors; `"spawn_agent"` → `"spawn_agents"`; `TestExecutor_DefaultDenyBlocksToolExecution` still valid once executor filters definitions itself.

### internal/supervision (supervisor.go)
- `provider llm.Provider` / `Config.Provider llm.Provider` → `llm.Model`. `Chat` calls unchanged. Nothing else.

### internal/setup (setup.go)
- `credentials.HasClaudeCliCredentials()` → `credentials.ClaudeCLICredentials() != nil`; `credentials.DefaultPath()` → in-source const/helper; `writeCredentials`: `Load` → `FileStore.SetAPIKey` + `Save(path)`.
- `mcp.NewManager().Connect(...)` → `mcp.Stdio(ctx, cfg)` + `manager.Register(name, client)`; `AllTools`/`Disconnect` unchanged (could skip the Manager and read `client.Tools()` directly).
- Policy-file generator (`setup.go:2085-2160`) emits legacy keys (`enabled`, `allowlist`, `denylist`, `allow_domains`, `[mcp] default_deny/allowed_tools`) → rewrite to NEW schema (`[tools.x]` presence, `allow`, `[mcp] enabled/allow`, `[content.security] patterns/keywords`). Local struct at `setup.go:363-369` decodes `policy.Tools.Bash.Enabled` → update to presence semantics.

### internal/tools/websearch (websearch.go)
- Implements OLD `tools.Tool` shape and relied on OLD `Register` overwriting the builtin by name. Under NEW: `Parameters() map[string]tools.Param{"query": {StringParam, Required:true}, "count": {IntParam}}`; `Execute(ctx, args tools.Args) (string, error)` — read via `args.String("query")`, `args.IntOr("count", 5)`; serialise `[]SearchResult` to a string (JSON) yourself. `CredProvider{GetAPIKey}` → take `credentials.Lookup` and use `string(creds.Get("brave"))`. Registration: simply don't register `tools.Search(...)`; register `tools.New(websearch.New(...))` instead (duplicate names now error). Reassess whether the override is still needed — NEW `web_search.go` may have fixed the DDG endpoint (not verified here).

### internal/swarm (dispatch.go) — not in the listed consumers but it implements `tools.Tool`
- `DispatchTool.Parameters()` → `map[string]tools.Param`; `Execute(ctx, tools.Args) (string, error)`. serve.go registers it via `tools.New(...)`.

### internal/step, internal/config, internal/skills, internal/packaging
- Grep shows **no** imports of the nine packages in this domain. `internal/config` only matters indirectly: its LLM `Provider` strings must be valid NEW `Service` names (`openai-compat`, `litellm`, `ollama-cloud`, … all accepted) and `MaxTokens` must default to non-zero before `llm.New`.

### cmd/agentmem (main.go)
- `memory.NewBleveStore/BleveStoreConfig/ListAll/RecallFIL/Close/ObservationItem` all unchanged. No edits expected beyond `go mod`.

### tests/* (failure, integration, performance, security)
- `llm.NewMockProvider` (13×) → in-source mock. `tools.NewRegistry(pol)` (14×) → repo builder. `policy.New()` default-deny flip; `pol.Workspace` removal; `ToolPolicy{Enabled, AllowDomains, Allowlist, Denylist}` → new shape.
- `tests/security`: tests call `registry.Get("read").Execute(ctx, map[string]interface{}{…})` directly — under NEW, `Get` returns the *guarded* tool but `Execute` needs `tools.Args` → use `registry.Execute(ctx, "read", map[string]any{…})`. `TestSecurity_BashCommandInjection` (allowlist/denylist) must be rewritten around `shellguard` (`userDeniedCommands`; no allowlist). `TestSecurity_DisabledTool` expects `Definitions()` to hide disabled tools → assert on the repo builder (which skips disabled tools) instead. `TestSecurity_WebDomainRestriction` → `Allow` instead of `AllowDomains`; `{"trusted.com", true}` for pattern `*.trusted.com` — verify NEW `matchDomain` semantics (policy.go:408) before keeping that expectation.
- `tests/performance` `BenchmarkToolRegistry`: lists `"read","write","edit","glob","grep","ls","bash"` — all exist in NEW.

---

## 8. Design decisions to surface before implementing

1. **policy.toml schema migration.** Every `policy.toml` in the repo (`brainstorm/`, `examples/agent/`, `examples/swarm/*`) and the setup generator use the legacy layout (`[read] enabled=true`, `[bash] allowlist/denylist`, `[mcp] default_deny/allowed_tools`, `[security] extra_*`). NEW silently ignores them → with `default_deny=true` *all tools are disabled*. Options: (a) convert files + generator to NEW schema and fail loudly on legacy keys via `FromTOMLWithUnknownKeys`; (b) write an in-repo legacy→new translator (OLD parser at `policy/policy.go:100-200` shows the mapping) and keep the files; (c) both with a deprecation warning. Also decide whether `default_deny` absent should mean true (NEW) or false (OLD examples rely on false).
2. **Where policy enforcement lives.** NEW tools do not consult policy. Choose: guards per tool at registration (recommended by NEW docs), plus executor-level `IsToolEnabled` check (already exists) and executor-level definition filtering. Decide whether `AllowedDirs` should additionally be passed as `extraRoots` to fs tools (workspace confinement is otherwise the only hard boundary).
3. **Bash allowlist / sandbox.** OLD supported `allowlist`, `denylist`, `CheckCommand` patterns, and bwrap/docker sandboxing. NEW has only `shellguard` denylist + LLM review. Keep (in-source) or drop?
4. **Credential lookup semantics.** Name normalisation, `[llm]` generic fallback, and open-ended env-var fallback are gone. Decide whether to wrap `Lookup` in-repo to preserve them (affects `searxng` URL via env, xai/openrouter/cerebras keys via env).
5. **Executor observation interfaces.** Reshape to `(findings, insights, lessons)` + `RememberFIL` (clean, no adapters) vs keep `any`-typed interfaces with adapters in cmd/agent.
6. **Spawn wiring.** `tools.NewSpawnBinder` registered by the runtime and bound by the executor, vs executor registering `tools.Spawn` itself (must then avoid duplicate registration). Also `spawn_agent` (single) no longer exists — prompts/docs mentioning it need updating.
7. **Test mock location.** Where the copied `MockProvider` lives (`internal/testutil/llmmock` suggested) and whether `tests/*` (external packages) may import an `internal/` path — they are inside the module, so yes.
