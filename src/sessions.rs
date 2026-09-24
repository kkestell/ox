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
    id                TEXT PRIMARY KEY,
    parent_session_id TEXT REFERENCES sessions (id) ON DELETE CASCADE,
    workspace_path    TEXT NOT NULL,
    title             TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_by_activity ON sessions (updated_at DESC, id);

CREATE INDEX IF NOT EXISTS sessions_by_parent ON sessions (parent_session_id);

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

/// One entry in a session transcript. Every nonempty transcript opens with a
/// turn start. Each assistant batch contains its message
/// and one outcome per call, in call order. Hook feedback follows the turn
/// start or assistant batch its hook ran after, allowing adjacent feedback of
/// the same kind. Agent messages appear only in a main session's transcript.
#[derive(Debug, Clone, PartialEq)]
pub enum TranscriptEntry {
    TurnStart(TurnStart),
    AssistantBatch(AssistantBatch),
    HookFeedback(HookFeedback),
    CompactionCheckpoint(CompactionCheckpoint),
    AgentMessages(Vec<AgentMessage>),
}

/// A subagent's final answer or failure, published to the main agent.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AgentMessage {
    /// The subagent's child session ID.
    pub subagent_id: String,
    pub content: AgentMessageContent,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "type", content = "text", rename_all = "snake_case")]
pub enum AgentMessageContent {
    FinalAnswer(String),
    Failure(String),
}

impl AgentMessage {
    /// The same attribution in model requests, summarizer material, and ACP
    /// updates.
    pub fn label(&self) -> String {
        match self.content {
            AgentMessageContent::FinalAnswer(_) => {
                format!("Final answer from subagent {}", self.subagent_id)
            }
            AgentMessageContent::Failure(_) => format!("Failure of subagent {}", self.subagent_id),
        }
    }

    pub fn text(&self) -> &str {
        match &self.content {
            AgentMessageContent::FinalAnswer(text) | AgentMessageContent::Failure(text) => text,
        }
    }
}

/// The input that starts a turn, saved with the model, effort level, and
/// session mode captured for that turn.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TurnStart {
    pub model: String,
    pub effort: EffortLevel,
    pub mode: SessionMode,
    pub input: TurnInput,
}

/// The user message or skill invocation that starts a turn.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(
    tag = "type",
    content = "content",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub enum TurnInput {
    UserMessage(UserMessage),
    SkillInvocation(SkillInvocation),
}

#[cfg(test)]
impl TranscriptEntry {
    /// A turn start entry with the settings of a new ACP session.
    pub(crate) fn turn(input: impl Into<TurnInput>) -> Self {
        Self::TurnStart(TurnStart::test(input))
    }
}

#[cfg(test)]
impl TurnStart {
    /// A turn start with the settings of a new ACP session: the test
    /// catalog's default model at Default effort in Ask mode.
    pub(crate) fn test(input: impl Into<TurnInput>) -> Self {
        Self {
            model: crate::openrouter::fixture::DEFAULT_MODEL.to_owned(),
            effort: EffortLevel::Default,
            mode: SessionMode::Ask,
            input: input.into(),
        }
    }
}

#[cfg(test)]
impl From<String> for TurnInput {
    fn from(text: String) -> Self {
        Self::UserMessage(text.into())
    }
}

#[cfg(test)]
impl From<UserMessage> for TurnInput {
    fn from(message: UserMessage) -> Self {
        Self::UserMessage(message)
    }
}

#[cfg(test)]
impl From<SkillInvocation> for TurnInput {
    fn from(invocation: SkillInvocation) -> Self {
        Self::SkillInvocation(invocation)
    }
}

/// The index and value of the latest turn start.
pub fn latest_turn_start(transcript: &[TranscriptEntry]) -> Option<(usize, &TurnStart)> {
    transcript
        .iter()
        .enumerate()
        .rev()
        .find_map(|(index, entry)| match entry {
            TranscriptEntry::TurnStart(turn_start) => Some((index, turn_start)),
            _ => None,
        })
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct UserMessage {
    pub parts: Vec<UserMessagePart>,
}

impl UserMessage {
    pub fn text(&self) -> String {
        self.parts
            .iter()
            .filter_map(|part| match part {
                UserMessagePart::Text(text) => Some(text.as_str()),
                UserMessagePart::Image(_) => None,
            })
            .collect::<Vec<_>>()
            .join("\n")
    }

    pub fn has_images(&self) -> bool {
        self.parts
            .iter()
            .any(|part| matches!(part, UserMessagePart::Image(_)))
    }
}

impl From<String> for UserMessage {
    fn from(text: String) -> Self {
        Self {
            parts: vec![UserMessagePart::Text(text)],
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "type", content = "content", rename_all = "snake_case")]
pub enum UserMessagePart {
    Text(String),
    Image(ImageAttachment),
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ImageAttachment {
    pub data: String,
    pub mime_type: String,
}

/// A skill invoked as a slash command, saved in place of the user message for
/// its turn. The instructions are copied from the skill when it is invoked.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SkillInvocation {
    pub name: String,
    pub arguments: String,
    pub instructions: String,
    #[serde(default)]
    pub images: Vec<ImageAttachment>,
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
        decision: StopDecision,
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
pub enum StopDecision {
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

/// Whether shell calls and input sent to shell processes require approval
/// from the ACP client.
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
            Self::Ask => "Ask before running each shell command or sending input to one.",
            Self::Auto => "Run shell commands and send them input without asking.",
        }
    }

    pub fn from_id(id: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|mode| mode.id() == id)
    }
}

/// How much reasoning Ox asks a model to do, in ascending order. `Default`
/// leaves the choice to the model; every other level is the OpenRouter effort
/// of the same id.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum EffortLevel {
    Default,
    None,
    Minimal,
    Low,
    Medium,
    High,
    #[serde(rename = "xhigh")]
    XHigh,
    Max,
}

impl EffortLevel {
    pub const ALL: [Self; 8] = [
        Self::Default,
        Self::None,
        Self::Minimal,
        Self::Low,
        Self::Medium,
        Self::High,
        Self::XHigh,
        Self::Max,
    ];

    pub fn id(self) -> &'static str {
        match self {
            Self::Default => "default",
            Self::None => "none",
            Self::Minimal => "minimal",
            Self::Low => "low",
            Self::Medium => "medium",
            Self::High => "high",
            Self::XHigh => "xhigh",
            Self::Max => "max",
        }
    }

    pub fn name(self) -> &'static str {
        match self {
            Self::Default => "Default",
            Self::None => "None",
            Self::Minimal => "Minimal",
            Self::Low => "Low",
            Self::Medium => "Medium",
            Self::High => "High",
            Self::XHigh => "Extra high",
            Self::Max => "Max",
        }
    }

    pub fn from_id(id: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|level| level.id() == id)
    }

    /// The OpenRouter `reasoning.effort`, omitted for `Default`.
    pub fn openrouter_effort(self) -> Option<&'static str> {
        (self != Self::Default).then(|| self.id())
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

    /// `completed`, `failed`, or `cancelled`.
    pub fn status(&self) -> &'static str {
        match self {
            Self::Completed(_) => "completed",
            Self::Failed(_) => "failed",
            Self::Cancelled(_) => "cancelled",
        }
    }
}

/// One validated assistant message with a final outcome for each tool call.
/// Outcome `i` belongs to tool call `i`. The store saves this entire value as
/// one entry in one transaction.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AssistantBatch {
    pub message: AssistantMessage,
    pub outcomes: Vec<ToolOutcome>,
}

impl AssistantBatch {
    pub fn new(message: AssistantMessage, outcomes: Vec<ToolOutcome>) -> io::Result<Self> {
        let batch = Self { message, outcomes };
        batch.validate()?;
        Ok(batch)
    }

    fn validate(&self) -> io::Result<()> {
        self.message.validate()?;
        let calls = self.message.tool_calls.len();
        let outcomes = self.outcomes.len();
        if calls != outcomes {
            return Err(invalid_data(format!(
                "an assistant message with {calls} tool calls has {outcomes} tool outcomes"
            )));
        }
        Ok(())
    }
}

fn validate_transcript(entries: &[TranscriptEntry]) -> io::Result<()> {
    if entries.is_empty() {
        return Ok(());
    }
    if !matches!(entries.first(), Some(TranscriptEntry::TurnStart(_))) {
        return Err(invalid_data("transcript does not open with a turn start"));
    }
    let mut previous_prefix = 0;
    let mut current_skill: Option<&str> = None;
    for (index, entry) in entries.iter().enumerate() {
        match entry {
            TranscriptEntry::TurnStart(turn_start) => {
                current_skill = match &turn_start.input {
                    TurnInput::UserMessage(_) => None,
                    TurnInput::SkillInvocation(invocation) => Some(&invocation.name),
                };
            }
            TranscriptEntry::HookFeedback(feedback) => {
                check_hook_feedback_skill(feedback, current_skill)?;
                check_hook_feedback_placement(entries, index, feedback)?;
            }
            TranscriptEntry::CompactionCheckpoint(checkpoint) => {
                check_compaction_checkpoint(checkpoint, index, previous_prefix, entries)?;
                previous_prefix = checkpoint.covered_prefix;
            }
            TranscriptEntry::AssistantBatch(batch) => batch.validate()?,
            TranscriptEntry::AgentMessages(messages) => {
                if messages.is_empty() {
                    return Err(invalid_data("agent messages entry is empty"));
                }
            }
        }
    }
    Ok(())
}

fn check_hook_feedback_skill(
    feedback: &HookFeedback,
    current_skill: Option<&str>,
) -> io::Result<()> {
    if feedback.skill.is_some() && current_skill != feedback.skill.as_deref() {
        return Err(invalid_data(
            "hook feedback does not belong to the current skill invocation",
        ));
    }
    Ok(())
}

fn check_hook_feedback_placement(
    entries: &[TranscriptEntry],
    index: usize,
    feedback: &HookFeedback,
) -> io::Result<()> {
    let previous = entries[..index].iter().rev().find(|entry| {
        !matches!(entry, TranscriptEntry::HookFeedback(prior) if prior.kind() == feedback.kind())
    }).expect("a transcript begins with a turn start");
    let (placed, place) = match feedback.content {
        HookFeedbackContent::BeforeRun { .. } => (
            matches!(previous, TranscriptEntry::TurnStart(_)),
            "a user message or skill invocation",
        ),
        HookFeedbackContent::AfterTools { .. } => (
            matches!(previous, TranscriptEntry::AssistantBatch(batch) if !batch.message.tool_calls.is_empty()),
            "the tool results of an assistant batch",
        ),
        HookFeedbackContent::BeforeStop { .. } => (
            matches!(previous,
                TranscriptEntry::AssistantBatch(batch) if batch.message.tool_calls.is_empty()),
            "an assistant message without tool calls",
        ),
    };
    if !placed {
        return Err(invalid_data(format!(
            "{} hook feedback does not follow {place}",
            feedback.kind().id()
        )));
    }
    Ok(())
}

fn check_compaction_checkpoint(
    checkpoint: &CompactionCheckpoint,
    index: usize,
    previous_prefix: usize,
    entries: &[TranscriptEntry],
) -> io::Result<()> {
    // The preceding scan already validated every assistant batch.
    if checkpoint.summary.trim().is_empty()
        || checkpoint.covered_prefix <= previous_prefix
        || checkpoint.covered_prefix > index
        || !matches!(
            entries[checkpoint.covered_prefix - 1],
            TranscriptEntry::AssistantBatch(_)
        )
    {
        return Err(invalid_data(
            "invalid compaction checkpoint or covered prefix",
        ));
    }
    Ok(())
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SessionSummary {
    pub id: SessionId,
    /// The main session of a child session; `None` for a main session.
    pub parent_session_id: Option<SessionId>,
    pub workspace_path: PathBuf,
    pub session_title: Option<String>,
    pub created_at: String,
    pub updated_at: String,
}

/// The sum of every saved model usage cost and summarizer cost in one
/// transcript, or `None` when no saved entry reported a cost.
pub fn transcript_cost(transcript: &[TranscriptEntry]) -> Option<f64> {
    transcript
        .iter()
        .filter_map(|entry| match entry {
            TranscriptEntry::AssistantBatch(batch) => {
                batch.message.usage.as_ref().map(|usage| usage.cost)
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
    /// The model, effort level, and session mode of the latest turn start. An
    /// empty transcript uses the supplied defaults.
    pub fn saved_settings(&self, defaults: &SessionSettings) -> SessionSettings {
        let Some((_, turn_start)) = latest_turn_start(&self.transcript) else {
            return defaults.clone();
        };
        SessionSettings::new(turn_start.model.clone(), turn_start.effort).with_mode(turn_start.mode)
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

    /// Creates an empty main session. Its settings are saved with its first
    /// turn start.
    pub fn create(&self, workspace_path: &Path) -> io::Result<SessionSummary> {
        self.insert(None, workspace_path)
    }

    /// Creates an empty child session of the main session `parent`. Deleting
    /// the main session deletes it; it never appears in listing.
    pub fn create_child(
        &self,
        parent: &SessionId,
        workspace_path: &Path,
    ) -> io::Result<SessionSummary> {
        self.insert(Some(parent), workspace_path)
    }

    fn insert(
        &self,
        parent: Option<&SessionId>,
        workspace_path: &Path,
    ) -> io::Result<SessionSummary> {
        let path = validate_workspace_path(workspace_path)?;
        let id = SessionId::new(uuid::Uuid::new_v4().to_string());
        let at = now();
        self.lock()
            .execute(
                "INSERT INTO sessions (id, parent_session_id, workspace_path, created_at, updated_at)
             VALUES (?1, ?2, ?3, ?4, ?4)",
                params![id.to_string(), parent.map(ToString::to_string), path, at],
            )
            .map_err(io::Error::other)?;
        Ok(SessionSummary {
            id,
            parent_session_id: parent.cloned(),
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
        let transcript = read_transcript(&tx, id)
            .and_then(|transcript| validate_transcript(&transcript).map(|()| transcript))
            .map_err(|error| {
                io::Error::new(
                    error.kind(),
                    format!("session {id} has an invalid transcript: {error}"),
                )
            })?;
        Ok(Some(StoredSession {
            summary,
            transcript,
        }))
    }

    /// Main sessions, most recently active first, ties broken by ID so the
    /// order is stable.
    pub fn list(&self, workspace_path: Option<&Path>) -> io::Result<Vec<SessionSummary>> {
        let filter = workspace_path.map(|path| path.to_string_lossy().into_owned());
        let connection = self.lock();
        let mut statement = connection
            .prepare(
                "SELECT sessions.id, sessions.parent_session_id, sessions.workspace_path,
                        sessions.title, sessions.created_at, sessions.updated_at
                 FROM sessions
                 WHERE sessions.parent_session_id IS NULL
                   AND (?1 IS NULL OR sessions.workspace_path = ?1)
                 ORDER BY sessions.updated_at DESC, sessions.id ASC",
            )
            .map_err(io::Error::other)?;
        statement
            .query_map(params![filter], summary_row)
            .and_then(Iterator::collect)
            .map_err(io::Error::other)
    }

    /// Appends a turn start, adopts a session title when none has been saved,
    /// and updates activity in one transaction.
    pub fn append_turn_start(
        &self,
        id: &SessionId,
        turn_start: &TurnStart,
    ) -> io::Result<SessionSummary> {
        let session_title = match &turn_start.input {
            TurnInput::UserMessage(message) => session_title_from_prompt(&message.text())
                .or_else(|| message.has_images().then(|| "Image".to_owned())),
            TurnInput::SkillInvocation(invocation) => {
                session_title_from_prompt(&invocation.command_text())
            }
        };
        self.append(
            id,
            session_title,
            &[TranscriptEntry::TurnStart(turn_start.clone())],
        )
    }

    /// Appends the assistant message and all of its outcomes, and updates
    /// activity, in one transaction.
    pub fn append_batch(&self, id: &SessionId, batch: &AssistantBatch) -> io::Result<()> {
        batch.validate()?;
        self.append(id, None, &[TranscriptEntry::AssistantBatch(batch.clone())])
            .map(drop)
    }

    /// Appends hook feedback and updates activity in one transaction.
    pub fn append_hook_feedback(&self, id: &SessionId, feedback: &HookFeedback) -> io::Result<()> {
        self.append(id, None, &[TranscriptEntry::HookFeedback(feedback.clone())])
            .map(drop)
    }

    /// Appends subagent messages to a main session and updates activity in
    /// one transaction.
    pub fn append_agent_messages(
        &self,
        id: &SessionId,
        messages: &[AgentMessage],
    ) -> io::Result<()> {
        if messages.is_empty() {
            return Err(invalid_data("agent messages entry is empty"));
        }
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        if summary(&tx, id)
            .map_err(io::Error::other)?
            .is_some_and(|summary| summary.parent_session_id.is_some())
        {
            return Err(io::Error::new(
                ErrorKind::InvalidInput,
                "agent messages belong only in a main session",
            ));
        }
        write_entries(
            &tx,
            id,
            None,
            &[TranscriptEntry::AgentMessages(messages.to_vec())],
        )?;
        tx.commit().map_err(io::Error::other)
    }

    /// The summed model usage and summarizer cost saved in every child
    /// session of `id`, or `None` when none reported a cost.
    pub fn children_cost(&self, id: &SessionId) -> io::Result<Option<f64>> {
        self.lock()
            .query_row(
                "SELECT SUM(CASE transcript_entries.kind
                         WHEN 'assistant_batch' THEN json_extract(data, '$.message.usage.cost')
                         ELSE json_extract(data, '$.summarizer_cost') END)
                 FROM transcript_entries
                 JOIN sessions ON sessions.id = transcript_entries.session_id
                 WHERE sessions.parent_session_id = ?1
                   AND transcript_entries.kind IN ('assistant_batch', 'compaction_checkpoint')",
                params![id.to_string()],
                |row| row.get(0),
            )
            .map_err(io::Error::other)
    }

    /// Appends a checkpoint atomically, after checking the transcript it was
    /// computed from is still the saved transcript.
    pub fn append_checkpoint(
        &self,
        id: &SessionId,
        expected_len: usize,
        checkpoint: &CompactionCheckpoint,
    ) -> io::Result<()> {
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        let mut entries = read_transcript(&tx, id)?;
        if entries.len() != expected_len {
            return Err(invalid_data(
                "transcript changed before compaction checkpoint",
            ));
        }
        let entry = TranscriptEntry::CompactionCheckpoint(checkpoint.clone());
        entries.push(entry.clone());
        validate_transcript(&entries)?;
        write_entries(&tx, id, None, &[entry])?;
        tx.commit().map_err(io::Error::other)
    }

    fn append(
        &self,
        id: &SessionId,
        session_title: Option<String>,
        entries: &[TranscriptEntry],
    ) -> io::Result<SessionSummary> {
        let mut connection = self.lock();
        let tx = connection.transaction().map_err(io::Error::other)?;
        let summary = write_entries(&tx, id, session_title, entries)?;
        tx.commit().map_err(io::Error::other)?;
        Ok(summary)
    }

    /// Removes a main session, its transcript, and its child sessions. An
    /// absent session is a success; a child session is deleted only with its
    /// main session.
    pub fn delete(&self, id: &SessionId) -> io::Result<()> {
        self.lock()
            .execute(
                "DELETE FROM sessions WHERE id = ?1 AND parent_session_id IS NULL",
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
            "SELECT sessions.id, sessions.parent_session_id, sessions.workspace_path,
                    sessions.title, sessions.created_at, sessions.updated_at
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
        parent_session_id: row.get::<_, Option<String>>(1)?.map(SessionId::new),
        workspace_path: PathBuf::from(row.get::<_, String>(2)?),
        session_title: row.get(3)?,
        created_at: row.get(4)?,
        updated_at: row.get(5)?,
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

/// Updates activity, adopts the session title if one has not been saved,
/// inserts `entries`, and returns the updated session summary. The caller
/// commits.
fn write_entries(
    tx: &Transaction<'_>,
    id: &SessionId,
    session_title: Option<String>,
    entries: &[TranscriptEntry],
) -> io::Result<SessionSummary> {
    let at = now();
    update_activity_and_adopt_session_title(tx, id, session_title, &at)?;
    for entry in entries {
        insert_entry(tx, id, &at, entry)?;
    }
    Ok(summary(tx, id)
        .map_err(io::Error::other)?
        .expect("a session that was just updated exists"))
}

fn read_transcript(tx: &Transaction<'_>, id: &SessionId) -> io::Result<Vec<TranscriptEntry>> {
    tx.prepare(
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
    .map_err(io::Error::other)?
    .into_iter()
    .map(|(kind, data)| decode_entry(&kind, &data))
    .collect()
}

fn insert_entry(
    tx: &Transaction<'_>,
    id: &SessionId,
    at: &str,
    entry: &TranscriptEntry,
) -> io::Result<()> {
    let (kind, data) = encode_entry(entry);
    tx.execute(
        "INSERT INTO transcript_entries (session_id, ts, kind, data)
         VALUES (?1, ?2, ?3, ?4)",
        params![id.to_string(), at, kind, data],
    )
    .map_err(io::Error::other)?;
    Ok(())
}

fn encode_entry(entry: &TranscriptEntry) -> (&'static str, String) {
    let (kind, data) = match entry {
        TranscriptEntry::TurnStart(turn_start) => ("turn_start", serde_json::to_string(turn_start)),
        TranscriptEntry::AssistantBatch(batch) => ("assistant_batch", serde_json::to_string(batch)),
        TranscriptEntry::HookFeedback(feedback) => {
            ("hook_feedback", serde_json::to_string(feedback))
        }
        TranscriptEntry::CompactionCheckpoint(checkpoint) => {
            ("compaction_checkpoint", serde_json::to_string(checkpoint))
        }
        TranscriptEntry::AgentMessages(messages) => {
            ("agent_messages", serde_json::to_string(messages))
        }
    };
    (kind, data.expect("transcript entries serialize"))
}

fn decode_entry(kind: &str, data: &str) -> io::Result<TranscriptEntry> {
    Ok(match kind {
        "turn_start" => TranscriptEntry::TurnStart(decode(kind, data)?),
        "assistant_batch" => TranscriptEntry::AssistantBatch(decode(kind, data)?),
        "hook_feedback" => TranscriptEntry::HookFeedback(decode(kind, data)?),
        "compaction_checkpoint" => TranscriptEntry::CompactionCheckpoint(decode(kind, data)?),
        "agent_messages" => TranscriptEntry::AgentMessages(decode(kind, data)?),
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

    fn completed() -> ToolOutcome {
        ToolOutcome::Completed("ok".to_owned())
    }

    fn turn_with(effort: EffortLevel, mode: SessionMode, input: impl Into<TurnInput>) -> TurnStart {
        TurnStart {
            effort,
            mode,
            ..TurnStart::test(input)
        }
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
            images: vec![],
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
            decision: StopDecision::Stop,
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
        let outcomes = vec![
            ToolOutcome::Completed("Sunny in Chicago.".to_owned()),
            ToolOutcome::Failed("Denver is unavailable.".to_owned()),
        ];
        let first = turn_with(
            EffortLevel::Default,
            SessionMode::Auto,
            "Weather in Chicago and Denver?".to_owned(),
        );
        let second = turn_with(EffortLevel::Low, SessionMode::Auto, invocation());
        let checkpoint = CompactionCheckpoint {
            summary: "Chicago checked; Denver unavailable.".to_owned(),
            covered_prefix: 2,
            summarizer_cost: None,
        };

        let id = {
            let store = SessionStore::open(&path).unwrap();
            let id = store.create(workspace()).unwrap().id;
            store.append_turn_start(&id, &first).unwrap();
            let batch = AssistantBatch::new(message.clone(), outcomes.clone()).unwrap();
            store.append_batch(&id, &batch).unwrap();
            store.append_checkpoint(&id, 2, &checkpoint).unwrap();
            store.append_turn_start(&id, &second).unwrap();
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
        assert_eq!(
            stored.transcript,
            vec![
                TranscriptEntry::TurnStart(first),
                TranscriptEntry::AssistantBatch(AssistantBatch {
                    message: message.clone(),
                    outcomes,
                }),
                TranscriptEntry::CompactionCheckpoint(checkpoint),
                TranscriptEntry::TurnStart(second),
                TranscriptEntry::HookFeedback(HookFeedback {
                    skill: None,
                    ..before_run_feedback()
                }),
                TranscriptEntry::HookFeedback(before_run_feedback()),
                TranscriptEntry::AssistantBatch(AssistantBatch {
                    message: answered,
                    outcomes: vec![]
                }),
                TranscriptEntry::HookFeedback(stop_feedback()),
            ]
        );
        assert_eq!(
            stored.saved_settings(&SessionSettings::new("other", EffortLevel::High)),
            SessionSettings::new(openrouter::fixture::DEFAULT_MODEL, EffortLevel::Low)
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

    fn insert_row(store: &SessionStore, id: &SessionId, kind: &str, data: &str) {
        store.with_connection(|connection| {
            connection
                .execute(
                    "INSERT INTO transcript_entries (session_id, ts, kind, data)
                     VALUES (?1, ?2, ?3, ?4)",
                    params![id.to_string(), now(), kind, data],
                )
                .unwrap()
        });
    }

    /// The error from reading a session whose stored rows are `rows`.
    fn read_error(rows: &[(&str, String)]) -> String {
        let store = SessionStore::in_memory();
        let id = store.create(workspace()).unwrap().id;
        for (kind, data) in rows {
            insert_row(&store, &id, kind, data);
        }
        store.read(&id).unwrap_err().to_string()
    }

    fn row(entry: &TranscriptEntry) -> (&'static str, String) {
        encode_entry(entry)
    }

    fn user_row() -> (&'static str, String) {
        row(&TranscriptEntry::turn("hello".to_owned()))
    }

    #[test]
    fn a_nonempty_transcript_opens_with_a_turn_start() {
        let answer = row(&TranscriptEntry::AssistantBatch(
            AssistantBatch::new(message(vec![]), vec![]).unwrap(),
        ));
        let model = ("model", r#""other/model""#.to_owned());
        for (rows, expected) in [
            (vec![answer], "transcript does not open with a turn start"),
            (
                vec![user_row(), model],
                r#"unknown transcript entry kind "model""#,
            ),
        ] {
            let error = read_error(&rows);
            assert!(error.ends_with(expected), "{error}");
        }
    }

    #[test]
    fn malformed_assistant_batches_are_rejected_at_every_boundary() {
        let called = message(vec![call("a", "one"), call("b", "two")]);
        let mut empty_id = called.clone();
        empty_id.tool_calls[0].call_id.clear();
        let mut repeated_id = called.clone();
        repeated_id.tool_calls[1].call_id = "a".to_owned();
        let mut unnamed = called.clone();
        unnamed.tool_calls[0].name.clear();
        let store = SessionStore::in_memory();
        for (case, message, outcomes) in [
            ("a missing outcome", called.clone(), vec![completed()]),
            ("an extra outcome", called, vec![completed(); 3]),
            ("an empty call ID", empty_id, vec![completed(); 2]),
            ("a repeated call ID", repeated_id, vec![completed(); 2]),
            ("an empty tool name", unnamed, vec![completed(); 2]),
        ] {
            assert!(
                AssistantBatch::new(message.clone(), outcomes.clone()).is_err(),
                "construction accepts {case}"
            );
            let batch = AssistantBatch { message, outcomes };
            let id = store.create(workspace()).unwrap().id;
            let (kind, data) = user_row();
            insert_row(&store, &id, kind, &data);
            assert!(
                store.append_batch(&id, &batch).is_err(),
                "append accepts {case}"
            );
            insert_row(
                &store,
                &id,
                "assistant_batch",
                &serde_json::to_string(&batch).unwrap(),
            );
            assert!(store.read(&id).is_err(), "read accepts {case}");
        }
    }

    #[test]
    fn entries_that_do_not_decode_fail_the_read() {
        let hello = json!({ "type": "user_message", "content": { "parts": [
            { "type": "text", "content": "hello" },
        ] } });
        for (case, kind, data) in [
            ("an unknown kind", "mystery", json!({})),
            (
                "an unknown batch field",
                "assistant_batch",
                json!({ "message": message(vec![]), "outcomes": [], "extra": true }),
            ),
            (
                "an unknown message field",
                "assistant_batch",
                json!({
                    "message": {"text":"hi", "reasoning":"", "tool_calls":[],
                        "continuation_metadata":[], "extra":true}, "outcomes": [],
                }),
            ),
            (
                "an unknown turn start field",
                "turn_start",
                json!({ "effort": "low", "mode": "ask", "input": hello, "extra": true }),
            ),
            (
                "a missing effort level",
                "turn_start",
                json!({ "mode": "ask", "input": hello }),
            ),
            (
                "an unknown effort level",
                "turn_start",
                json!({ "effort": "loud", "mode": "ask", "input": hello }),
            ),
            (
                "an unknown turn input",
                "turn_start",
                json!({ "effort": "low", "mode": "ask",
                    "input": { "type": "voice_note", "content": "hello" } }),
            ),
        ] {
            let error = read_error(&[(kind, data.to_string())]);
            assert!(error.contains(kind), "{case}: {error}");
        }
    }

    #[test]
    fn hook_feedback_follows_its_hook_point_within_its_skill_turn() {
        let user = user_row();
        let skill = row(&TranscriptEntry::turn(invocation()));
        let answer = row(&TranscriptEntry::AssistantBatch(
            AssistantBatch::new(message(vec![]), vec![]).unwrap(),
        ));
        let called = row(&TranscriptEntry::AssistantBatch(
            AssistantBatch::new(
                message(vec![call("call-1", "printf Chicago")]),
                vec![completed()],
            )
            .unwrap(),
        ));
        let stopped = row(&TranscriptEntry::HookFeedback(stop_feedback()));
        let global = |feedback: HookFeedback| HookFeedback {
            skill: None,
            ..feedback
        };
        let not_after_turn_start =
            "before_run hook feedback does not follow a user message or skill invocation";
        let not_after_tools =
            "after_tools hook feedback does not follow the tool results of an assistant batch";
        let not_after_answer =
            "before_stop hook feedback does not follow an assistant message without tool calls";
        let other_skill = "hook feedback does not belong to the current skill invocation";
        for (case, feedback, preceding, expected) in [
            (
                "global before_run after an answer",
                global(before_run_feedback()),
                vec![user.clone(), answer.clone()],
                not_after_turn_start,
            ),
            (
                "global after_tools after a turn start",
                global(after_tools_feedback()),
                vec![user.clone()],
                not_after_tools,
            ),
            (
                "global before_stop after tool calls",
                global(stop_feedback()),
                vec![user.clone(), called.clone()],
                not_after_answer,
            ),
            (
                "global before_run after before_stop feedback",
                global(before_run_feedback()),
                vec![skill.clone(), answer.clone(), stopped.clone()],
                not_after_turn_start,
            ),
            (
                "skill before_run in a user turn",
                before_run_feedback(),
                vec![user.clone()],
                other_skill,
            ),
            (
                "skill before_run after an answer",
                before_run_feedback(),
                vec![skill.clone(), answer.clone()],
                not_after_turn_start,
            ),
            (
                "skill after_tools in a user turn after an answer",
                after_tools_feedback(),
                vec![user.clone(), answer.clone()],
                other_skill,
            ),
            (
                "skill after_tools after a turn start",
                after_tools_feedback(),
                vec![skill.clone()],
                not_after_tools,
            ),
            (
                "skill after_tools in a user turn after tool calls",
                after_tools_feedback(),
                vec![user.clone(), called.clone()],
                other_skill,
            ),
            (
                "skill before_stop in a user turn",
                stop_feedback(),
                vec![user.clone()],
                other_skill,
            ),
            (
                "skill before_stop in a user turn after tool calls",
                stop_feedback(),
                vec![user.clone(), called],
                other_skill,
            ),
            (
                "skill before_stop in a user turn after before_stop feedback",
                stop_feedback(),
                vec![user.clone(), answer.clone(), stopped],
                other_skill,
            ),
        ] {
            let rows: Vec<_> = preceding
                .into_iter()
                .chain([row(&TranscriptEntry::HookFeedback(feedback))])
                .collect();
            let error = read_error(&rows);
            assert!(error.ends_with(expected), "{case}: {error}");
        }
    }

    #[test]
    fn checkpoints_cover_a_growing_prefix_that_ends_at_an_assistant_batch() {
        let base = [
            TranscriptEntry::turn("first".to_owned()),
            TranscriptEntry::AssistantBatch(AssistantBatch {
                message: message(vec![call("a", "one"), call("b", "two")]),
                outcomes: vec![completed(), completed()],
            }),
        ];
        let checkpoint = |summary: &str, covered_prefix| {
            row(&TranscriptEntry::CompactionCheckpoint(
                CompactionCheckpoint {
                    summary: summary.to_owned(),
                    covered_prefix,
                    summarizer_cost: None,
                },
            ))
        };
        for (case, checkpoints) in [
            ("a blank summary", vec![checkpoint(" ", 2)]),
            ("an empty prefix", vec![checkpoint("ok", 0)]),
            ("a prefix ending at a turn start", vec![checkpoint("ok", 1)]),
            ("a prefix past the checkpoint", vec![checkpoint("ok", 3)]),
            (
                "a repeated prefix",
                vec![checkpoint("ok", 2), checkpoint("next", 2)],
            ),
            (
                "a shrinking prefix",
                vec![checkpoint("ok", 2), checkpoint("next", 1)],
            ),
            (
                "a later prefix past its checkpoint",
                vec![checkpoint("ok", 2), checkpoint("next", 4)],
            ),
        ] {
            let rows: Vec<_> = base.iter().map(row).chain(checkpoints).collect();
            let error = read_error(&rows);
            assert!(
                error.ends_with("invalid compaction checkpoint or covered prefix"),
                "{case}: {error}"
            );
        }
    }

    #[test]
    fn saved_settings_come_from_the_latest_turn_start() {
        let store = SessionStore::in_memory();
        let id = store.create(workspace()).unwrap().id;
        let defaults = SessionSettings::new(openrouter::fixture::DEFAULT_MODEL, EffortLevel::High)
            .with_mode(SessionMode::Auto);
        assert_eq!(
            store.read(&id).unwrap().unwrap().saved_settings(&defaults),
            defaults,
            "an empty transcript uses the defaults"
        );

        let chosen = openrouter::catalog()[1].id.as_str();
        let turns = [
            (chosen, EffortLevel::Low, SessionMode::Auto),
            (
                openrouter::fixture::DEFAULT_MODEL,
                EffortLevel::High,
                SessionMode::Auto,
            ),
            (chosen, EffortLevel::Default, SessionMode::Ask),
        ];
        for (index, (model, effort, mode)) in turns.into_iter().enumerate() {
            let turn_start = TurnStart {
                model: model.to_owned(),
                ..turn_with(effort, mode, format!("turn {index}"))
            };
            store.append_turn_start(&id, &turn_start).unwrap();
        }
        let stored = store.read(&id).unwrap().unwrap();
        assert_eq!(
            stored.saved_settings(&defaults),
            SessionSettings::new(chosen, EffortLevel::Default),
            "returning to a model, Default, and Ask is saved rather than inherited"
        );
        let saved_turns: Vec<_> = stored
            .transcript
            .iter()
            .filter_map(|entry| match entry {
                TranscriptEntry::TurnStart(turn_start) => Some((
                    turn_start.model.as_str(),
                    turn_start.effort,
                    turn_start.mode,
                )),
                _ => None,
            })
            .collect();
        assert_eq!(saved_turns, turns);
    }

    #[test]
    fn turn_start_append_adopts_a_session_title_once_and_updates_activity() {
        let store = SessionStore::in_memory();
        let created = store.create(workspace()).unwrap();
        set_updated_at(&store, &created.id, "2026-09-18T09:00:00.000Z");

        let first = store
            .append_turn_start(
                &created.id,
                &TurnStart::test("\n\nFirst line\nsecond line".to_owned()),
            )
            .unwrap();
        assert_eq!(first.session_title.as_deref(), Some("First line"));
        assert_eq!(first.created_at, created.created_at);
        assert!(first.updated_at.as_str() > "2026-09-18T09:00:00.000Z");

        let second = store
            .append_turn_start(&created.id, &TurnStart::test("Something else".to_owned()))
            .unwrap();
        assert_eq!(second.session_title.as_deref(), Some("First line"));
        assert!(second.updated_at >= first.updated_at);

        let long = store.create(workspace()).unwrap();
        let session_title = store
            .append_turn_start(
                &long.id,
                &TurnStart::test("x".repeat(MAX_SESSION_TITLE_CHARS + 10)),
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

        let image_only = store.create(workspace()).unwrap();
        let image_title = store
            .append_turn_start(
                &image_only.id,
                &TurnStart::test(UserMessage {
                    parts: vec![UserMessagePart::Image(ImageAttachment {
                        data: "aGVsbG8=".to_owned(),
                        mime_type: "image/png".to_owned(),
                    })],
                }),
            )
            .unwrap()
            .session_title;
        assert_eq!(image_title.as_deref(), Some("Image"));

        let skill = store.create(workspace()).unwrap();
        let adopted = store
            .append_turn_start(&skill.id, &TurnStart::test(invocation()))
            .unwrap();
        assert_eq!(
            adopted.session_title.as_deref(),
            Some("/goal Pass the tests.")
        );

        assert!(
            store
                .append_turn_start(
                    &SessionId::new("missing"),
                    &TurnStart::test("hello".to_owned()),
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

    fn transcript_entry_count(store: &SessionStore) -> i64 {
        store.with_connection(|connection| {
            connection
                .query_row("SELECT count(*) FROM transcript_entries", [], |row| {
                    row.get(0)
                })
                .unwrap()
        })
    }

    #[test]
    fn child_sessions_are_hidden_and_deleted_only_with_their_main_session() {
        let store = SessionStore::in_memory();
        let main = store.create(workspace()).unwrap().id;
        let child = store.create_child(&main, workspace()).unwrap();
        assert_eq!(child.parent_session_id.as_ref(), Some(&main));
        store
            .append_turn_start(
                &child.id,
                &TurnStart::test("Inspect the parser.".to_owned()),
            )
            .unwrap();
        assert_eq!(ids(&store.list(None).unwrap()), vec![main.to_string()]);
        assert_eq!(
            ids(&store.list(Some(workspace())).unwrap()),
            vec![main.to_string()]
        );
        let read = store.read(&child.id).unwrap().unwrap().summary;
        assert_eq!(read.parent_session_id, Some(main.clone()));
        assert_eq!(read.session_title.as_deref(), Some("Inspect the parser."));

        store.delete(&child.id).unwrap();
        assert!(store.read(&child.id).unwrap().is_some());
        store.delete(&main).unwrap();
        assert!(store.read(&child.id).unwrap().is_none());
        assert_eq!(transcript_entry_count(&store), 0);
        assert!(
            store.create_child(&main, workspace()).is_err(),
            "a child session needs its main session"
        );
    }

    #[test]
    fn children_cost_sums_the_saved_costs_of_every_child_session() {
        let store = SessionStore::in_memory();
        let batch = |cost: Option<f64>| {
            let mut message = message(vec![]);
            message.usage = cost.map(|cost| ModelUsage {
                input_tokens: 10,
                output_tokens: 5,
                cost,
            });
            AssistantBatch::new(message, vec![]).unwrap()
        };
        let session = |parent: Option<&SessionId>, costs: &[Option<f64>]| {
            let id = match parent {
                Some(parent) => store.create_child(parent, workspace()).unwrap().id,
                None => store.create(workspace()).unwrap().id,
            };
            store
                .append_turn_start(&id, &TurnStart::test("work".to_owned()))
                .unwrap();
            for cost in costs {
                store.append_batch(&id, &batch(*cost)).unwrap();
            }
            id
        };
        let main = session(None, &[Some(1.0)]);
        assert_eq!(store.children_cost(&main).unwrap(), None);
        let summarized = session(Some(&main), &[Some(0.25)]);
        store
            .append_checkpoint(
                &summarized,
                2,
                &CompactionCheckpoint {
                    summary: "Worked.".to_owned(),
                    covered_prefix: 2,
                    summarizer_cost: Some(0.125),
                },
            )
            .unwrap();
        session(Some(&main), &[None]);
        session(Some(&main), &[Some(0.5), None]);
        let other = session(None, &[]);
        session(Some(&other), &[Some(8.0)]);
        assert_eq!(store.children_cost(&main).unwrap(), Some(0.875));
    }

    #[test]
    fn agent_messages_are_saved_only_in_main_transcripts() {
        let store = SessionStore::in_memory();
        let main = store.create(workspace()).unwrap().id;
        let child = store.create_child(&main, workspace()).unwrap().id;
        for id in [&main, &child] {
            store
                .append_turn_start(id, &TurnStart::test("work".to_owned()))
                .unwrap();
        }
        let messages = vec![
            AgentMessage {
                subagent_id: child.to_string(),
                content: AgentMessageContent::FinalAnswer("The parser is fixed.".to_owned()),
            },
            AgentMessage {
                subagent_id: child.to_string(),
                content: AgentMessageContent::Failure("The model refused.".to_owned()),
            },
        ];
        store.append_agent_messages(&main, &messages).unwrap();
        assert_eq!(
            store.read(&main).unwrap().unwrap().transcript[1],
            TranscriptEntry::AgentMessages(messages.clone())
        );
        assert_eq!(
            store
                .append_agent_messages(&child, &messages)
                .unwrap_err()
                .kind(),
            ErrorKind::InvalidInput
        );
        assert!(store.append_agent_messages(&main, &[]).is_err());
        let error = read_error(&[user_row(), ("agent_messages", "[]".to_owned())]);
        assert!(error.ends_with("agent messages entry is empty"), "{error}");
    }

    #[test]
    fn deleting_a_session_removes_its_transcript_entries_and_repeats_successfully() {
        let store = SessionStore::in_memory();
        let id = store.create(workspace()).unwrap().id;
        store
            .append_turn_start(&id, &TurnStart::test("hello".to_owned()))
            .unwrap();

        store.delete(&id).unwrap();
        assert!(store.read(&id).unwrap().is_none());
        assert_eq!(transcript_entry_count(&store), 0);

        store.delete(&id).unwrap();
    }
}
