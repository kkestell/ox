# Browser workspace supervisor review

## Scope and mode

General review of the unstaged browser workspace-supervision change: the Bun
host, browser protocol and rendering, supervisor, tests, end-to-end harness,
plan, and roadmap update.

No Go production code changed. The review applied the selected topics to the
TypeScript, React, Bun, documentation, and test boundaries.

## Selected topics

- **Correctness** — startup, initialization, child exit, and host snapshots are
  the behavior added by this change.
- **Concurrency** — the supervisor coordinates a child process, ACP transport,
  event listeners, and shutdown callers.
- **Resources** — the host owns process and transport lifetime.
- **Security** — child stderr crosses into a browser-visible diagnostic field.
- **Testing** — the change adds unit and real-process lifecycle coverage.
- **Architecture** — the supervisor and host implement the ownership boundary
  described in `eng/client-architecture.md`.
- **Readability** — the new lifecycle owner must keep its state transitions
  locally understandable.
- **Documentation** — the plan and roadmap state the completed behavior.

## Findings

### High — an Ox process that never answers `initialize` leaves host startup hung forever

`client/src/workspace-supervisor.ts:112-134` races initialization only against
child termination. A live but nonresponsive executable wins neither branch, so
`WorkspaceSupervisor.start` and consequently `startHost` never return. This
contradicts the required ready-or-unavailable startup state and makes a
connection failure indistinguishable from a hung host.

Suggested fix: bound initialization, make timeout publish an unavailable
diagnostic, close the ACP transport, and terminate the child. Add a fixture that
accepts stdin without replying to `initialize`.

### Medium — browser-visible stderr redaction does not cover common credential forms

`client/src/workspace-supervisor.ts:202-204` only masks an equals-delimited
value after names such as `token`. Standard error is untrusted process output
and can include `Authorization: Bearer <value>`, JSON fields, or colon-delimited
credentials, all of which currently reach browser snapshots unchanged. The
architecture requires browser-safe diagnostics.

Suggested fix: redact authorization bearer values and named secret values with
common assignment delimiters, then cover the accepted forms directly.

### Medium — shutdown waits four seconds after an executable launch failure

`client/src/workspace-supervisor.ts:58-72` treats a failed `spawn` child as if
it could still receive signals. Its exit fields remain unset, so `stop` waits
for both two-second termination windows even though the `error` event already
proves no child was launched. The existing launch-failure unit test took about
four seconds in this review run. This weakens the clean-shutdown guarantee and
can delay harness cleanup.

Suggested fix: retain a shared shutdown promise, recognize a launch failure as
already terminated, and have every `stop` caller await the same completion.

## Topic verdicts

- **Correctness:** initialization needs a failure deadline.
- **Concurrency:** shutdown needs a single shared completion path.
- **Resources:** failed launch must not take process-termination waits.
- **Security:** stderr needs broader credential redaction before browser
  publication.
- **Testing:** add timeout, redaction, and failed-launch-shutdown regressions.
- **Architecture:** host and supervisor ownership otherwise match the client
  architecture; no duplicate process owner found.
- **Readability:** the small modules remain direct; the lifecycle gaps above are
  the only issues found.
- **Documentation:** the plan and roadmap accurately describe the intended
  slice; no documentation issue found.

## Checks run

- `bun run check` — passed.
- `bun test` — the 15 Bun tests passed, but direct discovery also loaded the
  Playwright specification and failed because Playwright tests must run through
  its runner. The package's explicit `bun run test` command is the applicable
  unit-test entry point.
- After the fixes: `bun run check`, `bun run test`, and `bun run test:e2e` all
  passed; `make check-docs` and `git diff --check` also passed.

## Follow-up

The findings are fixed in the implementation work that follows this review and
the review report is included in that commit.
