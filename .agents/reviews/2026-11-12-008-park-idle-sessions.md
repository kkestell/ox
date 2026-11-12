# Review: 2026-11-12-008-park-idle-sessions

## Round 1

- Task: `2026-11-12-008-park-idle-sessions`
- Independence: I did not write the reviewed code; implementation was performed by a separate xwork child.
- Reviewed change: `/Users/kyle/projects/ox`, branch `main`, `git diff HEAD` against `ea1914d`; plan `.agents/plans/2026-11-12-008-park-idle-sessions.md`; work log `.agents/work/2026-11-12-008-park-idle-sessions.md`. The untracked work log is a workflow record, not code under review. Read the dependency plan for unit 007.

### Verdict

changes-required: the final parking check can reuse an expired delay after a session becomes busy and idle again while parking waits for its lifecycle lock. This violates the continuous-parkability requirement in Done-means.

### Findings

1. **R1 — P2: Re-check the lapse counter after acquiring the lifecycle lock.** `crates/oxd/src/session.rs:143-145`.
   The timed wait captures `seen` and watches `lapses` at lines 129–138, but stops watching while awaiting `lifecycle()` and then checks only the current boolean. A normal non-discard delete holds that lock while Git checks for unsaved work (`crates/oxd/src/daemon.rs:521-528`). If the old park timer expires during a slow check, a client can view the session, complete a prompt and unsubscribe before Git refuses the delete. These actions increment `lapses`, but leave the final boolean true. Parking consequently stops the agent immediately after the delete releases the lock instead of allowing a new full park delay. A focused protocol reproduction with a FIFO-gated Git status, dirty worktree and later-started control session confirmed `agent_parked` just **69.51 ms after the new turn ended**, with `--park-after-ms 1200`. The control parked before the new prompt, establishing that the old timer had elapsed. This is a lapse before the final re-check, not the accepted race between re-check and kill.
   Smallest correction: after acquiring the lifecycle lock, require both `parkable` and the unchanged `lapses == seen`; otherwise loop to restart the delay. Add an event-gated regression test covering a parkability interruption while waiting for that lock.

### Verification

- Reused: `make check-rust`, `make check-ui`, `make e2e`, `make check-scripts`, and `make smoke-claude`: passed, as recorded in the work log's Verification table and its linked `/tmp/008-*.log` evidence. Reused the logged undo-check failures for the viewer guard and background-list predicate.
- Run: `python3 -` with an inline, temporary-fixture wire-protocol reproduction against `target/debug/oxd` and `target/debug/oxd-fake-agent`. Started an isolated daemon with a 1,200 ms park delay, held a dirty worktree's delete at Git status using FIFOs, waited for an unviewed control session to park, completed a new target turn, unsubscribed, then released Git. Delete returned `UNSAVED_WORK` (wire code 7); the target recorded `agent_parked` 69.51 ms after the turn ended. This fresh check answered the lifecycle-wait correctness question. An initial harness invocation stopped at an assertion that mistakenly expected a string error code; the corrected invocation above completed. No repository code was edited, and fixture processes were stopped.
- No full checks were rerun. No additional smoke or E2E run was performed, as instructed. The review does not reopen settled assumptions or the plan's accepted re-check-to-kill limitation.
