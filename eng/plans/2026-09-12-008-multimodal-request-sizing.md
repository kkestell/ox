# Multimodal request sizing

## Goal

Ox advertises image and audio prompt support, the ACP boundary accepts both, and
`promptMessage` converts them into provider content. Then
`estimateProviderRequest` refuses any request that contains either, so every
advertised multimodal prompt fails. `docs/spec.md` describes multimodal input as
supported.

## Desired outcome

A prompt carrying an image or audio block is admitted and sent, sized by a
conservative estimate rather than refused, and an end-to-end test proves it over
ACP.

## Summary of approach

Size media by a fixed per-block allowance instead of by its serialized bytes. A
model prices media by its own tiling and sampling rules, which the request's
byte count does not predict: the same picture costs about the same whether it
arrives as a short link or as hundreds of kilobytes of base64. Counting those
bytes as tokens would refuse a normal image outright, and ignoring them would
under-count. One shared sizing function replaces the three estimators so the
admission planner and the compaction planner agree.

## Related code

- `internal/agent/compact.go` - `estimateProviderRequest`,
  `estimateRequestTokens`, `estimateMessages`, `bytesToTokens`.
- `internal/agent/loop.go` - `promptMessage` builds the media blocks.
- `internal/agent/agent.go` - advertises image and audio prompt capabilities.

## Current state

- Relevant existing behavior: `bytesToTokens` treats one serialized byte as one
  token, which is a genuine upper bound for text.
- Existing patterns to follow: `renderCompactionTranscript` already skips
  non-text blocks, so the summarizer needs no change.
- Constraints from the current implementation: the admission planner must not
  under-count, or a request can exceed the model's window at the provider.

## Test plan

- **Key behaviors to verify:** an image and an audio request are admitted, their
  estimate is above the payload-free text estimate and far below the base64 byte
  count, and a prompt with an image completes a turn over ACP.
- **Test levels:** unit in `internal/agent`, end-to-end in `internal/e2e`.
- **Edge cases and failure modes:** a linked image with a short URL must still
  be charged its allowance.
- **What not to test:** the exact allowance constants, which are deliberate
  estimates rather than provider facts.

## Implementation plan

- Add the media allowances and one `promptTokens` function that charges them.
- Route `estimateProviderRequest`, `estimateRequestTokens`, and
  `estimateMessages` through it, and delete the rejection.
- Replace the rejection test with an admission test.
- Add the end-to-end image prompt test.

## Documentation updates

- Todo list item "Make multimodal admission match advertised ACP capabilities
  (F07)".

## Impact assessment

- Code paths affected: provider request admission and compaction planning.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the agent, integration, and
  end-to-end suites.
- Static checks: `make check-go`.
