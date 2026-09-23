//! The concrete tool set: schemas sent to the model, tool call titles, and
//! execution of one complete call.

use std::path::Path;

use serde::Deserialize;
use serde_json::{Value, json};

use crate::sessions::{ToolCall, ToolOutcome};

mod patch;
mod read;
mod search;
mod shell;
mod workspace;

pub const APPLY_PATCH: &str = "apply_patch";
pub const READ_FILE: &str = "read_file";
pub const GLOB: &str = "glob";
pub const SHELL: &str = "shell";
pub const GREP: &str = "grep";
/// Every tool name in the concrete tool set.
pub const NAMES: [&str; 5] = [SHELL, READ_FILE, GLOB, GREP, APPLY_PATCH];

const OUTPUT_LIMIT: usize = 16 * 1024;
// Leave room for line numbers, continuation instructions, and truncation notices.
const BODY_LIMIT: usize = OUTPUT_LIMIT - 256;
// Long enough to name a path or pattern, short enough for one display line.
const MAX_TOOL_CALL_TITLE_CHARS: usize = 80;

const APPLY_PATCH_DESCRIPTION: &str = r#"Apply a text patch to files in the session workspace. Paths are relative to the workspace. Supports Add File, Update File, Delete File, and Move to.

```text
*** Begin Patch
*** Add File: notes.txt
+New notes.
*** Update File: src/greeting.rs
@@ fn greeting() -> &'static str {
-    "Hello"
+    "Hello, world"
 }
*** Update File: old-name.txt
*** Move to: new-name.txt
@@
-Old text
+New text
*** Delete File: obsolete.txt
*** End Patch
```

A patch contains zero or more file operations between the begin and end
markers. The markers and operation headers must appear exactly as shown at the
start of a line.

- `*** Add File: path` is followed by zero or more lines beginning with `+`.
  Removing that prefix gives the new file's contents.
- `*** Delete File: path` has no body.
- `*** Update File: path` is followed by one or more chunks. A move-only update
  may omit the chunks.
- `*** Move to: path` may appear immediately after an Update header. The update
  is written at the destination and the source is removed.
- A chunk starts with `@@` or `@@ anchor`. An anchor is a literal source line
  used to begin the search for the chunk; it is not a line number or regular
  expression.
- Within a chunk, a leading space is unchanged context, `-` removes a line, and
  `+` inserts a line. The prefix is syntax and is not part of the file content.
- Another operation header, chunk header, or the final marker ends the current
  body. Text outside the patch and unrecognized lines are errors.

Paths and payloads are literal. There is no quoting, escaping, heredoc support,
or standard unified-diff syntax inside the patch string. JSON escaping is
handled once by argument decoding and is not part of the patch language.

An empty patch succeeds without changing files. An Add with no body creates an
empty file; a nonempty Add ends with a newline. A source line that resembles a
marker remains expressible because it has a context, removal, or addition
prefix.

Match source lines exactly, including whitespace. Chunks search forward from the preceding match. An anchor is matched literally, and chunk matching starts after it. An additions-only chunk inserts after its anchor or preceding chunk; with neither, it appends. An updated file keeps the line-ending style of its first line and whether it ended with a newline. Add and Move destinations must not exist; their missing parent directories are created. Sources must be regular files; updates require UTF-8. Absolute paths, parent traversal, paths reaching outside the workspace, paths naming the workspace root or a symbolic link, and duplicate targets are rejected. A no-op Update succeeds and reports `Unchanged path`. All operations are checked before changes start. Filesystem failures may leave earlier operations applied; the result reports completed, failed, and unattempted operations.
"#;

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

pub fn schemas() -> Vec<Value> {
    vec![
        json!({
            "type": "function",
            "function": {
                "name": SHELL,
                "description": "Run a noninteractive /bin/sh command starting in the session workspace. Returns the exit status and tails of stdout and stderr, at most 16 KiB total. Output has a shared 14 KiB budget: 7 KiB per stream, with unused space given to the other stream. Earlier output may be omitted; redirect long logs to a workspace file for later inspection. Each call starts a fresh shell with stdin connected to /dev/null. No interactive input or persistent background processes. Commands run with Ox's permissions and can access paths outside the workspace.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "command": {
                            "type": "string",
                            "description": "Shell command or multiline script. Use shell syntax for directory changes, environment overrides, pipelines, and redirection."
                        },
                        "timeout_seconds": {
                            "type": "integer",
                            "minimum": 1,
                            "maximum": 600,
                            "default": 120,
                            "description": "Maximum execution time in seconds. Defaults to 120."
                        }
                    },
                    "required": [
                        "command"
                    ],
                    "additionalProperties": false
                }
            }
        }),
        json!({
            "type": "function",
            "function": {
                "name": READ_FILE,
                "description": "Read a UTF-8 text file inside the workspace. Returns numbered lines, at most 16 KiB, with the next offset when more remains. An oversized line returns a marked prefix; its omitted portion cannot be retrieved through line pagination. Example: {\"path\":\"src/main.rs\",\"offset\":1,\"limit\":100}.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "path": { "type": "string", "description": "Workspace-relative file path." },
                        "offset": { "type": "integer", "minimum": 1, "default": 1, "description": "1-based starting line." },
                        "limit": { "type": "integer", "minimum": 1, "maximum": 1000, "default": 200, "description": "Maximum number of lines to return." }
                    },
                    "required": ["path"],
                    "additionalProperties": false
                }
            }
        }),
        json!({
            "type": "function",
            "function": {
                "name": GLOB,
                "description": "Find files inside the workspace using a ripgrep glob. Returns `./`-prefixed workspace-relative paths, at most 16 KiB. Narrow the pattern or path if truncated. Uses ripgrep's normal hidden-file and ignore filtering, including glob overrides; does not follow symlinks during traversal. Example: {\"pattern\":\"*.rs\",\"path\":\"src\"}.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "pattern": { "type": "string", "description": "Ripgrep glob, e.g. *.rs or src/**/*.rs." },
                        "path": { "type": "string", "default": ".", "description": "Workspace-relative directory to search. Globs are relative to the workspace." }
                    },
                    "required": ["pattern"],
                    "additionalProperties": false
                }
            }
        }),
        json!({
            "type": "function",
            "function": {
                "name": GREP,
                "description": "Search workspace text files with a case-sensitive Rust regex; use (?i) for case-insensitivity. Returns path:line:content, at most 16 KiB. Narrow the pattern or path if truncated. Uses ripgrep's normal hidden-file and ignore filtering for file discovery, including explicit-path and glob overrides; does not follow symlinks during traversal. Example: {\"pattern\":\"fn main\",\"path\":\"src\",\"glob\":\"*.rs\"}.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "pattern": { "type": "string", "description": "Ripgrep regular expression." },
                        "path": { "type": "string", "default": ".", "description": "Workspace-relative file or directory to search." },
                        "glob": { "type": "string", "description": "Optional filename glob, relative to the workspace, e.g. *.rs." }
                    },
                    "required": ["pattern"],
                    "additionalProperties": false
                }
            }
        }),
        json!({
            "type": "function",
            "function": {
                "name": APPLY_PATCH,
                "description": APPLY_PATCH_DESCRIPTION,
                "parameters": {
                    "type": "object",
                    "properties": {
                        "patch": {
                            "type": "string",
                            "description": "A patch beginning with *** Begin Patch and ending with *** End Patch."
                        }
                    },
                    "required": ["patch"],
                    "additionalProperties": false
                }
            }
        }),
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
        SHELL => command_line(argument("command")?),
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

/// Unknown names and invalid arguments are failed results the model can read
/// on its next request, not errors that end the prompt.
pub async fn execute(
    workspace_path: &Path,
    call: &ToolCall,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome {
    if call.name == SHELL {
        return shell::execute(workspace_path, &call.arguments, cancelled).await;
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
    use super::*;

    async fn execute(workspace: &Path, call: &ToolCall) -> ToolOutcome {
        super::execute(workspace, call, std::future::pending()).await
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
        let file = pinned.resolve_existing(Path::new("file")).unwrap();
        std::fs::remove_file(workspace.0.join("file")).unwrap();
        std::os::unix::fs::symlink(outside.0.join("file"), workspace.0.join("file")).unwrap();
        assert!(pinned.read_file(&file).is_err());
    }

    #[tokio::test]
    async fn patch_schema_and_argument_errors() {
        let schema = schemas()
            .into_iter()
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
