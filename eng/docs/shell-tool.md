# Shell tool design notes

This document records the current shell-tool discussion. It is not a
specification, and none of the details below should be treated as permanent.
The goal is to identify a useful first experiment while the agent is still
small, without making later experiments expensive.

## Why start here

A coding agent should be able to start a slow command, continue doing useful
work, finish the current user turn, and wake up when that command exits or
times out. Process execution itself is straightforward; the harder part is the
lifecycle spanning model calls, ACP prompt requests, session history, and
process cleanup.

Nailing down that lifecycle early is more valuable than designing a broad
command-execution API.

## Current direction

### One adaptive tool

Start with one model-facing `shell` tool. It starts a command and waits for a
short yield window:

- If the command exits inside the window, the tool returns its final result.
- If it is still running, the tool returns a task ID and the output captured so
  far. Ox continues managing the task in the background.

The adaptive behavior is presentation policy. The underlying process manager
should expose ordinary start, read, write, wait, and kill operations so that we
can later try separate foreground and background tools without rewriting
process handling.

### Keep the input small

The normal call should be:

```json
{
  "command": "cargo test"
}
```

Only two optional arguments are currently contemplated:

```json
{
  "command": "cargo test",
  "pty": false,
  "yield_time_ms": 10000
}
```

- `command` is a shell command string. Pipelines, redirects, conditionals, and
  environment assignments are common enough in coding work to prefer this over
  model-facing `argv`.
- `pty` is explicit and defaults to `false`.
- `yield_time_ms` overrides the default yield window.

Do not initially add `mode`, `notify`, `cwd`, `env`, or a model-selected
timeout. Most calls should contain only `command`.

The internal executor can still use a program-plus-arguments representation;
the tool adapter is responsible for turning the command string into a shell
invocation.

### PTY selection and yielding are independent

PTY versus pipes and foreground versus background are orthogonal decisions.
All process I/O should be asynchronous internally. "Foreground" only means
that the tool result waits for process exit; it must not block the Tokio
runtime.

```text
                         ordinary pipes       PTY
                       +------------------+------------------+
exits before yield     | final pipe result| final PTY result |
                       +------------------+------------------+
still running          | background task  | background task  |
                       +------------------+------------------+
```

Ordinary pipes are the default. A PTY is opt-in for commands that require
terminal semantics. The same adaptive yield policy applies to either backend.

`tokio::process` is the likely pipe backend. `rust-pty` 0.6 provides async
master I/O, window resizing, child waiting, signals, and killing for the PTY
backend. Using `rust-pty` does not itself determine whether a tool call waits or
yields.

### Ox owns process execution

ACP terminals are not required for this design. Ox should own subprocesses and
their lifecycle. This keeps shell semantics independent of a client's optional
terminal capability and gives Ox one place to handle output, timeouts, and
wake-ups.

ACP terminal support may still be useful later as a presentation integration,
but it should not define the process manager.

## Background task lifecycle

Yielding does not keep the initiating ACP prompt request open. The prompt ends
normally while the task continues:

```text
ACP prompt A
  |
  +-- model calls shell("cargo test")
  +-- shell returns { status: running, task_id: 7, ... }
  +-- model continues with other work
  `-- PromptResponse::EndTurn

              ... no ACP prompt is active ...

task 7 exits or times out
  |
  `-- process event wakes the session
       +-- Ox starts autonomous Rig run B
       +-- the run receives the process event as its input
       +-- the model analyzes the result and may call tools
       `-- Ox sends the resulting session updates to the client
```

This wake-up behavior is intrinsic to a yielded shell task. It is not selected
with a `notify` argument.

If a task finishes while another run is active for the same session, its event
is queued until a safe boundary rather than interrupting an in-flight model
request or tool call.

### Completion is an external observation

The initial shell tool call returns exactly one model-visible tool result. A
later task completion must not be represented as a second result for that call,
and Ox must not fabricate an assistant status-check tool call merely to create
a matching tool result. Either approach would create misleading or invalid
provider history.

Instead, the process manager emits a typed event such as:

```text
ProcessExited {
    session_id,
    task_id,
    exit_code,
    output,
    truncated,
}
```

or:

```text
ProcessTimedOut {
    session_id,
    task_id,
    output,
    truncated,
}
```

Ox should persist this as its own transcript event. When starting the wake-up
run, it can translate the event into a provider-visible user observation because
Rig and model providers do not have a distinct environment-event role:

```text
<process_event task_id="7" status="exited" exit_code="1">
error[E0308]: mismatched types
...
</process_event>
```

That role conversion is an adapter detail. The stored event must remain
distinguishable from text entered by the human.

## Separation of concerns

A tentative boundary is:

```text
Shell tool
  - parses the small model-facing request
  - starts a task
  - waits through the yield window
  - formats either a final result or a running-task result

Process manager
  - owns pipe and PTY processes
  - drains output continuously
  - tracks task state
  - enforces runtime timeout policy
  - supports follow-up I/O and termination
  - emits exit and timeout events

Session runtime
  - associates tasks with sessions
  - persists process events
  - queues events while a session is busy
  - starts autonomous wake-up runs while idle
  - translates persisted events into Rig messages

ACP adapter
  - renders tool-call progress and results
  - emits autonomous agent output to the connected client
```

This boundary should make the model-facing tool easy to split or reshape while
keeping task management and wake-up behavior stable.

## Interaction with the current code

Ox currently:

- runs one Rig streaming agent call for each ACP `session/prompt` request;
- prevents concurrent prompt runs within a session;
- persists user, assistant, tool-call, and tool-result events in SQLite;
- reconstructs Rig history from those persisted events; and
- exposes only the canned weather tool.

The shell experiment will need to add:

- a process manager shared by tool executions;
- the current session identity in Rig's per-run `ToolContext`;
- persisted process lifecycle events;
- a per-session event queue or actor capable of starting autonomous Rig runs;
  and
- an ACP connection path that remains available after a prompt response.

This should not require treating the shell task as an unfinished Rig tool call.
The Rig tool call is finished once it returns the running task ID.

## Remaining questions

These are roughly in the order they should be answered.

1. **ACP wake-up behavior.** Ox currently implements ACP v1. Its documented
   lifecycle centers session updates on a client-initiated prompt turn, whereas
   ACP v2 explicitly permits background updates while the session is idle.
   Should we first test out-of-turn `session/update` notifications against Zed
   as a small v1 experiment, or move the session lifecycle to ACP v2?

2. **Minimal follow-up control.** After `shell` yields a task ID, what is the
   smallest way for the model to inspect output, write input, or stop the task
   before it exits? This should not bloat the common `shell` schema. Candidates
   include a separate narrow task-control tool or deferring all control except
   automatic completion.

3. **Timeout policy.** What fixed runtime timeout should the first version use?
   Does timing out send a graceful termination first, and how long should Ox
   wait before killing the process?

4. **Process-tree termination.** Does stopping or timing out a task terminate
   only the direct child or its entire process group? Compilers and test runners
   commonly spawn children, so killing only the direct child can leak work.

5. **Output representation.** For pipe-backed tasks, should final results retain
   separate stdout and stderr or preserve one interleaved stream? PTYs inherently
   produce a combined terminal stream. We also need initial byte limits,
   truncation direction, and the amount of output included in a wake-up event.

6. **Default yield window.** `yield_time_ms` is optional, but its default value
   is not decided. It should be long enough that ordinary commands usually
   finish inline and short enough that a slow command does not stall the agent.

7. **Shell selection.** Should command strings run through the inherited
   `$SHELL`, a fixed `/bin/sh`, or a platform-specific shell? Should it be a
   login shell? This affects startup files, portability, and reproducibility.

8. **Working directory.** The simplest behavior is to run every task in the
   ACP session's workspace directory. Is that enough initially, or does the
   model need a way to select a subdirectory without relying on `cd` inside the
   command string?

9. **Task lifetime across disconnects.** Should all tasks die when the Ox
   process or ACP connection closes? Persisting enough state to recover tasks
   after restart is substantially more complicated and probably not justified
   for the first experiment.

10. **Multiple completions.** If several tasks finish while a session is idle or
    busy, should Ox wake once per task or coalesce all currently queued events
    into one model run?

11. **ACP presentation.** Should the original shell tool call remain visually
    in progress after Rig receives `{ status: running }`, then become completed
    when the background task exits? This would deliberately separate ACP UI
    status from the completed model-side tool call.

12. **Wake-up limits.** What prevents a background completion from causing an
    unbounded autonomous loop—for example, a wake-up run that starts another
    background task? A small explicit run/task budget may be enough, but it
    should be based on an observed need rather than speculative machinery.

## References

- [ACP v1 overview](https://agentclientprotocol.com/protocol/v1/overview)
- [ACP v1 terminals](https://agentclientprotocol.com/protocol/v1/terminals)
- [ACP Rust SDK protocol v2 notes](https://github.com/agentclientprotocol/rust-sdk/blob/main/md/protocol-v2.md)
- [`rust-pty` 0.6.1 API](https://docs.rs/rust-pty/0.6.1/rust_pty/)
- [Rig 0.42 `Agent`](https://docs.rs/rig/0.42.0/rig/agent/struct.Agent.html)
- [Rig 0.42 `ToolContext`](https://docs.rs/rig/0.42.0/rig/agent/tool/struct.ToolContext.html)
