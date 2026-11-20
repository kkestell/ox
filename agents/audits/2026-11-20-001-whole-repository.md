# Whole-repository audit

## Scope and evidence

The whole repository at `aab647d` ("Let Claude read other sessions' transcripts"). The working tree had uncommitted edits to `AGENTS.md` and the `ox-plan` and `ox-review` skills, and the untracked `ox-audit` skill; no code differed from `aab647d`. There is no earlier audit in `agents/audits/`, so the history covers every commit from `2dd4034` to `aab647d` (102 commits).

Evidence read: `AGENTS.md`, `cmd/ox/web/AGENTS.md`, `docs/functional-requirements.md`, `docs/known-issues.md`, `docs/architecture.md`, the startup, turn, and failure sections of `docs/claude-code.md`; the 15 reviews and 12 plans in `agents/`; the two deslop audits deleted from `agents/reviews/` (at `c51bf80` and `7790725`); and the messages of every fix commit, with the diffs of those the reviews did not explain.

Gaps:

- Read in full: `internal/server`, `internal/claude`, `internal/agent`, `internal/fake`, `internal/config`, `internal/store` (queries), `cmd/oxd`, and the web client's `ox.tsx`, `App.tsx`, `SessionView.tsx`, `NewSessionView.tsx`, and the model picker. `connection.ts`, `Sidebar.tsx`, `ApprovalRequest.tsx`, `cmd/ox`, `cmd/ox-mcp`, and the tests were covered only through the reviews that examined them.
- Whether Claude writes output before its `initialize` response was not checked live; F1 notes what the startup reader would do with it.

## Summary

| ID | Cost   | Action   | Title                                                         | Places |
| -- | ------ | -------- | ------------------------------------------------------------- | ------ |
| F1 | High   | Plan     | Starting and stopping a harness block the session's goroutine | 8      |
| F2 | Medium | Plan     | Untaken inputs are split between the session and the harness  | 8      |
| F3 | Low    | Rejected | A session's label is derived on both sides of the protocol    | 5      |
| F4 | Low    | Rejected | Settings are held by both the session and the Claude agent    | 4      |
| F5 | Low    | Rejected | Each method decides what a parked session means               | 9      |
| F6 | Low    | Rejected | Claude's configuration rules are checked in `cmd/oxd`         | 3      |

## F1. Starting and stopping a harness block the session's goroutine

High · Plan

`agent.Harness.Start` returns only once Claude has answered its `initialize` handshake, and `Agent.Stop` returns only once the process has exited. Both run on the session's goroutine, which owns every read of the session. While a session starts (normally about a second, up to the one-minute startup limit) or stops (up to the ten-second kill timer), every request that reads it waits: a page's `session/get`, and another session's `list_sessions`, `send_message`, and `read_session`. `list_sessions` posts to every session, so one session starting holds up a tool call in any other session. That breaks **Independent sessions** ("One session starting ... never holds up another session") in normal use, whenever a send or a message starts a parked session while another session's Claude lists, messages, or reads.

The same structure makes Claude's stdout have two readers. Because `Start` cannot deliver events, the handshake is read by a synchronous loop in `initialize` with its own EOF, deadline, failure-reason, and stderr handling, and only after it returns does `process.read` take over the pipe with a second set of the same rules. Output Claude writes before its handshake response, other than a failed result or plain text, is discarded by the first reader; `docs/claude-code.md` records that a resumed conversation can start a turn during startup, which was not checked against the handshake's timing.

### Places

- `internal/server/session.go:219` — `start` calls the harness's `Start` on the session goroutine, from `startForInput` (`:198`), which `sendPrompt` (`:135`) and `deliver` (`:90`) call.
- `internal/server/session.go:246` — `stop` calls `Agent.Stop` on the goroutine, from the park timer (`:74`), `end` (`:262`), and `destroySession` (`internal/server/methods.go:457`).
- `internal/claude/claude.go:189-211` — `startProcess` waits for the handshake under a one-minute timer that kills Claude and sets the pipe's read deadline.
- `internal/claude/claude.go:227` — `initialize`, the startup reader of stdout, beside the running reader `process.read` (`:317`), each with its own end-of-output and reason handling.
- `internal/claude/agent.go:281` — `Stop` handles the agent's events in a loop until the process exits, bounded by `killAfter` (`internal/claude/claude.go:366`).
- `internal/server/mcp.go:223`, `:275`, `:309` — `list_sessions` (every other session), `send_message`, and `read_session` post to other sessions and wait for the operation to run.
- `internal/server/methods.go:307` — `getSession` waits the same way, so a page loading a starting session shows "Loading session…" until the start ends.
- `internal/server/methods.go:174` and `cmd/ox/web/src/ox.tsx:37-63` — `workspace/list` carries each session's label so the sidebar has one while `session/get` waits on a start.

### Defects

- Review 2026-11-20-001 F1: the sidebar labeled a session by its ID while its `session/get` waited on a start or stop. The fix added the label to `workspace/list` and a client fallback, which guards the symptom; `session/get` still waits.
- `3c99ff3`: both stdout readers dropped a last line without a newline; the fix changed each reader separately.
- `11869cd`: the startup reader kept `session/send` waiting while a child held stdout, "the same read-until-EOF structure the exit reader had" (`43122a2`); each got its own deadline.
- `f5719f8`, `a0a7efa`, `6508b8b`: a failed start lost Claude's reason three ways, each fixed inside the startup reader, apart from the exit reader's reason handling (review 2026-11-19-004 F12).
- `12fc607`: `Stop` waited without a deadline, hanging parking, deletion, and shutdown; the fix bounded the wait with a kill timer, keeping the wait on the goroutine.
- The session-messaging plan (2026-11-19-001, **Independent sessions**) accepted the wait as "bounded by the operation running there: a start (one minute) or a stop (ten seconds plus the kill)".

### Evidence

Probes against `oxd` and `ox-mcp` built from `aab647d` on a temporary socket and database, with stand-in `claude` scripts on `PATH`:

- A stand-in that answers `initialize` after five seconds: session B was sent a prompt, and half a second later session A's attached MCP connection called `list_sessions` and a page called `session/get` on B. Both answered 4.8 s after they were sent, when B's start ended.
- A stand-in that ignores `SIGTERM`: B was destroyed, and half a second later A's `list_sessions` answered after 9.4 s, when B's kill timer fired.
- Real Claude, run as `docs/claude-code.md` describes for model discovery, answered `initialize` after 1.82 s, 0.73 s, and 0.73 s, which is the hold in normal use.

### Replacement

The agent owns its process's whole lifecycle as phases driven by `Events`: `Start` spawns Claude and returns at once with the agent in a starting phase; the handshake response, a startup failure, the startup timeout, and the exit arrive as events through the one reader that runs from spawn to exit; stopping asks the process to exit and finishes on its exit event, as the configuration timeout already does through `timedOut`. The request that started the process is answered from the event that ends the start, as `session/configure` is answered from the acknowledgement.

This removes the synchronous `initialize` loop and its separate EOF, deadline, and reason handling, the inner event loop in `Agent.Stop`, and every wait another session or page has on a start or stop. The label in `workspace/list` (F3) exists only because `session/get` waits, and can then be reconsidered. The change touches `internal/claude` startup and stop, `agent.Harness` and the `Agent` contract, the fake harness, the session's start and stop callers, and the startup tests that drive `startProcess`. It may change what the user sees: a starting session's state, when `session/send` to a parked session replies, and how a failed start reaches the send that caused it, which the plan settles with the user.

## F2. Untaken inputs are split between the session and the harness

Medium · Plan

An input Claude has not taken lives in one of two places: the harness holds at most one written input, and the session's inbox holds the messages behind it. Each owner publishes its own lifecycle updates, so a message is announced twice, once by the inbox and once by the harness, and the session filters the second. `session/get` and `read_session` join the two lists. The rule that keeps them consistent, "the inbox is non-empty only while the agent exists and refused the last `Send` as busy" (session-messaging plan, Construction), is held by discipline: `deliver` runs after every operation and event, and three callers clear the inbox when the agent goes away. The inbox has no life of its own: outside an operation it holds messages only while an agent exists, so it is a second queue for the agent's inputs, kept in the session because the harness takes one at a time.

All current paths are guarded; no wrong state was reached at this revision. The structure is what the last four defects in this area came from, and each new path that ends an agent, writes an input, or refuses a prompt has to repeat the guards.

### Places

- `internal/server/session.go:35` — `inbox`, filled by `acceptMessage` (`:142`) and announced by `inboxUpdate` (`:157`).
- `internal/server/session.go:86` — `deliver`, run after every operation and event (`:78`), with its `ended` check.
- `internal/server/session.go:148` — `cancelInbox`, called from `startForInput` (`:207`), `parked` (`:258`), and `interruptSession` (`internal/server/methods.go:385`).
- `internal/server/session.go:127` — `messagesWaiting`, checked by `sendPrompt` (`:132`) and `configureSession` (`internal/server/methods.go:422`).
- `internal/server/session.go:165` — `agentUpdate` drops the harness's `prompt_queued` for a message.
- `internal/server/session.go:117` — `queued` joins `Snapshot.Queued` and the inbox.
- `internal/agent/agent.go:24-29`, `:170` — `Send` refuses a second untaken input, and `Snapshot.Queued` holds at most one.
- `internal/claude/prompt.go:26-53`, `:74`, `internal/claude/agent.go:257-261`, and `internal/fake/fake.go:127-169`, `:220-253` — each harness holds one untaken input and drops it on interrupt or exit.

### Defects

- Review 2026-11-19-004 F4: waiting messages had no ID or update and were dropped unseen. The fix gave the inbox its own lifecycle updates, which created the double announcement and the filter.
- Review 2026-11-19-002 (session messaging) F1: a Claude that crashed with messages waiting was started again to deliver them, because the inbox outlived the agent. Guarded by the clear in `parked` and a test.
- Review 2026-11-19-002 (session messaging) F2: a failed start was retried after every operation, because the inbox kept its messages. Guarded by the clear in `startForInput` and a test.
- Review 2026-11-20-001 F2: pending-input rules were decided in several places, and a caller that skipped a check would panic `oxd`. The fix moved the checks into shared helpers.
- Plan 2026-11-20-002: the server's copy of the written input could panic `oxd` or hold a session busy forever. The fix removed the copy and left the inbox.
- Review 2026-11-20-002 F2 (rejected): the `messagesWaiting` checks decide an outcome only after a failed write; no test reaches them.

### Evidence

Traced from the source: every path that ends an agent (`parked`, `startForInput`'s failure) or abandons inputs (`interruptSession`) clears the inbox itself; every message is announced at acceptance and again when written; and `deliver` must run after every operation and event for the inbox to drain. The defects above were each confirmed by their reviews with removed conditions or reproductions.

### Replacement

The harness owns every untaken input, in order: the session hands it each accepted message and prompt, starting a parked agent first, and the harness writes the next input when Claude takes the previous one, announces each input once, and drops them all on interrupt or exit. The refusal of a user prompt while anything is untaken reads the harness's queue, its one source.

This removes the inbox, `deliver` and its run after every operation and event, `messagesWaiting`, `cancelInbox` and its three callers, `inboxUpdate`, the `prompt_queued` filter in `agentUpdate`, the join in `queued`, and the messages branch of `startForInput`. It changes the `Agent` contract (`Send` accepts several inputs), both harnesses, and the server tests of message delivery. In the Claude harness the queue must outlive a configuration, since a message can arrive while one runs; the plan decides how that fits the phase design. With F1's starting phase, a message to a parked session goes straight to the starting agent, so the two changes fit together, and the plan should settle their order. The user-visible order and drops are unchanged unless the plan changes them; whether a sender learns of a failed start is a product question for the plan.

## F3. A session's label is derived on both sides of the protocol

Low · Rejected

The label is the first line of the first user prompt. `oxd` derives it with a store query for `list_sessions`, `workspace/list`, and a message's `from_label`; the page derives it again from the loaded transcript, uses the listed label before that, uses the ID for a session another page just created, and falls back to the sender's current label for a message without `from_label`.

### Places

- `internal/store/store.go:214` — `FirstPrompt`, the earliest prompt without a sender.
- `internal/server/mcp.go:451` — `Server.label`, called from `listWorkspaces` (`internal/server/methods.go:174`), `listSessions` (`internal/server/mcp.go:225`), and `sendMessage` (`:269`).
- `cmd/ox/web/src/ox.tsx:59` — `sessionLabel`, from the snapshot's entries.
- `cmd/ox/web/src/ox.tsx:395` — the ID label for a session created on another page.
- `cmd/ox/web/src/SessionView.tsx:110` — `PromptBubble`'s fallback for a message without `from_label`, which every message now carries.

### Defects

Review 2026-11-19-004 F10 (a deleted sender's messages showed its ID, fixed by recording `from_label`), F31 (`list_sessions` read labels outside the owner, fixed by reading them on the owner), and review 2026-11-20-001 F1 (the ID shown until a snapshot loads, part of F1 above).

### Evidence

Both derivations read the same append-only history and take the first user prompt's first trimmed line. Go's `strings.TrimSpace` and JavaScript's `trim` were compared on U+0085 and U+FEFF; they differ only on a leading U+FEFF, which renders the same.

### Replacement

Rejected: cheaper to keep. The two derivations agree on every visible input, the label changes once and only by appending, and a single label field would need a new notification or a read outside the session's goroutine under `Server.mu`. The listed label's reason to exist is F1's; reconsider it there.

## F4. Settings are held by both the session and the Claude agent

Low · Rejected

The session holds the settings it stores and shows; the Claude agent holds the settings its process uses, starting from the session's and reporting every change through `OnConfigured`, which the session adopts.

### Places

- `internal/server/session.go:30`, `:287` — `settings` and `adopt`.
- `internal/claude/agent.go:60`, `:229` — `settings` and `configured`.
- `internal/claude/configure.go:24-43` — `Configure` diffs the patch against the agent's copy.
- `internal/server/methods.go:426-436` — the parked path and the exit path adopt directly.

### Defects

The deslop audit of 2026-11-18 F1, `99be6fe`, and review 2026-11-19-004 F5: earlier designs showed settings other than those the process used. F5's fix made `OnConfigured` the one path from the agent to the session.

### Evidence

Traced: while an agent runs, only the agent changes its copy and the session changes its own only through `adopt`; the session writes directly only while parked or after the agent has exited (`configureSession`'s `ErrExited` path). No interleaving lets the two copies differ while a process runs.

### Replacement

Rejected: cheaper to keep. Ownership alternates with the process's life and never overlaps, the copy feeds the change-only control requests that `Configure` sends, and no defect has come from it since F5.

## F5. Each method decides what a parked session means

Low · Rejected

`sess.agent == nil` is checked by `approveSession`, `interruptSession`, `configureSession`, and `destroySession`, and by `run`, `snapshot`, `armPark`, `startForInput`, and `end` in the session.

### Places

- `internal/server/methods.go:364`, `:386`, `:426`, `:456`.
- `internal/server/session.go:57`, `:109`, `:186`, `:199`, `:263`.

### Defects

None at this revision. The parked snapshot was built in three places until the read-session change gave it one method, `session.snapshot`.

### Evidence

Traced: each check gives that operation's own meaning for a parked session (no approval to answer, nothing to interrupt, settings stored directly, nothing to stop), and each is one line.

### Replacement

Rejected: misreads the intended design, as review 2026-11-19-007 found. A parked agent value would have to store settings and start processes, which are the session's work, so the case analysis would move, not go away.

## F6. Claude's configuration rules are checked in `cmd/oxd`

Low · Rejected

`checkConfig` switches on the harness name `claude` to refuse Claude's `default` alias and effort levels Claude does not take, while the Claude package owns the effort list, the `default`-to-`manual` mapping, and the prompt check.

### Places

- `cmd/oxd/main.go:115-134` — `checkConfig`.
- `internal/claude/claude.go:98-107` — `efforts` and `CheckEffort`.
- `internal/claude/agent.go:219-227` — the `default` mapping.

### Defects

Review 2026-11-19-004 F20 and `22d60d8`, each fixed by adding a check here.

### Evidence

Traced: every rule is checked once at startup, against the one harness that has constraints.

### Replacement

Rejected: cheaper to keep while one harness has configuration rules, as review 2026-11-19-006 judged. A second harness with its own rules would earn a check on `agent.Harness`, beside `CheckPrompt`.

## Checks run

- Built `oxd` and `ox-mcp` from `aab647d` into a temporary directory and ran them on a temporary socket and database with stand-in `claude` scripts. With a five-second handshake, another session's `list_sessions` and a page's `session/get` waited 4.8 s for the start; with a stand-in that ignores `SIGTERM`, `list_sessions` waited 9.4 s for a destroy. The daemons and stand-ins were stopped afterwards.
- Timed real Claude's `initialize` handshake three times in a temporary directory, with no prompt sent: 1.82 s, 0.73 s, 0.73 s.
- Compared Go's `strings.TrimSpace` with JavaScript's `trim` on U+0085 and U+FEFF.
- No repository file other than this audit was changed.
