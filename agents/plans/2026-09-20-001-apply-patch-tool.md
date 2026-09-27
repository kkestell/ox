# Ox apply_patch tool

Status: implemented.

## 1. Goal

Add one local `apply_patch` function tool that can create, edit, delete, and
move text files in a session's workspace.

The tool uses the familiar `*** Begin Patch` format because models already know
how to produce it. Compatibility with Codex or an OpenAI SDK is not a product
goal. Ox owns a small format with deliberately simple behavior; upstream edge
cases do not become requirements merely because another implementation supports
them.

The implementation should be a concrete Rust parser and filesystem function. Do
not add an SDK, shell subprocess, provider-specific backend, general tool
registry, or configuration surface.

## 2. Tool interface

Register this function alongside the existing concrete tools:

```json
{
  "type": "function",
  "function": {
    "name": "apply_patch",
    "description": "Apply a text patch to files in the session workspace. Paths are relative to the workspace. Supports Add File, Update File, Delete File, and Move to.",
    "parameters": {
      "type": "object",
      "properties": {
        "patch": {
          "type": "string",
          "description": "A patch beginning with *** Begin Patch and ending with *** End Patch."
        }
      },
      "required": ["patch"],
      "additionalProperties": false
    }
  }
}
```

The description in `src/tools.rs` opens with the sentence above and continues
with the example and rules in section 3. The tool uses the session's workspace
path; it does not accept a separate working directory or execute shell syntax.

Malformed arguments and patch errors produce `ToolOutcome::Failed`. Tool
execution begins only after Ox validates the model completion and its assistant
message.

## 3. Patch format

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

A patch contains zero or more file operations between the begin and end markers.
The markers and operation headers must appear exactly as shown at the start of a
line.

- `*** Add File: path` is followed by zero or more lines beginning with `+`.
  Removing that prefix gives the new file's contents.
- `*** Delete File: path` has no body.
- `*** Update File: path` is followed by one or more chunks. A move-only update
  may omit the chunks.
- `*** Move to: path` may appear immediately after an Update header. The update
  is written at the destination and the source is removed.
- A chunk starts with `@@` or `@@ anchor`. An anchor is a literal source line
  used to begin the search for the chunk; it is not a line number or regular
  expression.
- Within a chunk, a leading space is unchanged context, `-` removes a line, and
  `+` inserts a line. The prefix is syntax and is not part of the file content.
- Another operation header, chunk header, or the final marker ends the current
  body. Text outside the patch and unrecognized lines are errors.

Paths and payloads are literal. There is no quoting, escaping, heredoc support,
or standard unified-diff syntax inside the patch string. JSON escaping is
handled once by argument decoding and is not part of the patch language.

An empty patch succeeds without changing files. An Add with no body creates an
empty file; a nonempty Add ends with a newline. A source line that resembles a
marker remains expressible because it has a context, removal, or addition
prefix.

Parser errors should report a line number and a useful reason. Do not build a
formal error taxonomy.

## 4. Applying updates

Treat updated files as ordinary UTF-8 text. Support LF and CRLF files, and use
LF for new files. Take an updated file's line-ending style from its first line
ending and write the whole file in that style. Mixed line endings and
byte-for-byte compatibility with other patch tools are not goals.

Apply chunks in their patch order:

1. Build the old sequence from context and removed lines.
2. Starting at the beginning of the file for the first chunk and after the
   preceding match thereafter, find the first exact occurrence of that sequence.
   If the chunk has an anchor, first find that exact line and begin the chunk
   search after it.
3. Replace the matched sequence with the context and inserted lines.
4. Continue searching after the applied chunk.

Matching is exact and case-sensitive. Do not trim whitespace, normalize Unicode,
calculate edit distance, or add other fuzzy matching. A mismatch is a `Failed`
outcome; the model can inspect it and try a more accurate patch.

A chunk containing only additions inserts after its anchor or preceding chunk.
With neither, it appends to the file. Context lines are included in both the old
and new sequences, so their contents remain unchanged.

Preserve whether an updated file ended with a newline; deleting all lines
produces an empty file. A move with no chunks preserves the source bytes
exactly. An update that produces the same contents succeeds as unchanged.

These rules are the whole matching contract. Behavior not described here should
be chosen for implementation simplicity rather than inferred from Codex or an
SDK.

## 5. Filesystem behavior

All paths are relative to the session workspace. Reject absolute paths, parent
traversal, the workspace root itself, and any path that resolves outside the
workspace, including through a symbolic link. Reject a path whose final
component is a symbolic link, so every operation names the file it changes. Do
not expand tildes, variables, globs, URLs, or quoted names.

Use these ordinary operation rules:

| Operation | Requirement                                                       | Effect                                                                                  |
| --------- | ----------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| Add       | Target does not exist                                             | Create parents as needed, then create the file.                                         |
| Update    | Source is an existing regular text file                           | Replace its contents.                                                                   |
| Delete    | Source is an existing regular file                                | Remove it.                                                                              |
| Move      | Source is an existing regular file and destination does not exist | Create destination parents as needed, then move it, applying chunks first when present. |

Reject a patch that names the same path for more than one operation, including
move destinations. This keeps preparation and outcome reporting unambiguous.

Parse the whole patch, validate its paths, read required sources, and calculate
updates before the first filesystem change. Apply the resulting operations in
patch order with ordinary filesystem APIs.

Filesystem application is not transactional. Stop on the first write, remove, or
rename error. Earlier operations may remain applied and no rollback is
attempted. The `Failed` outcome must identify completed operations, the failed
operation, and operations not attempted. Ox assumes the workspace is not being
modified concurrently; cross-process race hardening is outside this version.

## 6. Results and integration

Use the existing `ToolOutcome` variants. A `Completed` outcome is concise and
ordered like the patch:

```text
Applied patch.
Added notes.txt
Modified src/greeting.rs
Moved old-name.txt -> new-name.txt
Deleted obsolete.txt
```

Use `Unchanged path` for an update that does not alter the file. A `Failed`
outcome includes the phase—arguments, parse, prepare, or apply—and the relevant
path or chunk when known. It should provide enough context for the model to
correct the patch without dumping unrelated file contents.

Build the tool call title from the path it changes, or from the number of paths
when it changes several. A patch that does not parse names no paths and uses the
tool call title `Apply patch`. The tool kind is edit. The surrounding prompt run
continues to own ACP updates, cancellation, transcript saving, and replay. In
particular, filesystem execution is synchronous once started. Its observed
outcome enters the `UncommittedAssistantBatch` before the finished ACP update is
sent, and the complete batch is then appended to the transcript as specified by
[the architecture design](ox-architecture-design.md#15-tool-execution-and-persistence).
Do not duplicate that machinery inside the patch implementation.

Suggested module changes:

| Module               | Change                                                                           |
| -------------------- | -------------------------------------------------------------------------------- |
| `src/tools.rs`       | Add the model guidance, schema, tool call title, argument parsing, and dispatch. |
| `src/tools/patch.rs` | Parse and apply patches with concrete owned types.                               |
| `src/acp/prompt.rs`  | Pass the session workspace to tool execution.                                    |

An owned `Patch` containing a `Vec<FileOperation>` is sufficient. Keep parsing
and text transformation separate from filesystem I/O so the important behavior
can be tested without elaborate mocks. Do not add traits or abstractions for
hypothetical patch backends.

## 7. Acceptance tests

Use temporary workspaces and deterministic tool calls. No model request is
required.

- Parse and apply the example above.
- Cover empty patches and files, blank added lines, multiple chunks, append,
  deletion of all content, move-only, edited move, and unchanged update.
- Reject malformed markers, invalid body prefixes, missing context, and text
  outside the patch without writing files.
- Confirm exact forward matching, literal anchors, repeated-context selection,
  and failure on whitespace-only differences.
- Cover ordinary LF and CRLF files and preservation of the final newline.
- Reject workspace escapes, symlink escapes, symbolic links named as targets,
  missing sources, existing Add or Move destinations, directories, and duplicate
  targets.
- Verify that a preparation failure changes nothing and an application failure
  reports any earlier completed operations without attempting later ones.
- Verify the schema, tool call title, prompt run, cancellation before execution,
  transcript saving, and replay through focused integration tests.

Model evaluations may later measure patch validity and retry rates. They are not
part of the deterministic implementation contract.
