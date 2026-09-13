# One token unit

## Goal

`bytesToTokens` returns its byte count unchanged, so every estimate Ox derives
from a serialized request is a byte count wearing a token label. Those bytes are
then compared against the model's context window and published as context
occupancy.

An ordinary request is therefore reported as roughly four times its real size.
Automatic compaction runs when the conversation reaches about a fifth of the
window rather than four fifths, and the client's meter jumps when the provider
reports the turn's actual prompt tokens into the same field.

## Desired outcome

One documented token unit. Occupancy the client sees, the compaction threshold,
and the provider's reported prompt tokens are comparable, and compaction starts
near the share of the window the threshold names.

## Summary of approach

Keep counting the serialized request's bytes, which is the only measurement Ox
can make locally, and convert once in `bytesToTokens` using a documented
bytes-per-token ratio chosen to stay above what a model actually charges. Every
caller already treats that function's result as tokens, so nothing else changes.

## Related code

- `internal/agent/compact.go` - `bytesToTokens`, `promptTokens`,
  `estimateProviderRequest`, `shouldCompact`, `planRequestAdmission`.
- `internal/agent/state.go` - `occupancy` holds the provider's reported prompt
  tokens after an exchange and the estimate after a compaction.

## Current state

- Relevant existing behavior: media blocks are already charged a token
  allowance, added after the byte conversion, so they must not be converted
  again.
- Existing patterns to follow: the estimate is deliberately conservative, and
  `docs/spec.md` already says Ox does not claim exact occupancy.
- Constraints from the current implementation: under-counting sends a request
  the model refuses, so the ratio has to stay below any realistic tokenizer's
  bytes per token.

## Test plan

- **Key behaviors to verify:** an estimate is a fraction of the request's byte
  count, compaction triggers near the documented share of the window rather than
  at a quarter of it, and admission still refuses a request that genuinely
  cannot fit.
- **Test levels:** unit, in `internal/agent`.
- **Edge cases and failure modes:** a request whose size is not a multiple of
  the ratio must round up, never down.
- **What not to test:** the ratio's exact value, which is an estimate rather
  than a provider fact.

## Implementation plan

- Convert bytes to tokens with a documented ratio, rounding up.
- Adjust the compaction fixtures whose sizes assumed one byte per token.
- Add a test that an estimate is in token units and that compaction waits for
  the documented share of the window.

## Documentation updates

- `docs/spec.md` states that occupancy is an estimated token count derived from
  the serialized request.
- Todo list item "Use consistent token units for context occupancy and
  compaction (F12)".

## Impact assessment

- Code paths affected: request admission, compaction planning, usage reporting.
- Data, protocol, or schema impact: persisted compaction occupancy changes unit.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the agent, integration, and
  end-to-end suites.
- Static checks: `make check-go`.
