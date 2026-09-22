# Error handling

- Validate external input at the boundary that owns it. Failures should reach
  callers with enough context to act; internal invariant failures should remain
  visible.
- Check discarded, flattened, or overly broad errors for a real lost failure
  or lost context. Error classifications need callers that use the distinction.
- Trace cancellation and partial failure against the operation's contract.
  External effects need not roll back unless the product requires it.
