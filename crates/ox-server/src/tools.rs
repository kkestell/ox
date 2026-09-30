//! The concrete tool set: schemas sent to the model, tool call titles, and
//! execution of one complete call.

use std::path::{Path, PathBuf};

use agent_client_protocol::schema::v1::SessionId;
use serde_json::Value;

use crate::{
    sessions::{ToolCall, ToolContent, ToolOutcome},
    shell_processes::ShellProcesses,
    subagents::Subagents,
};

mod file;
mod read;
mod search;
mod shell;
mod subagent;
mod workspace;

pub use shell::Permission;

/// Which tools an agent has. The main agent also coordinates subagents,
/// which have only the workspace and shell tools.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Role {
    Main,
    Subagent,
}

/// What running and classifying one call needs from the agent that made it.
pub struct ToolContext {
    pub workspace_path: PathBuf,
    /// The agent session ID of the agent that made the call, which scopes the
    /// shell processes it can start and reach.
    pub session_id: SessionId,
    /// The active session's shell processes, which outlive the call. Each
    /// agent reaches only the ones it started.
    pub shell_processes: ShellProcesses,
    /// The main agent's subagents; `None` for a subagent.
    pub subagents: Option<Subagents>,
}

pub const WRITE_FILE: &str = "write_file";
pub const EDIT_FILE: &str = "edit_file";
pub const READ_FILE: &str = "read_file";
pub const GLOB: &str = "glob";
pub const SHELL: &str = "shell";
pub const SHELL_PROCESS: &str = "shell_process";
pub const GREP: &str = "grep";
pub const START_SUBAGENT: &str = "start_subagent";
pub const SEND_MESSAGE: &str = "send_message";
pub const STOP_SUBAGENT: &str = "stop_subagent";
pub const WAIT: &str = "wait";
pub(crate) const OUTPUT_LIMIT: usize = 16 * 1024;
// Leave room for line numbers, continuation instructions, and truncation notices.
pub(crate) const BODY_LIMIT: usize = OUTPUT_LIMIT - 256;
// Long enough to name a path or pattern, short enough for one display line.
const MAX_TOOL_CALL_TITLE_CHARS: usize = 80;

pub(crate) fn truncate(text: &mut String, limit: usize) {
    let mut end = text.len().min(limit);
    while !text.is_char_boundary(end) {
        end -= 1;
    }
    text.truncate(end);
}

/// The outcome of a tool that returns the model's text and the client's
/// content, or an error.
fn bounded_result(result: Result<(String, Vec<ToolContent>), String>) -> ToolOutcome {
    match result {
        Ok((text, content)) => {
            assert!(text.len() <= OUTPUT_LIMIT, "tool output exceeds its limit");
            ToolOutcome::completed(text).with_content(content)
        }
        Err(mut error) => {
            if error.len() > OUTPUT_LIMIT {
                truncate(&mut error, BODY_LIMIT);
                error.push_str("\nError truncated.");
            }
            ToolOutcome::failed(error)
        }
    }
}

/// The outcome of a tool that returns only the model's text, or an error.
fn bounded_text(result: Result<String, String>) -> ToolOutcome {
    bounded_result(result.map(|text| (text, Vec::new())))
}

/// Every tool definition sent to OpenRouter for an agent with `role`, each
/// owned by its tool's module.
pub fn schemas(role: Role) -> Vec<Value> {
    let mut schemas = vec![
        shell::schema(),
        shell::process_schema(),
        read::schema(),
        search::glob_schema(),
        search::grep_schema(),
        file::write_schema(),
        file::edit_schema(),
    ];
    if role == Role::Main {
        schemas.extend([
            subagent::start_schema(),
            subagent::send_schema(),
            subagent::stop_schema(),
            subagent::wait_schema(),
        ]);
    }
    schemas
}

/// What the ACP client shows for one call. Arguments come from the model and
/// may be missing or malformed, so a call that cannot be described by its
/// arguments falls back to naming its tool alone.
pub fn tool_call_title(call: &ToolCall) -> String {
    match describe(call) {
        Some(description) => shorten(&description),
        None => default_tool_call_title(call),
    }
}

fn default_tool_call_title(call: &ToolCall) -> String {
    match call.name.as_str() {
        SHELL => "Run shell command".to_owned(),
        SHELL_PROCESS => "Use shell process".to_owned(),
        READ_FILE => "Read file".to_owned(),
        GLOB => "Find files".to_owned(),
        GREP => "Search file contents".to_owned(),
        WRITE_FILE => "Write file".to_owned(),
        EDIT_FILE => "Edit file".to_owned(),
        START_SUBAGENT => "Start subagent".to_owned(),
        SEND_MESSAGE => "Message subagent".to_owned(),
        STOP_SUBAGENT => "Stop subagent".to_owned(),
        WAIT => "Wait for subagents".to_owned(),
        other => other.to_owned(),
    }
}

/// What one call does, in the terms its arguments give. Nothing when the
/// arguments do not parse or omit the part that would name the work.
fn describe(call: &ToolCall) -> Option<String> {
    let arguments: Value = serde_json::from_str(&call.arguments).ok()?;
    let argument = |name: &str| arguments.get(name).and_then(Value::as_str);
    match call.name.as_str() {
        SHELL => {
            let command = command_line(argument("command")?)?;
            Some(match arguments.get("background").and_then(Value::as_bool) {
                Some(true) => format!("Background: {command}"),
                _ => command,
            })
        }
        SHELL_PROCESS => {
            let process = || argument("process_id");
            Some(match argument("action")? {
                "list" => "List shell processes".to_owned(),
                "read" => format!("Read shell process {}", process()?),
                "write" => format!("Write to shell process {}", process()?),
                "stop" => format!("Stop shell process {}", process()?),
                _ => return None,
            })
        }
        READ_FILE => Some(format!("Read {}", argument("path")?)),
        GLOB => {
            let pattern = argument("pattern")?;
            Some(match searched_path(argument("path")) {
                Some(path) => format!("Find files matching {pattern} in {path}"),
                None => format!("Find files matching {pattern}"),
            })
        }
        GREP => {
            let mut description = format!("Search for {}", argument("pattern")?);
            if let Some(path) = searched_path(argument("path")) {
                description.push_str(&format!(" in {path}"));
            }
            if let Some(glob) = argument("glob") {
                description.push_str(&format!(" (files matching {glob})"));
            }
            Some(description)
        }
        WRITE_FILE => Some(format!("Write {}", argument("path")?)),
        EDIT_FILE => Some(format!("Edit {}", argument("path")?)),
        START_SUBAGENT => Some(format!(
            "Start subagent: {}",
            command_line(argument("prompt")?)?
        )),
        SEND_MESSAGE => Some(format!("Message subagent {}", argument("subagent_id")?)),
        STOP_SUBAGENT => Some(format!("Stop subagent {}", argument("subagent_id")?)),
        WAIT => {
            let seconds = arguments.get("seconds").and_then(Value::as_f64)?;
            Some(format!("Wait up to {seconds} seconds for subagents"))
        }
        _ => None,
    }
}

/// The part of the workspace a search covers, or nothing when it covers the
/// whole workspace and so says nothing useful.
fn searched_path(path: Option<&str>) -> Option<&str> {
    match path {
        None | Some("" | "." | "./") => None,
        Some(path) => Some(path),
    }
}

/// A command's first nonblank line, marked when more of the command follows.
fn command_line(command: &str) -> Option<String> {
    let mut lines = command
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty());
    let first = lines.next()?;
    Some(if lines.next().is_some() {
        format!("{first} …")
    } else {
        first.to_owned()
    })
}

/// A tool call title of at most `MAX_TOOL_CALL_TITLE_CHARS` characters,
/// counting the ellipsis that marks a shortened one.
fn shorten(tool_call_title: &str) -> String {
    if tool_call_title.chars().count() <= MAX_TOOL_CALL_TITLE_CHARS {
        return tool_call_title.to_owned();
    }
    let kept: String = tool_call_title
        .chars()
        .take(MAX_TOOL_CALL_TITLE_CHARS - 1)
        .collect();
    format!("{}…", kept.trim_end())
}

/// What Ask mode must request before `call` runs, classified by the same
/// argument validation its execution uses.
pub fn permission(context: &ToolContext, call: &ToolCall) -> Permission {
    match call.name.as_str() {
        // Every shell call needs permission, even one whose arguments are invalid.
        SHELL => Permission::Command,
        SHELL_PROCESS => shell::process_permission(
            &call.arguments,
            &context.shell_processes,
            &context.session_id,
        ),
        _ => Permission::NotRequired,
    }
}

/// Unknown names and invalid arguments are failed results the model can read
/// on its next request, not errors that end the prompt. Shell tools handle
/// cancellation themselves, so they can report a partial write or finish a
/// stop's cleanup.
pub async fn execute(
    context: &ToolContext,
    call: &ToolCall,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome {
    let workspace_path = &context.workspace_path;
    match call.name.as_str() {
        SHELL => {
            return shell::execute(
                workspace_path,
                &context.shell_processes,
                &context.session_id,
                &call.arguments,
                cancelled,
            )
            .await;
        }
        SHELL_PROCESS => {
            return shell::execute_process(
                &context.shell_processes,
                &context.session_id,
                &call.arguments,
                cancelled,
            )
            .await;
        }
        // A stop finishes even if the prompt is cancelled; a wait observes
        // cancellation itself.
        START_SUBAGENT | SEND_MESSAGE | STOP_SUBAGENT | WAIT => {
            return subagent::execute(
                context.subagents.as_ref(),
                &call.name,
                &call.arguments,
                cancelled,
            )
            .await;
        }
        _ => {}
    }
    tokio::select! {
        biased;
        outcome = execute_other(workspace_path, call) => outcome,
        () = cancelled => ToolOutcome::cancelled(
            "Cancelled while this tool was running; no result was observed.",
        ),
    }
}

async fn execute_other(workspace_path: &Path, call: &ToolCall) -> ToolOutcome {
    match call.name.as_str() {
        READ_FILE => bounded_result(read::execute(workspace_path, &call.arguments).await),
        GLOB | GREP => {
            bounded_result(search::execute(workspace_path, &call.name, &call.arguments).await)
        }
        WRITE_FILE => bounded_result(file::write(workspace_path, &call.arguments)),
        EDIT_FILE => bounded_result(file::edit(workspace_path, &call.arguments)),
        other => ToolOutcome::failed(format!("Unknown tool: {other}")),
    }
}

#[cfg(test)]
pub(crate) mod fixture {
    use std::{fs, path::PathBuf};

    pub struct Workspace(pub PathBuf);

    impl Workspace {
        pub fn new() -> Self {
            let path = std::env::temp_dir().join(format!("ox-tools-{}", uuid::Uuid::new_v4()));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for Workspace {
        fn drop(&mut self) {
            fs::remove_dir_all(&self.0).unwrap();
        }
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;
    use crate::sessions::ToolStatus;

    async fn execute(workspace: &Path, call: &ToolCall) -> ToolOutcome {
        let context = ToolContext {
            workspace_path: workspace.to_path_buf(),
            session_id: SessionId::new("session"),
            shell_processes: ShellProcesses::default(),
            subagents: None,
        };
        super::execute(&context, call, std::future::pending()).await
    }

    fn call(name: &str, arguments: &str) -> ToolCall {
        ToolCall {
            call_id: "call-1".to_owned(),
            name: name.to_owned(),
            arguments: arguments.to_owned(),
        }
    }

    #[tokio::test]
    async fn read_tools_validate_arguments_paths_and_bound_errors() {
        let workspace = fixture::Workspace::new();
        std::fs::write(workspace.0.join("file"), "text").unwrap();
        for name in [READ_FILE, GLOB, GREP] {
            for args in [
                "{",
                "{}",
                r#"{"path":2,"pattern":2}"#,
                r#"{"path":"file","pattern":"*","extra":true}"#,
            ] {
                assert_eq!(
                    execute(&workspace.0, &call(name, args)).await.status,
                    ToolStatus::Failed
                );
            }
            for path in ["../file", "/etc/passwd", "", "missing"] {
                let args = if name == READ_FILE {
                    json!({"path":path})
                } else {
                    json!({"path":path,"pattern":"*"})
                };
                assert_eq!(
                    execute(&workspace.0, &call(name, &args.to_string()))
                        .await
                        .status,
                    ToolStatus::Failed
                );
            }
            let schema = schemas(Role::Subagent)
                .into_iter()
                .find(|s| s["function"]["name"] == name)
                .unwrap();
            assert_eq!(
                schema["function"]["parameters"]["additionalProperties"],
                false
            );
        }
        for args in [
            json!({"path":"file","offset":0}),
            json!({"path":"file","limit":0}),
            json!({"path":"file","limit":1001}),
            json!({"path":"file","offset":-1}),
            json!({"path":"."}),
        ] {
            assert_eq!(
                execute(&workspace.0, &call(READ_FILE, &args.to_string()))
                    .await
                    .status,
                ToolStatus::Failed
            );
        }
        assert_eq!(
            execute(
                &workspace.0,
                &call(GLOB, r#"{"path":"file","pattern":"*"}"#)
            )
            .await
            .status,
            ToolStatus::Failed
        );
        let args = json!({"path":"雪".repeat(OUTPUT_LIMIT)}).to_string();
        let result = execute(&workspace.0, &call(READ_FILE, &args)).await;
        assert_eq!(result.status, ToolStatus::Failed);
        assert!(result.text.len() <= OUTPUT_LIMIT);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn read_tools_contain_symlinks_and_do_not_follow_them_recursively() {
        use std::os::unix::fs::symlink;
        let workspace = fixture::Workspace::new();
        let outside = fixture::Workspace::new();
        std::fs::write(workspace.0.join("file"), "inside").unwrap();
        std::fs::write(outside.0.join("file"), "outside").unwrap();
        symlink(&outside.0, workspace.0.join("escape")).unwrap();
        symlink(workspace.0.join("file"), workspace.0.join("alias")).unwrap();
        for name in [READ_FILE, GLOB, GREP] {
            let args = if name == READ_FILE {
                json!({"path":"escape/file"})
            } else {
                json!({"path":"escape","pattern":"*"})
            };
            assert_eq!(
                execute(&workspace.0, &call(name, &args.to_string()))
                    .await
                    .status,
                ToolStatus::Failed
            );
        }
        for name in [READ_FILE, GREP] {
            let args = if name == READ_FILE {
                json!({"path":"alias"})
            } else {
                json!({"path":"alias","pattern":"inside"})
            };
            assert_eq!(
                execute(&workspace.0, &call(name, &args.to_string()))
                    .await
                    .status,
                ToolStatus::Completed
            );
        }
        for (name, pattern) in [(GLOB, "*"), (GREP, "inside|outside")] {
            let result = execute(
                &workspace.0,
                &call(name, &json!({"pattern":pattern}).to_string()),
            )
            .await;
            assert_eq!(result.status, ToolStatus::Completed);
            assert!(!result.text.contains("escape"));
            assert!(!result.text.contains("alias"));
        }

        let pinned = workspace::Workspace::open(&workspace.0).unwrap();
        let file = pinned
            .resolve_allowing_link_target(Path::new("file"))
            .unwrap();
        std::fs::remove_file(workspace.0.join("file")).unwrap();
        std::os::unix::fs::symlink(outside.0.join("file"), workspace.0.join("file")).unwrap();
        assert!(pinned.read_file(&file).is_err());
    }

    #[test]
    fn tool_schemas_register_the_main_and_subagent_tools() {
        let names = |role| {
            schemas(role)
                .into_iter()
                .map(|schema| schema["function"]["name"].as_str().unwrap().to_owned())
                .collect::<Vec<_>>()
        };
        assert_eq!(
            names(Role::Subagent),
            [
                SHELL,
                SHELL_PROCESS,
                READ_FILE,
                GLOB,
                GREP,
                WRITE_FILE,
                EDIT_FILE
            ]
        );
        assert_eq!(
            names(Role::Main),
            [
                SHELL,
                SHELL_PROCESS,
                READ_FILE,
                GLOB,
                GREP,
                WRITE_FILE,
                EDIT_FILE,
                START_SUBAGENT,
                SEND_MESSAGE,
                STOP_SUBAGENT,
                WAIT
            ]
        );
        let schemas = schemas(Role::Main);
        for schema in &schemas {
            assert_eq!(
                schema["function"]["parameters"]["additionalProperties"],
                false
            );
        }
        for (name, required) in [
            (WRITE_FILE, json!(["path", "content"])),
            (EDIT_FILE, json!(["path", "old_text", "new_text"])),
        ] {
            let schema = schemas
                .iter()
                .find(|schema| schema["function"]["name"] == name)
                .unwrap();
            assert_eq!(schema["function"]["parameters"]["required"], required);
        }
    }

    #[test]
    fn tool_call_titles_describe_the_call_and_fall_back_to_the_tool_name() {
        let long_path = "a".repeat(200);
        for (name, arguments, expected) in [
            (SHELL, json!({"command":"cargo test"}), "cargo test"),
            (
                SHELL,
                json!({"command":"\n  cargo build\ncargo test\n"}),
                "cargo build …",
            ),
            (SHELL, json!({"command":"  \n"}), "Run shell command"),
            (SHELL, json!({"timeout_seconds":5}), "Run shell command"),
            (
                SHELL,
                json!({"command":"npm run dev","background":true}),
                "Background: npm run dev",
            ),
            (
                SHELL,
                json!({"command":"npm test","background":false}),
                "npm test",
            ),
            (
                SHELL_PROCESS,
                json!({"action":"list"}),
                "List shell processes",
            ),
            (
                SHELL_PROCESS,
                json!({"action":"read","process_id":"p-1","wait_seconds":5}),
                "Read shell process p-1",
            ),
            (
                SHELL_PROCESS,
                json!({"action":"write","process_id":"p-1","text":"y\n"}),
                "Write to shell process p-1",
            ),
            (
                SHELL_PROCESS,
                json!({"action":"stop","process_id":"p-1"}),
                "Stop shell process p-1",
            ),
            (SHELL_PROCESS, json!({"action":"stop"}), "Use shell process"),
            (
                SHELL_PROCESS,
                json!({"action":"restart","process_id":"p-1"}),
                "Use shell process",
            ),
            (READ_FILE, json!({"path":"src/main.rs"}), "Read src/main.rs"),
            (
                GLOB,
                json!({"pattern":"*.rs","path":"."}),
                "Find files matching *.rs",
            ),
            (
                GLOB,
                json!({"pattern":"*.rs","path":"src"}),
                "Find files matching *.rs in src",
            ),
            (GREP, json!({"pattern":"fn main"}), "Search for fn main"),
            (
                GREP,
                json!({"pattern":"fn main","path":"src","glob":"*.rs"}),
                "Search for fn main in src (files matching *.rs)",
            ),
            (
                WRITE_FILE,
                json!({"path":"src/new.rs", "content":""}),
                "Write src/new.rs",
            ),
            (
                EDIT_FILE,
                json!({"path":"src/main.rs", "old_text":"a", "new_text":"b"}),
                "Edit src/main.rs",
            ),
            (WRITE_FILE, json!({"content":""}), "Write file"),
            (EDIT_FILE, json!({"path":3}), "Edit file"),
            (
                START_SUBAGENT,
                json!({"prompt":"Fix the parser.\nThen run the tests."}),
                "Start subagent: Fix the parser. …",
            ),
            (
                SEND_MESSAGE,
                json!({"subagent_id":"s-1","message":"Yes."}),
                "Message subagent s-1",
            ),
            (
                STOP_SUBAGENT,
                json!({"subagent_id":"s-1"}),
                "Stop subagent s-1",
            ),
            (
                WAIT,
                json!({"seconds":2.5}),
                "Wait up to 2.5 seconds for subagents",
            ),
            (WAIT, json!({}), "Wait for subagents"),
            (
                READ_FILE,
                json!({"path": long_path}),
                "Read aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa…",
            ),
        ] {
            assert_eq!(
                tool_call_title(&call(name, &arguments.to_string())),
                expected
            );
        }
    }

    #[tokio::test]
    async fn coordination_calls_from_a_subagent_fail_as_results() {
        for (name, arguments) in [
            (START_SUBAGENT, json!({"prompt": "Nest."})),
            (SEND_MESSAGE, json!({"subagent_id": "a", "message": "Hi."})),
            (STOP_SUBAGENT, json!({"subagent_id": "a"})),
            (WAIT, json!({"seconds": 1})),
        ] {
            assert_eq!(
                execute(Path::new("/workspace"), &call(name, &arguments.to_string())).await,
                ToolOutcome::failed(format!("{name} is available only to the main agent."))
            );
        }
    }

    #[tokio::test]
    async fn unknown_tools_fail_as_results_and_keep_their_name_as_tool_call_title() {
        for name in ["launch", "apply_patch"] {
            assert_eq!(
                execute(Path::new("/workspace"), &call(name, "{}")).await,
                ToolOutcome::failed(format!("Unknown tool: {name}"))
            );
            assert_eq!(tool_call_title(&call(name, "{}")), name);
        }
    }
}
