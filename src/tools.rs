//! The concrete tool set: schemas sent to the model, tool call titles, and
//! execution of one complete call.

use std::path::{Path, PathBuf};

use serde::Deserialize;
use serde_json::Value;

use crate::{
    sessions::{ToolCall, ToolOutcome},
    shell_processes::ShellProcesses,
};

mod patch;
mod read;
mod search;
mod shell;
mod workspace;

pub use shell::Permission;

/// What running and classifying one call needs from the agent that made it.
pub struct ToolContext {
    pub workspace_path: PathBuf,
    /// The active session's shell processes, which outlive the call.
    pub shell_processes: ShellProcesses,
}

pub const APPLY_PATCH: &str = "apply_patch";
pub const READ_FILE: &str = "read_file";
pub const GLOB: &str = "glob";
pub const SHELL: &str = "shell";
pub const SHELL_PROCESS: &str = "shell_process";
pub const GREP: &str = "grep";
const OUTPUT_LIMIT: usize = 16 * 1024;
// Leave room for line numbers, continuation instructions, and truncation notices.
const BODY_LIMIT: usize = OUTPUT_LIMIT - 256;
// Long enough to name a path or pattern, short enough for one display line.
const MAX_TOOL_CALL_TITLE_CHARS: usize = 80;

fn truncate(text: &mut String, limit: usize) {
    let mut end = text.len().min(limit);
    while !text.is_char_boundary(end) {
        end -= 1;
    }
    text.truncate(end);
}

fn bounded_result(result: Result<String, String>) -> ToolOutcome {
    match result {
        Ok(text) => {
            assert!(text.len() <= OUTPUT_LIMIT, "tool output exceeds its limit");
            ToolOutcome::Completed(text)
        }
        Err(mut error) => {
            if error.len() > OUTPUT_LIMIT {
                truncate(&mut error, BODY_LIMIT);
                error.push_str("\nError truncated.");
            }
            ToolOutcome::Failed(error)
        }
    }
}

/// Every tool definition sent to OpenRouter, each owned by its tool's module.
pub fn schemas() -> Vec<Value> {
    vec![
        shell::schema(),
        shell::process_schema(),
        read::schema(),
        search::glob_schema(),
        search::grep_schema(),
        patch::schema(),
    ]
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PatchArgs {
    patch: String,
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
        APPLY_PATCH => "Apply patch".to_owned(),
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
        APPLY_PATCH => match patch::changed_paths(argument("patch")?).as_slice() {
            [] => None,
            [path] => Some(format!("Apply patch to {path}")),
            paths => Some(format!("Apply patch to {} files", paths.len())),
        },
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
        SHELL => shell::command_permission(&call.arguments),
        SHELL_PROCESS => shell::process_permission(&call.arguments, &context.shell_processes),
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
                &call.arguments,
                cancelled,
            )
            .await;
        }
        SHELL_PROCESS => {
            return shell::execute_process(&context.shell_processes, &call.arguments, cancelled)
                .await;
        }
        _ => {}
    }
    tokio::select! {
        biased;
        outcome = execute_other(workspace_path, call) => outcome,
        () = cancelled => ToolOutcome::Cancelled(
            "Cancelled while this tool was running; no result was observed.".to_owned(),
        ),
    }
}

async fn execute_other(workspace_path: &Path, call: &ToolCall) -> ToolOutcome {
    match call.name.as_str() {
        READ_FILE => bounded_result(read::execute(workspace_path, &call.arguments).await),
        GLOB | GREP => {
            bounded_result(search::execute(workspace_path, &call.name, &call.arguments).await)
        }
        APPLY_PATCH => match serde_json::from_str::<PatchArgs>(&call.arguments) {
            Ok(args) => match patch::apply(workspace_path, &args.patch) {
                Ok(summary) => ToolOutcome::Completed(summary),
                Err(error) => ToolOutcome::Failed(error),
            },
            Err(error) => ToolOutcome::Failed(format!("arguments: {error}")),
        },
        other => ToolOutcome::Failed(format!("Unknown tool: {other}")),
    }
}

#[cfg(test)]
pub(crate) mod fixture {
    use std::{fs, path::PathBuf};

    pub struct Workspace(pub PathBuf);

    impl Workspace {
        pub fn new() -> Self {
            let path = std::env::temp_dir().join(format!("ox-patch-{}", uuid::Uuid::new_v4()));
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

    async fn execute(workspace: &Path, call: &ToolCall) -> ToolOutcome {
        let context = ToolContext {
            workspace_path: workspace.to_path_buf(),
            shell_processes: ShellProcesses::default(),
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
                assert!(matches!(
                    execute(&workspace.0, &call(name, args)).await,
                    ToolOutcome::Failed(_)
                ));
            }
            for path in ["../file", "/etc/passwd", "", "missing"] {
                let args = if name == READ_FILE {
                    json!({"path":path})
                } else {
                    json!({"path":path,"pattern":"*"})
                };
                assert!(matches!(
                    execute(&workspace.0, &call(name, &args.to_string())).await,
                    ToolOutcome::Failed(_)
                ));
            }
            let schema = schemas()
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
            assert!(matches!(
                execute(&workspace.0, &call(READ_FILE, &args.to_string())).await,
                ToolOutcome::Failed(_)
            ));
        }
        assert!(matches!(
            execute(
                &workspace.0,
                &call(GLOB, r#"{"path":"file","pattern":"*"}"#)
            )
            .await,
            ToolOutcome::Failed(_)
        ));
        let args = json!({"path":"雪".repeat(OUTPUT_LIMIT)}).to_string();
        let result = execute(&workspace.0, &call(READ_FILE, &args)).await;
        assert!(matches!(result, ToolOutcome::Failed(_)));
        assert!(result.text().len() <= OUTPUT_LIMIT);
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
            assert!(matches!(
                execute(&workspace.0, &call(name, &args.to_string())).await,
                ToolOutcome::Failed(_)
            ));
        }
        for name in [READ_FILE, GREP] {
            let args = if name == READ_FILE {
                json!({"path":"alias"})
            } else {
                json!({"path":"alias","pattern":"inside"})
            };
            assert!(matches!(
                execute(&workspace.0, &call(name, &args.to_string())).await,
                ToolOutcome::Completed(_)
            ));
        }
        for (name, pattern) in [(GLOB, "*"), (GREP, "inside|outside")] {
            let result = execute(
                &workspace.0,
                &call(name, &json!({"pattern":pattern}).to_string()),
            )
            .await;
            assert!(matches!(result, ToolOutcome::Completed(_)));
            assert!(!result.text().contains("escape"));
            assert!(!result.text().contains("alias"));
        }

        let pinned = workspace::Workspace::open(&workspace.0).unwrap();
        let file = pinned
            .resolve_allowing_link_target(Path::new("file"))
            .unwrap();
        std::fs::remove_file(workspace.0.join("file")).unwrap();
        std::os::unix::fs::symlink(outside.0.join("file"), workspace.0.join("file")).unwrap();
        assert!(pinned.read_file(&file).is_err());
    }

    #[tokio::test]
    async fn tool_schemas_and_patch_argument_errors() {
        let schemas = schemas();
        assert_eq!(
            schemas
                .iter()
                .map(|schema| schema["function"]["name"].as_str().unwrap())
                .collect::<Vec<_>>(),
            [SHELL, SHELL_PROCESS, READ_FILE, GLOB, GREP, APPLY_PATCH]
        );
        let schema = schemas
            .iter()
            .find(|schema| schema["function"]["name"] == APPLY_PATCH)
            .unwrap();
        assert_eq!(
            schema["function"]["parameters"]["required"],
            json!(["patch"])
        );
        assert_eq!(
            schema["function"]["parameters"]["additionalProperties"],
            false
        );
        for arguments in ["{", "{}", r#"{"patch": 1}"#, r#"{"patch": "", "cwd": "/"}"#] {
            let call = call(APPLY_PATCH, arguments);
            assert!(
                matches!(execute(Path::new("/unused"), &call).await, ToolOutcome::Failed(error) if error.starts_with("arguments:"))
            );
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
                APPLY_PATCH,
                json!({"patch":"*** Begin Patch\n*** Delete File: src/old.rs\n*** End Patch\n"}),
                "Apply patch to src/old.rs",
            ),
            (
                APPLY_PATCH,
                json!({"patch":"*** Begin Patch\n*** Delete File: a\n*** Delete File: b\n*** End Patch\n"}),
                "Apply patch to 2 files",
            ),
            (
                APPLY_PATCH,
                json!({"patch":"*** Begin Patch\n"}),
                "Apply patch",
            ),
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
    async fn unknown_tools_fail_as_results_and_keep_their_name_as_tool_call_title() {
        assert_eq!(
            execute(Path::new("/workspace"), &call("launch", "{}")).await,
            ToolOutcome::Failed("Unknown tool: launch".to_owned())
        );
        assert_eq!(tool_call_title(&call("launch", "{}")), "launch");
    }
}
