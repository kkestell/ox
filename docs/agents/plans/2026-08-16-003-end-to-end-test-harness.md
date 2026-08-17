# 2026-08-16-003. End-to-end test harness

## Goal

Almost everything that makes Ox correct is only observable from outside the
process: the framing of each stdout line, the fact that logs never reach stdout,
the JSON-RPC error codes bad input produces, the order notifications arrive in,
and the exit behavior when a client hangs up. Ox has one ad-hoc subprocess test
that rebuilds this scaffolding inline. Replace it with a harness that drives the
real `ox` binary the way a client does, and give that harness a scripted model
endpoint so later prompt-loop work is testable without a network or a key.

## Desired outcome

`go test -race ./internal/e2e` builds `ox` once, then runs each test against a
fresh subprocess. A test sends real JSON-RPC over a pipe and asserts on the
lines the process wrote back. A test can queue what the model streams and read
back the requests Ox sent it. Every read carries a deadline, and a failing test
prints the child's stderr. `cmd/ox/main_test.go` no longer exists; everything it
covered is covered here.

## Summary of approach

A new package `internal/e2e` holds the harness and the tests that use it. Every
file in it is a `_test.go` file, so the harness adds no surface to the shipped
binary while still being checked by `go vet` and `staticcheck`.

The harness is a synchronous client, not a JSON-RPC library. It owns the child's
pipes directly: writing a request is `json.Marshal` plus a newline, and reading
is one line plus `json.Unmarshal` into a `message` struct that covers all four
JSON-RPC shapes. There is no `jrpc2.Client`, no dispatcher, and no background
goroutine, so nothing can reorder what the child wrote.

Ordering is a pending queue. One function,
`readUntil(what string, pred func(message) bool)`, reads lines in wire order,
returns the first that matches, and buffers the rest for later reads. Reading a
response by id, a notification by method, or the next agent-to-client request
are all one-line callers of it. Agent-to-client requests are answered inline by
the test that expects them: read the request, write a response with the same id.

Deadlines come from the pipe rather than from goroutines. The child's stdout is
an `os.Pipe` the harness creates, so it can call `SetReadDeadline` before each
line. A timeout is a test failure, never something to recover from, so the read
path goes straight to `t.Fatalf` naming what it was waiting for.

The model is mocked at the HTTP boundary. An `httptest.Server` serves
`POST /api/v1/chat/completions` as `text/event-stream` from a queue of canned
bodies, records every request it received, and asserts on close that the queue
was drained. Tests that need no model do not start one. Mocking at the wire
means the provider client, its SSE reader, and its chunk assembly will all be
under test rather than skipped.

## Related code

- `~/src/references/repos/third-party/coding-agents/codex/codex-rs/app-server/tests/common/test_app_server.rs`
  — the model for this harness. `send_jsonrpc_message` and
  `read_jsonrpc_message` are the whole transport; `read_stream_until_message`
  plus a `VecDeque` of pending messages is the whole synchronization design;
  `Drop` does a bounded graceful shutdown before killing.
- `~/src/references/repos/third-party/coding-agents/codex/codex-rs/app-server/tests/common/mock_model_server.rs`
  and `.../tests/common/responses.rs` — a queue of canned SSE bodies with an
  exact expected call count, and the two-layer frame vocabulary: tiny `ev_*`
  constructors composed by `sse(...)`, with task-level composites like
  `create_shell_command_sse_response` above them.
- `~/src/references/repos/third-party/coding-agents/codex/codex-rs/core/tests/common/responses.rs:39-81`
  — `ResponseMock`, which records requests and exposes them for assertions after
  the turn completes.
- `~/src/references/repos/personal/alpha/runtime/internal/openrouter/sse.go`,
  `stream.go`, and `client_test.go` — the exact wire shapes the mock must emit:
  `data:` frames terminated by `[DONE]`, `choices[].delta.content`,
  `delta.reasoning`, indexed `delta.tool_calls` fragments whose `arguments`
  concatenate across chunks, a trailing usage-only chunk, and a chunk carrying
  `error`. `client.go:18-21` fixes the path as `/api/v1/chat/completions`.
- `~/src/references/repos/personal/beta/tests/e2e.rs` — the reason the child
  gets a scratch working directory: it moves `HOME` and the working directory so
  no real dotenv, cache, or session store is reachable.

## Current state

- Relevant existing behavior: `cmd/ox/main_test.go` holds
  `TestInitializeAndStdoutPurity`, which builds the binary with
  `exec.Command("go", "build")`, drives `initialize` over pipes with
  hand-written JSON, and checks stdout purity after stdin close. `internal/acp`
  and `internal/agent` supply the wire types and the two handlers it exercises.
- Existing patterns to follow: the binary is configured entirely through the
  environment, and logging goes to stderr by construction.
  `internal/acp/types_test.go` already round-trips the wire types against
  literal JSON, so e2e tests assert behavior rather than field names.
- Constraints from the current implementation: the `Makefile` targets name `cmd`
  and `./cmd/...` explicitly, so `internal` is currently unformatted and
  unchecked.

## Structural considerations

- **Hierarchy:** `internal/e2e` sits above everything, depends on `internal/acp`
  for wire types, and is depended on by nothing. Nothing in `cmd` or `internal`
  learns that it exists.
- **Abstraction:** the harness observes Ox only through the process boundary —
  stdin, stdout, stderr, the environment, the working directory, and an HTTP
  endpoint. It reaches for no internal type beyond the wire vocabulary, so it
  cannot drift into testing implementation.
- **Modularization:** one package with three concerns kept in separate files —
  the process and its client, the mock model endpoint, and the tests themselves.
  Splitting further would produce packages that only exist to be imported once.
- **Encapsulation:** because every file is a test file, the harness is
  unreachable from production code by construction rather than by convention.
- **Testability:** the harness is the testability work. Its own correctness is
  checked by the tests that use it, plus one test over the pure SSE frame
  builders.

## Test plan

- **Key behaviors to verify:**
  - `initialize` returns protocol version 1 with the advertised agent
    capabilities and agent info.
  - A client asking for an unsupported version still receives 1 as a normal
    result rather than an error.
  - A missing or non-positive `protocolVersion` produces `-32602`.
  - An unknown method produces `-32601`.
  - Malformed JSON produces `-32700` and the process answers the next
    well-formed request.
  - `$/cancel_request` naming an id that is not in flight produces no response
    and leaves the process able to answer the next request, for a string id, a
    numeric id, and null.
  - At debug log level every line the process writes to stdout parses as
    JSON-RPC, and the startup logging appears on stderr.
  - Closing stdin exits the process zero.
  - `sse` and the `ev*` builders produce the exact frame bytes the OpenRouter
    reader expects.
- **Test levels:** everything except the frame builders runs against the built
  binary through the harness. The frame builders are pure functions and get a
  table test.
- **Edge cases and failure modes:** a test that never receives an expected line
  fails on a deadline rather than hanging, and its failure output names what it
  was waiting for and includes the child's stderr. A test that receives an
  unexpected agent-to-client request fails rather than leaving the agent
  blocked. A queued model response that no test consumed fails the test at
  cleanup.
- **What not to test:** `jrpc2`'s framing, dispatch, and standard error codes;
  `httptest`'s serving; `go build`; the JSON field names already covered by
  `internal/acp/types_test.go`.

## Implementation plan

1. Create `internal/e2e` with a `TestMain` that builds `./cmd/ox` into a
   temporary directory and removes it after `m.Run`. Build the child with
   `-race` when the test binary itself was raced, selected by a pair of two-line
   files constrained on the `race` build tag, and set `GORACE=halt_on_error=1`
   on the child so a detected race becomes the nonzero exit the harness already
   asserts against.
2. Write the process half. `start(t, options...)` creates the child's stdout as
   an `os.Pipe` so reads can carry deadlines, takes stdin from `StdinPipe`, and
   gives stderr a plain `bytes.Buffer`. Close the parent's copy of the stdout
   write end right after `Start`, or the read end never sees EOF. The child
   inherits the environment with four overrides: a scratch working directory so
   the repository's `.env` is out of reach, `HOME` and `XDG_CONFIG_HOME` pointed
   at that scratch directory, `OX_LOG_LEVEL=debug`, and
   `OPENROUTER_API_KEY=test-key`.
3. Write the teardown as a `t.Cleanup`: close stdin, arm a `time.AfterFunc` that
   kills the process, `Wait`, then stop the timer. Reading the stderr buffer is
   only safe after `Wait` returns, because that is when `exec` has finished its
   copying; log it with `t.Logf` when the test failed.
4. Write the client half. A `message` struct with `ID`, `Method`, `Params`,
   `Result`, and `Error` as raw JSON covers requests, responses, errors, and
   notifications, and keeps the raw line for failure messages. `readUntil` sets
   a read deadline, reads a line, returns it if the predicate matches, and
   otherwise pushes it onto a pending slice that later reads consult first.
5. Add the callers built on `readUntil`: `request` sends with a fresh id and
   returns the result or fails on an error response, `requestError` expects the
   error, `notify` sends a notification, `notification` waits for one by method,
   `serverRequest` waits for the next agent-to-client request, and `respond`
   answers it with the same id. Add `send` for raw lines so the malformed-JSON
   test can write bytes that are not valid JSON.
6. Write the mock model endpoint in its own file. `startModel(t, bodies...)`
   serves `POST /api/v1/chat/completions`, checks the method, path, and
   `Authorization` header, decodes the body into a small request struct local to
   the harness, records it under a mutex, and writes the next queued body as
   `data:` frames with a flush after each. An exhausted queue is a `t.Errorf`
   and a 500, because `t.Fatal` is not safe off the test goroutine. Cleanup
   closes the server and fails if any queued body went unused. `requests()`
   returns a copy for assertions after the turn.
7. Add the frame vocabulary beside it: `sse(chunks...)` joining `data:` frames
   and terminating with `data: [DONE]`, and the `ev*` chunk builders for
   assistant text, reasoning text, an indexed tool-call fragment, a finish
   reason, a usage-only trailer, and an error. Add the table test over `sse` and
   the builders.
8. Add the `withModel` option that sets `OX_OPENROUTER_BASE_URL` on the child,
   and record in the plan for the provider client that this is the seam it must
   read. Nothing reads it yet, and no default model server starts.
9. Rewrite the existing coverage on top of the harness and delete
   `cmd/ox/main_test.go`: the handshake, version negotiation, invalid
   parameters, unknown method, malformed JSON followed by recovery,
   `$/cancel_request` for an unknown id in all three id shapes, stdout purity,
   and clean exit on stdin close.
10. Change the `Makefile` to cover the whole module: `gofmt -l -w .`,
    `go vet ./...`, `staticcheck ./...`, and `go test -race ./...`.

## Documentation updates

- Add `docs/agents/testing.md` covering the test levels, when to reach for the
  harness rather than a unit test, and how to queue model responses. The Tests
  section of `AGENTS.md` already directs the reader to a testing reference that
  does not exist yet.
- Correct the trailing fragment on the harness line in the roadmap.
- Roadmap item completed: the end-to-end test harness.

## Impact assessment

- Code paths affected: adds `internal/e2e`; deletes `cmd/ox/main_test.go`;
  widens four `Makefile` targets. No production code changes.
- Data, protocol, or schema impact: none to the protocol. Introduces one
  environment variable, `OX_OPENROUTER_BASE_URL`, as the seam that points Ox at
  a model endpoint other than the real one.
- Dependency or API impact: none. Everything the harness uses is standard
  library.

## Validation

- Tests to write and run: `go test -race ./...`.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: break an assertion and confirm the failure names what it
  was waiting for and prints the child's stderr; confirm a run with the
  repository's `.env` present never reaches the real OpenRouter API.
