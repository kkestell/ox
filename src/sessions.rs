//! SQLite-backed session storage.
//!
//! Everything lives in one database, `{data}/ox.db`, where `{data}` is
//! `$XDG_DATA_HOME/ox`, defaulting to `~/.local/share/ox` (`$OX_DATA_DIR`
//! overrides the data dir, mainly for tests).
//!
//! Three tables:
//!
//! - `workspaces`, one row per directory a session was started in, so the
//!   path is stored once however many sessions share it;
//! - `sessions`, one row per session: its workspace, its title once a prompt
//!   gives it one, and the creation and last-activity times ACP reports;
//! - `events`, the conversation transcript, one row per record, ordered by
//!   its rowid and deleted with its session.
//!
//! Timestamps are RFC 3339 UTC strings with millisecond precision, which sort
//! lexicographically in the order they happened, so `ORDER BY updated_at`
//! needs no date parsing.
//!
//! Session ids go into the database as values rather than into a file name,
//! so there is nothing to sanitise: a strange id can only ever name a row
//! that is not there.

use std::fs;
use std::io::{self, ErrorKind};
use std::path::{Path, PathBuf};

use agent_client_protocol::schema::v1::{SessionId, SessionInfo};
use chrono::{SecondsFormat, Utc};
use rusqlite::{Connection, OptionalExtension, params};

/// Environment variable overriding the data directory (mainly for tests).
pub const DATA_DIR_ENV: &str = "OX_DATA_DIR";

/// Name of the database file inside the data directory.
const DATABASE_FILE: &str = "ox.db";

/// Longest title derived from a prompt, in characters.
const MAX_TITLE_CHARS: usize = 80;

/// Schema, applied on every open; each statement is a no-op once it has run.
const SCHEMA: &str = "
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS workspaces (
    id   INTEGER PRIMARY KEY,
    path TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS sessions (
    id           TEXT PRIMARY KEY,
    workspace_id INTEGER NOT NULL REFERENCES workspaces (id),
    title        TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_by_activity
    ON sessions (updated_at DESC, id);

CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    ts         TEXT NOT NULL,
    kind       TEXT NOT NULL,
    data       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS events_by_session ON events (session_id, id);
";

/// One record in a session's transcript.
//
// Written on every prompt; nothing reads it back yet, which is what
// `session/load` will do once it replays a session to the client.
#[allow(dead_code)]
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Event {
    /// RFC 3339 UTC time the record was written.
    pub ts: String,
    /// What kind of record this is, e.g. `"user_message"`.
    pub kind: String,
    /// The record itself, as JSON.
    pub data: serde_json::Value,
}

fn invalid_input(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidInput, message.into())
}

/// The current time as an RFC 3339 UTC string.
///
/// Millisecond precision so that sessions created in quick succession still
/// sort in the order they happened.
fn now() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true)
}

/// Resolve the path of the database.
pub fn database_path() -> io::Result<PathBuf> {
    if let Ok(dir) = std::env::var(DATA_DIR_ENV)
        && !dir.is_empty()
    {
        return Ok(PathBuf::from(dir).join(DATABASE_FILE));
    }
    if let Ok(xdg) = std::env::var("XDG_DATA_HOME")
        && !xdg.is_empty()
    {
        return Ok(PathBuf::from(xdg).join("ox").join(DATABASE_FILE));
    }
    let home = std::env::var("HOME").map_err(|_| invalid_input("HOME is not set"))?;
    Ok(PathBuf::from(home)
        .join(".local/share/ox")
        .join(DATABASE_FILE))
}

/// Open the database, creating it and its directory if they are not there.
fn open() -> io::Result<Connection> {
    let path = database_path()?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let connection = Connection::open(&path).map_err(io::Error::other)?;
    connection.execute_batch(SCHEMA).map_err(io::Error::other)?;
    Ok(connection)
}

/// The id of the `workspaces` row for `cwd`, inserting it if it is new.
fn workspace_id(connection: &Connection, cwd: &Path) -> rusqlite::Result<i64> {
    let path = cwd.to_string_lossy();
    connection.execute(
        "INSERT OR IGNORE INTO workspaces (path) VALUES (?1)",
        params![path],
    )?;
    connection.query_row(
        "SELECT id FROM workspaces WHERE path = ?1",
        params![path],
        |row| row.get(0),
    )
}

/// Record a new session in `cwd`.
///
/// Idempotent: re-creating an existing session leaves its row, and so its
/// title and creation time, alone.
pub fn create_session(cwd: &Path, session_id: &SessionId) -> io::Result<()> {
    create_session_in(&open()?, cwd, session_id).map_err(io::Error::other)
}

fn create_session_in(
    connection: &Connection,
    cwd: &Path,
    session_id: &SessionId,
) -> rusqlite::Result<()> {
    let workspace = workspace_id(connection, cwd)?;
    connection.execute(
        "INSERT OR IGNORE INTO sessions (id, workspace_id, created_at, updated_at)
         VALUES (?1, ?2, ?3, ?3)",
        params![session_id.to_string(), workspace, now()],
    )?;
    Ok(())
}

/// Check whether `cwd` has a session with this id.
pub fn session_exists(cwd: &Path, session_id: &SessionId) -> bool {
    open().is_ok_and(|connection| session_exists_in(&connection, cwd, session_id).unwrap_or(false))
}

fn session_exists_in(
    connection: &Connection,
    cwd: &Path,
    session_id: &SessionId,
) -> rusqlite::Result<bool> {
    connection
        .query_row(
            "SELECT 1 FROM sessions
             JOIN workspaces ON workspaces.id = sessions.workspace_id
             WHERE sessions.id = ?1 AND workspaces.path = ?2",
            params![session_id.to_string(), cwd.to_string_lossy()],
            |_| Ok(true),
        )
        .optional()
        .map(|found| found.unwrap_or(false))
}

/// List sessions, optionally limited to one workspace.
///
/// Most recently updated first, ties broken by id so the order is stable.
pub fn list_sessions(cwd: Option<&Path>) -> io::Result<Vec<SessionInfo>> {
    list_sessions_in(&open()?, cwd).map_err(io::Error::other)
}

fn list_sessions_in(
    connection: &Connection,
    cwd: Option<&Path>,
) -> rusqlite::Result<Vec<SessionInfo>> {
    let filter = cwd.map(|cwd| cwd.to_string_lossy().into_owned());
    let mut statement = connection.prepare(
        "SELECT sessions.id, workspaces.path, sessions.title, sessions.updated_at
         FROM sessions
         JOIN workspaces ON workspaces.id = sessions.workspace_id
         WHERE ?1 IS NULL OR workspaces.path = ?1
         ORDER BY sessions.updated_at DESC, sessions.id ASC",
    )?;
    let sessions = statement
        .query_map(params![filter], |row| {
            let id: String = row.get(0)?;
            let path: String = row.get(1)?;
            let title: Option<String> = row.get(2)?;
            let updated_at: String = row.get(3)?;
            Ok(SessionInfo::new(SessionId::new(id), PathBuf::from(path))
                .title(title)
                .updated_at(updated_at))
        })?
        .collect::<rusqlite::Result<Vec<_>>>()?;
    Ok(sessions)
}

/// Record activity on a session: bump `updated_at`, and adopt `title` if the
/// session has not been given one yet.
///
/// A session that is not there is left alone rather than resurrected.
pub fn record_activity(session_id: &SessionId, title: Option<String>) -> io::Result<()> {
    record_activity_in(&open()?, session_id, title).map_err(io::Error::other)
}

fn record_activity_in(
    connection: &Connection,
    session_id: &SessionId,
    title: Option<String>,
) -> rusqlite::Result<()> {
    connection.execute(
        "UPDATE sessions
         SET updated_at = ?2, title = COALESCE(title, ?3)
         WHERE id = ?1",
        params![session_id.to_string(), now(), title],
    )?;
    Ok(())
}

/// Append a record to a session's transcript.
pub fn append_event(
    session_id: &SessionId,
    kind: &str,
    data: &serde_json::Value,
) -> io::Result<()> {
    append_event_in(&open()?, session_id, kind, data).map_err(io::Error::other)
}

fn append_event_in(
    connection: &Connection,
    session_id: &SessionId,
    kind: &str,
    data: &serde_json::Value,
) -> rusqlite::Result<()> {
    connection.execute(
        "INSERT INTO events (session_id, ts, kind, data) VALUES (?1, ?2, ?3, ?4)",
        params![session_id.to_string(), now(), kind, data.to_string()],
    )?;
    Ok(())
}

/// Read a session's transcript, oldest record first.
#[allow(dead_code)]
pub fn events(session_id: &SessionId) -> io::Result<Vec<Event>> {
    events_in(&open()?, session_id).map_err(io::Error::other)
}

#[allow(dead_code)]
fn events_in(connection: &Connection, session_id: &SessionId) -> rusqlite::Result<Vec<Event>> {
    let mut statement = connection
        .prepare("SELECT ts, kind, data FROM events WHERE session_id = ?1 ORDER BY id ASC")?;
    let events = statement
        .query_map(params![session_id.to_string()], |row| {
            let data: String = row.get(2)?;
            Ok(Event {
                ts: row.get(0)?,
                kind: row.get(1)?,
                data: serde_json::from_str(&data).unwrap_or(serde_json::Value::Null),
            })
        })?
        .collect::<rusqlite::Result<Vec<_>>>()?;
    Ok(events)
}

/// Turn the first line of a prompt into a session title.
///
/// `None` when there is nothing worth showing, so an empty prompt does not
/// claim the one chance a session has to be titled.
pub fn title_from_prompt(text: &str) -> Option<String> {
    let line = text.lines().find(|line| !line.trim().is_empty())?.trim();
    let mut title: String = line.chars().take(MAX_TITLE_CHARS).collect();
    if line.chars().count() > MAX_TITLE_CHARS {
        title.push('…');
    }
    Some(title)
}

/// Delete a session and its transcript.
///
/// Returns `Ok(true)` if there was a session to delete.
pub fn delete_session(session_id: &SessionId) -> io::Result<bool> {
    delete_session_in(&open()?, session_id).map_err(io::Error::other)
}

fn delete_session_in(connection: &Connection, session_id: &SessionId) -> rusqlite::Result<bool> {
    let deleted = connection.execute(
        "DELETE FROM sessions WHERE id = ?1",
        params![session_id.to_string()],
    )?;
    Ok(deleted > 0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::DateTime;
    use serde_json::json;

    const CWD: &str = "/Users/kyle/projects/ox";
    const OTHER_CWD: &str = "/Users/kyle/projects/other";

    /// An empty database with the schema applied.
    fn db() -> Connection {
        let connection = Connection::open_in_memory().unwrap();
        connection.execute_batch(SCHEMA).unwrap();
        connection
    }

    fn test_id(id: &str) -> SessionId {
        SessionId::new(id.to_owned())
    }

    fn cwd() -> &'static Path {
        Path::new(CWD)
    }

    fn ids(sessions: &[SessionInfo]) -> Vec<String> {
        sessions
            .iter()
            .map(|session| session.session_id.to_string())
            .collect()
    }

    /// Rewrite a session's timestamps rather than sleeping: two creates in the
    /// same millisecond would otherwise leave the order to the id tie-break.
    fn set_updated_at(connection: &Connection, session_id: &SessionId, ts: &str) {
        connection
            .execute(
                "UPDATE sessions SET updated_at = ?2 WHERE id = ?1",
                params![session_id.to_string(), ts],
            )
            .unwrap();
    }

    #[test]
    fn create_list_delete_session() {
        let db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");

        create_session_in(&db, cwd(), &id).unwrap();
        assert!(session_exists_in(&db, cwd(), &id).unwrap());

        let sessions = list_sessions_in(&db, Some(cwd())).unwrap();
        assert_eq!(sessions.len(), 1);
        assert_eq!(sessions[0].session_id.to_string(), id.to_string());
        assert_eq!(sessions[0].cwd, cwd());
        assert_eq!(sessions[0].title, None);
        let updated_at = sessions[0]
            .updated_at
            .as_deref()
            .expect("ACP reports last activity");
        assert!(
            DateTime::parse_from_rfc3339(updated_at).is_ok(),
            "updated_at is RFC 3339: {updated_at:?}"
        );

        assert!(delete_session_in(&db, &id).unwrap());
        assert!(!session_exists_in(&db, cwd(), &id).unwrap());
        assert!(list_sessions_in(&db, Some(cwd())).unwrap().is_empty());
        assert!(
            !delete_session_in(&db, &id).unwrap(),
            "second delete finds nothing"
        );
    }

    #[test]
    fn a_workspace_is_stored_once_however_many_sessions_share_it() {
        let db = db();
        create_session_in(&db, cwd(), &test_id("a")).unwrap();
        create_session_in(&db, cwd(), &test_id("b")).unwrap();
        create_session_in(&db, Path::new(OTHER_CWD), &test_id("c")).unwrap();

        let workspaces: i64 = db
            .query_row("SELECT count(*) FROM workspaces", [], |row| row.get(0))
            .unwrap();
        assert_eq!(workspaces, 2);
    }

    #[test]
    fn sessions_are_scoped_per_workspace() {
        let db = db();
        let other = Path::new(OTHER_CWD);
        let mine = test_id("aaaaaaaa-0000-4000-8000-000000000001");
        let theirs = test_id("bbbbbbbb-0000-4000-8000-000000000002");

        create_session_in(&db, cwd(), &mine).unwrap();
        create_session_in(&db, other, &theirs).unwrap();

        assert!(session_exists_in(&db, cwd(), &mine).unwrap());
        assert!(
            !session_exists_in(&db, other, &mine).unwrap(),
            "a session belongs to the workspace it was created in"
        );

        assert_eq!(
            ids(&list_sessions_in(&db, Some(cwd())).unwrap()),
            vec![mine.to_string()]
        );
        assert_eq!(
            ids(&list_sessions_in(&db, Some(other)).unwrap()),
            vec![theirs.to_string()]
        );

        let all = list_sessions_in(&db, None).unwrap();
        assert_eq!(all.len(), 2, "unfiltered list spans every workspace");
        assert!(all.iter().any(|session| session.cwd == cwd()));
        assert!(all.iter().any(|session| session.cwd == other));

        assert!(delete_session_in(&db, &theirs).unwrap());
        assert_eq!(list_sessions_in(&db, None).unwrap().len(), 1);
    }

    #[test]
    fn list_is_most_recently_updated_first() {
        let db = db();
        let first = test_id("aaaaaaaa-0000-4000-8000-000000000001");
        let second = test_id("bbbbbbbb-0000-4000-8000-000000000002");
        create_session_in(&db, cwd(), &first).unwrap();
        create_session_in(&db, cwd(), &second).unwrap();
        set_updated_at(&db, &first, "2026-09-18T10:00:00.000Z");
        set_updated_at(&db, &second, "2026-09-18T09:00:00.000Z");

        assert_eq!(
            ids(&list_sessions_in(&db, None).unwrap()),
            vec![first.to_string(), second.to_string()]
        );

        record_activity_in(&db, &second, None).unwrap();
        assert_eq!(
            ids(&list_sessions_in(&db, None).unwrap()),
            vec![second.to_string(), first.to_string()],
            "activity moves a session to the front"
        );
    }

    #[test]
    fn first_prompt_titles_the_session() {
        let db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();
        let created: String = db
            .query_row("SELECT created_at FROM sessions", [], |row| row.get(0))
            .unwrap();

        record_activity_in(&db, &id, Some("Rejigger sessions".to_owned())).unwrap();
        let (title, created_now, updated): (Option<String>, String, String) = db
            .query_row(
                "SELECT title, created_at, updated_at FROM sessions",
                [],
                |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
            )
            .unwrap();
        assert_eq!(title.as_deref(), Some("Rejigger sessions"));
        assert_eq!(created_now, created, "creation time does not move");
        assert!(updated >= created);

        record_activity_in(&db, &id, Some("Something else".to_owned())).unwrap();
        let listed = list_sessions_in(&db, None).unwrap();
        assert_eq!(
            listed[0].title.as_deref(),
            Some("Rejigger sessions"),
            "a later prompt does not rename the session"
        );

        record_activity_in(&db, &test_id("not-a-session"), None)
            .expect("touching an unknown session is a no-op, not an error");
        assert_eq!(list_sessions_in(&db, None).unwrap().len(), 1);
    }

    #[test]
    fn titles_come_from_the_first_nonblank_line() {
        assert_eq!(title_from_prompt("  hello  "), Some("hello".to_owned()));
        assert_eq!(
            title_from_prompt("\n\nfirst line\nsecond line"),
            Some("first line".to_owned())
        );
        assert_eq!(title_from_prompt(""), None);
        assert_eq!(title_from_prompt("   \n\t "), None);

        let long = "x".repeat(MAX_TITLE_CHARS + 10);
        let title = title_from_prompt(&long).unwrap();
        assert_eq!(
            title.chars().count(),
            MAX_TITLE_CHARS + 1,
            "plus the ellipsis"
        );
        assert!(title.ends_with('…'));

        let wide = "é".repeat(MAX_TITLE_CHARS);
        assert_eq!(
            title_from_prompt(&wide),
            Some(wide),
            "counts chars, not bytes"
        );
    }

    #[test]
    fn events_are_a_transcript_in_the_order_they_happened() {
        let db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();

        append_event_in(&db, &id, "user_message", &json!({ "text": "hello" })).unwrap();
        append_event_in(&db, &id, "agent_message", &json!({ "text": "hi" })).unwrap();

        let events = events_in(&db, &id).unwrap();
        assert_eq!(
            events.iter().map(|e| e.kind.as_str()).collect::<Vec<_>>(),
            vec!["user_message", "agent_message"]
        );
        assert_eq!(events[0].data, json!({ "text": "hello" }));
        assert!(DateTime::parse_from_rfc3339(&events[0].ts).is_ok());

        assert!(
            append_event_in(&db, &test_id("not-a-session"), "user_message", &json!({})).is_err(),
            "an event belongs to a session"
        );
    }

    #[test]
    fn deleting_a_session_takes_its_events_with_it() {
        let db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();
        append_event_in(&db, &id, "user_message", &json!({ "text": "hello" })).unwrap();

        assert!(delete_session_in(&db, &id).unwrap());
        assert!(events_in(&db, &id).unwrap().is_empty());
        let orphans: i64 = db
            .query_row("SELECT count(*) FROM events", [], |row| row.get(0))
            .unwrap();
        assert_eq!(orphans, 0);
    }

    #[test]
    fn create_is_idempotent() {
        let db = db();
        let id = test_id("some-id");
        create_session_in(&db, cwd(), &id).unwrap();
        record_activity_in(&db, &id, Some("Keep me".to_owned())).unwrap();
        let before: (String, Option<String>) = db
            .query_row("SELECT created_at, title FROM sessions", [], |row| {
                Ok((row.get(0)?, row.get(1)?))
            })
            .unwrap();

        create_session_in(&db, cwd(), &id).unwrap();
        let after: (String, Option<String>) = db
            .query_row("SELECT created_at, title FROM sessions", [], |row| {
                Ok((row.get(0)?, row.get(1)?))
            })
            .unwrap();
        assert_eq!(after, before, "a second create does not reset the session");
        assert_eq!(list_sessions_in(&db, Some(cwd())).unwrap().len(), 1);
    }

    #[test]
    fn empty_database_lists_nothing() {
        let db = db();
        assert!(list_sessions_in(&db, None).unwrap().is_empty());
        assert!(list_sessions_in(&db, Some(cwd())).unwrap().is_empty());
        assert!(!session_exists_in(&db, cwd(), &test_id("some-id")).unwrap());
        assert!(!delete_session_in(&db, &test_id("some-id")).unwrap());
        assert!(events_in(&db, &test_id("some-id")).unwrap().is_empty());
    }
}
