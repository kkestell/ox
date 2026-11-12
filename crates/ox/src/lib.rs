mod conversation;

use std::collections::BTreeMap;
use std::fmt::Display;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use ox_protocol::schema::{ContentBlock, TextContent};
use ox_protocol::{
    Branches, Checkout, Client, ConfigValue, Deleted, Error, SessionInfo, WorkspaceInfo, code,
    content, default_socket_path,
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

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
enum Agent {
    Claude,
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
/// replacing the previous view; with no session, ends the previous view. The
/// view ends when the session closes or the connection drops, and a view that
/// cannot connect or subscribe sends nothing. The workspace watch reports the
/// connection, and the webview views the session again once the watch
/// reconnects.
#[tauri::command]
#[expect(
    clippy::needless_pass_by_value,
    reason = "Tauri passes managed state to commands as `State` by value"
)]
fn view_session(
    handle: AppHandle,
    app: State<'_, App>,
    session_id: Option<String>,
    channel: Channel<Item>,
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
            while let Ok(event) = events.next().await {
                for item in conversation.apply(&event) {
                    if channel.send(item).is_err() {
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

/// The workspace's branches, after fetching its remotes when `fetch` is set.
#[tauri::command]
async fn workspace_branches(
    app: State<'_, App>,
    workspace_id: String,
    fetch: bool,
) -> Result<Branches, String> {
    app.client()
        .await?
        .branches(&workspace_id, fetch, utf8_env())
        .await
        .map_err(message)
}

#[tauri::command]
async fn new_session(
    app: State<'_, App>,
    agent: Agent,
    workspace_id: String,
    checkout: Checkout,
) -> Result<SessionInfo, String> {
    let command = match agent {
        Agent::Claude => {
            let bridge = std::env::current_exe()
                .map_err(message)?
                .with_file_name("claude-code-acp");
            vec![bridge.to_string_lossy().into_owned()]
        }
    };
    app.client()
        .await?
        .new_session(&workspace_id, command, utf8_env(), checkout)
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

/// The outcome of deleting a session.
#[derive(Debug, PartialEq, Eq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum Deletion {
    Deleted {
        kept_branch: Option<String>,
    },
    /// The session's worktree has work the delete would lose; `message`
    /// lists it.
    UnsavedWork {
        message: String,
    },
}

/// Deletes a session, giving up its worktree's unsaved work if `discard`.
#[tauri::command]
async fn delete_session(
    app: State<'_, App>,
    session_id: String,
    discard: bool,
) -> Result<Deletion, String> {
    deletion(
        app.client()
            .await?
            .delete(&session_id, discard, utf8_env())
            .await,
    )
}

/// The outcome of a `session.delete` answer. A session that is already gone
/// counts as deleted.
fn deletion(answer: Result<Deleted, Error>) -> Result<Deletion, String> {
    match answer {
        Ok(Deleted { kept_branch }) => Ok(Deletion::Deleted { kept_branch }),
        Err(Error::Remote(error)) if error.code == code::UNSAVED_WORK => {
            Ok(Deletion::UnsavedWork {
                message: error.message,
            })
        }
        Err(Error::Remote(error)) if error.code == code::NOT_FOUND => {
            Ok(Deletion::Deleted { kept_branch: None })
        }
        Err(error) => Err(message(error)),
    }
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
            workspace_branches,
            new_session,
            load_session,
            prompt,
            cancel,
            permit,
            set_config_option,
            delete_session,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

#[cfg(test)]
mod tests {
    use ox_protocol::ErrorBody;

    use super::*;

    fn remote(code: i32, message: &str) -> Result<Deleted, Error> {
        Err(Error::Remote(ErrorBody {
            code,
            message: message.into(),
        }))
    }

    #[test]
    fn deletion_maps_the_answer() {
        assert_eq!(
            deletion(Ok(Deleted {
                kept_branch: Some("ox/1".into())
            })),
            Ok(Deletion::Deleted {
                kept_branch: Some("ox/1".into())
            })
        );
        assert_eq!(
            deletion(remote(code::UNSAVED_WORK, "untracked a.txt")),
            Ok(Deletion::UnsavedWork {
                message: "untracked a.txt".into()
            })
        );
        assert_eq!(
            deletion(remote(code::NOT_FOUND, "no session s")),
            Ok(Deletion::Deleted { kept_branch: None })
        );
        assert_eq!(
            deletion(remote(code::GIT, "worktree is locked")),
            Err("worktree is locked".into())
        );
        assert_eq!(
            deletion(Err(Error::Closed)),
            Err("connection to oxd closed".into())
        );
    }
}
