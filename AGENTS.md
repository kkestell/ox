# AGENTS.md

KEEP THIS FILE AND ITS LINKED REFERENCES UP TO DATE AT ALL TIMES.

Ox is a coding agent written in Go that speaks ACP v1 over standard input and
output. It is ACP-first and works with ACP clients. The durable design lives in
`eng/architecture.md`, and the build order and ACP method coverage live in
`eng/roadmap.md`. `docs/spec.md` owns target product behavior.

## Tech Stack

- **Language:** Go 1.26.4
- **Protocol:** ACP v1 over JSON-RPC 2.0 on stdio
- **Model provider:** OpenRouter Chat Completions over HTTP and SSE
- **Build and checks:** Make, gofmt, go vet, staticcheck, and go test

## Codebase Map

- `cmd/ox/` — The `ox` executable, stdio server, process configuration, and
  `login` command.
- `internal/acp/` — ACP wire types and protocol-boundary validation.
- `internal/agent/` — ACP methods, durable sessions, model/tool orchestration,
  permissions, subagents, replay, authentication, and cancellation.
- `internal/openrouter/` — OpenRouter transport, SSE parsing, retry, model
  catalog, and stream assembly.
- `internal/settings/` — Layered model and provider settings resolved for a
  session activation.
- `internal/credentials/` — Environment and OS-keyring credential storage.
- `internal/trace/` — Sanitized, concurrency-safe JSONL diagnostic tracing.
- `internal/tools/` — Local coding tools exposed to the model.
- `internal/shellrules/` — Parsed reusable permissions for shell commands.
- `internal/workspace/` — Confined filesystem access, traversal, and streamed
  output.
- `internal/e2e/` — Black-box tests that build and drive the real binary.
- `integration/` — Runtime integration tests across the agent, tools, and
  durable state.
- `docs/` — End-user documentation and the target product specification.
- `eng/` — Development and agent documentation. `eng/architecture.md` owns the
  design, `eng/roadmap.md` owns build order and status, and `eng/plans/` holds
  plans for individual roadmap slices.

## Commands

- Run every required check: `make check`
- Format Go and Markdown: `make format`
- Check Go formatting, vet, and static analysis: `make check-go`
- Check documentation formatting: `make check-docs`
- Run the race-enabled test suite without the test cache: `make test`
- Run the automated browser-client smoke test: `make test-client`
- Run the explicitly requested real-provider browser check:
  `make test-client-live`
- Install the binary: `make install`

## Project Rules

### Answering

Be short. Say the thing and stop.

A few sentences is the normal length of a reply. Most questions need one or two.
Never write five paragraphs where one would do. If a reply is running long, cut
whole points rather than compressing them into denser sentences.

Write plainly. Ordinary words, ordinary sentences, one idea each. Say it the way
you would say it out loud to someone sitting next to you. No throat-clearing
before the answer, no summary of what you just did after it, no restating the
question, no listing the options you considered and rejected.

Do not be clever or cryptic. Do not stack clauses onto a sentence with dashes
and semicolons; start a new sentence. Do not invent names for things that
already have names. Prefer the concrete: name the file, function, or value.

When you need a decision, ask one plain question.

This governs replies. Files follow the documentation rules below.

### Project priorities

Ox combines the strongest ideas from previous coding agents behind an ACP-first
boundary. Prefer standard ACP methods and capability negotiation over
client-specific side channels. When a required ACP client capability is absent,
fail clearly instead of silently changing semantics.

The project is private, greenfield, and has no users. There are no backward
compatibility constraints. Refactor freely when the result is simpler. Do not
add compatibility paths.

The value of the Go implementation is how cheaply it can be changed tomorrow.
Keep the domain rules and protocol semantics precise, and keep the code that
implements them thin and unsurprising. Treat simplicity as a maintained project
invariant, not a cleanup activity.

Read `eng/architecture.md` before changing structure and `eng/roadmap.md` before
planning work. Do not introduce a dependency, abstraction, subsystem, protocol,
storage format, background process, configuration surface, or significant
behavior change without consulting the user.

Preserve unrelated working-tree changes. Never commit unless the user asks for a
commit explicitly.

### Planning and milestone review

Write a plan in `eng/plans/` for every roadmap slice before implementation. Use
`eng/plans/TEMPLATE.md` as a scaffold, then keep the plan to the smallest useful
set of source references, implementation tasks, and tests. Do not restate the
roadmap, architecture, repository rules, or standard validation commands.

Do not review individual plans or run an independent review after each slice.
After every slice in a milestone is implemented and its gates pass, run one
completeness and simplification review over the cumulative milestone before
marking it complete. Fix its findings and rerun affected gates.

Use a focused task workflow for small work that does not need a plan or commit.

### Prior art

Before planning a substantial capability, explore the relevant Ox code and
consult the feature matrix in `~/src/references/index.md`. The referenced
repositories live in `~/src/references/repos`. Prefer the most robust solution
from the author's previous projects, and reuse substantial code when it fits.
Consult `~/src/references/repos/third-party` only when no strong first-party
candidate emerges.

Treat prior art as input, not as Ox's source of truth. Record the resulting Ox
design in `eng/architecture.md` when it changes a durable responsibility,
boundary, or hard-to-reverse decision.

### Referring to planned work

`eng/roadmap.md` orders milestones and slices by their position in the file. Do
not number them. Completed work can then be removed without renumbering what
remains.

Everywhere else—code comments, commit messages, pull requests, plans, and
replies—describe the work itself. Write "session replay is not implemented"
rather than "deferred to a later milestone." Every sentence must make sense to a
reader who has never opened the roadmap.

### Maintaining the roadmap

`eng/roadmap.md` is forward-looking. Keep detailed scope and gates only for work
that has not been completed. When a milestone is complete, replace its detailed
section with a concise summary and remove the previous completed summary. The
roadmap keeps exactly one completed milestone summary.

The summary identifies the completed outcome without restating protocol rules,
implementation design, or code behavior owned elsewhere.

Keep ACP method coverage in the roadmap synchronized with the implementation.

### One home for every fact

Every fact lives in exactly one place. `docs/spec.md` defines target product
behavior. End-user guides describe how to use shipped Ox, `eng/architecture.md`
defines the implementation design, `eng/roadmap.md` defines what gets built
next, plans say how one bounded change will be made, and this file defines how
to work in the repository. Reference a fact that lives elsewhere by naming the
file that owns it. Do not keep a convenient copy nearby.

### What gets documented

`docs/` holds end-user documentation. Guides describe behavior that exists
without previewing roadmap work. `docs/spec.md` is the explicit exception: it
defines target behavior, with implementation status owned by `eng/roadmap.md`.
Note an omission only when it is an intentional product decision a user must
understand.

`eng/architecture.md` covers process boundaries, component responsibilities,
dependency direction, state ownership, and decisions that would be expensive to
reverse. A reader should finish it able to say where a change belongs without
opening the source.

It does not explain local implementation mechanics. Function signatures, struct
layouts, algorithms, validation wording, and per-test setup live in code. A
sentence that would change because a helper was renamed or a loop reorganized
does not belong in the architecture.

Change `eng/architecture.md` when a responsibility changes, a boundary moves, a
package is added or split, or a durable decision is reversed. Implementing a
feature the design already accounts for does not require an architecture edit.

This file has the same limit. It says how to work in the repository. Detail too
fine for the architecture does not belong here either, or in a new document
invented to hold it.

### Go design rules

Use Go's cheap safety: static types, useful zero values, `go vet`,
`staticcheck`, and `go test -race`.

Validate bad external input at the boundary and return clear, human-readable
problems. Past that boundary, missing values and impossible states are bugs.
Panic rather than silently recovering or substituting zero values.

Prefer:

- values over pointers until mutation or sharing requires them;
- concrete types until multiple real implementations justify an interface;
- plain functions and `switch` statements over visitors, registries, or generic
  frameworks; and
- synchronous code until real independent work or cancellation requires
  concurrency.

Abstractions should be discovered through repetition, not imposed up front. Do
not build retries, fallbacks, error taxonomies, configuration layers,
concurrency, or extension points for cases that do not exist yet. Prefer an
explicit unsupported case or a focused TODO over machinery built on guesses.

User-facing validation problems are data: identify the source and give a clear
message. Everything else is plumbing: return `error` and wrap it only when
adding useful context. Do not define custom error types or sentinels unless code
actually branches on them.

### Simplicity rules

- Prefer a direct conditional to an indirect dispatch mechanism.
- A helper should remove or name a concept, not merely move lines elsewhere.
- Tolerate a little duplication until the repeated concept and its boundary are
  understood.
- Do not create catch-all packages such as `util`, `common`, or `base`.
- Keep interfaces at consumer boundaries and small enough to explain in one
  sentence.
- Keep public APIs deliberately small. Do not expose internal state merely to
  make tests convenient.
- Explain why in comments. Do not narrate code that is already clear or let chat
  context leak into comments and documentation.

### Testing

Testing is part of the design. Add the smallest useful test at the lowest stable
boundary, plus a regression test for every fixed bug. End-to-end tests with a
mocked model are the preferred proof of ACP-visible behavior.

The harness in `internal/e2e` builds the `ox` binary, starts a fresh process per
test, and drives stdin, stdout, stderr, environment, working directory, and a
queued HTTP model endpoint. Compose model streams with the `sse` and `ev*`
builders rather than hand-writing SSE framing. Seed files through harness
options. Name queued responses when concurrent turns must not depend on arrival
order. Model mid-turn cancellation with `hold`.

The end-to-end harness disables keyring access. Shipped-binary tests supply
credentials through the environment, while keyring behavior uses go-keyring's
in-memory provider in unit tests. When a test does not start the mock provider,
the harness points Ox at a refused local address.

Use the `OPENROUTER_API_KEY` in `.env` only for an explicitly required real
provider check. Such checks use `openai/gpt-5.6-luna` and no other model. Never
expose the credential in output or commit it.

The end-to-end harness builds outside Go's test cache, so always run tests with
`-count=1`. Before considering behavior complete, run:

```sh
make check
```

For documentation-only and filename-only work, run the focused documentation
checks and inspect the diff instead of rebuilding or testing the binary.

### Dependencies and tools

Keep production dependencies few. Add one only when it enforces or implements a
concrete current requirement better than a small local solution. Use the
standard library when it is clear and sufficient.

Keep the Make targets under [Commands](#commands) aligned with the commands
developers and CI actually run.
