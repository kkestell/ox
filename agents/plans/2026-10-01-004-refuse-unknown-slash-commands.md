# Refuse unknown slash commands

## Goal

OX-0025: pressing Enter on `/mo` sends it to the model as a user message,
because the Ox client matches only exact command names and the Ox server treats
any unrecognized first word as plain text. When this is done, the Ox client
refuses a prompt whose first word looks like a slash command but names no known
command. It shows a notice and keeps the text in the composer, so Tab can
complete it or the user can edit it.

## Related code

- `crates/ox/src/tui.rs:924` — the Enter branch of `key`. It matches `/resume`,
  `/quit`, `/new`, and `/model` exactly and sends everything else.
- `crates/ox/src/tui.rs:758` — `commands`, the names the composer completes: the
  client's own and the session's available commands.
- `crates/ox/src/tui/input.rs:53` — `Input::ghost_text`, the existing composer
  helper that reads the slash word against `commands`.
- `crates/ox-server/src/skills.rs:98` — a skill name may contain only lowercase
  letters, digits, and hyphens. The new check uses the same character set.
- `crates/ox-server/src/acp.rs:132` — `dispatch`, the Ox server's command
  parsing. It is unchanged.

## Decisions

- **The Ox client refuses; the Ox server does not change.** The Ox client
  already holds the full command list, including its own commands, which the Ox
  server never sees. Other ACP clients and headless runs keep sending text as
  written.
- **Refuse rather than complete on Enter.** Completing and running on Enter
  would start a skill that takes arguments without them. Refusing covers both a
  partial name (`/mo`) and a misspelled one (`/modle`) with one rule, and Tab
  already completes.
- **A slash command word is `/` followed by one or more lowercase letters,
  digits, or hyphens**, the first word of the trimmed input. Pasted paths such
  as `/Users/kyle/x.rs` or `/tmp/out` are not slash command words, so they are
  still sent. A lone `/` is also still sent.

## Naming

- `slash command word` — the first word of the composer's trimmed text when it
  is `/` followed by one or more lowercase letters, digits, or hyphens. Used in
  the doc comment of `Input::unknown_command`.
- `Input::unknown_command(&self, commands: &[String]) -> Option<&str>` — returns
  the slash command word when its name is not in `commands`.

## Test plan

- `crates/ox/src/tui/input.rs`: a table-driven test,
  `unknown_command_names_a_slash_command_word_missing_from_the_commands`, with
  commands `["compact", "model"]`. Refused: `/mo`, `/modle`, `/mo more text`,
  `/mo`, `/mo\nmore`. Not refused: `/model`, `/model x`, `/compact now`, `/`,
  `/Users/kyle/x.rs`, `/tmp/out`, `hi /mo`, plain text, and the empty input.
  Each case names its input in the failure message.
- `crates/ox/src/tui.rs`: `enter_refuses_an_unknown_slash_command` uses
  `with_session` and `press`, as
  `new_command_replaces_the_session_and_quit_command_quits` does. Pressing Enter
  on `/mo` leaves `/mo` in the composer, adds the notice `Unknown command /mo`,
  and starts no prompt (`session.busy` stays false).

## Implementation plan

- In `crates/ox/src/tui/input.rs`, add `Input::unknown_command` beside
  `ghost_text`, and its test.
- In `crates/ox/src/tui.rs`, in the Enter branch, after the `/model` case and
  before `send`, add a case: if `ui.input.unknown_command(&commands(session))`
  returns a word, show a red notice `Unknown command {word}` with
  `ui.view.notice` and keep the input. Add the key test.
- Mark OX-0025 done in `agents/todo.md`.
