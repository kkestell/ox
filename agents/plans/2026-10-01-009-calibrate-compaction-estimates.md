# Calibrate compaction estimates

## Goal

OX-0030: Ox estimates request tokens as bytes divided by three. Real requests
run closer to 4.8 bytes per token, so the estimate is too high and automatic
compaction fires at about half the usable context. The same estimate sets the
admission limit and the recent allowance. When this is done, estimates use the
bytes per token measured from the session's latest reported usage, so compaction
fires near 80 percent of the real admission limit.

This plan follows `2026-10-01-008-keep-user-messages-in-compacted-requests.md`
and refers to the code as that plan leaves it.

## Related code

- `crates/ox-server/src/compaction.rs` — `to_tokens`, the fixed
  three-bytes-per-token conversion.
- `crates/ox-server/src/compaction.rs` — `request_tokens`, the estimate behind
  the automatic threshold, the admission check, and the context usage shown to
  the client.
- `crates/ox-server/src/compaction.rs` — `projected_tokens`, `input_fits`, and
  `cut`, the other estimates measured against the admission limit and the recent
  allowance.
- `crates/ox-server/src/compaction.rs` — `next_piece`, which sizes summarizer
  requests with `to_tokens`.
- `crates/ox-server/src/sessions.rs:427` — `ModelUsage::input_tokens`, the
  reported input tokens of the request that produced an assistant message.

## Decisions

- **Bytes per token comes from the latest assistant batch.** Ox rebuilds the
  body of the request that produced that batch: the projection of the transcript
  before it, with the current model request parameters. It then divides that
  body's bytes by the batch's reported input tokens. Ox falls back to three
  bytes per token when there is no assistant batch, when the latest batch has no
  usage, or when its turn ran on a different model. A different model may
  tokenize differently. The code assumes reported input tokens are greater than
  zero, because every request carries a system prompt.
- **The estimate stays conservative for summarizer requests.** `next_piece` and
  `SUMMARY_ALLOWANCE_BYTES` keep three bytes per token. Summarizer requests do
  not decide when compaction fires, and they have no reported usage to measure.
- **Image allowances stay in bytes.** An image still adds
  `IMAGE_ESTIMATE_TOKENS * 3` bytes, which then divide by bytes per token like
  every other byte. The rebuilt request counts images the same way, so the
  measurement includes them.
- **`budget` and `RECENT_ALLOWANCE_PERCENT` are unchanged.** The admission
  limit, the 80 percent automatic threshold, and the recent allowance now apply
  to an accurate estimate. When an estimate falls short, the existing path that
  compacts and retries after a provider's context overflow error covers it.

## Naming

- `bytes per token` — the request body bytes for each reported input token,
  measured from the latest assistant batch or defaulting to three. Use it in
  `bytes_per_token` and `DEFAULT_BYTES_PER_TOKEN`.

## Test plan

- `compaction.rs`: a new table-driven test,
  `request_estimates_use_the_bytes_per_token_of_the_latest_reported_usage`.
  Build a turn start and an assistant batch, then compare `request_tokens` with
  the expected estimate in each case:
  - no usage: bytes divided by three
  - usage from the same model: bytes divided by the measured bytes per token
  - usage from a turn on another model: bytes divided by three
- `the_cut_keeps_the_newest_entries_within_the_recent_allowance` must still
  pass.
- The existing compaction and prompt run tests use fixture replies without
  usage, so they keep the default bytes per token and stay unchanged.

## Implementation plan

1. In `compaction.rs`, add `DEFAULT_BYTES_PER_TOKEN` and
   `fn bytes_per_token(parameters, transcript) -> f64` following the first
   decision. Get the batch's turn model from the turn start before it, and
   compare it with `parameters.model.qualified_id()`.
2. Replace `to_tokens` in `request_tokens`, `projected_tokens`, `input_fits`,
   and `cut` with a conversion that divides by `bytes_per_token` for the
   transcript being measured and rounds up. Keep the fixed conversion for
   `next_piece` only, named for summarizer requests.
3. Add the test above.
