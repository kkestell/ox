# Simplify TUI transcript layout

## Goal

Show each model tool call once, on one terminal line, as its raw tool name and
compact JSON arguments, truncated with an ellipsis when the line is too long.
Word-wrap user messages, visible reasoning, and assistant responses, trimming
leading and trailing whitespace from each complete message. Render visible
reasoning in bright black.

For example, the reasoning paragraph appears in bright black without a label.
Tool lines remain on one terminal row:

```text
> Where does Ox read its settings, and
  how can I select a different server?

I’ll check the settings example and ask
a subagent to verify server selection.

glob             {"pattern":"*config*"}
read_file        {"path":"crates/ox/src/config.rs"}
shell            {"command":"rg -n XDG_CONFIG_HOME crates/ox/src"}
start_subagent   {"prompt":"Check server selection"}
wait             {"seconds":10}

Ox reads its settings from the config
directory. Use `--server` to choose a
configured server by name.
```

These are the tool names Ox ACP sends to the model and JSON argument examples
that match their schemas. The TUI prints the raw name, pads it to 16 columns,
then prints compact JSON. The rendered lines assume a wide terminal; a narrower
one truncates them with `...`. Process and subagent IDs illustrate values
returned by earlier calls. The four subagent tools are available to the main
agent. Compact JSON may reorder object keys; the table shows the resulting order
for rows where it differs from the example arguments.

| Tool name        | Example JSON arguments                                                     | Transcript line                                                                             |
| ---------------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `shell`          | `{"command":"rg -n XDG_CONFIG_HOME crates/ox/src"}`                        | `shell            {"command":"rg -n XDG_CONFIG_HOME crates/ox/src"}`                        |
| `shell_process`  | `{"action":"read","process_id":"p-1"}`                                     | `shell_process    {"action":"read","process_id":"p-1"}`                                     |
| `read_file`      | `{"path":"crates/ox/src/config.rs"}`                                       | `read_file        {"path":"crates/ox/src/config.rs"}`                                       |
| `glob`           | `{"pattern":"*config*"}`                                                   | `glob             {"pattern":"*config*"}`                                                   |
| `grep`           | `{"pattern":"XDG_CONFIG_HOME","path":"crates/ox"}`                         | `grep             {"path":"crates/ox","pattern":"XDG_CONFIG_HOME"}`                         |
| `apply_patch`    | `{"patch":"*** Begin Patch\n*** Add File: notes.txt\n+ok\n*** End Patch"}` | `apply_patch      {"patch":"*** Begin Patch\n*** Add File: notes.txt\n+ok\n*** End Patch"}` |
| `start_subagent` | `{"prompt":"Check server selection"}`                                      | `start_subagent   {"prompt":"Check server selection"}`                                      |
| `send_message`   | `{"subagent_id":"child-1","message":"Check the default server too"}`       | `send_message     {"message":"Check the default server too","subagent_id":"child-1"}`       |
| `stop_subagent`  | `{"subagent_id":"child-1"}`                                                | `stop_subagent    {"subagent_id":"child-1"}`                                                |
| `wait`           | `{"seconds":10}`                                                           | `wait             {"seconds":10}`                                                           |

At a narrower width, the long JSON arguments become one clipped line each:

```text
apply_patch      {"patch":"*** Begin Patch\n*** Add File: notes...
send_message     {"message":"Check the default server too","sub...
```

## Related code

- `crates/ox/src/tui.rs`: `Output` turns ACP updates into text; `Terminal`
  writes it and draws input. Submitted user messages bypass `Output` today.
- `crates/ox-acp/src/acp/convert.rs`: live tool calls, replayed tool calls, and
  permission requests already carry raw input but omit ACP's optional
  programmatic tool name.
- `crates/ox/tests/tui.rs`: isolated tmux tests exercise terminal output,
  streaming, input, permissions, cancellation, and restoration.
- `crates/ox-fake-server/src/lib.rs`: `Script::render`, `Script::ask`, and
  streamed replies provide deterministic terminal test input.
- Follow `AGENTS.md`, `agents/architecture.md`, `agents/code-style.md`,
  `agents/glossary.md`, and `agents/testing.md`.

## Decisions

- Keep the current terminal scrollback approach. Format output as it is written;
  do not introduce a full-screen transcript view.
- Set ACP's `name` on Ox ACP's live and replayed model tool calls and shell
  permission requests. Keep their descriptive tool call titles for other ACP
  clients and permission handling.
- For a model tool call with `name` and `raw_input`, print the name in a
  16-column field, one space, then `serde_json::to_string(raw_input)`. This
  yields compact, escaped, single-line JSON. It does not preserve the model's
  original whitespace or object-key order. If malformed arguments reached Ox
  ACP, its existing string-valued `raw_input` renders as a JSON string.
- Print a model tool call on its initial update; continue merging later updates
  into its existing state without printing status changes, output, or diffs.
  Other ACP servers may omit `name` or `raw_input`; show their title once as a
  fallback.
- Truncate the entire tool line to the available terminal display width with
  `...` when needed, including at very narrow widths. Leave the final column
  unused so the terminal never wraps it automatically. Do not add trailing
  spaces to shorter lines.
- Subagent answers sent as ACP tool-call-shaped updates are not model tool
  calls: they have no `name` or `raw_input`. Keep their label and answer text
  visible, including on replay, rather than treating them as JSON arguments.
- Permission prompts remain a separate interaction: retain the full details and
  numbered choices needed to approve a call. Do not print its transcript line
  again when requesting permission.
- Trim complete messages, never individual ACP chunks. Preserve interior spaces
  and explicit line breaks, including blank lines. Treat consecutive chunks of
  the same message kind as one message; finish it on a change of kind, a tool
  call, a permission prompt, or turn completion. Diagnostics and cancellation
  notices must also finish pending text before printing.
- Continue streaming completed words. Retain an unfinished word and trailing
  whitespace until later text establishes whether they are interior or trailing.
  Flush the last word and discard trailing whitespace at the message boundary.
  Leading whitespace is discarded until the first non-whitespace character.
- Wrap at word boundaries using terminal display width, accounting for the
  existing user prefix. Split words wider than the available line by display
  width. Expand tabs consistently for width calculation. Use the existing
  Unicode width dependency and leave the final column unused. On resize, use the
  new width for subsequent output; do not redraw prior scrollback.
- Apply trimming only to displayed user text; send the original submitted text
  to the ACP server. Keep the input editor unchanged.
- Remove the `Thinking` heading. Use crossterm's `Color::DarkGrey`, the
  bright-black ANSI color, for visible reasoning. Reset the foreground before
  other output and during terminal restoration. Emit trusted style commands
  separately from escaped user and server text.

## Naming

Use the existing glossary terms, especially tool call title and visible
reasoning. No new domain terms are needed.

## Test plan

- In `crates/ox/src/tui.rs`, give message formatting table-driven coverage for
  leading and trailing whitespace, whitespace-only messages, interior blank
  lines, words and spaces divided across chunks, explicit newlines, tabs, long
  words, Unicode display width, and the user prefix. Equivalent text must render
  identically regardless of chunk boundaries.
- In `crates/ox-acp/src/acp/convert.rs`, verify the raw tool name accompanies
  the arguments in live, replayed, and permission updates. Keep the existing
  title assertions for other ACP clients.
- Replace the printed-status expectation in
  `partial_tool_updates_retain_omitted_fields`: omitted fields still survive,
  but repeated updates produce no extra transcript lines. Cover compact JSON,
  escaped newlines, object-key ordering, malformed arguments, omitted optional
  fields, short and truncated lines, and subagent answers.
- Retain the control-character escaping guarantee. Cover message finalization at
  tool calls, permissions, normal completion, errors, and cancellation so
  buffered words are not lost and trailing whitespace is not printed.
- In `crates/ox/tests/tui.rs`, capture physical rows without `capture-pane -J`
  for layout assertions, and capture ANSI attributes with `-e` for color
  assertions. Exercise a narrow terminal with the fake server: wrapped text, one
  line of name and JSON per model tool call despite updates and content,
  truncation with `...`, bright-black reasoning without a heading, and default
  foreground restored for the assistant response and input.
- Strengthen the existing permission test to ensure full permission details
  remain visible after ordinary tool content is hidden. Keep streaming and
  terminal-restoration coverage intact.

## Implementation plan

1. In `crates/ox/src/tui.rs`, replace direct chunk concatenation with a small
   stateful message formatter. Share it between submitted user messages and ACP
   message chunks; keep pending text, message kind, and column tracking with the
   output owner. Add focused formatting tests alongside it.
2. In `crates/ox-acp/src/acp/convert.rs`, populate ACP's `name` on model tool
   calls and shell permission requests without changing descriptive tool call
   titles. Extend the conversion tests for live and replayed calls.
3. In `crates/ox/src/tui.rs`, separate model tool call transcript output from
   permission details and subagent answers. Track whether each existing tool
   state has been displayed, retain partial-update merging, and emit a clipped
   name-and-JSON line once. Update the owning unit test and add layout cases.
4. Connect formatting and style commands to `Terminal` and the `run` event loop.
   Finalize messages at output boundaries, handle current terminal width, and
   reset reasoning color on every exit path. Preserve text escaping.
5. Extend `Script::render` in `crates/ox-fake-server/src/lib.rs` with whitespace
   around streamed reasoning and responses, words split across chunks, and tool
   updates carrying raw names, JSON arguments, and multiline content.
6. Add focused layout and color tests in `crates/ox/tests/tui.rs`, using
   physical rows and ANSI attributes without changing existing joined-text
   captures. Strengthen the permission-detail assertion in the existing
   interaction test.
