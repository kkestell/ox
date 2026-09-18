---
name: ksimplify
description: Review Ox Rust code for opportunities to remove unnecessary code and make the remaining design easier to reason about. Use for a simplification review of the codebase or a specified scope; produces findings rather than implementation changes.
---

## Objective

Find changes that leave less Rust code and fewer concepts to understand while
preserving required behavior. Excessive defensiveness, reinvented facilities,
needless ownership or async complexity, and redundancy are examples, not an
exhaustive checklist. Let the code reveal opportunities outside those
categories.

## Review

Resolve the scope from `<input_document> $ARGUMENTS </input_document>`,
defaulting to the whole codebase. Read repository instructions, module
documentation, and owning contracts as needed. Inventory the scope and explore
it broadly before choosing paths for deeper investigation. Do not let prior
review reports become the search checklist.

Trace candidate findings through callers, ownership, borrowing, lifetimes,
invariants, and relevant tests. Establish why the complexity exists and whether
its guarantee serves required user behavior. Current contracts and tests can
encode incidental implementation choices; surface those choices for
reconsideration rather than using them to dismiss a simplification. Preserve
actual authority, confinement, and cancellation boundaries.

In Rust, consider whether unnecessary clones, allocations, `Arc`/mutex layers,
trait indirection, generic bounds, macros, `unsafe`, or async wrappers can be
removed without weakening the contract. Prefer deleting concepts,
representations, and branches over shortening syntax or introducing
abstractions. Judge reuse by the total complexity it removes, including crate
and integration costs. Separate behavior-preserving simplification from
proposals that require a product or architectural decision. Do not manufacture
findings to meet a quota or present unverified line savings as measured
reductions.

Use focused searches or checks to settle concrete uncertainties. Review only: do
not implement findings or modify the roadmap unless the user also requests that
work.

## Report

Prioritize findings by likely reduction in code and reasoning burden. For each
finding, give precise source locations, evidence that the complexity is
unnecessary, the simpler direction, and the behavior or risk that must be
preserved. Group observations that share one underlying change.

State the scope actually examined, material coverage gaps, checks run, and
unresolved uncertainties. If there are no actionable findings, say so plainly.
Finish with a concise summary.
