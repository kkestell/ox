# Review: 2026-11-12-007-bridge-background-work

## Round 1

- Task: `2026-11-12-007-bridge-background-work`
- Independence: I did not write this code; the implementer was the coordinator's work-stage child.
- Reviewed change: `/Users/kyle/projects/ox`, branch `main`; `git diff HEAD` and untracked `crates/claude-code-acp/src/background.rs`. Plan: [plan](../plans/2026-11-12-007-bridge-background-work.md). Work log: [work log](../work/2026-11-12-007-bridge-background-work.md).

### Verdict

changes-required: Two scheduling paths can report an empty background list despite work the settled rules require retaining. Both break Done-means and the contract for the parking unit.

### Findings

1. **R1.1 — P1: A failed or unreadable stop call clears wakeups instead of retaining unknown work.** `crates/claude-code-acp/src/background.rs:180-184`, `:242`.
   Start with a successful `ScheduleWakeup` result containing a future `scheduledFor`. Claude then calls `ScheduleWakeup` with `{"stop":true}`, but its `tool_result` has `is_error:true` (or lacks the structured `tool_use_result`). Observing the call immediately removes the existing wakeup, and `finish`'s stop branch returns `false` even when `readable` is `None`. The next idle update therefore carries `background: []`, although the stop did not establish that the scheduled work ended. The plan explicitly requires a failed/unreadable scheduling call to add an unknown schedule until process exit (plan lines 170–173). Only clear wakeups after a readable successful stop result; route failed/unreadable stop results through the unknown-schedule rule. Add a protocol regression that creates a future wakeup, fails its stop call, and asserts the final idle list remains nonempty. The existing stop test supplies only a successful result (`tests/protocol.rs:2629-2634`), so it does not protect this path.

2. **R1.2 — P1: Resume seeding omits scheduling calls in subagent transcript files.** `crates/claude-code-acp/src/history.rs:76-79`, `crates/claude-code-acp/src/bridge.rs:379-383`.
   A subagent creates an unexpired recurring job with the documented `CronCreate` result (`id`, `recurring:true`, future `nextFireAt`); its tool call and `toolUseResult` live in `<session>/subagents/agent-<id>.jsonl`, while the main transcript contains the parent Agent call/result. Close the session and prompt it after resume. `StartRequest::transcript` receives only `History::entries`, which reads the main `.jsonl`; it never reads the subagent files that `History::subagents` already locates (`history.rs:82-105`). The tracker therefore seeds no job and reports idle with `[]` after that turn, missing a restored recurring schedule. This is the work log's acknowledged omission (lines 85–87), not an allowed plan limitation: plan lines 174 and 179–183 require subagent schedules and resume seeding from main-thread and sidechain calls. Include those transcript entries in the seed in timestamp order, with a protocol fixture placing the scheduling call/result only in the subagent file. The existing resume fixture stores every scheduling call in the main file (`tests/protocol.rs:2688-2750`), so it cannot catch this omission.

### Verification

- Reused: `make check-rust` — passed, from work log lines 71–75 (evidence `/tmp/check-rust.log`).
- Reused: `make check-scripts` — passed, from the same table (evidence `/tmp/check-scripts.log`).
- Reused: `make smoke-claude` — passed, from the same table (evidence `/tmp/smoke-claude.log`). Not rerun, as instructed.
- No fresh checks run: both findings follow directly from the scheduling input/result branches and the transcript-loading call graph. Reviewed the added protocol assertions and background unit tests; neither finding's sequence is covered.
- No code edited, staged, or committed. Real scheduling tool behavior was not independently exercised; the work log also records that limitation. No UI or `crates/ox` behavior changed, so UI/E2E checks are not applicable.

## Round 2

- Task: `2026-11-12-007-bridge-background-work`
- Independence: I did not implement the repair.
- Reviewed change: same checkout and branch; focused on the R1.1 repair and adjacent successful-stop behavior, using the work log's **Repair round 1** entry. R1.2 was dropped by the user and is not reopened.

### Verdict

accept: R1.1 is resolved. A stop call no longer removes work at invocation; only a readable, non-error result clears wakeups. Failed/unreadable results retain the wakeup and add unknown work. No blocking findings in this repair.

### Resolution of earlier findings

| Finding | Status | Evidence in current code |
| --- | --- | --- |
| R1.1 | Resolved | `background.rs:178-180` only records the call; `:199-201` rejects errors/missing structured results; `:237-241` clears wakeups only for an accepted result; `:263` otherwise adds unknown work. |
| R1.2 | Dropped by user | Retained as the accepted limitation in the work log, lines 85–87 and 100–101. Not a remaining blocking finding. |

The protocol regression `a_failed_stop_leaves_the_wakeup_listed` asserts both the original wakeup and unknown stop schedule in the final idle list (`tests/protocol.rs:2729-2734`). The unit test additionally asserts that invocation alone preserves the wakeup (`background.rs:523-528`). Successful stop still completes the original tool before dropping its list entry (`tests/protocol.rs:2669-2677`); its retain predicate removes every `Kind::Wakeup`, including implied fallbacks, without removing unrelated schedules. The work log records that undoing the fix makes the new regression and unit test fail.

### Verification

- Reused: `make check-rust` — passed (50 protocol and 13 bridge unit tests), work log lines 114–116; evidence `/tmp/check-rust-r1.log`.
- No fresh checks run. Successful fallback removal was verified from the shared `Kind::Wakeup { .. }` predicate; no separate new fallback-stop test is claimed.
- No code edited, staged, or committed. Real-model scheduling was not independently exercised in this focused re-review.
