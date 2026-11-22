# Codex contributions audit

Codex wrote the initial scaffold and nine later commits on `main`, and its own
session logs confirm each one. Ten findings are marked to fix and five are
rejected. The two most costly fixes:

- `4c96a77` added a compatibility fallback, with a test that edits SQLite, for
  transcripts recorded before `from_label` existed.
- `5b1c0e5` added draft recovery, which two places decide, and one of those
  places has no test.

The workflow gaps are concrete:

- `4c96a77` was committed before its review began.
- The tailnet plan never went through `/ox-plan`.
- The F1 planner was a Claude process launched outside Ox.

Nothing was fixed.

## Scope and evidence

- **Revision:** `main` at `d79ffe6`. The working tree holds another session's
  uncommitted Tailscale rework (`cmd/ox/main.go`, `cmd/ox/main_test.go`,
  `internal/config/*`, parts of `docs/` and the web client, and the deletion of
  `agents/reviews/2026-11-22-002-tailnet-access.md`). This audit leaves that
  rework out.
- **Attribution sources:**
  - Codex session logs under `~/.codex/sessions/2026/11/`, filtered to
    `session_meta.cwd == /Users/kyle/src/ox`. Each log was searched for
    `git commit` output (`[main <hash>]`), `apply_patch` targets, and user
    prompts.
  - `git blame HEAD` over every tracked file, to count the lines from each
    Codex commit that survive.
  - Claude Code transcripts under `~/.claude/projects/-Users-kyle-src-ox/`,
    used to rule authorship in or out.
  - The records in `agents/`.
- **History:**
  - All 150 commits on `main`, `2dd4034..d79ffe6`.
  - Codex sessions in this directory from Nov 10 to Nov 17 belong to an earlier
    repository that was deleted and reinitialized on Nov 17 ("delete .git and
    reinit a fresh repo"). None of their commits (`136b813`, `a435cf3`, and
    others) exist in this history.
- **Earlier audits read:** `2026-11-20-001`, `2026-11-20-002`, `2026-11-20-003`,
  and `2026-11-22-001`.
- **Also read:** `docs/known-issues.md`.

Gaps:

- Claude Code sessions do not record commit hashes in their transcripts, so
  commits that no Codex log claims cannot be attributed positively. They are
  treated as not Codex.
- Hypercube runs leave no transcript here. The bulk of `4c96a77` came from one,
  so its author is unknown (see Attribution).

## Attribution

| Change                                                     | Codex wrote                                                                                                    | Evidence                                                                                                                                                                                                                                                                                                                                                                                                          | Strength                                                                     |
| ---------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| `2dd4034` initial commit                                   | All of it: scaffold, approval, configure, `docs/protocol.md`, `AGENTS.md`                                      | Session `2026-11-17T22-58` created the Go project and reinitialized `.git`. Sessions `2026-11-18T07-05` ("Build it": tool approval) and `07-27` (`agent/configure`) patched exactly the 18 files in the commit. Codex did not run the commit, which landed at 08:20, four minutes after the last session ended.                                                                                                  | Strong                                                                       |
| `66c5467` system prompt append                             | All                                                                                                            | Session `2026-11-18T19-41`, commit output.                                                                                                                                                                                                                                                                                                                                                                        | Strong                                                                       |
| `9841bde` six composer and approval todos                  | All                                                                                                            | Session `2026-11-18T20-14`, commit output.                                                                                                                                                                                                                                                                                                                                                                        | Strong                                                                       |
| `7790725` deslop audit record                              | All                                                                                                            | Session `2026-11-18T23-34` (`$kdeslop`), commit output. The record was later removed from the repository in `1914e1c`.                                                                                                                                                                                                                                                                                            | Strong                                                                       |
| `6217704..6508b8b` (24 commits)                            | None                                                                                                           | Same session: Codex had Claude carry out Claude's plan ("tell Claude to execute its plan. 1-4"). Codex's own log credits these commits to Claude.                                                                                                                                                                                                                                                                  | Strong that Claude wrote them                                                |
| `5b1c0e5` drafts and settings across reconnects            | All                                                                                                            | Same session: Codex fixed the issues its own reviewer subagents found, then committed and pushed.                                                                                                                                                                                                                                                                                                                 | Strong                                                                       |
| `4c96a77` session queues, settings, and process state      | The sender-label fallback and its test only (`SessionView.tsx`, `protocol.ts`, `protocol.md`, `session.spec.ts`, `services.ts`) and a `untrack` comment | Session `2026-11-20T05-42` found all 42 files already staged ("All 42 changed files are staged"). The staged work answered `agents/reviews/2026-11-19-004-full-repository-multi-lens.md` and came from the Hypercube run named in session `2026-11-19T22-30`. Codex reviewed it, patched the files listed, then committed and pushed.                                                                          | Strong for Codex's patch and the commit. The rest is weak: author unknown, not Codex |
| `agents/plans/2026-11-20-001-tailnet-access.md`            | The plan                                                                                                       | Session `2026-11-20T07-04` wrote the plan and asked Claude for a sanity check. `362bbf4` and `d79ffe6` revised it.                                                                                                                                                                                                                                                                                                | Strong for the content. Who committed `362bbf4` is unknown                   |
| `agents/audits/2026-11-20-002-deslop.md`                   | Findings, through GPT-6.2 Sol reviewers                                                                        | The record names "nine GPT-6.2 Sol reviewers". Claude session `010df858` (01:02–02:46Z, Nov 21) orchestrated them and verified the Medium findings, and the commit `a93e16d` landed inside that window.                                                                                                                                                                                                           | Weak; counted as Claude's record                                             |
| `agents/audits/2026-11-22-001-conversation-launch-and-layout.md` and its todo entries | All                                                                                                            | Session `2026-11-22T06-28` (`$ox-audit`).                                                                                                                                                                                                                                                                                                                                                                         | Strong                                                                       |
| `857a4aa` Ur saves reply fragments                         | Code, tests, review. The plan is Claude's                                                                      | Same session. A Codex subagent (`f1_work`) implemented the change and `f1_review` reviewed it. The plan came from a Claude Code process Codex launched directly ("Damage is done. Use Claude's plan").                                                                                                                                                                                                          | Strong                                                                       |
| `f1350cd` ox-audit skill handoff                           | All                                                                                                            | Same session, commit output.                                                                                                                                                                                                                                                                                                                                                                                      | Strong                                                                       |
| `ee2cd33` Codex model line in `AGENTS.md`                  | All                                                                                                            | Session `2026-11-22T07-08`, commit output.                                                                                                                                                                                                                                                                                                                                                                        | Strong                                                                       |
| `d79ffe6` Tailscale                                        | All                                                                                                            | Same session, commit output. Subagent `tailnet_verify` ran the verify pass.                                                                                                                                                                                                                                                                                                                                       | Strong                                                                       |

Lines from Codex commits that survive in `HEAD`, by `git blame`:

| Commit    | Lines | Where                                                                                                           |
| --------- | ----- | --------------------------------------------------------------------------------------------------------------- |
| `2dd4034` | 255   | `approval_test.go`, `configure_test.go`, `docs/protocol.md`, `AGENTS.md`, `cmd/oxd/main.go`                     |
| `66c5467` | 1     | `internal/claude/claude.go`                                                                                     |
| `9841bde` | 16    | `ApprovalRequest.tsx`, `SessionView.tsx`                                                                        |
| `5b1c0e5` | 344   | `NewSessionView.tsx`, `App.tsx`, `methods.go`, `methods_test.go`, the e2e specs, `ox.tsx`, `connection.ts`, `cmd/oxd/main.go` |
| `4c96a77` | 1612  | Mostly the staged Hypercube work. About 40 are Codex's patch                                                    |
| `857a4aa` | 645   | 534 in records and `docs/todo.md`, 111 in `internal/ur`                                                         |
| `f1350cd` | 7     | `.claude/skills/ox-audit/SKILL.md`                                                                              |
| `ee2cd33` | 3     | `AGENTS.md`                                                                                                     |
| `d79ffe6` | 384   | Being rewritten by the Tailscale rework                                                                         |

## Workflow

The plan → work → review → verify workflow entered `AGENTS.md` in `9b74a59`
(Nov 19, 09:53), and the rule that every audit finding gets a todo entry
arrived in `0d2ee01` (Nov 21). Changes made before those commits are measured
against the rules of their day.

| Change                       | Plan                                                 | Review / verify                                                                                                                                     | Requirement text                                                               | Tests fail without it                                                                           | Verdict                                                                                                                                                                                       |
| ---------------------------- | ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `2dd4034`, `66c5467`, `9841bde` | Predates the workflow                                | `9841bde`: an informal `claude -p` review, no record                                                                                                | N/A                                                                            | Not shown                                                                                       | No workflow existed yet                                                                                                                                                                      |
| `7790725`, `5b1c0e5`         | Predates the workflow                                | Codex subagents reviewed; no record survives. `1914e1c` moved `.agents/` out of Git                                                                 | `5b1c0e5` edited `docs/guarantees.md`, the requirements document of the day, with no approval on record | Three guards have no test (F2, F3)                                                              | No workflow existed yet. The untested guards remain                                                                                                                                          |
| `4c96a77`                    | The staged work answered review `2026-11-19-004`. Codex's own patch had no plan | Committed and pushed **before** its review began. `agents/reviews/2026-11-20-001-full-review-fixes.md` says so: "committed before the review began". That review never examined the sender-label fallback | None                                                                           | The fallback's test edits the database directly (F1)                                            | **Bypassed:** committed before review, and an unplanned compatibility layer was added                                                                                                         |
| Tailnet plan (Nov 20)        | Written by Codex directly, not by `/ox-plan` on Opus, although the skill existed from `1914e1c` | N/A                                                                                                                                                 | The plan's Requirement changes section is what `d79ffe6` applied               | N/A                                                                                             | **Bypassed** `/ox-plan`                                                                                                                                                                       |
| `2026-11-22-001` audit       | `/ox-audit` invoked by the user                      | N/A                                                                                                                                                 | N/A                                                                            | N/A                                                                                             | Followed. Todo entries for F2–F4 are present and correct                                                                                                                                     |
| `857a4aa`                    | Claude's plan from a process Codex launched outside Ox, with `ANTHROPIC_API_KEY` unset. A second Codex plan was discarded | Codex subagents, not Ox child sessions. The review had no findings, so no verify pass followed. It left a test timeout without an outcome (F9) | None needed                                                                    | Yes: the review ran the tests against the base revision and against injected mutations         | Mostly followed. The children ran outside Ox, and one observation was dropped                                                                                                                |
| `f1350cd`, `ee2cd33`         | Skill and `AGENTS.md` edits; no plan required        | N/A                                                                                                                                                 | N/A                                                                            | N/A                                                                                             | Allowed                                                                                                                                                                                       |
| `d79ffe6`                    | Codex's plan, revised in the same commit at the user's direction | Codex subagents. The review's F1 prescribed the shadow decoder; its verify pass approved fixed-port socket tests                                    | Matches the plan's Requirement changes                                         | The bind-failure test did not check the cleanup it named                                        | Followed in form, not in quality. It kept the address as a re-parsed string, hid the field with a shadow decoder, and shaped a four-value `listen()` around its tests. The rework replaces it |

## Summary

| ID  | Cost   | Action   | Title                                                     | Origin               |
| --- | ------ | -------- | --------------------------------------------------------- | -------------------- |
| F1  | Medium | Fix      | A compatibility fallback for sender labels                | `4c96a77` (Codex patch) |
| F2  | Medium | Fix      | Draft recovery is decided in two places                   | `5b1c0e5`            |
| F3  | Low    | Fix      | Two oxd guards have no test                               | `5b1c0e5`            |
| F4  | Low    | Fix      | Ur copies an unread model into every reply fragment       | `857a4aa`            |
| F5  | Low    | Fix      | Ur decides where a reply starts in two places             | `857a4aa`            |
| F6  | Low    | Fix      | A Stop assertion that cannot fail                         | `857a4aa`            |
| F7  | Low    | Fix      | The new-session view decides when it can create           | `5b1c0e5`            |
| F8  | Low    | Fix      | One workspace test checks four behaviors under one name   | `5b1c0e5`            |
| F9  | Low    | Fix      | A stack test timeout left without an outcome              | `857a4aa` review     |
| F10 | Low    | Fix      | The audit skill restates the workflow                     | `f1350cd`            |
| F11 | Low    | Rejected | Ox's style instructions in Claude's system prompt         | `66c5467`            |
| F12 | Low    | Rejected | The Ur plan narrows audit F1 without saying so            | `857a4aa` plan       |
| F13 | Low    | Rejected | Two session ID formats                                    | `5b1c0e5`            |
| F14 | Low    | Rejected | Codex's model line in `AGENTS.md`                         | `ee2cd33`            |
| F15 | Medium | Rejected | `d79ffe6`'s Tailscale code                                | `d79ffe6`            |

## F1. A compatibility fallback for sender labels

Medium · Fix

Before committing `4c96a77`, Codex patched the client to label messages from
transcripts recorded before `from_label` existed. `from_label` became optional
in the client's types, and `PromptBubble` falls back first to the sender's
current label, then to a truncated ID. `AGENTS.md` says to avoid compatibility
layers, and its Status section calls Ox's test state disposable. The daemon
always writes `from_label` (`internal/agent/agent.go:154,183`), so the fallback
serves only databases older than `4c96a77`.

The test is the worst part. It opens `ox.db` with `node:sqlite`, deletes
`from_label` from a stored event, and reloads the page. That tests the storage
format rather than behavior through a public interface. It also required a new
`database` field on the e2e services.

### Places

- `cmd/ox/web/src/SessionView.tsx:131-144`: `senderLabel` falls back to
  `ox.session(id)` and `id.slice(0, 8)`.
- `cmd/ox/web/src/protocol.ts:36`: `from_label?: string`.
- `docs/protocol.md:481`: "Older recorded prompts can omit `from_label`."
- `cmd/ox/web/e2e/session.spec.ts:592-620`: "a recorded message without a saved
  label still shows its sender". It also adds the `node:sqlite` import at `:8`.
- `cmd/ox/web/e2e/services.ts:54,68,101`: the `database` field, which only this
  test uses.

### Evidence

Codex's patch is in session `2026-11-20T05-42`. Codex described it as "the
sender-label fix for older transcripts". The review that followed the commit
does not mention `from_label`.

### Replacement

Make `from_label` required in `protocol.ts`. Render `props.input.from_label`
directly. Delete the test, the `database` field, the `node:sqlite` import, and
the sentence in `docs/protocol.md`. Nothing the user sees changes on a current
database.

## F2. Draft recovery is decided in two places

Medium · Fix

When a draft's create reply is lost, two paths decide whether to reattach it
without sending.

- `App.tsx` (from `5b1c0e5`) has an effect that recovers a failed draft once
  its session appears live.
- `ox.tsx` (from `4c96a77`) recovers instead of posting when the daemon
  answers `existing: true`.

Only the first path has a test. If `ox.tsx:270` always called `post()`, every
test would still pass, and a retried create would send the prompt into the
recovered session. The `5b1c0e5` commit body rules that out: "Recovering the
session leaves submission to the user."

### Places

- `cmd/ox/web/src/App.tsx:112-125`: the recovery effect.
- `cmd/ox/web/src/ox.tsx:270`: `if (result.existing) recover(); else post(...)`.
- `cmd/ox/web/e2e/session.spec.ts:849`: the only recovery test, which reaches
  the `App.tsx` effect.
- `internal/server/methods_test.go:206-254`: `TestRepeatSessionCreation` checks
  only the server side of `existing`.

### Evidence

Confirmed by reading both paths and every e2e spec. No spec makes the daemon
answer `existing: true`.

### Replacement

Have the session control decide recovery in one place. Add a test that fails
when a retried create sends the prompt.

## F3. Two oxd guards have no test

Low · Fix

`5b1c0e5` added two guards that no test covers:

- an exclusive `flock` on the socket directory around the stale-socket check
- a format check that `session/create` applies to a client-chosen `session_id`

Deleting either guard fails nothing. `AGENTS.md` Verification requires a test
that fails without the behavior.

### Places

- `cmd/oxd/main.go:193-202`: the lock. `TestListenReplacesOnlyAStaleSocket` in
  `cmd/oxd/main_test.go` exercises replacement but never two starters.
- `internal/server/methods.go:306-311`: "session_id must be 32 hexadecimal
  digits". No test contains that message or a malformed ID.

### Replacement

For the lock: hold the directory lock in the test, start `listen` in a
goroutine, check that it waits, then release the lock and check that it binds.
For the format check: add one malformed `session_id` row to
`TestSettingsValidation`.

## F4. Ur copies an unread model into every reply fragment

Low · Fix

`entry.Model` is written on every delta. `857a4aa` moved saving to every
fragment, so each token-sized line now carries the model name. Nothing reads
the field: `request()` builds messages without it, and nothing outside
`internal/ur` reads Ur's file. Its only reader is a test that decodes the
private file format line by line. That test breaks on any storage change that
keeps behavior, such as compacting a turn's fragments.

### Places

- `internal/ur/ur.go:105-110`: the `entry` type.
- `internal/ur/ur.go:208`: `Model: a.settings.Model` on every delta.
- `internal/ur/ur_test.go:596-611`: `TestResume` decodes `entry` and checks
  `role content model` per line.

### Evidence

Searched for readers of `Model` in `internal/ur`. The behavior the file check
stands in for, shown text reaching the resumed request, is already covered
through the request body (`ur_test.go:581-586`) and by
`TestResumeDuringTurn`.

### Replacement

Remove `Model` from `entry` and delete the file-line block in `TestResume`.
The field was introduced by plan `2026-11-21-005`, which listed the reply's
model as something the file holds, but no code reads it.

## F5. Ur decides where a reply starts in two places

Low · Fix

The rule that a reply is a run of consecutive assistant entries is written
twice:

- forward in `request()`, which joins each run into one message
- backward in `Handle(ended)`, which finds the final reply's text

Any change to how a reply is delimited, such as a tool or reasoning role,
must be made in both. `AGENTS.md` says to decide each rule in one place.

### Places

- `internal/ur/ur.go:219-224`: the backward scan.
- `internal/ur/ur.go:258-271`: the forward grouping.

### Replacement

Build the messages with one function. The result text is the last message's
content when its role is assistant.

## F6. A Stop assertion that cannot fail

Low · Fix

`TestStopDuringTurn` asserts that `Stop` reported no errors. After `857a4aa`,
`Stop` has no error path (`internal/ur/ur.go:334-343`). The assertion guarded
the removed `keepReply` failure, and now it reads as coverage it does not
provide.

### Places

- `internal/ur/ur_test.go:508-510`.

### Replacement

Delete the assertion.

## F7. The new-session view decides when it can create

Low · Fix

`NewSessionView` repeats `draft.control.creating() || !ox.ready()` on three
controls. `control.create` checks its own version of the rule
(`!connected() || creating()`). `SessionView` instead asks its control
(`canSend()`, `canConfigure()`). Changing when a draft can be submitted means
editing four places.

### Places

- `cmd/ox/web/src/NewSessionView.tsx:67,76,83`.
- `cmd/ox/web/src/ox.tsx:259`.

### Replacement

Add `canCreate` to the session control, and use it in the view and in
`create`.

## F8. One workspace test checks four behaviors under one name

Low · Fix

"the workspace sidebar works after opening a malformed session link"
(`cmd/ox/web/e2e/workspace.spec.ts:9`) checks the malformed link in a few
lines. It then checks three more behaviors:

- Send is held until the session list loads.
- A draft's permission mode survives a relay restart.
- `effort` is omitted from the request.

A failure in draft settings is reported under the malformed-link name.

### Replacement

Split it into tests named for what they check.

## F9. A stack test timeout left without an outcome

Low · Fix

The `857a4aa` review reports that the stack test "one terminal Ctrl-C signals
each service once" (`cmd/ox/web/stack.test.mjs`) timed out in a full
`make test`. It then passed in isolation. Codex's own report called it "an
intermittent Ctrl-C test". `AGENTS.md` Defects gives every finding Fix, Reject,
or Record. This one got none, and neither `docs/known-issues.md` nor
`docs/todo.md` mentions it.

### Places

- `agents/reviews/2026-11-22-001-ur-saves-replies-as-they-stream.md`, Checks
  run.

### Replacement

Reproduce the timeout under `make test`. Fix it, or record it in
`docs/known-issues.md` with what it waits on.

## F10. The audit skill restates the workflow

Low · Fix

`f1350cd` added a paragraph to `.claude/skills/ox-audit/SKILL.md` saying that
the user chooses a finding with the top-level agent, which hands it to its
planning child. `AGENTS.md`'s Workflow section already says this: "Each
finding the user chooses to change goes through the workflow above, with the
finding as the request for `/ox-plan`." Step 11 now tells the auditor to "give
the planning child enough context", although the auditor never hands anything
to a child. `AGENTS.md` asks each fact to live in one place.

### Places

- `.claude/skills/ox-audit/SKILL.md:16` and step 11.

### Replacement

Keep the link to the workflow and drop the restated handoff. Phrase step 11 as
what a `Plan` finding must contain.

## F11. Ox's style instructions in Claude's system prompt

Low · Rejected

`66c5467` appends "Lead with the big idea up front …" to every Claude session
Ox starts (`internal/claude/claude.go:90`). The user commissioned the wording
and the A/B runs behind it in session `2026-11-18T19-41`, and asked for the
commit. It is intended behavior, not a defect.

## F12. The Ur plan narrows audit F1 without saying so

Low · Rejected

Audit `2026-11-22-001` F1 named two persistence boundaries for the same reply,
and warned that saving sooner "would still leave a gap between their writes".
The plan saves each fragment before publishing it and leaves the history store
as it was. `857a4aa` then says it "Closes F1". The remaining gap runs in the
safe direction: Ur's file may hold one fragment the page never showed. So no
wrong state the user can see remains. Reopening the finding costs more than
the gap it closes.

## F13. Two session ID formats

Low · Rejected

`oxd` creates 26-character base32 IDs (`rand.Text`), while a client that
chooses its own ID must send 32 hex digits. Both formats are valid, and no
defect follows from the difference.

## F14. Codex's model line in `AGENTS.md`

Low · Rejected

`ee2cd33` names the Codex model for Codex-led workflows. The user gave the
instruction in session `2026-11-22T07-08`.

## F15. `d79ffe6`'s Tailscale code

Medium · Rejected

Session `c2a98478b9d449bb8ecb5cf635e6211b` is already rewriting it in the
working tree, and has deleted the review. The Workflow table records what the
commit got wrong.

## Checks run

- Scanned every Codex session log for this directory for commit output and
  patch targets.
- Checked each claimed hash for ancestry of `HEAD`. `24906fd` belongs to the
  skills repository, and the pre-Nov 18 hashes are gone.
- Ran `git blame HEAD` over all tracked files and counted surviving lines per
  Codex commit.
- Read every surviving Codex chunk. Each finding was confirmed in the current
  source. F1–F3 and F7 were confirmed by finding no test that covers the
  behavior. F4–F6 were confirmed by reading `internal/ur` in full.
- Spot-checked audit `2026-11-22-001`'s F2–F4 against the current source. All
  three are confirmed, and their todo entries are correct.
- No tests were run.
