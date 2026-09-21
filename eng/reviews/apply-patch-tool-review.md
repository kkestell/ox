# Review: `apply_patch` tool

Date: 2026-09-20 Reviewer: kreview (general)

## Scope

The new patch tool as it stands in the working tree on branch `rust`:

- `src/tools/patch.rs` (new, 611 lines)
- `src/tools/patch-guide.txt` (new, shipped as the tool description)
- `src/tools.rs` (schema, title, dispatch, test workspace fixture)
- `src/acp/prompt.rs` (cancellation check before tool execution, integration
  test)
- `AGENTS.md`, `eng/ox-apply-patch-tool.md` (documentation updates)

Measured against `eng/ox-apply-patch-tool.md` as the owning contract.

## Topics selected

`correctness`, `security`, `error-handling`, `testing`, `documentation`, and
`readability`. The change is a synchronous parser plus filesystem writer driven
by model-chosen input, so path confinement and exact-match semantics carry the
risk. `unsafe`, `concurrency`, and `dependencies` were checked and found to have
nothing to report: no `unsafe`, no new crates, and the tool runs inside the
existing single prompt run.

## Coverage

Read every line of the new module and the full design document, traced
`tools::execute` through `PromptRun::execute`, and ran the checks listed at the
end. Chunk matching, path resolution, and duplicate detection were exercised
with throwaway probes beyond the committed tests. Not covered: Windows path
behavior (the project targets darwin) and live model requests.

## Findings

### 1. Deleting or moving a symbolic link acts on its target — `src/tools/patch.rs:307`

`resolve` canonicalizes every existing component, so a symlink inside the
workspace is replaced by the file it points at before `preflight` ever sees it.
`fs::metadata` then reports a regular file and the operation is accepted.

Confirmed by probe: with `real` holding contents and `link -> real`, the patch

```text
*** Begin Patch
*** Delete File: link
*** End Patch
```

returns `Applied patch.\nD link`, removes `real`, and leaves `link` dangling.
`*** Update File: link` + `*** Move to: moved` behaves the same way: `real` is
renamed to `moved` and `link` is left dangling.

This contradicts the design's table in section 5, which requires the source to
be "an existing regular file" for Delete and Move. The consequence is a file the
model did not name being removed, and the file it did name surviving. Symlinks
pointing within a workspace are ordinary in real repositories
(`node_modules/.bin`, vendored toolchains).

Remedy: have `resolve` report whether the final component was itself a symlink,
and reject Delete and Move sources in that case. The escape check already
handles links that point outward, so this only needs to reject the in-workspace
case rather than add a second resolution strategy.

### 2. One CRLF line converts the whole file to CRLF — `src/tools/patch.rs:131`

`update` splits into lines and rejoins with a single ending, so a mixed file is
always normalized. The ending is chosen with `source.contains("\r\n")`, which
picks CRLF whenever a single CRLF line is present. Probe:
`update("a\nb\r\nc\n", …)` with a chunk touching only `a` returns
`"A\r\nb\r\nc\r\n"` — an otherwise-LF file converted wholesale for a one-line
edit.

Normalizing is inherent to the line-split design and the design document rules
mixed endings out of scope, so the defect is the choice of style, not the
normalization. Taking it from the file's first line ending is the same amount of
code and keeps the common case on LF:

```rust
let ending = match source.split_once('\n') {
    Some((first, _)) if first.ends_with('\r') => "\r\n",
    _ => "\n",
};
```

### 3. An empty `Move to:` destination reports the wrong reason — `src/tools/patch.rs:192`

`Patch::parse` rejects an empty operation-header path at line 118, but a
`Move to:` destination skips that check. An empty destination reaches `resolve`,
whose component loop finds nothing, and the model is told:

```text
preflight: f: destination : path names the workspace root
```

The patch named no destination at all. Apply the same `path.is_empty()` check to
the parsed destination so the error names the real problem at parse time.

### 4. `AGENTS.md` does not list `patch-guide.txt` — `AGENTS.md:9`

The source map gained `src/tools/patch.rs` but not `src/tools/patch-guide.txt`,
which is a shipped artifact: it is `include_str!`'d as the whole `apply_patch`
tool description in `src/tools.rs`, so editing it changes every model request.
`eng/ox-apply-patch-tool.md` section 6's module table omits it as well, and
section 2 still shows only the one-sentence description. Given that `AGENTS.md`
opens with "THIS DOCUMENT MUST BE KEPT UP TO DATE", add the line.

### 5. The scenario-string integration test is hard to follow — `src/acp/prompt.rs:530`

`patch_execution_cancellation_saving_and_replay` loops over five scenario
strings and branches on them at eight points, including two early
`continue`/`if` blocks that change which assertions run. Reading what "cancel
after" actually asserts requires tracking the string through the whole 120-line
body.

The scenarios divide cleanly: "invalid completion" shares almost nothing with
the rest, and "update failure" only changes how the response is checked.
Splitting out "invalid completion" and keeping the remaining loop over the two
cancellation points plus the complete case would make each assertion's
precondition local, without reducing coverage.

### 6. The two adjacent cancellation checks need a reason — `src/acp/prompt.rs:278`

```rust
if self.cancellation.is_cancelled() { … }
(self.send_update)(convert::in_progress_tool_call_update(&call.call_id))…;
if self.cancellation.is_cancelled() { … }
```

The second check is the new one and looks redundant. It is not: `tools::execute`
is synchronous for `apply_patch` and completes on its first poll, so the
`biased` `tokio::select!` below can never choose the cancellation branch, and a
cancellation arriving during `send_update` would otherwise let the patch write
files. That is exactly the "cancel before" scenario in the test. The doc comment
above `execute` describes outcome ordering but not this; one sentence naming the
reason would stop a later reader from removing the check.

## Unresolved suspicions

None. The two behaviors I could not settle by reading — symlink handling and
line-ending selection — were settled by probe and appear above as findings 1
and 2.

## Behaviors checked and found correct

- Chunk matching is exact, case-sensitive, forward-only, and anchors are
  literal; context lines keep their relative position when interleaved with `+`
  and `-` lines.
- The cursor advances past inserted lines, so a later chunk cannot match text an
  earlier chunk inserted.
- Final-newline and empty-result handling: deleting every line yields an empty
  file, and a source without a trailing newline stays without one.
- Path confinement. Absolute paths, `..`, workspace escapes through symlinks,
  dangling symlinks, links to the workspace root, and aliased spellings of the
  same target are all rejected at preflight. `starts_with` is component-wise, so
  a sibling directory with the workspace name as a prefix cannot match. Root is
  canonicalized before any comparison.
- Duplicate-target detection works across spellings because parents are
  canonicalized even when the leaf does not exist.
- `b' '`/`b'-'`/`b'+'` prefixes are ASCII, so `body[1..]` is always on a char
  boundary; non-ASCII leading bytes end the chunk body rather than panicking.
- Preflight computes all updates before the first write, and every rejection
  path leaves the workspace untouched.
- Error phases match section 6: `arguments:`, `parse: line N:`, `preflight:`,
  `apply:`. Preflight errors carry the operation's path; chunk errors carry the
  chunk number.
- Application failure reports completed, failed, and unattempted operations.
- A path component that is a regular file is rejected at preflight with
  `Not a directory (os error 20)` rather than failing mid-apply.
- Acceptance tests in section 7 are all present, including edited move,
  move-only byte preservation, no-op update, CRLF, and replay of the saved
  transcript.

## Checks run

- `cargo test` — 51 passed, 0 failed.
- `cargo clippy --all-targets` — clean.
- Six throwaway probes in `src/tools/patch.rs` covering symlink delete and move,
  mixed line endings, `ENOTDIR` components, empty and trailing-slash paths, and
  append-then-anchor chunk ordering. Removed after the run; the working tree is
  as it was.

## Verdict

Sound. The matching rules and path confinement match the design, the module
stays inside the "just enough Rust" budget, and the tests cover the contract
rather than the internals. Finding 1 is a real data-loss path and should be
fixed before this lands. Findings 2 and 3 are small corrections with no new
machinery. Findings 4 through 6 are documentation and readability.

## Resolution

All six findings were addressed in the commit that follows this document. Both
open questions were decided by the author: reject symbolic links as targets for
every operation, and split the integration test.

1. **Fixed.** `resolve` tracks whether the final path component was a symbolic
   link and rejects it with `path is a symbolic link`, for Add, Update, Delete,
   and Move alike. `eng/ox-apply-patch-tool.md` section 5 and
   `src/tools/patch-guide.txt` carry the rule. The symlink test gained an
   in-workspace link and asserts its target survives all three operations.
2. **Fixed.** The output line ending now comes from the file's first line
   ending. A mixed file is still normalized to one style, which the design
   document states explicitly; two test cases pin both directions.
3. **Fixed.** An empty `Move to:` destination is a parse error naming the
   destination, alongside the existing empty-path check.
4. **Fixed.** `patch-guide.txt` is listed in the `AGENTS.md` source map and the
   design document's module table, and section 2 says the shipped description is
   the whole guide.
5. **Fixed.** The scenario loop became five tests over three shared fixtures
   (`patch_harness`, `patch_result`, `assert_replays_patch`), with no branching
   inside any test and no change in coverage.
6. **Fixed.** A three-line comment at the second cancellation check explains
   that a tool changing files runs to completion once started, so the `select!`
   below cannot observe the cancellation.

Verified after the fixes: `cargo test` 55 passed, `cargo clippy --all-targets`
clean, `cargo fmt` clean, and one live `ox run` against a scratch workspace and
`OX_DATA_DIR`, which edited an existing file and created a new one through the
model.
