# Discover and complete the agent's slash commands

- Task: `2026-11-12-015-slash-command-completion`
- Kind: plan
- Status: ready
- Source HEAD: `411c30564ff9ff9c6e57a08c33c800b9d2ed56ee`
- Dependencies: none

## Outcome and boundaries

Typing `/` in the composer discovers the current agent's commands and skills,
with descriptions, argument hints, keyboard and pointer selection. Selection
inserts `/name `; ordinary submission executes it through the existing prompt
path. Support initial ACP catalogs and replacement notifications from any agent,
including an empty replacement. Preserve attachments, unknown manually typed
commands, normal submission, cancellation and session isolation. No new command
execution API, dependencies, filesystem skill scanner or skill manager.

## Current system

`Bridge::prompt` lazily starts Claude; `Output::translate` discards successful
control-response data. `oxd` relays command notifications but ignores setup
catalogs and keeps no current catalog. `Composer` receives `SessionInfo` through
the workspace watch and implements Enter-to-send. `ModelPicker` supplies the
existing menu styling and keyboard precedent.

## Design

Start the session's existing Claude process during `session/new` and
`session/resume`. Factor shared startup from `Bridge::prompt`; preserve restart
after native exit. Send native `initialize` and await its control response using
the existing event-driven control channel, retaining response data. Do not hold
mutex guards across awaits. Initialization rejection or exit fails the opening
request; closing stdin stops the process when opening fails. Reuse a running
Claude on resume. Discovery itself sends no prompt and consumes no model turn.

Keep the latest canonical native commands with the running Claude. Map `name`,
`description`, and nonempty `argumentHint` to ACP command/text-input types.
Validate native descriptors at this boundary; do not invent commands or derive
them from filenames. Include the complete current catalog in setup responses.
Translate native `system/commands_changed` to replacement
`available_commands_update`, including clearing. Do not create a separate
discovery process or polling loop. Sources: [ACP command contract][acp] and
[Claude command events][sdk].

Add typed `available_commands` to daemon `Agent`, protocol `SessionInfo` and
`AgentStarted`, updating every caller. Opening responses replace the catalog,
including omission; live notifications replace it again. Consequently resumed
sessions obtain fresh commands even when replay is discarded, and new watchers
receive current state. Preserve unknown ACP variants in transport; the UI offers
known command variants and falls back to plain input for unknown input kinds.

Extract a focused command-menu component from composer wiring. Match names by
case-insensitive prefix in native order while the caret edits the leading slash
token. Up/Down select; Enter/Tab insert; Escape dismisses; pointer selection
retains textarea focus. Replace only the token, preserve arguments/attachments,
and place the caret for argument entry. Keep Shift+Enter and IME behavior.
Without suggestions, existing submission applies. Connect the listbox accessibly
to the textarea. Reuse `.menu`, shared listbox layout and existing tokens with
`ModelPicker`; add only necessary placement and overflow CSS.

## Check your assumptions

All four entries accepted by the user.

1. **Opening a Claude session starts Claude immediately.** This moves process
   memory, startup latency and startup failures earlier, replacing lazy startup.
   Native initialization is the verified source of the effective pre-prompt
   catalog; a separate probe process would duplicate startup and configuration.
2. **Choosing a command edits the draft without sending it.** Enter/Tab accept
   completion, then ordinary Enter submits. Immediate execution is given up so
   users can supply arguments and review attachments.
3. **Completion applies to the leading slash token only.** Use prefix matching,
   not fuzzy search or slash completion inside prose. Native dispatch operates
   on the leading command; ordinary text entry remains unchanged.
4. **Catalog refresh follows native events and session reopening.** Newly edited
   skill files are not proactively reloaded. Native `commands_changed` exists;
   automatic disk watching/reload controls would add a separate feature.

## Implementation sequence

1. Bridge: startup/control-response ownership, command translation and native
   events in `bridge.rs`, `runner.rs`, `updates.rs` and focused `commands.rs`;
   update request handlers in `main.rs` and protocol fixtures.
2. Daemon/protocol: setup catalogs, live replacement, watch snapshots and all
   constructors in `session.rs`, `state.rs`, `messages.rs` and their tests.
3. UI: protocol type, `CommandMenu`, composer interactions and shared menu CSS;
   extend `oxd-fake-agent` and add command-completion e2e cases.
4. Update support documentation and add a temporary project skill fixture to
   `scripts/smoke-acp.py` for catalog discovery and native invocation.

## Verification

- Bridge tests: pre-prompt catalog, hints, dynamic replacement/clear, resumed
  discovery, one reused process, initialization failure/exit and local-command
  acknowledgement/idle; preserve permission/config/cancel regressions.
- Daemon tests: setup replacement/omission, live replacement/clear, reconnect
  snapshot and resume with retained history. Use existing production interfaces.
- E2E: filter, hints, keyboard/pointer insertion, no premature submission,
  ordinary `/name args` execution, Escape/no matches, replacement/clear and
  session switching. Inspect menu against its model-menu peers.
- Planning probes passed: Claude 2.1.323 accepted text-block skill arguments;
  `/model` and `/clear` completed locally. Actual `oxctl → oxd → bridge → Claude`
  returned `OX_NATIVE_COMMAND_OXPATH` with `end_turn`; artifacts:
  `/private/tmp/ox-command-path-2gacsu61`.
- Run once after implementation: `make check`, `make e2e`, `make smoke-claude`
  (configured Haiku). Prove regression tests fail with their fix removed.

[acp]: https://github.com/agentclientprotocol/agent-client-protocol/blob/main/docs/protocol/v2/slash-commands.mdx
[sdk]: https://code.claude.com/docs/en/agent-sdk/typescript#sdkcommandschangedmessage
