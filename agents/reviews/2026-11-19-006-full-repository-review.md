# Full-repository review

- **Revision:** `main` at `f2c49fd` ("Let Claude call list_models through ox-mcp"), clean checkout.
- **Scope:** the whole repository: `cmd/` (`ox`, `ox-mcp`, `oxd`, and the web client), `internal/`, `Makefile`, and `docs/`.
- **Method:** three independent reviews, each by Opus 5.5 at high effort:
  - `code-review-and-quality`
  - `code-review-expert`
  - `codebase-design`, which judged the existing modules against its design principles

  Their findings were merged and deduplicated. Each finding kept here was checked against the source; where a reviewer's claim was overstated or a reviewer disagreed, the resolution is noted.
- **Known issues:** `docs/known-issues.md` has no open entries, so nothing below repeats a recorded defect.
- **Checks the reviewers ran:**
  - `make test` passes: lint, web build, and `go test -race ./...`.
  - `go vet` passes.
  - `make e2e` and `make live` were not run. This was a read-only review and nothing was changed.

## Summary

No critical defects were found. Two medium-severity defects stand out:

- A wildcard `-addr` exposes `oxd` to the network with no authentication.
- A resumed session whose first turn is interrupted or fails never parks.

There is also one medium test-design gap: the approval path is tested only live. The rest are low-severity defects, small design cleanups, and two rejected findings.

| # | Finding | Severity | Outcome |
|---|---------|----------|---------|
| 1 | Wildcard `-addr` lets any network client control `oxd` | Medium | Fix |
| 2 | A resumed session whose first turn is interrupted or fails never parks | Medium | Record, or fix after a live check |
| 3 | The fake harness cannot raise approvals, so approvals are tested only live | Medium (tests) | Fix |
| 4 | The same directory added through a symlink or different case makes a second workspace | Low | Fix |
| 5 | An orphaned tool call shows "running" after a reload once the session has restarted | Low | Fix |
| 6 | A failed write while forgetting a lost conversation makes every later send fail | Low | Fix |
| 7 | A session whose harness was dropped from the configuration shows no settings | Low | Fix |
| 8 | A reconnect recreates every transcript's DOM, closing open tool cards | Nit | Fix |
| 9 | Each harness must publish `parked`, a concept the session owns | Low (design) | Fix |
| 10 | `approvedTools` copies the tool list's `readOnlyHint` by hand | Nit (design) | Fix |
| 11 | Session operations are split between `methods.go` and `session.go` | Low (design) | Optional |
| 12 | `ox.tsx` holds Claude tool-payload parsing beside the provider | Nit (design) | Optional |
| 13 | `session/get`'s `prompt` field has no consumer | Nit | Decide |
| — | `oxd` drops in-flight replies when a client half-closes | — | Reject |
| — | MCP forwarder environment variable names are string literals on both sides | — | Reject |

---

## Medium

### 1. Wildcard `-addr` lets any network client control `oxd`

**Found by:** code-review-expert (P2) and code-review-and-quality (minor, filed as a decision). The two are merged here.

**Where:**
- `cmd/ox/main.go:39`: `host, _, err := net.SplitHostPort(*addr)`
- `cmd/ox/main.go:66-79`: `checkHost`
- `cmd/ox/main.go:58-59`: the `/events` and `/rpc` routes

**Evidence:**
- `checkHost` accepts a request whose `Host` header is `localhost`, `127.0.0.1`, `::1`, or the host given to `-addr` (`main.go:73`). Its comment gives the purpose: it defends against DNS rebinding. It does nothing about clients that are not browsers, which can send any `Host` header they like.
- `http.CrossOriginProtection` on `/rpc` lets through requests that have no `Origin` or `Sec-Fetch-Site` header, which is what `curl` sends.
- For `-addr :8080`, `r.host` is `""`. For `-addr 0.0.0.0:8080`, `r.host` is `0.0.0.0`. In both cases `ox` listens on every interface.

**Failure scenario:** the user runs `ox -addr :8080`. Another machine on the network runs `curl -H 'Host: localhost' http://<victim>:8080/events`, which returns a connection ID. It then sends `POST /rpc?connection=<id>` with the same header. That lets it call `workspace/create` with `/`, then `session/create`, `session/send` and `session/approve`, which amounts to arbitrary command execution as the user, billed to their Claude subscription.

**Requirement:** **Local access only** says "Only pages served from this machine (or from the host `ox` was told to serve) can reach `oxd`." A wildcard address names no host, yet it opens `oxd` to every network client. A named non-loopback host is open to the network by the requirement's own wording, so this finding covers only the unspecified address.

**Not in scope of the fix:** on the default `localhost:8080`, another local OS user on a shared machine can reach `oxd` the same way, even though the socket's directory is `0700`. The requirement's "this machine" appears to allow that. If per-user isolation matters, it needs a requirement decision first: the relay would need a token.

**Confidence:** confirmed from source; not exploited live.

**Recommendation:** refuse to start when the `-addr` host is empty or an unspecified address (`net.ParseIP(host).IsUnspecified()`), with an error such as `-addr must name a host`. This costs a few lines in `main` and a relay test, and it matches the AGENTS.md rule "Fail clearly when something unsupported happens."

### 2. A resumed session whose first turn is interrupted or fails never parks

**Found by:** code-review-expert (P2). code-review-and-quality (FYI) called it deliberate and likely to be rejected. **Resolution:** the behavior is deliberate and documented, but it still breaks the Parking requirement in a reachable case, so it is kept.

**Where:**
- `internal/claude/agent.go:73-75`: `mayHaveSchedules = true` on every resume.
- `internal/claude/park.go:9`: `Parkable()` requires `!a.mayHaveSchedules`.
- `internal/claude/park.go:12-16`: only `stopHook` clears it.
- Documented in `docs/protocol.md` ("A scheduling tool call or a resumed conversation keeps the harness active until its next Stop hook schedule report") and in `docs/architecture.md:119-120`.
- Locked in by `TestTurnWithoutStopHookKeepsSchedules` (`internal/claude/park_test.go:41`).

**Evidence:**
- `docs/claude-code.md` §8 records that "A turn that ends by interrupt or error sends no Stop callback (an API error fires `StopFailure`, which has no `session_crons`), and a resumed process reports its restored jobs only when its first turn ends."
- Every send to a parked session is a resume.

**Failure scenario:** a session parks. The user sends a prompt and then presses Stop, or the turn fails on an API error such as a usage limit. The session goes idle with no schedules, but `mayHaveSchedules` stays `true`, so the park timer never starts. The Claude process stays alive until a later turn completes normally or `oxd` restarts.

**Requirement:** **Parking**, "An idle session parks after five minutes". The cost is one idle Claude process per affected session. It loses no work.

**Confidence:** confirmed from source together with the documented Claude Code behavior. Not reproduced live.

**Recommendation:**
- The cause is Claude Code's missing Stop report, so the default outcome is to **record** it in `docs/known-issues.md`. The entry waits on Claude reporting restored schedules without a completed turn.
- A **fix** is possible if a live check confirms one point: every job `--resume` restores comes from a top-level `CronCreate` call already in Ox's history. Then a resumed agent could start with `mayHaveSchedules` false when the history has no such call.
  - `claude-code.md` §8 says resume restores only main-thread recurring and future one-shot jobs, not wakeups or subagent jobs. History records only top-level tool calls (`agent.go`, `ParentToolUseID == ""`), which covers exactly those jobs.
  - The open question is whether a fixed-interval `/loop` job always appears as a `CronCreate` tool call. That needs a live reproduction, as AGENTS.md requires for any fact inferred from tool calls.
  - The fix would pass the decision into `agent.Options` or derive it from the resume, and `TestTurnWithoutStopHookKeepsSchedules` would change.

### 3. The fake harness cannot raise approvals, so approvals are tested only live

**Found by:** codebase-design (medium).

**Where:**
- `internal/fake/fake.go:126-128`: `Approve` always returns `ErrApprovalNotFound`, and the fake never publishes an `approval` update.
- `cmd/ox/web/e2e/session.spec.ts:218-236`: `replayApprovals` and `replay` patch `window.EventSource` to inject a made-up `session/update`.
- The specs that depend on it: `session.spec.ts:238` ("Allow sends the exact numbers…") and `:254` ("AskUserQuestion is answered from a form").
- The only server test of `session/approve` is `internal/server/live_test.go:106`.

**Evidence:**
- `approveSession` (`internal/server/methods.go`) has no test in `make test`. Untested there:
  - routing the answer to the agent;
  - a parked session answering `ErrApprovalNotFound`;
  - pending approvals returning in a `session/get` snapshot after a reconnect.
- The two e2e specs never reach `oxd`'s approval handling. `oxd` never holds the approval, so the specs check only that the page posted `session/approve`. They never see the reply, the `approval_answered` update, or the card disappearing. They also depend on how `connection.ts` uses `EventSource`.

**Cost:** the requirements **Permission requests**, **Allow and Deny**, **Questions from Claude**, **Approvals wait** and **One answer per approval** are covered end to end only by `make live`. That suite needs real Claude, and the AGENTS.md checklist does not run it for every change.

**Recommendation:**
- Let the fake raise an approval when a prompt asks for one, for example text beginning `approve:`:
  - publish `UpdateApproval` with a `can_use_tool`-shaped request copied from a recorded one;
  - hold the turn until `Approve` or `Interrupt`;
  - publish `approval_answered` and finish the turn.
- Then drop the `EventSource` patch from the e2e specs and add a server approve test to `make test`.
- **Cost:** roughly 30–40 lines in `fake.go`.
- **Tension:** AGENTS.md asks to "avoid elaborate mocks and fake runtimes". This extends a fake that already exists. It is earned by five approval requirements that cheaper layers do not cover today.
- **Optional:** a trigger that calls `OnConfigured` would also let the three server tests that put a shell-script `claude` on `PATH` (`methods_test.go:327, 388, 432`) use the fake. Those tests are fair integration tests and can stay.

---

## Low

### 4. The same directory added through a symlink or different case makes a second workspace

**Found by:** code-review-and-quality (minor).

**Where:** `internal/server/methods.go:114` (`path := filepath.Clean(input.Path)`) and `:130` (`if w.path == path`).

**Evidence:** `createWorkspace` treats two paths as the same only when the cleaned strings are equal. It does not resolve symlinks or compare file identity.

**Failure scenario:** on macOS, adding `/tmp/proj` and then `/private/tmp/proj` gives two workspaces for one directory. On case-insensitive APFS, `/Users/kyle/src` and `/Users/Kyle/src` do the same.

**Requirement:** **Adding a workspace**, "adding the same directory again changes nothing."

**Confidence:** confirmed from source.

**Recommendation:** `createWorkspace` already calls `os.Stat` on the new path. Compare that result with `os.SameFile` against an `os.Stat` of each existing workspace's path, which covers both symlinks and case. A workspace path that no longer exists simply fails to match. Add a server test that uses a symlinked temporary directory.

### 5. An orphaned tool call shows "running" after a reload once the session has restarted

**Found by:** code-review-expert (P3).

**Where:**
- `cmd/ox/web/src/ox.tsx:97-99`: `stopTools`.
- `ox.tsx:128` and `:141`: called on a live `parked` update and on an `exited` entry.
- `ox.tsx:313-314`: on load, called only when the session's current state is `parked` ("Stored history has no parked update").
- `internal/claude/agent.go:213`: no `exited` update when `oxd` asked the process to stop.
- `internal/server/history.go:30`: `parked` is not recorded.

**Evidence:** a tool call with no result and no recorded `exited` after it is marked stopped only if the session is parked when the page loads.

**Failure scenarios:**
- `oxd` is killed or crashes mid-turn, which includes `make dev` halting on a race report, so nothing is recorded. After restart the session is parked and the tool shows "stopped". The user then sends a new prompt. Any page that now reloads shows the old tool with the "running" pulse, and it stays that way. A page that stayed open shows "stopped".
- A graceful `oxd` shutdown during a turn Claude started itself (for example a scheduled one) while a tool runs. `stopping` suppresses the `exited` update, so the result is the same.

**Requirements:** **One transcript** ("the same on every page and after a reload") and **Tool calls**.

**Confidence:** confirmed from source.

**Recommendation:** derive "stopped" from the transcript as well as the current state. A tool entry without a result that is followed by a later `prompt` entry belongs to a turn that has ended and is stopped. Keep the `parked` rule for the tail. This is a small change in `load`, plus an e2e or unit check.

### 6. A failed write while forgetting a lost conversation makes every later send fail

**Found by:** code-review-expert (P3).

**Where:** `internal/server/session.go:199-207`. `forgetConversation` returns the store error before clearing `sess.resumeToken` and before publishing the notice.

**Failure scenario:** Claude no longer has the conversation, and the database write fails at the same moment. Each later send resumes the same token, gets `ErrNoConversation` again, and fails with the database error. The session stays unusable until the database recovers or `oxd` restarts.

**Requirements:**
- **Lost conversation:** "the next send starts a new conversation".
- **Database failures:** "Saved state … persists only while Ox can write its database. `oxd` logs a failed write, and the page does not show it."

Here the failed write is shown to the user and blocks sending.

**Confidence:** confirmed from source. This follows the store-before-memory rule in `docs/architecture.md:58-61`, which is why it has not been treated as a defect. `markResumable` and `adopt` are already exceptions to that rule for the same reason.

**Recommendation:** clear the token in memory, log the failed write, and publish the notice. `markResumable` does the same. After a restart the stored token fails to resume again and is forgotten again, so the session recovers on its own. Add "a resume token Claude no longer has" to the exceptions in `docs/architecture.md:58-61`.

### 7. A session whose harness was dropped from the configuration shows no settings

**Found by:** code-review-and-quality (nit).

**Where:** `cmd/ox/web/src/SessionView.tsx:165`, `<Show when={ox.state.harnesses[props.session.harness]}>`. It wraps both the model picker and the permission mode selector.

**Evidence:** when the configuration no longer offers the session's harness, the settings row is not rendered at all. `Select` already shows retired values (`SettingsFields.tsx:188-191`).

**Requirements:** **Retired settings** ("A model or permission mode the configuration no longer offers still shows as the session's setting") and **Settings shown are settings used**.

**Confidence:** confirmed from source. It is reachable only when a configuration drops a harness that still has sessions, for example `fake` sessions left over from development.

**Recommendation:** when the harness is missing, render the stored `props.session.settings` read-only.

### 8. A reconnect recreates every transcript's DOM, closing open tool cards

**Found by:** code-review-and-quality (nit).

**Where:**
- `cmd/ox/web/src/ox.tsx:324`: `loaded.entries = session.entries`, an array of new objects.
- `cmd/ox/web/src/SessionView.tsx:113`: `<For each={props.entries}>`.
- `ox.tsx:287-288`: the comment says `load` "keeps its view and the view's state".

**Evidence:** Solid 2.0's `For` is keyed by item identity (`cmd/ox/web/AGENTS.md`: "`mapArray` … reuses entries for unchanged items"). Every entry object is new, so every entry is rendered again. Approvals avoid this with `reconcile(…, "id")` on the next line, but entries do not.

**Failure scenario:** the user expands a tool card (an uncontrolled `<details>`). `oxd` restarts or the connection drops, and on reconnect every expanded card in every loaded session collapses and any text selection is lost. This contradicts the comment and the architecture doc's "updating loaded sessions in place so their views keep their state."

**Confidence:** confirmed from source; not observed in a browser.

**Recommendation:** keep the existing entry objects for the prefix that matches. Entries have no stable ID, but on a reconnect the history only grows at the end. Alternatively, merge with `reconcile` by index. Either way the fix stays inside `load`.

---

## Design

These come from the `codebase-design` assessment, and each was checked against the code. None is a defect.

### 9. Each harness must publish `parked`, a concept the session owns

**Where:**
- `internal/agent/agent.go:44-50`: the `Stop`/`Exited` contract, "has published its last update, parked".
- `internal/claude/agent.go:216` and `internal/fake/fake.go:159`: both adapters publish `UpdateParked`.
- `internal/server/methods.go` (`getSession`): the server builds the parked snapshot itself.
- `internal/server/session.go:70-73,129-135`: where the session drops its agent.

**Evidence:** "parked" means that the session has no process, which is the session's state. Every adapter must nonetheless remember to publish it as its last update, so the interface carries a duty that is not about the harness.

**Recommendation:** the session publishes `UpdateParked` where it sets `sess.agent = nil`: in `run` after `Exited()`, and in `stop()`. Order is kept because this runs on the same goroutine, straight after `Handle` or `Stop` returns.
- Remove the clause from the `agent.Agent` contract and the two adapter calls.
- Move the assertions at `internal/claude/prompt_test.go:193,201` to a server test.
- **Cost:** about 10 lines. It narrows `internal/agent`, as AGENTS.md asks ("changes only for what a real harness needs").

### 10. `approvedTools` copies the tool list's `readOnlyHint` by hand

**Where:** `internal/server/mcp.go:107` (`readOnlyHint: true` on `list_models`) and `:110-112` (`var approvedTools = []string{"list_models"}`).

**Evidence:** two facts must agree:
- a tool runs without an approval prompt;
- the tool is annotated read-only, which is what stops plan mode from prompting.

Today they are written in two places.

**Recommendation:** derive `approvedTools` from the tool list's `readOnlyHint`, in about 5 lines. Low value while there is only one tool, so do it when a second tool is added, or now because it costs almost nothing.

### 11. Session operations are split between `methods.go` and `session.go`

**Where:** the closures in `internal/server/methods.go` read and write the session goroutine's fields directly: `sess.agent`, `sess.settings`, `sess.resumeToken` and `sess.ended`. Each handler decides on its own what a parked session (`sess.agent == nil`) means:

| Handler | Parked session |
|---------|----------------|
| get | builds a parked snapshot |
| send | starts the agent |
| approve | fails with `ErrApprovalNotFound` |
| interrupt | succeeds |
| configure | stores directly |
| destroy | skips stop |

The storage rule (`docs/architecture.md:58-61`) is applied in both files.

**Cost of keeping it:** understanding or changing what a session does means reading both files, and every new operation has to work out the parked case and the storage rule again.

**Recommendation (optional):** move each operation body into a `session` method that takes the decoded input and `reply`, such as `sess.send(text, reply)`, and leave decoding, configuration checks and `onSession` in `methods.go`. This moves no state and keeps responses queued by the state owner.
- **Cost:** a mechanical move of about 150 lines. The tests go through the protocol and need no change.
- It improves locality only, so skip it if churn is unwelcome.

### 12. `ox.tsx` holds Claude tool-payload parsing beside the provider

**Where:** `cmd/ox/web/src/ox.tsx:41-77`: `toolSubject`, `planOf`, `Question`, `questionsOf` and `toolInputJSON`. They are used by `ApprovalRequest.tsx` and `SessionView.tsx`.

**Evidence:** these helpers read Claude's `ExitPlanMode` and `AskUserQuestion` payloads, which have nothing to do with the provider's job (connection, store and controls). The web table in `docs/architecture.md` doesn't mention them.

**Recommendation (optional):** move them to their own file next to `ApprovalRequest.tsx`, about a 40-line move, and add one row to the architecture table.

### 13. `session/get`'s `prompt` field has no consumer

**Found by:** code-review-expert (removal candidate).

**Where:**
- `internal/agent/agent.go:136,140-142`: `Snapshot.Prompt` and `PromptSnapshot`.
- `internal/claude/agent.go:192-194`: builds it.
- `docs/protocol.md`, under `session/get`: documents it.
- `cmd/ox/web/src/protocol.ts:72`: only declares its type; nothing in `cmd/ox/web/src` reads it.

**Recommendation:** decide whether the protocol keeps it for other clients. If not, remove it from `agent.Snapshot`, the Claude and fake adapters, and `docs/protocol.md`. Otherwise leave it.

---

## Rejected

- **`oxd` drops in-flight replies when a client half-closes** (code-review-and-quality, nit).
  - **Where:** `cmd/ox-mcp/main.go:57` calls `CloseWrite()` after stdin reaches EOF. On EOF, `internal/server/server.go:90-106` returns and its deferred `c.close()` closes the connection while `handleMCP` goroutines may still be queuing replies.
  - **Why rejected:** this is accurate from source, but stdin reaches EOF only when Claude closes it, which happens when Claude is exiting and no longer waits for replies. No user-visible failure is reachable, and fixing it would add wait machinery to `serveConn`.
- **The MCP forwarder's environment variable names are string literals on both sides** (codebase-design, low).
  - **Where:** `OX_SOCKET` and `OX_SESSION_ID` in `internal/server/session.go:105` and `cmd/ox-mcp/main.go:17`.
  - **Why rejected:** a rename is rare, the live test exercises both sides together, and a shared constant would bring in a package dependency between the two binaries. Keeping the literals costs less.

## Keep as they are

The design assessment found these modules sound. No churn is proposed for them:

- **`internal/agent`:** a real seam with two adapters. Its `Events`/`Handle` split is what lets the session goroutine own all state.
- **`internal/claude`:** a deep module. Its internal seam (`newAgent` takes the writer, the events and the process) supports thorough tests with no mocks. The phase machine is divided by concern: prompt, configure, approval and park.
- **`internal/jsonrpc`, `internal/store`, `internal/paths`, `internal/config`:** small and direct, and rightly shallow.
- **`internal/server/conn.go` and `history.go`:** each does one job behind a small interface. `history` is tested through `record` and `entries` against real SQLite.
- **`cmd/ox` relay and `cmd/ox-mcp`:** minimal, and tested at the process or HTTP level.
- **`cmd/oxd` `checkConfig`:** it handles `claude` as a special case, which is right while only one harness has constraints.
- **Web `connection.ts` and `createSessionControl`:** deep modules with small interfaces. Components reach `oxd` only through `useOx`.

The code-review-expert reviewer also checked and found sound:

- session goroutine ownership and lock order (`mu` before `connsMu`, nothing posted while `mu` is held);
- `Close` racing a send or a destroy;
- `mcp/attach` ordering;
- store cascade deletes;
- Claude's start, initialize and exit paths, including their timeouts;
- configure timeouts and `ErrExited`;
- approval validation;
- Markdown XSS handling;
- the order of reload and reconnect in `ox.tsx`;
- the draft and recovery flow.
