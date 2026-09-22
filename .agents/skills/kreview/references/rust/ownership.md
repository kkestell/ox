# Rust ownership

- Trace owned values, borrows, and lifetimes through callers. Check whether
  shared backing storage or interior mutability changes observable behavior.
- Judge clones, `Arc`, references, and `Cow` by the boundary they serve. Values
  containing synchronization state should not be duplicated without
  understanding their semantics. Do not treat a harmless clone as a defect.
- Prefer a simpler ownership model when lifetime parameters or shared state
  exist only to avoid small, ordinary allocations.
