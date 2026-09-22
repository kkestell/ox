---
name: kreview
description: "Review code in a diff, branch, commit, or specified files and write findings in `eng/reviews/`. Supports general and language-specific focused code reviews. Do not use for plan critiques or standalone documentation reviews."
argument-hint: "[general|lens[,lens...]] [review scope]"
---

## Objective

Review the code the user names. Write findings without changing code unless
the user also asks for fixes. Follow review and delegation rules in `AGENTS.md`.

This skill and `eng/reviews/` are for code reviews. Plan critiques and standalone
documentation reviews are outside its scope. Read plans and documentation as
context for code under review; the documentation lens checks guidance for that
code.

## Choose what to review

Read `<input_document> $ARGUMENTS </input_document>`. Use `general` unless the
user names one or more lenses from the list below. Ask for clarification only
when the answer would change what you review.

For a diff, branch, or commit, inspect its changes and the code that calls or
depends on them. For named files, directories, or the whole codebase, review
that full scope. For a feature review, check other code the feature needs,
even when it is outside the diff.

For a general review, choose lenses that fit the code and its risks, including
language-specific lenses when applicable. For a focused review, stay within the
requested lenses and examine the relevant code closely. Read only the matching
lens files linked below. Each lens is either general or specific to one
language.

## Review

Trace behavior through callers, state changes, resource lifetimes, and tests.
Judge implementation choices by their purpose and consequence. Do not treat a
language feature or coding pattern as a defect on its own.

Read the requirements that define the behavior. If the user has approved a new
design, review against that design. Flag complex or expensive code that adds no
required behavior. Do not recommend extra code for a hypothetical edge case
alone.

Check suspected bugs by tracing the code, reproducing the behavior, or running
a focused check. Follow `AGENTS.md` when choosing validation. A lens does not
require a fixed set of commands, a new test for each error path, or a
broader review than the requested scope. Report only supported findings, and
say when you reviewed only part of the code.

## Report

Read `eng/reviews/TEMPLATE.md` and use it to write the review in
`eng/reviews/YYYY-MM-DD-NNN-slug.md`, using the next sequence for the day.
State the scope, selected lenses, and significant gaps in coverage. Group
confirmed findings by severity (high, medium, low), then by lens within each
severity.
For each finding, give the source location, what can happen, the evidence, and
a suggested fix. List unconfirmed issues separately and say what would confirm
them. Record the checks you ran. If there are no findings, say so. End with a
short verdict.

## General lenses

`general` is the review mode that chooses applicable lenses; it is not a lens.

- [`resources`](references/general/resources.md) — resource ownership, lifetime,
  and cleanup.
- [`error-handling`](references/general/error-handling.md) — failures, useful
  errors, cancellation, and partial effects.
- [`api-design`](references/general/api-design.md) — caller contracts and shared
  interfaces.
- [`naming`](references/general/naming.md) — clear and consistent vocabulary.
- [`performance`](references/general/performance.md) — material costs for
  realistic inputs.
- [`testing`](references/general/testing.md) — behavior coverage and reliable
  assertions.
- [`readability`](references/general/readability.md) — clear control flow and
  local reasoning.
- [`concurrency`](references/general/concurrency.md) — shared state, task
  lifetime, and cancellation.
- [`security`](references/general/security.md) — trust boundaries, permissions,
  and secrets.
- [`correctness`](references/general/correctness.md) — required behavior and
  reachable state changes.
- [`architecture`](references/general/architecture.md) — responsibilities,
  dependencies, and needless complexity.
- [`dependencies`](references/general/dependencies.md) — library choices and
  integration costs.
- [`documentation`](references/general/documentation.md) — caller guidance and
  design documents associated with the code under review.

## Rust lenses

Use these only for Rust code. Future language lenses belong in their own
language directory, with no language-specific rules in general lenses.

- [`rust-ownership`](references/rust/ownership.md) — borrowing, clones,
  allocation, and shared ownership.
- [`rust-idioms`](references/rust/idioms.md) — `Result`/`Option`, traits,
  generics, macros, and Rust concurrency types.
- [`rust-cargo`](references/rust/cargo.md) — Cargo manifests, lockfiles,
  features, and toolchain costs.
