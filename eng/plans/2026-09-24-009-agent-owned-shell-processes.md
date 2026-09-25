# Shell processes owned by the agent that started them

## Goal

The main agent and its subagents share one list of shell processes. Any agent
can list, read, write to, or stop a background command another agent started,
and a subagent's background commands keep running after the subagent ends,
where no agent can reach them.

After this change, each shell process belongs to the agent that started it.
`list` shows only that agent's shell processes, and `read`, `write`, and `stop`
reject a shell process ID another agent started as unknown. A subagent's shell
processes are killed when the subagent ends: when `stop_subagent` stops it, when
it fails, and when the prompt run ends. The main agent's shell processes keep
their current lifetime.

## Related code

- `src/shell_processes.rs` — `ShellProcesses`, the active session's registry of
  `ShellProcess` handles, with `start`, `list`, `get`, `begin_shutdown`, and
  `shutdown`. The 16-process limit counts every process in the registry.
- `src/tools.rs` — `ToolContext`, which carries the shell processes to
  `permission` and `execute`.
- `src/tools/shell.rs` — `execute`, `execute_process`, `list`, and
  `process_permission`, which call `start`, `list`, and `get`; the `shell` and
  `shell_process` schema descriptions; the unknown-ID and empty-list messages.
- `src/acp/prompt.rs` — `AgentTurn::new` builds the `ToolContext` and the
  `Launch` from `PromptInput.shell_processes`; the turn's own session ID is
  `stored.summary.id`.
- `src/subagents.rs` — `Launch.shell_processes`, `State.started`,
  `Subagents::shutdown`, `Subagents::stop`, `Drop for Subagents`, and
  `Shared::finish_turn` and `Shared::next_turn`, which remove ended subagents.
  The stop test near line 790 asserts that a subagent's background command
  keeps running after the subagent stops.
- `src/tools/subagent.rs` — the `start_subagent` and `stop_subagent` schema
  descriptions, which say subagents share background commands and that stopping
  a subagent leaves them running.
- `src/prompts/subagent_prompt.md` — tells the subagent it shares background
  commands with other agents.
- `src/acp.rs` — test helper `start_shell_process` and shell process tests that
  call `start`, `list`, and `get` on the active session's owner.

## Decisions

- **One registry per active session, with each shell process tagged by its
  agent session ID.** The main agent's session ID is the main session ID; a
  subagent's is its child session ID. The registry stays the active session's
  `ShellProcesses`, so session deletion, connection shutdown, and headless
  shutdown keep covering every shell process, including a subagent's, with no
  new owner to track. A subagent's processes are reachable only through its
  child session ID, which no other agent's `ToolContext` holds.
- **The limit of 16 counts per agent session ID.** Removing the oldest finished
  process to make room also picks from that agent's processes only. One agent
  cannot exhaust another's limit or remove another's finished output.
- **An ended subagent's shell processes are killed at once**, with SIGKILL to
  each process group, the same as owner shutdown. No agent can reach them after
  the subagent ends, so a grace period helps nothing. A kill is requested
  synchronously wherever a subagent is removed; the prompt run awaits every
  kill before `after_run`.
- **The prompt run removes its subagents' shell processes from the registry**
  after their groups are cleaned up, so ended subagents leave nothing behind for
  later prompt runs. A dropped prompt future only requests the kills; their
  finished entries stay in the registry until the session's owner shuts down.
- **`State.started` becomes the list of every subagent ID started in the prompt
  run**, including subagents that already ended. Shutdown and drop need the IDs
  of subagents that removed themselves after a failure or cancellation. It
  replaces the existing boolean, which shutdown still uses to report whether
  the cost may have changed.
- **Tool descriptions describe ownership without roles.** The same `shell` and
  `shell_process` schemas serve both roles, so they say "the background
  commands you started" instead of "this session's". The subagent role in
  `subagent_prompt.md` says a subagent's background commands end when it ends.

## Naming

- **Shell process** — One background command started by one agent of an active
  session, together with its process group, stdin, retained output, and current
  state. Replaces "owned by an active session" in the glossary.
- **Agent session ID** — The session ID of the agent that started a shell
  process: the main session ID for the main agent, or the child session ID for a
  subagent. It is `ShellProcess::session_id` and `ToolContext::session_id` in
  code.
- `ShellProcesses::kill(session_id)` — Requests SIGKILL for every running shell
  process that agent session ID started, without waiting.
- `ShellProcesses::remove(session_id)` — Kills every shell process that agent
  session ID started, waits until each group is cleaned up, and removes them
  from the registry.

## Test plan

- `src/shell_processes.rs`: extend
  `the_limit_removes_the_oldest_finished_process_and_never_a_running_one` so a
  second agent session ID can still start a command when the first has 16
  running, and a full first session removes only its own oldest finished
  process.
- `src/shell_processes.rs`: new test
  `removing_an_agent_session_kills_and_forgets_only_its_shell_processes` —
  `remove` for one agent session ID kills its running command's group, removes
  it from that ID's `list`, and leaves another ID's running command running.
- `src/tools/shell.rs`: new table-driven test
  `a_shell_process_is_visible_only_to_the_agent_that_started_it` — start a
  background command under one agent session ID; under another, `list` reports
  no shell processes and `read`, `write`, and `stop` each fail with the unknown
  ID message and leave the command running; under the starting ID, `read`
  succeeds.
- `src/subagents.rs`: rewrite the test near line 790 so that after
  `stop_subagent` the subagent's background command's state is `Stopped` and
  it no longer appears in that child session ID's `list`.
- `src/subagents.rs`: new test
  `ending_the_prompt_run_kills_every_subagents_shell_processes` — one subagent
  starts a background command and goes idle, another fails after starting one;
  after `Subagents::shutdown`, both commands have ended and the main session ID's
  shell processes are untouched.
- Update the live subagent steps in `eng/testing.md`: a subagent starts
  `python3 -m http.server 8765` in the background and goes idle; the main agent
  lists no shell processes; after the prompt ends, `curl` to the server fails.

## Implementation plan

1. `src/shell_processes.rs`: add `session_id: SessionId` to `ShellProcess` with
   an accessor. `start`, `list`, and `get` take the agent session ID; `list` and
   `get` filter by it, and `start` applies the limit and the oldest-finished
   removal to that ID's processes. Add `kill` and `remove`. Update the docs of
   `MAX_SHELL_PROCESSES`, `Ending::Kill`, and `State::Stopped`, and the tests.
2. `src/tools.rs`: add `session_id: SessionId` to `ToolContext`, documented as
   the agent session ID that scopes its shell processes, and pass it with the
   shell processes to the shell tool functions. Update the doc on
   `shell_processes`.
3. `src/tools/shell.rs`: thread the agent session ID through `execute`,
   `execute_process`, `list`, and `process_permission`. Reword the `shell` and
   `shell_process` descriptions, the unknown-ID message, and the empty-list
   message to say "you started" instead of "this session". Add the visibility
   test and update existing tests.
4. `src/acp/prompt.rs`: set `ToolContext::session_id` from the turn's session ID
   in `AgentTurn::new`. Update the `PromptInput.shell_processes` doc.
5. `src/subagents.rs`: change `State.started` to `Vec<SessionId>`, pushed in
   `start`. `Shared::finish_turn` and `Shared::next_turn` call `kill` for a
   subagent they remove. `Subagents::stop` calls `remove` after awaiting the
   task. `Subagents::shutdown` awaits the tasks, then awaits `remove` for every
   started ID, and returns whether any started. `Drop for Subagents` calls
   `kill` for every started ID after cancelling. Update the module and field
   docs and the tests.
6. `src/tools/subagent.rs`: reword `start_subagent` to drop the sharing clause
   and `stop_subagent` to say it kills the background commands the subagent
   started.
7. `src/prompts/subagent_prompt.md`: replace the sharing sentence with one
   saying the subagent's background commands are killed when it ends.
8. `src/acp.rs`: pass the session ID in `start_shell_process` and the shell
   process tests.

## Documentation updates

- `eng/architecture.md`: Shell process lifetime (ownership by agent session ID,
  per-agent limit, ID resolution); Subagent lifetime (replace the sharing
  paragraph; `stop_subagent`, failure, and prompt-run end kill the subagent's
  shell processes, awaited before `after_run`); Process state; invariant 16 (a
  shell process belongs to one agent of one active session); invariant 18
  (every subagent's shell processes have ended before `after_run`).
- `eng/glossary.md`: Shell process, Shell process ID (identifies one shell
  process among one agent's), Tool context (adds the agent session ID).
- `AGENTS.md`: the `src/shell_processes.rs`, `src/subagents.rs`, and
  `src/tools.rs` entries.
