//! The subagents of one main prompt run. Each runs the shared loop against its
//! own child session in a Tokio task, concurrently with the main agent, and
//! publishes each final answer or failure for the main loop to save and
//! deliver. Follow-up messages start later turns of the same conversation.

use std::{
    collections::VecDeque,
    path::PathBuf,
    sync::{Arc, Mutex, MutexGuard, PoisonError},
    time::Duration,
};

use agent_client_protocol::{Client, ConnectionTo, Result, schema::v1::SessionId};
use futures::future::BoxFuture;
use tokio::{sync::Notify, task::JoinHandle};

use crate::{
    acp::prompt::{self, Presentation, PromptInput, PromptOutput},
    cancellation::PromptCancellation,
    hooks::HookSource,
    openrouter,
    sessions::{AgentMessage, AgentMessageContent, SessionSettings, SessionStore, TurnInput},
    shell_processes::ShellProcesses,
    tools,
};

/// Idle subagents count, because each keeps its conversation for follow-up
/// messages until it is stopped.
pub const MAX_SUBAGENTS: usize = 4;

const CLOSED: &str = "This prompt run is ending, so its subagents accept no more work.";

/// What every turn of every subagent in one prompt run inherits from the main
/// agent.
pub struct Launch {
    pub store: SessionStore,
    pub openrouter: openrouter::Client,
    pub main_session_id: SessionId,
    pub workspace_path: PathBuf,
    /// The main turn's captured model, effort level, and session mode.
    pub settings: SessionSettings,
    /// The subagent system prompt derived from the main agent's.
    pub system_prompt: String,
    /// Global hooks only; invoked skill hooks belong to the main agent.
    pub global_hooks: Vec<HookSource>,
    pub shell_processes: ShellProcesses,
    /// The main agent's ACP connection, which carries subagent permission
    /// requests.
    pub connection: Option<ConnectionTo<Client>>,
    /// The main prompt's cancellation, which every subagent task observes.
    pub cancellation: PromptCancellation,
}

/// The prompt-owned guard over one prompt run's subagents. Tasks hold the
/// shared state, not this guard, so dropping it closes admission, discards
/// queued work and unread messages, and cancels every subagent even while
/// their tasks finish unwinding.
pub struct Subagents(Arc<Shared>);

struct Shared {
    launch: Launch,
    state: Mutex<State>,
    /// Wakes waits after a message is published or a subagent becomes idle
    /// or ends.
    changed: Notify,
}

struct State {
    open: bool,
    /// Whether any subagent started, so shutdown knows the cost may have
    /// changed.
    started: bool,
    /// Live subagents in start order.
    agents: Vec<Agent>,
    /// Messages for the main agent in publication order.
    messages: VecDeque<AgentMessage>,
}

struct Agent {
    /// The child session ID, which is also the subagent ID.
    id: SessionId,
    status: Status,
    /// Follow-up messages for after the current turn, in acceptance order.
    queued: VecDeque<String>,
    /// Cancels the current turn. A cancelled subagent never runs again.
    cancellation: PromptCancellation,
    /// The task running its turns. An idle subagent's task has finished.
    task: Option<JoinHandle<()>>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Status {
    Busy,
    Idle,
    Stopping,
}

/// What `send` did with a follow-up message.
pub enum Sent {
    /// The idle subagent started a turn with it.
    Started,
    /// The busy subagent reads it after its current turn; the count includes
    /// it.
    Queued(usize),
}

/// Why a wait returned, with every subagent's state at that moment.
pub struct Waited {
    pub reason: WaitReason,
    pub states: String,
}

pub enum WaitReason {
    Messages(usize),
    NoSubagents,
    AllIdle,
    TimedOut,
}

type Turn = BoxFuture<'static, Result<PromptOutput>>;

impl Subagents {
    pub fn new(launch: Launch) -> Self {
        Self(Arc::new(Shared {
            launch,
            state: Mutex::new(State {
                open: true,
                started: false,
                agents: Vec::new(),
                messages: VecDeque::new(),
            }),
            changed: Notify::new(),
        }))
    }

    /// Removes and returns the messages published since the last call.
    pub fn take_messages(&self) -> Vec<AgentMessage> {
        self.0.lock().messages.drain(..).collect()
    }

    /// Closes admission, discards unread messages, cancels every subagent,
    /// and waits for their current tasks, so each can attempt to save its
    /// interrupted batch. Returns whether any subagent started.
    pub async fn shutdown(&self) -> bool {
        let (tasks, started) = {
            let mut state = self.0.lock();
            state.open = false;
            state.messages.clear();
            let tasks: Vec<_> = state.agents.iter_mut().filter_map(Agent::stop).collect();
            (tasks, state.started)
        };
        for result in futures::future::join_all(tasks).await {
            propagate_panic(result);
        }
        self.0.lock().agents.clear();
        started
    }

    /// Starts a subagent on `task` in a new child session and returns its ID
    /// without waiting for its turn.
    pub fn start(&self, task: String) -> std::result::Result<SessionId, String> {
        let shared = &self.0;
        let mut state = shared.lock();
        if !state.open {
            return Err(CLOSED.to_owned());
        }
        if state.agents.len() >= MAX_SUBAGENTS {
            return Err(format!(
                "A prompt run can have at most {MAX_SUBAGENTS} subagents, including idle ones. Stop one with stop_subagent before starting another.\n\n{}",
                describe(&state)
            ));
        }
        let launch = &shared.launch;
        let id = launch
            .store
            .create_child(&launch.main_session_id, &launch.workspace_path)
            .map_err(|error| format!("Could not create the subagent's session: {error}"))?
            .id;
        let cancellation = PromptCancellation::new();
        let turn = shared.begin_turn(&id, task, &cancellation)?;
        let task = spawn(shared, id.clone(), cancellation.clone(), turn);
        state.agents.push(Agent {
            id: id.clone(),
            status: Status::Busy,
            queued: VecDeque::new(),
            cancellation,
            task: Some(task),
        });
        state.started = true;
        Ok(id)
    }

    /// Starts a turn of an idle subagent with `message`, or queues it for
    /// after a busy subagent's current turn.
    pub fn send(&self, id: &str, message: String) -> std::result::Result<Sent, String> {
        let shared = &self.0;
        let mut state = shared.lock();
        if !state.open {
            return Err(CLOSED.to_owned());
        }
        let agent = find(&mut state, id)?;
        match agent.status {
            Status::Busy => {
                agent.queued.push_back(message);
                Ok(Sent::Queued(agent.queued.len()))
            }
            Status::Idle => {
                let turn = shared.begin_turn(&agent.id, message, &agent.cancellation)?;
                agent.task = Some(spawn(
                    shared,
                    agent.id.clone(),
                    agent.cancellation.clone(),
                    turn,
                ));
                agent.status = Status::Busy;
                Ok(Sent::Started)
            }
            Status::Stopping => Err(format!("Subagent {id} is stopping.")),
        }
    }

    /// Cancels a subagent's current turn, discards its queued messages, and
    /// waits for its task before removing it.
    pub async fn stop(&self, id: &str) -> std::result::Result<(), String> {
        let task = find(&mut self.0.lock(), id)?.stop();
        if let Some(task) = task {
            propagate_panic(task.await);
        }
        self.0
            .lock()
            .agents
            .retain(|agent| agent.id.to_string() != id);
        self.0.changed.notify_waiters();
        Ok(())
    }

    /// Waits at most `limit` for a published message, every subagent to be
    /// idle, or no subagent to remain, returning at once when one already
    /// holds. `None` when `cancelled` completes first.
    pub async fn wait(
        &self,
        limit: Duration,
        cancelled: impl Future<Output = ()>,
    ) -> Option<Waited> {
        let deadline = tokio::time::sleep(limit);
        tokio::pin!(deadline, cancelled);
        loop {
            // Registering before the check means a change between the check
            // and the wait still wakes it.
            let changed = self.0.changed.notified();
            tokio::pin!(changed);
            changed.as_mut().enable();
            {
                let state = self.0.lock();
                let reason = if !state.messages.is_empty() {
                    Some(WaitReason::Messages(state.messages.len()))
                } else if state.agents.is_empty() {
                    Some(WaitReason::NoSubagents)
                } else if state
                    .agents
                    .iter()
                    .all(|agent| agent.status == Status::Idle)
                {
                    Some(WaitReason::AllIdle)
                } else {
                    None
                };
                if let Some(reason) = reason {
                    return Some(Waited {
                        reason,
                        states: describe(&state),
                    });
                }
            }
            tokio::select! {
                biased;
                () = &mut cancelled => return None,
                () = &mut changed => {}
                () = &mut deadline => {
                    return Some(Waited {
                        reason: WaitReason::TimedOut,
                        states: describe(&self.0.lock()),
                    });
                }
            }
        }
    }
}

impl Drop for Subagents {
    fn drop(&mut self) {
        let mut state = self.0.state.lock().unwrap_or_else(PoisonError::into_inner);
        state.open = false;
        state.messages.clear();
        for agent in state.agents.drain(..) {
            agent.cancellation.cancel();
        }
    }
}

impl Agent {
    /// Marks the subagent stopping, discards its queued messages, cancels
    /// its turn, and returns its task for the caller to await.
    fn stop(&mut self) -> Option<JoinHandle<()>> {
        self.status = Status::Stopping;
        self.queued.clear();
        self.cancellation.cancel();
        self.task.take()
    }
}

impl Shared {
    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().expect("subagents mutex poisoned")
    }

    /// Saves `text` as the next turn start of subagent `id`, a user message
    /// with the inherited settings, and returns its turn. Text that cannot
    /// fit the model context is rejected before it is saved.
    fn begin_turn(
        &self,
        id: &SessionId,
        text: String,
        cancellation: &PromptCancellation,
    ) -> std::result::Result<Turn, String> {
        let launch = &self.launch;
        let turn = prompt::run(
            launch.store.clone(),
            launch.openrouter.clone(),
            PromptInput {
                session_id: id.clone(),
                turn_input: TurnInput::UserMessage(text.into()),
                hook_sources: launch.global_hooks.clone(),
                selected_settings: launch.settings.clone(),
                system_prompt: launch.system_prompt.clone(),
                shell_processes: launch.shell_processes.clone(),
            },
            cancellation.clone(),
            Presentation::subagent(
                launch.connection.clone(),
                launch.main_session_id.clone(),
                id.clone(),
            ),
        )
        .map_err(|error| prompt::error_text(&error))?;
        Ok(Box::pin(turn))
    }

    /// Publishes the result of subagent `id`'s turn and returns its next
    /// turn from its queue. An empty queue leaves it idle; a failure ends it.
    /// A cancelled turn publishes nothing, because the subagent is being
    /// stopped or the main prompt was cancelled.
    fn finish_turn(&self, id: &SessionId, result: Result<PromptOutput>) -> Option<Turn> {
        let mut state = self.lock();
        // A stop or shutdown awaiting this task removes the subagent; a
        // dropped guard already has.
        let index = state.agents.iter().position(|agent| &agent.id == id)?;
        if !state.open || state.agents[index].status == Status::Stopping {
            return None;
        }
        let content = match result {
            Ok(PromptOutput::Finished(answer)) => AgentMessageContent::FinalAnswer(bounded(
                answer,
                "the complete answer remains saved in the subagent's session",
            )),
            Ok(PromptOutput::Cancelled) => {
                state.agents.remove(index);
                drop(state);
                self.changed.notify_waiters();
                return None;
            }
            Ok(PromptOutput::TokenLimit) => {
                AgentMessageContent::Failure("The model reached its token limit.".to_owned())
            }
            Ok(PromptOutput::Refused) => {
                AgentMessageContent::Failure("The model refused.".to_owned())
            }
            Err(error) => AgentMessageContent::Failure(bounded(
                prompt::error_text(&error),
                "the rest of the error was omitted",
            )),
        };
        let finished = matches!(content, AgentMessageContent::FinalAnswer(_));
        state.messages.push_back(AgentMessage {
            subagent_id: id.to_string(),
            content,
        });
        let next = if finished {
            self.next_turn(&mut state, index)
        } else {
            state.agents.remove(index);
            None
        };
        drop(state);
        self.changed.notify_waiters();
        next
    }

    /// Begins the subagent's next queued message, or marks it idle. A
    /// message rejected by input admission ends it with a failure.
    fn next_turn(&self, state: &mut State, index: usize) -> Option<Turn> {
        let agent = &mut state.agents[index];
        let Some(text) = agent.queued.pop_front() else {
            agent.status = Status::Idle;
            return None;
        };
        let (id, cancellation) = (agent.id.clone(), agent.cancellation.clone());
        match self.begin_turn(&id, text, &cancellation) {
            Ok(turn) => Some(turn),
            Err(error) => {
                state.messages.push_back(AgentMessage {
                    subagent_id: id.to_string(),
                    content: AgentMessageContent::Failure(format!(
                        "Its next queued message was rejected: {error}"
                    )),
                });
                state.agents.remove(index);
                None
            }
        }
    }
}

fn spawn(
    shared: &Arc<Shared>,
    id: SessionId,
    cancellation: PromptCancellation,
    turn: Turn,
) -> JoinHandle<()> {
    tokio::spawn(run_turns(shared.clone(), id, cancellation, turn))
}

/// Runs a subagent's turns until it becomes idle or ends. Cancelling the main
/// prompt cancels the current turn, which still finishes its own cleanup.
async fn run_turns(
    shared: Arc<Shared>,
    id: SessionId,
    cancellation: PromptCancellation,
    mut turn: Turn,
) {
    loop {
        let finished = tokio::select! {
            biased;
            result = &mut turn => Some(result),
            () = shared.launch.cancellation.cancelled() => None,
        };
        let result = match finished {
            Some(result) => result,
            None => {
                cancellation.cancel();
                (&mut turn).await
            }
        };
        match shared.finish_turn(&id, result) {
            Some(next) => turn = next,
            None => return,
        }
    }
}

fn find<'a>(state: &'a mut State, id: &str) -> std::result::Result<&'a mut Agent, String> {
    state
        .agents
        .iter_mut()
        .find(|agent| agent.id.to_string() == id)
        .ok_or_else(|| format!("No subagent {id} in this prompt run."))
}

/// Each subagent's ID and state, one per line.
fn describe(state: &State) -> String {
    if state.agents.is_empty() {
        return "No subagents.".to_owned();
    }
    state
        .agents
        .iter()
        .map(|agent| {
            let status = match agent.status {
                Status::Busy => "busy",
                Status::Idle => "idle",
                Status::Stopping => "stopping",
            };
            match agent.queued.len() {
                0 => format!("{}: {status}", agent.id),
                1 => format!("{}: {status}, 1 queued message", agent.id),
                queued => format!("{}: {status}, {queued} queued messages", agent.id),
            }
        })
        .collect::<Vec<_>>()
        .join("\n")
}

/// Keeps a forwarded message within the tool-result limit, saying what
/// happened to the rest.
fn bounded(mut text: String, rest: &str) -> String {
    if text.len() > tools::OUTPUT_LIMIT {
        tools::truncate(&mut text, tools::BODY_LIMIT);
        text.push_str(&format!("\n[Truncated; {rest}.]"));
    }
    text
}

/// A panic in a subagent task is a bug, so it surfaces in the main prompt.
fn propagate_panic(result: std::result::Result<(), tokio::task::JoinError>) {
    if let Err(error) = result
        && error.is_panic()
    {
        std::panic::resume_unwind(error.into_panic());
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;
    use crate::{
        openrouter::fixture::{DEFAULT_MODEL, Gate, Reply, Server, calls_reply, text_reply},
        sessions::{
            AssistantBatch, AssistantMessage, EffortLevel, SessionMode, ToolOutcome,
            TranscriptEntry, TurnStart,
        },
        tools::fixture::Workspace,
    };

    /// One prompt run's subagents, answered by a routed server.
    struct Owner {
        subagents: Subagents,
        server: Server,
        store: SessionStore,
        shell_processes: ShellProcesses,
        workspace: Workspace,
    }

    impl Owner {
        async fn new(routes: Vec<(&str, Vec<Reply>)>) -> Self {
            let workspace = Workspace::new();
            let store = SessionStore::in_memory();
            let main_session_id = store.create(&workspace.0).unwrap().id;
            let server = Server::routed(routes).await;
            let shell_processes = ShellProcesses::default();
            let subagents = Subagents::new(Launch {
                store: store.clone(),
                openrouter: server.client(),
                main_session_id,
                workspace_path: workspace.0.clone(),
                settings: SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default)
                    .with_mode(SessionMode::Auto),
                system_prompt: "You are a subagent.".to_owned(),
                global_hooks: Vec::new(),
                shell_processes: shell_processes.clone(),
                connection: None,
                cancellation: PromptCancellation::new(),
            });
            Self {
                subagents,
                server,
                store,
                shell_processes,
                workspace,
            }
        }

        fn start(&self, task: &str) -> String {
            self.subagents.start(task.to_owned()).unwrap().to_string()
        }

        fn transcript(&self, id: &str) -> Vec<TranscriptEntry> {
            self.store
                .read(&SessionId::new(id))
                .unwrap()
                .unwrap()
                .transcript
        }

        async fn wait(&self, limit: Duration) -> Waited {
            self.subagents
                .wait(limit, std::future::pending())
                .await
                .expect("the wait is not cancelled")
        }

        /// Waits until no subagent is busy and returns the texts of the
        /// messages published meanwhile, in publication order.
        async fn settle(&self) -> Vec<String> {
            let mut texts = Vec::new();
            loop {
                let waited = self.wait(Duration::from_secs(10)).await;
                texts.extend(
                    self.subagents
                        .take_messages()
                        .iter()
                        .map(|message| message.text().to_owned()),
                );
                match waited.reason {
                    WaitReason::Messages(_) => {}
                    WaitReason::AllIdle | WaitReason::NoSubagents => return texts,
                    WaitReason::TimedOut => panic!("subagents did not settle"),
                }
            }
        }
    }

    fn turn(text: &str) -> TranscriptEntry {
        TranscriptEntry::TurnStart(TurnStart {
            mode: SessionMode::Auto,
            ..TurnStart::test(text.to_owned())
        })
    }

    fn answer(text: &str) -> TranscriptEntry {
        TranscriptEntry::AssistantBatch(
            AssistantBatch::new(
                AssistantMessage {
                    text: text.to_owned(),
                    reasoning: String::new(),
                    tool_calls: vec![],
                    continuation_metadata: vec![],
                    usage: None,
                },
                vec![],
            )
            .unwrap(),
        )
    }

    #[tokio::test]
    async fn an_owner_admits_at_most_four_subagents_including_idle_ones() {
        let tasks: Vec<_> = (1..=5).map(|n| format!("Task {n}")).collect();
        let owner = Owner::new(
            tasks
                .iter()
                .map(|task| (task.as_str(), vec![text_reply(&format!("{task} done."))]))
                .collect(),
        )
        .await;
        let ids: Vec<_> = tasks[..4].iter().map(|task| owner.start(task)).collect();
        let mut answers = owner.settle().await;
        answers.sort();
        assert_eq!(
            answers,
            [
                "Task 1 done.",
                "Task 2 done.",
                "Task 3 done.",
                "Task 4 done."
            ]
        );

        let error = owner.subagents.start(tasks[4].clone()).unwrap_err();
        assert_eq!(
            error,
            format!(
                "A prompt run can have at most 4 subagents, including idle ones. Stop one with stop_subagent before starting another.\n\n{}",
                ids.iter()
                    .map(|id| format!("{id}: idle"))
                    .collect::<Vec<_>>()
                    .join("\n")
            )
        );
        owner.subagents.stop(&ids[0]).await.unwrap();
        owner.start(&tasks[4]);
        assert_eq!(owner.settle().await, ["Task 5 done."]);
    }

    #[tokio::test]
    async fn subagent_ids_resolve_only_among_the_live_subagents_of_their_prompt_run() {
        let owner = Owner::new(vec![("Task", vec![text_reply("Done.")])]).await;
        let other = Owner::new(vec![]).await;
        let id = owner.start("Task");
        owner.settle().await;
        let unknown = |id: &str| Err(format!("No subagent {id} in this prompt run."));
        assert_eq!(
            other.subagents.send(&id, "Hello.".to_owned()).map(drop),
            unknown(&id)
        );
        assert_eq!(other.subagents.stop(&id).await, unknown(&id));
        assert_eq!(owner.subagents.stop("missing").await, unknown("missing"));
        owner.subagents.stop(&id).await.unwrap();
        assert_eq!(
            owner.subagents.send(&id, "Hello.".to_owned()).map(drop),
            unknown(&id)
        );
    }

    #[tokio::test]
    async fn follow_up_messages_start_an_idle_subagent_and_queue_behind_a_busy_turn() {
        let gate = Gate::new();
        let owner = Owner::new(vec![(
            "Task",
            vec![
                gate.hold(text_reply("Which file?")),
                text_reply("Read it."),
                text_reply("Both done."),
                text_reply("Again done."),
            ],
        )])
        .await;
        let id = owner.start("Task: inspect the parser.");
        owner.server.wait_for_requests("Task", 1).await;
        for (message, queued) in [("First follow-up.", 1), ("Second follow-up.", 2)] {
            assert!(matches!(
                owner.subagents.send(&id, message.to_owned()),
                Ok(Sent::Queued(count)) if count == queued
            ));
        }
        gate.open();
        assert_eq!(
            owner.settle().await,
            ["Which file?", "Read it.", "Both done."]
        );
        assert!(matches!(
            owner.subagents.send(&id, "Third follow-up.".to_owned()),
            Ok(Sent::Started)
        ));
        assert_eq!(owner.settle().await, ["Again done."]);

        assert_eq!(
            owner.transcript(&id),
            [
                turn("Task: inspect the parser."),
                answer("Which file?"),
                turn("First follow-up."),
                answer("Read it."),
                turn("Second follow-up."),
                answer("Both done."),
                turn("Third follow-up."),
                answer("Again done."),
            ]
        );
        let second = &owner.server.requests_for("Task")[1];
        assert_eq!(
            second["messages"],
            json!([
                {"role": "system", "content": "You are a subagent."},
                {"role": "user", "content": "Task: inspect the parser."},
                {"role": "assistant", "content": "Which file?"},
                {"role": "user", "content": "First follow-up."},
            ])
        );
    }

    #[tokio::test]
    async fn waits_return_for_messages_idleness_timeouts_and_cancellation() {
        let gate = Gate::new();
        let owner = Owner::new(vec![("Task", vec![gate.hold(text_reply("Done."))])]).await;
        let waited = owner.wait(Duration::from_secs(600)).await;
        assert!(matches!(waited.reason, WaitReason::NoSubagents));
        assert_eq!(waited.states, "No subagents.");

        let id = owner.start("Task");
        owner.server.wait_for_requests("Task", 1).await;
        for limit in [Duration::ZERO, Duration::from_millis(20)] {
            let waited = owner.wait(limit).await;
            assert!(matches!(waited.reason, WaitReason::TimedOut));
            assert_eq!(waited.states, format!("{id}: busy"));
        }
        assert!(
            owner
                .subagents
                .wait(Duration::from_secs(600), std::future::ready(()))
                .await
                .is_none()
        );

        let (waited, ()) =
            tokio::join!(owner.wait(Duration::from_secs(600)), async { gate.open() });
        assert!(matches!(waited.reason, WaitReason::Messages(1)));
        assert_eq!(owner.subagents.take_messages()[0].text(), "Done.");
        let waited = owner.wait(Duration::from_secs(600)).await;
        assert!(matches!(waited.reason, WaitReason::AllIdle));
        assert_eq!(waited.states, format!("{id}: idle"));
    }

    #[tokio::test]
    async fn stopping_a_subagent_cancels_its_turn_discards_its_queue_and_keeps_its_commands() {
        let owner = Owner::new(vec![(
            "Task",
            vec![calls_reply(&[
                (
                    "background",
                    tools::SHELL,
                    json!({"command": "exec sleep 30", "background": true}),
                ),
                (
                    "foreground",
                    tools::SHELL,
                    json!({"command": "touch running; sleep 30"}),
                ),
            ])],
        )])
        .await;
        let id = owner.start("Task");
        let running = owner.workspace.0.join("running");
        tokio::time::timeout(Duration::from_secs(10), async {
            while !running.exists() {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap();
        assert!(matches!(
            owner.subagents.send(&id, "Queued.".to_owned()),
            Ok(Sent::Queued(1))
        ));

        owner.subagents.stop(&id).await.unwrap();

        let transcript = owner.transcript(&id);
        assert_eq!(transcript.len(), 2, "the queued message never started");
        let TranscriptEntry::AssistantBatch(batch) = &transcript[1] else {
            panic!("the interrupted batch was saved");
        };
        assert!(
            matches!(&batch.outcomes[..], [ToolOutcome::Completed(started), ToolOutcome::Cancelled(_)]
            if started.starts_with("Started shell process"))
        );
        assert!(owner.subagents.take_messages().is_empty());
        let processes = owner.shell_processes.list();
        assert_eq!(processes.len(), 1);
        assert_eq!(processes[0].state(), crate::shell_processes::State::Running);
        owner.shell_processes.shutdown().await;
    }

    #[tokio::test]
    async fn a_failed_turn_reports_its_failure_and_ends_the_subagent() {
        let owner = Owner::new(vec![(
            "Task",
            vec![Reply::Status(500, "unavailable".to_owned())],
        )])
        .await;
        let id = owner.start("Task");
        let messages = owner.settle().await;
        assert_eq!(messages.len(), 1);
        assert!(
            messages[0].contains("OpenRouter returned 500"),
            "{}",
            messages[0]
        );
        assert!(owner.subagents.send(&id, "Retry.".to_owned()).is_err());
    }

    #[tokio::test]
    async fn a_subagent_compacts_its_own_conversation() {
        // The summary names the task, so the compacted request still routes
        // to the child's script.
        let owner = Owner::new(vec![(
            "Task C",
            vec![
                text_reply(&"y".repeat(2_300_000)),
                text_reply("Task C report summarized."),
                text_reply("Follow-up done."),
            ],
        )])
        .await;
        let id = owner.start("Task C: write a long report.");
        owner.settle().await;
        owner
            .subagents
            .send(&id, "Shorten it.".to_owned())
            .map(drop)
            .unwrap();
        assert_eq!(owner.settle().await, ["Follow-up done."]);
        let requests = owner.server.requests_for("Task C");
        assert!(requests[1].get("tools").is_none(), "a summarizer request");
        assert_eq!(
            requests[2]["messages"][1]["content"],
            "Compaction summary of earlier conversation:\nTask C report summarized."
        );
        assert!(
            owner
                .transcript(&id)
                .iter()
                .any(|entry| matches!(entry, TranscriptEntry::CompactionCheckpoint(_)))
        );
        let main = owner.store.list(None).unwrap().remove(0).id;
        assert!(
            owner
                .store
                .read(&main)
                .unwrap()
                .unwrap()
                .transcript
                .is_empty()
        );
    }

    #[tokio::test]
    async fn forwarded_answers_are_bounded_while_the_child_keeps_the_complete_answer() {
        let long = "x".repeat(20_000);
        let owner = Owner::new(vec![("Task", vec![text_reply(&long)])]).await;
        let id = owner.start("Task");
        let forwarded = owner.settle().await.remove(0);
        assert!(forwarded.len() <= tools::OUTPUT_LIMIT);
        assert!(forwarded.ends_with(
            "\n[Truncated; the complete answer remains saved in the subagent's session.]"
        ));
        assert_eq!(owner.transcript(&id)[1], answer(&long));
    }
}
