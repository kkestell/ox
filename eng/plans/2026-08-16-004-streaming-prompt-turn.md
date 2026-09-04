# 2026-08-16-004. Streaming prompt turn

## Goal

Ox answers `initialize` and then has nothing to say. Give it the conversation: a
client creates a session rooted at a working directory, sends a user message,
watches the model's answer arrive token by token as `session/update`
notifications, and gets back a stop reason. A client that changes its mind
cancels the turn and still gets a well-formed answer rather than an error.

This is also the first code that talks to a model, so it introduces the
OpenRouter streaming client the rest of the agent will be built on.

## Desired outcome

An ACP client calls `session/new` with an absolute `cwd` and receives a session
ID. It calls `session/prompt` with a text block. Before the response arrives it
receives a run of `agent_message_chunk` notifications whose concatenated text is
the model's answer, preceded by `agent_thought_chunk` notifications when the
model streams reasoning. The response carries `stopReason: "end_turn"`. A second
prompt on the same session sends the first exchange back to the model, so the
conversation accumulates.

If the client sends `session/cancel` mid-stream, the model request is abandoned
and the prompt responds `stopReason: "cancelled"` — never an error. Every
notification for a turn reaches the client before that turn's response.

## Summary of approach

Three pieces land together.

`internal/openrouter` is a new package holding the model client: the request and
completion types, an SSE line reader, a chunk assembler, and one `Stream` method
that posts a streaming chat completion and invokes a callback per delta. It is
the Alpha client with everything later removed — no retry, no model catalog, no
tool calls, no provider routing, no reasoning-detail merging. `Stream` returns
the assembled completion alongside any error, so a turn cut short still knows
what text it streamed.

`internal/acp` gains the wire vocabulary for the three methods plus the
`session/update` payload, and `Validate` methods for the two requests and the
one notification, following the pattern already set by `InitializeRequest`.

`internal/agent` gains a session registry and the three handlers. A session is
an ID, a canonical working directory, a message history, and the cancellation
state of the turn currently running in it. `session/new` mints one.
`session/prompt` claims it, converts the prompt to a user message, runs one
model request, and answers. `session/cancel` fires the claimed turn's cancel
function.

The turn runs inline on the handler goroutine and streams by calling
`jrpc2.Server.Notify` directly from the delta callback. `Notify` encodes and
writes under the server lock before returning, so notifications are on the wire
before the handler returns its response, which is exactly the ordering the
protocol requires. There is no event channel, no adapter, and no flush ticker:
those exist in Alpha to coalesce live tool output, and there are no tools yet.

Without tools a turn is exactly one model request, so this plan builds no loop.
The tool loop arrives with the tools it exists to serve.

## Related code

- `~/src/references/repos/personal/alpha/runtime/internal/openrouter/sse.go`,
  `stream.go`, `client.go`, and `types.go` — the source for the new package.
  `readSSE` handles multi-line `data:` payloads, comment lines, and `[DONE]`,
  and reports a stream that ended without `[DONE]` as an error.
  `streamAssembler.push` is the shape to copy for accumulating text and
  reasoning while emitting deltas; `sawChoice` is what turns a body containing
  no choices into an error instead of a silent empty answer.
- `~/src/references/repos/personal/alpha/runtime/internal/agent/agent.go:249-307`
  — `NewSession`: validate, canonicalize, mint, register. `:787-941` is
  `Prompt`, and `:1089-1176` is the `claim`/`cancel`/`release` trio that keeps
  one turn per session and distinguishes a client cancellation from any other
  context end.
- `~/src/references/repos/personal/alpha/runtime/internal/agent/loop.go:473-504`
  — `promptMessage`, which renders a `resource_link` block as an escaped
  Markdown link. `:980-989` is the `finish_reason` to `StopReason` mapping.
- `~/src/references/repos/personal/alpha/runtime/cmd/amber-runtime/main.go:53-56`
  — sets `Concurrency: 16` explicitly, for the reason given under Structural
  considerations.
- `~/src/references/repos/personal/beta/src/acp.rs:315-364` and `:462-500` — the
  same design in Rust. Its `busy` swap rejecting a concurrent prompt, and
  `run_turn` clearing busy and cancel state on every exit path before
  responding, are the invariants worth copying.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/docs/protocol/v1/prompt-turn.mdx`
  — the prose contract, including the requirement that a cancelled turn answers
  with the `cancelled` stop reason rather than a transport error, and that
  updates may be sent after `session/cancel` but must precede the response.
  `session-setup.mdx` covers `cwd` and `mcpServers`. `schema/v1/schema.json` has
  the exact field sets.
- `/Users/kyle/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/server.go:474-515`
  — `pushReq`, which encodes notifications synchronously under the server mutex
  and ignores the passed context for notifications, so an update still reaches
  the client after a turn's context ends. `:374` is the concurrency semaphore
  acquired by every handler.

## Current state

- Relevant existing behavior: `internal/agent` holds one `Agent` value with
  `Initialize` and `CancelRequest`, and no session state at all. `internal/acp`
  covers the initialize handshake and the cancel notification. `cmd/ox/main.go`
  builds the server with `AllowPush: true` and nothing else.
- Existing patterns to follow: wire types and their `Validate` methods live in
  `internal/acp`; handlers convert a validation error to `-32602` and live in
  `internal/agent`; the logger is constructed in `main` and passed explicitly;
  configuration reaches the process through the environment.
- Prerequisite: this plan's tests are written against the end-to-end harness and
  the mock model endpoint, which are planned but not yet implemented. That work
  lands first, and this plan reads the `OX_OPENROUTER_BASE_URL` seam the harness
  sets.
- Constraints from the current implementation: `initialize` already advertises
  the right capabilities for this work. Text and resource-link prompt content
  are the ACP baseline that every agent must support without advertising
  anything, and `loadSession` stays false. Nothing in the initialize response
  changes.

## Structural considerations

- **Hierarchy:** `internal/openrouter` depends on nothing of Ox's.
  `internal/acp` stays independent. `internal/agent` depends on both and
  translates between them; neither of them learns the other exists. That
  translation is the agent's whole job, so it is the right place for it.
- **Abstraction:** `internal/openrouter` speaks the provider's vocabulary —
  messages, deltas, finish reasons. `internal/acp` speaks the client's — content
  blocks, session updates, stop reasons. Keeping the two vocabularies apart is
  what will let a second provider or a protocol revision land without touching
  the other side. The client is a concrete `*openrouter.Client` on the `Agent`,
  not an interface: there is one implementation, and the tests drive the real
  HTTP path against the mock endpoint, which is stronger coverage than a
  substituted fake.
- **Modularization:** the model client is its own package because it is a
  self-contained protocol implementation with its own test surface, and because
  the retry, catalog, and reasoning work already on the roadmap all belong to
  it. Within `internal/agent`, the session registry and the turn go in their own
  files beside `agent.go`.
- **Encapsulation:** a session's history and cancellation state are unexported
  and only reachable through the methods that keep their locking honest.
  Handlers never touch the map directly.
- **Testability:** every behavior here is observable at the process boundary —
  the notifications on stdout, the response, and the HTTP request the model
  endpoint received. The SSE reader, the assembler, the content conversion, and
  the validation are pure functions with unit tests.
- **Concurrency:** the server must set `Concurrency` explicitly. `jrpc2` bounds
  handlers with a semaphore that defaults to `runtime.NumCPU()`, and a prompt
  handler holds its slot for the whole turn. On a single-CPU machine that one
  slot is the only slot, and `session/cancel` would wait behind the very turn it
  exists to stop. ACP handlers block on the network and on the client rather
  than on the CPU, so a fixed bound well above the number of live sessions is
  the correct setting; 16 matches Alpha.

## Test plan

- **Key behaviors to verify:**
  - `session/new` returns a session ID, and two calls return different IDs.
  - `session/new` rejects a relative `cwd`, an unreachable `cwd`, a non-empty
    `mcpServers`, and a non-empty `additionalDirectories`, each with `-32602`.
  - `session/prompt` for an unknown session, an empty prompt, and an unsupported
    content type each produce `-32602`.
  - A scripted stream of text deltas produces `agent_message_chunk`
    notifications in order whose concatenation is the answer, all carrying one
    `messageId`, all delivered before the response.
  - Reasoning deltas produce `agent_thought_chunk` notifications carrying a
    `messageId` distinct from the answer's.
  - The request the mock endpoint received names the configured model, sets
    `stream`, and carries the user's text.
  - A second prompt sends user, assistant, user back to the model, proving
    history accumulates.
  - `finish_reason` of `length` yields `max_tokens`, `content_filter` and
    `refusal` yield `refusal`, and anything else yields `end_turn`.
  - `session/cancel` during a held-open stream ends the turn with
    `stopReason: "cancelled"` and no error, and the session accepts a further
    prompt afterwards.
  - `$/cancel_request` naming the in-flight prompt's ID produces `-32800`.
  - `session/cancel` for an unknown session, and for a session with no turn
    running, are no-ops that leave the process able to answer the next request.
  - A second `session/prompt` while a turn is running is refused, and the
    running turn still completes normally.
  - An `error` object inside a stream chunk, and a non-2xx response from the
    endpoint, each fail the prompt with a JSON-RPC error carrying the provider's
    message.
  - A prompt with no model or no API key configured fails with a message naming
    the missing environment variable.
- **Test levels:** everything above runs through the harness against the built
  binary and the mock endpoint. Unit tests cover the SSE reader (multi-line
  data, comments, `[DONE]`, a body that ends without `[DONE]`, a body with no
  choices), the assembler (text and reasoning accumulation, finish reason, a
  usage-only trailing chunk, an error chunk), the content-block conversion
  including resource-link escaping, the stop-reason mapping, and the new
  `Validate` methods.
- **Edge cases and failure modes:** a cancellation arriving before the first
  delta and one arriving between deltas; a stream whose body ends mid-event; a
  delta with empty text, which must not produce an empty notification. The mock
  endpoint must abandon a held-open response when the request context ends, or
  the cancellation tests hang instead of failing.
- **What not to test:** `jrpc2` framing and dispatch, `net/http` behavior, and
  OpenRouter's own semantics beyond the frame shapes the assembler parses.

## Implementation plan

1. Extend the mock model endpoint from the harness with a held-open response: a
   queued body that writes its opening frames, signals the test that it has
   started, then blocks until the test releases it or the request context ends.
   Cancellation cannot be tested deterministically without it.
2. Write `internal/openrouter/types.go`: `Role` constants; `Request` with
   `model`, `messages`, and `stream`; `Message` with role and text content;
   `Usage` with the three token counts; `Completion` carrying the assembled
   text, reasoning, finish reason, and usage; and `Delta` with a kind and text.
   Leave out tools, tool choice, provider preferences, cache control, reasoning
   configuration, and reasoning details — each belongs to a later item.
3. Write `internal/openrouter/sse.go` as Alpha's `readSSE`: accumulate `data:`
   lines, join multi-line payloads with newlines, skip comments, stop at
   `[DONE]`, and report a stream that ended without it as an error.
4. Write `internal/openrouter/stream.go`: the chunk types and an assembler that
   appends text and reasoning while emitting deltas, records the finish reason
   and any trailing usage, turns an `error` object in a chunk into an error, and
   records whether any choice was ever seen.
5. Write `internal/openrouter/client.go`: a `Client` with an API key, a base URL
   defaulting to `https://openrouter.ai/api/v1`, an HTTP client, and a logger.
   `Stream(ctx, request, onDelta)` posts to `/chat/completions` with
   `Authorization`, `Content-Type`, and `Accept: text/event-stream`, reads a
   non-2xx body into the error, feeds the body through the SSE reader, and
   returns the assembled completion together with any error so a cancelled turn
   keeps its partial text. A stream that produced no choices is an error. No
   retry.
6. Add the ACP types: `NewSessionRequest` with `cwd`, `mcpServers`, and
   `additionalDirectories`; `NewSessionResponse`; `PromptRequest`;
   `PromptResponse` with a stop reason; `CancelNotification`; `ContentBlock`
   covering `text` and `resource_link` with a `MarshalJSON` that emits each
   variant's required fields; `ContentChunk` carrying the `sessionUpdate` tag,
   the content block, and an optional `messageId`; `SessionNotification`; and
   the stop-reason constants the turn can produce. `SessionNotification.Update`
   is a `ContentChunk` today and widens when tool-call updates land.
7. Add the `Validate` methods. `cwd` must be present and absolute. `mcpServers`
   must be present, because the protocol requires the field, and must be empty,
   because MCP is not supported. `additionalDirectories` must be empty, because
   the capability is not advertised. A prompt needs a session ID and at least
   one block; each block must be `text` or `resource_link`, and a resource link
   needs both `name` and `uri`. A cancel needs a session ID. Every message says
   plainly what was wrong.
8. Add `internal/agent/session.go`: a `session` with its ID, canonical `cwd`,
   history, and the active turn's cancel function and client-cancelled flag,
   plus a registry on the `Agent` guarded by a mutex. Give the session a `claim`
   that refuses a second concurrent turn and returns a derived context and a
   release function reporting whether the client cancelled, and a `cancel` that
   is a no-op when no turn is running. Model these on Alpha's, dropping the turn
   correlation and steering it carries.
9. Add the `session/new` handler: validate, canonicalize `cwd` through
   `filepath.EvalSymlinks`, refuse when no model or API key is configured, mint
   a random hex ID, register the session, and return it.
10. Add the `session/prompt` handler: look up the session, convert the prompt to
    a user message, claim the turn, append the user message to the history, then
    run one `Stream` with a delta callback that notifies `agent_message_chunk`
    for text and `agent_thought_chunk` for reasoning under two freshly minted
    message IDs.
11. Resolve the turn's outcome. If the client cancelled, append whatever
    assistant text was streamed — the client has already displayed it, and
    dropping it would leave the model's view of the conversation behind the
    user's — and answer `cancelled`. If the handler's own context ended instead,
    return `-32800`. If the stream failed for any other reason, return the
    wrapped error. Otherwise append the assistant message and answer with the
    mapped stop reason. Release the claim on every path.
12. Add the `session/cancel` handler: fire the claimed turn's cancel function.
    Note in the code that a notification handler's error is discarded by
    `jrpc2`, so an unknown session is logged rather than reported.
13. Wire `main`: read `OX_MODEL`, `OPENROUTER_API_KEY`, and
    `OX_OPENROUTER_BASE_URL` from the environment, build the client, pass it and
    the model to the agent, and set `Concurrency` on the server options with a
    comment giving the reason.

## Documentation updates

- Mark `session/new`, `session/prompt`, and `session/cancel` as accepted in the
  ACP method coverage list.
- Note in the Tests section of `AGENTS.md` that a held-open response is how a
  mid-turn cancellation is scripted.
- Roadmap item completed: the streaming prompt turn.

## Impact assessment

- Code paths affected: adds `internal/openrouter`; adds session state and three
  handlers to `internal/agent`; extends `internal/acp`; extends
  `cmd/ox/main.go`. `Initialize` is unchanged.
- Data, protocol, or schema impact: Ox becomes a usable ACP agent for text
  conversation. Sessions are in-memory, so they do not survive the process;
  durability is a later item and nothing here should anticipate it. No
  advertised capability changes.
- Dependency or API impact: none. The model client is standard library only.
- Deliberately deferred, each to a named roadmap item, and none of them should
  be half-built here: a system prompt, which has nothing to describe until there
  are tools and workspace instructions; the tool loop and its
  `max_turn_requests` stop reason; `usage_update` notifications and cost
  accounting, though the assembler does capture and log the trailing usage
  chunk; provider retry; richer prompt content and the capabilities that gate
  it; configuration files and credential storage, which the two environment
  variables stand in for.

## Validation

- Tests to write and run: `go test -race ./...`.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: with a real `OPENROUTER_API_KEY` and `OX_MODEL`, drive
  `initialize`, `session/new`, and `session/prompt` by hand and confirm the
  answer streams and the turn ends cleanly. Repeat with a `session/cancel` sent
  mid-answer and confirm the response is a `cancelled` stop reason rather than
  an error.
