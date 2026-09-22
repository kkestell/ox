# Rust idioms

- Check `Result` and `Option` handling for lost failures or unclear ownership
  of validation. `unwrap`, `expect`, and panics should expose programming errors
  rather than reachable bad external input.
- Check whether traits, generics, dynamic dispatch, and macros clarify real
  uses or add conversions and indirection without benefit.
- In concurrent code, check `Send` and `Sync` bounds, `Arc` and mutex use, and
  asynchronous work against the actual ownership model. A language construct
  alone is not a finding.
