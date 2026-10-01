# OpenAI subscription support

## Plan

`agents/plans/2026-10-01-001-openai-subscription.md`

## Summary

The plan's goal is met. Live ChatGPT inference completed tool turns through both
headless and terminal clients, including compaction and a subsequent turn.

## Decisions

- The authenticated catalog uses `models`, `slug`, `display_name`, `visibility`,
  `context_window`, `input_modalities`, `supported_in_api`, and
  `supported_reasoning_levels`. Keep account order and use `context_window`,
  rather than the larger `max_context_window`. Map supported efforts to Ox's
  existing effort levels.
- Live subscription streams finish output through `response.output_item.done`
  and send an empty `response.completed.output`. Validate the assembled items
  only after successful completion, including their indices, identities, and
  streamed deltas. The initial live check rejected this shape; its regression
  test now owns acceptance and rejection of separately completed items.
- The credential lock also coordinates host creation, final login replacement,
  and logout with rotating refresh tokens. It never covers model requests or
  session state. After logout, login defaults to the saved registration and
  offers account replacement while retaining the host ID.

## Automated checks

- `make check` — passed. Initial Clippy fixture warnings were corrected.
- `make e2e` — passed all 12 isolated terminal tests.
- `cargo test -p ox-server openai --all-features` — passed all 23 focused tests.

The complete test diff was inspected. New owning tests cover provider command
syntax and settings, local authorization and rotating credentials, Responses
encoding and validated streams, prompt batch commits and cancellation,
subagents, compaction and overflow recovery, and unpriced usage. Existing
OpenRouter and compaction guarantees retain their tests; shared type changes
move their imports, not their ownership. Bare authentication command acceptance
was replaced by mandatory provider acceptance. Child cost aggregation now also
checks saved token usage with absent cost. No other guarantee lost its test.

## Manual verification

1. Ran `target/debug/ox auth login openai`; the browser callback completed and
   Ox saved its own credentials. Fetched the authenticated public catalog to
   verify its shape before defining the parser.
2. Temporarily selected `openai` in global settings, removed OpenRouter model
   IDs and pins, and used a temporary workspace and `OX_DATA_DIR`:

   ```sh
   target/debug/ox run --dir "$workspace" \
     'Create acceptance.txt containing subscription tool turn passed. Read it back with a tool, then answer OPENAI_HEADLESS_ACCEPTANCE_PASSED.'
   ```

   The default `gpt-6-astra` completed the write, read, and final answer.
   Repeated with `--model gpt-5.5`, including `--effort low`; both completed.
   Saved token usage retained absent costs.
3. Launched `target/debug/ox --dir "$workspace"` in an isolated tmux session
   with the same temporary provider settings. Requested a file write and read,
   entered `/compact`, then requested another read. Both final answers arrived,
   a compaction checkpoint was saved, and the earlier transcript entries
   remained. All saved model and summarizer costs were absent. Restored the
   original global settings after every live check.
