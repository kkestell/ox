# Code style

> Make things as simple as possible, but not simpler.

Optimize for code that is cheap to change. Keep behavior correct. Keep the
implementation thin, boring, and easy to replace. Minimize committed surface.
When in doubt, do less.

Start with the simplest thing that compiles and shows whether the idea works. A
placeholder for an unneeded case is better than hardening around a design that
is still moving.

## Correctness and robustness

- Use safe Rust.
- Treat bad external input as normal. Return a clear error and keep running when
  recovery is possible.
- Treat broken internal invariants as bugs. Fail loudly instead of continuing
  with bad state.
- Do not add fallbacks or defensive machinery for failures not yet observed.

## Rust style

- Prefer ordinary, idiomatic Rust.
- Use enums and exhaustive matching for closed data. Let the compiler find
  missing cases.
- Prefer owned data in structs. Clone when it keeps the design clear. Avoid
  lifetime parameters and optimization until measurement requires it.
- Use concrete types until multiple real uses earn an abstraction. No
  speculative generality for a single caller.
- Keep dependencies few. Do not add a crate for one use.

## Structure and configuration

- Keep one module focused on one concern, with shallow trees and clear
  boundaries. Cohesion matters more than short files.
- Hardcode local tuning values near their use until user-facing configuration is
  needed.
- Prefer direct functions and data flow. Split a module when the boundary
  clarifies responsibility, not because the file is long.

## Errors, tests, and comments

- Use existing project error conventions. Add a custom error type only when
  callers recover differently.
- Do not use quiet fallbacks that hide broken invariants or turn bad input into
  bad state.
- Test behavior at the boundary that matters. Prefer focused unit tests for
  tricky, stable logic and end-to-end tests for observable behavior. Avoid tests
  that freeze internals while the design is changing.
- Cover every fixed bug when practical, preferably by strengthening an existing
  test. Add a separate regression test only when no existing test fits.
- Comments explain why, surprising behavior, or an external rule. Do not restate
  the code.

## When to harden

Harden only after the design works, and only against failures actually observed.
Then add the abstractions, recovery paths, and tests the stable behavior has
earned.
