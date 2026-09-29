# Frontier models in the model picker

## Goal

`make install` refreshes an ordered `frontier` array of OpenRouter model IDs in
the user's `config.json` from the Arena Agent Pareto view. The model picker
switches between all offered models and the configured frontier with Left and
Right. The search row shows `All / Frontier` at its right edge, with the active
label white and the other label and slash dim. When no configured frontier model
is offered by the session, the picker shows all models without the toggle.

## Related code

- `Makefile` installs the binaries and copies `examples/settings.json`; it does
  not currently create or update `config.json`.
- `scripts/fetch_arena_agent_models.py` fetches the Arena Agent ranking and the
  OpenRouter catalog, matches names to IDs, and prints a report. The Pareto view
  is at `https://arena.ai/leaderboard/agent/pareto` and has a separate model
  list.
- `crates/ox/src/config.rs` reads the client's `config.json` and supplies the
  bundled server when the file is absent.
- `crates/ox/src/main.rs` and `crates/ox/src/acp.rs` pass the selected server
  into the client; `crates/ox/src/tui.rs` owns the picker, search, keys, and
  drawing.
- `crates/ox/tests/tui.rs` exercises the model picker through a fake ACP server.

## Decisions

- Keep `frontier` in the Ox client's `config.json`, since the client owns the
  picker. Its absence means an empty list. A file containing only `frontier`
  still starts the bundled server; configured servers continue to work.
- Reuse the script's name matching, but read the Pareto model list instead of
  the ranking table. Keep the source order, omit unmatched or ambiguous names,
  and keep only the first occurrence of each matched OpenRouter ID. Fail with a
  clear error if the Pareto list or resulting ID list is empty; leave the old
  config intact on failure.
- Let the script update only `frontier` in the JSON object, preserving server
  entries. `make install` passes `$(CONFIG_DIR)/config.json` to it after the
  binaries are installed. The client matches configured IDs against the
  session's model choices, so other ACP servers may offer none of them.
- Open the model picker on All, preserving the current model selection. Left
  selects All; Right selects Frontier. Keep the search query when switching,
  then filter the chosen list and select its first match. The Frontier list
  follows config order; All keeps the server's order. Only the model picker
  responds to Left and Right this way.

## Naming

- **Model catalog** — the OpenRouter models Ox ACP offers, as defined in
  `agents/glossary.md`.
- **Frontier** — the ordered OpenRouter model IDs in the client's `frontier`
  config field. The picker includes only IDs offered by the active session.
- **All** — every model choice the active session offers, in its original order.
- **Model picker** — the `/model` picker, which shows one of these two lists.

## Test plan

- Exercise the script with saved Pareto markup and a small OpenRouter catalog:
  source order, repeated effort variants, unmatched and ambiguous names, and
  empty results. Exercise config updates with an existing server entry and an
  absent file; a fetch or parse failure must leave an existing config intact.
- In `config.rs`, cover absent `frontier`, a frontier-only file, and preserved
  custom server selection.
- In `tui.rs`, cover the right-aligned search-row colors, both arrow directions,
  Frontier order, filtering across switches, and an absent or unavailable
  frontier. Keep the existing session picker key behavior.
- In `crates/ox/tests/tui.rs`, extend the model picker test to switch lists,
  choose a Frontier model, and verify the selected model changes.

## Implementation plan

1. In `scripts/fetch_arena_agent_models.py`, parse the Pareto model list,
   produce unique matched IDs, and add a config-update entry point that writes
   the full JSON to a temporary file before replacing the target. Add focused
   offline tests in `scripts/test_fetch_arena_agent_models.py`.
2. In `Makefile`, invoke the config-update entry point from `make install` with
   the configured install directory. Add the script's offline test to `check` so
   the new install behavior has a regular check.
3. In `crates/ox/src/config.rs`, add `frontier` with an empty default and make
   omitted servers use the bundled server, then pass the list from
   `crates/ox/src/main.rs` through `crates/ox/src/acp.rs` to the UI.
4. In `crates/ox/src/tui.rs`, retain the server's model choices, derive the
   configured Frontier order, switch the picker view on Left and Right, and draw
   the right-aligned toggle on the search row. Extend its focused tests.
5. In `crates/ox/tests/tui.rs`, extend the terminal test for the visible toggle
   and Frontier selection.
