# Parked-session rules on session, and less RPC boilerplate

## Goal

The handlers for `session/approve`, `session/interrupt`, and
`session/configure` in `internal/server/methods.go` each check
`sess.agent == nil` and decide the parked case themselves, and `configure`
also holds the effort fallback, the model check, the archived refusal, and the
`agent.ErrExited` recovery. Move those three operations onto `*session`, next to
`sendPrompt`, `destroy`, and `archive`, so the session decides what a parked
session does in one place and the handlers only parse params and reply. Cut
the boundary boilerplate the issue names: the `session_id`-only parsing, the
empty success replies, the hand-built "workspace not found" failures, and the
two copies of the session wire struct.

This is a refactor. Every method keeps its params, results, error codes, and
messages. The plan found no defect. Resolves #203.

The issue's line numbers predate #213. Current locations:
`approveSession` `methods.go:534`, `interruptSession` `methods.go:558`,
`configureSession` `methods.go:581`, the hand-built workspace failures
`methods.go:254` and `methods.go:297`, the session wire structs
`methods.go:202` (`listWorkspaces`) and `create.go:164` (`publishLocked`).
`errWorkspaceNotFound` is not unused: `create.go:69` and `create.go:114` use
it; the two hand-built failures duplicate it.

## Related code

- `internal/server/methods.go` — the RPC handlers, `onSession`, `failure`,
  `codedError`, `fileRoot`, `removeWorkspace`, `listWorkspaces`.
- `internal/server/session.go` — `*session`, its goroutine-owned `agent`
  (nil while parked) and `settings`, `startForInput`, `adopt`, `destroy`, and
  the session errors (`errArchived`, `errSessionNotFound`, `errShuttingDown`).
- `internal/server/archive.go` — `sess.archive`, `sess.isArchived`,
  `s.unarchiveLocked`.
- `internal/server/create.go` — `publishLocked`, which broadcasts
  `session/created`.
- `internal/server/mcp.go` — `attach`, which parses a `session_id`-only
  request.
- `internal/agent/agent.go` — `Agent.Approve`, `Interrupt`, `Configure`, and
  their error contract (`ErrApprovalNotFound`, `ErrBusy`, `ErrExited`).
- `internal/config` — `Harness.Effort` and `Harness.CheckModel`, the
  configuration checks `configure` runs.
- `docs/protocol.md` — `session/created` and `workspace/list` fields, which
  stay as they are.

## Decisions

- **Requirements:** Stop, Allow and Deny, One answer per approval, When
  settings can change, Effort across models, Retired settings, Archived
  sessions, Removing a workspace. Each request still runs on the session's
  goroutine through `onSession` and replies from the same place it does now:
  `approve` and an archived or parked `configure` at once, `interrupt` and a
  running `configure` from the agent's `done` callback, `unarchive` and
  `workspace/remove` under `s.mu` after their broadcasts. Ordering of replies
  against updates is therefore unchanged for every page, reconnect, and second
  page.
- **Settings:** `configure` keeps exactly today's sequence: fill the effort
  from `harness.Effort` when only the model changes, check only a changed model
  or effort with `harness.CheckModel`, then for a parked session refuse when
  archived or `adopt` the settings, and for a running one call
  `agent.Configure` and, on `agent.ErrExited`, `adopt(patch.Apply(sess.settings))`
  so changes the harness reported before it exited stay. The patch passed to
  `agent.Configure` is the one with the filled effort, as today.
- **Construction:**
  - `*session` decides the parked case for `approve`, `interrupt`, and
    `configure`; no code outside `session.go` and `archive.go` reads
    `sess.agent` to choose behavior, except `create.go:243`, which stops an
    unpublished session's agent it still owns.
  - A rejected configuration is a `*codedError` with code
    `jsonrpc.InvalidParams` returned by `sess.configure`, so `failure` maps it
    and the handler has no second error path.
  - `fileRoot` returns an `error` (`*codedError` for the selector,
    `errSessionNotFound`, `errWorkspaceNotFound`) instead of a prebuilt
    response, and `removeWorkspace` returns `errWorkspaceNotFound`,
    `errShuttingDown`, and a `*codedError{operationFailed, "workspace has
    sessions"}`. `failure` builds every response, so the not-found code and
    message live once.
  - One `sessionInfo` type is the session's wire form for both
    `workspace/list` and `session/created`. `session/created` sends it with
    `workspace_id` alongside by embedding, so the two cannot drift. A newly
    published session is never archived, and `archived` is `omitempty`, so
    `session/created` keeps its current fields; only their order in the
    object changes, which no reader depends on.
- **Ownership:** no new state, timer, retry, lock, or stored field.

## Check your assumptions

None.

## Test plan

Existing server tests already observe each moved rule and must keep passing
unchanged:

- Parked approve answers `approvalNotPending`: `TestApprovalAnsweredOnce`
  (`methods_test.go`).
- Archived configure refuses with "session is archived": `mcp_test.go`
  (the `session/configure` refusal after archiving).
- Configure that outlives its process keeps the settings:
  `TestConfigurationOutlivesItsProcess`.
- Invalid model or effort answers `InvalidParams`, and a parked configure
  reaches session lookup: `TestSettingsValidation`, and the `effort: "max"`
  case in `methods_test.go`.
- Workspace not found from the file methods: `TestWorkspaceFiles`; "workspace
  has sessions": `methods_test.go` and `mcp_test.go`.
- `workspace/list` and `session/created` fields: the tests that decode them
  across `methods_test.go` and `mcp_test.go`.

One rule has no test: a parked session answers `session/interrupt` with
success. Add that assertion to `TestApprovalAnsweredOnce`, beside the parked
approve check, before the first `session/send`.

## Implementation plan

1. `internal/server/methods.go`: add
   - `sessionIDParam(params json.RawMessage) (string, bool)`, which reads
     params holding only `session_id` and reports false when it is missing.
   - `onSessionID(reply, id, params, op func(*session))`, which replies
     `invalidParams(id, "session_id is required")` when `sessionIDParam`
     fails and otherwise calls `onSession`.
   - `replyEmpty(reply replyFunc, id json.RawMessage) func(error)`, which
     replies `failure(id, err)` or `jsonrpc.Success(id, struct{}{})`.
2. `internal/server/session.go`: add, beside `sendPrompt`,
   - `approve(requestID string, response json.RawMessage) error`: parked
     returns `agent.ErrApprovalNotFound`, else `sess.agent.Approve`.
   - `interrupt(done func(error))`: parked calls `done(nil)`, else
     `sess.agent.Interrupt(done)`.
   - `configure(patch agent.SettingsPatch, done func(error))`: the body now in
     `configureSession`'s `onSession` closure, reading the harness from
     `sess.server.config.Harnesses[sess.harness]`, returning a model check
     failure as `&codedError{jsonrpc.InvalidParams, err.Error()}` through
     `done`.
3. `internal/server/methods.go`: rewrite the handlers on the helpers.
   `getSession`, `interruptSession`, `destroySession`, and `archiveSession` use
   `onSessionID`; `unarchiveSession` uses `sessionIDParam` and
   `replyEmpty(reply, id)(s.unarchiveLocked(sessionID))` under `s.mu`.
   `approveSession`, `interruptSession`, `configureSession`, `destroySession`,
   and `archiveSession` reply through `replyEmpty`. `configureSession` keeps
   its own param checks and calls `sess.configure(input.SettingsPatch,
   replyEmpty(reply, id))`.
4. `internal/server/methods.go`: `fileRoot(input fileParams) (dir, workspace
   string, err error)` returns errors as in Decisions; its three callers reply
   `failure(id, err)`. `removeWorkspace` moves its checks, store delete,
   slice removal, and broadcast into `removeWorkspaceLocked(workspaceID string)
   error`, called under `s.mu` and answered with `replyEmpty`.
5. `internal/server/mcp.go`: `attach` parses with `sessionIDParam` and replies
   with `jsonrpc.Success(request.ID, struct{}{})` as now.
6. Wire struct: in `methods.go` define package-level `sessionInfo` (the nine
   fields `listWorkspaces` declares now, same tags) and
   `(sess *session) infoLocked() sessionInfo` (caller holds `s.mu`).
   `listWorkspaces` uses it; `publishLocked` in `create.go` broadcasts
   `struct{ WorkspaceID string \`json:"workspace_id"\`; sessionInfo }{w.id,
   sess.infoLocked()}`.
7. `internal/server/methods_test.go`: add the parked interrupt assertion.

## Requirement changes

None.
