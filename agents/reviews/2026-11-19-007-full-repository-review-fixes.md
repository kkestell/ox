# Full-repository review: fixes

## Scope and coverage

This applies the review's outcomes to the findings of `agents/reviews/2026-11-19-006-full-repository-review.md`. That review covered the whole repository at `f2c49fd` and was written by three independent reviewers (code-review-and-quality, code-review-expert, codebase-design), with every retained finding checked against the source. Each finding is fixed here, recorded in `docs/known-issues.md`, or rejected with its reason, as `AGENTS.md` describes. No brief applies.

Gaps:

- F2 is recorded, not fixed. Its fix depends on Claude Code behavior that has not been reproduced live.
- The relay change (F1) affects only how `ox` starts, so it was checked by a unit test and not in headless Chrome through `make dev`. The `make dev` stack uses `-addr localhost:8081`, which the change still accepts.

## Summary

| ID | Severity | Category | Fix | Title |
| -- | -------- | -------- | --- | ----- |
| F1 | Medium | Security | Fixed | Wildcard `-addr` lets any network client control `oxd` |
| F2 | Medium | Correctness | Recorded | A resumed session whose first turn is interrupted or fails never parks |
| F3 | Medium | Testing | Fixed | The fake harness cannot raise approvals, so approvals are tested only live |
| F4 | Low | Correctness | Fixed | The same directory added through a symlink makes a second workspace |
| F5 | Low | Correctness | Fixed | An orphaned tool call shows "running" after a reload |
| F6 | Low | Error handling | Fixed | A failed write while forgetting a lost conversation makes every later send fail |
| F7 | Low | Correctness | Fixed | A session whose harness was dropped from the configuration shows no settings |
| F8 | Low | Correctness | Fixed | A reconnect recreates every transcript's DOM, closing open tool cards |
| F9 | Low | Architecture | Fixed | Each harness must publish `parked`, a concept the session owns |
| F10 | Low | API design | Fixed | `session/get`'s `prompt` field has no consumer |
| F11 | Low | Simplicity | Fixed | `approvedTools` copies the tool list's `readOnlyHint` by hand |
| F12 | Low | Readability | Fixed | `ox.tsx` holds Claude tool-payload parsing beside the provider |

## F1. Wildcard `-addr` lets any network client control `oxd`

Medium · Security · Fixed · `cmd/ox/main.go:39`

`ox -addr :8080` and `ox -addr 0.0.0.0:8080` listen on every interface. `checkHost` protects only against DNS rebinding: it accepts any request whose `Host` header is a loopback name. A client that is not a browser chooses its own `Host` header, and `CrossOriginProtection` lets through requests that have no `Origin` or `Sec-Fetch-Site` header. So any machine on the network could open `/events` with `Host: localhost`, then drive `/rpc` to create a workspace at `/`, create a session, send prompts and approve tool calls.

This breaks **Local access only**, because an unspecified address names no host.

### Fix

The new `serveHost` refuses an `-addr` whose host is empty or an unspecified IP, with `-addr ":8080" must name a host, not every interface`, so `ox` fails at startup. Named hosts, loopback included, are unchanged. `TestRelayRefusesEveryInterface` covers both the refused and the accepted addresses.

## F2. A resumed session whose first turn is interrupted or fails never parks

Medium · Correctness · Recorded · `internal/claude/agent.go:73-75`, `internal/claude/park.go:9-16`

A resumed agent starts with `mayHaveSchedules` set, and only a Stop hook clears it. Claude sends no Stop callback for a turn that ends by interrupt or error. So if the user stops, or an API error fails, the first turn after a resume, the session never parks until a later turn completes. This is the documented design (`docs/protocol.md`, `docs/architecture.md`), and it costs one idle Claude process per affected session.

### Fix

Recorded in `docs/known-issues.md`. A fix would need a live check first. If every job `--resume` restores, including a fixed-interval `/loop`, shows up as a top-level `CronCreate` call in Ox's history, a resumed process with no such call could start parkable. Without that check, clearing the flag could park a session that has a restored schedule, which would break **Parked schedules**.

## F3. The fake harness cannot raise approvals, so approvals are tested only live

Medium · Testing · Fixed · `internal/fake/fake.go`, `cmd/ox/web/e2e/session.spec.ts`

`fake.Approve` always returned `ErrApprovalNotFound`. As a result `session/approve` had no test in `make test`. The two e2e approval specs patched `window.EventSource` to inject a made-up `approval` update, so `oxd` never held the approval, and its reply, `approval_answered`, and the card's removal were never checked.

### Fix

- A fake prompt of the form `approve <tool> <input JSON>` now does what Claude does in manual mode:
  - publishes the tool call;
  - raises a `can_use_tool` approval and reports `requires_action`;
  - holds the approval in its snapshot until it is answered, interrupted, or stopped.
- Answering it publishes `approval_answered` and a tool result whose text is the raw response, then streams the reply.
- `TestApprovalAnsweredOnce` covers:
  - a parked session refusing an answer;
  - the approval appearing in `session/get`;
  - one answer reaching the harness untouched;
  - a second answer being refused.
- The e2e specs "Allow sends the exact numbers…" and "AskUserQuestion is answered from a form" now run through `oxd`. They have no `EventSource` patch, and they assert on the response the harness received.
- The `docs/architecture.md` harness table describes the new fake prompt.

## F4. The same directory added through a symlink makes a second workspace

Low · Correctness · Fixed · `internal/server/methods.go:114-134`

`createWorkspace` matched an existing workspace only by its cleaned path string. Adding `/tmp/proj` and then `/private/tmp/proj` on macOS, or a path that differs only in case on case-insensitive APFS, created two workspaces for one directory. This breaks **Adding a workspace**.

### Fix

The new path's `os.Stat` result is compared with `os.SameFile` against each existing workspace's directory. `TestSameDirectoryIsOneWorkspace` adds a directory and then a symlink to it, and gets one workspace.

## F5. An orphaned tool call shows "running" after a reload

Low · Correctness · Fixed · `internal/server/history.go`, `cmd/ox/web/src/ox.tsx`

The transcript did not record when a process ended:

- history skipped `parked`;
- Claude publishes no `exited` when Ox stops its process;
- an `oxd` crash or a second stop signal records nothing.

The page made up for this with a guess at load: when the session was parked, it marked unfinished tools stopped. Once the session ran again that guess no longer applied, so a reloaded page showed the cut-off call with the "running" marker indefinitely, while a page that had stayed open showed it stopped. This breaks **One transcript**.

### Fix

The cause is fixed: history now records every process's end.

- **Server:**
  - `history.record` keeps `parked` updates.
  - Since F9 the session publishes `parked` whenever it drops a process, so every clean end is recorded.
  - A process that ended with `oxd` itself has no recorded end. A session therefore starts by recording `parked` (`history.recordEnd`) when its last stored event is anything else. The new `store.LastEvent` reads that event.
- **Page:**
  - It replays history through the same `apply` as live updates, so `parked` and `exited` entries mark unfinished tools stopped.
  - `load` sets the session's current state after the replay.
  - The guess for parked sessions at load is removed.
- **Fake:** cancelling an approval ends its tool call with an error result, so an interrupted fake turn leaves no call running.
- **Docs:** `docs/protocol.md` lists `parked` among the recorded updates and says a missing end is recorded at the next start.
- **Tests:**
  - `TestUnrecordedEndIsRecordedAtLoad` covers a history that ends mid-tool: loading records one `parked`, and loading again records no second one.
  - `TestHistoryMergesTextAndKeepsConversation` and `TestSessionLifecycle` now expect the recorded `parked`.
  - The e2e test "a tool whose process ended with oxd shows stopped on every page" kills `oxd` with SIGKILL while a tool waits for approval, restarts it, sends again, and opens the session in a second page.
  - `services.restartDaemon` takes the signal to use.

## F6. A failed write while forgetting a lost conversation makes every later send fail

Low · Error handling · Fixed · `internal/server/session.go:199-207`

`forgetConversation` returned the store error before clearing the resume token. While the database failed, every send resumed the same missing conversation and failed with the database error. This breaks **Lost conversation**, and **Database failures** too, which says a failed write is not shown.

### Fix

The session clears the token in memory, logs the failed write, and publishes the notice. A restart reads the stale stored token, fails to resume, and forgets it again. `docs/architecture.md` adds this to the exceptions to "written before applied in memory". `TestUnresumableConversationForgottenWhenNotStored` closes the store and checks that the next send starts a new conversation. Its setup is shared with `TestUnresumableConversationStartsAnew` through `unresumableSession`.

## F7. A session whose harness was dropped from the configuration shows no settings

Low · Correctness · Fixed · `cmd/ox/web/src/SessionView.tsx:165`

The settings row was wrapped in `<Show when={ox.state.harnesses[props.session.harness]}>`, so it disappeared entirely when the configuration no longer offered the session's harness. This breaks **Retired settings**.

### Fix

`SettingsForm` uses an empty catalog for a missing harness. The model picker shows the stored model, and the permission select shows the stored mode through its existing fallback for retired values, with nothing else to choose. The new `services.restartDaemon(config)` restarts `oxd` over the same database. The e2e test "a session whose harness is no longer offered shows its settings" uses it to drop the `fake` harness.

## F8. A reconnect recreates every transcript's DOM, closing open tool cards

Low · Correctness · Fixed · `cmd/ox/web/src/ox.tsx:324`

`load` replaced a loaded session's entries with new objects. `For` is keyed by item identity, so every entry was rendered again on each reconnect. That collapsed every expanded tool card and contradicted the comment and architecture text, which say loaded views keep their state.

### Fix

`reconcile(session.entries, null)(loaded.entries)` merges the reloaded entries by position, which keeps each entry's store identity. The e2e test "a reconnect keeps an expanded tool call open" expands a tool card, restarts the relay, waits for the session's reload, and checks the card is still open.

## F9. Each harness must publish `parked`, a concept the session owns

Low · Architecture · Fixed · `internal/agent/agent.go:41-47`, `internal/claude/agent.go:216`, `internal/fake/fake.go:159`

The `agent.Agent` contract required each harness to publish `parked` as its last update, though parked means only that the session has no process.

### Fix

The session publishes `parked` in `parked()`, where it drops an exited agent: in `run` after `Exited()`, and in `stop()`. Both run on the session goroutine right after `Handle` or `Stop` returns, so the order is unchanged. The `Stop`/`Exited` contract no longer mentions parking, and neither adapter publishes it. The Claude tests no longer expect `parked`. `TestResumeTokenKeptWhenNotStored` now checks that a process that exits on its own produces `parked`, and `TestSessionLifecycle` already checks parking by timer. The `parked` row in `docs/protocol.md` describes it as the session's update.

## F10. `session/get`'s `prompt` field has no consumer

Low · API design · Fixed · `internal/agent/agent.go:136-142`

`agent.Snapshot.Prompt` was built by both harnesses and served by `session/get`, but no client read it. The web client only declared its type.

### Fix

The field is removed from `agent.Snapshot`, both harnesses, `protocol.ts`, and `docs/protocol.md`. Claude tests that observed the held prompt through it now observe it through `Send` returning `ErrBusy`, or succeeding once the prompt is released. `TestSnapshotReturnsRunningPrompt` is now `TestSnapshotReturnsStateAndApprovals`.

## F11. `approvedTools` copies the tool list's `readOnlyHint` by hand

Low · Simplicity · Fixed · `internal/server/mcp.go:101-112`

Two facts had to agree: a tool runs without approval, and the tool is annotated read-only, which is what stops plan mode from prompting for it. They were written in two places.

### Fix

`tools` is the served list, and `approvedTools` is built from the tools in it whose `readOnlyHint` is true.

## F12. `ox.tsx` holds Claude tool-payload parsing beside the provider

Low · Readability · Fixed · `cmd/ox/web/src/ox.tsx:41-77`

`toolSubject`, `planOf`, `Question`, `questionsOf` and `toolInputJSON` read Claude's tool payloads. They have nothing to do with the provider's job, and the architecture table did not list them.

### Fix

They moved unchanged to `cmd/ox/web/src/toolInput.ts`, and `docs/architecture.md` has a row for it.

## Rejected

- **Session operations split between `methods.go` and `session.go`** (codebase-design). Each handler's parked case is that operation's own meaning, and moving the bodies into `session` methods would not remove that case analysis. A move of about 150 lines that buys only locality costs more than keeping the split.
- **`oxd` drops in-flight replies when a client half-closes** (code-review-and-quality). It is reachable only when Claude closes `ox-mcp`'s stdin, which happens as Claude exits and stops waiting for replies, so no user-visible failure follows.
- **The MCP forwarder's environment variable names are string literals on both sides** (codebase-design). A rename is rare, and the live tests exercise both sides together. A shared constant would bring in a package dependency between the two binaries.

## Checks run

- `make test`: lint, web build, and `go test -race ./...`. Passed.
- `make e2e`: 19 passed, with the 3 live-only specs skipped.
- `make live`: `TestLive` passed, and the 3 Haiku approval specs passed.
- New tests run against code without their fix, each in a detached worktree that has since been removed:
  - At the base revision `f2c49fd`, `TestApprovalAnsweredOnce`, `TestSameDirectoryIsOneWorkspace` and `TestUnresumableConversationForgottenWhenNotStored` fail.
  - With the new Go code and the base web client, the e2e tests for F7 and F8 fail.
  - The two rewritten approval specs pass with the base web client, as they should: they check existing page behavior, now through `oxd`.
  - With this change minus F5's fix (history not recording `parked`, no `recordEnd`, the load-time guess restored), `TestUnrecordedEndIsRecordedAtLoad`, `TestHistoryMergesTextAndKeepsConversation`, `TestSessionLifecycle` and the e2e test "a tool whose process ended with oxd shows stopped on every page" fail.
  - `TestRelayRefusesEveryInterface` uses `serveHost`, which the change adds, so it was not run there.

## Verdict

F1 and F3–F12 are fixed and covered by tests, F2 is recorded in `docs/known-issues.md`, and three findings are rejected with their reasons. The change is ready to commit.
