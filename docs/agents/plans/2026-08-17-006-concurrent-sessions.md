# 2026-08-17-006. Concurrent sessions

## Goal

A client opens several sessions on one Ox process and expects to run them at
once. Ox already keeps each session's history and turn state to itself, so the
data is separate. What is not separate is the handler slot.

A prompt turn runs inline on its jrpc2 handler goroutine and holds that
goroutine for the whole turn, and jrpc2 bounds handlers with a semaphore
acquired inside every handler invocation. `cmd/ox/main.go` sets that bound to
16. Once sixteen turns are in flight, the seventeenth message waits for a slot —
and so does `session/cancel`, and so does `$/cancel_request`. The only thing
that would free a slot is the cancellation that cannot run, so the process stops
answering until a provider stream ends on its own.

The failure is worse than a delay because of jrpc2's notification barrier. The
read loop calls `nbar.Wait()` before dispatching each batch, so once one
notification handler is stuck on the semaphore, no further request is dispatched
at all.

This is not theoretical. A jrpc2 server with `Concurrency: 2`, two long-running
calls in flight, and a notification behind them never runs the notification.
Raising the bound runs it immediately.

## Desired outcome

A client runs turns in as many sessions as it created, all at once, and every
one of them makes progress.

Each session's `session/update` notifications carry that session's ID and that
session's message IDs. Two sessions streaming at the same time interleave on the
wire, which is what a client demultiplexing by `sessionId` expects, and neither
one's chunks appear under the other's ID.

Every session's updates still arrive before that session's own `session/prompt`
response. Another session's traffic may appear between them.

`session/cancel` for one session ends that turn with `cancelled` and leaves
every other turn streaming. Its session takes another prompt afterwards, and the
untouched sessions finish with `end_turn`.

`session/new`, a prompt in a fresh session, and a cancellation all answer while
any number of turns are in flight. There is no count at which the process stops
responding.

A second prompt in a session that is already running one is still refused with
`-32602`. Per-session serialization is the existing rule and it does not change.

## Summary of approach

Three changes, in three places, none of them large.

**The handler bound goes.** jrpc2's `Concurrency` option exists to bound
parallel work in handlers, and it is the right knob for handlers that compute.
Ox's handlers do not compute: a prompt handler blocks on the provider's socket
and on the client, and a notification handler does a map lookup. The thing that
actually bounds concurrent turns is the number of sessions the client chose to
open. So `cmd/ox/main.go` sets the bound high enough that it can never be what
stops a request, and says why in a comment that names the hazard rather than
guessing a session count.

This makes explicit an invariant Ox must keep: **notification handlers must
never block.** jrpc2 dispatches each batch only after every previously issued
notification handler has returned, so a slow `session/cancel` would stall the
read loop for every session. `Cancel` and `CancelRequest` are a lookup and a
context cancel today, and the plan's job is to record that this is load-bearing,
not incidental.

**Agent-level state becomes safe to touch from concurrent handlers.**
`Agent.clientCapabilities` is written by `Initialize` with no synchronization.
Nothing reads it yet, so there is no race today, but two `initialize` calls in
flight together are a write-write race that the e2e harness — which builds ox
with `-race` and sets `GORACE=halt_on_error=1` — would turn into a crash. It
becomes an `atomic.Pointer[acp.ClientCapabilities]`. Everything else the agent
shares is already accounted for: the sessions map has `sessionsMu`, session
history and turn state go through `claim`, `cancel`, `appendMessage`,
`truncateHistory`, and `messages`, and `openrouter.Client`'s fields are set once
at startup and only read.

**The harness learns to script more than one turn at a time.** The mock model
hands out queued responses in arrival order, which is exactly the thing that
stops being deterministic when two turns are in flight. It gains content
routing: a queued response may name the prompt it answers, and the handler picks
the first unconsumed response that matches the request's final message. The
existing unrouted responses keep matching anything, so every sequential test is
unchanged. `updates` becomes session-scoped, because draining every buffered
notification is wrong once two sessions are producing them.

Nothing about how a turn runs changes. The turn stays inline on the handler
goroutine, because ACP answers `session/prompt` when the turn ends and jrpc2 has
no way to answer a request from anywhere but its handler. Beta reaches the same
place from the other direction: it spawns the turn and moves the responder into
the spawned task, precisely so the event loop stays free for `session/cancel`
and other sessions. Ox's event loop is already free; it is the semaphore behind
it that is not.

## Related code

- `~/src/references/repos/personal/beta/src/acp.rs:1-42` — the module comment
  states the target directly: "One process hosts N concurrent sessions", and the
  prompt turn "is offloaded with `ConnectionTo::spawn` so it never blocks the
  event loop (leaving `session/cancel` and other sessions live)". `:306-359` is
  the handler: validate, mark busy, hand the responder to a spawned task. The
  busy flag is the same one-turn-per-session rule Ox already has in
  `session.claim`.
- `~/src/references/repos/personal/alpha/runtime/cmd/amber-runtime/main.go:52-56`
  — the same `Concurrency: 16` Ox inherited, with the same latent hazard. Alpha
  is where the number came from and it is wrong in both places.
- `~/src/references/repos/personal/alpha/runtime/internal/agent/agent.go:1088-1134`
  — `session.claim`, the fuller version of Ox's: a per-turn id,
  `cancelledByClient` as an `atomic.Bool`, and a `done` channel so `close` can
  wait out the turn it cancelled. Ox's simplification is already faithful;
  nothing here needs porting for this item.
- `/Users/kyle/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/server.go:370-378`
  — `invoke` acquires the semaphore around every handler call, notifications
  included. This is the bottleneck.
- `…/jrpc2@v1.3.5/server.go:236-243` — `dispatchLocked` calls `waitForBarrier`
  before returning the dispatcher, so the read loop waits for outstanding
  notification handlers. This is why a blocked notification stalls everything.
- `…/jrpc2@v1.3.5/server.go:474-484` and `:277-297` — `pushReq` and `deliver`
  both write under `s.mu`, so concurrent `session/update` notifications and
  responses never interleave mid-line, and a session's updates always precede
  its own response. No work needed; worth knowing before someone invents a write
  lock Ox does not need.
- `…/jrpc2@v1.3.5/server.go:665-693` — `filterBatchLocked` delivers a client's
  response to a pending `Callback` from inside the read loop, deliberately
  bypassing the queue and the barrier. `session/request_permission` will not
  deadlock on the semaphore when it arrives.
- `~/src/references/repos/third-party/coding-agents/gemini-cli/packages/cli/src/acp/acpSession.ts:312-314`
  — a second prompt aborts the first (`this.pendingPrompt?.abort()`) instead of
  being refused. Recorded as the road not taken: Ox and Beta both refuse, and
  `TestPromptRefusesASecondConcurrentTurn` pins that.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/docs/get-started/architecture.mdx:20`
  — "Each connection can support several concurrent sessions, so you can have
  multiple trains of thought going on at once." The spec asserts the capability
  and says nothing further about it, so the observable contract is the one in
  Desired outcome.

## Current state

- Relevant existing behavior: `Agent` holds `sessions` behind `sessionsMu`, and
  a `session` holds its history and active turn behind its own `mu`. `claim`
  refuses a second turn in one session and hands back a context plus a release
  that reports whether the client cancelled. `Prompt` runs the stream inline and
  notifies through `jrpc2.ServerFromContext(ctx).Notify`, which writes before it
  returns. None of this needs to change.
- Existing patterns to follow: locking lives on `session` with the comment in
  `internal/agent/session.go` explaining why — "reachable only through the
  methods below, which keep the locking honest". New shared state should follow
  that, not spread a mutex across call sites.
- Constraints from the current implementation: `mockModel.serveHTTP` pops
  `m.responses[m.next]` on arrival, and `m.received` records arrival order, so a
  test with two turns in flight cannot say which body answers which prompt or
  index into `requests()`. `updates(t, child)` drains every buffered
  `session/update` regardless of session.
- The harness is already close: `process.begin` sends without waiting,
  `readUntil` buffers everything that is not what it was asked for, and
  `pending` keeps arrival order. Two prompts in flight and interleaved
  notifications are readable today from the single test goroutine; only the
  model side and the update helper are missing.

## Structural considerations

- **Hierarchy:** unchanged. `cmd/ox` owns transport configuration and is the
  only place that knows jrpc2 has a concurrency knob. `internal/agent` owns
  session state. Neither learns anything about the other.
- **Abstraction:** the bound is a transport concern, so it stays in `main.go`
  with the reasoning next to it. The agent does not gain a "max concurrent
  turns" setting; the honest bound is the client's session count, and inventing
  a number to enforce would be machinery for a case that does not exist.
- **Modularization:** no new package and no new type in `internal/agent`. The
  mock model grows one field on `modelResponse` and one lookup; the harness
  grows nothing.
- **Encapsulation:** `clientCapabilities` stops being a bare field and becomes
  one that can only be read and written atomically, which is the property the
  concurrent boundary needs. The session's locking discipline is untouched.
- **Testability:** every claim in Desired outcome is observable at the process
  boundary — two prompts in flight over one stdio pipe, with the mock model
  holding both streams open. The bound's removal is observable as "more
  concurrent turns than any handler bound would allow, all of them answering".
  The one invariant that is not directly observable, that notification handlers
  never block, is enforced by keeping them the two-line functions they are.

## Refactoring

- `updates(t, child)` in `internal/e2e/prompt_test.go` becomes
  `updates(t, child, session)` and drains only that session's notifications.
  Every existing call site already has the session in scope, so this is
  mechanical, and it removes the need for a second session-scoped helper beside
  a session-blind one.
- `chunks(t, sent, kind, session)` drops its `session` parameter and its
  session-mismatch check, which `updates` now guarantees. Sequence it after the
  `updates` change.

## Test plan

- **Key behaviors to verify:**
  - Two sessions stream at the same time. With both provider responses held
    open, each session's chunks carry that session's ID, the two sessions'
    message IDs differ, and both prompts answer `end_turn`.
  - Each session's updates arrive before its own response. Reading a session's
    response and then finding all of its updates already buffered is the
    assertion; another session's updates appearing among them is expected and
    must not fail the test.
  - Cancelling one session mid-stream ends that turn with `cancelled` while the
    other turn is still held open, and the other turn then finishes `end_turn`
    with its full answer. The cancelled session's next prompt sees the partial
    answer in its history; the other session's next prompt sees its own complete
    one.
  - Histories stay separate. Two sessions each take two prompts, interleaved,
    and each second request carries only its own session's exchange.
  - More concurrent turns than any handler bound stay responsive. With a number
    of turns in flight comfortably above both `runtime.NumCPU()` and the bound
    Ox previously set, a `session/new` round trip still answers, and a
    `session/cancel` aimed at one of the held turns still ends it with
    `cancelled`. This is the regression test for the semaphore; it hangs until
    the bound is raised.
  - The mock model routes by content, not arrival. Two requests sent
    concurrently, in either order, each receive the body queued for their
    prompt.
  - `session.claim` admits exactly one of many concurrent claimants, and the
    losers all get the refusal error.
- **Test levels:** the session behaviors are e2e against the built binary, since
  the thing under test is what the process does over stdio with several streams
  live. `claim` gets a focused unit test in `internal/agent`, and `Initialize`
  gets one that calls it from several goroutines so `-race` can see the field.
  The routing change gets a unit test in the e2e package beside the existing
  `TestMockModel*` tests.
- **Edge cases and failure modes:** a cancelled session and a running session
  releasing at nearly the same moment; a session cancelled before its provider
  stream produced any delta while another session is mid-stream; a prompt
  refused for an unroutable content block in one session leaving another
  session's running turn untouched.
- **What not to test:** how many sessions the process can hold before the
  machine runs out of sockets; the wire interleaving order of two sessions'
  updates, which is timing and not contract; that jrpc2 serializes its own
  writes.

## Implementation plan

1. Replace `Concurrency: 16` in `cmd/ox/main.go` with a named constant set high
   enough never to bind, and rewrite the comment: jrpc2's bound is meant for
   handlers that compute, a prompt handler holds its slot for the whole turn
   while blocked on the provider and the client, and if the bound is ever
   reached the cancellation that would free a slot is itself queued behind it.
   Note that concurrent turns are bounded by the sessions the client opened.
2. Change `Agent.clientCapabilities` to
   `atomic.Pointer[acp.ClientCapabilities]`, store in `Initialize`, and update
   `TestInitializeRetainsClientCapabilities` to read through `Load`.
3. Add the comment on `Cancel` and `CancelRequest` recording that a notification
   handler must not block, because jrpc2 holds the read loop until every issued
   notification handler returns.
4. Give `modelResponse` a `prompt` field and add `queueFor(prompt, body)` and
   `holdFor(prompt, opening)` to `mockModel`, with `queue` and `hold` becoming
   the unrouted cases. In `serveHTTP`, replace the `m.next` cursor with a scan
   for the first unconsumed response whose `prompt` is empty or equals the text
   of the request's final message — which is always the new user message, since
   history precedes it. Keep the "more requests than queued responses" error and
   the cleanup check for unused bodies, both now phrased in terms of unmatched
   responses.
5. Add `mockModel.requestFor(prompt)` returning the recorded request whose final
   message carries that text, since `requests()` is ordered by arrival and
   arrival order stops being meaningful.
6. Apply the two harness refactors: session-scoped `updates`, and `chunks`
   without its session parameter.
7. Add the e2e tests from the test plan to `internal/e2e/prompt_test.go`, and
   the responsiveness test that runs more concurrent turns than any handler
   bound.
8. Add the `internal/agent` unit tests for concurrent `claim` and concurrent
   `Initialize`, and the mock model routing test in `internal/e2e`.

## Documentation updates

- `AGENTS.md`: check off the concurrent sessions roadmap item. No ACP method
  coverage changes — this adds no methods.
- `AGENTS.md` Tests section: the paragraph describing the harness names `sse`,
  the `ev*` builders, and `hold`. Add that a queued response may name the prompt
  it answers, so tests with more than one turn in flight do not depend on which
  request arrives first.

## Impact assessment

- Code paths affected: the jrpc2 server options in `cmd/ox/main.go`, the
  capabilities field in `internal/agent/agent.go`, and the e2e mock model and
  update helpers. No change to the prompt turn, the session type, or the
  OpenRouter client.
- Data, protocol, or schema impact: none. No message changes shape and no method
  is added. The observable difference is that Ox keeps answering under load it
  previously stopped answering under.
- Dependency or API impact: none. `sync/atomic` is standard library.
- Deliberately deferred, and none of it half-built here: a cap on concurrent
  turns or sessions, which would be a number invented for a problem that has not
  appeared; removing finished sessions from the map, which belongs with
  `session/close`; per-session provider connection limits; and running a session
  turn anywhere other than its handler goroutine, which nothing needs until a
  turn has to outlive its request.

## Validation

- Tests to write and run: `go test -race -count=1 ./...`. The e2e harness builds
  ox with `-race` and `GORACE=halt_on_error=1`, so a concurrent-session test is
  also the race check on the agent.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: with a real `OPENROUTER_API_KEY` and `OX_MODEL`, drive
  `initialize`, several `session/new` calls, and a `session/prompt` in each by
  hand, confirm the answers stream interleaved under the right session IDs,
  cancel one mid-stream, and confirm the rest finish.
