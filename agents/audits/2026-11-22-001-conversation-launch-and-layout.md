# Structural audit since the Ur merge

## Scope and evidence

Whole-repository audit of the 21 commits after `604ed8d`, the revision covered
by the [last structural audit](2026-11-20-003-since-first-audit.md), through
`784d484812b33b808d64817dc9f5b75ea163bca3` on `main`. At the start of the audit,
the index was clean. The working tree included a header alignment change in
`cmd/ox/web/src/style.css`, its assertion in `cmd/ox/web/e2e/workspace.spec.ts`,
and feature entries in `docs/todo.md`. The Chrome probe used that working tree.

Read the repository and web instructions, functional requirements,
architecture, known issues, and the relevant protocol and Claude Code notes.
Compared changes across the harnesses, server, store, relay, web client, and
dev stack with the reviews and commit history. Read all three earlier audits.
The [first structural audit](2026-11-20-001-whole-repository.md) had already
landed both of its Plan findings by the previous audit. The previous audit's
three Plan findings landed in `ca73f2c` (harness input state), `9aaa6f4`
(session labels), and `8992e6d` (harness configuration checks).

The [deslop audit](2026-11-20-002-deslop.md) has no outstanding entries. Its
shutdown, database, and reply persistence findings landed in `0975ec3`, child
publication in `3a2b3d2`, archive ownership in `99a909d`, and hollow tests in
`784d484`. Its remaining fixes landed in `ca73f2c`, `9aaa6f4`, `adcba8f`,
`43a8b5f`, `27efb42`, and `2125f54`. The existing todo entries describe feature
work. Neither known issue's prerequisite changed in the audited commits.

Gaps:

- The review traced changes and their owners. It did not repeat the earlier
  whole-repository review against every functional requirement.
- The Ur probe used a local HTTP server and a killed daemon subprocess. No
  live OpenRouter or Claude Code turns were run. The child probe established
  blocking with an injected wait and did not measure production launch times.
- The layout probe used headless Chrome. It did not check other browsers.

## Summary

| ID | Cost   | Action | Title                                                        | Places |
| -- | ------ | ------ | ------------------------------------------------------------ | ------ |
| F1 | High   | Plan   | Ur saves the transcript and model context at different times | 6      |
| F2 | Medium | Plan   | A child's first send holds the daemon's membership lock      | 6      |
| F3 | Medium | Plan   | The file split assumes space the side panels can take away   | 6      |
| F4 | Low    | Plan   | The dev stack decides its exit status before services stop   | 4      |

## F1. Ur saves the transcript and model context at different times

High · Plan

The same assistant text has two persistence boundaries. `oxd` records each
`text_delta` before sending it to clients. Ur keeps those fragments in the
running turn and writes them to its conversation file only when the turn
finishes or the agent is stopped. Ur builds later requests from the file's
entries. If the daemon is killed mid-turn, the user can still read text that
the resumed model has lost. Both files are readable, so the lost-conversation
notice never appears. The user has to notice the missing context themselves.

### Places

- `internal/ur/ur.go:201–219` accumulates each delta and publishes it live.
- `internal/server/session.go:415–423` records the update before broadcasting it.
- `internal/server/history.go:19–24,41–76` stores the delta and joins adjacent
  fragments when clients read history.
- `internal/ur/ur.go:268–294,328–339` saves partial replies on orderly turn
  completion, interrupt, failure, and stop.
- `internal/ur/ur.go:116–159` reads and appends the separate JSONL conversation
  and announces its token after the first entry.
- `internal/ur/ur.go:236–265` saves the prompt and builds the next request from
  Ur's loaded entries. For inter-session messages, these entries hold the model
  input with its sender wrapper.

### Defects

[Deslop F1](2026-11-20-002-deslop.md#f1-reply-text-shown-live-is-lost-on-an-abrupt-oxd-exit)
found the transcript half of this failure. `0975ec3` fixed its in-memory buffer
by recording the transcript's deltas immediately. The Ur file added in
`f177bb0` still has a different writer and commit boundary. The resulting
restart loses the assistant text from the model's conversation while retaining
it in the page. This breaks **Ur sessions** even though **Surviving restarts**
holds for the transcript in the probe.

### Evidence

A local HTTP server sent a `remember blue` delta and left the stream open.
The client received `text_delta`, then the probe sent the daemon subprocess
`SIGKILL` and reopened the same database and Ur directory. `session/get`
contained `remember blue`. The next Ur request contained only the two user
messages. The earlier Ur review rejected adding recovery for a partial file
write. This run used successful disk writes and a valid conversation file.

### Replacement

Give Ur's conversation one owner for recorded prompts and reply fragments.
The owner observes deltas as Ur handles them. The client transcript and the
next request should project those same committed records. This replaces the
second reply checkpoint in `finish` and `Stop`, and removes the join between
two independently persisted versions of a turn. Appending the same text to
both stores sooner would still leave a gap between their writes.

The Ur plan chose to leave the file on deletion “as deleting a Claude session
leaves Claude's transcript.” The file carries the actual model input and
records the model that wrote a reply. Preserve those properties, existing saved
conversations, display attribution, and the ability to start a fresh model
conversation while keeping the earlier transcript. The plan should settle how
Ur's existing file and Ox's session history supply the two views. The change
touches Ur's persistence, the session history boundary, and restart tests. The
visible correction is that resumed context includes the streamed reply already
shown in the transcript.
[Durable Ur conversations](../plans/2026-11-21-005-ur-durable-conversations.md#check-your-assumptions)

## F2. A child's first send holds the daemon's membership lock

Medium · Plan

The parent owns child construction until the child starts processing its
operations. That ownership is sound. Its implementation also holds
`Server.mu` across harness launch and the child's first `Send`. Every other
session request must acquire the same lock to look up its target. A child
launch or first send that takes time can stall requests across all workspaces.
The ordinary Ur first send already records a taken prompt, opens and appends
its conversation file, stores its resume token, and marshals the request while
that lock is held.

### Places

- `internal/server/mcp.go:359–398` posts to the parent and takes the membership
  lock through `addChild`.
- `internal/server/methods.go:361–396` keeps launch, publication and first
  delivery within `addSession`'s lock scope.
- `internal/server/session.go:338–363,415–423,468–472` invokes the harness and
  provides the synchronous history and resume-token callbacks.
- `internal/ur/ur.go:139–159,227–265` performs the first-turn work before
  `Send` returns.
- `internal/server/methods.go:96–109,402–421` requires the same lock to route an
  unrelated `session/get`.
- `internal/server/server.go:140–154` uses the lock to close membership and
  obtain the shutdown set.

### Defects

[Deslop F3](2026-11-20-002-deslop.md#f3-create_session-publishes-the-child-before-its-first-message-has-an-owner)
found the archive and shutdown races when child publication preceded first
message ownership. `3a2b3d2` closed that gap and introduced the cross-session
wait. The new wait breaks **Independent sessions**. The launch path already
noted the cost of holding the lock over an exec and a pipe write. The plan also
assumed first `Send` would do “no blocking I/O at start,” which is false for Ur.
[Child first-message plan](../plans/2026-11-21-007-child-owns-first-message.md#decisions)

### Evidence

A server probe gated the child's first `Send`. While it was held, `harness/list`
replied over the same connection, and `session/get` for an unrelated parked
session waited. The unrelated snapshot came back once the gate was released.
Source tracing shows the real Ur path performs synchronous I/O in that exact
scope. The probe establishes which requests wait and does not establish the
usual length of that wait.

### Replacement

Leave construction with the parent, then give the child its first input as
part of its initial operation before it can service outside requests. Scope
`Server.mu` to membership, publication order, and the shutdown decision.
Keep agent startup and delivery outside that critical section. The plan needs
to preserve the reason for the existing lock scope. A failed launch leaves no
published child, a successful child cannot be archived or sent another message
before its first input, and a child launch racing shutdown must either join
the shutdown set or be disposed of by its owner. This removes the process and
first-send work from the global lock without reopening the old post-publication
delivery race. It touches child construction, the startup and shutdown handoff,
and their tests. The user-visible change is isolation from a slow child start.

## F3. The file split assumes space the side panels can take away

Medium · Plan

The layout stores two side-panel widths independently of the transcript/file
split. The side panels can leave less than 480 px for the two center panes.
The split setter always clamps within `[240, total − 240]`. With less than
480 px that interval is inverted, so every drag picks the same width. Around
240 px available, it saves a split that reduces the transcript to almost
nothing. The session keeps that split when the user switches views or widens
the window.

### Places

- `cmd/ox/web/src/Panels.tsx:3–22` clamps each side panel in isolation.
- `cmd/ox/web/src/Panels.tsx:54–64` samples the adjacent widths at drag start.
- `cmd/ox/web/src/ox.tsx:175–178,246–252` stores the split and clamps a drag
  against two fixed minima.
- `cmd/ox/web/src/App.tsx:83–92,127–133,174–201` supplies the independent
  widths and applies complementary split shares to the center panes.
- `cmd/ox/web/src/FileView.tsx:29` applies the file viewer's share.
- `cmd/ox/web/src/style.css:193,372` lets either flex item shrink to zero
  content width.

### Defects

The panel review's [F1](../reviews/2026-11-21-010-panel-toggles-and-resizing.md#f1-transcript-fills-half-its-space-with-no-file-open)
fixed the application of the saved split when the file viewer was closed.
The layout now checks that both panes exist. It still fails **Resizing panels**
when both panes are present and lack the assumed width. The panel plan's
240 px minimum was intended to keep both panes usable.
[Panel layout plan](../plans/2026-11-21-010-panel-toggles-and-resizing.md#decisions)

### Evidence

With default 288 px side panels and an 820 px Chrome viewport, the two center
panes started at 121.5 px and 122.5 px. A 35 px left drag changed them to
approximately 4 px and 240 px. A 35 px right drag left those widths unchanged.
The tracked drag test exercises the split at a 1600 px viewport, where its
center panes have room for both fixed minima.

### Replacement

Have `Layout`, which composes all four columns, own the effective allocation
of the available width. The session control keeps only a valid split
preference. Derive both rendered widths and the split drag's feasible range
from the same allocation, so an impossible range cannot reach the session
setter. This replaces independent assumptions about the center width with one
rule while keeping each session's preference through navigation. The plan can
settle how narrow windows divide the available space and where to enforce that
rule. It touches the layout, the split setter, and the drag test. The visible
change is how the center panes resize when space is tight.

## F4. The dev stack decides its exit status before services stop

Low · Plan

The dev stack has two ways to handle a service's exit. A rebuild awaits the
old process and checks its exit status. A terminal shutdown resolves the
stack's result immediately. Later exits still reach the service's handler,
but `stopping` prevents the handler from reporting a failure through the
stack result. A daemon that exits on a race during shutdown can print its
report while the stack returns the same status as an ordinary Ctrl-C.

### Places

- `cmd/ox/web/stack.mjs:10–23,77–89` checks the exit of a retiring service on
  restart.
- `cmd/ox/web/stack.mjs:46–66` resolves the stack result at the start of a
  shutdown and ignores later service exit statuses.
- `cmd/ox/web/stack.mjs:122–123` routes terminal and targeted shutdown signals
  into that early resolution.
- `cmd/ox/web/dev.mjs:17–19` uses the result as the dev process's exit status.

### Defects

[Deslop F7](2026-11-20-002-deslop.md#f7-a-race-report-from-a-stopping-oxd-is-lost-in-e2e-and-dev-restarts)
found this class of failure in rebuilds and e2e teardown. `0975ec3` gave both
paths an exit check. The shutdown plan handled terminal Ctrl-C separately so
the stack “stops without signalling them and resolves 130.” Omitting the
second signal preserves graceful shutdown. Resolving 130 before observing
exit leaves the status blind spot in ordinary dev shutdown.
[Daemon shutdown plan](../plans/2026-11-21-009-daemon-shutdown-and-ownership.md#decisions)

### Evidence

Ran `runStack` in its own process group with a service that returned `66`
when the group received `SIGINT`, standing in for the race detector. The
service reported `66` and the stack process exited `130`.

### Replacement

Make the stack's service lifecycle owner judge exit outcomes once for both
rebuilds and dev shutdown. The signal path decides who sends a signal, and
the observed exits decide the result. The same outcome rule can apply when
the terminal already delivered SIGINT. This removes the immediate result path
that discards later statuses and keeps one-Ctrl-C graceful shutdown. The
change touches the stack supervisor and its tests. An ordinary Ctrl-C can
keep status 130 while an unexpected daemon failure becomes distinguishable.

## Checks run

- Two temporary Go probes under `go test -race`, using a Go overlay. Both
  reproduced the reported behavior. One used a subprocess and local HTTP
  server to compare the restored transcript with the next request. The other
  gated a first child send and observed which unrelated RPC could answer.
- One temporary Playwright spec against the race-built daemon and relay in
  headless Chrome reproduced the narrow file split and measured both panes.
- One Node subprocess probe reproduced the dev stack's exit status with a
  stand-in service in a separate process group.
- `dprint check` and `git diff --check` for the documentation changes.
