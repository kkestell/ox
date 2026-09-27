# Image input abstraction review

## Scope and coverage

Reviewed the working image-input diff in `src/acp.rs`, `src/acp/convert.rs`,
`src/acp/prompt.rs`, `src/sessions.rs`, `src/openrouter.rs`, and
`src/compaction.rs`, including callers and the changed tests. Read the image
input plan and repository architecture, style, glossary, and testing guidance.

Used general review mode with architecture, API design, readability, Rust
idioms, and correctness lenses, concentrating on small abstractions that
remove duplicated decisions. This was not a comprehensive security,
performance, or live-provider compatibility review. No implementation or tests
were changed.

## Findings

### Medium

#### Correctness

- **Preserve text separators when replaying a multipart message**
  (`src/acp/convert.rs:92`): Replay now emits each saved text part unchanged as
  a separate user-message chunk. Two input blocks containing `first` and
  `second` therefore replay as chunks containing `first` and `second`, with
  no newline between them. A client appending these chunks displays
  `firstsecond`. Previously, input conversion joined the blocks with a
  newline, and replay sent that joined string. Text-only model requests still
  use `UserMessage::text()`, which joins with newlines
  (`src/sessions.rs:83`, `src/openrouter.rs:439`), so replay and model input
  now disagree. Resource links have the same problem when following text.
  The conversion test checks the joined text and the replayed image, but does
  not check replayed text separators. A small message-construction operation
  that joins adjacent text parts with the existing newline rule would give
  replay and model encoding the same content while preserving image order.
  Extend the existing conversion/replay test to compare reconstructed text
  for multiple text blocks and text followed by a resource link.

### Low

#### Architecture

- **Let the OpenRouter encoder substitute images before building data URLs**
  (`src/compaction.rs:67`, `src/compaction.rs:263`): Both whole-request
  estimation and compaction cut ranking first build normal OpenRouter JSON,
  including full base64 data URLs, then walk its `content`, `type`,
  `image_url`, and `url` fields to replace images with placeholders. The
  actual image encoding lives in `src/openrouter.rs:431`. This gives
  compaction a second owner of the provider's image representation and
  allocates data URLs only to discard them. The two size paths also repeat
  the serialized-byte count plus image-allowance calculation. These are real
  shared uses: `request_estimate` and `projected_estimate` use one path;
  `ranked_cuts` uses both.

  Add a small internal choice to the OpenRouter encoder for full image data
  versus a placeholder. Have its estimate-facing operations return encoded
  byte counts and image counts for a body or entry, using the same message
  encoding underneath. Compaction can retain its token allowance, budget,
  and cut-selection policy without parsing provider JSON. This would remove
  `strip_images`, the post-encoding mutation, and the duplicated accounting
  formula. Keep it concrete; this does not need a provider trait or a general
  message framework. Preserve the existing estimate-independence assertion
  and check that entry sizes used for ranking agree with the corresponding
  whole-request estimate.

## Checks run

- Inspected the complete Rust diff and traced conversion, dispatch,
  persistence, replay, model encoding, admission, and compaction sizing.
- Confirmed the replay separator mismatch by tracing the emitted text payloads
  against `UserMessage::text()` and the previous conversion implementation.
  No ACP client rendering test was run.
- Ran the existing conversion/replay, OpenRouter request-shape, and manual
  compaction tests with `cargo test --all-features --
  prompt_to_user_message_keeps_links_and_images_and_rejects_invalid_content
  requests_group_messages_and_send_continuation_metadata_once
  manual_compaction_uses_a_summary_and_keeps_the_complete_transcript`:
  all three passed. Their current assertions do not cover the text separator
  regression.
- `git diff --check` passed before adding this report.
- Full test, formatting, build, Clippy, and example-hook suites were not run;
  this review changes only a Markdown report. Test-function and test-code
  deltas from this review are both zero.

## Verdict

Fix the replay regression. Two small abstractions are justified: one place to
construct message parts with consistent text separators, and an image encoding
choice shared by requests and estimates. The existing `UserMessage` and
`ImageAttachment` types already provide useful common data; further wrappers
around each text/image match would add little value.
