# Headless Agent — Requirements

## R1. Agentfile Parser

### R1.1 Lexer
- R1.1.1: Tokenize keywords: NAME, INPUT, AGENT, GOAL, RUN, LOOP, FROM, USING, WITHIN, DEFAULT
- R1.1.2: Tokenize identifiers (alphanumeric + underscore)
- R1.1.3: Tokenize strings (double-quoted, support escape sequences)
- R1.1.4: Tokenize paths (for FROM clause)
- R1.1.5: Tokenize variables ($identifier)
- R1.1.6: Tokenize numbers (integers)
- R1.1.7: Tokenize commas (identifier lists)
- R1.1.8: Skip comments (# to end of line)
- R1.1.9: Skip empty lines
- R1.1.10: Track line numbers for error reporting

### R1.2 Parser
- R1.2.1: Parse NAME statement
- R1.2.2: Parse INPUT statement with optional DEFAULT
- R1.2.3: Parse AGENT statement with FROM path
- R1.2.4: Parse GOAL statement with inline string or FROM path
- R1.2.5: Parse GOAL statement with optional USING clause
- R1.2.6: Parse RUN statement with USING identifier list
- R1.2.7: Parse LOOP statement with USING identifier list and WITHIN limit
- R1.2.8: Support variable references in WITHIN clause
- R1.2.9: Produce AST with Workflow, Input, Agent, Goal, Step nodes
- R1.2.10: Report syntax errors with line numbers

### R1.3 Validation
- R1.3.1: Verify all agents referenced in USING clauses are defined
- R1.3.2: Verify all goals referenced in RUN/LOOP are defined
- R1.3.3: Verify goals are defined before use (in file order)
- R1.3.4: Verify all FROM paths exist and are readable
- R1.3.5: Verify no circular dependencies in goal references
- R1.3.6: Verify NAME is specified exactly once
- R1.3.7: Verify at least one RUN or LOOP step exists

### R1.4 File Loading
- R1.4.1: Load external prompt files (FROM paths)
- R1.4.2: Resolve paths relative to Agentfile location
- R1.4.3: Support .md and .txt prompt files
- R1.4.4: Report file not found errors with context

---

## R2. Workflow Executor

### R2.1 Input Binding
- R2.1.1: Accept inputs as key-value pairs from CLI
- R2.1.2: Apply DEFAULT values for missing inputs
- R2.1.3: Error on missing required inputs (no DEFAULT)
- R2.1.4: Store bound inputs in execution state

### R2.2 Step Execution
- R2.2.1: Execute RUN/LOOP steps in file order
- R2.2.2: For RUN: execute goals sequentially
- R2.2.3: For LOOP: repeat goals until convergence or max iterations
- R2.2.4: Pass execution state between goals
- R2.2.5: Support variable interpolation in goal prompts ($var)

### R2.3 Convergence Detection
- R2.3.1: Detect explicit convergence (LLM signals goal achieved)
- R2.3.2: Detect implicit convergence (no tool calls made)
- R2.3.3: Detect state unchanged (no progress from previous iteration)
- R2.3.4: Exit loop when any convergence condition met
- R2.3.5: Exit loop when WITHIN iteration limit reached

### R2.4 State Management
- R2.4.1: Track goal outputs in execution state
- R2.4.2: Make prior goal outputs available via $goal_name
- R2.4.3: Track current loop iteration count
- R2.4.4: Track overall workflow progress

---

## R3. Agent Core

### R3.1 Goal Execution
- R3.1.1: Build system prompt for goal
- R3.1.2: Build user prompt from goal outcome + state context
- R3.1.3: Send prompt to LLM provider
- R3.1.4: Process LLM response
- R3.1.5: Execute tool calls from response
- R3.1.6: Loop until LLM signals goal complete (no pending tool calls)
- R3.1.7: Update execution state with goal result

### R3.2 Multi-Agent Orchestration
- R3.2.1: Detect USING clause on goal
- R3.2.2: Spawn parallel LLM calls (one per agent)
- R3.2.3: Include agent persona in each system prompt
- R3.2.4: Collect all agent responses
- R3.2.5: Synthesize responses (send to LLM for reconciliation)
- R3.2.6: Return synthesized result as goal output

### R3.3 Prompt Construction
- R3.3.1: Load agent prompt from file (AGENT FROM)
- R3.3.2: Load goal prompt from file or inline (GOAL FROM / GOAL "...")
- R3.3.3: Interpolate variables ($var) in prompts
- R3.3.4: Include relevant execution state context
- R3.3.5: Include tool definitions in LLM request

---

## R4. LLM Provider

### R4.1 Provider Interface
- R4.1.1: Define Chat method (messages, tools) → response
- R4.1.2: Define Stream method (messages, tools) → event channel
- R4.1.3: Support tool definitions in LLM schema format
- R4.1.4: Parse tool calls from LLM response

### R4.2 Provider Adapters
- R4.2.1: Use native LLM SDKs (anthropic-sdk-go, openai-go, generative-ai-go)
- R4.2.2: Configure provider (Anthropic, OpenAI, Gemini, etc.)
- R4.2.3: Configure model selection
- R4.2.4: Configure max tokens
- R4.2.5: Handle API key from environment variable
- R4.2.6: Handle rate limiting and retries

---

## R5. Tool Registry

### R5.1 Registry
- R5.1.1: Register built-in tools at startup
- R5.1.2: Provide tool definitions for LLM (name, description, schema)
- R5.1.3: Look up tool by name for execution
- R5.1.4: Execute tool with arguments, return result
- R5.1.5: Filter available tools based on policy (enabled flag)

### R5.2 Built-in Tools — File

#### R5.2.1 read
- Read file contents from path
- Return text content or error

#### R5.2.2 write
- Write content to path
- Create parent directories if needed
- Overwrite existing files

#### R5.2.3 edit
- Find and replace text in file
- Exact match required
- Report if pattern not found

#### R5.2.4 glob
- Pattern-based file search
- Support * and ** patterns
- Return list of matching paths

#### R5.2.5 grep
- Regex content search
- Search in file or directory
- Return matching lines with context

#### R5.2.6 ls
- List directory contents
- Return file names and types

### R5.3 Built-in Tools — Execution

#### R5.3.1 bash
- Execute shell command
- Capture stdout, stderr, exit code
- Enforce allowlist/denylist from policy

### R5.4 Built-in Tools — Web

#### R5.4.1 web_fetch
- Fetch URL content
- Route through Internet Gateway (when configured)
- Enforce domain allowlist from policy
- Enforce rate limit from policy

#### R5.4.2 web_search
- Search query via gateway
- Route through Internet Gateway (when configured)
- Enforce rate limit from policy

### R5.5 Built-in Tools — Memory

#### R5.5.1 memory_read
- Read value by key from persistent store

#### R5.5.2 memory_write
- Write key-value to persistent store

---

## R6. Security Policy

### R6.1 Policy Loading
- R6.1.1: Load policy.toml from workflow directory
- R6.1.2: Parse TOML into policy structure
- R6.1.3: Apply defaults for missing tool sections
- R6.1.4: Validate policy structure

### R6.2 Policy Enforcement
- R6.2.1: Check tool enabled flag before execution
- R6.2.2: Check global default_deny setting
- R6.2.3: Evaluate deny patterns (deny wins on match)
- R6.2.4: Evaluate allow patterns
- R6.2.5: Block if default_deny=true and no allow match

### R6.3 Path Policy
- R6.3.1: Expand $WORKSPACE variable to workspace path
- R6.3.2: Expand ~ to user home directory
- R6.3.3: Match paths against glob patterns
- R6.3.4: Support * (single segment) and ** (recursive)

### R6.4 Bash Policy
- R6.4.1: Check command against denylist first
- R6.4.2: Check command against allowlist
- R6.4.3: Block if not in allowlist
- R6.4.4: Support glob patterns in command matching

### R6.5 Web Policy
- R6.5.1: Check domain against allow_domains list
- R6.5.2: Enforce rate_limit (requests per minute)
- R6.5.3: Track request counts per time window

---

## R7. Session Manager

### R7.1 Session Lifecycle
- R7.1.1: Create new session for workflow run
- R7.1.2: Generate unique session ID
- R7.1.3: Store workflow name and inputs
- R7.1.4: Update session on state changes
- R7.1.5: Mark session complete or failed on finish

### R7.2 State Persistence
- R7.2.1: Persist execution state after each goal
- R7.2.2: Persist all messages (user, assistant, tool)
- R7.2.3: Persist tool call details (args, result, timing)
- R7.2.4: Support recovery from persisted state

### R7.3 SQLite Backend
- R7.3.1: Create database schema on first run
- R7.3.2: Store sessions table
- R7.3.3: Store messages table
- R7.3.4: Store tool_calls table
- R7.3.5: Query session by ID

### R7.4 Filesystem Backend
- R7.4.1: Store session as JSON file
- R7.4.2: One file per session
- R7.4.3: Atomic writes (write temp, rename)

---

## R8. Telemetry Exporter

### R8.1 Message Export
- R8.1.1: Export all LLM messages (user, assistant, tool)
- R8.1.2: Include session_id, workflow_name, goal
- R8.1.3: Include agent name (for multi-agent goals)
- R8.1.4: Include token counts (in, out)
- R8.1.5: Include latency
- R8.1.6: Include model name
- R8.1.7: Include iteration count (for loops)

### R8.2 Log Export
- R8.2.1: Export agent logs (debug, info, warn, error)
- R8.2.2: Include structured fields
- R8.2.3: Include timestamp

### R8.3 Exporters

#### R8.3.1 HTTP Exporter
- POST to configured endpoint
- Batch messages for efficiency
- Retry on failure

#### R8.3.2 OTLP Exporter
- Send via OpenTelemetry Protocol
- Support logs signal
- Configure endpoint

#### R8.3.3 File Exporter
- Write JSON lines to local file
- One file per session
- For offline/debug use

#### R8.3.4 Noop Exporter
- Discard all telemetry
- For telemetry disabled mode

---

## R9. Transport (stdio)

### R9.1 JSON-RPC 2.0
- R9.1.1: Read JSON-RPC requests from stdin
- R9.1.2: Write JSON-RPC responses to stdout
- R9.1.3: Support request/response pattern
- R9.1.4: Support notifications (events)

### R9.2 Methods
- R9.2.1: Implement `run` method (execute workflow)
- R9.2.2: Accept file path and inputs as params
- R9.2.3: Return status and session_id

### R9.3 Events
- R9.3.1: Emit `goal_started` event
- R9.3.2: Emit `goal_complete` event
- R9.3.3: Emit `tool_call` event
- R9.3.4: Emit `loop_iteration` event
- R9.3.5: Emit `error` event

---

## R10. Configuration

### R10.1 Config File
- R10.1.1: Load config from JSON file
- R10.1.2: Support --config CLI flag
- R10.1.3: Default to agent.json in current directory

### R10.2 Config Sections
- R10.2.1: agent.id — agent identifier
- R10.2.2: agent.workspace — workspace directory path
- R10.2.3: llm.provider — LLM provider name
- R10.2.4: llm.model — model name
- R10.2.5: llm.api_key_env — environment variable for API key
- R10.2.6: llm.max_tokens — max tokens per request
- R10.2.7: web.gateway_url — Internet Gateway URL
- R10.2.8: web.gateway_token_env — env var for gateway token
- R10.2.9: telemetry.enabled — enable/disable telemetry
- R10.2.10: telemetry.endpoint — telemetry service URL
- R10.2.11: telemetry.protocol — http, otlp, file, noop
- R10.2.12: session.store — sqlite or file
- R10.2.13: session.path — path to session storage

---

## R11. CLI

### R11.1 Commands
- R11.1.1: `agent run <Agentfile>` — run workflow
- R11.1.2: `agent validate <Agentfile>` — validate syntax
- R11.1.3: `agent inspect <Agentfile>` — show workflow structure

### R11.2 Flags
- R11.2.1: `--input key=value` — provide input (repeatable)
- R11.2.2: `--config <path>` — config file path
- R11.2.3: `--policy <path>` — policy file path (override default)
- R11.2.4: `--workspace <path>` — workspace directory (override config)

### R11.3 Output
- R11.3.1: Print workflow events to stderr
- R11.3.2: Print final result to stdout
- R11.3.3: Exit code 0 on success, non-zero on failure

---

## R12. Docker Deployment

### R12.1 Dockerfile
- R12.1.1: Multi-stage build (builder + runtime)
- R12.1.2: Use golang:1.24-alpine for builder
- R12.1.3: Use alpine:3.21 for runtime
- R12.1.4: Install git in runtime (for bash git commands)
- R12.1.5: Copy binary to /usr/local/bin/agent
- R12.1.6: Set WORKDIR to /workspace
- R12.1.7: Set ENTRYPOINT to agent binary

### R12.2 Runtime
- R12.2.1: Mount workspace as volume
- R12.2.2: Mount data directory for sessions
- R12.2.3: Pass API keys via environment variables
- R12.2.4: Support stdin/stdout for stdio transport

---

## R13. Error Handling

### R13.1 Parser Errors
- R13.1.1: Syntax errors with line numbers
- R13.1.2: Undefined reference errors
- R13.1.3: File not found errors for FROM paths

### R13.2 Runtime Errors
- R13.2.1: LLM API errors (retry with backoff)
- R13.2.2: Tool execution errors (report to LLM)
- R13.2.3: Policy violation errors (block and report)
- R13.2.4: Timeout errors (configurable)

### R13.3 Recovery
- R13.3.1: Persist state before each goal
- R13.3.2: Support resume from last successful goal
- R13.3.3: Log all errors to telemetry
