//! A session's state machine and the event history it writes.

use agent_client_protocol::schema::MaybeUndefined;
use agent_client_protocol::v1::schema::{
    ConfigOptionUpdate, ContentBlock, EmbeddedResourceResource, PermissionOption,
    PromptCapabilities, RequestPermissionOutcome, RequestPermissionRequest,
    SelectedPermissionOutcome, SessionConfigOption, SessionUpdate, StopReason,
};
use ox_protocol::{AgentState, ErrorBody, Event, EventKind, SessionState, code};
use tokio::sync::oneshot;

/// Events a subscriber is handed at a time.
const BATCH: usize = 256;

/// What an agent reports when its ACP session opens.
pub struct Agent {
    pub session_id: String,
    pub prompt_capabilities: PromptCapabilities,
    pub config_options: Vec<SessionConfigOption>,
}

impl Agent {
    /// The agent session of a session whose agent is not running: its ID, and
    /// nothing the agent reported.
    fn saved(session_id: String) -> Self {
        Self {
            session_id,
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }
    }
}

/// What a client asks of a session. The session refuses a request its state
/// does not allow.
pub enum Request {
    Prompt(Vec<ContentBlock>),
    Permit {
        request_id: String,
        option_id: String,
    },
}

/// Something that happened to a session. A change that does not fit the
/// session's state, such as an agent's answer that arrives after the session
/// closed, is ignored.
pub enum Change {
    Started(Agent),
    Update(SessionUpdate),
    /// The options an agent answered `session/set_config_option` with. The
    /// ACP library handles a notification sent before the response first, so
    /// options that differ from the session's have not been reported.
    ConfigOptionsSet(Vec<SessionConfigOption>),
    PermissionRequested {
        request: RequestPermissionRequest,
        answer: oneshot::Sender<RequestPermissionOutcome>,
    },
    /// The agent was told to cancel the turn.
    Cancelled,
    /// The agent answered the prompt.
    TurnEnded(Result<StopReason, String>),
    /// The agent said it started or stopped working. Outside a prompt's turn
    /// that starts and ends a turn of the agent's own; within one it says
    /// what the turn already does.
    AgentState(AgentState),
    Exited(Option<String>),
    Closed,
}

/// A permission request the agent is waiting on.
struct Pending {
    request_id: String,
    options: Vec<PermissionOption>,
    answer: oneshot::Sender<RequestPermissionOutcome>,
}

impl Pending {
    /// Answers the agent and returns the event that records the answer.
    fn resolve(self, outcome: RequestPermissionOutcome) -> EventKind {
        let _ = self.answer.send(outcome.clone());
        EventKind::PermissionResolved {
            request_id: self.request_id,
            outcome,
        }
    }
}

/// What started the running turn.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Turn {
    Prompt,
    Agent,
}

/// An agent with an open ACP session.
struct Live {
    agent: Agent,
    turn: Option<Turn>,
    pending: Vec<Pending>,
}

impl Live {
    fn state(&self) -> SessionState {
        if !self.pending.is_empty() {
            SessionState::AwaitingPermission
        } else if self.turn.is_some() {
            SessionState::Running
        } else {
            SessionState::Idle
        }
    }

    fn cancel_pending(&mut self) -> Vec<EventKind> {
        self.pending
            .drain(..)
            .map(|pending| pending.resolve(RequestPermissionOutcome::Cancelled))
            .collect()
    }

    /// Ends what the agent left open: every pending permission request is
    /// cancelled, and a running turn ends with `error`.
    fn end(mut self, error: &str) -> (Agent, Vec<EventKind>) {
        let mut events = self.cancel_pending();
        if self.turn.is_some() {
            events.push(EventKind::TurnEnded {
                stop_reason: None,
                error: Some(error.into()),
            });
        }
        (self.agent, events)
    }
}

enum Phase {
    /// The agent is starting. A loading agent replays the conversation as
    /// updates before it starts. `replay` holds them until the agent
    /// starts, when they are recorded, or is `None` when the session keeps
    /// its history and drops the replay. `previous` is the agent the session
    /// had before it loaded.
    Starting {
        replay: Option<Vec<SessionUpdate>>,
        previous: Option<Agent>,
    },
    Live(Live),
    /// The agent is gone. It holds what the agent reported if it started,
    /// or the saved agent session ID of a session restored from the
    /// database.
    Exited(Option<Agent>),
    Closed,
}

impl Phase {
    fn state(&self) -> SessionState {
        match self {
            Self::Starting { .. } => SessionState::Starting,
            Self::Live(live) => live.state(),
            Self::Exited(_) | Self::Closed => SessionState::Exited,
        }
    }

    /// The phase after `change` and the events that record it.
    /// Permission requests take their IDs from `next_request`, which the
    /// session keeps across agents.
    fn after(self, change: Change, next_request: &mut u64) -> (Self, Vec<EventKind>) {
        match (self, change) {
            (Self::Starting { replay, .. }, Change::Started(agent)) => {
                let mut events: Vec<_> = replay
                    .into_iter()
                    .flatten()
                    .map(EventKind::Update)
                    .collect();
                events.push(EventKind::AgentStarted {
                    agent_session_id: agent.session_id.clone(),
                    prompt_capabilities: agent.prompt_capabilities.clone(),
                    config_options: agent.config_options.clone(),
                });
                let live = Live {
                    agent,
                    turn: None,
                    pending: Vec::new(),
                };
                (Self::Live(live), events)
            }
            // A loading agent replays its conversation before it starts. A
            // session that has a history keeps it and drops the replay.
            (
                Self::Starting {
                    mut replay,
                    previous,
                },
                Change::Update(update),
            ) => {
                if let Some(replay) = &mut replay {
                    replay.push(update);
                }
                (Self::Starting { replay, previous }, Vec::new())
            }
            (Self::Starting { previous, .. }, Change::Exited(error)) => (
                Self::Exited(previous),
                vec![EventKind::AgentExited { error }],
            ),
            (Self::Starting { .. } | Self::Exited(_), Change::Closed) => {
                (Self::Closed, vec![EventKind::SessionClosed])
            }

            (Self::Live(mut live), Change::Update(update)) => {
                if let SessionUpdate::ConfigOptionUpdate(options) = &update {
                    live.agent
                        .config_options
                        .clone_from(&options.config_options);
                }
                (Self::Live(live), vec![EventKind::Update(update)])
            }
            (Self::Live(mut live), Change::ConfigOptionsSet(options)) => {
                let mut events = Vec::new();
                if live.agent.config_options != options {
                    live.agent.config_options.clone_from(&options);
                    let update = ConfigOptionUpdate::new(options);
                    events.push(EventKind::Update(SessionUpdate::ConfigOptionUpdate(update)));
                }
                (Self::Live(live), events)
            }
            (Self::Live(mut live), Change::PermissionRequested { request, answer }) => {
                let request_id = next_request.to_string();
                *next_request += 1;
                live.pending.push(Pending {
                    request_id: request_id.clone(),
                    options: request.options.clone(),
                    answer,
                });
                let requested = EventKind::PermissionRequested {
                    request_id,
                    tool_call: request.tool_call,
                    options: request.options,
                };
                (Self::Live(live), vec![requested])
            }
            (Self::Live(mut live), Change::Cancelled) => {
                let events = live.cancel_pending();
                (Self::Live(live), events)
            }
            (Self::Live(mut live), Change::TurnEnded(result))
                if live.turn == Some(Turn::Prompt) =>
            {
                live.turn = None;
                let (stop_reason, error) = match result {
                    Ok(stop_reason) => (Some(stop_reason), None),
                    Err(error) => (None, Some(error)),
                };
                (
                    Self::Live(live),
                    vec![EventKind::TurnEnded { stop_reason, error }],
                )
            }
            (Self::Live(mut live), Change::AgentState(AgentState::Running))
                if live.turn.is_none() =>
            {
                live.turn = Some(Turn::Agent);
                (Self::Live(live), vec![EventKind::TurnStarted])
            }
            (Self::Live(mut live), Change::AgentState(AgentState::Idle { stop_reason }))
                if live.turn == Some(Turn::Agent) =>
            {
                live.turn = None;
                let ended = EventKind::TurnEnded {
                    stop_reason,
                    error: None,
                };
                (Self::Live(live), vec![ended])
            }
            (Self::Live(live), Change::Exited(error)) => {
                let (agent, mut events) = live.end("the agent exited");
                events.push(EventKind::AgentExited { error });
                (Self::Exited(Some(agent)), events)
            }
            (Self::Live(live), Change::Closed) => {
                let (_, mut events) = live.end("the session was closed");
                events.push(EventKind::SessionClosed);
                (Self::Closed, events)
            }

            (
                phase @ Self::Starting { .. },
                Change::ConfigOptionsSet(_)
                | Change::PermissionRequested { .. }
                | Change::Cancelled
                | Change::TurnEnded(_)
                | Change::AgentState(_),
            )
            | (
                phase @ Self::Live(_),
                Change::Started(_) | Change::TurnEnded(_) | Change::AgentState(_),
            )
            | (
                phase @ Self::Exited(_),
                Change::Started(_)
                | Change::Update(_)
                | Change::ConfigOptionsSet(_)
                | Change::PermissionRequested { .. }
                | Change::Cancelled
                | Change::TurnEnded(_)
                | Change::AgentState(_)
                | Change::Exited(_),
            )
            | (phase @ Self::Closed, _) => (phase, Vec::new()),
        }
    }
}

/// A session's phase and every event it has recorded. `SessionClosed` is the
/// last event of a session, each `PromptStarted` and `TurnStarted` is
/// followed by one `TurnEnded`, and each `PermissionRequested` by one
/// `PermissionResolved`.
pub struct State {
    session_id: String,
    phase: Phase,
    /// The title from the last recorded `session_info_update` that set or
    /// cleared it.
    title: Option<String>,
    events: Vec<Event>,
    /// The ID of the next permission request. Request IDs stay unique for
    /// the session's lifetime, across every agent it loads.
    next_request: u64,
}

impl State {
    pub fn new(session_id: String) -> Self {
        Self {
            session_id,
            phase: Phase::Starting {
                replay: Some(Vec::new()),
                previous: None,
            },
            title: None,
            events: Vec::new(),
            next_request: 1,
        }
    }

    /// A session saved in the database: exited, with no events and no agent.
    pub fn restored(session_id: String, title: Option<String>, agent_session_id: String) -> Self {
        Self {
            session_id,
            phase: Phase::Exited(Some(Agent::saved(agent_session_id))),
            title,
            events: Vec::new(),
            next_request: 1,
        }
    }

    /// Starts a load: the session goes from `exited` to `starting` and
    /// returns the agent session ID to load. A session whose agent has never
    /// started records the agent's replay as its history; one whose agent
    /// has started already has the conversation in its history.
    pub fn load(&mut self) -> Result<String, ErrorBody> {
        let id = match &self.phase {
            Phase::Exited(Some(agent)) => agent.session_id.clone(),
            Phase::Exited(None) => {
                return Err(ErrorBody {
                    code: code::INVALID_STATE,
                    message: "the session never started, so it has nothing to load".into(),
                });
            }
            phase => return Err(invalid_state(phase.state())),
        };
        self.phase = Phase::Starting {
            replay: (!self
                .events
                .iter()
                .any(|event| matches!(event.kind, EventKind::AgentStarted { .. })))
            .then(Vec::new),
            previous: Some(Agent::saved(id.clone())),
        };
        Ok(id)
    }

    pub fn title(&self) -> Option<&str> {
        self.title.as_deref()
    }

    pub fn state(&self) -> SessionState {
        self.phase.state()
    }

    /// What the agent reported when it started, with its current config
    /// options.
    pub fn agent(&self) -> Option<&Agent> {
        match &self.phase {
            Phase::Live(live) => Some(&live.agent),
            Phase::Exited(agent)
            | Phase::Starting {
                previous: agent, ..
            } => agent.as_ref(),
            Phase::Closed => None,
        }
    }

    pub fn last_seq(&self) -> u64 {
        self.events.len() as u64
    }

    /// Checks that a subscriber that has every event through `after_seq`
    /// has more to come, the last of them `SessionClosed`.
    pub fn subscribe(&self, after_seq: u64) -> Result<(), ErrorBody> {
        let last_seq = self.last_seq();
        if after_seq > last_seq {
            return Err(invalid_request(format!(
                "after_seq {after_seq} is past the session's last event, {last_seq}"
            )));
        }
        if after_seq == last_seq && matches!(self.phase, Phase::Closed) {
            return Err(invalid_request(format!(
                "the session is closed and has no event after {after_seq}"
            )));
        }
        Ok(())
    }

    /// The first events with `seq > after_seq`, in order.
    pub fn events_after(&self, after_seq: u64) -> Vec<Event> {
        let start = usize::try_from(after_seq)
            .unwrap_or(usize::MAX)
            .min(self.events.len());
        self.events[start..].iter().take(BATCH).cloned().collect()
    }

    pub fn request(&mut self, request: Request) -> Result<(), ErrorBody> {
        let event = match (&mut self.phase, request) {
            (Phase::Live(live), Request::Prompt(prompt)) => {
                let state = live.state();
                if state != SessionState::Idle {
                    return Err(invalid_state(state));
                }
                check_prompt(&prompt, &live.agent.prompt_capabilities)?;
                live.turn = Some(Turn::Prompt);
                EventKind::PromptStarted { prompt }
            }
            (
                Phase::Live(live),
                Request::Permit {
                    request_id,
                    option_id,
                },
            ) => {
                let Some(index) = live
                    .pending
                    .iter()
                    .position(|pending| pending.request_id == request_id)
                else {
                    return Err(not_pending(&request_id));
                };
                if !live.pending[index]
                    .options
                    .iter()
                    .any(|option| *option.option_id.0 == *option_id)
                {
                    return Err(invalid_request(format!(
                        "{option_id} is not an option of request {request_id}"
                    )));
                }
                live.pending
                    .remove(index)
                    .resolve(RequestPermissionOutcome::Selected(
                        SelectedPermissionOutcome::new(option_id),
                    ))
            }
            (
                phase @ (Phase::Starting { .. } | Phase::Exited(_) | Phase::Closed),
                Request::Prompt(_),
            ) => {
                return Err(invalid_state(phase.state()));
            }
            (
                Phase::Starting { .. } | Phase::Exited(_) | Phase::Closed,
                Request::Permit { request_id, .. },
            ) => return Err(not_pending(&request_id)),
        };
        self.append(event);
        Ok(())
    }

    pub fn record(&mut self, change: Change) {
        let phase = std::mem::replace(&mut self.phase, Phase::Closed);
        let (phase, events) = phase.after(change, &mut self.next_request);
        self.phase = phase;
        for event in events {
            self.append(event);
        }
    }

    fn append(&mut self, kind: EventKind) {
        if let EventKind::Update(SessionUpdate::SessionInfoUpdate(update)) = &kind {
            match &update.title {
                MaybeUndefined::Value(title) => self.title = Some(title.clone()),
                MaybeUndefined::Null => self.title = None,
                MaybeUndefined::Undefined => {}
            }
        }
        self.events.push(Event {
            session_id: self.session_id.clone(),
            seq: self.last_seq() + 1,
            kind,
        });
    }
}

/// Checks a prompt against what the agent accepts. Every agent accepts text
/// and resource links.
fn check_prompt(prompt: &[ContentBlock], accepts: &PromptCapabilities) -> Result<(), ErrorBody> {
    if prompt.is_empty() {
        return Err(invalid_request("the prompt is empty".into()));
    }
    for block in prompt {
        let (kind, accepted) = match block {
            ContentBlock::Text(_) | ContentBlock::ResourceLink(_) => continue,
            ContentBlock::Image(_) => ("image", accepts.image),
            ContentBlock::Audio(_) => ("audio", accepts.audio),
            ContentBlock::Resource(resource) => match resource.resource {
                EmbeddedResourceResource::TextResourceContents(_)
                | EmbeddedResourceResource::BlobResourceContents(_) => {
                    ("embedded resource", accepts.embedded_context)
                }
                _ => ("unknown resource", false),
            },
            _ => ("unknown", false),
        };
        if !accepted {
            return Err(invalid_request(format!(
                "the agent does not accept {kind} content"
            )));
        }
    }
    Ok(())
}

fn invalid_request(message: String) -> ErrorBody {
    ErrorBody {
        code: code::INVALID_REQUEST,
        message,
    }
}

pub fn invalid_state(state: SessionState) -> ErrorBody {
    ErrorBody {
        code: code::INVALID_STATE,
        message: format!("session is {}", state.as_str()),
    }
}

fn not_pending(request_id: &str) -> ErrorBody {
    ErrorBody {
        code: code::INVALID_STATE,
        message: format!("permission request {request_id} is not pending"),
    }
}

#[cfg(test)]
mod tests {
    use agent_client_protocol::v1::schema::{
        ContentChunk, PermissionOptionKind, SessionInfoUpdate, TextContent, ToolCallUpdate,
        ToolCallUpdateFields,
    };

    use super::*;

    fn started() -> State {
        let mut state = State::new("s".into());
        state.record(Change::Started(Agent {
            session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }));
        state
    }

    fn prompt(state: &mut State) -> Result<(), ErrorBody> {
        state.request(Request::Prompt(vec![ContentBlock::Text(TextContent::new(
            "hello",
        ))]))
    }

    /// Records a permission request with the option `allow` and returns the
    /// receiver of its answer.
    fn request_permission(state: &mut State) -> oneshot::Receiver<RequestPermissionOutcome> {
        let (answer, outcome) = oneshot::channel();
        state.record(Change::PermissionRequested {
            request: RequestPermissionRequest::new(
                "agent",
                ToolCallUpdate::new("tool", ToolCallUpdateFields::new()),
                vec![PermissionOption::new(
                    "allow",
                    "Allow",
                    PermissionOptionKind::AllowOnce,
                )],
            ),
            answer,
        });
        outcome
    }

    fn chunk() -> Change {
        Change::Update(SessionUpdate::AgentMessageChunk(ContentChunk::new(
            ContentBlock::Text(TextContent::new("late")),
        )))
    }

    /// The names of the events after the first `skip`.
    fn kinds(state: &State, skip: u64) -> Vec<&'static str> {
        state
            .events_after(skip)
            .iter()
            .map(|event| match event.kind {
                EventKind::AgentStarted { .. } => "agent_started",
                EventKind::PromptStarted { .. } => "prompt_started",
                EventKind::TurnStarted => "turn_started",
                EventKind::Update(_) => "update",
                EventKind::PermissionRequested { .. } => "permission_requested",
                EventKind::PermissionResolved { .. } => "permission_resolved",
                EventKind::TurnEnded { .. } => "turn_ended",
                EventKind::AgentExited { .. } => "agent_exited",
                EventKind::SessionClosed => "session_closed",
            })
            .collect()
    }

    #[test]
    fn state_follows_the_turn_and_its_permission_requests() {
        let mut state = State::new("s".into());
        assert_eq!(state.state(), SessionState::Starting);
        assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);

        let mut state = started();
        assert_eq!(state.state(), SessionState::Idle);
        prompt(&mut state).unwrap();
        assert_eq!(state.state(), SessionState::Running);
        assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);

        let mut outcome = request_permission(&mut state);
        assert_eq!(state.state(), SessionState::AwaitingPermission);
        let permit = |option_id: &str| Request::Permit {
            request_id: "1".into(),
            option_id: option_id.into(),
        };
        let error = state.request(permit("deny")).unwrap_err();
        assert_eq!(error.code, code::INVALID_REQUEST);
        state.request(permit("allow")).unwrap();
        assert_eq!(state.state(), SessionState::Running);
        assert!(matches!(
            outcome.try_recv(),
            Ok(RequestPermissionOutcome::Selected(selected)) if &*selected.option_id.0 == "allow"
        ));
        let error = state.request(permit("allow")).unwrap_err();
        assert_eq!(error.code, code::INVALID_STATE);

        state.record(Change::TurnEnded(Ok(StopReason::EndTurn)));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(
            kinds(&state, 1),
            [
                "prompt_started",
                "permission_requested",
                "permission_resolved",
                "turn_ended"
            ]
        );
    }

    #[test]
    fn exit_and_close_end_the_turn_and_its_permission_requests() {
        for (change, last) in [
            (Change::Exited(None), "agent_exited"),
            (Change::Closed, "session_closed"),
        ] {
            let mut state = started();
            prompt(&mut state).unwrap();
            let mut outcome = request_permission(&mut state);
            state.record(change);
            assert_eq!(state.state(), SessionState::Exited);
            assert_eq!(
                kinds(&state, 3),
                ["permission_resolved", "turn_ended", last]
            );
            assert_eq!(
                outcome.try_recv(),
                Ok(RequestPermissionOutcome::Cancelled),
                "{last}"
            );
        }
    }

    #[test]
    fn the_agent_starts_and_ends_turns_of_its_own() {
        let mut state = started();
        state.record(Change::AgentState(AgentState::Idle { stop_reason: None }));
        assert_eq!(kinds(&state, 1), [] as [&str; 0]);

        state.record(Change::AgentState(AgentState::Running));
        assert_eq!(state.state(), SessionState::Running);
        assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);
        state.record(Change::AgentState(AgentState::Running));
        state.record(Change::TurnEnded(Ok(StopReason::EndTurn)));
        assert_eq!(state.state(), SessionState::Running);

        let mut outcome = request_permission(&mut state);
        assert_eq!(state.state(), SessionState::AwaitingPermission);
        state.record(Change::Cancelled);
        assert_eq!(outcome.try_recv(), Ok(RequestPermissionOutcome::Cancelled));
        assert_eq!(state.state(), SessionState::Running);

        state.record(Change::AgentState(AgentState::Idle {
            stop_reason: Some(StopReason::Cancelled),
        }));
        assert_eq!(state.state(), SessionState::Idle);
        state.record(Change::AgentState(AgentState::Idle { stop_reason: None }));
        assert_eq!(
            kinds(&state, 1),
            [
                "turn_started",
                "permission_requested",
                "permission_resolved",
                "turn_ended"
            ]
        );
        assert_eq!(
            state.events_after(4)[0].kind,
            EventKind::TurnEnded {
                stop_reason: Some(StopReason::Cancelled),
                error: None
            }
        );
        prompt(&mut state).unwrap();
    }

    #[test]
    fn a_prompts_turn_is_the_only_turn_while_it_runs() {
        let mut state = started();
        prompt(&mut state).unwrap();
        // The agent reports the work the prompt asked for, then stops.
        state.record(Change::AgentState(AgentState::Running));
        state.record(Change::AgentState(AgentState::Idle { stop_reason: None }));
        assert_eq!(state.state(), SessionState::Running);
        state.record(Change::TurnEnded(Ok(StopReason::EndTurn)));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(kinds(&state, 1), ["prompt_started", "turn_ended"]);
    }

    #[test]
    fn exit_ends_a_turn_the_agent_started() {
        let mut state = started();
        state.record(Change::AgentState(AgentState::Running));
        state.record(Change::Exited(None));
        assert_eq!(
            kinds(&state, 1),
            ["turn_started", "turn_ended", "agent_exited"]
        );
        assert!(matches!(
            state.events_after(2)[0].kind,
            EventKind::TurnEnded {
                stop_reason: None,
                error: Some(_)
            }
        ));
    }

    #[test]
    fn a_turn_ends_once() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(Change::TurnEnded(Ok(StopReason::EndTurn)));
        state.record(Change::TurnEnded(Err("late".into())));
        assert_eq!(kinds(&state, 1), ["prompt_started", "turn_ended"]);
    }

    #[test]
    fn nothing_follows_session_closed() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(Change::Closed);
        let closed = state.last_seq();
        let (answer, _outcome) = oneshot::channel();
        for change in [
            chunk(),
            Change::ConfigOptionsSet(Vec::new()),
            Change::PermissionRequested {
                request: RequestPermissionRequest::new(
                    "agent",
                    ToolCallUpdate::new("tool", ToolCallUpdateFields::new()),
                    Vec::new(),
                ),
                answer,
            },
            Change::Cancelled,
            Change::TurnEnded(Ok(StopReason::EndTurn)),
            Change::AgentState(AgentState::Running),
            Change::Exited(None),
            Change::Closed,
        ] {
            state.record(change);
        }
        assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);
        assert_eq!(state.last_seq(), closed);
        state.subscribe(closed - 1).unwrap();
        let error = state.subscribe(closed).unwrap_err();
        assert_eq!(error.code, code::INVALID_REQUEST);
    }

    fn replayed(text: &str) -> Change {
        Change::Update(SessionUpdate::UserMessageChunk(ContentChunk::new(
            ContentBlock::Text(TextContent::new(text)),
        )))
    }

    fn restored() -> State {
        State::restored("s".into(), Some("Fix the build".into()), "agent".into())
    }

    #[test]
    fn a_restored_session_is_exited_and_loads_its_replay_as_history() {
        let mut state = restored();
        assert_eq!(state.state(), SessionState::Exited);
        assert_eq!(state.title(), Some("Fix the build"));
        assert_eq!(state.agent().unwrap().session_id, "agent");
        assert_eq!(state.last_seq(), 0);
        assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);

        assert_eq!(state.load().unwrap(), "agent");
        assert_eq!(state.state(), SessionState::Starting);
        state.record(replayed("hello"));
        state.record(Change::Started(Agent {
            session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(kinds(&state, 0), ["update", "agent_started"]);
        assert_eq!(state.title(), Some("Fix the build"));
    }

    #[test]
    fn a_session_with_a_history_loads_without_duplicating_it() {
        let mut state = started();
        state.record(Change::Exited(None));
        assert_eq!(state.load().unwrap(), "agent");
        state.record(replayed("hello"));
        state.record(Change::Started(Agent {
            session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(
            kinds(&state, 0),
            ["agent_started", "agent_exited", "agent_started"]
        );
    }

    #[test]
    fn only_an_exited_session_with_an_agent_session_loads() {
        let mut state = State::new("s".into());
        assert_eq!(state.load().unwrap_err().code, code::INVALID_STATE);
        state.record(Change::Exited(Some("never started".into())));
        assert_eq!(state.state(), SessionState::Exited);
        assert_eq!(state.load().unwrap_err().code, code::INVALID_STATE);

        let mut state = started();
        assert_eq!(state.load().unwrap_err().code, code::INVALID_STATE);
        state.record(Change::Closed);
        assert_eq!(state.load().unwrap_err().code, code::INVALID_STATE);
    }

    #[test]
    fn a_load_that_fails_leaves_the_session_loadable() {
        let mut state = restored();
        state.load().unwrap();
        state.record(Change::Exited(Some("cannot load".into())));
        assert_eq!(state.state(), SessionState::Exited);
        assert_eq!(state.agent().unwrap().session_id, "agent");
        assert_eq!(kinds(&state, 0), ["agent_exited"]);

        // The failed load recorded no conversation, so the next one does.
        assert_eq!(state.load().unwrap(), "agent");
        state.record(replayed("hello"));
        state.record(Change::Started(Agent {
            session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }));
        assert_eq!(kinds(&state, 1), ["update", "agent_started"]);
    }

    #[test]
    fn a_load_that_fails_partway_through_its_replay_records_none_of_it() {
        let mut state = restored();
        state.load().unwrap();
        state.record(replayed("hello"));
        state.record(Change::Exited(Some("load interrupted".into())));
        assert_eq!(kinds(&state, 0), ["agent_exited"]);

        state.load().unwrap();
        state.record(replayed("hello"));
        state.record(Change::Started(Agent {
            session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }));
        assert_eq!(
            kinds(&state, 0),
            ["agent_exited", "update", "agent_started"]
        );
    }

    fn info(update: SessionInfoUpdate) -> Change {
        Change::Update(SessionUpdate::SessionInfoUpdate(update))
    }

    #[test]
    fn session_info_updates_set_clear_and_keep_the_title() {
        // A loading agent replays its title before it starts.
        let mut state = State::new("s".into());
        state.record(info(SessionInfoUpdate::new().title("Replayed")));
        state.record(Change::Started(Agent {
            session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }));
        assert_eq!(state.title(), Some("Replayed"));

        state.record(info(SessionInfoUpdate::new().title("Fix the build")));
        assert_eq!(state.title(), Some("Fix the build"));
        state.record(info(
            SessionInfoUpdate::new().updated_at("2026-11-11T00:00:00Z"),
        ));
        assert_eq!(state.title(), Some("Fix the build"));
        state.record(Change::Exited(None));
        assert_eq!(state.title(), Some("Fix the build"));

        let mut state = started();
        state.record(info(SessionInfoUpdate::new().title("Fix the build")));
        state.record(info(SessionInfoUpdate::new().title(None::<String>)));
        assert_eq!(state.title(), None);
    }
}
