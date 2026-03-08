# Swarm Collaboration: Deliberation, Interrupts, and Shared Context

*Design Document — March 2026*

---

## 1. Introduction

A swarm is a collection of autonomous agents working toward a common goal. Each agent brings a distinct capability — one writes frontend code, another handles backend logic, a third manages infrastructure. The power of a swarm lies not in individual agent capability, but in how agents coordinate: how they divide work, share discoveries, adapt to changing circumstances, and converge on a coherent result.

This document presents a collaboration model for swarm agents built on three interconnected mechanisms: **deliberation**, **execution with interrupts**, and **shared swarm context**. Together, these mechanisms transform agents from isolated workers that receive tasks and produce outputs into collaborative participants that discuss, negotiate, adapt, and align.

### 1.1 The Problem with the Current Model

The current swarm design follows a **decide-execute-forget** pattern:

1. A task arrives on `work.<capability>.<task_id>` or `discuss.<task_id>`.
2. A single LLM call triages the task, producing one of three outcomes: EXECUTE, COMMENT, or SKIP.
3. If EXECUTE, the agent runs its workflow linearly, producing a result on `done.<capability>.<task_id>`.
4. If COMMENT, the agent publishes a single message on `discuss.<task_id>` and moves on.
5. If SKIP, the task is ignored entirely.

This model has three fundamental deficiencies:

**Triage is a point-in-time bet.** The agent makes an irrevocable decision based on whatever context exists at the moment the task arrives. There is no mechanism to revise this decision as new information surfaces. An agent that SKIPs a task cannot later decide it was relevant. An agent that EXECUTEs cannot discover, mid-execution, that another agent has already solved the problem differently.

**Execution is deaf.** Once an agent begins executing, it enters a closed loop: LLM generates tool calls, tools execute, results feed back to the LLM, repeat until the goal is satisfied. No external information penetrates this loop. If the frontend agent announces on `discuss.*` that it has switched from REST to WebSocket, the backend agent — currently writing REST handlers — will not hear this until it publishes its now-incompatible result.

**Discussion is fire-and-forget.** A COMMENT decision produces a single message. There is no mechanism for back-and-forth deliberation, for building shared understanding through dialogue, or for agents to negotiate who handles what. The discuss channel exists, but agents use it as a bulletin board, not a conversation medium.

### 1.2 Design Principles

The collaboration model presented here is guided by four principles:

**Agents are participants, not workers.** A worker receives instructions and produces output. A participant engages with the problem, discusses approaches with peers, adapts to changing circumstances, and takes ownership of a self-determined portion of the work. The collaboration model treats agents as the latter.

**Discussion and execution are interleaved, not sequential.** In human teams, a developer does not wait for all discussion to conclude before writing any code. They discuss, start working, encounter something unexpected, discuss again, adjust, continue. The collaboration model supports this natural rhythm.

**Awareness is continuous, not episodic.** An agent should always have access to what is happening in the swarm — who is working on what, what decisions have been made, what remains unresolved. This awareness should be maintained passively, without requiring the agent to explicitly request it.

**Coordination emerges from communication, not from central control.** There is no dispatcher that assigns work to agents. Agents observe the swarm state, participate in discussion, and self-select their contributions. The NATS messaging infrastructure provides the communication substrate; the agents provide the intelligence.

---

## 2. The Agent State Machine

At any moment, a swarm agent is in exactly one of four states. These states govern how the agent processes incoming messages, whether it participates in discussion, and whether it is actively executing work.

### 2.1 States

**MONITORING.** The agent is idle but attentive. It listens to all NATS subjects it is subscribed to — `work.*`, `discuss.*`, `done.*`, `heartbeat.*` — and updates its swarm context (Section 5) as messages arrive. It does not participate in discussions or execute work. This is the agent's resting state.

An agent enters MONITORING when it first joins the swarm, when it completes a task, or when it aborts execution and steps back. MONITORING is not passive disengagement — the agent is actively maintaining its understanding of the swarm. It simply has no current task.

**DELIBERATING.** The agent is engaged in discussion about a specific task. It reads and contributes to messages on `discuss.<task_id>`, building understanding of what the task requires, what other agents are planning, and what role it should play. Deliberation is a multi-round process — the agent may ask questions, answer others' questions, propose approaches, and refine its understanding through dialogue.

Deliberation ends when the agent either claims a portion of the work (transitioning to EXECUTING) or determines the task is not relevant to its capabilities (transitioning back to MONITORING).

**EXECUTING.** The agent is actively running its workflow — invoking tools, generating code, processing data. During execution, the agent remains connected to `discuss.<task_id>` through an interrupt buffer (Section 4). It is focused on its claimed work but can be interrupted by relevant new information.

**PAUSED.** The agent has temporarily suspended execution because an interrupt requires discussion before work can continue. The agent re-enters deliberation on `discuss.<task_id>`, specifically about the concern that caused the pause. Once resolved, the agent resumes execution from where it stopped.

PAUSED is distinct from DELIBERATING in one important way: a PAUSED agent has execution state — partially completed work, a position in its workflow, accumulated tool results. When it resumes, it continues from that state rather than starting fresh. A DELIBERATING agent has no execution state; it is deciding whether and what to execute.

### 2.2 Transitions

The state machine has the following transitions:

**MONITORING → DELIBERATING.** Triggered when a task arrives that is relevant to the agent's capability. Relevance is determined by the existing similarity-based triage mechanism — if the task's semantic similarity to the agent's capability exceeds the configured threshold, the agent enters deliberation.

**DELIBERATING → EXECUTING.** Triggered when the agent publishes a CLAIM on `discuss.<task_id>`. The CLAIM specifies what portion of the task the agent is taking responsibility for. This transition initializes the interrupt buffer and begins workflow execution.

**DELIBERATING → MONITORING.** Triggered when the agent determines, through deliberation, that the task is not relevant to its capabilities, or that other agents have the task fully covered. The agent may optionally publish a brief message on `discuss.<task_id>` explaining its withdrawal.

**EXECUTING → MONITORING.** Triggered on successful completion. The agent publishes its result on `done.<capability>.<task_id>` and returns to monitoring.

**EXECUTING → PAUSED.** Triggered when the agent, upon processing an interrupt (Section 4), determines it cannot continue without further discussion. The agent publishes its concern on `discuss.<task_id>` and waits.

**EXECUTING → MONITORING (via abort).** Triggered when the agent determines its work is no longer viable. The agent publishes the abort reason on `discuss.<task_id>`, stops execution, and returns to monitoring. Partial work is not cleaned up — other agents may find it useful. The agent continues to watch `discuss.<task_id>` and may re-enter DELIBERATING if subsequent messages resolve the abort reason.

**PAUSED → EXECUTING.** Triggered when the concern that caused the pause is resolved through discussion. The agent resumes execution from its saved state.

**PAUSED → MONITORING (via abort).** Same as EXECUTING → MONITORING abort, but from the paused state.

### 2.3 State Visibility

Each agent's current state is communicated to the swarm through the existing heartbeat mechanism. The heartbeat metadata already includes agent name, capability, and session information. The state field is added to this metadata, allowing all agents (and the UI) to observe the swarm's collective state at a glance.

---

## 3. Deliberation

Deliberation is the process by which agents develop shared understanding of a task before committing to execution. It replaces the current single-shot triage decision with a multi-round discussion that allows agents to ask questions, propose approaches, negotiate responsibilities, and signal readiness.

### 3.1 Entry

When a task arrives and passes the relevance threshold, the agent enters deliberation. Its first action is to read any existing messages on `discuss.<task_id>` — other agents may have already begun discussing the task. The agent then makes its initial assessment: does it understand the task well enough to claim a portion, or does it need more information?

### 3.2 Signals

During deliberation, each agent's contribution on `discuss.<task_id>` includes a structured signal alongside its natural language content. There are exactly two signals:

**NEED_INFO.** The agent has questions or is waiting for information before it can commit. This signal tells other agents that deliberation is not yet complete — at least one participant still needs clarity. A NEED_INFO message typically includes the specific question or dependency: "I need to know the API contract before I can implement the handlers" or "Waiting for the coder to finish before I can write tests."

**CLAIM.** The agent has sufficient understanding and is committing to a specific portion of the work. The CLAIM message specifies what the agent is taking responsibility for: "CLAIM: I'm handling the REST API routes, request validation, and database queries." This specificity is important — it tells other agents what is covered and, by implication, what remains unclaimed.

### 3.3 Convergence

Deliberation converges when all relevant agents have either CLAIMed their portion or withdrawn to MONITORING. There is no explicit "deliberation complete" signal — convergence is emergent. Each agent independently decides when it has enough information to CLAIM or enough evidence to withdraw.

A practical consideration: some agents may depend on others' work. The tester cannot CLAIM until the coder has CLAIMed (and ideally completed). This is natural and expected — the tester remains in deliberation with NEED_INFO until the dependency is satisfied. It does not block other agents from CLAIMing and executing.

### 3.4 Straggler Handling

If an agent fails to participate in deliberation within the configured straggler timeout (default: 30 seconds), other agents proceed without it. The non-responsive agent may still join later — if it enters deliberation after others have already CLAIMed, it can read the discussion history and decide whether there is unclaimed work for it.

The straggler timeout is a swarm-level configuration parameter, not a per-agent setting. It represents the swarm operator's tolerance for waiting.

### 3.5 Deliberation Context

Each deliberation round is an LLM call with the following context:

- The task description (from the original `work.*` or `discuss.*` message)
- The agent's capability description and persona (from its Agentfile)
- All discuss messages for this task (or a summary if they exceed the context summary threshold — see Section 6)
- The agent's current swarm context snapshot (Section 5)

The LLM produces both a natural language contribution (the discussion content) and a structured signal (NEED_INFO or CLAIM). If the signal is CLAIM, the agent transitions to EXECUTING.

---

## 4. Execution with Interrupts

Once an agent CLAIMs a task and begins execution, it does not become deaf to the swarm. An interrupt mechanism allows new information to reach the agent at natural breakpoints in its workflow, enabling it to adapt its approach without losing progress.

### 4.1 The Interrupt Buffer

When an agent transitions to EXECUTING, it initializes an **interrupt buffer** — a thread-safe queue that collects messages arriving on `discuss.<task_id>`. The NATS subscriber writes to this buffer; the executor reads from it.

The buffer is a simple FIFO queue. Messages are not filtered, prioritized, or deduplicated at the buffer level — that is the LLM's job when it processes them. The buffer's only responsibility is to safely bridge the asynchronous NATS message flow with the synchronous executor loop.

### 4.2 Injection Point

The interrupt buffer is checked at exactly one point in the execution cycle: **between LLM turns, after all tool results have been collected and before the next LLM invocation.**

The execution cycle of a single LLM turn is:

1. LLM receives context (messages, tool results, etc.)
2. LLM produces a response (text, tool calls, or both)
3. If tool calls are present, all requested tools execute (possibly in parallel)
4. Tool results are collected
5. **Interrupt check: drain the buffer**
6. Tool results (and any interrupts) are assembled into the next LLM turn
7. Return to step 1

This injection point is chosen for three reasons. First, it avoids the complexity of interrupting parallel tool execution — all tools complete before interrupts are considered. Second, it provides a natural seam where new context can be introduced without corrupting in-progress work. Third, it guarantees that the LLM sees interrupts before making its next decision, including the decision that the task is complete.

The third point deserves emphasis. If the LLM's previous turn was intended to be the final one — producing the task result — the interrupt check still occurs before that result is published. The LLM receives both its tool results and the new interrupt messages, and can decide whether the result is still valid or whether the interrupts necessitate further work. Completion is never premature.

### 4.3 The Interrupts Block

When the interrupt buffer contains messages, they are formatted into an XML block and prepended to the next LLM turn. The block has three components:

```xml
<interrupts>
  <context>
    Current execution state: implementing REST API route handlers.
    Completed: schema definition (step 1), route scaffolding (step 2).
    Remaining: handler implementation (step 3), integration wiring (step 4).
  </context>

  <messages>
    <message from="frontend" timestamp="2026-03-08T14:30:00Z">
      We're switching to WebSocket for the notification feed.
      REST is fine for CRUD operations but real-time updates need
      a persistent connection. Updated interface spec is in
      /shared/specs/notification-api.md.
    </message>
    <message from="webserver" timestamp="2026-03-08T14:31:00Z">
      I can handle the WebSocket upgrade in the reverse proxy config.
      Backend just needs to implement the WS handler.
    </message>
  </messages>

  <guidance>
    The above messages arrived while you were executing. Evaluate
    them against your current work and decide:

    CONTINUE — The messages are irrelevant to your current step.
               Proceed with your plan unchanged.
    ADJUST   — The messages affect your approach. Modify your
               remaining work accordingly and continue executing.
    PAUSE    — You cannot proceed without further discussion.
               Publish your concern on discuss and wait.
    ABORT    — Your work is no longer viable given this new
               information. Publish the reason and stop.

    State your decision and reasoning before taking action.
  </guidance>
</interrupts>
```

The **context** element grounds the LLM in what it was doing. Without this, the LLM must reconstruct its execution state from the conversation history — feasible but error-prone and token-expensive. The context element makes the agent's current position explicit.

The **messages** element contains the raw discuss messages, preserving attribution and timestamps. These are not summarized — the LLM needs the original wording to assess relevance and implications.

The **guidance** element provides the decision framework. It defines exactly four options, each with clear semantics. This is not merely instructional — it constrains the LLM's response space, reducing the likelihood of ambiguous or unexpected reactions to interrupts.

### 4.4 Interrupt Decisions

When the LLM processes an interrupts block, it produces one of four decisions:

**CONTINUE.** The interrupt messages are not relevant to the agent's current work. Execution proceeds unchanged. The messages are recorded in the swarm context (Section 5) for future reference but do not affect the current workflow.

Example: The frontend agent discusses CSS framework choices. The backend agent, implementing database queries, continues without adjustment.

**ADJUST.** The interrupt messages are relevant and require the agent to modify its approach, but the agent can incorporate the changes without stopping. The LLM adjusts its plan for remaining steps and continues executing.

Example: The frontend agent announces it will send dates in ISO 8601 format instead of Unix timestamps. The backend agent adjusts its serialization logic in the next tool call.

**PAUSE.** The interrupt raises a question the agent cannot resolve alone. The agent publishes its concern on `discuss.<task_id>` and transitions to the PAUSED state (Section 2.1). Execution state is preserved — the agent's position in the workflow, accumulated tool results, and the current conversation context are all retained.

Example: The frontend agent announces a complete API redesign. The backend agent cannot determine which of its completed work is still valid without discussing the new design. It pauses and asks for clarification.

**ABORT.** The interrupt reveals that the agent's entire approach is no longer viable. The agent publishes the abort reason on `discuss.<task_id>`, stops execution, and transitions to MONITORING. Partial work is left in place — other agents or future tasks may build on it.

Example: The team decides to replace the custom backend with a third-party service. The backend agent's work is no longer needed. It aborts but remains in MONITORING, ready to re-engage if the decision is reversed.

### 4.5 Interrupt Processing Cost

Each interrupt check that finds messages incurs an LLM call — the LLM must evaluate the interrupts and decide how to respond. This cost is inherent and unavoidable; the agent cannot decide relevance without reasoning about the messages.

However, interrupt checks that find an empty buffer incur zero cost — the check is a non-blocking channel read that completes in nanoseconds. Since most tool call cycles will produce empty buffers (discuss messages are infrequent relative to tool calls), the amortized cost of interrupt checking is low.

The swarm operator can disable interrupt checking entirely by setting `interrupt_check: false` in the swarm manifest, reverting to the current deaf-execution model. This is a tradeoff: lower cost at the expense of adaptability.

---

## 5. Swarm Context

Each agent maintains a personal, in-memory representation of the swarm's state. This representation — the **swarm context** — provides the agent with continuous awareness of what other agents are doing, what has been decided, and what work has been completed.

### 5.1 Nature of Swarm Context

Swarm context is **personal, ephemeral, and asynchronous.**

**Personal.** Each agent maintains its own swarm context independently. There is no shared consensus state, no central authority, and no synchronization protocol. Agent A's understanding of the swarm may differ slightly from Agent B's — perhaps Agent A processed a heartbeat message that Agent B has not yet received.

This divergence is acceptable and even desirable. In human teams, each member holds a slightly different mental model of the project's state. These models are approximately aligned through communication (standups, code reviews, conversations) and precisely aligned when it matters (pull request discussions, design reviews). The same applies here: the discuss channel is where divergent understandings converge.

The alternative — a shared consensus state — would require distributed consensus protocols, write coordination, conflict resolution, and an authority model. This is a distributed systems problem layered on top of an AI coordination problem, and the complexity is not justified. Personal interpretation with communication-based alignment is simpler, more resilient, and sufficient.

**Ephemeral.** Swarm context exists only in memory for the duration of the agent's participation in the swarm. It is not persisted to disk. When the swarm shuts down, context is lost. When the swarm restarts, context is rebuilt from the NATS message stream (if JetStream persistence is enabled) or starts empty.

This is appropriate because swarm context represents *current* state, not historical knowledge. "Agent X is currently executing task Y" is valuable now but meaningless after the swarm completes. Long-term knowledge — lessons learned, architectural decisions — belongs in the agent's persistent memory (BM25/semantic graph), not in ephemeral swarm context.

**Asynchronous.** Swarm context is updated by a background goroutine that listens to NATS messages, independent of the executor's LLM loop. The executor reads from swarm context at specific injection points; it never writes to it. This separation ensures that context updates do not block execution and that the executor always has access to the latest available state without explicitly requesting it.

### 5.2 Contents

Swarm context contains three categories of information, each maintained by a different NATS subscription:

**Agent states** (from `heartbeat.*`). A map of agent name to current status: state (MONITORING, DELIBERATING, EXECUTING, PAUSED), capability, current task (if any), and last heartbeat timestamp. This is always current — each heartbeat overwrites the previous entry. The data is compact: one map entry per agent, no accumulation.

**Task discussions** (from `discuss.*`). A per-task log of discussion messages. When the log for a task exceeds the configured summary threshold (default: 10 messages), older messages are summarized by the small LLM and replaced with the summary. Recent messages are preserved verbatim. This sliding window ensures discussion context remains bounded while retaining the most current exchanges in full fidelity.

**Completed work** (from `done.*`). A log of task completions: which agent completed what, with a brief summary of the result. This gives agents awareness of what has been accomplished without requiring them to read full results.

### 5.3 Relevance Filtering

Not all swarm activity is relevant to every agent. A backend agent does not need detailed awareness of CSS discussions between the frontend and design agents. Swarm context applies a relevance filter:

- **Agent states**: Always included for all agents. This is compact and universally useful — knowing who is alive and what they're doing costs few tokens and provides broad situational awareness.
- **Task discussions**: Only the current task's discussion is included in full. Other tasks' discussions are excluded unless the agent is subscribed to them.
- **Completed work**: Included only for tasks whose capability is related to the agent's own capability or whose discuss messages the agent participated in. Unrelated completions are excluded.

The goal is to answer: **does this information help the agent do its current job?** If not, it does not enter the context.

### 5.4 Injection Points

Swarm context is injected into the LLM's input at exactly two points:

**During deliberation.** Every deliberation round includes the current swarm context snapshot. The agent needs to know what others are doing to decide its own role — who has already CLAIMed, what dependencies exist, what the overall swarm state is. This is essential for informed deliberation.

**During interrupt processing.** When an interrupts block is assembled (Section 4.3), the current swarm context snapshot is available as additional grounding. This helps the LLM evaluate whether an interrupt is relevant: if it knows that the frontend agent has CLAIMed the WebSocket migration, a message about WebSocket from that agent carries more weight than a speculative comment.

Swarm context is **not** injected during normal execution turns (tool calls and result processing). When the agent is heads-down writing code, swarm awareness is noise. The interrupt mechanism handles the case where external developments require attention; swarm context is not needed when no interrupts are present.

---

## 6. Context Management

Deliberation, interrupts, and swarm context all contribute to the token budget consumed by each LLM call. Without active management, a long deliberation with many participants or a task that accumulates many interrupts can exceed the model's context window. This section describes how context is kept bounded.

### 6.1 Discussion Summarization

When the discussion log for a task exceeds the configured threshold (default: 10 messages), the oldest messages are summarized by the small LLM. The summary replaces the original messages; recent messages (those arriving after the last summarization) are preserved verbatim.

The summarization prompt applies **Shannon's information theory** as a guiding principle. In information theory, the information content of a signal is inversely proportional to its probability — rare, surprising signals carry more information than common, expected ones. Applied to discussion summarization:

```
Summarize the following discussion using Shannon's information
theory as a guiding principle: high-entropy (surprising, rare,
contradictory) signals carry more information than low-entropy
(expected, repeated) ones.

Preserve:
1. Decisions made (who committed to what)
2. Constraints or requirements stated
3. High-information-content insights — surprising, contradictory,
   or novel points. A single dissent outweighs repeated agreement.
4. Unresolved questions

Compress consensus into brief statements. Preserve outliers verbatim.
```

This approach ensures that summarization does not destroy the most valuable information. A discussion where five agents agree on REST and one suggests WebSocket should produce a summary that prominently features the WebSocket suggestion — it is the high-entropy signal. Repeated agreements ("sounds good," "I agree") compress into a single statement ("all agents agreed on REST for CRUD operations").

Summarization is lazy: it runs only when the discussion log is needed (for a deliberation round or interrupt context) and has grown beyond the threshold since the last summarization. This avoids unnecessary LLM calls for discussions that are no longer active.

### 6.2 Interrupt History

During execution, processed interrupts — those the LLM has already evaluated and decided on — accumulate. If many interrupts arrive over a long execution, the raw interrupt history can grow large.

Processed interrupts are consolidated using the same summarization approach as discussions. The summary captures what interrupts arrived, what decisions the agent made (CONTINUE, ADJUST), and what adjustments were applied. New, unprocessed interrupts remain verbatim. This ensures the LLM has full context on recent interrupts while maintaining a compressed history of earlier ones.

### 6.3 Deliberation-to-Execution Handoff

When an agent transitions from DELIBERATING to EXECUTING, the full deliberation history does not transfer into the execution context. Instead, the deliberation outcome is summarized into a compact handoff:

- What the task requires (from the original task description)
- What the agent CLAIMed (its specific responsibilities)
- Key decisions from deliberation (constraints, agreements, dependencies)
- What other agents are handling (from their CLAIMs)

This handoff is injected as context in the first execution LLM turn. It is typically a few paragraphs — enough to ground the agent in what was decided without carrying the full deliberation transcript.

---

## 7. Scenarios

This section walks through concrete scenarios to illustrate how the collaboration mechanisms interact in practice. Each scenario uses a three-agent swarm: **frontend** (UI/HTML/CSS/JS), **backend** (API/database), and **webserver** (routing/proxy/infrastructure).

### 7.1 Scenario: Clean Task Division

**Task submitted:** "Build a user registration system with email verification."

1. Task arrives on `work.*`. All three agents pass the relevance threshold and enter DELIBERATING.

2. **Backend** (first to respond): "I'll handle the registration API endpoint, user database schema, and email verification token generation. NEED_INFO: What email service are we using?"

3. **Frontend**: "I'll build the registration form and the email verification landing page. NEED_INFO: What fields does the registration form need?"

4. **Webserver**: "I'll set up the routes: POST /register → backend, GET /verify → backend, static assets → frontend build output. NEED_INFO: Are there any rate limiting requirements?"

5. **Backend**: "Registration fields: email, password, display name. No specific rate limiting unless frontend wants it. CLAIM: Registration API, user schema, email token generation."

6. **Frontend**: "Got it — three fields plus a submit button. CLAIM: Registration form, verification landing page, client-side validation."

7. **Webserver**: "CLAIM: Route configuration, reverse proxy rules, static asset serving."

8. All three agents transition to EXECUTING. Each works on its claimed portion. No interrupts arise because the task was cleanly divided.

This scenario illustrates the simplest case: agents discuss briefly, divide work, and execute independently. Deliberation took four rounds and produced a clear division of responsibility.

### 7.2 Scenario: Mid-Execution Course Correction

**Task submitted:** "Add real-time notifications to the dashboard."

1. All three agents deliberate. Backend CLAIMs the notification API (REST), frontend CLAIMs the notification UI panel, webserver CLAIMs the routing.

2. Frontend begins executing. After implementing the initial UI, it realizes REST polling will create a poor user experience for real-time updates. It publishes on `discuss.<task_id>`: "REST polling every 2 seconds is going to be janky. We should use WebSocket for the notification feed."

3. Backend is mid-execution, writing REST notification endpoints. At the next interrupt check (after its current tool calls complete), it sees the frontend's message in the interrupts block.

4. Backend's LLM evaluates: the REST endpoints it has written are for fetching notification history — those are still valid. But the "new notification" push mechanism needs to change from REST to WebSocket. Decision: **ADJUST**. The backend modifies its remaining plan to implement a WebSocket handler alongside the REST history endpoint.

5. Webserver also receives the interrupt. It evaluates: WebSocket requires an upgrade-capable proxy configuration. Decision: **ADJUST**. It modifies its nginx config to support WebSocket upgrade headers on the `/ws/notifications` path.

6. All three agents complete execution with a coherent result — REST for history, WebSocket for real-time — despite the mid-execution design change.

This scenario illustrates the interrupt mechanism's primary value: agents can adapt to discoveries made by other agents during execution, without requiring a full stop and restart.

### 7.3 Scenario: Pause and Resume

**Task submitted:** "Implement OAuth2 login with Google and GitHub providers."

1. Agents deliberate. Backend CLAIMs the OAuth2 flow implementation, frontend CLAIMs the login UI, webserver CLAIMs the callback route configuration.

2. Backend begins executing. It implements the Google OAuth2 flow. At the next interrupt check, it finds a message from the frontend: "The design team wants to support 'Sign in with Apple' too. Apple's OAuth is significantly different — they require server-side token validation and have a unique key rotation mechanism."

3. Backend's LLM evaluates: Apple's OAuth2 implementation has security implications (key rotation, server-side validation) that it cannot resolve alone. It needs to discuss the approach with the swarm. Decision: **PAUSE**.

4. Backend publishes on `discuss.<task_id>`: "PAUSED: Apple OAuth requires server-side JWT validation with rotating keys. This changes the token verification architecture. Do we add a dedicated key rotation service, or handle it in the main backend? Also, this adds a dependency on Apple's JWKS endpoint — reliability concern."

5. Frontend responds: "From the UI side, Apple requires a specific button style and a different redirect flow. I'll need the callback URL format before I can finalize the login page."

6. Webserver responds: "I can add a caching proxy for Apple's JWKS endpoint to handle reliability. Suggest a 1-hour cache with background refresh."

7. Backend evaluates the discussion. The webserver's caching proxy solution addresses the reliability concern. Backend can implement the JWT validation knowing the JWKS endpoint is reliably cached. Decision: Resume execution.

8. Backend transitions from PAUSED back to EXECUTING, continuing from where it stopped (Google flow complete, now implementing Apple flow with JWT validation).

This scenario illustrates the PAUSE mechanism: the agent encounters a problem it cannot solve in isolation, pauses to discuss, and resumes once the swarm provides a solution. The key property is that execution state is preserved — the backend does not restart its Google OAuth implementation.

### 7.4 Scenario: Abort and Re-engagement

**Task submitted:** "Set up CI/CD pipeline with automated testing."

1. Agents deliberate. Backend CLAIMs the test suite and CI configuration, frontend CLAIMs frontend test setup, webserver CLAIMs the deployment pipeline.

2. Backend begins executing, setting up a GitHub Actions workflow with unit tests.

3. An interrupt arrives from the swarm operator (via `discuss.<task_id>`): "We're migrating from GitHub to GitLab next week. Don't invest in GitHub-specific CI."

4. Backend's LLM evaluates: the entire GitHub Actions workflow it is building will be obsolete. Decision: **ABORT**. Backend publishes: "ABORT: GitHub Actions workflow no longer viable given GitLab migration. Partial work in .github/workflows/ may be useful as reference for GitLab CI syntax translation."

5. Backend transitions to MONITORING. It continues watching `discuss.<task_id>`.

6. Frontend finishes its test setup (framework-agnostic, not affected by the CI migration). Webserver pauses its deployment pipeline work.

7. Later, the swarm operator posts: "GitLab migration complete. Here are the repo URLs and CI runner details."

8. Backend sees this message on `discuss.<task_id>`. The abort reason (GitHub → GitLab migration) is resolved. Backend re-enters DELIBERATING, reads the discussion history, and CLAIMs: "I'll set up the GitLab CI pipeline using the completed test suite. Can reference the GitHub Actions structure for pipeline design."

9. Backend executes, now building for GitLab CI instead.

This scenario illustrates abort as a reversible disengagement. The agent stops work that is no longer viable, communicates why, and remains available to re-engage when circumstances change.

### 7.5 Scenario: Dependency Chain

**Task submitted:** "Build an API with tests and documentation."

1. All three agents deliberate. This is a case where dependencies are inherent:
   - **Backend**: "I'll build the API. Others will need my code before they can test or document."
   - **Frontend**: "I have nothing to contribute to an API-only task. Withdrawing." Frontend returns to MONITORING.
   - **Webserver**: "I'll write the API documentation once the endpoints are defined. NEED_INFO: waiting for backend to define the API."

2. Backend CLAIMs: "API implementation — routes, handlers, models." It begins executing.

3. Webserver remains in DELIBERATING with NEED_INFO. It is not blocked — it is simply waiting for the information it needs. It continues updating its swarm context as backend heartbeats arrive showing EXECUTING status and progress.

4. Backend completes and publishes its result on `done.*`. The result includes the API specification.

5. Webserver sees the `done.*` message. Its dependency is satisfied. It CLAIMs: "API documentation based on the implemented endpoints." It begins executing.

6. If a tester agent existed, it would follow the same pattern — waiting in deliberation until the API is built, then CLAIMing test development.

This scenario illustrates that NEED_INFO is not a failure state — it is a natural expression of dependency. Agents wait intelligently, maintaining awareness of progress, and engage when their prerequisites are met.

---

## 8. Configuration

The collaboration model introduces a new `collaboration` section in the swarm manifest (`swarm.yaml`). All parameters have sensible defaults; the section is entirely optional.

```yaml
collaboration:
  straggler_timeout: 30s
  max_deliberation_rounds: 20
  interrupt_check: true
  context_summary_threshold: 10
```

**straggler_timeout** (default: 30s). Maximum time to wait for an agent that has not participated in deliberation. After this timeout, other agents proceed without the straggler. The straggler may still join later if it comes online.

**max_deliberation_rounds** (default: 20). Maximum number of discuss message exchanges per task before the system forces agents to decide. This prevents infinite deliberation loops where agents keep asking questions without converging. When the limit is reached, each agent must either CLAIM or withdraw on its next turn.

**interrupt_check** (default: true). Whether executing agents check the interrupt buffer between LLM turns. When false, agents execute in isolation (current behavior). When true, agents receive and evaluate interrupts from the discuss channel.

**context_summary_threshold** (default: 10). Number of discussion messages per task before older messages are summarized. Lower values produce more aggressive summarization (fewer tokens, less detail). Higher values preserve more raw messages (more tokens, more detail).

---

## 9. Relationship to Existing Architecture

The collaboration model builds on top of the existing swarm infrastructure rather than replacing it. This section maps the new concepts to existing code structures.

### 9.1 NATS Subjects

No new NATS subjects are required. The existing subject hierarchy — `work.*`, `discuss.*`, `done.*`, `heartbeat.*` — provides all the communication channels the collaboration model needs.

- Deliberation occurs on `discuss.<task_id>` (existing)
- CLAIMs are published on `discuss.<task_id>` (existing, new message type)
- Interrupt sources are `discuss.<task_id>` messages (existing)
- Swarm context is built from `heartbeat.*` and `done.*` (existing)

### 9.2 Agentfile

No changes to Agentfile syntax. The agent's persona, goals, and workflow steps remain as defined. The collaboration model changes how the executor *engages with* the Agentfile — goals and steps become a starting strategy that can be adapted based on interrupts — but the Agentfile format itself is untouched.

### 9.3 Executor

The executor is the primary code change. The current execution loop:

```
loop:
  send messages to LLM
  receive response (text + tool calls)
  execute tool calls
  collect results
  if stop_reason == "end_turn": break
  add results to messages
```

Becomes:

```
loop:
  send messages to LLM
  receive response (text + tool calls)
  execute tool calls
  collect results
  drain interrupt buffer
  if interrupts present:
    format interrupts block
    add to messages
  if stop_reason == "end_turn" and no interrupts: break
  add results to messages
```

The change is minimal in code terms — an additional check between tool result collection and the next LLM invocation. The complexity lies not in the mechanism but in the prompt engineering: the interrupts block, the guidance framework, and the context management that keeps the conversation coherent.

### 9.4 Triage

The current triage mechanism (similarity scoring + LLM decision) becomes the entry gate to deliberation rather than the entry gate to execution. Its role narrows: instead of deciding EXECUTE/COMMENT/SKIP, it decides only whether the task is relevant enough to enter deliberation. The richer decisions — what to do, what to claim, how to participate — are made during deliberation itself.

---

## 10. Open Questions

Several design questions remain unresolved. They are captured here for future consideration.

**Sub-agent forking for sidequests.** When an interrupt reveals orthogonal work — work that is relevant but not part of the agent's current task — the agent currently has two options: ignore it (CONTINUE) or handle it by adjusting its plan (ADJUST). A third option — forking a sub-agent to handle the sidequest while the main agent continues — is architecturally appealing but adds significant complexity. The implications for state management, resource consumption, and swarm coordination need careful analysis before this is viable.

**Conflict resolution on duplicate CLAIMs.** If two agents CLAIM overlapping work, the current design has no explicit resolution mechanism. In practice, the discuss channel should surface this during deliberation — agents can see each other's CLAIMs and adjust. But if CLAIMs happen simultaneously, overlap may go undetected until results are published. A future extension might introduce CLAIM acknowledgment or NATS-based deduplication.

**Interrupt batching vs. immediacy.** The current design processes all buffered interrupts at each check point. An alternative is to batch interrupts over a minimum interval (e.g., collect for 5 seconds before injecting) to reduce the frequency of LLM evaluations. The tradeoff is latency vs. cost, and the right balance likely depends on the task.

**Swarm context divergence detection.** While Section 5.1 argues that personal interpretation is acceptable, there may be cases where significant divergence causes problems that surface too late. A lightweight divergence detection mechanism — perhaps agents periodically publishing their key assumptions on `discuss.*` — could catch misalignment early. The cost-benefit of this is unclear.

**Persistent swarm context across restarts.** The current design makes swarm context fully ephemeral. For long-running swarms (hours or days), losing all context on restart is expensive — agents must rebuild understanding from scratch. JetStream message replay partially addresses this, but a dedicated checkpoint mechanism for swarm context may be warranted for long-lived swarms.
