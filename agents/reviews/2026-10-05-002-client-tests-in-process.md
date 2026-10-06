# Review: commit c49c7e8, client tests in process

## Scope and coverage

Reviewed all changes in c49c7e8 against
`agents/plans/2026-10-05-003-client-tests-in-process.md`: the client connection
constructor and process startup, the in-process client and terminal fixtures,
the retained terminal tests and their build setup, catalog fixtures and boundary
coverage, and event-driven agent cancellation tests. Traced the ACP transport,
client session state, server request ordering and shutdown, and terminal config
updates. Validation used scripted agents and OpenRouter responses; no live model
or external ACP server was used.

## Fixed

- **Startup stderr can block initialization** (`internal/client/conn.go:109`,
  medium): `Start` called `Connect` before reading stderr. A server that filled
  the stderr pipe before replying to initialization blocked until the client's
  30-second startup timeout. A helper process writing 1 MiB to stderr reproduced
  the timeout. `Start` now starts the connection and stderr reader, installs its
  process cleanup, and then initializes. `Connect` shares the connection
  construction through a private helper. The regression test also checks that
  both startup diagnostics arrive intact.
- **The composer cancellation test can hang** (`internal/tui/tui_test.go:429`,
  medium): `Session.Prompt` dispatches from a goroutine, so the test could send
  cancellation before the scripted agent received the prompt. The agent ignored
  that cancellation and its subsequent hanging reply never finished. The test
  timed out with `GOMAXPROCS=1`; its stack showed the reply waiting for
  cancellation and the driver waiting for an event. A channel now makes the test
  wait until the scripted reply starts before exercising composer keys and
  cancellation.

## Findings

None open.

## Checks run

- Before fixes, `TestStartupDrainsStandardError` failed with
  `server startup
  timed out` after 30 seconds.
- Before fixes,
  `GOMAXPROCS=1 go test -count=20 -timeout=5s -run
  '^TestATurnKeepsPromptsAndResumeInTheComposerUntilItEnds$' ./internal/tui`
  timed out. After the fix, the same 20 runs passed with a 20-second deadline.
- The startup stderr, process exit, and stderr diagnostic tests passed together
  after the fix.
- `make check` failed in `dprint check` on the existing untracked
  `evals/reports/smoke-yaml.md`. Its later Go checks were skipped by Make, so
  they were run separately: the repository gofmt check, `go vet ./...`, and
  `go test ./...` all passed. The untracked report was not changed.
- `go test -race -count=1 -timeout=60s ./internal/client ./internal/tui` passed.
- `make e2e` passed.
- The new review passed `dprint check`; `git diff --check` passed.

## Verdict

Two medium findings fixed; none open. The in-process tests meet the plan's goal,
with startup stderr draining restored and deterministic cancellation coverage.
The full `make check` target remains blocked by the unrelated untracked report's
formatting error.
