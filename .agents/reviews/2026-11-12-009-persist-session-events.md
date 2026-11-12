# Review: 2026-11-12-009-persist-session-events

## Round 1

- Task: `2026-11-12-009-persist-session-events`
- Independence: I did not write this code; implementation was supplied by the worker assigned by Hypercube session `8921ca46`.
- Reviewed change: `/Users/kyle/projects/ox`, branch `main`, `git diff HEAD` against `765e0de`; plan `.agents/plans/2026-11-12-009-persist-session-events.md`; work log `.agents/work/2026-11-12-009-persist-session-events.md`. The untracked work log is a workflow record, not code.

### Verdict

changes-required: lazy history loading can finish after deletion and create a subscription that never terminates, or spins without delivering anything. The acknowledged polling and shutdown-test gaps also violate explicit repository rules; both have practical fixture-based alternatives.

### Findings

1. **R1 — P1: deleting during the first history read can leave a permanently incomplete subscription or a busy loop.** `crates/oxd/src/session.rs:345`.
   After a restart, client A begins subscribing to an unloaded session with stored last seq `N > 0`. `conn.rs:168-170` obtains the session and awaits history without its lifecycle guard. While the blocking read is pending, client B deletes it: `daemon.rs:542-543` removes its rows and closes it. Closing changes its phase but appends no `session_closed` because history is still `None` (`state.rs:681-683`). The first read then fills this already-closed state unconditionally and A's subscribe is accepted because `0 < N` (`state.rs:583-595`). If the read preceded row deletion, A gets history but never its terminal event. If deletion preceded the read, history is empty while `last_seq` remains `N`; `Session::events_after(0)` immediately passes its watch condition and returns an empty batch forever (`session.rs:359-365`, `conn.rs:185-197`). This violates the subscription's terminal-event contract and AGENTS.md's event-driven waiting rule.

   A focused probe using the actual `state.rs` confirmed both transitions: `restored(last_seq=1) -> Closed -> fill([event])` accepts a subscription but has no terminal event; the same sequence with `fill([])` accepts it with `last_seq=1` and no events. Serialize initial history loading/subscription validation with deletion and recheck registry membership under the lifecycle guard, or otherwise preserve terminal-event and seq/history invariants when filling a closed session. Add a deterministic regression for close/delete during lazy loading.

2. **R2 — P2: the SIGKILL test waits by polling SQLite every 5 ms.** `crates/oxd/tests/daemon.rs:139-151`.
   `a_killed_daemon_leaves_a_history_that_ends_its_turn` calls this loop at line 1676 on every run. The sleep is neither the behavior under test nor a timeout on an event wait; it repeatedly guesses when the event writer committed. This directly violates AGENTS.md:5-8. The work log names the workaround but does not make it compliant.

   No new production notification or test-only flag is necessary: construct a committed incomplete-history database fixture, then verify repair and continued seqs through the ordinary daemon subscribe/load interfaces. Alternatively, an in-process fixture can use the existing production `Store::flush()` barrier before dropping the daemon without `close_all`. Keep fixture machinery in tests and retain the regression that fails when `fill` stops closing open ends.

3. **R3 — P2: shutdown durability has no test that fails when its flush is removed.** `crates/oxd/tests/daemon.rs:1625-1657`; production call `crates/oxd/src/main.rs:194`.
   The work log's undo check explicitly says removing `flush()` still passes this test (`.agents/work/2026-11-12-009-persist-session-events.md:37-41`). Thus it does not protect the difference between orderly shutdown and exiting while writes remain queued. On a backed-up writer, omitting the await loses the shutdown's permission resolution and `turn_ended` even though the test normally passes. This is the precise regression the plan's verification requires, and violates AGENTS.md:80-81's failing-without-the-fix rule.

   Add deterministic control over the writer acknowledgement in a test fixture, not a production flag or a longer delay. For example, make close-and-flush one production shutdown operation used by `main`; exercise it with a test-owned writer receiver, confirm it remains pending until the flush acknowledgement, and then release it. Pair that with the real-store persistence assertion. Removing the shutdown flush must fail the focused test; record that undo result.

### Notes

- The ordered enqueue under the session state lock, exclusion of `session_closed` from persistence, lazy read before load, replay suppression, permission-ID recovery, and cascading delete implement the intended normal path. No settled assumptions or accepted retention/kill limits are reopened.
- The first `make check-rust` attempt reportedly failed in the untouched bridge test `claude_exiting_fails_the_tool_calls_of_its_background_work`; the worker records three isolated passes and a passing full rerun. This unrelated intermittent failure is reported, not fixed here.

### Verification

- Reused: `make check-rust` — passed on rerun, from the work log and `/tmp/check-rust.log` (formatting, clippy and all workspace test summaries pass).
- Reused: `make check-scripts` — passed, as recorded in the work log; no separate output artifact was supplied.
- Reused: `make smoke-claude` — passed, from `/tmp/smoke-claude.log`, including persisted history after restart, no duplicate replay and successful continuation.
- Reused: worker's undo checks — removing open-end repair fails the killed-daemon test; removing shutdown flush does not fail the shutdown test. Evidence: work log lines 37-41.
- Run: `cargo build -p oxd --message-format=json` — passed, solely to identify the matching existing dependency artifacts for a focused read-only probe.
- Run: `python3 -` with an inline `rustc --edition=2024 -` probe importing the checkout's actual `state.rs` — confirmed both close-before-fill failures described in R1. Exact inline command and assertions are in this review session's tool transcript. Its initial attempt selected incompatible cached dependency artifacts and failed to compile; resolving the artifacts through Cargo corrected the probe. No source changes were needed.
- The focused probe verifies the state transitions, not the scheduling of an end-to-end two-client race. No broad checks, smoke checks or e2e checks were rerun; no production code, tests, staging or commits were changed.

## Resolution

The user dropped R1 and R3 (R3 stays in the work log's Limitations) and had R2
fixed: the polling SIGKILL test is replaced by a fixture history test (work log
repair round 1). The coordinator checked the fix; no re-review round.
