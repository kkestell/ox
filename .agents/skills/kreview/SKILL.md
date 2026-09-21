---
name: kreview
description: "Review a Rust code change through a general, change-directed assessment or one or more focused quality lenses. Use when the user asks for a Rust code review of a diff, branch, commit, or set of files."
argument-hint: "[general|topic[,topic...]] [review scope]"
---

## Objective

Review the requested Rust change or corpus. Produce findings rather than editing
implementation, unless the user also requests fixes. Follow repository guidance
for delegation and completion reviews.

## Scope and mode

Resolve `<input_document> $ARGUMENTS </input_document>`. Default to `general`,
or use the requested comma-separated topic list below. Ask only when an unclear
scope or topic would materially change the review.

For a diff, branch, or commit, inspect its changes and relevant call paths. For
explicit files, directories, or the whole codebase, inventory that corpus rather
than substituting the current diff. For a feature review, include required
integration even when absent from the diff.

In general mode, select topics that the actual behavior and risks make relevant.
In focused mode, stay within the requested topics and examine the corpus deeply
enough to support the requested coverage. Read their sections in
[references/topics.md](references/topics.md).

## Review

Trace concrete behavior through callers, ownership, borrowing, lifetimes, and
tests. Judge clones, allocations, `unwrap`/`expect`, traits, macros, async
machinery, and `unsafe` by their purpose and consequence rather than treating
them as automatic defects.

Read the owning contracts, allowing the user's agreed redesign to change them.
Challenge costly implementation guarantees when they do not serve required user
behavior. A hypothetical edge case alone does not justify more machinery.

Verify suspected defects with the smallest useful source trace, reproduction, or
check. Choose validation according to `AGENTS.md`; topic selection does not
require a fixed Cargo command suite, new tests for every failure path, or a
hardening pass. Do not manufacture findings to fill each topic or call a partial
scan exhaustive.

## Report

State the scope, selected topics, and material coverage gaps. Order confirmed
findings by severity, with precise source, consequence, evidence, and a simpler
remedy. Separate unresolved suspicions and explain what would settle them.
Record checks actually run. No findings is a valid result. Give a concise
verdict.

## Topics

- `general` — select topics relevant to the requested change.
- `resources` — ownership, borrowing, lifetimes, clones, allocations, and cleanup.
- `error-handling` — `Result`/`Option`, useful errors, cancellation, and panic behavior.
- `api-design` — caller contracts, traits, naming, and shared surface.
- `naming` — invented terms, jargon, vague names, and inconsistent vocabulary.
- `performance` — repeated work, allocations, and costs at actual input sizes.
- `testing` — useful behavior coverage and reliable assertions.
- `readability` — local reasoning, control flow, and naming.
- `concurrency` — async tasks, threads, synchronization, cancellation, and lifetime.
- `security` — trust boundaries, authority, confinement, and secrets.
- `correctness` — required behavior and reachable state transitions.
- `unsafe` — unsafe blocks, raw pointers, FFI, and soundness assumptions.
- `architecture` — responsibility, coupling, and redundant machinery.
- `dependencies` — replaced machinery, Cargo configuration, and integration costs.
- `documentation` — useful caller guidance and accurate design documents.
