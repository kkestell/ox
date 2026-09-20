# Ox architecture proposal: own the agent loop and OpenRouter adapter

Independent proposal by Codex, revised 2026-09-19 for a custom OpenRouter adapter
and the expanded ACP architecture review.

This proposal uses the two existing architecture reviews, the Ox source at
`e8366bcda2b179ff75afd844179e3dc4e96b0393`, and the dependency/API sources discussed
below. Claude's separate design has not been used to revise this one. This
document proposes changes; it does not claim an implemented migration or
live-provider validation. Descriptions of current Ox behavior refer to that
pinned snapshot, not subsequent working-tree changes.

## Recommendation

**Replace Rig's agent runtime with a small loop owned by Ox and a custom
OpenRouter adapter.** Ox decides when to invoke a model, execute a tool, commit
a conversation batch, and finish a prompt. The concrete adapter handles one
OpenRouter request, stream assembly, and response conversion. Use ordinary
HTTP, JSON, and SSE dependencies underneath it; neither Rig nor rust-genai is
part of the target runtime.

Keep one binary, one concrete provider adapter, one authoritative SQLite
conversation, and one process-local session admission point. Add no repository
trait, provider trait, event bus, session actor, or second conversation log.

The adapter is a design assumption, not a library-selection experiment. Prove
its wire handling with local fixtures before switching the production loop.
Keep its scope to Ox's model and tool requirements; do not build a general
provider SDK, two production loops, or a runtime fallback to Rig.

## What the existing reviews get right—and what they miss

The [organization review](ox-rust-organization-review.md) correctly favors
concrete owners and a modest module split. The
[ACP architecture review](acp-architecture-review.md) correctly identifies
session admission, replay, cancellation, and durable history as the important
boundaries. The comparison projects illustrate possible failure modes; they do
not establish that Ox should keep its current runtime dependency.

The main adjustment is to put the model/tool loop under Ox's control rather
than organize an increasingly elaborate observer around somebody else's loop.
Several current problems are Ox integration bugs, not proof that Rig is broken.
Nevertheless, removing that integration layer can be a better design than
perfecting it.

### Lessons from the expanded ACP review

The updated review covers fourteen projects: twelve direct ACP implementations,
OpenHands as a gateway, and Ante as a protocol-only surface. Its newer examples
sharpen the existing design rather than justify more runtime machinery:

| Review evidence | Consequence for this design |
| --- | --- |
| Octomind persists each built message before adding it to memory. | Commit each closed batch before extending accepted model history. Pending results may exist in memory, but a failed write must not advance accepted history. |
| Stakpak shares conversation state and cancellation across ACP sessions; its checkpoints are not read back by ACP. | Keep history and cancellation session-local. Reconstruct every prompt from SQLite; a write-only checkpoint is not resumable conversation storage. |
| VTCode's bridge-created IDs do not match its archive load namespace. | Use the same ID for create, list, prompt, load, and delete; verify restart/load using an ID actually returned by Ox. Advertise only capabilities that work for Ox-created sessions. |
| Load can replace or re-point live state while old work keeps streaming. | Reserve load through the same admission point as prompt and delete, and hold it through replay. Rejection avoids the generation counters needed for live replacement. |
| Octomind and other agents lose some visible output on cancellation; Ante distinguishes transient, final, replay-only, and never-persisted events. | State which output is provisional, which records are durable, and which fields replay intentionally omits. Do not imply that live chunks are the durable transcript. |
| OpenHands separates UI recovery from model context; Ante documents only a bounded replay window and hides daemon internals. | Test model continuation and client replay separately. Neither a recovered UI nor a documented resume operation proves durable execution or a complete transcript. |

These are findings reported by the [ACP review](acp-architecture-review.md),
not new audits of those projects. Adopt the useful contracts without copying
their queues, actors, multiple logs, or protocol crates. Keep Ox's existing
full committed-history replay; bounded replay and resume-without-replay would
need explicit client semantics before becoming features.

### The current commit marker does not mean what Ox needs

Ox commits and clears its pending events on Rig's `CompletionCall`
([acp.rs:673](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L673), [acp.rs:239](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L239)). In the pinned
Rig 0.42.0 source, that event marks completion of a provider request, before the
subsequent tool phase returns its results. Cancellation repair scans only the
pending vector ([acp.rs:301](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L301)). Therefore:

```text
ToolCall(A)       -> pending A; UI shows the call
CompletionCall   -> A committed; pending vector cleared
cancel           -> repair sees no pending A
next prompt      -> durable history still contains A without a result
```

This is a source-demonstrated failure path, not a reported production incident.
A crash after the same commit creates the same durable problem. The canned
weather tool makes the window short but does not make it correct.

SQLite atomically commits the slice it receives
([sessions.rs:429](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/sessions.rs#L429)); it cannot establish that this slice
is a complete conversational batch. Both reviews overstate the existing
assistant/tool iteration guarantee.

### Cleanup and delivery are separate from registry cleanup

On provider failure, current code commits pending output only when it repaired
at least one open tool ([acp.rs:691](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L691)). Notification failures
can bypass settlement through `?`. Observed tool starts and results are put in
the pending vector after attempting notification
([acp.rs:607](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L607), [acp.rs:656](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L656)). An observed
result can thus be lost before storage has a chance to record it.

A drop guard fixes admission ownership. It cannot perform asynchronous
settlement, guarantee persistence, or reverse external tool effects.

The pinned ACP SDK's synchronous `send_notification` enqueues into an unbounded
channel. It does not acknowledge transport flush or client receipt. Consequently,
“updates succeeded before the response” must be stated as enqueue ordering.
Adding a bounded queue before this SDK channel would not bound the final queue.
The SDK also documents that an error escaping a spawned task shuts down the
server: ordinary provider failures must become RPC errors, not task errors.

### Busy checks must claim ownership

Load currently bypasses the prompt registry
([acp.rs:410](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L410)). Delete currently holds the registry mutex
across its database work ([acp.rs:147](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L147)). A refactor into
`if !is_active(id) { delete(id) }` would lose atomic exclusion. Load's current
synchronous handler limits some interleavings; it does not establish a lasting
ownership contract.

### The current transcript loses message structure

History conversion creates a separate assistant message for each tool call and
can attach preceding reasoning to a later answer instead of the call-bearing
message ([acp.rs:171](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L171)). Sharing an event enum between replay
and model reconstruction does not guarantee correct message grouping.

Plain `AgentThought(String)` also cannot represent opaque provider continuation
metadata. Changing libraries without addressing that persistence boundary would
carry the same limitation into the new implementation.

## Custom OpenRouter adapter

`model.rs` owns the OpenRouter wire contract: encode one streaming Chat
Completions request, parse the response, and expose provisional display deltas
followed by one assembled completion. It never executes tools, persists
sessions, or decides whether another model call is allowed. `tools::execute`
returns its outcome directly to the loop that stores it.

Keep private request/response DTOs and a concrete stream accumulator. Use a
normal HTTP client and an SSE parser for transport framing; Ox owns the
OpenRouter-specific interpretation. Do not reproduce HTTP/TLS machinery or
introduce a provider trait, adapter registry, model-name inference, or generic
cross-provider message format.

The earlier rust-genai inspection identified useful acceptance cases—multiple
tool calls in one frame, structured reasoning, and terminal/error handling.
Those become tests of Ox's adapter, not prerequisites for adopting or patching
that library.

### Adapter contract

- Encode the configured model, accepted history, and concrete tool schemas.
  Request one completion choice and keep mutable stream assembly local to that
  request. Credentials and a reusable HTTP connection pool can be shared;
  conversation state cannot.
- Accumulate every tool-call delta by its stream index, including multiple
  calls in one frame and arguments split across frames. Preserve the final
  provider call IDs and order. Validate envelopes before accepting the message;
  parse typed arguments only when dispatching a complete call.
- Preserve structured `reasoning_details`, including opaque continuation data,
  with the assistant message that produced it. Assemble fragments without
  flattening them into visible reasoning, and echo the complete sequence on
  tool follow-up. OpenRouter requires that sequence to remain unchanged.
  [Reasoning documentation](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens).
- Handle SSE comments and fragmented frames. Distinguish HTTP errors before
  streaming from `error` payloads inside a successful HTTP response. A final
  usage frame may repeat the finish reason; it is accounting, not a second
  completion. [Streaming documentation](https://openrouter.ai/docs/api/reference/streaming).
- Emit one completed value only after a valid finish reason and the `[DONE]`
  marker, consuming trailing usage first. This is Ox's acceptance policy:
  missing termination, conflicting terminal data, malformed payloads, and the
  first stream error enter settlement. EOF or `[DONE]` alone is not success.
- Keep cancellation attached to the owning request. Closing the response stops
  Ox's consumption; it does not promise that every upstream provider stops
  processing or billing. [Streaming cancellation](https://openrouter.ai/docs/api/reference/streaming).

Expose a small closed set of Ox-owned events for text/reasoning deltas and the
assembled completion. Tool deltas may be used for provisional display, but
never for execution. Keep wire details inside `model.rs`; the prompt loop
receives the complete assistant message, finish reason, and any usage needed
by Ox. No background reader or second event queue is needed.

The custom adapter makes Ox responsible for wire compatibility and stream
assembly bugs. Its value is direct control of the fields Ox must preserve,
not an unmeasured claim about binary size, build time, or reliability. Local
HTTP/SSE fixtures must establish this contract before migration.

## Proposed modules and owned state

```text
src/
  main.rs                 CLI and entry point
  auth.rs                 environment/keyring access
  model.rs                custom OpenRouter HTTP/SSE adapter, one completion
  tools.rs                concrete tool definitions, execution, display titles
  sessions.rs             transcript domain, SessionStore, private SQL/codec
  acp.rs                  connection setup, ServerState, short handlers
  acp/
    operations.rs         atomic session admission and cancellation signal
    prompt.rs             Ox-owned model/tool loop and terminal settlement
    convert.rs            ACP input and replay projections
```

`model.rs` replaces the current `agent.rs` runtime wrapper. It knows the fixed
provider/model and how to make one request. It does not know SQLite, session
admission, or ACP. `tools.rs` uses direct functions and a small name match, not
Rig's `Tool` trait or a new registry framework.

`acp/prompt.rs` owns the only agent loop. Its placement reflects the one current
frontend, not a second ACP implementation alongside another runtime. If another
frontend appears, move this existing loop behind the boundary it actually
requires; do not copy it or design a hypothetical sink trait now.

`convert.rs` maps ACP input and replay; `model.rs` projects accepted transcript
records into OpenRouter requests. `sessions.rs` constructs neither ACP updates
nor HTTP payloads. Keep private versioned database DTOs separate from the wire
DTOs so an adapter change does not silently redefine old data.
The existing ACP `SessionId` newtype can remain in the store for now; introducing
a second identity wrapper is less valuable than fixing conversation structure.
Use its value unchanged across creation, listing, prompt, load, and deletion.
Do not create a separate archive ID namespace or a shared current-session alias.

```rust
#[derive(Clone)]
struct ServerState {
    store: SessionStore,
    model: Arc<Mutex<Option<ModelClient>>>,
    operations: SessionOperations,
}
```

The mutex protects the credential-dependent client slot only. Clone under the
lock and release it before I/O. The entire server state must not be behind one
mutex. Each prompt owns its history, pending batch, tool outcomes, and loop
counter; no shared current-session slot or mutable global conversation exists.

These are ownership sketches, not a promised compilable patch.

### Model client

A concrete `ModelClient` contains the HTTP client and the credential returned
by `auth::api_key`. Preserve `OPENROUTER_API_KEY` and the existing keyring
lookup. Keep the OpenRouter endpoint and current model identifier as nearby
constants; fixture tests may inject a local endpoint without creating a
user-facing backend configuration layer.

Capture final content, tool calls, and reasoning for every streamed completion.
Present text/reasoning deltas to the client as provisional output; use the
captured structured message for accepted history. Keep opaque signatures and
provider details for continuation, never as visible thought text.

Replacing Rig also removes `agent::verify_api_key`. Preserve login validation
with the adapter's non-generation `GET /api/v1/key` request using bearer auth,
as documented by [OpenRouter](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key).
Test successful authentication, rejection, and transport failure separately;
a successful key lookup does not guarantee a later model request will succeed.

Keep the present credential lifecycle unless intentionally changed: logout
prevents future prompts through the client slot, while a prompt that already
cloned the client can finish. Hot reload and revocation of active requests are
separate product decisions.

### Store

Use `SessionStore { path: PathBuf }`, resolving the path once. Keep ordinary
connections per operation initially. Expose `create`, `read`, `list`,
`append_user`, `append_batch`, and `delete`. `read` returns an optional owned
session containing metadata and transcript, so missing and untitled remain
distinct. Load validates the stored workspace against the requested workspace
using the current path comparison policy.

Read metadata and transcript in a single read transaction. User append adopts
the first nonblank title and updates activity in the same transaction. Batch
append stores an ordered complete batch and activity timestamp atomically.
Clear pending state and extend accepted in-memory history only after the write
succeeds. This applies octomind's persist-before-memory rule at Ox's closed-batch
boundary, rather than treating each stream delta as a durable message.

Return a small `SessionSummary`, not ACP `SessionInfo`, from listing. Keep SQL,
codecs, and their tests together in `sessions.rs`; its length is not by itself
an argument for `session/sqlite.rs`. No repository trait, connection pool, or
database actor is required.

Synchronous SQLite can block the executor, and WAL does not remove its
single-writer constraint. Do not hold the admission mutex during database work.
Add a blocking-executor boundary only when responsiveness or measured contention
requires it; this design makes no hard latency guarantee.

### Session admission

```rust
enum Operation {
    Prompt(PromptCancellation),
    Load,
    Delete,
}

#[derive(Clone, Default)]
struct SessionOperations(Arc<Mutex<HashMap<SessionId, Operation>>>);

// Unique, non-Clone ownership of one occupied entry.
struct OperationGuard {
    operations: SessionOperations,
    session_id: SessionId,
}
```

Provide `try_prompt`, `try_load`, and `try_delete`, backed by one atomic insertion
helper. Only prompt entries have cancellation signals. Clone the signal as
needed; never clone the membership guard. Busy means any of those three
operations already owns the session.

| Incoming operation | Idle | Prompt active | Load active | Delete active |
| --- | --- | --- | --- | --- |
| Prompt | Claim | Busy | Busy | Busy |
| Load | Claim | Busy | Busy | Busy |
| Delete | Claim | Busy | Busy | Busy |
| Cancel | No-op | Signal | No-op | No-op |
| List | Read committed snapshot | Read | Read | Read |

Claim before spawning and move the guard into the prompt future. A rejected
spawn that drops the future releases membership. Hold membership through
settlement and response enqueue. Load holds it through all replay notifications
and its response; delete holds it through deletion and response. Deleting an
already absent session remains an idempotent success.

Drop performs only synchronous map removal. It does not save output or send
notifications. With private membership mutation and exactly one owner, no
manual `finish`, replacement generation, or actor is necessary. Process abort
and termination do not promise that Drop runs.

This exclusion is process-local. Multiple Ox processes sharing SQLite do not
share the registry. Supporting simultaneous writers to the same conversation
would require an explicit cross-process ownership design; database transaction
serialization is not conversation serialization.

## Ox's model/tool loop

The loop directly connects model completion, tool execution, and persistence:

```text
validate and convert prompt
claim session; read and validate durable history
commit user input and title/activity
for each allowed model call:
    stream one completion, forwarding provisional display deltas
    require a terminal event and classify its stop reason
    capture the accepted structured assistant message
    if it requests tools:
        validate complete call envelopes
        execute each tool sequentially, checking cancellation
        retain each outcome before attempting its notification
        close unexecuted/interrupted calls if ending the prompt
    commit the assistant message plus closed results atomically
    append that same accepted batch to in-memory model history
    if the assistant is finished:
        enqueue the prompt result and release admission
settle with an explicit model-call limit outcome
```

The handler must acquire admission before lifecycle work; pure input validation
can happen before that claim. No provider call precedes a durable user entry.
The loop updates in-memory accepted history only after a successful commit.

The provider client never executes a tool. A tool-call delta can describe an
incomplete argument string and is presentation only. Execute calls taken from
the final accepted message after the stream has terminated successfully. Store
all calls together in that assistant message, followed by their results.

For the initial concrete tool set, `tools::execute(name, arguments)` validates
arguments into the tool's owned input type and returns `ToolOutcome`. Unknown
names and invalid arguments produce an explicit failure result associated with
the model's call ID, so the model may correct them on a later allowed call.
Malformed call envelopes, empty IDs, and conflicting IDs fail before execution;
do not invent replacement correlation IDs.

Use sequential tool execution initially even if a completion requests several
calls. A result's success/failure is known at its return site, eliminating
`ToolOutcomeTracker`, its shared map, and the hook-before-stream assertion.
Cancellation is checked before dispatch and selected against active awaits.
Dropping today's weather future is adequate; a subprocess tool must explicitly
terminate and reap its child before claiming cancellation. Dropping a future
does not undo effects.

Keep one nearby `MAX_MODEL_CALLS` constant. A final permitted completion that
requests tools should receive explicit budget-failure results without starting
work the loop cannot continue from. Persist the closed batch and report the
limit distinctly from ordinary completion. This is an explicit Ox policy, not
an attempt to preserve undocumented Rig `max_turns` behavior.

Classify OpenRouter finish reasons explicitly. Natural completion can finish;
a tool-call stop enters dispatch; token exhaustion reports a limit; filtering
reports the appropriate refusal/error; an unknown or missing reason is a clear
unsupported-provider-response error until its meaning is established. No custom
stop sequences are needed initially. Validate the finish reason against the
assembled content; contradictory or incomplete tool-call envelopes are errors.
Require the adapter's completed value after terminal validation; EOF alone is
not completion. Stop polling on the first stream error.

## Conversation representation and durability

### One durable sequence, with complete assistant messages

Keep the events table as the authoritative sequence, but stop flattening a
new assistant message into unrelated thought/text/call records. Introduce a
versioned assistant-message record holding ordered content parts. This gives
model reconstruction its real boundary and keeps associated provider metadata
with the message that produced it.

The supported parts are a closed domain enum for text, reasoning text, and
tool calls, with OpenRouter continuation metadata attached to the message or
call it belongs to. Preserve structured reasoning details and signatures in
their original order and association, qualified by the producing model. The
adapter maps this record to OpenRouter fields; do not invent a universal
content taxonomy. Reject unsupported media rather than silently dropping it.
Opaque provider payloads are data, not executable instructions or display text.

Retain user entries and tool-result entries. A committed batch consists of one
accepted assistant message plus the result of every call it contains. Tool
outcomes remain completed, failed, or cancelled. Replay projects displayable
parts into ACP updates and omits opaque continuation data intentionally. Model
history reconstructs the complete assistant message and all paired results.

Use private DTOs with an explicit format version. Do not serialize HTTP wire
DTOs directly as the permanent database contract. Changing the adapter must
not silently reinterpret a transcript. Preserve old serialized kind strings
and their existing defaults; new grouped messages get a distinct kind/version.
Unknown kinds and malformed payloads remain errors.

This small schema change is earned by message grouping and observed
continuation-metadata requirements. It does not imply a second model transcript,
a general event-sourcing framework, or support for every provider feature.

### Closed-batch commits

Commit the user entry first. Commit an assistant response and its tool results
only once the batch is closed. A model completion without tool calls is already
a closed batch. An interrupted tool is closed with an explicit synthetic
outcome that does not claim successful execution.

The temporary owner is a `PendingBatch` containing the accepted assistant
message, observed outcomes, and the outstanding calls. Before model acceptance,
streaming output is provisional. The owned loop has separate model and tool
phases, so it needs no inference from `CompletionCall`, runtime hooks, or
cross-iteration event ordering.

This choice deliberately accepts that a crash can lose effects performed by a
tool before batch commit. Today's canned tool has no external effects. Before
adding mutating tools, decide whether to persist execution intent/results at
finer boundaries and recover them. Conversation recovery is not durable tool
execution or exactly-once effects; do not advertise either.

### One terminal settlement path

After accepting the user entry, ordinary errors must lead to one settlement
block. Do not let a notification `?` or unsupported result branch bypass it.
A small private exit enum can distinguish completion, cancellation, provider
failure, delivery failure, and storage failure; these cases have different
behavior and do not need a public error hierarchy.

| Exit | Durable action | Client action |
| --- | --- | --- |
| Complete | Commit accepted closed batch | Enqueue success after prior updates and commit |
| Cancel/provider failure before model acceptance | Discard provisional assistant output; retain committed user/history | Return cancellation/error; terminate any provisional tool display |
| Cancel after model acceptance, during tools | Preserve observed results; synthesize terminal outcomes for remaining calls; commit | Send terminal tool states if possible, then cancellation |
| Tool failure | Record failed outcome in the batch | Report failure state; let the next model call respond within the budget |
| Notification failure | Stop new execution; settle any accepted batch independently of client health | Propagate connection failure after local settlement |
| SQLite failure | Roll back that transaction; never return success or rerun effects automatically | Report persistence failure with original exit context if possible |
| Dropped task/process termination | Only earlier committed batches are guaranteed | Reconstruct through the next load; no async Drop promise |

Discarding unfinished model output is a deliberate change from current
cancellation, which can store partial text as an ordinary assistant message.
Live partial text can disappear on reload. Keeping it durably would require an
explicit interrupted-output representation; do not silently treat it as an
accepted answer. A committed user message without an assistant reply means
input was saved, not that the prompt finished.

Append observed results in memory before sending their updates. Commit terminal
repair before trying to notify a failing connection. Preserve known completed
outcomes; synthetic failure/cancellation means a result was not obtained, not
that the tool necessarily had no effects. Never re-execute a tool to repair
missing history.

### Existing transcripts

Read existing records with the existing codec. Group old contiguous assistant
thought/text/call records before the associated result boundary when converting
them to model messages. Do not attach a thought preceding a tool call to a later
answer. Test multi-call and reasoning-plus-tool histories explicitly.

Under the session guard, an unambiguous trailing open tool batch from the old
writer can be closed by appending missing failed results explaining that the
previous execution ended before recording a result and effects are unknown.
Commit those repairs before replay or inference. A second load must not add more
repairs. Orphan results, conflicting IDs, or unresolved calls buried before
later exchanges require an actionable transcript error, not a guessed rewrite.

Old records cannot recover provider signatures they never stored. Preserve
compatible old sessions within the previously supported model scope; surface a
clear continuation error if required metadata is absent. Do not fabricate it
or claim that changing adapters retroactively restores it.

## ACP behavior

Input conversion accepts text and resource-link descriptions. Replace silent
omission of other blocks ([acp.rs:156](https://github.com/kkestell/ox/blob/e8366bcda2b179ff75afd844179e3dc4e96b0393/src/acp.rs#L156)) with an unsupported
content error before persistence. Require usable nonblank input after
conversion, and derive titles from that accepted input policy.

Load reads the authoritative SQLite conversation under its session claim,
validates and repairs it as described above, and replays committed history to
the client. Every later prompt reconstructs model context from that same
history. Creating or loading one session never replaces another session's
history, tools, or cancellation state. Session-scoped settings must not mutate
shared process configuration.

Keep load distinct from a possible future resume-without-replay operation.
After restart, the client can load saved history; in-flight inference, tools,
and pending notifications are not resumed. Advertise load support only with
the create/restart/load path working for the same session ID. Do not add a
bounded replay window or claim recovery of running computations.

Live and replay tool updates share the same constructors. Live text arrives in
deltas; replay emits stored complete segments. Equivalence means accepted
content and terminal tool states, not byte-identical chunking or preservation
of aborted provisional output. Iterate over loaded history for replay instead
of allocating another complete vector of ACP updates when unnecessary.

A successful prompt response means stable data is committed and preceding
updates have been enqueued on the connection before that response. It does not
mean the server observed the client render or acknowledge them. If the
connection fails after commit, later load can replay the saved data.

Keep the SDK's queue policy explicit for this experiment. Do not add another
queue or claim bounded buffering. A demonstrated slow-client problem needs a
change at the real transport queue. Ordinary provider/tool/session errors must
be translated into ACP results; reserve task-level connection errors for the
connection failure boundary.

## Disposition of the organization review

| Recommendation | Decision | Reason |
| --- | --- | --- |
| One crate, concrete runtime, SQLite truth | Accept with runtime change | One concrete Ox loop plus a provider client is sufficient. Rig is not an architectural requirement. |
| Ownership/lifetime boundaries | Accept | Store, operation guard, pending batch, and model request have different owners. |
| Four ACP files | Modify | Keep `acp.rs` plus prompt, operations, and conversion children. A `mod.rs` rename is optional. |
| Separate session domain and SQLite files | Defer | Keep the concrete codec and SQL together until a real boundary earns the split. |
| Concrete path-owned SessionStore | Accept | Makes storage explicit and tests independent of process environment mutation. |
| Many small store query methods | Modify | Read metadata/history together; avoid three connection opens to establish one prompt snapshot. |
| Store returns SessionSummary | Accept | ACP presentation does not belong in SQL row mapping. |
| A new domain ID wrapper | Defer | Keep one ID value and the current small type coupling; no translation table. |
| TranscriptEvent/TranscriptEntry names | Accept when touched | These describe roles; serialized format names remain compatible. |
| ServerState | Accept | Use a concrete struct, with a mutex only around the mutable client slot. |
| ActiveTurn RAII | Broaden | Reserve prompt, load, and delete operations; cancellation belongs only to prompts. |
| is_active as busy decision | Reject for mutation | Checking is not claiming. Use atomic admission. |
| PendingIteration with separate strings | Replace | Own an accepted structured message and outcomes; keep provider metadata and message boundaries. |
| Prompt transaction script | Accept with clarification | Direct model/tool loop and multiple explicit database commits, not one rollbackable transaction. |
| Pure conversion module | Accept with boundary adjustment | ACP input/replay live in convert.rs; model.rs owns pure OpenRouter history encoding alongside stream handling. |
| Share live/replay vocabulary | Accept | Reuse display constructors without storing every token or duplicating logs. |
| Keep Rig-specific adapter/hooks | Reject as target | Use a custom OpenRouter adapter for one completion and direct tool outcomes in Ox. |
| Rename OxAgent and ToolOutcomeStatus | Superseded | Remove the runtime wrapper/status tracker; introduce the concrete ModelClient. |
| Explicit commit boundaries | Accept after correction | Provider completion is not a closed tool batch. |
| Preserve the current event schema unchanged | Modify | Preserve old decoding, add grouped assistant content needed for faithful continuation. |
| No traits, actors, event bus, workspace split | Accept | No second implementation or background workflow earns them now. |
| No ACP-specific agent loop | Clarify | There must be one loop. Owning that loop in the only frontend's integration module is not duplication. |
| Boundary tests | Accept | Exercise stream frames, tool cancellation, restart, and failure ordering, not only enum variants. |
| All refactoring steps behavior-preserving | Reject | Admission policy, partial-output retention, commit timing, and history schema are intentional behavior changes. |
| Stop when navigation is clear | Accept | Do not force a target directory tree or cosmetic rename campaign. |

## Migration sequence and acceptance tests

These are proposed implementation checks, not tests run for this document.

1. **Implement and qualify the OpenRouter adapter.** Use local HTTP/SSE fixtures
   for request encoding, key validation, comments and fragmented frames, text,
   split arguments, two calls in one frame, reasoning metadata through a tool
   round trip, trailing usage with repeated finish reason, malformed arguments,
   HTTP and midstream errors, missing finish reason, and missing `[DONE]`.
   Assert exactly one assembled completion and no execution of partial calls.
   Do not make paid inference calls to test deterministic stream assembly.
2. **Add structured assistant storage and compatibility.** Keep old decoding,
   add the new private DTO, test exact opaque-data round trips, grouped calls,
   atomic assistant/results append, legacy trailing repair, and database reopen.
   Inject a failed append and assert that accepted in-memory history does not
   advance. Reject unsupported content rather than quietly losing it.
3. **Replace the loop in one coherent change.** Introduce ModelClient, direct
   tool dispatch, and explicit settlement; remove Rig's agent path and outcome
   hook. Preserve CLI credential lookup and supply its key explicitly. Do not
   retain a runtime switch or two production implementations.
4. **Make admission cover load/delete.** Check both prompt/load and prompt/delete
   admission orders, load/load exclusion, cross-session independence,
   cancellation isolation, dropped-future release, and failed-spawn release.
   Creating or loading session B must not reset, redirect, or cancel session A.
   Use barriers or controlled futures, not timing sleeps.
5. **Move modules and finish API naming.** Separate mechanical moves from the
   behavioral change. Keep existing codec/SQL tests beside their implementation.
   Run appropriate Rust checks for the implementation, not for this Markdown.

The main end-to-end acceptance sequence is: create a session; request a tool;
cancel after model acceptance but before the tool returns; load the session;
restart with the same database; reconstruct the next model request. There must
be no unresolved call, duplicate result, misplaced reasoning, or interleaved
replay. A notification failure after an observed tool result must not erase that
result from a successfully settled batch. An ordinary provider error must leave
the server able to handle another session.

Use the ID returned by create throughout list, prompt, load after restart, and
delete; verify advertised load support works for Ox-created sessions. Assert
both the replayed ACP content and the next encoded OpenRouter request, since
one can be correct while the other loses history.

A second end-to-end case must carry actual structured provider continuation
metadata through save/reopen/tool-follow-up. Seeing reasoning text in the UI is
not sufficient evidence of a valid reasoning round trip.

## Tradeoffs and explicit non-goals

Owning the loop increases Ox's direct code for budgets, tool dispatch, and
termination. In exchange, those rules are local, testable, and aligned with
persistence. The choice is justified by removing competing lifecycle ownership,
not by assuming a handwritten loop is inherently more reliable.

Closed-batch commits keep stored conversations structurally valid but lose
uncommitted execution history on a crash. Eager tool-intent/result persistence
is a legitimate alternative for real mutating tools. Decide that before shell
execution; it does not require returning loop ownership to a framework.

The new assistant record has more structure than today's flat string events.
That cost is deliberate: model continuation requires more than UI text. Keep
one log with two projections and a small private codec, rather than a raw SDK
serialization contract or independent model/UI transcripts.

No queueing, steering, automatic retry, fallback provider, configurable backend,
permission framework, background session actor, or cross-process execution lease
is part of this change. Add an actor only when independently arriving session
work needs serialized handling. Add a backend abstraction only for a real second
implementation. Add more provider features only when the product needs them.

Owning the OpenRouter adapter also means maintaining request encoding, stream
assembly, error handling, and continuation metadata as the API changes. Keep
those responsibilities in one concrete module with fixtures. This does not
remove ACP backpressure constraints, make tools exactly-once, or repair old
transcripts automatically.

## Evidence and confidence

Ox findings are based on the original source snapshot, pinned in the code links.
Source and dependency files changed concurrently later in this review; those
subsequent implementation changes are outside this assessment. For the existing Rig
ordering, the pinned `rig-agent 0.42.0` file
`src/agent/prompt_request/streaming.rs` documents `CompletionCall` at lines
92–126, emits it in the provider phase at lines 1202–1216, and surfaces tool
results after tool settlement at lines 849–908. ACP enqueue/task-error claims
come from pinned `agent-client-protocol 2.1.0`, `src/jsonrpc.rs:3705–3716`,
`src/jsonrpc.rs:4205–4249`, and `src/jsonrpc/outgoing_actor.rs:9–17`.

The earlier rust-genai inspection used revision
`83119d69311fef53c6c1da2542e655229424538f`; it motivates fixture cases only and is
not a target dependency. The OpenRouter API links above were checked for this
revision of the proposal. They describe the wire contract, not a validated Ox
implementation. This review did not benchmark libraries, compile a migration,
or run a live provider round trip.

The expanded ACP review supplies the comparative findings. Its twelve direct
implementations inform lifecycle design; OpenHands informs split ownership and
UI recovery, and Ante informs wire-schema and replay-window distinctions only.
Neither provides independently verified ACP-server internals. The comparison
projects were not independently re-audited for this document.
