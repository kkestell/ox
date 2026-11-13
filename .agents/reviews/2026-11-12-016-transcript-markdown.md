# Review: 2026-11-12-016-transcript-markdown

## Round 1

- Task: `2026-11-12-016-transcript-markdown`
- Independence: I did not write this code; the implementer was `/root/builder`.
- Reviewed change: `/Users/kyle/src/ox`, branch `main`, against
  `605310cc7b3bcc829fa9fe1d578705878ece54db`, including untracked code.
- Plan: [plan](../plans/2026-11-12-016-transcript-markdown.md)
- Work log: [work](../work/2026-11-12-016-transcript-markdown.md)

### Verdict

changes-required: shared rendering, streaming/replay integration and literal
tool output conform to the approved scope. One supported URL form is rejected
by the native opener because its scheme is not normalized.

### Findings

1. **P2: Normalize the URL before invoking the native opener.**
   `ui/src/Markdown.tsx:20–27` accepts HTTP(S)/mailto case-insensitively but sends
   the original `href`. For `[Docs](HTTPS://example.com/docs)`, react-markdown
   preserves `HTTPS://example.com/docs`. The configured permission patterns are
   lowercase, and the opener's `Scope::is_url_allowed` matches these with
   case-sensitive `glob::Pattern::matches`, rejecting the otherwise valid link.
   Pass the anchor's normalized `event.currentTarget.href` (or a canonical URL)
   and extend the existing opener test with a mixed-case scheme.

### Verification

- Run: Node SSR using `react-dom/server` and `react-markdown` confirmed the
  uppercase scheme is retained. Inspected the installed opener's
  `commands.rs`, `scope.rs` and glob's default match options to trace rejection.
- Run: `git diff --check`: passed. Lockfile comparison found no version changes
  among existing npm packages.
- Reused: `make check-ui`, `make check-rust` (265 tests), `make e2e` (46 tests,
  eight suites), `make smoke-claude` (Haiku): passed, with final log paths in the
  work record. Initial lint/test-authoring failures and expected regression
  removal failures are recorded there; they are resolved on current sources.
- Inspected final `markdown-blocks.png` and `markdown-overflow.png` in the work
  record's screenshot directory: consistent shared spacing, readable tables,
  full table height, and local code/table overflow. The opener E2E intercepts
  native transport; it does not exercise native permission matching or launch
  an external application.

## Coordinator disposition

Accepted after repair. The user authorized the finding's fix and commit.
Inspected the repair: the native opener receives `event.currentTarget.href`,
and the existing opener test checks canonical lowercase and mixed-case URLs.
Removing normalization makes that test fail; restoring it passes. Final
`make check-ui`, `make e2e` and the full screenshot run passed. Rust and Haiku
smoke results remain applicable; the repair changes only UI code and its test.
No findings remain unresolved. Screenshot manifests and gallery were checked:
all indexed PNGs exist, including all 46 test starts and passing ends in the
final capture. See the work record for evidence.
