//! The shell processes of one active session: background commands started by
//! the shell tool, each belonging to the agent that started it. Each has one
//! supervisor task that owns its child, process group, and output capture
//! until the command ends and its group is cleaned up. The owner registers,
//! looks up, kills, and shuts down shell processes.

use std::{
    io,
    process::{ExitStatus, Stdio},
    sync::{Arc, Mutex, MutexGuard},
    time::Duration,
};

use agent_client_protocol::schema::v1::SessionId;
use futures::{
    FutureExt,
    future::{BoxFuture, Shared, join_all},
};
use tokio::{
    io::AsyncWriteExt,
    process::{Child, ChildStdin, Command},
    sync::watch,
    time::{sleep, timeout},
};

use crate::process::{Capture, OutputPipes, ProcessGroup, Stream};

/// The most shell processes one agent of an active session retains.
const MAX_SHELL_PROCESSES: usize = 16;
/// How long an explicit stop waits after SIGTERM before sending SIGKILL.
const STOP_GRACE: Duration = Duration::from_secs(2);
/// The longest one write waits for the command to accept its input.
const WRITE_TIMEOUT: Duration = Duration::from_secs(5);

/// The shell processes of one active session. Clones share them.
#[derive(Clone, Default)]
pub struct ShellProcesses(Arc<Mutex<Registry>>);

#[derive(Default)]
struct Registry {
    /// Set when shutdown begins; no command starts afterward.
    closed: bool,
    /// Oldest first.
    processes: Vec<ShellProcess>,
}

/// A handle to one shell process. Clones refer to the same command.
#[derive(Clone)]
pub struct ShellProcess {
    id: String,
    /// The agent session ID of the agent that started it.
    session_id: SessionId,
    command: String,
    output: Arc<Mutex<Output>>,
    stdin: Arc<tokio::sync::Mutex<Option<ChildStdin>>>,
    ending: Arc<watch::Sender<Ending>>,
    /// Completes after the command ended, its group was cleaned up, and its
    /// final state was published.
    supervisor: Shared<BoxFuture<'static, ()>>,
}

/// What the supervisor is asked to do, in increasing severity.
#[derive(Clone, Copy, PartialEq)]
enum Ending {
    None,
    /// An explicit stop: SIGTERM, then SIGKILL after the grace period.
    Stop,
    /// Owner shutdown or the end of the subagent that started it: SIGKILL at
    /// once.
    Kill,
}

/// What is known about a shell process's command.
#[derive(Clone, Debug, PartialEq)]
pub enum State {
    Running,
    /// The command exited on its own.
    Exited(ExitStatus),
    /// The command ended after an explicit stop, owner shutdown, or the end
    /// of the subagent that started it.
    Stopped(ExitStatus),
    /// Reading its output failed, so Ox ended the command.
    Failed {
        error: String,
        status: ExitStatus,
    },
}

/// The state and retained output tails of one shell process.
#[derive(Clone)]
pub struct Output {
    pub state: State,
    pub stdout: Capture,
    pub stderr: Capture,
    /// Output capture problems that did not decide the state.
    pub diagnostics: String,
}

/// What one write accomplished.
#[derive(Debug, PartialEq)]
pub struct Written {
    pub bytes: usize,
    pub stdin_closed: bool,
    /// Why the write stopped before finishing, if it did.
    pub interruption: Option<Interruption>,
}

#[derive(Debug, PartialEq)]
pub enum Interruption {
    StdinClosed,
    Finished,
    TimedOut(Duration),
    Cancelled,
    Failed(String),
}

impl ShellProcesses {
    /// Spawns `command` with piped stdin, stdout, and stderr in a new process
    /// group and registers it under the agent session ID `session_id`,
    /// keeping up to `output_limit` bytes of each stream. When that agent
    /// already retains the most shell processes, its oldest finished one is
    /// removed first. Fails without spawning when shutdown has begun or every
    /// shell process that agent retains is running.
    pub fn start(
        &self,
        session_id: &SessionId,
        mut command: Command,
        command_text: &str,
        output_limit: usize,
    ) -> io::Result<ShellProcess> {
        // Spawning under the lock means a concurrent shutdown either sees
        // this process or prevents it from starting.
        let mut registry = self.lock();
        if registry.closed {
            return Err(io::Error::other(
                "this session's shell processes are shutting down",
            ));
        }
        let retained = registry
            .processes
            .iter()
            .filter(|process| &process.session_id == session_id);
        let removable = if retained.clone().count() < MAX_SHELL_PROCESSES {
            None
        } else {
            Some(
                registry
                    .processes
                    .iter()
                    .position(|process| &process.session_id == session_id && process.is_finished())
                    .ok_or_else(|| {
                        io::Error::other(format!(
                            "{MAX_SHELL_PROCESSES} shell processes are running; stop one before starting another"
                        ))
                    })?,
            )
        };
        let mut child = command
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .process_group(0)
            .spawn()?;
        let group = ProcessGroup::new(&child);
        if let Some(index) = removable {
            registry.processes.remove(index);
        }
        let output = Arc::new(Mutex::new(Output {
            state: State::Running,
            stdout: Capture::new(output_limit),
            stderr: Capture::new(output_limit),
            diagnostics: String::new(),
        }));
        let (ending, requests) = watch::channel(Ending::None);
        let stdin = Arc::new(tokio::sync::Mutex::new(child.stdin.take()));
        let supervisor = tokio::spawn(supervise(
            child,
            group,
            output.clone(),
            stdin.clone(),
            requests,
        ))
        .map(|result| result.expect("shell process supervisor panicked"))
        .boxed()
        .shared();
        let process = ShellProcess {
            id: uuid::Uuid::new_v4().to_string(),
            session_id: session_id.clone(),
            command: command_text.to_owned(),
            output,
            stdin,
            ending: Arc::new(ending),
            supervisor,
        };
        registry.processes.push(process.clone());
        Ok(process)
    }

    /// Every shell process the agent session ID `session_id` retains, oldest
    /// first.
    pub fn list(&self, session_id: &SessionId) -> Vec<ShellProcess> {
        self.lock()
            .processes
            .iter()
            .filter(|process| &process.session_id == session_id)
            .cloned()
            .collect()
    }

    /// The shell process `process_id`, only when the agent session ID
    /// `session_id` started it.
    pub fn get(&self, session_id: &SessionId, process_id: &str) -> Option<ShellProcess> {
        self.lock()
            .processes
            .iter()
            .find(|process| &process.session_id == session_id && process.id == process_id)
            .cloned()
    }

    /// Asks the supervisor of every shell process the agent session ID
    /// `session_id` started to kill its group at once, without waiting.
    pub fn kill(&self, session_id: &SessionId) {
        for process in &self.lock().processes {
            if &process.session_id == session_id {
                process.ending.send_replace(Ending::Kill);
            }
        }
    }

    /// Kills every shell process the agent session ID `session_id` started,
    /// waits until each group was cleaned up, and removes them.
    pub async fn remove(&self, session_id: &SessionId) {
        self.kill(session_id);
        let supervisors: Vec<_> = self
            .list(session_id)
            .iter()
            .map(|process| process.supervisor.clone())
            .collect();
        join_all(supervisors).await;
        self.lock()
            .processes
            .retain(|process| &process.session_id != session_id);
    }

    /// Closes registration and asks every supervisor to kill its group at
    /// once, without waiting. Repeating it is harmless.
    pub fn begin_shutdown(&self) {
        let mut registry = self.lock();
        registry.closed = true;
        for process in &registry.processes {
            process.ending.send_replace(Ending::Kill);
        }
    }

    /// Begins shutdown and waits until every command ended and its group was
    /// cleaned up.
    pub async fn shutdown(&self) {
        self.begin_shutdown();
        let supervisors: Vec<_> = self
            .lock()
            .processes
            .iter()
            .map(|process| process.supervisor.clone())
            .collect();
        join_all(supervisors).await;
    }

    fn lock(&self) -> MutexGuard<'_, Registry> {
        self.0.lock().expect("shell processes mutex poisoned")
    }
}

impl ShellProcess {
    /// The opaque shell process ID, never an operating-system PID.
    pub fn id(&self) -> &str {
        &self.id
    }

    pub fn command(&self) -> &str {
        &self.command
    }

    pub fn output(&self) -> Output {
        self.lock_output().clone()
    }

    pub fn state(&self) -> State {
        self.lock_output().state.clone()
    }

    fn is_finished(&self) -> bool {
        self.lock_output().state != State::Running
    }

    fn lock_output(&self) -> MutexGuard<'_, Output> {
        lock(&self.output)
    }

    /// Waits up to `limit` for the command to end. False when `cancelled`
    /// completed first.
    pub async fn wait(&self, limit: Duration, cancelled: impl Future<Output = ()>) -> bool {
        tokio::select! {
            biased;
            () = cancelled => false,
            _ = timeout(limit, self.supervisor.clone()) => true,
        }
    }

    /// Writes `text` to stdin within the write timeout, then closes stdin if
    /// `close_stdin` and the whole text was written. A failed write closes
    /// stdin; a timeout or cancellation leaves it open.
    pub async fn write(
        &self,
        text: &[u8],
        close_stdin: bool,
        cancelled: impl Future<Output = ()>,
    ) -> Written {
        let mut stdin = self.stdin.lock().await;
        let interrupted = |interruption, stdin_closed| Written {
            bytes: 0,
            stdin_closed,
            interruption: Some(interruption),
        };
        if self.is_finished() {
            return interrupted(Interruption::Finished, stdin.is_none());
        }
        let Some(pipe) = stdin.as_mut() else {
            return interrupted(Interruption::StdinClosed, true);
        };
        let mut bytes = 0;
        let interruption = tokio::select! {
            biased;
            () = cancelled => Some(Interruption::Cancelled),
            () = sleep(WRITE_TIMEOUT) => Some(Interruption::TimedOut(WRITE_TIMEOUT)),
            result = async {
                while bytes < text.len() {
                    bytes += pipe.write(&text[bytes..]).await?;
                }
                io::Result::Ok(())
            } => result.err().map(|error| Interruption::Failed(error.to_string())),
        };
        if matches!(interruption, Some(Interruption::Failed(_)))
            || (interruption.is_none() && close_stdin)
        {
            // Dropping the pipe closes it.
            *stdin = None;
        }
        Written {
            bytes,
            stdin_closed: stdin.is_none(),
            interruption,
        }
    }

    /// Asks the supervisor to stop the command and waits for its bounded
    /// cleanup, which continues if the caller stops waiting. A finished
    /// command is not signalled.
    pub async fn stop(&self) -> Output {
        self.ending.send_if_modified(|ending| {
            let requested = *ending == Ending::None;
            if requested {
                *ending = Ending::Stop;
            }
            requested
        });
        self.supervisor.clone().await;
        self.output()
    }
}

/// Why the supervisor's main loop ended.
enum Reason {
    Exited,
    Requested,
    Failed(String),
}

/// Captures output until the command exits, a stop or shutdown is requested,
/// or reading fails; then cleans up the whole group, reaps the child, drains
/// the remaining output, and publishes the final state.
async fn supervise(
    mut child: Child,
    mut group: ProcessGroup,
    output: Arc<Mutex<Output>>,
    stdin: Arc<tokio::sync::Mutex<Option<ChildStdin>>>,
    mut requests: watch::Receiver<Ending>,
) {
    let mut pipes = OutputPipes::new(&mut child, |stream, bytes: &[u8]| {
        let mut output = lock(&output);
        match stream {
            Stream::Stdout => output.stdout.append(bytes),
            Stream::Stderr => output.stderr.append(bytes),
        }
    });
    let reason = tokio::select! {
        biased;
        status = child.wait() => {
            status.expect("reap owned child");
            Reason::Exited
        }
        // A dropped owner counts as shutdown.
        _ = requests.wait_for(|ending| *ending != Ending::None) => Reason::Requested,
        error = pipes.read_until_failure() => Reason::Failed(error),
    };
    let grace = match (&reason, *requests.borrow()) {
        (Reason::Requested, Ending::Stop) => STOP_GRACE,
        _ => Duration::ZERO,
    };
    let drained = pipes
        .drain_during(async {
            let kill_requested = async {
                let _ = requests.wait_for(|ending| *ending == Ending::Kill).await;
            };
            group.terminate(grace, &mut child, kill_requested).await;
            child
                .wait()
                .await
                .expect("reap owned child after process group cleanup")
        })
        .await;
    // A completed command cannot receive input. Wait for an in-flight write
    // to finish before publishing the final state.
    *stdin.lock().await = None;
    let status = drained.status;
    let exited = matches!(reason, Reason::Exited);
    let (late_failure, diagnostics) = drained.into_failure_and_diagnostics(exited);
    let failure = match reason {
        Reason::Failed(error) => Some(error),
        Reason::Exited | Reason::Requested => late_failure,
    };
    let mut output = lock(&output);
    output.diagnostics = diagnostics;
    output.state = match failure {
        Some(error) => State::Failed { error, status },
        None if exited => State::Exited(status),
        None => State::Stopped(status),
    };
}

fn lock(output: &Mutex<Output>) -> MutexGuard<'_, Output> {
    output.lock().expect("shell process output mutex poisoned")
}

#[cfg(test)]
mod tests {
    use std::{os::unix::process::ExitStatusExt, path::Path};

    use tokio::time::Instant;

    use super::*;
    use crate::{process::OUTPUT_DRAIN_TIMEOUT, tools::fixture::Workspace};

    const LIMIT: usize = 1024;

    fn agent() -> SessionId {
        SessionId::new("agent")
    }

    fn shell(workspace: &Path, command: &str) -> Command {
        let mut shell = Command::new("/bin/sh");
        shell.arg("-c").arg(command).current_dir(workspace);
        shell
    }

    fn start(owner: &ShellProcesses, workspace: &Path, command: &str) -> ShellProcess {
        start_as(owner, &agent(), workspace, command)
    }

    fn start_as(
        owner: &ShellProcesses,
        session_id: &SessionId,
        workspace: &Path,
        command: &str,
    ) -> ShellProcess {
        owner
            .start(session_id, shell(workspace, command), command, LIMIT)
            .unwrap()
    }

    /// Waits until the command has printed `marker` to stdout.
    async fn printed(process: &ShellProcess, marker: &str) {
        timeout(Duration::from_secs(5), async {
            while !process.output().stdout.clone().decode().contains(marker) {
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap_or_else(|_| panic!("{} never printed {marker}", process.command()));
    }

    async fn finished(process: &ShellProcess) -> Output {
        assert!(
            process
                .wait(Duration::from_secs(5), std::future::pending())
                .await
        );
        let output = process.output();
        assert_ne!(output.state, State::Running, "{}", process.command());
        output
    }

    /// Waits until the PID in `file` no longer names a live process; a zombie
    /// counts as stopped only for a descendant, which Ox does not reap.
    async fn assert_gone(file: &Path, reaped: bool) {
        let pid = std::fs::read_to_string(file).unwrap();
        timeout(Duration::from_secs(3), async {
            loop {
                let output = Command::new("ps")
                    .args(["-o", "stat=", "-p", pid.trim()])
                    .output()
                    .await
                    .unwrap();
                let state = String::from_utf8(output.stdout).unwrap();
                if state.trim().is_empty() || (!reaped && state.trim().starts_with('Z')) {
                    break;
                }
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap_or_else(|_| panic!("{} is still running", file.display()));
    }

    #[tokio::test]
    async fn a_command_keeps_running_and_receives_exact_input() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let process = start(&owner, &workspace.0, "printf ready; cat");
        printed(&process, "ready").await;
        let other = start(&owner, &workspace.0, "printf other");
        finished(&other).await;
        assert_eq!(process.state(), State::Running, "other work proceeds");
        for (text, close_stdin) in [("one\ntwo", false), ("", true)] {
            assert_eq!(
                process
                    .write(text.as_bytes(), close_stdin, std::future::pending())
                    .await,
                Written {
                    bytes: text.len(),
                    stdin_closed: close_stdin,
                    interruption: None,
                }
            );
        }
        let output = finished(&process).await;
        assert_eq!(output.state, State::Exited(ExitStatus::from_raw(0)));
        assert_eq!(output.stdout.clone().decode(), "readyone\ntwo");
        assert_eq!(
            process.write(b"late", false, std::future::pending()).await,
            Written {
                bytes: 0,
                stdin_closed: true,
                interruption: Some(Interruption::Finished),
            }
        );
        assert_eq!(
            owner
                .list(&agent())
                .iter()
                .map(ShellProcess::command)
                .collect::<Vec<_>>(),
            ["printf ready; cat", "printf other"]
        );
    }

    #[tokio::test]
    async fn finished_commands_report_their_exit_and_final_output() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        for (command, status, stdout) in [
            ("printf done", ExitStatus::from_raw(0), "done"),
            (
                "printf '\\377tail'; exit 7",
                ExitStatus::from_raw(7 << 8),
                "\u{fffd}tail",
            ),
            (
                "printf last; kill -TERM $$",
                ExitStatus::from_raw(15),
                "last",
            ),
        ] {
            let output = finished(&start(&owner, &workspace.0, command)).await;
            assert_eq!(output.state, State::Exited(status), "{command}");
            assert_eq!(output.stdout.clone().decode(), stdout, "{command}");
        }
    }

    #[tokio::test]
    async fn finished_commands_release_stdin() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let process = start(&owner, &workspace.0, "true");
        finished(&process).await;

        assert!(process.stdin.lock().await.is_none());
        assert_eq!(
            process.write(b"late", false, std::future::pending()).await,
            Written {
                bytes: 0,
                stdin_closed: true,
                interruption: Some(Interruption::Finished),
            }
        );
        owner.shutdown().await;
    }

    /// Waits until the command's output satisfies `ready`.
    async fn until(process: &ShellProcess, what: &str, ready: impl Fn(&Output) -> bool) {
        timeout(Duration::from_secs(5), async {
            while !ready(&process.output()) {
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap_or_else(|_| panic!("{}: {what}", process.command()));
    }

    fn ends_with(capture: &Capture, marker: &str) -> bool {
        capture.clone().decode().ends_with(marker)
    }

    #[tokio::test]
    async fn output_floods_are_drained_fairly_and_snapshots_repeat() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        // Each stream has its own producer, so neither waits for the other.
        let process = start(
            &owner,
            &workspace.0,
            "(i=0; while [ $i -lt 5000 ]; do printf 'stdout line\\n'; i=$((i+1)); done; printf OUT_END; touch out-flooded) & \
             (i=0; while [ $i -lt 5000 ]; do printf 'stderr line\\n' >&2; i=$((i+1)); done; printf ERR_END >&2; touch err-flooded) & \
             wait; read wait",
        );
        timeout(Duration::from_secs(5), async {
            while !workspace.0.join("out-flooded").exists()
                || !workspace.0.join("err-flooded").exists()
            {
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("the command progressed although nothing read its output");
        until(&process, "both markers retained", |output| {
            ends_with(&output.stdout, "OUT_END") && ends_with(&output.stderr, "ERR_END")
        })
        .await;
        let first = process.output();
        let second = process.output();
        for capture in [&first.stdout, &first.stderr] {
            assert!(capture.omitted);
            assert_eq!(capture.bytes.len(), LIMIT);
        }
        assert_eq!(
            first.stdout.bytes, second.stdout.bytes,
            "reads do not consume"
        );
        assert_eq!(first.stderr.bytes, second.stderr.bytes);

        let starving = start(
            &owner,
            &workspace.0,
            "yes & i=0; while [ $i -lt 100 ]; do printf 'stderr line\\n' >&2; i=$((i+1)); done; printf ERR_END >&2; wait",
        );
        until(&starving, "continuous stdout starved stderr", |output| {
            ends_with(&output.stderr, "ERR_END")
        })
        .await;
        owner.shutdown().await;
    }

    #[tokio::test]
    async fn the_limit_removes_the_oldest_finished_process_and_never_a_running_one() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let other = SessionId::new("other");
        let other_done = start_as(&owner, &other, &workspace.0, "true");
        finished(&other_done).await;
        let done = start(&owner, &workspace.0, "true");
        finished(&done).await;
        let running: Vec<_> = (1..MAX_SHELL_PROCESSES)
            .map(|_| start(&owner, &workspace.0, "exec sleep 30"))
            .collect();
        let replacement = start(&owner, &workspace.0, "exec sleep 30");
        assert!(
            owner.get(&agent(), done.id()).is_none(),
            "the finished process was removed"
        );
        assert!(
            owner.get(&other, other_done.id()).is_some(),
            "another agent's finished process was kept"
        );
        assert_eq!(owner.list(&agent()).len(), MAX_SHELL_PROCESSES);
        let refused = owner
            .start(
                &agent(),
                shell(&workspace.0, "touch spawned"),
                "touch spawned",
                LIMIT,
            )
            .map(|process| process.id().to_owned())
            .unwrap_err();
        assert!(
            refused
                .to_string()
                .contains("16 shell processes are running")
        );
        assert!(
            running
                .iter()
                .all(|process| owner.get(&agent(), process.id()).is_some())
        );
        assert!(owner.get(&agent(), replacement.id()).is_some());
        let unaffected = start_as(&owner, &other, &workspace.0, "exec sleep 30");
        assert_eq!(unaffected.state(), State::Running);
        assert_eq!(owner.list(&other).len(), 2);
        owner.shutdown().await;
        assert!(!workspace.0.join("spawned").exists(), "nothing was spawned");
    }

    #[tokio::test]
    async fn removing_an_agent_session_kills_and_forgets_only_its_shell_processes() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let other = SessionId::new("other");
        let removed = start(&owner, &workspace.0, &format!("trap '' TERM; {TREE}"));
        let kept = start_as(&owner, &other, &workspace.0, "exec sleep 30");
        printed(&removed, "ready").await;
        let start_time = Instant::now();
        owner.remove(&agent()).await;
        assert!(start_time.elapsed() < STOP_GRACE, "no SIGTERM grace period");
        assert_eq!(removed.state(), State::Stopped(ExitStatus::from_raw(9)));
        assert_gone(&workspace.0.join("shell"), true).await;
        assert_gone(&workspace.0.join("child"), false).await;
        assert!(owner.list(&agent()).is_empty());
        assert_eq!(
            owner.get(&other, kept.id()).map(|process| process.state()),
            Some(State::Running)
        );
        owner.shutdown().await;
    }

    #[tokio::test]
    async fn writes_are_bounded_and_report_what_was_sent() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let ignoring = start(&owner, &workspace.0, "printf ready; exec sleep 30");
        printed(&ignoring, "ready").await;
        let flood = vec![b'x'; 1024 * 1024];
        let start_time = Instant::now();
        let written = ignoring.write(&flood, true, std::future::pending()).await;
        assert!(start_time.elapsed() >= WRITE_TIMEOUT);
        assert!(
            written.bytes > 0 && written.bytes < flood.len(),
            "{written:?}"
        );
        assert_eq!(
            (written.stdin_closed, written.interruption),
            (false, Some(Interruption::TimedOut(WRITE_TIMEOUT)))
        );
        assert_eq!(
            ignoring.state(),
            State::Running,
            "a timed-out write keeps it running"
        );

        // The reader takes its input only after the cancelled write stopped.
        let reader = start(
            &owner,
            &workspace.0,
            "while [ ! -e go ]; do sleep 0.01; done; wc -c",
        );
        let (cancel, cancelled) = futures::channel::oneshot::channel::<()>();
        let write = reader.write(&flood, false, async {
            let _ = cancelled.await;
        });
        tokio::pin!(write);
        // A megabyte cannot fit in the pipe, so the write fills it and waits.
        assert!(
            timeout(Duration::from_millis(200), &mut write)
                .await
                .is_err(),
            "the write waits for the command"
        );
        cancel.send(()).unwrap();
        let written = write.await;
        assert!(
            written.bytes > 0 && written.bytes < flood.len(),
            "{written:?}"
        );
        assert_eq!(
            (written.stdin_closed, written.interruption),
            (false, Some(Interruption::Cancelled))
        );
        assert_eq!(
            reader.state(),
            State::Running,
            "cancellation keeps it running"
        );
        std::fs::write(workspace.0.join("go"), "").unwrap();
        assert_eq!(
            reader.write(b"", true, std::future::pending()).await,
            Written {
                bytes: 0,
                stdin_closed: true,
                interruption: None,
            }
        );
        assert_eq!(
            finished(&reader).await.stdout.clone().decode().trim(),
            written.bytes.to_string(),
            "only the reported prefix was sent"
        );

        let closed = start(
            &owner,
            &workspace.0,
            "exec 0<&-; printf ready; exec sleep 30",
        );
        printed(&closed, "ready").await;
        let written = closed.write(b"text", false, std::future::pending()).await;
        assert!(
            matches!(written.interruption, Some(Interruption::Failed(ref error)) if error.contains("Broken pipe")),
            "{written:?}"
        );
        assert!(written.stdin_closed);
        assert_eq!(
            closed.write(b"text", false, std::future::pending()).await,
            Written {
                bytes: 0,
                stdin_closed: true,
                interruption: Some(Interruption::StdinClosed),
            }
        );
        owner.shutdown().await;
    }

    const TREE: &str = "echo $$ > shell; sleep 30 & echo $! > child; printf ready; wait";

    #[tokio::test]
    async fn stop_terminates_the_group_with_a_grace_period_and_reaps_it() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        for (trap, grace_used, status) in [
            ("touch terminated; exit 0", false, ExitStatus::from_raw(0)),
            ("", true, ExitStatus::from_raw(9)),
        ] {
            let process = start(&owner, &workspace.0, &format!("trap '{trap}' TERM; {TREE}"));
            printed(&process, "ready").await;
            let start_time = Instant::now();
            let output = process.stop().await;
            assert_eq!(start_time.elapsed() >= STOP_GRACE, grace_used, "{trap}");
            assert_eq!(output.state, State::Stopped(status), "{trap}");
            assert_eq!(
                std::fs::remove_file(workspace.0.join("terminated")).is_ok(),
                !grace_used
            );
            assert_gone(&workspace.0.join("shell"), true).await;
            assert_gone(&workspace.0.join("child"), false).await;
            let again = Instant::now();
            assert_eq!(process.stop().await.state, State::Stopped(status));
            assert!(
                again.elapsed() < Duration::from_millis(100),
                "no second cleanup"
            );
        }
    }

    #[tokio::test]
    async fn natural_exit_stops_remaining_descendants() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let process = start(
            &owner,
            &workspace.0,
            "echo $$ > shell; sleep 30 & echo $! > child; exit 0",
        );
        assert_eq!(
            timeout(Duration::from_secs(3), finished(&process))
                .await
                .expect("output drained after the descendants stopped")
                .state,
            State::Exited(ExitStatus::from_raw(0))
        );
        assert_gone(&workspace.0.join("child"), false).await;
    }

    #[tokio::test]
    async fn cancelling_a_waiting_read_leaves_the_command_running() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let process = start(&owner, &workspace.0, "exec sleep 30");
        let (cancel, cancelled) = futures::channel::oneshot::channel::<()>();
        let wait = process.wait(Duration::from_secs(30), async {
            let _ = cancelled.await;
        });
        tokio::pin!(wait);
        assert!(futures::poll!(&mut wait).is_pending(), "the read waits");
        cancel.send(()).unwrap();
        assert!(!wait.await);
        assert!(process.wait(Duration::ZERO, std::future::pending()).await);
        assert_eq!(process.state(), State::Running);
        owner.shutdown().await;
        assert_eq!(process.state(), State::Stopped(ExitStatus::from_raw(9)));
    }

    #[tokio::test]
    async fn shutdown_kills_at_once_even_during_a_stop_grace_period() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        // The detached process keeps the output pipes open past the cleanup.
        let stubborn = start(
            &owner,
            &workspace.0,
            "python3 -c 'import subprocess; p = subprocess.Popen([\"sleep\", \"30\"], start_new_session=True); open(\"detached\", \"w\").write(str(p.pid))'; \
             trap 'touch terminated' TERM; echo $$ > shell; printf ready; while :; do sleep 1; done",
        );
        let other = start(&owner, &workspace.0, "trap '' TERM; exec sleep 30");
        printed(&stubborn, "ready").await;
        let stopping = tokio::spawn({
            let stubborn = stubborn.clone();
            async move { stubborn.stop().await }
        });
        timeout(Duration::from_secs(3), async {
            while !workspace.0.join("terminated").exists() {
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("the stop sent SIGTERM");
        let start_time = Instant::now();
        owner.shutdown().await;
        let elapsed = start_time.elapsed();
        let detached = std::fs::read_to_string(workspace.0.join("detached")).unwrap();
        rustix::process::kill_process(
            rustix::process::Pid::from_raw(detached.parse().unwrap()).unwrap(),
            rustix::process::Signal::KILL,
        )
        .unwrap();
        assert!(
            elapsed >= OUTPUT_DRAIN_TIMEOUT && elapsed < STOP_GRACE,
            "shutdown sent SIGKILL at once and then drained for its own deadline: {elapsed:?}"
        );
        let stopped = stopping.await.unwrap();
        assert_eq!(stopped.state, State::Stopped(ExitStatus::from_raw(9)));
        assert!(
            stopped
                .diagnostics
                .contains("Output capture stopped before EOF")
        );
        assert_eq!(other.state(), State::Stopped(ExitStatus::from_raw(9)));
        assert_gone(&workspace.0.join("shell"), true).await;
        owner.shutdown().await;
    }

    #[tokio::test]
    async fn an_output_read_failure_kills_the_group_at_once_and_reaps_the_child() {
        use std::os::fd::OwnedFd;
        use tokio::net::{TcpListener, TcpSocket};

        let workspace = Workspace::new();
        // Ignoring SIGTERM means only an immediate SIGKILL ends it quickly.
        let mut child = shell(
            &workspace.0,
            "trap '' TERM; echo $$ > shell; sleep 30 & echo $! > child; touch ready; wait",
        )
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .process_group(0)
        .spawn()
        .unwrap();
        let group = ProcessGroup::new(&child);
        timeout(Duration::from_secs(5), async {
            while !workspace.0.join("ready").exists() {
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("the command started its descendant");

        // A connection whose peer resets it makes a real read fail, standing
        // in for the child's stdout pipe.
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let peer = TcpSocket::new_v4().unwrap();
        peer.set_zero_linger().unwrap();
        let peer = peer.connect(listener.local_addr().unwrap()).await.unwrap();
        let (accepted, _) = listener.accept().await.unwrap();
        drop(peer);
        let failing = OwnedFd::from(accepted.into_std().unwrap());
        child.stdout = Some(
            tokio::process::ChildStdout::from_std(std::process::ChildStdout::from(failing))
                .unwrap(),
        );

        let output = Arc::new(Mutex::new(Output {
            state: State::Running,
            stdout: Capture::new(LIMIT),
            stderr: Capture::new(LIMIT),
            diagnostics: String::new(),
        }));
        // A live sender, so the supervisor sees no stop or shutdown request.
        let (_ending, requests) = watch::channel(Ending::None);
        let stdin = Arc::new(tokio::sync::Mutex::new(None));
        let start_time = Instant::now();
        timeout(
            Duration::from_secs(5),
            supervise(child, group, output.clone(), stdin, requests),
        )
        .await
        .expect("the supervisor finished its cleanup");
        assert!(start_time.elapsed() < STOP_GRACE, "no SIGTERM grace period");
        match &lock(&output).state {
            State::Failed { error, status } => {
                assert!(
                    error.starts_with("Reading stdout failed: ") && error.contains("reset"),
                    "{error}"
                );
                assert_eq!(*status, ExitStatus::from_raw(9));
            }
            state => panic!("{state:?}"),
        }
        assert_gone(&workspace.0.join("shell"), true).await;
        assert_gone(&workspace.0.join("child"), false).await;
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 4)]
    async fn a_start_racing_shutdown_is_refused_or_cleaned_up() {
        let workspace = Workspace::new();
        let owner = ShellProcesses::default();
        let starts: Vec<_> = (0..8)
            .map(|index| {
                let owner = owner.clone();
                let workspace = workspace.0.clone();
                tokio::spawn(async move {
                    let command = format!("touch started-{index}; exec sleep 30");
                    (
                        index,
                        owner.start(&agent(), shell(&workspace, &command), &command, LIMIT),
                    )
                })
            })
            .collect();
        owner.shutdown().await;
        for start in starts {
            let (index, started) = start.await.unwrap();
            match started {
                Ok(process) => {
                    owner.shutdown().await;
                    assert_ne!(process.state(), State::Running, "{index} was cleaned up");
                }
                Err(error) => {
                    assert!(error.to_string().contains("shutting down"));
                    assert!(!workspace.0.join(format!("started-{index}")).exists());
                }
            }
        }
    }
}
