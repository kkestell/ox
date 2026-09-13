---
name: kreview
description: "Review a Go code change through a general, change-directed assessment or one or more focused quality lenses. Use when the user asks for a Go code review of a diff, branch, commit, or set of files."
argument-hint: "[general|topic[,topic...]] [review scope]"
---

## Workflow

Review the requested corpus directly. Do not edit implementation files or
delegate the review. Write its report under `eng/reviews/`.

1. Resolve the review mode and scope from
   `<input_document> $ARGUMENTS </input_document>`.
   - The first argument may be `general`, or a comma-separated list of one or
     more specific topics below, such as `performance,concurrency`. Preserve the
     listed order and remove duplicates.
   - `general` is mutually exclusive with specific topics. If a topic list
     combines it with another topic, or names an unknown topic, ask one focused
     question rather than silently changing the requested review.
   - Default to `general` when no topic is given. The remaining arguments are
     the review scope.
2. Read the repository instructions, then identify the review corpus.
   - For a diff, branch, or commit, inspect its diff and changed-file list.
   - For explicit files, directories, or the whole codebase, inventory that
     scope. Do not reduce it to the current diff.
   - For a feature or milestone, map its owning contracts and repository
     completion rules to the implementation, tests, fixtures, examples, and
     documentation they require. A required artifact missing from the diff is
     still part of the review. Ask one focused question only when the review
     scope remains ambiguous.
3. Read the relevant sections in [references/topics.md](references/topics.md),
   then run the requested mode: the `General` section followed by the selected
   topic sections in general mode, or every requested topic section in
   specific-topic mode.
4. Review intentional copies, panics, `unsafe`, allocation, and dependency
   choices in context rather than treating them as automatic defects. This
   repository panics deliberately on violated internal invariants.
5. Write `eng/reviews/YYYY-MM-DD-NNN-slug.md`, using the next sequence for the
   day. Name the scope, mode, and reviewed topics, then record findings ordered
   by severity with path, line, consequence, and suggested fix. In general mode,
   record the selected topics and why they fit the change. Record unresolved
   suspicions and checks run. When there are no findings, say so plainly and
   note any material validation gap.
6. Report the review concisely and link its document. Do not implement findings
   in the review turn. A later implementation commit includes this report, and
   includes its plan too when the finding becomes a planned task.

### General mode

Choose a thorough, change-directed set of topics. Read the `General` section,
inventory the corpus and its contracts, then select the topic sections that the
change materially touches or risks. Read and review every selected section in
full; do not read or report on irrelevant topics merely to make the review look
comprehensive.

- Let the actual change determine the selection. For example, skip `unsafe` when
  there is no `unsafe`, `reflect`, or cgo boundary, `api-design` and
  `documentation` when no exported or shared contract changes, and
  `dependencies` when no module configuration changes. Apply the same judgment
  to concurrency, security, performance, resources, and every other topic.
- State the selected topics and concise reasons before reporting findings. Do
  not claim that skipped topics were reviewed.

- Breadth does not reduce depth. Inventory every changed production file and its
  relevant tests, then examine the selected topics' code, callers, callees, and
  invariants.
- Apply every relevant checklist item. Trace representative success, boundary,
  and failure paths, and verify suspicions with focused searches, tests, builds,
  or small reproductions.
- A selected topic with nothing to report is a normal outcome; say so in one
  line. Close with a one-line verdict for each selected topic and recommend any
  follow-up that needs evidence outside the requested corpus.

### Specific-topic mode

An exhaustive review of each requested lens. Read each selected section in
`references/topics.md` and apply every check it lists.

- For a change review, examine every changed line each topic touches, plus the
  code it calls, the code that calls it, and the invariants it depends on. For a
  whole-codebase or directory review, read every production file in the corpus
  and the relevant tests before following the topic's call paths. Do not present
  a pattern search or a pass over central types as an exhaustive whole-codebase
  review.
- Trace representative success, boundary, and failure paths concretely, naming
  the values that reach each branch.
- Verify each suspicion with a focused search, a test, or a build rather than
  reasoning alone. Prefer `go test -race -count=1`, `go vet`, `staticcheck`, a
  targeted `grep`, or a small reproduction over speculation.
- Check the change against the contract that owns the behavior, and against the
  repository's own rules for that topic.
- Report confirmed findings, then suspicions you could not settle and what would
  settle them, then the checks you ran and what they showed.
- Depth is the point. Stay within the requested topics and do not drift into
  others.

## Topics

- `general` — a thorough review that selects the topics relevant to the change.
- `resources` — value and pointer semantics, aliasing, copies, and cleanup.
- `error-handling` — `error` values, wrapping, context, and panic policy.
- `api-design` — naming, exported surface, signatures, and interfaces.
- `performance` — algorithms, allocations, copies, and hot loops.
- `testing` — edge cases, failure paths, interactions, and assertions.
- `readability` — local reasoning, function length, nesting, and naming.
- `concurrency` — races, locks, channels, `context`, and goroutine lifetime.
- `security` — trust boundaries, validation, authorization, and secrets.
- `correctness` — invariants, boundaries, arithmetic, and state transitions.
- `unsafe` — `unsafe`, `reflect`, cgo, layout, aliasing, and soundness.
- `architecture` — boundaries, coupling, duplication, and needless machinery.
- `dependencies` — modules, portability, and supply-chain exposure.
- `documentation` — exported contracts and non-obvious invariants.
