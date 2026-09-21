# Review: `shell` tool

Date: 2026-09-20
Reviewer: kreview (general)

## Scope

The new shell tool as it stands in the working tree on branch `rust`:

- `src/tools/shell.rs` (new, 561 lines)
- `src/tools.rs` (schema, title, dispatch with a cancellation future)
- `src/acp/prompt.rs` (tool execution awaited directly, three integration tests)
- `src/acp.rs` (headless SIGINT translation, shutdown and SIGINT tests)
- `src/openrouter.rs` (`shell_reply` fixture)
- `Cargo.toml`, `Cargo.lock` (`rustix` with `process`; Tokio `time` and `signal`)
- `AGENTS.md`, `README.md`, `eng/ox-architecture-design.md`, `eng/ox-shell-tool.md`

Measured against `eng/ox-shell-tool.md` as the owning contract, with section 12
of the architecture document for the cancellation rules.

## Topics selected

`concurrency`, `correctness`, `error-handling`, `resources`, `security`,
`testing`, `readability`, and `documentation`. The change owns a subprocess, a
process group, two pipes, a timer, and a cancellation future inside one
`select!` loop, so process lifetime and cleanup carry the risk. `unsafe` and
`dependencies` were checked and have nothing to report: no `unsafe`, and
`rustix` was already in the tree and is used for one safe call. `performance`
was checked at real sizes (an 8 KiB read buffer, one `waitpid` per read, a
bounded `VecDeque`) and has nothing to report.

## Coverage

Read every line of the new module, the full design document, and the diffs to
every other file. Traced `tools::execute` through `PromptRun::execute`,
`run_headless_prompt`, and the ACP prompt handler, and traced cancellation from
`PromptCancellation` through `now_or_never` and the `biased` selects. Ran the
checks listed at the end, including throwaway probes and one live headless run.

Material gaps:

- Linux behavior was reasoned from kernel source, not run. Finding 1 is
  macOS-specific and the reasoning says Linux is unaffected; that was not
  executed.
- The transport-error path of the ACP server (as opposed to clean EOF) was not
  traced into the `agent-client-protocol` crate. See the unresolved suspicion.
- Root-owned survivors in the shell's process group were not reproduced,
  because doing so needs `sudo`. Finding 1 covers them by the same errno.

## Findings

### 1. `kill_group` panics after an ordinary background job — `src/tools/shell.rs:85`

`kill_group` treats every error except `ESRCH` as a broken invariant and
panics. On macOS, `kill(-pgid, SIGKILL)` returns `EPERM` when the group still
exists but no member could be signaled, and the kernel's group iteration skips
zombies. So a group whose only remaining member is an unreaped zombie yields
`EPERM`, not `ESRCH`.

That state is reached by the normal-exit path. `child.wait()` reaps the shell
before `kill_group` runs. A background child that is still alive when the shell
exits is reparented to launchd. If it then exits before launchd reaps it, the
group holds one zombie and nothing else when `kill_group` is called.

Confirmed twice. First, the kernel semantics, from a Python probe that spawned
`true` in its own group and did not reap it:

```text
zombie state: 'Z'
killpg on zombie-only group: EPERM
killpg after reap: ESRCH
killpg with live member + zombie: OK (0)
```

Second, through the real tool path. A throwaway test called `execute` in a loop
with the command `true & exit 0` and panicked within the first few runs:

```text
panicked at src/tools/shell.rs:85:23:
failed to terminate owned shell process group: Operation not permitted (os error 1)
```

The same errno arises when the only survivors are processes Ox cannot signal,
such as a root-owned child left behind by `sudo` in a terminal with a cached
ticket. That is inside the "ordinary subprocess" model the design supports, not
a detached daemon.

Consequence: the prompt run unwinds before `finish` and `commit`, so the
shell's observed exit is lost and the assistant batch is never saved. In
`ox run` the process exits with a panic. The release profile sets
`panic = "abort"`, so in the ACP server the whole process dies with every
session it serves. The trigger is any command that leaves a short-lived
background job behind, which build and test scripts do routinely. Linux is
unaffected: it accepts and drops signals to zombies and returns 0 when any
member accepted the signal.

Remedy: treat `Errno::PERM` the same as `Errno::SRCH` in `kill_group`. Both mean
no member that Ox can signal remains, which is the end of cleanup; a zombie holds
no pipe, so draining is unaffected. Update section 5 of `eng/ox-shell-tool.md`
to name both errnos as successful cleanup. Add a deterministic regression test
next to the existing empty-group check in
`normal_exit_stops_background_children_with_and_without_pipes`: spawn
`/bin/sh -c true` in its own process group, wait about 100 ms without reaping,
call `kill_group`, then reap. No new machinery is needed.

### 2. The reason for the second cancellation check was removed — `src/acp/prompt.rs:283`

The previous review asked for a comment at this check, because the check looks
redundant next to the one at line 278 and is not. The diff removed the comment
along with the outer `select!` it referred to, but the reason still holds one
level down: `tools::execute` polls `execute_other` first in a `biased` select,
and `patch::apply` is synchronous, so a patch runs to completion on its first
poll and the dispatcher never observes a cancellation that arrived during the
in-progress update. `cancelling_before_execution_leaves_the_workspace_untouched`
passes only because of this check.

Remedy: restore one sentence at the check naming the dispatcher's tool-first
race and the synchronous patch. The alternative is to move the already-cancelled
check into `tools::execute` for every tool, as the shell already does with
`now_or_never`, and drop this one; the comment is the smaller change.

### 3. Diagnostics end with a newline, leaving a blank line before `stdout:` — `src/tools/shell.rs:161`

Each diagnostic line is pushed with a trailing `\n` (lines 161 and 170), and
`render` then appends `\n\nstdout:` at line 237. A probe of `render` with the
drain-deadline diagnostic produced:

```text
Exit code: 0\nOutput capture stopped before EOF; additional output may be missing.\n\n\nstdout:\n(empty)\n\nstderr:\n(empty)
```

Cosmetic, but every diagnosed result carries it. Remedy: push the separator
before each diagnostic line instead of after it, so the status block never ends
in a newline.

## Unresolved suspicions

**A transport error during a running shell may drop the prompt task.**
`serve` cancels prompts and waits for their guards in `on_close`, and the clean
EOF case is covered by `acp_shutdown_waits_for_shell_cleanup_saving_and_response`.
I did not trace what the `agent-client-protocol` crate does when the incoming
stream yields an error rather than ending. If `connect_to` returns and drops the
spawned prompt task, the `Child` is dropped without `kill_on_drop`, the group
is never signaled, and the shell survives Ox's exit as an orphan. The README's
"forced termination" clause arguably covers a client that breaks the pipe, but
this is a non-crash path. A variant of the shutdown test that sends
`Err(io::Error)` on the incoming stream instead of dropping the sender would
settle it.

## Observations outside the contract

These are accepted behaviors or small polish items, recorded so the decisions
are visible. None is a defect against the design.

- A new process group means terminal hangup and SIGTERM to `ox run` no longer
  reach the shell command. Before this change, children shared Ox's foreground
  group and received the terminal's SIGHUP. The README's forced-termination
  clause covers it; registering `SignalKind::hangup()` and `terminate()` beside
  `interrupt()` would cost two `select!` arms if wanted.
- A NUL byte in `command` is reported as
  `Could not start /bin/sh in <workspace>: nul byte found in provided data`,
  a spawn-failure message for an argument problem. The input is unlikely and the
  reason is included.
- `detached_pipe_has_a_drain_deadline_and_late_cancellation_keeps_exit` and
  `headless_sigint_cleans_up_and_saves_even_with_repeated_signals` require
  `python3`. Without it they fail through the five-second `wait_file` timeout
  inside the cancellation future rather than with a clear message.
- `environment_excludes_api_key` and the headless SIGINT test run a nested test
  binary with inherited stdout, so nested libtest lines such as
  `running 1 test` appear inside the outer run's output.
- `arguments_schema_and_title` compiles the first fenced JSON block of
  `eng/ox-shell-tool.md` into the test binary. Adding a JSON block above the
  schema in that document breaks the test with an unrelated-looking error.
- `shell_result_survives_update_failure_or_late_cancellation` reads its shell
  result through the `patch_result` helper.
- `let observed = loop { … }` at line 128 is rebound as `let mut observed =
  observed;` at line 158; the loop binding can be `mut` directly.
- On normal exit there is a microsecond window between reaping the shell and
  signaling its group in which the PID could be reused by a new group leader.
  This is inherent to group kills and unchanged by the remedy for finding 1.

## Behaviors checked and found correct

- Argument decoding rejects malformed JSON, unknown fields, wrong types,
  fractions, strings, negatives, and timeouts outside 1 through 600, without
  spawning. The shipped schema equals the design document's block.
- The command is one `/bin/sh -c` argument with no rewriting. Stdin is
  `/dev/null`, `OPENROUTER_API_KEY` is removed, the working directory is the
  session workspace, and shell state does not persist between calls.
- The `biased` order observes an already-ready exit before timeout or
  cancellation, and an exit observed before cancellation stays the outcome
  through draining. Cancellation observed before spawn does not spawn.
- `child.wait()` is cancel-safe and caches its status, so the second `wait` in
  the cleanup `join!` returns immediately after a normal exit. `AsyncReadExt::read`
  is cancel-safe, and `Capture` mutates state only after the read completes, so
  a read dropped by the `select!` loses no bytes.
- The nested `select!` is guarded so it never has all branches disabled. Reads
  continue after the retention limit, so a chatty command is neither stopped
  nor blocked on a full pipe; the marker file in
  `large_output_keeps_tails_and_finishes_writing` proves it.
- The process group is established before `spawn` returns on both the
  `posix_spawn` and fork paths in `std`, so there is no window in which
  `kill_group` targets a group that does not yet exist.
- Timeout starts at spawn and does not cover cleanup. Output draining has its
  own one-second deadline whose expiry drops the pipes, and reaping runs
  alongside it.
- A shell stopped by `SIGSTOP` (or by `SIGTTIN` from opening `/dev/tty` in a
  background group) sits until the timeout, after which `SIGKILL` ends it and
  it is reaped. Verified by probe with `kill -STOP $$` and a one-second timeout.
- Output allocation matches section 4: half each when both exceed half, the
  remainder to the larger stream otherwise, applied to decoded lengths, trimmed
  from the front at a character boundary, with worst-case metadata plus
  replacement-character expansion staying under 16 KiB. The size assertion is
  an internal invariant, as the design requires.
- Nonzero exit, signal termination, timeout, and cancellation map to the
  outcomes in the section 4 table. A nonzero exit reaches the next model
  request and replays as a failed ACP call.
- Headless SIGINT is registered before the prompt starts, the prompt future is
  kept alive through cleanup and saving, and repeated signals cancel again
  without bypassing cleanup.
- The ACP prompt task holds its operation guard through cleanup, the save
  attempt, and the response, and clean shutdown waits for it.
- `AGENTS.md`, `README.md`, and the architecture document describe the shipped
  behavior, including the inherited-environment exposure and the API-key
  exception.

## Checks run

- `cargo fmt --check` — clean.
- `cargo clippy --all-targets` — clean.
- `cargo test` — 79 passed, 0 failed.
- Throwaway test module appended to `src/tools/shell.rs` and removed
  afterwards; the file was restored from a backup and verified byte-for-byte
  with `cmp`. It rendered a result with the drain diagnostic, ran
  `kill -STOP $$` with a one-second timeout and checked the shell was reaped,
  passed a NUL byte in `command`, closed stdout before writing stderr, and
  passed an already-ready cancellation future.
- Python probe of `killpg` return values on macOS 26.5 for a zombie-only
  group, an empty group, and a group with one live member plus a zombie.
- Throwaway stress test calling `execute` with `true & exit 0` in a loop,
  which reproduced finding 1 on the first pattern.
- One live `ox run` with the `.env` key, a temporary `OX_DATA_DIR`, and a
  temporary workspace. The model called `shell` once with
  `printf hello > greeting.txt && cat greeting.txt && printf oops >&2 && exit 3`.
  The file held `hello`, the saved `tool_result` row had status `failed` with
  content `Exit code: 3\n\nstdout:\nhello\n\nstderr:\noops`, the model replied
  `done`, and the process exited 0. The scratch directories were removed.

## Verdict

The implementation follows the design closely: the select loop, the shared
cleanup path, the output budget, and the headless signal handling all do what
section 5 and section 6 specify, and the tests exercise the contract rather
than the internals. Finding 1 is a process-killing panic on a command shape
that ordinary scripts produce, reproduced on the first attempt, and must be
fixed before this lands; the fix is one extra errno in a match arm plus a
sentence in the design document. Findings 2 and 3 are small corrections with no
new machinery. The transport-error suspicion is worth one test before the
server is used against a real editor.

## Resolution

All three findings were addressed in the change that follows this document.
The transport-error suspicion is also settled by the existing regression test.

1. **Fixed.** `kill_group` treats `EPERM` like `ESRCH`. The design now records
   why macOS can return `EPERM`, and the normal-exit test exercises a completed,
   unreaped process group before also checking the empty-group case.
2. **Fixed.** The second cancellation check again explains the dispatcher's
   tool-first race with a synchronous patch.
3. **Fixed.** Diagnostics are joined without a trailing newline, and the drain
   deadline test rejects a blank line before the `stdout:` section.

`transport_error_stops_the_shell_process_group` sends an I/O error through the
incoming ACP stream while a shell and child are running. It confirms that the
server returns the transport error and the process-group guard stops both
processes, so the suspected orphan path is covered.
