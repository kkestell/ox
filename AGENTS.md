THIS DOCUMENT MUST BE KEPT UP TO DATE

## Code

- `Makefile`: Release builds and fast-by-default or release local installs.
  Installs replace the Ox binary, overwrite `~/.config/ox/settings.json` with
  `examples/settings.json`, and delete the disposable session database.
- `.github/workflows/release.yml`: Publishes a GitHub release when a pushed `v*`
  tag points at a commit on main: a guard job checks the tag's commit is
  reachable from `origin/main`, build jobs run `cargo build --release --locked`
  for the matrix targets and upload each packaged archive, and the publish job
  checksums the archives and creates the release with its assets.
- `scripts/run.py`: Temporary workspace runner for a headless prompt, with
  optional model, effort, and checkout of a pinned GitHub commit.
- `src/main.rs`: Command parsing and process entry; starts the ACP server, runs
  one headless prompt with an optional model and effort and prints its final
  answer, runs a credential command, or prints help.
- `src/auth.rs`: Environment and operating-system keyring credential storage.
- `src/cancellation.rs`: Shared cancellation signal for active session
  operations.
- `src/system_prompt.rs`: Assembly of Ox's built-in system prompt with bounded
  workspace-root `AGENTS.md` instructions, and of a subagent's system prompt
  from the main agent's with the subagent role appended.
- `src/text_file.rs`: Bounded UTF-8 reads for `AGENTS.md`, `SKILL.md`, and
  `settings.json`.
- `src/prompts/system_prompt.md`: The editable built-in instructions that define
  Ox's coding-agent behavior.
- `src/prompts/subagent_prompt.md`: The subagent role, which says a subagent's
  final answer goes to the main agent.
- `src/skills.rs`: Skill definitions in the skills directories
  `~/.config/ox/skills/`, `~/.agents/skills/`, and the workspace's
  `.agents/skills/`, highest priority first; frontmatter parsing, and skill
  catalog loading where a higher-priority skill replaces one with the same name
  and an invalid definition is skipped with a message while keeping its name.
- `src/settings.rs`: The home directory; the settings file format shared by
  `~/.config/ox/settings.json` and the workspace settings file
  `.ox/settings.json`, with one reader that checks the model against the model
  catalog; the required default model and optional global hooks read from
  `~/.config/ox/settings.json` at process startup, with global hook suppression
  inside hook commands; and each workspace's effective settings, where each key
  the workspace settings file sets replaces the same key from
  `~/.config/ox/settings.json`, except `hooks`, which is an error there.
- `src/hooks.rs`: Shared hook definitions, validation, and the protocol for
  every hook kind: JSON input on stdin with the fields every hook shares, one
  response object per kind on stdout, deadlines, limits, and hook errors.
- `src/process.rs`: Child processes in a new process group with optional stdin,
  bounded output tails, a deadline, cancellation, and group cleanup with an
  optional, interruptible SIGTERM grace period. Shell process supervisors share
  its output capture and process-group cleanup.
- `src/shell_processes.rs`: The shell processes of one active session:
  background commands spawned and registered under the owner's lock, one
  supervisor per command that drains its output tails and cleans up its process
  group, bounded stdin writes, state snapshots, listing, the limit of 16 with
  removal of the oldest finished one, explicit stops with a two-second SIGTERM
  grace period, and owner shutdown that kills every group at once.
- `src/openrouter.rs`: The model catalog fetched from OpenRouter at startup, its
  catalog filter and each model's effort levels and image input support, model
  request parameters including the agent's role, which selects the advertised
  tools, transcript encoding into chat messages that pairs each tool call with
  its outcome and gives each agent message its own attributed user message,
  request encoding from those messages with the skill invocation message,
  context limits, client, streamed response assembly with usage parsing, and
  explicit input-context overflow errors. Its test fixture routes each request
  by its first message after the system prompt, holds gated replies until a
  test opens them, and builds replies from the requests they answer.
- `src/compaction.rs`: Image-aware request estimates, safe transcript cuts,
  bounded summarizer input with explicit tool-result excerpts and attributed
  agent messages, checkpoint commits, and model-request projection into encoded
  chat messages, which repeats a covered skill invocation after the summary.
- `src/prompts/compaction_prompt.md`: Dedicated summarizer instructions.
- `src/tools.rs`: Concrete tool names, the agent role, the ordered list of tool
  schemas sent to the model for a role, tool call titles, permission
  classification, and execution of one complete call with a `ToolContext`: the
  workspace path, the active session's shell processes, and, for the main
  agent only, its subagents. Each tool module owns its own schema; this module
  only collects them.
- `src/tools/read.rs`: The `read_file` schema and bounded text-file reading with
  line pagination.
- `src/tools/shell.rs`: The `shell` schema with background starts, the
  `shell_process` schema and its `list`, `read`, `write`, and `stop` actions,
  argument validation shared with permission classification, API-key removal,
  and rendering of process runs and shell process states as tool outcomes.
- `src/tools/search.rs`: The `glob` and `grep` schemas, ripgrep file discovery,
  workspace-checked candidates, and bounded glob and grep results.
- `src/tools/patch.rs`: The `apply_patch` schema with its patch-language
  description, patch parsing, exact text matching, workspace path validation,
  and filesystem changes.
- `src/tools/subagent.rs`: The main agent's `start_subagent`, `send_message`,
  `stop_subagent`, and `wait` schemas, argument validation including the
  16 KiB message limit and wait bounds, and rendering of their results. A call
  from a subagent fails.
- `src/tools/workspace.rs`: Descriptor-relative file operations shared by read,
  search, and patch tools.
- `src/subagents.rs`: `Subagents`, the prompt-owned guard over one prompt run's
  subagents and their shared state: at most four live subagents including idle
  ones, each with its child session, queued follow-up messages, busy, idle, or
  stopping status, cancellation, and Tokio task; the queue of agent messages
  for the main agent; and the notification waits use. Each task runs the
  shared loop against its child session with the inherited settings, system
  prompt, global hooks, and shell processes, publishes each final answer or
  failure with bounded text, then starts the next queued message or goes idle.
  Stopping, normal shutdown, and dropping the guard close admission and cancel
  subagents; stopping and shutdown also await their tasks.
- `src/sessions.rs`: Transcript types, including turn starts that save each
  turn's input with its model, effort, and mode, ordered user message parts and
  image attachments, skill invocations, hook kinds, hook feedback, assistant
  batch entries with tool outcomes in call order, model usage, compaction
  checkpoints with their summarizer cost, agent messages saved only in main
  sessions, transcript cost, transcript entry encoding as stored JSON,
  transcript validation, saved settings from the latest turn start, database
  path selection, and `SessionStore` over one SQLite connection, with child
  sessions linked to their main session by `parent_session_id`, hidden from
  listing, deleted with it, and summed by the child cost query.
- `src/acp.rs`: Connection wiring, `ServerState`, lazy OpenRouter client,
  request handlers, advertised slash commands and their prompt dispatch, the
  settings from `~/.config/ox/settings.json` and the home directory captured at
  startup, per-session model, effort, and mode selections, workspace settings,
  system prompts, and skill catalogs loaded when a session becomes active,
  per-session shell processes kept across repeated loads, deletion spawned under
  its operation guard that stops the session's shell processes after the
  database deletion, connection shutdown on incoming EOF or SIGINT, SIGTERM, or
  SIGHUP that finishes every shell process cleanup after any connection result,
  guarded `/compact`, usage updates after `/compact` and load with the saved
  cost of child sessions, loading that rejects child sessions, and the
  automatic headless prompt entry point, which stops its shell processes before
  returning.
- `src/acp/operations.rs`: At most one prompt, load, or delete running for a
  session at a time, enforced by an operation guard; `/compact` runs as a prompt
  operation. Once connection shutdown begins, no operation starts.
- `src/acp/prompt.rs`: The shared loop that runs one agent turn, `AgentTurn`,
  for the main agent and every subagent, and `Presentation`, which sends the
  main agent's ACP updates, suppresses a subagent's or a headless run's, and
  sends Ask mode permission requests under the main session with tool call IDs
  scoped by an `AcpIdentity`. One turn: reject oversized input before saving,
  save accepted input with captured settings as one turn start, announce it and
  run `before_run` hooks, save and present published agent messages before
  each model request and before `before_stop` on a finished answer, compact
  before large model requests, retry explicit input overflow once, process each
  assistant batch in the one step that owns it (run `before_tool` before each
  tool call, request Ask mode permission for shell starts and shell process
  writes, run tools with a `ToolContext`, and on every exit give each call an
  outcome and attempt the save), send a usage update after each committed batch
  and after each automatic compaction, run `after_tools` after each batch and
  `before_stop` on each finished answer, stop the main agent's subagents once
  its result is known, run `after_run` on the result, and return an outcome
  carrying the accepted answer only when finished. The main agent creates the
  `Subagents` owner; a subagent runs only global hooks.
- `src/acp/convert.rs`: ACP text and image input conversion, session update
  construction including each tool call's kind, shell permission content for
  commands, background starts, and shell process input with a subagent's
  scoped tool call ID and title, hook runs, agent messages shown as attributed
  tool calls both live and in replay, and usage updates whose cost includes
  child sessions, and transcript replay.
- `examples/skills/goal/`: An example skill whose `before_stop` hook,
  `scripts/judge.py`, asks a headless `ox run` to judge each answer, with its
  test and a README describing the `before_stop` protocol and installation.
- `examples/skills/careful/`: An example skill whose `before_run`,
  `before_tool`, `after_tools`, and `after_run` hooks, all `scripts/careful.py`,
  supply the Git status, deny destructive shell commands and shell process
  input, check each patch batch, and log each run outcome, with its test and a
  README describing every hook kind, batch timing, hook errors, and global
  hooks.

## Validation

For changes affecting behavior, interfaces, artifacts, or builds, run full
validation: `cargo fmt --all -- --check`,
`cargo test --all-targets --all-features`, `cargo build --all-features`,
`cargo clippy --all-targets --all-features -- -D warnings`, and both example
test commands in `eng/testing.md`. Report any skipped or failed check; do not
call partial validation complete. For documentation-only, comment-only, and
filename-only changes, use focused searches and diff inspection.

## Documentation

Use `YYYY-MM-DD-NNN-slug.md` filenames for:

- Plans in `eng/plans/`.
- Code reviews in `eng/reviews/`.

Plan reviews stay in the conversation. Do not create review documents for plans.
Include this rule explicitly when asking Claude or another agent to review a
plan.

Read before planning and changing code:

- `eng/architecture.md`
- `eng/code-style.md`
- `eng/glossary.md`
- `eng/testing.md`

## Backwards Compatibility

Currently, there is none. Recreate `ox.db`, `ox.db-shm`, and `ox.db-wal` instead
of adding migrations or versions. Their directory is `$OX_DATA_DIR`, else
`$XDG_DATA_HOME/ox`, else `~/.local/share/ox`; local install targets do this.

## Communication

- Always describe things directly, clearly, and plainly
- Follow big idea up front and progressive disclosure
- Never use jargon, invented terms, or shorthand
- Never mix definitions or overload terms
