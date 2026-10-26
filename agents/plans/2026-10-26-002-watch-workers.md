# Watch workers

## Goal

The terminal client shows every worker's transcript beside the orchestrator's,
as the stage "Next: watching workers" in
`agents/plans/2026-10-26-001-agent-teams.md` describes, without prompting
workers or switching views.

While the orchestrator has no workers, the screen is unchanged. Once it has one,
the screen splits 50/50 with a vertical line:

- The left half shows the orchestrator under a label line: its transcript,
  approval dialog, and composer.
- The right half stacks one pane per worker, of equal height. A horizontal line
  above each pane holds the worker's name.

```text
━━ orchestrator ━━━━━━━━━━━━━┳━ docs ━━━━━━━━━━━━━━━━━━━━━━━
orchestrator transcript      ┃
                             ┃  docs transcript
                             ┃
                             ┣━ server ━━━━━━━━━━━━━━━━━━━━━
composer                     ┃  server transcript
```

The orchestrator always has focus. Worker panes show only their transcript and
always follow its end. Page Up and Page Down page the orchestrator. Ctrl-T and
Ctrl-O apply to every pane. A picker still fills the screen. Worker panes use
the same margins as every other region, with no special handling for small
panes.

## Related code

- `internal/server/team.go` — `CreateAgent`, `RemoveAgent`, `wake`, and
  `workerClient`, which discards a worker's events today.
- `internal/server/server.go` — `newSession`, `loadSession`, `closeSession`,
  `deleteSession`, `notify`, and `acpClient`, whose `Send` gives each tool
  invocation its own ACP ID.
- `internal/server/convert.go` — `update` turns an agent event into a session
  update; `replay` turns a saved transcript into session updates.
- `internal/store/store.go` — `Members` returns a parent's workers ordered by
  name.
- `internal/acp/types.go` — method names and Ox's additions to ACP types.
- `internal/client/conn.go` — `HandleNotification` turns server notifications
  into events.
- `internal/tui/tui.go` — `model`, `event`, `newSession`, and `choose`, which
  reset the transcript when a session opens.
- `internal/tui/view.go` — `draw` lays out one session over the whole screen.
- `internal/tui/transcript.go` — `transcript`, which each worker pane reuses.
- `cmd/ox/e2e_test.go` — tmux tests with the fake OpenRouter server.

## Decisions

### The team notification

The client learns the team from a new server notification, `_ox/team`. ACP
reserves method names starting with an underscore for extensions. Its parameters
are the orchestrator's session ID and its workers, each with a session ID and a
name, in the order `Members` returns them:

```go
type TeamNotification struct {
	SessionID string       `json:"sessionId"`
	Members   []TeamMember `json:"members"`
}

type TeamMember struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}
```

Each notification carries the whole team, so the client replaces its list
instead of tracking additions and removals.

### Watched orchestrators

The server sends a worker's events and team notifications only while the
worker's orchestrator is watched, which means it is open on this connection.
`Server` gets `watched map[string]bool`, keyed by orchestrator ID, and
`watchMu sync.Mutex`, which guards `watched` and is held while sending any
worker's update or team notification. `active` cannot serve, since it also holds
workers and becomes set in `loadSession` before the replay.

- `watch(id)` takes `watchMu`. If the orchestrator has workers, it sends the
  team notification and then replays each worker's saved transcript, tagged with
  the worker's session ID. It then marks the orchestrator watched. `newSession`
  and `loadSession` call it just before they respond, after the orchestrator's
  own replay.
- `unwatch(id)` removes the mark. `closeSession` and `deleteSession` call it
  before `deactivate`.
- `sendTeam(parent)` sends the team notification when the parent is watched.
  `CreateAgent` calls it before `wake`, so the client knows the worker before
  its first update. `RemoveAgent` calls it after the worker is deleted.

Holding `watchMu` through `watch` keeps a running worker's updates from arriving
before its replay. Updates sent before `watch` reach the client ahead of the
team notification, and the client drops them as updates for an unknown session.
The batch a worker was running at load time therefore shows only from the point
the watch began, and saved batches show in full. This is the accepted gap.

### Worker updates

`workerClient` gets the server, the worker's ID, and its parent's ID. Its `Send`
converts the event with `update`, exactly as `acpClient.Send` does, and notifies
under `watchMu` when the parent is watched. The tool call ID mapping moves out
of `acpClient` into a `toolCallIDs` type with an
`assign(agent.Event) agent.Event` method that both clients use.
`acpClient.Approve` reads the same map.

### Client state

`client.Team` is a new event with the orchestrator's session ID and its members.
The terminal client keeps `workers []worker`, where `worker` holds the session
ID, the name, and a `transcript`.

- A `client.Team` event for the open session rebuilds `workers` in the event's
  order and keeps the transcript of each worker that stays.
- A `client.Update` for a worker's session ID goes to that worker's transcript.
  Updates for unknown session IDs are dropped, as today.
- Opening a session clears `workers` wherever the transcript is reset today.

### Layout

`draw` keeps the picker case. Otherwise, with no workers it draws the session in
the whole screen as today. With workers it splits the screen:

- The divider column is `width / 2`. The orchestrator gets the columns left of
  it, and the workers get the columns right of it.
- The current body of `draw` becomes `drawSession(buf, area, s)`, which lays out
  the transcript, approval dialog, and composer inside `area` and returns the
  cursor and layout as `draw` does now. The layout describes only the
  orchestrator.
- The orchestrator's label line, `━━ orchestrator ━━━…`, takes the top row of
  the left half, and `drawSession` gets the rows below it.
- Worker `i` of `n` gets rows `i*height/n` through `(i+1)*height/n`. Its first
  row is its label line, `┣━ name ━━━…`, with the corner on the divider column.
  The first worker's corner is `┳`, where it meets the orchestrator's line. The
  remaining rows, inset by `marginX` and `marginY`, show the end of its
  transcript.
- The divider is `┃` on every other row. Lines are drawn in `faint` and names in
  `gray`. The workers' half and the divider fill with `background`.

## Implementation plan

1. **ACP types.** In `internal/acp/types.go`, add `MethodTeam = "_ox/team"`,
   `TeamNotification`, and `TeamMember`.
2. **Server.** In `internal/server/server.go`, add `watched` and `watchMu` to
   `Server`, `watch`, `unwatch`, and `sendTeam`, and call them from
   `newSession`, `loadSession`, `closeSession`, and `deleteSession`. Move the
   tool call ID mapping into `toolCallIDs`. In `internal/server/team.go`, give
   `workerClient` its fields and `Send`, construct it in `wake`, and call
   `sendTeam` from `CreateAgent` and `RemoveAgent`.
3. **Client.** In `internal/client/conn.go`, add the `Team` event and decode
   `MethodTeam` in `HandleNotification`.
4. **Terminal client.** In `internal/tui/tui.go`, add `worker` and `workers`,
   handle `client.Team` and worker updates in `event`, clear `workers` when a
   session opens, and pass `workers` to `screen`. In `internal/tui/view.go`,
   split `draw` into `draw` and `drawSession` and draw the worker panes, the
   divider, and the label lines.
5. **README.** In the Teams section, replace "Workers have no terminal view."
   with a sentence saying the terminal client shows each worker's transcript
   beside the orchestrator's.

## Test plan

- Server, over a real ACP connection in `internal/server/server_test.go`:
  - An orchestrator prompt that calls `create_agent` sends `_ox/team` with the
    worker before any update tagged with the worker's session ID, and then the
    worker's updates.
  - After `closeSession`, the worker's updates and team notifications stop.
  - `loadSession` of an orchestrator with a worker sends the team notification
    and the worker's replayed transcript before it responds.
  - `remove_agent` sends a team notification without the worker.
- Client: `HandleNotification` turns `_ox/team` into `client.Team`.
- Terminal client in `internal/tui/tui_test.go`:
  - A team event adds panes; a later one without a worker drops its pane and
    keeps the others' transcripts.
  - A worker update reaches only that worker's transcript; an update for an
    unknown session ID is dropped.
  - Opening a session clears the panes.
- Layout in `internal/tui/view_test.go`:
  - With no workers, the frame is unchanged; the existing tests cover it.
  - With two workers, `┃` is in column `width / 2`, the label lines read `┳━ a`
    and `┣━ b` at the expected rows, each pane shows the end of its transcript,
    and the composer and cursor stay in the left half.
- `cmd/ox/e2e_test.go`: the orchestrator's reply calls `create_agent` in Auto
  mode, and the screen shows the divider, the worker's label, and the worker's
  reply. The two replies after `create_agent` are the same text, since the
  orchestrator and the worker request them in either order.
