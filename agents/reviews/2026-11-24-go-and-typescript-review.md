# Comprehensive Go and TypeScript Code Review

- **Revision:** `main` at `791f405890012125ee88798cfeb8f66b4751f755`.
- **Scope:** All Go and TypeScript production and test code across `cmd/` (`ox`, `ox-mcp`, `oxd`, `ox/web/src/`, `ox/web/e2e/`, `ox/web/*.mjs`) and `internal/` (`agent`, `children`, `claude`, `config`, `fake`, `jsonrpc`, `paths`, `server`, `store`, `ur`), checked against `AGENTS.md`, `cmd/ox/web/AGENTS.md`, `docs/architecture.md`, `docs/protocol.md`, `docs/functional-requirements.md`, and `docs/harnesses/claude-code.md`.
- **Open `bug` issues skipped:** Read all 7 open `bug` issues before reviewing and excluded them from findings:
  - `#5` (resumed session whose first turn is interrupted or fails never parks)
  - `#6` (plan written to a file shows no plan in its tool call)
  - `#33` (Claude stopping reading stdin blocks session and others)
  - `#34` (Stop does nothing while resumed Claude runs a startup turn)
  - `#84` (relay 503 can leave Send disabled after reconnect)
  - `#85` (child report can reach its parent while a sibling is still running)
  - `#86` (usage countdown freezes when the page disconnects)
- **Method:** Five parallel line-by-line review streams (Go core/daemons, Go server, Go harnesses, TypeScript state/protocol/utilities, and TypeScript UI/e2e specs). Every kept finding was independently verified against the source code and, where applicable, reproduced in Node. This is a read-only review; no source files were modified.
- **Checks run:**
  - `go vet ./...`: pass.
  - `bin/golangci-lint-2.15.0 run ./...`: pass (0 issues).
  - `go test -race` on `internal/agent`, `internal/claude`, `internal/config`, `internal/store`, `cmd/ox`: pass (tests requiring Unix/TCP sockets or `git` could not run inside the sandbox).
  - `tsc -p cmd/ox/web/tsconfig.json --noEmit` and `tsc -p cmd/ox/web/e2e/tsconfig.json`: pass (0 errors).
  - `biome check .` in `cmd/ox/web`: pass (0 errors, 4 CSS specificity warnings in `src/style.css`).
  - `node --test`: `highlight.test.mjs`, `id.test.mjs`, `toolInput.test.mjs` pass; 24/25 pass overall (1 `stack.test.mjs` subtest hit sandbox `spawn EPERM`).
  - `make e2e` and `make live`: not run.

---

## Summary

The codebase is remarkably clean, disciplined, and well-structured: state ownership between `Server.mu` and per-session goroutines is clear, protocol parity between Go and TypeScript is exact, SQLite persistence and locking are solid, and Biome/golangci-lint/TypeScript checks pass cleanly.

Across the full 27k-line Go and TypeScript codebase, this review found **6 High**, **18 Medium**, **12 Low**, and **1 grouped Nit** actionable findings, plus **4 Rejected** candidates documented below.

| ID | Severity | Area | Category | Action | Title |
| -- | -------- | ---- | -------- | ------ | ----- |
| F1 | High | Go (`server`) | Security / Correctness | Fix | `workspace/list_folder` blocks forever on a FIFO (named pipe) |
| F2 | High | Go (`server`) | Correctness / Requirements | Fix | `session/archive` succeeds during an in-flight `session/configure` and broadcasts `session/configured` after `session/archived` |
| F3 | High | Go (`server`) | Correctness / Requirements | Fix | Deleting a running child session generates and delivers `"Exited: Claude was stopped"` to its parent |
| F4 | High | Go (`claude`) | Correctness | Fix | Claude `commandCancelled` leaves `Agent.phase` stuck in `*prompting` after `Tracker` transitions to `idle` |
| F5 | High | Go (`ur`) | Correctness | Fix | Ur stripping earlier turns' `ReasoningDetails` emits empty `{"role":"assistant"}` messages that permanently poison future requests |
| F6 | High | TS (`web`) | Correctness | Fix | Global `rawJSON` reviver turns small protocol floats (such as `used_percent`) into null-prototype objects and crashes `Usage` |
| F7 | Medium | Go (`server`) | Requirements / Correctness | Fix | `addWorktree` creates the `-worktrees` directory inside the user's repository before rejecting it |
| F8 | Medium | Go (`claude`) | Correctness / Requirements | Fix | Claude `sendControl` write failure returns a raw pipe error instead of falling back to `ErrExited` |
| F9 | Medium | Go (`ur`) | Correctness | Fix | Ur `appendReasoning` corrupts distinct reasoning blocks that share an index (including omitted `index: 0`) |
| F10 | Medium | Go (`server`) | Correctness | Fix | `leadingLine` / `cleanTitle` mishandle CRLF (`\r\n`), leaving `\r` in labels and bypassing quote stripping |
| F11 | Medium | Go (`server`) | Correctness | Fix | `sess.run()` does not arm the park timer before its first `select` |
| F12 | Medium | Go (`server`) | Design / Correctness | Fix | `addChild` can leave a child session published and running after returning an error from `acceptMessage` |
| F13 | Medium | Go (`cmd/ox`) | Correctness | Fix | Case-sensitive `Host` check rejects mixed-case configured hosts and uppercase loopback names |
| F14 | Medium | Go (`config`) | Tests | Fix | Hollow test: `TestChoiceIDsAndNames` omits required `"titling"`, so all 12 cases fail before choice validation runs |
| F15 | Medium | Docs / Go | Requirements | Fix | `docs/protocol.md` omits required `titling` field in `Configuration` and `harness/list` |
| F16 | Medium | TS (`web`) | Reactivity / Correctness | Fix | Solid 2.0 deferred signal flush makes `reconcilePrompt()` read stale `phase: "sending"` inside `post()` |
| F17 | Medium | TS (`web`) | Correctness / Requirements | Fix | `stopping` is never reset on disconnect/reconnect, hiding Stop for a new turn if `idle` was missed |
| F18 | Medium | TS (`web`) | Correctness / Design | Fix | Concurrent ancestor/descendant list requests can display an archived parent with an unarchived child |
| F19 | Medium | TS (`web`) | Reactivity / Accessibility | Fix | Unkeyed `<For each={props.items}>` in `Menu` destroys and recreates menu items on any store update |
| F20 | Medium | TS (`web`) | Reactivity / Correctness | Fix | `Effort` slider's `dragged` signal gets permanently stuck when `onChange` does not fire |
| F21 | Medium | TS (`web`) | Correctness / Requirements | Fix | Local attachment errors persist after valid attachments and leak as a cross-view "Reopen draft" alert banner |
| F22 | Medium | TS (`web`) | Security / Requirements | Fix | `Markdown.tsx` relies on `markdown-it`'s denylist `validateLink`, permitting arbitrary custom URI schemes |
| F23 | Medium | TS (`web`) | Correctness | Fix | `ApprovalRequest` lacks repeat-key and in-flight submission guards, enabling duplicate `session/approve` RPCs |
| F24 | Medium | TS (`e2e`) | Tests | Fix | `e2e/services.ts` leaks child processes and temp directories on failure and misses signal/relay crashes |
| F25 | Low | Go (`cmd/ox`) | Requirements / Design | Fix | `GET /` bypasses `checkHost`, contradicting **Web access** |
| F26 | Low | Go (`cmd/oxd`) | Correctness / Concurrency | Fix | `listen` misreports `os.Lstat` errors (including concurrent shutdown `ENOENT`) as `"exists and is not a socket"` |
| F27 | Low | Go (`agent`) | Design / Correctness | Fix | `agent.Tracker` retains taken/dropped `Input` slices (including images) in backing arrays and exposes transient `idle` in `Take()` |
| F28 | Low | Go (`ur`) | Design / Correctness | Fix | `ur.Agent.Send` and `Configure` do not check `a.exited` |
| F29 | Low | Go (`ur`) | Correctness | Fix | Ur SSE parser requires a space after `data:` and misformats error chunks with empty `message` |
| F30 | Low | Go (`claude`) | Correctness / Design | Fix | Claude `controlRequest` decodes `hook_callback` and `can_use_tool` into one struct with colliding `input` fields |
| F31 | Low | Go (`claude`) | Security | Fix | Claude child processes inherit `OPENROUTER_API_KEY` from `oxd` |
| F32 | Low | Go (`server`) | Correctness / Lifecycle | Fix | Small server lifecycle and edge-case defects (`sendPrompt` order, `labelFrom`, `New` goroutine leak, `onSession` close race, `within("/")`) |
| F33 | Low | TS (`web`) | Reactivity / Design | Fix | Client lifecycle and owner hazards (`connection` init order, `poll.ts` sync throw wedge, `control(id)` owner, `pending.set` order) |
| F34 | Low | TS (`web`) | Reactivity / Accessibility | Fix | UI reactivity, accessibility, and defensive guards (`ModelPicker`, `Menu`, offline delete/remove, eager Markdown highlight, `FileView` tablist, `Usage` bounds, Safari IME) |
| F35 | Low | JS (`stack`) | Correctness | Fix | `stack.mjs` misses retiring processes on second `SIGINT`, leaks signal listeners, and leaves in-flight build running on `stop()` |
| F36 | Low | Go & TS | Tests | Fix | Test coverage gaps and test hygiene violations across Go unit tests, Node unit tests, and Playwright specs |
| F37 | Nit | Go & TS | Conventions / Cleanup | Fix | Minor cleanup items (`fake.thoughts`, `relay.events`, `logStderr` UTF-8, prop `untrack`, dead `autofocus`, doc drift) |
| R1 | — | Go (`server`) | Lifecycle | Reject | `Server.Close` does not wait for in-progress `session/destroy` worktree removals |
| R2 | — | Go (`server`) | Design | Reject | `createWorkspace` calls `os.Stat` under `Server.mu` |
| R3 | — | Go (`ur`) | Concurrency | Reject | `shell.wait()` calls `killGroup` after `s.cmd.Wait()` reaps the leader PID |
| R4 | — | TS (`e2e`) | Conventions | Reject | E2E fault-injection helpers monkey-patch `EventSource` and `document.visibilityState` |

---

## High

### F1. `workspace/list_folder` blocks forever on a FIFO (named pipe)

High · Security / Correctness · Fix · `internal/server/files.go:29-46`

`readFile` (`internal/server/files.go:72-89`) explicitly calls `root.Stat(file)` and checks `info.Mode().IsRegular()` before calling `root.Open(file)`, with a comment noting *"so a FIFO or device cannot block the read"*. `listFolder` does not stat `folder` before calling `root.Open(folder)` at line 38.

On Unix, `os.Root.Open` opens the target with `O_RDONLY` without `O_NONBLOCK` or `O_DIRECTORY`. If a client passes a path to a FIFO inside the workspace or session directory (such as the `pipe` FIFO already created in `TestWorkspaceFiles` at `internal/server/methods_test.go:1632`), `openat` blocks indefinitely in the kernel waiting for a writer, hanging the RPC goroutine permanently and never replying.

**Action:** Stat `folder` before opening it (matching `readFile`):
```go
info, err := root.Stat(folder)
if err != nil {
	return nil, err
}
if !info.IsDir() {
	return nil, errors.New("not a directory")
}
```
Add a case in `TestWorkspaceFiles` calling `workspace/list_folder` on `"pipe"`.

---

### F2. `session/archive` succeeds during an in-flight `session/configure` and broadcasts `session/configured` after `session/archived`

High · Correctness / Requirements · Fix · `internal/server/session.go:322-326`, `366-395`; `internal/server/methods.go:693-705`

`sess.free()` (`session.go:323-326`) checks only `sess.snapshot().State`:
```go
func (sess *session) free() bool {
	state := sess.snapshot().State
	return state == agent.StateIdle || state == agent.StateBackground || state == agent.StateParked
}
```
While a `session/configure` request is in flight with no queued prompt, both `claude.Agent` (`phase` is `*configuring` or `timedOut`) and `fake.Agent` (`a.config != nil`) still report `tracker.State() == "idle"`, so `sess.free()` returns `true`.

Failure sequence:
1. Client 1 sends `session/configure` to an idle session `S` (or a child of `P`).
2. While `configure` waits for Claude's `control_response`, Client 2 sends `session/archive` on `S` (or `P`).
3. `archive()` sees `free() == true`, sets and stores `archived = true`, broadcasts `session/archived`, and calls `sess.stop()`.
4. `sess.stop()` ends the in-flight configuration by invoking its callback with `agent.ErrExited`.
5. `configureSession`'s callback (`methods.go:694-698`) treats `ErrExited` as success, calls `sess.adopt(input.Apply(sess.settings))` — persisting settings to SQLite, broadcasting `session/configured` **after** `session/archived`, and returning `{}` on an already-archived session.

This violates **Archiving a session** and **When settings can change** (`docs/functional-requirements.md`), as well as `docs/protocol.md` (*"Configuration reserves it until completion"*). `TestArchive` (`internal/server/mcp_test.go:977-992`) missed this because it queued a prompt (`"held"`) alongside `configure`, flipping `Snapshot().State` to `"running"`.

**Action:** Track an in-flight configuration on `session` (e.g., `configuring bool` owned by `sess.run`, set before `sess.agent.Configure(...)` and cleared in the `done` callback) and have `sess.free()` return `false` while `sess.configuring` is true so `session/archive` returns `-32002 session is busy`. Add a test in `TestArchive` archiving a configuring session with no queued prompt.

---

### F3. Deleting a running child session generates and delivers `"Exited: Claude was stopped"` to its parent

High · Correctness / Requirements · Fix · `internal/server/session.go:525-545`, `610-615`, `642-644`, `683-711`

In `session.destroy` (`session.go:526-531`):
```go
if sess.agent != nil {
	sess.stop()
}
exiting := sess.exiting
sess.exiting = nil
sess.deleting = &deleting{reply: reply}
```
When the user destroys a running child session `C`:
1. `C.stop()` runs **before** `C.deleting` is set and **before** `C` is removed from `s.sessions`.
2. `C.agent.Stop()` publishes `UpdateExited{Error: "Claude was stopped"}` (or `"stopped"` in Ur/Fake).
3. `C.update()` calls `C.report(UpdateExited)` (`session.go:642-644`), appending `"Exited: Claude was stopped"` to `C.toParent.reports`.
4. `C.parked()` publishes `UpdateParked`, which sets `C.state = "parked"` and calls `C.pingFamily()` -> `go parent.post(parent.deliverReports)`.
5. While `C`'s delete goroutine waits on `<-reaped` (and `removeWorktree`), `C`'s goroutine stays alive. `parent.deliverReports()` calls `ask(C, ...)`, collects `"Exited: Claude was stopped"` (plus any earlier undelivered reports from `C`), and delivers them to `parent` — even waking a parked parent for a child the user just deleted.

This violates `docs/protocol.md:820` (*"as are reports not delivered when `oxd` stops or their child is deleted"*).

**Action:** Set `sess.deleting` before calling `sess.stop()` in `destroy()` and clear `sess.toParent.reports = nil` (when `sess.toParent != nil`), guarding `sess.report()` and `deliverReports`'s `ask(c, ...)` when `deleting != nil`. Keep `pingFamily()` firing when `C` stops running/parks so any waiting *sibling*'s reports are not stranded. Add a test in `TestChildReports` destroying a running child and checking that no report from it reaches the parent while a waiting sibling's report still delivers.

---

### F4. Claude `commandCancelled` leaves `Agent.phase` stuck in `*prompting` after `Tracker` transitions to `idle`

High · Correctness · Fix · `internal/claude/prompt.go:139-145` (with `agent.go:298-301, 388-395`, `configure.go:19-23`, `park.go:8-10`)

`commandCancelled` drops the written untaken input and tries to write the next one without checking whether `a.tracker.Idle()` is now true:
```go
func (a *Agent) commandCancelled(id string) {
	if a.waiting(id) == nil {
		return
	}
	a.tracker.DropFirst()
	a.writeNext()
}
```
Two concrete sequences reach an illegal state (`a.tracker.Idle() == true`, `Snapshot().State == "idle"`, `Snapshot().Queued == []`, yet `a.phase` is still `*prompting`):
1. Turn 1 ends and Claude emits `session_state_changed: idle` while a steered/queued prompt `p2` is untaken (`tracker.Len() == 1`). `sessionState("idle")` (`prompt.go:152`) skips `a.ready()` because `a.tracker.Len() != 0`, and sets `tracker.reported = StateIdle`. Then `Interrupt()` cancels `p2` and Claude emits `command_lifecycle{command_uuid: p2, state: "cancelled"}`. `DropFirst()` empties the queue and publishes `UpdateState{State: "idle"}`. No second `session_state_changed: idle` arrives.
2. Similarly, `p1` is written to an idle Claude (`tracker.reported == StateIdle`) and cancelled before Claude reports `running`, or `writeNext()` fails to write a subsequent held input and drops all inputs while `tracker.reported == StateIdle`.

Once stuck in `*prompting` while `tracker.Idle()`:
- `a.idle()` returns `false`, so every `session/configure` fails with `ErrBusy` despite the session showing as `idle`.
- `Parkable()` returns `false`, so the idle session never parks.
- `Stop()` (including via `session/archive`, which sees `Snapshot().State == "idle"`) hits `a.phase.(*prompting)` (`agent.go:391`) and publishes a spurious `exited` error `"Claude was stopped"` tagged with the cancelled `messageID`.

**Action:** In `commandCancelled`, after `a.writeNext()`, release the phase if the tracker is idle:
```go
if a.tracker.Idle() {
	a.phase = available{}
}
```
Add a unit test in `internal/claude/prompt_test.go` covering `command_lifecycle cancelled` while `tracker.reported == StateIdle` and asserting `Parkable()` and `Configure()` succeed.

---

### F5. Ur stripping earlier turns' `ReasoningDetails` emits empty `{"role":"assistant"}` messages that permanently poison future requests

High · Correctness · Fix · `internal/ur/ur.go:436-440`, `459-489` (with `internal/ur/stream.go:20-26`)

Each streamed reasoning delta is persisted as an assistant entry (`ur.go:257`):
```go
a.save(message{Role: "assistant", Content: content{Text: e.text}, ReasoningDetails: e.reasoning})
```
On any later turn, `body()` strips `ReasoningDetails` from all earlier turns (`ur.go:436-439`):
```go
history := slices.Clone(a.history)
for i := range a.turn.start {
	history[i].ReasoningDetails = nil
}
```
If turn 1 on a reasoning model (including Ur's built-in DeepSeek model) streams reasoning deltas and then ends before any text or tool call arrives — via user `Interrupt()`, a mid-stream HTTP error, an `oxd` restart, or hitting max tokens during reasoning — turn 2 clears `ReasoningDetails` on turn 1's entries. Because `message.Content` has `omitzero`, `conversation(history)` emits `{"role":"assistant"}` with no `content`, `tool_calls`, or `reasoning_details`. OpenRouter/OpenAI-compatible providers reject empty assistant messages with HTTP 400, permanently breaking every subsequent turn in that conversation (including across restarts).

**Action:** In `conversation(history)` (`internal/ur/ur.go:459-489`), skip any `assistant` entry that has empty `Content.Text`, no `Content.Images`, no `ReasoningDetails`, and no `ToolCalls`. Add a unit test in `internal/ur/ur_test.go` interrupting/failing a reasoning-only turn and asserting the next turn sends no empty assistant message.

---

### F6. Global `rawJSON` reviver turns small protocol floats (such as `used_percent`) into null-prototype objects and crashes `Usage` rendering

High · Correctness · Fix · `cmd/ox/web/src/connection.ts:78-84` (manifests in `cmd/ox/web/src/Usage.tsx:68`)

`connection.ts` parses every incoming SSE message with a global reviver meant only for tool/approval inputs:
```ts
const message = parse(event.data, (_key, value, context?: { source?: string }) => {
  if (typeof value === "number" && context?.source && String(value) !== context.source) {
    return rawJSON(context.source);
  }
  return value;
});
```
Go's `encoding/json` serializes floats in `(0, 1e-6)` with a two-digit negative exponent (`5e-07`, `1e-07`) and negative zero as `-0`, whereas JavaScript's `String(value)` formats them as `"5e-7"`, `"1e-7"`, and `"0"`. Whenever `String(value) !== context.source`, `@ungap/raw-json` replaces the number with an `Object.create(null)` wrapper `{ rawJSON: "5e-07" }`.

In `internal/claude/messages.go:71`, `UsageWindow.UsedPercent` is `w.Utilization * 100` (`float64`). Any tiny nonzero utilization `< 1e-8` serializes as e.g. `5e-07`, arrives as a `rawJSON` object, and crashes `Usage.tsx:68` (`Math.round(props.window.used_percent)`) with `TypeError: Cannot convert object to primitive value` (reproduced directly with Node and `@ungap/raw-json`), tearing down the session view.

**Action:** Preserve `rawJSON` tokens only for opaque tool/approval inputs (or coerce `isRawJSON(v) ? Number(v.rawJSON) : v` on known numeric protocol fields such as `id`, `error.code`, `used_percent`, and `interval_seconds` before dispatching). Add a unit/regression test covering scientific-notation `used_percent` (e.g. `5e-07`).

---

## Medium

### F7. `addWorktree` creates the `-worktrees` directory inside the user's repository before rejecting it

Medium · Requirements / Correctness · Fix · `internal/server/worktree.go:87-102`

`addWorktree` runs `os.MkdirAll(worktrees, 0o700)` on line 87 before resolving `listed` and checking `within(physical, path)` on lines 98-102. When `-worktrees` points to a non-existent path inside the repository (e.g., `<repo>/worktrees`), `addWorktree` creates that directory inside the user's repo and leaves it behind when returning the refusal error — violating **Ox's own files** (*"In the user's repositories, Ox writes only the worktrees and branches of worktree sessions"*). `TestWorktreeCreationFails` (`worktree_test.go:289, 301`) only checked `os.ReadDir(worktrees)` was empty rather than asserting the directory did not exist.

**Action:** Resolve the nearest existing ancestor of `worktrees` via `filepath.EvalSymlinks` (re-appending the remaining segments) and check `within(..., path)` against `listed` before calling `os.MkdirAll`. Assert in `TestWorktreeCreationFails` that `<repo>/worktrees` does not exist after refusal.

---

### F8. Claude `sendControl` write failure returns a raw pipe error instead of falling back to `ErrExited`

Medium · Correctness / Requirements · Fix · `internal/claude/configure.go:53-60` (with `internal/server/methods.go:693-704`)

`docs/protocol.md:403-406` states that if Claude exits before acknowledging a configuration control request, the session adopts the requested settings for the next process and `session/configure` succeeds (`methods.go:694-698` checks `errors.Is(err, agent.ErrExited)`).

In `sendControl` (`configure.go:55-58`), however, a failed stdin write (e.g., `broken pipe` when Claude exits between step 1 and step 2 of a multi-setting configure, before `processExit` arrives) immediately calls `a.endConfiguration(c.done, c.fail(err))` with the raw pipe error. `methods.go` fails `session/configure` with `-32000 ... broken pipe` instead of adopting the settings, and `endConfiguration` calls `a.ready()` -> `a.writeNext()`, hitting broken pipe a second time on any held prompt.

**Action:** On write failure in `sendControl`, log via `a.onError(c.fail(err))`, terminate `a.proc` if non-nil, and transition to `timedOut{c.done, c.fail(agent.ErrExited)}` (matching `controlTimedOut` and `Start`), letting `processExit` finish the configuration with `agent.ErrExited`. Add a broken-pipe test in `internal/claude/configure_test.go`.

---

### F9. Ur `appendReasoning` corrupts distinct reasoning blocks that share an index (including omitted `index: 0`)

Medium · Correctness · Fix · `internal/ur/stream.go:82-91`, `107-124`

`appendReasoning` merges fragment `f` into `details[last]` solely on `details[last].Index == f.Index`, where `Index` is a plain `int` defaulting to `0`. If a provider omits `index` or emits a `reasoning.summary`/`reasoning.text` block and a `reasoning.encrypted` block both with `index: 0` (or distinct `id`s), `appendReasoning` concatenates `f.Data` into the preceding text/summary block while preserving the first block's `Type` and `ID`. That corrupted `reasoning_details` entry is then sent back to OpenRouter on the next request in the tool loop.

**Action:** Only merge `f` into `details[last]` when `Index` matches AND both `Type` (`d.Type == "" || f.Type == "" || d.Type == f.Type`) and `ID` (`d.ID == "" || f.ID == "" || d.ID == f.ID`) are compatible. Add a unit test in `internal/ur/ur_test.go`.

---

### F10. `leadingLine` / `cleanTitle` mishandle CRLF (`\r\n`), leaving `\r` in labels and bypassing quote stripping

Medium · Correctness · Fix · `internal/server/session.go:114-117`, `131-137`

`leadingLine` cuts on `"\n"` without trimming the returned line:
```go
func leadingLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}
```
1. A multi-line prompt with Windows CRLF endings (`"Fix bug\r\nDetails"`) retains a trailing `\r` in `sess.firstLine` and `session/labeled`.
2. A titling reply with CRLF (`"\"Fix bug\"\r\nextra"`) ends with `'\r'` rather than `'"'`, so `cleanTitle` (line 133) skips quote stripping and stores literal quotes.

**Action:** Return `strings.TrimSpace(line)` from `leadingLine` and add CRLF test cases.

---

### F11. `sess.run()` does not arm the park timer before its first `select`

Medium · Correctness · Fix · `internal/server/session.go:185-207`; `internal/server/mcp.go:457-458`

`sess.run()` calls `sess.armPark()` only at the bottom of the loop (`session.go:206`), after handling an op, event, or timer tick. For a child session created in `addChild` (`mcp.go:457-458`), `sess.acceptMessage(...)` runs before `go sess.run()`. If `Send` finishes the turn synchronously and leaves the agent `Parkable()` — as `ur.Agent.Send` does when `a.save` or `a.body` (`AGENTS.md` read error) fails (`ur.go:336-345`) — `sess.run()` enters its first `select` with `sess.agent != nil`, `Parkable() == true`, `sess.parkTimer == nil`, and no pending events. Because the parent is busy during `create_session`, `deliverReports()` returns early without posting to the child, leaving the child unparked indefinitely until some unrelated op arrives.

**Action:** Call `sess.armPark()` at the top of the `for !sess.ended` loop (before checking `sess.parkTimer`).

---

### F12. `addChild` can leave a child session published and running after returning an error from `acceptMessage`

Medium · Design / Correctness · Fix · `internal/server/mcp.go:452-460`

`addChild` stores and publishes the child (`publishLocked`, broadcasting `session/created`), then calls `err = sess.acceptMessage(...)` and unconditionally starts `go sess.run()`, yet returns `err`. If `agent.Agent.Send` ever returns an immediate write error (which `internal/agent/agent.go:35-38` permits), `create_session` replies with `isError: true` while leaving a published, live child session in memory and SQLite.

**Action:** Decide one clean outcome when `acceptMessage` fails on a freshly started child (either unpublish/destroy it cleanly before returning `err`, or log and return the created child if publication is the point of no return).

---

### F13. Case-sensitive `Host` check rejects mixed-case configured hosts and uppercase loopback names

Medium · Correctness · Fix · `cmd/ox/main.go:93-102`, `138-151`

`serveHost` preserves the case of `-addr`, and `checkHost` compares `req.Host` case-sensitively against `["localhost", "127.0.0.1", "::1"]` and `r.hosts`. HTTP hostnames are case-insensitive (RFC 9110 §7.2) and browsers always lowercase `Host`. Running `ox -addr MyMac.local:8080` serves `GET /` (see F25), then rejects `/events`, `/rpc`, and `/images/...` with `403 Forbidden: unknown host` because Chrome/Safari send `Host: mymac.local:8080`.

**Action:** Lowercase hosts with `strings.ToLower` in `serveHost` (or `newRelay`) and in `checkHost`, and add a mixed-case host test in `cmd/ox/main_test.go`.

---

### F14. Hollow test: `TestChoiceIDsAndNames` omits required `"titling"`, so all 12 cases fail before choice validation runs

Medium · Tests · Fix · `internal/config/config_test.go:171-195`

Line 187 builds every test config without `"titling"`:
```go
data := `{"harnesses":{"h":` + harness + `},"defaults":{"harness":"h","model":"m","effort":"low","permission_mode":"auto"},"max_child_depth":2}`
```
and line 191 asserts only `if _, err := Load(path); err == nil`. Because `config.Load` always rejects missing `"titling"`, all 12 cases pass even if every ID/name/duplicate check in `addChoice` and `checkOptions` were deleted.

**Action:** Add `,"titling":{"harness":"h","model":"m","effort":"low"}` to `data` on line 187, verify that a valid harness in the same wrapper loads cleanly, and assert the error message matches choice validation rather than a missing top-level field.

---

### F15. `docs/protocol.md` omits required `titling` field in `Configuration` and `harness/list`

Medium · Requirements · Fix · `docs/protocol.md:75-126` (lines 103, 116-117, 124)

`config.Load` (`internal/config/config.go:94-96, 189-201`) refuses to start without `"titling"`, and `harness/list` (`internal/server/methods.go:47`) returns `*config.Config` verbatim including `"titling"`. Yet `docs/protocol.md` does not mention `titling` under `## Configuration` or `### harness/list`, and copying its example `config.json` (line 103) fails startup with `titling is required`.

**Action:** Document `titling` under `## Configuration` and `### harness/list` and add it to both JSON examples in `docs/protocol.md`.

---

### F16. Solid 2.0 deferred signal flush makes `reconcilePrompt()` read stale `phase: "sending"` inside `post()`

Medium · Reactivity / Correctness · Fix · `cmd/ox/web/src/ox.tsx:270-276`, `303-310`

In `post()` (`ox.tsx:304-309`):
```ts
if (outgoing()?.id !== sending.id) return;
setOutgoing({ ...sending, phase: "sent" });
const snapshot = live();
if (snapshot) reconcilePrompt(snapshot);
```
In Solid 2.0, `setOutgoing(...)` queues the write for the next microtask; a synchronous `outgoing()` read inside `reconcilePrompt(snapshot)` (line 271) still returns `{ ...sending, phase: "sending" }`. Consequently, `else if (prompt.phase === "sent") finishPrompt(prompt.id, true)` on line 275 is **dead on this call path**. Normal live streams currently mask this because `oxd` delivers `prompt_cancelled` before `session/send`'s reply, but any live snapshot already missing a sent prompt fails to restore it to the composer.

**Action:** Pass `sent` directly into `reconcilePrompt(snapshot, sent)` (defaulting `prompt = outgoing()` for external callers) so the newly committed phase is inspected without relying on a synchronous signal read.

---

### F17. `stopping` is never reset on disconnect/reconnect, hiding Stop for a new turn if `idle` was missed

Medium · Correctness / Requirements · Fix · `cmd/ox/web/src/ox.tsx:215-217`, `438-448`, `564`, `748-758`

`stopping` is set `true` in `interrupt()` (line 439) and cleared only if `session/interrupt` rejects (line 442) or `setState()` observes a state other than `"running"` and `"requires_action"` (line 564). If the user clicks Stop (`session/interrupt` replies `{}`), the connection drops before `state: "idle"` arrives, and while disconnected the turn finishes and another turn starts (from another page, `send_message`, child report, or schedule), `reload()` sees `"running"` and never calls `stopped()`. The Stop button stays hidden for the entire new turn, violating **Stop** (*"While Claude works, a Stop button shows."*).

**Action:** Clear `stopping` across all controls in `onError` (`for (const c of controls.values()) c.stopped();`) when the stream drops.

---

### F18. Concurrent ancestor/descendant list requests can display an archived parent with an unarchived child

Medium · Correctness / Design · Fix · `cmd/ox/web/src/ox.tsx:498-533`

`busy(id)` (`ox.tsx:519`) checks only `id` and its **ancestors** (`lineOf`), not its **descendants**, and `archived(session)` (`ox.tsx:509-516`) walks child-to-ancestor returning on the first match.

If child `C` is archived and parent `P` is not, clicking **Unarchive** on `C` sets `listing.sessions[C.id] = "unarchive"` while `busy(P.id)` stays `false`. Clicking **Archive** on `P` while `C`'s unarchive is in flight sets `listing.sessions[P.id] = "archive"`. Now `archived(P)` is `true` while `archived(C)` hits `C`'s `"unarchive"` first and returns `false`, rendering an unarchived child under an archived parent (violating `Sidebar.tsx:104-106`'s tree invariant). Similarly, `P` can be archived while a child `C` has `"destroy"` in flight.

**Action:** Have `busy(id)` check `id`, its ancestors, **and its descendants** so at most one list request runs in a family at a time. In `archived(session)`, let an ancestor's `"archive"` dominate over a descendant's `"unarchive"`.

---

### F19. Unkeyed `<For each={props.items}>` in `Menu` destroys and recreates menu items on any store update, dropping focus and clicks

Medium · Reactivity / Accessibility · Fix · `cmd/ox/web/src/Sidebar.tsx:264-275`, `338-399`

`openMenu()` calls `sessionItems(session)` / `workspaceItems(workspace)`, allocating fresh `MenuItem` objects on every reactive update (`session.state`, `session.label`, `ox.ready()`, `ox.busy()`, etc.). `Menu` renders `<For each={props.items}>` with default identity keying (line 264). Any store change while a menu is open (such as a running session finishing or titling arriving) destroys and recreates every `<button role="menuitem">`, dropping keyboard focus to `document.body` and aborting in-progress pointer clicks.

**Action:** Key `<For>` by item slot or stable key (e.g., `keyed={false}` or `keyed={(item) => item.label}`) and read `item()` inside JSX/handlers so `<button role="menuitem">` DOM nodes update in place.

---

### F20. `Effort` slider's `dragged` signal gets permanently stuck when `onChange` does not fire

Medium · Reactivity / Correctness · Fix · `cmd/ox/web/src/SettingsFields.tsx:148-176`

`Effort` writes `setDragged(...)` in `onInput` (line 170) and clears it only in `onChange` (line 172), while `position()` always prefers `dragged()` over `props.value`. Two normal interactions leave `dragged` stuck indefinitely:
1. Dragging the thumb from index `0` to `1` and back to `0` before releasing — browsers do not fire `change` when the final value equals the pre-drag value.
2. `props.disabled` becoming `true` mid-drag (e.g. disconnect or another page starting a turn).

Once stuck, future `props.value` updates from other pages or model switches are ignored, and switching to a model with fewer efforts leaves `dragged()` out of bounds (`--p > 1`, `aria-valuetext` undefined).

**Action:** Make `dragged` a Solid 2.0 writable derived signal that resets whenever committed props change (`createSignal<number | undefined>(() => { props.value; props.efforts; props.disabled; return undefined; })`), and also clear `setDragged(undefined)` on `onPointerUp`, `onPointerCancel`, and `onBlur`.

---

### F21. Local attachment validation errors persist after valid attachments and leak as a cross-view "Reopen draft" alert banner

Medium · Correctness / Requirements · Fix · `cmd/ox/web/src/App.tsx:182-191`; `cmd/ox/web/src/ox.tsx:332-344`

`control.attach()` (`ox.tsx:332-344`) writes local file-type and 10 MiB validation errors into `control.error()` via `start(Promise.reject(...))` and never calls `clearError()` when valid files are subsequently attached. Meanwhile, `App.tsx:182` renders the global cross-view banner for every draft with `d.control.error() && d !== draft()`.

If a user tries to attach a `.txt` file in a new-session draft (never submitting it) and navigates to an existing session, `"Attach PNG, JPEG, GIF, or WebP images. [Reopen draft]"` stays pinned across every other view until the draft is submitted or the page reloads. **Failed creation** (`docs/functional-requirements.md`) specifies the cross-view link only when creating a session fails.

**Action:** Call `clearError()` in `attach()` when valid files are accepted, and gate `App.tsx:182`'s cross-view banner specifically on a failed creation (e.g. `unconfirmed()` or a creation-failure flag) rather than any draft composer error.

---

### F22. `Markdown.tsx` relies on `markdown-it`'s denylist `validateLink`, permitting arbitrary custom URI schemes

Medium · Security / Requirements · Fix · `cmd/ox/web/src/Markdown.tsx:5-13`

`Markdown.tsx` uses `markdown-it`'s default `validateLink`, which is a denylist blocking only `javascript:`, `vbscript:`, `file:`, and non-raster `data:` URLs. Verified against installed `markdown-it@15.0.3`: it renders active `<a href="..." target="_blank" rel="noopener noreferrer">` links for arbitrary OS protocol handlers (`vscode://`, `ssh://`, `smb://`, custom schemes) and `blob:` URLs. Under **Replies as Markdown** and **Markdown preview** (*"unsafe links are not links"*), an explicit scheme allowlist is strictly safer for untrusted model output and workspace files.

**Action:** Override `md.validateLink` with an allowlist:
```ts
md.validateLink = (url) => /^(https?:|mailto:)/i.test(url.trim());
```
(Adjust if relative links are ever intended in Markdown preview.) Add an e2e check confirming a custom scheme such as `vscode://` or `ssh://` does not render as a link.

---

### F23. `ApprovalRequest` lacks repeat-key and in-flight submission guards, enabling duplicate `session/approve` RPCs

Medium · Correctness · Fix · `cmd/ox/web/src/ApprovalRequest.tsx:43-50`, `96-98`, `138-145`, `220-222`; `cmd/ox/web/src/ox.tsx:449-458`

Neither `PermissionCard`'s `onKeyDown` (line 97) nor `QuestionForm`'s `onKeyDown` (line 221) checks `!event.repeat` (unlike `sendOnEnter` in `SessionView.tsx:19`), and neither component nor `control.approve()` guards against submitting while `props.hidden` / `answering().has(requestId)` is already true. Because Solid 2.0 batches state writes in a microtask, holding `Enter` or double-triggering Submit sends duplicate `session/approve` RPCs; `oxd` rejects the second with `-32003`, displaying a spurious error alert even though the approval succeeded.

**Action:** Check `!event.repeat` (with `event.preventDefault()`) in both `onKeyDown` handlers, guard `if (props.hidden) return;` before calling `props.onAnswer`, and guard `if (answering().has(requestId)) return;` in `control.approve()`.

---

### F24. `e2e/services.ts` leaks child processes and temp directories on failure and misses signal/relay crashes

Medium · Tests · Fix · `cmd/ox/web/e2e/services.ts:66-132`

1. If `startRelay()` / `waitFor(url)` throws at line 105, `startServices` rejects without stopping the already-spawned `daemon`/`relay` processes or removing `dir` and `workspace`.
2. In `stop()` (lines 122-130), `throw` statements at lines 126-127 run before `rmSync(dir)` / `rmSync(workspace)`, leaking temp directories on any test failure.
3. `stop()` checks only `daemon.exitCode !== null`, ignoring `daemon.signalCode !== null` (e.g. `SIGABRT`/`SIGSEGV`) and ignoring unexpected exits of `relay` (`ox` built with `-race` can crash undetected).

**Action:** Clean up spawned processes and temp directories in a `catch` block around startup; move `rmSync` into a `finally` block in `stop()`; assert `exitCode === null && signalCode === null` for both `daemon` and `relay` before stopping them.

---

## Low

### F25. `GET /` bypasses `checkHost`, contradicting **Web access**

Low · Requirements / Design · Fix · `cmd/ox/main.go:126-133`; `docs/functional-requirements.md:285-286`

`(*relay).handler` wraps `/events`, `/images/...`, and `/rpc` individually with `r.checkHost`, leaving `GET /` open to any `Host` header. **Web access** states *"The web UI answers only requests addressed to Ox's local or configured hosts."* Wrapping the whole `mux` once (`return r.checkHost(mux)`) enforces the rule in one place (`AGENTS.md`), satisfies the requirement, and prevents future routes from skipping it. Update `docs/architecture.md:404` and `TestRelayRefusesForeignPages`.

### F26. `listen` misreports `os.Lstat` errors (including concurrent shutdown `ENOENT`) as `"exists and is not a socket"`

Low · Correctness / Concurrency · Fix · `cmd/oxd/main.go:209-227`

Because clean shutdown unlinks the socket in `listener.Close()` without taking the directory `Flock`, a concurrent `oxd` startup can hit `EADDRINUSE` right before the old `oxd` unlinks the path. `os.Lstat(path)` then returns `fs.ErrNotExist`, which line 213-214 swallows and misreports as `"%s exists and is not a socket"` (and similarly `net.Dial` fails with `ENOENT` instead of `ECONNREFUSED`). Distinguish `statErr` from the mode check, retry `net.Listen` on `fs.ErrNotExist`, and return other `Lstat` errors directly.

### F27. `agent.Tracker` retains taken/dropped `Input` slices in backing arrays and exposes transient `idle` during `Take()`

Low · Design / Correctness · Fix · `internal/agent/tracker.go:73-105`

- `Take()` (line 76), `DropFirst()` (line 88), and `DropFrom()` (lines 97-98) reslice `t.queued` without zeroing popped/dropped `Input` structs (which can hold up to 10 MiB of raw `Image.Data` and 1 MiB of text) or setting `t.queued = nil` when empty. Zero popped elements (`clear`) and nil empty slices.
- In `Take()`, `t.reported = StateRunning` (line 78) runs after `t.publish(UpdatePrompt)` (line 77), leaving `t.State()` transiently `idle` during the callback. Move `t.reported = StateRunning` before `t.publish`.

### F28. `ur.Agent.Send` and `Configure` do not check `a.exited`

Low · Design / Correctness · Fix · `internal/ur/ur.go:310-316`, `544-554`

Unlike `claude.Agent`, `ur.Agent.Send` and `Configure` omit `a.exited` guards, violating `internal/agent/agent.go:35-38, 47-48`. Calling `Send` after `Stop()` writes to the JSONL file, spawns a stream goroutine, and can race `a.shells.Add(1)` against `a.shells.Wait()`. Guard both with `if a.exited` returning / calling `done(agent.ErrExited)`.

### F29. Ur SSE parser requires a space after `data:` and misformats error chunks with empty `message`

Low · Correctness · Fix · `internal/ur/stream.go:256`, `265-267`

1. Line 256 requires `"data: "` with a space, whereas the WHATWG SSE spec allows `"data:"` without a space (`data:{...}`, `data:[DONE]`). Strip `"data:"` then optional single leading space.
2. Line 266 formats `c.Error` as `"OpenRouter: "` when `c.Error.Message == ""`; fall back to `"OpenRouter returned an error"` (matching `statusError`'s guard at line 233).

### F30. Claude `controlRequest` decodes `hook_callback` and `can_use_tool` into one struct with colliding `input` fields

Low · Correctness / Design · Fix · `internal/claude/approval.go:12-31`

Decoding both control subtypes into one struct with a typed `Input.SessionCrons` field means a `hook_callback` whose `input` fails type-unmarshaling falls through and is rejected with `"Unsupported control request"` instead of answered `{}`. Decode `Subtype string` and `Input json.RawMessage` first, then switch on `Subtype`.

### F31. Claude child processes inherit `OPENROUTER_API_KEY` from `oxd`

Low · Security · Fix · `internal/claude/claude.go:168-172`; `internal/claude/ask.go:30`

Ur's shell tool explicitly strips `OPENROUTER_API_KEY` (`internal/ur/shell.go:138`) so agent commands cannot read it, while `claude`'s `subscriptionEnv` strips only `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`. Confirm whether Claude sessions are intentionally permitted to inherit `OPENROUTER_API_KEY`; if not, strip it in `subscriptionEnv` too.

### F32. Small Go server lifecycle and edge-case defects

Low · Correctness / Lifecycle · Fix

- `internal/server/session.go:228-240`: `sendPrompt` calls `startForInput()` before `store.AddImages(...)`; a DB failure on `AddImages` leaves a freshly launched harness running with no prompt.
- `internal/server/session.go:731` and `internal/server/server.go:78`: `labelFrom` checks `sess.firstLine != ""` without checking `sess.title != ""`; if `events` write failed on the first prompt while `SetTitle` succeeded, restart re-triggers titling and hits a UNIQUE constraint error. Skip `UserPrompts` in `New` when `record.Title != ""`, and guard `labelFrom` with `sess.title != ""`.
- `internal/server/server.go:74-96`: `New` spawns `go sess.run()` inside the store-loading loop, leaking already-started goroutines if `st.UserPrompts` fails on a later session. Start session goroutines in a second loop after all store reads succeed.
- `internal/server/methods.go:98-108`: If `Server.Close()` runs between `s.mu.Unlock()` and `sess.post(...)`, `onSession` replies `-32001 session not found` instead of `-32000 daemon is shutting down`. Re-check `s.closed` when `sess.post` returns `false`.
- `internal/server/worktree.go:60-63`: `within(path, "/")` checks prefix `"//"` and returns `false`. Handle `dir == string(filepath.Separator)`.

### F33. TypeScript client lifecycle and owner hazards

Low · Reactivity / Design · Fix

- `cmd/ox/web/src/ox.tsx:489, 742-761` and `cmd/ox/web/src/poll.ts:17-30`: `let connection!: Connection` is assigned inside `onSettled`, which runs after child `createEffect`s in Solid 2.0. Any child mounted at initial render that invokes `ox.*` from an effect will throw `TypeError`; and in `poll.ts:19-20`, a synchronous throw from `request()` leaves `inFlight = true` permanently. Type `connection: Connection | undefined` (rejecting with `Disconnected` until set) and invoke `Promise.resolve().then(request)` in `poll.ts`.
- `cmd/ox/web/src/ox.tsx:537-551`: `control(id)` creates `createSessionControl` under the calling view's transient owner instead of `OxProvider`'s owner. Capture `const owner = getOwner()` in `createOx()` and wrap creation in `runWithOwner(owner, ...)`.
- `cmd/ox/web/src/connection.ts:114-122`: `pending.set(id, ...)` runs before `stringify(...)`; a synchronous serialization error leaks the map entry until disconnect. Serialize before inserting into `pending`.

### F34. TypeScript UI reactivity, accessibility, and defensive guards

Low · Reactivity / Accessibility / Design · Fix

- `cmd/ox/web/src/SettingsFields.tsx:25-32, 45-60, 106, 118-134`: `entries()` and `matches()` are unmemoized and allocate fresh objects on every keystroke, causing unkeyed `<For each={matches()}>` to tear down and recreate every option and `<HarnessIcon>` SVG; `active()` is also unclamped if `matches().length` shrinks while open. Memoize `entries`/`matches` (or key `<For>`) and clamp `activeIndex`.
- `cmd/ox/web/src/Sidebar.tsx:236-301`: `Menu` does not restore focus to its trigger on Escape/close, lacks `ArrowDown`/`ArrowUp` keyboard navigation, and unconditionally sets `aria-describedby={`${id}-reason`}` even when no reason element exists.
- `cmd/ox/web/src/Sidebar.tsx:357, 378`: `Delete` and `Remove workspace` omit the `ox.ready()` check that `Archive` and `Unarchive` use, allowing offline users to confirm a dialog only to see an immediate disconnect error and rollback.
- `cmd/ox/web/src/FileView.tsx:32-35`: `source` uses an eager `createMemo`, running Shiki syntax highlighting on Markdown files on every poll edit even while viewed in Preview mode. Pass `{ lazy: true }`.
- `cmd/ox/web/src/FileView.tsx:44-58, 88-99`: `role="tablist"` wraps a non-`tab` `<div class="tab">` and nests the Close `<button>` inside `role="tablist"`, and `role="tab"`/`role="tabpanel"` are not linked via `aria-controls`/`aria-labelledby`.
- `cmd/ox/web/src/SessionView.tsx:160-162` and `cmd/ox/web/src/Usage.tsx:40, 67-71, 94`: `<Show when={props.snapshot.usage}>` passes `windows[0] === undefined` if `usage` is ever `[]`, and `Usage.tsx:94` clamps `used()` at `<= 100` but not `>= 0` (nor guards invalid `resets_at` / non-positive `interval_seconds`).
- `cmd/ox/web/src/SessionView.tsx:14-21` (and `ApprovalRequest.tsx:97, 221`, `SettingsFields.tsx:56`): `Enter` handlers check `!event.isComposing` without checking `event.keyCode !== 229` for Safari/WebKit's CJK IME confirmation quirk (needs live Safari verification).

### F35. `stack.mjs` misses retiring processes on a second `SIGINT`, leaks listeners, and leaves in-flight builds running

Low · Correctness · Fix · `cmd/ox/web/stack.mjs:74-84`, `104-124`, `156-166`

1. `restart(record)` clears `record.proc = null` (line 114) before `await retire(old, "SIGTERM")`, so a second `SIGINT` during a rebuild restart skips `old`.
2. `signals?.on(...)` listeners are never removed when `stop()` finishes.
3. Calling `stop()` while `runBuild()` is active resolves without terminating the running build child process.

### F36. Test coverage gaps and test hygiene issues (Go and TypeScript)

Low · Tests · Fix

- **Go harness & server test gaps:**
  - `internal/claude/claude_test.go:496-508`: `TestUnknownEffortFailsToStart` passes a 1-slot `events` channel with `done = nil` and never drains it or waits on `<-p.reaped`, leaking `deliverOutput` goroutines per run.
  - Missing unit tests for `validApprovalResponse` rejections and unsupported `control_request` rejection (`internal/claude/approval_test.go`), `capture.text()` UTF-8 boundary truncation (`internal/ur/shell_test.go`), and `removeWorktree` unregistered-folder / both-folders-gone / symlink-escaping-worktree cases (`internal/server/worktree_test.go`), in addition to the regression tests noted in F1–F9.
  - `internal/server/history_test.go:64`: checks `entries[2].Time` but logs `entries[1].Time` on failure.
  - `internal/server/client_test.go:33`: `newServer` uses `os.TempDir()` for worktrees instead of `t.TempDir()`, requiring manual cleanup in callers.
- **TypeScript unit & E2E test gaps and `cmd/ox/web/AGENTS.md` hygiene:**
  - `cmd/ox/web/toolInput.test.mjs`: leaves `planOf`, `questionsOf`, `toolInputJSON`, `rawJSON` subjects, and 120-char truncation untested (and `toolInput.ts:34` lacks `Object.hasOwn(OX_SUBJECT_FIELD, tool)`).
  - `cmd/ox/web/e2e/workspace.spec.ts:611-617`: asserts toggle alignment via `boundingBox()` equality, violating `cmd/ox/web/AGENTS.md:29-30`.
  - One-shot `expect` reads where web-first / `expect.poll` assertions apply: `workspace.spec.ts:670`, `session.spec.ts:1541, 1634, 2026, 2103, 2108, 2112`.
  - Duplicate test (`session.spec.ts:122-128` vs `153-156`) and multi-behavior tests (`workspace.spec.ts:299-339` hiding a session-deletion test inside expanded-folders, `592-640`, `673-709`, `1123-1156`; `session.spec.ts:1525-1554`).
  - Missing E2E coverage for **New-session choices** (model/harness search + keyboard select), **Model within its harness** (multi-harness switch), **Blank prompts**, **Attached images in the composer** surviving view switch/reconnect, and **Setting commands** (`/model`/`/effort` UI refusal).

---

## Nit

### F37. Minor cleanup and documentation drift

Nit · Conventions / Cleanup · Fix

- `internal/fake/fake.go:173-183`: `begin()` does not reset `a.thoughts = nil`, so leftover reasoning pieces from a `"think ..."` turn can bleed into a queued input that takes over mid-turn.
- `cmd/ox/main.go:169-186`: `relay.events` spawns a goroutine to close `conn` on context cancellation instead of using `context.AfterFunc` like `relay.image`, and discards `fmt.Fprintf` write errors.
- `internal/claude/claude.go:198-211`: `logStderr` tail slicing at `stderrKept` can split a multi-byte UTF-8 rune on a single long line without `strings.ToValidUTF8`.
- `internal/server/session.go:151`: `sess.id[:8]` assumes ID length `>= 8`; `sess.id[:min(8, len(sess.id))]` avoids panicking on manually inserted short IDs.
- `cmd/ox/web/src/Sidebar.tsx:23` and `cmd/ox/web/src/NewSessionView.tsx:31, 103`: top-level `props.request` / `props.draft` reads without `untrack(...)`, dead `autofocus` attribute on dynamically mounted `<textarea>`, and manual class string interpolation in `cmd/ox/web/src/HarnessIcon.tsx:44` instead of array syntax.
- Documentation drift: `docs/architecture.md:375-385` omits `think`, `usage`, and `usage reset` fake prompts; `docs/protocol.md:774, 784` omits `usage` from `read_session`'s hidden list and `[images: <n>]` from prompt rendering; `cmd/ox/web/AGENTS.md:56-57` omits `id.test.mjs`.

---

## Rejected

- **R1. `Server.Close` does not wait for in-progress `session/destroy` worktree removals (`internal/server/server.go:160-176`, `internal/server/session.go:533-544`).**
  **Rejected.** Consistent with `agents/reviews/2026-11-24-session-worktrees.md` (Verification 1): git finishes on its own once started, a cut-short delete leaves the session listed with its stored row, and a repeated delete completes it (covered by `TestWorktreeDeleteAfterRemovedByHand` and `TestWorktreeDeleteRepeated`).
- **R2. `createWorkspace` calls `os.Stat` on existing workspaces under `Server.mu` (`internal/server/methods.go:147-152`).**
  **Rejected.** Runs only on `workspace/create` over a handful of local workspaces; adding a separate path cache or two-phase check costs more complexity than keeping it unless an unresponsive mount is actually observed.
- **R3. `shell.wait()` calls `killGroup(s.cmd.Process.Pid)` after `s.cmd.Wait()` reaps the leader PID (`internal/ur/shell.go:107, 116-120`).**
  **Rejected.** PID reuse between `cmd.Wait()` returning and `syscall.Kill(-pid, SIGKILL)` is a hypothetical edge case; replacing `exec.Cmd.Wait` with manual pipe draining and reaping adds machinery not earned by a concrete failure.
- **R4. E2E fault-injection helpers monkey-patch `EventSource` and `document.visibilityState` (`cmd/ox/web/e2e/ui.ts:270-285`, `session.spec.ts:1128-1136`, `workspace.spec.ts:95`).**
  **Rejected** as a code change. Playwright has no native primitive for dropping one mid-stream SSE frame or toggling `visibilityState` directly; if strict wording alignment is desired, note narrow fault-injection exceptions in `cmd/ox/web/AGENTS.md`.
