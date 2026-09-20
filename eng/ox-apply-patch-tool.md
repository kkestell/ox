# Ox apply_patch tool

Status: proposed implementation specification. No implementation is implied.

## 1. Purpose and decisions

Expose one local `apply_patch` function tool that creates, edits, deletes, and
moves text files beneath a session's workspace. The model supplies a complete
patch; Ox parses it, computes the changes, applies them, and returns a result
through the existing tool loop.

Use ordinary JSON function calling over the current OpenRouter Chat Completions
adapter. The arguments are `{ "patch": "..." }`. The patch string uses the
Codex multi-file format, including `*** Begin Patch` and `*** End Patch`.
Neither the Responses API nor a hosted patch service is required. This is
transport compatibility with Ox's existing tools, not a claim that every model
will produce equally good patches.

The implementation is a small concrete Rust parser and filesystem executor.
Keep parsing and text transformation independent of filesystem I/O. Do not add
an agent SDK, shell subprocess, provider-specific backend, or tool registry.

The normative behavior is specified below. Pinned upstream implementations
explain its origin; upstream changes do not silently change this contract.

## 2. References and compatibility

The following implementations and tests were inspected for this specification:

- OpenAI Python Agents SDK, commit
  `f23da767df176df1733af277061c072b235e6c95`:
  [apply_diff.py](https://github.com/openai/openai-agents-python/blob/f23da767df176df1733af277061c072b235e6c95/src/agents/apply_diff.py),
  [tests](https://github.com/openai/openai-agents-python/blob/f23da767df176df1733af277061c072b235e6c95/tests/test_apply_diff.py),
  [filesystem editor example](https://github.com/openai/openai-agents-python/blob/f23da767df176df1733af277061c072b235e6c95/examples/tools/apply_patch.py).
- OpenAI TypeScript Agents SDK, commit
  `a967d2729ddabb846e3112e2878df344574bb6a7`:
  [applyDiff.ts](https://github.com/openai/openai-agents-js/blob/a967d2729ddabb846e3112e2878df344574bb6a7/packages/agents-core/src/utils/applyDiff.ts),
  [CRLF workflow test](https://github.com/openai/openai-agents-js/blob/a967d2729ddabb846e3112e2878df344574bb6a7/packages/agents-core/test/utils/applyDiff.crlf-workflow.test.ts).
- OpenAI Codex, commit `5c5308fc9a9ee789049d646ef11e5400384b9c6f`:
  [parser](https://github.com/openai/codex/blob/5c5308fc9a9ee789049d646ef11e5400384b9c6f/codex-rs/apply-patch/src/parser.rs),
  [parser state machine](https://github.com/openai/codex/blob/5c5308fc9a9ee789049d646ef11e5400384b9c6f/codex-rs/apply-patch/src/streaming_parser.rs),
  [file updates](https://github.com/openai/codex/blob/5c5308fc9a9ee789049d646ef11e5400384b9c6f/codex-rs/apply-patch/src/file_update.rs),
  [matching](https://github.com/openai/codex/blob/5c5308fc9a9ee789049d646ef11e5400384b9c6f/codex-rs/apply-patch/src/seek_sequence.rs),
  [line endings](https://github.com/openai/codex/blob/5c5308fc9a9ee789049d646ef11e5400384b9c6f/codex-rs/apply-patch/src/text_file.rs).

The SDK helpers operate on a single file's headerless diff and leave filesystem
operations to the application. They are useful references, but are not identical
to the Codex tool. For example, the SDKs support stacked anchors, tolerate an
unmatched single anchor, and differ from each other on newline handling. The
Python SDK uses CRLF when any CRLF occurs in the source; the TypeScript SDK uses
CRLF only when all source LF characters belong to CRLF pairs. Codex additionally
supports preserving each source line's ending.

Ox follows Codex's full patch envelope, forward context search, append behavior,
and `PreserveLineEndings` text behavior. Ox deliberately narrows path handling,
rejects duplicate targets and accidental overwrites, validates the entire patch
before writing, and rejects overlapping replacements. It does not implement
Codex's shell/heredoc recovery, environment IDs, remote filesystems, streaming
patch execution, or the SDKs' stacked-anchor dialect.

If code or substantial portions are ported, retain the applicable upstream
notices: the inspected Python SDK is MIT licensed and Codex is Apache-2.0
licensed. Do not import the surrounding SDK/runtime architecture merely to
reuse the matching algorithm.

## 3. Tool interface and model instructions

Register this function alongside the existing concrete tools:

```json
{
  "type": "function",
  "function": {
    "name": "apply_patch",
    "description": "Apply a complete text patch to files in the session workspace. Paths are relative to the workspace. Supports Add File, Update File, Delete File, and Move to. Supply the patch as text, without Markdown fences or a shell command.",
    "parameters": {
      "type": "object",
      "properties": {
        "patch": {
          "type": "string",
          "description": "A complete patch beginning with *** Begin Patch and ending with *** End Patch."
        }
      },
      "required": ["patch"],
      "additionalProperties": false
    }
  }
}
```

The function description sent to the model must also include the format rules
and worked example in section 4, either directly or as adjacent tool guidance.
Do not depend on a model already knowing the format. Explain that anchors are
literal source lines, pure additions append, all targets must be distinct, and
a failed call can have partial effects. JSON escaping is only transport: decode
the string once, then parse its contents without shell expansion or unescaping
it a second time.

Reject malformed JSON, duplicate fields, missing or non-string `patch`, and
unknown fields as ordinary failed tool results. Preserve the original argument
string in the transcript as today. Execute only after the model completion has been accepted;
never execute partially streamed arguments.

There is no `cwd`, approval flag, arbitrary command, or dry-run parameter.
Execution uses the session's stored `cwd`. Enabling this tool makes workspace
editing available to the agent without a separate per-call approval dialogue;
this specification introduces no general permission framework.

Ox currently has no file-reading tool. For initial use, source text must be
available in the conversation. ACP resource links alone are not fetched.
Adding file reading or shell execution is separate work; this tool must not
pretend that the model has inspected a file merely because it names its path.

## 4. Patch syntax

```text
*** Begin Patch
*** Add File: notes.txt
+New notes.
*** Update File: src/greeting.rs
@@ fn greeting() -> &'static str {
-    "Hello"
+    "Hello, world"
 }
*** Update File: old-name.txt
*** Move to: new-name.txt
@@
-Old text
+New text
*** Delete File: obsolete.txt
*** End Patch
```

The following grammar is descriptive; the rules after it resolve boundary cases.
`line` excludes its terminating newline. A path is literal, not quoted or escaped.

```text
patch   = begin, operation*, end
begin   = "*** Begin Patch"
end     = "*** End Patch"
add     = "*** Add File: " path, ("+" line)*
delete  = "*** Delete File: " path
update  = "*** Update File: " path, move?, chunk*
move    = "*** Move to: " path
chunk   = anchor?, body+, eof?
anchor  = "@@" | "@@ " line
body    = " " line | "+" line | "-" line | empty-line
eof     = "*** End of File"
operation = add | delete | update
```

- Patch lines may use LF or CRLF. Strip the CR belonging to a CRLF delimiter;
  reject a remaining bare CR in patch text. The final marker may lack a newline.
- Ignore blank lines before the begin marker and after the end marker. Require
  exact marker spelling and column-zero placement inside the patch. Do not trim
  source payloads or paths. Whitespace-only surrounding lines count as blank.
- Reject prose, Markdown fences, heredocs, shell commands, standard unified-diff
  headers, environment IDs, and any unconsumed text. Consume the whole patch.
- An empty patch is successful with no filesystem effects. An Add with no body
  creates an empty file. A Delete has no body.
- Every Add body line begins with `+`, including blank file lines. Each payload
  receives an LF terminator. `+hello` creates `hello\n`; `+` creates `\n`.
- An Update contains at least one nonempty chunk, unless it is a move-only
  operation. A chunk containing context but no changes is valid. A bare anchor
  with no body is invalid. Move-only operations preserve bytes exactly.
- The first update chunk may omit `@@`. Subsequent chunks require it. Exactly
  one optional anchor introduces each chunk; consecutive/stacked anchors fail.
  An anchor with text contains a literal source line, not line numbers or a
  regular expression. A unified-diff range such as `@@ -1,2 +1,2 @@` is unsupported.
- In an Update, a leading space means unchanged context, `-` means removed text,
  and `+` means inserted text. Remove exactly that one prefix character.
  An unprefixed empty line means empty context, matching Codex's leniency.
- `*** End of File` terminates the current chunk and anchors its old text at EOF.
  It must follow a body. After it, allow empty separator lines, another `@@`
  chunk, another file operation, or the final marker; no other body text.
- `*** Move to:` appears at most once, immediately after an Update header. No
  other operation accepts it. An Add/Delete/Update header starts a new operation.
- A source line resembling a control marker remains expressible through its
  body prefix, for example `+*** End Patch` or ` *** End Patch`.

Syntax failures include the one-based patch line number and a useful reason.
They cause no filesystem changes.

## 5. Text transformation

### Lines and chunks

Read updated files as UTF-8. Reject invalid UTF-8 and NUL bytes in file contents
or patch text. Treat LF, CRLF, and bare CR in existing files as line endings;
retain the ending of each source line. A final newline belongs to its preceding
line and does not introduce a phantom empty source line. An empty file has zero
lines. Preserve a UTF-8 BOM as ordinary content; it is not silently stripped.

For each chunk, form `old_lines` from context and removed lines, and `new_lines`
from context and inserted lines. Record which entries were explicit context so
that a tolerant match does not rewrite those lines' indentation or punctuation.
All chunks match against the original file snapshot, not a progressively edited
buffer. The initial search cursor is zero.

### Matching

For an anchor, search for that one line at or after the cursor. Failure is an
error, even if the body would match elsewhere. Success advances the cursor to
the line immediately after the anchor.

For a nonempty `old_lines`, search at or after the cursor. Test all candidate
positions in each pass before using the next, more permissive pass:

1. Exact equality.
2. Equality after trimming trailing Unicode whitespace from each line.
3. Equality after trimming leading and trailing Unicode whitespace.
4. Equality after trimming both ends and normalizing the characters below.

Use Rust's `str::trim` and `str::trim_end` whitespace definitions. The final pass
maps these characters on both sides only for comparison:

| Characters | Comparison replacement |
| --- | --- |
| U+2010 through U+2015, U+2212 | ASCII hyphen `-` |
| U+2018 through U+201B | ASCII single quote |
| U+201C through U+201F | ASCII double quote |
| U+00A0, U+2002 through U+200A, U+202F, U+205F, U+3000 | ASCII space |

The first match in the first successful pass wins. Repeated context is not an
ambiguity error: include more context or a literal anchor to select a later
occurrence. A later exact match wins over an earlier whitespace-only match.
Anchors use these same passes. Matching is case sensitive, with no regex,
edit-distance search, or other Unicode normalization.

For an EOF chunk, the only candidate is the suffix with the same line count
as `old_lines`, provided it is not before the cursor. Apply the same comparison
passes there; do not fall back to an earlier occurrence. This follows the
inspected Codex implementation, whose search behavior is stricter than its
comment about an EOF fallback.

If matching fails and `old_lines` ends in an empty string, retry once without
that last entry. Also remove the final entry of `new_lines` if it is empty.
This is Codex's trailing-empty-line tolerance. Preserve only context mappings
that remain within the shortened arrays. If the retry has an empty pattern,
match at the current cursor. Otherwise use the same search rules. Exhausting
these attempts is a context-mismatch failure.

After a matched nonempty body, advance the cursor to the end of the matched
old range. If the original `old_lines` is empty, append its new lines to EOF,
regardless of whether the chunk contains an EOF marker; do not advance the
search cursor. Any supplied anchor must still match first. Inserting elsewhere
requires a context line. Multiple pure append chunks retain patch order.

Compute replacements only for changed portions between explicit context lines.
Keep matched context from the original source verbatim. Sort replacements by
source position with stable ordering for insertions at the same position.
Reject overlapping removed ranges or insertions strictly inside a removed
range. Insertions at a removed range's boundary are permitted in patch order.
For replacements sharing a start position, emit their new segments in patch
order, then consume the one nonempty old range, if any, exactly once.
Do not silently discard or reorder conflicting edits.

### Output bytes

Use the first line ending in the source as the preferred ending for inserted
lines; use LF if the source contains none. Preserve existing line endings on
unchanged/context lines, including mixed endings. Changed lines receive the
preferred ending. Every resulting line has an ending, including a previously
unterminated final line. Removing all lines produces an empty file.

This follows Codex's `PreserveLineEndings` behavior: a text Update may add a
final newline. This format has no `\ No newline at end of file` feature.
A move with no chunks is a byte-preserving filesystem rename, so it does not
add a newline. An update whose computed bytes equal its original bytes is a
successful no-op and must not rewrite the file.

## 6. Paths and filesystem operations

Resolve paths under the canonical form of the session's stored workspace
directory. Resolve the root at execution time and fail if it is absent or not
a directory. Do not change the process working directory or change how sessions
are keyed by their stored `cwd`.

Paths must be nonempty relative paths with normal components. Permit spaces
within names. Reject absolute paths, `.` and `..` components, repeated or
trailing separators, NUL/control characters, and leading/trailing whitespace.
Use `/` as the patch separator and reject backslashes. Do not expand `~`, `$`,
globs, URLs, or quotes. Reject the workspace root as a target.

Walk existing components beneath the canonical root with `symlink_metadata`.
Reject symbolic links in every component, including a dangling target link;
reject non-directory ancestors and non-regular existing targets. A symlink in
the supplied workspace root is resolved by root canonicalization, not traversed
as a patch component. This is pathname validation in a trusted local workspace,
not an OS sandbox against another process racing directory changes.

Every touched path may occur in only one operation in a call. A move reserves
both its source and destination; they must differ. Reject duplicate or aliased
existing targets and ancestor/descendant target conflicts during preflight.
On filesystems with case-insensitive names, use filesystem identity for existing
targets and recheck destination existence at application time. Do not implement
a Unicode/case-folding approximation. Case-only renames are outside this version.

| Operation | Required state | Effect |
| --- | --- | --- |
| Add | Target absent | Create parents as necessary; create the file exclusively. Never overwrite. |
| Update | Regular UTF-8 text file exists | Compute and write the replacement; preserve ordinary permission bits. |
| Delete | Regular UTF-8 text file exists | Unlink that file. Missing targets fail; directories are never recursively deleted. |
| Move, no chunks | Regular UTF-8 text source exists; destination absent | Create parents as necessary and rename the source. |
| Move with chunks | Same as move-only | Write the updated source, then rename it to the destination. |

Require valid text even for move/delete to keep the tool's scope explicit.
New files use ordinary process creation permissions. Do not add a mode-changing
syntax, ownership changes, or promises to preserve ACLs/extended attributes.
Do not remove empty parent directories after deletes or moves. Cross-filesystem
rename errors fail without a copy/delete fallback.

In-place updates can affect other hard links to the same inode. Reject files
with multiple hard links for Update, Delete, and Move on the initial supported
Unix platform; use filesystem metadata, not path-string comparison, for this
check and for existing-target aliases. Porting to another platform requires
an equivalent check before enabling this tool there.

## 7. Preflight, writes, and partial failure

Serialize patch execution across all sessions in this Ox process with one
concrete shared filesystem-edit mutex. Hold it from preflight through the end
of filesystem execution. A per-session admission guard alone is insufficient
because different sessions can edit the same workspace. Do not hold the SQLite
mutex during file I/O, or the filesystem mutex during an asynchronous wait.

Before the first mutation, parse the complete patch, validate every target and
operation, read all needed source snapshots, and compute all output text in
memory. Failure at this stage returns `Failed` with no changes, including no
new parent directories. No staging directory or persistent backup is needed.

Apply operations in patch order. Immediately before each operation, repeat
path/type/existence checks and compare its source bytes with the preflight
snapshot. Changed source bytes fail that operation. This detects ordinary
editor changes between preflight and write; it is not a compare-and-swap against
arbitrary concurrent external writers. The process mutex excludes other Ox
patches, not editors or other processes.

In particular, existence checks followed by ordinary `rename` cannot guarantee
no overwrite if another process creates the destination in between. This
version assumes no external writer races the operation itself; preflight and
rechecks do not establish exclusive filesystem access.

Use ordinary filesystem operations: exclusive creation for Add, in-place writes
for Update, `remove_file` for Delete, and `rename` for Move. Flush userspace
buffers/finish writes before reporting success.

Stop at the first application error. Earlier effects remain; later operations
are not attempted. A write can truncate or partially write its target before
failing. A failed edited move can leave the updated source at its old path.
New parent directories or an incompletely created file can remain. Do not
automatically roll back, delete possibly useful output, or retry the patch.

Report observed effects and uncertainty accurately. Do not label a call
atomic merely because parsing and matching were preflighted. Successful earlier
operations, the failed operation's possible effects, and unattempted operations
must all be distinguishable in its result.

## 8. Results and client presentation

Use existing `ToolOutcome` variants. Success means every requested operation
completed; it does not mean tests passed. Return `Completed` with a concise
summary in patch order:

```text
Applied patch.
A notes.txt
M src/greeting.rs
R old-name.txt -> new-name.txt (edited)
D obsolete.txt
```

Use `R old -> new` for a move-only operation and `N path` for a no-op update.
An empty patch returns `Completed("No changes requested.")`. A context-only
update that adds a final newline is `M`, not `N`.

Failures are ordinary `Failed` results that the model can read and correct.
Include the phase (arguments, parse, preflight, or apply), operation/path when
known, and reason. Context errors include the chunk number and expected lines;
do not dump unrelated file contents. For example:

```text
Patch failed during preflight: src/greeting.rs, chunk 1: expected lines not found.
No files changed.
```

```text
Patch failed during apply: new-name.txt: rename failed: permission denied.
Completed:
A notes.txt
M old-name.txt (edited before rename failed; source remains at old-name.txt)
Not attempted:
D obsolete.txt
No rollback was performed.
```

If a write failed without a fully observed result, identify the path as possibly
partially written. If parent directories were created, include that fact. Exact
OS error wording is not a stable API; phase, affected paths, completion status,
and uncertainty are. Do not turn an I/O error into a successful result.

Use the stable ACP title `Apply patch`. Pending/in-progress updates and raw
arguments continue through `acp/convert.rs`. The terminal update carries the
stored text result. Replay uses that same result without reading current files
or rerunning the patch. Rich ACP diff content is not required in this version.

## 9. Cancellation and shutdown

The filesystem portion executes synchronously and is not interruptible once
started. Check cancellation before starting it, including after obtaining the
edit mutex. Once started, finish the current call, observe its real result,
persist it, and only then honor cancellation before another call/model request.
Keep the session operation guard until settlement finishes. Connection shutdown
waits for this work through the existing operation registry.

The existing `tokio::select!` that drops a running tool future must not detach
or cancel a file mutation. A dropped `spawn_blocking` handle does not stop its
worker. Initially, use a short synchronous executor directly; do not add a
background task. If measured workloads require offloading later, the owner
must await completion even after cancellation.

A call skipped before execution receives `Cancelled` with an explicit
not-started message. A call that completes after a cancellation request keeps
its actual `Completed` or `Failed` outcome; the enclosing prompt can still end
as cancelled. Cancellation is not rollback.

## 10. Execution and transcript persistence

Apply patch uses the prompt's existing `PendingBatch` and closed-batch storage
path. Its execution and persistence guarantees are:

1. The model's complete assistant message is accepted before any patch begins.
2. Cancellation is checked immediately before synchronous filesystem execution.
   Once execution begins, the prompt owns it through completion and captures its
   observed `ToolOutcome` before responding to cancellation.
3. The outcome enters the in-memory pending batch before its terminal client
   update, any later tool call, or another model request.
4. Once every call in the assistant message has a terminal outcome, the message
   and its results are appended to the transcript in one transaction.
5. Accepted in-memory history advances only after that transaction succeeds.
   Another model request and a successful prompt response follow the same
   boundary.
6. A delivery failure stops later calls in the accepted batch and settles their
   outcomes. A batch storage failure stops the prompt before another model
   request. The session operation guard remains held until settlement finishes.
7. Orderly connection shutdown waits for admitted prompt work and settlement to
   finish.

These guarantees are implemented by the existing prompt owner, in-memory
pending batch, closed-batch transaction, and session operation guard. Apply
patch adds its resource-specific execution behavior to those boundaries.

## 11. Implementation boundaries

| Module | Change |
| --- | --- |
| `src/tools.rs` | Add schema/guidance, argument validation, stable title, and dispatch with session workspace context. |
| `src/tools/patch.rs` | Own patch parsing, text matching, preflight, and filesystem execution using concrete types/functions. |
| `src/acp.rs` | Own/share one edit mutex; pass it into the prompt's tool context. |
| `src/acp/prompt.rs` | Supply stored `cwd`; keep synchronous patch execution owned through its observed outcome. |

Suggested internal shape: an owned `Patch` with `Vec<FileOperation>`, where
`FileOperation` is a closed Add/Delete/Update enum and Update carries an optional
destination plus its chunks. Preflight produces owned source/output snapshots.
Use existing `io::Error` conventions for ordinary boundary errors. A parser or
filesystem error from model input must not panic; broken internal state must
fail loudly. Do not add traits or configuration for hypothetical executors.

The edit mutex is held only through synchronous preflight and filesystem
execution, then released before the prompt stores the closed batch. No mutex
guard crosses an asynchronous wait. Only the prompt owner advances result slots.

Advertise the tool after its filesystem behavior, cancellation ownership, and
closed-batch integration are implemented together.

## 12. Acceptance tests

Use temporary workspaces, deterministic tool calls, and in-memory or temporary
SQLite stores. No paid model request is required for correctness. Port relevant
upstream examples with attribution and adapt expectations only where this
specification explicitly differs.

### Patch and text behavior

- Parse the worked multi-file example; reject malformed markers, trailing prose,
  fenced/heredoc input, duplicate fields, unknown JSON fields, stacked anchors,
  empty chunks, and misplaced moves/EOF markers without writes.
- Cover empty patches/files, Add blank lines, omitted first anchors, multiple
  chunks, pure append, contextual insertion, deletion of all lines, move-only,
  and edited moves. Include source lines that resemble control markers.
- Establish exact-before-tolerant matching, repeated-context selection, all
  normalization categories, required anchors, strict EOF suffix matching,
  trailing-empty-line retry, and overlap rejection. Preserve actual context
  bytes when the supplied context matches only tolerantly.
- Check LF, CRLF, bare-CR and mixed source endings; absent final newline; empty
  input; BOM preservation; and no-op updates. Verify output bytes, not just lines.
- Reject invalid UTF-8, NULs, and bare CR in patch text as specified.

### Filesystem boundary

- Resolve relative targets from the session workspace when process cwd differs.
- Reject escapes, absolute paths, invalid components, symlink ancestors/targets,
  hard links, directories, missing update/delete sources, duplicate/aliased
  targets, and existing Add/Move destinations. Test case aliases where supported.
- Verify parent creation, preserved ordinary permissions, byte-identical moves,
  no-op files not rewritten, and no recursive deletion.
- A later preflight mismatch must leave every file and directory unchanged.
- A deterministic later application failure must retain earlier effects, stop
  subsequent operations, and report partial state. Cover failure after an edited
  move's source write. Use a narrow test-only failpoint when OS permissions would
  make the failure unreliable; do not add a production filesystem abstraction.
- Two sessions editing the same files cannot interleave preflight and writes.
  A changed preflight source is rejected before that operation's mutation.

### Agent loop, storage, and replay

- Tool schema and guidance appear in the existing model request. Fragmented
  streamed arguments never execute before completion. A failed patch returns
  through the normal function-result loop and can be followed by a new call.
- Cancellation before execution makes no changes. Cancellation during execution
  retains its observed result and skips remaining calls. Shutdown waits for it.
- An observed outcome enters the in-memory pending batch before terminal
  delivery. Delivery failure still settles the accepted batch through storage.
- A successful batch commit stores the assistant message and exactly one result
  per call; replay and the next model request reconstruct that same batch.
- A failed batch commit prevents another model request and returns a storage
  error through the existing settlement path.

An optional model evaluation can later compare valid-call rate, patch success,
unintended edits, retries, and tokens across models. Keep that separate from
deterministic implementation tests and from any assertion of GPT-only support.
