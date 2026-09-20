//! Conversation records and the SQLite store that holds them, in one database
//! at `{data}/ox.db`: `$OX_DATA_DIR`, else `$XDG_DATA_HOME/ox`, else
//! `~/.local/share/ox`.
//!
//! Timestamps are RFC 3339 UTC with millisecond precision, so they sort
//! lexicographically and `ORDER BY updated_at` needs no date parsing.

use std::{
    fs,
    io::{self, ErrorKind},
    path::{Path, PathBuf},
    sync::{Arc, Mutex, MutexGuard},
};

use agent_client_protocol::schema::v1::SessionId;
use chrono::{SecondsFormat, Utc};
use rusqlite::{Connection, OptionalExtension, Row, Transaction, params};
use serde::{Deserialize, Serialize, de::DeserializeOwned};

/// Overrides the data directory, mainly for tests.
pub const DATA_DIR_ENV: &str = "OX_DATA_DIR";

const DATABASE_FILE: &str = "ox.db";

/// Stamped into `PRAGMA user_version`. There is no migration path: a
/// database with another version is rejected at open.
const SCHEMA_VERSION: i32 = 2;

/// Longest title derived from a prompt, in characters.
const MAX_TITLE_CHARS: usize = 80;

const SCHEMA: &str = "
BEGIN;

CREATE TABLE workspaces (
    id   INTEGER PRIMARY KEY,
    path TEXT NOT NULL UNIQUE
);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    workspace_id INTEGER NOT NULL REFERENCES workspaces (id),
    title        TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX sessions_by_activity ON sessions (updated_at DESC, id);

CREATE TABLE events (
    id         INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    ts         TEXT NOT NULL,
    kind       TEXT NOT NULL,
    data       TEXT NOT NULL
);

CREATE INDEX events_by_session ON events (session_id, id);

PRAGMA user_version = 2;

COMMIT;
";

/// One record of a session transcript. Every transcript opens with the model
/// its completions use, which does not change within the session. Tool
/// results follow the assistant message that called them, one per call, in
/// call order.
#[derive(Debug, Clone, PartialEq)]
pub enum TranscriptEvent {
    Model(String),
    UserMessage(String),
    AssistantMessage(AssistantMessage),
    ToolResult(ToolResult),
}

/// One complete model completion. `reasoning_details` is the provider's
/// opaque continuation metadata, stored as received and never displayed.
#[derive(Debug, Clone, PartialEq)]
pub struct AssistantMessage {
    pub text: String,
    pub reasoning: String,
    pub tool_calls: Vec<ToolCall>,
    pub reasoning_details: Vec<serde_json::Value>,
}

impl AssistantMessage {
    /// Tool calls need nonempty, unique IDs and nonempty names before they
    /// can be executed or stored.
    pub fn validate(&self) -> io::Result<()> {
        for (index, call) in self.tool_calls.iter().enumerate() {
            if call.call_id.is_empty() {
                return Err(invalid_data(format!(
                    "tool call {index} has an empty call ID"
                )));
            }
            if call.name.is_empty() {
                return Err(invalid_data(format!(
                    "tool call {} has an empty tool name",
                    call.call_id
                )));
            }
            if self.tool_calls[..index]
                .iter()
                .any(|earlier| earlier.call_id == call.call_id)
            {
                return Err(invalid_data(format!(
                    "tool call ID {} is repeated within one assistant message",
                    call.call_id
                )));
            }
        }
        Ok(())
    }
}

/// `arguments` is the complete string the model produced, kept verbatim so
/// invalid JSON can still receive an ordinary failed result.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ToolCall {
    pub call_id: String,
    pub name: String,
    pub arguments: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ToolResult {
    pub call_id: String,
    pub name: String,
    pub outcome: ToolOutcome,
}

/// Every variant carries text describing what Ox knows about the call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ToolOutcome {
    Completed(String),
    Failed(String),
    Cancelled(String),
}

impl ToolOutcome {
    pub fn text(&self) -> &str {
        match self {
            Self::Completed(text) | Self::Failed(text) | Self::Cancelled(text) => text,
        }
    }
}

/// One accepted assistant message with a terminal result for each of its
/// tool calls. This is the unit the store commits atomically.
#[derive(Debug, Clone, PartialEq)]
pub struct AssistantBatch {
    pub message: AssistantMessage,
    pub results: Vec<ToolResult>,
}

impl AssistantBatch {
    pub fn new(message: AssistantMessage, results: Vec<ToolResult>) -> io::Result<Self> {
        message.validate()?;
        pair_results(&message.tool_calls, &results.iter().collect::<Vec<_>>())?;
        Ok(Self { message, results })
    }
}

fn pair_results(calls: &[ToolCall], results: &[&ToolResult]) -> io::Result<()> {
    for (index, call) in calls.iter().enumerate() {
        match results.get(index) {
            None => {
                return Err(invalid_data(format!(
                    "tool call {} has no result",
                    call.call_id
                )));
            }
            Some(result) if result.call_id != call.call_id => {
                return Err(invalid_data(format!(
                    "expected a result for tool call {} but found one for {}",
                    call.call_id, result.call_id
                )));
            }
            Some(result) if result.name != call.name => {
                return Err(invalid_data(format!(
                    "result for tool call {} names tool {} but the call was for {}",
                    call.call_id, result.name, call.name
                )));
            }
            Some(_) => {}
        }
    }
    if let Some(extra) = results.get(calls.len()) {
        return Err(invalid_data(format!(
            "tool result {} does not belong to any call in its assistant message",
            extra.call_id
        )));
    }
    Ok(())
}

fn validate_transcript(events: &[TranscriptEvent]) -> io::Result<()> {
    if !matches!(events.first(), Some(TranscriptEvent::Model(_))) {
        return Err(invalid_data("transcript does not open with a model"));
    }
    let mut index = 1;
    while let Some(event) = events.get(index) {
        match event {
            TranscriptEvent::Model(_) => {
                return Err(invalid_data(
                    "a model record appears after the transcript opened",
                ));
            }
            TranscriptEvent::UserMessage(_) => index += 1,
            TranscriptEvent::ToolResult(result) => {
                return Err(invalid_data(format!(
                    "tool result {} does not follow an assistant message that called it",
                    result.call_id
                )));
            }
            TranscriptEvent::AssistantMessage(message) => {
                message.validate()?;
                let results = events[index + 1..]
                    .iter()
                    .take(message.tool_calls.len())
                    .map(|event| match event {
                        TranscriptEvent::ToolResult(result) => Ok(result),
                        _ => Err(invalid_data(
                            "an assistant message's tool calls are not all resolved before the next message",
                        )),
                    })
                    .collect::<io::Result<Vec<_>>>()?;
                pair_results(&message.tool_calls, &results)?;
                index += 1 + results.len();
            }
        }
    }
    Ok(())
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SessionSummary {
    pub id: SessionId,
    pub cwd: PathBuf,
    pub title: Option<String>,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Debug, Clone, PartialEq)]
pub struct StoredSession {
    pub summary: SessionSummary,
    pub transcript: Vec<TranscriptEvent>,
}

impl StoredSession {
    /// The model every completion in this session uses.
    pub fn model(&self) -> &str {
        match self.transcript.first() {
            Some(TranscriptEvent::Model(model)) => model,
            _ => panic!("session {} has no model record", self.summary.id),
        }
    }
}

/// One connection behind a mutex held only for synchronous database work.
#[derive(Clone)]
pub struct SessionStore(Arc<Mutex<Connection>>);

impl SessionStore {
    pub fn open(path: &Path) -> io::Result<Self> {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        let connection = Connection::open(path).map_err(io::Error::other)?;
        Self::initialize(connection, &path.display().to_string())
    }

    fn initialize(connection: Connection, location: &str) -> io::Result<Self> {
        let version: i32 = connection
            .query_row("PRAGMA user_version", [], |row| row.get(0))
            .map_err(io::Error::other)?;
        let tables: i64 = connection
            .query_row(
                "SELECT count(*) FROM sqlite_master WHERE type = 'table'",
                [],
                |row| row.get(0),
            )
            .map_err(io::Error::other)?;
        match version {
            0 if tables == 0 => connection.execute_batch(SCHEMA).map_err(io::Error::other)?,
            SCHEMA_VERSION => {}
            _ => {
                return Err(io::Error::new(
                    ErrorKind::InvalidData,
                    format!(
                        "{location} has schema version {version}; this build of ox uses version \
                         {SCHEMA_VERSION}. Delete the file to start over."
                    ),
                ));
            }
        }
        connection
            .execute_batch("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;")
            .map_err(io::Error::other)?;
        Ok(Self(Arc::new(Mutex::new(connection))))
    }

    #[cfg(test)]
    pub(crate) fn in_memory() -> Self {
        Self::initialize(
            Connection::open_in_memory().expect("in-memory database opens"),
            "in-memory database",
        )
        .expect("fresh database initializes")
    }

    #[cfg(test)]
    pub(crate) fn with_connection<T>(&self, f: impl FnOnce(&Connection) -> T) -> T {
        f(&self.lock())
    }

    fn lock(&self) -> MutexGuard<'_, Connection> {
        self.0.lock().expect("session store mutex poisoned")
    }

    /// Creates a session whose completions use `model`, recorded as the
    /// first transcript event.
    pub fn create(&self, cwd: &Path, model: &str) -> io::Result<SessionSummary> {
        let path = workspace_path(cwd)?;
        let id = SessionId::new(uuid::Uuid::new_v4().to_string());
        let at = now();
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        tx.execute(
            "INSERT OR IGNORE INTO workspaces (path) VALUES (?1)",
            params![path],
        )
        .map_err(io::Error::other)?;
        tx.execute(
            "INSERT INTO sessions (id, workspace_id, created_at, updated_at)
             VALUES (?1, (SELECT id FROM workspaces WHERE path = ?2), ?3, ?3)",
            params![id.to_string(), path, at],
        )
        .map_err(io::Error::other)?;
        insert_event(
            &tx,
            &id,
            &at,
            "model",
            &ModelRow {
                model: model.to_owned(),
            },
        )?;
        tx.commit().map_err(io::Error::other)?;
        Ok(SessionSummary {
            id,
            cwd: cwd.to_path_buf(),
            title: None,
            created_at: at.clone(),
            updated_at: at,
        })
    }

    /// `None` for an absent session. A malformed transcript is an error, not
    /// partial history.
    pub fn read(&self, id: &SessionId) -> io::Result<Option<StoredSession>> {
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        let Some(summary) = summary(&tx, id).map_err(io::Error::other)? else {
            return Ok(None);
        };
        let rows = tx
            .prepare("SELECT kind, data FROM events WHERE session_id = ?1 ORDER BY id ASC")
            .and_then(|mut statement| {
                statement
                    .query_map(params![id.to_string()], |row| {
                        Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
                    })?
                    .collect::<rusqlite::Result<Vec<_>>>()
            })
            .map_err(io::Error::other)?;
        let transcript = rows
            .into_iter()
            .map(|(kind, data)| decode_event(&kind, &data))
            .collect::<io::Result<Vec<_>>>()
            .and_then(|transcript| validate_transcript(&transcript).map(|()| transcript))
            .map_err(|error| {
                invalid_data(format!("session {id} has an invalid transcript: {error}"))
            })?;
        Ok(Some(StoredSession {
            summary,
            transcript,
        }))
    }

    /// Most recently active first, ties broken by ID so the order is stable.
    pub fn list(&self, cwd: Option<&Path>) -> io::Result<Vec<SessionSummary>> {
        let filter = cwd.map(|cwd| cwd.to_string_lossy().into_owned());
        let connection = self.lock();
        let mut statement = connection
            .prepare(
                "SELECT sessions.id, workspaces.path, sessions.title,
                        sessions.created_at, sessions.updated_at
                 FROM sessions
                 JOIN workspaces ON workspaces.id = sessions.workspace_id
                 WHERE ?1 IS NULL OR workspaces.path = ?1
                 ORDER BY sessions.updated_at DESC, sessions.id ASC",
            )
            .map_err(io::Error::other)?;
        statement
            .query_map(params![filter], summary_row)
            .and_then(Iterator::collect)
            .map_err(io::Error::other)
    }

    /// Appends the accepted input, titles a still-untitled session from it,
    /// and updates activity in one transaction.
    pub fn append_user(&self, id: &SessionId, text: &str) -> io::Result<SessionSummary> {
        let at = now();
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        touch(&tx, id, title_from_prompt(text), &at)?;
        insert_event(
            &tx,
            id,
            &at,
            "user_message",
            &UserMessageRow {
                text: text.to_owned(),
            },
        )?;
        let summary = summary(&tx, id)
            .map_err(io::Error::other)?
            .expect("a session that was just updated exists");
        tx.commit().map_err(io::Error::other)?;
        Ok(summary)
    }

    /// Appends the assistant message and all of its results, and updates
    /// activity, in one transaction.
    pub fn append_batch(&self, id: &SessionId, batch: &AssistantBatch) -> io::Result<()> {
        let at = now();
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        touch(&tx, id, None, &at)?;
        insert_event(
            &tx,
            id,
            &at,
            "assistant_message",
            &AssistantMessageRow::from(&batch.message),
        )?;
        for result in &batch.results {
            insert_event(&tx, id, &at, "tool_result", &ToolResultRow::from(result))?;
        }
        tx.commit().map_err(io::Error::other)
    }

    /// Removes the session and its transcript. An absent session is a success.
    pub fn delete(&self, id: &SessionId) -> io::Result<()> {
        self.lock()
            .execute(
                "DELETE FROM sessions WHERE id = ?1",
                params![id.to_string()],
            )
            .map_err(io::Error::other)?;
        Ok(())
    }
}

/// Workspaces are compared as the exact absolute path the client supplied.
fn workspace_path(cwd: &Path) -> io::Result<String> {
    if !cwd.is_absolute() {
        return Err(io::Error::new(
            ErrorKind::InvalidInput,
            format!("workspace path is not absolute: {}", cwd.display()),
        ));
    }
    Ok(cwd.to_string_lossy().into_owned())
}

fn summary(connection: &Connection, id: &SessionId) -> rusqlite::Result<Option<SessionSummary>> {
    connection
        .query_row(
            "SELECT sessions.id, workspaces.path, sessions.title,
                    sessions.created_at, sessions.updated_at
             FROM sessions
             JOIN workspaces ON workspaces.id = sessions.workspace_id
             WHERE sessions.id = ?1",
            params![id.to_string()],
            summary_row,
        )
        .optional()
}

fn summary_row(row: &Row<'_>) -> rusqlite::Result<SessionSummary> {
    Ok(SessionSummary {
        id: SessionId::new(row.get::<_, String>(0)?),
        cwd: PathBuf::from(row.get::<_, String>(1)?),
        title: row.get(2)?,
        created_at: row.get(3)?,
        updated_at: row.get(4)?,
    })
}

/// Sets `updated_at`, adopts `title` if the session is still untitled, and
/// fails for an absent session rather than creating one.
fn touch(tx: &Transaction<'_>, id: &SessionId, title: Option<String>, at: &str) -> io::Result<()> {
    let changed = tx
        .execute(
            "UPDATE sessions SET updated_at = ?2, title = COALESCE(title, ?3) WHERE id = ?1",
            params![id.to_string(), at, title],
        )
        .map_err(io::Error::other)?;
    if changed == 0 {
        return Err(io::Error::new(
            ErrorKind::NotFound,
            format!("session {id} does not exist"),
        ));
    }
    Ok(())
}

fn insert_event<T: Serialize>(
    tx: &Transaction<'_>,
    id: &SessionId,
    at: &str,
    kind: &str,
    row: &T,
) -> io::Result<()> {
    let data = serde_json::to_string(row).expect("transcript rows serialize");
    tx.execute(
        "INSERT INTO events (session_id, ts, kind, data) VALUES (?1, ?2, ?3, ?4)",
        params![id.to_string(), at, kind, data],
    )
    .map_err(io::Error::other)?;
    Ok(())
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ModelRow {
    model: String,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct UserMessageRow {
    text: String,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct AssistantMessageRow {
    text: String,
    reasoning: String,
    tool_calls: Vec<ToolCallRow>,
    reasoning_details: Vec<serde_json::Value>,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ToolCallRow {
    call_id: String,
    name: String,
    arguments: String,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ToolResultRow {
    call_id: String,
    name: String,
    status: ToolResultStatus,
    content: String,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
enum ToolResultStatus {
    Completed,
    Failed,
    Cancelled,
}

impl From<&AssistantMessage> for AssistantMessageRow {
    fn from(message: &AssistantMessage) -> Self {
        Self {
            text: message.text.clone(),
            reasoning: message.reasoning.clone(),
            tool_calls: message
                .tool_calls
                .iter()
                .map(|call| ToolCallRow {
                    call_id: call.call_id.clone(),
                    name: call.name.clone(),
                    arguments: call.arguments.clone(),
                })
                .collect(),
            reasoning_details: message.reasoning_details.clone(),
        }
    }
}

impl From<&ToolResult> for ToolResultRow {
    fn from(result: &ToolResult) -> Self {
        let status = match result.outcome {
            ToolOutcome::Completed(_) => ToolResultStatus::Completed,
            ToolOutcome::Failed(_) => ToolResultStatus::Failed,
            ToolOutcome::Cancelled(_) => ToolResultStatus::Cancelled,
        };
        Self {
            call_id: result.call_id.clone(),
            name: result.name.clone(),
            status,
            content: result.outcome.text().to_owned(),
        }
    }
}

fn decode_event(kind: &str, data: &str) -> io::Result<TranscriptEvent> {
    Ok(match kind {
        "model" => TranscriptEvent::Model(decode::<ModelRow>(kind, data)?.model),
        "user_message" => TranscriptEvent::UserMessage(decode::<UserMessageRow>(kind, data)?.text),
        "assistant_message" => {
            let row: AssistantMessageRow = decode(kind, data)?;
            TranscriptEvent::AssistantMessage(AssistantMessage {
                text: row.text,
                reasoning: row.reasoning,
                tool_calls: row
                    .tool_calls
                    .into_iter()
                    .map(|call| ToolCall {
                        call_id: call.call_id,
                        name: call.name,
                        arguments: call.arguments,
                    })
                    .collect(),
                reasoning_details: row.reasoning_details,
            })
        }
        "tool_result" => {
            let row: ToolResultRow = decode(kind, data)?;
            let outcome = match row.status {
                ToolResultStatus::Completed => ToolOutcome::Completed(row.content),
                ToolResultStatus::Failed => ToolOutcome::Failed(row.content),
                ToolResultStatus::Cancelled => ToolOutcome::Cancelled(row.content),
            };
            TranscriptEvent::ToolResult(ToolResult {
                call_id: row.call_id,
                name: row.name,
                outcome,
            })
        }
        _ => {
            return Err(invalid_data(format!(
                "unknown transcript event kind {kind:?}"
            )));
        }
    })
}

fn decode<T: DeserializeOwned>(kind: &str, data: &str) -> io::Result<T> {
    serde_json::from_str(data).map_err(|error| invalid_data(format!("{kind} event: {error}")))
}

fn invalid_data(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidData, message.into())
}

fn now() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true)
}

/// The first nonblank line of a prompt, truncated. `None` for a blank prompt,
/// so it does not spend the session's one chance at a title.
fn title_from_prompt(text: &str) -> Option<String> {
    let line = text.lines().find(|line| !line.trim().is_empty())?.trim();
    let mut title: String = line.chars().take(MAX_TITLE_CHARS).collect();
    if line.chars().count() > MAX_TITLE_CHARS {
        title.push('…');
    }
    Some(title)
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
    let home = std::env::var("HOME")
        .map_err(|_| io::Error::new(ErrorKind::InvalidInput, "HOME is not set"))?;
    Ok(PathBuf::from(home)
        .join(".local/share/ox")
        .join(DATABASE_FILE))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{acp::convert, model};
    use agent_client_protocol::schema::v1::{SessionUpdate, ToolCallStatus};
    use serde_json::json;

    const CWD: &str = "/Users/kyle/projects/ox";

    fn cwd() -> &'static Path {
        Path::new(CWD)
    }

    fn call(id: &str, location: &str) -> ToolCall {
        ToolCall {
            call_id: id.to_owned(),
            name: "get_weather".to_owned(),
            arguments: json!({ "location": location }).to_string(),
        }
    }

    fn result(id: &str, outcome: ToolOutcome) -> ToolResult {
        ToolResult {
            call_id: id.to_owned(),
            name: "get_weather".to_owned(),
            outcome,
        }
    }

    fn completed(id: &str) -> ToolResult {
        result(id, ToolOutcome::Completed("ok".to_owned()))
    }

    fn message(tool_calls: Vec<ToolCall>) -> AssistantMessage {
        AssistantMessage {
            text: "Checking both.".to_owned(),
            reasoning: "Two cities.".to_owned(),
            tool_calls,
            reasoning_details: vec![json!({
                "type": "reasoning.encrypted",
                "data": "opaque",
                "id": "rs_1",
                "format": "openai-responses-v1",
                "index": 0,
            })],
        }
    }

    fn ids(summaries: &[SessionSummary]) -> Vec<String> {
        summaries.iter().map(|s| s.id.to_string()).collect()
    }

    fn set_updated_at(store: &SessionStore, id: &SessionId, at: &str) {
        store.with_connection(|connection| {
            connection
                .execute(
                    "UPDATE sessions SET updated_at = ?2 WHERE id = ?1",
                    params![id.to_string(), at],
                )
                .unwrap()
        });
    }

    #[test]
    fn a_batch_survives_reopen_and_projects_to_replay_and_the_next_request() {
        let dir = std::env::temp_dir().join(format!("ox-test-{}", uuid::Uuid::new_v4()));
        let path = dir.join(DATABASE_FILE);
        let message = message(vec![call("call-1", "Chicago"), call("call-2", "Denver")]);
        let results = vec![
            result(
                "call-1",
                ToolOutcome::Completed("Sunny in Chicago.".to_owned()),
            ),
            result(
                "call-2",
                ToolOutcome::Failed("Denver is unavailable.".to_owned()),
            ),
        ];

        let id = {
            let store = SessionStore::open(&path).unwrap();
            let id = store.create(cwd(), model::MODEL).unwrap().id;
            store
                .append_user(&id, "Weather in Chicago and Denver?")
                .unwrap();
            let batch = AssistantBatch::new(message.clone(), results.clone()).unwrap();
            store.append_batch(&id, &batch).unwrap();
            id
        };

        let store = SessionStore::open(&path).unwrap();
        let stored = store.read(&id).unwrap().expect("session persists");
        assert_eq!(stored.summary.cwd, cwd());
        assert_eq!(
            stored.summary.title.as_deref(),
            Some("Weather in Chicago and Denver?")
        );
        assert_eq!(stored.model(), model::MODEL);
        assert_eq!(
            stored.transcript,
            vec![
                TranscriptEvent::Model(model::MODEL.to_owned()),
                TranscriptEvent::UserMessage("Weather in Chicago and Denver?".to_owned()),
                TranscriptEvent::AssistantMessage(message.clone()),
                TranscriptEvent::ToolResult(results[0].clone()),
                TranscriptEvent::ToolResult(results[1].clone()),
            ]
        );

        let mut updates = Vec::new();
        convert::replay(&stored.transcript, |update| {
            updates.push(update);
            Ok(())
        })
        .unwrap();
        assert_eq!(updates.len(), 5);
        assert!(matches!(updates[0], SessionUpdate::UserMessageChunk(_)));
        assert!(matches!(updates[1], SessionUpdate::AgentThoughtChunk(_)));
        assert!(matches!(updates[2], SessionUpdate::AgentMessageChunk(_)));
        assert!(matches!(
            &updates[3],
            SessionUpdate::ToolCall(call)
                if call.tool_call_id.to_string() == "call-1"
                    && call.title == "Weather Chicago"
                    && call.status == ToolCallStatus::Completed
                    && call.raw_input == Some(json!({ "location": "Chicago" }))
                    && call.raw_output == Some(json!("Sunny in Chicago."))
        ));
        assert!(matches!(
            &updates[4],
            SessionUpdate::ToolCall(call)
                if call.tool_call_id.to_string() == "call-2"
                    && call.status == ToolCallStatus::Failed
                    && call.raw_output == Some(json!("Denver is unavailable."))
        ));

        let messages = model::request_messages(&stored.transcript);
        assert_eq!(messages.len(), 4);
        assert_eq!(messages[0]["role"], "user");
        assert_eq!(messages[1]["role"], "assistant");
        assert_eq!(messages[1]["content"], "Checking both.");
        assert_eq!(messages[1]["tool_calls"].as_array().unwrap().len(), 2);
        assert_eq!(messages[1]["tool_calls"][1]["id"], "call-2");
        assert_eq!(
            messages[1]["reasoning_details"],
            json!(message.reasoning_details)
        );
        assert!(
            messages[1].get("reasoning").is_none(),
            "visible reasoning is not sent twice"
        );
        assert_eq!(messages[2]["role"], "tool");
        assert_eq!(messages[2]["tool_call_id"], "call-1");
        assert_eq!(messages[3]["tool_call_id"], "call-2");
        assert_eq!(messages[3]["content"], "Denver is unavailable.");

        drop(store);
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn batch_validation_rejects_orphan_duplicate_and_missing_results() {
        let two_calls = message(vec![call("call-1", "Chicago"), call("call-2", "Denver")]);

        assert!(AssistantBatch::new(two_calls.clone(), vec![completed("call-1")]).is_err());
        assert!(
            AssistantBatch::new(
                two_calls.clone(),
                vec![completed("call-1"), completed("call-1")]
            )
            .is_err()
        );
        assert!(
            AssistantBatch::new(
                two_calls.clone(),
                vec![
                    completed("call-1"),
                    completed("call-2"),
                    completed("call-3")
                ],
            )
            .is_err()
        );
        assert!(
            AssistantBatch::new(
                two_calls.clone(),
                vec![completed("call-2"), completed("call-1")]
            )
            .is_err()
        );
        let mut renamed = completed("call-2");
        renamed.name = "other".to_owned();
        assert!(
            AssistantBatch::new(two_calls.clone(), vec![completed("call-1"), renamed]).is_err()
        );
        assert!(
            AssistantBatch::new(two_calls, vec![completed("call-1"), completed("call-2")]).is_ok()
        );

        let repeated = message(vec![call("call-1", "Chicago"), call("call-1", "Denver")]);
        assert!(repeated.validate().is_err());
        let unnamed = message(vec![ToolCall {
            name: String::new(),
            ..call("call-1", "Chicago")
        }]);
        assert!(unnamed.validate().is_err());
        let anonymous = message(vec![call("", "Chicago")]);
        assert!(anonymous.validate().is_err());
    }

    #[test]
    fn read_rejects_a_malformed_transcript() {
        let store = SessionStore::in_memory();
        let insert = |id: &SessionId, kind: &str, data: &str| {
            store.with_connection(|connection| {
                connection
                    .execute(
                        "INSERT INTO events (session_id, ts, kind, data) VALUES (?1, ?2, ?3, ?4)",
                        params![id.to_string(), now(), kind, data],
                    )
                    .unwrap()
            });
        };

        let orphan = store.create(cwd(), model::MODEL).unwrap().id;
        store.append_user(&orphan, "hello").unwrap();
        insert(
            &orphan,
            "tool_result",
            r#"{"call_id":"x","name":"get_weather","status":"completed","content":"ok"}"#,
        );
        assert!(store.read(&orphan).is_err());

        let unresolved = store.create(cwd(), model::MODEL).unwrap().id;
        let unresolved_message =
            AssistantMessageRow::from(&message(vec![call("call-1", "Chicago")]));
        insert(
            &unresolved,
            "assistant_message",
            &serde_json::to_string(&unresolved_message).unwrap(),
        );
        assert!(store.read(&unresolved).is_err());

        let unknown = store.create(cwd(), model::MODEL).unwrap().id;
        insert(&unknown, "mystery", "{}");
        assert!(store.read(&unknown).is_err());

        let malformed = store.create(cwd(), model::MODEL).unwrap().id;
        insert(&malformed, "user_message", r#"{"text":"hi","extra":true}"#);
        assert!(store.read(&malformed).is_err());

        let switched = store.create(cwd(), model::MODEL).unwrap().id;
        insert(&switched, "model", r#"{"model":"other/model"}"#);
        assert!(store.read(&switched).is_err());

        let unmodelled = store.create(cwd(), model::MODEL).unwrap().id;
        store.with_connection(|connection| {
            connection
                .execute(
                    "DELETE FROM events WHERE session_id = ?1",
                    params![unmodelled.to_string()],
                )
                .unwrap()
        });
        assert!(store.read(&unmodelled).is_err());
    }

    #[test]
    fn user_append_adopts_a_title_once_and_updates_activity() {
        let store = SessionStore::in_memory();
        let created = store.create(cwd(), model::MODEL).unwrap();
        set_updated_at(&store, &created.id, "2026-09-18T09:00:00.000Z");

        let first = store
            .append_user(&created.id, "\n\nFirst line\nsecond line")
            .unwrap();
        assert_eq!(first.title.as_deref(), Some("First line"));
        assert_eq!(first.created_at, created.created_at);
        assert!(first.updated_at.as_str() > "2026-09-18T09:00:00.000Z");

        let second = store.append_user(&created.id, "Something else").unwrap();
        assert_eq!(second.title.as_deref(), Some("First line"));
        assert!(second.updated_at >= first.updated_at);

        let long = store.create(cwd(), model::MODEL).unwrap();
        let title = store
            .append_user(&long.id, &"x".repeat(MAX_TITLE_CHARS + 10))
            .unwrap()
            .title
            .unwrap();
        assert_eq!(
            title.chars().count(),
            MAX_TITLE_CHARS + 1,
            "plus the ellipsis"
        );
        assert!(title.ends_with('…'));

        assert!(
            store
                .append_user(&SessionId::new("missing"), "hello")
                .is_err(),
            "appending never creates a session"
        );
    }

    #[test]
    fn list_orders_by_activity_and_filters_by_workspace() {
        let store = SessionStore::in_memory();
        let first = store.create(cwd(), model::MODEL).unwrap().id;
        let second = store.create(cwd(), model::MODEL).unwrap().id;
        let other = store
            .create(Path::new("/Users/kyle/projects/other"), model::MODEL)
            .unwrap()
            .id;
        set_updated_at(&store, &first, "2026-09-18T10:00:00.000Z");
        set_updated_at(&store, &second, "2026-09-18T09:00:00.000Z");
        set_updated_at(&store, &other, "2026-09-18T11:00:00.000Z");

        assert_eq!(
            ids(&store.list(None).unwrap()),
            vec![other.to_string(), first.to_string(), second.to_string()]
        );
        assert_eq!(
            ids(&store.list(Some(cwd())).unwrap()),
            vec![first.to_string(), second.to_string()]
        );
        assert!(
            store
                .create(Path::new("relative/path"), model::MODEL)
                .is_err()
        );
    }

    #[test]
    fn deleting_a_session_removes_its_records_and_repeats_successfully() {
        let store = SessionStore::in_memory();
        let id = store.create(cwd(), model::MODEL).unwrap().id;
        store.append_user(&id, "hello").unwrap();

        store.delete(&id).unwrap();
        assert!(store.read(&id).unwrap().is_none());
        let events: i64 = store.with_connection(|connection| {
            connection
                .query_row("SELECT count(*) FROM events", [], |row| row.get(0))
                .unwrap()
        });
        assert_eq!(events, 0);

        store.delete(&id).unwrap();
    }

    #[test]
    fn a_database_with_another_schema_version_is_rejected() {
        let connection = Connection::open_in_memory().unwrap();
        connection
            .execute_batch("CREATE TABLE legacy (id INTEGER); PRAGMA user_version = 7;")
            .unwrap();
        let error = SessionStore::initialize(connection, "/data/ox.db")
            .err()
            .expect("another version is rejected");
        assert!(error.to_string().contains("/data/ox.db"));
        assert!(error.to_string().contains("version 7"));

        let unversioned = Connection::open_in_memory().unwrap();
        unversioned
            .execute_batch("CREATE TABLE legacy (id INTEGER);")
            .unwrap();
        assert!(SessionStore::initialize(unversioned, "/data/ox.db").is_err());
    }
}
