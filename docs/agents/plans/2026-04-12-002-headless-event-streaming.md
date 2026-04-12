# Headless Mode Event Streaming

## Goal

Print all agent loop events to stderr when Ox runs in headless mode, and stream
that output to the console in real time during eval runs, so developers can
watch the agent work without attaching a TUI.

## Desired outcome

When running `ox --headless --yolo --turn "..."`:
- All events (tool calls, tool completions, subagent activity, compaction
  notices, token usage) appear on stderr as they happen.
- Response text continues to flow to stdout, keeping the stdout/stderr
  separation that scripting and eval infrastructure depend on.

When running `make evals-run` (or equivalent):
- The eval runner prints container stderr to the host terminal in real time so
  the developer can watch each agent step without waiting for the container
  to exit.

## How we got here

HeadlessRunner was written to do the minimum: stream response text to stdout
and fatal errors to stderr. The TUI handles all event rendering for the
interactive path, so headless rendering was deferred. Now that evals run in
containers, there is no other way to observe agent behavior without adding
event output to the headless path.

## Approaches considered

### Option A — Always emit events in headless mode (stderr)

HeadlessRunner always renders every `AgentLoopEvent` type to stderr without
a new flag. The stdout/stderr split is the opt-out: callers that only want
agent text redirect or discard stderr.

- **Pros**: No new flags; matches the `ur chat` CLI pattern (tool calls → stderr,
  response text → stdout); headless is a developer mode and having all output is
  the expected default.
- **Cons**: Existing scripts that capture stderr for error detection will see
  new content — though stderr was already used for warnings and errors.
- **Failure modes**: Slightly noisier eval log if streaming is added to
  ContainerRunner without a gate.

### Option B — `--verbose` flag on Ox

Add `--verbose` to `OxBootOptions`. HeadlessRunner only prints events when the
flag is set. ContainerRunner passes `--verbose` when opted-in from the eval
runner with a matching flag.

- **Pros**: Zero impact on existing headless callers; clean opt-in.
- **Cons**: Two new flags (one in Ox, one in EvalRunner); the common case
  (developer watching an eval) requires two flags.
- **Failure modes**: Easy to forget the flag, making debugging harder.

## Recommended approach

Option A for HeadlessRunner — always emit all events to stderr. The headless
mode is a developer/evaluation path, not a user-facing entry point. Matching
what `ur chat` does (tool calls to stderr, text to stdout) keeps the model
consistent.

For eval runner / ContainerRunner: add opt-in real-time streaming. Running
hundreds of scenarios in parallel could be overwhelming if every event is
printed unconditionally. A `--stream-output` flag in EvalRunner enables this
for interactive observation sessions.

## Related code

- `src/Ox/HeadlessRunner.cs` — Only handles `ResponseChunk` and `TurnError` today;
  needs a full event switch.
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — Defines all event types; `ToolCallStarted.FormatCall()`
  already produces a display-ready string.
- `src/Ur/Permissions/TurnCallbacks.cs` — `SubagentEventEmitted` is the relay hook
  for subagent events; HeadlessRunner must wire this up to see inner-agent activity.
- `evals/EvalRunner/ContainerRunner.cs` — Uses `ReadToEndAsync` for both stdout and
  stderr; must switch to line-by-line streaming to show eval progress in real time.
- `evals/EvalRunner/Program.cs` — Top-level parse and dispatch; needs `--stream-output`
  flag.

## Current state

- HeadlessRunner handles two of ~eight event types.
- In non-yolo mode, `callbacks` is `null`; the non-null check inside `TurnCallbacks`'s
  consumers (`callbacks?.SubagentEventEmitted`) silently drops subagent events.
- ContainerRunner collects all process output with `ReadToEndAsync`, which blocks
  until exit; live streaming is not possible with the current approach.
- The `ToolCallStarted.FormatCall()` method already formats tool calls for display 
  (used by the TUI's EventRouter); HeadlessRunner can reuse it directly.

## Structural considerations

**HeadlessRunner — callbacks lifetime**

Currently callbacks is only non-null in yolo mode (to supply `RequestPermissionAsync`).
To relay subagent events in all headless runs, callbacks must be created in both
branches — non-yolo with only `SubagentEventEmitted` set, yolo with both.
This is the existing `TurnCallbacks` contract: a null `RequestPermissionAsync` inside
a non-null `TurnCallbacks` still means "auto-deny" (checked explicitly in ToolInvoker
`CheckPermission`), so the non-yolo denial behavior is preserved.

**SubagentEvent is a relay envelope, not a main-stream event**

`SubagentEvent` is fired via `TurnCallbacks.SubagentEventEmitted`, not yielded by
`session.RunTurnAsync()`. HeadlessRunner's foreach switch handles the main stream;
a separate async callback handles subagent events. These two print sites must use
consistent formatting — both should delegate to the same private `PrintEvent` helper
to avoid divergence.

**ContainerRunner streaming**

Switching from `ReadToEndAsync` to event-based line reading (`BeginErrorReadLine` +
`ErrorDataReceived`) lets stderr be both printed to the host console and collected
for the error message path. Stdout remains buffered (only used to satisfy the async
pattern; actual eval artifacts come from disk). The streaming behavior is gated on a
`bool streamOutput` parameter so existing callers don't change behavior.

**Layer boundaries are respected**

All changes are either in the Ox presentation layer (HeadlessRunner) or the EvalRunner
utility layer (ContainerRunner, Program). No changes touch Ur's AgentLoop, UrSession,
or event types.

## Implementation plan

### HeadlessRunner

- [x] Wire `SubagentEventEmitted` into callbacks for both yolo and non-yolo paths.
  In non-yolo: `new TurnCallbacks { SubagentEventEmitted = PrintSubagentEvent }`.
  In yolo: extend the existing initializer to also include `SubagentEventEmitted`.

- [x] Add a private `PrintEvent(AgentLoopEvent evt, string prefix = "")` helper
  that handles the full event switch and writes to stderr. Prefix is an empty string
  for main-stream events and an indented tag like `"  [sub] "` for subagent events.

  Events to handle:
  - `ToolCallStarted` → `stderr: [tool] {evt.FormatCall()}`
  - `ToolCallCompleted { IsError: true }` → `stderr: [tool-err] {evt.ToolName}: {TruncatedResult}`
  - `ToolCallCompleted` → `stderr: [tool-ok] {evt.ToolName}: {TruncatedResult}`
  - `ToolAwaitingApproval` → `stderr: [awaiting-approval] {evt.CallId}`
  - `TurnCompleted` → `stderr: [done] {InputTokens} input tokens` (when non-null)
  - `TurnError` → already handled; keep in the main switch (different path: triggers
    `hadFatalError` and exits) — do not move into `PrintEvent`.
  - `Compacted` → `stderr: [compacted] {evt.Message}`
  - `SubagentEvent` → recurse: `PrintEvent(evt.Inner, "  [sub] ")`

- [x] Call `PrintEvent(evt)` inside the main `await foreach` loop for every event
  that is not `ResponseChunk` or `TurnError`.

- [x] Add a private `ValueTask PrintSubagentEvent(AgentLoopEvent evt)` adapter that
  calls `PrintEvent(evt, "  [sub] ")` and returns default, used as the
  `SubagentEventEmitted` delegate.

- [x] End each turn's event output with a blank line to stderr (currently a blank line
  is written to stdout; write one to stderr as well to separate turns visually).

### EvalRunner / ContainerRunner

- [x] Add a `bool streamOutput` parameter to `ContainerRunner.RunAsync`.
  When `false`, behavior is identical to today (buffered output, no real-time printing).

- [x] When `streamOutput` is `true`:
  - Do not call `ReadToEndAsync` on stderr; use `BeginErrorReadLine` with a
    `ErrorDataReceived` handler that both appends to a `StringBuilder` and
    calls `Console.Error.WriteLine`.
  - Prefix each output line with the scenario+model tag so interleaved output
    from parallel runs (future) can be distinguished, e.g.:
    `"[scenario × model] <line>"`.
  - Stdout can keep `ReadToEndAsync` (not shown to user; only used to satisfy
    the async process-wait pattern).

- [x] Add `--stream-output` flag to `EvalRunner/Program.cs` `ParseOptions`.
  Default `false`. When set, pass `streamOutput: true` to `ContainerRunner.RunAsync`.

- [x] Update `RunnerOptions` record/class with a `StreamOutput` bool field.

## Impact assessment

- **Code paths affected**: `HeadlessRunner.RunAsync`, `ContainerRunner.RunAsync`,
  `EvalRunner/Program.cs` argument parsing.
- **Data or schema impact**: None.
- **Dependency or API impact**: `ContainerRunner.RunAsync` signature changes (new
  optional parameter); callers — only `Program.cs` — must be updated.

## Validation

- Build: `dotnet build` from repo root, confirm no errors in `Ox` and `EvalRunner`
  projects.
- Smoke test headless events: run a local `ox --headless --yolo --turn "list files"`,
  confirm stderr shows `[tool] Glob(...)`, `[tool-ok] glob: ...`, `[done] ...`.
- Subagent relay: run a task that involves a subagent, confirm `[sub]`-prefixed lines
  appear on stderr.
- Eval streaming: run `make evals-run` with `--stream-output`, confirm real-time
  output appears per scenario.
- Eval no-stream: run without `--stream-output`, confirm existing behavior unchanged
  (no extra output, PASS/FAIL summary only).

## Open questions

- Should `ToolCallCompleted` result output be truncated? The TUI clips at ~200 chars;
  suggest the same limit for headless to avoid flooding the terminal.
