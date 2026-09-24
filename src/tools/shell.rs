use std::{os::unix::process::ExitStatusExt, path::Path, process::ExitStatus, time::Duration};

use futures::FutureExt;
use serde::Deserialize;
use serde_json::{Value, json};
use tokio::process::Command;

use crate::{
    process::{self, Capture, Limits, Observed},
    sessions::ToolOutcome,
    shell_processes::{Interruption, Output, ShellProcesses, State, Written},
};

use super::{SHELL, SHELL_PROCESS};

const OUTPUT_BODY_LIMIT: usize = 14 * 1024;
const DEFAULT_TIMEOUT_SECONDS: u64 = 120;
const MAX_WAIT_SECONDS: u64 = 30;
const MAX_INPUT_BYTES: usize = 16 * 1024;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Args {
    command: String,
    /// Optional so an explicit value can be rejected for background commands.
    /// Omission is `None`; an explicit null is rejected like any non-integer.
    #[serde(default, deserialize_with = "present")]
    timeout_seconds: Option<u64>,
    #[serde(default)]
    background: bool,
}

fn present<'de, D: serde::Deserializer<'de>>(deserializer: D) -> Result<Option<u64>, D::Error> {
    u64::deserialize(deserializer).map(Some)
}

#[derive(Debug, Deserialize, PartialEq)]
#[serde(tag = "action", rename_all = "snake_case", deny_unknown_fields)]
enum ProcessAction {
    List {},
    Read {
        process_id: String,
        #[serde(default)]
        wait_seconds: u64,
    },
    Write {
        process_id: String,
        text: String,
        #[serde(default)]
        close_stdin: bool,
    },
    Stop {
        process_id: String,
    },
}

/// What an Ask mode permission request must show before a call runs.
#[derive(Clone, Debug, PartialEq)]
pub enum Permission {
    NotRequired,
    /// Run a shell command; `background` when it keeps running after the call.
    Command {
        background: bool,
    },
    /// Send text to a shell process's stdin. `command` is the shell process's
    /// command, or nothing when the ID names no shell process.
    Input {
        process_id: String,
        command: Option<String>,
        text: String,
        close_stdin: bool,
    },
}

/// Every shell call needs permission, even one whose arguments are invalid.
pub(super) fn command_permission(arguments: &str) -> Permission {
    Permission::Command {
        background: serde_json::from_str::<Args>(arguments).is_ok_and(|args| args.background),
    }
}

/// Writes need permission. Other actions, and arguments that fail the
/// validation execution repeats, run without it.
pub(super) fn process_permission(arguments: &str, shell_processes: &ShellProcesses) -> Permission {
    match process_action(arguments) {
        Ok(ProcessAction::Write {
            process_id,
            text,
            close_stdin,
        }) => Permission::Input {
            command: shell_processes
                .get(&process_id)
                .map(|process| process.command().to_owned()),
            process_id,
            text,
            close_stdin,
        },
        Ok(ProcessAction::List {} | ProcessAction::Read { .. } | ProcessAction::Stop { .. })
        | Err(_) => Permission::NotRequired,
    }
}

fn process_action(arguments: &str) -> Result<ProcessAction, String> {
    let action = serde_json::from_str::<ProcessAction>(arguments)
        .map_err(|error| format!("arguments: {error}"))?;
    match &action {
        ProcessAction::Read { wait_seconds, .. } if *wait_seconds > MAX_WAIT_SECONDS => Err(
            format!("arguments: wait_seconds must be from 0 through {MAX_WAIT_SECONDS}"),
        ),
        ProcessAction::Write { text, .. } if text.len() > MAX_INPUT_BYTES => {
            Err("arguments: text exceeds 16 KiB".to_owned())
        }
        ProcessAction::Write {
            text,
            close_stdin: false,
            ..
        } if text.is_empty() => Err(
            "arguments: text is empty; set close_stdin to close stdin without writing".to_owned(),
        ),
        _ => Ok(action),
    }
}

pub(super) fn schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": SHELL,
            "description": "Run a /bin/sh command starting in the session workspace. An ordinary call waits for the command to finish and returns the exit status and tails of stdout and stderr, at most 16 KiB total. Output has a shared 14 KiB budget: 7 KiB per stream, with unused space given to the other stream. Earlier output may be omitted; redirect long logs to a workspace file for later inspection. Each ordinary call starts a fresh shell with stdin connected to /dev/null. Set background to true for a development server, watcher, or long build: the call returns a process ID as soon as the command starts, and the command keeps running across turns until it exits, shell_process stops it, the session is deleted, or Ox exits. Commands run with Ox's permissions and can access paths outside the workspace.",
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
                        "default": DEFAULT_TIMEOUT_SECONDS,
                        "description": "Maximum execution time in seconds of an ordinary call. Defaults to 120. Not allowed with background."
                    },
                    "background": {
                        "type": "boolean",
                        "default": false,
                        "description": "Start the command with piped stdin and return its process ID without waiting for it. Background commands have no timeout; use shell_process to read their output, write to their stdin, or stop them. Keep the main process in the foreground of this shell, as in `npm run dev` rather than `npm run dev &`."
                    }
                },
                "required": [
                    "command"
                ],
                "additionalProperties": false
            }
        }
    })
}

pub(super) fn process_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": SHELL_PROCESS,
            "description": "Inspect or control this session's background commands, started by shell with background true. list returns each process ID, command, and state. read returns the state and the retained tails of stdout and stderr, optionally waiting up to wait_seconds for the command to end; reads do not consume output, so repeated reads may repeat it. write sends text to stdin exactly as given, adding no newline, and close_stdin closes stdin afterward. stop sends SIGTERM to the command's process group, then SIGKILL after 2 seconds. Process IDs are not operating-system PIDs and are valid only in this session.",
            "parameters": {
                "type": "object",
                "properties": {
                    "action": {
                        "type": "string",
                        "enum": ["list", "read", "write", "stop"],
                        "description": "What to do."
                    },
                    "process_id": {
                        "type": "string",
                        "description": "The process ID returned by shell. Required for read, write, and stop."
                    },
                    "wait_seconds": {
                        "type": "integer",
                        "minimum": 0,
                        "maximum": MAX_WAIT_SECONDS,
                        "default": 0,
                        "description": "For read only: seconds to wait for the command to end before returning. Reaching the limit leaves the command running. Defaults to 0."
                    },
                    "text": {
                        "type": "string",
                        "description": "For write only, and required there: UTF-8 text of at most 16 KiB, sent exactly as given. Include \\n to end a line. May be empty only with close_stdin."
                    },
                    "close_stdin": {
                        "type": "boolean",
                        "default": false,
                        "description": "For write only: close stdin after writing, which signals end of input."
                    }
                },
                "required": [
                    "action"
                ],
                "additionalProperties": false
            }
        }
    })
}

fn shell_command(workspace: &Path, command_text: &str) -> Command {
    let mut command = Command::new("/bin/sh");
    command
        .arg("-c")
        .arg(command_text)
        .current_dir(workspace)
        .env_remove("OPENROUTER_API_KEY");
    command
}

pub(super) async fn execute(
    workspace: &Path,
    shell_processes: &ShellProcesses,
    arguments: &str,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome {
    let args = match serde_json::from_str::<Args>(arguments) {
        Ok(args) => args,
        Err(error) => return failure(format!("arguments: {error}")),
    };
    let timeout_seconds = match (args.background, args.timeout_seconds) {
        (true, Some(_)) => {
            return failure(
                "arguments: timeout_seconds is not allowed with background; background commands have no timeout"
                    .into(),
            );
        }
        (true, None) => None,
        (false, seconds) => match seconds.unwrap_or(DEFAULT_TIMEOUT_SECONDS) {
            seconds @ 1..=600 => Some(seconds),
            _ => return failure("arguments: timeout_seconds must be from 1 through 600".into()),
        },
    };
    tokio::pin!(cancelled);
    if cancelled.as_mut().now_or_never().is_some() {
        return ToolOutcome::Cancelled("Cancelled before this tool was started.".into());
    }
    let command = shell_command(workspace, &args.command);
    let Some(seconds) = timeout_seconds else {
        // Once registered, the start is the observed outcome; later
        // cancellation does not undo it.
        return match shell_processes.start(command, &args.command, OUTPUT_BODY_LIMIT) {
            Ok(process) => ToolOutcome::Completed(format!(
                "Started shell process {}.\nThe command is running in the background. This confirms that it started, not that it finished or is ready. Use shell_process to read its output, write to its stdin, or stop it.",
                process.id()
            )),
            Err(error) => failure(format!(
                "Could not start /bin/sh in {}: {error}",
                workspace.display()
            )),
        };
    };
    let limits = Limits {
        stdout: OUTPUT_BODY_LIMIT,
        stderr: OUTPUT_BODY_LIMIT,
        deadline: Duration::from_secs(seconds),
        grace: Duration::ZERO,
    };
    match process::run(command, None, limits, cancelled).await {
        Ok(finished) => render(
            finished.observed,
            finished.stdout,
            finished.stderr,
            finished.diagnostics,
        ),
        Err(error) => failure(format!(
            "Could not start /bin/sh in {}: {error}",
            workspace.display()
        )),
    }
}

/// Runs one `shell_process` action. Reads and writes handle cancellation
/// themselves so the command keeps running; a stop finishes its cleanup.
pub(super) async fn execute_process(
    shell_processes: &ShellProcesses,
    arguments: &str,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome {
    let action = match process_action(arguments) {
        Ok(action) => action,
        Err(error) => return super::bounded_result(Err(error)),
    };
    let process_id = match &action {
        ProcessAction::List {} => return list(shell_processes),
        ProcessAction::Read { process_id, .. }
        | ProcessAction::Write { process_id, .. }
        | ProcessAction::Stop { process_id } => process_id,
    };
    let Some(process) = shell_processes.get(process_id) else {
        return super::bounded_result(Err(format!(
            "No shell process {process_id} in this session. Call shell_process with action \"list\" to see the available shell processes."
        )));
    };
    match action {
        ProcessAction::List {} => unreachable!("list returned above"),
        ProcessAction::Read { wait_seconds, .. } => {
            if !process
                .wait(Duration::from_secs(wait_seconds), cancelled)
                .await
            {
                return ToolOutcome::Cancelled(format!(
                    "Cancelled while waiting for shell process {}; it was not stopped.",
                    process.id()
                ));
            }
            render_process(process.id(), process.command(), process.output(), false)
        }
        ProcessAction::Write {
            text, close_stdin, ..
        } => {
            let written = process.write(text.as_bytes(), close_stdin, cancelled).await;
            render_written(process.id(), text.len(), written)
        }
        ProcessAction::Stop { .. } => {
            render_process(process.id(), process.command(), process.stop().await, true)
        }
    }
}

fn list(shell_processes: &ShellProcesses) -> ToolOutcome {
    let processes = shell_processes.list();
    if processes.is_empty() {
        return ToolOutcome::Completed("No shell processes in this session.".to_owned());
    }
    let lines: Vec<_> = processes
        .iter()
        .map(|process| {
            let state = match process.state() {
                State::Running => "running".to_owned(),
                State::Exited(status) => format!("exited ({})", exit_status(status)),
                State::Stopped(status) => format!("stopped ({})", exit_status(status)),
                State::Failed { status, .. } => format!("failed ({})", exit_status(status)),
            };
            format!(
                "{} {state}: {}",
                process.id(),
                shortened_command(process.command())
            )
        })
        .collect();
    super::bounded_result(Ok(lines.join("\n")))
}

fn shortened_command(command: &str) -> String {
    super::shorten(&super::command_line(command).unwrap_or_default())
}

/// A read outcome follows the shell conventions for the observed exit. A stop
/// call completes whatever the command's exit.
fn render_process(process_id: &str, command: &str, output: Output, stop: bool) -> ToolOutcome {
    let (state, succeeded) = match &output.state {
        State::Running => ("State: running".to_owned(), true),
        State::Exited(status) => (
            format!("State: exited\n{}", exit_status(*status)),
            status.success(),
        ),
        State::Stopped(status) => (
            format!("State: stopped\n{}", exit_status(*status)),
            status.success(),
        ),
        State::Failed { error, status } => (
            format!("State: failed\n{error}\n{}", exit_status(*status)),
            false,
        ),
    };
    let status = format!(
        "Process ID: {process_id}\nCommand: {}\n{state}",
        shortened_command(command)
    );
    let text = report(status, output.stdout, output.stderr, &output.diagnostics);
    if stop || succeeded {
        ToolOutcome::Completed(text)
    } else {
        ToolOutcome::Failed(text)
    }
}

fn render_written(process_id: &str, total: usize, written: Written) -> ToolOutcome {
    let stdin = if written.stdin_closed {
        "stdin is closed"
    } else {
        "stdin remains open"
    };
    let Some(interruption) = written.interruption else {
        return ToolOutcome::Completed(format!(
            "Wrote {} bytes to shell process {process_id}; {stdin}.",
            written.bytes
        ));
    };
    let reason = match &interruption {
        Interruption::StdinClosed => "stdin was already closed".to_owned(),
        Interruption::Finished => "the command has finished".to_owned(),
        Interruption::TimedOut(limit) => format!(
            "the command did not accept the input within {} seconds",
            limit.as_secs()
        ),
        Interruption::Cancelled => "the prompt was cancelled".to_owned(),
        Interruption::Failed(error) => format!("writing failed: {error}"),
    };
    let text = format!(
        "Wrote {} of {total} bytes to shell process {process_id}, then stopped because {reason}; {stdin}. The rest was not sent, and the command may not have processed what was.",
        written.bytes
    );
    match interruption {
        Interruption::Cancelled => ToolOutcome::Cancelled(text),
        _ => ToolOutcome::Failed(text),
    }
}

fn failure(message: String) -> ToolOutcome {
    render(
        Observed::Failed(message),
        Capture::new(OUTPUT_BODY_LIMIT),
        Capture::new(OUTPUT_BODY_LIMIT),
        String::new(),
    )
}

fn trim_front(text: &mut String, limit: usize) -> bool {
    if text.len() <= limit {
        return false;
    }
    let mut start = text.len() - limit;
    while !text.is_char_boundary(start) {
        start += 1;
    }
    text.drain(..start);
    true
}

fn exit_status(status: ExitStatus) -> String {
    match status.code() {
        Some(code) => format!("Exit code: {code}"),
        None => format!(
            "Terminated by signal: {}",
            status.signal().expect("Unix signal exit")
        ),
    }
}

fn render(observed: Observed, out: Capture, err: Capture, diagnostics: String) -> ToolOutcome {
    let status = match &observed {
        Observed::Exit(status) => exit_status(*status),
        Observed::Timeout(deadline) => format!(
            "Timed out after {} seconds; partial changes may remain.",
            deadline.as_secs()
        ),
        Observed::Cancelled => "Cancelled during execution; partial changes may remain.".into(),
        Observed::Failed(error) => error.clone(),
    };
    let text = report(status, out, err, &diagnostics);
    match observed {
        Observed::Exit(exit) if exit.success() => ToolOutcome::Completed(text),
        Observed::Cancelled => ToolOutcome::Cancelled(text),
        _ => ToolOutcome::Failed(text),
    }
}

/// `status` and `diagnostics` followed by the output tails, within the tool
/// output limit.
fn report(mut status: String, mut out: Capture, mut err: Capture, diagnostics: &str) -> String {
    if !diagnostics.is_empty() {
        status.push('\n');
        status.push_str(diagnostics);
    }
    if status.len() > 1536 {
        super::truncate(&mut status, 1500);
        status.push_str("\nDiagnostic truncated.");
    }
    let mut stdout = out.decode();
    let mut stderr = err.decode();
    let half = OUTPUT_BODY_LIMIT / 2;
    let out_budget = half.max(OUTPUT_BODY_LIMIT.saturating_sub(stderr.len()));
    let err_budget = OUTPUT_BODY_LIMIT - stdout.len().min(out_budget);
    out.omitted |= trim_front(&mut stdout, out_budget);
    err.omitted |= trim_front(&mut stderr, err_budget);
    for (name, text, omitted) in [
        ("stdout", stdout, out.omitted),
        ("stderr", stderr, err.omitted),
    ] {
        status.push_str(&format!("\n\n{name}:"));
        if omitted {
            status.push_str(" (tail; earlier output omitted)");
        }
        status.push('\n');
        status.push_str(if text.is_empty() { "(empty)" } else { &text });
    }
    assert!(
        status.len() <= super::OUTPUT_LIMIT,
        "shell output exceeds its limit"
    );
    status
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        hooks,
        process::{OUTPUT_DRAIN_TIMEOUT, kill_group},
        sessions::{EffortLevel, SessionMode, StopDecision},
        tools::{self, fixture::Workspace},
    };
    use rustix::process::Pid;
    use serde_json::json;
    use tokio::time::{Instant, timeout};

    /// Runs one shell call with a shell-process owner of its own.
    async fn execute(
        workspace: &Path,
        arguments: &str,
        cancelled: impl Future<Output = ()>,
    ) -> ToolOutcome {
        super::execute(workspace, &ShellProcesses::default(), arguments, cancelled).await
    }

    async fn run(workspace: &Path, command: &str) -> ToolOutcome {
        execute(
            workspace,
            &json!({"command": command}).to_string(),
            std::future::pending(),
        )
        .await
    }

    async fn wait_file(path: &Path) {
        timeout(Duration::from_secs(5), async {
            while !path.exists() {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
    }

    async fn assert_stopped(workspace: &Path) {
        for name in ["shell", "child"] {
            let pid = std::fs::read_to_string(workspace.join(name)).unwrap();
            timeout(Duration::from_secs(3), async {
                loop {
                    let output = Command::new("ps")
                        .args(["-o", "stat=", "-p", pid.trim()])
                        .output()
                        .await
                        .unwrap();
                    let state = String::from_utf8(output.stdout).unwrap();
                    if state.trim().is_empty() || (name == "child" && state.trim().starts_with('Z'))
                    {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(10)).await;
                }
            })
            .await
            .expect("shell was reaped and child stopped");
        }
    }

    #[tokio::test]
    async fn arguments_and_schema() {
        let workspace = Workspace::new();
        assert_eq!(
            serde_json::from_str::<Args>(r#"{"command":""}"#)
                .unwrap()
                .timeout_seconds,
            None
        );
        for args in [
            "{",
            "{}",
            r#"{"command":1}"#,
            r#"{"command":"touch wrong","extra":1}"#,
            r#"{"command":"touch wrong","timeout_seconds":0}"#,
            r#"{"command":"touch wrong","timeout_seconds":601}"#,
            r#"{"command":"touch wrong","timeout_seconds":-1}"#,
            r#"{"command":"touch wrong","timeout_seconds":1.5}"#,
            r#"{"command":"touch wrong","timeout_seconds":"1"}"#,
            r#"{"command":"touch wrong","timeout_seconds":null}"#,
            r#"{"command":"touch wrong","background":true,"timeout_seconds":null}"#,
            r#"{"command":"touch wrong","background":"yes"}"#,
            r#"{"command":"touch wrong","background":true,"timeout_seconds":120}"#,
        ] {
            assert!(
                matches!(
                    execute(&workspace.0, args, std::future::pending()).await,
                    ToolOutcome::Failed(_)
                ),
                "{args}"
            );
        }
        assert!(!workspace.0.join("wrong").exists());
        for seconds in [1, 600] {
            assert!(matches!(
                execute(
                    &workspace.0,
                    &json!({"command":"", "timeout_seconds":seconds}).to_string(),
                    std::future::pending()
                )
                .await,
                ToolOutcome::Completed(_)
            ));
        }
        let schema = |name: &str| {
            tools::schemas()
                .into_iter()
                .find(|s| s["function"]["name"] == name)
                .unwrap()["function"]["parameters"]
                .clone()
        };
        let shell = schema(SHELL);
        assert_eq!(shell["required"], json!(["command"]));
        assert_eq!(shell["additionalProperties"], false);
        assert_eq!(
            shell["properties"]["background"],
            json!({
                "type": "boolean",
                "default": false,
                "description": shell["properties"]["background"]["description"],
            })
        );
        assert_eq!(shell["properties"]["timeout_seconds"]["default"], 120);
        let process = schema(SHELL_PROCESS);
        assert_eq!(process["required"], json!(["action"]));
        assert_eq!(process["additionalProperties"], false);
        assert_eq!(
            process["properties"]["action"]["enum"],
            json!(["list", "read", "write", "stop"])
        );
        assert_eq!(process["properties"]["wait_seconds"]["maximum"], 30);
    }

    #[test]
    fn process_actions_validate_arguments_and_classify_permission() {
        let owner = ShellProcesses::default();
        let write = |text: &str, close_stdin| ProcessAction::Write {
            process_id: "p".to_owned(),
            text: text.to_owned(),
            close_stdin,
        };
        for (arguments, expected) in [
            (json!({"action":"list"}), Ok(ProcessAction::List {})),
            (
                json!({"action":"read","process_id":"p"}),
                Ok(ProcessAction::Read {
                    process_id: "p".to_owned(),
                    wait_seconds: 0,
                }),
            ),
            (
                json!({"action":"read","process_id":"p","wait_seconds":30}),
                Ok(ProcessAction::Read {
                    process_id: "p".to_owned(),
                    wait_seconds: 30,
                }),
            ),
            (
                json!({"action":"write","process_id":"p","text":"y\n"}),
                Ok(write("y\n", false)),
            ),
            (
                json!({"action":"write","process_id":"p","text":"","close_stdin":true}),
                Ok(write("", true)),
            ),
            (
                json!({"action":"write","process_id":"p","text":"x".repeat(MAX_INPUT_BYTES)}),
                Ok(write(&"x".repeat(MAX_INPUT_BYTES), false)),
            ),
            (
                json!({"action":"stop","process_id":"p"}),
                Ok(ProcessAction::Stop {
                    process_id: "p".to_owned(),
                }),
            ),
            (json!({}), Err("missing field `action`")),
            (
                json!({"action":"restart"}),
                Err("unknown variant `restart`"),
            ),
            (
                json!({"action":"list","process_id":"p"}),
                Err("unknown field `process_id`"),
            ),
            (json!({"action":"read"}), Err("missing field `process_id`")),
            (
                json!({"action":"read","process_id":"p","wait_seconds":31}),
                Err("wait_seconds must be from 0 through 30"),
            ),
            (
                json!({"action":"read","process_id":"p","text":"y"}),
                Err("unknown field `text`"),
            ),
            (
                json!({"action":"stop","process_id":"p","wait_seconds":1}),
                Err("unknown field `wait_seconds`"),
            ),
            (
                json!({"action":"write","process_id":"p"}),
                Err("missing field `text`"),
            ),
            (
                json!({"action":"write","process_id":"p","text":""}),
                Err("text is empty"),
            ),
            (
                json!({"action":"write","process_id":"p","text":"x".repeat(MAX_INPUT_BYTES + 1)}),
                Err("text exceeds 16 KiB"),
            ),
        ] {
            let arguments = arguments.to_string();
            let parsed = process_action(&arguments);
            match &expected {
                Ok(action) => assert_eq!(parsed.as_ref(), Ok(action), "{arguments}"),
                Err(message) => assert!(
                    parsed.as_ref().is_err_and(|error| error.contains(message)),
                    "{arguments}: {parsed:?}"
                ),
            }
            let permission = process_permission(&arguments, &owner);
            match expected {
                Ok(ProcessAction::Write {
                    process_id,
                    text,
                    close_stdin,
                }) => assert_eq!(
                    permission,
                    Permission::Input {
                        process_id,
                        command: None,
                        text,
                        close_stdin,
                    },
                    "{arguments}"
                ),
                _ => assert_eq!(permission, Permission::NotRequired, "{arguments}"),
            }
        }
        for (arguments, background) in [
            (r#"{"command":"ls"}"#, false),
            (r#"{"command":"npm run dev","background":true}"#, true),
            (r#"{"comm"#, false),
        ] {
            assert_eq!(
                command_permission(arguments),
                Permission::Command { background },
                "{arguments}"
            );
        }
    }

    #[tokio::test]
    async fn a_background_start_reports_an_id_that_later_calls_use() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let process = |arguments: serde_json::Value| {
            let owner = owner.clone();
            async move { execute_process(&owner, &arguments.to_string(), std::future::pending()).await }
        };
        let started = super::execute(
            &workspace.0,
            &owner,
            &json!({"command":"printf ready; read line; printf 'got:%s' \"$line\"; touch done", "background":true}).to_string(),
            std::future::pending(),
        )
        .await;
        let ToolOutcome::Completed(text) = &started else {
            panic!("{started:?}");
        };
        assert!(text.contains("confirms that it started, not that it finished"));
        assert!(!workspace.0.join("done").exists(), "the start did not wait");
        let id = owner.list()[0].id().to_owned();
        assert!(text.starts_with(&format!("Started shell process {id}.")));

        let running = process(json!({"action":"read","process_id":id})).await;
        assert!(
            matches!(&running, ToolOutcome::Completed(text) if text.contains("State: running")),
            "{running:?}"
        );
        let cancelled = execute_process(
            &owner,
            &json!({"action":"read","process_id":id,"wait_seconds":30}).to_string(),
            async {},
        )
        .await;
        assert!(
            matches!(cancelled, ToolOutcome::Cancelled(text) if text.contains("was not stopped"))
        );
        assert_eq!(
            process(json!({"action":"write","process_id":id,"text":"hi\n"})).await,
            ToolOutcome::Completed(format!(
                "Wrote 3 bytes to shell process {id}; stdin remains open."
            ))
        );
        let finished = process(json!({"action":"read","process_id":id,"wait_seconds":5})).await;
        assert!(
            matches!(&finished, ToolOutcome::Completed(text)
                if text.starts_with(&format!("Process ID: {id}\nCommand: printf ready; read line;"))
                    && text.contains("State: exited\nExit code: 0")
                    && text.contains("stdout:\nreadygot:hi")),
            "{finished:?}"
        );
        assert!(
            matches!(process(json!({"action":"list"})).await, ToolOutcome::Completed(text)
                if text == format!("{id} exited (Exit code: 0): printf ready; read line; printf 'got:%s' \"$line\"; touch done"))
        );
        assert!(matches!(
            process(json!({"action":"write","process_id":id,"text":"late"})).await,
            ToolOutcome::Failed(text) if text.contains("because the command has finished")
        ));
        assert!(matches!(
            process(json!({"action":"stop","process_id":id})).await,
            ToolOutcome::Completed(text) if text.contains("State: exited")
        ));
        assert!(matches!(
            process(json!({"action":"read","process_id":"missing"})).await,
            ToolOutcome::Failed(text) if text == "No shell process missing in this session. Call shell_process with action \"list\" to see the available shell processes."
        ));
        assert_eq!(
            execute_process(
                &ShellProcesses::default(),
                &json!({"action":"list"}).to_string(),
                std::future::pending()
            )
            .await,
            ToolOutcome::Completed("No shell processes in this session.".to_owned())
        );
        let closed = ShellProcesses::default();
        closed.begin_shutdown();
        let refused = super::execute(
            &workspace.0,
            &closed,
            &json!({"command":"touch refused", "background":true}).to_string(),
            std::future::pending(),
        )
        .await;
        assert!(matches!(refused, ToolOutcome::Failed(text) if text.contains("shutting down")),);
        assert!(!workspace.0.join("refused").exists());
    }

    #[test]
    fn process_outcomes_follow_the_observed_exit_and_stay_bounded() {
        use std::os::unix::process::ExitStatusExt;
        let (success, code, signal) = (
            ExitStatus::from_raw(0),
            ExitStatus::from_raw(7 << 8),
            ExitStatus::from_raw(15),
        );
        let output = |state| Output {
            state,
            stdout: capture(b"out".to_vec()),
            stderr: capture(vec![]),
            diagnostics: String::new(),
        };
        let failed = State::Failed {
            error: "Reading stdout failed: broken".to_owned(),
            status: signal,
        };
        for (state, read, stop, text) in [
            (State::Running, "completed", "completed", "State: running"),
            (
                State::Exited(success),
                "completed",
                "completed",
                "State: exited\nExit code: 0",
            ),
            (
                State::Exited(code),
                "failed",
                "completed",
                "State: exited\nExit code: 7",
            ),
            (
                State::Exited(signal),
                "failed",
                "completed",
                "State: exited\nTerminated by signal: 15",
            ),
            (
                State::Stopped(signal),
                "failed",
                "completed",
                "State: stopped\nTerminated by signal: 15",
            ),
            (
                State::Stopped(success),
                "completed",
                "completed",
                "State: stopped\nExit code: 0",
            ),
            (
                failed,
                "failed",
                "completed",
                "State: failed\nReading stdout failed: broken\nTerminated by signal: 15",
            ),
        ] {
            for (stop_call, expected) in [(false, read), (true, stop)] {
                let outcome =
                    render_process("p-1", "npm run dev", output(state.clone()), stop_call);
                let kind = match &outcome {
                    ToolOutcome::Completed(_) => "completed",
                    ToolOutcome::Failed(_) => "failed",
                    ToolOutcome::Cancelled(_) => "cancelled",
                };
                assert_eq!(kind, expected, "{state:?}, stop: {stop_call}");
                assert_eq!(
                    outcome.text(),
                    format!(
                        "Process ID: p-1\nCommand: npm run dev\n{text}\n\nstdout:\nout\n\nstderr:\n(empty)"
                    )
                );
            }
        }
        let invalid = || capture(vec![0xff; OUTPUT_BODY_LIMIT]);
        let flooded = Output {
            state: State::Exited(success),
            stdout: invalid(),
            stderr: invalid(),
            diagnostics: "雪".repeat(10000),
        };
        let outcome = render_process("p-1", &"雪".repeat(1000), flooded, false);
        assert!(outcome.text().len() <= tools::OUTPUT_LIMIT);
        assert!(outcome.text().contains("Diagnostic truncated."));
        assert_eq!(outcome.text().matches("earlier output omitted").count(), 2);

        for (written, expected) in [
            (
                Written {
                    bytes: 2,
                    stdin_closed: true,
                    interruption: None,
                },
                ToolOutcome::Completed("Wrote 2 bytes to shell process p-1; stdin is closed.".to_owned()),
            ),
            (
                Written {
                    bytes: 1,
                    stdin_closed: false,
                    interruption: Some(Interruption::TimedOut(Duration::from_secs(5))),
                },
                ToolOutcome::Failed("Wrote 1 of 2 bytes to shell process p-1, then stopped because the command did not accept the input within 5 seconds; stdin remains open. The rest was not sent, and the command may not have processed what was.".to_owned()),
            ),
            (
                Written {
                    bytes: 0,
                    stdin_closed: false,
                    interruption: Some(Interruption::Cancelled),
                },
                ToolOutcome::Cancelled("Wrote 0 of 2 bytes to shell process p-1, then stopped because the prompt was cancelled; stdin remains open. The rest was not sent, and the command may not have processed what was.".to_owned()),
            ),
            (
                Written {
                    bytes: 0,
                    stdin_closed: true,
                    interruption: Some(Interruption::StdinClosed),
                },
                ToolOutcome::Failed("Wrote 0 of 2 bytes to shell process p-1, then stopped because stdin was already closed; stdin is closed. The rest was not sent, and the command may not have processed what was.".to_owned()),
            ),
        ] {
            assert_eq!(render_written("p-1", 2, written), expected);
        }
    }

    #[tokio::test]
    async fn shell_semantics_and_results() {
        let workspace = Workspace::new();
        let outcome = run(&workspace.0, "pwd; printf hello; printf problem >&2; read value; test $? -ne 0; test ! -t 0; test -c /dev/stdin").await;
        assert!(matches!(outcome, ToolOutcome::Completed(_)), "{outcome:?}");
        assert!(
            outcome
                .text()
                .contains(workspace.0.file_name().unwrap().to_str().unwrap())
        );
        assert!(outcome.text().contains("hello\n\nstderr:\nproblem"));
        assert_eq!(
            run(&workspace.0, "").await.text(),
            "Exit code: 0\n\nstdout:\n(empty)\n\nstderr:\n(empty)"
        );
        assert!(
            matches!(run(&workspace.0, "printf bad >&2; exit 7").await, ToolOutcome::Failed(text) if text.contains("Exit code: 7") && text.ends_with("bad"))
        );
        assert!(
            matches!(run(&workspace.0, "kill -TERM $$").await, ToolOutcome::Failed(text) if text.contains("signal: 15"))
        );
        assert!(
            matches!(run(&workspace.0.join("missing"), "true").await, ToolOutcome::Failed(text) if text.contains("Could not start /bin/sh"))
        );
        assert!(matches!(run(&workspace.0, "false; false | true; mkdir sub; cd sub; export OX_SHELL_LOCAL=changed; printf saved > ../file").await, ToolOutcome::Completed(_)));
        assert!(
            matches!(run(&workspace.0, "test -z \"${OX_SHELL_LOCAL+x}\" && test -f file && cat file").await, ToolOutcome::Completed(text) if text.contains("saved"))
        );
        assert!(matches!(
            run(&workspace.0, "cd /; test -d /tmp").await,
            ToolOutcome::Completed(_)
        ));
    }

    #[tokio::test]
    async fn environment_excludes_api_key() {
        // A subprocess sets the dummy environment without mutating the test runner.
        const FLAG: &str = "OX_SHELL_ENV_TEST";
        if std::env::var_os(FLAG).is_some() {
            let workspace = Workspace::new();
            let check =
                "test -z \"${OPENROUTER_API_KEY+x}\" && test \"$OX_SHELL_ENV_TEST\" = inherited";
            let outcome = run(&workspace.0, check).await;
            assert!(matches!(outcome, ToolOutcome::Completed(_)), "{outcome:?}");
            let owner = ShellProcesses::default();
            let started = super::execute(
                &workspace.0,
                &owner,
                &json!({"command": check, "background": true}).to_string(),
                std::future::pending(),
            )
            .await;
            assert!(matches!(started, ToolOutcome::Completed(_)), "{started:?}");
            let process = owner.list().remove(0);
            let read = execute_process(
                &owner,
                &json!({"action":"read","process_id":process.id(),"wait_seconds":5}).to_string(),
                std::future::pending(),
            )
            .await;
            assert!(
                matches!(&read, ToolOutcome::Completed(text) if text.contains("Exit code: 0")),
                "a background command also lacks the key: {read:?}"
            );
            // A hook inherits the key and reads its input from stdin.
            let hooks = hooks::HookSource {
                skill: Some("goal".to_owned()),
                hooks: hooks::Hooks {
                    before_stop: Some(hooks::HookCommand {
                        command: r#"test "$OPENROUTER_API_KEY" = dummy-key && test "$OX_IN_HOOK" = 1 && cat > input.json && printf '{"decision":"stop","message":"Key present."}'"#.to_owned(),
                    }),
                    ..hooks::Hooks::default()
                },
                directory: workspace.0.clone(),
            };
            let context = hooks::Context {
                skill: Some("goal".to_owned()),
                arguments: "Check the key.".to_owned(),
                session_id: "session-1".to_owned(),
                mode: SessionMode::Ask,
                run_id: "run-1".to_owned(),
                workspace: "/workspace".into(),
                model: "test/model".to_owned(),
                effort: EffortLevel::Low,
            };
            let decision: hooks::StopResponse = hooks::run(
                &hooks,
                &context,
                &hooks::Event::BeforeStop {
                    answer: "The answer.".to_owned(),
                },
                std::future::pending(),
            )
            .await
            .unwrap();
            assert_eq!(
                (decision.decision, decision.message.as_str()),
                (StopDecision::Stop, "Key present.")
            );
            let input: serde_json::Value = serde_json::from_str(
                &std::fs::read_to_string(workspace.0.join("input.json")).unwrap(),
            )
            .unwrap();
            assert_eq!(
                input,
                json!({
                    "kind": "before_stop",
                    "skill": "goal",
                    "arguments": "Check the key.",
                    "session_id": "session-1",
                    "mode": "ask",
                    "run_id": "run-1",
                    "workspace": "/workspace",
                    "ox": std::env::current_exe().unwrap(),
                    "model": "test/model",
                    "effort": "low",
                    "answer": "The answer.",
                })
            );
            return;
        }
        let status = Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "tools::shell::tests::environment_excludes_api_key",
            ])
            .env(FLAG, "inherited")
            .env("OPENROUTER_API_KEY", "dummy-key")
            .status()
            .await
            .unwrap();
        assert!(status.success());
    }

    #[tokio::test]
    async fn large_output_keeps_tails_and_finishes_writing() {
        let workspace = Workspace::new();
        let outcome = run(&workspace.0, "i=0; while [ $i -lt 5000 ]; do printf 'stdout line\n'; printf 'stderr line\n' >&2; i=$((i+1)); done; printf OUT_END; printf ERR_END >&2; touch finished").await;
        assert!(matches!(outcome, ToolOutcome::Completed(_)));
        assert!(workspace.0.join("finished").exists());
        assert!(outcome.text().len() <= tools::OUTPUT_LIMIT);
        assert_eq!(outcome.text().matches("earlier output omitted").count(), 2);
        assert!(outcome.text().contains("OUT_END"));
        assert!(outcome.text().ends_with("ERR_END"));
    }

    #[test]
    fn output_allocation_utf8_and_metadata() {
        for (out_len, err_len, expected_out, expected_err) in [
            (20000, 0, OUTPUT_BODY_LIMIT, 0),
            (0, 20000, 0, OUTPUT_BODY_LIMIT),
            (20000, 1000, OUTPUT_BODY_LIMIT - 1000, 1000),
            (1000, 20000, 1000, OUTPUT_BODY_LIMIT - 1000),
            (20000, 20000, OUTPUT_BODY_LIMIT / 2, OUTPUT_BODY_LIMIT / 2),
        ] {
            let out = capture(vec![b'X'; out_len]);
            let err = capture(vec![b'Y'; err_len]);
            let result = render(Observed::Failed("error".into()), out, err, String::new());
            assert_eq!(result.text().matches('X').count(), expected_out);
            assert_eq!(result.text().matches('Y').count(), expected_err);
        }
        let mut text = "a雪🙂z".to_owned();
        assert!(trim_front(&mut text, 5));
        assert_eq!(text, "🙂z");
        for observed in [
            Observed::Failed("雪".repeat(10000)),
            Observed::Cancelled,
            Observed::Timeout(Duration::from_secs(600)),
        ] {
            let invalid = || capture(vec![0xff; OUTPUT_BODY_LIMIT]);
            let result = render(observed, invalid(), invalid(), "雪".repeat(10000));
            assert!(result.text().len() <= tools::OUTPUT_LIMIT);
            assert_eq!(result.text().matches("earlier output omitted").count(), 2);
        }
    }

    fn capture(bytes: Vec<u8>) -> Capture {
        let mut capture = Capture::new(OUTPUT_BODY_LIMIT);
        capture.bytes = bytes.into();
        capture
    }

    const CHILD: &str =
        "echo $$ > shell; sleep 30 & echo $! > child; printf started; touch ready; wait";

    #[tokio::test]
    async fn timeout_and_cancellation_stop_the_group() {
        let workspace = Workspace::new();
        let args = json!({"command":CHILD, "timeout_seconds":1}).to_string();
        let result = execute(&workspace.0, &args, std::future::pending()).await;
        assert!(
            matches!(result, ToolOutcome::Failed(ref text) if text.contains("Timed out after 1 seconds") && text.contains("partial changes") && text.contains("started"))
        );
        assert_stopped(&workspace.0).await;
        std::fs::remove_file(workspace.0.join("ready")).unwrap();
        let args = json!({"command":CHILD}).to_string();
        let cancellation = async {
            wait_file(&workspace.0.join("ready")).await;
        };
        let result = timeout(
            Duration::from_secs(5),
            execute(&workspace.0, &args, cancellation),
        )
        .await
        .unwrap();
        assert!(
            matches!(result, ToolOutcome::Cancelled(text) if text.contains("partial changes") && text.contains("started"))
        );
        assert_stopped(&workspace.0).await;
        let result = execute(&workspace.0, r#"{"command":"touch wrong"}"#, async {}).await;
        assert!(matches!(result, ToolOutcome::Cancelled(_)));
        assert!(!workspace.0.join("wrong").exists());

        // With a grace period, a group that exits on SIGTERM is not killed,
        // and one that ignores SIGTERM is killed when the period ends.
        let grace = Duration::from_millis(500);
        for (trap, exits_on_term) in [("touch terminated; exit 0", true), ("", false)] {
            std::fs::remove_file(workspace.0.join("ready")).unwrap();
            let mut command = Command::new("/bin/sh");
            command
                .arg("-c")
                .arg(format!("trap '{trap}' TERM; {CHILD}"))
                .current_dir(&workspace.0);
            let limits = Limits {
                stdout: 64,
                stderr: 64,
                deadline: Duration::from_secs(5),
                grace,
            };
            let start = Instant::now();
            let finished =
                process::run(command, None, limits, wait_file(&workspace.0.join("ready")))
                    .await
                    .unwrap();
            assert!(matches!(finished.observed, Observed::Cancelled));
            assert_eq!(start.elapsed() >= grace, !exits_on_term, "{trap}");
            assert_eq!(
                std::fs::remove_file(workspace.0.join("terminated")).is_ok(),
                exits_on_term
            );
            assert_stopped(&workspace.0).await;
        }
    }

    #[tokio::test]
    async fn normal_exit_stops_background_children_with_and_without_pipes() {
        let workspace = Workspace::new();
        for redirect in ["", ">/dev/null 2>&1"] {
            let result = timeout(
                Duration::from_secs(5),
                run(
                    &workspace.0,
                    &format!("echo $$ > shell; sleep 30 {redirect} & echo $! > child; exit 0"),
                ),
            )
            .await
            .unwrap();
            assert!(matches!(result, ToolOutcome::Completed(_)));
            assert_stopped(&workspace.0).await;
        }
        let mut child = Command::new("/bin/sh")
            .args(["-c", "exit 0"])
            .process_group(0)
            .spawn()
            .unwrap();
        let group = Pid::from_raw(child.id().unwrap() as i32).unwrap();
        tokio::time::sleep(Duration::from_millis(100)).await;
        kill_group(group);
        assert!(child.wait().await.unwrap().success());
        kill_group(group);
    }

    #[tokio::test]
    async fn detached_pipe_has_a_drain_deadline_and_late_cancellation_keeps_exit() {
        let workspace = Workspace::new();
        let command = "python3 -c 'import subprocess; p = subprocess.Popen([\"sleep\", \"30\"], start_new_session=True); open(\"detached\", \"w\").write(str(p.pid))'; echo done";
        let args = json!({"command":command}).to_string();
        let cancellation = async {
            wait_file(&workspace.0.join("detached")).await;
            tokio::time::sleep(Duration::from_millis(300)).await;
        };
        let start = Instant::now();
        let result = timeout(
            Duration::from_secs(5),
            execute(&workspace.0, &args, cancellation),
        )
        .await;
        let pid = std::fs::read_to_string(workspace.0.join("detached"))
            .unwrap()
            .parse()
            .unwrap();
        kill_group(Pid::from_raw(pid).unwrap());
        let result = result.unwrap();
        assert!(start.elapsed() >= OUTPUT_DRAIN_TIMEOUT);
        assert!(
            matches!(result, ToolOutcome::Completed(ref text) if text.contains("done") && text.contains("Output capture stopped before EOF"))
        );
        assert!(!result.text().contains("missing.\n\n\nstdout:"));
    }
}
