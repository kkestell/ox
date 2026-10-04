# Simplify Go infrastructure

## Scope and coverage

Implemented the five approved replacements from the Go simplicity review and
removed workspace confinement at the user's request. Reviewed the affected
search, file, protocol, stream, and command paths and their callers. This is a
follow-up to the whole-codebase review, not another audit of every package.

## Fixed

- **Handwritten glob parsing** (`server/internal/tools/search.go:62`): Removed
  the glob-to-regexp compiler. The glob tool uses ripgrep directly, including
  its native glob and ignore behavior.
- **Handwritten content search** (`server/internal/tools/search.go:62`): Ripgrep
  now performs grep searches directly. Ox decodes its JSON output for display,
  counts, and output limits. Go regexp validation and per-file scanning are
  gone; patterns use ripgrep syntax.
- **Handwritten protocol infrastructure** (`server/internal/acp/conn.go:40`):
  Replaced message dispatch, request IDs, pending calls, and serialization with
  Sourcegraph's JSON-RPC library. ACP requests, responses, and content use
  Coder's generated ACP types. The adapter retains ordered operation dispatch
  and drains replies before connection shutdown. Small extensions retain tool
  names used by the terminal client. Malformed transport input closes the
  connection with an error.
- **Handwritten CLI flag parsing** (`server/cmd/ox-server/main.go:158`): Uses
  Go's `flag` package. Options precede the prompt; `--` permits a prompt
  starting with a dash. README instructions and tests cover the supported
  syntax.
- **Handwritten SSE framing** (`server/internal/openrouter/stream.go:104`): Uses
  `go-sse` to parse events, keeping OpenRouter completion assembly and
  cancellation in Ox. Coverage includes BOMs, CR line endings, multiple data
  fields, events over 64 KiB, and final usage. Events have a 16 MiB limit.
- **Unwanted workspace confinement** (`server/internal/tools/files.go:10`): File
  reads and patches accept absolute paths, parent paths, and symlinks. Removed
  `os.Root`, path-boundary validation, descriptor-relative move setup, and the
  workspace object. Ordinary file operations retain regular-file checks and
  exclusive creation; moves still avoid overwriting destinations.

## Findings

None remain in the approved replacement scope.

## Checks run

- Focused Go tests passed after updating obsolete confinement, canonical-path,
  and glob-parser expectations. Initial runs failed on those expectations and
  caught a duplicate `./` output prefix, which was corrected.
- `make check` passed: documentation, Go build/vet/tests, Rust formatting,
  tests, build, and Clippy.
- `make e2e` passed all 12 terminal tests.
- `go test -race ./internal/server ./internal/openrouter ./internal/tools`
  passed.
- Linux runtime validation was not run; local validation ran on macOS.

## Verdict

All five approved infrastructure replacements are complete. File access now
matches the user's intended use outside the session workspace.
