use std::time::Duration;

use serde::Deserialize;
use serde_json::{Value, json};

use crate::{
    sessions::ToolOutcome,
    subagents::{MAX_SUBAGENTS, Sent, Subagents, WaitReason},
};

use super::{SEND_MESSAGE, START_SUBAGENT, STOP_SUBAGENT, WAIT};

/// The largest assigned task or follow-up message, the same limit as shell
/// process input.
const MAX_MESSAGE_BYTES: usize = 16 * 1024;
const MAX_WAIT_SECONDS: f64 = 600.0;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StartArgs {
    prompt: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SendArgs {
    subagent_id: String,
    message: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StopArgs {
    subagent_id: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WaitArgs {
    seconds: f64,
}

pub(super) fn start_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": START_SUBAGENT,
            "description": format!("Start a subagent on a task and return its subagent ID at once; it works concurrently while you keep using your tools. A subagent uses your model, workspace, instructions, and the workspace and shell tools, shares this session's background commands, and starts with no other context, so the prompt must say everything it needs. Its final answer, or its failure, arrives automatically as a message before a later model request. A subagent that needs input ends its turn with a question; reply with send_message. At most {MAX_SUBAGENTS} subagents can exist at once, including idle ones; stop_subagent removes one. Every subagent is stopped when your turn ends, so wait for the answers you need before finishing."),
            "parameters": {
                "type": "object",
                "properties": {
                    "prompt": {
                        "type": "string",
                        "description": "The complete task, at most 16 KiB of UTF-8 text."
                    }
                },
                "required": ["prompt"],
                "additionalProperties": false
            }
        }
    })
}

pub(super) fn send_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": SEND_MESSAGE,
            "description": "Send a follow-up message to a subagent, such as the answer to its question. An idle subagent starts a new turn at once with its conversation so far; a busy subagent reads the message after its current turn. Messages start turns in the order they were sent.",
            "parameters": {
                "type": "object",
                "properties": {
                    "subagent_id": {
                        "type": "string",
                        "description": "The ID returned by start_subagent."
                    },
                    "message": {
                        "type": "string",
                        "description": "The message, at most 16 KiB of UTF-8 text."
                    }
                },
                "required": ["subagent_id", "message"],
                "additionalProperties": false
            }
        }
    })
}

pub(super) fn stop_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": STOP_SUBAGENT,
            "description": "Stop a subagent: cancel its current turn, discard its queued messages, and wait until it has stopped. Its saved conversation remains, and background commands it started keep running.",
            "parameters": {
                "type": "object",
                "properties": {
                    "subagent_id": {
                        "type": "string",
                        "description": "The ID returned by start_subagent."
                    }
                },
                "required": ["subagent_id"],
                "additionalProperties": false
            }
        }
    })
}

pub(super) fn wait_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": WAIT,
            "description": "Wait until a subagent message arrives, every subagent is idle, or the given seconds pass. Returns at once when messages are already waiting, no subagents exist, or all are idle. The result gives the reason and each subagent's state; the messages themselves arrive after this call.",
            "parameters": {
                "type": "object",
                "properties": {
                    "seconds": {
                        "type": "number",
                        "minimum": 0,
                        "maximum": MAX_WAIT_SECONDS,
                        "description": "The longest wait, in seconds. Values above 600 wait 600 seconds."
                    }
                },
                "required": ["seconds"],
                "additionalProperties": false
            }
        }
    })
}

/// Runs one coordination call against the main agent's subagents. `None`
/// means the calling agent is a subagent, which cannot coordinate.
pub(super) async fn execute(
    subagents: Option<&Subagents>,
    name: &str,
    arguments: &str,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome {
    let Some(subagents) = subagents else {
        return ToolOutcome::Failed(format!("{name} is available only to the main agent."));
    };
    let result = match name {
        START_SUBAGENT => start(subagents, arguments),
        SEND_MESSAGE => send(subagents, arguments),
        STOP_SUBAGENT => stop(subagents, arguments).await,
        WAIT => return wait(subagents, arguments, cancelled).await,
        _ => unreachable!("{name} is not a coordination tool"),
    };
    super::bounded_result(result)
}

fn parse<'a, T: Deserialize<'a>>(arguments: &'a str) -> Result<T, String> {
    serde_json::from_str(arguments).map_err(|error| format!("arguments: {error}"))
}

fn check_message(name: &str, text: &str) -> Result<(), String> {
    if text.trim().is_empty() {
        return Err(format!("arguments: {name} is blank"));
    }
    if text.len() > MAX_MESSAGE_BYTES {
        return Err(format!("arguments: {name} exceeds 16 KiB"));
    }
    Ok(())
}

fn start(subagents: &Subagents, arguments: &str) -> Result<String, String> {
    let args: StartArgs = parse(arguments)?;
    check_message("prompt", &args.prompt)?;
    let id = subagents.start(args.prompt)?;
    Ok(format!(
        "Started subagent {id}. Its final answer arrives as a message before a later model request; call wait to wait for it."
    ))
}

fn send(subagents: &Subagents, arguments: &str) -> Result<String, String> {
    let args: SendArgs = parse(arguments)?;
    check_message("message", &args.message)?;
    let id = args.subagent_id;
    Ok(match subagents.send(&id, args.message)? {
        Sent::Started => format!("Subagent {id} started a turn with the message."),
        Sent::Queued(1) => {
            format!("Subagent {id} is busy; it reads the message after its current turn.")
        }
        Sent::Queued(queued) => format!(
            "Subagent {id} is busy; it reads the message after its current turn and {} earlier queued messages.",
            queued - 1
        ),
    })
}

async fn stop(subagents: &Subagents, arguments: &str) -> Result<String, String> {
    let args: StopArgs = parse(arguments)?;
    subagents.stop(&args.subagent_id).await?;
    Ok(format!(
        "Stopped subagent {}. Its conversation remains saved.",
        args.subagent_id
    ))
}

/// Nonpositive seconds wait not at all, and longer waits are capped.
async fn wait(
    subagents: &Subagents,
    arguments: &str,
    cancelled: impl Future<Output = ()>,
) -> ToolOutcome {
    let args: WaitArgs = match parse(arguments) {
        Ok(args) => args,
        Err(error) => return super::bounded_result(Err(error)),
    };
    let limit = Duration::from_secs_f64(args.seconds.clamp(0.0, MAX_WAIT_SECONDS));
    let Some(waited) = subagents.wait(limit, cancelled).await else {
        return ToolOutcome::Cancelled("Cancelled while waiting for subagents.".to_owned());
    };
    let reason = match waited.reason {
        WaitReason::Messages(1) => "1 subagent message arrived; it follows this result.".to_owned(),
        WaitReason::Messages(count) => {
            format!("{count} subagent messages arrived; they follow this result.")
        }
        WaitReason::NoSubagents => "No subagents exist.".to_owned(),
        WaitReason::AllIdle => "Every subagent is idle.".to_owned(),
        WaitReason::TimedOut => "The wait ended without a subagent message.".to_owned(),
    };
    super::bounded_result(Ok(format!("{reason}\n\n{}", waited.states)))
}
