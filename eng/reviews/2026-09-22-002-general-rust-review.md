# General Rust code review

Date: 2026-09-22

## Scope and coverage

Reviewed the full Rust corpus in `src/` (14 files), including the ACP request
handlers, prompt lifecycle, OpenRouter stream assembly, session persistence,
authentication, and every tool implementation. Selected topics: correctness,
security, concurrency, resource cleanup, error handling, architecture, and
testing. I traced production paths and relevant tests; I did not inspect every
test assertion or make a live OpenRouter request. No `unsafe` code is present.

## Finding

### Moderate: Concurrent path changes can escape the workspace

`src/tools/patch.rs:238-303` validates paths and prepares changes, then
`src/tools/patch.rs:309-338` uses those paths later. A separate process or
concurrent session can replace a validated file with a symbolic link in between.
`fs::write` follows the new link, so `apply_patch` can write outside the
workspace despite its documented path restriction.

I reproduced this with a throwaway probe using the actual patch module: prepare
an update to an inside file, replace that file with a link to a sibling file,
then apply the prepared change. The tool returned `Applied patch` and the
outside file changed from `outside` to `changed`.

The read and search tools have the same check/use gap. `src/tools.rs:106-123`
canonicalizes and checks a path, but `src/tools/read.rs:30-40` later opens the
path by name, while `src/tools/search.rs:59-78` passes the original path to
`rg`. A path replaced after validation can therefore be read or searched
outside the workspace. The patch write was reproduced; the read and search
cases are established from their source paths and ordinary symbolic link
following behavior.

If confinement must hold while a workspace changes concurrently, use opened
directory or file handles for the operation and constrain symbolic link
resolution at use time. Another pathname check does not close the race. If
concurrent workspace mutation is outside the intended trust boundary, state
that limit in the workspace confinement contract.

## Unresolved suspicions

None. A possible model-selection race was ruled out after tracing the ACP
library's incoming actor: it dispatches one handler at a time, and Ox saves the
first user message before its prompt handler returns.

## Checks run

- `cargo test`: 84 passed, 0 failed.
- `cargo build`: passed.
- Isolated path-swap probe against `src/tools/patch.rs`: confirmed the outside
  write. The probe was outside the repository; no implementation files changed.

## Verdict

The main session, stream, and tool lifecycles are coherent and covered by
focused tests. The confirmed workspace confinement race needs a contract or
implementation decision before relying on file-tool paths as a security
boundary.

## Resolution

The patch and read tools now use descriptor-relative file operations with
`O_NOFOLLOW` at use time. Search keeps ripgrep's file discovery and ignore
filtering, then checks each candidate through the same workspace directory
handle before returning a path or opening it to search contents. Existing tests
were strengthened to swap a validated path for an outside symbolic link between
validation and use. Raw ripgrep path diagnostics are not forwarded because a
concurrent change could make them name outside files. All 84 tests pass after
the fix. The touched tool modules still have 21 test functions; their test code
grew by 37 lines to cover path swaps while retaining subprocess cancellation
coverage.
