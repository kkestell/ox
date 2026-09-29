# Naming review — client and server

## Scope and coverage

Reviewed the production code of all three crates against `agents/glossary.md`,
`agents/architecture.md`, `agents/code-style.md`, and the communication rules in
`AGENTS.md`. Only the `naming` lens was used. Two reviewers each read one part:
one read `crates/ox/` and `crates/ox-fake-server/`, the other read
`crates/ox-acp/`. Each finding below was checked against the code before it was
recorded. The earlier naming reviews (2026-09-21-001, 2026-09-22-001,
2026-09-23-004) and the 2026-09-20-003 terminology review were read first, and
their resolved findings are not raised again unless later code reopened them.
The `hook` vocabulary the last naming review touched is gone from the code and
docs.

Coverage gaps: the client and fake server were read in full. In `ox-acp`, test
bodies were read by test name and helper name only. `process.rs`, `skills.rs`,
`tools/workspace.rs`, `tools/patch.rs`, `tools/read.rs`, and `tools/search.rs`
were read in part, and the tails of `tools/shell.rs`, `shell_processes.rs`, and
`openrouter.rs` were searched for stale terms, not read. `examples/` and
`scripts/` were not reviewed. Candidates judged not to cause a misreading were
left alone: `Terminal.title` inside `Terminal`, the fake server's `loaded`
session set (its comment says "created or loaded"), and `Owner`, the test
harness struct in `subagents.rs`.

## Fixed

- **`Session.pending` was a queue of permission requests, and a doc called the
  queued prompt "pending"** (`crates/ox/src/acp.rs:34`, `:146`): three things
  were "pending": the permission requests, the queued prompt, and ACP's
  `ToolCallStatus::Pending`. The field is now `permission_requests`, and the
  `prompt` doc says "already queued".
- **"option" and "choice" named four things in `tui.rs`**
  (`crates/ox/src/tui.rs:460`, `:677`, `:855`): `select_option` returned a
  `SessionConfigSelect`, while ACP's `SessionConfigSelectOption` is one value of
  it. `options` was the config option list in three functions and the permission
  option count in `key`, and `choices` was both the select values and the
  permission option count in `draw`. Now `select_config_option`,
  `config_options`, `approval_option_count`, and `option_count`. The test
  `long_approval_keeps_options_and_composer_visible_and_pages_through_details`
  follows.
- **Approval names collided in `draw`** (`crates/ox/src/tui.rs:414`, `:458`,
  `:154`): `approval` was a `Vec<Line>` beside `screen.approval`, `request` was
  an `Approval` (`request.request.options`), and the local `approval_lines`
  shadowed the function of that name while holding only the body count. So did
  `Layout.approval_lines`. `Ui.selected` sat beside `approval_scroll` and
  `Picker.selected` and meant the approval choice. Now `dialog_lines`,
  `approval`, `approval_body_lines`, and `approval_selected`.
- **`escape` did not say what it escapes** (`crates/ox/src/tui.rs:40`): the
  transcript docs "Escaped Markdown" and "Escaped and trimmed text" read as
  Markdown escaping. It is now `escape_control_characters` with a doc, and the
  two transcript docs say what is escaped.
- **`config` named a path, a `Config`, and a directory**
  (`crates/ox/src/acp.rs:283`, `crates/ox/src/tui.rs:166`,
  `crates/ox/src/main.rs:38`, `crates/ox/src/config.rs:127`): the path is now
  `config_path`, and the directory in `config::path` is `config_home`.
- **The fake server called its saved sessions "history"**
  (`crates/ox-fake-server/src/lib.rs:70`): `SavedHistory` holds saved sessions,
  and its own doc and `Saved.sessions` say so, while the glossary bars "history"
  as a transcript synonym. It is now `SavedSessions`, its bindings are
  `saved_sessions`, and the comments and panic messages say "saved sessions".
- **Docs used "prompt run" for one turn** (`crates/ox-acp/src/acp/prompt.rs:1`,
  `:194`, `:233`, `crates/ox-acp/src/compaction.rs:35`,
  `agents/architecture.md:21`): the glossary's prompt run includes subagents,
  but `prompt::run` runs one turn for the main agent or a subagent. The module
  doc, the `PromptOutput` and `PromptOutcome` docs, and the compaction threshold
  doc now say "turn". `PromptOutput`'s doc no longer calls itself an "outcome",
  which is the other type's name. `PromptInput.selected_settings` says session
  settings, since a subagent passes inherited ones. The architecture component
  now runs "the turns of one prompt". The type names are OX-0008.
- **Server comments said "Ox" for the server**
  (`crates/ox-acp/src/sessions.rs:302`, `:455`,
  `crates/ox-acp/src/openrouter.rs:49`, `:106`,
  `crates/ox-acp/src/shell_processes.rs:82`, `:503`,
  `crates/ox-acp/src/process.rs:229`, `crates/ox-acp/src/compaction.rs:33`,
  `crates/ox-acp/src/acp/convert.rs:287`,
  `crates/ox-acp/src/tools/search.rs:111`): the glossary defines Ox as the
  client, and its own tool outcome entry says `ox-acp` knows what happened to a
  tool call, while `sessions.rs:455` said Ox. The comments now say "Ox ACP".
  Strings and the model persona are OX-0010.
- **`SessionOperations::close` meant connection shutdown, while `begin_close`
  closed one session** (`crates/ox-acp/src/acp/operations.rs:31`, `:107`,
  `:117`): `close` took no session and `closed` meant shutdown. They are now
  `begin_shutdown`, `shutting_down`, and `is_shutting_down`, matching
  `ServerState::begin_shutdown` and `ShellProcesses::begin_shutdown`.
  `agents/architecture.md` listed a session operation as "prompt, load, or
  delete" while the glossary and `operations.rs` include close. It now says
  "prompt, load, close, or delete".
- **"admission" came back for the session operation guard**
  (`crates/ox-acp/src/acp/operations.rs:121`, `crates/ox-acp/src/acp.rs:1240`,
  `crates/ox-acp/src/subagents.rs:55`, `:142`, `:411`, `:641`,
  `crates/ox-acp/src/acp/prompt.rs:521`, `crates/ox-acp/src/compaction.rs:168`):
  the 2026-09-20-003 review told the code to avoid the word for this mechanism.
  It also named stopping new subagent work and the context limit check, and
  `compaction.rs:168` called a transcript a "prompt". Those comments and one
  test name now say what happens. `Budget.admission` keeps its name.
- **"owner" named the registry, the holder of a process handle, and a test
  local** (`crates/ox-acp/src/shell_processes.rs:4`, `:68`, `:79`, `:401`,
  `crates/ox-acp/src/tools/shell.rs:527`, `crates/ox-acp/src/acp.rs:569`): the
  docs now say `ShellProcesses`, "shutdown", or "every `ShellProcess` handle",
  and the test locals are `shell_processes`, which is what `tools/shell.rs`
  already called the parameter.
- **`settings.rs` bound `workspace_path` to the settings file**
  (`crates/ox-acp/src/settings.rs:126`, `:290`): `for_workspace` uses the name
  for the workspace directory. The file is now `workspace_file`, and `save`
  takes `workspace_path`. The `Settings` doc said "effective settings for one
  workspace", but `load` returns the global file's values until `for_workspace`
  applies the workspace file. It now says that.
- **Stale terms in test names and messages**
  (`crates/ox-acp/src/acp/convert.rs:413`, `crates/ox-acp/src/acp.rs:670`,
  `:1958`, `crates/ox-acp/src/compaction.rs:548`,
  `crates/ox-acp/src/subagents.rs:505`): the test named
  `prompt_to_user_message_…` tests `prompt_message`. An assertion said "setting
  entries are not replayed" though no setting entries exist. Another said
  "automatic trigger", but the field is `automatic_threshold`. A doc said
  "tool-result limit", but the constant is `OUTPUT_LIMIT`. A `TurnInput` was
  bound to `turn`. All now match the code.

## Findings

### Medium

#### Naming

- **OX-0007 "Agent message" names two things**
  (`crates/ox-acp/src/acp/convert.rs:102`, `:288`,
  `crates/ox-acp/src/sessions.rs:66`, `agents/glossary.md:23`): the glossary
  defines an agent message as a subagent's answer or failure, and the code names
  it `AgentMessage`. ACP's `AgentMessageChunk` is the assistant's own text, and
  `convert::agent_message_chunk` builds it. In `convert.rs:333-343` one `match`
  in the replay code calls `agent_message_updates` for subagent messages and
  `agent_message_chunk` for assistant text. `prompt.rs:478` and `:544` do the
  same. `TranscriptEntry::AgentMessages` sits beside `AssistantBatch`, so
  "agent" reads as the main agent. Comments, error text, and the model-visible
  tool text already say "subagent message" (`openrouter.rs:480`,
  `sessions.rs:795`, `convert.rs:285`, `prompt.rs:521`, `:537`,
  `tools/subagent.rs:115`, `:214`, `:216`, `:220`). As a result, a reader cannot
  tell which "agent message" a function handles. Fix: rename to
  `SubagentMessage` in code and the glossary: `AgentMessage`,
  `AgentMessageContent`, `TranscriptEntry::AgentMessages`, `agent_messages`,
  `agent_message_text`, `agent_message_updates`, `deliver_agent_messages`, and
  `append_agent_messages`. Keep `agent_message_chunk`. The decision is that the
  saved entry kind `"agent_messages"` (`sessions.rs:1010`, `:1021`, and the test
  at `:1682`), the error strings at `sessions.rs:573`, `:803`, and `:813`, and
  the tool call ID prefix `agent-message-` (`convert.rs:298`, `:633`) change
  too. `ox.db` is recreated, so no migration is needed.

### Low

#### Naming

- **OX-0008 The turn functions and types are named for the prompt**
  (`crates/ox-acp/src/acp/prompt.rs:177`, `:194`, `:233`,
  `crates/ox-acp/src/cancellation.rs:10`): `prompt::run` runs one turn for the
  main agent or a subagent. `subagents.rs:340` calls it with a `PromptInput`,
  and subagent turns observe a `PromptCancellation`. The glossary's prompt run
  is the work of one prompt, including subagents, so `PromptInput`,
  `PromptOutput`, and `PromptOutcome` describe a turn under a prompt name. The
  docs now say turn, but the names do not. Commit 0321bf1 renamed `PromptRun` to
  `AgentTurn` and left the rest. Fix: decide whether to rename them to turn
  names or keep them. `TurnInput` is already the saved user message or skill
  invocation, so `PromptInput` cannot take that name.
- **OX-0009 The client's "resume" uses the name of a different ACP method**
  (`crates/ox/src/acp.rs:32`, `:61`, `:365`, `crates/ox/src/tui.rs:162`,
  `:745`): `/resume` lists saved sessions and calls `session/load`, which
  replays the transcript. `can_resume` is true when the server advertises load,
  list, and close. ACP has its own `session/resume`, "without returning previous
  messages (unlike `session/load`)", with a `sessionCapabilities.resume` flag
  (`agent-client-protocol-schema` 1.7.0, `src/v1/agent.rs:1278`, `:4343`), and
  Ox ACP does not implement it. As a result, a reader comparing the client to
  the protocol expects `can_resume` to test that flag. `agents/architecture.md`
  and the glossary say "load". Fix: keep `/resume` as the command, and name the
  internals for what they do, for example `can_switch_sessions` and
  `session_picker_after_turn`. The other choice is to rename the command.
- **OX-0010 Server strings, the model persona, and the bundled server's name say
  "Ox"** (`crates/ox-acp/src/acp.rs:580`,
  `crates/ox-acp/src/tools/shell.rs:126`,
  `crates/ox-acp/src/prompts/system_prompt.md:1`,
  `crates/ox-acp/src/system_prompt.rs:1`, `crates/ox/src/config.rs:34`): the
  glossary defines Ox as the client and Ox ACP as the server. The server sends
  "Ox is shutting down" to any ACP client, and the test at `acp.rs:3599` asserts
  it. The shell tool text the model reads says "or Ox exits" and "Ox's
  permissions". The system prompt says "You are Ox", and the tests at
  `system_prompt.rs:93` and `openrouter.rs:1302`, `:1401`, `:1430` repeat it.
  `Config::bundled_server` names the server "Ox", which is also the `--server`
  value. Fix: decide whether "Ox" is the agent's name, then widen the glossary
  entry to say so, or change these strings and the server name to "Ox ACP".
- **OX-0011 The client says "line" and "row" for the same thing**
  (`crates/ox/src/tui/input.rs:13`, `:140`, `:148`, `crates/ox/src/tui.rs:147`,
  `crates/ox/src/tui/transcript.rs:622`, `:646`, `crates/ox/tests/tui.rs:352`):
  `input.rs` uses "line" for an unwrapped logical line and "row" for one wrapped
  display row. `Rows.lines` holds rows, `Layout.lines` counts rows, and
  `item_lines`, `content_lines`, `described_lines`, `approval_lines`, and
  `picker_lines` return rows. The `hanging` and `wrap` docs call a logical line
  "a wrapped row" that has "rows after the first". The e2e test says
  `one_line_per_event`, while the unit test at `tui.rs:1279` and commit 88109ea
  say row. As a result, a reader cannot tell whether a count is logical lines or
  screen rows. Fix: use "row" for display rows and "line" for logical lines in
  those names and docs. Renaming the fields and functions is the decision.
- **OX-0012 The README calls saved sessions "history" and denies the resume
  command** (`README.md:44`): it says the client "has no local saved history,
  session browser, or resume command", but `/resume` and the session picker
  exist (`crates/ox/src/tui.rs:745`). The glossary bars "history" as a
  transcript synonym. Line 35 also says slash commands are sent as ordinary
  text, though `/model`, `/new`, `/quit`, and `/resume` are handled by the
  client. Fix: correct or delete those passages. `README.md` had uncommitted
  edits when this review ran, so it was left alone.

## Checks run

- `cargo fmt --all` and
  `cargo clippy --workspace --all-targets --all-features -- -D warnings` are
  clean.
- `make check` passes: dprint, rustfmt, all workspace tests (66 client unit
  tests, 164 `ox-acp` tests), build, and clippy.
- `make e2e` passes (12 tmux tests).
- Searched the workspace for every old name after the renames, including
  `SavedHistory`, `select_option`, `is_closed`, `prompt_to_user_message`, and
  the client's `escape`. No matches remain outside dated plans and reviews.

## Verdict

The core vocabulary holds: transcript, turn start, session settings, session
title, tool call title, and the config option ids match between the client, the
fake server, and Ox ACP, and the hook vocabulary is gone. The fixes rename
overloaded words in the client's dialog and config code and put the server's
comments and docs back in line with the glossary. One medium finding is open:
"agent message" names both a subagent's message and ACP's assistant text, and
fixing it changes a saved entry kind and an ACP-visible ID. Five low findings
need a naming decision or a README edit. No behavior changed.
