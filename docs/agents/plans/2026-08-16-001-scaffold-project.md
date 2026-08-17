# Scaffold project

## Goal

The repository has no Go code. Establish the module, the binary entry point, the
build and check tooling, and the logging discipline a stdio protocol server
requires, so protocol work can land on a working foundation.

## Desired outcome

`make check` runs clean and `go build ./...` produces an `ox` binary that
starts, logs to stderr, and exits zero when stdin closes. Nothing is written to
stdout.

## Summary of approach

One module, `github.com/kkestell/ox`. A single `cmd/ox/main.go` that reads the
environment, builds a logger, and blocks until stdin closes. Subsystem packages
arrive under `internal/` as the work that needs them lands; none are created
speculatively.

Logging uses `log/slog` with a text handler writing to `os.Stderr`, at a level
read from `OX_LOG_LEVEL`. The logger is constructed in `main` and passed
explicitly to anything that needs it. There is no package-level logger global.

The Makefile grows Go targets beside the existing docs targets, wrapping exactly
the four commands the code style requires: `gofmt`, `go vet`, `staticcheck`, and
`go test -race`.

## Related code

- `~/src/references/repos/personal/alpha/runtime/cmd/amber-runtime/main.go` -
  The entry-point shape to follow: read environment, construct subsystems,
  attach the transport, block. Roughly 50 lines with no logic of its own.
- `~/src/references/repos/personal/alpha/runtime/internal/` - Eight subsystem
  packages, each owning one concern. The boundary granularity to grow toward.
- `Makefile`, `dprint.json` - Existing docs tooling to extend rather than
  replace.

## Current state

- Relevant existing behavior: none. The repository contains documentation,
  `Makefile`, `dprint.json`, and `.gitignore` only.
- Existing patterns to follow: the `Makefile` already splits `check` and
  `format` into per-concern subtargets (`check-docs`, `format-docs`). Go targets
  follow that naming.
- Constraints from the current implementation: `.gitignore` ignores
  `/~/src/references/repos` and `/.env`; both stay ignored.

## Structural considerations

- **Hierarchy:** `cmd/` depends on `internal/`, never the reverse. `main` is the
  only place that reads the environment or touches `os.Stdin`/`os.Stdout`.
- **Abstraction:** No interfaces are introduced. Concrete types only, until a
  second real implementation exists.
- **Modularization:** No `internal/` packages are created by this plan. Creating
  empty subsystem packages ahead of the code that fills them would be
  speculative structure.
- **Encapsulation:** stdout belongs to the protocol transport alone. Logging is
  confined to stderr by construction, not by convention.
- **Testability:** The logger is a constructor parameter, so tests can capture
  output. `main` holds no logic worth testing.

## Test plan

- **Key behaviors to verify:** the binary builds; it exits zero on stdin EOF; it
  writes nothing to stdout.
- **Test levels:** one integration test that runs the built binary as a
  subprocess and asserts stdout is empty and the exit code is zero.
- **Edge cases and failure modes:** an unset or unrecognized `OX_LOG_LEVEL`
  falls back to info rather than failing.
- **What not to test:** the Makefile, `gofmt` behavior, and `slog`'s own
  formatting.

## Implementation plan

- `go mod init github.com/kkestell/ox`, letting the toolchain write the `go`
  directive.
- Add `github.com/creachadair/jrpc2` with `go get`, so the version is resolved
  rather than hand-written. It is used by the protocol work that follows.
- Write `cmd/ox/main.go`: parse `OX_LOG_LEVEL`, build the stderr `slog` handler,
  log a startup line, block on stdin until EOF, exit zero.
- Add Makefile targets `format-go` (`gofmt -l -w .`), `check-go` (`go vet ./...`
  then `staticcheck ./...`), and `test` (`go test -race ./...`); fold `check-go`
  and `test` into `check`, and `format-go` into `format`.
- Add the integration test asserting the binary produces empty stdout and exits
  zero.

## Documentation updates

- Roadmap item completed: "Scaffold project".

## Impact assessment

- Code paths affected: new module; no existing code.
- Data, protocol, or schema impact: none.
- Dependency or API impact: adds `github.com/creachadair/jrpc2` as the sole
  direct dependency. `staticcheck` is expected on `PATH` rather than pinned as a
  module tool, keeping the manifest free of roughly two hundred indirect
  requirements.

## Validation

- Tests to write and run: `go test -race ./...`.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: `echo -n '' | ./ox` exits zero and prints only to stderr;
  `./ox > /tmp/out 2>/dev/null` leaves `/tmp/out` empty.
