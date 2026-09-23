//! Runs a skill's `before_stop` hook: one JSON object in on stdin, one
//! decision object out on stdout.

use std::{
    io::{self, ErrorKind},
    os::unix::process::ExitStatusExt,
    path::{Path, PathBuf},
    time::Duration,
};

use serde::{Deserialize, Serialize};
use tokio::process::Command;

use crate::{
    process::{self, Limits, Observed},
    sessions::{EffortLevel, HookDecision, HookFeedback, SkillInvocation},
};

const DEADLINE: Duration = Duration::from_secs(600);
const STDOUT_LIMIT: usize = 16 * 1024;
const STDERR_LIMIT: usize = 4 * 1024;
/// Long enough for a nested `ox run` to stop its own shell process groups.
const GRACE: Duration = Duration::from_secs(2);

/// The hook of the skill invoked for one prompt run. It comes from the skill
/// catalog and is never saved.
#[derive(Debug, Clone, PartialEq)]
pub struct Hook {
    pub command: String,
    /// The skill directory, where the command runs.
    pub directory: PathBuf,
}

#[derive(Serialize)]
struct Input<'a> {
    skill: &'a str,
    arguments: &'a str,
    workspace: &'a Path,
    ox: &'a Path,
    model: &'a str,
    effort: &'a str,
    answer: &'a str,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Output {
    decision: HookDecision,
    message: String,
}

/// Runs the hook on a finished answer. The hook inherits Ox's environment,
/// including `OPENROUTER_API_KEY`. Cancellation is an `Interrupted` error;
/// every other failure is an error naming the skill with the hook's stderr
/// tail.
pub async fn run(
    hook: &Hook,
    invocation: &SkillInvocation,
    workspace: &Path,
    model: &str,
    effort: EffortLevel,
    answer: &str,
    cancelled: impl Future<Output = ()>,
) -> io::Result<HookFeedback> {
    let failure = |reason: String, stderr: &str| {
        let mut message = format!("{} before_stop hook {reason}", invocation.name);
        if !stderr.trim().is_empty() {
            message.push_str("\nstderr:\n");
            message.push_str(stderr.trim_end());
        }
        io::Error::other(message)
    };
    let ox = std::env::current_exe()
        .map_err(|error| failure(format!("could not find the ox executable: {error}"), ""))?;
    let input = serde_json::to_vec(&Input {
        skill: &invocation.name,
        arguments: &invocation.arguments,
        workspace,
        ox: &ox,
        model,
        effort: effort.id(),
        answer,
    })
    .map_err(|error| failure(format!("input could not be encoded: {error}"), ""))?;
    let mut command = Command::new("/bin/sh");
    command
        .arg("-c")
        .arg(&hook.command)
        .current_dir(&hook.directory);
    let limits = Limits {
        stdout: STDOUT_LIMIT,
        stderr: STDERR_LIMIT,
        deadline: DEADLINE,
        grace: GRACE,
    };
    let mut finished = process::run(command, Some(input), limits, cancelled)
        .await
        .map_err(|error| {
            failure(
                format!("could not start in {}: {error}", hook.directory.display()),
                "",
            )
        })?;
    let stderr = finished.stderr.decode();
    match finished.observed {
        Observed::Exit(status) if status.success() => {}
        Observed::Exit(status) => {
            let reason = match status.code() {
                Some(code) => format!("exited with code {code}"),
                None => format!(
                    "was terminated by signal {}",
                    status.signal().expect("Unix signal exit")
                ),
            };
            return Err(failure(reason, &stderr));
        }
        Observed::Timeout(deadline) => {
            return Err(failure(
                format!("timed out after {} seconds", deadline.as_secs()),
                &stderr,
            ));
        }
        Observed::Cancelled => {
            return Err(io::Error::new(
                ErrorKind::Interrupted,
                format!("{} before_stop hook was cancelled", invocation.name),
            ));
        }
        Observed::Failed(error) => return Err(failure(error, &stderr)),
    }
    if finished.stdout.omitted {
        return Err(failure(
            format!("output exceeds {} KiB", STDOUT_LIMIT / 1024),
            &stderr,
        ));
    }
    let stdout = String::from_utf8(finished.stdout.bytes.into())
        .map_err(|_| failure("output is not UTF-8".to_owned(), &stderr))?;
    let output: Output = serde_json::from_str(&stdout).map_err(|error| {
        failure(
            format!("output is not one decision object: {error}"),
            &stderr,
        )
    })?;
    if output.message.trim().is_empty() {
        return Err(failure("message is blank".to_owned(), &stderr));
    }
    Ok(HookFeedback {
        skill: invocation.name.clone(),
        decision: output.decision,
        message: output.message,
    })
}
