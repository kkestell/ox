# A Unix-only platform contract and accurate repository documentation

## Goal

Settle which systems Ox targets, and remove the documentation and comparison
edges that assume otherwise.

## Desired outcome

The repository states one platform contract and carries no code that implies
another. A verifier's trailing-newline tolerance treats CRLF the same as LF. The
repository and user documentation describe what exists.

## Summary of approach

Ox is Unix-only. Shell execution, process-group cancellation, owner-only
credential files, and session locking all use POSIX semantics directly, and the
shipped command has never built for Windows. Say so in the specification,
architecture, and README, and delete the Windows lock and credential-security
implementations that suggested otherwise. With one implementation left, their
build tags and the file-handle parameter that existed only for the Windows
variant both go away.

Evaluation file verification already forgives one trailing newline. Make that
either terminator, because a workspace file may use CRLF regardless of the host.

## Related code

- `internal/tools/shell.go` - Uses `/bin/sh`, `Setpgid`, and `syscall.Kill`
  unconditionally.
- `internal/agent/lock.go`, `internal/credentials/owner.go` - Where the
  remaining single implementations belong.
- `evals/internal/eval/task.go` - Compares produced files to expected content.
- `AGENTS.md`, `eng/review-targets.md`, `README.md`, `docs/zed.md` - The drifted
  maps and guides.

## Current state

- `GOOS=windows go build ./...` fails in `internal/tools`, and a Windows vet
  pass fails on two untagged tests that call `syscall.Mkfifo`. Meanwhile
  `lock_windows.go` and `owner_windows.go` are maintained, so the contract reads
  both ways.
- `credentials.validateFileSecurity` takes an unused `*os.File` because the
  Windows implementation needed a handle.
- `strings.TrimSuffix(value, "\n")` leaves the carriage return in a CRLF
  terminator, so a CRLF file never matches an expected value without one.
- `AGENTS.md` calls `internal/lsp` unimported and describes environment-backed
  credentials the process does not have; `eng/review-targets.md` points at
  deleted files; the README omits the language tools; `docs/zed.md` links a
  guide that does not exist.

## Structural considerations

- **Abstraction:** Deleting the second platform removes a build-tag split that
  named a distinction Ox does not make.
- **Encapsulation:** The credential check loses a parameter that existed only
  for a caller that no longer exists.

## Test plan

- **Key behaviors to verify:** Expected content matches a file ending in LF,
  CRLF, or no terminator, and still rejects a blank line or a bare carriage
  return.
- **Test levels:** The existing focused verifier test, extended.
- **What not to test:** Locking and credential security, whose single
  implementation and coverage are unchanged.

## Implementation plan

- State the Unix-only contract, delete the Windows implementations, and collapse
  the remaining files and the credential check.
- Trim either final terminator in evaluation verification and cover CRLF.
- Repair the drifted repository maps and guides.

## Documentation updates

- `docs/spec.md`, `eng/architecture.md`, and `README.md` state the platform.
- `AGENTS.md`, `eng/review-targets.md`, `README.md`, and `docs/zed.md` are
  corrected.

## Impact assessment

- Code paths affected: Session locking, credential-file security, evaluation
  verification.
- Data, protocol, or schema impact: None.
- Dependency or API impact: `golang.org/x/sys` becomes indirect. The deleted
  Windows files were Ox's only direct use of it.

## Validation

- Tests to write and run: The extended verifier test, then `make check` and
  `make test-eval`.
