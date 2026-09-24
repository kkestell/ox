# Persistent shell processes

## Goal

Let Ox start a development server or long build, continue working, inspect its
output, send ordinary text input, and stop it. Background commands remain
available across turns and prompt cancellation in the same active session.
Session deletion, ACP connection shutdown, and the end of a headless run stop
the processes they own.

Use [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), [testing guidance](../testing.md), and
[`AGENTS.md`](../../AGENTS.md) as the implementation constraints.

## Related code

- `src/tools/shell.rs` — Shell arguments, command construction, API-key
  removal, output rendering, and process cleanup tests.
- `src/process.rs` — Bounded output capture, process groups, termination,
  reaping, and the runner also used by hooks.
- `src/tools.rs` — Tool schemas, tool call titles, dispatch, and cancellation.
- `src/acp.rs` — `ActiveSession`, activation and repeated load, deletion,
  prompt startup, connection shutdown, signal handling, and headless execution.
- `src/acp/operations.rs` — Operation guards, cancellation, and rejecting new
  operations once connection shutdown begins.
- `src/acp/prompt.rs` — Tool execution, Ask mode permission requests, hook
  ordering, and committing tool outcomes.
- `src/acp/convert.rs` — ACP tool kinds, permission content, and replay.
- `examples/skills/careful/scripts/careful.py` — The example hook's checks of
  shell command text, which must also cover text sent to a running command.

## Decisions

### Tool interface

Extend `shell` with `background: bool`, defaulting to `false`. Ordinary calls
keep their current timeout, output, and cleanup behavior. With
`background: true`, start `/bin/sh -c` in the session workspace with piped
stdin, stdout, and stderr, remove `OPENROUTER_API_KEY`, and return a shell
process ID after successful registration. The result confirms that the
command started, not that it finished or that a server is ready.

Background commands have no execution timeout. Reject an explicitly supplied
`timeout_seconds` with `background: true`; for ordinary calls retain the
current default and range. Parse the timeout as optional so omission and an
explicit value remain distinguishable.

Add one `shell_process` tool with these actions:

| Action | Arguments beyond `action` | Behavior |
| ------ | ------------------------- | -------- |
| `list` | None | Return shell process IDs, shortened commands, and current states for the active session. |
| `read` | `process_id`; optional `wait_seconds`, default `0`, range `0..=30` | Wait up to the specified time for termination, then return the current state and retained output tails. |
| `write` | `process_id`; `text`; optional `close_stdin`, default `false` | Write the exact UTF-8 text, then optionally close stdin. |
| `stop` | `process_id` | Request termination of the process group and wait for bounded cleanup. |

Reject unknown fields and fields inappropriate for the selected action.
`write` accepts at most 16 KiB of UTF-8 text. Empty text with
`close_stdin: true` closes stdin without writing; empty text without closure
is invalid. Writes append no newline implicitly. Closed stdin and unavailable
shell process IDs produce ordinary failed tool outcomes.

Use pipes for ordinary text input. Terminal emulation, terminal dimensions,
and terminal control characters are outside this change. Commands should keep
their main process in the foreground of the launched shell; Ox owns its whole
process group until that command terminates.

### Ownership and cleanup

Add a concrete `ShellProcesses` owner in `src/shell_processes.rs`. Store a
shared handle on `ActiveSession` and pass it through `PromptInput` to the tool
boundary. Cloning an active session shares this owner. Repeated load in the
same Ox process preserves it, including completed commands whose output is
still retained. A headless run creates the same owner locally.

Each background command has one supervisor that owns its child, process-group
guard, output capture, and cleanup. It continuously drains both output pipes
even when no tool call is reading them. Process control and output inspection
must not hold the active-session map lock across an asynchronous wait.

Retain at most 16 shell processes per active session. When a new command needs
space, remove the oldest finished shell process; never evict a running one.
If all 16 are running, fail the start before spawning. Removal releases its
retained output. Keep these limits as local constants.

A shell process ID is an opaque UUID, not an operating-system PID. Resolve it
only through the current active session's owner. IDs are never reused, and
neither loading a transcript after restart nor reading an old tool result
reconstructs a process. Unavailable IDs produce an error directing the model
to `shell_process` with `action: "list"`.

Prompt cancellation does not stop registered background commands, including
commands started earlier in that prompt run. It cancels a waiting `read` or
`write` call without terminating its command. Check cancellation before a
start; once spawning and registration succeed, return the observed successful
start. A later cancellation or transcript commit failure does not undo it.

A write waits at most five seconds. On timeout, cancellation, or a broken
pipe, report the number of bytes written and whether stdin was closed. Stop
the remaining write; do not retry input automatically or claim the receiving
program processed it. Output draining continues while a write is waiting.

Once an explicit stop has begun, finish its bounded cleanup even if the prompt
is cancelled. Send SIGTERM, allow two seconds, then send SIGKILL if needed,
reap the child, and bound output draining with `OUTPUT_DRAIN_TIMEOUT`. A stop
of an already finished shell process returns its existing state without
signalling an old PID. On natural command exit, clean up remaining members
of its process group and finish output capture before publishing its final
state. An output-read failure also ends the command through cleanup.

Use immediate SIGKILL for owner-ending cleanup, natural-exit descendants, and
capture failures. Reserve the two-second SIGTERM grace period for an explicit
`stop`. Owner-ending cleanup also interrupts an explicit stop's grace period.
All paths reuse the supervisor's reaping and bounded output draining.

Owner shutdown first closes registration and signals every owned group, then
awaits the supervisors. Serialize closing registration with spawning and
registration so a concurrent start either fails before spawning or belongs to
the cleanup. Repeated shutdown is harmless. Signal all groups across all
owners before awaiting cleanup so shutdown time does not multiply by the
process count.

Delete an active session only while holding its operation guard. Acquire the
guard in the ACP callback, then move it into `connection.spawn`, following
`spawn_prompt_run`. After database deletion succeeds, shut down its owner,
remove the active state, and respond. Keep the owner in the active state until
cleanup finishes so connection failure during deletion still leaves it
available to server cleanup. A failed database deletion leaves the active
session and its processes available. Waiting for cleanup must leave the ACP
connection free to process other sessions' messages.

In serve mode, keep `connect_to` so accepted responses drain through the
physical output transport before returning. On incoming EOF, reject new
session operations, cancel active prompts, begin owner shutdown, and await
the operations in the close callback. On SIGINT, SIGTERM, or SIGHUP, begin the
same shutdown while continuing to forward incoming requests until the active
operations finish; then close the agent's incoming channel so `connect_to`
drains its output. Keep the admission flag in `SessionOperations`. After the
connection returns on success or error, always finish owner shutdown before
returning its result. Retain the owners outside the connection future so
transport errors cannot skip cleanup.

A headless run begins owner shutdown on its first termination signal, before
waiting for the prompt or hooks to finish. Every result then awaits cleanup
before returning the answer or error. Immediate group termination keeps a
nested `ox run` from spending its parent hook's two-second grace period on
another grace period. Keep the current hook deadline and grace constants.

Keep process-group guards for cleanup if a supervisor is dropped. Explicit
awaited cleanup is the normal path; do not rely on dropping a task handle to
terminate a command. The owner must retain a stop mechanism and a way to await
each supervisor. Closing an editor tab has no separate cleanup meaning unless
it deletes the session or closes the ACP connection. SIGKILL of Ox itself
cannot run cleanup and remains a documented limitation.

### Output, tool outcomes, and persistence

Reuse the shell's output limits: retain up to 14 KiB per stream and allocate
14 KiB total to stdout and stderr when rendering a result. Keep the complete
tool outcome within 16 KiB, including the shell process ID, state, labels,
and diagnostics. Preserve lossy UTF-8 decoding and explicit truncation notices.

Reads return snapshots of the retained tails without consuming them. Repeated
reads may repeat output. `wait_seconds` waits for termination, not for fresh
output, and reaching that wait limit leaves the command running. `list` does
not include output. Ox retains no full hidden log; commands can redirect logs
to workspace files when needed.

Distinguish a completed tool call from a finished command. Starting a command,
reading a running command, listing commands, successfully writing input, and
explicitly stopping a command return completed tool outcomes. Reading a
finished command follows the existing shell conventions for successful exit,
nonzero exit, signal termination, and capture failure. A command deliberately
stopped through `stop` has an explicit stopped state, with the observed exit
information, rather than a fabricated successful exit code.
Later reads still use the observed exit information to choose their tool
outcome; a signal-terminated command produces a failed read outcome even when
the earlier stop call completed successfully.

Each tool call still has exactly one final tool outcome in its assistant
batch. Background output and command termination do not independently append
transcript entries, send ACP updates, invoke hooks, or trigger model requests.
Replay shows the observations saved by past tool calls. A live read or list is
the authority for current shell process state. No session-store format change
is needed.

### Permissions and hooks

Ask mode requests permission for `shell` starts and `shell_process` writes,
including closing stdin. Approval of a start does not approve later input.
Use the session mode captured for the current turn. Reads, lists, and stops
need no permission request. Auto mode executes these calls without requesting
permission.

Keep argument parsing and permission classification together in the shell
tool code so malformed or newly added actions cannot fall through to execution
without the appropriate permission. For a write permission request, show the
shell process ID, original command, complete input text, and whether stdin
will close. A background start permission request must explicitly describe
its background lifetime.

Run `before_tool` before permission and execution for every action. Hook
denial prevents starting, writing, or stopping just as it prevents existing
tools; later calls in the batch can proceed. Automatic cleanup is an owned
resource operation and does not invoke model-tool hooks or ask permission.
`after_tools` observes the completed tool calls, not eventual background
command completion.

Extend the careful example's existing text checks to `shell_process` writes.
Its ordinary `shell` checks already cover background starts. Keep malformed
arguments delegated to Ox's argument validation.

## Naming

Use existing terms as defined in `eng/glossary.md`, with these additions:

- **Shell process** — One background shell command owned by an active session,
  together with its process group, stdin, retained output, and current state.
  Use `ShellProcesses` for the owner in `src/shell_processes.rs` and
  `shell_processes` for the field passed from the active session to tools.
- **Shell process ID** — An opaque identifier scoped to one active session's
  shell processes. Use `process_id` in tool arguments and results; never use
  it as an operating-system PID.
- **Background command** — A command started by `shell` with
  `background: true`, whose lifetime continues after that tool call returns.

## Test plan

- `src/shell_processes.rs`: Start a command that remains available while other
  work proceeds; read its state and output; send exact input; close stdin;
  observe successful, nonzero, and signal exits. Use explicit readiness
  signals rather than fixed sleeps to coordinate tests.
- `src/shell_processes.rs`: Flood both output streams beyond their retention
  limits and prove the command keeps progressing. Cover repeated snapshots,
  invalid UTF-8, final output drain, the process limit, and finished-process
  removal. Exercise bounded writes to a program that never reads stdin.
- `src/shell_processes.rs`: Stop a command and its descendants, exercise
  SIGTERM grace and SIGKILL, and confirm reaping. Cover natural exit with
  remaining descendants, repeated stop, cancellation of read and write,
  and immediate cleanup when ownership ends, including during a stop's grace
  period. Cover shutdown racing a start and repeated shutdown.
- `src/tools/shell.rs` and `src/tools.rs`: Validate the new arguments and
  action combinations; ensure background start reports an ID rather than
  command completion; cover state-to-outcome rendering, truncation, and tool
  call titles. Extend the existing credential test to background starts.
  Keep the existing ordinary-shell and hook cleanup guarantees with their
  current owning tests.
- `src/acp.rs`: Extend the existing ACP permission fixture for background
  starts and process actions. Cover Ask and Auto behavior for start and write,
  hook denial before permission, and actions that need no permission.
- `src/acp/prompt.rs` and `src/acp/convert.rs`: Verify that each call saves one
  outcome even when the command is still running or a later update fails;
  check complete permission content in the conversion tests.
- `src/acp.rs`: Start a command in one turn and use it in another; preserve it
  through prompt cancellation and repeated load. Reject access from another
  session. Loading saved observations into a fresh server creates no process.
  Verify cleanup on deletion and connection shutdown, and on successful,
  failed, and cancelled headless runs.
- `src/acp.rs`: Extend transport-error and signal cleanup tests to background
  commands, including one that ignores SIGTERM and a nested headless run
  stopped by a hook. Verify another session can respond while deletion waits
  for cleanup, and that shutdown rejects new operations.
- `examples/skills/careful/scripts/test_careful.py`: Extend the existing
  destructive-text test across ordinary starts, background starts, and
  writes, including malformed input and unrelated actions.

## Implementation plan

1. In `src/process.rs`, expose only the capture and process-group operations
   needed by the new owner. Preserve `process::run` as the bounded runner for
   ordinary shell calls and hooks; do not add background lifetime options to
   hook execution.
2. Add `src/shell_processes.rs` and declare it in `src/main.rs`. Implement
   spawning, registration, supervised capture, bounded input, state reads,
   listing, limits, and shutdown with the tests owned by this module. Keep
   shutdown signaling separate from awaiting supervisors so every owner can
   begin cleanup before any wait.
3. In `src/tools/shell.rs`, implement `background` and the `shell_process`
   schema, validation, permission classification, and result rendering. In
   `src/tools.rs`, register and dispatch the new tool, pass the shell-process
   owner, and generate action-specific tool call titles. Keep cancellation
   handling inside operations that must report partial writes or finish
   cleanup. Update the shell tool description and `src/sessions.rs` Ask mode
   description. Replace the shell schema's equality assertion against the old
   plan with focused assertions for the changed arguments and behavior.
4. In `src/acp.rs`, attach the owner to `ActiveSession`, preserve it on
   repeated load, and pass it through `PromptInput`. Spawn deletion under its
   operation guard. Add the EOF and signal shutdown path through
   `connect_to` with signal-aware input forwarding, and await owner cleanup
   after every connection and headless result. In `src/acp/operations.rs`,
   close admission when shutdown begins.
   Keep shared-state locks out of these waits.
5. In `src/acp/prompt.rs`, pass the owner to tool execution and apply the
   permission rules. In `src/acp/convert.rs`, use the execute tool kind for
   process actions and render their permission details. Add the ACP and prompt
   tests at their respective boundaries.
6. Extend the careful example and its existing test, then update the affected
   documentation below.

## Documentation updates

- `README.md`: Explain background commands, the process actions, ordinary
  text input, output retention, lifetime across turns and cancellation, and
  graceful explicit stops versus immediate owner-ending cleanup.
- `eng/architecture.md`: Describe active-session ownership, bounded process
  state, tool-call completion versus command termination, permission scope,
  replay observations, and cleanup. Clarify that sequential tool calls can
  interact with commands that continue running between calls.
- `eng/glossary.md`: Add the new terms and update Ask mode and shell permission
  request definitions to cover input to a shell process.
- `eng/testing.md`: Add a local workflow that starts a server, uses it in a
  later turn, cancels a prompt, and explicitly stops the server.
- `AGENTS.md`: Add the new module and update the descriptions of shell tools,
  active sessions, permissions, and process cleanup.
- `examples/skills/careful/README.md` and `SKILL.md`: Describe the example's
  checks of text sent to a shell process.
