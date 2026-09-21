# Ox shell tool

Status: implemented. Verified locally on macOS with focused process and
prompt tests, ACP shutdown and repeated headless SIGINT tests, and an isolated
live OpenRouter run confirming a shell file change and its saved transcript.

## 1. Goal

Add one local `shell` function tool for builds, tests, Git, package commands,
and scripts. Each call runs one command and returns its final result. Shell
syntax and installed programs provide the utility; Ox owns a small execution
boundary.

No process survives as a resource the model can address in a later call. Do not
add persistent shell sessions, background job management, polling tools, or a
general process framework.

The initial implementation supports macOS and Linux with `/bin/sh`. Windows
support and alternate shells are outside this version.

## 2. Tool interface

Register this function alongside the existing concrete tools:

```json
{
  "type": "function",
  "function": {
    "name": "shell",
    "description": "Run a noninteractive /bin/sh command starting in the session workspace. Returns the exit status and tails of stdout and stderr, at most 16 KiB total. Output has a shared 14 KiB budget: 7 KiB per stream, with unused space given to the other stream. Earlier output may be omitted; redirect long logs to a workspace file for later inspection. Each call starts a fresh shell with stdin connected to /dev/null. No interactive input or persistent background processes. Commands run with Ox's permissions and can access paths outside the workspace.",
    "parameters": {
      "type": "object",
      "properties": {
        "command": {
          "type": "string",
          "description": "Shell command or multiline script. Use shell syntax for directory changes, environment overrides, pipelines, and redirection."
        },
        "timeout_seconds": {
          "type": "integer",
          "minimum": 1,
          "maximum": 600,
          "default": 120,
          "description": "Maximum execution time in seconds. Defaults to 120."
        }
      },
      "required": ["command"],
      "additionalProperties": false
    }
  }
}
```

Decode into one concrete owned argument struct. Reject malformed JSON, unknown
fields, wrong types, and timeouts outside 1 through 600. Do not clamp invalid
values. Argument errors produce `ToolOutcome::Failed` without starting a
process.

The command is passed as one argument to `/bin/sh -c`. Do not parse, rewrite,
interpolate, or wrap the supplied script in additional shell syntax. Empty
scripts follow ordinary shell behavior.

## 3. Execution environment

Every call starts a fresh noninteractive, non-login shell in the session's
stored workspace directory. Inherit Ox's environment except for
`OPENROUTER_API_KEY`, which is removed with `env_remove` before spawning.
Connect stdin to `/dev/null` using `Stdio::null()`; do not close file descriptor
0. Capture stdout and stderr through separate pipes. Do not allocate a terminal
or load the user's interactive shell configuration on Ox's behalf.

Use shell syntax for behavior that does not need another tool parameter:

```sh
cd frontend && npm test
RUST_BACKTRACE=1 cargo test
git diff --stat
python3 scripts/check.py
cargo test > test.log 2>&1
```

Working directory changes, variables, and shell options last only for that
call. Filesystem changes persist normally. Shell exit semantics apply,
including the default use of the last command's status in a pipeline; Ox adds
no implicit `set -e` or `pipefail` behavior.

The workspace is the starting directory, not a filesystem restriction. Shell
commands run with Ox's user permissions and inherited environment. They can
access absolute paths, change directories outside the workspace, and use the
network. Do not reuse the file tools' path validation as a claim of shell
confinement.

The model can inspect the inherited environment through shell commands. The
README must describe this exposure and the `OPENROUTER_API_KEY` exception.
Removing that variable prevents incidental inheritance; it does not restrict
access to credentials available through files or other mechanisms.

A missing shell, invalid working directory, or other spawn failure is a failed
tool result with an actionable error. Do not fall back to another shell.

## 4. Output and results

Return one text result using the existing `ToolOutcome` variants. Use the
stable ACP title `Run shell command` and the existing in-progress and finished
ACP updates. Do not stream subprocess output.

An ordinary result has an explicit exit status and labeled streams:

```text
Exit code: 0

stdout:
All checks passed.

stderr:
(empty)
```

| Observed result | Tool outcome |
| --- | --- |
| Shell exits with code 0 and cleanup succeeds | `Completed` with status and output. |
| Shell exits with a nonzero code | `Failed` with that code and output. |
| Shell exits because of a signal | `Failed` identifying the signal and including output. |
| Execution reaches its timeout | `Failed` stating the timeout and possible partial changes, with captured output. |
| Prompt cancellation interrupts execution | `Cancelled` stating possible partial changes, with captured output. |
| Spawn or output reading fails | `Failed` describing the error and including available output where possible. |

A nonzero exit code is an ordinary tool failure. It does not abort the prompt
or prevent the model from reading the result and deciding what to do next.
Do not invent an exit code for a timeout or signal termination, and do not
describe partial execution as rolled back.

The complete result must fit the existing 16 KiB output limit, including status,
stream labels, and diagnostics. Reserve 2 KiB for that metadata, bounding error
text within that reservation, and use `OUTPUT_BODY_LIMIT = 14 * 1024` for the
combined stream output. Keep the tail of each stream; build and test failures
commonly end with the useful diagnostics.

While reading, retain up to `OUTPUT_BODY_LIMIT` bytes per stream. At formatting
time, give each stream half the body budget if both exceed half. Otherwise give
the larger stream any space the smaller one does not use. Apply this allocation
to decoded text lengths. A command writing only stderr or only stdout can use
the whole body budget. Mark each truncated stream explicitly as a tail with
earlier output omitted.

Read both pipes concurrently and keep memory bounded while reading. Continue
draining output after its retention limit is reached. The output limit must
neither stop the command nor block it on a full pipe. In particular, do not
copy the search tool's behavior of stopping ripgrep after enough matches.

Decode captured bytes lossily as UTF-8 and enforce the final byte budget after
decoding, since replacement characters can expand the retained bytes. Trim
tails from the front at a valid character boundary; the existing `truncate`
helper removes the end and is unsuitable for this purpose. A retained tail may
start partway through a line. The shell module must guarantee the size of every
outcome itself; `bounded_result` asserts the size of successful results rather
than truncating them. No terminal emulation, ANSI processing, or binary-output
protocol is required.

Ox does not preserve a full hidden log. The model can redirect output to a
workspace file when it needs later inspection. Truncation notices must not
suggest that omitted output can be retrieved from Ox.

## 5. Process lifetime and timeout

The tool owns execution and cleanup for its entire call. Start the shell in a
new process group using `tokio::process::Command::process_group(0)` so ordinary
child commands and pipelines can be stopped together. Use the safe
`rustix::process::kill_process_group` API for group signaling. Add `rustix` as a
direct dependency with its `process` feature; it is already in the dependency
tree. No direct libc calls or process-management framework are needed.

The timeout starts when the shell is spawned. It bounds command execution,
not the subsequent mandatory cleanup and transcript save. Use a monotonic
timer. Preserve an exit outcome already observed before timeout or
cancellation; check for a ready exit status first when handling simultaneous
readiness.

On normal shell exit, terminate any remaining members of its process group.
On timeout, cancellation, or an execution I/O failure, terminate the group,
including the shell. These paths share the same cleanup, with different tool
outcomes. Use `SIGKILL` without a configurable grace period or signal-escalation
policy. Reap the shell and finish output handling before returning the result.

Treat `ESRCH` (no such process) and `EPERM` (no group member can be signaled)
from group signaling as successful cleanup. `EPERM` can occur on macOS when the
group contains only an unreaped zombie; neither error leaves a process Ox can
stop. Under the supported process model, any other signaling error is a broken
invariant and must fail loudly with `expect` or `panic!`. Do not turn it into a
recoverable tool failure. An exit-zero command remains `Completed` after
successful cleanup. Failure to reap the owned shell is also an internal
lifecycle failure, not a successful cleanup.

After group termination, allow up to `OUTPUT_DRAIN_TIMEOUT`, initially one
second, for the output readers to reach EOF. This is a separate monotonic
deadline, and draining can run alongside shell reaping. If it expires, drop the
readers, keep captured output, and append: "Output capture stopped before EOF;
additional output may be missing." Preserve the observed exit, timeout, or
cancellation outcome. This deadline bounds output draining, not shell reaping
or transcript saving.

Coordinate shell waiting and pipe reading so a descendant holding a pipe open
cannot prevent timeout handling or cleanup after shell exit. Remaining
background children are stopped even if they redirected their output and no
longer hold the pipes. Dropping the execution future or killing only the shell
is not the normal cleanup mechanism.

Process groups cover ordinary subprocesses, not programs that deliberately
detach into another group or session. Detached daemons are unsupported; this
version does not discover arbitrary descendants, establish an operating-system
sandbox, or guarantee cleanup after Ox itself crashes. Do not let an inherited
pipe from an unsupported detached process cause an indefinite output wait.

Keep a process-group guard alive while the execution future runs. If an
unexpected ACP transport error drops that future before orderly cancellation,
the guard must send `SIGKILL` to the group. This fallback stops ordinary
subprocesses but cannot reap the shell or save a tool result; the normal timeout
and cancellation paths above remain responsible for those steps.

## 6. Cancellation and transcript saving

Cancellation interrupts shell execution through the same cleanup path as
timeout. Both can interrupt writes and leave partial changes. Scope the
repository rule about letting a file change finish to `apply_patch`, where the
change is short and identifiable. Shell cancellation follows the architecture's
requirement to terminate and reap an owned process before reporting it stopped.

- If cancellation is observed before execution, do not spawn the shell.
- If cancellation interrupts running execution, terminate the group, reap the
  shell, and drain output with the deadline in section 5. Return `Cancelled`
  with captured output and a notice that partial changes may remain.
- Preserve an exit outcome already observed before cancellation. Do not replace
  a completed command's result with `Cancelled` during output draining or saving.
- After the result, act on cancellation before starting another tool or model
  request. Give unstarted calls the existing cancelled outcomes.

Stop and ACP connection shutdown both signal cancellation immediately. They
wait for cleanup and saving, not for the command's execution timeout.

Pass a cancellation future into `tools::execute`:

```rust
pub async fn execute(
    workspace_path: &Path,
    call: &ToolCall,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome
```

In `src/acp/prompt.rs`, pass `self.cancellation.cancelled()` and await execution
directly. Remove the outer cancellation `select!` that could drop the running
shell future. Keep the checks before execution and before later calls.

In `src/tools.rs`, pass the future to shell execution, which selects between
exit, timeout, cancellation, and output-read failure, then awaits cleanup.
For other tools, preserve the existing tool-first cancellation `select!` in
the dispatcher. Synchronous patch application still finishes once started.
The tools module depends on a future, not on ACP's `PromptCancellation` type.
Do not add a per-tool lifecycle predicate, detached worker, process registry,
or generic lifecycle trait.

In `src/acp.rs::run_headless`, register a Tokio SIGINT listener before starting
the prompt. A new process group no longer receives the terminal's foreground
Ctrl-C signal, so Ox must translate SIGINT into the prompt's cancellation signal.
Keep the prompt future alive and await its cleanup and transcript save after
signaling cancellation; do not drop it when the signal arrives. Repeated SIGINT
signals do not bypass cleanup. A cancelled headless run exits unsuccessfully
through its existing stop-reason handling. Forced termination and crashes remain
outside the cleanup guarantee.

Record the observed outcome in the uncommitted assistant batch before sending
the finished ACP update. After every call has an outcome, save the assistant
message and tool results together with `SessionStore::append_batch`. An ACP
update failure must not erase completed execution or its result.

Keep the operation guard through execution, cleanup, the save attempt, and
response handling. Reuse the existing transcript representation and replay
path. Add no database tables, migrations, process restart recovery, or extra
per-call saves. These rules extend
[the architecture's tool lifecycle](ox-architecture-design.md#12-tools-and-cancellation).

## 7. Implementation scope

| File | Change |
| --- | --- |
| `src/tools.rs` | Add the shell schema, stable title, and dispatch accepting a cancellation future. Preserve other tools' cancellation behavior. |
| `src/tools/shell.rs` | Parse arguments, run the shell, retain bounded output, and complete shared cleanup for exit, timeout, and cancellation. |
| `src/acp/prompt.rs` | Pass the cancellation future and await tool execution without an outer cancellation race. |
| `src/acp.rs` | Translate headless SIGINT into prompt cancellation and await cleanup and saving. |
| `Cargo.toml` | Explicitly enable Tokio's `time` and `signal` features. Add a direct `rustix` dependency with `process`. |
| `AGENTS.md` | Add the shell module to the source map and scope uninterrupted file changes to `apply_patch` when implemented. |
| `README.md` | Describe shell availability, output limits, permissions, inherited environment and its API-key exception, and cancellation. |
| `eng/ox-architecture-design.md` | Document the concrete shell cancellation and cleanup behavior when implemented. |

Keep the implementation local and concrete. Hardcode `OUTPUT_BODY_LIMIT`, the
metadata reservation, the default and maximum execution timeouts, and
`OUTPUT_DRAIN_TIMEOUT` as nearby constants. Do not refactor existing tools into
a shared subprocess framework for this addition.

Excluded from this version: PTYs, interactive input, live output updates,
persistent shell state, background jobs, process IDs exposed to the model,
polling or resume tools, shell selection, separate working-directory and
environment parameters, command classification, automatic retries, automatic
log storage, permission prompts, and sandboxing.

## 8. Acceptance tests

Use temporary workspaces and deterministic commands for focused tests:

- Validate arguments, default timeout, timeout bounds, and the shipped schema.
- Verify workspace selection, inherited environment with `OPENROUTER_API_KEY`
  removed, stdin connected to `/dev/null`, shell syntax, and fresh shell state
  between calls. Use a dummy API key for the environment test.
- Verify success, nonzero exit, signal termination, spawn failure, and labeled
  stdout and stderr, including empty streams.
- Produce more than the output budget on both streams. Verify retained tails,
  truncation notices, and allocation when one stream is empty, below half, or
  above half the budget. Verify front trimming at character boundaries and the
  final size with worst-case metadata plus replacement-character expansion.
  Have the command write a marker afterward to prove truncation did not stop it.
- Time out a command with an ordinary child. Verify group cleanup, shell
  reaping, retained output, and the partial-changes notice.
- Let the shell exit while an ordinary background child remains, both with
  inherited pipes and with redirected output. Verify cleanup and return without
  waiting indefinitely for pipe EOF.
- Verify an already-empty process group is successful cleanup. Hold an output
  pipe open beyond `OUTPUT_DRAIN_TIMEOUT` in a controlled fixture and verify
  bounded draining, the missing-output notice, and the preserved outcome.
- Cancel before spawn and verify no side effects. Cancel during execution and
  verify group termination and shell reaping without waiting for the execution
  timeout. Verify captured output, the partial-changes notice, the saved
  `Cancelled` outcome, skipped later calls, and a busy session through saving
  and response handling. Verify an exit outcome observed before cancellation
  remains unchanged.
- Exercise ACP shutdown and headless SIGINT while an ordinary child is running.
  Verify cleanup and transcript saving before Ox exits, including repeated
  SIGINT during cleanup. Signal the test subprocess, not the test runner.
- Verify timeout and nonzero-exit results reach the next model request. Verify
  failed and cancelled results replay with the existing failed ACP status.
  Verify an ACP update failure after execution still leaves its result available
  for the batch save.

Use short explicit timeouts for process tests. Synchronize cancellation tests
on observable command progress rather than relying on long sleeps.

Finish implementation validation with an isolated live `ox run` using the
`OPENROUTER_API_KEY` from `.env` and a temporary `OX_DATA_DIR`. Have the model run
a small command that changes a temporary workspace file, then inspect that
file and the saved transcript to verify execution and persistence.
