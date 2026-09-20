---
project: "<owner/repository>"
repository: "<https://github.com/owner/repository>"
revision: "<full commit SHA>"
researched_at: "<YYYY-MM-DD>"
primary_language: "<language or unknown>"
implementation_form: "<native-agent|adapter|gateway-orchestrator|unknown>"
process_model: "<single-process|process-per-session|shared-daemon|hybrid|unknown>"
session_owner: "<acp-layer|agent-runtime|wrapped-agent|shared|none|unknown>"
durability: "<none|memory-only|local-files|embedded-database|external-service|wrapped-agent|mixed|unknown>"
cross_session_concurrency: "<concurrent|serialized|bounded|unknown>"
same_session_concurrency: "<serialized|concurrent|rejected|cancel-previous|unknown>"
event_delivery: "<direct|channel|polling|subprocess-stream|mixed|unknown>"
resume_strategy: "<native|reconstruct|delegate|unsupported|unknown>"
overall_confidence: "<high|medium|low>"
---

# <Project> ACP architecture

## Executive summary

<!-- Five to eight bullets. Cover the ACP boundary, session ownership,
durability, both concurrency scopes, live event delivery, replay/resume, and
the most important uncertainty. -->

- **ACP boundary:** <summary with evidence>
- **Session model:** <summary with evidence>
- **Concurrency:** <summary with evidence>
- **Durability and replay:** <summary with evidence>
- **Event flow:** <summary with evidence>
- **Notable uncertainty:** <summary or "None">

## Classification

The values in this table must match the front matter. Use only the controlled
values listed in the front matter.

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `<value>` | <explanation> | <citations> |
| Process model | `<value>` | <explanation> | <citations> |
| Session owner | `<value>` | <explanation> | <citations> |
| Durability | `<value>` | <explanation> | <citations> |
| Cross-session concurrency | `<value>` | <explanation> | <citations> |
| Same-session concurrency | `<value>` | <explanation> | <citations> |
| Event delivery | `<value>` | <explanation> | <citations> |
| Resume strategy | `<value>` | <explanation> | <citations> |

## System architecture

<!-- Replace this block with a compact ASCII diagram. Show process boundaries,
the ACP client, protocol transport, ACP handlers, session/runtime owner, agent
loop or wrapped process, and durable storage. Label the direction of requests,
live updates, and replay. Do not add speculative components. -->

```text
<architecture diagram>
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| <component> | <responsibility> | <process/session/prompt/request> | <state> | <citations> |

### ACP surface

<!-- Identify transport, SDK or protocol implementation, initialization,
capabilities, and session/prompt/cancel handlers. Say whether the ACP layer is
native to the agent or an adapter boundary. -->

<analysis>

### Runtime and process boundaries

<!-- Explain tasks, threads, subprocesses, daemons, and channels only where they
matter to session ownership, concurrency, event delivery, or cleanup. -->

<analysis>

## Session model

### Identity and ownership

<!-- Define "session" in this codebase. Trace an ACP session ID to any runtime
object, wrapped-agent ID, storage key, workspace, and model conversation. Note
one-to-one, one-to-many, or lossy mappings. -->

<analysis>

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | <behavior> | <effect> | <behavior> | <citations> |
| Load/resume | <behavior> | <effect> | <behavior> | <citations> |
| Prompt | <behavior> | <effect> | <behavior> | <citations> |
| Cancel | <behavior> | <effect> | <behavior> | <citations> |
| Close/delete | <behavior or Not found> | <effect> | <behavior> | <citations> |

### Durable representation

<!-- Describe storage technology, schema or record types, ordering keys,
transaction/flush boundaries, and what is deliberately not persisted. Explain
whether stored history is authoritative, a cache, or owned elsewhere. -->

<analysis>

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `<concurrent|serialized|bounded|unknown>` | <locks/tasks/limits> | <citations> |
| Two prompts in the same session | `<serialized|concurrent|rejected|cancel-previous|unknown>` | <locks/tasks/guards> | <citations> |
| Load/resume during an active prompt | <result or unknown> | <mechanism> | <citations> |
| Delete/close during an active prompt | <result or unknown> | <mechanism> | <citations> |
| Cancellation isolation | <per request/session/process/global/unknown> | <mechanism> | <citations> |

<!-- Explain whether shared registries, databases, queues, provider limits, or
subprocess I/O introduce additional coupling. Identify the precise critical
section and when it is released. Do not equate spawned tasks with correctness. -->

<analysis>

## Event and data flow

### New prompt: ACP client to live response

<!-- Number the actual end-to-end path. Include validation, session lookup,
history construction, prompt persistence, agent invocation, event translation,
ACP notification delivery, final response, and cleanup when present. -->

1. <step with evidence>
2. <step with evidence>

### Durable history to ACP client

<!-- Trace session load/replay separately from a live prompt. Explain selection,
ordering, conversion to ACP updates, delivery, and any events that cannot be
reconstructed. If the agent never replays history to the client, say so. -->

1. <step with evidence>
2. <step with evidence>

### Live events to durable history

<!-- Trace user input, assistant text/reasoning, tool calls, tool results,
terminal state, and usage where supported. State whether persistence occurs
before notification, after notification, in batches, or in another process. -->

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | <value> | <value or none> | <value> | <when> | <citations> |
| Assistant text | <value> | <notification> | <value> | <when> | <citations> |
| Reasoning/thought | <value or n/a> | <notification or none> | <value or none> | <when> | <citations> |
| Tool call | <value or n/a> | <notification or none> | <value or none> | <when> | <citations> |
| Tool result | <value or n/a> | <notification or none> | <value or none> | <when> | <citations> |
| Completion/failure | <value> | <response/update> | <value or none> | <when> | <citations> |

### Subsequent-prompt reconstruction

<!-- Explain exactly how durable history becomes model/runtime input on a later
prompt. Note filtering, compaction, lossy conversion, or delegation. -->

<analysis>

### Ordering, cancellation, failure, and backpressure

<!-- Identify guarantees and gaps. What happens to partially streamed text,
in-flight tools, durable state, the ACP response, and future resumability when
delivery or execution stops? Are queues bounded? Can a slow client stall the
agent or other sessions? -->

<analysis>

## Capability matrix

Use only `yes`, `partial`, `no`, `unknown`, or `n/a` in the Support column.

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `<value>` | <notes> | <citations> |
| Concurrent work across sessions | `<value>` | <notes> | <citations> |
| Same-session prompt exclusion | `<value>` | <notes> | <citations> |
| Durable sessions | `<value>` | <notes> | <citations> |
| Session list | `<value>` | <notes> | <citations> |
| Session load/resume | `<value>` | <notes> | <citations> |
| History replay to ACP client | `<value>` | <notes> | <citations> |
| Prior history reused by model | `<value>` | <notes> | <citations> |
| Prompt cancellation | `<value>` | <notes> | <citations> |
| Tool-call progress updates | `<value>` | <notes> | <citations> |
| Partial-output persistence | `<value>` | <notes> | <citations> |
| Recovery after process restart | `<value>` | <notes> | <citations> |

## Design assessment

### Strengths

<!-- Two to five architecture-specific observations, not general praise. -->

- <observation with evidence>

### Tradeoffs and limitations

<!-- Two to five concrete consequences of the observed design. Do not call an
unverified hypothetical a bug. -->

- <observation with evidence>

### Ideas relevant to Ox

<!-- Briefly identify patterns worth considering or avoiding in Ox. Separate
facts about this project from the researcher's judgment. Do not prescribe a
change without naming the tradeoff. -->

- <lesson and tradeoff>

## Unknowns and conflicts

<!-- List unanswered questions, missing implementation, unreachable code paths,
and disagreements among code, tests, and docs. Include the searches or files
checked so "Not found" is reproducible. Write "None" if there are none. -->

- <unknown or conflict>

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | <`path:line` (`symbol`)> | <reason> |
| Session management | <locations> | <reason> |
| Concurrency | <locations> | <reason> |
| Persistence | <locations> | <reason> |
| Event translation | <locations> | <reason> |
| Replay/reconstruction | <locations> | <reason> |
| Cancellation/errors | <locations> | <reason> |

## Research notes

- **Revision inspected:** `<full commit SHA>`
- **Primary evidence:** <production files and tests actually inspected>
- **Relevant docs:** <docs used, or None>
- **Commands/tests run:** <read-only inspection or focused tests, or None>
- **Report confidence:** `<high|medium|low>` — <one-sentence rationale>
