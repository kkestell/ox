# Experimental Claude Code multiplexer

This is an experiment. Optimize for learning and code that is cheap to change,
not production reliability.

## Status

Not in production use: just testing using `make dev`. There is no data or state
that cannot be deleted if necessary.

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
