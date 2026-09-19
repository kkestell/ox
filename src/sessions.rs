//! Session storage, in one SQLite database at `{data}/ox.db`: `$OX_DATA_DIR`,
//! else `$XDG_DATA_HOME/ox`, else `~/.local/share/ox`.
//!
//! Timestamps are RFC 3339 UTC with millisecond precision, so they sort
//! lexicographically and `ORDER BY updated_at` needs no date parsing.

use std::fs;
use std::io::{self, ErrorKind};
use std::path::{Path, PathBuf};

use agent_client_protocol::schema::v1::{SessionId, SessionInfo};
use chrono::{SecondsFormat, Utc};
use rusqlite::{Connection, OptionalExtension, params, types::Type};
use serde::{Deserialize, Serialize, de::DeserializeOwned};

/// Overrides the data directory, mainly for tests.
pub const DATA_DIR_ENV: &str = "OX_DATA_DIR";

const DATABASE_FILE: &str = "ox.db";

/// Longest title derived from a prompt, in characters.
const MAX_TITLE_CHARS: usize = 80;

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

/// A valid entry in a session transcript.
#[derive(Debug, Clone, PartialEq)]
pub enum EventKind {
    UserMessage(String),
    AgentThought(String),
    AgentMessage(String),
    ToolCall {
        call_id: String,
        name: String,
        arguments: serde_json::Value,
    },
    ToolResult {
        call_id: String,
        name: String,
        result: String,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
struct MessageData {
    content: Vec<Content>,
}

impl MessageData {
    fn text(text: String) -> Self {
        Self {
            content: vec![Content::Text { text }],
        }
    }

    fn into_text(self, kind: &str) -> rusqlite::Result<String> {
        match self.content.as_slice() {
            [Content::Text { text }] => Ok(text.clone()),
            _ => Err(invalid_event_data(
                kind,
                "must contain exactly one text block",
            )),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum Content {
    Text { text: String },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
struct ToolCallData {
    call_id: String,
    name: String,
    arguments: serde_json::Value,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
struct ToolResultData {
    call_id: String,
    name: String,
    content: Vec<Content>,
}

impl EventKind {
    fn database_fields(&self) -> (&str, serde_json::Value) {
        match self {
            Self::UserMessage(text) => ("user_message", data(MessageData::text(text.clone()))),
            Self::AgentThought(text) => ("agent_thought", data(MessageData::text(text.clone()))),
            Self::AgentMessage(text) => ("agent_message", data(MessageData::text(text.clone()))),
            Self::ToolCall {
                call_id,
                name,
                arguments,
            } => (
                "tool_call",
                data(ToolCallData {
                    call_id: call_id.clone(),
                    name: name.clone(),
                    arguments: arguments.clone(),
                }),
            ),
            Self::ToolResult {
                call_id,
                name,
                result,
            } => (
                "tool_result",
                data(ToolResultData {
                    call_id: call_id.clone(),
                    name: name.clone(),
                    content: MessageData::text(result.clone()).content,
                }),
            ),
        }
    }

    fn from_database(kind: String, data: String) -> rusqlite::Result<Self> {
        match kind.as_str() {
            "user_message" => decode(&kind, &data)
                .and_then(|data: MessageData| data.into_text(&kind).map(Self::UserMessage)),
            "agent_thought" => decode(&kind, &data)
                .and_then(|data: MessageData| data.into_text(&kind).map(Self::AgentThought)),
            "agent_message" => decode(&kind, &data)
                .and_then(|data: MessageData| data.into_text(&kind).map(Self::AgentMessage)),
            "tool_call" => decode(&kind, &data).map(|data: ToolCallData| Self::ToolCall {
                call_id: data.call_id,
                name: data.name,
                arguments: data.arguments,
            }),
            "tool_result" => decode(&kind, &data).and_then(|data: ToolResultData| {
                MessageData {
                    content: data.content,
                }
                .into_text(&kind)
                .map(|result| Self::ToolResult {
                    call_id: data.call_id,
                    name: data.name,
                    result,
                })
            }),
            _ => Err(invalid_event_data(&kind, "has an unknown kind")),
        }
    }
}

fn data<T: Serialize>(value: T) -> serde_json::Value {
    serde_json::to_value(value).expect("event DTO serializes")
}

fn decode<T: DeserializeOwned>(kind: &str, data: &str) -> rusqlite::Result<T> {
    serde_json::from_str(data).map_err(|err| invalid_event_data(kind, err))
}

fn invalid_event_data(kind: &str, message: impl std::fmt::Display) -> rusqlite::Error {
    rusqlite::Error::FromSqlConversionFailure(
        2,
        Type::Text,
        Box::new(io::Error::new(
            ErrorKind::InvalidData,
            format!("{kind} event {message}"),
        )),
    )
}

/// One timestamped record in a session's transcript.
#[derive(Debug, Clone, PartialEq)]
pub struct Event {
    pub ts: String,
    pub kind: EventKind,
}

fn invalid_input(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidInput, message.into())
}

pub fn now() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true)
}

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

fn open() -> io::Result<Connection> {
    let path = database_path()?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let connection = Connection::open(&path).map_err(io::Error::other)?;
    connection.execute_batch(SCHEMA).map_err(io::Error::other)?;
    Ok(connection)
}

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

/// Records a new session in `cwd`. Idempotent: re-creating an existing
/// session keeps its title and creation time.
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

pub fn session_exists(cwd: &Path, session_id: &SessionId) -> io::Result<bool> {
    session_exists_in(&open()?, cwd, session_id).map_err(io::Error::other)
}

pub fn session_exists_anywhere(session_id: &SessionId) -> io::Result<bool> {
    session_exists_anywhere_in(&open()?, session_id).map_err(io::Error::other)
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

fn session_exists_anywhere_in(
    connection: &Connection,
    session_id: &SessionId,
) -> rusqlite::Result<bool> {
    connection
        .query_row(
            "SELECT 1 FROM sessions WHERE id = ?1",
            params![session_id.to_string()],
            |_| Ok(true),
        )
        .optional()
        .map(|found| found.unwrap_or(false))
}

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

/// Appends one event and updates the session's activity in one transaction.
pub fn append_event(
    session_id: &SessionId,
    title: Option<String>,
    event: &EventKind,
    at: &str,
) -> io::Result<()> {
    append_events(session_id, title, std::slice::from_ref(event), at)
}

/// Appends one completed assistant iteration and updates session activity in
/// one transaction. Empty iterations do not change the session.
pub fn append_events(
    session_id: &SessionId,
    title: Option<String>,
    events: &[EventKind],
    at: &str,
) -> io::Result<()> {
    if events.is_empty() {
        return Ok(());
    }
    append_events_in(&mut open()?, session_id, title, events, at).map_err(io::Error::other)
}

fn append_events_in(
    connection: &mut Connection,
    session_id: &SessionId,
    title: Option<String>,
    events: &[EventKind],
    at: &str,
) -> rusqlite::Result<()> {
    let transaction = connection.transaction()?;
    record_activity_in(&transaction, session_id, title, at)?;
    for event in events {
        insert_event_in(&transaction, session_id, event, at)?;
    }
    transaction.commit()
}

/// Sets `updated_at` to `at`, and adopts `title` if the session is still
/// untitled. An unknown session is a no-op.
fn record_activity_in(
    connection: &Connection,
    session_id: &SessionId,
    title: Option<String>,
    at: &str,
) -> rusqlite::Result<()> {
    connection.execute(
        "UPDATE sessions
         SET updated_at = ?2, title = COALESCE(title, ?3)
         WHERE id = ?1",
        params![session_id.to_string(), at, title],
    )?;
    Ok(())
}

/// Inserts a transcript record, timed `at`.
fn insert_event_in(
    connection: &Connection,
    session_id: &SessionId,
    event: &EventKind,
    at: &str,
) -> rusqlite::Result<()> {
    let (kind, data) = event.database_fields();
    connection.execute(
        "INSERT INTO events (session_id, ts, kind, data) VALUES (?1, ?2, ?3, ?4)",
        params![session_id.to_string(), at, kind, data.to_string()],
    )?;
    Ok(())
}

/// A session's transcript, oldest record first.
pub fn events(session_id: &SessionId) -> io::Result<Vec<Event>> {
    events_in(&open()?, session_id).map_err(io::Error::other)
}

fn events_in(connection: &Connection, session_id: &SessionId) -> rusqlite::Result<Vec<Event>> {
    let mut statement = connection
        .prepare("SELECT ts, kind, data FROM events WHERE session_id = ?1 ORDER BY id ASC")?;
    let events = statement
        .query_map(params![session_id.to_string()], |row| {
            let kind: String = row.get(1)?;
            let data: String = row.get(2)?;
            Ok(Event {
                ts: row.get(0)?,
                kind: EventKind::from_database(kind, data)?,
            })
        })?
        .collect::<rusqlite::Result<Vec<_>>>()?;
    Ok(events)
}

/// The first non-blank line of a prompt, truncated. `None` for a blank
/// prompt, so it does not spend the session's one chance at a title.
pub fn title_from_prompt(text: &str) -> Option<String> {
    let line = text.lines().find(|line| !line.trim().is_empty())?.trim();
    let mut title: String = line.chars().take(MAX_TITLE_CHARS).collect();
    if line.chars().count() > MAX_TITLE_CHARS {
        title.push('…');
    }
    Some(title)
}

/// Deletes a session and its transcript, `Ok(true)` if there was one.
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

    /// Set timestamps rather than sleeping: two creates in the same
    /// millisecond would leave the order to the id tie-break.
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
        assert!(session_exists_anywhere_in(&db, &id).unwrap());

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
        assert!(!session_exists_anywhere_in(&db, &id).unwrap());
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

        record_activity_in(&db, &second, None, "2026-09-18T11:00:00.000Z").unwrap();
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

        record_activity_in(&db, &id, Some("Rejigger sessions".to_owned()), &now()).unwrap();
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

        record_activity_in(&db, &id, Some("Something else".to_owned()), &now()).unwrap();
        let listed = list_sessions_in(&db, None).unwrap();
        assert_eq!(
            listed[0].title.as_deref(),
            Some("Rejigger sessions"),
            "a later prompt does not rename the session"
        );

        record_activity_in(&db, &test_id("not-a-session"), None, &now())
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
    fn an_iteration_and_its_events_share_one_timestamp() {
        let mut db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();
        let at = "2026-09-18T12:00:00.000Z";

        append_events_in(
            &mut db,
            &id,
            Some("Hello".to_owned()),
            &[
                EventKind::UserMessage("hello".to_owned()),
                EventKind::AgentMessage("hi".to_owned()),
            ],
            at,
        )
        .unwrap();

        let updated: String = db
            .query_row("SELECT updated_at FROM sessions", [], |row| row.get(0))
            .unwrap();
        assert_eq!(updated, at);
        assert!(
            events_in(&db, &id).unwrap().iter().all(|e| e.ts == at),
            "activity never lands before the records it summarises"
        );
    }

    #[test]
    fn a_failed_iteration_leaves_the_previous_events_intact() {
        let mut db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();
        set_updated_at(&db, &id, "2026-09-18T09:00:00.000Z");
        db.execute_batch(
            "CREATE TRIGGER refuse_the_reply BEFORE INSERT ON events
             WHEN NEW.kind = 'agent_message'
             BEGIN SELECT RAISE(ABORT, 'no reply for you'); END;",
        )
        .unwrap();

        append_events_in(
            &mut db,
            &id,
            Some("Hello".to_owned()),
            &[EventKind::UserMessage("hello".to_owned())],
            "2026-09-18T10:00:00.000Z",
        )
        .unwrap();
        let failed = append_events_in(
            &mut db,
            &id,
            None,
            &[EventKind::AgentMessage("hi".to_owned())],
            "2026-09-18T12:00:00.000Z",
        );
        assert!(failed.is_err());

        assert!(
            matches!(
                events_in(&db, &id).unwrap().as_slice(),
                [Event { kind: EventKind::UserMessage(text), .. }] if text == "hello"
            ),
            "the user event survives a failed assistant iteration"
        );
        let (title, updated): (Option<String>, String) = db
            .query_row("SELECT title, updated_at FROM sessions", [], |row| {
                Ok((row.get(0)?, row.get(1)?))
            })
            .unwrap();
        assert_eq!(title.as_deref(), Some("Hello"));
        assert_eq!(updated, "2026-09-18T10:00:00.000Z");
    }

    #[test]
    fn events_are_a_transcript_in_the_order_they_happened() {
        let db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();

        insert_event_in(
            &db,
            &id,
            &EventKind::UserMessage("hello".to_owned()),
            &now(),
        )
        .unwrap();
        insert_event_in(&db, &id, &EventKind::AgentMessage("hi".to_owned()), &now()).unwrap();

        let events = events_in(&db, &id).unwrap();
        assert_eq!(
            events.iter().map(|e| &e.kind).collect::<Vec<_>>(),
            vec![
                &EventKind::UserMessage("hello".to_owned()),
                &EventKind::AgentMessage("hi".to_owned()),
            ]
        );
        assert!(DateTime::parse_from_rfc3339(&events[0].ts).is_ok());

        assert!(
            insert_event_in(
                &db,
                &test_id("not-a-session"),
                &EventKind::UserMessage(String::new()),
                &now()
            )
            .is_err(),
            "an event belongs to a session"
        );
    }

    #[test]
    fn unknown_event_kind_is_an_error() {
        let db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();
        db.execute(
            "INSERT INTO events (session_id, ts, kind, data) VALUES (?1, ?2, ?3, ?4)",
            params![id.to_string(), now(), "not_an_event", "bogus"],
        )
        .unwrap();

        assert!(events_in(&db, &id).is_err());
    }

    #[test]
    fn tool_events_round_trip() {
        let mut db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();
        let events = [
            EventKind::ToolCall {
                call_id: "weather-1".to_owned(),
                name: "get_weather".to_owned(),
                arguments: json!({ "location": "Chicago" }),
            },
            EventKind::ToolResult {
                call_id: "weather-1".to_owned(),
                name: "get_weather".to_owned(),
                result: "The weather in Chicago is warm and sunny.".to_owned(),
            },
        ];

        append_events_in(&mut db, &id, None, &events, &now()).unwrap();
        assert_eq!(
            events_in(&db, &id)
                .unwrap()
                .into_iter()
                .map(|event| event.kind)
                .collect::<Vec<_>>(),
            events
        );

        let rows = db
            .prepare("SELECT kind, data FROM events ORDER BY id")
            .unwrap()
            .query_map([], |row| {
                Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
            })
            .unwrap()
            .collect::<rusqlite::Result<Vec<_>>>()
            .unwrap();
        assert_eq!(
            rows,
            vec![
                (
                    "tool_call".to_owned(),
                    json!({
                        "call_id": "weather-1",
                        "name": "get_weather",
                        "arguments": { "location": "Chicago" },
                    })
                    .to_string(),
                ),
                (
                    "tool_result".to_owned(),
                    json!({
                        "call_id": "weather-1",
                        "name": "get_weather",
                        "content": [{ "type": "text", "text": "The weather in Chicago is warm and sunny." }],
                    })
                    .to_string(),
                ),
            ]
        );
    }

    #[test]
    fn deleting_a_session_takes_its_events_with_it() {
        let db = db();
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&db, cwd(), &id).unwrap();
        insert_event_in(
            &db,
            &id,
            &EventKind::UserMessage("hello".to_owned()),
            &now(),
        )
        .unwrap();

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
        record_activity_in(&db, &id, Some("Keep me".to_owned()), &now()).unwrap();
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
