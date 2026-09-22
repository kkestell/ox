# Correctness

- Check required behavior and explicitly agreed changes against the owning
  contract. Separate a regression from an intentional redesign.
- Trace reachable state transitions, ordering, encoding, and boundary values
  that could change the result. Check exceptional values when reachable, rather
  than adding checks around every operation.
- Judge partial effects and recovery by the product contract. Identify a
  simpler state or ownership model when several fields must stay synchronized.
