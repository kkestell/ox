# 2026-08-16-003. End-to-end test harness

## Goal

Almost everything that makes Ox correct is only observable from outside the
process: the framing of each stdout line, the fact that logs never reach stdout,
the JSON-RPC error codes bad input produces, the order notifications arrive in,
and the exit behavior when a client hangs up. Ox has one ad-hoc subprocess test
that rebuilds this scaffolding inline. Replace it with a single harness that
drives the real `ox` binary the way a client does, and give that harness a
scripted model endpoint so later prompt-loop work is testable without a network
or a key.

## Desired outcome

`go test -race ./internal/e2e` builds `ox` once, then runs each test against a
fresh subprocess in an environment that contains nothing from the developer's
machine. A test sends real JSON-RPC over a pipe and asserts on the exact lines
the process wrote back, in wire order. A test can script what the model streams
and read back the requests Ox sent. When a test fails it prints the full
bidirectional wire log and the process's stderr. `cmd/ox/main_test.go` no longer
exists; everything it covered is covered here.

## Summary of approach

A new package `internal/e2e` holds the harness and the tests that use it. Every
file in it is a `_test.go` file, so the harness adds no surface to the shipped
binary while still being checked by `go vet` and `staticcheck`.

`TestMain` builds `./cmd/ox` once into a temporary directory and removes it
afterward. Each test calls `start`, which launches that binary, attaches a
`jrpc2.Client` to its pipes over `channel.Line`, and registers cleanup that
closes stdin and waits for exit.

The client's channel is wrapped. `channel.Channel` guarantees that records are
received in the order they were sent, and jrpc2's read loop calls `Recv`
serially, so the wrapper is the one place that sees exact wire order. It records
every line in both directions, checks that each inbound line is valid JSON, and
appends `session/update` notifications to an ordered log that tests wait on.
Notification capture deliberately does not go through jrpc2's `OnNotify` hook:
jrpc2 delivers each received batch from its own goroutine, so hook order is not
the order the lines arrived in.

The model is mocked at the HTTP boundary rather than behind a Go interface. An
`httptest.Server` serves `POST /api/v1/chat/completions` as `text/event-stream`,
and the child is pointed at it with `OX_OPENROUTER_BASE_URL`. A test supplies a
function from the decoded request to the raw SSE data payloads to stream back.
Mocking at the wire means the provider client, its SSE reader, and its chunk
assembly are all under test rather than skipped.

## Related code

- `~/src/references/repos/personal/alpha/runtime/integration/runtime_test.go` — the
  closest existing model: `TestMain` builds the binary once, a small process
  type owns stdin/stdout/stderr, and separate tests cover the handshake, stdout
  purity at debug level, malformed input, and clean exit on stdin EOF. Its
  stderr buffer is written by `exec` and read by the test without a lock; the
  harness here guards it.
- `~/src/references/repos/personal/gamma/cmd/client/main.go` — a real client driving
  the real server binary over `channel.Line`: a channel wrapper that traces both
  directions, a `jrpc2.Client` with a callback dispatcher, notifications
  restored to wire order before anything stateful consumes them, and wait
  helpers that all carry deadlines. The structure to follow.
- `~/src/references/repos/personal/beta/tests/e2e.rs` — spawns the real agent binary
  against a mock OpenRouter server selected by an env var, with `HOME`, the XDG
  directories, and the working directory all moved to a scratch dir so no real
  config, dotenv, cache, or session store is reachable. The isolation rules to
  copy.
- `~/src/references/repos/personal/alpha/runtime/internal/openrouter/sse.go`,
  `stream.go`, and `client_test.go` — the exact wire shapes the mock must emit:
  `data:` frames terminated by `[DONE]`, `choices[].delta.content`,
  `delta.reasoning`, indexed `delta.tool_calls` fragments whose `arguments`
  concatenate across chunks, a trailing usage-only chunk, and a chunk carrying
  `error`.
- `~/src/references/repos/personal/gamma/internal/checker/checker.go` — a client-side
  conformance checker that validates a notification stream against lifecycle
  rules without consulting server state. There are no session updates to check
  yet; this is the shape to grow the harness into once there are.
- `~/src/references/repos/personal/gamma/internal/llm/fake.go` — a fake model that is
  a pure function of the conversation, so a restarted agent replaying the same
  history behaves identically. The shape to adopt when replay tests need a
  script that survives a restart.
- `~/src/references/repos/personal/alpha/runtime/integration/agent_loop_test.go` — the
  assertions worth porting once a prompt loop exists. It drives the agent
  in-process through `server.NewLocal`, which is exactly what this harness does
  not do.
- `.env` — holds a real `OPENROUTER_API_KEY`. It is the concrete reason the
  child process gets a scratch working directory rather than inheriting the
  repository root.

## Current state

- Relevant existing behavior: `cmd/ox/main_test.go` builds the binary with
  `exec.Command("go", "build")` inside the test, runs it with a hand-built
  environment, and asserts stdout is empty and stderr carries the startup line.
  The protocol boundary lands before this work, so `internal/acp` request and
  response types are available for encoding and decoding.
- Existing patterns to follow: the binary is configured entirely through the
  environment, and logging goes to stderr by construction.
- Constraints from the current implementation: `format-go` in the `Makefile`
  only formats `cmd`, so a new `internal` package would go unformatted.
  `go test -race ./...` is the standard command, and the harness must be
  race-clean under it.

## Structural considerations

- **Hierarchy:** `internal/e2e` sits above everything, depends on `internal/acp`
  for wire types, and is depended on by nothing. Nothing in `cmd` or `internal`
  learns that it exists.
- **Abstraction:** the harness observes Ox only through the process boundary —
  stdin, stdout, stderr, the environment, the working directory, and an HTTP
  endpoint. It reaches for no internal type beyond the wire vocabulary, so it
  cannot drift into testing implementation.
- **Modularization:** one package with three concerns kept in separate files —
  process control and the ACP client, the mock model endpoint, and the tests
  themselves. Splitting further would produce packages that only exist to be
  imported once.
- **Encapsulation:** because every file is a test file, the harness is
  unreachable from production code by construction rather than by convention.
- **Testability:** the harness is the testability work. Its own correctness is
  checked by the tests that use it, plus one test that exercises the mock
  endpoint directly with an `http.Client`.

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
    and leaves the process able to answer the next request.
  - At debug log level every line the process writes to stdout parses as
    JSON-RPC, and the startup logging appears on stderr.
  - Closing stdin exits the process zero.
  - The mock model endpoint serves the scripted SSE frames and records the
    request body it received.
- **Test levels:** all of these run against the built binary through the
  harness. The mock endpoint is additionally exercised directly by an
  `http.Client` in the same package, because nothing in Ox calls it yet.
- **Edge cases and failure modes:** a test that never receives an expected line
  must fail on a deadline rather than hang, and its failure output must include
  the wire log and stderr. A test that receives an unexpected agent-to-client
  request must fail rather than silently return an error to the agent. A test
  that triggers no model call must fail if one arrives.
- **What not to test:** jrpc2's framing, dispatch, and standard error codes;
  `httptest`'s serving; `go build`.

## Implementation plan

1. Create `internal/e2e` with a `TestMain` that builds `./cmd/ox` into a
   temporary directory and removes it after `m.Run`. Build the child with
   `-race` when the test binary itself was built with the race detector,
   selected by a two-line pair of files constrained on the `race` build tag, and
   set `GORACE=halt_on_error=1` on the child so a detected race becomes a
   nonzero exit the harness already asserts against.
2. Write the process half of the harness. `start` takes options, launches the
   binary, and returns a handle. The child's environment is built from empty
   rather than inherited: `PATH` and `TMPDIR` carried over, `HOME` and the XDG
   config, data, and cache directories pointed at a per-test scratch directory,
   `OX_LOG_LEVEL=debug`, `OPENROUTER_API_KEY=test-key`, and
   `OX_OPENROUTER_BASE_URL` naming the mock endpoint. The child's working
   directory is the scratch directory, so the repository's `.env` is out of
   reach. Stderr is captured through a mutex-guarded writer.
3. Write the channel wrapper. It delegates to
   `channel.Line(childStdout, childStdin)`, records each outbound and inbound
   line into an ordered log, fails the test if an inbound line is not valid
   JSON, and appends `session/update` notification params to a separate ordered
   slice, waking waiters.
4. Attach a `jrpc2.Client` over the wrapper. `OnCallback` dispatches
   agent-to-client requests to handlers the test registered by method name, and
   any method with no handler fails the test and returns `-32601`. Leave
   `OnNotify` unset; the wrapper owns notification capture.
5. Add the wait helpers: a call helper that encodes params and decodes results
   with a deadline, and an update helper that blocks until a recorded update
   satisfies a predicate or the deadline passes. Every timeout message names
   what was being waited for.
6. Add failure diagnostics with `t.Cleanup`: when the test failed, dump the
   recorded wire log and the captured stderr. Do this from cleanup rather than
   logging as lines arrive, so no goroutine logs after the test has finished.
7. Write the mock model endpoint. An `httptest.Server` handles
   `POST /api/v1/chat/completions`, verifies the method, path, and
   `Authorization` header, decodes the body into a small request struct local to
   the harness, records it, and calls the test's script function. The script
   returns raw SSE data payloads, which the handler writes as `data:` frames
   followed by `data: [DONE]`, flushing after each. The default script fails the
   test, so an unexpected model call is caught. Add small helpers that build the
   common chunks: assistant text, reasoning text, an indexed tool-call fragment,
   a finish reason, and a usage-only trailer.
8. Rewrite the existing coverage on top of the harness and delete
   `cmd/ox/main_test.go`: the handshake, version negotiation, invalid
   parameters, unknown method, malformed JSON followed by recovery,
   `$/cancel_request` for an unknown id, stdout purity, and clean exit on stdin
   close.
9. Add the direct test of the mock endpoint that drives it with an `http.Client`
   and asserts the frames and the recorded request.
10. Change `format-go` in the `Makefile` to format the whole module rather than
    `cmd`.

## Documentation updates

- Add `docs/agents/testing.md` describing the test levels, when to reach for the
  harness rather than a unit test, and how to script the model. The Tests
  section of `AGENTS.md` already directs the reader to a testing reference that
  does not exist yet.
- Correct the trailing fragment on the harness line in the roadmap.
- Roadmap item completed: the end-to-end test harness.

## Impact assessment

- Code paths affected: adds `internal/e2e`; deletes `cmd/ox/main_test.go`; edits
  one `Makefile` target. No production code changes.
- Data, protocol, or schema impact: none to the protocol. Introduces one
  environment variable, `OX_OPENROUTER_BASE_URL`, as the seam that points Ox at
  a model endpoint other than the real one. Nothing reads it until the provider
  client exists; the harness sets it now so that work needs no harness change.
- Dependency or API impact: none. `jrpc2` is already a direct dependency and
  everything else is standard library.

## Validation

- Tests to write and run: `go test -race ./...`.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: run `go test -race ./internal/e2e -v` and confirm the
  wire log appears for a deliberately broken assertion; confirm a run with the
  repository's `.env` present never reaches the real OpenRouter API.
