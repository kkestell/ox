# Review: write the default config, and give child titles a convention

## Scope and coverage

Uncommitted changes on `dac4fb8`: `internal/config/config.go`,
`internal/config/config_test.go`, `internal/server/mcp.go`,
`docs/architecture.md`, `docs/functional-requirements.md`, `docs/protocol.md`,
and `docs/todo.md`, checked against
`agents/plans/2026-11-22-008-default-config-and-child-titles.md` and the user's
answers recorded in it. Callers of `config.Load` (`cmd/oxd`, `cmd/ox`, and the
tests and e2e services that load a missing path) were traced.

The **Built-in catalog** requirement matches the approved text word for word.
The `create_session` description and `title` property match the plan's text.
The documentation edits match the plan's Documentation updates.

The plan's deviation is sound. The plan's test for "a path whose parent is a
regular file" assumed `os.ReadFile` reports the file missing. It reports
`ENOTDIR`, which is not `fs.ErrNotExist`, so `Load` never tries to write and
returns the read error: `open /tmp/oxrev/file/ox/config.json: not a directory`
(probed). `oxd` and `ox` stop with that message through `log.Fatal`. It names
the path and the reason, so **Configuration checks** and the plan's "refuses to
start and says why" hold. The test case now uses a read-only parent, where
`MkdirAll` fails and the `write default configuration to` prefix is reached,
which is the branch the plan meant to cover.

Gaps:

- The title convention is guidance to the model and has no automated check, as
  the plan states.

## Summary

| ID | Severity | Category      | Action | Status | Title                                                  |
| -- | -------- | ------------- | ------ | ------ | ------------------------------------------------------ |
| F1 | Low      | Testing       | Fix    | Resolved | Concurrent-load test rarely catches a non-atomic write |
| F2 | Low      | Documentation | Fix    | Resolved | Architecture row's `config` sentence misparses         |

## F1. Concurrent-load test rarely catches a non-atomic write

Low · Testing · Fix · `internal/config/config_test.go:201`

The plan's atomic publication exists so that `ox` and `oxd` starting together
never read each other's half-written file. The concurrent block in
`TestLoadWritesTheDefault` is the test that guards it. It runs one round of
eight `Load`s, and with `writeDefault` replaced by a plain
`os.WriteFile(path, defaultConfig, 0o600)` it fails in only 10 of 40 runs. The
failures it does catch are the real defect: `read …/config.json: EOF`.

### Evidence

With the temp-file-and-link body replaced by `os.WriteFile`,
`go test -count=40 -run TestLoadWritesTheDefault ./internal/config/` reported
10 failures. Dropping the `fs.ErrExist` tolerance failed 5 of 5 runs, and
dropping the temporary file's removal failed 1 of 1, so those guards are
protected.

### Action

Repeat the concurrent block over fresh paths, for example 50 rounds of eight
`Load`s each in its own `t.TempDir()`, checking every round's results and
directory contents as now. At the 25% per-round rate measured above, 50 rounds
miss a non-atomic write with probability below one in a million. Keep it in
`TestLoadWritesTheDefault`, and confirm it fails reliably with the
`os.WriteFile` replacement.

## F2. Architecture row's `config` sentence misparses

Low · Documentation · Fix · `docs/architecture.md:46`

The row now reads "Reading the configuration file, writing the built-in default
there when it is missing, into each harness's models, efforts, and permission
modes …". The inserted clause separates "Reading" from its object, so it reads
as writing the default into each harness's models. The plan supplied this text.
The fact is right, but the sentence does not state it.

### Action

Reword the row, for example: "Writing the built-in default to a missing
configuration file, reading the file into each harness's models, efforts, and
permission modes and the new-session defaults, and checking settings against
them." Run `make docs`.

## Checks run

- `make test`: passed.
- `make e2e`: 108 passed, 4 skipped.
- `dprint check`: passed.
- `go test -race ./internal/config/`: passed.
- Removals, one at a time, each reverted: `fs.ErrExist` tolerance on `Link`
  (fails 5/5), `defer os.Remove` of the temporary file (fails), non-atomic
  `os.WriteFile` publication (fails 10/40, F1).
- Probe: `Load` beneath a regular file returns
  `open …/file/ox/config.json: not a directory`.
- `make live` was not run. The change alters only the text of an MCP tool
  description, which live tests do not assert, and not how Ox drives Claude
  Code.

## Verification 1

Uncommitted changes on `dac4fb8`, after the fixes. `internal/config/config.go`
is unchanged since the review. Checks: `make test` passed; `make e2e` 108
passed, 4 skipped; `dprint check` passed; `go test -race -count=10` of
`TestLoadWritesTheDefault` passed.

| ID | Status   | Evidence                                                                                                                                               |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| F1 | Resolved | The concurrent block runs 50 rounds in fresh directories; with `os.WriteFile` in place of the link the test fails 20 of 20 runs, and passes without it |
| F2 | Resolved | The row reads "Writing the built-in default to a missing configuration file, reading the file into …"                                                  |

No new findings.

## Verdict

`Ready to commit`.
