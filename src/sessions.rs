//! Session transcripts and the SQLite store that holds them, in one database
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

/// Longest session title derived from a prompt, in characters.
const MAX_SESSION_TITLE_CHARS: usize = 80;

const SCHEMA: &str = "
BEGIN;

CREATE TABLE IF NOT EXISTS sessions (
    id             TEXT PRIMARY KEY,
    workspace_path TEXT NOT NULL,
    title          TEXT,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_by_activity ON sessions (updated_at DESC, id);

CREATE TABLE IF NOT EXISTS transcript_entries (
    id         INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    ts         TEXT NOT NULL,
    kind       TEXT NOT NULL,
    data       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS transcript_entries_by_session
    ON transcript_entries (session_id, id);

COMMIT;
";

/// One entry in a session transcript. Every nonempty transcript opens with
/// the model its completions use, which does not change within the session.
/// A turn starts with a user message or a skill invocation. Effort and mode
/// entries form a settings block immediately before the turn start where they
/// take effect. Tool results follow the assistant message that called them,
/// one per call, in call order. Hook feedback follows the turn start,
/// tool results, or assistant message its hook ran after, allowing adjacent
/// feedback of the same kind.
#[derive(Debug, Clone, PartialEq)]
pub enum TranscriptEntry {
    Model(String),
    Effort(EffortLevel),
    Mode(SessionMode),
    UserMessage(String),
    SkillInvocation(SkillInvocation),
    AssistantMessage(AssistantMessage),
    ToolResult(ToolResult),
    HookFeedback(HookFeedback),
    CompactionCheckpoint(CompactionCheckpoint),
}

/// A skill invoked as a slash command, saved in place of the user message for
/// its turn. The instructions are copied from the skill when it is invoked.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SkillInvocation {
    pub name: String,
    pub arguments: String,
    pub instructions: String,
}

impl SkillInvocation {
    /// The slash command as typed, used for the session title and replay.
    pub fn command_text(&self) -> String {
        if self.arguments.is_empty() {
            format!("/{}", self.name)
        } else {
            format!("/{} {}", self.name, self.arguments)
        }
    }
}

/// The point in a prompt run where a hook runs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HookKind {
    BeforeRun,
    BeforeTool,
    AfterTools,
    BeforeStop,
    AfterRun,
}

impl HookKind {
    pub const ALL: [Self; 5] = [
        Self::BeforeRun,
        Self::BeforeTool,
        Self::AfterTools,
        Self::BeforeStop,
        Self::AfterRun,
    ];

    pub fn id(self) -> &'static str {
        match self {
            Self::BeforeRun => "before_run",
            Self::BeforeTool => "before_tool",
            Self::AfterTools => "after_tools",
            Self::BeforeStop => "before_stop",
            Self::AfterRun => "after_run",
        }
    }
}

/// The same attribution in live updates, saved feedback, and errors.
pub fn hook_label(skill: Option<&str>, kind: HookKind) -> String {
    let source = match skill {
        Some(name) => format!("skill /{name}"),
        None => "global".to_owned(),
    };
    format!("{source} {} hook", kind.id())
}

/// A hook's saved message, which later model requests receive.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HookFeedback {
    pub skill: Option<String>,
    pub content: HookFeedbackContent,
}

/// Each variant has its own place in the transcript.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum HookFeedbackContent {
    /// Follows a user message or skill invocation.
    BeforeRun { message: String },
    /// Follows the last tool result of an assistant batch.
    AfterTools { message: String },
    /// Follows an assistant message without tool calls.
    BeforeStop {
        decision: HookDecision,
        message: String,
    },
}

impl HookFeedback {
    pub fn label(&self) -> String {
        hook_label(self.skill.as_deref(), self.kind())
    }

    pub fn kind(&self) -> HookKind {
        match self.content {
            HookFeedbackContent::BeforeRun { .. } => HookKind::BeforeRun,
            HookFeedbackContent::AfterTools { .. } => HookKind::AfterTools,
            HookFeedbackContent::BeforeStop { .. } => HookKind::BeforeStop,
        }
    }

    pub fn message(&self) -> &str {
        match &self.content {
            HookFeedbackContent::BeforeRun { message }
            | HookFeedbackContent::AfterTools { message }
            | HookFeedbackContent::BeforeStop { message, .. } => message,
        }
    }
}

/// A `before_stop` decision. `Continue` makes another model request in the
/// same prompt run; `Stop` ends the run. It is distinct from an OpenRouter
/// stop.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum HookDecision {
    Continue,
    Stop,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CompactionCheckpoint {
    pub summary: String,
    pub covered_prefix: usize,
    /// The summed cost of the summarizer requests made by the compaction that
    /// committed this checkpoint, including cuts it tried and rejected. `None`
    /// when none of them reported usage.
    pub summarizer_cost: Option<f64>,
}

/// Whether shell calls require approval from the ACP client.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SessionMode {
    Ask,
    Auto,
}

impl SessionMode {
    pub const ALL: [Self; 2] = [Self::Ask, Self::Auto];

    pub fn id(self) -> &'static str {
        match self {
            Self::Ask => "ask",
            Self::Auto => "auto",
        }
    }

    pub fn name(self) -> &'static str {
        match self {
            Self::Ask => "Ask",
            Self::Auto => "Auto",
        }
    }

    pub fn description(self) -> &'static str {
        match self {
            Self::Ask => "Ask before running each shell command.",
            Self::Auto => "Run shell commands without asking.",
        }
    }

    pub fn from_id(id: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|mode| mode.id() == id)
    }
}

/// How much reasoning Ox asks a model to do. `Default` leaves the choice to
/// the model.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum EffortLevel {
    Default,
    Low,
    Medium,
    High,
}

impl EffortLevel {
    pub const ALL: [Self; 4] = [Self::Default, Self::Low, Self::Medium, Self::High];

    pub fn id(self) -> &'static str {
        match self {
            Self::Default => "default",
            Self::Low => "low",
            Self::Medium => "medium",
            Self::High => "high",
        }
    }

    pub fn name(self) -> &'static str {
        match self {
            Self::Default => "Default",
            Self::Low => "Low",
            Self::Medium => "Medium",
            Self::High => "High",
        }
    }

    pub fn from_id(id: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|level| level.id() == id)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SessionSettings {
    pub model: String,
    pub effort: EffortLevel,
    pub mode: SessionMode,
}

impl SessionSettings {
    pub fn new(model: impl Into<String>, effort: EffortLevel) -> Self {
        Self {
            model: model.into(),
            effort,
            mode: SessionMode::Ask,
        }
    }

    pub fn with_mode(mut self, mode: SessionMode) -> Self {
        self.mode = mode;
        self
    }
}

#[derive(Debug, Default)]
pub struct SessionSettingsChange {
    pub model: Option<String>,
    pub effort: Option<EffortLevel>,
    pub mode: Option<SessionMode>,
}

/// The content of one validated model completion. The serde derives on this
/// type and its parts define the JSON stored in the `transcript_entries` table.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AssistantMessage {
    pub text: String,
    pub reasoning: String,
    pub tool_calls: Vec<ToolCall>,
    /// OpenRouter's opaque `reasoning_details`, retained for the next request.
    pub continuation_metadata: Vec<serde_json::Value>,
    /// The usage OpenRouter reported for the model request that produced this
    /// message, or `None` when the stream carried none.
    pub usage: Option<ModelUsage>,
}

/// The input tokens, output tokens, and cost that OpenRouter reports for one
/// model request. OpenRouter reports cost in credits, whose base currency is
/// the US dollar.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModelUsage {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub cost: f64,
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
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ToolCall {
    pub call_id: String,
    pub name: String,
    pub arguments: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ToolResult {
    pub call_id: String,
    pub name: String,
    pub outcome: ToolOutcome,
}

/// Every variant carries text describing what Ox knows about the call.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "status", content = "content", rename_all = "snake_case")]
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

/// One validated assistant message with a final result for each tool call.
/// The store saves this entire value in one transaction.
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

fn validate_transcript(entries: &[TranscriptEntry]) -> io::Result<()> {
    if entries.is_empty() {
        return Ok(());
    }
    if !matches!(entries.first(), Some(TranscriptEntry::Model(_))) {
        return Err(invalid_data("transcript does not open with a model"));
    }
    let mut index = 1;
    let mut previous_prefix = 0;
    let mut complete_batches = Vec::new();
    let mut current_skill: Option<&str> = None;
    while let Some(entry) = entries.get(index) {
        match entry {
            TranscriptEntry::Model(_) => {
                return Err(invalid_data(
                    "a model entry appears after the transcript opened",
                ));
            }
            TranscriptEntry::Effort(_) | TranscriptEntry::Mode(_) => {
                let mut saw_effort = false;
                let mut saw_mode = false;
                while let Some(setting) = entries.get(index) {
                    match setting {
                        TranscriptEntry::Effort(_) if saw_effort => {
                            return Err(invalid_data(
                                "a settings block contains more than one effort entry",
                            ));
                        }
                        TranscriptEntry::Effort(_) => saw_effort = true,
                        TranscriptEntry::Mode(_) if saw_mode => {
                            return Err(invalid_data(
                                "a settings block contains more than one mode entry",
                            ));
                        }
                        TranscriptEntry::Mode(_) => saw_mode = true,
                        _ => break,
                    }
                    index += 1;
                }
                if !matches!(
                    entries.get(index),
                    Some(TranscriptEntry::UserMessage(_) | TranscriptEntry::SkillInvocation(_))
                ) {
                    return Err(invalid_data(
                        "a settings block does not immediately precede a user message or skill invocation",
                    ));
                }
            }
            TranscriptEntry::UserMessage(_) => {
                current_skill = None;
                index += 1;
            }
            TranscriptEntry::SkillInvocation(invocation) => {
                current_skill = Some(&invocation.name);
                index += 1;
            }
            TranscriptEntry::HookFeedback(feedback) => {
                if feedback.skill.is_some() && current_skill != feedback.skill.as_deref() {
                    return Err(invalid_data(
                        "hook feedback does not belong to the current skill invocation",
                    ));
                }
                let previous = entries[..index].iter().rev().find(|entry| {
                    !matches!(entry, TranscriptEntry::HookFeedback(prior) if prior.kind() == feedback.kind())
                }).expect("a transcript begins with a model entry");
                let (placed, place) = match feedback.content {
                    HookFeedbackContent::BeforeRun { .. } => (
                        matches!(
                            previous,
                            TranscriptEntry::UserMessage(_) | TranscriptEntry::SkillInvocation(_)
                        ),
                        "a user message or skill invocation",
                    ),
                    HookFeedbackContent::AfterTools { .. } => (
                        matches!(previous, TranscriptEntry::ToolResult(_)),
                        "the tool results of an assistant batch",
                    ),
                    HookFeedbackContent::BeforeStop { .. } => (
                        matches!(previous,
                            TranscriptEntry::AssistantMessage(message) if message.tool_calls.is_empty()),
                        "an assistant message without tool calls",
                    ),
                };
                if !placed {
                    return Err(invalid_data(format!(
                        "{} hook feedback does not follow {place}",
                        feedback.kind().id()
                    )));
                }
                index += 1;
            }
            TranscriptEntry::CompactionCheckpoint(checkpoint) => {
                if checkpoint.summary.trim().is_empty()
                    || checkpoint.covered_prefix <= previous_prefix
                    || checkpoint.covered_prefix > index
                    || complete_batches
                        .binary_search(&checkpoint.covered_prefix)
                        .is_err()
                {
                    return Err(invalid_data(
                        "invalid compaction checkpoint or covered prefix",
                    ));
                }
                // The preceding scan already validated every assistant batch.
                previous_prefix = checkpoint.covered_prefix;
                index += 1;
            }
            TranscriptEntry::ToolResult(result) => {
                return Err(invalid_data(format!(
                    "tool result {} does not follow an assistant message that called it",
                    result.call_id
                )));
            }
            TranscriptEntry::AssistantMessage(message) => {
                message.validate()?;
                let results = entries[index + 1..]
                    .iter()
                    .take(message.tool_calls.len())
                    .map(|entry| match entry {
                        TranscriptEntry::ToolResult(result) => Ok(result),
                        _ => Err(invalid_data(
                            "an assistant message's tool calls are not all resolved before the next message",
                        )),
                    })
                    .collect::<io::Result<Vec<_>>>()?;
                pair_results(&message.tool_calls, &results)?;
                index += 1 + results.len();
                complete_batches.push(index);
            }
        }
    }
    Ok(())
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SessionSummary {
    pub id: SessionId,
    pub workspace_path: PathBuf,
    pub session_title: Option<String>,
    pub created_at: String,
    pub updated_at: String,
}

/// The sum of every saved model usage cost and summarizer cost, or `None` when
/// no saved entry reported a cost.
pub fn session_cost(transcript: &[TranscriptEntry]) -> Option<f64> {
    transcript
        .iter()
        .filter_map(|entry| match entry {
            TranscriptEntry::AssistantMessage(message) => {
                message.usage.as_ref().map(|usage| usage.cost)
            }
            TranscriptEntry::CompactionCheckpoint(checkpoint) => checkpoint.summarizer_cost,
            _ => None,
        })
        .reduce(|total, cost| total + cost)
}

#[derive(Debug, Clone, PartialEq)]
pub struct StoredSession {
    pub summary: SessionSummary,
    pub transcript: Vec<TranscriptEntry>,
}

impl StoredSession {
    /// The session settings in force after the last transcript entry. An empty
    /// transcript still uses the supplied defaults.
    pub fn saved_settings(&self, defaults: &SessionSettings) -> SessionSettings {
        let mut settings = defaults.clone();
        for entry in &self.transcript {
            match entry {
                TranscriptEntry::Model(model) => settings.model.clone_from(model),
                TranscriptEntry::Effort(effort) => settings.effort = *effort,
                TranscriptEntry::Mode(mode) => settings.mode = *mode,
                TranscriptEntry::UserMessage(_)
                | TranscriptEntry::SkillInvocation(_)
                | TranscriptEntry::AssistantMessage(_)
                | TranscriptEntry::ToolResult(_)
                | TranscriptEntry::HookFeedback(_)
                | TranscriptEntry::CompactionCheckpoint(_) => {}
            }
        }
        settings
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
        Self::initialize(connection)
    }

    fn initialize(connection: Connection) -> io::Result<Self> {
        connection.execute_batch(SCHEMA).map_err(io::Error::other)?;
        connection
            .execute_batch("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;")
            .map_err(io::Error::other)?;
        Ok(Self(Arc::new(Mutex::new(connection))))
    }

    #[cfg(test)]
    pub(crate) fn in_memory() -> Self {
        Self::initialize(Connection::open_in_memory().expect("in-memory database opens"))
            .expect("fresh database initializes")
    }

    #[cfg(test)]
    pub(crate) fn with_connection<T>(&self, f: impl FnOnce(&Connection) -> T) -> T {
        f(&self.lock())
    }

    fn lock(&self) -> MutexGuard<'_, Connection> {
        self.0.lock().expect("session store mutex poisoned")
    }

    /// Creates an empty session. Its settings are saved with its first user
    /// message.
    pub fn create(&self, workspace_path: &Path) -> io::Result<SessionSummary> {
        let path = validate_workspace_path(workspace_path)?;
        let id = SessionId::new(uuid::Uuid::new_v4().to_string());
        let at = now();
        self.lock()
            .execute(
                "INSERT INTO sessions (id, workspace_path, created_at, updated_at)
             VALUES (?1, ?2, ?3, ?3)",
                params![id.to_string(), path, at],
            )
            .map_err(io::Error::other)?;
        Ok(SessionSummary {
            id,
            workspace_path: workspace_path.to_path_buf(),
            session_title: None,
            created_at: at.clone(),
            updated_at: at,
        })
    }

    /// `None` for an absent session. A malformed transcript is an error; no
    /// partial transcript is returned.
    pub fn read(&self, id: &SessionId) -> io::Result<Option<StoredSession>> {
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        let Some(summary) = summary(&tx, id).map_err(io::Error::other)? else {
            return Ok(None);
        };
        let rows = tx
            .prepare(
                "SELECT kind, data FROM transcript_entries
                 WHERE session_id = ?1 ORDER BY id ASC",
            )
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
            .map(|(kind, data)| decode_entry(&kind, &data))
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
    pub fn list(&self, workspace_path: Option<&Path>) -> io::Result<Vec<SessionSummary>> {
        let filter = workspace_path.map(|path| path.to_string_lossy().into_owned());
        let connection = self.lock();
        let mut statement = connection
            .prepare(
                "SELECT sessions.id, sessions.workspace_path, sessions.title,
                        sessions.created_at, sessions.updated_at
                 FROM sessions
                 WHERE ?1 IS NULL OR sessions.workspace_path = ?1
                 ORDER BY sessions.updated_at DESC, sessions.id ASC",
            )
            .map_err(io::Error::other)?;
        statement
            .query_map(params![filter], summary_row)
            .and_then(Iterator::collect)
            .map_err(io::Error::other)
    }

    /// Appends setting entries and the turn start, a user message or skill
    /// invocation, adopts a session title when none has been saved, and
    /// updates activity in one transaction.
    pub fn append_user(
        &self,
        id: &SessionId,
        settings: &SessionSettingsChange,
        turn_start: &TranscriptEntry,
    ) -> io::Result<SessionSummary> {
        let session_title = match turn_start {
            TranscriptEntry::UserMessage(text) => session_title_from_prompt(text),
            TranscriptEntry::SkillInvocation(invocation) => {
                session_title_from_prompt(&invocation.command_text())
            }
            other => panic!("{other:?} does not start a turn"),
        };
        let at = now();
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        update_activity_and_adopt_session_title(&tx, id, session_title, &at)?;
        if let Some(model) = &settings.model {
            insert_entry(&tx, id, &at, "model", model)?;
        }
        if let Some(effort) = settings.effort {
            insert_entry(&tx, id, &at, "effort", &effort)?;
        }
        if let Some(mode) = settings.mode {
            insert_entry(&tx, id, &at, "mode", &mode)?;
        }
        match turn_start {
            TranscriptEntry::UserMessage(text) => {
                insert_entry(&tx, id, &at, "user_message", text)?;
            }
            TranscriptEntry::SkillInvocation(invocation) => {
                insert_entry(&tx, id, &at, "skill_invocation", invocation)?;
            }
            _ => unreachable!("the session title match rejects other entries"),
        }
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
        update_activity_and_adopt_session_title(&tx, id, None, &at)?;
        insert_entry(&tx, id, &at, "assistant_message", &batch.message)?;
        for result in &batch.results {
            insert_entry(&tx, id, &at, "tool_result", result)?;
        }
        tx.commit().map_err(io::Error::other)
    }

    /// Appends hook feedback and updates activity in one transaction.
    pub fn append_hook_feedback(&self, id: &SessionId, feedback: &HookFeedback) -> io::Result<()> {
        let at = now();
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        update_activity_and_adopt_session_title(&tx, id, None, &at)?;
        insert_entry(&tx, id, &at, "hook_feedback", feedback)?;
        tx.commit().map_err(io::Error::other)
    }

    /// Appends a checkpoint atomically, after checking the transcript it was
    /// computed from is still the saved transcript.
    pub fn append_checkpoint(
        &self,
        id: &SessionId,
        expected_len: usize,
        checkpoint: &CompactionCheckpoint,
    ) -> io::Result<()> {
        let at = now();
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        let rows = tx
            .prepare("SELECT kind, data FROM transcript_entries WHERE session_id = ?1 ORDER BY id")
            .and_then(|mut statement| {
                statement
                    .query_map(params![id.to_string()], |row| {
                        Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
                    })?
                    .collect::<rusqlite::Result<Vec<_>>>()
            })
            .map_err(io::Error::other)?;
        if rows.len() != expected_len {
            return Err(invalid_data(
                "transcript changed before compaction checkpoint",
            ));
        }
        let mut entries = rows
            .into_iter()
            .map(|(kind, data)| decode_entry(&kind, &data))
            .collect::<io::Result<Vec<_>>>()?;
        entries.push(TranscriptEntry::CompactionCheckpoint(checkpoint.clone()));
        validate_transcript(&entries)?;
        update_activity_and_adopt_session_title(&tx, id, None, &at)?;
        insert_entry(&tx, id, &at, "compaction_checkpoint", checkpoint)?;
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
fn validate_workspace_path(workspace_path: &Path) -> io::Result<String> {
    if !workspace_path.is_absolute() {
        return Err(io::Error::new(
            ErrorKind::InvalidInput,
            format!(
                "workspace path is not absolute: {}",
                workspace_path.display()
            ),
        ));
    }
    Ok(workspace_path.to_string_lossy().into_owned())
}

fn summary(connection: &Connection, id: &SessionId) -> rusqlite::Result<Option<SessionSummary>> {
    connection
        .query_row(
            "SELECT sessions.id, sessions.workspace_path, sessions.title,
                    sessions.created_at, sessions.updated_at
             FROM sessions
             WHERE sessions.id = ?1",
            params![id.to_string()],
            summary_row,
        )
        .optional()
}

fn summary_row(row: &Row<'_>) -> rusqlite::Result<SessionSummary> {
    Ok(SessionSummary {
        id: SessionId::new(row.get::<_, String>(0)?),
        workspace_path: PathBuf::from(row.get::<_, String>(1)?),
        session_title: row.get(2)?,
        created_at: row.get(3)?,
        updated_at: row.get(4)?,
    })
}

/// Updates activity, adopts the session title if one has not been saved, and
/// fails for an absent session rather than creating one.
fn update_activity_and_adopt_session_title(
    tx: &Transaction<'_>,
    id: &SessionId,
    session_title: Option<String>,
    at: &str,
) -> io::Result<()> {
    let changed = tx
        .execute(
            "UPDATE sessions SET updated_at = ?2, title = COALESCE(title, ?3) WHERE id = ?1",
            params![id.to_string(), at, session_title],
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

fn insert_entry<T: Serialize>(
    tx: &Transaction<'_>,
    id: &SessionId,
    at: &str,
    kind: &str,
    payload: &T,
) -> io::Result<()> {
    let data = serde_json::to_string(payload).expect("transcript entries serialize");
    tx.execute(
        "INSERT INTO transcript_entries (session_id, ts, kind, data)
         VALUES (?1, ?2, ?3, ?4)",
        params![id.to_string(), at, kind, data],
    )
    .map_err(io::Error::other)?;
    Ok(())
}

fn decode_entry(kind: &str, data: &str) -> io::Result<TranscriptEntry> {
    Ok(match kind {
        "model" => TranscriptEntry::Model(decode(kind, data)?),
        "effort" => TranscriptEntry::Effort(decode(kind, data)?),
        "mode" => TranscriptEntry::Mode(decode(kind, data)?),
        "user_message" => TranscriptEntry::UserMessage(decode(kind, data)?),
        "skill_invocation" => TranscriptEntry::SkillInvocation(decode(kind, data)?),
        "assistant_message" => TranscriptEntry::AssistantMessage(decode(kind, data)?),
        "tool_result" => TranscriptEntry::ToolResult(decode(kind, data)?),
        "hook_feedback" => TranscriptEntry::HookFeedback(decode(kind, data)?),
        "compaction_checkpoint" => TranscriptEntry::CompactionCheckpoint(decode(kind, data)?),
        _ => {
            return Err(invalid_data(format!(
                "unknown transcript entry kind {kind:?}"
            )));
        }
    })
}

fn decode<T: DeserializeOwned>(kind: &str, data: &str) -> io::Result<T> {
    serde_json::from_str(data).map_err(|error| invalid_data(format!("{kind} entry: {error}")))
}

fn invalid_data(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidData, message.into())
}

fn now() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true)
}

/// The first nonblank line of a prompt, at most `MAX_SESSION_TITLE_CHARS`
/// characters counting the ellipsis that marks a shortened one. `None` for a
/// blank prompt, so it does not spend the session's one chance at a session
/// title.
fn session_title_from_prompt(text: &str) -> Option<String> {
    let line = text.lines().find(|line| !line.trim().is_empty())?.trim();
    if line.chars().count() <= MAX_SESSION_TITLE_CHARS {
        return Some(line.to_owned());
    }
    let kept: String = line.chars().take(MAX_SESSION_TITLE_CHARS - 1).collect();
    Some(format!("{kept}…"))
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
    use crate::{openrouter, tools};
    use serde_json::json;

    const WORKSPACE_PATH: &str = "/Users/kyle/projects/ox";

    fn workspace() -> &'static Path {
        Path::new(WORKSPACE_PATH)
    }

    fn call(id: &str, command: &str) -> ToolCall {
        ToolCall {
            call_id: id.to_owned(),
            name: tools::SHELL.to_owned(),
            arguments: json!({ "command": command }).to_string(),
        }
    }

    fn result(id: &str, outcome: ToolOutcome) -> ToolResult {
        ToolResult {
            call_id: id.to_owned(),
            name: tools::SHELL.to_owned(),
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
            continuation_metadata: vec![json!({
                "type": "reasoning.encrypted",
                "data": "opaque",
                "id": "rs_1",
                "format": "openai-responses-v1",
                "index": 0,
            })],
            usage: None,
        }
    }

    fn invocation() -> SkillInvocation {
        SkillInvocation {
            name: "goal".to_owned(),
            arguments: "Pass the tests.".to_owned(),
            instructions: "Work until the hook stops you.".to_owned(),
        }
    }

    fn feedback(content: HookFeedbackContent) -> HookFeedback {
        HookFeedback {
            skill: Some("goal".to_owned()),
            content,
        }
    }

    fn before_run_feedback() -> HookFeedback {
        feedback(HookFeedbackContent::BeforeRun {
            message: "The parser lives in src/parse.rs.".to_owned(),
        })
    }

    fn after_tools_feedback() -> HookFeedback {
        feedback(HookFeedbackContent::AfterTools {
            message: "Formatting is clean.".to_owned(),
        })
    }

    fn stop_feedback() -> HookFeedback {
        feedback(HookFeedbackContent::BeforeStop {
            decision: HookDecision::Stop,
            message: "Objective met.".to_owned(),
        })
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
    fn a_saved_batch_survives_database_reopen_in_order() {
        let dir = std::env::temp_dir().join(format!("ox-test-{}", uuid::Uuid::new_v4()));
        let path = dir.join(DATABASE_FILE);
        let answered = message(vec![]);
        let message = message(vec![
            call("call-1", "printf Chicago"),
            call("call-2", "printf Denver"),
        ]);
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
            let id = store.create(workspace()).unwrap().id;
            store
                .append_user(
                    &id,
                    &SessionSettingsChange {
                        model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                        effort: None,
                        mode: Some(SessionMode::Auto),
                    },
                    &TranscriptEntry::UserMessage("Weather in Chicago and Denver?".to_owned()),
                )
                .unwrap();
            let batch = AssistantBatch::new(message.clone(), results.clone()).unwrap();
            store.append_batch(&id, &batch).unwrap();
            store
                .append_checkpoint(
                    &id,
                    6,
                    &CompactionCheckpoint {
                        summary: "Chicago checked; Denver unavailable.".to_owned(),
                        covered_prefix: 6,
                        summarizer_cost: None,
                    },
                )
                .unwrap();
            store
                .append_user(
                    &id,
                    &SessionSettingsChange {
                        model: None,
                        effort: Some(EffortLevel::Low),
                        mode: None,
                    },
                    &TranscriptEntry::SkillInvocation(invocation()),
                )
                .unwrap();
            for skill in [None, Some("goal".to_owned())] {
                store
                    .append_hook_feedback(
                        &id,
                        &HookFeedback {
                            skill,
                            ..before_run_feedback()
                        },
                    )
                    .unwrap();
            }
            store
                .append_batch(&id, &AssistantBatch::new(answered.clone(), vec![]).unwrap())
                .unwrap();
            store.append_hook_feedback(&id, &stop_feedback()).unwrap();
            id
        };

        let store = SessionStore::open(&path).unwrap();
        let stored = store.read(&id).unwrap().expect("session persists");
        assert_eq!(stored.summary.workspace_path, workspace());
        assert_eq!(
            stored.summary.session_title.as_deref(),
            Some("Weather in Chicago and Denver?")
        );
        assert!(matches!(
            stored.transcript.first(),
            Some(TranscriptEntry::Model(model)) if model == openrouter::DEFAULT_MODEL
        ));
        assert_eq!(
            stored.transcript,
            vec![
                TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
                TranscriptEntry::Mode(SessionMode::Auto),
                TranscriptEntry::UserMessage("Weather in Chicago and Denver?".to_owned()),
                TranscriptEntry::AssistantMessage(message.clone()),
                TranscriptEntry::ToolResult(results[0].clone()),
                TranscriptEntry::ToolResult(results[1].clone()),
                TranscriptEntry::CompactionCheckpoint(CompactionCheckpoint {
                    summary: "Chicago checked; Denver unavailable.".to_owned(),
                    covered_prefix: 6,
                    summarizer_cost: None,
                }),
                TranscriptEntry::Effort(EffortLevel::Low),
                TranscriptEntry::SkillInvocation(invocation()),
                TranscriptEntry::HookFeedback(HookFeedback {
                    skill: None,
                    ..before_run_feedback()
                }),
                TranscriptEntry::HookFeedback(before_run_feedback()),
                TranscriptEntry::AssistantMessage(answered),
                TranscriptEntry::HookFeedback(stop_feedback()),
            ]
        );
        assert_eq!(
            stored.saved_settings(&SessionSettings::new("other", EffortLevel::High)),
            SessionSettings::new(openrouter::DEFAULT_MODEL, EffortLevel::Low)
                .with_mode(SessionMode::Auto)
        );
        let mut replay = Vec::new();
        crate::acp::convert::replay_transcript(&stored.transcript, |update| {
            replay.push(update);
            Ok(())
        })
        .unwrap();
        assert_eq!(
            replay.len(),
            11,
            "the checkpoint is hidden but the other entries replay"
        );

        drop(store);
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn batch_validation_rejects_orphan_duplicate_and_missing_results() {
        let two_calls = message(vec![
            call("call-1", "printf Chicago"),
            call("call-2", "printf Denver"),
        ]);

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

        let repeated = message(vec![
            call("call-1", "printf Chicago"),
            call("call-1", "printf Denver"),
        ]);
        assert!(repeated.validate().is_err());
        let unnamed = message(vec![ToolCall {
            name: String::new(),
            ..call("call-1", "printf Chicago")
        }]);
        assert!(unnamed.validate().is_err());
        let anonymous = message(vec![call("", "printf Chicago")]);
        assert!(anonymous.validate().is_err());
    }

    #[test]
    fn read_rejects_a_malformed_transcript() {
        let store = SessionStore::in_memory();
        let insert = |id: &SessionId, kind: &str, data: &str| {
            store.with_connection(|connection| {
                connection
                    .execute(
                        "INSERT INTO transcript_entries (session_id, ts, kind, data)
                         VALUES (?1, ?2, ?3, ?4)",
                        params![id.to_string(), now(), kind, data],
                    )
                    .unwrap()
            });
        };

        let orphan = store.create(workspace()).unwrap().id;
        store
            .append_user(
                &orphan,
                &SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("hello".to_owned()),
            )
            .unwrap();
        insert(
            &orphan,
            "tool_result",
            r#"{"call_id":"x","name":"shell","outcome":{"status":"completed","content":"ok"}}"#,
        );
        assert!(store.read(&orphan).is_err());

        let unresolved = store.create(workspace()).unwrap().id;
        insert(
            &unresolved,
            "model",
            &serde_json::to_string(openrouter::DEFAULT_MODEL).unwrap(),
        );
        let unresolved_message = message(vec![call("call-1", "printf Chicago")]);
        insert(
            &unresolved,
            "assistant_message",
            &serde_json::to_string(&unresolved_message).unwrap(),
        );
        assert!(store.read(&unresolved).is_err());

        let unknown = store.create(workspace()).unwrap().id;
        insert(&unknown, "mystery", "{}");
        assert!(store.read(&unknown).is_err());

        let malformed = store.create(workspace()).unwrap().id;
        insert(
            &malformed,
            "assistant_message",
            r#"{"text":"hi","reasoning":"","tool_calls":[],"continuation_metadata":[],"extra":true}"#,
        );
        assert!(store.read(&malformed).is_err());

        let switched = store.create(workspace()).unwrap().id;
        insert(
            &switched,
            "model",
            &serde_json::to_string(openrouter::DEFAULT_MODEL).unwrap(),
        );
        insert(&switched, "model", r#""other/model""#);
        assert!(store.read(&switched).is_err());

        let unmodelled = store.create(workspace()).unwrap().id;
        insert(&unmodelled, "user_message", r#""hello""#);
        assert!(store.read(&unmodelled).is_err());

        let effort_in_batch = store.create(workspace()).unwrap().id;
        insert(
            &effort_in_batch,
            "model",
            &serde_json::to_string(openrouter::DEFAULT_MODEL).unwrap(),
        );
        let called = message(vec![call("call-1", "printf Chicago")]);
        insert(
            &effort_in_batch,
            "assistant_message",
            &serde_json::to_string(&called).unwrap(),
        );
        insert(&effort_in_batch, "effort", r#""high""#);
        insert(&effort_in_batch, "user_message", r#""next""#);
        assert!(store.read(&effort_in_batch).is_err());

        let user = ("user_message", r#""hello""#.to_owned());
        let skill = (
            "skill_invocation",
            serde_json::to_string(&invocation()).unwrap(),
        );
        let answer = (
            "assistant_message",
            serde_json::to_string(&message(vec![])).unwrap(),
        );
        let called = [
            (
                "assistant_message",
                serde_json::to_string(&message(vec![call("call-1", "printf Chicago")])).unwrap(),
            ),
            (
                "tool_result",
                serde_json::to_string(&completed("call-1")).unwrap(),
            ),
        ];
        let stopped = (
            "hook_feedback",
            serde_json::to_string(&stop_feedback()).unwrap(),
        );
        let global = |feedback: HookFeedback| HookFeedback {
            skill: None,
            ..feedback
        };
        for (feedback, preceding) in [
            (
                global(before_run_feedback()),
                vec![user.clone(), answer.clone()],
            ),
            (global(after_tools_feedback()), vec![user.clone()]),
            (
                global(stop_feedback()),
                [vec![user.clone()], called.to_vec()].concat(),
            ),
            (
                global(before_run_feedback()),
                vec![skill.clone(), answer.clone(), stopped.clone()],
            ),
            (before_run_feedback(), vec![user.clone()]),
            (before_run_feedback(), vec![skill.clone(), answer.clone()]),
            (after_tools_feedback(), vec![user.clone(), answer.clone()]),
            (after_tools_feedback(), vec![skill.clone()]),
            (
                after_tools_feedback(),
                [vec![user.clone()], called.to_vec()].concat(),
            ),
            (stop_feedback(), vec![user.clone()]),
            (
                stop_feedback(),
                [vec![user.clone()], called.to_vec()].concat(),
            ),
            (stop_feedback(), vec![user.clone(), answer.clone(), stopped]),
        ] {
            let misplaced = store.create(workspace()).unwrap().id;
            insert(
                &misplaced,
                "model",
                &serde_json::to_string(openrouter::DEFAULT_MODEL).unwrap(),
            );
            for (kind, data) in &preceding {
                insert(&misplaced, kind, data);
            }
            insert(
                &misplaced,
                "hook_feedback",
                &serde_json::to_string(&feedback).unwrap(),
            );
            assert!(
                store.read(&misplaced).is_err(),
                "{} hook feedback after {:?}",
                feedback.kind().id(),
                preceding.last().unwrap().0
            );
        }

        let duplicate_settings = store.create(workspace()).unwrap().id;
        insert(
            &duplicate_settings,
            "model",
            &serde_json::to_string(openrouter::DEFAULT_MODEL).unwrap(),
        );
        insert(&duplicate_settings, "mode", r#""ask""#);
        insert(&duplicate_settings, "effort", r#""low""#);
        insert(&duplicate_settings, "mode", r#""auto""#);
        insert(&duplicate_settings, "user_message", r#""next""#);
        assert!(store.read(&duplicate_settings).is_err());

        let base = vec![
            TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
            TranscriptEntry::UserMessage("first".to_owned()),
            TranscriptEntry::AssistantMessage(message(vec![call("a", "one"), call("b", "two")])),
            TranscriptEntry::ToolResult(completed("a")),
            TranscriptEntry::ToolResult(completed("b")),
        ];
        for (first, second) in [
            (
                CompactionCheckpoint {
                    summary: " ".to_owned(),
                    covered_prefix: 5,
                    summarizer_cost: None,
                },
                None,
            ),
            (
                CompactionCheckpoint {
                    summary: "ok".to_owned(),
                    covered_prefix: 4,
                    summarizer_cost: None,
                },
                None,
            ),
            (
                CompactionCheckpoint {
                    summary: "ok".to_owned(),
                    covered_prefix: 6,
                    summarizer_cost: None,
                },
                None,
            ),
            (
                CompactionCheckpoint {
                    summary: "ok".to_owned(),
                    covered_prefix: 5,
                    summarizer_cost: None,
                },
                Some(CompactionCheckpoint {
                    summary: "same".to_owned(),
                    covered_prefix: 5,
                    summarizer_cost: None,
                }),
            ),
            (
                CompactionCheckpoint {
                    summary: "ok".to_owned(),
                    covered_prefix: 5,
                    summarizer_cost: None,
                },
                Some(CompactionCheckpoint {
                    summary: "future".to_owned(),
                    covered_prefix: 7,
                    summarizer_cost: None,
                }),
            ),
        ] {
            let id = store.create(workspace()).unwrap().id;
            for entry in &base {
                let (kind, data) = match entry {
                    TranscriptEntry::Model(value) => {
                        ("model", serde_json::to_string(value).unwrap())
                    }
                    TranscriptEntry::UserMessage(value) => {
                        ("user_message", serde_json::to_string(value).unwrap())
                    }
                    TranscriptEntry::AssistantMessage(value) => {
                        ("assistant_message", serde_json::to_string(value).unwrap())
                    }
                    TranscriptEntry::ToolResult(value) => {
                        ("tool_result", serde_json::to_string(value).unwrap())
                    }
                    _ => unreachable!(),
                };
                insert(&id, kind, &data);
            }
            insert(
                &id,
                "compaction_checkpoint",
                &serde_json::to_string(&first).unwrap(),
            );
            if let Some(second) = second {
                insert(
                    &id,
                    "compaction_checkpoint",
                    &serde_json::to_string(&second).unwrap(),
                );
            }
            assert!(store.read(&id).is_err());
        }
    }

    #[test]
    fn empty_transcripts_are_valid_and_settings_fold_in_order() {
        assert!(validate_transcript(&[]).is_ok());
        assert!(
            validate_transcript(&[
                TranscriptEntry::UserMessage("first".to_owned()),
                TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
            ])
            .is_err()
        );
        for settings in [
            [
                TranscriptEntry::Effort(EffortLevel::Low),
                TranscriptEntry::Mode(SessionMode::Auto),
            ],
            [
                TranscriptEntry::Mode(SessionMode::Auto),
                TranscriptEntry::Effort(EffortLevel::Low),
            ],
        ] {
            assert!(
                validate_transcript(&[
                    TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
                    settings[0].clone(),
                    settings[1].clone(),
                    TranscriptEntry::UserMessage("first".to_owned()),
                ])
                .is_ok()
            );
        }
        for duplicate in [
            TranscriptEntry::Effort(EffortLevel::High),
            TranscriptEntry::Mode(SessionMode::Ask),
        ] {
            assert!(
                validate_transcript(&[
                    TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
                    duplicate.clone(),
                    duplicate,
                    TranscriptEntry::UserMessage("first".to_owned()),
                ])
                .is_err()
            );
        }
        assert!(
            validate_transcript(&[
                TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
                TranscriptEntry::Mode(SessionMode::Auto),
                TranscriptEntry::AssistantMessage(message(vec![])),
            ])
            .is_err()
        );
        assert!(
            validate_transcript(&[
                TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
                TranscriptEntry::Mode(SessionMode::Auto),
                TranscriptEntry::SkillInvocation(invocation()),
                TranscriptEntry::AssistantMessage(message(vec![])),
                TranscriptEntry::HookFeedback(stop_feedback()),
            ])
            .is_ok()
        );

        let store = SessionStore::in_memory();
        let id = store.create(workspace()).unwrap().id;
        let empty = store.read(&id).unwrap().unwrap();
        assert!(empty.transcript.is_empty());
        let defaults = SessionSettings::new(openrouter::DEFAULT_MODEL, EffortLevel::Default);
        assert_eq!(empty.saved_settings(&defaults), defaults);

        let without_mode = store.create(workspace()).unwrap().id;
        store
            .append_user(
                &without_mode,
                &SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("old transcript".to_owned()),
            )
            .unwrap();
        assert_eq!(
            store
                .read(&without_mode)
                .unwrap()
                .unwrap()
                .saved_settings(&defaults),
            defaults,
            "a transcript without a mode entry uses Ask from the defaults"
        );

        store
            .append_user(
                &id,
                &SessionSettingsChange {
                    model: Some(openrouter::MODEL_CATALOG[1].id.to_owned()),
                    effort: Some(EffortLevel::Low),
                    mode: Some(SessionMode::Auto),
                },
                &TranscriptEntry::UserMessage("first".to_owned()),
            )
            .unwrap();
        store
            .append_user(
                &id,
                &SessionSettingsChange {
                    model: None,
                    effort: Some(EffortLevel::High),
                    mode: None,
                },
                &TranscriptEntry::UserMessage("second".to_owned()),
            )
            .unwrap();
        store
            .append_user(
                &id,
                &SessionSettingsChange {
                    model: None,
                    effort: Some(EffortLevel::Medium),
                    mode: None,
                },
                &TranscriptEntry::UserMessage("third".to_owned()),
            )
            .unwrap();
        assert_eq!(
            store.read(&id).unwrap().unwrap().saved_settings(&defaults),
            SessionSettings::new(openrouter::MODEL_CATALOG[1].id, EffortLevel::Medium)
                .with_mode(SessionMode::Auto)
        );
        assert!(matches!(
            &store.read(&id).unwrap().unwrap().transcript[..4],
            [
                TranscriptEntry::Model(_),
                TranscriptEntry::Effort(EffortLevel::Low),
                TranscriptEntry::Mode(SessionMode::Auto),
                TranscriptEntry::UserMessage(_),
            ]
        ));
    }

    #[test]
    fn user_append_adopts_a_session_title_once_and_updates_activity() {
        let store = SessionStore::in_memory();
        let created = store.create(workspace()).unwrap();
        set_updated_at(&store, &created.id, "2026-09-18T09:00:00.000Z");

        let first = store
            .append_user(
                &created.id,
                &SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("\n\nFirst line\nsecond line".to_owned()),
            )
            .unwrap();
        assert_eq!(first.session_title.as_deref(), Some("First line"));
        assert_eq!(first.created_at, created.created_at);
        assert!(first.updated_at.as_str() > "2026-09-18T09:00:00.000Z");

        let second = store
            .append_user(
                &created.id,
                &SessionSettingsChange::default(),
                &TranscriptEntry::UserMessage("Something else".to_owned()),
            )
            .unwrap();
        assert_eq!(second.session_title.as_deref(), Some("First line"));
        assert!(second.updated_at >= first.updated_at);

        let long = store.create(workspace()).unwrap();
        let session_title = store
            .append_user(
                &long.id,
                &SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("x".repeat(MAX_SESSION_TITLE_CHARS + 10)),
            )
            .unwrap()
            .session_title
            .unwrap();
        assert_eq!(
            session_title.chars().count(),
            MAX_SESSION_TITLE_CHARS,
            "the ellipsis counts against the limit"
        );
        assert!(session_title.ends_with('…'));

        let skill = store.create(workspace()).unwrap();
        let adopted = store
            .append_user(
                &skill.id,
                &SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::SkillInvocation(invocation()),
            )
            .unwrap();
        assert_eq!(
            adopted.session_title.as_deref(),
            Some("/goal Pass the tests.")
        );

        assert!(
            store
                .append_user(
                    &SessionId::new("missing"),
                    &SessionSettingsChange {
                        model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                        effort: None,
                        mode: None,
                    },
                    &TranscriptEntry::UserMessage("hello".to_owned()),
                )
                .is_err(),
            "appending never creates a session"
        );
    }

    #[test]
    fn list_orders_by_activity_and_filters_by_workspace() {
        let store = SessionStore::in_memory();
        let first = store.create(workspace()).unwrap().id;
        let second = store.create(workspace()).unwrap().id;
        let other = store
            .create(Path::new("/Users/kyle/projects/other"))
            .unwrap()
            .id;
        set_updated_at(&store, &first, "2026-09-18T10:00:00.000Z");
        set_updated_at(&store, &second, "2026-09-18T10:00:00.000Z");
        set_updated_at(&store, &other, "2026-09-18T11:00:00.000Z");

        let mut tied = [first.to_string(), second.to_string()];
        tied.sort();

        assert_eq!(
            ids(&store.list(None).unwrap()),
            vec![other.to_string(), tied[0].clone(), tied[1].clone()]
        );
        assert_eq!(ids(&store.list(Some(workspace())).unwrap()), tied);
        assert!(store.create(Path::new("relative/path")).is_err());
    }

    #[test]
    fn deleting_a_session_removes_its_transcript_entries_and_repeats_successfully() {
        let store = SessionStore::in_memory();
        let id = store.create(workspace()).unwrap().id;
        store
            .append_user(
                &id,
                &SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("hello".to_owned()),
            )
            .unwrap();

        store.delete(&id).unwrap();
        assert!(store.read(&id).unwrap().is_none());
        let transcript_entries: i64 = store.with_connection(|connection| {
            connection
                .query_row("SELECT count(*) FROM transcript_entries", [], |row| {
                    row.get(0)
                })
                .unwrap()
        });
        assert_eq!(transcript_entries, 0);

        store.delete(&id).unwrap();
    }
}
