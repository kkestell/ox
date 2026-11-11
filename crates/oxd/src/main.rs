//! oxd: a per-user daemon that owns ACP agent sessions on behalf of Ox UIs.
//!
//! Usage: `oxd [--socket PATH] [--idle-exit-ms MS] [--startup-timeout-ms MS]`

mod conn;
mod daemon;
mod session;

use std::ffi::OsString;
use std::fs::{File, OpenOptions, TryLockError};
use std::io;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::sync::Arc;
use std::time::Duration;

use tokio::net::UnixListener;
use tokio::signal::unix::{SignalKind, signal};

use crate::daemon::Daemon;

const DEFAULT_IDLE_EXIT: Duration = Duration::from_millis(5000);
const DEFAULT_STARTUP_TIMEOUT: Duration = Duration::from_secs(60);

/// Pause after a failed `accept`, which persists while descriptors are
/// exhausted.
const ACCEPT_RETRY_DELAY: Duration = Duration::from_millis(100);

struct Args {
    socket: PathBuf,
    idle_exit: Duration,
    startup_timeout: Duration,
}

fn parse_args() -> Result<Args, String> {
    let mut socket = None;
    let mut idle_exit = DEFAULT_IDLE_EXIT;
    let mut startup_timeout = DEFAULT_STARTUP_TIMEOUT;
    let mut args = std::env::args_os().skip(1);
    while let Some(arg) = args.next() {
        let mut value = || {
            args.next()
                .ok_or_else(|| format!("{} needs a value", arg.display()))
        };
        match arg.to_str() {
            Some("--socket") => socket = Some(PathBuf::from(value()?)),
            Some("--idle-exit-ms") => idle_exit = millis(&arg, value()?)?,
            Some("--startup-timeout-ms") => startup_timeout = millis(&arg, value()?)?,
            _ => return Err(format!("unexpected argument: {}", arg.display())),
        }
    }
    Ok(Args {
        socket: socket.unwrap_or_else(ox_protocol::default_socket_path),
        idle_exit,
        startup_timeout,
    })
}

fn millis(flag: &OsString, value: OsString) -> Result<Duration, String> {
    value
        .to_str()
        .and_then(|value| value.parse().ok())
        .map(Duration::from_millis)
        .ok_or_else(|| format!("invalid {}: {}", flag.display(), value.display()))
}

#[tokio::main]
async fn main() -> ExitCode {
    let args = match parse_args() {
        Ok(args) => args,
        Err(message) => {
            eprintln!("oxd: {message}");
            return ExitCode::from(2);
        }
    };
    match run(args).await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("oxd: {error}");
            ExitCode::FAILURE
        }
    }
}

async fn run(args: Args) -> io::Result<()> {
    if let Some(dir) = args.socket.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let lock_path = {
        let mut path = OsString::from(args.socket.as_os_str());
        path.push(".lock");
        PathBuf::from(path)
    };
    let Some(_lock) = lock(&lock_path)? else {
        return Ok(());
    };

    match std::fs::remove_file(&args.socket) {
        Err(error) if error.kind() != io::ErrorKind::NotFound => return Err(error),
        _ => {}
    }
    let listener = bind(&args.socket)?;

    let mut hangup = signal(SignalKind::hangup())?;
    tokio::spawn(async move { while hangup.recv().await.is_some() {} });
    let mut terminate = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;

    // SAFETY: `geteuid` has no preconditions.
    let uid = unsafe { libc::geteuid() };
    let daemon = Arc::new(Daemon::new(args.startup_timeout));
    let idle = daemon.idle(args.idle_exit);
    tokio::pin!(idle);
    loop {
        tokio::select! {
            accepted = listener.accept() => match accepted {
                Ok((stream, _)) => {
                    if stream.peer_cred().is_ok_and(|cred| cred.uid() == uid) {
                        let client = daemon.client();
                        tokio::spawn(conn::serve(daemon.clone(), stream, client));
                    }
                }
                Err(error) => {
                    eprintln!("oxd: accept failed: {error}");
                    tokio::time::sleep(ACCEPT_RETRY_DELAY).await;
                }
            },
            () = &mut idle => break,
            _ = terminate.recv() => break,
            _ = interrupt.recv() => break,
        }
    }

    // Clients that connect from here on find no socket and start a new
    // daemon, which takes the lock once this one exits.
    let _ = std::fs::remove_file(&args.socket);
    drop(listener);
    daemon.close_all();
    Ok(())
}

/// Binds the socket readable and writable by the owner only.
fn bind(socket: &Path) -> io::Result<UnixListener> {
    // SAFETY: `umask` has no preconditions. The daemon creates no other
    // files while the mask is narrowed.
    let previous = unsafe { libc::umask(0o177) };
    let listener = UnixListener::bind(socket);
    unsafe { libc::umask(previous) };
    listener
}

/// Takes the single-instance lock, or returns `None` if another daemon holds
/// it.
fn lock(path: &PathBuf) -> io::Result<Option<File>> {
    let file = OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .open(path)?;
    match file.try_lock() {
        Ok(()) => Ok(Some(file)),
        Err(TryLockError::WouldBlock) => Ok(None),
        Err(TryLockError::Error(error)) => Err(error),
    }
}
