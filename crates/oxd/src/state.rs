//! A session's state machine and the event history it writes.

use std::collections::BTreeMap;

use agent_client_protocol::schema::MaybeUndefined;
use agent_client_protocol::v2::schema::{
    BackgroundWork, CancelledPermissionOutcome, ConfigOptionUpdate, ContentBlock,
    CreateElicitationRequest, ElicitationAction, ElicitationContentValue, ElicitationMode,
    ElicitationPropertySchema, ElicitationSchema, ElicitationScope, EmbeddedResourceResource,
    MultiSelectItems, PermissionOption, PromptCapabilities, RequestPermissionOutcome,
    RequestPermissionRequest, SelectedPermissionOutcome, SessionConfigOption, SessionUpdate,
    StateUpdate, ToolCallId,
};
use ox_protocol::{ErrorBody, Event, EventKind, SessionState, code};
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
    /// An answer to an elicitation.
    Respond {
        request_id: String,
        action: ElicitationAction,
    },
}

/// Something that happened to a session. A change that does not fit the
/// session's state, such as an agent's answer that arrives after the session
/// closed, is ignored.
#[allow(
    clippy::large_enum_variant,
    reason = "a change is built and consumed at once, never stored"
)]
pub enum Change {
    Started(Agent),
    Update(SessionUpdate),
    /// The options an agent answered `session/set_config_option` with. The
    /// connection's dispatch loop waits for each handler to finish before it
    /// reads the next message (the SDK's `concepts::ordering` chapter), so a
    /// `config_option_update` the agent sent before its response is already
    /// recorded, and options that differ from the session's have not been
    /// reported.
    ConfigOptionsSet(Vec<SessionConfigOption>),
    PermissionRequested {
        request: RequestPermissionRequest,
        answer: oneshot::Sender<RequestPermissionOutcome>,
    },
    /// A form elicitation that `check_form` accepted.
    ElicitationRequested {
        message: String,
        tool_call_id: Option<ToolCallId>,
        schema: ElicitationSchema,
        answer: oneshot::Sender<ElicitationAction>,
    },
    /// The agent withdrew an elicitation with `$/cancel_request`. Its handler
    /// closed the answer's receiver, so every pending elicitation whose
    /// receiver is closed is the one withdrawn.
    ElicitationWithdrawn,
    /// The agent was told to cancel the turn. Pending permission requests
    /// are cancelled; elicitations stay open until the agent withdraws them.
    Cancelled,
    /// The agent answered `session/prompt` with an error: it did not insert
    /// the prompt, which is the one its `PromptStarted` event at seq `prompt`
    /// records. A successful answer changes nothing, since the turn runs and
    /// ends on the agent's `state_update`s.
    PromptRejected {
        prompt: u64,
        error: String,
    },
    Exited(Option<String>),
    /// The daemon stopped the agent of a session that was idle, with no
    /// background work, and unviewed, or that it archived.
    Parked,
    Closed,
}

/// A request the agent is waiting on a client to answer.
struct Pending {
    request_id: String,
    kind: PendingKind,
}

enum PendingKind {
    Permission {
        options: Vec<PermissionOption>,
        answer: oneshot::Sender<RequestPermissionOutcome>,
    },
    Elicitation {
        schema: ElicitationSchema,
        answer: oneshot::Sender<ElicitationAction>,
    },
}

impl Pending {
    fn is_permission(&self) -> bool {
        matches!(self.kind, PendingKind::Permission { .. })
    }

    /// Answers the agent that the request is cancelled and returns the
    /// event that records it.
    fn cancel(self) -> EventKind {
        match self.kind {
            PendingKind::Permission { answer, .. } => resolve_permission(
                self.request_id,
                answer,
                CancelledPermissionOutcome::new().into(),
            ),
            PendingKind::Elicitation { answer, .. } => {
                resolve_elicitation(self.request_id, answer, ElicitationAction::Cancel)
            }
        }
    }
}

/// Answers a permission request and returns the event that records it.
fn resolve_permission(
    request_id: String,
    answer: oneshot::Sender<RequestPermissionOutcome>,
    outcome: RequestPermissionOutcome,
) -> EventKind {
    let _ = answer.send(outcome.clone());
    EventKind::PermissionResolved {
        request_id,
        outcome,
    }
}

/// Answers an elicitation and returns the event that records it.
fn resolve_elicitation(
    request_id: String,
    answer: oneshot::Sender<ElicitationAction>,
    action: ElicitationAction,
) -> EventKind {
    let _ = answer.send(action.clone());
    EventKind::ElicitationResolved { request_id, action }
}

/// The running turn. A turn starts when a prompt is sent or when the agent
/// reports work, and ends when the agent reports `idle` after reporting work.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Turn {
    /// The prompt whose `PromptStarted` event has this seq was sent, and
    /// the agent has not reported work for it. An `idle` here repeats a
    /// snapshot from before the prompt.
    Prompted(u64),
    /// The agent reported work. `recorded` is false for work a resumed agent
    /// was already doing, whose start the session's history does not have.
    Working { recorded: bool },
}

/// An agent with an open ACP session.
struct Live {
    agent: Agent,
    turn: Option<Turn>,
    pending: Vec<Pending>,
    /// The background work the agent last reported. `None` when it does not
    /// report any.
    background: Option<Vec<BackgroundWork>>,
}

impl Live {
    /// No turn, no pending request, and the agent reports no background
    /// work.
    fn quiet(&self) -> bool {
        self.turn.is_none() && self.pending.is_empty() && self.background.as_deref() == Some(&[])
    }

    fn state(&self) -> SessionState {
        if self.pending.iter().any(Pending::is_permission) {
            SessionState::AwaitingPermission
        } else if !self.pending.is_empty() {
            SessionState::AwaitingInput
        } else if self.turn.is_some() {
            SessionState::Running
        } else {
            SessionState::Idle
        }
    }

    /// Applies a `state_update` and returns the events of the turn it starts
    /// or ends. Any state but `idle`, including one this daemon does not
    /// know, is work in progress.
    fn follow(&mut self, state: &StateUpdate) -> Vec<EventKind> {
        match (state, self.turn) {
            (StateUpdate::Idle(idle), Some(Turn::Working { recorded })) => {
                self.turn = None;
                if recorded {
                    vec![EventKind::TurnEnded {
                        stop_reason: idle.stop_reason.clone(),
                        error: None,
                    }]
                } else {
                    Vec::new()
                }
            }
            (StateUpdate::Idle(_), _) | (_, Some(Turn::Working { .. })) => Vec::new(),
            (_, None) => {
                self.turn = Some(Turn::Working { recorded: true });
                vec![EventKind::TurnStarted]
            }
            (_, Some(Turn::Prompted(_))) => {
                self.turn = Some(Turn::Working { recorded: true });
                Vec::new()
            }
        }
    }

    /// Cancels the pending requests that `which` selects.
    fn cancel_pending(&mut self, which: impl Fn(&Pending) -> bool) -> Vec<EventKind> {
        self.pending
            .extract_if(.., |pending| which(pending))
            .map(Pending::cancel)
            .collect()
    }

    /// Ends what the agent left open: every pending request is cancelled,
    /// and a running turn ends with `error`.
    fn end(mut self, error: &str) -> (Agent, Vec<EventKind>) {
        let mut events = self.cancel_pending(|_| true);
        if let Some(Turn::Prompted(_) | Turn::Working { recorded: true }) = self.turn {
            events.push(EventKind::TurnEnded {
                stop_reason: None,
                error: Some(error.into()),
            });
        }
        (self.agent, events)
    }
}

enum Phase {
    /// The agent is starting. A resuming agent replays the conversation as
    /// updates, then reports its state, before it starts. `replay` holds
    /// them until the agent starts, when they are recorded, or is `None`
    /// when the session keeps its history and drops the replay. `busy` is
    /// whether the last state reported was not `idle`, and `background` the
    /// work it listed. `previous` is the
    /// agent the session had before it resumed.
    Starting {
        replay: Option<Vec<SessionUpdate>>,
        busy: bool,
        background: Option<Vec<BackgroundWork>>,
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
            (
                Self::Starting {
                    replay,
                    busy,
                    background,
                    ..
                },
                Change::Started(agent),
            ) => {
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
                // A resumed agent that is working has a turn the session did
                // not see start.
                let live = Live {
                    agent,
                    turn: busy.then_some(Turn::Working { recorded: false }),
                    pending: Vec::new(),
                    background,
                };
                (Self::Live(live), events)
            }
            // A resuming agent replays its conversation and reports its state
            // before it starts. A session that has a history keeps it and
            // drops the replay.
            (
                Self::Starting {
                    mut replay,
                    mut busy,
                    mut background,
                    previous,
                },
                Change::Update(update),
            ) => {
                if let SessionUpdate::StateUpdate(state) = &update {
                    busy = !matches!(state, StateUpdate::Idle(_));
                    background = state.background().map(<[_]>::to_vec);
                }
                if let Some(replay) = &mut replay {
                    replay.push(update);
                }
                (
                    Self::Starting {
                        replay,
                        busy,
                        background,
                        previous,
                    },
                    Vec::new(),
                )
            }
            (Self::Starting { previous, .. }, Change::Exited(error)) => (
                Self::Exited(previous),
                vec![EventKind::AgentExited { error }],
            ),
            (Self::Starting { .. } | Self::Exited(_), Change::Closed) => {
                (Self::Closed, vec![EventKind::SessionClosed])
            }

            (Self::Live(mut live), Change::Update(update)) => {
                let mut turn = Vec::new();
                match &update {
                    SessionUpdate::ConfigOptionUpdate(options) => {
                        live.agent
                            .config_options
                            .clone_from(&options.config_options);
                    }
                    SessionUpdate::StateUpdate(state) => {
                        live.background = state.background().map(<[_]>::to_vec);
                        turn = live.follow(state);
                    }
                    _ => {}
                }
                let mut events = vec![EventKind::Update(update)];
                events.extend(turn);
                (Self::Live(live), events)
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
                    kind: PendingKind::Permission {
                        options: request.options.clone(),
                        answer,
                    },
                });
                let requested = EventKind::PermissionRequested {
                    request_id,
                    title: request.title,
                    description: request.description,
                    subject: request.subject,
                    options: request.options,
                };
                (Self::Live(live), vec![requested])
            }
            (
                Self::Live(mut live),
                Change::ElicitationRequested {
                    message,
                    tool_call_id,
                    schema,
                    answer,
                },
            ) => {
                let request_id = next_request.to_string();
                *next_request += 1;
                live.pending.push(Pending {
                    request_id: request_id.clone(),
                    kind: PendingKind::Elicitation {
                        schema: schema.clone(),
                        answer,
                    },
                });
                let requested = EventKind::ElicitationRequested {
                    request_id,
                    message,
                    tool_call_id,
                    requested_schema: schema,
                };
                (Self::Live(live), vec![requested])
            }
            (Self::Live(mut live), Change::ElicitationWithdrawn) => {
                let events = live.cancel_pending(|pending| {
                    matches!(&pending.kind, PendingKind::Elicitation { answer, .. } if answer.is_closed())
                });
                (Self::Live(live), events)
            }
            (Self::Live(mut live), Change::Cancelled) => {
                let events = live.cancel_pending(Pending::is_permission);
                (Self::Live(live), events)
            }
            // The agent rejected the prompt, so no turn began. After the
            // agent reported work for it, the turn ends at the agent's `idle`.
            (Self::Live(mut live), Change::PromptRejected { prompt, error })
                if live.turn == Some(Turn::Prompted(prompt)) =>
            {
                live.turn = None;
                let ended = EventKind::TurnEnded {
                    stop_reason: None,
                    error: Some(error),
                };
                (Self::Live(live), vec![ended])
            }
            (Self::Live(live), Change::Exited(error)) => {
                let (agent, mut events) = live.end("the agent exited");
                events.push(EventKind::AgentExited { error });
                (Self::Exited(Some(agent)), events)
            }
            // The daemon parks a quiet session, and archives any session. One
            // that stopped being quiet before the agent was stopped, or that
            // is archived while it works, ends what the agent left open.
            (Self::Live(live), Change::Parked) => {
                let (agent, mut events) = live.end("the agent was stopped");
                events.push(EventKind::AgentParked);
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
                | Change::ElicitationRequested { .. }
                | Change::ElicitationWithdrawn
                | Change::Cancelled
                | Change::PromptRejected { .. },
            )
            | (phase @ Self::Live(_), Change::Started(_) | Change::PromptRejected { .. })
            | (
                phase @ Self::Exited(_),
                Change::Started(_)
                | Change::Update(_)
                | Change::ConfigOptionsSet(_)
                | Change::PermissionRequested { .. }
                | Change::ElicitationRequested { .. }
                | Change::ElicitationWithdrawn
                | Change::Cancelled
                | Change::PromptRejected { .. }
                | Change::Exited(_)
                | Change::Parked,
            )
            | (phase @ Self::Starting { .. }, Change::Parked)
            | (phase @ Self::Closed, _) => (phase, Vec::new()),
        }
    }
}

/// A session's phase and every event it has recorded. `SessionClosed` ends a
/// session's subscriptions and is not stored. Each `PromptStarted` and `TurnStarted` is
/// followed by one `TurnEnded`, each `PermissionRequested` by one
/// `PermissionResolved`, and each `ElicitationRequested` by one
/// `ElicitationResolved`.
///
/// A session restored from the database knows its last seq but holds no
/// events until `fill` gives it the stored history, and records none before.
pub struct State {
    session_id: String,
    phase: Phase,
    /// The title from the last recorded `session_info_update` that set or
    /// cleared it.
    title: Option<String>,
    events: Option<Vec<Event>>,
    last_seq: u64,
    /// Events recorded since `take_unlogged`, which the database has not been
    /// sent. `SessionClosed` is not stored.
    unlogged: Vec<Event>,
    /// The ID of the next permission request or elicitation. Request IDs
    /// stay unique for the session's lifetime, across every agent it loads.
    next_request: u64,
}

impl State {
    pub fn new(session_id: String) -> Self {
        Self {
            session_id,
            phase: Phase::Starting {
                replay: Some(Vec::new()),
                busy: false,
                background: None,
                previous: None,
            },
            title: None,
            events: Some(Vec::new()),
            last_seq: 0,
            unlogged: Vec::new(),
            next_request: 1,
        }
    }

    /// A session saved in the database: exited, with no agent and its
    /// history, which ends at `last_seq`, not yet read.
    pub fn restored(
        session_id: String,
        title: Option<String>,
        agent_session_id: String,
        last_seq: u64,
    ) -> Self {
        Self {
            session_id,
            phase: Phase::Exited(Some(Agent::saved(agent_session_id))),
            title,
            events: None,
            last_seq,
            unlogged: Vec::new(),
            next_request: 1,
        }
    }

    /// A saved session whose agent never started: exited, with no events and
    /// nothing to load.
    pub fn unstarted(session_id: String, title: Option<String>) -> Self {
        Self {
            session_id,
            phase: Phase::Exited(None),
            title,
            events: Some(Vec::new()),
            last_seq: 0,
            unlogged: Vec::new(),
            next_request: 1,
        }
    }

    /// Whether the session holds its history.
    pub fn loaded(&self) -> bool {
        self.events.is_some()
    }

    /// Gives a restored session its stored history. What a crash left open is
    /// closed: a running turn ends with an error, and a pending permission
    /// request or elicitation is cancelled. Request IDs continue after the
    /// history's.
    pub fn fill(&mut self, history: Vec<Event>) {
        let mut turn_open = false;
        // Each open request's ID, and whether it is a permission request.
        let mut pending: Vec<(&str, bool)> = Vec::new();
        let mut last_request = 0;
        for event in &history {
            match &event.kind {
                EventKind::PromptStarted { .. } | EventKind::TurnStarted => turn_open = true,
                EventKind::TurnEnded { .. } => turn_open = false,
                EventKind::PermissionRequested { request_id, .. }
                | EventKind::ElicitationRequested { request_id, .. } => {
                    let permission = matches!(event.kind, EventKind::PermissionRequested { .. });
                    pending.push((request_id, permission));
                    last_request = last_request.max(request_id.parse().unwrap_or(0));
                }
                EventKind::PermissionResolved { request_id, .. }
                | EventKind::ElicitationResolved { request_id, .. } => {
                    pending.retain(|(id, _)| id != request_id);
                }
                _ => {}
            }
        }
        let cancelled: Vec<_> = pending
            .into_iter()
            .map(|(request_id, permission)| {
                let request_id = request_id.into();
                if permission {
                    EventKind::PermissionResolved {
                        request_id,
                        outcome: CancelledPermissionOutcome::new().into(),
                    }
                } else {
                    EventKind::ElicitationResolved {
                        request_id,
                        action: ElicitationAction::Cancel,
                    }
                }
            })
            .collect();
        self.next_request = last_request + 1;
        self.events = Some(history);
        for event in cancelled {
            self.append(event);
        }
        if turn_open {
            self.append(EventKind::TurnEnded {
                stop_reason: None,
                error: Some("oxd stopped during the turn".into()),
            });
        }
    }

    /// The events recorded since the last call, in order, for the database.
    pub fn take_unlogged(&mut self) -> Vec<Event> {
        std::mem::take(&mut self.unlogged)
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
            busy: false,
            background: None,
            replay: (!self
                .events
                .iter()
                .flatten()
                .any(|event| matches!(event.kind, EventKind::AgentStarted { .. })))
            .then(Vec::new),
            previous: Some(Agent::saved(id.clone())),
        };
        Ok(id)
    }

    /// Whether the agent is running with nothing to do: no turn, no pending
    /// request, and no background work.
    pub fn quiet(&self) -> bool {
        matches!(&self.phase, Phase::Live(live) if live.quiet())
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
        self.last_seq
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

    /// The first events with `seq > after_seq`, in order. A session that has
    /// not loaded its history has none to give.
    pub fn events_after(&self, after_seq: u64) -> Vec<Event> {
        let events = self.events.as_deref().unwrap_or_default();
        let start = usize::try_from(after_seq)
            .unwrap_or(usize::MAX)
            .min(events.len());
        events[start..].iter().take(BATCH).cloned().collect()
    }

    pub fn request(&mut self, request: Request) -> Result<(), ErrorBody> {
        let seq = self.last_seq() + 1;
        let event = match (&mut self.phase, request) {
            (Phase::Live(live), Request::Prompt(prompt)) => {
                let state = live.state();
                if state != SessionState::Idle {
                    return Err(invalid_state(state));
                }
                check_prompt(&prompt, &live.agent.prompt_capabilities)?;
                live.turn = Some(Turn::Prompted(seq));
                EventKind::PromptStarted { prompt }
            }
            (
                Phase::Live(live),
                Request::Permit {
                    request_id,
                    option_id,
                },
            ) => {
                let Some(index) = live.pending.iter().position(|pending| {
                    pending.request_id == request_id && pending.is_permission()
                }) else {
                    return Err(not_pending(&request_id));
                };
                let PendingKind::Permission { options, .. } = &live.pending[index].kind else {
                    unreachable!("the request found is a permission request");
                };
                if !options
                    .iter()
                    .any(|option| *option.option_id.0 == *option_id)
                {
                    return Err(invalid_request(format!(
                        "{option_id} is not an option of request {request_id}"
                    )));
                }
                let PendingKind::Permission { answer, .. } = live.pending.remove(index).kind else {
                    unreachable!("the request found is a permission request");
                };
                resolve_permission(
                    request_id,
                    answer,
                    RequestPermissionOutcome::Selected(SelectedPermissionOutcome::new(option_id)),
                )
            }
            (Phase::Live(live), Request::Respond { request_id, action }) => {
                // An elicitation the agent is withdrawing has a closed
                // receiver, and its handler records its end.
                let Some(index) = live.pending.iter().position(|pending| {
                    pending.request_id == request_id
                        && matches!(&pending.kind, PendingKind::Elicitation { answer, .. } if !answer.is_closed())
                }) else {
                    return Err(not_pending(&request_id));
                };
                let PendingKind::Elicitation { schema, .. } = &live.pending[index].kind else {
                    unreachable!("the request found is an elicitation");
                };
                match &action {
                    ElicitationAction::Accept(accepted) => {
                        let empty = BTreeMap::new();
                        check_content(schema, accepted.content.as_ref().unwrap_or(&empty))
                            .map_err(invalid_request)?;
                    }
                    ElicitationAction::Decline | ElicitationAction::Cancel => {}
                    _ => return Err(invalid_request("unknown elicitation action".into())),
                }
                let PendingKind::Elicitation { answer, .. } = live.pending.remove(index).kind
                else {
                    unreachable!("the request found is an elicitation");
                };
                resolve_elicitation(request_id, answer, action)
            }
            (
                phase @ (Phase::Starting { .. } | Phase::Exited(_) | Phase::Closed),
                Request::Prompt(_),
            ) => {
                return Err(invalid_state(phase.state()));
            }
            (
                Phase::Starting { .. } | Phase::Exited(_) | Phase::Closed,
                Request::Permit { request_id, .. } | Request::Respond { request_id, .. },
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
        // A session that has not loaded its history records nothing.
        let Some(events) = &mut self.events else {
            return;
        };
        self.last_seq += 1;
        let event = Event {
            session_id: self.session_id.clone(),
            seq: self.last_seq,
            kind,
        };
        if !matches!(event.kind, EventKind::SessionClosed) {
            self.unlogged.push(event.clone());
        }
        events.push(event);
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
            ContentBlock::Image(_) => ("image", accepts.image.is_some()),
            ContentBlock::Audio(_) => ("audio", accepts.audio.is_some()),
            ContentBlock::Resource(resource) => match resource.resource {
                EmbeddedResourceResource::TextResourceContents(_)
                | EmbeddedResourceResource::BlobResourceContents(_) => {
                    ("embedded resource", accepts.embedded_context.is_some())
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

/// Checks a form elicitation that oxd can show to clients, and returns its
/// message, tool call, and schema. Other modes, a request scope, and forms a
/// client cannot render are refused with `-32602`: a required property of
/// unknown type or with no schema, and a property with both `enum` and
/// `oneOf`.
pub fn check_form(
    request: CreateElicitationRequest,
) -> Result<(String, Option<ToolCallId>, ElicitationSchema), agent_client_protocol::Error> {
    let refuse = |reason: String| agent_client_protocol::Error::invalid_params().data(reason);
    let ElicitationMode::Form(form) = request.mode else {
        return Err(refuse("oxd supports form elicitation only".into()));
    };
    let ElicitationScope::Session(scope) = form.scope else {
        return Err(refuse(
            "oxd supports session-scoped elicitation only".into(),
        ));
    };
    let schema = form.requested_schema;
    for (name, property) in &schema.properties {
        if let ElicitationPropertySchema::String(string) = property
            && string.enum_values.is_some()
            && string.one_of.is_some()
        {
            return Err(refuse(format!("property {name} has both enum and oneOf")));
        }
    }
    for name in schema.required.iter().flatten() {
        match schema.properties.get(name) {
            None => return Err(refuse(format!("required property {name} has no schema"))),
            Some(ElicitationPropertySchema::Other(other)) => {
                return Err(refuse(format!(
                    "required property {name} has the unknown type {}",
                    other.type_
                )));
            }
            Some(_) => {}
        }
    }
    Ok((request.message, scope.tool_call_id, schema))
}

/// Checks accepted content against an elicitation's schema: every key names
/// a property, each value fits its property, and every required property is
/// present. A property of unknown type takes any value. `pattern` and
/// `format` are not checked.
fn check_content(
    schema: &ElicitationSchema,
    content: &BTreeMap<String, ElicitationContentValue>,
) -> Result<(), String> {
    for (name, value) in content {
        let Some(property) = schema.properties.get(name) else {
            return Err(format!("{name} is not a field of the form"));
        };
        check_value(name, property, value)?;
    }
    for name in schema.required.iter().flatten() {
        if !content.contains_key(name) {
            return Err(format!("{name} is required"));
        }
    }
    Ok(())
}

/// Checks one value of accepted content against its property.
#[allow(
    clippy::cast_precision_loss,
    reason = "a number field's limits are floats, so an integer answer is compared as one"
)]
fn check_value(
    name: &str,
    property: &ElicitationPropertySchema,
    value: &ElicitationContentValue,
) -> Result<(), String> {
    match (property, value) {
        (ElicitationPropertySchema::String(string), ElicitationContentValue::String(text)) => {
            let choices = string
                .enum_values
                .as_ref()
                .map(|values| values.iter().map(String::as_str).collect::<Vec<_>>())
                .or_else(|| {
                    string
                        .one_of
                        .as_ref()
                        .map(|options| options.iter().map(|o| o.value.as_str()).collect())
                });
            if let Some(choices) = choices
                && !choices.contains(&text.as_str())
            {
                return Err(format!("{text} is not a choice of {name}"));
            }
            let length = text.chars().count();
            if let Some(min) = string.min_length
                && length < min as usize
            {
                return Err(format!("{name} needs at least {min} characters"));
            }
            if let Some(max) = string.max_length
                && length > max as usize
            {
                return Err(format!("{name} takes at most {max} characters"));
            }
            Ok(())
        }
        (
            ElicitationPropertySchema::Number(number),
            ElicitationContentValue::Integer(_) | ElicitationContentValue::Number(_),
        ) => {
            let value = match value {
                ElicitationContentValue::Integer(integer) => *integer as f64,
                ElicitationContentValue::Number(float) => *float,
                _ => unreachable!("the value is a number"),
            };
            check_range(name, value, number.minimum, number.maximum)
        }
        (ElicitationPropertySchema::Integer(integer), ElicitationContentValue::Integer(value)) => {
            check_range(name, *value, integer.minimum, integer.maximum)
        }
        (ElicitationPropertySchema::Boolean(_), ElicitationContentValue::Boolean(_))
        | (ElicitationPropertySchema::Other(_), _) => Ok(()),
        (ElicitationPropertySchema::Array(array), ElicitationContentValue::StringArray(items)) => {
            let choices: Option<Vec<&str>> = match &array.items {
                MultiSelectItems::String(items) => {
                    Some(items.values.iter().map(String::as_str).collect())
                }
                MultiSelectItems::Titled(items) => {
                    Some(items.options.iter().map(|o| o.value.as_str()).collect())
                }
                _ => None,
            };
            if let Some(choices) = choices
                && let Some(item) = items.iter().find(|item| !choices.contains(&item.as_str()))
            {
                return Err(format!("{item} is not a choice of {name}"));
            }
            let count = items.len() as u64;
            if let Some(min) = array.min_items
                && count < min
            {
                return Err(format!("{name} needs at least {min} choices"));
            }
            if let Some(max) = array.max_items
                && count > max
            {
                return Err(format!("{name} takes at most {max} choices"));
            }
            Ok(())
        }
        (property, _) => Err(format!("{name} takes {}", kind_name(property))),
    }
}

fn check_range<T: Copy + PartialOrd + std::fmt::Display>(
    name: &str,
    value: T,
    minimum: Option<T>,
    maximum: Option<T>,
) -> Result<(), String> {
    if let Some(minimum) = minimum
        && value < minimum
    {
        return Err(format!("{name} is at least {minimum}"));
    }
    if let Some(maximum) = maximum
        && value > maximum
    {
        return Err(format!("{name} is at most {maximum}"));
    }
    Ok(())
}

/// What a property's values are, for an error.
fn kind_name(property: &ElicitationPropertySchema) -> &'static str {
    match property {
        ElicitationPropertySchema::String(_) => "text",
        ElicitationPropertySchema::Number(_) => "a number",
        ElicitationPropertySchema::Integer(_) => "an integer",
        ElicitationPropertySchema::Boolean(_) => "true or false",
        ElicitationPropertySchema::Array(_) => "a list of choices",
        _ => "another kind of value",
    }
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
        message: format!("request {request_id} is not pending"),
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use agent_client_protocol::v2::schema::{
        BackgroundToolCall, ContentChunk, ElicitationAcceptAction, ElicitationFormMode,
        ElicitationRequestScope, ElicitationSessionScope, ElicitationUrlMode, EnumOption,
        IdleStateUpdate, IntegerPropertySchema, MultiSelectPropertySchema,
        OtherElicitationPropertySchema, OtherStateUpdate, PermissionOptionKind,
        RequiresActionStateUpdate, RunningStateUpdate, SessionInfoUpdate, StopReason,
        StringPropertySchema, TextContent,
    };
    use serde_json::json;

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
                "Tool",
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

    fn state_change(state: StateUpdate) -> Change {
        Change::Update(SessionUpdate::StateUpdate(state))
    }

    fn running() -> Change {
        state_change(StateUpdate::Running(RunningStateUpdate::new()))
    }

    fn idle(stop_reason: Option<StopReason>) -> Change {
        state_change(StateUpdate::Idle(
            IdleStateUpdate::new().stop_reason(stop_reason),
        ))
    }

    fn chunk() -> Change {
        Change::Update(SessionUpdate::AgentMessageChunk(ContentChunk::new(
            ContentBlock::Text(TextContent::new("late")),
            "message",
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
                EventKind::ElicitationRequested { .. } => "elicitation_requested",
                EventKind::ElicitationResolved { .. } => "elicitation_resolved",
                EventKind::TurnEnded { .. } => "turn_ended",
                EventKind::AgentExited { .. } => "agent_exited",
                EventKind::AgentParked => "agent_parked",
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
        state.record(running());

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

        state.record(idle(Some(StopReason::EndTurn)));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(
            kinds(&state, 1),
            [
                "prompt_started",
                "update",
                "permission_requested",
                "permission_resolved",
                "update",
                "turn_ended"
            ]
        );
        assert_eq!(
            state.events_after(6)[0].kind,
            EventKind::TurnEnded {
                stop_reason: Some(StopReason::EndTurn),
                error: None
            }
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
                Ok(CancelledPermissionOutcome::new().into()),
                "{last}"
            );
        }
    }

    #[test]
    fn the_agent_starts_and_ends_turns_of_its_own() {
        let mut state = started();
        state.record(idle(None));
        assert_eq!(kinds(&state, 1), ["update"]);
        assert_eq!(state.state(), SessionState::Idle);

        state.record(running());
        assert_eq!(state.state(), SessionState::Running);
        assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);
        state.record(running());

        let mut outcome = request_permission(&mut state);
        assert_eq!(state.state(), SessionState::AwaitingPermission);
        state.record(Change::Cancelled);
        assert_eq!(
            outcome.try_recv(),
            Ok(CancelledPermissionOutcome::new().into())
        );
        assert_eq!(state.state(), SessionState::Running);

        state.record(idle(Some(StopReason::Cancelled)));
        assert_eq!(state.state(), SessionState::Idle);
        state.record(idle(None));
        assert_eq!(
            kinds(&state, 1),
            [
                "update",
                "update",
                "turn_started",
                "update",
                "permission_requested",
                "permission_resolved",
                "update",
                "turn_ended",
                "update"
            ]
        );
        assert_eq!(
            state.events_after(8)[0].kind,
            EventKind::TurnEnded {
                stop_reason: Some(StopReason::Cancelled),
                error: None
            }
        );
        prompt(&mut state).unwrap();
    }

    #[test]
    fn a_repeated_idle_snapshot_after_a_prompt_leaves_the_turn_running() {
        let mut state = started();
        prompt(&mut state).unwrap();
        // The agent repeats the snapshot it sent before the prompt, then
        // reports the work the prompt asked for, then stops.
        state.record(idle(None));
        assert_eq!(state.state(), SessionState::Running);
        state.record(running());
        state.record(idle(Some(StopReason::EndTurn)));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(
            kinds(&state, 1),
            ["prompt_started", "update", "update", "update", "turn_ended"]
        );
    }

    /// The agent's rejection of the last prompt sent.
    fn rejected(state: &State, error: &str) -> Change {
        let prompt = state
            .events_after(0)
            .iter()
            .rev()
            .find(|event| matches!(event.kind, EventKind::PromptStarted { .. }))
            .map_or(0, |event| event.seq);
        Change::PromptRejected {
            prompt,
            error: error.into(),
        }
    }

    #[test]
    fn a_rejected_prompt_ends_the_turn_with_its_error() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(rejected(&state, "not inserted"));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(kinds(&state, 1), ["prompt_started", "turn_ended"]);
        assert_eq!(
            state.events_after(2)[0].kind,
            EventKind::TurnEnded {
                stop_reason: None,
                error: Some("not inserted".into())
            }
        );
        // The agent may report the state it was already in.
        state.record(idle(None));
        assert_eq!(kinds(&state, 3), ["update"]);
        prompt(&mut state).unwrap();
    }

    #[test]
    fn a_rejected_prompt_after_the_agent_reported_work_waits_for_idle() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(running());
        state.record(rejected(&state, "late"));
        assert_eq!(state.state(), SessionState::Running);
        state.record(idle(Some(StopReason::Cancelled)));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(
            kinds(&state, 1),
            ["prompt_started", "update", "update", "turn_ended"]
        );
        assert_eq!(
            state.events_after(4)[0].kind,
            EventKind::TurnEnded {
                stop_reason: Some(StopReason::Cancelled),
                error: None
            }
        );
    }

    #[test]
    fn an_answered_prompt_that_was_rejected_after_its_turn_ended_changes_nothing() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(running());
        state.record(idle(Some(StopReason::EndTurn)));
        state.record(rejected(&state, "late"));
        assert_eq!(
            kinds(&state, 1),
            ["prompt_started", "update", "update", "turn_ended"]
        );
    }

    #[test]
    fn a_late_rejection_leaves_the_next_prompt_running() {
        let mut state = started();
        prompt(&mut state).unwrap();
        let first = rejected(&state, "late");
        state.record(running());
        state.record(idle(Some(StopReason::EndTurn)));
        prompt(&mut state).unwrap();
        state.record(first);
        assert_eq!(state.state(), SessionState::Running);
        state.record(running());
        assert_eq!(
            kinds(&state, 1),
            [
                "prompt_started",
                "update",
                "update",
                "turn_ended",
                "prompt_started",
                "update"
            ]
        );
    }

    #[test]
    fn states_other_than_idle_are_work() {
        let unknown = || {
            state_change(StateUpdate::Other(OtherStateUpdate::new(
                "_waiting",
                BTreeMap::new(),
            )))
        };
        let requires_action =
            || state_change(StateUpdate::RequiresAction(RequiresActionStateUpdate::new()));
        for change in [unknown(), requires_action()] {
            let mut state = started();
            state.record(change);
            assert_eq!(state.state(), SessionState::Running);
            assert_eq!(kinds(&state, 1), ["update", "turn_started"]);
            assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);
            // An unknown state is never taken for idle.
            state.record(running());
            state.record(idle(None));
            assert_eq!(state.state(), SessionState::Idle);
        }
    }

    #[test]
    fn exit_ends_a_turn_the_agent_started() {
        let mut state = started();
        state.record(running());
        state.record(Change::Exited(None));
        assert_eq!(
            kinds(&state, 1),
            ["update", "turn_started", "turn_ended", "agent_exited"]
        );
        assert!(matches!(
            state.events_after(3)[0].kind,
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
        state.record(running());
        state.record(idle(Some(StopReason::EndTurn)));
        state.record(idle(Some(StopReason::EndTurn)));
        state.record(rejected(&state, "late"));
        assert_eq!(
            kinds(&state, 1),
            ["prompt_started", "update", "update", "turn_ended", "update"]
        );
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
                request: RequestPermissionRequest::new("agent", "Tool", Vec::new()),
                answer,
            },
            Change::Cancelled,
            rejected(&state, "late"),
            running(),
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
            "message",
        )))
    }

    fn restored() -> State {
        let mut state =
            State::restored("s".into(), Some("Fix the build".into()), "agent".into(), 0);
        state.fill(Vec::new());
        state
    }

    /// The history of a session whose daemon stopped after `kinds`.
    fn stored(kinds: Vec<EventKind>) -> Vec<Event> {
        kinds
            .into_iter()
            .zip(1..)
            .map(|(kind, seq)| Event {
                session_id: "s".into(),
                seq,
                kind,
            })
            .collect()
    }

    fn agent_started() -> EventKind {
        EventKind::AgentStarted {
            agent_session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }
    }

    fn requested(request_id: &str) -> EventKind {
        EventKind::PermissionRequested {
            request_id: request_id.into(),
            title: "Tool".into(),
            description: None,
            subject: None,
            options: Vec::new(),
        }
    }

    #[test]
    fn a_restored_session_holds_no_events_until_it_is_filled() {
        let mut state = State::restored("s".into(), None, "agent".into(), 3);
        assert!(!state.loaded());
        assert_eq!(state.last_seq(), 3);
        assert!(state.events_after(0).is_empty());
        state.record(Change::Closed);
        assert_eq!(state.last_seq(), 3);
        assert!(state.take_unlogged().is_empty());
    }

    #[test]
    fn fill_closes_the_turn_and_the_permission_request_a_stop_left_open() {
        let mut state = State::restored("s".into(), None, "agent".into(), 6);
        state.fill(stored(vec![
            agent_started(),
            EventKind::TurnStarted,
            requested("1"),
            EventKind::PermissionResolved {
                request_id: "1".into(),
                outcome: CancelledPermissionOutcome::new().into(),
            },
            requested("2"),
            requested("7"),
        ]));
        assert!(state.loaded());
        assert_eq!(
            kinds(&state, 0),
            [
                "agent_started",
                "turn_started",
                "permission_requested",
                "permission_resolved",
                "permission_requested",
                "permission_requested",
                "permission_resolved",
                "permission_resolved",
                "turn_ended",
            ]
        );
        let logged = state.take_unlogged();
        assert_eq!(logged.iter().map(|e| e.seq).collect::<Vec<_>>(), [7, 8, 9]);
        assert!(matches!(
            &logged[2].kind,
            EventKind::TurnEnded { error: Some(error), .. } if error == "oxd stopped during the turn"
        ));
        assert!(state.take_unlogged().is_empty());
    }

    #[test]
    fn fill_leaves_a_finished_history_as_it_is() {
        let mut state = State::restored("s".into(), None, "agent".into(), 4);
        state.fill(stored(vec![
            agent_started(),
            EventKind::TurnStarted,
            EventKind::TurnEnded {
                stop_reason: None,
                error: None,
            },
            EventKind::AgentParked,
        ]));
        assert_eq!(state.last_seq(), 4);
        assert!(state.take_unlogged().is_empty());
    }

    #[test]
    fn permission_request_ids_continue_after_the_history() {
        let mut state = State::restored("s".into(), None, "agent".into(), 2);
        state.fill(stored(vec![agent_started(), requested("5")]));
        state.load().unwrap();
        state.record(Change::Started(Agent {
            session_id: "agent".into(),
            prompt_capabilities: PromptCapabilities::default(),
            config_options: Vec::new(),
        }));
        state.record(running());
        let _outcome = request_permission(&mut state);
        let last = state.events_after(state.last_seq() - 1);
        assert!(matches!(
            &last[0].kind,
            EventKind::PermissionRequested { request_id, .. } if request_id == "6"
        ));
    }

    #[test]
    fn session_closed_is_not_logged() {
        let mut state = started();
        state.take_unlogged();
        state.record(Change::Closed);
        assert_eq!(kinds(&state, 1), ["session_closed"]);
        assert!(state.take_unlogged().is_empty());
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

    fn idle_with(background: Vec<BackgroundWork>) -> Change {
        state_change(StateUpdate::Idle(
            IdleStateUpdate::new().background(background),
        ))
    }

    fn work() -> BackgroundWork {
        BackgroundWork::ToolCall(BackgroundToolCall::new("tool"))
    }

    #[test]
    fn a_session_is_quiet_when_the_agent_reports_no_background_work() {
        let mut state = started();
        assert!(!state.quiet(), "an agent that reports no list is not quiet");
        state.record(idle_with(vec![work()]));
        assert!(!state.quiet());
        state.record(idle_with(Vec::new()));
        assert!(state.quiet());
        state.record(idle(None));
        assert!(!state.quiet(), "a state without the list is not quiet");

        state.record(idle_with(Vec::new()));
        prompt(&mut state).unwrap();
        assert!(!state.quiet());
        state.record(idle_with(Vec::new()));
        state.record(running());
        state.record(idle_with(Vec::new()));
        assert!(state.quiet());
        let _answer = request_permission(&mut state);
        assert!(!state.quiet());
    }

    #[test]
    fn the_resume_snapshot_gives_the_session_its_background_work() {
        let resumed = |snapshot: Change| {
            let mut state = restored();
            state.load().unwrap();
            state.record(snapshot);
            state.record(Change::Started(Agent {
                session_id: "agent".into(),
                prompt_capabilities: PromptCapabilities::default(),
                config_options: Vec::new(),
            }));
            state
        };
        assert!(resumed(idle_with(Vec::new())).quiet());
        assert!(!resumed(idle_with(vec![work()])).quiet());
        assert!(!resumed(idle(None)).quiet());
    }

    #[test]
    fn parking_exits_a_quiet_session_and_records_it() {
        let mut state = started();
        state.record(idle_with(Vec::new()));
        state.record(Change::Parked);
        assert_eq!(state.state(), SessionState::Exited);
        assert_eq!(
            kinds(&state, 0),
            ["agent_started", "update", "agent_parked"]
        );
        assert_eq!(state.load().unwrap(), "agent");

        let mut state = State::new("s".into());
        state.record(Change::Parked);
        assert_eq!(state.state(), SessionState::Starting);
        assert!(kinds(&state, 0).is_empty());
    }

    #[test]
    fn the_resume_snapshot_sets_the_state_without_recording_a_turn() {
        let resumed = |state: &mut State, snapshot: Change| {
            assert_eq!(state.load().unwrap(), "agent");
            state.record(replayed("hello"));
            state.record(snapshot);
            state.record(Change::Started(Agent {
                session_id: "agent".into(),
                prompt_capabilities: PromptCapabilities::default(),
                config_options: Vec::new(),
            }));
        };

        let mut state = restored();
        resumed(&mut state, idle(None));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(kinds(&state, 0), ["update", "update", "agent_started"]);

        let mut state = restored();
        resumed(&mut state, running());
        assert_eq!(state.state(), SessionState::Running);
        assert_eq!(kinds(&state, 0), ["update", "update", "agent_started"]);
        assert_eq!(prompt(&mut state).unwrap_err().code, code::INVALID_STATE);
        state.record(idle(Some(StopReason::Cancelled)));
        assert_eq!(state.state(), SessionState::Idle);
        assert_eq!(
            kinds(&state, 0),
            ["update", "update", "agent_started", "update"]
        );
        prompt(&mut state).unwrap();

        // The snapshot counts when the session keeps its history and drops
        // the replay, and a turn it reports ends silently if the agent exits.
        let mut state = started();
        state.record(Change::Exited(None));
        resumed(&mut state, running());
        assert_eq!(state.state(), SessionState::Running);
        state.record(Change::Exited(None));
        assert_eq!(
            kinds(&state, 0),
            [
                "agent_started",
                "agent_exited",
                "agent_started",
                "agent_exited"
            ]
        );
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

    /// A form like the fake agent's: `color` (required, `red` or `blue`),
    /// `extras` (any of `sprinkles` and `nuts`, at most one), `note` (2 to 5
    /// characters), `count` (1 to 9), and `rush`.
    fn order() -> ElicitationSchema {
        ElicitationSchema::new()
            .property(
                "color",
                StringPropertySchema::new().one_of(vec![
                    EnumOption::new("red", "Red"),
                    EnumOption::new("blue", "Blue"),
                ]),
                true,
            )
            .property(
                "extras",
                MultiSelectPropertySchema::titled(vec![
                    EnumOption::new("sprinkles", "Sprinkles"),
                    EnumOption::new("nuts", "Nuts"),
                ])
                .max_items(1),
                false,
            )
            .property(
                "note",
                StringPropertySchema::new().min_length(2).max_length(5),
                false,
            )
            .property(
                "count",
                IntegerPropertySchema::new().minimum(1).maximum(9),
                false,
            )
            .boolean("rush", false)
    }

    /// Records an elicitation of `order()` and returns the receiver of its
    /// answer.
    fn elicit(state: &mut State) -> oneshot::Receiver<ElicitationAction> {
        let (answer, action) = oneshot::channel();
        state.record(Change::ElicitationRequested {
            message: "Order".into(),
            tool_call_id: None,
            schema: order(),
            answer,
        });
        action
    }

    fn accept(content: serde_json::Value) -> ElicitationAction {
        ElicitationAction::Accept(ElicitationAcceptAction::new().content(
            serde_json::from_value::<BTreeMap<String, ElicitationContentValue>>(content).unwrap(),
        ))
    }

    fn respond(
        state: &mut State,
        request_id: &str,
        action: ElicitationAction,
    ) -> Result<(), ErrorBody> {
        state.request(Request::Respond {
            request_id: request_id.into(),
            action,
        })
    }

    #[test]
    fn an_elicitation_awaits_input_unless_a_permission_request_is_open() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(running());
        let mut action = elicit(&mut state);
        assert_eq!(state.state(), SessionState::AwaitingInput);
        assert!(!state.quiet());
        let _outcome = request_permission(&mut state);
        assert_eq!(state.state(), SessionState::AwaitingPermission);
        state
            .request(Request::Permit {
                request_id: "2".into(),
                option_id: "allow".into(),
            })
            .unwrap();
        assert_eq!(state.state(), SessionState::AwaitingInput);

        // Each kind of request answers only to its own method.
        let error = state
            .request(Request::Permit {
                request_id: "1".into(),
                option_id: "allow".into(),
            })
            .unwrap_err();
        assert_eq!(error.code, code::INVALID_STATE);
        let error = respond(&mut state, "2", ElicitationAction::Decline).unwrap_err();
        assert_eq!(error.code, code::INVALID_STATE);

        let error = respond(&mut state, "1", accept(json!({"count": 3}))).unwrap_err();
        assert_eq!(error.code, code::INVALID_REQUEST);
        assert_eq!(state.state(), SessionState::AwaitingInput);
        respond(&mut state, "1", accept(json!({"color": "blue"}))).unwrap();
        assert_eq!(action.try_recv(), Ok(accept(json!({"color": "blue"}))));
        assert_eq!(state.state(), SessionState::Running);
        let error = respond(&mut state, "1", ElicitationAction::Decline).unwrap_err();
        assert_eq!(error.code, code::INVALID_STATE);
        assert_eq!(
            state.events_after(state.last_seq() - 1)[0].kind,
            EventKind::ElicitationResolved {
                request_id: "1".into(),
                action: accept(json!({"color": "blue"})),
            }
        );
    }

    #[test]
    fn cancelling_the_turn_leaves_an_elicitation_open() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(running());
        let mut action = elicit(&mut state);
        let mut outcome = request_permission(&mut state);
        state.record(Change::Cancelled);
        assert_eq!(
            outcome.try_recv(),
            Ok(CancelledPermissionOutcome::new().into())
        );
        assert!(action.try_recv().is_err());
        assert_eq!(state.state(), SessionState::AwaitingInput);
        respond(&mut state, "1", ElicitationAction::Decline).unwrap();
        assert_eq!(action.try_recv(), Ok(ElicitationAction::Decline));
    }

    #[test]
    fn exit_park_and_close_cancel_an_elicitation() {
        for (change, last) in [
            (Change::Exited(None), "agent_exited"),
            (Change::Parked, "agent_parked"),
            (Change::Closed, "session_closed"),
        ] {
            let mut state = started();
            prompt(&mut state).unwrap();
            let mut action = elicit(&mut state);
            state.record(change);
            assert_eq!(
                kinds(&state, 3),
                ["elicitation_resolved", "turn_ended", last]
            );
            assert_eq!(action.try_recv(), Ok(ElicitationAction::Cancel), "{last}");
        }
    }

    #[test]
    fn a_withdrawal_ends_only_the_elicitation_whose_receiver_closed() {
        let mut state = started();
        prompt(&mut state).unwrap();
        state.record(running());
        let first = elicit(&mut state);
        let mut second = elicit(&mut state);
        drop(first);
        // A client's answer to the withdrawn elicitation is refused.
        let error = respond(&mut state, "1", ElicitationAction::Decline).unwrap_err();
        assert_eq!(error.code, code::INVALID_STATE);
        state.record(Change::ElicitationWithdrawn);
        assert_eq!(
            state.events_after(state.last_seq() - 1)[0].kind,
            EventKind::ElicitationResolved {
                request_id: "1".into(),
                action: ElicitationAction::Cancel,
            }
        );
        assert_eq!(state.state(), SessionState::AwaitingInput);
        assert!(second.try_recv().is_err());
        respond(&mut state, "2", ElicitationAction::Cancel).unwrap();
        assert_eq!(second.try_recv(), Ok(ElicitationAction::Cancel));
    }

    #[test]
    fn fill_cancels_an_open_elicitation_and_ids_continue_past_it() {
        let mut state = State::restored("s".into(), None, "agent".into(), 3);
        state.fill(stored(vec![
            agent_started(),
            requested("1"),
            EventKind::ElicitationRequested {
                request_id: "4".into(),
                message: "Order".into(),
                tool_call_id: None,
                requested_schema: order(),
            },
        ]));
        assert_eq!(
            kinds(&state, 3),
            ["permission_resolved", "elicitation_resolved"]
        );
        assert_eq!(
            state.events_after(4)[0].kind,
            EventKind::ElicitationResolved {
                request_id: "4".into(),
                action: ElicitationAction::Cancel,
            }
        );
        state.load().unwrap();
        state.record(Change::Started(Agent::saved("agent".into())));
        let _action = elicit(&mut state);
        assert!(matches!(
            &state.events_after(state.last_seq() - 1)[0].kind,
            EventKind::ElicitationRequested { request_id, .. } if request_id == "5"
        ));
    }

    #[test]
    fn content_must_fit_the_schema() {
        let check = |content: serde_json::Value| {
            check_content(&order(), &serde_json::from_value(content).unwrap())
        };
        check(json!({"color": "red"})).unwrap();
        check(json!({
            "color": "blue", "extras": ["nuts"], "note": "héllo", "count": 9, "rush": false
        }))
        .unwrap();
        check(json!({"color": "blue", "extras": []})).unwrap();
        for (content, error) in [
            (
                json!({"color": "red", "size": 2}),
                "size is not a field of the form",
            ),
            (json!({"color": "green"}), "green is not a choice of color"),
            (json!({"color": 1}), "color takes text"),
            (
                json!({"color": "red", "count": "3"}),
                "count takes an integer",
            ),
            (
                json!({"color": "red", "count": 2.5}),
                "count takes an integer",
            ),
            (json!({"color": "red", "count": 10}), "count is at most 9"),
            (
                json!({"color": "red", "rush": "yes"}),
                "rush takes true or false",
            ),
            (
                json!({"color": "red", "note": "x"}),
                "note needs at least 2 characters",
            ),
            (
                json!({"color": "red", "note": "toolong"}),
                "note takes at most 5 characters",
            ),
            (
                json!({"color": "red", "extras": ["nuts", "sprinkles"]}),
                "extras takes at most 1 choices",
            ),
            (
                json!({"color": "red", "extras": ["ham"]}),
                "ham is not a choice of extras",
            ),
            (json!({"extras": ["nuts"]}), "color is required"),
        ] {
            assert_eq!(check(content.clone()).unwrap_err(), error, "{content}");
        }
        let schema = ElicitationSchema::new()
            .number("ratio", 0.0, 1.0, false)
            .property(
                "enumerated",
                StringPropertySchema::new().enum_values(vec!["a".into()]),
                false,
            )
            .property(
                "location",
                ElicitationPropertySchema::Other(OtherElicitationPropertySchema::new(
                    "_point",
                    BTreeMap::new(),
                )),
                false,
            );
        let check = |content: serde_json::Value| {
            check_content(&schema, &serde_json::from_value(content).unwrap())
        };
        check(json!({"ratio": 1, "enumerated": "a", "location": {"x": 1}})).unwrap();
        assert_eq!(
            check(json!({"ratio": 1.5})).unwrap_err(),
            "ratio is at most 1"
        );
        assert_eq!(
            check(json!({"enumerated": "b"})).unwrap_err(),
            "b is not a choice of enumerated"
        );
    }

    #[test]
    fn forms_a_client_cannot_render_are_refused() {
        let form = |schema: ElicitationSchema| {
            CreateElicitationRequest::new(
                ElicitationFormMode::new(
                    ElicitationSessionScope::new("agent").tool_call_id("t"),
                    schema,
                ),
                "Order",
            )
        };
        let (message, tool_call_id, schema) = check_form(form(order())).unwrap();
        assert_eq!(message, "Order");
        assert_eq!(tool_call_id, Some(ToolCallId::new("t")));
        assert_eq!(schema, order());

        let unknown = ElicitationPropertySchema::Other(OtherElicitationPropertySchema::new(
            "_point",
            BTreeMap::new(),
        ));
        let mut missing = ElicitationSchema::new();
        missing.required = Some(vec!["missing".into()]);
        check_form(form(ElicitationSchema::new().property(
            "location",
            unknown.clone(),
            false,
        )))
        .unwrap();
        let refused = [
            CreateElicitationRequest::new(
                ElicitationUrlMode::new(
                    ElicitationSessionScope::new("agent"),
                    "e",
                    "https://example.com",
                ),
                "Sign in",
            ),
            CreateElicitationRequest::new(
                ElicitationFormMode::new(ElicitationRequestScope::new(1), order()),
                "Order",
            ),
            form(ElicitationSchema::new().property("location", unknown, true)),
            form(missing),
            form(
                ElicitationSchema::new().property(
                    "color",
                    StringPropertySchema::new()
                        .enum_values(vec!["red".into()])
                        .one_of(vec![EnumOption::new("red", "Red")]),
                    false,
                ),
            ),
        ];
        for request in refused {
            let error = check_form(request).unwrap_err();
            assert_eq!(error.code, agent_client_protocol::ErrorCode::INVALID_PARAMS);
        }
    }
}
