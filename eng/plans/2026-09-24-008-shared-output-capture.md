# Shared output capture

## Goal

Ordinary shell calls and hooks capture child output through `process::run`,
and shell processes capture it through `shell_processes::supervise`. Each has
its own copy of the same steps: reading stdout and stderr fairly, stopping on
a read error, draining both pipes within a deadline around process group
cleanup, and deciding whether a later read error sets the outcome or becomes a
diagnostic. When this work is done, `process.rs` owns one implementation of
those steps and both callers use it. Each caller keeps only its own stop
conditions and cleanup. The Medium finding in
`eng/reviews/2026-09-24-008-simplification-review.md` describes the
duplication.

## Related code

- `src/process.rs` — `Capture` (`:24`), which holds one stream's retained
  output tail; `Capture::read` and `Capture::drain` (`:44`, `:69`), and the
  `done` and `error` fields that exist only for them; `run` (`:176`), with its
  read loop (`:208-226`), its drain under a `grace + OUTPUT_DRAIN_TIMEOUT`
  deadline (`:237-243`), and its handling of late read errors (`:244-262`).
- `src/shell_processes.rs` — `supervise` (`:341`), with its own buffers and
  `Stream` enum (`:325`), its read loop (`:355-389`), its drain loop that
  allows `OUTPUT_DRAIN_TIMEOUT` after cleanup finishes (`:395-426`), its
  handling of late read errors (`:436-457`), and its `drain` function
  (`:462`).
- `src/hooks.rs:299` and `src/tools/shell.rs:261` — the callers of
  `process::run`. They read `Finished.observed`, `stdout`, `stderr`, and
  `diagnostics`, which do not change.
- `src/tools/shell.rs` tests
  `detached_pipe_has_a_drain_deadline_and_late_cancellation_keeps_exit` and
  `large_output_keeps_tails_and_finishes_writing`, and `src/shell_processes.rs`
  tests `output_floods_are_drained_fairly_and_snapshots_repeat`,
  `shutdown_kills_at_once_even_during_a_stop_grace_period`, and
  `an_output_read_failure_kills_the_group_at_once_and_reaps_the_child`. These
  already cover the capture behavior the change must preserve. The last one
  calls `supervise` directly with a failing stdout pipe, so `supervise` keeps
  its signature.

## Decisions

- **Output goes to a sink the caller supplies.** A shell process publishes its
  output tails through the shared `Output` mutex while the command runs, so
  that `read` sees them; `process::run` keeps two local `Capture` values.
  `OutputPipes` therefore writes each chunk through an `FnMut(Stream, &[u8])`
  closure, rather than owning the captures. `run`'s closure appends to its
  locals. `supervise`'s closure locks `Output` and appends, as its
  `append_out` and `append_err` closures do today.
- **Reading is one future that finishes only on a read failure.** Each
  caller's `select!` loop keeps its own stop conditions: exit, deadline,
  cancellation, and stdin writes for `run`; exit and stop or shutdown requests
  for `supervise`. It adds one arm, `OutputPipes::read_until_failure`, which
  reads from whichever stream has output and waits forever once both are at
  EOF. A tokio pipe read is cancel-safe, so recreating this future on each loop
  iteration loses no output. A failure closes that stream and returns its
  message. Reads into the sink happen inside the future, so the caller's loop
  no longer handles chunks.
- **The drain deadline follows the shell process rule for both callers.**
  `OutputPipes::drain_during(cleanup)` drains while `cleanup` stops the group
  and reaps the child, then for up to `OUTPUT_DRAIN_TIMEOUT` more. `run`
  currently puts one `grace + OUTPUT_DRAIN_TIMEOUT` deadline on the drain,
  measured from the start of cleanup. Because `run`'s grace period is never
  interrupted, the two rules agree except for the time it takes to reap the
  child after SIGKILL. `supervise` needs its rule because an owner shutdown can
  cut a stop's grace period short.
- **Late read errors are handled the shell process way.** A read error seen
  while draining decides the outcome only after a natural exit; otherwise it
  is added to the diagnostics. `run` currently discards late errors when the
  main loop already failed on a read. After this change, those errors appear
  in the diagnostics, as they do for shell processes. This is a small,
  intended change in what an ordinary shell call reports.

## Naming

- `Capture` — Unchanged name: the retained tail of one output stream. It keeps
  `bytes`, `omitted`, and `limit`, with `new`, `append`, and `decode`.
- `Stream` — `Stdout` or `Stderr`, moved from `shell_processes.rs` into
  `process.rs` and made public, with `name()` returning `"stdout"` or
  `"stderr"` for messages.
- `OutputPipes` — The stdout and stderr pipes of one child process in
  `process.rs`, taken from the `Child`, together with the sink their output is
  written to and each pipe's open state.
- `Drained` — What `OutputPipes::drain_during` returns: the exit status from
  cleanup, whether both pipes reached EOF (`complete`), and the read errors
  seen while draining, in stdout-then-stderr order.
- `Drained::into_failure_and_diagnostics(exited)` — Returns the read error
  that decides the outcome, only when `exited` is true, and the diagnostics
  text: the "Output capture stopped before EOF; additional output may be
  missing." notice when the drain did not complete, followed by the remaining
  read errors, one per line.

## Test plan

- Existing tests own the behavior that must not change: fair capture under
  continuous output on both streams, tails bounded to their limit, the drain
  deadline when a detached descendant holds a pipe open, the "Output capture
  stopped before EOF" diagnostic after shutdown, and an immediate SIGKILL and
  `Failed` state after a shell process read failure. They must pass unchanged.
- Add one table-driven unit test in `src/process.rs`,
  `late_read_errors_decide_only_a_natural_exit`, for
  `into_failure_and_diagnostics`. This rule is small, stable logic that both
  callers now depend on, and it is hard to trigger through a real child. Each
  case gives `exited`, `complete`, and the errors, and names the case in its
  failure message:
  - exited with one error returns that error as the failure and empty
    diagnostics;
  - exited with two errors returns the first as the failure and the second as
    a diagnostic;
  - not exited with one error returns no failure and the error as a
    diagnostic;
  - an incomplete drain puts the notice before any error diagnostics;
  - no errors and a complete drain return no failure and empty diagnostics.

## Implementation plan

1. In `src/process.rs`, add `Stream` and `OutputPipes`:
   - `OutputPipes::new(child: &mut Child, append)` takes stdout and stderr,
     both of which must be piped.
   - `read_until_failure(&mut self) -> String` uses an unbiased `select!`
     between the open streams, as `supervise` does today, and appends each
     chunk through the sink. Keep the comment explaining that the unbiased
     choice stops one stream from starving the other.
   - `drain_during(self, cleanup: impl Future<Output = ExitStatus>) -> Drained`
     drains while `cleanup` runs, then for up to `OUTPUT_DRAIN_TIMEOUT`. Move
     the comment from `supervise` about a detached descendant holding a pipe
     open.

   Add `Drained` and `into_failure_and_diagnostics`, with the unit test from
   the test plan.
2. Rewrite `process::run` on `OutputPipes`: the loop keeps its exit, deadline,
   cancellation, and stdin write arms, plus the `read_until_failure` arm, which
   breaks with `Observed::Failed`. Cleanup terminates the group with
   `limits.grace` and reaps the child inside `drain_during`. When
   `into_failure_and_diagnostics(matches!(observed, Observed::Exit(_)))`
   returns a failure, replace `observed` with `Observed::Failed`.
3. Remove `Capture::read`, `Capture::drain`, and the `done` and `error` fields.
4. Rewrite `supervise` on `OutputPipes`: its loop keeps its exit and request
   arms, and its `read_until_failure` arm becomes `Reason::Failed`. Its cleanup
   future (interruptible `terminate` and reap) goes into `drain_during`. The
   final state comes from `Reason`, `Drained.status`, and
   `into_failure_and_diagnostics(exited)`. Delete its buffers, `Stream`,
   `append_out` and `append_err`, the drain `select!` loop, and `drain`.
   Closing stdin before publishing the final state stays as it is.
5. Run full validation.

## Documentation updates

- `AGENTS.md`, `src/process.rs` entry: say that the module owns the reading
  and draining of child output pipes into a caller's sink, and the rule for
  late read errors, and that shell process supervisors use it together with
  process group cleanup.
