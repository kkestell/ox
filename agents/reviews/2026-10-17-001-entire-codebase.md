# Review the entire Ox codebase

## Scope and coverage

Reviewed all tracked executable source, its tests, and the requirements in
README.md, AGENTS.md, and relevant approved plans. Three subagents covered the
server and model integration, tools and process management, and terminal client;
the primary reviewer covered command entry points, settings, credentials,
scripts, evaluations, build rules, and release wiring, then inspected the fixes.
Each finding was confirmed before fixing it. Every fix followed existing
requirements and needed no new behavior or interface decision.

Gaps:

- Validation ran on macOS. Linux-specific code and release packaging were read
  but not executed on Linux.
- No live provider, system keyring mutation, third-party ACP server, Docker
  evaluation, or GitHub release was exercised. Provider and ACP behavior was
  tested with local servers; evaluation failures used synthetic local fixtures.
- Ignored research material, built binaries, cached repositories, and saved
  evaluation runs were excluded from the source review.

## Summary

| ID  | Severity | Category       | Fix   | Title                                                |
| --- | -------- | -------------- | ----- | ---------------------------------------------------- |
| F1  | High     | Security       | Fixed | Markdown entities can inject terminal controls       |
| F2  | High     | Correctness    | Fixed | Installing overwrites saved settings                 |
| F3  | High     | Correctness    | Fixed | Final responses crash evaluation metrics             |
| F4  | Medium   | Correctness    | Fixed | Edited moves discard file permissions                |
| F5  | Medium   | Resources      | Fixed | Inherited stderr can prevent the client from exiting |
| F6  | Medium   | Correctness    | Fixed | Reused provider call identifiers erase earlier calls |
| F7  | Medium   | API design     | Fixed | New sessions require an optional server method       |
| F8  | Medium   | Error handling | Fixed | Completed streams can fail while waiting for closure |
| F9  | Medium   | Concurrency    | Fixed | Evaluation workers race to create repository mirrors |
| F10 | Medium   | Correctness    | Fixed | Zero measurements crash evaluation charts            |
| F11 | Medium   | Correctness    | Fixed | Missing usage is counted as free                     |
| F12 | Medium   | Correctness    | Fixed | The run script interprets prompts as options         |
| F13 | Medium   | Testing        | Fixed | The shutdown test discards an early close response   |

## F1. Markdown entities can inject terminal controls

High · Security · Fixed · `internal/tui/markdown.go:186`

The renderer escaped controls before parsing Markdown, then decoded numeric
entities into new control characters. Model or workspace text could introduce
terminal escape sequences and hide or alter displayed content.

### Evidence

Rendering `before &#x1b;[31mafter` produced an actual escape byte followed by
`[31m`. The regression uses the conceal sequence `&#x1b;[8m` and requires a
visible escaped representation.

### Fix

Escape decoded text before applying terminal styles. Existing Markdown styles
and ordinary entities retain their behavior.

## F2. Installing overwrites saved settings

High · Correctness · Fixed · `Makefile:33`

Every `make install` copied the example settings over the user's existing
settings, discarding model selections, provider pins, configured servers, and
favorites during an ordinary reinstall.

### Evidence

Installed into a temporary prefix and configuration directory with a saved
favorite. Installation succeeded but the favorite disappeared.

### Fix

Copy example settings only when the settings path does not exist. A focused
installation check covers both initial setup and preservation on reinstall.

## F3. Final responses crash evaluation metrics

High · Correctness · Fixed · `evals/eval.py:561`

Go encodes nil tool-call and outcome slices as JSON null. A normal final text
response has those nil slices, but metrics collection passed the resulting
Python None values to zip. Finished evaluations raised TypeError before saving
result.json, so a rerun repeated already completed work.

### Evidence

Traced the nil slices from stream assembly through AssistantBatch serialization.
A scratch transcript database reproduced `'NoneType' object is not iterable`.

### Fix

Treat null slices as empty when counting tools. A regression verifies that a
final response still contributes its request, tokens, and cost. The evaluation
regressions run as part of make check.

## F4. Edited moves discard file permissions

Medium · Correctness · Fixed · `internal/tools/patch.go:396`

Moving a file while changing its content recreated the destination with mode
0666 instead of retaining source permissions. Under a normal umask, executable
files lost their executable bits and private files became readable by others.

### Evidence

An edited move changed modes 0700 and 0600 to 0644. The regression covers 0755,
0700, and 0600 with umasks 022 and 077, updated content, and source removal.

### Fix

Create the destination exclusively with the source permissions and restore those
permissions after umask filtering, before writing its content.

## F5. Inherited stderr can prevent the client from exiting

Medium · Resources · Fixed · `internal/client/conn.go:135`

The client drained stderr before waiting for its server. A descendant that
inherited stderr could keep that drain blocked after the server exited. Killing
the server after two seconds did not close the descendant's pipe, so quitting or
reporting startup failure could wait forever.

### Evidence

A helper server spawned a sleep process inheriting stderr. Closing the client
waited for the descendant's full six-second lifetime despite the kill timeout.
The permanent regression retains stderr with a longer-lived descendant.

### Fix

Force-close the client's stderr reader at the shutdown timeout, allowing the
reader to finish and the server to be reaped.

## F6. Reused provider call identifiers erase earlier calls

Medium · Correctness · Fixed · `internal/server/server.go:523`

The transcript requires call identifiers to be unique within one assistant
message. The server forwarded those identifiers to ACP unchanged. If a provider
reused an identifier in another message of the same turn, the terminal client
replaced the earlier tool display, including during replay.

### Evidence

The local provider fixture produces shell-0 in successive responses. Tracing the
two batches into the terminal's tool map showed the second replacing the first.
The regression checks separate live calls, matching permission requests and
updates, replay, and unchanged identifiers in model history.

### Fix

Assign each invocation a unique ACP identifier and use it consistently through
permission and status updates. Give replayed calls unique identifiers as well.
Preserve provider identifiers in the stored transcript and model requests.

## F7. New sessions require an optional server method

Medium · API design · Fixed · `internal/tui/tui.go:435`

The client always sent session/close before session/new. A compatible server
without the optional close capability rejected that request, preventing /new
from creating another session. This violated the documented support for other
ACP servers and the approved behavior of /new during a running turn.

### Evidence

Traced capability negotiation and the unconditional close request. The
regression uses a server that advertises no close capability and has an active
turn waiting for permission.

### Fix

Track close support independently from resume support. Without close support,
cancel the old turn and pending permission, discard its local state, and create
the replacement session. Advertised close support continues to use
session/close.

## F8. Completed streams can fail while waiting for closure

Medium · Error handling · Fixed · `internal/openrouter/stream.go:111`

The stream ignored the terminal DONE event and waited for HTTP EOF after a
validated finish and usage. A response left open after DONE eventually failed as
a stall, discarding the answer and potentially retrying a successful request.

### Evidence

A local server sent text, finish, usage, and DONE while holding the HTTP
response open. A shortened stall timeout reproduced the failure. Regressions
also check that DONE without a validated finish remains an error.

### Fix

End event reading at DONE, preserving the validated completion and final usage.

## F9. Evaluation workers race to create repository mirrors

Medium · Concurrency · Fixed · `evals/eval.py:196`

Parallel repetitions share repository mirrors. On first use, multiple workers
could clone the same missing directory or read it before cloning finished,
failing an otherwise valid evaluation.

### Evidence

Two synchronized workers both attempted the first clone; one failed because the
destination already existed. The regression holds a clone open and checks that
another worker cannot access the incomplete mirror.

### Fix

Serialize mirror lookup, cloning, and fetching within the evaluation process.

## F10. Zero measurements crash evaluation charts

Medium · Correctness · Fixed · `evals/eval.py:985`

A failed startup legitimately records zero requests, tokens, and cost. When one
baseline repetition was zero but its median was positive, chart coloring divided
by the minimum baseline value and raised ZeroDivisionError.

### Evidence

Chart generation with baseline measurements 0, 1, and 2 reproduced the error.
Regressions cover mixed zero values and a candidate with all-zero measurements.

### Fix

Compare the measurement ranges directly to choose colors. Percentage changes
still use the median, with the existing zero-median handling.

## F11. Missing usage is counted as free

Medium · Correctness · Fixed · `evals/eval.py:547`

When a model response or compaction had no usage record, metrics skipped it
without invalidating total cost. Comparisons therefore showed zero or a partial
sum as the complete cost, despite the runner's existing rule that an unknown
request cost makes the run's cost unavailable.

### Evidence

A scratch transcript with a response and no usage returned cost 0.0. Tests cover
missing assistant and compaction usage before and after a priced request.

### Fix

Mark total cost unavailable when either usage or its cost is missing, while
continuing to collect reported token counts.

## F12. The run script interprets prompts as options

Medium · Correctness · Fixed · `scripts/run.py:64`

The wrapper accepted a prompt beginning with a dash after its own option
separator, then forwarded that prompt without a separator to ox-server. The
server interpreted the prompt as a flag and rejected it.

### Evidence

Traced the wrapper's generated command against parseRunOptions and its existing
option tests. A mocked subprocess check verifies ordinary and dash-prefixed
prompts without making a provider request.

### Fix

Insert an option separator before the prompt passed to the server.

## F13. The shutdown test discards an early close response

Medium · Testing · Fixed · `internal/server/server_test.go:516`

The shutdown test assumed the prompt response always arrived before the close
response. A completed prompt releases its operation before writing its response,
so close can legally respond first. The test discarded that early response while
awaiting the prompt, then timed out waiting for it again.

### Evidence

The combined race-enabled checks exposed the timeout. A focused run reproduced
it three times in ten attempts. After the fix, thirty race-enabled repetitions
passed without changing timeouts or production code.

### Fix

Retain responses received before the prompt response and use an already received
close response instead of waiting for it twice.

## Checks run

- `make check` passed: Markdown, gofmt, go vet, all Go package tests, and the
  evaluation regression tests.
- `make e2e` passed the isolated terminal tests.
- `uv run --with pyyaml python -m unittest discover -s evals -v` passed four
  evaluation regression tests.
- Focused installation and script-forwarding checks passed in temporary
  directories with mocked credentials and subprocess execution where needed.
- The first combined race-enabled run passed internal/client, internal/tui, and
  internal/tools but failed the server shutdown test. This exposed F13; thirty
  focused repetitions passed after its fix. The full server race suite then
  passed with `go test -race ./internal/server`.
- The first plain-Python evaluation probe could not import PyYAML. It was rerun
  successfully with the evaluation script's declared dependency through uv.
- Live and Linux checks listed under coverage gaps were not run.

## Verdict

Fixed thirteen confirmed findings: three High and ten Medium. No design rewrite
was needed; the fixes preserve existing responsibilities and behavior contracts.
