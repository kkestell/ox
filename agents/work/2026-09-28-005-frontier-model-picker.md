# Frontier models in the model picker

## Plan

`agents/plans/2026-09-28-005-frontier-model-picker.md`

## Summary

`make install` writes the Pareto view's OpenRouter IDs into the `frontier` array
of the client's `config.json`. The model picker opens on All, Right shows the
configured Frontier models the session offers, Left returns to All, and the
search row shows `All / Frontier` at its right edge. Without an offered Frontier
model the picker shows All alone. The plan's goal is met.

## Decisions

- The Pareto page has one element with `role="figure"`, and the model names are
  the titled spans inside it. The parser tracks `div` depth to find where that
  element ends.
- The config update is a `--config PATH` option on the existing script. Without
  it the script prints the ranking report as before.
- An explicitly empty `servers` array also starts the bundled server, because a
  serde default cannot tell an omitted field from an empty one.
- The picker keeps one list of the session's model choices and stores Frontier
  as indexes into it. Both lists share the same price column widths, and Enter
  reads the chosen model from the one list.
- When the Arena or OpenRouter fetch fails, `make install` fails at its last
  step. The binaries are already installed and any existing config is untouched.
- The tmux tests configure `gemma` as the only Frontier model.

## Automated checks

- `make check` — passed, including the new Python unit test step.
- `make e2e` — passed, including the extended
  `model_picker_shows_prices_and_changes_the_model`.

## Manual verification

1. A live config update against the current Arena and OpenRouter data.

   ```sh
   mkdir -p /tmp/ox-frontier-test
   python3 scripts/fetch_arena_agent_models.py --config /tmp/ox-frontier-test/config.json
   cat /tmp/ox-frontier-test/config.json
   ```

   Wrote 14 IDs from the 15 Pareto names, in the page's order. Claude Opus 5
   appears twice on the page with different effort levels and once in the file.
   No temporary file was left beside the config.

## Follow-up work

- A hand-edited `frontier` array with a repeated ID shows that model twice in
  the Frontier list. The script never writes repeats.
