# Ox agent architecture

Status: proposed design. This document specifies the target implementation;
it does not claim that the implementation or its verification is complete.

## 1. Purpose and decisions

Ox is a local conversational agent exposed through the Agent Client Protocol
(ACP). It receives prompts associated with a workspace and session, streams
model output, executes a small set of tools, and stores conversations in
SQLite so that clients can reopen them and the model can continue them.

Ox owns its agent loop. A concrete OpenRouter client performs one model
completion at a time. The loop decides when to call the model, execute tools,
commit conversation history, and terminate the prompt. No external runtime
owns any of those decisions.

The architecture has these central choices:

- One binary and one ACP connection per process.
- One concrete OpenRouter adapter, built over ordinary HTTP and streaming
  transport libraries.
- One authoritative SQLite transcript with separate model and ACP projections.
- One persistent SQLite connection behind a short-held synchronous mutex.
- One operation registry that excludes overlapping prompt, load, and delete
  operations for the same session.
- One prompt task that owns its model request, pending assistant message,
  tool outcomes, cancellation handling, and terminal settlement.
- Complete assistant messages in storage, including the continuation metadata
  needed to send them back to the model.
- Atomic commits of an assistant message together with terminal results for
  every tool call in that message.
- Lazy credential loading into a cached, cloneable model client.
- Concrete functions and closed enums, with a small notification closure for
  testing delivery behavior.

The design prioritizes correct conversation semantics and code that is cheap
to change. It does not aim to be a reusable agent framework, a general model
SDK, or a durable workflow engine.

The OpenRouter API itself is outside the scope of this document. The adapter
must implement the documented API; the sections below specify its ownership,
acceptance boundary, and obligations to the rest of Ox rather than reproducing
request fields or streaming wire formats.

## 2. Scope

### Included

- Text prompts and descriptive resource links.
- Streaming visible answer text and visible reasoning when available.
- Complete model messages that can request multiple tools.
- Sequential execution of the concrete tool set.
- Session creation, listing, loading, prompting, cancellation, and deletion.
- Persistent conversations and restart followed by replay and continuation.
- Terminal login, lazy use of newly saved credentials, and logout.
- Explicit treatment of model limits, malformed input, delivery failure,
  storage failure, and cancellation.

### Excluded

- Multiple providers, runtime-selectable backends, or provider fallback.
- Automatic model retries or automatic tool retries.
- Prompt queues, steering during a running prompt, background wake-ups, or
  independently scheduled session work.
- Multiple processes concurrently modifying the same database.
- Reading databases written by earlier schema versions.
- Durable execution of tools, automatic resumption of interrupted work, and
  exactly-once external effects.
- Mutating tools in the initial implementation.
- Images, audio, other unsupported media, context compaction, and automatic
  truncation of stored history.
- A general permission system, tool registry framework, repository trait,
  event bus, session actor, or dependency-injection framework.

The initial tool remains a simple read-only operation. Adding shell execution
or file editing changes the durability requirements and requires the decision
described in section 15 before those tools become available.

## 3. Assumed invariants

These are assumptions about the supported operating environment and product
scope. They are not guarantees established merely by constructing a Rust
type. If an assumption becomes false, the corresponding design decision must
be revisited rather than patched with an implicit fallback.

| Assumption | What depends on it | When to revisit |
| --- | --- | --- |
| One Ox process is the sole application writer to its database. | Process-local session admission is sufficient. | Two agent processes need to share writable conversation storage. |
| Each process serves one ACP connection. | Connection shutdown ends that process's live work; there is no cross-client routing layer. | A daemon or multiple simultaneous clients become a requirement. |
| SQLite runs on local storage and normal operations are short. | Synchronous operations behind one connection mutex are acceptable initially. | Measurements show storage work delaying streaming or cancellation. |
| Session transcripts fit comfortably in memory. | Load and prompt read a whole committed transcript. | Real conversations require compaction or bounded-memory replay. |
| The initial tools have no persistent external effects. | Losing an uncommitted tool result on a crash is acceptable. | A tool edits a file, starts a subprocess, or changes external state. |
| Tool futures implement their stated cancellation behavior. | The prompt task can stop work before settling its conversation batch. | A new tool owns resources that require explicit asynchronous cleanup. |
| The supported model and endpoint are fixed local choices. | A concrete adapter and nearby constants suffice. | Users need model selection or another actual provider. |
| The model's required continuation data can be represented losslessly as stored JSON values. | Structured metadata survives storage and request reconstruction. | A supported feature requires a different representation or byte-level preservation. |
| The ACP transport preserves enqueue order between updates and responses. | Enqueuing updates before the final response establishes their relative delivery order while the transport remains healthy. | The transport implementation or ordering contract changes. |
| The transport's outgoing queue is not controlled by Ox's prompt loop. | Ox does not claim bounded outbound buffering or client acknowledgement. | A demonstrated slow-client problem requires a transport-level change. |
| A client can recover from a busy response and can reload committed history. | Rejecting conflicting operations is an adequate interaction policy. | A client requires live replay handover or queued prompts. |

No assumption treats provider output, database contents, tool arguments, or
client input as trustworthy. Those are validated at their boundaries.

The system also does not assume that a cancelled request stopped upstream
processing, that dropping a future reversed its effects, or that the client
received an enqueued notification.

## 4. Invariants enforced by the implementation

The following properties are part of Ox's contract and must be supported by
ownership, validation, transactions, or focused tests:

1. A session has at most one active prompt, load, or delete operation in the
   process. Admission is an atomic claim, not a check followed by later work.
2. Each admitted operation has exactly one non-cloneable membership guard.
   Its lifetime includes settlement and response enqueue.
3. Every prompt owns its mutable history, pending batch, model-call count,
   active request, and cancellation state. Another session cannot replace them.
4. The accepted user message is durable before the first model request.
5. Tool execution begins only after the adapter has delivered a validated,
   complete assistant message and the loop has accepted it.
6. A newly committed assistant batch contains exactly one result for every
   call in that assistant message and no result for an unrelated call.
7. Observed tool outcomes enter the pending batch before their notifications
   are attempted. A failed notification cannot erase an observed outcome.
8. Accepted in-memory history advances only after the corresponding database
   transaction succeeds. Pending state is not cleared before successful commit.
9. No database transaction or mutex guard crosses an asynchronous wait.
10. All ordinary exits after user persistence converge on terminal settlement.
    A provider, tool, or notification error cannot bypass it through an early
    return from the loop.
11. Synthetic results describe what Ox knows. They do not claim that a tool
    succeeded, that an interrupted operation had no effects, or that work was
    executed when it was not started.
12. Opaque continuation metadata is stored and reconstructed with its producing
    assistant message. It is never shown as visible reasoning or tool output.
13. Replay and model continuation are projections of the same committed
    transcript. Neither maintains an independent authoritative log.
14. A successful prompt response is enqueued only after required commits and
    preceding update enqueues succeed. It is not proof of client receipt.
15. Ordinary input, provider, tool, and session failures remain request-level
    outcomes. They do not terminate a healthy connection or poison unrelated
    sessions.
16. No recovery path automatically re-executes a tool to reconstruct missing
    history.

## 5. Modules and dependency direction

```text
src/
  main.rs                 command parsing and process entry
  auth.rs                 environment and operating-system credential storage
  model.rs                concrete OpenRouter client; one model completion
  tools.rs                concrete tool schemas, execution, and display titles
  sessions.rs             transcript types, SessionStore, private codec and SQL
  acp.rs                  connection wiring, ServerState, request handlers
  acp/
    operations.rs         per-session admission and cancellation signals
    prompt.rs             model/tool loop and terminal settlement
    convert.rs            ACP input conversion, replay, and update construction
```

`main.rs` chooses a command. It starts the ACP server or runs a credential
command. It does not contain conversational behavior.

`model.rs` knows the provider and the transcript types required to encode a
request. It does not know ACP, SQLite, session admission, or tool execution.

`tools.rs` defines the available tools and executes one complete call. It does
not invoke the model, persist conversation history, or send ACP notifications.

`sessions.rs` owns durable conversation semantics and the concrete store. It
constructs neither ACP notifications nor HTTP requests. SQL and the private
codec stay together with the types they store.

`acp/prompt.rs` owns the only agent loop. Its placement reflects the single
current frontend. If another frontend is implemented, move this same loop
behind the boundary that frontend needs; do not introduce a second loop.

`acp/convert.rs` translates supported ACP input to accepted user text and
projects transcript records into client updates. It does not perform I/O.

The dependency direction is:

```text
main -> acp, auth, model
acp -> auth, model, sessions, operations, prompt, convert
prompt -> model, tools, sessions, operations, convert
model -> sessions (conversation types only), tools (schemas only)
tools -> sessions (tool call and outcome types only)
convert -> sessions, tools (display titles only)
```

The ACP schema's `SessionId` remains the shared identifier type. It has one
unchanged value across create, list, prompt, load, and delete. There is no
separate archive identifier, conversion table, or process-global current
session.

Type and function sketches in this document describe ownership and contracts.
They are not a requirement to copy signatures literally or to add abstractions
solely to match the sketches.

## 6. Process state and credentials

The process state is small and concrete:

```rust
#[derive(Clone)]
struct ServerState {
    store: SessionStore,
    model: Arc<Mutex<Option<ModelClient>>>,
    operations: SessionOperations,
}
```

The server itself is not behind a mutex. Cloning `ServerState` clones the
shared handles; it does not duplicate conversation history or the database.

### Startup

Resolve the data directory, open the database, check its schema version,
apply initialization, and construct the operation registry before accepting
requests. A database that cannot be opened or that carries another schema
version is a startup error. Credentials are not read at startup: the process
must remain available for terminal authentication, listing, and deletion
without them.

The database location is resolved once, using this precedence:

1. `OX_DATA_DIR`, when set to a usable value.
2. `XDG_DATA_HOME/ox`, when that base directory is available.
3. `~/.local/share/ox`.

The database filename is `ox.db`. Failure to resolve a required base directory
is reported explicitly rather than silently selecting the working directory.

### Lazy model client

When an authenticated operation needs the model client:

1. Lock the client slot.
2. Return a clone if a client is already cached.
3. Otherwise resolve credentials, giving a nonempty `OPENROUTER_API_KEY`
   environment value precedence over a saved key.
4. If no key exists, return an authentication-required error and leave the
   slot empty.
5. Construct and cache a concrete client, clone it, and release the lock.

Credential lookup and client construction are synchronous. No network request
occurs while holding the slot lock. This short critical section intentionally
serializes cache initialization and cache clearing. Credential-store errors
are reported; they are not treated as a missing key. After the first
successful lookup, no request touches the credential store.

ACP clients run the terminal login as a separate process and then retry the
original request rather than calling `authenticate`. The empty slot on that
retry is how a newly saved key is picked up without a process restart.

A prompt holds its own clone of the client. The client contains immutable
credentials and a reusable HTTP connection pool, not mutable conversation
state. Each request has its own stream assembly state.

Explicit login validates the key using the provider's non-generation
authentication operation before saving it. A successful key check is not a
guarantee that every model request will succeed.

### Logout and credential changes

Logout removes the saved key and clears the cached client under the same
credential-state serialization policy. Clear the cached client even if removal
reports an error, and report that error accurately: the saved key may still
exist and be loaded on a later request.

Prompts that already own a client clone may finish. Logout is not cancellation
or upstream revocation. If the environment still supplies a key, the next
authenticated operation loads it again; the logout command reports this.

An external change to a saved key does not replace an already cached client.
Hot reload is outside the initial scope. Never log keys, authorization headers,
or raw credential-bearing request objects.

## 7. Conversation model

The transcript represents messages and completed tool exchanges rather than
transport chunks. Its central distinction is between a complete accepted
assistant message and provisional output observed while producing that message.

A representative domain model is:

```rust
enum TranscriptEvent {
    UserMessage(String),
    AssistantMessage(AssistantMessage),
    ToolResult(ToolResult),
}

struct AssistantMessage {
    model: String,
    text: String,
    reasoning: String,
    tool_calls: Vec<ToolCall>,
    reasoning_details: Vec<serde_json::Value>,
}

struct ToolCall {
    call_id: String,
    name: String,
    arguments: String,
}

struct ToolResult {
    call_id: String,
    name: String,
    outcome: ToolOutcome,
}

enum ToolOutcome {
    Completed(String),
    Failed(String),
    Cancelled(String),
}
```

### Message structure

All calls from one assistant completion belong to one assistant message.
Associated text and reasoning stay with that message. Multiple calls do not
become multiple assistant messages, and reasoning does not drift into a later
answer during history reconstruction.

The fields mirror the provider's assistant message: visible answer text,
visible reasoning text, the tool calls, and the opaque continuation details.
Empty text or reasoning means the completion produced none. There is no
interleaving order to preserve. Network arrival order can interleave fragments
of these fields without defining a semantic message order, and the adapter's
assembled result is what the transcript stores.

Visible reasoning and continuation metadata have different purposes. Visible
reasoning supports client presentation. Structured provider details support
model continuation. The encoder must avoid duplicating equivalent reasoning
fields and must use the representation required for the supported model.

The `reasoning_details` field is deliberately concrete and provider-qualified.
Preserve its complete JSON values, nested fields, association, and array order
rather than interpreting opaque elements as display text. JSON formatting and
object key order are not a byte-preservation contract. Unknown fields inside
opaque metadata are retained; unknown transcript formats remain errors.

The producing model is retained so a later configuration change cannot
silently reinterpret incompatible continuation data. Unsupported continuation
is an actionable error, not a reason to drop metadata or invent replacements.

### Tool calls and results

Call IDs must be nonempty and unique within the accepted assistant message.
Each result must match that message's call ID and tool name. Pairing is scoped
to the assistant batch rather than an unbounded global map of call IDs.

The argument string is the complete string provided by the model. Retaining
it permits invalid JSON or an invalid typed argument shape to receive an
ordinary failed tool result after message acceptance. Incomplete transport
assembly is a different problem: it cannot become an executable call.

An empty tool name is a malformed envelope. An unknown nonempty name is a
dispatch failure the model can see and potentially correct on a later call.

Cancellation results contain a short explanation distinguishing a tool that
was never started from one interrupted before its result was observed. Failure
results likewise distinguish validation failure, execution failure, and work
not started because the model-call budget was exhausted.

Results are persisted in original call order even though terminal states may
become known at different times. With sequential execution this ordering is
straightforward.

### What is not a transcript record

Do not persist individual deltas, connection state, admission ownership,
cancellation signals, HTTP payloads, or SDK objects. Usage information is not
required for conversational correctness and does not earn a second log.

Timestamps remain available in storage and session summaries. Transcript reads
return conversation records without a timestamp wrapper unless a real
consumer needs per-record timestamps.

## 8. Store and database contract

```rust
#[derive(Clone)]
struct SessionStore(Arc<Mutex<rusqlite::Connection>>);

struct SessionSummary {
    id: SessionId,
    cwd: PathBuf,
    title: Option<String>,
    created_at: String,
    updated_at: String,
}

struct StoredSession {
    summary: SessionSummary,
    transcript: Vec<TranscriptEvent>,
}
```

The store opens one connection and initializes schema and connection settings
once. Production and tests use the same methods; tests can construct an
in-memory connection. There are no duplicate production and test query APIs.

Every store operation acquires the connection mutex only for its synchronous
database work. All reads as well as writes serialize on this connection.
Model requests, tool awaits, replay notifications, and credential lookup happen
outside that mutex. Code must not recursively acquire the store lock.

### Public operations

| Operation | Contract |
| --- | --- |
| `create(cwd)` | Generate a session ID and create its workspace association and metadata atomically. Return the new summary. |
| `read(id)` | Return `None` for absence; otherwise return metadata and ordered transcript from one read transaction. |
| `list(cwd)` | Return owned summaries, optionally filtered by workspace, ordered by descending activity with a stable ID tie-breaker. |
| `append_user(id, text)` | Append the accepted input, adopt the first usable title when absent, and update activity in one transaction. Return the resulting summary. |
| `append_batch(id, batch)` | Validate a closed assistant batch, append all its records, and update activity atomically. |
| `delete(id)` | Remove the session and its dependent records atomically; absence is an idempotent success. |

The prompt loop has no unrestricted event append. `read` distinguishes an
absent session from an existing untitled session. `append_user` and
`append_batch` fail for an absent session; they never create one implicitly.
Empty batch writes are not used to change activity.

### Logical schema

The database contains workspace rows, session rows, and an ordered events
table. Session rows contain the session ID, workspace association, optional
title, and creation and activity timestamps. Event rows contain an ordered
integer ID, session ID, timestamp, kind, and serialized payload.

Foreign keys are enabled. Deleting a session cascades to its events. Event
ordering uses the integer sequence, never timestamps. UTC timestamps use one
consistent representation; equal timestamps do not imply equal events.

The first accepted nonblank user input supplies an initial title, limited to
80 Unicode scalar values. Later appends do not overwrite an established title.
Title derivation does not modify the input sent to the model.

Workspace identity is the supplied absolute path compared using one consistent
path policy across create, list filtering, and load. Initially use exact path
equality without resolving symlink aliases. Reject relative workspace paths
where an absolute workspace is required. Do not silently merge two workspace
identities that happen to refer to the same directory.

### Schema version, codec, and transactions

The database records its schema version in `PRAGMA user_version`. A new
database is stamped with the current version when the schema is created.
Opening a database whose version differs from the current one is a startup
error that names the file so the user can delete it. There is no migration
path and no reader for earlier formats.

Private database DTOs are distinct from both domain structs and HTTP DTOs. A
provider adapter change must not silently change the meaning of stored
records.

A batch transaction inserts the assistant message and all paired results,
then updates session activity. If any step fails, the transaction rolls back.
The caller retains its pending batch and does not advance accepted history.

Read validation rejects malformed payloads, unknown kinds, orphan results,
duplicate results, and unresolved calls. Do not skip unreadable rows or
substitute empty history.

A committed batch is immutable conversation history. There is no automatic
rewrite to make a provider accept a previously stored message.

### Limitations

The database mutex is not session admission. It serializes individual database
operations, not a whole conversation turn. Another process is not prevented
from opening the file by the Rust mutex; single-writer process ownership is an
operating assumption.

The mutex and SQLite calls may block executor workers. This is accepted for
the initial local workload without promising a latency bound. If measurements
justify it, introduce a blocking-executor boundary around this concrete store;
do not preemptively add an actor, pool, or repository abstraction.

## 9. Session admission and cancellation ownership

```rust
enum Operation {
    Prompt(PromptCancellation),
    Load,
    Delete,
}

#[derive(Clone, Default)]
struct SessionOperations(Arc<Mutex<HashMap<SessionId, Operation>>>);

struct OperationGuard {
    operations: SessionOperations,
    session_id: SessionId,
}
```

`try_prompt`, `try_load`, and `try_delete` use one atomic insertion helper.
Failure to claim returns busy immediately. There is no wait queue and no
check-then-delete API.

| Incoming operation | Idle | Prompt owns session | Load owns session | Delete owns session |
| --- | --- | --- | --- | --- |
| Prompt | Claim | Busy | Busy | Busy |
| Load | Claim | Busy | Busy | Busy |
| Delete | Claim | Busy | Busy | Busy |
| Cancel | No-op | Signal prompt | No-op | No-op |
| List | Read committed summaries | Read committed summaries | Read committed summaries | Read committed summaries |

Session creation chooses a new ID and does not replace existing live state.

The membership guard is private and non-cloneable. Cancellation handles can
be cloned without duplicating ownership. `Drop` removes the occupied entry
synchronously; it performs no persistence, notification, or asynchronous
cleanup.

The prompt handler claims admission before spawning. It moves the guard into
the task. Failure to spawn must drop the future and its guard. The guard stays
alive through the response enqueue attempt, including any settlement after an
execution or delivery failure.

Load holds its claim through read, validation, replay, and response enqueue.
Delete holds its claim through the database operation and response enqueue.
Neither holds the registry mutex while doing that work.

Cancellation is a latched signal belonging to the admitted prompt. It remains
observable if it arrives before the task begins polling. Repeated cancellation
is harmless. A cancellation received while idle does not cancel a later prompt.

Only short synchronous map access occurs under the registry mutex. The guard
never holds that mutex for its entire lifetime.

## 10. Model adapter boundary

`ModelClient` performs one model request. Its inputs are accepted conversation
history, the fixed model choice, and the concrete tool schemas. Its outputs
are provisional display deltas followed by at most one accepted completion,
or a request failure.

A conceptual event set is:

```rust
enum ModelEvent {
    TextDelta(String),
    ReasoningDelta(String),
    Completed(ModelCompletion),
}

struct ModelCompletion {
    message: AssistantMessage,
    stop: ModelStop,
}

enum ModelStop {
    Finished,
    ToolCalls,
    TokenLimit,
    Refused,
}
```

Keep wire-specific response codes, DTOs, and assembly details private. The
adapter translates supported provider termination states into this closed
set. An unknown or contradictory response is a clear adapter error, not an
implicit normal completion.

The adapter is responsible for:

- Encoding accepted history without losing message grouping or continuation
  data.
- Assembling a complete response according to the documented streaming
  protocol, including terminal validation and supported metadata.
- Keeping all mutable assembly state local to the request.
- Emitting exactly one completion only after the response meets the adapter's
  acceptance contract.
- Distinguishing provisional text from accepted message content.
- Reporting malformed responses and transport failures without executing
  tools or changing stored history.
- Stopping consumption when the owning request is cancelled or dropped.

The completion event means the adapter has finished the protocol work needed
to accept that response. No later stream event can revise its accepted content.
End-of-stream without that event is an error.

Visible deltas are not authoritative history, even when their concatenation
looks complete. The final assembled message is authoritative for acceptance.
Opaque data never travels through a visible reasoning delta.

The initial UI does not display partial tool-call arguments. A tool starts
appearing in the UI after model acceptance, as a pending tool call, when Ox
has a complete call to represent. This avoids provisional tool objects that
need repair if model assembly fails.

Use a normal HTTP client and streaming framing library. Do not implement a
general networking stack, background stream reader, or second event queue.
Fixture tests can supply a local endpoint without introducing user-facing
backend configuration. The endpoint, model identifier, and tuning limits are
nearby implementation constants.

## 11. Prompt execution

### Admission and preparation

The handler converts and validates input before starting execution. It accepts
text blocks and resource-link descriptions; a resource link contributes its
name and URI as text and is not fetched automatically. Unsupported content
causes an invalid-parameters error before any part of the prompt is persisted.
The converted input must contain nonblank text.

After obtaining credentials and claiming session admission, the handler spawns
one prompt task. That task:

1. Reads the session summary and committed transcript.
2. Reconstructs the accepted model conversation.
3. Appends the user message and title/activity update in one transaction.
4. Adds that same user message to its owned accepted history.
5. Enqueues the session metadata update. The update carries the title only
   when the summary read in step 1 had none and the append adopted one.
6. Enters the model/tool loop.

No model request occurs before step 3 succeeds. Preparation failures before
that point require an error response and guard release but no batch settlement.
After step 3, every ordinary exit goes through settlement, even when there is
no accepted assistant batch to commit.

If cancellation is already observed before user append begins, return
cancelled without writing that input. Once user append succeeds, the input
remains saved even if cancellation immediately prevents inference. A
synchronous transaction is not interrupted halfway through to implement this
distinction.

### Owned prompt state

Each prompt owns:

- The operation guard and its cancellation observer.
- A cloned model client.
- Accepted history loaded from the store and extended after successful writes.
- A model-call counter.
- At most one active model request.
- At most one pending accepted assistant batch.
- The terminal execution outcome and any settlement failure.

`PendingBatch` contains one accepted assistant message and one result slot per
tool call. The slots begin empty and can be filled once. Iterating them in
message order constructs the closed batch. This state needs neither a global
outcome map nor a tool callback that runs before some other callback.

The pending batch is distinct from accepted history: the model response has
been accepted from the provider, but it is not durable conversational context
until its batch is closed and committed.

### Loop

```text
while another model call is allowed:
    check cancellation
    start one request using accepted history; increment model-call count
    forward supported provisional display deltas
    require one validated completion
    capture its assistant message in a pending batch
    announce every call in that message as pending

    classify its stop condition
    if tool calls are allowed:
        visit calls in order
        check cancellation before starting each call
        mark the call in progress, then execute it or record its validation failure
        retain the terminal result before attempting its update
    otherwise:
        close any accepted calls that will not be executed

    close and commit the batch
    extend accepted history only after commit succeeds
    clear the pending batch

    if the stop condition ends the prompt:
        finalize the corresponding response
        stop

settle any interrupted accepted batch and finalize the terminal outcome
```

The implementation may use early returns inside helpers, but the prompt's
execution driver catches their ordinary errors and passes through its common
settlement block. Notification helpers do not escape this structure through
an unhandled `?`.

### Model-call budget

Use one nearby `MAX_MODEL_CALLS` constant, initially 8. Every started model
request consumes one call. There are no automatic retries that bypass the
counter.

A normal final answer on the last allowed call succeeds. If that completion
requests tools, do not start tool work when no follow-up model call remains.
Record explicit failed results stating that those calls were not started
because the request budget was exhausted, commit the closed batch, and return
the ACP model-request-limit stop.

This is a product policy: tools are used as part of a model exchange that Ox
can continue, rather than started after the final opportunity to interpret
their results. It does not impose a duration limit on an individual request.

### Completion classification

- `Finished`: commit a valid message with no outstanding calls and finish.
- `ToolCalls`: require at least one complete call, execute or settle its batch,
  then request the next completion if allowed.
- `TokenLimit`: commit an accepted, structurally valid text response and return
  the token-limit stop. Never execute an incomplete or truncated call.
- `Refused`: commit any accepted displayable refusal response and return the
  refusal stop.
- Unknown termination or contradictory message structure: fail acceptance and
  enter settlement without inventing a valid assistant message.

A token-limited answer is distinct from a transport-interrupted answer: the
former has a valid terminal response from the provider. Unsupported tool-bearing
limit or refusal responses are adapter errors until their semantics are
explicitly supported; they do not trigger opportunistic execution.

## 12. Tools and cancellation

`tools::execute` takes a complete tool call, the session's workspace context,
and a cancellation observer when needed. It parses arguments into the owned
input type for the named tool and returns a concrete `ToolOutcome`.

Dispatch is a small exhaustive name match over the implemented tools. Schemas,
argument types, display titles, and execution stay together. No plugin registry
or general tool trait is needed for this set.

Unknown tool names, invalid argument JSON, and invalid typed arguments produce
failed results associated with the original call ID. A tool's ordinary runtime
failure also becomes a failed result. The model may respond to that result on
the next allowed call. Broken internal invariants remain programming bugs.

Execute calls sequentially. Successful and failed calls both consume their
position in the batch; a failure does not erase prior outcomes. Ordinary tool
failure does not stop later calls unless the prompt is also cancelled or a
delivery/storage condition requires stopping new execution.

After the loop accepts a complete assistant message, tool handling follows
this sequence:

1. Enqueue every tool call in the message as a pending tool-call object, in
   call order.
2. Check cancellation and the model-call budget.
3. Execute eligible calls sequentially. Before each call, check cancellation,
   then move the call to in progress. While it runs, observe cancellation
   according to that tool's resource lifecycle. As soon as a result is
   obtained, record it in the pending batch before sending its terminal
   update or checking whether later work should begin.
4. Settle every unexecuted call directly from pending to a terminal state.

Announcement finishes before execution or settlement begins, so the client
holds an object for every call and settlement only ever sends updates. Ox
tracks no per-call announcement state. If enqueueing fails partway through
step 1, execute nothing, stop attempting client updates, and settle the entire
accepted batch to storage. The connection has failed; a later load supplies
the committed history.

The loop must not classify a result it has already obtained as cancelled just
because cancellation arrives before its notification. An outcome not yet
observed by Ox cannot be claimed as known success. Cancellation races are
resolved at these explicit observation boundaries.

For the initial tool, dropping its work is sufficient to stop local execution.
This rule does not extend automatically to subprocesses or other resources.
A future process-owning tool must terminate and reap its process before
claiming it has stopped. Do not detach cleanup and release the session while
the tool is still changing its workspace.

Cancellation during a model request stops Ox's consumption and prevents new
tool execution. It does not establish whether upstream processing or billing
stopped. Cancellation during tool execution preserves observed results and
fills all remaining slots with accurate terminal outcomes.

Once final settlement and commit begin, cancellation does not interrupt the
commit or rewrite the already selected terminal outcome. A late cancellation
may therefore race with normal completion and receive the normal completed
response. It never applies to a subsequent prompt.

## 13. Settlement and response semantics

The driver uses a small private outcome enum for normal completion,
cancellation, model-request limit, token limit, refusal, provider failure,
delivery failure, and storage failure. These distinctions exist because their
settlement behavior differs; they do not require a public error hierarchy.

Settlement executes in this order:

1. Stop starting new model requests and tools.
2. Stop or clean up the current owned operation as required.
3. Preserve every tool outcome already observed.
4. If an accepted batch exists, fill missing result slots with explicit
   cancelled, failed, or not-started outcomes appropriate to the exit.
5. Commit that closed batch, unless a storage failure has already made this
   append unsuccessful. Do not automatically retry failed writes.
6. Extend accepted history and clear pending state only after commit succeeds.
7. Enqueue terminal updates for calls settled without execution, unless
   delivery has already failed. Every call was announced as pending after
   acceptance, so settlement sends updates only, never a first announcement.
8. Enqueue the final response or request error.
9. Release admission by dropping the guard.

Normal live tool updates may have been emitted earlier; settlement does not
need to re-emit terminal updates that were already enqueued successfully.

| Exit | Persistent effect | Client result |
| --- | --- | --- |
| Normal final answer | Commit accepted final batch. | Successful end-of-turn response. |
| Ordinary tool failure | Commit failed result with its assistant batch; continue within budget. | Failed tool update, followed by the model's subsequent response. |
| Model-call budget reached | Close accepted unexecuted calls with budget failures and commit. | Failed updates for the pending calls, then the model-request-limit stop. |
| Accepted token-limited answer | Commit the valid accepted message. | Token-limit stop. |
| Accepted refusal | Commit the supported accepted message when present. | Refusal stop. |
| Cancellation before model acceptance | Retain the user and prior committed batches; discard provisional output. | Cancelled response. |
| Cancellation after acceptance | Preserve observed results; close remaining calls; commit. | Terminal tool updates where possible, then cancelled response. |
| Provider failure before acceptance | Retain the user and prior committed batches; discard provisional output. | Request error. |
| Delivery failure | Stop new execution; settle any accepted batch independently of delivery. | Propagate connection failure after local settlement; receipt is not guaranteed. |
| Storage failure | Failed transaction contributes no accepted history; retain earlier commits. | Persistence error, with the original exit retained as context where useful. |
| Unexpected task drop or process termination | Only previously committed data is guaranteed. | No guaranteed terminal response. |

A tool has no special persistence authority: a successful tool result can
still be lost if its batch cannot be committed. Report that storage failure
without rerunning the tool or claiming its effects were rolled back.

An error response does not imply a successful final commit or successful prior
delivery. Only the successful response contract asserts the required write
and enqueue ordering. If the connection fails after commit, a subsequent load
can recover the committed conversation.

Do not mask a storage error with the cancellation or provider failure that
caused settlement. Preserve the original cause as diagnostic context, but make
the failed persistence clear to the client whenever delivery remains possible.

### Provisional output

Text and visible reasoning may reach the client before the model response is
accepted. If that response fails or is cancelled, those provisional chunks
are not stored as an ordinary assistant message. They can disappear on reload.

This behavior is deliberate and must be understood as a UI limitation. Ox
does not claim live output and replay are byte-for-byte identical. If durable
interrupted output becomes a product requirement, add an explicit interrupted
record with defined replay and model-context treatment. Do not silently
promote incomplete output to a completed answer.

A saved user message with no assistant reply means the input was accepted and
saved. It is not an instruction to resume inference automatically after restart.

## 14. ACP behavior and projections

### Session operations

Creation requires credentials, validates the workspace path, creates a durable
session, and returns its ID. Loading and prompting also require credentials.
Listing and deletion do not need provider access. Cancellation is always
available and is a no-op when no prompt owns the session.

Load claims the session, reads and validates committed history, verifies the
requested workspace matches the stored workspace, and replays the transcript.
Workspace validation precedes replay. The claim remains held until the load
response is enqueued.

Prompt looks up the session's stored workspace for tool context; it never
uses a process-global current directory as a substitute. Unknown session IDs
return a resource-not-found error.

List returns committed summaries without waiting for a session operation to
finish. A running session's title or activity can therefore reflect its most
recent completed write. The initial listing is unpaginated and returns no
continuation cursor. Unsupported nonempty cursor input is rejected rather than
silently returning the wrong page.

Delete requires its session claim and atomically removes the stored session
and transcript. Deleting an already absent session succeeds. It does not
cancel an active prompt; it returns busy.

### Live updates and replay

Use shared constructors for visible text, reasoning, pending tool calls, and
in-progress and terminal tool states. Live text uses deltas; replay uses
stored complete segments.
An assistant message projects to several updates (reasoning, text, and one
per tool call), so the conversion API must not assume one transcript record
equals one ACP update.

Replay emits only displayable content and tool states. Opaque continuation
data is intentionally omitted. Each tool call replays as one complete
tool-call object already in its terminal state: completed tools as completed,
failed and cancelled tools as the protocol's available terminal failure state
with an accurate explanatory message.

Iterate through the loaded transcript during replay rather than building a
second full vector of notifications. A delivery error ends replay and releases
the claim after the response attempt. Already committed history is unchanged.

The initial schema does not record standalone stop reasons. Replay recovers
content and terminal tool states, not an exact reproduction of historical
prompt responses or network timing.

After restart, load restores committed conversation and later prompts rebuild
model context from that same source. It does not resume an in-flight model
request, running tool, or undelivered notification.

### Delivery and connection lifecycle

Sending an update means enqueueing it on the connection. The final response
is enqueued after the updates and commits required by its outcome. There is
no acknowledgement that the client received or rendered those updates.

Ox adds no intermediate event queue and makes no bounded-buffering claim.
Should slow-client memory growth become an observed problem, solve it where
the outgoing transport queue is owned.

Connection shutdown should signal active prompts and allow ordinary settlement
while the task executor remains available. Unexpected task abortion, process
exit, or a shutdown deadline can prevent asynchronous settlement; only earlier
commits are guaranteed in those cases. A drop guard promises admission cleanup
when dropped, not durable cleanup on every termination path.

Provider and input failures become request errors on a healthy connection.
Only actual connection failure escapes at the connection task boundary.

## 15. Durability boundary and future mutating tools

Closed-batch commits establish conversation consistency. They do not establish
durable execution of external actions.

Consider this sequence:

```text
accept assistant message containing a tool call
execute tool
observe result
process terminates before the batch commit
```

After restart, the database contains the earlier committed history and user
input but may contain neither that assistant message nor the tool result.
With a read-only tool this loses an observation. With a mutating tool it can
lose the record of a real external change.

The initial implementation accepts that window only within the read-only tool
scope. It never infers from missing history that the tool had no effects and
never automatically retries the prompt or tool after restart.

Before introducing a mutating tool, specify at least:

- What execution intent is committed before starting the effect.
- How an observed result becomes durable independently of a later model call.
- How load represents an operation with intent but no known outcome.
- Which tool-specific resources must be stopped or inspected during recovery.
- Whether any operation is safe to retry and what concrete evidence establishes
  that safety.
- How unresolved execution records are excluded from normal model continuation
  until they have an explicit terminal or uncertain outcome.

Persisting intent and results at finer boundaries is a likely direction, but
this document does not prebuild an execution journal or promise exactly-once
semantics. A local database transaction cannot atomically commit an arbitrary
external effect. Even a future execution journal must represent uncertainty
honestly.

This is a scope boundary, not an invitation to add generic workflow machinery
before any mutating tool exists.

## 16. Errors and diagnostics

Use existing I/O and orchestration error conventions for storage, credentials,
and connection work. Convert errors at the ACP boundary into the appropriate
protocol result. Introduce a custom error type only where code must actually
recover differently by variant.

| Condition | Treatment |
| --- | --- |
| Unsupported or blank prompt content | Invalid parameters; no user append. |
| Missing credentials | Authentication required; keep process available. |
| Credential-store failure | Actionable request or command error. |
| Database from another schema version | Startup error naming the file. |
| Unknown session | Resource not found. |
| Conflicting session operation | Busy; do not enqueue work. |
| Wrong workspace on load | Explicit mismatch error before replay. |
| Malformed provider response | Request failure through settlement. |
| Invalid tool arguments or ordinary tool failure | Failed tool result. |
| Invalid stored transcript | Actionable storage/format error; do not skip data. |
| Failed transaction | Persistence error; no advancement of accepted history. |
| Broken internal invariant | Loud programming failure through `expect` or panic. |

Diagnostics should identify the session, operation phase, and relevant call ID
when useful. Avoid raw transcripts, opaque reasoning metadata, credentials,
and full tool arguments in routine logs. A short error summary usually provides
enough context without duplicating sensitive conversation data.

No fallback path silently substitutes empty history, a made-up call ID, a
successful tool result, or another provider.

## 17. Verification strategy

Tests establish behavior at the boundaries the design exists to get right:
the provider wire, the persisted transcript and its two projections, session
admission, and delivery. They do not freeze private helper organization, and
the list below is the whole initial suite. Add cases when a failure earns
them.

### Adapter fixtures

Use a local HTTP server and deterministic stream fixtures. Verify request
encoding (message grouping, tool schemas, continuation metadata sent back
without duplicating visible reasoning), stream assembly (text, reasoning,
several calls, fragmented call arguments), stop classification, a malformed
or incomplete stream reported as an error, and cancellation mid-stream.
Assert that partial calls never execute and that completion is accepted at
most once. Test credential validation separately from inference. No test
makes a paid model request.

### Store

Use an in-memory connection for ordinary operations and one temporary
database file for the reopen test. Verify:

- A batch with several tool calls, continuation metadata, and paired results
  survives write, close, reopen with a new store, and read as identical
  history. From that reopened transcript, both the ACP replay updates and the
  next encoded model request are correct: no unresolved call, duplicate
  result, or misplaced reasoning.
- Orphan, duplicate, and missing results fail batch validation explicitly.
- User append adopts a title once and updates activity.
- Deletion removes the session's records and repeated deletion succeeds.
- A database with another `user_version` is rejected at open.

### Loop

Pass a small `FnMut(SessionUpdate) -> Result<()>` closure to the prompt driver
for update delivery. Production wraps the connection; tests collect updates
or fail a chosen enqueue. Drive the real loop against the fixture adapter.
Cases:

- Normal text answer, with accepted history advanced after commit.
- Several tool calls in one assistant message with ordered terminal results.
- Cancellation during a tool, preserving the result already observed and
  closing the rest as cancelled.
- Failed batch append, leaving accepted in-memory history unchanged.
- Delivery failure after an observed tool result, with that result still
  committed in the settled batch.
- Final allowed model call requesting tools, with no tool executed and the
  explicit budget failures persisted.

### Admission

One table-driven test over the conflict matrix in section 9: prompt, load,
and delete arriving while the session is idle, prompting, loading, or
deleting, plus release of the claim when the guard drops. Cancelling session
B does not signal session A's prompt.

## 18. Implementation sequence

1. Define the structured conversation records and closed-batch validation.
   Implement the persistent store with its schema version check and the
   storage tests.
2. Implement the concrete one-completion adapter and qualify its boundary with
   local fixtures. Keep it independent of ACP, persistence, and tool execution.
3. Implement atomic session admission and the unique operation guard.
4. Implement direct tool dispatch, the owned prompt loop, and common settlement.
   Connect the adapter, store, and delivery closure without introducing a
   general runtime abstraction.
5. Wire ACP handlers, replay conversion, lazy credential loading, and the
   successful response ordering contract.
6. Switch the production path to this loop and remove the superseded execution
   path and dependencies. Do not retain two selectable production runtimes.
7. Run the boundary tests and the reopen test, then the Rust build and test
   checks.

Mechanical module moves should remain distinguishable from behavior changes.
The implementation need not reach a predetermined file count if a smaller
arrangement retains the same ownership boundaries. Do not add a compatibility
framework, runtime toggle, or fallback merely to make incremental construction
possible.

## 19. Tradeoffs and extension triggers

| Decision | Accepted cost | Trigger for change |
| --- | --- | --- |
| Ox owns the loop and provider adapter. | Ox maintains its request boundary, tool dispatch, and termination behavior. | A concrete unmet provider feature or measured maintenance problem. |
| One persistent database connection. | Reads and writes serialize; synchronous calls can block an executor worker. | Measured contention or latency. |
| Whole-history reads. | Memory and request size grow with conversations. | Actual context-limit or memory pressure, addressed with explicit compaction semantics. |
| No schema migration. | A schema change requires deleting the local database. | Conversations become valuable enough to carry across schema versions. |
| Busy instead of queueing. | Clients must retry conflicting operations. | A real requirement for queued or independently arriving work. |
| Sequential tools. | Independent calls cannot overlap. | A measured latency benefit sufficient to justify concurrency and cancellation complexity. |
| Closed-batch commits. | Uncommitted execution observations can be lost on termination. | Mutating tools; execution durability must be decided before enabling them. |
| Provisional output is discarded on interruption. | Some live text disappears on reload. | A product requirement for durable interrupted output. |
| Concrete provider-qualified continuation metadata. | Storage understands one provider's continuation concept. | A real second provider, addressed without losing existing data semantics. |
| No independent session actor. | The prompt task handles only work initiated by its prompt. | Background work that must enter a session independently. |
| No automatic retry. | Transient failures are visible to the caller. | A specific retryable operation with clear budget, cancellation, and effect semantics. |

The architecture should grow by making these boundaries explicit when their
assumptions change. Its initial form remains one concrete model client, one
owned loop, one store, and one authoritative conversation.
