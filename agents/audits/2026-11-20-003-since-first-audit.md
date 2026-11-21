# Audit since the first structural audit

## Scope and evidence

The repository at `604ed8d` ("Add Ur, a built-in harness that chats through OpenRouter"). The working tree differed only by the staged move of the deslop audit from `agents/reviews/2026-11-20-014-deslop.md` to `agents/audits/2026-11-20-002-deslop.md`; no code differed from `604ed8d`. The history covers the 23 commits from `aab647d`, where the first audit stopped, to `604ed8d`.

Earlier audits read:

- `2026-11-20-001-whole-repository.md`. Both of its `Plan` findings have landed: F1 in `ac6f188` (start and stop off the session goroutine) and F2 in `e347e89` (the agent owns every untaken input). Two of its rejections rest on evidence that has since changed, and come back here as F2 and F3.
- `2026-11-20-002-deslop.md`, taken at `32db923`. None of its 23 findings has landed. Its reproductions are used here as evidence of defects.

Also read: `AGENTS.md`, `docs/functional-requirements.md`, `docs/known-issues.md`, `docs/architecture.md`; the protocol's label, snapshot, and update sections; the reviews `2026-11-20-005` through `2026-11-20-015`; the create-session, archive, and Ur plans; and the code of `internal/server` (sessions, methods, MCP tools), `internal/agent`, `internal/fake`, `internal/ur`, the Claude agent's prompt, configure, and lifecycle code, `cmd/oxd`, and the web client's `ox.tsx` and `App.tsx`.

Gaps:

- In the web client, only the label, selection, and composer code was read. `Sidebar.tsx`, `FileTree.tsx`, `FileView.tsx`, and the tests were covered only through the reviews and the deslop audit.
- The deslop audit's reproductions were not repeated, except F11 (fake idle with a queued input), F9's configuration lead, and F12's trim difference. All three were rechecked at `604ed8d`.
- No live Claude check was run. F1's configuration case depends on Claude reporting `running` before it acknowledges a control request, which has not been observed.

## Summary

| ID | Cost   | Action   | Title                                                                   | Places |
| -- | ------ | -------- | ----------------------------------------------------------------------- | ------ |
| F1 | Medium | Plan     | Each harness keeps its own input queue and sets its published state     | 12     |
| F2 | Low    | Plan     | A session's label is derived on both sides of the protocol              | 10     |
| F3 | Low    | Plan     | Harness configuration rules are chosen by harness name in `cmd/oxd`     | 6      |
| F4 | Low    | Rejected | A session's archived flag and its agent have different owners           | 7      |
| F5 | Low    | Rejected | Each place that resolves a path decides whether to clean it lexically   | 4      |

## F1. Each harness keeps its own input queue and sets its published state

Medium · Plan

Since `e347e89`, the harness owns every input it has not taken. The rules for those inputs are written as a comment on `Agent.Send`, and each harness implements them separately: Claude, the fake, and now Ur. Each has its own queue, its own `prompt_queued`, `prompt`, and `prompt_cancelled` publishing, its own drop on interrupt and on stop, and its own copy of the queue in `Snapshot`.

The execution state clients see is stored in each harness and set by individual transitions, when it could be derived. Two rules govern it. A session is `running` from the moment an input is accepted until no input is untaken. It is `background` while idle with tasks running. Claude enforces the first rule with an explicit set in `Send` and a guard in `sessionState`. The fake sets `running` only when a turn begins. Ur sets it when a turn begins, which it does at once whenever no turn is running. The `background` rule exists as identical `published` and `publishState` functions in Claude and the fake.

Each place that writes the state can write a value the rules forbid. The server trusts the result. `session.hold` uses the published state to decide whether archive may stop a session, and `sendPrompt` reads the snapshot's queue.

Two wrong states are reachable at this revision:

- **The fake reports idle with an input queued.** This happens when an input is sent during a configuration. Archive then accepts the session, stops it, and cancels the accepted prompt.
- **A Claude configuration acknowledgement resets a reported `running` to `idle`.** `ready()` sets `idle` whenever nothing is untaken, whatever Claude last reported. The agent then counts as parkable, and `Interrupt` writes nothing.

### Places

- `internal/agent/agent.go:23-33` — the `Send` contract, which every harness must implement in full.
- `internal/claude/prompt.go:30-50` — `Send` queues, publishes `prompt_queued`, and sets `running` itself.
- `internal/claude/prompt.go:82-88` — `ready` sets `idle` when nothing is untaken.
- `internal/claude/prompt.go:90-97` — `dropUntaken`.
- `internal/claude/prompt.go:160-180` — `sessionState` ignores Claude's `idle` while inputs are untaken.
- `internal/claude/prompt.go:182-213` — `setState`, `published`, `publishState`, and `endPrompt`.
- `internal/fake/fake.go:129-143` — `published` and `publishState`, the same code as Claude's.
- `internal/fake/fake.go:161-177` — `Send` queues, and `begin` sets `running` only when a turn starts.
- `internal/fake/fake.go:203-210` — `drop`.
- `internal/ur/ur.go:143-159` — `Snapshot` derives `running` from the turn, and `Send` queues.
- `internal/ur/ur.go:204-211` — `drop`.
- `internal/server/session.go:118`, `:197-210` — `sendPrompt` reads `Queued`, and `hold` reads `State` to decide whether archive may stop the session.

### Defects

- Deslop F11: the fake reported `idle` with an accepted prompt queued, and archive stopped the session and cancelled the prompt. Unfixed.
- Deslop F9, second bullet: a configuration acknowledgement reset `running` to `idle`. The deslop audit recorded it as a lead that needs a live reproduction. Unfixed, and not in `docs/known-issues.md`.
- Review 2026-11-20-012 F1: Claude's `Send` published `published()` at a point where the state is always `running`. Fixed by publishing the field directly. That fix touched one of the transitions that set the state.
- Review 2026-11-20-015 F1: no test covered Ur's `Stop` dropping queued inputs. Fixed with an Ur-only test. Each harness needs its own tests of the same contract.
- `32db923` added the `background` derivation to Claude and the fake as two identical copies.

### Evidence

- A probe test in `internal/fake` called `Configure`, then `Send`, and read `Snapshot`: `state=idle queued=1`.
- A probe test in `internal/claude` initialized the agent, started a configuration, delivered Claude's `running` state, then the acknowledgement: `state=idle parkable=true`.
- Both probe files were deleted.
- Traced from the source: the server's archive check and prompt refusal read only what each harness publishes, so a harness that breaks the contract changes what archive stops.

### Replacement

One type in `internal/agent`, used by every harness, owns the untaken inputs and the execution state the harness reports. It publishes the input lifecycle updates and reports the queue in `Snapshot`. It derives the published state, publishing it when it changes: `running` while any input is untaken, `background` while the reported state is `idle` with tasks running, and otherwise the reported state. Each harness still decides when an input is written, taken, or dropped, which is why `e347e89` made the harness the owner. The type keeps that decision with the harness and takes only the bookkeeping and the state rules. Two real harnesses now need the type, which meets `AGENTS.md`'s bar for changing `internal/agent`.

What it removes:

- the three queue-and-drop implementations, and the copying in each `Snapshot`;
- the two `published`/`publishState` copies;
- Claude's explicit `running` set in `Send`;
- the untaken-input case in `sessionState`;
- the `idle` set in `ready`;
- the window between the fake's `Send` and `begin`.

Both wrong states above become impossible to build.

The change touches `internal/agent`, all three harnesses, and the harness tests that assert exact update sequences. The order or number of `state` updates may shift. It may change what the user sees in two ways: the fake reports `running` from acceptance, and after a configuration Claude's state follows Claude's own report. The plan settles both with the user.

## F2. A session's label is derived on both sides of the protocol

Low · Plan

`oxd` derives a label from the stored first user prompt. The page derives its own label from the transcript it has loaded. Before the transcript loads, the page shows the label `oxd` listed, and for a session another page just created it shows the ID prefix. Both sides apply the child-title rule. `workspace/list` runs one store query per session while holding `Server.mu`.

The first audit (its F3) rejected this for two reasons: the derivations agreed on every visible input, and the listed label existed only because `session/get` waited on starts. Neither reason holds any longer. `ac6f188` removed the wait, and the deslop audit (F12) reproduced three divergences:

- **Reconnect:** the page keeps a reloading snapshot's old label over the fresh listed label.
- **Trim:** Go strips a leading U+0085 and JavaScript does not.
- **Failed write:** the page shows a prompt whose write failed, while `oxd` labels the session by its ID.

### Places

- `internal/store/store.go:277` — `FirstPrompt`.
- `internal/server/mcp.go:643` — `Server.label`, with the child-title rule.
- `internal/server/methods.go:186` — `listWorkspaces` derives each label while holding `s.mu`.
- `internal/server/mcp.go:261`, `:305`, `:390` — `list_sessions`, `send_message`, and `create_session` derive labels.
- `cmd/ox/web/src/ox.tsx:48-50` — `Session.label`, the listed label, shown until a snapshot loads.
- `cmd/ox/web/src/ox.tsx:75-81` — `sessionLabel`, the page's own derivation, with the child-title rule again.
- `cmd/ox/web/src/ox.tsx:395` — reload overwrites the listed label, which `sessionLabel` ignores while any snapshot exists.
- `cmd/ox/web/src/ox.tsx:432` — the ID label for a session created on another page.
- `cmd/ox/web/src/SessionView.tsx:131-133` — a message without `from_label` falls back to the sender's current label.

### Defects

- Review 2026-11-19-004 F10 and F31, and review 2026-11-20-001 F1, as the first audit lists them.
- Deslop F12: the three divergences above. Unfixed.

### Evidence

- Rechecked the trim case at `604ed8d`. For `"\u0085\nActual title"`, Go's `strings.TrimSpace` followed by a cut at the newline gives `Actual title`. JavaScript's `.trim().split("\n")[0]` gives the lone U+0085.
- Traced the reconnect case: `sessionLabel` reads `session.load.snapshot`, which a reloading session keeps, before the listed `session.label`.

### Replacement

The session owns its label, as `oxd` derives it, and the page shows what `oxd` sends. A child's label is fixed at creation. Any other session's label changes once, when its goroutine records the first user prompt. `oxd` then sends the label in listings and announces the change. The plan decides how: a new notification or a field on an existing message.

What it removes:

- the page's derivation, and the rule for which label to show before a snapshot loads;
- the ID label on `session/created`;
- the store query per session under `s.mu` in `workspace/list`.

The change touches the protocol, the server's label sources, and the page's session store. It may change what the user sees for a session whose first prompt failed to store (**Database failures**, **Session labels**), which the plan settles with the user.

## F3. Harness configuration rules are chosen by harness name in `cmd/oxd`

Low · Plan

`checkConfig` switches on the harness name. For `claude` it refuses the `default` alias and checks effort levels with `claude.CheckEffort`. For `ur` it checks permission modes and efforts with Ur's own functions. For every harness other than `ur`, it refuses a model with `providers`. The rules belong to each harness's package, but `cmd/oxd` decides which rules apply to which harness. `providers` is a field on every configured model, though only Ur reads it.

The first audit (its F6) rejected this as cheaper to keep "while one harness has configuration rules". It added that "a second harness with its own rules would earn a check on `agent.Harness`, beside `CheckPrompt`." Ur is that second harness.

### Places

- `cmd/oxd/main.go:124-156` — `checkConfig`, with its name switch and the providers rule.
- `cmd/oxd/main.go:46-50` — the harness map, which passes Ur its provider orders from the configuration.
- `internal/claude/claude.go:112-116` — Claude's efforts and `CheckEffort`.
- `internal/ur/ur.go:23-41` — Ur's efforts, `CheckEffort`, and `CheckPermissionMode`.
- `internal/config/config.go:38-39`, `:161-169` — `Model.Providers` and `Harness.Providers`.
- `internal/agent/agent.go:62-65` — `Harness`, which has `CheckPrompt` but no configuration check.

### Defects

- Review 2026-11-19-004 F20 and `22d60d8`, each fixed by adding a case to `checkConfig`.
- `604ed8d` added the `ur` case and the providers rule in the same way.

No defect from the current rules was found.

### Evidence

Traced: every rule is checked once at startup. Adding a harness with rules means adding a case to `checkConfig`, as both earlier fixes and the Ur change did.

### Replacement

`agent.Harness` carries each harness's configuration checks beside `CheckPrompt`: its effort levels, its permission modes, and whether it takes a provider order. Each harness supplies its own checks, and `checkConfig` runs them without naming any harness. This removes the name switch and the harness-specific providers rule from `cmd/oxd`. The change touches `agent.Harness`, the Claude and Ur harness values, `checkConfig`, and its tests. It does not change what the user sees.

## F4. A session's archived flag and its agent have different owners

Low · Rejected

`session.archived` is guarded by `Server.mu`, while the agent that an archived session must not have is owned by the session's goroutine. Because of the split:

- the goroutine reads the flag under the lock;
- archive holds every family member's goroutine from the request goroutine;
- unarchive touches no goroutine.

### Places

- `internal/server/session.go:31-33` — the flag.
- `internal/server/session.go:170-184` — `startForInput` and `isArchived`.
- `internal/server/session.go:186-243` — `hold` and `archive`.
- `internal/server/session.go:247-288` — `unarchive` and `setArchivedLocked`.
- `internal/server/methods.go:514-518` — `configureSession`'s parked path.
- `internal/server/mcp.go:246`, `:408` — `list_sessions` and `create_session` read the flag.
- `internal/server/methods.go:557-581` — `archiveSession` looks the session up, then acts after releasing the lock.

### Defects

- Deslop F3: `create_session` publishes the child before its goroutine owns the first message, so an archive in between makes creation delete the child.
- Deslop F4: unarchive acts on a session pointer captured before a delete, and archive replies before the members have stopped.

Both are unfixed.

### Evidence

Traced from the source. The archive plan (2026-11-20-010, Ownership) chose the owner on purpose: "It is a membership-like fact: list filtering, `workspace/list`, the cascade, and the parent check all read it with membership under `s.mu`."

### Replacement

Rejected: neither defect comes from the flag's owner, and keeping the owner is cheaper than moving it.

- Deslop F3 comes from creation publishing the child before its goroutine owns the first message. Its correction hands the message over before publication.
- Deslop F4 comes from unarchive acting on a stale pointer and from the archive reply's timing. Its correction looks the session up by ID under `s.mu`, and waits for the stops if success is to mean they have happened.

Moving the flag to the goroutine would make `workspace/list` read it from every goroutine or keep a copy. That would give up the plan's single ordering of membership changes and listings. Deslop F3 and F4 remain open as fixes.

## F5. Each place that resolves a path decides whether to clean it lexically

Low · Rejected

`workspace/create` and `create_session` keep a path as given, so that the filesystem resolves a symlink before any following `..`. Two other places clean lexically: `oxd`'s `-socket` flag, through `filepath.Abs`, and the file tree's symlink classification, through `path.Join`.

### Places

- `internal/server/methods.go:124-126` — `createWorkspace` keeps the path as given.
- `internal/server/mcp.go:360-366` — `create_session` keeps `dir` as given.
- `cmd/oxd/main.go:38` — `filepath.Abs` cleans `-socket`.
- `internal/server/files.go:55` — `path.Join` cleans before classifying a link.

### Defects

- `f2c49fd`: `workspace/create` cleaned lexically; fixed in place.
- Deslop F19: the socket flag and the file tree's link classification. Unfixed.

### Evidence

Traced from the source. The deslop audit reproduced both cases.

### Replacement

Rejected: cheaper to fix in place. Each place is one line, and a shared helper would not stop the next caller from calling `filepath.Join`. Deslop F19 remains open as a fix.

## Checks run

- A probe test in `internal/fake` at `604ed8d`: `Configure` then `Send` left `Snapshot` at `state=idle` with one input queued. Deleted afterwards.
- A probe test in `internal/claude` at `604ed8d`: a `running` report during a configuration, then the acknowledgement, left the agent `idle` and parkable. Deleted afterwards.
- Compared Go's `strings.TrimSpace` with JavaScript's `trim` on `"\u0085\nActual title"`: `Actual title` against a lone U+0085.
- `git status` after the checks showed only the staged move of the deslop audit and this audit. No other repository file was changed.
