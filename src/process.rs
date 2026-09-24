//! Child processes in a new process group: bounded output tails, reading and
//! draining a child's output pipes into a caller's sink, cleanup of the whole
//! group, and a runner for one child with optional stdin, a deadline, and
//! cancellation.

use std::{
    collections::VecDeque,
    io,
    process::{ExitStatus, Stdio},
    time::Duration,
};

use rustix::process::{Pid, Signal, kill_process_group, test_kill_process_group};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    process::{Child, ChildStderr, ChildStdout, Command},
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
}

impl Capture {
    pub fn new(limit: usize) -> Self {
        Self {
            bytes: VecDeque::new(),
            omitted: false,
            limit,
        }
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

    pub fn decode(&mut self) -> String {
        String::from_utf8_lossy(self.bytes.make_contiguous()).into_owned()
    }
}

/// One of a child's two output streams.
#[derive(Clone, Copy)]
pub enum Stream {
    Stdout,
    Stderr,
}

impl Stream {
    pub fn name(self) -> &'static str {
        match self {
            Self::Stdout => "stdout",
            Self::Stderr => "stderr",
        }
    }
}

/// The stdout and stderr pipes of one child, and the sink `append` their
/// output is written to.
pub struct OutputPipes<A> {
    stdout: ChildStdout,
    stderr: ChildStderr,
    stdout_open: bool,
    stderr_open: bool,
    append: A,
}

/// What draining a child's output pipes observed.
pub struct Drained {
    /// The exit status the cleanup returned.
    pub status: ExitStatus,
    /// Both pipes reached EOF.
    pub complete: bool,
    /// Read errors seen while draining, stdout's first.
    pub errors: Vec<String>,
}

impl<A: FnMut(Stream, &[u8])> OutputPipes<A> {
    /// Takes the child's stdout and stderr, both of which must be piped.
    pub fn new(child: &mut Child, append: A) -> Self {
        Self {
            stdout: child.stdout.take().expect("stdout was piped"),
            stderr: child.stderr.take().expect("stderr was piped"),
            stdout_open: true,
            stderr_open: true,
            append,
        }
    }

    fn open(&self) -> bool {
        self.stdout_open || self.stderr_open
    }

    /// Reads one chunk from whichever open pipe has output into the sink. EOF
    /// or a read error closes that pipe; an error is returned with its
    /// stream. Cancel-safe.
    async fn read_chunk(&mut self) -> Option<(Stream, String)> {
        let mut stdout_buffer = [0; 8192];
        let mut stderr_buffer = [0; 8192];
        // Unbiased between the streams, so continuous output on one cannot
        // starve the other.
        let (stream, read) = tokio::select! {
            read = self.stdout.read(&mut stdout_buffer), if self.stdout_open => (Stream::Stdout, read),
            read = self.stderr.read(&mut stderr_buffer), if self.stderr_open => (Stream::Stderr, read),
        };
        let (open, buffer) = match stream {
            Stream::Stdout => (&mut self.stdout_open, &stdout_buffer),
            Stream::Stderr => (&mut self.stderr_open, &stderr_buffer),
        };
        match read {
            Ok(0) => {
                *open = false;
                None
            }
            Ok(read) => {
                (self.append)(stream, &buffer[..read]);
                None
            }
            Err(error) => {
                *open = false;
                Some((stream, format!("Reading {} failed: {error}", stream.name())))
            }
        }
    }

    /// Reads output into the sink until a read fails, and returns that
    /// failure. Once both pipes reached EOF, it never completes. Cancel-safe,
    /// so a caller may recreate it on each iteration of its loop.
    pub async fn read_until_failure(&mut self) -> String {
        while self.open() {
            if let Some((_, error)) = self.read_chunk().await {
                return error;
            }
        }
        std::future::pending().await
    }

    /// Drains both pipes into the sink while `cleanup` stops the group and
    /// reaps the child, then for up to `OUTPUT_DRAIN_TIMEOUT` more.
    pub async fn drain_during(mut self, cleanup: impl Future<Output = ExitStatus>) -> Drained {
        let mut errors = [None, None];
        let (status, complete) = {
            let drain = async {
                while self.open() {
                    if let Some((stream, error)) = self.read_chunk().await {
                        errors[stream as usize] = Some(error);
                    }
                }
            };
            tokio::pin!(cleanup, drain);
            // Capture continues through any grace period. A detached
            // descendant can hold a pipe open, so draining gets a fixed time
            // after the group is actually cleaned up, however that cleanup
            // ended.
            let mut complete = false;
            let status = loop {
                tokio::select! {
                    biased;
                    status = &mut cleanup => break status,
                    () = &mut drain, if !complete => complete = true,
                }
            };
            let complete = complete || timeout(OUTPUT_DRAIN_TIMEOUT, &mut drain).await.is_ok();
            (status, complete)
        };
        Drained {
            status,
            complete,
            errors: errors.into_iter().flatten().collect(),
        }
    }
}

impl Drained {
    /// Splits the read errors into the one that decides the outcome and the
    /// diagnostics text. A read error decides the outcome only after the child
    /// `exited` on its own; every other error, after a notice when the drain
    /// did not complete, becomes one diagnostics line.
    pub fn into_failure_and_diagnostics(self, exited: bool) -> (Option<String>, String) {
        let mut errors = self.errors.into_iter();
        let failure = if exited { errors.next() } else { None };
        let mut lines = Vec::new();
        if !self.complete {
            lines.push(
                "Output capture stopped before EOF; additional output may be missing.".to_owned(),
            );
        }
        lines.extend(errors);
        (failure, lines.join("\n"))
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
    let mut out = Capture::new(limits.stdout);
    let mut err = Capture::new(limits.stderr);
    let mut pipes = OutputPipes::new(&mut child, |stream, bytes: &[u8]| match stream {
        Stream::Stdout => out.append(bytes),
        Stream::Stderr => err.append(bytes),
    });
    let mut observed = loop {
        tokio::select! {
            biased;
            status = child.wait() => break Observed::Exit(status.expect("reap owned child")),
            () = sleep_until(deadline) => break Observed::Timeout(limits.deadline),
            () = &mut cancelled => break Observed::Cancelled,
            () = &mut write, if !written => written = true,
            error = pipes.read_until_failure() => break Observed::Failed(error),
        }
    };
    drop(write);
    let drained = pipes
        .drain_during(async {
            group
                .terminate(limits.grace, &mut child, std::future::pending())
                .await;
            child
                .wait()
                .await
                .expect("reap owned child after process group cleanup")
        })
        .await;
    let (failure, diagnostics) =
        drained.into_failure_and_diagnostics(matches!(observed, Observed::Exit(_)));
    if let Some(error) = failure {
        observed = Observed::Failed(error);
    }
    Ok(Finished {
        observed,
        stdout: out,
        stderr: err,
        diagnostics,
    })
}

#[cfg(test)]
mod tests {
    use std::os::unix::process::ExitStatusExt;

    use super::*;

    #[test]
    fn late_read_errors_decide_only_a_natural_exit() {
        const NOTICE: &str = "Output capture stopped before EOF; additional output may be missing.";
        for (case, exited, complete, errors, failure, diagnostics) in [
            (
                "exited with one error",
                true,
                true,
                vec!["out"],
                Some("out"),
                String::new(),
            ),
            (
                "exited with two errors",
                true,
                true,
                vec!["out", "err"],
                Some("out"),
                "err".to_owned(),
            ),
            (
                "not exited with one error",
                false,
                true,
                vec!["out"],
                None,
                "out".to_owned(),
            ),
            (
                "incomplete drain",
                false,
                false,
                vec!["out", "err"],
                None,
                format!("{NOTICE}\nout\nerr"),
            ),
            ("no errors", true, true, vec![], None, String::new()),
        ] {
            let drained = Drained {
                status: ExitStatus::from_raw(0),
                complete,
                errors: errors.into_iter().map(str::to_owned).collect(),
            };
            assert_eq!(
                drained.into_failure_and_diagnostics(exited),
                (failure.map(str::to_owned), diagnostics),
                "{case}"
            );
        }
    }
}
