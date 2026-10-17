# Review the entire Ox codebase

## Scope and coverage

Reviewed all tracked executable source and its tests, with README.md, AGENTS.md,
approved plans, and the preceding review as context. Three subagents covered the
server and persistence, terminal client and protocol, and tools and processes.
The primary reviewer covered OpenRouter, settings, credentials, Python scripts,
evaluations, and build and release wiring, then inspected the combined fixes.
Every finding was confirmed before fixing it. All fixes followed existing
requirements and needed no new user decision.

Gaps:

- Execution was on macOS. Linux-specific code and release wiring were read but
  not executed on Linux. The clipboard regression uses a fake macOS clipboard
  command and is skipped on other systems.
- No live provider, keyring mutation, third-party ACP server, Docker evaluation,
  or GitHub release was exercised. Provider tests use local HTTP fixtures.
- Ignored research scripts, generated binaries, cached repositories, and saved
  evaluation runs were excluded from the source review.

## Summary

| ID | Severity | Category       | Fix   | Title                                           |
| -- | -------- | -------------- | ----- | ----------------------------------------------- |
| F1 | Medium   | Correctness    | Fixed | Aliased patch updates silently discard edits    |
| F2 | Medium   | Security       | Fixed | Saving settings broadens file permissions       |
| F3 | Medium   | Concurrency    | Fixed | Cancelled stdin writes still change the process |
| F4 | Medium   | Error handling | Fixed | Special instruction files can block startup     |
| F5 | Medium   | Correctness    | Fixed | Clipboard command results are discarded         |
| F6 | Medium   | Correctness    | Fixed | Malformed tool arguments can execute commands   |
| F7 | Medium   | Error handling | Fixed | Headless answer output failures report success  |

## F1. Aliased patch updates silently discard edits

Medium · Correctness · Fixed · `internal/tools/patch.go:316`

Two in-place updates through different paths to the same file were prepared
against the original contents. Applying the second overwrote the first edit,
while the tool reported that both succeeded. Duplicate-target checks compared
only path strings, although updates through symlinks were supported.

### Evidence

A patch changed `one` to `ONE` through the original path and `two` to `TWO`
through its alias. The resulting file was `one\nTWO\n`. Regression tests cover
both symbolic and hard links and verify that rejection happens before any
operation writes.

### Fix

Compare the file identities of actual in-place updates during preparation and
reject repeated targets. Deleting distinct hard links remains supported and has
a regression test.

## F2. Saving settings broadens file permissions

Medium · Security · Fixed · `internal/settings/settings.go:187`

Saving a model selection or favorite replaced the settings file with a temporary
file created with mode 0666. Under the usual umask, a private settings file
became readable by other users. Settings can contain private server arguments
and configuration.

### Evidence

A temporary regression created settings with mode 0600, called SaveFavorites,
and observed mode 0644. Permanent tests cover modes 0600 and 0640 through both
SaveFavorites and Save.

### Fix

Retain the existing permission bits when creating the replacement and restore
them after umask filtering. Remove temporary files on every exit. New files keep
the previous creation behavior.

## F3. Cancelled stdin writes still change the process

Medium · Concurrency · Fixed · `internal/shellproc/background.go:280`

Write relied on an asynchronous cancellation callback without checking an
already-cancelled context. A small write could finish and close stdin before the
callback ran. Stopping that callback also did not wait for an invocation already
running, so it could install an expired deadline after cleanup and interfere
with the next write.

### Evidence

A cancelled write to a live cat process wrote its bytes and closed stdin. The
regression now verifies that cancelled data and close-only requests leave stdin
unchanged, and that a subsequent valid write succeeds. The callback race follows
from resetting the deadline without waiting when AfterFunc's stop returns false.

### Fix

Check cancellation while holding the stdin lock before writing or closing. Wait
for a started cancellation callback before clearing the deadline and releasing
that lock.

## F4. Special instruction files can block startup

Medium · Error handling · Fixed · `internal/textfile/textfile.go:19`

The shared reader opened settings, skill definitions, and workspace instructions
with a blocking open. A FIFO named AGENTS.md could hold session creation forever
before the byte limit was applied. The same problem affected the other callers.

### Evidence

A workspace instruction FIFO did not return until an external writer connected.
The regression requires directories, FIFOs, and symlinks to FIFOs to fail
promptly.

### Fix

Open without blocking, inspect the opened file, and reject nonregular files
before reading, matching the existing tool file-reader behavior.

## F5. Clipboard command results are discarded

Medium · Correctness · Fixed · `internal/tui/tui.go:138`

Control+V reached the textarea and started its clipboard command, but the TUI
dropped the command's private result message. Pasting therefore inserted
nothing.

### Evidence

A fake pbpaste executable returned known text. Sending Control+V through the
test driver left the composer empty before the fix. The regression never reads
or modifies the user's clipboard.

### Fix

Forward otherwise unhandled messages to the textarea and return its command.

## F6. Malformed tool arguments can execute commands

Medium · Correctness · Fixed · `internal/tools/tools.go:136`

Argument validation used Decoder.More to detect trailing input. That method
checks for another array or object element, so a trailing closing brace or
bracket was accepted. Malformed model output could run a shell command instead
of producing the promised argument error.

### Evidence

Arguments consisting of a valid shell object followed by an extra closing brace
executed a command that created a file. The regression requires trailing braces,
brackets, objects, and scalar values to fail before execution.

### Fix

Attempt a second decode and require EOF after the first object.

## F7. Headless answer output failures report success

Medium · Error handling · Fixed · `cmd/ox-server/main.go:271`

The headless runner discarded the error from printing the final answer. A
redirected output file that rejects writes, such as a full filesystem, could
lose the answer while the command reported success.

### Evidence

A local-provider test completed a turn with stdout set to a closed file.
runHeadless returned nil before the fix; the regression now requires the output
error.

### Fix

Return the error from printing the final answer.

## Checks run

- Focused settings, terminal, tools, process, instruction, and skill tests
  passed.
- Race-enabled tests passed for the server, agent, store, control, and server
  command packages.
- Reproduction probes failed as expected before fixes and were removed before
  permanent changes. Permanent regressions cover the confirmed behavior.
- Race-enabled process cancellation and subsequent-write regressions passed.
- `make check` passed Markdown formatting, Go formatting, go vet, all Go package
  tests, and all four evaluation regression tests.
- `make e2e` passed the isolated terminal tests, which the normal Go test run
  leaves disabled.
- Live and Linux checks listed under coverage gaps were not run.

## Verdict

Fixed seven Medium findings. The changes address lost edits, permissions,
cancellation, blocked reads, input handling, and error reporting without
changing the architecture or adding dependencies.
