# Ox agent architecture

Status: proposed design. This document specifies the target implementation; it
does not claim that the implementation or its verification is complete.

## 1. Purpose and decisions

Ox is a local conversational agent exposed through the Agent Client Protocol
(ACP). It receives prompts associated with a workspace and session, streams
model output, executes a small set of tools, and stores conversations in SQLite
so that clients can reopen them and the model can continue them.

Ox owns its prompt run. A concrete OpenRouter client performs one model request
at a time. The run decides when to make model requests, run tools, save the
transcript, and respond. No external runtime owns any of those decisions.

The architecture has these central choices:

- One binary and one ACP connection per process.
- One concrete OpenRouter adapter, built over ordinary HTTP and streaming
  transport libraries.
- One authoritative SQLite transcript with separate model and ACP projections.
- One persistent SQLite connection behind a short-held synchronous mutex.
- One operation-guard registry that allows at most one prompt, load, or delete
  per session.
- One prompt run that saves the user message, makes model requests, runs tools,
  saves complete assistant batches, and responds.
- Complete assistant messages in storage, including the continuation metadata
  needed to send them back to the model.
- Assistant batches saved atomically: one validated message with final results
  for every tool call in that message.
- Lazy credential loading into a cached, cloneable OpenRouter client.
- Concrete functions and closed enums, with a small update closure for testing
  update sending.

The design prioritizes correct conversation semantics and code that is cheap to
change.

The OpenRouter API itself is outside the scope of this document. The adapter
must implement the documented API; the sections below specify its
responsibilities, validation boundary, and obligations to the rest of Ox rather
than reproducing request fields or streaming wire formats.

## 2. Scope

### Included

- Text prompts and descriptive resource links.
- Streaming visible answer text and visible reasoning when available.
- Complete model messages that can request multiple tools.
- Sequential execution of the concrete tool set.
- Local workspace editing through the apply-patch tool.
- Session creation, listing, loading, prompting, cancellation, and deletion.
- Persistent conversations and restart followed by replay and continuation.
- Terminal login, lazy use of newly saved credentials, and logout.
- Explicit treatment of model limits, malformed input, ACP-update failure,
  storage failure, and cancellation.

### Excluded

- Multiple providers, runtime-selectable backends, or provider fallback.
- Automatic model retries or automatic tool retries.
- Prompt queues, steering during a running prompt, background wake-ups, or
  independently scheduled session work.
- Multiple processes concurrently modifying the same database.
- Reading databases written by an earlier schema.
- Images, audio, other unsupported media, context compaction, and automatic
  truncation of the stored transcript.
- A general permission system, tool registry framework, repository trait, event
  bus, session actor, or dependency-injection framework.

Tool execution and transcript persistence follow the persistence contract in
section 15.

## 3. Assumed invariants

These are assumptions about the supported operating environment and product
scope. They are not guarantees established merely by constructing a Rust type.
If an assumption becomes false, the corresponding design decision must be
revisited rather than patched with an implicit fallback.

| Assumption                                                                                             | What depends on it                                                                                                               | When to revisit                                                                     |
| ------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| One Ox process is the sole application writer to its database.                                         | Process-local operation guards are sufficient.                                                                                   | Two agent processes need to share writable conversation storage.                    |
| Each process serves one ACP connection.                                                                | Connection shutdown ends that process's live work; there is no cross-client routing layer.                                       | A daemon or multiple simultaneous clients become a requirement.                     |
| SQLite runs on local storage and normal operations are short.                                          | Synchronous operations behind one connection mutex are acceptable initially.                                                     | Measurements show storage work delaying streaming or cancellation.                  |
| Session transcripts fit comfortably in memory.                                                         | Load and prompt read a whole saved transcript.                                                                                   | Real conversations require compaction or bounded-memory replay.                     |
| Tool execution is owned by the prompt run that validated the call.                                     | The run keeps resource-specific work within its cancellation boundary and adds its observed outcome to the uncommitted batch.    | Work must continue independently of the prompt run that started it.                 |
| Tool execution owns its cleanup through the returned outcome.                                          | The prompt awaits shell process-group termination, shell reaping, and bounded output draining before saving its assistant batch. | A tool needs work to survive its prompt run.                                        |
| The default model and the endpoint are fixed local choices; each session stores its model at creation. | A concrete adapter, nearby constants, and one transcript entry suffice.                                                          | Users need to choose or change a session's model, or another actual provider.       |
| The model's required continuation data can be represented losslessly as stored JSON values.            | Structured metadata survives storage and request reconstruction.                                                                 | A supported feature requires a different representation or byte-level preservation. |
| The ACP transport preserves enqueue order between updates and responses.                               | Enqueuing updates before the final response establishes their relative send order while the transport remains healthy.           | The transport implementation or ordering contract changes.                          |
| The transport's outgoing queue is not controlled by Ox's prompt loop.                                  | Ox does not claim bounded outbound buffering or client acknowledgement.                                                          | A demonstrated slow-client problem requires a transport-level change.               |
| A client can recover from a busy response and can reload the saved transcript.                         | Rejecting conflicting operations is an adequate interaction policy.                                                              | A client requires live replay handover or queued prompts.                           |

No assumption treats provider output, database contents, tool arguments, or
client input as trustworthy. Those are validated at their boundaries.

The system also does not assume that a cancelled request stopped upstream
processing, that dropping a future reversed its effects, or that the client
received an enqueued notification.

## 4. Invariants enforced by the implementation

The following properties are part of Ox's contract and must be supported by
ownership, validation, transactions, or focused tests:

1. A session has at most one active prompt, load, or delete operation in the
   process. Acquiring its operation guard is atomic, not a check followed by
   later work.
2. Each active operation holds exactly one non-cloneable operation guard. Its
   lifetime includes the finish path and sending the response.
3. Every prompt run owns its saved transcript, uncommitted batch, active
   completion stream, and cancellation signal. Another session cannot replace
   them.
4. The saved user message is durable before the first model request.
5. Tool execution begins only after the completion stream has yielded a
   validated, complete assistant message.
6. A newly saved assistant batch contains exactly one final result for every
   call in that assistant message and no result for an unrelated call.
7. Observed tool outcomes enter the uncommitted batch before their ACP updates
   are sent. A failed update cannot erase an observed outcome.
8. The saved in-memory transcript advances only after the corresponding database
   transaction succeeds. The uncommitted batch is not cleared before that save.
9. No database transaction or mutex guard crosses an asynchronous wait.
10. All ordinary exits after user persistence converge on the finish path. An
    OpenRouter, tool, or ACP-update error cannot bypass it through an early
    return from the loop.
11. Synthetic results describe what Ox knows. They do not claim that a tool
    succeeded, that an interrupted operation had no effects, or that work was
    executed when it was not started.
12. Opaque continuation metadata is stored and reconstructed with its producing
    assistant message. It is never shown as visible reasoning or tool output.
13. Replay and model continuation are projections of the same saved transcript.
    Neither maintains an independent authoritative log.
14. A successful prompt response is sent only after required saves and preceding
    updates succeed. It is not proof of client receipt.
15. Ordinary input, OpenRouter, tool, and session failures remain request-level
    outcomes. They do not terminate a healthy connection or poison unrelated
    sessions.
16. Session recovery reconstructs model context and replay exclusively from
    saved transcript entries.

## 5. Modules and dependency direction

```text
src/
  main.rs                 command parsing and process entry
  auth.rs                 environment and operating-system credential storage
  openrouter.rs           OpenRouter client, request encoding, and streamed-response assembly
  tools.rs                concrete tool schemas, tool call titles, and execution of one complete call
  sessions.rs             transcript types and their stored encoding, assistant-batch validation, SessionStore, and SQL
  acp.rs                  connection wiring, ServerState, request handlers
  acp/
    operations.rs         one active prompt, load, or delete per session, via an operation guard
    prompt.rs             one prompt run: model requests, tools, and the final response
    convert.rs            ACP input conversion, replay, and update construction
```

`main.rs` chooses a command. It starts the ACP server or runs a credential
command. It does not contain conversational behavior.

`openrouter.rs` knows OpenRouter and the transcript types required to encode a
request. It does not know ACP, SQLite, operation guards, or tool execution.

`tools.rs` defines the available tools and executes one complete call. It does
not make model requests, save the transcript, or send ACP updates.

`sessions.rs` owns durable transcript semantics and the concrete store. It
constructs neither ACP updates nor HTTP requests. The transcript types derive
their stored JSON encoding, and the SQL stays with the store.

`acp/prompt.rs` owns the only prompt run. Its placement reflects the single
current frontend. If another frontend is implemented, move this same run behind
the boundary that frontend needs; do not introduce a second loop.

`acp/convert.rs` converts supported ACP input to a user message and projects
transcript entries into ACP updates. It does not perform I/O. Every tool call it
sends carries a tool call title built from the call's arguments and the tool
kind that tells the client which icon to show.

The dependency direction is:

```text
main -> acp, auth, openrouter
acp -> auth, openrouter, sessions, operations, prompt, convert
prompt -> openrouter, tools, sessions, operations, convert
openrouter -> sessions (transcript types only), tools (schemas only)
tools -> sessions (tool call and outcome types only)
convert -> sessions, tools (tool call titles only)
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
    openrouter: Arc<Mutex<Option<openrouter::Client>>>,
    operations: SessionOperations,
}
```

The server itself is not behind a mutex. Cloning `ServerState` clones the shared
handles; it does not duplicate the transcript or the database.

### Startup

Resolve the data directory, open the database, apply initialization, and
construct the operation registry before accepting requests. A database that
cannot be opened is a startup error. Credentials are not read at startup: the
process must remain available for terminal authentication, listing, and deletion
without them.

The database location is resolved once, using this precedence:

1. `OX_DATA_DIR`, when set to a usable value.
2. `XDG_DATA_HOME/ox`, when that base directory is available.
3. `~/.local/share/ox`.

The database filename is `ox.db`. Failure to resolve a required base directory
is reported explicitly rather than silently selecting the working directory.

### Lazy OpenRouter client

When an authenticated operation needs the OpenRouter client:

1. Lock the client slot.
2. Return a clone if a client is already cached.
3. Otherwise resolve credentials, giving a nonempty `OPENROUTER_API_KEY`
   environment value precedence over a saved key.
4. If no key exists, return an authentication-required error and leave the slot
   empty.
5. Construct and cache a concrete client, clone it, and release the lock.

Credential lookup and client construction are synchronous. No network request
occurs while holding the slot lock. This short critical section intentionally
serializes cache initialization and cache clearing. Credential-store errors are
reported; they are not treated as a missing key. After the first successful
lookup, no request touches the credential store.

ACP clients run the terminal login as a separate process and then retry the
original request rather than calling `authenticate`. The empty slot on that
retry is how a newly saved key is picked up without a process restart.

A prompt run holds its own clone of the client. The client contains immutable
credentials and a reusable HTTP connection pool, not conversation state. Each
request has its own stream assembly state.

Explicit login validates the key using OpenRouter's key endpoint without
generating, before saving it. A successful key check is not a guarantee that
every model request will succeed.

### Logout and credential changes

Logout removes the saved key and clears the cached client under the same
credential-state serialization policy. Clear the cached client even if removal
reports an error, and report that error accurately: the saved key may still
exist and be loaded on a later request.

Prompts that already own a client clone may finish. Logout is not cancellation
or upstream revocation. If the environment still supplies a key, the next
authenticated operation loads it again; the logout command reports this.

An external change to a saved key does not replace an already cached client. Hot
reload is outside the initial scope. Never log keys, authorization headers, or
raw credential-bearing request objects.

## 7. Conversation model

The transcript represents messages and completed tool exchanges rather than
transport chunks. Its central distinction is between a validated, complete
assistant message and provisional output observed while producing that message.

A representative domain model is:

```rust
enum TranscriptEntry {
    Model(String),
    UserMessage(String),
    AssistantMessage(AssistantMessage),
    ToolResult(ToolResult),
}

struct AssistantMessage {
    text: String,
    reasoning: String,
    tool_calls: Vec<ToolCall>,
    /// OpenRouter's opaque `reasoning_details`, retained for the next request.
    continuation_metadata: Vec<serde_json::Value>,
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

All calls from one model completion belong to one assistant message. Associated
text and reasoning stay with that message. Multiple calls do not become multiple
assistant messages, and reasoning does not drift into a later answer during
transcript reconstruction.

The fields mirror OpenRouter's assistant message: visible answer text, visible
reasoning text, the tool calls, and the opaque continuation metadata. Empty text
or reasoning means the completion produced none. There is no interleaving order
to preserve. Network arrival order can interleave fragments of these fields
without defining a semantic message order, and the client's assembled result is
what the transcript stores.

Visible reasoning and continuation metadata have different purposes. Visible
reasoning supports client presentation. Structured OpenRouter details support
model continuation. The encoder must avoid duplicating equivalent reasoning
fields and must use the representation required for the supported model.

The `continuation_metadata` field is deliberately concrete and
provider-qualified: OpenRouter's `reasoning_details` at its wire boundary.
Preserve its complete JSON values, nested fields, association, and array order
rather than interpreting opaque elements as display text. JSON formatting and
object key order are not a byte-preservation contract. Unknown fields inside
opaque metadata are retained; unknown transcript formats remain errors.

Every transcript opens with the model its completions use. A new session stores
the current default; an existing session continues with its stored model, so
continuation data is only ever sent back to the model that produced it. The
model does not change within a session.

### Tool calls and results

Call IDs must be nonempty and unique within the validated assistant message.
Each result must match that message's call ID and tool name. Pairing is scoped
to the assistant batch rather than an unbounded global map of call IDs.

The argument string is the complete string provided by the model. Retaining it
permits invalid JSON or an invalid typed argument shape to receive an ordinary
failed tool result after the message is validated. Incomplete stream assembly is
a different problem: it cannot become an executable call.

An empty tool name is a malformed envelope. An unknown nonempty name is a
dispatch failure the model can see and potentially correct on a later call.

Cancellation results contain a short explanation distinguishing a tool that was
never started from one interrupted before its result was observed. Failure
results likewise distinguish validation failure from execution failure.

Results are persisted in original call order even though final outcomes may
become known at different times. With sequential execution this ordering is
straightforward.

### What is not a transcript entry

Do not persist individual deltas, connection state, operation guards,
cancellation signals, HTTP payloads, or SDK objects. Usage information is not
required for conversational correctness and does not earn a second log.

Timestamps remain available in storage and session summaries. Transcript reads
return transcript entries without a timestamp wrapper unless a real consumer
needs per-entry timestamps.

## 8. Store and database contract

```rust
#[derive(Clone)]
struct SessionStore(Arc<Mutex<rusqlite::Connection>>);

struct SessionSummary {
    id: SessionId,
    workspace_path: PathBuf,
    session_title: Option<String>,
    created_at: String,
    updated_at: String,
}

struct StoredSession {
    summary: SessionSummary,
    transcript: Vec<TranscriptEntry>,
}
```

The store opens one connection and initializes schema and connection settings
once. Production and tests use the same methods; tests can construct an
in-memory connection. There are no duplicate production and test query APIs.

Every store operation acquires the connection mutex only for its synchronous
database work. All reads as well as writes serialize on this connection. Model
requests, tool awaits, replay updates, and credential lookup happen outside that
mutex. Code must not recursively acquire the store lock.

### Public operations

| Operation                 | Contract                                                                                                                                 |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `create(workspace_path)`  | Generate a session ID and create its workspace association and metadata atomically. Return the new summary.                              |
| `read(id)`                | Return `None` for absence; otherwise return metadata and ordered transcript from one read transaction.                                   |
| `list(workspace_path)`    | Return owned summaries, optionally filtered by workspace, ordered by descending activity with a stable ID tie-breaker.                   |
| `append_user(id, text)`   | Append the user message, adopt the first usable session title when absent, and update activity in one transaction. Return the resulting summary. |
| `append_batch(id, batch)` | Validate a complete assistant batch, append all its entries, and update activity atomically.                                             |
| `delete(id)`              | Remove the session and its dependent entries atomically; absence is an idempotent success.                                               |

The prompt run has no unrestricted entry append. `read` distinguishes an absent
session from an existing untitled session. `append_user` and `append_batch` fail
for an absent session; they never create one implicitly. Empty batch writes are
not used to change activity.

### Logical schema

The database contains workspace rows, session rows, and an ordered
`transcript_entries` table. Session rows contain the session ID, workspace
association, optional session title, and creation and activity timestamps.
`transcript_entries` rows contain an ordered integer ID, session ID, timestamp,
kind, and serialized payload.

Foreign keys are enabled. Deleting a session cascades to its
`transcript_entries` rows. Transcript entry ordering uses the integer sequence,
never timestamps. UTC timestamps use one consistent representation; equal
timestamps do not imply equal rows.

The first saved nonblank user message supplies an initial session title, limited
to 80 Unicode scalar values including the ellipsis that marks a shortened one.
Later appends do not overwrite an established session title. Session-title
derivation does not modify the input sent to the model.

Workspace identity is the supplied absolute path compared using one consistent
path policy across create, list filtering, and load. Initially use exact path
equality without resolving symlink aliases. Reject relative workspace paths
where an absolute workspace is required. Do not silently merge two workspace
identities that happen to refer to the same directory.

### Schema, encoding, and transactions

Opening a database creates any missing tables and indexes. The database carries
no schema version, and there is no migration path or reader for earlier formats:
a schema change means deleting the local database and starting over.

Each `transcript_entries` row stores its kind and the serde-derived JSON of the
transcript type it holds. OpenRouter request and response shapes are separate
structs in `openrouter.rs`, so a client change does not alter stored entries.

A batch transaction inserts the assistant message and all paired results, then
updates session activity. If any step fails, the transaction rolls back. The
caller retains its uncommitted batch and does not extend the transcript.

Read validation rejects malformed payloads, unknown kinds, orphan results,
duplicate results, and unresolved calls. Do not skip unreadable rows or
substitute an empty transcript.

A saved batch is immutable transcript content. There is no automatic rewrite to
make OpenRouter accept a previously stored message.

### Limitations

The database mutex is not the operation guard. It serializes individual database
operations, not a whole conversation turn. Another process is not prevented from
opening the file by the Rust mutex; single-writer process ownership is an
operating assumption.

The mutex and SQLite calls may block executor workers. This is accepted for the
initial local workload without promising a latency bound. If measurements
justify it, introduce a blocking-executor boundary around this concrete store;
do not preemptively add an actor, pool, or repository abstraction.

## 9. Operation guards and cancellation

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
Failure to acquire a guard returns busy immediately. There is no wait queue and
no check-then-delete API.

| Incoming operation | Idle                 | Prompt holds the guard | Load holds the guard | Delete holds the guard |
| ------------------ | -------------------- | ---------------------- | -------------------- | ---------------------- |
| Prompt             | Acquire              | Busy                   | Busy                 | Busy                   |
| Load               | Acquire              | Busy                   | Busy                 | Busy                   |
| Delete             | Acquire              | Busy                   | Busy                 | Busy                   |
| Cancel             | No-op                | Signal prompt          | No-op                | No-op                  |
| List               | Read saved summaries | Read saved summaries   | Read saved summaries | Read saved summaries   |

Session creation chooses a new ID and does not replace existing live state.

The operation guard is private and non-cloneable. Cancellation signals can be
cloned without duplicating the guard. `Drop` removes the occupied entry
synchronously; it performs no persistence, update sending, or asynchronous
cleanup.

The prompt handler acquires the guard before spawning. It moves the guard into
the task. Failure to spawn must drop the future and its guard. The guard stays
alive until the final response is sent, including the finish path after an
OpenRouter or update-sending failure.

Load holds its guard through read, validation, replay, and sending the response.
Delete holds its guard through the database operation and sending the response.
Neither holds the registry mutex while doing that work.

Cancellation is a latched signal belonging to the active prompt. It remains
observable if it arrives before the task begins polling. Repeated cancellation
is harmless. A cancellation received while idle does not cancel a later prompt.

Only short synchronous map access occurs under the registry mutex. The guard
never holds that mutex for its entire lifetime.

## 10. OpenRouter adapter boundary

`openrouter::Client` performs one model request. Its inputs are the saved
transcript, the fixed model choice, and the concrete tool schemas. Its outputs
are live text and reasoning updates followed by at most one validated
completion, or a request failure.

A conceptual stream item set is:

```rust
enum StreamItem {
    TextDelta(String),
    ReasoningDelta(String),
    Completion(Completion),
}

struct Completion {
    message: AssistantMessage,
    stop: Stop,
}

enum Stop {
    Finished,
    ToolCalls,
    TokenLimit,
    Refused,
}
```

Keep wire-specific response codes, DTOs, and assembly details private. The
client translates supported OpenRouter finish reasons into this closed set. An
unknown or contradictory response is a clear client error, not an implicit
normal completion.

The client is responsible for:

- Encoding the saved transcript without losing message grouping or continuation
  metadata.
- Assembling a complete response according to the documented streaming protocol,
  including final validation and supported metadata.
- Keeping all mutable assembly state local to the request.
- Yielding exactly one completion only after the response meets the client's
  validation contract and the stream has been read to its end, so the connection
  returns to the pool.
- Distinguishing provisional text from validated message content.
- Reporting malformed responses and transport failures without executing tools
  or changing the saved transcript.
- Stopping consumption when the owning request is cancelled or dropped.

The completion item means the client has finished the protocol work needed to
validate that response. No later stream item can revise its validated content.
End-of-stream without that item is an error.

Visible deltas are not the authoritative transcript, even when their
concatenation looks complete. The final assembled message is authoritative for
saving. Opaque data never travels through a visible reasoning delta.

The initial UI does not display partial tool-call arguments. A tool starts
appearing in the UI after validation, as a pending tool call, when Ox has a
complete call to represent. This avoids provisional tool objects that need
repair if stream assembly fails.

Use a normal HTTP client and streaming framing library. Do not implement a
general networking stack, background stream reader, or second event queue.
Fixture tests can supply a local endpoint without introducing user-facing
backend configuration. The endpoint, model identifier, and tuning limits are
nearby implementation constants.

## 11. Prompt execution

### Guard acquisition and preparation

The handler converts and validates input before starting the run. It accepts
text blocks and resource-link descriptions; a resource link contributes its name
and URI as text and is not fetched automatically. Unsupported content causes an
invalid-parameters error before any part of the prompt is saved. The converted
input must contain nonblank text.

After obtaining credentials and acquiring the operation guard, the handler
spawns one prompt run. That run:

1. Reads the session summary and saved transcript.
2. Reconstructs the model conversation from the saved transcript.
3. Appends the user message and updates the session title and activity in one
   transaction.
4. Adds that same user message to its in-memory transcript.
5. Enqueues the session metadata update. The update carries the session title
   only when the summary read in step 1 had none and the append adopted one.
6. Enters the model/tool loop.

No model request occurs before step 3 succeeds. Preparation failures before that
point require an error response and dropping the guard, but no batch saving.
After step 3, every ordinary exit goes through the finish path, even when there
is no uncommitted assistant batch to save.

If cancellation is already observed before user append begins, return cancelled
without writing that input. Once user append succeeds, the input remains saved
even if cancellation immediately prevents inference. A synchronous transaction
is not interrupted halfway through to implement this distinction.

### Owned prompt state

Each prompt run owns:

- The operation guard and its cancellation signal.
- A cloned OpenRouter client.
- The saved transcript loaded from the store and extended after successful
  writes.
- At most one active completion stream.
- At most one uncommitted assistant batch.
- The final outcome and any finish-path failure.

`UncommittedAssistantBatch` contains one validated assistant message and one
outcome slot per tool call. The slots begin empty and can be filled once.
Iterating them in message order constructs the complete batch. This state needs
neither a global outcome map nor a tool callback that runs before some other
callback.

The uncommitted batch is distinct from the saved transcript: the assistant
message has been validated, but it is not durable transcript content until its
batch is complete and saved.

### Loop

```text
loop:
    check cancellation
    make one model request using the transcript
    forward live text and reasoning as ACP updates
    require one validated completion
    capture its assistant message in an uncommitted batch
    send a pending tool-call update for every call in that message

    classify its stop condition
    if tool calls need running:
        run the calls in order
        check cancellation before starting each call
        send the in-progress update, then run the call or set its validation-failure outcome
        set the outcome before sending its finished update

    save the complete batch; extend the transcript only after the save succeeds
    clear the uncommitted batch

    if the stop condition ends the prompt run:
        build the corresponding final response
        stop

give every unstarted call an explicit outcome, save the batch, send the
remaining updates, and build the final response
```

The implementation may use early returns inside helpers, but the prompt run
catches their ordinary errors and passes through its common finish path. Update
helpers do not escape this structure through an unhandled `?`.

### Completion classification

- `Finished`: save a valid message with no outstanding calls and finish.
- `ToolCalls`: require at least one complete call, run its tools, then request
  the next completion if allowed.
- `TokenLimit`: save a validated, structurally valid text response and return
  the token-limit stop. Never execute an incomplete or truncated call.
- `Refused`: save a validated displayable refusal response and return the
  refusal stop.
- Unknown termination or contradictory message structure: treat it as an
  OpenRouter failure and enter the finish path without inventing a valid
  assistant message.

A token-limited answer is distinct from a transport-interrupted answer: the
former has a valid final response from OpenRouter. Unsupported tool-bearing
limit or refusal responses are client errors until their semantics are
explicitly supported; they do not trigger opportunistic execution.

## 12. Tools and cancellation

Before running each shell call over ACP, the prompt loop sends
`session/request_permission` with the command, workspace, and two choices:
Approve (`allow_once`) and Deny (`reject_once`). The call remains `pending`
until approved. Denial produces a failed tool result and allows later calls and
model requests to continue. Cancellation stops the prompt; a permission request
error or unknown option also prevents execution and ends the prompt with an
error. The existing finish path saves all tool outcomes together. Keep the
operation guard while waiting for permission, saving, and responding. Headless
`ox run` automatically approves all tools. Other tools need no approval.

`tools::execute` takes a complete tool call, the session's workspace context,
and a cancellation future. It parses arguments into the owned input type for the
named tool and returns a concrete `ToolOutcome`.

Dispatch is a small exhaustive name match over the implemented tools. Schemas,
argument types, tool call titles, and execution stay together. No plugin
registry or general tool trait is needed for this set.

Unknown tool names, invalid argument JSON, and invalid typed arguments produce
failed results associated with the original call ID. A tool's ordinary runtime
failure also becomes a failed result. The model may respond to that result on
the next request. Broken internal invariants remain programming bugs.

Execute calls sequentially. Successful and failed calls both consume their
position in the batch; a failure does not erase prior outcomes. Ordinary tool
failure does not stop later calls unless the prompt is also cancelled or an
update/storage condition requires stopping new execution.

After the loop validates a complete assistant message, tool handling follows
this sequence:

1. Send every tool call in the message as a pending tool-call update, in call
   order.
2. Check cancellation.
3. Execute eligible calls sequentially. Before each call, check cancellation and
   request shell approval when using ACP. Send an in-progress update only for
   calls that will run. While a call runs, observe cancellation according to
   that tool's resource lifecycle. As soon as an outcome is obtained, add it to
   the uncommitted batch before sending its finished update or checking
   whether later work should begin.
4. Give every unexecuted call an explicit outcome and send its finished update.

Sending finishes before execution begins, so the client holds an object for
every call and the finish path only ever sends updates. Ox tracks no per-call
announcement state. If sending fails partway through step 1, execute nothing,
stop attempting client updates, and save the entire uncommitted batch to
storage. The connection has failed; a later load supplies the saved transcript.

The loop must not classify an outcome it has already obtained as cancelled just
because cancellation arrives before its update is sent. An outcome not yet
observed by Ox cannot be claimed as known success. Cancellation races are
resolved at these explicit observation boundaries.

Each tool defines a concrete cancellation boundary for the resources it owns.
Apply patch checks cancellation before synchronous filesystem execution; once
started, that execution finishes and its observed outcome enters the uncommitted
batch before the prompt run finishes. Other existing tools retain the
dispatcher's tool-first cancellation race. Shell execution receives the
cancellation future directly; the prompt awaits it without an outer race that
could drop its cleanup.

Each shell call starts `/bin/sh -c` in a new process group. Exit, timeout,
cancellation, and output-read failure all lead to SIGKILL of the group and
reaping of the shell. An already-observed exit takes precedence over timeout or
cancellation, including during cleanup. Even normal exit stops remaining
background children. Output draining has a separate one-second deadline so an
unsupported detached descendant cannot keep an inherited pipe open indefinitely.
Results retain bounded stdout and stderr tails and report possible partial
changes after timeout or cancellation. See
[the shell tool specification](ox-shell-tool.md) for output allocation and the
limits of process-group cleanup.

ACP Stop and connection shutdown signal cancellation immediately. Headless
`ox run` also translates SIGINT into prompt cancellation, including repeated
signals, while keeping the prompt alive through cleanup and saving. The
operation guard remains held through cleanup, the batch save attempt, and
response handling.

Cancellation during a model request stops Ox's consumption and prevents new tool
execution. It does not establish whether upstream processing or billing stopped.
Cancellation during tool execution preserves observed outcomes and fills all
remaining slots with accurate final outcomes.

Once the final save begins, cancellation does not interrupt the save or rewrite
the outcome already present in the batch. A late cancellation may therefore
race with normal completion and receive the normal completed response. It never
applies to a subsequent prompt.

## 13. Finish path and response semantics

The driver uses a small private `PromptOutcome` enum for normal completion,
cancellation, token limit, refusal, OpenRouter failure, ACP-update failure, and
storage failure. These distinctions exist because their finish behavior differs;
they do not require a public error hierarchy.

The finish path executes in this order:

1. Stop starting new model requests and tools.
2. Stop or clean up the current running work as required.
3. Preserve every tool outcome already observed.
4. If an uncommitted batch exists, fill empty outcome slots with explicit
   cancelled, failed, or not-started outcomes appropriate to the outcome.
5. Save that complete batch, unless a storage failure has already made this
   write unsuccessful. Do not automatically retry failed writes.
6. Extend the transcript and clear the uncommitted batch only after the save
   succeeds.
7. Send finished updates for calls completed without execution, unless sending
   an update has already failed. Every call was sent as pending after
   validation, so the finish path sends updates only, never a first
   announcement.
8. Send the final response or request error.
9. Make the session available by dropping the guard.

Normal live tool updates may have been sent earlier; the finish path does not
need to re-send finished updates that were already sent successfully.

| Outcome                              | Persistent effect                                                          | Client result                                                                  |
| ------------------------------------ | -------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Normal final answer                  | Save the final batch.                                                      | Successful end-of-turn response.                                               |
| Ordinary tool failure                | Save the failed result with its assistant batch; continue.                 | Failed tool update, followed by the model's subsequent response.               |
| Validated token-limited answer       | Save the valid assistant message.                                          | Token-limit stop.                                                              |
| Validated refusal                    | Save the validated assistant message when present.                         | Refusal stop.                                                                  |
| Cancellation before validation       | Keep the user message and prior saved batches; discard provisional output. | Cancelled response.                                                            |
| Cancellation after validation        | Preserve observed outcomes; fill remaining slots; save.                    | Finished tool updates where possible, then cancelled response.                 |
| OpenRouter failure before validation | Keep the user message and prior saved batches; discard provisional output. | Request error.                                                                 |
| ACP-update failure                   | Stop new execution; save any validated batch independently of updates.     | Propagate connection failure after local saving; receipt is not guaranteed.    |
| Storage failure                      | The failed write contributes no transcript content; earlier saves remain.  | Persistence error, with the original outcome retained as context where useful. |

The complete assistant batch is the persistence boundary for tool results. The
prompt run keeps the batch through the save attempt, stops later work when the
save fails, and reports the storage error.

An error response does not imply a successful final save or successful prior
updates. Only the successful response contract asserts the required save and
send ordering. If the connection fails after a save, a subsequent load can
recover the saved conversation.

Do not mask a storage error with the cancellation or OpenRouter failure that
caused the finish path. Preserve the original cause as diagnostic context, but
make the failed save clear to the client whenever sending updates remains
possible.

### Provisional output

Text and visible reasoning may reach the client before the model response is
validated. If that response fails or is cancelled, those provisional chunks are
not stored as an ordinary assistant message. They can disappear on reload.

This behavior is deliberate and must be understood as a UI limitation. Ox does
not claim live output and replay are byte-for-byte identical. If durable
interrupted output becomes a product requirement, add an explicit interrupted
entry with defined replay and model-context treatment. Do not silently promote
incomplete output to a completed answer.

A saved user message with no assistant reply means the input was saved. It is
not an instruction to resume inference automatically after restart.

## 14. ACP behavior and projections

### Session operations

Creation requires credentials, validates the workspace path, creates a durable
session, and returns its ID. Loading and prompting also require credentials.
Listing and deletion do not need OpenRouter access. Cancellation is always
available and is a no-op when no prompt run holds the session.

Load acquires the operation guard, reads and validates the saved transcript,
verifies the requested workspace matches the stored workspace, and replays the
transcript. Workspace validation precedes replay. The guard remains held until
the load response is sent.

Prompt looks up the session's stored workspace for tool context; it never uses a
process-global current directory as a substitute. Unknown session IDs return a
resource-not-found error.

List returns saved summaries without waiting for a session operation to finish.
The session title or activity of a running session can therefore reflect its
most recent completed write. The initial listing is unpaginated and returns no
continuation cursor. Unsupported nonempty cursor input is rejected rather than
silently returning the wrong page.

Delete requires the operation guard and atomically removes the stored session
and transcript. Deleting an already absent session succeeds. It does not cancel
an active prompt; it returns busy.

### Live updates and replay

Use shared constructors for visible text, reasoning, pending tool calls, and
in-progress and finished tool states. Live text uses deltas; replay uses saved
complete segments. An assistant message projects to several updates (reasoning,
text, and one per tool call), so the conversion API must not assume one
transcript entry equals one ACP update.

Replay sends only displayable content and tool states. Opaque continuation
metadata is intentionally omitted. Each tool call replays as one complete
tool-call object already in its final state: completed tools as completed,
failed and cancelled tools as the protocol's available final failure state with
an accurate explanatory message.

Iterate through the loaded transcript during replay rather than building a
second full vector of updates. An update error ends replay and drops the guard
after the response attempt. Already saved transcript content is unchanged.

The initial schema does not save standalone stop reasons. Replay recovers
content and final tool states, not an exact reproduction of historical prompt
responses or network timing.

After restart, load restores the saved conversation and later prompts rebuild
model context from that same source. It does not resume an in-flight model
request, running tool, or unsent update.

### Sending updates and the connection lifecycle

Sending an update means enqueueing it on the connection. The final response is
sent after the saves and preceding updates required by its outcome. There is no
acknowledgement that the client received or rendered those updates.

Ox adds no intermediate event queue and makes no bounded-buffering claim. Should
slow-client memory growth become an observed problem, solve it where the
outgoing transport queue is owned.

Connection shutdown signals active prompts and keeps the task executor available
until active operations finish and drop their guards.

OpenRouter and input failures become request errors on a healthy connection.
Only actual connection failure escapes at the connection task boundary.

## 15. Tool execution and persistence

Ox's tool-execution persistence contract is:

1. The user message is saved before the model request begins.
2. Tool execution begins from a validated, complete assistant message owned by
   the active prompt run.
3. The prompt run owns each tool through its resource-specific cancellation
   boundary. A synchronous mutating operation finishes and yields an observed
   outcome before the run acts on cancellation.
4. Every observed outcome enters the run's in-memory `UncommittedAssistantBatch`
   before a finished update, a later tool call, or another model request.
5. Once every call has a final outcome, the assistant message and all results
   are appended to the transcript in one transaction.
6. The saved in-memory transcript advances after that transaction commits. The
   next model request and a successful prompt response follow the same boundary.
7. An update failure stops later calls in the batch and gives their outcomes
   explicit results. A batch storage failure stops the prompt run before another
   model request. The operation guard remains held through the finish path.
8. Orderly connection shutdown waits for running tool work and the finish path
   to complete.
9. Session load, replay, and model continuation use saved transcript entries.

The prompt run, `UncommittedAssistantBatch`, `SessionStore::append_batch`, and
the operation guard implement this contract. New tools integrate their concrete
resource lifecycle with these existing boundaries. This is the complete
persistence model for prompt-owned tool execution.

## 16. Errors and diagnostics

Use existing I/O and orchestration error conventions for storage, credentials,
and connection work. Convert errors at the ACP boundary into the appropriate
protocol result. Introduce a custom error type only where code must actually
recover differently by variant.

| Condition                                       | Treatment                                                  |
| ----------------------------------------------- | ---------------------------------------------------------- |
| Unsupported or blank prompt content             | Invalid parameters; no user append.                        |
| Missing credentials                             | Authentication required; keep process available.           |
| Credential-store failure                        | Actionable request or command error.                       |
| Unknown session                                 | Resource not found.                                        |
| Conflicting session operation                   | Busy; do not enqueue work.                                 |
| Wrong workspace on load                         | Explicit mismatch error before replay.                     |
| Malformed OpenRouter response                   | Request failure through the finish path.                   |
| Invalid tool arguments or ordinary tool failure | Failed tool result.                                        |
| Invalid stored transcript                       | Actionable storage/format error; do not skip data.         |
| Failed transaction                              | Persistence error; no advancement of the saved transcript. |
| Broken internal invariant                       | Loud programming failure through `expect` or panic.        |

Diagnostics should identify the session, operation phase, and relevant call ID
when useful. Avoid raw transcripts, opaque reasoning metadata, credentials, and
full tool arguments in routine logs. A short error summary usually provides
enough context without duplicating sensitive conversation data.

No fallback path silently substitutes an empty transcript, a made-up call ID, a
successful tool result, or another provider.

## 17. Verification strategy

Tests establish behavior at the boundaries the design exists to get right: the
OpenRouter wire, the persisted transcript and its two projections, operation
guards, and update sending. They do not freeze private helper organization, and
the list below is the whole initial suite. Add cases when a failure earns them.

### OpenRouter fixtures

Use a local HTTP server and deterministic stream fixtures. Verify request
encoding (message grouping, tool schemas, continuation metadata sent back
without duplicating visible reasoning), stream assembly (text, reasoning,
several calls, fragmented call arguments), stop classification, a malformed or
incomplete stream reported as an error, and cancellation mid-stream. Assert that
partial calls never execute and that a completion is produced at most once. Test
credential validation separately from inference. No test makes a paid model
request.

### Store

Use an in-memory connection for ordinary operations and one temporary database
file for the reopen test. Verify:

- A batch with several tool calls, continuation metadata, and paired results
  survives write, close, reopen with a new store, and read as the identical
  transcript. From that reopened transcript, both the ACP replay updates and the
  next encoded model request are correct: no unresolved call, duplicate result,
  or misplaced reasoning.
- Orphan, duplicate, and missing results fail batch validation explicitly.
- User append adopts a session title once and updates activity.
- Deletion removes the session's entries and repeated deletion succeeds.
- Opening a database twice reuses its existing tables.

### Loop

Pass a small `FnMut(SessionUpdate) -> Result<()>` closure to the prompt driver
for sending updates. Production wraps the connection; tests collect updates or
fail a chosen send. Drive the real loop against the fixture client. Cases:

- Normal text answer, with the saved transcript extended after the save.
- Several tool calls in one assistant message with ordered final results.
- Cancellation during a tool, preserving the outcome already observed and giving
  the rest explicit cancelled outcomes.
- Failed batch save, leaving the saved in-memory transcript unchanged.
- Update failure after an observed tool result, with that result still saved in
  the finished batch.

### Operation guards

One table-driven test over the conflict matrix in section 9: prompt, load, and
delete arriving while the session is idle, prompting, loading, or deleting, plus
dropping the guard making the session available. Cancelling session B does not
signal session A's prompt.

## 18. Implementation sequence

1. Define the structured transcript entries and assistant-batch validation.
   Implement the persistent store and the storage tests.
2. Implement the concrete OpenRouter client and qualify its boundary with local
   fixtures. Keep it independent of ACP, persistence, and tool execution.
3. Implement atomic operation-guard acquisition and the unique operation guard.
4. Implement direct tool dispatch, the owned prompt run, and the common finish
   path. Connect the client, store, and update closure without introducing a
   general runtime abstraction.
5. Wire ACP handlers, replay conversion, lazy credential loading, and the
   successful response ordering contract.
6. Switch the production path to this loop and remove the superseded execution
   path and dependencies. Do not retain two selectable production runtimes.
7. Run the boundary tests and the reopen test, then the Rust build and test
   checks.

Mechanical module moves should remain distinguishable from behavior changes. The
implementation need not reach a predetermined file count if a smaller
arrangement retains the same ownership boundaries. Do not add a compatibility
framework, runtime toggle, or fallback merely to make incremental construction
possible.

## 19. Tradeoffs and extension triggers

| Decision                                           | Accepted cost                                                                     | Trigger for change                                                                        |
| -------------------------------------------------- | --------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| Ox owns the prompt run and OpenRouter client.      | Ox maintains its request boundary, tool dispatch, and termination behavior.       | A concrete unmet OpenRouter feature or measured maintenance problem.                      |
| One persistent database connection.                | Reads and writes serialize; synchronous calls can block an executor worker.       | Measured contention or latency.                                                           |
| Whole-transcript reads.                            | Memory and request size grow with conversations.                                  | Actual context-limit or memory pressure, addressed with explicit compaction semantics.    |
| No schema migration or version.                    | A schema change requires deleting the local database.                             | Conversations become valuable enough to carry across schema changes.                      |
| Busy instead of queueing.                          | Clients must retry conflicting operations.                                        | A real requirement for queued or independently arriving work.                             |
| Sequential tools.                                  | Independent calls cannot overlap.                                                 | A measured latency benefit sufficient to justify concurrency and cancellation complexity. |
| Assistant-batch tool persistence.                  | Tool results become saved transcript content with their complete assistant batch. | Work that must continue independently of the prompt run that started it.                  |
| Provisional output is discarded on interruption.   | Some live text disappears on reload.                                              | A product requirement for durable interrupted output.                                     |
| Concrete provider-qualified continuation metadata. | Storage understands one provider's continuation concept.                          | A real second provider, addressed without losing existing data semantics.                 |
| No independent session actor.                      | The prompt run handles only work initiated by its prompt.                         | Background work that must enter a session independently.                                  |
| No automatic retry.                                | Transient failures are visible to the caller.                                     | A specific retryable operation with clear budget, cancellation, and effect semantics.     |

The architecture should grow by making these boundaries explicit when their
assumptions change. Its initial form remains one concrete OpenRouter client, one
prompt run, one store, and one authoritative transcript.
