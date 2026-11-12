# oxd archives and unarchives sessions

- Task: `2026-11-12-010-archive-sessions`
- Status: complete
- Plan: `.agents/plans/2026-11-12-010-archive-sessions.md`

## Changes

As the plan designs it.

- `crates/ox-protocol`: `session.archive`, `session.unarchive`, `SessionInfo.archived`, `Client::archive` / `unarchive`, `AgentParked` doc; round-trip tests.
- `crates/oxd/src/store.rs`: `archived` table, `SavedSession.archived`, `set_archived`; unit test.
- `crates/oxd/src/state.rs`: `Parked` from `Live` ends an open turn with "the agent was stopped".
- `crates/oxd/src/session.rs`: `archived` flag in `SessionInfo`, `archived`/`set_archived`, `started` (waits until not `starting`), `load` refuses an archived session.
- `crates/oxd/src/worktree.rs`: `restore`.
- `crates/oxd/src/daemon.rs`, `conn.rs`: `archive_session`, `unarchive_session`, dispatch; both run in their own tasks.
- `crates/oxctl/src/main.rs`: `archive [--discard]`, `unarchive`; `ls` shows `archived`.
- `crates/oxd/tests/worktrees.rs`: five tests (round trip with restart, unsaved work, running turn, unarchive git failures, delete archived).
- `scripts/smoke-acp.py`: `check_archive`.
- `docs/architecture.md`: archiving, `oxctl`.

Changed paths: the files above.

## Deviations

- `README.md` lists no `oxctl` commands, so it is unchanged.
- The round-trip test commits the fake agent's transcript (`fake-agent-<id>.jsonl`, written in the worktree) so the resumed fake agent finds it in the restored worktree; real Claude keeps its transcript outside the worktree.
- `check_archive` in the smoke script archives a session in a non-git workspace (no worktree); worktree archive is covered by the daemon tests only.

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-rust` | passed | `cargo test -p oxd --test worktrees archiv`: 5 passed; full run exit 0 |
| `make check-scripts` | passed | ruff check and format clean |
| `make smoke-claude` | passed | `check_archive` reported `archived_session_history_survives_daemon_restart` and `unarchived_session_retains_conversation`; artifacts `/private/tmp/ox-acp-z73oc0f1` |

`make check-ui` and `make e2e` not run: no change in `crates/ox` or the UI.
Tests were not run with the fixes undone.

## Limitations

- A never-started session archiving is not tested.
- Archive of a session still `starting` in a worktree waits on the lifecycle lock; the `started` wait only covers a session without a worktree, and is not tested.
