# Untaken inputs owned by the agent

## Scope and coverage

The uncommitted changes to the 15 tracked files under `docs` and `internal`,
on base revision `38e421a`, against the plan
`agents/plans/2026-11-20-006-untaken-inputs-one-owner.md` (audit finding F2).
The plan's product question is settled as the plan proposes, and its
**Message delivery** change is approved as written. The untracked plans and
`.claude/skills/README.md` were not reviewed.

Traced: the Claude agent's queue through `Send`, `writeNext`, `ready`,
`commandStarted`, `commandCancelled`, `sessionState`, `Interrupt`, `end`, and
`endConfiguration` in every phase; the fake agent's queue; the session's
`sendPrompt`, `acceptMessage`, `startForInput`, `parked`, and `snapshot`;
`getSession`, `interruptSession`, `configureSession`; `send_message` and
`read_session`; and the web client's use of `queued`, `prompt_queued`, and
`prompt_cancelled`.

Requirements checked: **One prompt at a time**, **Stop**, **Message
delivery**, **Messages in the transcript**, **Pending setting changes**,
**When settings can change**, **Parking**, **Failed startup**, **Every request
settles**. `docs/functional-requirements.md` carries the approved **Message
delivery** text word for word and no other edit.

Gaps:

- The new and changed tests do not compile against the base revision, since
  `Snapshot.Queued` changes type, so the base-revision run shows only that.
  Removing each condition the change adds, one at a time, stands in for it.

## Summary

No findings.

## Checks run

- `make test` (lint, web build, `go test -race ./...`): passed.
- `make e2e`: 35 passed, 3 skipped (the live-only approval specs).
- `make live`: `TestLive` passed; the live approval specs, 3 passed.
- Each condition the change adds, removed one at a time; every removal failed
  a test:
  - `sendPrompt`'s queue check: `TestPromptQueuedWhileClaudeWorks`,
    `TestSendMessage`.
  - `Send` writing only with nothing untaken: `TestPromptSentWhileClaudeWorks`,
    `TestStopDropsQueuedPrompt`.
  - `Interrupt` keeping the written input, and dropping the held ones:
    `TestPromptReleasedAfterClaudeTakesIt`, `TestStopDropsQueuedPrompt`,
    `TestInterruptDuringConfiguration`, `TestInputWaitsForStartup`.
  - `ready`'s idle check: `TestInputWaitsForStartup`.
  - `endConfiguration` calling `ready`: `TestConfigureWaitsForAcknowledgements`,
    `TestConfigureFailure`, `TestInterruptDuringConfiguration`.
  - `writeNext` after `commandStarted` and `commandCancelled`:
    `TestPromptSentWhileClaudeWorks`, `TestInputWaitsForStartup`,
    `TestStopDropsQueuedPrompt`.
  - `sessionState` holding in every phase while inputs are untaken:
    `TestConfigureWaitsForAcknowledgements`.
  - `end` dropping untaken inputs, naming the latest held input, and ending a
    configuration without `endConfiguration`: `TestStartupFailureKeepsClaudesReason`,
    `TestInputWaitsForStartup`, `TestConfigureFailure`,
    `TestConfigureTimeoutStopsProcess`.
  - `writeNext` dropping on a failed write: `TestInputWaitsForStartup`.
  - `Send`'s `ErrExited`: `TestStopDropsQueuedPrompt`.
  - The fake starting a queued input after a configuration, and dropping on
    interrupt while idle: `TestSendMessage`.
  - `send_message` returning `acceptMessage`'s error, and `stateLine` counting
    the snapshot's queue: `TestSendMessage`.
  - The parked snapshot's empty queue: `TestExitDropsWaitingMessages`.
- Headless Chrome through `make dev`: not needed; the page's requests and the
  `session/get` shape are unchanged, and `make e2e` covers the page.

## Verdict

Ready to commit.
