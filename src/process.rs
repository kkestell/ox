//! Child processes in a new process group: bounded output tails, cleanup of
//! the whole group, and a runner for one child with optional stdin, a
//! deadline, and cancellation.

use std::{
    collections::VecDeque,
    io,
    process::{ExitStatus, Stdio},
    time::Duration,
};

use rustix::process::{Pid, Signal, kill_process_group, test_kill_process_group};
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWriteExt},
    process::{Child, Command},
    time::{Instant, sleep, sleep_until, timeout},
};

pub const OUTPUT_DRAIN_TIMEOUT: Duration = Duration::from_secs(1);
const GROUP_POLL_INTERVAL: Duration = Duration::from_millis(10);

/// The tail of one output stream, at most `limit` bytes.
#[derive(Clone)]
pub struct Capture {
    pub bytes: VecDeque<u8>,
    /// Earlier output was dropped to stay within the limit.
    pub omitted: bool,
    limit: usize,
    done: bool,
    error: Option<String>,
}

impl Capture {
    pub fn new(limit: usize) -> Self {
        Self {
            bytes: VecDeque::new(),
            omitted: false,
            limit,
            done: false,
            error: None,
        }
    }

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
        self.append(&buffer[..read]);
        Ok(())
    }

    /// Keeps the last `limit` bytes of the stream after `bytes`.
    pub fn append(&mut self, bytes: &[u8]) {
        let excess = (self.bytes.len() + bytes.len()).saturating_sub(self.limit);
        self.omitted |= excess > 0;
        // A limit smaller than one read also drops the front of that read.
        let dropped = excess.min(self.bytes.len());
        self.bytes.drain(..dropped);
        self.bytes.extend(&bytes[excess - dropped..]);
    }

    async fn drain(&mut self, pipe: &mut (impl AsyncRead + Unpin)) -> io::Result<()> {
        while !self.done {
            self.read(pipe).await?;
        }
        Ok(())
    }

    pub fn decode(&mut self) -> String {
        String::from_utf8_lossy(self.bytes.make_contiguous()).into_owned()
    }
}

pub enum Observed {
    Exit(ExitStatus),
    Timeout(Duration),
    Cancelled,
    Failed(String),
}

pub struct Limits {
    pub stdout: usize,
    pub stderr: usize,
    pub deadline: Duration,
    /// How long cleanup waits after SIGTERM for the group to exit before
    /// sending SIGKILL. Zero sends SIGKILL at once.
    pub grace: Duration,
}

/// What was observed about a child after its group was cleaned up.
pub struct Finished {
    pub observed: Observed,
    pub stdout: Capture,
    pub stderr: Capture,
    /// Output capture problems that did not decide the observation.
    pub diagnostics: String,
}

/// Sends SIGKILL to a group. An empty group, or one whose remaining members
/// Ox may not signal, is not an error.
pub fn kill_group(group: Pid) {
    match kill_process_group(group, Signal::KILL) {
        Ok(()) | Err(rustix::io::Errno::SRCH | rustix::io::Errno::PERM) => {}
        Err(error) => panic!("failed to terminate owned process group: {error}"),
    }
}

/// The process group led by a child spawned with `process_group(0)`. It
/// stops the group if dropped before its normal cleanup finishes.
pub struct ProcessGroup(Option<Pid>);

impl ProcessGroup {
    pub fn new(child: &Child) -> Self {
        Self(Some(
            Pid::from_raw(child.id().expect("spawned child has a PID") as i32)
                .expect("spawned child has a positive PID"),
        ))
    }

    /// Sends SIGTERM and waits up to `grace` for the group to exit, or until
    /// `interrupted` completes, then sends SIGKILL. A zero grace sends SIGKILL
    /// at once.
    pub async fn terminate(
        &mut self,
        grace: Duration,
        child: &mut Child,
        interrupted: impl Future<Output = ()>,
    ) {
        let group = self.0.expect("process group is terminated once");
        if !grace.is_zero() {
            match kill_process_group(group, Signal::TERM) {
                Ok(()) | Err(rustix::io::Errno::SRCH | rustix::io::Errno::PERM) => {}
                Err(error) => panic!("failed to signal owned process group: {error}"),
            }
            tokio::pin!(interrupted);
            let deadline = Instant::now() + grace;
            while Instant::now() < deadline {
                // Reaping the leader lets a group whose members all exited
                // report as empty.
                child.try_wait().expect("poll owned child");
                if test_kill_process_group(group).is_err() {
                    break;
                }
                tokio::select! {
                    biased;
                    () = &mut interrupted => break,
                    () = sleep(GROUP_POLL_INTERVAL) => {}
                }
            }
        }
        kill_group(group);
        self.0 = None;
    }
}

impl Drop for ProcessGroup {
    fn drop(&mut self) {
        if let Some(group) = self.0 {
            let _ = kill_process_group(group, Signal::KILL);
        }
    }
}

/// Spawns `command` in a new process group, writes `stdin` if given, and
/// captures output until the child exits, the deadline passes, cancellation
/// arrives, or reading fails. The whole group is then stopped, even after a
/// normal exit, because the child may leave background processes. Fails only
/// when the child cannot start.
pub async fn run(
    mut command: Command,
    stdin: Option<Vec<u8>>,
    limits: Limits,
    cancelled: impl Future<Output = ()>,
) -> io::Result<Finished> {
    let mut child = command
        .stdin(if stdin.is_some() {
            Stdio::piped()
        } else {
            Stdio::null()
        })
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .process_group(0)
        .spawn()?;
    tokio::pin!(cancelled);
    let deadline = Instant::now() + limits.deadline;
    let mut group = ProcessGroup::new(&child);
    let pipe = child.stdin.take();
    // A child may exit without reading its input, so a failed write is not
    // itself a failure. Dropping the pipe closes it.
    let mut write = Box::pin(async move {
        if let (Some(mut pipe), Some(bytes)) = (pipe, stdin) {
            let _ = pipe.write_all(&bytes).await;
        }
    });
    let mut written = false;
    let mut stdout = child.stdout.take().expect("stdout was piped");
    let mut stderr = child.stderr.take().expect("stderr was piped");
    let mut out = Capture::new(limits.stdout);
    let mut err = Capture::new(limits.stderr);
    let observed = loop {
        tokio::select! {
            biased;
            status = child.wait() => break Observed::Exit(status.expect("reap owned child")),
            () = sleep_until(deadline) => break Observed::Timeout(limits.deadline),
            () = &mut cancelled => break Observed::Cancelled,
            () = &mut write, if !written => written = true,
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
    drop(write);
    let cleanup = async {
        group
            .terminate(limits.grace, &mut child, std::future::pending())
            .await;
        child
            .wait()
            .await
            .expect("reap owned child after process group cleanup");
    };
    let drain = timeout(limits.grace + OUTPUT_DRAIN_TIMEOUT, async {
        // Own the pipes here so the drain deadline closes them even while reaping waits.
        let mut stdout = stdout;
        let mut stderr = stderr;
        tokio::join!(out.drain(&mut stdout), err.drain(&mut stderr))
    });
    let ((), drained) = tokio::join!(cleanup, drain);
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
    Ok(Finished {
        observed,
        stdout: out,
        stderr: err,
        diagnostics,
    })
}
