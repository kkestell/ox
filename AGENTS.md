# Experimental Claude Code multiplexer

This is an experiment. Optimize for learning and code that is cheap to change,
not production reliability.

## Status

Not in production use: just testing using `make dev`. There is no data or state
that cannot be deleted if necessary.

## Implementation

- Keep the code direct, readable, and simple. Implement the requested behavior
  completely, using the smallest coherent design.
- Handle normal use and concrete failures. Do not build machinery for unusual
  edge cases or hypothetical failures. Fail clearly when something unsupported
  happens.
- Avoid speculative abstractions, compatibility layers, automatic recovery,
  and durability guarantees. Add them only when a current requirement earns
  their cost.
- Keep state and lifecycle ownership easy to follow. Reconsider the design when
  a change needs coordination machinery to support earlier choices.
- Treat research and prior implementations as evidence, not requirements.
  Keep unresolved protocol choices open until they need a decision.

## Verification

- Keep unit tests minimal. Use them for consequential logic that benefits from
  an isolated check, not to mirror the implementation.
- Prefer small live tests against real Claude Code processes, using Haiku only.
  Check observable protocol behavior rather than exact model wording.
- Keep test setup simple; avoid elaborate mocks and fake runtimes.
- Report what was verified and any live checks that could not be run.
