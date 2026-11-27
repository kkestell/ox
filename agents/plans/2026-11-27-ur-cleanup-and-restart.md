# End Ur's commands before deletion settles, refuse a child cleanly, and restart Ur on rebuild

## Goal

Three Ur defects, from [issue #147](https://github.com/kkestell/ox/issues/147)
and [issue #154](https://github.com/kkestell/ox/issues/154):

- [F2](../audits/2026-11-26-main.md#f2-deleting-an-ur-session-can-leave-its-shell-command-running):
  when `ur` dies during a command and Stop reaches the agent before the exit
  event, or `ur` ignores SIGTERM and is killed, nothing kills the command's
  process group. Delete then removes the worktree under a live command. After
  this change, `Agent.Reaped` closes only once every command `ur` reported
  started and not ended has been killed, whatever order Stop and the exit come
  in.
- [F9](../audits/2026-11-26-main.md#f9-a-failed-first-write-to-ur-leaves-an-empty-child-and-blocks-retry):
  `create_session` publishes the child before its first `Send`, so a refused
  first input leaves an empty child that blocks a retry under the same name.
  After this change, the creator owns the child until its agent has accepted
  the first input. A refused input leaves no published child, no reserved name,
  and no stored row.
- [F19](../audits/2026-11-26-main.md#f19-an-ur-only-rebuild-leaves-running-ur-sessions-on-the-old-binary):
  `make restart` ignores changes to `bin/ur` and `bin/ox-mcp`. After this
  change, a change to either restarts `oxd`, which starts them.

## Related code

- `internal/process/process.go` — `Start`, `read`: the reader goroutine reads
  every stdout line, in order, through to EOF. The wait goroutine closes
  `reaped` and calls `children.Done` right after `cmd.Wait`, before the output
  is drained.
- `internal/ur/agent.go` — `Agent.groups`: filled by `handle` from delivered
  `command_started`/`command_ended` events and killed in
  `Handle(process.Exit)`. `end` clears the groups and closes `done`, which
  discards the queued events, including the exit.
- `internal/ur/engine/engine.go:429`, `:526` — `ur` emits `command_started`
  after starting a command and `command_ended` after reaping it. After SIGTERM,
  `ur` kills and reaps its commands without emitting `command_ended`.
- `internal/claude/claude.go:120` — Claude's `process.Start` call.
- `internal/server/session.go` — `destroy` waits on `session.exiting` (each
  agent's `Reaped`) before it removes the worktree. `update` broadcasts every
  update.
- `cmd/oxd/main.go` — shutdown waits on `children.WaitAll`.
- `internal/server/mcp.go` — `addChild`: store, launch, publish, then
  `acceptMessage`.
- `internal/server/methods.go` — `publishLocked` takes the state from the
  agent's snapshot.
- `cmd/ox/web/stack.mjs` — `runStack`: each service's record hashes only its
  `command`. `cmd/ox/web/dev.mjs` registers `oxd` and `ox`.

## Decisions

- **F2 owner: the reader goroutine of `ur`'s process.** It is the one component
  that sees every `command_started` and `command_ended` in order and also sees
  `ur`'s exit, whether or not the agent has ended. The group list moves out of
  `Agent` into a `commands` value in `internal/ur`. Only the reader goroutine
  uses it: `commands.parse`, the `parse` function passed to `process.Start`,
  records and validates the groups, and `commands.kill` kills those still listed
  once `ur` has exited and its output is drained. `process.read` calls this
  hook before `children.Done` and before it closes `reaped`. `Reaped` then means
  "`ur` and the commands it left are gone", across a normal Stop, a crash, a
  SIGKILL after `killAfter`, and their overlap. `destroy` and
  `children.WaitAll` need no change. The agent no longer kills anything on
  `process.Exit`.
- **Defects rule.** A `ur` that dies, or ignores SIGTERM, while a command runs is
  not everyday use. The user asked for the fix, though, and it removes state
  (`Agent.groups`) and a kill path rather than adding any, so it is planned at
  its cause.
- **Command events stop at the parser.** `commands.parse` returns `nil` for a
  valid `command_started` or `command_ended`, and `process.read` delivers no
  event for a `nil`. An invalid one, with a group ≤ 0 or an end for a group that
  did not start, becomes a `protocolError`, which the agent handles as it does
  now. The agent's `case wire.EventCommandStarted/Ended` goes away.
- **Groups `ur` already killed.** After SIGTERM, `ur` reaps its commands without
  reporting `command_ended`, so `kill` signals groups that are already gone,
  and `ESRCH` is ignored. The current exit path already does this.
- **F9 owner: the creator, until the first input is accepted.** `addChild`
  stores the row, launches the agent, and calls `acceptMessage` before it
  publishes. When `Send` fails, it calls `sess.agent.Stop()` and
  `deleteUnpublished`, then returns the error. Publication happens after that,
  under `s.mu`, and the existing shutdown check stays there. A shutdown that
  arrives during the first `Send` now refuses the child and leaves nothing
  behind, the same as one that arrives during launch.
- **Updates of an unpublished session are not broadcast.** A successful first
  `Send` publishes `prompt_queued` and `state` (and `prompt` from the fake)
  before publication. `session.update` keeps recording history and the
  session's own state for every update, but broadcasts, and calls
  `familyChangedLocked`, only when the session is a member
  (`s.sessions[sess.id] == sess`), checked under `s.mu`. Membership is the
  single source; there is no flag. Nothing is lost: `publishLocked` reads
  `state` from the snapshot, and `session/get` returns the queue and history. A
  child that becomes running cannot make its parent ready, so the skipped
  `familyChangedLocked` delivers nothing. `session/created` for a child now
  carries `running`. Every update now takes `s.mu`, a cost that has not been
  measured and is accepted.
- **Ur's write error names Ur.** `ur.Agent.Send` wraps a failed write as
  `send input to Ur: %w`, like Claude's `send prompt to Claude`, so the parent
  sees what failed.
- **F19: `oxd`'s record covers the executables it starts.** A service spec
  takes an optional `starts` list of paths. Its record's hash covers `command`
  and every path in `starts`, so a change to any of them restarts that service.
  `dev.mjs` gives `oxd` `starts: ["bin/ur", "bin/ox-mcp"]`. Restarting `oxd`
  stops every session, as an `oxd` change does now, and each later launch uses
  the new binaries.
- **Requirements:** **Deleting a session** ("stops Claude and waits for it to
  exit … removes its worktree") holds for Ur's commands, since deletion waits on
  `Reaped`. **Ur's shell commands** ("ends the processes it started") and
  **Stopping oxd** ("stops every … Ur command it runs") hold because
  `children.WaitAll` now follows the group kill. **Claude can start child
  sessions:** a refused first message is reported to the caller, and no child
  appears on any page. A page that reconnects during creation sees the child
  only after publication, already holding its first message.
- **Settling:** `Reaped` and `children.Done` now wait for the output drain. That
  drain is bounded by the read deadline set one second after `cmd.Wait`, so for
  Claude too, `Reaped` closes at most a second later than it does now, and only
  when a child holds the pipe. `kill` sends signals and does not wait; the
  groups' processes are not `oxd`'s children.
- **Process I/O:** the `Exit` event is still delivered after the last line. It
  now follows the group kill.
- **Construction:** "a command `ur` started outlives `Reaped`" cannot happen,
  because the only owner of the group list runs the kill before `Reaped`
  closes. "A published child without its first message" cannot happen, because
  publication follows a successful `Send`.
- **Ownership:** no new state, timer, or lock. `Agent.groups` moves to
  `commands`, owned by the reader goroutine. It is created at `startAgent` and
  goes away with the goroutine. The commit body states this.

## Check your assumptions

1. **A rebuild that changes only Ur restarts the whole dev daemon.**
   - **Plan:** In `make dev`, when a rebuild changes Ur (or the small helper
     Claude uses to reach Ox), the daemon restarts, the same as when the daemon
     itself changes. Every session in that dev stack stops, Claude's included,
     and each one resumes on the new binaries at its next prompt.
   - **Alternative:** Restart only the running Ur sessions and leave Claude's
     running. Turns in progress in Claude sessions would survive an Ur-only
     rebuild, but the daemon would need its own watcher for the Ur binary and
     a restart path for sessions.

## Test plan

- **F2, Ur harness (`internal/ur/ur_test.go`).** Make `TestCrashEndsCommands`
  table-driven:
  - "exit handled" keeps the current flow.
  - "stopped before the exit" sends SIGKILL to `ur`, calls `r.agent.Stop()`
    without handling events, waits on `r.agent.Reaped()` with a timeout, and
    then asserts `gone`. It fails today: Stop discards the exit and no one
    kills `sleep 60`.
- **F2, protocol errors.** `TestUnexpectedExit`'s "command without a group" case
  keeps passing through `commands.parse`. Add a case for `command_ended` of a
  group that did not start, with its current message.
- **F9, server (`internal/server/mcp_test.go`).** New
  `TestRefusedFirstMessageLeavesNoChild`. A `Start` wrapper whose agent's first
  `Send` returns an error, with a gate agent like `sendGateAgent`. The test
  asserts that:
  - `create_session` is refused with that error;
  - the page receives no `session/created` and no `session/update` for the
    child;
  - `workspace/list` and a restarted server list only the parent;
  - a retry under the same name succeeds.

  No existing test reaches a failing `Send`.
- **F9, shutdown during the first `Send`.** In
  `TestChildStartHoldsUpNoOtherSession`, the "send" case now expects the same
  outcome as "start": `daemon is shutting down`, nothing published, and only
  the parent and the unrelated session after a restart. Remove the branches
  that expected the child to survive.
- **F9, published state.** In `TestCreateSession`'s `createdChild`, expect a
  child's `session/created` state to be `running`.
- **F19, `cmd/ox/web/stack.test.mjs`.** New test "a changed executable a service
  starts restarts it". `start` gains a non-service file listed in `oxd`'s
  `starts`, and the build appends to it. Expect `restarted oxd`, a new `oxd`
  PID, and the same `ox` PID. The existing tests change only binaries that are
  services.

## Implementation plan

1. `internal/process/process.go`:
   - Add an `exited func()` parameter to `Start`, after `parse`. It may be
     `nil`. The doc says it runs on the reader goroutine once the process has
     exited and its output is read, before `Reaped` closes.
   - In `read`, the wait goroutine keeps setting the read deadlines and sending
     the error. After the output loop, `read` receives the error, calls
     `exited`, calls `children.Done(p.cmd)`, closes `p.reaped`, and then sends
     `Exit`.
   - Skip a `nil` event from `parse`, and update `Start`'s doc to say so.
2. `internal/claude/claude.go`: pass `nil` for `exited`.
3. `internal/ur/agent.go`:
   - Add `commands` with `parse(line) agent.Event`, which does the current
     parsing and the group validation and bookkeeping, and `kill()`. Each gets
     a doc stating that only the reader goroutine uses it.
   - `startAgent` passes `c.parse, c.kill`.
   - Remove `Agent.groups`, the command cases in `handle`, the kill loop and
     its comment in `Handle(process.Exit)`, and `a.groups = nil` in `end`.
   - Wrap `Send`'s write error.
   - Update `Reaped`'s doc.
4. `internal/ur/wire/wire.go`: keep the comment on the command events, which
   stays true.
5. `internal/server/mcp.go`:
   - In `addChild`, move `acceptMessage` before publication. On its error,
     call `sess.agent.Stop()` and `s.deleteUnpublished(sess)`, then return the
     error.
   - Publish under `s.mu` with the shutdown check, then `go sess.run()`.
   - Update the comments on `createSession` and `addChild`.
6. `internal/server/session.go`: in `update`, take `s.mu` for every update. Set
   `sess.state` for state and parked updates. Broadcast, and for state updates
   call `familyChangedLocked`, only when `s.sessions[sess.id] == sess`. Add a
   comment saying that a child's creator holds it outside membership until its
   first input is accepted.
7. `cmd/ox/web/stack.mjs`: hash `command` together with `spec.starts` into
   `record.hash`, at `start` and in `rebuild`. Document `starts` in
   `runStack`'s comment. `cmd/ox/web/dev.mjs`: give `oxd`
   `starts: ["bin/ur", "bin/ox-mcp"]`.
8. Tests as in the test plan, then the documentation updates.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`:
  - In the `Agent.Reaped` bullet, say that `ur`'s reader kills the process
    groups of the commands `ur` reported and did not end, after `ur` exits and
    before `Reaped` closes.
  - In the Ur adapter paragraph, move the command groups from the adapter to
    the reader.
  - Reorder the `create_session` creation flow: store and launch, then hand
    over the first message (a refusal stops the agent and deletes the row),
    then publish under `Server.mu` unless shutting down, then start the
    goroutine. Note that updates are broadcast only for member sessions.
  - Change the `make dev` sentence to: restarts each binary that changed, and
    `oxd` when `ur` or `ox-mcp` changed.
- `docs/protocol.md`:
  - In the `session/created` row, a child is `running` with its first message,
    in place of `idle` and started with it.
  - In the `create_session` refusals, add: a first message the child's harness
    refuses, which returns its error, and no child is created.
