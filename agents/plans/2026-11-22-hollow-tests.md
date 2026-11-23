# Make thirteen tests fail when their behavior breaks

## Goal

Thirteen tests pass when the behavior they name breaks ([#31](https://github.com/kkestell/ox/issues/31), finding F5 of `agents/audits/2026-11-22-deslop-audit.md`). Change each test so it observes the outcome it names, and change no production code. `TestLiveUr` (`internal/server/live_test.go`), the fourteenth test in F5, stays as it is: [#27](https://github.com/kkestell/ox/issues/27) reworks Ur's interrupt, and its change rewrites that test.

## Related code

- `internal/claude/configure.go` — `controlTimedOut` ignores a timer whose request ID is not the current request's.
- `internal/claude/agent.go` — `initializeRequest` registers the Stop hook; `newAgent` writes it, so a test agent's input starts with it.
- `internal/server/server.go` — the read loop drops every request without an ID, for plain and MCP connections alike.
- `internal/server/methods.go` — `workspace/create` broadcasts `workspace/created` only for a new workspace.
- `internal/server/mcp.go` — `listSessions`, and `timeLayout` for `read_session`'s timestamps.
- `cmd/ox/web/src/ApprovalRequest.tsx` — `QuestionForm`: checking Other on a single-select question clears its chosen label.
- `cmd/ox/web/src/highlight.ts` — `highlight` writes one styled `<span>` per Shiki token.
- `cmd/ox/web/src/style.css` — `.markdown table` scrolls horizontally on its own.

## Decisions

- **Scope:** tests only. Each fix changes what the test observes. A test gets a second client or page only where the claim in its name needs one.
- **Stop hook registration:** the cheapest observation is the `initialize` request a test agent writes. `TestParkableFollowsActivity` takes the Stop hook's callback ID from that request and sends its `hook_callback` with that ID, so the test fails when `initialize` declares no Stop hook. The `restored schedules` section of `TestLive` needs no change: its `control` session resumes after the restart with `mayHaveSchedules` set, and parks only once a Stop report clears it.
- **ID-less requests:** `serve`'s read loop decides the rule once for both connection kinds, so the side-effect check runs on the plain connection; the MCP connection keeps its `ping` check.
- **Timestamps:** `TestReadSession` writes the date and time literally. The zone offset still comes from the machine's zone, formatted on its own, since the test cannot fix `time.Local`.

## Check your assumptions

None.

## Test plan

Each item names the test, what it observes after the change, and how.

- **`TestConfigureTimeoutStopsProcess`** (`internal/claude/configure_test.go`): a timer for an already acknowledged request leaves the configuration running. Configure three settings (`Model`, `Effort`, `PermissionMode`). Acknowledge the first request, handle `controlTimeout` with the first request's ID (now stale), then acknowledge the second request and assert `reported` holds two settings. Then handle the third request's timeout and keep the rest of the test, with `len(reported) != 2` in place of `!= 0`. A stale timeout that stopped the process would leave the second acknowledgement unreported.
- **`TestParkableFollowsActivity`** (`internal/claude/park_test.go`): decode the first line of `input`, the `initialize` request, and take `request.hooks.Stop[0].hookCallbackIds[0]`; fail if it is missing. The `stop` helper sends that ID as `callback_id`.
- **`TestUnrecordedEndIsRecordedAtLoad`** (`internal/server/history_test.go`): read `h.entries()` after each `New` and before its `Close`, and assert `prompt,tool_use,parked` both times. A repair at shutdown would leave the first read without `parked`.
- **`TestApprovalAnsweredOnce`** (`internal/server/methods_test.go`): connect a second client before `session/send`, and assert it receives the `approval` and the `approval_answered` with the asked request ID.
- **`TestInvalidLines`** (`internal/server/methods_test.go`): on the plain connection, send `workspace/create` without an ID for a fresh temporary directory in place of the ID-less `ping`, then assert `workspace/list` does not list that path. The existing check that no reply arrives stays.
- **`TestSameDirectoryIsOneWorkspace`** (`internal/server/methods_test.go`): after the four adds, drain the client and assert its backlog holds exactly two `workspace/created` notifications, one for `dir` and one for its parent.
- **The archive test's `list_sessions` check** (`internal/server/mcp_test.go:971`): every session but the caller `b` is archived there, so assert `list_sessions` returns exactly `[]` in place of the `strings.Contains` check.
- **`TestReadSession`** (`internal/server/mcp_test.go`): build `at` with `time.Local` and set `stamp := "2026-11-20 09:30:00 " + at.Format("-0700")`, dropping `timeLayout` from the test.
- **"a pending setting is marked pending and holds Send until confirmed"** (`cmd/ox/web/e2e/session.spec.ts`): after `configure.release()`, wait for `aria-busy` to clear before asserting the value `plan`.
- **"choosing an option or Other leaves one answer to a single-select question"** (`session.spec.ts`): start with `red.check()`, then `other.check()`, and assert `red` is unchecked and `next` is disabled before the existing steps.
- **"files of a registered language come back as tokens"** (`cmd/ox/web/highlight.test.mjs`): for each source, collect the `style` attributes of the output's spans and assert at least two distinct ones.
- **"a wide table scrolls inside its reply instead of the transcript"** (`session.spec.ts`): wait for the table to hold `paths.length + 1` rows and its last cell to read the last path. Assert the table's `scrollWidth` exceeds its `clientWidth`, scroll the table to its end, and assert the last row's last cell is in the viewport. The transcript-overflow check stays.
- **"an archived session is archived on every page"** (`session.spec.ts`): open the second page on the session before archiving, then archive from the first page and assert the second shows "This session is archived." and no prompt, without reloading.

## Implementation plan

1. `internal/claude/configure_test.go`: rewrite the start of `TestConfigureTimeoutStopsProcess` as above.
2. `internal/claude/park_test.go`: take the Stop callback ID from the written `initialize` request in `TestParkableFollowsActivity`.
3. `internal/server/history_test.go`: move the history check inside the server loop.
4. `internal/server/methods_test.go`: `TestApprovalAnsweredOnce`, `TestInvalidLines`, `TestSameDirectoryIsOneWorkspace`.
5. `internal/server/mcp_test.go`: the empty `list_sessions` assertion and `TestReadSession`'s literal timestamp.
6. `cmd/ox/web/highlight.test.mjs`: distinct token styles.
7. `cmd/ox/web/e2e/session.spec.ts`: the four specs above.

## Requirement changes

None.
