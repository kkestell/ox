# Concurrency

- Establish the owner and synchronization of shared mutable state. Trace
  actual interleavings that could race, deadlock, block progress, or apply a
  stale decision.
- Follow threads, asynchronous tasks, channels, callbacks, and subprocesses
  through cancellation and shutdown. Their lifetime should agree with the
  work that owns them.
- Check that cancellation reaches relevant waits and that live policy changes
  have the behavior required by the product contract. Use focused interleaving
  checks when they would settle a concrete concern.
