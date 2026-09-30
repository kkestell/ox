# Provider pins

## Plan

`agents/plans/2026-09-30-001-provider-pins.md`

## Summary

The global settings file's `models` key pins a model's model requests to the
listed OpenRouter providers, tried in order without fallbacks. A workspace
settings file with `models` is an error. The plan's goal is met.

## Automated checks

- `make check` — passed.

## Manual verification

1. A pin reaches OpenRouter, and a workspace `models` key is rejected. Build
   `ox`, then run headless prompts with a temporary home directory whose global
   settings file pins `deepseek/deepseek-v4.1-flash` first to `deepseek`, then
   to `no-such-provider`, and finally with `{"models":{}}` in the workspace
   settings file.

   ```sh
   cargo build -p ox
   H=$(mktemp -d); W=$(mktemp -d); mkdir -p $H/.config/ox
   printf '{"model":"deepseek/deepseek-v4.1-flash","models":{"deepseek/deepseek-v4.1-flash":{"providers":["deepseek"]}}}' \
     > $H/.config/ox/settings.json
   HOME=$H OX_DATA_DIR=$H/data OPENROUTER_API_KEY=... \
     ./target/debug/ox run --dir $W "Reply with the word ok."
   ```

   With `deepseek`, the prompt answered `ok`. With `no-such-provider`,
   OpenRouter returned 404 "No endpoints found", with every endpoint removed by
   its "Filter by Fallback" step. With the workspace `models` key, `ox` exited
   with
   `<workspace>/.ox/settings.json: models can be set only in the global
   settings file`.
