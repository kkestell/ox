# Session review: tool and harness friction during release implementation

**Scope:** durable session `f8f154c2b21096cbd2e09a3828beaf7f`, which implemented
`eng/plans/2026-09-13-031-versioned-binary-releases.md` in the Ox workspace on
2026-09-13.

**Mode:** model-behavior and developer-experience review.

**Severity:** severity measures the likelihood that the tool or harness behavior
causes wasted model work, unreliable verification, or an avoidable coverage gap.
It does not imply that the completed release change is incorrect.

The session completed successfully in 20 minutes and 38 seconds. It made 106
provider requests and started 133 tools: 55 shell calls, 37 file reads, and 30
exact edits. Nine file mutations were rejected. The final checkpoint occupied
131,562 tokens and reported 85,703 thought tokens, including the independent
review child's work. Those totals are not defects by themselves, but the trace
shows repeated reasoning and retries around a small set of tool rules.

## Findings

### High: read-before-edit state creates an invisible cross-tool dependency

**Location:** `internal/tools/shell.go:78-80`, `internal/tools/edit.go:98-104`,
`internal/tools/read.go:89-93`, and `internal/agent/prompt.go:27-38`

All nine rejected mutations reported that `read_file` had not read the file's
current contents. The first rejection followed earlier paged reads of
`cmd/ox/main.go`. The model inferred that a single full-file read might be
required, even though `read_file` hashes the complete raw file before returning
any requested window. Later rejections produced the same confusion; after one it
reasoned that it had read the file in the previous exchange and called the
result "weird."

The actual dependency is that every shell invocation clears every live file-read
scope before the command starts. That includes commands which only inspect Git
state, access the network, or delete an unrelated temporary artifact. It also
means a shell call and an exact edit are not independent even when they target
different paths: if the model submits them together and shell runs first, the
edit loses its evidence and fails.

The system prompt says to parallelize independent tool calls, but it does not
explain that shell is globally coupled to every later file mutation. The edit
error distinguishes neither a file that was never read from a read invalidated
by shell, nor the call that caused the invalidation. The model therefore learned
the wrong rule and continued to pay read-and-retry round trips.

The state does not provide a strong model-awareness or concurrency guarantee. A
paged `read_file` call records the hash of the complete file even though the
model sees only the requested window. The local mutation path can still race an
external writer after reading current content, and the delegated path has the
same read-then-write gap. For an exact edit, matching `old_string` against the
current content is the useful localized precondition; a historical whole-file
hash additionally rejects harmless changes outside the replaced span.

Whole-file overwrite is the one operation for which a localized exact match is
absent. Retaining the global evidence mechanism for that case would keep its
session state, child-scope invalidation, shell coupling, and executor
complexity. Making writes create-only would instead add special handling for
empty existing files and a guarantee the ACP filesystem callbacks cannot enforce
atomically.

**Suggested fix:** remove session-scoped read evidence while retaining
`write_file` as an explicit create-or-replace operation. Keep `edit_file`'s
exact current-content match as its mutation precondition. Local and delegated
operations must continue to use the selected filesystem consistently, but
neither should depend on earlier tool history. Remove shell invalidation and the
resulting mixed-batch dependency.

### Medium: output-truncation pipelines can turn a failed validation into success

**Location:** `internal/tools/shell.go:93-98`,
`internal/tools/shell.go:139-154`, and `internal/agent/prompt.go:32-38`

The model repeatedly bounded validation output itself:

```sh
make check 2>&1 | tail -40
go test ... 2>&1 | grep ...
go run ...actionlint@latest ... 2>&1 | tail -40
```

Ox invokes `/bin/sh -c` and reports the shell's exit status. Without `pipefail`,
these commands report the status of `tail` or `grep`, not the validator. A test,
build, or linter could fail while the tool reports `exit code: 0`, after which
the model may claim the gate passed. The validators did pass in this session,
but that was visible from their output rather than guaranteed by the reported
status.

This workaround is unnecessary because the shell recorder already bounds inline
output and spills accepted overflow. The prompt explains spill retrieval but
does not connect that behavior to preserving a command's exit status.

**Suggested fix:** tell the model to run validators directly because output is
already bounded, and explicitly warn that piping through `head`, `tail`, or
`grep` can hide the validator's status. Add focused evaluation coverage in which
a noisy validator fails, so a model must retain both bounded output and the
original nonzero status. If instruction alone is insufficient, provide an
output-view option on `shell` that limits displayed lines without changing the
executed command.

### Medium: the process harness cannot prove that an informational command ignores open stdin

**Location:** `internal/e2e/harness_test.go:269-289` and
`internal/e2e/version_test.go`

The release plan required proof that `ox --version` does not depend on stdin.
The new test uses `runCommand`, which always installs a `strings.Reader` as
stdin. The empty string is immediately at EOF, so an implementation that reads
stdin before printing the version could still pass.

The independent review found the gap. The primary model understood it and
considered several workarounds, but rejected them as too much harness machinery
for one test. It left the todo item complete and disclosed the limitation in its
final answer. Invalid settings and empty stderr prove that version handling
precedes process configuration and server startup; they do not prove that no
earlier stdin read was introduced.

**Suggested fix:** let `runCommand` accept an `io.Reader`, or add a focused
helper that keeps the write end of a pipe open while waiting for the process.
Use the existing command timeout to fail if `ox --version` waits for input, and
retain the exact stdout, stderr, and exit-code assertions.

## Other observed friction

- A plain `go build ./cmd/ox` left a 16 MiB untracked `ox` executable at the
  repository root. The primary did not notice it until the review child checked
  the working tree. The child caught the problem and the primary removed it;
  subsequent builds used cleanup or temporary output paths. Per-shell workspace
  mutation reporting might surface this sooner, but computing a reliable delta
  for every arbitrary command may cost more than this isolated mistake
  justifies.
- A `read_file` request with offset 130 against a 99-line file returned
  `[offset 130 is past the end of the file (99 lines)]` as ordinary successful
  content. The model recovered on the next request. Treating an out-of-range
  window as data is internally consistent, but structured `totalLines` and
  `nextOffset` metadata would remove the need to guess offsets.
- Manual network inspection through shell produced `curl` error 56 when `head`
  closed a pipe and an empty response when a redirect was fetched without `-L`.
  The model diagnosed both. They reinforce the exit-status finding but do not
  justify a separate network tool change from this session alone.

## What worked

- Exact-edit failures were safe and recoverable; no unintended mutation was
  applied.
- Shell output included enough detail for the model to diagnose linker-symbol,
  checksum-tool, and cross-platform differences.
- The independent review child found a release-blocking GitHub CLI problem,
  incomplete archive-presence coverage, the stdin limitation, documentation
  inaccuracies, and the stray executable. The parent consumed the result,
  verified its material claims, and fixed every issue except the disclosed
  open-stdin coverage gap.
- The final `make check` completed successfully, the turn ended normally, and
  the durable checkpoint recorded every changed file.

## Recommended sequence

1. Remove session read evidence while retaining create-or-replace writes and
   exact matching for edits. This eliminates the dependency that caused every
   rejected mutation without adding another file-operation contract.
2. Make the existing bounded shell-output behavior the safe path for validation
   commands, preserving the validator's exit status.
3. Add the small open-stdin harness primitive and close the release regression
   test gap.
