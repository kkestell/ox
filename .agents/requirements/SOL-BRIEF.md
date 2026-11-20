# Brief: check Ox's functional requirements for completeness and accuracy

## Background

Ox (repository root /Users/kyle/projects/ox) runs Claude Code sessions. `oxd` is
a daemon that runs Claude processes and stores sessions; `ox` serves a web page
that talks to `oxd`. The file
`/Users/kyle/projects/ox/.agents/requirements/requirements-draft.md` is a draft
of Ox's functional requirements: 52 numbered statements, then a "Not met today"
list of requirements the code currently breaks. It will replace
`docs/guarantees.md`.

## What belongs in the file (the criteria)

A requirement belongs only if all of these hold:

- **User-observable.** The person using Ox notices it through the web page, or
  while running `oxd` and `ox` on their machine (startup, shutdown, their files,
  their Claude processes, their billing).
- **A user would call its breaking a bug.** Not cosmetic detail (pixel
  thresholds, character counts, icon choices, exact wording of labels).
- **Not the protocol contract.** Error codes, message fields, ordering of
  notifications, ID formats, and anything only a program speaking `oxd`'s
  socket protocol could see belong in `docs/protocol.md`, not here.
- **Not implementation.** Goroutines, queues, timers, signals, flags passed to
  Claude, file and line locations stay out.
- One or two plain sentences, present tense, with no code or line references.

## Your task

1. **Accuracy.** For each of the 52 requirements, check against the source
   that Ox does what it says. A requirement the code breaks is accurate if the
   "Not met today" list names it with the right cause; otherwise it is a
   finding. Also check that each "Not met today" entry is a real failure.
2. **Completeness.** Find user-observable behavior that meets the criteria and
   is missing from the list. Only behavior the code implements or the docs
   promise; do not invent requirements Ox should have.
3. **Criteria.** Flag any requirement that fails the criteria above, and any
   that states two separate requirements that should be split, or duplicates
   another.

## Sources

- Code: `cmd/ox/main.go`, `cmd/ox/web/src/*.ts(x)`, `cmd/oxd/main.go`,
  every `.go` file under `internal/`, and `internal/config/default.json`.
- Tests, to see intended behavior: `cmd/ox/web/e2e/*.spec.ts` and the Go
  `_test.go` files.
- Docs: `docs/guarantees.md`, `docs/protocol.md`, `docs/architecture.md`,
  `docs/claude-code.md`, `docs/known-issues.md`.
- Do not read any other file under `.agents/`.

## Boundaries

- Read-only. The only file you create is your output file. Do not edit the
  draft, source, docs, tests, git state, or configuration.
- Read and search only (cat, grep, git log/show). Do not run make, tests,
  servers, or Claude.
- Do not start other agents or sessions.
- Do not rewrite the whole draft or propose redesigns of Ox. Report findings
  only; each finding carries the corrected or new sentence.
- Report only what you confirmed in the source, with file:line evidence. Leave
  out anything you could not confirm.
- Stop once every requirement has been checked and every source file listed
  above has been read.

## Output

Write `/Users/kyle/projects/ox/.agents/requirements/sol-review.md` with exactly
these sections:

### Inaccurate

One entry per finding: requirement number, what the draft says, what the code
does, file:line evidence, the corrected sentence (or "move to Not met today"
with the cause).

### Missing

One entry per finding: the proposed requirement sentence, file:line evidence,
and one line on why a user would call its breaking a bug.

### Fails the criteria

One entry per finding: requirement number, which criterion it fails, and
whether to drop it, rewrite it (give the sentence), split it, or merge it with
another (name which).

### Checked and accurate

The numbers of the requirements you confirmed, as one comma-separated line.

Then reply with three lines: the output path, and the count of findings in
each of the first three sections.
