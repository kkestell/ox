mod conversation;

use std::collections::BTreeMap;
use std::fmt::Display;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use ox_protocol::schema::{ContentBlock, TextContent};
use ox_protocol::{
    Client, ConfigValue, End, SessionInfo, WorkspaceInfo, content, default_socket_path,
};
use serde::{Deserialize, Serialize};
use tauri::async_runtime::JoinHandle;
use tauri::ipc::Channel;
use tauri::{AppHandle, Manager, State};

use crate::conversation::{Conversation, Item};

/// A task that stops when dropped.
struct Task(JoinHandle<()>);

impl Drop for Task {
    fn drop(&mut self) {
        self.0.abort();
    }
}

/// The app's connection to oxd and the tasks that forward its streams to
/// the webview.
#[derive(Default)]
struct App {
    client: tokio::sync::Mutex<Option<Arc<Client>>>,
    watch: Mutex<Option<Task>>,
    view: Mutex<Option<Task>>,
}

impl App {
    /// The open connection to oxd, connecting first, and starting oxd, if
    /// there is none.
    async fn client(&self) -> Result<Arc<Client>, String> {
        let mut client = self.client.lock().await;
        if let Some(client) = client.as_ref().filter(|client| !client.is_closed()) {
            return Ok(client.clone());
        }
        let connected = Arc::new(
            Client::connect_or_start(&default_socket_path())
                .await
                .map_err(message)?,
        );
        *client = Some(connected.clone());
        Ok(connected)
    }
}

#[derive(Clone, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum WatchMessage {
    List {
        workspaces: Vec<WorkspaceInfo>,
        sessions: Vec<SessionInfo>,
    },
    /// The watch could not start.
    Error {
        message: String,
    },
    Disconnected,
}

#[derive(Clone, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum ViewMessage {
    Item { item: Item },
    Disconnected,
}

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
enum Agent {
    Claude,
    Opencode,
}

// `watch` and `view_session` are synchronous, so they run on the
// main thread in the order the webview calls them, and the last call's task
// is the one that stays.

/// Sends the workspaces and sessions lists on `channel` each time either
/// changes, replacing the previous watch.
#[tauri::command]
#[expect(
    clippy::needless_pass_by_value,
    reason = "Tauri passes managed state to commands as `State` by value"
)]
fn watch(handle: AppHandle, app: State<'_, App>, channel: Channel<WatchMessage>) {
    let task = tauri::async_runtime::spawn(async move {
        let app = handle.state::<App>();
        let watch = match app.client().await {
            Ok(client) => client.watch().await.map_err(message),
            Err(error) => Err(error),
        };
        let mut watch = match watch {
            Ok(watch) => watch,
            Err(message) => {
                let _ = channel.send(WatchMessage::Error { message });
                return;
            }
        };
        loop {
            let Ok(listing) = watch.next().await else {
                let _ = channel.send(WatchMessage::Disconnected);
                return;
            };
            let list = WatchMessage::List {
                workspaces: listing.workspaces,
                sessions: listing.sessions,
            };
            if channel.send(list).is_err() {
                return;
            }
        }
    });
    *app.watch.lock().unwrap() = Some(Task(task));
}

/// Sends a session's conversation on `channel` from its first event,
/// replacing the previous view; with no session, ends the previous view. A
/// session that cannot be viewed, as one closed meanwhile, sends nothing.
#[tauri::command]
#[expect(
    clippy::needless_pass_by_value,
    reason = "Tauri passes managed state to commands as `State` by value"
)]
fn view_session(
    handle: AppHandle,
    app: State<'_, App>,
    session_id: Option<String>,
    channel: Channel<ViewMessage>,
) {
    let task = session_id.map(|session_id| {
        Task(tauri::async_runtime::spawn(async move {
            let app = handle.state::<App>();
            let Ok(client) = app.client().await else {
                return;
            };
            let Ok(mut events) = client.subscribe(&session_id, 0).await else {
                return;
            };
            let mut conversation = Conversation::default();
            loop {
                match events.next().await {
                    Ok(event) => {
                        if let Some(item) = conversation.apply(&event)
                            && channel.send(ViewMessage::Item { item }).is_err()
                        {
                            return;
                        }
                    }
                    Err(End::SessionClosed) => return,
                    Err(End::Disconnected) => {
                        let _ = channel.send(ViewMessage::Disconnected);
                        return;
                    }
                }
            }
        }))
    });
    *app.view.lock().unwrap() = task;
}

#[tauri::command]
async fn add_workspace(app: State<'_, App>, path: PathBuf) -> Result<WorkspaceInfo, String> {
    app.client()
        .await?
        .add_workspace(path)
        .await
        .map_err(message)
}

#[tauri::command]
async fn remove_workspace(app: State<'_, App>, workspace_id: String) -> Result<(), String> {
    app.client()
        .await?
        .remove_workspace(&workspace_id)
        .await
        .map_err(message)
}

#[tauri::command]
async fn new_session(
    app: State<'_, App>,
    agent: Agent,
    workspace_id: String,
) -> Result<SessionInfo, String> {
    let command = match agent {
        Agent::Claude => {
            let bridge = std::env::current_exe()
                .map_err(message)?
                .with_file_name("claude-code-acp");
            vec![bridge.to_string_lossy().into_owned()]
        }
        Agent::Opencode => vec!["opencode".into(), "acp".into()],
    };
    app.client()
        .await?
        .new_session(&workspace_id, command, utf8_env())
        .await
        .map_err(message)
}

/// Restarts the agent of an exited session, which replays its conversation.
#[tauri::command]
async fn load_session(app: State<'_, App>, session_id: String) -> Result<(), String> {
    app.client()
        .await?
        .load_session(&session_id, utf8_env())
        .await
        .map(drop)
        .map_err(message)
}

#[tauri::command]
async fn prompt(
    app: State<'_, App>,
    session_id: String,
    text: String,
    attachments: Vec<PathBuf>,
) -> Result<(), String> {
    let mut prompt = Vec::new();
    if !text.is_empty() {
        prompt.push(ContentBlock::Text(TextContent::new(text)));
    }
    for path in attachments {
        prompt.push(content::file_link(&path).map_err(message)?);
    }
    app.client()
        .await?
        .prompt(&session_id, prompt)
        .await
        .map_err(message)
}

#[tauri::command]
async fn cancel(app: State<'_, App>, session_id: String) -> Result<(), String> {
    app.client()
        .await?
        .cancel(&session_id)
        .await
        .map_err(message)
}

#[tauri::command]
async fn permit(
    app: State<'_, App>,
    session_id: String,
    request_id: String,
    option_id: String,
) -> Result<(), String> {
    app.client()
        .await?
        .permit(&session_id, &request_id, &option_id)
        .await
        .map_err(message)
}

#[tauri::command]
async fn set_config_option(
    app: State<'_, App>,
    session_id: String,
    config_id: String,
    value: ConfigValue,
) -> Result<(), String> {
    app.client()
        .await?
        .set_config_option(&session_id, &config_id, value)
        .await
        .map(drop)
        .map_err(message)
}

#[tauri::command]
async fn close_session(app: State<'_, App>, session_id: String) -> Result<(), String> {
    app.client()
        .await?
        .close(&session_id)
        .await
        .map_err(message)
}

fn message(error: impl Display) -> String {
    error.to_string()
}

/// The environment variables whose names and values are UTF-8.
fn utf8_env() -> BTreeMap<String, String> {
    std::env::vars_os()
        .filter_map(|(name, value)| Some((name.into_string().ok()?, value.into_string().ok()?)))
        .collect()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let builder = tauri::Builder::default().plugin(tauri_plugin_dialog::init());
    #[cfg(feature = "webdriver")]
    let builder = builder.plugin(tauri_plugin_wdio_webdriver::init());
    builder
        .manage(App::default())
        .invoke_handler(tauri::generate_handler![
            watch,
            view_session,
            add_workspace,
            remove_workspace,
            new_session,
            load_session,
            prompt,
            cancel,
            permit,
            set_config_option,
            close_session,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
