# Review: each Claude agent phase carries its own fields

## Scope and coverage

Reviewed the uncommitted changes to `internal/claude/agent.go`,
`internal/claude/prompt.go`, `internal/claude/configure.go`,
`internal/claude/configure_test.go`, `internal/claude/prompt_test.go`, and
`docs/architecture.md` on base `f7d4459`, against
`agents/plans/2026-11-20-003-agent-phase-values.md`. The uncommitted changes
to `.claude/skills/ox-plan/SKILL.md` and `.claude/skills/ox-review/SKILL.md`
are outside the scope. Callers traced: `internal/server/session.go`
(`sendPrompt`, inbox delivery) and `internal/server/methods.go`
(`configureSession`'s `done`).

The change delivers the plan's Construction decision as written: the `phase`
interface with `available`, `*prompting`, `*configuring`, `timedOut`, and
`exited`; `prompt`, `turn`, `config`, `submission`, and the empty-`requestID`
marker are gone, and every state the Goal lists is no longer representable.
Updates, their order, their `message_id`s, and the busy rules trace unchanged.
The two reported deviations are sound: `waiting(id)` holds the one rule
`commandStarted` and `commandCancelled` share, and setting `reported` in the
`*prompting` case is the plan's "unless the phase was `*prompting`". The
`docs/architecture.md` edits match the plan's Documentation updates word for
word. No requirement changes, as the plan says.

Gaps: none.

## Summary

| ID | Severity | Category     | Action   | Status   | Title                                                        |
| -- | -------- | ------------ | -------- | -------- | ------------------------------------------------------------ |
| F1 | Medium   | Testing      | Fix      | Resolved | No test guards a taken prompt's `cancelled` changing nothing |
| F2 | Low      | Testing      | Fix      | Resolved | No test guards one `exited` update for a prompt's crash      |
| F3 | Low      | Construction | Rejected | —        | `waiting` checks for an empty ID oxd never assigns           |

## F1. No test guards a taken prompt's `cancelled` changing nothing

Medium · Testing · Fix · Resolved · `internal/claude/prompt.go:84`

`waiting(id)` matches only while `p.queued` is set. That check is what keeps
the `command_lifecycle cancelled` Claude sends for a taken prompt whose turn an
interrupt stopped ([Interrupt](../../docs/claude-code.md#interrupt), and
[`command_lifecycle`](../../docs/claude-code.md#command_lifecycle)) from publishing
`prompt_cancelled` for a prompt the user already saw delivered. Interrupting a
running prompt with nothing queued, the everyday case, produces exactly that
sequence.

The base code enforced this with a nil `queued` pointer; the change replaces
it with a boolean beside `input`, as the plan decided, and no test now fails
when the boolean is ignored.

### Evidence

Mutation: `ok && p.queued && id` → `ok && id` in `waiting`. `go test
./internal/claude` passes. The only test sending `cancelled` for a taken
prompt, `TestStopDropsQueuedPrompt`, does so while a different prompt is
queued, so the ID mismatch rejects it before the `queued` check matters.

### Action

Keep a harness test: in `TestStopDropsQueuedPrompt`'s second block (the
interrupt with nothing queued, after `promptingAgent`), deliver
`command_lifecycle cancelled` for the taken prompt and assert it publishes no
update, and that the following `idle` releases with a `state` update naming
that prompt.

## F2. No test guards one `exited` update for a prompt's crash

Low · Testing · Fix · Resolved · `internal/claude/agent.go:260`

When Claude exits on its own while a prompt holds the agent, `exit` ends the
prompt with the `exited` update naming it and must not publish a second,
unprompted `exited` update. The change rewrites the condition that decides
this (`reported = true` in the `*prompting` case, one of the reported
deviations). No test reaches it: `TestUnexpectedExitIsReported` exits from
`available`, and `TestStopDropsQueuedPrompt`'s exit is a requested stop, where
`stopping` suppresses the second update anyway.

### Evidence

Mutation: delete `reported = true`. `go test ./internal/claude` passes. With
the mutation, an unexpected exit during a prompt publishes two `exited`
updates.

### Action

Keep a harness test in `TestUnexpectedExitIsReported`: send a prompt with
`testAgent`, deliver `processExit` with an error, and assert exactly one
`exited` update, naming the prompt's `message_id`.

## F3. `waiting` checks for an empty ID oxd never assigns

Low · Construction · Rejected · `internal/claude/prompt.go:84`

`waiting` tests `id != ""` although every input reaching `Send` carries a
`rand.Text()` `MessageID` (`sendPrompt` and `acceptMessage` in
`internal/server/session.go`), so an empty ID can never equal the queued one.

### Action

Rejected: `agent.Input` does not make the ID non-empty, so the clause is the
only thing stating that an empty `command_uuid` matches nothing, and it costs
one comparison. Removing it buys nothing a test or reader would notice.

## Checks run

- New assertions against base `f7d4459` in a detached worktree (removed):
  `TestConfigureTimeoutStopsProcess` fails there ("configuration ended before
  the process exited: <nil>"), as the plan intends.
  `TestStopDropsQueuedPrompt` passes there, as expected for a guard on
  preserved behavior; the mutation below shows it protects the release name.
- Mutations of conditions the change adds, each reverted:
  - release names `p.turn` instead of `p.input.MessageID`: fails
    `TestStopDropsQueuedPrompt`, `TestPromptSentWhileClaudeWorks`,
    `TestTurnAfterPromptResultIsClaudes`.
  - `controlTimedOut` leaves the phase `*configuring`: fails
    `TestConfigureTimeoutStopsProcess`.
  - `exit` ignores `timedOut`: the timeout test hangs.
  - `result` keeps `turn`: fails `TestTurnAfterPromptResultIsClaudes`.
  - `publish` ignores `turn`: fails `TestApprovalRouting` and others.
  - `Send` accepts while queued: fails `TestPromptReleasedAfterClaudeTakesIt`.
  - idle ignores a queued prompt: fails `TestPromptReleasedAfterClaudeTakesIt`.
  - `exit` skips the drop: fails `TestStopDropsQueuedPrompt`.
  - `commandStarted` leaves `queued` set: fails three tests.
  - `waiting` ignores `queued`: passes (F1).
  - `exit` leaves `reported` false: passes (F2).
- `make test`: passes.
- `make e2e`: 35 passed, 3 skipped.
- `make live`: 3 passed.

## Verification 1

Checked the working tree on base `f7d4459` after the fixes. Since the review,
only `internal/claude/prompt_test.go` changed; the production code is the
reviewed code.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `TestStopDropsQueuedPrompt` now delivers `cancelled` for the taken prompt with nothing queued, asserts no update, and asserts the idle release names it. It fails with `p.queued` removed from `waiting` and passes with it. |
| F2 | Resolved | `TestUnexpectedExitIsReported` now exits during a prompt and asserts exactly one `exited` update, naming the prompt. It fails with `reported = true` removed and passes with it. |

No new findings.

Checks run: both mutations above, each reverted; `make test` passes;
`make e2e` 35 passed, 3 skipped; `make live` 3 passed.

## Verdict

Ready to commit.
