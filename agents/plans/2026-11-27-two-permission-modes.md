# Two permission modes, Manual and Auto, for every harness

## Goal

Ox offers Claude's own permission modes (Manual, Auto, Accept edits, Plan) for
Claude and Manual and Auto for Ur. A Claude session in Accept edits or Plan
cannot start an Ur session, because the child takes its parent's mode and Ur
does not offer it ([#193](https://github.com/kkestell/ox/issues/193)).

After the change Ox offers exactly two modes, Manual and Auto, the same for
every harness, and implements both itself:

- **Manual:** the user approves each tool call the harness asks about.
- **Auto:** Ox approves each tool call itself, except a shell command that
  starts with `rm` and a question for the user, which go to the user as in
  Manual.

Claude always runs in its own manual mode, and `oxd`'s Claude adapter answers
`can_use_tool` itself in Auto. A child or top-level session can always take its
caller's mode, which resolves #193 by construction.

## Related code

- `internal/agent/agent.go` — `Settings`, `SettingsPatch`, and
  `Harness.CheckPermissionMode`, the per-harness check that goes away.
- `internal/config/config.go`, `internal/config/default.json` — the
  per-harness `permission_modes` lists, `CheckPermissionMode`, and
  `defaults.permission_mode` (`acceptEdits` today).
- `cmd/oxd/main.go` — `checkConfig` runs each harness's check over its
  configured modes.
- `internal/claude/claude.go` — `permissionModes`, `checkPermissionMode`, and
  the `--permission-mode` and `--disallowedTools` flags.
- `internal/claude/approval.go` — `controlRequest` forwards every
  `can_use_tool` to the user.
- `internal/claude/agent.go` — `permissionMode()` adopts the `permissionMode`
  Claude reports on `system` lines; `handleOutput` passes Claude's
  `session_state_changed` to `sessionState`.
- `internal/claude/configure.go` — a permission mode change is a
  `set_permission_mode` control request.
- `internal/claude/messages.go` — the `permissionMode` field.
- `internal/ur/harness.go`, `internal/ur/agent.go`,
  `internal/ur/engine/engine.go` (`next`, flag parsing) — Ur's check, its
  `-permission-mode` flag, and the engine's auto decision.
- `internal/server/mcp.go` — `startedRecord` checks the caller's mode against
  the target harness (the cause of #193); `tool.readOnly` and its
  `readOnlyHint` annotation exist for Claude's plan mode.
- `internal/server/methods.go` — `catalog`, `createSession`, `admitSessionLocked`
  (line 527), `configureSession`.
- `internal/store/store.go` — `Session.PermissionMode`, `Open`.
- `cmd/ox/web/src/protocol.ts`, `NewSessionView.tsx`, `SessionView.tsx`,
  `SettingsFields.tsx`, `ApprovalRequest.tsx` — the page reads each harness's
  `permission_modes` and renders an `ExitPlanMode` approval's plan.
- `docs/screenshots/hero.json` — the README scene uses `acceptEdits`.

## Decisions

- **One type for the mode.** `internal/agent` defines
  `type PermissionMode string` with `Manual = "manual"` and `Auto = "auto"`,
  `ParsePermissionMode(string) (PermissionMode, error)`, and an `UnmarshalJSON`
  that refuses any other value with `permission mode "x" is not manual or auto`.
  `Settings.PermissionMode`, `SettingsPatch.PermissionMode`,
  `config.Defaults.PermissionMode`, and `store.Session.PermissionMode` take the
  type. Every boundary parses into it: configuration and request params by
  JSON decoding, Ur's `-permission-mode` flag by `ParsePermissionMode`, and the
  wire's `configure` by JSON. `Harness.CheckPermissionMode`,
  `config.Harness.CheckPermissionMode`, both harnesses' `checkPermissionMode`,
  and `checkConfig`'s mode loop go. `startedRecord` drops its mode check, so a
  session started on any harness takes its caller's mode.
- **The catalog.** The configuration loses `permission_modes`. `harness/list`
  returns a top-level `permission_modes`,
  `[{"id":"manual","name":"Manual"},{"id":"auto","name":"Auto"}]`, defined once
  in `internal/server` beside `catalog`, and each harness entry loses its own
  list. `list_models` serves the same catalog. `createSession` and
  `configureSession` reply with the decode error when their params do not
  decode, so a bad mode is refused as invalid params naming the mode.
- **The rule.** `internal/agent/auto.go` holds the one copy:
  `func AutoRuns(command string) bool`, true unless the command, after leading
  whitespace, starts with `rm`. It is the only place that classifies, so a
  later rule replaces one function. Each harness adapter extracts the shell
  command from its own tool's input and asks `AutoRuns`; the session goroutine
  and the server never read a tool payload.
- **Claude.** The process always starts with `--permission-mode=manual`,
  whatever Ox's mode, and `--disallowedTools` adds `EnterPlanMode` and
  `ExitPlanMode`, so Claude's own mode never leaves manual. In
  `controlRequest`, a `can_use_tool` in `Auto` that `autoAllows` is answered at
  once with `{"behavior":"allow","updatedInput":<the request's input, raw>}`
  (`{}` when the input is not an object) and is never published. `autoAllows`
  is false for a request with `requires_user_interaction` (`AskUserQuestion`),
  `AutoRuns(input.command)` for `Bash` and `Monitor`, and true otherwise. A
  failed write is logged through `onError`, and Claude's exit ends the turn,
  as for a user's answer.
- **Waiting on the user is Ox's fact.** Claude reports `requires_action` just
  before every `can_use_tool`, including those Ox answers itself, so passing it
  through would flash "waiting on the user" for every auto-approved call. The
  Claude adapter ignores Claude's `requires_action` and reports it to the
  tracker itself when it publishes an approval. Claude's `running` after the
  answer restores `running`, as today.
- **Claude's reported mode.** `permissionMode()` and `message.PermissionMode`
  go: with the flag fixed and plan mode's tools removed, Claude's mode cannot
  change, so Ox has nothing to adopt. `OnConfigured` stays for `Configure`'s
  acknowledgements.
- **Switching modes.** A permission mode change on a running Claude process
  is applied by the adapter at once, with no control request, before any model
  or effort control requests in the same `Configure`. Ur keeps its `configure`
  command. A pending approval cannot meet a switch: `Configure` requires the
  agent idle, and going idle withdraws every approval (Claude's
  `sessionState`, Ur's turn end). The new mode applies from the next tool call.
- **Ur.** The engine keeps deciding, with the shared rule: `next` runs a shell
  call at once when `e.settings.PermissionMode == agent.Auto &&
  agent.AutoRuns(command.Command)`, and asks otherwise. The engine already owns
  the call's step and its `requires_action` state; having it always ask and
  the adapter answer would need the adapter to hide the engine's state change
  and approval update.
- **Retired modes in the store.** `store.Open` runs
  `UPDATE sessions SET permission_mode = 'manual' WHERE permission_mode NOT IN ('manual', 'auto')`
  after the schema, so every stored session holds a valid mode and shows what
  it runs. A stored Claude `auto` session keeps `auto`, now Ox's rule.
- **The user's configuration.** An existing `config.json` with
  `permission_modes` or `defaults.permission_mode: "acceptEdits"` is refused at
  startup with the decoder's reason (`unknown field "permission_modes"`, or the
  mode error). The user edits it once; no compatibility reading.
- **MCP annotations.** `tool.readOnly` and the `readOnlyHint` annotation go:
  they existed only to let plan mode run Ox's read-only tools. In Claude's
  manual mode `--allowedTools` runs every Ox tool without asking
  (`claude-code.md` §1), in both Ox modes.
- **Page.** `Harness` loses `permission_modes`; the page stores the catalog's
  top-level list and both pickers read it. A new-session draft keeps its mode
  when the harness changes, so `defaultPermissionMode` goes and `pick` keeps
  `draft.choice().permission_mode`. `ApprovalRequest` drops its plan rendering,
  since `ExitPlanMode` can no longer be requested; the transcript keeps
  `planOf` for earlier sessions' history.
- **Requirements:** Permission requests, Allow and Deny, Questions from Claude,
  Approvals wait, and One answer per approval hold unchanged for every request
  that reaches the user; an auto-approved call never becomes an approval, so a
  reload, a second page, or a reconnect has nothing to show for it. When
  settings can change holds: a mode change is refused while the session waits
  on the user, as today. Sessions can start child sessions and Sessions can
  start top-level sessions now hold across harnesses. Settings shown are
  settings used holds because Ox's mode is applied by Ox and Claude's own mode
  is fixed. Changed requirements are below.
- **Settings:** Claude's flags carry no Ox mode; `--permission-mode=manual`
  beats the user's `permissions.defaultMode` (observed with `"auto"` in the
  user's settings, 2.1.332). A parked or restarted session's mode comes from
  the store, applied by the adapter from the first request.
- **Construction:** a mode outside manual and auto cannot reach a session: the
  type is parsed at every boundary and the store is rewritten on open. Claude's
  mode cannot differ from manual. "Waiting on the user" for Claude has one
  source, the adapter's published approvals.
- **Ownership:** no new state, timer, or stored field. The Claude adapter now
  owns the `requires_action` report, which it derives when it forwards a
  request; cleanup is unchanged (`withdrawApprovals` and Claude's `running` or
  `idle`). The commit body says so.
- **Claude Code:** `can_use_tool` order and allow shape: `claude-code.md` §5;
  `requires_action` precedes every `can_use_tool`: §3 `system`. Observed live
  with Haiku on 2.1.332, to record in `claude-code.md`: in manual mode Claude
  runs `Read` and read-only Bash (`ls -la`, `cat`, `find`, `grep`,
  `git status`) and calls a settings `allow` rule matches without asking, and
  asks for `touch`, `rm`, `rm -f`, and `Write`; with `EnterPlanMode` and
  `ExitPlanMode` disallowed, `init` lists neither and `ToolSearch` cannot load
  them; a `/plan` prompt changes no mode; `--permission-mode=manual` beats the
  settings `defaultMode`.

## Check your assumptions

1. **Claude can no longer enter plan mode on its own.**
   - **Plan:** Claude loses its plan tools, so it never switches itself into a
     read-only planning mode or asks the user to approve a plan; it works in
     Manual or Auto only. The "Claude's own mode changes" requirement goes.
   - **Alternative:** let Claude still plan on its own. Ox would show a mode
     the user cannot pick, every plan approval would need to bypass Auto, and
     Ox's own tools would need a plan-mode exception again.
2. **New sessions start in Manual.**
   - **Plan:** the built-in configuration's default is Manual, since Auto runs
     everything except `rm` without asking. The user's own configuration also
     needs its default changed from Accept edits.
   - **Alternative:** default to Auto: fewer prompts, but a new session runs
     edits and commands unasked from its first turn.
3. **Manual asks only about what Claude asks about.**
   - **Plan:** in Manual, Claude still reads files and runs read-only commands
     such as `ls`, `cat`, `grep`, and `git status`, and anything the user's
     Claude settings allow, without asking, as Manual does today. Ur asks about
     every command.
   - **Alternative:** have Manual ask about every Claude tool call, reads
     included, through extra rules Ox passes to Claude: every file read and
     search would prompt.
4. **Sessions in a retired mode become Manual.**
   - **Plan:** existing sessions in Accept edits or Plan switch to Manual.
     Existing Claude sessions in Auto stay in Auto, which now follows Ox's rule
     instead of Claude's safety classifier.
   - **Alternative:** move Accept edits sessions to Auto, which keeps them
     from prompting for edits but also runs their commands unasked.

## Test plan

- Auto answers a Claude `Bash` request whose command does not start with `rm`
  with an allow echoing the input, publishes no approval, and publishes no
  `requires_action` state around it (Claude harness,
  `internal/claude/approval_test.go`, new `TestAutoApproval`: the auto path is
  new and no existing test sets the mode).
- In Auto, `rm -rf x`, an `AskUserQuestion` with
  `requires_user_interaction`, and a `Monitor` starting with `rm` are published
  as approvals with `requires_action`; in Manual a `Write` is (same test).
- Claude's `requires_action` without a following forwarded request publishes
  no state change (`TestApprovalRouting`, which already feeds
  `session_state_changed` and `can_use_tool`).
- A permission-mode change on a running Claude process writes no control
  request and reports the new settings through `OnConfigured`; a combined model
  and mode change sends only `set_model` (`configure_test.go`, replacing the
  `set_permission_mode` expectations and `TestClaudeReportsPermissionMode`).
- Claude starts with `--permission-mode=manual` in Auto, and with
  `EnterPlanMode,ExitPlanMode` disallowed (`claude_test.go`, the existing
  stand-in argument test).
- An Ur auto session's `rm notes.txt` publishes an approval and waits;
  `echo hi` runs at once (`internal/ur/ur_test.go`, an existing auto-mode
  shell test gains the `rm` call; `TestPermissionModes` goes).
- A session on one harness in `auto` starts a child on another harness, which
  takes `auto` (`internal/server/mcp_test.go`, replacing the
  `permission mode "plan" is not configured` case — the #193 regression test).
- `session/create` and `session/configure` with `acceptEdits` are refused as
  invalid params naming the mode; `harness/list` returns the top-level
  `permission_modes` (`methods_test.go`).
- `config.Load` refuses `permission_modes` and a `defaults.permission_mode`
  other than manual or auto (`config_test.go`, replacing the per-harness mode
  cases); `cmd/oxd/main_test.go` drops `TestConfigRefusesPermissionModesClaudeRefuses`
  and the mode cases in its table, and its stand-in in
  `TestDaemonOwnsSocketAndDatabaseUntilShutdown` keys the hanging start on
  `--effort=high` instead of `--permission-mode=plan`.
- `store.Open` rewrites a stored `acceptEdits` session to `manual`
  (`store_test.go`).
- e2e: the specs that pick `plan` pick `auto`; `services.ts` serves the
  top-level list; a new-session draft keeps Auto when the harness changes
  (`workspace.spec.ts`, beside "a draft keeps its permission mode through a
  relay restart").
- Live (`TestLive`): a new section "auto mode" creates a Claude session in
  `auto`, asks for `touch auto.txt` then `rm auto.txt`; the touch runs with no
  approval and no `requires_action` state, the `rm` arrives as an approval.
  "own mode change" and "plan-mode message asks" go; "list models", "read long
  transcript", and "configure keeps other settings" use `auto` instead of
  `plan` or `acceptEdits`; the misspelled-mode startup check goes.
  `approval.spec.ts` drops the `ExitPlanMode` test. `TestLiveUr` keeps its
  auto section.

## Implementation plan

1. `internal/agent`: add `PermissionMode`, `Manual`, `Auto`,
   `ParsePermissionMode`, and `UnmarshalJSON` in `agent.go`; type the
   `Settings` and `SettingsPatch` fields; remove `Harness.CheckPermissionMode`.
   Add `auto.go` with `AutoRuns`.
2. `internal/config`: remove `Harness.PermissionModes` and
   `CheckPermissionMode`; `check` requires models only; type
   `Defaults.PermissionMode`. `default.json`: drop both `permission_modes`
   lists; `defaults.permission_mode` as product question 2 settles.
3. `cmd/oxd/main.go`: remove the mode loop from `checkConfig`.
4. `internal/store`: type `Session.PermissionMode`; `Open` runs the rewrite.
5. `internal/server`: `permissionModes` and the top-level field in `catalog`;
   remove the mode checks in `admitSessionLocked`, `configureSession`, and
   `startedRecord`; decode errors in `createSession` and `configureSession`;
   remove `tool.readOnly` and the annotation in `mcp.go`.
6. `internal/claude`: `claude.go` passes `--permission-mode=manual` and
   disallows `EnterPlanMode,ExitPlanMode`, and drops `permissionModes` and
   `checkPermissionMode` (and its use in `prompt.go`); `approval.go` adds
   `autoAllows` and the auto answer, and reports `requires_action` when it
   publishes an approval; `agent.go` drops `permissionMode()` and Claude's
   `requires_action` in `sessionState`'s path; `messages.go` drops
   `PermissionMode`; `configure.go` applies the mode without a control request.
7. `internal/ur`: drop `checkPermissionMode`; the engine parses its flag with
   `ParsePermissionMode` and `next` asks `AutoRuns`.
8. Web client: `protocol.ts`, `ox.tsx` (store `permission_modes` from
   `harness/list`), `NewSessionView.tsx`, `SessionView.tsx`,
   `SettingsFields.tsx`, `ApprovalRequest.tsx`.
9. Tests as above, including `cmd/ox-mcp/main_test.go`,
   `internal/server/worktree_test.go`, `live_test.go`, and the e2e specs and
   `services.ts` that list per-harness modes.
10. `docs/screenshots/hero.json`: `acceptEdits` becomes `manual`; re-run
    `make screenshots` if the README images show the mode.
11. Documentation and requirements below; `make docs`.

## Requirement changes

- **Added** (under Approvals, after Permission requests): **Permission
  modes.** Every session, of any harness, runs in Manual or Auto. In Manual the
  user approves each tool call the agent asks about; Claude reads files, runs
  read-only commands, and makes the calls the user's Claude settings allow
  without asking. In Auto, Ox approves each tool call itself, except a shell
  command that starts with `rm` and a question for the user, which wait for the
  user as in Manual.
- **Changed:** Retired settings.
  - Old: A model or permission mode the configuration no longer offers still
    shows as the session's setting.
  - New: A model the configuration no longer offers still shows as the
    session's setting. A session in a permission mode Ox no longer offers, such
    as Accept edits or Plan, runs in Manual and shows it.
- **Removed:** Claude's own mode changes. Claude can no longer enter or leave
  plan mode, so its mode never changes on its own.
- **Changed:** Working together without approval.
  - Old: A session of any harness does each of these without asking for
    approval, except a Claude session in plan mode, which asks before each
    message it sends and each session it starts.
  - New: A session of any harness does each of these without asking for
    approval, in either permission mode.
- **Changed:** Ur's shell commands.
  - Old: … In manual mode each command asks for approval first, and a denied
    command tells Ur so and the turn goes on; in auto mode every command runs
    without asking. …
  - New: … Each command runs or asks for approval first as its session's
    permission mode says, and a denied command tells Ur so and the turn goes
    on. …
- **Changed:** Built-in catalog.
  - Old: … offering the Claude and Ur models Ox supports with their effort
    levels and permission modes.
  - New: … offering the Claude and Ur models Ox supports with their effort
    levels.

## Documentation updates

- `docs/configuration.md`: drop `permission_modes` and the Claude `default`
  note and Ur's mode sentence; `defaults.permission_mode` is `manual` or
  `auto`; update the example.
- `docs/protocol.md`: `harness/list`'s top-level `permission_modes` and its
  example; `permission_mode` in `session/create` is `manual` or `auto`, applied
  by Ox; `session/configure` without Claude's permission-mode acknowledgement
  and without "a change Claude makes on its own"; `session/configured` without
  "such as Claude leaving plan mode"; approvals reach clients only for requests
  Ox does not answer itself in auto; Ox's MCP tools run without approval in
  both modes, without `readOnlyHint`.
- `docs/architecture.md`: the `config` row; the Claude agent's auto answers
  and its `requires_action` report; the Ur engine's use of `AutoRuns`.
- `docs/harnesses/claude-code.md`: the live observations listed under Claude
  Code, in §1 (flag beats `defaultMode`; plan tools disallowed; `/plan`) and §5
  (what manual mode runs without asking).
