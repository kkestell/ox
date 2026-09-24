//! Runs one hook command: one JSON object in on stdin, one response
//! object out on stdout.

use std::{
    io::{self, ErrorKind},
    os::unix::process::ExitStatusExt,
    path::{Path, PathBuf},
    time::Duration,
};

use serde::{Deserialize, Serialize, de::DeserializeOwned};
use tokio::process::Command;

use crate::{
    process::{self, Limits, Observed},
    sessions::{EffortLevel, HookKind, SessionMode, StopDecision, ToolCall, ToolOutcome},
};

/// At most one command per hook kind. Unknown hook kinds belong to other
/// agents and are ignored.
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
pub struct Hooks {
    pub before_run: Option<HookCommand>,
    pub before_tool: Option<HookCommand>,
    pub after_tools: Option<HookCommand>,
    pub before_stop: Option<HookCommand>,
    pub after_run: Option<HookCommand>,
}

/// A hook command, run with `/bin/sh -c`.
#[derive(Debug, Clone, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HookCommand {
    pub command: String,
}

impl Hooks {
    pub fn command(&self, kind: HookKind) -> Option<&str> {
        match kind {
            HookKind::BeforeRun => self.before_run.as_ref().map(|hook| hook.command.as_str()),
            HookKind::BeforeTool => self.before_tool.as_ref().map(|hook| hook.command.as_str()),
            HookKind::AfterTools => self.after_tools.as_ref().map(|hook| hook.command.as_str()),
            HookKind::BeforeStop => self.before_stop.as_ref().map(|hook| hook.command.as_str()),
            HookKind::AfterRun => self.after_run.as_ref().map(|hook| hook.command.as_str()),
        }
    }

    pub fn validate(&self) -> io::Result<()> {
        for kind in HookKind::ALL {
            if self
                .command(kind)
                .is_some_and(|command| command.trim().is_empty())
            {
                return Err(io::Error::new(
                    ErrorKind::InvalidData,
                    format!("{} hook command is blank", kind.id()),
                ));
            }
        }
        Ok(())
    }
}

pub const IN_HOOK_ENV: &str = "OX_IN_HOOK";

const STDOUT_LIMIT: usize = 16 * 1024;
const STDERR_LIMIT: usize = 4 * 1024;
/// Long enough for a nested `ox run` to stop its own shell process groups.
const GRACE: Duration = Duration::from_secs(2);

fn deadline(kind: HookKind) -> Duration {
    Duration::from_secs(match kind {
        HookKind::BeforeRun => 30,
        HookKind::BeforeTool => 10,
        HookKind::AfterTools => 60,
        HookKind::BeforeStop => 600,
        HookKind::AfterRun => 5,
    })
}

/// The hooks from one source, the global settings or one skill, supplied to
/// one prompt run. Commands are never saved.
#[derive(Debug, Clone, PartialEq)]
pub struct HookSource {
    pub hooks: Hooks,
    pub skill: Option<String>,
    /// The directory containing the definition, where its commands run.
    pub directory: PathBuf,
}

/// The input fields every command of one hook definition shares in a prompt
/// run. Global definitions have no skill and empty arguments.
#[derive(Serialize)]
pub struct Context {
    pub skill: Option<String>,
    pub arguments: String,
    pub session_id: String,
    pub mode: SessionMode,
    /// Identifies one prompt run across its hook commands.
    pub run_id: String,
    pub workspace: PathBuf,
    pub model: String,
    pub effort: EffortLevel,
}

/// The input fields of one hook kind.
#[derive(Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum Event {
    BeforeRun,
    BeforeTool {
        tool: ToolCall,
    },
    AfterTools {
        tools: Vec<ToolReport>,
    },
    BeforeStop {
        answer: String,
    },
    AfterRun {
        outcome: AfterRunOutcome,
        answer: Option<String>,
        error: Option<String>,
    },
}

impl Event {
    pub fn kind(&self) -> HookKind {
        match self {
            Self::BeforeRun => HookKind::BeforeRun,
            Self::BeforeTool { .. } => HookKind::BeforeTool,
            Self::AfterTools { .. } => HookKind::AfterTools,
            Self::BeforeStop { .. } => HookKind::BeforeStop,
            Self::AfterRun { .. } => HookKind::AfterRun,
        }
    }
}

/// One saved tool call and its outcome.
#[derive(Serialize)]
pub struct ToolReport {
    pub call_id: String,
    pub name: String,
    pub arguments: String,
    /// `completed`, `failed`, or `cancelled`.
    pub outcome: &'static str,
    pub text: String,
}

impl ToolReport {
    pub fn new(call: &ToolCall, outcome: &ToolOutcome) -> Self {
        let status = match outcome {
            ToolOutcome::Completed(_) => "completed",
            ToolOutcome::Failed(_) => "failed",
            ToolOutcome::Cancelled(_) => "cancelled",
        };
        Self {
            call_id: call.call_id.clone(),
            name: call.name.clone(),
            arguments: call.arguments.clone(),
            outcome: status,
            text: outcome.text().to_owned(),
        }
    }
}

/// The `after_run` input's `outcome`, derived from the prompt run's result.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum AfterRunOutcome {
    Finished,
    Cancelled,
    TokenLimit,
    Refused,
    Failed,
}

#[derive(Serialize)]
struct Input<'a> {
    #[serde(flatten)]
    context: &'a Context,
    ox: &'a Path,
    #[serde(flatten)]
    event: &'a Event,
}

/// One hook kind's stdout object.
pub trait Output: DeserializeOwned {
    /// A message present in the output, which must be nonblank.
    fn message(&self) -> Option<&str>;
}

/// `before_run` and `after_tools` output: `{}` saves nothing.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feedback {
    #[serde(default, deserialize_with = "deserialize_present_string")]
    pub message: Option<String>,
}

fn deserialize_present_string<'de, D>(deserializer: D) -> Result<Option<String>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    String::deserialize(deserializer).map(Some)
}

impl Output for Feedback {
    fn message(&self) -> Option<&str> {
        self.message.as_deref()
    }
}

/// `before_tool` output for one model tool call. It is never saved.
#[derive(Debug, PartialEq, Eq, Deserialize)]
#[serde(tag = "decision", rename_all = "snake_case", deny_unknown_fields)]
pub enum ToolDecision {
    /// A struct variant, so `deny_unknown_fields` rejects a message.
    Allow {},
    Deny {
        message: String,
    },
}

impl Output for ToolDecision {
    fn message(&self) -> Option<&str> {
        match self {
            Self::Allow {} => None,
            Self::Deny { message } => Some(message),
        }
    }
}

/// `before_stop` output.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StopResponse {
    pub decision: StopDecision,
    pub message: String,
}

impl Output for StopResponse {
    fn message(&self) -> Option<&str> {
        Some(&self.message)
    }
}

/// `after_run` output, which is always `{}`.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Report {}

impl Output for Report {
    fn message(&self) -> Option<&str> {
        None
    }
}

/// Runs one configured command for `event`. The command inherits Ox's
/// environment, including `OPENROUTER_API_KEY`. Cancellation is an
/// `Interrupted` error; every other failure is an error naming the origin and
/// hook kind with the command's stderr tail.
pub async fn run<T: Output>(
    source: &HookSource,
    context: &Context,
    event: &Event,
    cancelled: impl Future<Output = ()>,
) -> io::Result<T> {
    let kind = event.kind();
    let command = source
        .hooks
        .command(kind)
        .expect("a hook runs only when its definition declares it");
    let name = crate::sessions::hook_label(context.skill.as_deref(), kind);
    let failure = |reason: String, stderr: &str| {
        let mut message = format!("{name} {reason}");
        if !stderr.trim().is_empty() {
            message.push_str("\nstderr:\n");
            message.push_str(stderr.trim_end());
        }
        io::Error::other(message)
    };
    let ox = std::env::current_exe()
        .map_err(|error| failure(format!("could not find the ox executable: {error}"), ""))?;
    let input = serde_json::to_vec(&Input {
        context,
        ox: &ox,
        event,
    })
    .map_err(|error| failure(format!("input could not be encoded: {error}"), ""))?;
    let mut process = Command::new("/bin/sh");
    process
        .arg("-c")
        .arg(command)
        .current_dir(&source.directory)
        .env(IN_HOOK_ENV, "1");
    let limits = Limits {
        stdout: STDOUT_LIMIT,
        stderr: STDERR_LIMIT,
        deadline: deadline(kind),
        grace: GRACE,
    };
    let mut finished = process::run(process, Some(input), limits, cancelled)
        .await
        .map_err(|error| {
            failure(
                format!("could not start in {}: {error}", source.directory.display()),
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
                format!("{name} was cancelled"),
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
    parse(kind, &stdout).map_err(|reason| failure(reason, &stderr))
}

fn parse<T: Output>(kind: HookKind, stdout: &str) -> Result<T, String> {
    let value: serde_json::Value = serde_json::from_str(stdout).map_err(|error| {
        format!(
            "output is not one valid {} response object: {error}",
            kind.id()
        )
    })?;
    if !value.is_object() {
        return Err("output must be a JSON object".to_owned());
    }
    let output: T = serde_json::from_value(value).map_err(|error| {
        format!(
            "output is not one valid {} response object: {error}",
            kind.id()
        )
    })?;
    if output
        .message()
        .is_some_and(|message| message.trim().is_empty())
    {
        return Err("message is blank".to_owned());
    }
    Ok(output)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn error<T: Output + std::fmt::Debug>(kind: HookKind, stdout: &str) -> String {
        parse::<T>(kind, stdout).unwrap_err()
    }

    #[test]
    fn each_kind_accepts_only_its_response_object() {
        let feedback = |stdout| {
            parse::<Feedback>(HookKind::AfterTools, stdout)
                .unwrap()
                .message
        };
        assert_eq!(feedback("{}"), None);
        assert_eq!(
            feedback(r#"{"message":"Run the formatter."}"#).as_deref(),
            Some("Run the formatter.")
        );
        let tool = |stdout| parse::<ToolDecision>(HookKind::BeforeTool, stdout);
        assert_eq!(tool(r#"{"decision":"allow"}"#), Ok(ToolDecision::Allow {}));
        assert_eq!(
            tool(r#"{"decision":"deny","message":"Use the test script."}"#),
            Ok(ToolDecision::Deny {
                message: "Use the test script.".to_owned()
            })
        );
        let stop = parse::<StopResponse>(
            HookKind::BeforeStop,
            r#"{"decision":"stop","message":"Done."}"#,
        )
        .unwrap();
        assert_eq!(
            (stop.decision, stop.message.as_str()),
            (StopDecision::Stop, "Done.")
        );
        parse::<Report>(HookKind::AfterRun, "{}").unwrap();

        for (message, expected) in [
            (
                error::<Feedback>(
                    HookKind::BeforeRun,
                    r#"{"message":"Hi.","decision":"stop"}"#,
                ),
                "output is not one valid before_run response object: unknown field `decision`",
            ),
            (
                error::<Feedback>(HookKind::AfterTools, r#"{"message":"  "}"#),
                "message is blank",
            ),
            (
                error::<ToolDecision>(
                    HookKind::BeforeTool,
                    r#"{"decision":"allow","message":"Fine."}"#,
                ),
                "unknown field `message`",
            ),
            (
                error::<ToolDecision>(HookKind::BeforeTool, r#"{"decision":"deny"}"#),
                "missing field `message`",
            ),
            (
                error::<ToolDecision>(HookKind::BeforeTool, r#"{"decision":"deny","message":""}"#),
                "message is blank",
            ),
            (
                error::<StopResponse>(
                    HookKind::BeforeStop,
                    r#"{"decision":"maybe","message":"Unsure."}"#,
                ),
                "unknown variant `maybe`",
            ),
            (
                error::<StopResponse>(HookKind::BeforeStop, r#"{"decision":"stop","message":" "}"#),
                "message is blank",
            ),
            (
                error::<StopResponse>(HookKind::BeforeStop, "not json"),
                "output is not one valid before_stop response object",
            ),
            (
                error::<Report>(HookKind::AfterRun, r#"{"message":"Logged."}"#),
                "unknown field `message`",
            ),
        ] {
            assert!(message.contains(expected), "{message}");
        }
    }
}
