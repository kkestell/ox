# Brief: extract Ox's user-observable behavior

## Objective

Produce a complete list of the user-observable behavior of one section of the
Ox codebase (Ox is a Claude Code multiplexer in /Users/kyle/projects/ox: `oxd`
is a daemon that runs Claude Code sessions, and `ox` serves a web UI that
reaches it). The list becomes the raw material for Ox's functional
requirements document. Another agent extracts the same section independently,
and the lists will be merged and their disagreements checked against the
source, so precision and evidence matter more than polish.

## What counts as a behavior

A behavior is something a person using Ox can observe: what they see, what
they can do, what happens to their input, their Claude conversations, their
files and processes, and what happens on failure, reconnect, reload, restart,
or a second page acting on the same session. Where only a client of `oxd`'s
socket protocol can observe it (no web UI surface), still list it and mark it
`protocol`.

Leave out internal structure that has no observable effect (goroutines,
locks, type names, package layout) except where it produces an observable
effect, in which case state the effect.

Write each behavior as one present-tense sentence about what the user
observes, e.g. "Text typed in the composer while a send is pending stays in
the composer after the send succeeds." Not "the control keeps a draft
signal". Include concrete limits (timeouts, sizes, retries) when the code has
them.

## Inputs

- Your section's source and tests (listed below). The code is the authority.
- `docs/protocol.md`, `docs/architecture.md`, `docs/claude-code.md` for
  context. Where a doc and the code disagree, list it under Doc conflicts.
- Do NOT read `docs/guarantees.md`, `docs/known-issues.md`, `docs/todo.md`,
  or anything under `.agents/` other than this brief. The list must come from
  the code, not from the existing summary.

## Boundaries

- Read-only. The only file you create or edit is your output file. Do not
  modify source, docs, tests, git state, or config.
- You may run read-only commands (grep, git log/blame/show). Do not run
  `make`, servers, Claude, or tests.
- Do not start other agents or sessions.
- Stop when every file in your section has been read in full and every
  behavior you found is in the output. Then reply with a 3-line summary: the
  output path, the number of behaviors, the number of suspected defects.

## Output

Write Markdown to the output path given in your assignment, with exactly
these sections:

### Behaviors

A table with columns: `ID` (B1, B2, ...), `Behavior` (one sentence),
`Surface` (`ui` or `protocol`), `Code` (file:line where it is implemented),
`Test` (test file:line that exercises it, or `untested`).

Every row needs a real file:line you have read. No row without evidence.

### Suspected defects

Behavior the code exhibits that a user would plausibly not want, or that
contradicts another behavior or a doc. One entry each: what happens, the
file:line, and why it looks unintended. These are NOT requirements; they are
flagged for the user to decide.

### Doc conflicts

Each place where `docs/protocol.md`, `docs/architecture.md`, or
`docs/claude-code.md` states something about your section that the code does
not do: doc file:line, code file:line, the difference.

### Coverage

The list of every file you read in full.
