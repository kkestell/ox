# Start Ur without a settings file

## Plan

`agents/plans/2026-09-29-004-default-model-without-settings.md`

## Summary

Ur now starts with no `~/.config/ur/settings.json`. A missing file, or one
without `model`, uses `~deepseek/deepseek-flash-latest`. If that alias is not in
the model catalog, startup fails with an error that names the file and the
`model` setting. Nothing is written at startup. The plan's goal is met.

## Departures from the plan

- The plan named one table-driven test for loading the global file. Its cases
  cover two guarantees, so there are two tests: defaults without a file or
  `model`, and files that must be valid JSON naming a known model. This follows
  `agents/testing.md`.

## Decisions

- Line counts from `rsloc` on `crates/ur/src/settings.rs` against HEAD:
  production 160 to 175 (+15), tests 183 to 224 (+41), docs 15 to 20 (+5). The
  production growth is the built-in model constant and its catalog check with an
  error message.

## Automated checks

- `make check` — Passed: dprint, rustfmt, all workspace tests, build, and
  clippy.
- `make e2e` — Not run. No terminal behavior changed, and `crates/ox` has no
  diff.

## Manual verification

1. `ur run` works with an empty home directory, and no settings file appears.

   ```sh
   H=$(mktemp -d); W=$(mktemp -d); D=$(mktemp -d)
   HOME=$H UR_DATA_DIR=$D OPENROUTER_API_KEY=<key> \
     ./target/debug/ur run --dir $W 'Reply with the single word: ready'
   find $H -type f | wc -l
   ```

   OpenRouter accepted the alias ID with its leading tilde. The reply was
   `ready`, the exit status was 0, and the temporary home directory held no
   files.
