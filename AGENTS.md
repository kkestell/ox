# AGENTS.md

KEEP THIS FILE AND ITS LINKED REFERENCES UP TO DATE.

Ox is a coding agent written in Go that speaks ACP v1 over standard input and
output.

I have written many coding agents. The goal if Ox is to combine the best ideas
and the most advanced features from all of them.

## Roadmap

Milestones are ordered and build on each other. Items within a milestone can be
worked in parallel. Each checkbox is one plannable roadmap item.

### M0 — Skeleton and protocol boundary

- [x] Scaffold project
- [x] ACP stdio server core: `initialize`, protocol input validation,
      `$/cancel_request`

### M1 — End-to-end testing

Comes before the prompt loop so every later item lands with end-to-end coverage.

- [x] End-to-end test harness driving the Ox binary over stdio with a mock LLM

### M2 — Core prompt loop

- [x] Streaming prompt turn: `session/new`, `session/prompt`, `session/cancel`
- [x] Prompt content handling
- [x] Concurrent sessions
- [x] Configuration precedence
- [x] Credential storage and lookup
- [x] `authenticate` and `logout`

### M3 — Tools and permissions

- [ ] Workspace confinement
- [ ] File read, write, and exact edit tools
- [ ] Glob and grep tools
- [ ] Shell execution tool
- [ ] Permission requests and reusable grants
- [ ] Client-delegated filesystem and terminal: `fs/*`, `terminal/*`

### M4 — Durable sessions

- [ ] Durable session log
- [ ] `session/load` and lossless replay
- [ ] `session/list`, `session/delete`, `session/close`, `session/fork`,
      `session/resume`
- [ ] Session locking and interrupted-turn recovery
- [ ] Session-frozen request prefix and tool declarations

### M5 — Robustness and accounting

- [ ] Provider retry and streaming resilience
- [ ] Model catalog and cache
- [ ] Reasoning-detail round-trip
- [ ] Usage, cost, and context-window accounting
- [ ] Changed-file accounting
- [ ] Context compaction
- [ ] Trace telemetry

### M6 — Higher-level capabilities

- [ ] Delegated subagents and nested activity reporting
- [ ] Todo / plan tool
- [ ] Workspace instructions (`AGENTS.md`)
- [ ] On-demand skills
- [ ] `session/set_mode`
- [ ] `session/set_config_option`
- [ ] MCP support
- [ ] LLM-generated session titles
- [ ] Mid-turn steering / queued user input
- [ ] Concurrent tool execution

### M7 — Differentiators

- [ ] Ephemeral Git snapshots and rollback
- [ ] Persistent semantic workspace memory
- [ ] Automatic task decomposition with a durable subtask queue
- [ ] Language-server navigation and diagnostics
- [ ] Harbor evaluation adapter

Out of scope for Ox itself (client responsibilities under ACP): interactive UI,
Markdown rendering, IDE file/diff navigation, active-editor context attachment,
editor runtime supervision, GitHub inbox, terminal UI.

## Workflow

- Plans go in `docs/agents/plans/YYYY-MM-DD-NNN-slug.md`.
- Plans should follow the plan template: `docs/agents/plans/TEMPLATE.md`.
- Only roadmap items get plans.
- A plan is committed with the work it plans, in the same commit. A plan for a
  roadmap item that has not been implemented yet stays uncommitted.
- Before planning, research. Explore the relevant Ox code and then identify
  which of my previous projects are strong candidates for cribbing ideas and/or
  code. Consult the feature matrix in `~/src/references/index.md`. The
  repositories are in `~/src/references/repos`. Find the most robust, advanced
  solutions. Adopting large chunks of code from my previous projects is welcome
  and encouraged. If no strong candidates emerge, consult the third-party
  references in `~/src/references/repos/third-party`.
- After research and planning, stop. Implementation will happen in a fresh
  session.
- After implementation, stop. Code review will happen in a fresh session.
- Implementation is not finished until unit tests, end-to-end tests, lint, etc.
  are have all been run.
- There are no preexisting issues. If you see something, fix it.
- This is a private greenfield project with no users. There is no backwards
  compatibility concerns. Refactor ruthlessly.
- Do not mention the roadmap, milestones, or M numbers ANYWHERE except for in
  the roadmap.
- When reviewing code, focus on issues that are reasonably likely to occur in
  real world usage, not theoretical issues and extreme edge cases.

## ACP method coverage

Requests and notifications Ox accepts from an ACP client:

- [x] `initialize`
- [x] `session/new`
- [ ] `session/load`
- [x] `session/prompt`
- [x] `session/cancel`
- [x] `authenticate`
- [x] `logout`
- [ ] `session/list`
- [ ] `session/delete`
- [ ] `session/close`
- [ ] `session/fork`
- [ ] `session/resume`
- [ ] `session/set_mode`
- [ ] `session/set_config_option`
- [x] `$/cancel_request`

Requests and notifications Ox sends to an ACP client:

- [x] `session/update`
- [x] `session/request_permission`
- [ ] `fs/read_text_file`
- [ ] `fs/write_text_file`
- [ ] `terminal/create`
- [ ] `terminal/output`
- [ ] `terminal/wait_for_exit`
- [ ] `terminal/kill`
- [ ] `terminal/release`

## Configuration

Ox reads the model for each session from `OX_MODEL`, from
`<cwd>/.ox/config.json`, or from the global configuration file at
`$XDG_CONFIG_HOME/ox/config.json` (falling back to
`$HOME/.config/ox/config.json`). That order is the precedence order: the
environment overrides the workspace file, which overrides the global file. The
resolved configuration is frozen when the session is created.

Ox reads the OpenRouter credential from `OPENROUTER_API_KEY` or from the OS
keyring under service `ox` and account `openrouter`. The environment wins when
both have a credential. `OX_KEYRING_DISABLED=1` turns keyring access off.
`ox login` verifies a key with OpenRouter and stores it in that keyring entry.
The ACP `authenticate` method re-reads both sources and verifies the resolved
credential. `logout` deletes the keyring entry and is refused while
`OPENROUTER_API_KEY` supplies the credential.

`model` is the only configuration-file key. Unknown keys and blank model values
are errors. `OX_LOG_LEVEL` and `OX_OPENROUTER_BASE_URL` remain environment-only;
in particular, a configuration file cannot redirect prompts to another host or
carry a credential.

## Code Style

### “Just Enough” Go

The value of this code is how fast it can be rewritten tomorrow, so minimize
committed surface area. When in doubt, do less.

Note the split of concerns: the domain — the rules, the semantics, the thing
being modeled — deserves care and fidelity to its source of truth. The Go
implementing it should stay as thin and boring as possible.

### Keep correctness; skip speculative robustness

Use Go’s cheap safety: static types, useful zero values, `go vet`,
`staticcheck`, and `go test -race`.

Do not build retries, fallbacks, error taxonomies, configuration layers,
concurrency, or abstraction for cases that do not exist yet. Prefer a `TODO`,
panic, or unsupported case over machinery built on guesses.

### Boundaries vs. invariants

Bad external input is ordinary input. Validate it at the boundary and report
clear human-readable problems with a nonzero exit.

Past that boundary, missing values and impossible states are bugs. **Panic
rather than silently recovering or substituting zero values.**

### Keep the implementation concrete

Prefer:

- values over pointers until mutation or sharing requires them
- concrete types until multiple real implementations justify an interface
- plain functions and `switch` over visitors, registries, or generic frameworks

Abstractions should be discovered through repetition, not imposed up front.

### Errors

User-facing validation problems are data: location/source plus a human-readable
message.

Everything else is plumbing: return `error` and wrap it when adding useful
context. Do not define custom error types or sentinels unless code actually
branches on them.

### Tests

Testing is part of the design. Add the smallest useful test at the lowest
appropriate level and use real Ox boundaries wherever practical.

End to end tests with a mocked LLM are the gold standard. The harness in
`internal/e2e` builds the `ox` binary, starts a fresh process per test, and
drives it the way a client does: stdin, stdout, stderr, the environment, the
working directory, and a queued HTTP model endpoint. Every file in the package
is a test file, so the harness adds nothing to the shipped binary. Compose
scripted model responses from the `sse` and `ev*` builders rather than
hand-writing SSE framing. Seed configuration and other files into the scratch
directory with the harness file options before Ox starts. A queued response may
name the prompt it answers, so tests with more than one turn in flight do not
depend on request arrival order. A mid-turn cancellation is scripted with a
held-open response: `hold` queues a body that writes its opening frames, signals
the test that it has started, then blocks until the test releases the rest or
the request context ends.

The end-to-end harness disables keyring access, so tests of the shipped binary
supply credentials through the environment. Keyring behavior is covered by unit
tests against go-keyring's in-memory provider. Unless a test starts the mock
model, the harness points Ox at a refused local provider address.

There is an `OPENROUTER_API_KEY` in `.env` for you to use for testing. Checks
against the real endpoint use `gpt-5.6-luna` and no other model.

Add focused tests for stable, tricky rules and regression cases for fixed bugs.

The end to end harness builds the `ox` binary in a subprocess, which Go's test
cache cannot see, so run the tests with `-count=1`.

Always run:

```sh
gofmt
go vet ./...
staticcheck ./...
go test -race -count=1 ./...
```

### Design and collaboration

Do not introduce dependencies, abstractions, subsystems, protocols, storage
formats, background processes, configuration, or significant behavior changes
without consulting the user. Resolve routine implementation details directly
when they follow from an agreed design.
