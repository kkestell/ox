# Ox architecture and design proposal

Independent proposal, 2026-09-19, branch `rust` at `e8366bc`, revised the
same day to assume a custom OpenRouter adapter and to incorporate the
fourteen-report ACP review. It pressure tests
`eng/ox-rust-organization-review.md` (the organization review) against
`eng/acp-architecture-review.md` (the ACP review), `eng/llm-abstractions.md`
(the LLM review), the code in `src/`, the ACP SDK, the OpenRouter API
reference, and the philosophy in `AGENTS.md`.

Descriptions of current behavior and every `src/` line reference are pinned
to `e8366bc`, where the model layer is Rig. The working tree's uncommitted
swap of Rig for `genai` is an interim step, not the target described here.

## Position

Ox's architecture is right and should not change shape: one process, one
ACP connection, one model loop that Ox owns over one OpenRouter adapter that
Ox writes, one SQLite transcript, two projections of the same events. The
work is to make ownership explicit, to take the model loop back from the SDK,
and to fix seven observable defects in the prompt path. Most of the
organization review's structural recommendations are accepted. Four are
modified where the code, the SDK, or the wire contradict them, and two are
rejected as premature.

Accepted: `ServerState`, a concrete `SessionStore`, an RAII active-turn
guard, a `PendingIteration` type that owns the iteration invariants, a pure
`convert` module, the ACP module split, the naming table, every "do not add"
item.

Modified: the store holds one open connection instead of a path; the
transcript API returns events without a timestamp wrapper; the ACP `SessionId`
stays the shared identifier across all modules; the model/tool loop lives in
`acp/prompt.rs` beside the commits and notifications it interleaves, not in
the adapter module.

Rejected for now: the `session/` directory split and a separate session-owned
ID type.

Added, because they are current defects or wire requirements rather than
organization: a hand-written OpenRouter adapter with a six-type seam, a
uniform terminal-outcome rule for the prompt loop, all five ACP stop reasons
produced from OpenRouter finish reasons and Ox's own call budget, one commit
per model call that includes the call's tool results, in-flight model history
extended only from committed events, assistant-message grouping that the
OpenAI-compatible wire requires, rejection of `session/load` while a prompt is
active, and lazy credential loading so terminal login works without a restart.

## Evidence base

Read in full: `AGENTS.md`, the three review documents, `src/*.rs`,
`Cargo.toml`, `README.md`, `eng/prompts/*`, the shell-tool notes at
`HEAD:eng/docs/shell-tool.md`. Read selectively: `agent-client-protocol`
2.1.0 and `agent-client-protocol-schema` 1.7.0 from the Cargo registry;
`rig-agent` 0.42.0 and `rig-core` 0.42.0 for the pinned code's behavior only.
OpenRouter API reference pages fetched 2026-09-19: chat completion,
streaming, tool calling, reasoning tokens, errors. `cargo test` passes: 33
tests at `e8366bc` and 33 in the working tree.

Two facts about the repository shape matter for scoping:

- The `rust` branch is an orphan. It shares no commit with `main`, which is
  the Go implementation with a web client. Fourteen commits exist on `rust`.
- `docs/` is gitignored and describes the Go agent: permissions, modes, a
  tool catalog, settings files, headless mode, LSP. It is roadmap evidence
  for what Ox may grow toward. It is not a specification for this branch and
  nothing below treats it as one.

## Current architecture, as built

```text
main.rs        command parsing; `ox` serves ACP, `ox auth login|logout` manage the key
acp.rs         everything protocol-facing: server wiring, seven handlers, in-flight map,
               cancellation, prompt loop, eight pure projection functions   (767 lines + tests)
agent.rs       Rig OpenRouter client, model/turn constants, tool-outcome hook   (95 lines + tests)
tools.rs       canned `get_weather` tool and its display title
sessions.rs    schema, private JSON DTOs, per-call connection opens, free functions (520 lines + tests)
auth.rs        env var then keyring
```

Dependency direction today: `acp` → `agent`, `sessions`, `tools`, `auth`;
`agent` → `tools`; `sessions` → ACP schema types (`SessionId`, `SessionInfo`,
`src/sessions.rs:11`). Nothing else crosses.

The durable model is a per-session ordered event log (`src/sessions.rs:44-52`)
with five closed event kinds (`src/sessions.rs:55-71`), private on-disk DTOs
(`src/sessions.rs:89-147`), and unknown kinds rejected on read
(`src/sessions.rs:218`). The same events feed model history
(`src/acp.rs:171-233`) and client replay (`src/acp.rs:339-371`). This is the
strongest part of the codebase and the proposal keeps it byte-for-byte.

The model loop belongs to Rig at this commit: Ox observes a stream that
interleaves model deltas, hidden tool execution, and hook callbacks. The
working tree moves the loop into `acp.rs` over `genai`, which is the right
direction and the wrong stopping point: the loop is Ox's, so the request
underneath it should be too.

## Constraints from the ACP SDK

These are verified in the vendored sources. Several organization-review
recommendations and one ACP-review recommendation depend on them.

| Fact | Where | Consequence for Ox |
| --- | --- | --- |
| Request and notification handlers run inside the connection's dispatch loop and block all further message processing until they return. | `agent-client-protocol-2.1.0/src/jsonrpc.rs` doc on `on_receive_request` (line 1494); `src/concepts/ordering.rs` | Only the prompt handler spawns (`src/acp.rs:492`). `session/load`, `session/new`, `session/list`, `session/delete`, and `logout` run inline, including their SQLite I/O. A `session/cancel` cannot be processed while one of them runs. |
| Spawned tasks go through an unbounded channel and run concurrently. | `src/jsonrpc/task_actor.rs:9,57-66` | Prompts for different sessions run concurrently on the Tokio multi-thread runtime (`Cargo.toml:16`). |
| Outgoing notifications and responses share one unbounded channel drained by one writer. `send_notification` is synchronous and never blocks. | `src/jsonrpc/outgoing_actor.rs:9-17` | A notification enqueued before `responder.respond` is written before the response. There is no backpressure and no place for Ox to add a bounded output policy below its own code. |
| An error escaping a spawned task shuts the server down. | `src/jsonrpc.rs:3707`, `spawn` docs | Provider and storage failures must become RPC error responses inside the prompt task, never task errors. |
| An incoming request with no registered handler gets `method_not_found`. | `src/jsonrpc/incoming_actor.rs:620` | Ox registers no `authenticate` handler. That is correct for terminal auth, see next row. |
| Terminal auth: "The client MUST NOT pass this method to `authenticate`." A zero exit status signals success. | `agent-client-protocol-schema-1.7.0/src/v1/agent.rs`, `AuthMethodTerminal` docs | After `ox auth login` exits, the client retries the failed request. Ox reads the keyring once at startup (`src/acp.rs:374-379`), so the retry fails until the agent process restarts. |
| `StopReason` has `EndTurn`, `MaxTokens`, `MaxTurnRequests`, `Refusal`, `Cancelled`. | `schema/src/v1/agent.rs:3164-3183` | Ox emits only `EndTurn` and `Cancelled` (`src/acp.rs:743-747`). OpenRouter's finish reasons plus Ox's call budget produce all five. |

## Constraints from the OpenRouter API

Verified against the OpenRouter reference on 2026-09-19. These replace the
Rig stream contract as the facts the prompt loop is written against.

| Fact | Where | Consequence for Ox |
| --- | --- | --- |
| Chat completions are OpenAI-shaped: `POST /api/v1/chat/completions`, `Authorization: Bearer`, body with `model`, `messages`, `tools`, `stream`. Messages are `user` (`content`), `assistant` (`content`, `tool_calls[]`, optional `reasoning`), and `tool` (`tool_call_id`, `content`, optional `name`). | chat completion reference | The adapter's `Message` enum has exactly these three variants and serializes to these shapes. No system message until Ox has a system prompt. |
| `tools` must be sent on every request of a tool loop, including the request that carries the results. | tool calling guide | `tools::definitions()` is part of every request, not only the first. |
| Streamed tool calls arrive in `delta.tool_calls[]` keyed by `index`; `id` and `function.name` come in the first fragment, `function.arguments` as JSON string fragments. `parallel_tool_calls` defaults to true, so one completion can carry several calls. | tool calling guide; API overview | The adapter accumulates fragments per index and yields complete calls only in its terminal event. Nothing executes from a fragment. Missing ids, missing names, or arguments that do not parse fail the stream with the call id in the message. |
| `finish_reason` is normalized to `stop`, `tool_calls`, `length`, `content_filter`, `error`; `native_finish_reason` carries the provider's raw value. | API overview | `FinishReason` is a four-variant enum. `error` is a stream error, not a finish. Each of the four maps to an ACP stop reason or to tool dispatch. |
| SSE framing: comment lines such as `: OPENROUTER PROCESSING` are keep-alives; `data: [DONE]` terminates; the last data chunk before `[DONE]` repeats `finish_reason` with an empty delta and carries `usage`. | streaming reference | The parser ignores comments, stops at `[DONE]`, ignores empty deltas after the finish, and treats a stream that ends without a `finish_reason` as an error. Usage is on the stream when a consumer wants it. |
| Errors after the 200 header arrive as a data event with top-level `error {code, message, metadata}` and `finish_reason: "error"`. Errors before the body use an HTTP status equal to `error.code`: 400, 401, 402, 403, 408, 429, 500, 502, 503. | errors reference | Both become one `openrouter::Error` carrying the code and OpenRouter's message. The prompt path treats them as one failure kind. |
| An empty completion with `finish_reason: "length"` is a documented normal response: cold start, scaling, or a reasoning budget exhausted before output. | errors reference | `Length` with no text is not an error. It ends the prompt with `MaxTokens` and commits whatever thought arrived. |
| Reasoning arrives in `delta.reasoning` (plain text) and `delta.reasoning_details[]` (typed summary, text, or encrypted blocks). Assistant messages sent back in a tool loop may carry `reasoning` as a string or `reasoning_details` unmodified and in order. `reasoning.exclude` still bills. | reasoning guide | `ModelEvent::Reasoning` carries the plain text for display and the transcript. The adapter sends the stored reasoning text back as `reasoning`. `reasoning_details` are neither parsed nor stored until a fixture shows the configured model needs them. |
| Aborting a streaming request stops provider processing for supported providers; non-streaming requests run and bill to completion. | streaming reference | Every request streams. Dropping the response on cancel is the cancellation mechanism, not a UI nicety. |
| `GET /api/v1/key` validates a key. | `src/agent.rs:92-95` uses it today | Moves into the adapter unchanged. |

### Why the loop moves into Ox

The Rig contract at 0.42.0 shaped the code at `e8366bc` in four places. Each
disappears when Ox makes the request itself.

| Rig contract | Effect at `e8366bc` | With the adapter |
| --- | --- | --- |
| Tool execution runs inside the stream; success or failure is visible only through an `on_tool_result` hook (`rig-agent/src/agent/hook.rs`). | `ToolOutcomeTracker` (`src/agent.rs:36-90`): a shared map and an `expect` that the hook ran before the streamed result. | `tools::execute` returns the outcome at the call site. The tracker is deleted. |
| `CompletionCall` marks the end of a provider request, before the batch's tools run; calls and results surface after it (`streaming.rs:648-672,731-736,1210`). | Commit at `CompletionCall` (`src/acp.rs:673-683`) stores a batch's tool events one turn late; cancel repair scans only the pending vector. | One commit per model call, after its tools ran, holding the message and every result. |
| Turn-budget exhaustion is `Err(MaxTurnsError)` (`run/mod.rs:735-741`). | Reported as an internal error (P2). | The budget is Ox's constant. Exhaustion is `Outcome::MaxTurns`. |
| `ModelTurnRetried` exists for hooks Ox does not install (`streaming.rs:1436-1441`). | A handler that would clear committed tool events (`src/acp.rs:684-688`). | No retry event exists. |

The LLM review places every in-repo model path on the same seam: one
streaming call over neutral message and tool types, events out. Seven of the
fourteen projects write that seam themselves over raw HTTP and SSE. Ox joins
them with one provider and one model, which is the smallest form of the seam.

## Current problems versus hypothetical concerns

Everything in the first table is observable in the code at `e8366bc`. The
second table lists things that are not wrong today.

### Observed

| # | Problem | Where | Effect |
| --- | --- | --- | --- |
| P1 | On a stream error, pending events are committed only if an open tool call had to be repaired. With a completed tool batch and no open call, the `ToolCall` and `ToolResult` events already shown to the client are never stored. Partial thought and text are also dropped, while the cancel path stores them. | `src/acp.rs:691-716`, gate at `696`; cancel path stores at `734-741` | Reload and model history omit an exchange the client displayed. Two terminal paths, two durability rules. |
| P2 | Turn-budget exhaustion is reported as an internal error. | `src/acp.rs:691-716` receives `MaxTurnsError` | Clients show a failure for a normal, protocol-defined stop. |
| P3 | Terminal login cannot take effect without restarting the agent. | `src/acp.rs:374-379`, `35-41`; TODO at `43` | The advertised auth method does not complete the flow. |
| P4 | `session/load` during an active prompt is unguarded. Replay of the stored transcript interleaves with live updates on the same session, and the replay omits the uncommitted iteration. | `src/acp.rs:410-440`; the ACP review names this the weakest common lifecycle edge | Client transcript diverges from both the live turn and the eventual durable log. |
| P5 | A tool result with no text content returns an error from inside the stream loop, skipping repair and commit. The pending `ToolCall` stays in progress in the client and is never stored. | `src/acp.rs:629-639` | Unreachable with the string-returning weather tool. Structural: any early return inside the loop bypasses settlement. |
| P6 | Non-text prompt blocks are silently dropped from model input. | `src/acp.rs:164` | Ox advertises no image or audio capability, so a conforming client should not send them. When one does, the model sees a truncated prompt with no error. |
| P7 | Model history emits one assistant message per `ToolCall` event and attaches reasoning to the next text message rather than to the completion that produced it. | `src/acp.rs:199-209` (one message per call), `189-197` (thought carried forward) | A completion with two calls rebuilds as assistant(call 1), assistant(call 2), tool(1), tool(2). The OpenAI-compatible wire requires each `tool` message to follow the assistant message holding its call. Whether Rig's provider merged adjacent messages before sending is unverified; with a custom adapter the grouping is Ox's to get right. |

Smaller observations that are simplifications rather than defects:

- Every store call opens a connection, creates the directory, and re-runs the
  schema batch including `PRAGMA journal_mode = WAL` (`src/sessions.rs:274-282`).
  One prompt performs at least four opens before inference (`src/acp.rs:494-532`)
  plus one per iteration.
- Ten public functions have `_in` twins that exist only so tests can pass an
  in-memory connection (`src/sessions.rs:297-519`, `530-534`).
- `Event.ts` is never read in production; both projections use only `.kind`
  (`src/acp.rs:176,343`).
- `session_history_from_events` and `session_updates` return `Result` but have
  no failing path (`src/acp.rs:232,370`).
- `delete_if_idle` holds the in-flight registry mutex across a SQLite delete
  (`src/acp.rs:147-153`).
- Seven named clones of shared state feed seven closures (`src/acp.rs:380-386`).
- `finish_tool_calls` mutates the pending event list and builds ACP updates in
  one function (`src/acp.rs:321-337`), the only place a pure transcript
  operation is fused with protocol output.
- `ToolOutcomeTracker` and the `FinalResponse` fallback with its
  `completed_iteration` flag (`src/acp.rs:550,668-672,682`) exist only to
  observe Rig's hidden tool phase and emission order.

### Hypothetical

| Concern | Why it is not a problem now | Trigger that would make it one |
| --- | --- | --- |
| Blocking SQLite and keyring calls on Tokio worker threads. | Local database, sub-millisecond operations, single user. | Measured prompt latency attributable to storage, or a networked store. |
| Unbounded outgoing buffer with a stalled client. | The SDK owns the channel; Ox cannot bound it from above. Local editors drain stdio promptly. Stakpak's per-notification acks fire on enqueue and buy nothing more. | Memory growth observed with a real client. Fix belongs in the SDK. |
| Rate limits and transient 5xx from OpenRouter. | One user, one model, no observed 429 or 502. | Observed in normal use. Then a bounded retry inside the adapter before the first delta, never after output was shown. |
| Reasoning continuity without `reasoning_details`. | The stored reasoning text is sent back as `reasoning`. The configured model has not been observed to degrade. | A fixture or provider error showing the model needs its typed blocks back. Would add an opaque field to the assistant record. |
| Several conflicting tool calls in one completion. | Execution is sequential and the weather tool is idempotent. | A tool with side effects. `parallel_tool_calls: false` is one request field. |
| Wire drift when `MODEL` changes. | One model is configured. OpenRouter normalizes finish reasons and tool-call shapes. | Changing `MODEL`. Rerun the adapter fixtures against a captured stream from the new model. |
| `session/list` cursor ignored (`src/acp.rs:443`). | Session counts are small. | A client that paginates. Not architectural. |
| Two ACP clients or two Ox processes on one database. | ACP clients start one agent process per configured agent (inference about client behavior, not verified here). WAL tolerates readers. | Multiple writers. Would need the per-session guard to move into the database. |

## Pressure test of the organization review

| Recommendation | Verdict | Reasoning |
| --- | --- | --- |
| Keep one crate, one process, one runtime, one transcript. | Accept | Matches the ACP review's "native, in-process" family and everything observed. |
| Split `acp.rs` into `mod`, `prompt`, `convert`, `turns`. | Accept | The prompt closure is 280 lines of nested state (`src/acp.rs:476-757`). The pure functions (`156-371`) and the registry (`69-154`) are each self-contained with their own tests. Four files with clear owners beats one file with four owners. |
| Split `sessions.rs` into `session/mod.rs` and `session/sqlite.rs`. | Reject for now | DTOs are already private (`src/sessions.rs:89-147`) and the public enum is already behavior-shaped. The split would separate the enum from its codec with no second consumer. `AGENTS.md`: split when the boundary clarifies responsibility, not for length. Trigger: a second storage concern such as spill files. |
| Concrete `SessionStore { path: PathBuf }`. | Modify | Hold `Arc<Mutex<Connection>>`, opened once at startup with the schema applied once. This deletes the `_in` twins because tests construct `SessionStore::in_memory()`, and an in-memory database needs a persistent connection anyway. A path-holding store keeps per-call opens and needs temp directories in tests. |
| Store returns domain data; `convert` builds `SessionInfo`. | Accept | `SessionSummary` with four fields. The prompt path also needs a summary for validation and title, so the type pays for itself immediately. |
| Session module owns its ID representation. | Reject | `SessionId` is an `Arc<str>` newtype from the schema crate. A second ID type adds conversions at every call and buys nothing. The ACP review lists "one session ID across protocol, runtime, and storage" as a recurring strength. Keep the schema type everywhere. |
| `Event` becomes `TranscriptEntry`, keeping the timestamp wrapper. | Modify | Drop the wrapper. `transcript()` returns `Vec<TranscriptEvent>`. The `ts` column stays in the schema and is materialized when a consumer exists. Minimizes committed surface. |
| Private versioned DTOs; `#[serde(default)]` only where an old record's meaning is defined. | Accept | Ante's schema crate is the review's model for this. The DTOs already round-trip in tests and reject unknown kinds. No defaults are added now. |
| `ActiveTurns` with RAII `ActiveTurn` guard. | Accept | Removes the manual `finish` call and identity check (`src/acp.rs:125-133,752`). Gives load and delete one `is_active` definition. Also fixes the mutex-across-I/O in `delete_if_idle`. |
| `PendingIteration` owning thought, text, events, ordering, repair. | Accept, and extend | It is the fix for P1 and P5: every exit from the stream loop settles through the same type. `start_tools` takes a whole completion's calls at once, which is the grouping the wire needs. |
| Prompt as a transaction script with eight numbered steps. | Accept | With one change: step 6 (repair) and step 7 (final commit) run on every outcome, including errors, and step 8 maps the outcome to one of the five stop reasons. |
| `convert` as a functional core, same vocabulary for live and replay. | Accept, and tighten | One function `session_update(&TranscriptEvent) -> SessionUpdate` serves both paths. Live text deltas are projected through it too, removing the duplicated chunk constructors (`src/acp.rs:570-579` versus `349-353`; `585-594` versus `354-358`). `model_history` gains the grouping rule that fixes P7 and is also what extends the in-flight history. |
| Keep the agent module concrete; no `AgentRuntime` trait; the agent module owns the model/tool loop. | Modify | The adapter is concrete and owns one request. The loop stays in `acp/prompt.rs` because every step of it is a store commit or a client notification; moving it into the adapter would make the adapter depend on `session` and ACP types. The review's goal, no duplicated loop, holds because `prompt::drive` is the only loop and a second frontend would call it with its own `emit`. |
| Naming table. | Accept | All entries. `OxAgent` becomes `ModelClient` in `openrouter.rs`. Rename as part of the step that touches each item, never as a standalone commit. |
| No repository trait, backend trait, event bus, actor, workspace split, second transcript, ACP-specific loop. | Accept | Nothing in the code or the reviews needs any of them. The shell notes (`HEAD:eng/docs/shell-tool.md`) describe the one future that would justify a per-session queue; that document is deleted in the working tree and calls itself not a specification. |
| Testing by boundary. | Accept, with a concrete seam | `prompt::drive` takes a `complete` closure that returns model streams and an `emit` closure that receives updates. Tests feed `futures::stream::iter` of `ModelEvent` values and collect updates in a `Vec`. The adapter is tested against SSE fixtures without a network. No trait. |
| Refactoring order: convert, store/state, turns, prompt, session split. | Modify | Adapter first, because it is the largest unknown and its fixtures prove the wire before anything is built on it. Store and state second, because the prompt refactor wants `store.session()` and `ServerState`. Convert third. Turns fourth. Prompt fifth, carrying the P1, P2, P5, P7 fixes and their tests. Credential loading is independent and can land any time. Drop the session split. |

The ACP review's six implications are all adopted: rejection stays the busy
policy; load-during-prompt is rejected explicitly; the durability contract is
stated below; one event schema remains the seam; no bounded output policy is
added (and none can be, see the SDK constraints); lifecycle interleavings get
tests before capabilities.

## What the expanded ACP review adds

The review now covers fourteen projects. The newer entries sharpen the
existing design; none justifies more machinery.

| Evidence | Consequence for Ox |
| --- | --- |
| Octomind commits each built message before it enters memory ("atomic add"). | The in-flight `history` is extended only from events the store has committed, through the same `convert::model_history` projection a reload uses. The model never sees a message the transcript does not hold. |
| Octomind drops cancelled post-tool rounds, so the client can have seen work the log does not contain. | Cross-project precedent for P1. Settlement and the final commit run on every outcome. |
| Octomind holds a per-session mutex across the whole prompt and removes the session from its registry while a turn runs. | `ActiveTurns` holds a cancel signal, not the session. Load, delete, and list read the map without waiting on a running prompt. |
| Stakpak collapses every ACP session onto one history and cancels every session on any cancel; its checkpoints are never read back. | Per-session signal, per-prompt history, and a test that cancelling session A leaves session B streaming. Every prompt reconstructs from the store, which is the only runtime truth. |
| VTCode mints session IDs its load path cannot resolve, so the advertised `loadSession` capability is unreachable. | One `SessionId` from `session/new` through `load` and `delete`. The load test uses an ID Ox returned. Capabilities stay limited to what works for Ox-created sessions. |
| VTCode drops notification send errors from the prompt task. | A failed `emit` is `Outcome::Failed`, settled and committed like any other failure. |
| Ante classifies events as live deltas, final records, replay-only records, and never persisted. | The persistence contract names Ox's classes explicitly, below. |
| Ante versions its wire types with `#[serde(default)]` and round-trip tests. | The private DTOs keep their round-trip tests and reject unknown kinds. No defaults until an old record's meaning for a new field is defined. |
| Ante replays a bounded window; OpenHands recovers the UI separately from model context. | Full committed-history replay stays. Model continuation and client replay are tested separately: `model_history` over `transcript()`, and `session_update` per event. |
| Stakpak's per-notification acks fire on enqueue, not consumption. | "Updates before the response" is an enqueue-ordering guarantee under this SDK. Nothing stronger is claimed. |

## Proposed architecture

### Module layout

```text
src/
  main.rs            command parsing, process composition
  auth.rs            unchanged
  openrouter.rs      ModelClient, Message, ToolCall, ModelEvent, FinishReason, SSE parsing, key verification
  tools.rs           canned get_weather: JSON schema definition, execute, display title
  session.rs         SessionStore, SessionSummary, TranscriptEvent, ToolOutcome, codecs, SQL
  acp/
    mod.rs           ServerState, run(), seven handlers, error helpers
    turns.rs         ActiveTurns, ActiveTurn
    prompt.rs        run(), drive() which is the model/tool loop, PendingIteration, Outcome
    convert.rs       pure projections between ACP, transcript, and adapter types
```

Dependency direction: `main → acp`; `acp::mod → turns, prompt, convert,
session, openrouter, auth`; `prompt → turns, convert, session, openrouter,
tools`; `convert → session, openrouter, tools` (for `tool_call_title`).
`openrouter` imports nothing from the crate. `session` imports only
`SessionId` from the schema crate. `openrouter` and `session` never import
each other.

`agent.rs` is deleted; `openrouter.rs` replaces it. `Cargo.toml` drops `rig`
(and the interim `genai`); `reqwest` gains its `json` and `stream` features.
No SSE or OpenAI client crate: the parser is a `data:` line accumulator of
about thirty lines with fixture tests, and `AGENTS.md` says not to add a
crate for something used once.

### Owned types and interfaces

`openrouter.rs`:

```rust
pub(crate) const MODEL: &str = "openai/gpt-5.6-luna";
const ENDPOINT: &str = "https://openrouter.ai/api/v1/chat/completions";
const KEY_ENDPOINT: &str = "https://openrouter.ai/api/v1/key";

#[derive(Clone)]
pub(crate) struct ModelClient { http: reqwest::Client, api_key: String }

#[derive(Debug, Clone, PartialEq)]
pub(crate) enum Message {
    User(String),
    Assistant { reasoning: Option<String>, text: String, tool_calls: Vec<ToolCall> },
    Tool { call_id: String, name: String, content: String },
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) struct ToolCall { pub id: String, pub name: String, pub arguments: serde_json::Value }

#[derive(Debug, Clone, PartialEq)]
pub(crate) enum ModelEvent {
    Reasoning(String),                                              // delta
    Text(String),                                                   // delta
    Finished { reason: FinishReason, tool_calls: Vec<ToolCall> },   // exactly once, last
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum FinishReason { Stop, ToolCalls, Length, ContentFilter }

#[derive(Debug)]
pub(crate) struct Error { pub code: Option<u16>, pub message: String }  // Display: "OpenRouter 429: Rate limit exceeded"

pub(crate) type ModelStream = Pin<Box<dyn Stream<Item = Result<ModelEvent, Error>> + Send>>;

impl ModelClient {
    pub(crate) fn new(api_key: &str) -> Self;
    /// One streaming completion. Resolves when the response headers arrive; HTTP errors resolve here.
    pub(crate) async fn complete(&self, messages: &[Message], tools: &[serde_json::Value])
        -> Result<ModelStream, Error>;
}

pub(crate) async fn verify_key(api_key: &str) -> Result<(), Error>;

/// Pure: one SSE data payload in, zero or more events out; owns the per-index tool-call fragments.
struct StreamState { tool_calls: BTreeMap<u32, PartialToolCall>, finished: bool }
fn parse_chunk(state: &mut StreamState, data: &str) -> Result<Vec<ModelEvent>, Error>;
```

Wire rules the adapter enforces, each backed by a fixture test:

- The request body is `model`, `messages`, `tools`, `stream: true`. No
  `reasoning`, `parallel_tool_calls`, `max_tokens`, or `stream_options`
  until something needs them.
- `Message::Assistant` serializes `content` as the text or `null`,
  `tool_calls` as `{id, type: "function", function: {name, arguments}}` with
  `arguments` re-serialized to a string, and `reasoning` when present.
  `Message::Tool` serializes `tool_call_id`, `name`, `content`.
- `delta.content` yields `Text`; `delta.reasoning` yields `Reasoning`;
  `delta.tool_calls` fragments merge by `index`. Empty strings yield nothing.
- A chunk carrying `error` fails the stream with its code and message.
- The first chunk with a `finish_reason` closes the completion: every
  accumulated call must have an id, a name, and arguments that parse as JSON,
  or the stream fails naming the call id. `Finished` is yielded once. Later
  chunks with empty deltas, the usage chunk among them, are ignored.
- `[DONE]` or end of body before `Finished` fails the stream: "stream ended
  without a finish reason". A `finish_reason` outside the four normalized
  values is the same kind of error, with the raw value in the message.
- Comment lines and blank lines are skipped.

`tools.rs` keeps `execute(name, arguments) -> Result<String, String>` and
`tool_call_title`. `definitions()` returns the `{"type": "function",
"function": {...}}` values the request carries.

`session.rs`:

```rust
#[derive(Clone)]
pub(crate) struct SessionStore(Arc<Mutex<Connection>>);

pub(crate) struct SessionSummary {
    pub id: SessionId,
    pub cwd: PathBuf,
    pub title: Option<String>,
    pub updated_at: String,
}

#[derive(Debug, Clone, PartialEq)]
pub(crate) enum TranscriptEvent {            // was EventKind; variants unchanged
    UserMessage(String),
    AgentThought(String),
    AgentMessage(String),
    ToolCall { call_id: String, name: String, arguments: serde_json::Value },
    ToolResult { call_id: String, name: String, outcome: ToolOutcome },
}

impl SessionStore {
    pub(crate) fn open() -> io::Result<Self>;      // resolves the data dir, creates it, applies SCHEMA once
    pub(crate) fn in_memory() -> Self;             // tests
    pub(crate) fn create(&self, cwd: &Path, id: &SessionId) -> io::Result<()>;
    pub(crate) fn session(&self, id: &SessionId) -> io::Result<Option<SessionSummary>>;
    pub(crate) fn list(&self, cwd: Option<&Path>) -> io::Result<Vec<SessionSummary>>;
    pub(crate) fn transcript(&self, id: &SessionId) -> io::Result<Vec<TranscriptEvent>>;
    pub(crate) fn append(&self, id: &SessionId, title: Option<String>,
                         events: &[TranscriptEvent], at: &str) -> io::Result<()>;
    pub(crate) fn delete(&self, id: &SessionId) -> io::Result<bool>;
}

pub(crate) fn now() -> String;
pub(crate) fn title_from_prompt(text: &str) -> Option<String>;
```

`append` keeps today's semantics: one transaction, activity update with
`COALESCE(title, ?)`, empty slices are a no-op (`src/sessions.rs:417-459`).
`session()` replaces `session_exists`, `session_exists_anywhere`, and
`session_title`; callers compare `cwd` themselves. The private DTOs, kind
strings, and JSON shapes are unchanged, so existing databases open as-is.

`acp/mod.rs`:

```rust
#[derive(Clone)]
pub(crate) struct ServerState {
    client: Arc<Mutex<Option<openrouter::ModelClient>>>,
    pub(crate) store: session::SessionStore,
    pub(crate) turns: turns::ActiveTurns,
}

impl ServerState {
    /// Returns the cached client, or builds one from the keyring when none is cached.
    pub(crate) fn client(&self) -> Result<openrouter::ModelClient>;   // Error::auth_required when no key
    pub(crate) fn forget_client(&self);                                // logout
}

fn busy() -> Error;   // Error::invalid_request() with "session has a prompt in progress"
pub async fn run() -> Result<()>;
```

`acp/turns.rs`:

```rust
#[derive(Clone, Default)]
pub(crate) struct ActiveTurns(Arc<Mutex<HashMap<SessionId, CancelSignal>>>);

impl ActiveTurns {
    pub(crate) fn try_start(&self, id: SessionId) -> Option<ActiveTurn>;
    pub(crate) fn cancel(&self, id: &SessionId);
    pub(crate) fn is_active(&self, id: &SessionId) -> bool;
}

pub(crate) struct ActiveTurn { id: SessionId, turns: ActiveTurns, signal: CancelSignal }

impl ActiveTurn {
    pub(crate) async fn cancelled(&self);
    pub(crate) fn is_cancelled(&self) -> bool;
}

impl Drop for ActiveTurn { /* remove `id` from `turns` */ }
```

`CancelSignal` is today's oneshot-plus-`Shared` pair (`src/acp.rs:72-75`),
kept private. The identity check in `finish` (`src/acp.rs:127-131`) is not
needed: a guard removes only its own entry, and `try_start` cannot insert a
second entry while the first guard lives.

`acp/prompt.rs`:

```rust
const MAX_MODEL_CALLS: usize = 8;

pub(crate) enum Outcome {
    EndTurn,
    MaxTokens,
    Refusal,
    MaxTurns,
    Cancelled,
    Failed(Error),
}

/// Handler-facing entry: validates, commits the user message, builds the closures, drives, responds.
pub(crate) async fn run(state: ServerState, turn: ActiveTurn, request: PromptRequest,
                        connection: ConnectionTo<Client>, responder: Responder<PromptResponse>)
    -> Result<()>;

/// Testable core: the model/tool loop. Runs to a terminal outcome, settling and committing on every path.
pub(crate) async fn drive(
    store: &SessionStore, id: &SessionId, turn: &ActiveTurn,
    history: Vec<Message>,
    complete: &mut impl FnMut(&[Message]) -> BoxFuture<'static, Result<ModelStream, openrouter::Error>>,
    emit: &mut impl FnMut(SessionUpdate) -> Result<()>,
) -> Outcome;

struct PendingIteration { thought: String, message: String, events: Vec<TranscriptEvent> }

impl PendingIteration {
    fn push_thought(&mut self, delta: &str);
    fn push_text(&mut self, delta: &str);
    fn start_tools(&mut self, calls: &[ToolCall]) -> &[TranscriptEvent];   // flushes thought and text, then one ToolCall per call
    fn finish_tool(&mut self, call_id: String, name: String, outcome: ToolOutcome) -> &TranscriptEvent;
    fn close_open_tools(&mut self, outcome: ToolOutcome) -> Vec<TranscriptEvent>;
    fn drain(&mut self) -> Vec<TranscriptEvent>;                             // flushes text first
}
```

`start_tools` flushes buffered thought and text before appending the calls,
which is the ordering rule `flush_assistant_text` enforces today
(`src/acp.rs:599-603,640-644`), and appends all of a completion's calls
together so `model_history` can rebuild them as one assistant message.
`close_open_tools` is `unfinished_tool_calls` plus the append
(`src/acp.rs:301-337`) minus the ACP update construction; the caller projects
the returned events through `convert::session_update`.

`complete` and `emit` are closure parameters, not traits. The handler passes
`|messages| { let client = client.clone(); let tools = tools::definitions();
Box::pin(async move { client.complete(messages, &tools).await }) }` and
`|update| connection.send_notification(SessionNotification::new(id.clone(), update))`.
Tests pass a `VecDeque` of prepared streams and a `Vec` collector. One
caller, two closures.

`acp/convert.rs`:

```rust
pub(crate) fn prompt_text(blocks: &[ContentBlock]) -> Result<String>;      // Error::invalid_params for unsupported blocks
pub(crate) fn model_history(events: &[TranscriptEvent]) -> Vec<Message>;   // no Result: it cannot fail
pub(crate) fn session_update(event: &TranscriptEvent) -> SessionUpdate;    // one projection for live and replay
pub(crate) fn session_info(summary: SessionSummary) -> SessionInfo;
pub(crate) fn stop_reason(outcome: &Outcome) -> Option<StopReason>;        // None for Failed
```

`model_history` groups by completion: a run of `AgentThought`,
`AgentMessage`, and `ToolCall` events becomes one `Message::Assistant`,
closed by the first `ToolResult` or `UserMessage` or by the end of the log;
each `ToolResult` becomes one `Message::Tool` (fixes P7). The same function
extends the in-flight history after each commit, so the model sees exactly
what a restart would rebuild.

`session_update` on `AgentMessage(text)` yields an `AgentMessageChunk`; the
live path calls it with a one-delta event, the replay path with the
consolidated event. Chunk boundaries differ, vocabulary does not.

`stop_reason` maps `EndTurn`, `MaxTokens`, `Refusal`, `MaxTurns`, and
`Cancelled` to the five schema variants in order.

### Turn lifecycle

```mermaid
sequenceDiagram
    participant C as ACP client
    participant H as acp::mod prompt handler (dispatch loop)
    participant T as ActiveTurns
    participant P as prompt::drive (spawned task)
    participant S as SessionStore
    participant O as OpenRouter (via ModelClient)

    C->>H: session/prompt
    H->>H: state.client()? (lazy keyring load)
    H->>T: try_start(session_id)
    alt busy
        T-->>H: None
        H-->>C: error: session has a prompt in progress
    else admitted
        T-->>H: ActiveTurn guard
        H->>P: connection.spawn(run(...)); guard moves into the task
        P->>S: session(id); None → resource_not_found
        P->>S: transcript(id) → convert::model_history
        P->>S: append(user message, derived title)  «commit 1»
        P-->>C: SessionInfoUpdate{updated_at, title if newly adopted}
        loop up to MAX_MODEL_CALLS
            P->>O: complete(history, tools)  (select with turn.cancelled())
            loop select(turn.cancelled(), stream.next())
                O-->>P: Reasoning / Text → iteration.push_*; emit chunk
                O-->>P: Finished{reason, tool_calls} → leave the inner loop
                O-->>P: Err → Outcome::Failed
                O-->>P: None before Finished → Outcome::Failed
                T-->>P: cancelled → Outcome::Cancelled
            end
            alt reason is Length or ContentFilter
                P->>P: Outcome::MaxTokens or Refusal; calls are not started
            else Stop or ToolCalls
                P-->>C: tool_call started, one per call (iteration.start_tools)
                loop each call, checking is_cancelled first
                    P->>P: tools::execute → iteration.finish_tool
                    P-->>C: tool_call finished
                end
                P->>S: append(iteration.drain())  «commit per model call»
                P->>P: history.extend(convert::model_history(batch))
                P->>P: no calls → Outcome::EndTurn; calls → next model call
            end
        end
        P->>P: budget spent with calls pending → Outcome::MaxTurns
        P->>P: settle: repaired = iteration.close_open_tools(outcome → ToolOutcome)
        P-->>C: tool_call finished, one per repaired call
        P->>S: append(iteration.drain())  «final commit, every outcome»
        alt EndTurn / MaxTokens / Refusal / MaxTurns / Cancelled
            P-->>C: PromptResponse{stop_reason(outcome)}
        else Failed
            P-->>C: error response
        end
        P->>T: guard drops → session idle
    end
```

Rules the diagram encodes:

1. Admission happens in the handler, before spawning, so a rejected second
   prompt gets its error without a task. If `spawn` fails the guard drops and
   the session is idle again.
2. Every stream item is handled by `PendingIteration` methods through an
   exhaustive match on `ModelEvent`. The loop has no early return. Any
   failure, including a failed `emit`, becomes `Outcome::Failed` and falls
   through to settlement (fixes P5).
3. Tool calls surface only in `Finished`, so a cancel or failure during the
   stream has no open call to repair; partial thought and text are the whole
   pending state. A cancel between tool dispatches closes the calls that did
   not run as `ToolOutcome::Cancelled` and keeps the results that did.
4. The loop continues while the completion carried tool calls and the reason
   was `Stop` or `ToolCalls`. Calls arriving with `Length` or
   `ContentFilter` are truncated by definition and are never started; the
   transcript records the text and thought only.
5. Each model call commits atomically after its tools ran (contract rule 3).
   Only then is the in-flight history extended, from the committed batch.
6. Settlement runs for all six outcomes. Open calls map to
   `ToolOutcome::Cancelled` on cancel, `ToolOutcome::Failed(message)` on
   failure, and `ToolOutcome::Failed("turn ended before the tool result
   arrived")` on the rest. The last arm is unreachable under rule 3 and is
   kept so the match is exhaustive rather than assumed.
7. The final commit runs for all six outcomes, storing repaired results and
   any partial thought or text (fixes P1). On the `EndTurn` path it is an
   empty no-op. The response is sent only after the commit succeeds. If the
   commit fails, the response is that storage error.
8. `Outcome::MaxTurns` responds with `StopReason::MaxTurnRequests` (fixes
   P2). The last permitted completion's tools do run and are committed, so
   the next prompt's history ends with tool results and is valid on the wire.
9. Dropping the response stream on cancel ends the HTTP request, which
   OpenRouter documents as stopping provider work for streaming requests.

### Session state model

Live state per session is one bit: idle, or active with a cancel signal.
Durable state is the `sessions` row plus its `events`.

| Operation | Precondition | Live effect | Durable effect | Error |
| --- | --- | --- | --- | --- |
| `session/new` | client available | none | `create` (idempotent) | `auth_required`, storage |
| `session/load` | client available; idle; row exists with matching `cwd` | none | none | `auth_required`, `busy()`, `resource_not_found`, storage |
| `session/prompt` | client available; idle; row exists | active until settled | commits 1..N | `auth_required`, `busy()`, `resource_not_found`, storage, model failure |
| `session/cancel` | none | signal if active; no-op otherwise | via the running prompt's settlement | none |
| `session/delete` | idle | none | row and events removed | `busy()`, storage |
| `session/list` | none | none | none | storage |
| `logout` | none | cached client dropped; running prompts keep their clone | none | keyring |

`session/load` while active is rejected with `busy()` (fixes P4). The
alternative, replaying through a captured event cursor and then handing over
to the live stream, needs a cursor concept in the store and ordering logic
between two producers. The ACP review's own summary calls rejection "the
safest small default". A client loads a thread when it is opened rather than
while prompting in it (inference), so the rejection is expected to be rare.

`session/delete` checks `is_active` and then deletes, as two steps. A prompt
admitted between the two steps fails at its first `append` with a foreign-key
error, which is surfaced as a storage error. That race is accepted: it needs a
client to delete a session it is prompting, and the result is a clear error
and a clean database, not bad state.

### Concurrency

- Cross-session: concurrent. Spawned prompt tasks run on the multi-thread
  runtime. The store serializes writes through its mutex for the microseconds
  each statement takes; guards are never held across an `await`.
- Same-session: rejected. `ActiveTurns` is the only admission point.
- Cancellation: per session, delivered by the cancel notification handler
  which runs inline in the dispatch loop and only flips a oneshot. A cancel
  arriving between admission and the first poll is observed by the `select!`
  around `complete` and produces `Outcome::Cancelled` with nothing to repair.
  Cancelling one session never touches another; a test asserts it.
- Shared client: one `ModelClient` clone per prompt, a `reqwest::Client`
  handle and the key. Per-prompt state is the history, the
  `PendingIteration`, and the call counter, all local to `drive`. Nothing
  about a prompt is shared.
- Dispatch loop: `load`, `new`, `list`, `delete`, and `logout` keep running
  inline. Their I/O is local SQLite and the keyring. Spawning them would let a
  cancel be processed during a load, at the cost of a `Responder` move into a
  task for each. Not worth it until a load is observed to take long enough to
  matter.

### Error behavior

- `io::Error` inside `session` and `auth`. `openrouter::Error` inside the
  adapter, carrying the HTTP status or in-stream error code and OpenRouter's
  message, so a client sees `OpenRouter 402: Insufficient credits` rather
  than a transport wrapper. `agent_client_protocol::Error` at the ACP
  boundary, produced by `Error::into_internal_error` for storage and model
  failures, `Error::auth_required` when no key exists,
  `Error::resource_not_found(Some(id))` for unknown sessions,
  `Error::invalid_params` for unsupported prompt blocks (fixes P6), and
  `busy()` for admission failures.
- Malformed wire input is a normal boundary case: an unparseable chunk,
  a finish without a reason, or a call with malformed arguments fails the
  stream with a clear message and the prompt settles normally.
- Malformed stored data stays an error on read (`src/sessions.rs:218`), never
  a skipped row.
- Broken invariants panic: poisoned mutexes, a second `Finished` from the
  parser. With `panic = "abort"` these end the process, which is the intended
  loud failure for internal bugs.
- No error enum with recovery variants. Nothing recovers differently today;
  the 401-to-`auth_required` mapping is deferred with its trigger.

### Persistence and protocol contract

Stated so the tests can assert it:

1. The user message is durable before the first model call.
2. Text and reasoning deltas and tool-call starts reach the client before they
   are durable. Their durable form is the consolidated event committed at the
   end of the model call or at settlement.
3. Each model call commits atomically, after its tools ran: the call's
   thought, then its text, then every `ToolCall`, then every `ToolResult`,
   sharing one `updated_at`. Nothing from one model call is committed with
   another.
4. Every open tool call is closed with a terminal `ToolResult` before the
   final commit, on every outcome.
5. Partial thought and text present at cancellation or failure are committed.
   Replay then shows what the client saw. The alternative, rolling them back,
   would make replay and the client's live view disagree, since ACP has no
   retraction update.
6. A prompt response, success or error, means the final commit and every
   preceding notification enqueue succeeded. Enqueue is the strongest
   guarantee the SDK offers.
7. After a process restart the transcript ends at a commit boundary. Because
   of rule 3, it never ends with an open tool call and never holds an
   assistant message whose calls lack results. `model_history` relies on
   this and does not repair.
8. The in-flight history is extended only from committed events, through
   `model_history`. What the model sees mid-prompt equals what a restart
   would rebuild.
9. Replay is a projection: consolidated chunks, `ToolCall` with
   `InProgress` followed by `ToolCallUpdate` with the terminal status, no
   `SessionInfoUpdate`. Stop reasons are not recorded in the transcript.
10. The on-disk schema, kind strings, and JSON shapes do not change. No data
    migration.

Ox's event classes, in the ACP review's terms:

| Class | Ox |
| --- | --- |
| Provisional: live before durable | `AgentThoughtChunk` and `AgentMessageChunk` from deltas; `ToolCall` with `InProgress`. |
| Durable: the transcript | The five `TranscriptEvent` kinds. Replay emits nothing that was not one of these. |
| Never persisted | `SessionInfoUpdate`, stop reasons, usage, `native_finish_reason`, `reasoning_details`, keep-alive comments. |

### Credentials

`ServerState::client` returns the cached client or, when none is cached,
calls `auth::api_key()` and builds one (fixes P3). `logout` deletes the
keyring entry and clears the cache. Running prompts keep their clone and
finish. When `OPENROUTER_API_KEY` is set in the environment, logout has no
effect on the next lazy load; `main.rs:58-60` already warns about this for the
CLI and the same sentence belongs in the logout response path as a log line.
`ox auth login` validates through `openrouter::verify_key`. Ox still registers
no `authenticate` handler, which is what the schema requires for a terminal
method.

## Migration steps

Each step is one commit, leaves `cargo test` green, and updates the source
map in `AGENTS.md` when it changes the file layout.

1. **Adapter.** Add `openrouter.rs` with `ModelClient`, the six types, the
   SSE parser, and `verify_key`. Remove `rig` and `genai`; enable `reqwest`'s
   `json` and `stream` features. Port the existing loop's stream match to
   `ModelEvent` with no other change and delete `ToolOutcomeTracker`.
   Fixture tests are listed in the next section. Behavior change: none
   intended; the weather prompt is checked manually against OpenRouter.
2. **Store and state.** Rename `sessions.rs` to `session.rs`. Introduce
   `SessionStore(Arc<Mutex<Connection>>)`, `SessionSummary`,
   `TranscriptEvent`, `transcript()`, `session()`. Delete the `_in` twins and
   the `Event` wrapper; port the storage tests to `SessionStore::in_memory()`.
   Introduce `ServerState` in `acp.rs` and replace the seven clones. Open the
   store in `acp::run` so a bad data directory fails at startup. Behavior
   change: none observable by a client.
3. **Convert.** Create `acp/convert.rs` and move the pure functions with
   their tests under the new names. Collapse the two chunk constructors into
   `session_update`. Drop the two impossible `Result`s. Make `prompt_text`
   exhaustive and return `invalid_params` for unsupported blocks, with a test.
   Give `model_history` the grouping rule, with a two-call test. Behavior
   change: P6, P7.
4. **Turns.** Create `acp/turns.rs` with the guard. Replace `begin`/`finish`
   in the prompt handler. Make `load` and `delete` call `is_active`. Tests:
   duplicate admission, release on drop, release on early return, cancel
   isolation across two sessions, load rejected while active. Behavior
   change: P4.
5. **Prompt.** Create `acp/prompt.rs` with `run`, `drive`, `PendingIteration`,
   `Outcome`. Move the loop body out of the closure. Commit per model call
   and extend the history from the committed batch. Map every finish reason
   and the call budget to an outcome. Settle and commit on every outcome.
   Behavior change: P1, P2, P5, plus `MaxTokens` and `Refusal` stops.
6. **Credentials.** Implement lazy loading in `ServerState::client` behind a
   `client_or_load(&self, load: impl FnOnce() -> io::Result<Option<String>>)`
   helper in the style of `auth::api_key_with` (`src/auth.rs:23-38`) so the
   test does not touch the keyring. Behavior change: P3. This step has no
   dependency on the others and can land any time.

Stop after any step if the code is easy to navigate. Step 1 removes the
largest unknown; step 5 carries the defect fixes and is the reason steps 2
through 4 exist.

## Verification strategy

Unit tests beside the code, organized by the boundary they specify:

- `openrouter`, `parse_chunk` and the body reader against SSE fixtures
  captured from OpenRouter and committed as strings:
  - text with keep-alive comments and the trailing usage chunk: `Text`
    deltas, one `Finished { Stop }`, nothing after;
  - reasoning deltas then text: `Reasoning` before `Text`;
  - two tool calls whose arguments are split across chunks: one `Finished {
    ToolCalls }` with both calls parsed, in index order;
  - a mid-stream `error` event: `Err` with the code and message, no
    `Finished`;
  - `finish_reason: "length"` with empty content: `Finished { Length }` and
    no `Text`;
  - `[DONE]` with no finish reason: `Err`;
  - a call whose arguments are not JSON: `Err` naming the call id;
  - request serialization: an `Assistant` with two calls and reasoning, and
    a `Tool` message, match the documented shapes;
  - an HTTP 402 body: `Err { code: Some(402) }` from `complete`.
- `session`: the existing fourteen tests ported to `in_memory()`, plus:
  `session()` returns `None` for unknown ids and the workspace path for known
  ones; `append` with an empty slice leaves `updated_at` unchanged.
- `acp::convert`: every `TranscriptEvent` variant through `model_history` and
  `session_update`; thought, text, and two calls from one completion rebuild
  as one assistant message followed by two tool messages (P7 regression);
  `Cancelled` and `Failed` outcomes both replay as `Failed`; `prompt_text`
  rejects an image block and keeps resource links; `stop_reason` covers all
  six outcomes.
- `acp::turns`: as listed in step 4.
- `acp::prompt`, `PendingIteration` alone: thought and text are flushed before
  a completion's calls are appended, in that order; `close_open_tools`
  closes only calls without results and preserves order; `drain` empties all
  three buffers.
- `acp::prompt`, `drive` with fake streams built from `ModelEvent`, a
  `complete` closure that serves a `VecDeque` of streams, and an in-memory
  store:
  - text, `Finished { Stop }`: one commit, `EndTurn`, updates in order;
  - `Finished { ToolCalls, [a] }` then a second stream that cancels the turn
    as a side effect of its first poll: `Cancelled`, the first call executed
    and committed, nothing to repair, empty final commit;
  - a terminal item that cancels the turn before yielding two calls:
    `Cancelled`, no tool executed, both calls committed as `Cancelled`
    (cancel between dispatches);
  - `Finished { ToolCalls, [a] }` with its result, then `Err` on the second
    call: `Failed`, the first call and its result committed (P1 regression);
  - text then `Err` mid-stream: `Failed`, the partial text committed (P1);
  - `emit` returning an error after a tool start: `Failed`, the call closed
    and committed;
  - eight completions each carrying a call: `MaxTurns`, eight commits, the
    rebuilt history ends with a tool result;
  - `Finished { Length }` with no text: `MaxTokens`, empty final commit;
  - `Finished { ContentFilter }`: `Refusal`;
  - two completions: two commits with distinct timestamps and each tool pair
    grouped with its own completion (contract rule 3);
  - after any of the above, `model_history(transcript())` equals the history
    `drive` held when it returned (contract rule 8).
- Server boundary, manual until an in-process transport harness is worth
  building: new thread, prompt that triggers the weather tool against
  OpenRouter, cancel during a multi-call prompt, reopen the thread by the ID
  Ox returned and compare with the live view, delete, and the terminal login
  flow against Zed with no agent restart.

The fake-stream tests are the ones the ACP review ranks highest: prompt
versus load, cancel during a tool call, failure with an open tool call, send
failure after persistence, restart followed by load. The adapter fixtures are
what the LLM review says every bespoke seam owes: the wire proved locally
before the loop depends on it.

## Tradeoffs

| Choice | Cost | Why it is still right |
| --- | --- | --- |
| Hand-written OpenRouter adapter instead of an SDK | Ox owns SSE parsing, tool-call assembly, and error shapes for one provider | One provider and one model; the seam is one request and six types; the SDK loops hid the tool phase and the commit point, which produced P1, P2, P5, and the tracker |
| Tool calls surface only in the terminal event | The client sees a call after its arguments finish streaming | Arguments arrive as fragments; one durable shape; nothing executes from a fragment |
| Sequential tool execution | Slower when a completion carries several calls | One canned tool; no ordering or cancellation ambiguity; `parallel_tool_calls` stays default until a tool has side effects |
| In-flight history rebuilt from committed events | One projection call per model call; `reasoning_details` are not round-tripped | The model sees exactly what a restart would rebuild; Octomind's rule for free; one projection instead of two |
| Reasoning sent back as a plain string | Encrypted or signed reasoning continuity is lost for models that use it | The transcript already holds the text; typed blocks are opaque blobs with no consumer yet |
| One connection behind a mutex | Writes for different sessions serialize; blocking calls on async threads | Statements take microseconds; the alternative reruns the schema per call and blocks the same threads anyway; enables in-memory tests without twins |
| Reject load during a prompt | A client that loads mid-prompt gets an error | Consistent with delete and prompt; a cursor-based handover is the only correct alternative and needs two-producer ordering |
| Commit partial text on failure | Replay contains an unfinished sentence | The client already displayed it; one durability rule instead of two |
| `MaxTurnRequests`, `MaxTokens`, `Refusal` instead of errors | Clients that special-case errors lose a signal | They are the protocol's words for these stops; the transcript is complete either way |
| Lazy credential load | Keyring read on each `auth_required` path until a key exists | One read per failed request, only while logged out; removes the restart requirement |
| Two closures instead of a sink or runtime trait | `drive` has a six-parameter signature | Single caller; the closures are the entire abstraction; tests need nothing else |
| Four ACP files | More files to open | Each file has one owner and one test focus; the closure it replaces is the least navigable code in the repository |
| Keep `session.rs` as one file | A 450-line file holding types, codec, and SQL | Nothing else consumes the codec; a split would separate an enum from its serializer |

## Deferred, with triggers

| Item | Trigger |
| --- | --- |
| Store and return `reasoning_details` | A fixture or provider error showing the configured model needs its typed reasoning blocks back |
| `reasoning.effort`, `max_tokens`, a system prompt | A client that exposes config options, or the first behavior that needs one |
| Bounded retry on 429 and 5xx before the first delta | Observed rate limits or gateway errors in normal use |
| 401 clears the cached client and responds `auth_required` | Observed after a key rotation |
| Usage reporting via `SessionUpdate::UsageUpdate` | A client that renders it; the usage chunk is already on the stream |
| `tool_call` started when the name fragment arrives | A tool slow enough that the wait before "started" is visible |
| `parallel_tool_calls: false` | A tool with side effects |
| Per-session event queue or actor for out-of-turn work | The shell tool's background wake-up (`HEAD:eng/docs/shell-tool.md`, "Background task lifecycle"). Design `prompt::drive` so the initiator is a parameter; do not build the queue. |
| `spawn_blocking` around store and keyring calls | Measured latency attributable to them |
| `session/` directory split | A second storage concern, such as spill files or memory |
| Spawning `load`, `new`, `list`, `delete` off the dispatch loop | A load observed to delay a cancel |
| Cursor-based `session/list` | A client that paginates |
| Materializing `ts` on transcript reads | A consumer that displays or filters by time |
| ACP v2 or background `session/update` while idle | The shell wake-up experiment |

## Non-goals

- Permissions, modes, plans, commands, config options, compaction, MCP,
  additional tools beyond the canned weather tool. `docs/` describes these for
  the Go agent; none is designed here.
- A provider trait, a second provider, a model catalog, `provider:model`
  parsing, or thinking-dialect normalization. One provider, one model, one
  constant.
- An SSE, OpenAI, or OpenRouter client crate under the adapter.
- A second runtime, transport, or transcript. No traits for any of them.
- Image or audio prompt content; unsupported blocks are rejected.
- Multiple connections per process, or multiple processes per database.
- Bounded outgoing buffering. The SDK owns that channel.
- Durable turn execution across restarts. Restart recovery is transcript
  recovery, as in every project the ACP review examined.
- Any change to the SQLite schema or the stored JSON.

## Evidence index

| Area | Locations |
| --- | --- |
| Server wiring and handlers | `src/acp.rs:373-767` |
| In-flight registry and cancel signal | `src/acp.rs:69-154` |
| Prompt loop | `src/acp.rs:476-757`; stream match `562-717`; error branch `691-716`; settlement `723-748` |
| Projections and iteration helpers | `src/acp.rs:156-371`; history grouping `171-233` |
| Transcript model and codec | `src/sessions.rs:55-247` |
| Storage operations | `src/sessions.rs:274-519` |
| Rig client, tool-outcome hook, key verification | `src/agent.rs:16-95` |
| Credentials | `src/auth.rs:15-50`; startup read `src/acp.rs:374-379` |
| ACP dispatch and channels | `agent-client-protocol-2.1.0/src/jsonrpc.rs:1494`, `src/jsonrpc/task_actor.rs`, `src/jsonrpc/outgoing_actor.rs`, `src/concepts/ordering.rs` |
| ACP schema | `agent-client-protocol-schema-1.7.0/src/v1/{agent,client,content,error}.rs` |
| Rig stream contract, pinned code only | `rig-agent-0.42.0/src/agent/prompt_request/streaming.rs:52-127,491-497,648-672,731-736,1210,1335-1349,1436-1441`; `src/agent/run/mod.rs:735-741`; `src/agent/hook.rs` |
| OpenRouter chat completions | https://openrouter.ai/docs/api-reference/chat-completion; https://openrouter.ai/docs/api-reference/overview |
| OpenRouter streaming and cancellation | https://openrouter.ai/docs/api-reference/streaming |
| OpenRouter tool calling | https://openrouter.ai/docs/guides/features/tool-calling |
| OpenRouter reasoning tokens | https://openrouter.ai/docs/guides/best-practices/reasoning-tokens |
| OpenRouter errors | https://openrouter.ai/docs/api-reference/errors |
| Model-seam survey | `eng/llm-abstractions.md`, "The convergent seam" and "Implications for Ox" |
| Expanded ACP evidence | `eng/acp-architecture-review.md`, "Recurring strengths", "Recurring failure modes", "Implications for Ox" |
