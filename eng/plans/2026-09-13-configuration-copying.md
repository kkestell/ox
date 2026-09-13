# Configuration copying

## Goal

Two copying problems sit at opposite ends of the same path.

The model catalog is deep-cloned into every session at activation, and cloned
again every time the configuration options are built, only so the option list
can be sorted. The provider already hands out a private copy, so both clones
duplicate work no one needs.

Settings merging claims to mutate neither input, and it does not — but the value
it returns shares pointers and slices with them. When one side is nil the merge
returns the other side's nested object outright, and even a full merge picks the
inputs' own pointers field by field. The contract holds only because no caller
mutates what it receives.

## Desired outcome

A session reads one frozen catalog rather than its own copy of one. A merged
configuration shares nothing with the settings it was merged from, so the
non-mutation contract is a property of the code rather than of its callers.

## Summary of approach

Treat the catalog a session receives as frozen: it is already private, and
nothing mutates it. Sort the rendered option list instead of the models, which
removes the reason the second clone existed.

Make every merge result independent by copying at the two places a value crosses
from an input to the result: the scalar pointer chosen per field, and the slice
chosen per field. The nested objects then follow, because they are built from
those.

## Related code

- `internal/agent/agent.go` - the two activation sites that clone the catalog.
- `internal/agent/config_options.go` - `modelOptions`, `cloneModels`.
- `internal/settings/merge.go` - `Merge` and its helpers.

## Current state

- Relevant existing behavior: `openrouter.Catalog.Models` already clones each
  model, so a session's slice is not shared with the memoized catalog.
- Existing patterns to follow: the turn configuration is already shared rather
  than copied, with the invariant named where it is read.
- Constraints from the current implementation: slice merging must keep the
  nil-versus-empty distinction, because an empty workspace list clears a global
  one.

## Test plan

- **Key behaviors to verify:** mutating a merged result's nested reasoning,
  provider, and max-price values leaves both inputs unchanged, including when
  one side is nil; an empty workspace slice still clears a global list.
- **Test levels:** unit, in `internal/settings`.
- **Edge cases and failure modes:** both sides nil.
- **What not to test:** which side wins, already covered.

## Implementation plan

- Stop cloning the catalog at activation and in the option builder.
- Copy the chosen pointer and slice in the merge helpers.
- Add the merge mutation test.

## Documentation updates

- Todo list item "Reduce configuration copying and prevent settings-result
  aliasing (F33, F40)".

## Impact assessment

- Code paths affected: session activation, configuration options, settings
  merging.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the merge mutation test, then the whole suite.
- Static checks: `make check-go`.
