use std::{
    collections::VecDeque,
    io,
    os::unix::process::ExitStatusExt,
    path::Path,
    process::{ExitStatus, Stdio},
    time::Duration,
};

use futures::FutureExt;
use rustix::process::{Pid, Signal, kill_process_group};
use serde::Deserialize;
use tokio::{
    io::{AsyncRead, AsyncReadExt},
    process::Command,
    time::{Instant, sleep_until, timeout},
};

use crate::sessions::ToolOutcome;

const OUTPUT_BODY_LIMIT: usize = 14 * 1024;
const OUTPUT_DRAIN_TIMEOUT: Duration = Duration::from_secs(1);

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Args {
    command: String,
    #[serde(default = "default_timeout")]
    timeout_seconds: u64,
}

fn default_timeout() -> u64 {
    120
}

#[derive(Default)]
struct Capture {
    bytes: VecDeque<u8>,
    omitted: bool,
    done: bool,
    error: Option<String>,
}

impl Capture {
    async fn read(&mut self, pipe: &mut (impl AsyncRead + Unpin)) -> io::Result<()> {
        let mut buffer = [0; 8192];
        let read = match pipe.read(&mut buffer).await {
            Ok(read) => read,
            Err(error) => {
                self.done = true;
                self.error = Some(error.to_string());
                return Err(error);
            }
        };
        self.done = read == 0;
        let excess = (self.bytes.len() + read).saturating_sub(OUTPUT_BODY_LIMIT);
        self.omitted |= excess > 0;
        self.bytes.drain(..excess);
        self.bytes.extend(&buffer[..read]);
        Ok(())
    }

    async fn drain(&mut self, pipe: &mut (impl AsyncRead + Unpin)) -> io::Result<()> {
        while !self.done {
            self.read(pipe).await?;
        }
        Ok(())
    }

    fn decode(&mut self) -> String {
        String::from_utf8_lossy(self.bytes.make_contiguous()).into_owned()
    }
}

enum Observed {
    Exit(ExitStatus),
    Timeout(u64),
    Cancelled,
    Failed(String),
}

fn kill_group(group: Pid) {
    match kill_process_group(group, Signal::KILL) {
        Ok(()) | Err(rustix::io::Errno::SRCH | rustix::io::Errno::PERM) => {}
        Err(error) => panic!("failed to terminate owned shell process group: {error}"),
    }
}

/// Stops the group if the tool future is dropped before its normal cleanup runs.
struct ProcessGroup(Option<Pid>);

impl ProcessGroup {
    fn terminate(&mut self) {
        kill_group(self.0.take().expect("process group is terminated once"));
    }
}

impl Drop for ProcessGroup {
    fn drop(&mut self) {
        if let Some(group) = self.0 {
            let _ = kill_process_group(group, Signal::KILL);
        }
    }
}

pub(super) async fn execute(
    workspace: &Path,
    arguments: &str,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome {
    let args = match serde_json::from_str::<Args>(arguments) {
        Ok(args) if (1..=600).contains(&args.timeout_seconds) => args,
        Ok(_) => return failure("arguments: timeout_seconds must be from 1 through 600".into()),
        Err(error) => return failure(format!("arguments: {error}")),
    };
    tokio::pin!(cancelled);
    if cancelled.as_mut().now_or_never().is_some() {
        return ToolOutcome::Cancelled("Cancelled before this tool was started.".into());
    }
    let mut child = match Command::new("/bin/sh")
        .arg("-c")
        .arg(&args.command)
        .current_dir(workspace)
        .env_remove("OPENROUTER_API_KEY")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .process_group(0)
        .spawn()
    {
        Ok(child) => child,
        Err(error) => {
            return failure(format!(
                "Could not start /bin/sh in {}: {error}",
                workspace.display()
            ));
        }
    };
    let deadline = Instant::now() + Duration::from_secs(args.timeout_seconds);
    let mut group = ProcessGroup(Some(
        Pid::from_raw(child.id().expect("spawned shell has a PID") as i32)
            .expect("spawned shell has a positive PID"),
    ));
    let mut stdout = child.stdout.take().expect("stdout was piped");
    let mut stderr = child.stderr.take().expect("stderr was piped");
    let mut out = Capture::default();
    let mut err = Capture::default();
    let observed = loop {
        tokio::select! {
            biased;
            status = child.wait() => break Observed::Exit(status.expect("reap owned shell")),
            () = sleep_until(deadline) => break Observed::Timeout(args.timeout_seconds),
            () = &mut cancelled => break Observed::Cancelled,
            (name, result) = async {
                tokio::select! {
                    result = out.read(&mut stdout), if !out.done => ("stdout", result),
                    result = err.read(&mut stderr), if !err.done => ("stderr", result),
                }
            }, if !out.done || !err.done => {
                if let Err(error) = result {
                    break Observed::Failed(format!("Reading {name} failed: {error}"));
                }
            }
        }
    };
    // Even a successful shell may leave background children, with or without pipes.
    group.terminate();
    let drain = timeout(OUTPUT_DRAIN_TIMEOUT, async {
        // Own the pipes here so the drain deadline closes them even while reaping waits.
        let mut stdout = stdout;
        let mut stderr = stderr;
        tokio::join!(out.drain(&mut stdout), err.drain(&mut stderr))
    });
    let (status, drained) = tokio::join!(child.wait(), drain);
    status.expect("reap owned shell after process group cleanup");
    let mut diagnostics = String::new();
    let mut observed = observed;
    if drained.is_err() {
        diagnostics
            .push_str("Output capture stopped before EOF; additional output may be missing.");
    }
    for (name, error) in [("stdout", &out.error), ("stderr", &err.error)] {
        if let Some(error) = error {
            let message = format!("Reading {name} failed: {error}");
            if matches!(observed, Observed::Exit(_)) {
                observed = Observed::Failed(message);
            } else if !matches!(observed, Observed::Failed(_)) {
                if !diagnostics.is_empty() {
                    diagnostics.push('\n');
                }
                diagnostics.push_str(&message);
            }
        }
    }
    render(observed, out, err, diagnostics)
}

fn failure(message: String) -> ToolOutcome {
    render(
        Observed::Failed(message),
        Capture::default(),
        Capture::default(),
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

fn render(
    observed: Observed,
    mut out: Capture,
    mut err: Capture,
    diagnostics: String,
) -> ToolOutcome {
    let mut status = match &observed {
        Observed::Exit(status) => match status.code() {
            Some(code) => format!("Exit code: {code}"),
            None => format!(
                "Terminated by signal: {}",
                status.signal().expect("Unix signal exit")
            ),
        },
        Observed::Timeout(seconds) => {
            format!("Timed out after {seconds} seconds; partial changes may remain.")
        }
        Observed::Cancelled => "Cancelled during execution; partial changes may remain.".into(),
        Observed::Failed(error) => error.clone(),
    };
    if !diagnostics.is_empty() {
        status.push('\n');
        status.push_str(&diagnostics);
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
    match observed {
        Observed::Exit(exit) if exit.success() => ToolOutcome::Completed(status),
        Observed::Cancelled => ToolOutcome::Cancelled(status),
        _ => ToolOutcome::Failed(status),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::{self, fixture::Workspace};
    use serde_json::json;

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
            120
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
        ] {
            assert!(matches!(
                execute(&workspace.0, args, std::future::pending()).await,
                ToolOutcome::Failed(_)
            ));
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
        let schema = tools::schemas()
            .into_iter()
            .find(|s| s["function"]["name"] == "shell")
            .unwrap();
        let specified: serde_json::Value = serde_json::from_str(
            include_str!("../../eng/plans/2026-09-20-002-shell-tool.md")
                .split("```json\n")
                .nth(1)
                .unwrap()
                .split("\n```")
                .next()
                .unwrap(),
        )
        .unwrap();
        assert_eq!(schema, specified);
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
            let outcome = run(
                &workspace.0,
                "test -z \"${OPENROUTER_API_KEY+x}\" && test \"$OX_SHELL_ENV_TEST\" = inherited",
            )
            .await;
            assert!(matches!(outcome, ToolOutcome::Completed(_)), "{outcome:?}");
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
            let out = Capture {
                bytes: vec![b'X'; out_len].into(),
                ..Capture::default()
            };
            let err = Capture {
                bytes: vec![b'Y'; err_len].into(),
                ..Capture::default()
            };
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
            Observed::Timeout(600),
        ] {
            let capture = || Capture {
                bytes: vec![0xff; OUTPUT_BODY_LIMIT].into(),
                ..Capture::default()
            };
            let result = render(observed, capture(), capture(), "雪".repeat(10000));
            assert!(result.text().len() <= tools::OUTPUT_LIMIT);
            assert_eq!(result.text().matches("earlier output omitted").count(), 2);
        }
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
