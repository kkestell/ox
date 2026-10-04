# Simplify and clarify the Go codebase

## Scope and coverage

Reviewed all Go source and tests under `server/`, including the command entry
point, ACP transport, turn execution, session operations, transcript and
storage, OpenRouter integration and test helpers, settings, skills, system
prompts, file tools, and foreground and background shell processes. Traced
callers and tests before simplifying code. Read `AGENTS.md`, `README.md`, the
existing plans, and the previous Go review as requirements and context.

The three pre-existing modifications in `agent/agent_test.go`, `tools/tools.go`,
and `tools/tools_test.go` were reviewed as context and excluded from this
commit. Validation includes them in the working tree. Live provider requests,
system keyring operations, and Linux runtime behavior were not exercised. This
review focused on simplicity and readability, not an exhaustive correctness or
security audit.

## Fixed

- **Usage accounting splits one result across two passes**
  (`server/internal/transcript/transcript.go:398`, low, simplicity): Latest
  context usage and accumulated cost required separate transcript traversals,
  repeating batch handling and creating a new pointed-to total for each reported
  cost. Compute both in one forward pass with one total. Preserve missing usage,
  unreported versus zero cost, and chronological cost addition. Added tests for
  these observable distinctions.
- **Patch preparation opens the same source twice**
  (`server/internal/tools/patch.go:393`, low, simplicity): An update opened and
  checked its source, closed it, then reopened and checked it through `readText`
  when chunks needed its contents. Read through the already checked handle and
  close it on return. Move-only updates still check the source without reading
  its contents, and existing confinement and regular-file checks remain.
- **Process reporting hides a condition in an empty switch case**
  (`server/internal/tools/shell.go:255`, low, readability): The special handling
  for stops depended on an empty case preceding success and failure cases. Use
  explicit conditions to show that reads attach content on success and fail on
  unsuccessful exits, while stops report completion regardless of exit status.
- **Output conversion repeats Go's UTF-8 decoding**
  (`server/internal/shellproc/shellproc.go:77`, low, simplicity): A handwritten
  rune-decoding loop duplicated Go's conversion from a string to runes. Use that
  conversion, retaining the valid UTF-8 fast path. Strengthened the capture test
  to verify that consecutive invalid bytes each receive a replacement character.

## Findings

No open findings within the review's scope.

## Checks run

- Diff inspection confirmed the changes retain file confinement, move-only
  validation, process-result conventions, and usage-summary semantics.
- `make check` — Passed: code formatting, Go vet and tests, Rust tests and
  build, and Clippy. Twelve tmux tests were ignored.
- `make check-docs` — Initially failed on this review's paragraph wrapping;
  passed after formatting the review file.
- `make e2e` — Skipped; these refactors preserve terminal behavior. The tmux
  tests are ignored by `make check`.

## Verdict

Four low-severity simplicity and readability findings fixed; none remain open.
The code's existing package boundaries and explicit resource ownership are
appropriate. No larger redesign is called for by this review.
