# Performance

- Establish actual input size and call frequency before judging cost.
- Investigate repeated traversal, copying, allocation, I/O, serialization, or
  retained output when it can materially affect the operation. Prefer
  eliminating work over clever local tuning.
- Support performance claims with measurements when needed, and label
  unmeasured concerns. Avoid complexity for an irrelevant micro-optimization.
