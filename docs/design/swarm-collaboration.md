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

![Agent State Machine](diagrams/state-machine.png)

*Figure 1: The three-state agent lifecycle. MONITORING is the stable resting state. DELIBERATING is transient — triggered by each incoming message, producing a single decision, then returning to MONITORING. EXECUTING runs the agent loop with interrupt awareness. The dashed abandon path routes through DELIBERATING (to publish an explanation) before returning to MONITORING.*

At any moment, a swarm agent is in exactly one of three states. These states govern how the agent processes incoming messages, whether it participates in discussion, and whether it is actively executing work.

### 2.1 States

**MONITORING.** The agent is idle, listening to all NATS subjects it is subscribed to — `work.*`, `discuss.*`, `done.*`, `heartbeat.*`. It performs no outbound communication and takes no action. MONITORING is the agent's resting state — the state it occupies when it has no task to deliberate on and no work to execute.

The only outbound activity during MONITORING is heartbeat emission, which is a timer-driven infrastructure concern independent of the agent's logical state. The agent does not update swarm context as a deliberate action; rather, the NATS message handler passively maintains the in-memory swarm context data structure as messages arrive on subscribed subjects. This is a side effect of message receipt, not an activity of the MONITORING state itself.

An agent enters MONITORING when it first joins the swarm, when it completes a task, or when it abandons execution. MONITORING is the stable state — the state the agent always returns to between engagements.

**DELIBERATING.** The agent is evaluating whether and how to respond to an incoming message. Deliberation is **transient** — it is triggered by the arrival of a message on `work.*` or `discuss.*`, and it concludes with one of three outcomes:

1. The agent publishes a response on `discuss.<task_id>` and returns to MONITORING.
2. The agent determines it has nothing to contribute and returns to MONITORING silently.
3. The agent determines it has sufficient information to begin work and transitions to EXECUTING.

Critically, deliberation is not a sustained state. The agent does not "sit in" deliberation waiting for more information. It evaluates the current message in the context of everything it knows — the task description, prior discussion, swarm context — makes a single decision, acts on it, and returns to MONITORING. Multi-round discussion emerges naturally from repeated cycles of MONITORING → DELIBERATING → MONITORING, where each new `discuss.*` message triggers a fresh deliberation. The agent is reactive, not blocking.

This design eliminates the question of "how long should deliberation last?" There is no deliberation window. Each message is a stimulus, each response is a reaction, and the conversation unfolds through successive cycles. An agent that needs more information publishes its question, returns to MONITORING, and re-enters DELIBERATING when an answer arrives.

**EXECUTING.** The agent is actively running its workflow — invoking tools, generating code, processing data. During execution, the agent remains connected to `discuss.<task_id>` through an interrupt buffer (Section 4). It is focused on its claimed work but receives new information at natural breakpoints in its execution loop.

The execution loop is iterative. At the end of each iteration — after all tool calls have completed and results have been collected — the agent checks its interrupt buffer for new messages. If messages are present, they are folded into the context for the next iteration. At the beginning of that next iteration, the LLM evaluates the combined state: its own work-in-progress, the tool results from the previous iteration, and any interrupt messages. Based on this evaluation, it decides whether to continue executing, or to abandon execution and transition to DELIBERATING (to explain why) or directly to MONITORING.

This design has an important degenerate case: when no interrupts arrive, the buffer is always empty, the interrupts block is never included, and the execution loop behaves identically to the current non-collaborative agent loop. The collaboration machinery has zero overhead when there is nothing to collaborate about.

### 2.2 Transitions

The state machine has five transitions:

**MONITORING → DELIBERATING.** Triggered when a message arrives on `work.*` or `discuss.*` that passes the agent's relevance threshold. The existing similarity-based triage mechanism determines relevance — if the message's semantic similarity to the agent's capability exceeds the configured threshold, the agent enters deliberation.

**DELIBERATING → MONITORING.** The default exit from deliberation. The agent has either published a response on `discuss.<task_id>` or determined it has nothing to contribute. In either case, it returns to MONITORING and awaits the next message.

**DELIBERATING → EXECUTING.** Triggered when the agent determines, during deliberation, that it has sufficient information to begin work. The agent publishes a CLAIM on `discuss.<task_id>` specifying what portion of the task it is taking responsibility for, initializes its interrupt buffer, and begins workflow execution.

**EXECUTING → MONITORING.** Triggered on successful completion. The agent publishes its result on `done.<capability>.<task_id>` and returns to MONITORING.

**EXECUTING → DELIBERATING.** Triggered when the agent, during execution, determines it must communicate something to the swarm before it can continue — or that its work is no longer viable. The agent transitions to DELIBERATING, where it publishes its message (an explanation of the problem, a question, or an abort reason) on `discuss.<task_id>` or on `work.<name>.*` if the message targets a specific agent. After publishing, it follows the normal DELIBERATING exit: back to MONITORING. The agent does not resume its prior execution. If the situation resolves and the agent's capabilities are still relevant, a subsequent message will trigger a new MONITORING → DELIBERATING → EXECUTING cycle — a fresh execution, not a continuation.

This transition deserves careful attention. When an agent abandons execution, it does not silently disappear. It communicates the reason for abandonment so that other agents can update their understanding of the problem. The abandon message might reveal a constraint that other agents have not encountered ("Apple's OAuth requires server-side JWT validation with rotating keys — this changes the token verification architecture"), flag a dependency ("I can't proceed until the database schema is finalized"), or declare that the work is no longer viable ("the migration to GitLab makes this GitHub Actions workflow obsolete"). Other agents, upon receiving this message, will enter their own DELIBERATING state and decide how to respond.

Partial work produced before abandonment is not cleaned up. Files written, code generated, configurations created — all remain in place. Other agents may find partial work useful as a starting point if the task is later re-engaged.

### 2.3 State Visibility

Each agent's current state is communicated to the swarm through the existing heartbeat mechanism. The heartbeat metadata already includes agent name, capability, and session information. The state field is added to this metadata, allowing all agents (and the UI) to observe the swarm's collective state at a glance.

---

## 3. Deliberation

![Reactive Deliberation Cycle](diagrams/reactive-deliberation.png)

*Figure 2: A single deliberation cycle. Each incoming message triggers one evaluation, producing one of three outcomes: respond with NEED_INFO (then return to MONITORING), stay silent (return to MONITORING), or CLAIM (transition to EXECUTING). Multi-round discussion emerges from repeated cycles.*

Deliberation is the mechanism by which agents develop shared understanding of a task. It replaces the current single-shot triage decision — which produces an irrevocable EXECUTE, COMMENT, or SKIP — with a reactive, message-driven process that allows agents to ask questions, propose approaches, negotiate responsibilities, and commit to work.

### 3.1 The Reactive Model

In the current system, triage is a single decision point: a task arrives, the agent evaluates it once, and the outcome is final. The collaboration model replaces this with a reactive loop:

1. A message arrives (task or discuss).
2. The agent enters DELIBERATING.
3. The agent evaluates the message in context (task description, prior discussion, swarm state).
4. The agent produces one of: a discuss response, silence, or a CLAIM.
5. The agent returns to MONITORING (or transitions to EXECUTING on CLAIM).

Each message is an independent trigger. There is no persistent deliberation session, no accumulated state within the DELIBERATING phase, and no timer governing how long deliberation lasts. The agent's understanding of the task accumulates in the swarm context and in the discuss message history — both of which persist across deliberation cycles in the background data structures — not in the DELIBERATING state itself.

This reactive model has a natural analogy in human collaboration: a developer receives a Slack message, reads the thread, posts a reply, and goes back to whatever they were doing. They do not enter a "deliberation mode" and wait. If someone replies to their message, they receive a new notification, read it, respond, and return. The conversation unfolds through a series of independent reactions, not through a sustained deliberation session.

### 3.2 Signals

During deliberation, each agent's contribution on `discuss.<task_id>` includes a structured signal alongside its natural language content. There are exactly two signals:

**NEED_INFO.** The agent has questions or is waiting for information before it can commit. This signal tells other agents that at least one participant still needs clarity. A NEED_INFO message typically includes the specific question or dependency: "I need to know the API contract before I can implement the handlers" or "Waiting for the coder to finish before I can write tests."

After publishing a NEED_INFO message, the agent returns to MONITORING. When the answer arrives — as a new `discuss.*` message — the agent re-enters DELIBERATING and evaluates whether the answer is sufficient to CLAIM.

**CLAIM.** The agent has sufficient understanding and is committing to a specific portion of the work. The CLAIM message specifies what the agent is taking responsibility for: "CLAIM: I'm handling the REST API routes, request validation, and database queries." This specificity is important — it tells other agents what is covered and, by implication, what remains unclaimed.

A CLAIM triggers the transition to EXECUTING. It is the only signal that does not return to MONITORING.

### 3.3 Convergence

Deliberation converges when all relevant agents have either CLAIMed their portion or withdrawn to MONITORING. There is no explicit "deliberation complete" signal — convergence is emergent. Each agent independently decides, upon each incoming message, whether it has enough information to CLAIM or enough evidence to stay silent.

A practical consideration: some agents may depend on others' work. A tester cannot CLAIM until a coder has CLAIMed (and ideally completed). This is natural and expected. The tester publishes NEED_INFO ("waiting for code to test"), returns to MONITORING, and re-enters DELIBERATING when the `done.*` message from the coder arrives. It does not block other agents from CLAIMing and executing. Dependencies resolve naturally through the message-driven reactive loop.

### 3.4 Straggler Handling

If an agent fails to participate in deliberation within the configured straggler timeout (default: 30 seconds), other agents proceed without it. The non-responsive agent may still join later — if it enters deliberation after others have already CLAIMed, it can read the discussion history and decide whether there is unclaimed work for it.

The straggler timeout is a swarm-level configuration parameter, not a per-agent setting. It represents the swarm operator's tolerance for waiting.

### 3.5 Deliberation Context

Each deliberation is a single LLM call with the following context:

- The triggering message (from `work.*` or `discuss.*`)
- The agent's capability description and persona (from its Agentfile)
- All prior discuss messages for this task (or a summary if they exceed the context summary threshold — see Section 6)
- The agent's current swarm context snapshot (Section 5)

The LLM produces both a natural language contribution (the discussion content) and a structured signal (NEED_INFO or CLAIM). If the signal is CLAIM, the agent transitions to EXECUTING. Otherwise, the response is published on `discuss.<task_id>` (or discarded if the agent has nothing to say), and the agent returns to MONITORING.

### 3.6 Deliberation Limits

While individual deliberation cycles are transient, the accumulated discussion for a task can grow unbounded if agents keep exchanging NEED_INFO messages without converging. The `max_deliberation_rounds` configuration parameter (default: 20) caps the total number of discuss messages per task across all agents. When the limit is reached, each agent must either CLAIM or withdraw on its next deliberation cycle.

This is a safety valve, not a design goal. Well-functioning agents should converge long before the limit. If agents routinely hit the limit, it suggests the task is poorly scoped or agent capabilities are poorly matched — problems that configuration cannot solve.

---

## 4. Execution with Interrupts

![Execution Loop with Interrupts](diagrams/execution-loop.png)

*Figure 3: The execution loop. After each LLM turn, the agent checks for abandonment, executes tools, drains the interrupt buffer, and assembles the next turn. If interrupts are present, they are formatted into an XML block and included in the next LLM invocation. The loop exits on task completion (with no pending interrupts) or on abandonment.*

Once an agent CLAIMs a task and begins execution, it does not become deaf to the swarm. An interrupt mechanism allows new information to reach the agent at natural breakpoints in its workflow, enabling it to adapt its approach without losing progress.

### 4.1 The Interrupt Buffer

When an agent transitions to EXECUTING, it initializes an **interrupt buffer** — a thread-safe queue that collects messages arriving on `discuss.<task_id>`. The NATS subscriber writes to this buffer; the executor reads from it.

The buffer is a simple FIFO queue. Messages are not filtered, prioritized, or deduplicated at the buffer level — that is the LLM's job when it processes them. The buffer's only responsibility is to safely bridge the asynchronous NATS message flow with the synchronous executor loop.

### 4.2 Injection Point

The interrupt buffer is checked at exactly one point in the execution cycle: **between LLM turns, after all tool results have been collected and before the next LLM invocation.**

The execution cycle of a single iteration is:

1. LLM receives context (messages, tool results, interrupts if any)
2. LLM produces a response (text, tool calls, or both)
3. If tool calls are present, all requested tools execute (possibly in parallel)
4. Tool results are collected
5. **Interrupt check: drain the buffer**
6. Tool results (and any interrupts) are assembled into the next LLM turn
7. Return to step 1

This injection point is chosen for three reasons. First, it avoids the complexity of interrupting parallel tool execution — all tools complete before interrupts are considered. Second, it provides a natural seam where new context can be introduced without corrupting in-progress work. Third, it guarantees that the LLM sees interrupts before making its next decision, including the decision that the task is complete.

The third point deserves emphasis. If the LLM's previous turn was intended to be the final one — producing the task result — the interrupt check still occurs before that result is published. The LLM receives both its tool results and the new interrupt messages, and can decide whether the result is still valid or whether the interrupts necessitate further work. Completion is never premature.

### 4.3 The Interrupts Block

When the interrupt buffer contains messages, they are formatted into an XML block and included in the next LLM turn. The block has three components:

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
    them against your current work and decide how to proceed.
    You may continue working if the messages are irrelevant,
    adjust your approach if they affect your remaining work,
    or abandon execution if your work is no longer viable.

    If you abandon, you MUST explain the reason in your response.
    This explanation will be published to the swarm so other agents
    understand why work was stopped and can adapt accordingly.
  </guidance>
</interrupts>
```

The **context** element grounds the LLM in what it was doing. Without this, the LLM must reconstruct its execution state from the conversation history — feasible but error-prone and token-expensive. The context element makes the agent's current position explicit.

The **messages** element contains the raw discuss messages, preserving attribution and timestamps. These are not summarized — the LLM needs the original wording to assess relevance and implications.

The **guidance** element frames the decision. Rather than prescribing a fixed set of categorical decisions (CONTINUE, ADJUST, ABORT), the guidance instructs the LLM to reason naturally about how to proceed. The LLM may continue working unchanged, modify its plan, ask a question on discuss (which it can do via tool calls during the same iteration), or decide to abandon. The only hard requirement is that abandonment must include an explanation — this is enforced because other agents depend on understanding why work stopped.

### 4.4 Execution Decisions After Interrupts

When the LLM processes an interrupts block at the start of an iteration, it reasons about the interrupt messages in the context of its current work and decides how to proceed. Three broad outcomes are possible:

**Continue or adjust.** The LLM determines that the interrupt messages are either irrelevant to its current work or relevant but manageable. In the former case, it proceeds with its plan unchanged. In the latter, it modifies its approach for remaining steps — for example, switching from REST to WebSocket serialization after learning that the frontend changed the transport protocol. In both cases, execution continues within the same loop. The LLM may also publish a question or clarification on `discuss.<task_id>` during the same iteration (via tool calls), without interrupting its own execution.

**Abandon.** The LLM determines that its work is no longer viable or that it cannot proceed without resolving a fundamental question. It produces an explanation of the problem — surfacing constraints, flagging architectural concerns, or declaring that the approach is obsolete. The executor detects the abandonment signal, transitions the agent to DELIBERATING (where the explanation is published to `discuss.<task_id>` or `work.<name>.*`), and then to MONITORING. The agent does not resume its prior execution. If the situation resolves and a new message triggers deliberation, the agent starts a fresh execution cycle.

The distinction between adjustment and abandonment is not categorical — it is a judgment the LLM makes based on the severity of the interrupt's implications. An interrupt that changes a detail (date format, endpoint path) warrants adjustment. An interrupt that invalidates the premise (technology migration, complete redesign) warrants abandonment. The LLM's reasoning, visible in its response, makes this judgment transparent and auditable.

### 4.5 The Degenerate Case

When no interrupts arrive during execution — the common case in quiet swarms or solo agent runs — the interrupt buffer is always empty. The check at step 5 of the execution cycle finds nothing, no interrupts block is assembled, and the next iteration proceeds with tool results alone. The execution loop behaves identically to the current non-collaborative agent loop.

This is not merely an optimization; it is a design guarantee. The collaboration machinery is purely additive. An agent running outside a swarm (`agent run` with no NATS connection) never initializes an interrupt buffer, never starts a swarm context goroutine, and never enters deliberation. The buffer drain check short-circuits on a nil buffer in nanoseconds. The executor produces the exact same behavior as the pre-collaboration implementation. Solo agents pay zero cost for the existence of collaboration code.

### 4.6 Interrupt Processing Cost

Each interrupt check that finds messages adds context to the next LLM invocation — the LLM must process the interrupts alongside its regular tool results. This is not a separate LLM call; it is additional content in the same call that would have happened anyway. The marginal cost is the token count of the interrupts block.

Interrupt checks that find an empty buffer incur zero cost — the check is a non-blocking channel read that completes in nanoseconds.

The swarm operator can disable interrupt checking entirely by setting `interrupt_check: false` in the swarm manifest, reverting to the current deaf-execution model. This is a tradeoff: lower token cost at the expense of adaptability.

---

## 5. Swarm Context

Each agent maintains a personal, in-memory representation of the swarm's state. This representation — the **swarm context** — provides the agent with awareness of what other agents are doing, what has been decided, and what work has been completed.

### 5.1 Nature of Swarm Context

Swarm context is **personal, ephemeral, and passively maintained.**

**Personal.** Each agent maintains its own swarm context independently. There is no shared consensus state, no central authority, and no synchronization protocol. Agent A's understanding of the swarm may differ slightly from Agent B's — perhaps Agent A processed a heartbeat message that Agent B has not yet received.

This divergence is acceptable and even desirable. In human teams, each member holds a slightly different mental model of the project's state. These models are approximately aligned through communication (standups, code reviews, conversations) and precisely aligned when it matters (pull request discussions, design reviews). The same applies here: the discuss channel is where divergent understandings converge.

The alternative — a shared consensus state — would require distributed consensus protocols, write coordination, conflict resolution, and an authority model. This is a distributed systems problem layered on top of an AI coordination problem, and the complexity is not justified. Personal interpretation with communication-based alignment is simpler, more resilient, and sufficient.

**Ephemeral.** Swarm context exists only in memory for the duration of the agent's participation in the swarm. It is not persisted to disk. When the swarm shuts down, context is lost. When the swarm restarts, context is rebuilt from the NATS message stream (if JetStream persistence is enabled) or starts empty.

This is appropriate because swarm context represents *current* state, not historical knowledge. "Agent X is currently executing task Y" is valuable now but meaningless after the swarm completes. Long-term knowledge — lessons learned, architectural decisions — belongs in the agent's persistent memory (BM25/semantic graph), not in ephemeral swarm context.

**Passively maintained.** Swarm context is updated as a side effect of NATS message receipt. The message handler — a background goroutine that exists regardless of agent state — writes to the swarm context data structure whenever a relevant message arrives. The agent does not actively "update" its swarm context; the data structure stays current because the message handler keeps it current. This is analogous to a human's peripheral awareness: you do not actively decide to "update your understanding of the room" — you simply hear conversations happening around you, and your mental model updates automatically.

### 5.2 Contents

Swarm context contains three categories of information, each maintained by a different NATS subscription:

**Agent states** (from `heartbeat.*`). A map of agent name to current status: state (MONITORING, DELIBERATING, EXECUTING), capability, current task (if any), and last heartbeat timestamp. This is always current — each heartbeat overwrites the previous entry. The data is compact: one map entry per agent, no accumulation.

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

**During deliberation.** Every deliberation cycle includes the current swarm context snapshot. The agent needs to know what others are doing to decide its own role — who has already CLAIMed, what dependencies exist, what the overall swarm state is. This is essential for informed deliberation.

**During interrupt processing.** When an interrupts block is assembled (Section 4.3), the current swarm context snapshot is available as additional grounding. This helps the LLM evaluate whether an interrupt is relevant: if it knows that the frontend agent has CLAIMed the WebSocket migration, a message about WebSocket from that agent carries more weight than a speculative comment.

Swarm context is **not** injected during normal execution turns (tool calls and result processing without interrupts). When the agent is heads-down writing code, swarm awareness is noise. The interrupt mechanism handles the case where external developments require attention; swarm context is not needed when no interrupts are present.

---

## 6. Context Management

Deliberation, interrupts, and swarm context all contribute to the token budget consumed by each LLM call. Without active management, a long discussion or a task that accumulates many interrupts can exceed the model's context window. This section describes how context is kept bounded.

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

Summarization is lazy: it runs only when the discussion log is needed (for a deliberation cycle or interrupt context) and has grown beyond the threshold since the last summarization. This avoids unnecessary LLM calls for discussions that are no longer active.

### 6.2 Interrupt History

During execution, processed interrupts — those the LLM has already seen and responded to — accumulate in the conversation context. If many interrupts arrive over a long execution, the raw interrupt history can grow large.

Processed interrupts are consolidated using the same summarization approach as discussions. The summary captures what interrupts arrived, what the agent decided, and what adjustments were applied. New, unprocessed interrupts remain verbatim. This ensures the LLM has full context on recent interrupts while maintaining a compressed history of earlier ones.

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

1. Task arrives on `work.*`. All three agents pass the relevance threshold. Each transitions from MONITORING to DELIBERATING.

2. **Backend** (first to respond): "I'll handle the registration API endpoint, user database schema, and email verification token generation. NEED_INFO: What email service are we using?" Backend returns to MONITORING.

3. **Frontend** (triggered by the discuss message): "I'll build the registration form and the email verification landing page. NEED_INFO: What fields does the registration form need?" Frontend returns to MONITORING.

4. **Webserver** (triggered by the discuss messages): "I'll set up the routes: POST /register → backend, GET /verify → backend, static assets → frontend build output. NEED_INFO: Are there any rate limiting requirements?" Webserver returns to MONITORING.

5. **Backend** (triggered by frontend's question): "Registration fields: email, password, display name. No specific rate limiting unless frontend wants it. CLAIM: Registration API, user schema, email token generation." Backend transitions to EXECUTING.

6. **Frontend** (triggered by backend's response): "Got it — three fields plus a submit button. CLAIM: Registration form, verification landing page, client-side validation." Frontend transitions to EXECUTING.

7. **Webserver** (triggered by the discussion): "CLAIM: Route configuration, reverse proxy rules, static asset serving." Webserver transitions to EXECUTING.

8. All three agents execute their claimed portions independently. No interrupts arise because the task was cleanly divided.

This scenario illustrates the simplest case: the discussion unfolds through reactive cycles of MONITORING → DELIBERATING → MONITORING, each triggered by the previous agent's discuss message. No agent "waits" in deliberation — each responds to a message and returns to MONITORING until the next message arrives. Convergence happens naturally.

### 7.2 Scenario: Mid-Execution Course Correction

**Task submitted:** "Add real-time notifications to the dashboard."

1. All three agents deliberate through several reactive cycles. Backend CLAIMs the notification API (REST), frontend CLAIMs the notification UI panel, webserver CLAIMs the routing.

2. Frontend begins executing. After implementing the initial UI, it realizes REST polling will create a poor user experience for real-time updates. It publishes on `discuss.<task_id>`: "REST polling every 2 seconds is going to be janky. We should use WebSocket for the notification feed."

3. Backend is mid-execution, writing REST notification endpoints. At the next interrupt check (after its current tool calls complete), it sees the frontend's message in the interrupts block.

4. Backend's LLM evaluates: the REST endpoints it has written are for fetching notification history — those are still valid. But the "new notification" push mechanism needs to change from REST to WebSocket. The LLM adjusts its remaining plan to implement a WebSocket handler alongside the REST history endpoint. Execution continues.

5. Webserver also receives the interrupt. It evaluates: WebSocket requires an upgrade-capable proxy configuration. It adjusts its nginx config to support WebSocket upgrade headers on the `/ws/notifications` path. Execution continues.

6. All three agents complete execution with a coherent result — REST for history, WebSocket for real-time — despite the mid-execution design change.

This scenario illustrates the interrupt mechanism's primary value: agents adapt to discoveries made by other agents during execution, without requiring a full stop and restart.

### 7.3 Scenario: Execution with Discussion

**Task submitted:** "Implement OAuth2 login with Google and GitHub providers."

1. Agents deliberate through reactive cycles. Backend CLAIMs the OAuth2 flow implementation, frontend CLAIMs the login UI, webserver CLAIMs the callback route configuration.

2. Backend begins executing. It implements the Google OAuth2 flow. At the next interrupt check, it finds a message from the frontend: "The design team wants to support 'Sign in with Apple' too. Apple's OAuth is significantly different — they require server-side token validation and have a unique key rotation mechanism."

3. Backend's LLM evaluates: Apple OAuth adds complexity but doesn't invalidate the current work. The Google flow is already complete. However, the LLM has a question about key management. It publishes on `discuss.<task_id>`: "Adding Apple OAuth. Question: Apple's JWKS endpoint has reliability concerns — should I add a caching layer, or will webserver handle that at the proxy level?" It then continues executing the GitHub OAuth flow, which is independent of the Apple question.

4. Frontend responds on discuss: "From the UI side, Apple requires a specific button style. I'll need the callback URL format."

5. Webserver responds on discuss: "I can add a caching proxy for Apple's JWKS endpoint. 1-hour cache with background refresh."

6. Backend finishes GitHub OAuth. At the next interrupt check, it receives the webserver's and frontend's responses. The question is answered — webserver handles JWKS caching. Backend proceeds to implement Apple OAuth with JWT validation. It publishes the callback URL format on discuss, answering frontend's question.

7. All three agents complete with aligned understanding. The backend did not stop working — it asked its question, continued on independent work, and incorporated the answer when it arrived.

This scenario illustrates a key property of the three-state model: an agent can participate in discussion *during* execution by publishing questions on discuss (via tool calls) and receiving answers as future interrupts. There is no need for a separate "paused" state — the agent keeps working on what it can and adapts when answers arrive.

### 7.4 Scenario: Abandonment and Re-engagement

**Task submitted:** "Set up CI/CD pipeline with automated testing."

1. Agents deliberate. Backend CLAIMs the test suite and CI configuration, frontend CLAIMs frontend test setup, webserver CLAIMs the deployment pipeline.

2. Backend begins executing, setting up a GitHub Actions workflow with unit tests.

3. An interrupt arrives from the swarm operator (via `discuss.<task_id>`): "We're migrating from GitHub to GitLab next week. Don't invest in GitHub-specific CI."

4. Backend's LLM evaluates: the entire GitHub Actions workflow it is building will be obsolete. It decides to abandon execution. The executor transitions the agent to DELIBERATING, where it publishes: "Abandoning GitHub Actions workflow — not viable given GitLab migration. Partial work in .github/workflows/ may be useful as reference for GitLab CI syntax translation." Backend then transitions to MONITORING.

5. Frontend finishes its test setup (framework-agnostic, not affected by the CI migration). Webserver evaluates the same interrupt and also abandons, publishing its own explanation.

6. Later, the swarm operator posts: "GitLab migration complete. Here are the repo URLs and CI runner details."

7. Backend receives this message, enters DELIBERATING, reads the discussion history. It CLAIMs: "I'll set up the GitLab CI pipeline using the completed test suite. Can reference the GitHub Actions structure for pipeline design." Backend transitions to EXECUTING and begins fresh.

8. Backend executes, now building for GitLab CI.

This scenario illustrates abandonment as a reversible disengagement. The agent stops work that is no longer viable, communicates why (so other agents can adapt), returns to MONITORING, and re-engages when circumstances change. The re-engagement is a fresh execution, not a resumption — there is no saved state to restore, which keeps the model simple.

### 7.5 Scenario: Dependency Chain

**Task submitted:** "Build an API with tests and documentation."

1. All three agents enter deliberation via reactive cycles:
   - **Backend**: "I'll build the API. Others will need my code before they can test or document. CLAIM: API implementation — routes, handlers, models." Backend transitions to EXECUTING.
   - **Frontend**: "I have nothing to contribute to an API-only task." Frontend returns to MONITORING silently.
   - **Webserver**: "I'll write the API documentation once the endpoints are defined. NEED_INFO: waiting for backend to define the API." Webserver returns to MONITORING.

2. Backend executes, building the API. Webserver is in MONITORING — idle, but its swarm context updates as backend heartbeats arrive showing EXECUTING status.

3. Backend completes and publishes its result on `done.*`. The result includes the API specification.

4. Webserver receives the `done.*` message, enters DELIBERATING. Its dependency is satisfied. It CLAIMs: "API documentation based on the implemented endpoints." Webserver transitions to EXECUTING.

5. If a tester agent existed, it would follow the same pattern — staying in MONITORING with NEED_INFO until the API is built, then CLAIMing test development when the `done.*` message triggers a new deliberation.

This scenario illustrates that NEED_INFO followed by a return to MONITORING is a natural expression of dependency. The agent does not block or hold resources while waiting. It is genuinely idle in MONITORING, and the arrival of the dependency resolution (via `done.*`) triggers a new reactive cycle that leads to CLAIM and execution.

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

**straggler_timeout** (default: 30s). Maximum time to wait for an agent that has not participated in deliberation for a given task. After this timeout, other agents proceed without the straggler. The straggler may still join later if it comes online.

**max_deliberation_rounds** (default: 20). Maximum number of discuss message exchanges per task across all agents before the system forces a decision. This prevents runaway deliberation where agents keep exchanging NEED_INFO without converging. When the limit is reached, each agent must either CLAIM or withdraw on its next deliberation cycle.

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
- Abandonment explanations are published on `discuss.<task_id>` or `work.<name>.*` (existing)
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
  if abandonment signaled: exit loop → DELIBERATING
  execute tool calls
  collect results
  drain interrupt buffer
  if interrupts present:
    format interrupts block
    add to messages
  if stop_reason == "end_turn" and no interrupts: break
  add results to messages
```

The changes are minimal in code terms: a buffer drain between tool result collection and the next LLM invocation, and an abandonment check on LLM responses. The complexity lies not in the mechanism but in the prompt engineering: the interrupts block, the guidance framework, and the context management that keeps the conversation coherent.

**Solo agent compatibility.** When an agent runs outside a swarm (`agent run` with no NATS connection), the interrupt buffer is never created (nil), the swarm context goroutine is never started, and the deliberation loop is never entered. The buffer drain check short-circuits immediately on a nil buffer — zero overhead. The executor behaves identically to the pre-collaboration execution loop. The state machine collapses to a single state (EXECUTING) with no transitions. This is by design: the collaboration model is additive. It activates only when NATS subscriptions exist, which only happens in swarm mode.

### 9.4 Triage

The current triage mechanism (similarity scoring + LLM decision) becomes the entry gate to deliberation rather than the entry gate to execution. Its role narrows: instead of deciding EXECUTE/COMMENT/SKIP, it decides only whether the incoming message is relevant enough to trigger DELIBERATING. The richer decisions — what to say, what to claim, how to participate — are made during deliberation itself.

---

## 10. Open Questions

Several design questions remain unresolved. They are captured here for future consideration.

**Sub-agent forking for sidequests.** When an interrupt reveals orthogonal work — work that is relevant but not part of the agent's current task — the agent currently has two options: ignore it or adjust its plan to incorporate it. A third option — forking a sub-agent to handle the sidequest while the main agent continues — is architecturally appealing but adds significant complexity. The implications for state management, resource consumption, and swarm coordination need careful analysis before this is viable.

**Conflict resolution on duplicate CLAIMs.** If two agents CLAIM overlapping work, the current design has no explicit resolution mechanism. In practice, the discuss channel should surface this during deliberation — agents can see each other's CLAIMs and adjust. But if CLAIMs happen simultaneously (two agents CLAIM in the same reactive cycle before seeing each other's messages), overlap may go undetected until results are published. A future extension might introduce CLAIM acknowledgment or NATS-based deduplication.

**Interrupt batching vs. immediacy.** The current design processes all buffered interrupts at each check point. An alternative is to batch interrupts over a minimum interval (e.g., collect for 5 seconds before injecting) to reduce the token cost when many messages arrive in rapid succession. The tradeoff is latency vs. cost, and the right balance likely depends on the task.

**Swarm context divergence detection.** While Section 5.1 argues that personal interpretation is acceptable, there may be cases where significant divergence causes problems that surface too late. A lightweight divergence detection mechanism — perhaps agents periodically publishing their key assumptions on `discuss.*` — could catch misalignment early. The cost-benefit of this is unclear.

**Persistent swarm context across restarts.** The current design makes swarm context fully ephemeral. For long-running swarms (hours or days), losing all context on restart is expensive — agents must rebuild understanding from scratch. JetStream message replay partially addresses this, but a dedicated checkpoint mechanism for swarm context may be warranted for long-lived swarms.
