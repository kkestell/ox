# Rust Cargo

- Check `Cargo.toml` and `Cargo.lock` against real dependency use. Consider
  transitive crates, enabled features, toolchain requirements, and portability.
- Assess version and edition changes against the intended product. A local
  scratch replacement does not establish a shippable Cargo configuration.
- Prefer existing deterministic Cargo test facilities when a focused check is
  useful. Do not require a fixed Cargo command suite for every review.
