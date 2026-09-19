use agent_client_protocol::schema::v1::{
    AgentAuthCapabilities, AgentCapabilities, AuthMethod, AuthMethodTerminal, CancelNotification,
    ContentBlock, ContentChunk, DeleteSessionRequest, DeleteSessionResponse, InitializeRequest,
    InitializeResponse, ListSessionsRequest, ListSessionsResponse, LoadSessionRequest,
    LoadSessionResponse, LogoutCapabilities, LogoutRequest, LogoutResponse, NewSessionRequest,
    NewSessionResponse, PromptRequest, PromptResponse, SessionCapabilities,
    SessionDeleteCapabilities, SessionId, SessionListCapabilities, SessionNotification,
    SessionUpdate, StopReason, TextContent,
};
use agent_client_protocol::{Agent, Error, Result, Stdio};
use futures::{
    FutureExt, StreamExt,
    channel::oneshot,
    future::{BoxFuture, Shared},
};
use rig::{
    agent::MultiTurnStreamItem,
    completion::{AssistantContent, Message},
    message::{
        Reasoning, Text, ToolCall, ToolCallId, ToolFunction, ToolResult, ToolResultContent,
        UserContent,
    },
    streaming::{StreamedAssistantContent, StreamedUserContent},
};
use std::{
    collections::HashMap,
    sync::{Arc, Mutex},
};

use crate::{agent, auth, sessions};

type AgentState = Arc<Mutex<Option<agent::OxAgent>>>;

fn current_agent(agents: &AgentState) -> Result<agent::OxAgent> {
    agents
        .lock()
        .expect("agent state mutex poisoned")
        .clone()
        .ok_or_else(Error::auth_required)
}

// TODO: Test terminal authentication with the preview version of Zed.
fn terminal_auth_method() -> AuthMethod {
    AuthMethod::Terminal(
        AuthMethodTerminal::new("openrouter", "Log in to OpenRouter")
            .description("Enter an OpenRouter API key and save it in the system keyring")
            .args(vec!["auth".to_owned(), "login".to_owned()]),
    )
}

fn initialize_response(initialize: &InitializeRequest) -> InitializeResponse {
    let mut response = InitializeResponse::new(initialize.protocol_version).agent_capabilities(
        AgentCapabilities::new()
            .load_session(true)
            .session_capabilities(
                SessionCapabilities::new()
                    .list(SessionListCapabilities::new())
                    .delete(SessionDeleteCapabilities::new()),
            )
            .auth(AgentAuthCapabilities::new().logout(LogoutCapabilities::new())),
    );
    if initialize.client_capabilities.auth.terminal {
        response = response.auth_methods(vec![terminal_auth_method()]);
    }
    response
}

#[derive(Clone)]
struct PromptCancellation(Arc<PromptCancellationState>);

struct PromptCancellationState {
    signal_tx: Mutex<Option<oneshot::Sender<()>>>,
    signal_rx: Shared<BoxFuture<'static, ()>>,
}

impl PromptCancellation {
    fn new() -> Self {
        let (signal_tx, signal_rx) = oneshot::channel();
        Self(Arc::new(PromptCancellationState {
            signal_tx: Mutex::new(Some(signal_tx)),
            signal_rx: signal_rx.map(|_| ()).boxed().shared(),
        }))
    }

    async fn cancelled(&self) {
        self.0.signal_rx.clone().await;
    }

    fn cancel(&self) {
        let signal_tx = self
            .0
            .signal_tx
            .lock()
            .expect("prompt cancellation signal mutex poisoned")
            .take();
        if let Some(signal_tx) = signal_tx {
            let _ = signal_tx.send(());
        }
    }

    fn is_same(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.0, &other.0)
    }
}

#[derive(Clone, Default)]
struct InFlightPrompts(Arc<Mutex<HashMap<SessionId, PromptCancellation>>>);

impl InFlightPrompts {
    fn begin(&self, session_id: SessionId) -> Option<PromptCancellation> {
        let cancellation = PromptCancellation::new();
        let mut prompts = self.0.lock().expect("in-flight prompts mutex poisoned");
        if prompts.contains_key(&session_id) {
            return None;
        }
        prompts.insert(session_id, cancellation.clone());
        Some(cancellation)
    }

    fn finish(&self, session_id: &SessionId, cancellation: &PromptCancellation) {
        let mut prompts = self.0.lock().expect("in-flight prompts mutex poisoned");
        if prompts
            .get(session_id)
            .is_some_and(|candidate| candidate.is_same(cancellation))
        {
            prompts.remove(session_id);
        }
    }

    fn cancel(&self, session_id: &SessionId) {
        let cancellation = self
            .0
            .lock()
            .expect("in-flight prompts mutex poisoned")
            .get(session_id)
            .cloned();
        if let Some(cancellation) = cancellation {
            cancellation.cancel();
        }
    }
}

fn text_content(content: &[ContentBlock]) -> String {
    content
        .iter()
        .map(|block| match block {
            ContentBlock::Text(text) => text.text.clone(),
            ContentBlock::ResourceLink(link) => {
                format!("Resource link: {}\nURI: {}", link.name, link.uri)
            }
            _ => String::new(),
        })
        .filter(|content| !content.is_empty())
        .collect::<Vec<_>>()
        .join("\n")
}

fn session_history_from_events(events: Vec<sessions::Event>) -> Result<Vec<Message>> {
    let mut history = Vec::new();
    let mut thought = String::new();

    for event in events {
        match event.kind {
            sessions::EventKind::UserMessage(content) => {
                if !thought.is_empty() {
                    history.push(Message::Assistant {
                        id: None,
                        content: vec![AssistantContent::Reasoning(Reasoning::new(&thought))],
                    });
                    thought.clear();
                }
                history.push(Message::User {
                    content: vec![UserContent::Text(Text::new(content))],
                });
            }
            sessions::EventKind::AgentThought(text) => thought.push_str(&text),
            sessions::EventKind::AgentMessage(text) => {
                let mut content = Vec::new();
                if !thought.is_empty() {
                    content.push(AssistantContent::Reasoning(Reasoning::new(&thought)));
                    thought.clear();
                }
                content.push(AssistantContent::Text(Text::new(text)));
                history.push(Message::Assistant { id: None, content });
            }
            sessions::EventKind::ToolCall {
                call_id,
                name,
                arguments,
            } => history.push(Message::Assistant {
                id: None,
                content: vec![AssistantContent::ToolCall(ToolCall::new(
                    ToolCallId::new_or_mint(call_id),
                    ToolFunction::new(name, arguments),
                ))],
            }),
            sessions::EventKind::ToolResult {
                call_id,
                name,
                result,
            } => history.push(Message::User {
                content: vec![UserContent::ToolResult(ToolResult {
                    call: ToolCallId::new_or_mint(call_id),
                    provider: None,
                    name,
                    content: vec![ToolResultContent::text(result)],
                })],
            }),
        }
    }

    if !thought.is_empty() {
        history.push(Message::Assistant {
            id: None,
            content: vec![AssistantContent::Reasoning(Reasoning::new(&thought))],
        });
    }

    Ok(history)
}

fn session_history(session_id: &SessionId) -> Result<Vec<Message>> {
    session_history_from_events(sessions::events(session_id).map_err(Error::into_internal_error)?)
}

fn record_iteration(
    session_id: &SessionId,
    events: &mut Vec<sessions::EventKind>,
    thought: &mut String,
    response: &mut String,
) -> Result<()> {
    flush_assistant_text(events, thought, response);
    sessions::append_events(session_id, None, events, &sessions::now())
        .map_err(Error::into_internal_error)?;
    events.clear();
    Ok(())
}

fn flush_assistant_text(
    events: &mut Vec<sessions::EventKind>,
    thought: &mut String,
    response: &mut String,
) {
    if !thought.is_empty() {
        events.push(sessions::EventKind::AgentThought(std::mem::take(thought)));
    }
    if !response.is_empty() {
        events.push(sessions::EventKind::AgentMessage(std::mem::take(response)));
    }
}

fn session_updates(events: Vec<sessions::Event>) -> Result<Vec<SessionUpdate>> {
    let mut updates = Vec::new();

    for event in events {
        match event.kind {
            sessions::EventKind::UserMessage(text) => {
                updates.push(SessionUpdate::UserMessageChunk(ContentChunk::new(
                    ContentBlock::Text(TextContent::new(text)),
                )))
            }
            sessions::EventKind::AgentThought(text) => {
                updates.push(SessionUpdate::AgentThoughtChunk(ContentChunk::new(
                    ContentBlock::Text(TextContent::new(text)),
                )))
            }
            sessions::EventKind::AgentMessage(text) => {
                updates.push(SessionUpdate::AgentMessageChunk(ContentChunk::new(
                    ContentBlock::Text(TextContent::new(text)),
                )))
            }
            sessions::EventKind::ToolCall { .. } | sessions::EventKind::ToolResult { .. } => {}
        }
    }

    Ok(updates)
}

pub async fn run() -> Result<()> {
    let agent = auth::api_key()
        .map_err(Error::into_internal_error)?
        .map(|api_key| agent::OxAgent::new(&api_key))
        .transpose()
        .map_err(Error::into_internal_error)?;
    let agents = Arc::new(Mutex::new(agent));
    let in_flight_prompts = InFlightPrompts::default();
    let new_session_agents = agents.clone();
    let load_session_agents = agents.clone();
    let logout_agents = agents.clone();
    let prompt_agents = agents.clone();
    let prompt_cancellations = in_flight_prompts.clone();

    Agent
        .builder()
        .name("ox")
        .on_receive_request(
            async move |initialize: InitializeRequest, responder, _connection| {
                responder.respond(initialize_response(&initialize))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |new_session: NewSessionRequest, responder, _connection| {
                if let Err(error) = current_agent(&new_session_agents) {
                    return responder.respond_with_error(error);
                }
                let session_id = SessionId::new(uuid::Uuid::new_v4().to_string());
                match sessions::create_session(&new_session.cwd, &session_id) {
                    Ok(_) => responder.respond(NewSessionResponse::new(session_id)),
                    Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |load_session: LoadSessionRequest, responder, connection| {
                if let Err(error) = current_agent(&load_session_agents) {
                    return responder.respond_with_error(error);
                }
                match sessions::session_exists(&load_session.cwd, &load_session.session_id) {
                    Ok(true) => {
                        let updates = match sessions::events(&load_session.session_id)
                            .map_err(Error::into_internal_error)
                            .and_then(session_updates)
                        {
                            Ok(updates) => updates,
                            Err(err) => return responder.respond_with_error(err),
                        };
                        for update in updates {
                            if let Err(err) = connection.send_notification(
                                SessionNotification::new(load_session.session_id.clone(), update),
                            ) {
                                return responder.respond_with_error(err);
                            }
                        }
                        responder.respond(LoadSessionResponse::new())
                    }
                    Ok(false) => responder.respond_with_error(Error::resource_not_found(Some(
                        load_session.session_id.to_string(),
                    ))),
                    Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |list_sessions: ListSessionsRequest, responder, _connection| {
                match sessions::list_sessions(list_sessions.cwd.as_deref()) {
                    Ok(sessions) => responder.respond(ListSessionsResponse::new(sessions)),
                    Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |delete_session: DeleteSessionRequest, responder, _connection| {
                match sessions::delete_session(&delete_session.session_id) {
                    Ok(true) => responder.respond(DeleteSessionResponse::new()),
                    Ok(false) => responder.respond_with_error(Error::resource_not_found(Some(
                        delete_session.session_id.to_string(),
                    ))),
                    Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |_logout: LogoutRequest, responder, _connection| match auth::delete_api_key()
            {
                Ok(_) => {
                    logout_agents
                        .lock()
                        .expect("agent state mutex poisoned")
                        .take();
                    responder.respond(LogoutResponse::new())
                }
                Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |prompt: PromptRequest, responder, connection| {
                let agent = match current_agent(&prompt_agents) {
                    Ok(agent) => agent,
                    Err(error) => return responder.respond_with_error(error),
                };
                let Some(cancellation) = prompt_cancellations.begin(prompt.session_id.clone())
                else {
                    return responder.respond_with_error(Error::new(
                        -32600,
                        "session already has a prompt in progress",
                    ));
                };
                let in_flight_prompts = prompt_cancellations.clone();
                let task_connection = connection.clone();

                connection.spawn(async move {
                    let result = async {
                        match sessions::session_exists_anywhere(&prompt.session_id) {
                            Ok(true) => {}
                            Ok(false) => {
                                return responder.respond_with_error(Error::resource_not_found(
                                    Some(prompt.session_id.to_string()),
                                ));
                            }
                            Err(err) => {
                                return responder
                                    .respond_with_error(Error::into_internal_error(err));
                            }
                        }
                        let history = match session_history(&prompt.session_id) {
                            Ok(history) => history,
                            Err(err) => return responder.respond_with_error(err),
                        };
                        let title = prompt.prompt.iter().find_map(|block| match block {
                            ContentBlock::Text(text) => sessions::title_from_prompt(&text.text),
                            _ => None,
                        });

                        let input = text_content(&prompt.prompt);
                        let user_message = sessions::EventKind::UserMessage(input.clone());
                        if let Err(err) = sessions::append_event(
                            &prompt.session_id,
                            title,
                            &user_message,
                            &sessions::now(),
                        ) {
                            return responder.respond_with_error(Error::into_internal_error(err));
                        }
                        let stream = tokio::select! {
                            _ = cancellation.cancelled() => None,
                            stream = agent.stream(input.clone(), history) => Some(stream),
                        };
                        let mut response = String::new();
                        let mut thought = String::new();
                        let mut events = Vec::new();
                        let mut completed_iteration = false;

                        let cancelled = if let Some(mut stream) = stream {
                            loop {
                                let item = tokio::select! {
                                    _ = cancellation.cancelled() => break true,
                                    item = stream.next() => item,
                                };
                                let Some(item) = item else {
                                    break false;
                                };

                                match item {
                                    Ok(MultiTurnStreamItem::StreamAssistantItem(
                                        StreamedAssistantContent::ReasoningDelta {
                                            reasoning: delta,
                                            ..
                                        },
                                    )) => {
                                        thought.push_str(&delta);
                                        task_connection.send_notification(
                                            SessionNotification::new(
                                                prompt.session_id.clone(),
                                                SessionUpdate::AgentThoughtChunk(
                                                    ContentChunk::new(ContentBlock::Text(
                                                        TextContent::new(delta),
                                                    )),
                                                ),
                                            ),
                                        )?;
                                    }
                                    Ok(MultiTurnStreamItem::StreamAssistantItem(
                                        StreamedAssistantContent::Text(text),
                                    )) => {
                                        response.push_str(&text.text);
                                        task_connection.send_notification(
                                            SessionNotification::new(
                                                prompt.session_id.clone(),
                                                SessionUpdate::AgentMessageChunk(
                                                    ContentChunk::new(ContentBlock::Text(
                                                        TextContent::new(text.text),
                                                    )),
                                                ),
                                            ),
                                        )?;
                                    }
                                    Ok(MultiTurnStreamItem::StreamAssistantItem(
                                        StreamedAssistantContent::ToolCall { tool_call, .. },
                                    )) => {
                                        flush_assistant_text(
                                            &mut events,
                                            &mut thought,
                                            &mut response,
                                        );
                                        events.push(sessions::EventKind::ToolCall {
                                            call_id: tool_call.id.as_str().to_owned(),
                                            name: tool_call.function.name,
                                            arguments: tool_call.function.arguments,
                                        });
                                    }
                                    Ok(MultiTurnStreamItem::StreamUserItem(
                                        StreamedUserContent::ToolResult { tool_result, .. },
                                    )) => {
                                        let Some(result) = tool_result
                                            .content
                                            .iter()
                                            .find_map(ToolResultContent::as_text)
                                        else {
                                            return responder.respond_with_error(
                                                Error::into_internal_error(std::io::Error::other(
                                                    "tool result has no text content",
                                                )),
                                            );
                                        };
                                        flush_assistant_text(
                                            &mut events,
                                            &mut thought,
                                            &mut response,
                                        );
                                        events.push(sessions::EventKind::ToolResult {
                                            call_id: tool_result.call.as_str().to_owned(),
                                            name: tool_result.name,
                                            result: result.to_owned(),
                                        });
                                    }
                                    Ok(MultiTurnStreamItem::FinalResponse(final_response)) => {
                                        if !completed_iteration && response.is_empty() {
                                            response = final_response.output().to_owned();
                                        }
                                    }
                                    Ok(MultiTurnStreamItem::CompletionCall(_)) => {
                                        if let Err(err) = record_iteration(
                                            &prompt.session_id,
                                            &mut events,
                                            &mut thought,
                                            &mut response,
                                        ) {
                                            return responder.respond_with_error(err);
                                        }
                                        completed_iteration = true;
                                    }
                                    Ok(MultiTurnStreamItem::ModelTurnRetried { .. }) => {
                                        thought.clear();
                                        response.clear();
                                        events.clear();
                                    }
                                    Ok(_) => {}
                                    Err(err) => {
                                        return responder
                                            .respond_with_error(Error::into_internal_error(err));
                                    }
                                }
                            }
                        } else {
                            true
                        };

                        if let Err(err) = record_iteration(
                            &prompt.session_id,
                            &mut events,
                            &mut thought,
                            &mut response,
                        ) {
                            return responder.respond_with_error(err);
                        }

                        let stop_reason = if cancelled {
                            StopReason::Cancelled
                        } else {
                            StopReason::EndTurn
                        };
                        responder.respond(PromptResponse::new(stop_reason))
                    }
                    .await;

                    in_flight_prompts.finish(&prompt.session_id, &cancellation);
                    result
                })
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |cancel: CancelNotification, _connection| {
                in_flight_prompts.cancel(&cancel.session_id);
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(Stdio::new())
        .await
}

#[cfg(test)]
mod tests {
    use super::*;
    use agent_client_protocol::schema::{
        ProtocolVersion,
        v1::{AuthCapabilities, ClientCapabilities, ErrorCode},
    };

    fn event(kind: sessions::EventKind) -> sessions::Event {
        sessions::Event {
            ts: "2026-09-18T12:00:00.000Z".to_owned(),
            kind,
        }
    }

    #[test]
    fn terminal_login_is_advertised_only_to_supporting_clients() {
        let response = initialize_response(&InitializeRequest::new(ProtocolVersion::V1));
        assert!(response.auth_methods.is_empty());

        let initialize = InitializeRequest::new(ProtocolVersion::V1).client_capabilities(
            ClientCapabilities::new().auth(AuthCapabilities::new().terminal(true)),
        );
        let response = initialize_response(&initialize);

        assert!(response.agent_capabilities.auth.logout.is_some());
        assert!(matches!(
            &response.auth_methods[..],
            [AuthMethod::Terminal(method)]
                if method.id.to_string() == "openrouter"
                    && method.args == ["auth", "login"]
        ));
    }

    #[test]
    fn missing_credentials_require_authentication() {
        let agents = Arc::new(Mutex::new(None));
        let error = match current_agent(&agents) {
            Ok(_) => panic!("missing credentials should require authentication"),
            Err(error) => error,
        };
        assert_eq!(error.code, ErrorCode::AuthRequired);
    }

    #[test]
    fn a_second_prompt_for_a_session_is_rejected_until_the_first_finishes() {
        let prompts = InFlightPrompts::default();
        let session_id = SessionId::new("session");
        let first = prompts.begin(session_id.clone()).unwrap();

        assert!(prompts.begin(session_id.clone()).is_none());

        prompts.cancel(&session_id);

        futures::executor::block_on(first.cancelled());
        assert!(prompts.begin(session_id.clone()).is_none());
        prompts.finish(&session_id, &first);
        assert!(
            prompts
                .0
                .lock()
                .expect("in-flight prompts mutex poisoned")
                .get(&session_id)
                .is_none()
        );
        assert!(prompts.begin(session_id).is_some());
    }

    #[test]
    fn prompt_text_keeps_resource_links() {
        let content = text_content(&[
            ContentBlock::Text(TextContent::new("Review this")),
            ContentBlock::ResourceLink(agent_client_protocol::schema::v1::ResourceLink::new(
                "src/main.rs",
                "file:///workspace/src/main.rs",
            )),
        ]);

        assert_eq!(
            content,
            "Review this\nResource link: src/main.rs\nURI: file:///workspace/src/main.rs"
        );
    }

    #[test]
    fn history_preserves_user_reasoning_and_reply_roles() {
        let history = session_history_from_events(vec![
            event(sessions::EventKind::UserMessage(
                "What is two plus two?".to_owned(),
            )),
            event(sessions::EventKind::AgentThought(
                "Add the numbers. ".to_owned(),
            )),
            event(sessions::EventKind::AgentThought(
                "The result is four.".to_owned(),
            )),
            event(sessions::EventKind::AgentMessage("Four.".to_owned())),
        ])
        .unwrap();

        assert!(matches!(
            &history[0],
            Message::User { content }
                if matches!(&content[..], [UserContent::Text(text)] if text.text == "What is two plus two?")
        ));
        assert!(matches!(
            &history[1],
            Message::Assistant { content, .. }
                if matches!(
                    &content[..],
                    [AssistantContent::Reasoning(reasoning), AssistantContent::Text(text)]
                        if reasoning.display_text() == "Add the numbers. The result is four."
                            && text.text == "Four."
                )
        ));
    }

    #[test]
    fn updates_replay_the_transcript() {
        let updates = session_updates(vec![
            event(sessions::EventKind::UserMessage("Hello".to_owned())),
            event(sessions::EventKind::AgentThought("Thinking".to_owned())),
            event(sessions::EventKind::AgentMessage("Hi".to_owned())),
        ])
        .unwrap();

        assert!(matches!(updates[0], SessionUpdate::UserMessageChunk(_)));
        assert!(matches!(updates[1], SessionUpdate::AgentThoughtChunk(_)));
        assert!(matches!(updates[2], SessionUpdate::AgentMessageChunk(_)));
    }
}
