# YYYY-MM-DD-NN. Plan Title

Use this template as a scaffold, not a cage. Drop sections that do not apply,
expand the ones that matter, and add sections when the work needs more
structure. A plan is a settled work order, not a record of the conversation that
produced it: resolve material decisions before writing it.

## Research

- `docs/agents/research/topic.md` - Prior art this decision used. Cite without
  restating it. Omit this section when no research applied.

## Goal

State the problem this plan solves.

## Desired outcome

State what this plan delivers and how a user will know it worked.

## Summary of approach

Give a concise overview of the implementation's major components and how they
interact. State the chosen approach without alternatives, rejected options, or
open questions.

## Related code

- `path/to/file` - Why this file or pattern matters.

## Current state

- Relevant existing behavior:
- Existing patterns to follow:
- Constraints from the current implementation:

## Structural considerations

Explain how the change fits Ox's architecture. Address the applicable PHAME
lenses and how the plan preserves them.

- **Hierarchy:** Does the design preserve ownership and dependency direction?
- **Abstraction:** Does each responsibility live at the right level?
- **Modularization:** Does the change keep components focused without creating a
  catch-all or nano-module?
- **Encapsulation:** Does it preserve boundaries and avoid exposing internals?
- **Testability:** Can behavior be verified at stable public interfaces without
  complex or implementation-coupled setup?

## Refactoring

List preparatory refactors and what each achieves structurally. Sequence them
before feature work. Omit when none are needed.

## Test plan

Define tests before implementation tasks. Focus on edge cases, error paths, and
boundaries at stable public interfaces.

- **Key behaviors to verify:**
- **Test levels:**
- **Edge cases and failure modes:**
- **What not to test:**

## Implementation plan

- Concrete task in execution order.
- Concrete task in execution order.

## Documentation updates

- Current-state contract to update when the implementation lands. Omit when no
  owned fact changes.
- Roadmap item completed or unblocked. Omit when none applies.

## Impact assessment

- Code paths affected:
- Data, protocol, or schema impact:
- Dependency or API impact:

## Validation

- Tests to write and run:
- Static checks:
- Manual verification:
