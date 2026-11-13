# oxd relays form elicitations from agents to clients

- Task: `2026-11-12-012-elicitation-relay-in-oxd`
- Status: complete
- Plan: `.agents/plans/2026-11-12-012-elicitation-relay-in-oxd.md`

## Changes

As the plan designs it, with the deviations below.

- `crates/ox-protocol/src/messages.rs`: `SessionState::AwaitingInput`, `EventKind::{ElicitationRequested, ElicitationResolved}`, `Method::ElicitationRespond` (`elicitation.respond`, flattened action); round-trip tests for the method's accept, decline and cancel forms and for both events.
- `crates/ox-protocol/src/client.rs`: `Client::respond_elicitation`.
- `crates/oxd/src/state.rs`: `Pending` with `PendingKind::{Permission, Elicitation}`; `Change::{ElicitationRequested, ElicitationWithdrawn}`; `Request::Respond`; `AwaitingInput` below `AwaitingPermission`; `Cancelled` cancels permissions only, `end` and `fill` cancel both kinds; shared request IDs; `check_form`, `check_content`; `not_pending` says "request {id} is not pending". Unit tests listed in the plan's Verification.
- `crates/oxd/src/session.rs`: advertises `elicitation.form` in `initialize`; `CreateElicitationRequest` handler; `respond_elicitation`; `withdraw_elicitation`.
- `crates/oxd/src/daemon.rs`: dispatches `ElicitationRespond`.
- `crates/oxd/src/bin/oxd-fake-agent.rs`: `elicit` directive and its form, documented in the module comment.
- `crates/oxctl/src/main.rs`: `describe` for both events, `oxctl answer`, unit test of `describe`.
- `crates/ox/src/conversation.rs`: the two new events produce no items (see Deviations).
- `crates/oxd/tests/daemon.rs`: `elicitation_round_trip`, `decline_reaches_the_agent`, `cancel_withdraws_an_elicitation`; `a_history_that_a_crash_left_open_ends_its_turn` extended with an open elicitation.
- `docs/acp-v2.md`, `docs/architecture.md`: rows and paragraphs the plan names, plus `oxctl answer`.

Changed paths:

- `crates/ox-protocol/src/messages.rs`
- `crates/ox-protocol/src/client.rs`
- `crates/oxd/src/state.rs`
- `crates/oxd/src/session.rs`
- `crates/oxd/src/daemon.rs`
- `crates/oxd/src/bin/oxd-fake-agent.rs`
- `crates/oxd/tests/daemon.rs`
- `crates/oxctl/src/main.rs`
- `crates/ox/src/conversation.rs`
- `docs/acp-v2.md`
- `docs/architecture.md`

## Deviations

- **Withdrawal race.** The plan has the handler drop the receiver, then record `ElicitationWithdrawn`. A client's answer that landed between the two would be recorded as accepted while the agent got `cancel`. Instead, `Session::withdraw_elicitation` closes the receiver and reads any answer already sent while holding the state lock. An answer that got there first wins and goes to the agent; otherwise it records `ElicitationWithdrawn` and answers `cancel`. `Request::Respond` refuses an elicitation whose receiver is closed with `not_pending`. The handler's `select!` is `biased` toward the answer.
- **Fake agent withdrawal.** The plan has the fake agent drop the request, which makes the SDK send `$/cancel_request`. It now calls `cancellation_handle().cancel()` and waits for oxd's answer before sending `withdrawn`. That fixes the order the daemon test asserts: oxd's resolution comes before the chunk. It is also the cooperative cancellation the SDK documents.
- **Fake agent checks the capability.** `elicit` sends `elicitation/create` only when the client advertised `elicitation.form`. Otherwise it sends the chunk `no form elicitation`, so the daemon tests cover the capability oxd advertises.
- **`crates/ox`.** `Conversation::apply` matches `EventKind` exhaustively, so the app would not compile without arms for the new events. Both events produce no items. Unit `2026-11-12-014` replaces this.
- **`$/cancel_request` doc row.** The plan gives no status. The row is marked *partial* because a withdrawn permission request stays pending (assumption 6).

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-rust` | failed: fmt and clippy passed; one test in `oxd --test worktrees` failed, `a_git_directory_git_rejects_is_refused_with_gits_reason`. This change does not touch that code. Git 2.50.1 (Apple Git-155, the only git on PATH) reports `fatal: not a git repository: …/missing` for a broken gitfile, and the test expects `gitfile does not point to a valid repository`. | `/tmp/ox-012/check-rust.log` |
| `cargo test --workspace --no-fail-fast` | every other target passed: daemon 60, worktrees 48 of 49, oxd unit 48, ox-protocol 12, oxctl 1, claude-code-acp 13 + 50, ox 13 | `/tmp/ox-012/test-no-fail-fast.log` |
| `make check-ui` | passed | `/tmp/ox-012/check-ui.log` |
| `make e2e` | passed, 6 spec files | `/tmp/ox-012/e2e.log` |
| `make smoke-claude` | passed, all checks; artifacts `/private/tmp/ox-acp-od5yslxv` | `/tmp/ox-012/smoke-claude.log` |

Fixes undone to confirm the tests fail without them:

- With the `withdrawn.cancelled()` branch replaced by a pending future, `cancel_withdraws_an_elicitation` times out waiting for an event.
- Without `.form(...)` in the `initialize` capabilities, `elicitation_round_trip` times out.

The other tests were not run with their fix undone.

## Limitations

- The `worktrees` test failure above comes from the git version and is outside this unit. It was neither fixed nor reproduced at HEAD.
- Until unit 014, the app shows nothing for an elicitation. `ui/src/protocol.ts` and `SessionList.tsx` have no `awaiting_input` state or label, so a session awaiting input has no label in the sidebar. No agent elicits until unit 013.
- No test exercises the race between a withdrawal and a client's answer.
- `make smoke-claude` does not exercise elicitation, because the bridge does not elicit yet.
- These approved assumptions are implemented as written:
  - oxd advertises `elicitation.form` but refuses request-scoped form elicitations with `-32602`.
  - `pattern` and `format` are not checked.
  - Stop does not resolve an open elicitation.
  - A permission request the agent withdraws with `$/cancel_request` stays pending.
- An integer property refuses a JSON float answer, `3.0` included.
