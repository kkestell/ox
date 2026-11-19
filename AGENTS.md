# Experimental Claude Code multiplexer

Optimize for learning and code that is cheap to change.

## Status

We test with `make dev`. Ox's test state is disposable.

## Source Map

```
.
├── AGENTS.md                  # agent instructions
├── bin                        # build output
│   ├── ox
│   └── oxd
├── cmd
│   ├── ox                     # web UI server
│   │   ├── main.go            # web UI and its relay to oxd
│   │   └── web                # web client
│   └── oxd                    # daemon
│       └── main.go            # daemon startup
├── docs
│   ├── architecture.md        # how oxd and ox are built
│   ├── claude-code.md         # observed Claude Code behavior
│   ├── known-issues.md        # unfixed defects
│   ├── protocol.md            # the interface oxd serves
│   └── todo.md                # planned work
├── go.mod                     # Go module
├── go.sum                     # dependency checksums
├── internal
│   ├── agent
│   │   └── agent.go           # harness interface
│   ├── claude
│   │   ├── agent.go           # Claude agent state machine
│   │   ├── approval_test.go   # approval tests
│   │   ├── approval.go        # tool approvals
│   │   ├── claude.go          # Claude process and input
│   │   ├── configure_test.go  # configuration tests
│   │   ├── configure.go       # setting changes on a running process
│   │   ├── messages.go        # Claude output format
│   │   ├── park_test.go       # parking tests
│   │   ├── park.go            # when the process can be parked
│   │   ├── prompt_test.go     # prompt tests
│   │   └── prompt.go          # prompt lifecycle
│   ├── config
│   │   ├── config_test.go     # configuration tests
│   │   ├── config.go          # user configuration
│   │   └── default.json       # built-in configuration
│   ├── fake
│   │   └── fake.go            # echo harness
│   ├── jsonrpc
│   │   └── jsonrpc.go         # JSON-RPC messages
│   ├── paths
│   │   └── paths.go           # XDG file locations
│   ├── server
│   │   ├── client_test.go     # test client
│   │   ├── conn.go            # client connections
│   │   ├── history_test.go    # history tests
│   │   ├── history.go         # session history
│   │   ├── live_test.go       # live Claude tests
│   │   ├── methods_test.go    # method tests
│   │   ├── methods.go         # RPC methods
│   │   ├── server.go          # listener, workspaces, and sessions
│   │   └── session.go         # session goroutine and lifecycle
│   └── store
│       └── store.go           # SQLite persistence
└── Makefile                   # build, test, and run
```

## Implementation

- Keep the code direct, readable, and simple. Implement the requested behavior
  completely, using the smallest coherent design.
- Handle normal use and concrete failures. Do not build machinery for unusual
  edge cases or hypothetical failures. Fail clearly when something unsupported
  happens.
- Avoid speculative abstractions, compatibility layers, automatic recovery,
  and durability guarantees. Add them only when a current requirement earns
  their cost.
- Keep state and lifecycle ownership easy to follow. Reconsider the design when
  a change needs coordination machinery to support earlier choices.
- Treat research and prior implementations as evidence, not requirements.
  Keep unresolved protocol choices open until they need a decision.
- A session's state is owned by its goroutine, and other code reaches it only
  by posting an operation to it. A harness's own goroutines only read its
  process and fire timers, and deliver what they produce through `Events`.
- A response is queued by the owner of the state it reports, when it reads
  that state.
- A request carries only what the user changed, and `oxd` applies it to the
  current state.
- A fact inferred from a harness's tool calls or hooks names the event that
  clears it, and has a live reproduction.
- `internal/agent` changes only for what a real harness needs. Harness
  payloads pass through the server untouched.
- Session updates have one type, `agent.Update`, sent to clients and stored as
  history as is.
- The body of a commit that adds a state, timer, retry, lock, or stored field
  names the user-visible behavior that needs it and the races and cleanup it
  brings.

## Conventions

- Ox's files live in the XDG base directories, beneath an `ox` directory.
  `internal/paths` is the one place that resolves them. Whoever creates a file
  creates its directory.
- A setting or field has one name everywhere: configuration, protocol, store,
  Go, and the web client all use the protocol's name, such as
  `permission_mode`.
- `oxd` checks every request against the configuration it serves, the way it
  checks the configuration's defaults.
- Error messages from `oxd` follow Go style: lowercase, no trailing period.
  The standard JSON-RPC messages keep the specification's text.
- A log line about a session names it by its ID.
- The web client shows a request's error with `createAttempt` and asks for
  confirmation with a `<dialog>`.

## Defects

- Fix a defect once it happens in real use. Before then, fix it only when one
  of these holds:
  - Its damage reaches anything beyond Ox's own disposable state: the user's
    work, machine, accounts, or money. Examples include losing work in a
    repository or worktree, Claude acting with permissions other than the ones
    shown, and runaway spending on model usage.
  - It fails silently in ordinary use, so nobody would notice it. The fix makes
    the failure visible.
  - It comes from a structure that keeps producing defects. The fix changes the
    structure.
- A race that can leave state the design says cannot exist comes from such a
  structure, however unlikely it is. Fix it by changing who owns the state, not
  by handling the interleaving. A race between independent requests in which
  every outcome is valid is not a defect.
- Record every other confirmed defect in `docs/known-issues.md` with how often
  it occurs in real use, its worst cost, and how it was confirmed: live, from
  the source, or by an injected fault. Keep each entry current with the code.
- A review reads `docs/known-issues.md` first. It reports a recorded defect
  again only when its frequency or cost has changed. It records a new defect
  that falls below the threshold instead of proposing a fix.
- When a recorded defect happens in real use, fix it and remove its entry.

## Verification

- Keep unit tests minimal. Use them for consequential logic that benefits from
  an isolated check, not to mirror the implementation.
- Prefer small live tests against real Claude Code processes, using Haiku only.
  Check observable protocol behavior rather than exact model wording. They live
  in `TestLive` and run with `make live`.
- Keep test setup simple; avoid elaborate mocks and fake runtimes.
- `make test` and `make dev` build with the race detector. A data race fails
  the test, or stops `oxd` with the race's report.
- Report what was verified and any live checks that could not be run.

## Documentation

The code describes the implementation, and Git history records changes.
Comments explain non-obvious reasons or external rules. Each document owns the
facts listed for it:

- `AGENTS.md`: instructions for agents working anywhere in the repository.
- `cmd/ox/web/AGENTS.md`: instructions for agents working on the web client,
  and the Solid 2.0 reference.
- `docs/architecture.md`: how `oxd` and `ox` are built: their components, the
  boundaries between them, and what each owns.
- `docs/protocol.md`: the interface `oxd` serves: messages, methods, updates,
  events, and errors.
- `docs/claude-code.md`: how Claude Code behaves when driven with `claude -p`
  and stream-json, as observed.
- `docs/known-issues.md`: confirmed defects that are not fixed yet, kept as
  the Defects section describes.
- `docs/todo.md`: planned work, by priority.

Every fact lives in one place. A document that needs a fact owned by another
links to it instead of restating it. Most changes need no doc edits: edit a
document only when the change alters a fact it owns. Correct or delete a wrong
passage without expanding it. Add a document or a section only when asked.
