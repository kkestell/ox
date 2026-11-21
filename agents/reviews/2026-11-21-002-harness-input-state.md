# Review: one tracker for every harness's untaken inputs and execution state

## Scope and coverage

Reviewed the uncommitted changes on base `8992e6d` for
[the plan](../plans/2026-11-21-001-harness-input-state.md): `internal/agent`
(`tracker.go`, `tracker_test.go`, `agent.go`), the Claude harness
(`agent.go`, `prompt.go`, `park.go` and their tests), the fake, Ur and its
tests, `internal/server/mcp_test.go`, `AGENTS.md`, and
`docs/architecture.md`. Also read the server's readers of the snapshot
(`sendPrompt`, `hold`, `list_sessions`), the web client's handling of
`state` updates, and the parts of `docs/protocol.md` and
`docs/claude-code.md` on state, interrupt, and `command_lifecycle`. The
untracked `agents/plans/2026-11-21-002-session-labels.md` belongs to other
work and was not reviewed.

The implementer's deviations from the plan:

1. `State()` returns the reported state unless it is `idle`, so
   `requires_action` stays visible while an input waits behind an approval.
   Accepted: Claude's published state showed `requires_action` with inputs
   untaken before this change, the web client's approval view depends on it,
   and the plan's guarantee, that the published state is never `idle` or
   `background` while an input is untaken, still holds by construction.
   `hold` refuses archive for `requires_action` as for `running`.
2. The fake's `Send` reads `idle()` before `Accept`. Accepted: `Accept` makes
   the published state `running`, so reading it after would never begin a
   turn. Moving the read after `Accept` fails ten server tests.
3. Claude's `waiting` drops its nonempty-queue check. Accepted: `First`
   returns an empty `MessageID` on an empty queue, and `id != ""` excludes it.
4. Dropping the last untaken input publishes the derived state. Accepted: the
   plan's Stop decision says so. It adds a `state idle` before `exited` on a
   stop or failed start with inputs held, which no client misreads; see F1
   for the update's `message_id`.
5. `TestArchive` wraps the fake in `configuringAgent`. Accepted: it holds the
   configuration open so the send lands during it, as the plan's test needs.

Gaps:

- The headless Chrome check did not run. The page's path to `oxd` is
  unchanged, so `AGENTS.md` does not require it.

## Summary

| ID | Severity | Category    | Action | Status | Title                                                |
| -- | -------- | ----------- | ------ | ------ | ---------------------------------------------------- |
| F1 | Low      | Correctness | Fix    | Resolved | A drop that releases the session names no prompt     |

## F1. A drop that releases the session names no prompt

Low · Correctness · Fix · `internal/agent/tracker.go:84`, `:94`

[Session updates](../../docs/protocol.md#session-updates) says the `state`
update that releases the session names the latest prompt Claude took or
dropped. `DropFirst` and `DropFrom` call `changed(was, "")`, so when a drop
leaves nothing untaken and the reported state is `idle`, the release is
tagged by the harness's publisher instead: `p.turn` in Claude's `prompting`
phase, which is empty for an input written to an idle Claude, and nothing in
the other phases.

Before the change, Claude's `endPrompt` published the release naming
`p.input.MessageID`. Now, when Stop interrupts a prompt Claude has received
but not yet reported `running` for
([§4](../../docs/claude-code.md#when-a-prompt-is-taken): within
milliseconds), the `command_lifecycle cancelled` drop publishes `state idle`
with no `message_id`, and Claude's following `idle` publishes nothing. The
same untagged release comes from an interrupt or stop while Claude starts or
configures, from the fake, and from Ur. The web client reads no `message_id`
on `state`, so nothing in Ox misbehaves; the protocol's contract is what
breaks.

### Evidence

A probe in `internal/claude`: `testAgent`, `Send(prompt("hi"))`,
`Interrupt`, `command_lifecycle cancelled` for the input, then `idle`.
Updates: `prompt_cancelled id=<input>`, `state id="" state=idle`.

### Action

In `DropFirst` and `DropFrom`, pass the last dropped input's `MessageID` to
`changed` (empty when nothing was dropped), so a release by drop names the
prompt it dropped, as `Accept` names the input it accepts. Keep the test in
`internal/agent/tracker_test.go`: extend `TestTrackerDropFrom` to assert the
final `state idle` carries the last dropped input's ID, and add the same
assertion for `DropFirst`.

## Checks run

- `make test`: passed.
- New tests against base `8992e6d`: `TestConfigurationKeepsReportedState`
  (copied alone into a base worktree) fails with the acknowledgement
  publishing `idle`; the new `TestArchive` step fails with the archive
  accepted (`got <nil>, not the refusal -32002`).
- Conditions removed one at a time, each caught:
  - `State()`'s untaken-input case: `TestTrackerQueuedInputIsRunning`,
    `TestTrackerDropFrom`, `TestTrackerBackground`, and Claude configure and
    startup tests fail.
  - `a.tracker.Len() == 0` in Claude's `sessionState`:
    `TestPromptSentWhileClaudeWorks` fails.
  - The fake's `idle()` read moved after `Accept`: `TestArchive` and nine
    other server tests fail.
  - `Take` setting the reported state `running`:
    `TestTrackerQueuedInputIsRunning`, `TestPromptReleasedAfterClaudeTakesIt`,
    `TestTurnAfterPromptResultIsClaudes`, `TestArchive`, `TestReadSession`,
    `TestSessionLifecycle` fail.
- From the implementer's report: `make e2e` passed (40 passed, 3 skipped);
  `make live` passed (`TestLive`, `TestLiveUr`, `approval.spec`).

## Verification 1

Checked the working tree on base `8992e6d` after the fix to
`internal/agent/tracker.go` and `tracker_test.go`. `make test` passed. Each
half of the fix was removed in turn: with `DropFirst` back to an untagged
release, `TestTrackerDropFrom` fails (`state  idle`, not `state p4 idle`);
with `DropFrom` back to an untagged release, `TestTrackerDropFrom` and
`TestTrackerBackground` fail. The fix changes only the ID the release
carries, so it adds no regression. From the implementer's report: `make e2e`
(40 passed, 3 skipped) and `make live` passed on the final tree.

| ID | Status   | Evidence                                                                                       |
| -- | -------- | ---------------------------------------------------------------------------------------------- |
| F1 | Resolved | `DropFirst` and `DropFrom` name the last dropped input; `TestTrackerDropFrom` fails without it |

No new findings.

## Verdict

`Ready to commit`: F1 is resolved.
