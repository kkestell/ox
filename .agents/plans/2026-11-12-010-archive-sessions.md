# oxd archives and unarchives sessions

- Task: `2026-11-12-010-archive-sessions`
- Kind: plan
- Status: ready
- Source HEAD: `ddb1b273b492bd880310fd7d3880727afee215df`; executes on the
  commit of `2026-11-12-009-persist-session-events`
- Author: planner child of Hypercube session 945c2ca2, Claude Code, Opus 5.5
  (`claude-opus-5-5`); coordinator ID `planner`; native conversation ID
  `7eb96350-35c2-461c-9497-348302bd4237`
- Dependencies: `2026-11-12-009-persist-session-events` and
  `2026-11-12-008-park-idle-sessions`. `2026-11-12-011-app-archive` relies on
  **Contract for later units**. `2026-11-12-005-remove-session-worktrees` is
  committed context.

## Outcome and boundaries

Fourth of five units (request in `2026-11-12-007-bridge-background-work`).
Archive, as settled: the agent is stopped; the conversation is kept
read-only; the worktree is removed after the unsaved-work check; the branch is
always kept. Unarchive is `git worktree add <path> <branch>`. Ox does not copy
Hypercube's delete-branch-and-rebuild-from-commit. The history comes from
009's event log, so archive stores nothing of its own.

Done means:

- `session.archive { session_id, discard, env }` stops the agent, removes the
  worktree, and marks the session archived; `SessionInfo.archived` is true;
- an archived session's conversation is read by subscribing, with no agent;
- `session.unarchive { session_id, env }` re-adds the worktree on its branch
  and unmarks the session, which is then an ordinary `exited` session;
- `session.delete` deletes an archived session as any other;
- `oxctl archive [--discard]`, `oxctl unarchive`.

Not in this unit: the app (011), search.

## Contracts relied on

- 005 (committed): `worktree::unsaved`, `worktree::remove_worktree(git,
  worktree, discard)` (handles a missing directory), `worktree::unsaved_work`,
  `Git::for_client(env)`, `SavedWorktree { info, common_dir }`; the
  `worktrees` row cascades on session delete.
- 008: `EventKind::AgentParked`; `Session::stop(change)`; parking takes the
  lifecycle lock and never parks a subscribed session.
- 009: every session's history is in the database and loads on subscribe
  without an agent; deleting a session removes its events.

## Current system

- `Daemon::delete_session` (`crates/oxd/src/daemon.rs`): lifecycle lock,
  unsaved check unless `discard`, `stop`, `remove_worktree`,
  `apply_branch_rule`, `forget`, `close`.
- `Session::load` refuses a missing directory; `State::load` refuses anything
  but `exited`.
- `Store`: `CREATE TABLE IF NOT EXISTS`, no migrations; a new table needs no
  database deletion, a new `sessions` column would.

## Design

### Protocol (`ox-protocol`)

```rust
#[serde(rename = "session.archive")]
SessionArchive { session_id: String, #[serde(default)] discard: bool,
                 #[serde(default)] env: BTreeMap<String, String> },
#[serde(rename = "session.unarchive")]
SessionUnarchive { session_id: String,
                   #[serde(default)] env: BTreeMap<String, String> },
```

Both answer the session's `SessionInfo`, which gains `archived: bool`.
Errors: `UNSAVED_WORK` (archive, worded as delete's), `GIT` (git's message,
e.g. "invalid reference" or "is already used by worktree", verified with git
2.56), `INVALID_STATE` (archiving an archived session; unarchiving one that is
not; `session.load` of an archived session: "the session is archived"),
`STORAGE`, `NOT_FOUND`. `Client::archive` / `Client::unarchive`.
`AgentParked`'s doc gains "or the session was archived".

### Store

```sql
CREATE TABLE IF NOT EXISTS archived (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
);
```

`SavedSession.archived` comes from an `EXISTS` in `sessions()`;
`Store::set_archived(id, bool)` inserts or deletes the row. The `worktrees`
row stays while archived, so unarchive knows path, branch and repository.

### Archive (`Daemon::archive_session`)

Under the session's lifecycle lock:

1. Refuse if already archived.
2. With a worktree and without `discard`: `worktree::unsaved`, refusing with
   `UNSAVED_WORK`.
3. If the session is `starting` (a load whose lock was released), wait until
   it is not, through a `Session` helper waiting on its state.
4. `stop(Change::Parked)`. This unit makes `Parked` from `Live` end an open
   turn and pending permissions with the error "the agent was stopped", as
   `Exited` does (008 only ever parks quiet sessions, which have neither).
   Background work stops without a warning (settled entry 7).
5. With a worktree: `remove_worktree(git, worktree, discard)`, which
   re-checks unless `discard`, as delete does. The branch rule is not applied:
   the branch is always kept. A refusal or git failure answers the error and
   leaves the session `exited` and not archived.
6. `Store::set_archived(id, true)`, then set the session's flag through
   `update` so watchers see it. Written last, so any earlier failure leaves the
   session unarchived and archive can be retried.

A never-started session archives too: it has no agent and an empty history.

### Opening an archived session

Subscribing loads its history from the log (009) and delivers it; no agent
starts. `session.load` refuses it with `INVALID_STATE`, so nothing can run in
it; prompts and config changes are already refused for an `exited` session.

### Unarchive (`Daemon::unarchive_session`)

Under the lifecycle lock: refuse if not archived; with a worktree,
`worktree::restore(git, worktree)` runs `git --git-dir=<common_dir> worktree
add <path> <branch>` (new in `worktree.rs`); then `Store::set_archived(id,
false)` and clear the flag. A git failure answers `GIT` and leaves the session
archived. The session is `exited`; `session.load` resumes the agent and drops
the bridge's replay because the history is in the log. If Claude has deleted
the transcript, the resume fails with the bridge's "Claude Code session … not
found" and the session stays `exited` with its history readable (009's known
limit).

### Delete

Unchanged: an archived session's missing worktree passes `unsaved` (empty for
a missing directory), `remove_worktree` unregisters it if git still lists it,
the branch rule applies to a branch Ox created, and the cascade removes the
`archived` row and the events.

### `oxctl` and docs

`oxctl archive <session> [--discard]` (an `UNSAVED_WORK` refusal prints the
list, as `delete` does) and `oxctl unarchive <session>`; `oxctl ls` shows
`archived` in place of the state for an archived session. Docs:
`docs/architecture.md` (archive, unarchive), `README.md` where it lists
`oxctl` commands.

## Contract for later units

- `session.archive` / `session.unarchive` as above; `SessionInfo.archived`.
- An archived session is `exited`; a subscription delivers its history;
  `session.load` refuses it.
- Archive's `UNSAVED_WORK` message lists unsaved work as delete's does;
  archive never reports a kept branch.

## Check your assumptions

- **Confirmed, changed from the sketch (entry 6), then changed again by the
  user.** `oxd` persists every session's events (009); archive stores nothing
  of its own, and an archived session opens from the log with no agent.
  Known limit: continuing a conversation still needs Claude's transcript.
- **Confirmed as proposed (entry 7).** Archive refuses on unsaved work like
  Delete and offers Discard and Archive; it stops background work without a
  warning.

## Implementation sequence

1. `ox-protocol`: the two methods, `SessionInfo.archived`, `Client` methods,
   docs.
2. `store.rs`: `archived` table, `SavedSession.archived`, `set_archived`;
   unit tests.
3. `state.rs`: `Parked` from `Live` ends what is open.
4. `session.rs`: `archived` flag in `SessionInfo`; the wait-until-not-starting
   helper; `load` refuses an archived session.
5. `worktree.rs`: `restore`.
6. `daemon.rs`: `archive_session`, `unarchive_session`, dispatch; `conn.rs`
   runs both in their own tasks, as requests that do git work do.
7. `oxctl`: `archive`, `unarchive`, `ls`.
8. Docs; `scripts/smoke-acp.py` `check_archive`.

## Verification

`crates/oxd/tests/worktrees.rs` and `daemon.rs` with the fake agent and real
git:

- archive a clean worktree session: worktree gone, branch present, `archived`
  in the listing, agent process exited; a subscription delivers the
  conversation; after a daemon restart it still does, and no agent process
  starts;
- archive with an uncommitted file: `UNSAVED_WORK`, nothing changed; with
  `discard`: archived;
- archive a running session: its turn ends with "the agent was stopped";
- unarchive: worktree back on the branch, `archived` false; `load` resumes,
  the history has no replayed duplicates, and a prompt works; unarchive with
  the branch deleted or checked out elsewhere: `GIT`, still archived;
- delete an archived session: rows gone, a created branch removed when merged
  elsewhere;
- `load` of an archived session, prompt to it, archive twice:
  `INVALID_STATE`.

`scripts/smoke-acp.py` `check_archive` with real Claude: archive the session,
restart `oxd`, subscribe and find the earlier turns' text, unarchive, load,
and prompt.

Commands: `make check-rust`, `make smoke-claude`. `crates/ox` changes only to
compile against the new `SessionInfo` field; `make e2e` runs in 011.

## Handoff

Ready. Work size: protocol, a store table, two daemon operations with git,
`oxctl`, and daemon tests; a normal assignment comparable to 005, smaller than
the sketch's version now that 009 owns the history. `plan_metrics.py`:
1257 words, inside the band.
