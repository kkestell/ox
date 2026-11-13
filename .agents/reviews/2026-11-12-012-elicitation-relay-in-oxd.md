# Review: 2026-11-12-012-elicitation-relay-in-oxd

## Round 1

- Task: `2026-11-12-012-elicitation-relay-in-oxd`
- Independence: I did not write this code; the implementer was the xwork
  stage worker.
- Reviewed change: `/Users/kyle/src/ox`, branch `main`, `git diff HEAD`
  (no untracked code files); plan
  `.agents/plans/2026-11-12-012-elicitation-relay-in-oxd.md`; work log
  `.agents/work/2026-11-12-012-elicitation-relay-in-oxd.md`

### Verdict

accept: Every outcome the plan requires is implemented and tested through
public entry points: the advertised capability, `elicitation/create` relay,
`awaiting_input`, `elicitation.respond` with schema checks, persisted and
replayed events, withdrawal via `$/cancel_request`, cancel on exit, park,
close and crash recovery, `oxctl answer`, and the fake agent's `elicit`. Both
deviations in the work log improve on the plan's mechanisms and are
justified.

### No blocking findings

The invariants that needed checking hold:

- **Withdrawal against a client's answer.** `Session::withdraw_elicitation`
  (`crates/oxd/src/session.rs:473`) closes the receiver and calls `try_recv`
  under the state lock. tokio's `oneshot` documents that a value sent before
  `close` is still received, and that `Sender::is_closed` is true afterwards.
  So either the client's answer was already recorded and goes to the agent,
  or `Request::Respond` (`crates/oxd/src/state.rs:773`) finds the receiver
  closed and refuses with `not_pending`, and `ElicitationWithdrawn` records
  `cancel`. Each `ElicitationRequested` gets exactly one
  `ElicitationResolved`. When `end` has already cancelled the elicitation,
  `try_recv` returns its `Cancel`, and when the phase is no longer live,
  `ElicitationWithdrawn` is ignored.
- **SDK contract.** The handler uses `Responder::cancellation()` and answers
  with the method-defined `cancel` action. This is the pattern
  `agent_client_protocol::concepts::cancellation` (5.2.0) documents for
  elicitations. The handler records the request, then spawns its wait, so
  the dispatch loop is never blocked and keeps processing
  `$/cancel_request`.
- **State and parking.** A permission request takes precedence over an
  elicitation in `Live::state`. `quiet` counts both kinds, so an open
  elicitation blocks parking. `Cancelled` leaves elicitations open, while
  `end` and `fill` cancel both kinds. Request IDs come from one sequence, and
  `fill` continues it past both kinds of request.
- **Requests outside a live phase.** An elicitation that arrives while the
  session is `Starting` or `Exited` is ignored, and its dropped sender
  answers `cancel`. Permission requests already behave this way.
- **Tests confirm what they wait for.** `elicit` waits for
  `ElicitationRequested` and checks `AwaitingInput` before answering.
  `cancel_withdraws_an_elicitation` checks that the resolution comes before
  the `withdrawn` chunk and the `cancelled` turn end. The work log confirms
  that this test times out without the `cancelled()` branch.

### Notes

- No test covers the race between a withdrawal and a client's answer
  (`session.rs:473`, `biased` at `session.rs:645`). The work log names this
  gap. A deterministic test would need to drive the withdrawal and the
  answer from inside one state lock, so leaving it untested is reasonable.
  The state-level half, where an elicitation with a closed receiver is
  refused and resolved, is covered by
  `a_withdrawal_ends_only_the_elicitation_whose_receiver_closed`.
- `State::request` finds the pending entry with `position`, then
  destructures `PendingKind` twice in each arm, and each destructure guards
  an `unreachable!` (`state.rs:754`, `765`, `783`, `796`). A helper that
  removes or borrows the entry of the requested kind would drop the four
  `unreachable!`s. This is optional polish.
- The fake agent's `no form elicitation` branch
  (`crates/oxd/src/bin/oxd-fake-agent.rs:744`) is reached by no test. It
  exists so that removing the capability shows up as a failure, and the
  work log says removing `.form(...)` makes `elicitation_round_trip` time
  out rather than fail on the chunk. That is acceptable for a fixture.
- Until unit 014 lands, the app does not handle `awaiting_input`.
  `ui/src/protocol.ts` and `SessionList.tsx` lack the state, and
  `Composer.tsx:21` hides Stop for it. No agent elicits before unit 013, and
  plan 014 explicitly covers these three places. The work log names this
  gap.
- An integer property refuses a JSON float, `3.0` included (named in the
  work log). Clients send integers for integer fields, so this is
  acceptable.

### Verification

- Reused: `make check-rust`: fmt and clippy pass. Every test passes except
  `a_git_directory_git_rejects_is_refused_with_gits_reason` in
  `crates/oxd/tests/worktrees.rs`, which fails because of the local git
  2.50.1 wording, in code this change does not touch (out of scope). From
  the work log, `/tmp/ox-012/check-rust.log` and
  `/tmp/ox-012/test-no-fail-fast.log`.
- Reused: `make check-ui`: passed. From the work log.
- Reused: `make e2e`: passed, 6 spec files. From the work log.
- Reused: `make smoke-claude`: passed. From the work log. It does not
  exercise elicitation, because the bridge does not elicit yet (unit 013).
- Reused: fail-without-fix checks for the `cancelled()` branch and the
  `.form(...)` capability. From the work log.
- Run: none. No open question needed a fresh run. The work log's results
  apply to the current tree, which has not changed since.
