#![cfg(unix)]

use std::{fs, os::unix::fs::PermissionsExt, path::Path, process::Stdio};

use serde_json::{Value, json};
use tokio::{
    io::{AsyncBufReadExt, AsyncWriteExt, BufReader},
    process::{Child, ChildStdout, Command},
    time::{Duration, timeout},
};

/// What every fake claude does with its stdin, which stays open across
/// turns as the real one's does.
const FAKE_CLAUDE: &str = r#"
# Reads the next message into $message. Claude ends when its stdin closes.
next_message() {
  IFS= read -r message || exit 0
}
# Reports the turn of the prompt in $message as started.
started() {
  uuid=$(printf '%s\n' "$message" | sed 's/.*"uuid":"\([^"]*\)".*/\1/')
  printf '{"type":"command_lifecycle","command_uuid":"%s","state":"started"}\n' "$uuid"
}
# Reads a prompt and starts its turn.
next_prompt() {
  next_message
  case "$message" in *'"type":"user"'*) ;; *) exit 20;; esac
  started
}
# Waits for an interrupt and ends the turn it stops.
interrupted() {
  next_message
  case "$message" in *'"subtype":"interrupt"'*) ;; *) exit 21;; esac
  printf '%s\n' '{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}'
  printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_tools"}'
}
# Says claude is running or idle.
state() {
  printf '{"type":"system","subtype":"session_state_changed","state":"%s"}\n' "$1"
}
# Echoes the prompt in $message as taken into a turn, as claude does with
# --replay-user-messages.
echoed() {
  uuid=$(printf '%s\n' "$message" | sed 's/.*"uuid":"\([^"]*\)".*/\1/')
  printf '{"type":"user","uuid":"%s","isReplay":true,"message":{"role":"user","content":[{"type":"text","text":"hello"}]}}\n' "$uuid"
}
# Accepts the control request in $message.
accept() {
  request=$(printf '%s\n' "$message" | sed 's/.*"request_id":"\([^"]*\)".*/\1/')
  printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s"}}\n' "$request"
}
"#;

fn fake_claude(dir: &Path, script: &str) {
    let path = dir.join("claude");
    fs::write(&path, format!("#!/bin/sh\n{FAKE_CLAUDE}\n{script}\n")).unwrap();
    fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
}

fn start(dir: &Path, store: &Path) -> (Child, BufReader<ChildStdout>) {
    let mut child = Command::new(env!("CARGO_BIN_EXE_claude-code-acp"))
        .env(
            "PATH",
            format!("{}:{}", dir.display(), std::env::var("PATH").unwrap()),
        )
        .env("CLAUDE_ACP_STORE_DIR", store)
        .env(
            "CLAUDE_CONFIG_DIR",
            dir.parent().unwrap().join("empty-claude"),
        )
        .env("ANTHROPIC_API_KEY", "must-be-removed")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let reader = BufReader::new(child.stdout.take().unwrap());
    (child, reader)
}

async fn send(child: &mut Child, message: Value) {
    let stdin = child.stdin.as_mut().unwrap();
    stdin
        .write_all(message.to_string().as_bytes())
        .await
        .unwrap();
    stdin.write_all(b"\n").await.unwrap();
}

async fn receive(reader: &mut BufReader<ChildStdout>) -> Value {
    let mut line = String::new();
    timeout(Duration::from_secs(5), reader.read_line(&mut line))
        .await
        .expect("timed out waiting for ACP response")
        .unwrap();
    assert!(!line.is_empty(), "ACP process closed stdout");
    serde_json::from_str(&line).unwrap()
}

async fn response(reader: &mut BufReader<ChildStdout>, id: i64) -> (Value, Vec<Value>) {
    let mut notifications = Vec::new();
    loop {
        let value = receive(reader).await;
        if value["id"] == id {
            return (value, notifications);
        }
        notifications.push(value);
    }
}

/// The `session/update` update of a notification.
fn update(notification: &Value) -> &Value {
    &notification["params"]["update"]
}

/// The text an update shows: a chunk's, or the first block of a whole
/// message.
fn shown_text(notification: &Value) -> Option<&str> {
    let content = &update(notification)["content"];
    content["text"]
        .as_str()
        .or_else(|| content[0]["text"].as_str())
}

fn is_state(notification: &Value, state: &str) -> bool {
    notification["method"] == "session/update"
        && update(notification)["sessionUpdate"] == "state_update"
        && update(notification)["state"] == state
}

/// Reads until the prompt `id` is answered and its turn goes idle, with
/// what was `seen` already, and checks that the response names the user
/// message the bridge echoed. Returns the idle update, and the
/// notifications from `seen` and after.
async fn finish(
    reader: &mut BufReader<ChildStdout>,
    id: i64,
    mut seen: Vec<Value>,
) -> (Value, Vec<Value>) {
    let idle = loop {
        let answered = seen.iter().position(|value| value["id"] == id);
        if let Some(answered) = answered
            && let Some(idle) = seen[answered..].iter().find(|v| is_state(v, "idle"))
        {
            break update(idle).clone();
        }
        seen.push(receive(reader).await);
    };
    let response = seen.iter().find(|value| value["id"] == id).unwrap();
    let message_id = &response["result"]["messageId"];
    assert!(message_id.is_string(), "{response}");
    assert!(
        seen.iter().any(|value| {
            update(value)["sessionUpdate"] == "user_message"
                && update(value)["messageId"] == *message_id
        }),
        "no user message {message_id} in {seen:?}"
    );
    seen.retain(|value| value["method"] == "session/update");
    (idle, seen)
}

/// Reads until the prompt `id` is answered and its turn goes idle.
async fn finished(reader: &mut BufReader<ChildStdout>, id: i64) -> (Value, Vec<Value>) {
    finish(reader, id, Vec::new()).await
}

/// Reads until the client gets a permission request. Returns it and what
/// was read before it.
async fn until_permission(reader: &mut BufReader<ChildStdout>) -> (Value, Vec<Value>) {
    let mut seen = Vec::new();
    loop {
        let value = receive(reader).await;
        if value["method"] == "session/request_permission" {
            return (value, seen);
        }
        seen.push(value);
    }
}

async fn initialize(child: &mut Child, reader: &mut BufReader<ChildStdout>) {
    send(
        child,
        json!({"jsonrpc":"2.0","id":1,"method":"initialize","params":{
            "protocolVersion":2,"capabilities":{},"info":{"name":"test","version":"1"}
        }}),
    )
    .await;
    let (value, _) = response(reader, 1).await;
    assert_eq!(value["result"]["protocolVersion"], 2);
    let session = &value["result"]["capabilities"]["session"];
    assert!(session["prompt"]["image"].is_object(), "{value}");
    assert!(session["prompt"]["embeddedContext"].is_object(), "{value}");
    assert!(session["mcp"]["stdio"].is_object(), "{value}");
}

#[tokio::test]
async fn streams_and_resumes_a_persisted_session() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    let store = temp.path().join("store");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
if [ -n "$ANTHROPIC_API_KEY" ] || [ -n "$ANTHROPIC_AUTH_TOKEN" ]; then exit 11; fi
mode=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--permission-mode' ]; then mode="$2"; break; fi
  shift
done
if [ "$mode" != 'plan' ]; then exit 12; fi
next_prompt
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello "}}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"},{"type":"tool_use","name":"Read","input":{"file_path":"x"}}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2","result":"hello world"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{
            "cwd":project,"mcpServers":[]
        }}),
    )
    .await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].as_str().unwrap().to_owned();
    assert_eq!(new["result"]["configOptions"].as_array().unwrap().len(), 2);
    let mode = new["result"]["configOptions"]
        .as_array()
        .unwrap()
        .iter()
        .find(|option| option["category"] == "mode")
        .expect("mode config option");
    assert_eq!(mode["configId"], "mode");
    assert_eq!(mode["currentValue"], "manual");
    assert_eq!(mode["options"].as_array().unwrap().len(), 4);
    assert_eq!(
        new["result"]["configOptions"][0]["currentValue"],
        "claude-opus-5-5"
    );

    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":7,"method":"session/set_config_option","params":{
            "sessionId":id,"configId":"mode","type":"id","value":"plan"
        }}),
    )
    .await;
    let (set_mode, mode_updates) = response(&mut reader, 7).await;
    let mode = set_mode["result"]["configOptions"]
        .as_array()
        .unwrap()
        .iter()
        .find(|option| option["configId"] == "mode")
        .unwrap();
    assert_eq!(mode["currentValue"], "plan");
    assert!(
        mode_updates.iter().any(|update| {
            update["params"]["update"]["sessionUpdate"] == "config_option_update"
        })
    );

    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":3,"method":"session/set_config_option","params":{
            "sessionId":id,"configId":"model","type":"id","value":"claude-opus-5-5"
        }}),
    )
    .await;
    let (set, _) = response(&mut reader, 3).await;
    assert_eq!(
        set["result"]["configOptions"][0]["currentValue"],
        "claude-opus-5-5"
    );

    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{
            "sessionId":id,"prompt":[{"type":"text","text":"say hello"}]
        }}),
    )
    .await;
    let (idle, updates) = finished(&mut reader, 4).await;
    assert_eq!(idle["stopReason"], "end_turn");
    let texts: Vec<_> = updates
        .iter()
        .filter(|v| v["params"]["update"]["sessionUpdate"] == "agent_message_chunk")
        .map(|v| v["params"]["update"]["content"]["text"].as_str().unwrap())
        .collect();
    assert_eq!(texts, ["hello "]);
    assert!(
        updates
            .iter()
            .any(|v| v["params"]["update"]["sessionUpdate"] == "tool_call_update")
    );
    assert!(store.join("acp-sessions.json").exists());
    child.kill().await.unwrap();

    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":5,"method":"session/resume","params":{
            "sessionId":id,"cwd":project,"mcpServers":[]
        }}),
    )
    .await;
    let (resume, _) = response(&mut reader, 5).await;
    assert_eq!(
        resume["result"]["configOptions"][0]["currentValue"],
        "claude-opus-5-5"
    );
    assert_eq!(resume["result"]["configOptions"][1]["currentValue"], "plan");
    child.kill().await.unwrap();
}

/// Sets a config option and returns the response.
async fn set_config(
    child: &mut Child,
    reader: &mut BufReader<ChildStdout>,
    request_id: i64,
    session: &Value,
    config: &str,
    value: &str,
) -> Value {
    send(child, json!({"jsonrpc":"2.0","id":request_id,"method":"session/set_config_option","params":{"sessionId":session,"configId":config,"type":"id","value":value}})).await;
    response(reader, request_id).await.0
}

#[tokio::test]
async fn config_changes_reach_the_running_claude() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // Each turn says the mode and model claude has: those it started with,
    // then those it was asked to change to. It refuses Sonnet.
    fake_claude(
        &bin,
        r#"
printf '%s\n' "$*" >> calls
while [ "$#" -gt 0 ]; do
  case "$1" in
    --permission-mode) mode="$2";;
    --model) model="$2";;
  esac
  shift
done
while :; do
  next_message
  case "$message" in
    *'"subtype":"set_model"'*'sonnet'*)
      request=$(printf '%s\n' "$message" | sed 's/.*"request_id":"\([^"]*\)".*/\1/')
      printf '{"type":"control_response","response":{"subtype":"error","request_id":"%s","error":"no such model"}}\n' "$request";;
    *'"subtype":"set_permission_mode"'*)
      mode=$(printf '%s\n' "$message" | sed 's/.*"mode":"\([^"]*\)".*/\1/')
      accept;;
    *'"subtype":"set_model"'*)
      model=$(printf '%s\n' "$message" | sed 's/.*"model":"\([^"]*\)".*/\1/')
      accept;;
    *)
      started
      printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s %s"}]}}\n' "$mode" "$model"
      printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}';;
  esac
done
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (_, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert!(
        updates
            .iter()
            .any(|v| shown_text(v) == Some("manual claude-opus-5-5")),
        "{updates:?}"
    );

    let set = set_config(&mut child, &mut reader, 4, &session, "mode", "plan").await;
    assert_eq!(set["result"]["configOptions"][1]["currentValue"], "plan");
    let model = "claude-haiku-5-5";
    let set = set_config(&mut child, &mut reader, 5, &session, "model", model).await;
    assert_eq!(set["result"]["configOptions"][0]["currentValue"], model);
    let refused = "claude-sonnet-5-5";
    let set = set_config(&mut child, &mut reader, 6, &session, "model", refused).await;
    assert_eq!(set["error"]["message"], "no such model", "{set}");

    let (_, updates) = prompt(&mut child, &mut reader, 7, &session).await;
    assert!(
        updates
            .iter()
            .any(|v| shown_text(v) == Some("plan claude-haiku-5-5")),
        "{updates:?}"
    );
    send(&mut child, json!({"jsonrpc":"2.0","id":8,"method":"session/resume","params":{"sessionId":session,"cwd":project,"mcpServers":[]}})).await;
    let (resumed, _) = response(&mut reader, 8).await;
    assert_eq!(resumed["result"]["configOptions"][0]["currentValue"], model);
    assert_eq!(calls(&project).len(), 1);
    child.kill().await.unwrap();
}

#[tokio::test]
async fn cancellation_interrupts_the_turn_and_keeps_claude() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
printf '%s\n' "$*" >> calls
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"started"}]}}'
interrupted
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"again"}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"wait"}]}})).await;
    let seen = until_text(&mut reader, "started").await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (result, updates) = finish(&mut reader, 3, seen).await;
    assert_eq!(result["stopReason"], "cancelled", "{result}");
    // Claude's note that it was interrupted is not something the user said:
    // the prompt is the only user message.
    let user_messages = updates
        .iter()
        .filter(|v| update(v)["sessionUpdate"] == "user_message")
        .count();
    assert_eq!(user_messages, 1, "{updates:?}");

    let (result, updates) = prompt(&mut child, &mut reader, 4, &session).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    assert!(
        updates.iter().any(|v| shown_text(v) == Some("again")),
        "{updates:?}"
    );
    assert_eq!(calls(&project).len(), 1);
    child.kill().await.unwrap();
}

#[tokio::test]
async fn permission_request_round_trips_through_client() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
next_prompt
case "$message" in *'"run touch"'*) ;; *) exit 11;; esac
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"touch /tmp/claude-approval-test"}}]}}'
printf '%s\n' '{"type":"control_request","request_id":"permission-1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-1","input":{"command":"touch /tmp/claude-approval-test"}}}'
IFS= read -r answer || exit 12
for part in '"type":"control_response"' '"request_id":"permission-1"' '"behavior":"allow"' '"toolUseID":"tool-1"'; do
  case "$answer" in *"$part"*) ;; *) exit 13;; esac
done
printf '%s\n' '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":""}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"approved"}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2","result":"done"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].as_str().unwrap().to_owned();
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{
            "sessionId":id,"prompt":[{"type":"text","text":"run touch"}]
        }}),
    )
    .await;
    let (permission, before) = until_permission(&mut reader).await;
    // The turn requires action while the request is open.
    let states: Vec<&str> = before
        .iter()
        .filter(|v| update(v)["sessionUpdate"] == "state_update")
        .map(|v| update(v)["state"].as_str().unwrap())
        .collect();
    assert_eq!(states, ["running", "requires_action"], "{before:?}");
    let tool_call = &permission["params"]["subject"]["toolCall"];
    assert_eq!(tool_call["toolCallId"], "tool-1", "{permission}");
    assert_eq!(
        permission["params"]["title"],
        "Bash `touch /tmp/claude-approval-test`"
    );
    assert_eq!(
        tool_call["rawInput"]["command"],
        "touch /tmp/claude-approval-test"
    );
    // The input is the tool call's only description of the request.
    assert!(tool_call["content"].is_null(), "{tool_call}");
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":permission["id"],"result":{
            "outcome":{"outcome":"selected","optionId":"allow_once"}
        }}),
    )
    .await;
    let (idle, updates) = finish(&mut reader, 3, before).await;
    assert_eq!(idle["stopReason"], "end_turn");
    assert!(updates.iter().any(|v| shown_text(v) == Some("approved")));
    let statuses: Vec<&Value> = updates
        .iter()
        .filter(|v| update(v)["toolCallId"] == "tool-1")
        .map(|v| &update(v)["status"])
        .filter(|status| !status.is_null())
        .collect();
    assert_eq!(statuses, ["pending", "pending", "in_progress", "completed"]);
    // The turn runs again once the request is answered.
    let states: Vec<&str> = updates
        .iter()
        .filter(|v| update(v)["sessionUpdate"] == "state_update")
        .map(|v| update(v)["state"].as_str().unwrap())
        .collect();
    assert_eq!(states, ["running", "requires_action", "running", "idle"]);
    child.kill().await.unwrap();
}

#[tokio::test]
async fn persistent_approval_and_denial_reach_claude() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"control_request","request_id":"permission-1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-1","input":{"command":"pwd"},"permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"pwd"}],"behavior":"allow","destination":"session"}]}}'
IFS= read -r answer || exit 11
case "$answer" in *'"behavior":"allow"'*) ;; *) exit 12;; esac
case "$answer" in *'"updatedPermissions"'*) ;; *) exit 13;; esac
printf '%s\n' '{"type":"control_request","request_id":"permission-2","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-2","input":{"command":"whoami"}}}'
IFS= read -r answer || exit 14
case "$answer" in *'"behavior":"deny"'*) ;; *) exit 15;; esac
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2","result":"done"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].as_str().unwrap().to_owned();
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":id,"prompt":[{"type":"text","text":"test permissions"}]}})).await;
    let (first, mut seen) = until_permission(&mut reader).await;
    assert!(
        first["params"]["options"]
            .as_array()
            .unwrap()
            .iter()
            .any(|option| option["optionId"] == "allow_always")
    );
    send(&mut child, json!({"jsonrpc":"2.0","id":first["id"],"result":{"outcome":{"outcome":"selected","optionId":"allow_always"}}})).await;
    let (second, more) = until_permission(&mut reader).await;
    seen.extend(more);
    assert!(
        !second["params"]["options"]
            .as_array()
            .unwrap()
            .iter()
            .any(|option| option["optionId"] == "allow_always")
    );
    send(&mut child, json!({"jsonrpc":"2.0","id":second["id"],"result":{"outcome":{"outcome":"selected","optionId":"reject_once"}}})).await;
    let (idle, _) = finish(&mut reader, 3, seen).await;
    assert_eq!(idle["stopReason"], "end_turn", "{idle}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn a_permission_without_an_argument_is_titled_with_its_tool_name() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"control_request","request_id":"permission-1","request":{"subtype":"can_use_tool","tool_name":"mcp__x__act","tool_use_id":"tool-1","input":{},"title":"Claude wants to use `act`"}}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"act"}]}})).await;
    let (permission, _) = until_permission(&mut reader).await;
    assert_eq!(permission["params"]["title"], "mcp__x__act");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn cancellation_stops_a_pending_permission() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // Claude withdraws a permission request when it is interrupted, and the
    // next message it reads is the next prompt, not an answer.
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"control_request","request_id":"permission-1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-1","input":{"command":"pwd"}}}'
next_message
case "$message" in *'"subtype":"interrupt"'*) ;; *) exit 21;; esac
printf '%s\n' '{"type":"control_cancel_request","request_id":"permission-1"}'
printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_tools"}'
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"after"}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"run pwd"}]}})).await;
    let (permission, seen) = until_permission(&mut reader).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (result, updates) = finish(&mut reader, 3, seen).await;
    assert_eq!(result["stopReason"], "cancelled", "{result}");
    // The withdrawn request no longer requires action: the turn runs until
    // it ends, whether or not the client sees that in between.
    let states: Vec<&str> = updates
        .iter()
        .filter(|v| update(v)["sessionUpdate"] == "state_update")
        .map(|v| update(v)["state"].as_str().unwrap())
        .collect();
    let (last, rest) = states.split_last().unwrap();
    assert_eq!(*last, "idle", "{updates:?}");
    assert_eq!(rest[..2], ["running", "requires_action"], "{updates:?}");
    assert!(
        rest[2..].iter().all(|state| *state == "running"),
        "{states:?}"
    );
    // An answer to the withdrawn request does not reach claude.
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":permission["id"],"result":{"outcome":{"outcome":"cancelled"}}}),
    )
    .await;
    assert!(
        updates.iter().any(|v| {
            v["params"]["update"]["toolCallId"] == "tool-1"
                && v["params"]["update"]["status"] == "failed"
        }),
        "{updates:?}"
    );

    let (result, updates) = prompt(&mut child, &mut reader, 4, &session).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    assert!(
        updates.iter().any(|v| shown_text(v) == Some("after")),
        "{updates:?}"
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn passes_stdio_mcp_servers_to_claude() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
found=0
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--mcp-config" ]; then
    case "$2" in *'"filesystem":{'*) found=1;; *) exit 12;; esac
    for part in '"command":"/usr/bin/node"' '"args":["server.js"]' '"env":{"TOKEN":"test"}'; do
      case "$2" in *"$part"*) ;; *) exit 12;; esac
    done
    break
  fi
  shift
done
if [ "$found" -ne 1 ]; then exit 12; fi
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"mcp ok"}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2","result":"mcp ok"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{
            "cwd":project,"mcpServers":[{"type":"stdio","name":"filesystem","command":"/usr/bin/node","args":["server.js"],
                "env":[{"name":"TOKEN","value":"test"}]}]
        }}),
    )
    .await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].as_str().unwrap().to_owned();
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{
            "sessionId":id,"prompt":[{"type":"text","text":"use mcp"}]
        }}),
    )
    .await;
    let (idle, updates) = finished(&mut reader, 3).await;
    assert_eq!(idle["stopReason"], "end_turn");
    assert!(updates.iter().any(|v| shown_text(v) == Some("mcp ok")));
    child.kill().await.unwrap();
}

#[tokio::test]
async fn lists_and_resumes_claude_history() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    let transcripts = temp.path().join("empty-claude/projects/sample");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fs::create_dir_all(&transcripts).unwrap();
    let history_id = "79ed6f89-b693-4192-8169-4554404b43a2";
    fs::write(transcripts.join(format!("{history_id}.jsonl")), format!(
        "{{\"cwd\":\"{}\",\"type\":\"user\",\"message\":{{\"content\":\"Original prompt\"}}}}\n",
        project.display()
    )).unwrap();
    fake_claude(
        &bin,
        r#"
found=0
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--resume" ] && [ "$2" = "79ed6f89-b693-4192-8169-4554404b43a2" ]; then found=1; break; fi
  shift
done
if [ "$found" -ne 1 ]; then exit 13; fi
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"resumed"}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2","result":"resumed"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":2,"method":"session/list","params":{"cwd":project}}),
    )
    .await;
    let (list, _) = response(&mut reader, 2).await;
    assert_eq!(
        list["result"]["sessions"][0]["sessionId"], history_id,
        "{list}"
    );
    assert_eq!(list["result"]["sessions"][0]["title"], "Original prompt");
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":3,"method":"session/resume","params":{
            "sessionId":history_id,"cwd":project,"mcpServers":[]
        }}),
    )
    .await;
    response(&mut reader, 3).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{
            "sessionId":history_id,"prompt":[{"type":"text","text":"continue"}]
        }}),
    )
    .await;
    let (idle, updates) = finished(&mut reader, 4).await;
    assert_eq!(idle["stopReason"], "end_turn");
    assert!(updates.iter().any(|v| shown_text(v) == Some("resumed")));
    child.kill().await.unwrap();
}

#[tokio::test]
async fn new_sessions_are_fresh_and_resume_is_explicit() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    let store = temp.path().join("store");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
printf '%s\n' "$*" >> calls
next_prompt
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    let mut first_id = Value::Null;
    for index in 0..2 {
        let new_id = 2 + index * 2;
        send(&mut child, json!({"jsonrpc":"2.0","id":new_id,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
        let (new, _) = response(&mut reader, new_id).await;
        let id = new["result"]["sessionId"].clone();
        if index == 0 {
            first_id = id.clone();
        } else {
            assert_ne!(id, first_id);
        }
        send(&mut child, json!({"jsonrpc":"2.0","id":new_id+1,"method":"session/prompt","params":{"sessionId":id,"prompt":[{"type":"text","text":"hello"}]}})).await;
        let (idle, _) = finished(&mut reader, new_id + 1).await;
        assert_eq!(idle["stopReason"], "end_turn", "{idle}");
    }
    child.kill().await.unwrap();

    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":first_id,"cwd":project,"mcpServers":[]}})).await;
    let (resumed, _) = response(&mut reader, 2).await;
    assert!(resumed.get("result").is_some(), "{resumed}");
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":first_id,"prompt":[{"type":"text","text":"continue"}]}})).await;
    finished(&mut reader, 3).await;
    let calls = calls(&project);
    assert_eq!(calls.len(), 3);
    assert!(!calls[0].contains("--resume"));
    assert!(!calls[1].contains("--resume"));
    assert!(calls[2].contains("--resume 79ed6f89-b693-4192-8169-4554404b43a2"));
    let stored: Vec<_> = fs::read_dir(&store)
        .unwrap()
        .flatten()
        .map(|entry| entry.file_name())
        .collect();
    assert_eq!(stored.len(), 2, "{stored:?}");
    assert!(
        stored
            .iter()
            .all(|name| name == "acp-sessions.json" || name == "acp-sessions.lock")
    );

    send(&mut child, json!({"jsonrpc":"2.0","id":4,"method":"session/resume","params":{"sessionId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","cwd":project,"mcpServers":[]}})).await;
    let (unknown, _) = response(&mut reader, 4).await;
    assert_eq!(unknown["error"]["code"], -32002);
    child.kill().await.unwrap();
}

#[tokio::test]
async fn failed_resume_does_not_retry_the_prompt() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    let store = temp.path().join("store");
    // A claude that cannot resume reports why and exits without reading
    // the prompt.
    fake_claude(
        &bin,
        r#"
printf '%s\n' "$*" >> calls
case " $* " in
  *' --resume '*)
    printf '%s\n' '{"type":"result","is_error":true,"result":"resume failed"}'
    exit 1;;
esac
next_prompt
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (idle, _) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(idle["stopReason"], "end_turn", "{idle}");
    child.kill().await.unwrap();

    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":session,"cwd":project,"mcpServers":[]}})).await;
    response(&mut reader, 2).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    let (failed, updates) = response(&mut reader, 3).await;
    assert_eq!(failed["error"]["message"], "resume failed", "{failed}");
    // The prompt never entered the conversation, so the turn it would have
    // started is not reported.
    assert!(
        !updates
            .iter()
            .any(|v| update(v)["sessionUpdate"] == "state_update"),
        "{updates:?}"
    );
    assert_eq!(calls(&project).len(), 2);
    child.kill().await.unwrap();
}

const HISTORY_ID: &str = "79ed6f89-b693-4192-8169-4554404b43a2";

fn dirs(temp: &Path) -> (std::path::PathBuf, std::path::PathBuf) {
    let bin = temp.join("bin");
    let project = temp.join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    (bin, project.canonicalize().unwrap())
}

fn write_transcript(temp: &Path, project: &Path, id: &str) {
    let transcripts = temp.join("empty-claude/projects/sample");
    fs::create_dir_all(&transcripts).unwrap();
    fs::write(
        transcripts.join(format!("{id}.jsonl")),
        format!(
            "{{\"cwd\":\"{}\",\"type\":\"user\",\"message\":{{\"content\":\"Transcript prompt\"}}}}\n",
            project.display()
        ),
    )
    .unwrap();
}

/// The arguments of each claude the bridge started in `project`, where a
/// fake claude records them with `printf '%s\n' "$*" >> calls`.
fn calls(project: &Path) -> Vec<String> {
    fs::read_to_string(project.join("calls"))
        .unwrap()
        .lines()
        .map(str::to_owned)
        .collect()
}

async fn new_session(
    child: &mut Child,
    reader: &mut BufReader<ChildStdout>,
    request_id: i64,
    project: &Path,
) -> Value {
    send(child, json!({"jsonrpc":"2.0","id":request_id,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(reader, request_id).await;
    new["result"]["sessionId"].clone()
}

async fn prompt(
    child: &mut Child,
    reader: &mut BufReader<ChildStdout>,
    request_id: i64,
    session: &Value,
) -> (Value, Vec<Value>) {
    send(child, json!({"jsonrpc":"2.0","id":request_id,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    finished(reader, request_id).await
}

/// Reads messages until one has `text` as its update content.
async fn until_text(reader: &mut BufReader<ChildStdout>, text: &str) -> Vec<Value> {
    let mut seen = Vec::new();
    loop {
        let value = receive(reader).await;
        let done = shown_text(&value) == Some(text);
        seen.push(value);
        if done {
            return seen;
        }
    }
}

/// Reads messages after `seen` until one of them all has `text` as its
/// update content, and returns them all.
async fn and_until_text(
    mut seen: Vec<Value>,
    reader: &mut BufReader<ChildStdout>,
    text: &str,
) -> Vec<Value> {
    if !seen.iter().any(|value| shown_text(value) == Some(text)) {
        seen.extend(until_text(reader, text).await);
    }
    seen
}

/// The message of the error an idle update ends its turn with.
fn error_message(idle: &Value) -> Option<&str> {
    (idle["stopReason"] == "error").then(|| idle["error"]["message"].as_str().unwrap())
}

#[tokio::test]
async fn cancel_sent_right_after_prompt_cancels_it() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r"
next_prompt
interrupted
next_message
",
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let prompt = json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}});
    let cancel = json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}});
    let stdin = child.stdin.as_mut().unwrap();
    stdin
        .write_all(format!("{prompt}\n{cancel}\n").as_bytes())
        .await
        .unwrap();
    let (result, _) = finished(&mut reader, 3).await;
    assert_eq!(result["stopReason"], "cancelled", "{result}");
    child.kill().await.unwrap();
}

/// A prompt claude drops when it is interrupted, before it takes the prompt
/// into a turn, is answered as cancelled.
#[tokio::test]
async fn cancel_drops_a_prompt_claude_has_not_taken() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r"
next_message
interrupted
next_message
",
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let prompt = json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}});
    let cancel = json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}});
    let stdin = child.stdin.as_mut().unwrap();
    stdin
        .write_all(format!("{prompt}\n{cancel}\n").as_bytes())
        .await
        .unwrap();
    let (result, updates) = response(&mut reader, 3).await;
    assert_eq!(result["error"]["code"], -32800, "{result}");
    assert!(
        !updates
            .iter()
            .any(|v| update(v)["sessionUpdate"] == "user_message"),
        "{updates:?}"
    );
    child.kill().await.unwrap();
}

/// A claude that names its session and exits in its first turn, and one
/// that answers after it.
fn exiting_claude(bin: &Path) {
    fake_claude(
        bin,
        &format!(
            r#"
printf '%s\n' "$*" >> calls
next_prompt
if [ "$(wc -l < calls)" -eq 1 ]; then
  printf '%s\n' '{{"type":"system","subtype":"init","session_id":"{HISTORY_ID}"}}'
  exit 1
fi
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
next_message
"#
        ),
    );
}

async fn fail_then_prompt_again(temp: &Path, bin: &Path, project: &Path) -> Vec<String> {
    let (mut child, mut reader) = start(bin, &temp.join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, project).await;
    let (idle, _) = prompt(&mut child, &mut reader, 3, &session).await;
    assert!(error_message(&idle).is_some(), "{idle}");
    let (idle, _) = prompt(&mut child, &mut reader, 4, &session).await;
    assert_eq!(idle["stopReason"], "end_turn", "{idle}");
    child.kill().await.unwrap();
    calls(project)
}

#[tokio::test]
async fn a_claude_that_exits_is_replaced_by_one_that_resumes() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    exiting_claude(&bin);
    write_transcript(temp.path(), &project, HISTORY_ID);
    let calls = fail_then_prompt_again(temp.path(), &bin, &project).await;
    assert_eq!(calls.len(), 2);
    assert!(
        calls[1].contains(&format!("--resume {HISTORY_ID}")),
        "{calls:?}"
    );
}

#[tokio::test]
async fn a_claude_that_exits_without_a_transcript_is_replaced_by_a_fresh_one() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    exiting_claude(&bin);
    let calls = fail_then_prompt_again(temp.path(), &bin, &project).await;
    assert_eq!(calls.len(), 2);
    assert!(!calls[1].contains("--resume"), "{calls:?}");
}

#[tokio::test]
async fn max_turns_reports_its_stop_reason() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
printf '%s\n' '{{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"{HISTORY_ID}","errors":["Reached maximum number of turns (6)"]}}'
exit 1
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (idle, _) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(idle["stopReason"], "max_turn_requests", "{idle}");
    assert_eq!(error_message(&idle), None);
    child.kill().await.unwrap();
}

#[tokio::test]
async fn error_results_report_their_errors() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["first","second"]}'
exit 1
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (idle, _) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(error_message(&idle), Some("first; second"), "{idle}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn listed_sessions_can_be_loaded() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    write_transcript(temp.path(), &project, HISTORY_ID);
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    prompt(&mut child, &mut reader, 3, &session).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":4,"method":"session/list","params":{"cwd":project}}),
    )
    .await;
    let (list, _) = response(&mut reader, 4).await;
    let ids: Vec<&Value> = list["result"]["sessions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|s| &s["sessionId"])
        .collect();
    assert_eq!(ids, [&session], "{list}");
    assert_eq!(list["result"]["sessions"][0]["title"], "hello");

    send(&mut child, json!({"jsonrpc":"2.0","id":5,"method":"session/resume","params":{"sessionId":session,"cwd":project,"mcpServers":[]}})).await;
    let (load, _) = response(&mut reader, 5).await;
    assert!(load.get("result").is_some(), "{load}");
    send(&mut child, json!({"jsonrpc":"2.0","id":6,"method":"session/resume","params":{"sessionId":session,"cwd":temp.path(),"mcpServers":[]}})).await;
    let (load, _) = response(&mut reader, 6).await;
    assert!(load.get("error").is_some(), "{load}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn bridges_sharing_a_store_keep_each_others_sessions() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(&bin, "exit 0");
    let store = temp.path().join("store");
    let (mut a, mut a_reader) = start(&bin, &store);
    let (mut b, mut b_reader) = start(&bin, &store);
    initialize(&mut a, &mut a_reader).await;
    initialize(&mut b, &mut b_reader).await;
    let s1 = new_session(&mut a, &mut a_reader, 2, &project).await;
    let s2 = new_session(&mut b, &mut b_reader, 2, &project).await;
    let s3 = new_session(&mut a, &mut a_reader, 3, &project).await;

    send(&mut b, json!({"jsonrpc":"2.0","id":3,"method":"session/resume","params":{"sessionId":s1,"cwd":project,"mcpServers":[]}})).await;
    let (load, _) = response(&mut b_reader, 3).await;
    assert!(load.get("result").is_some(), "{load}");

    let saved: Value =
        serde_json::from_slice(&fs::read(store.join("acp-sessions.json")).unwrap()).unwrap();
    for id in [&s1, &s2, &s3] {
        assert!(
            saved.get(id.as_str().unwrap()).is_some(),
            "{id} missing: {saved}"
        );
    }
    a.kill().await.unwrap();
    b.kill().await.unwrap();
}

#[tokio::test]
async fn a_corrupt_store_is_set_aside() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(&bin, "exit 0");
    let store = temp.path().join("store");
    fs::create_dir_all(&store).unwrap();
    fs::write(store.join("acp-sessions.json"), "not json").unwrap();
    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let aside: Vec<_> = fs::read_dir(&store)
        .unwrap()
        .flatten()
        .filter(|e| {
            e.file_name()
                .to_string_lossy()
                .starts_with("acp-sessions.json.corrupt-")
        })
        .collect();
    assert_eq!(aside.len(), 1);
    assert_eq!(fs::read_to_string(aside[0].path()).unwrap(), "not json");
    let saved: Value =
        serde_json::from_slice(&fs::read(store.join("acp-sessions.json")).unwrap()).unwrap();
    assert!(saved.get(session.as_str().unwrap()).is_some(), "{saved}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn an_unusable_store_fails_session_requests() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(&bin, "exit 0");
    write_transcript(temp.path(), &project, HISTORY_ID);
    let store = temp.path().join("store");
    fs::write(&store, "").unwrap();
    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    let requests = [
        json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}}),
        json!({"jsonrpc":"2.0","id":3,"method":"session/list","params":{"cwd":project}}),
        json!({"jsonrpc":"2.0","id":4,"method":"session/resume","params":{
            "sessionId":HISTORY_ID,"cwd":project,"mcpServers":[]
        }}),
    ];
    for request in requests {
        let id = request["id"].as_i64().unwrap();
        let method = request["method"].clone();
        send(&mut child, request).await;
        let (answer, _) = response(&mut reader, id).await;
        assert_eq!(answer["error"]["code"], -32603, "{method}: {answer}");
    }
    child.kill().await.unwrap();
}

#[tokio::test]
async fn failed_registry_reads_and_writes_fail_session_new() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(&bin, "exit 0");
    // A registry that is a directory cannot be read.
    let unreadable = temp.path().join("unreadable");
    fs::create_dir_all(unreadable.join("acp-sessions.json")).unwrap();
    // A read-only store can be locked and read, but not written.
    let read_only = temp.path().join("read-only");
    fs::create_dir_all(&read_only).unwrap();
    fs::write(read_only.join("acp-sessions.lock"), "").unwrap();
    fs::write(read_only.join("acp-sessions.json"), "{}").unwrap();
    fs::set_permissions(&read_only, fs::Permissions::from_mode(0o555)).unwrap();
    let mut answers = Vec::new();
    for store in [&unreadable, &read_only] {
        let (mut child, mut reader) = start(&bin, store);
        initialize(&mut child, &mut reader).await;
        send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
        let (answer, _) = response(&mut reader, 2).await;
        child.kill().await.unwrap();
        answers.push((store.display().to_string(), answer));
    }
    fs::set_permissions(&read_only, fs::Permissions::from_mode(0o755)).unwrap();
    for (store, answer) in answers {
        assert_eq!(answer["error"]["code"], -32603, "{store}: {answer}");
    }
}

#[tokio::test]
async fn unreadable_store_entries_survive_updates() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(&bin, "exit 0");
    let store = temp.path().join("store");
    fs::create_dir_all(&store).unwrap();
    fs::write(
        store.join("acp-sessions.json"),
        r#"{"other": {"unexpected": true}}"#,
    )
    .unwrap();
    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    new_session(&mut child, &mut reader, 2, &project).await;
    let saved: Value =
        serde_json::from_slice(&fs::read(store.join("acp-sessions.json")).unwrap()).unwrap();
    assert_eq!(saved["other"], json!({"unexpected": true}));
    child.kill().await.unwrap();
}

#[tokio::test]
async fn cancel_fails_tool_calls_that_did_not_finish() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"sleep 30"}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
interrupted
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    let seen = until_text(&mut reader, "working").await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (result, updates) = finish(&mut reader, 3, seen).await;
    assert_eq!(result["stopReason"], "cancelled", "{result}");
    assert!(
        updates.iter().any(|v| {
            update(v)["sessionUpdate"] == "tool_call_update"
                && update(v)["toolCallId"] == "tool-1"
                && update(v)["status"] == "failed"
        }),
        "{updates:?}"
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn prompt_content_reaches_claude() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    let captured = temp.path().join("message.json");
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
printf '%s\n' "$message" > '{}'
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}","result":"done"}}'
"#,
            captured.display()
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":2,"capabilities":{},"info":{"name":"test","version":"1"}}}),
    )
    .await;
    let (init, _) = response(&mut reader, 1).await;
    let capabilities = &init["result"]["capabilities"]["session"]["prompt"];
    assert!(capabilities["image"].is_object(), "{init}");
    assert!(capabilities["embeddedContext"].is_object(), "{init}");
    assert!(capabilities["audio"].is_null(), "{init}");

    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let prompt = json!([
        {"type": "text", "text": "Look at these"},
        {"type": "image", "mimeType": "image/png", "data": "iVBORw0KGgo="},
        {"type": "resource_link", "name": "main.rs", "uri": "file:///p/src/main.rs"},
        {"type": "resource", "resource": {"uri": "file:///p/notes.md", "text": "Notes body", "mimeType": "text/markdown"}},
        {"type": "resource", "resource": {"uri": "file:///p/spec.pdf", "blob": "JVBERi0=", "mimeType": "application/pdf"}},
        {"type": "resource", "resource": {"uri": "file:///p/shot.jpg", "blob": "/9j/4A==", "mimeType": "image/jpeg"}}
    ]);
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":prompt}})).await;
    let (result, _) = finished(&mut reader, 3).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");

    let message: Value = serde_json::from_str(&fs::read_to_string(&captured).unwrap()).unwrap();
    assert_eq!(
        message["message"]["content"],
        json!([
            {"type": "text", "text": "Look at these"},
            {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}},
            {"type": "text", "text": "[@main.rs](file:///p/src/main.rs)"},
            {"type": "text", "text": "<context ref=\"file:///p/notes.md\">\nNotes body\n</context>"},
            {"type": "document", "source": {"type": "base64", "media_type": "application/pdf", "data": "JVBERi0="}},
            {"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "/9j/4A=="}}
        ])
    );
    let _ = child.kill().await;
}

#[tokio::test]
async fn unsupported_prompt_content_is_rejected() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    let ran = temp.path().join("ran");
    fake_claude(&bin, &format!("touch '{}'", ran.display()));
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let prompts = [
        json!([{"type": "text", "text": "listen"}, {"type": "audio", "mimeType": "audio/wav", "data": "UklGRg=="}]),
        json!([{"type": "image", "mimeType": "image/bmp", "data": "Qk0="}]),
        json!([{"type": "resource", "resource": {"uri": "file:///p/a.zip", "blob": "UEs=", "mimeType": "application/zip"}}]),
        json!([{"type": "text", "text": "  "}]),
        json!([]),
    ];
    for (id, prompt) in (10_i64..).zip(prompts) {
        send(&mut child, json!({"jsonrpc":"2.0","id":id,"method":"session/prompt","params":{"sessionId":session,"prompt":prompt}})).await;
        let (result, _) = response(&mut reader, id).await;
        assert_eq!(result["error"]["code"], -32602, "{result}");
    }
    assert!(!ran.exists(), "claude ran for a rejected prompt");
    let _ = child.kill().await;
}

#[tokio::test]
async fn failed_exit_reports_claude_stderr() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // The background process keeps stdout and stderr open after claude exits.
    fake_claude(
        &bin,
        r#"
next_prompt
state running
echo "unknown option --bogus" >&2
sleep 10 &
exit 1
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (idle, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    let error = error_message(&idle).unwrap();
    assert!(error.ends_with(": unknown option --bogus"), "{error}");
    // A claude that exits while working is no longer working.
    let states: Vec<String> = describe_updates(&updates)
        .into_iter()
        .filter(|update| update.starts_with("state_update"))
        .collect();
    assert_eq!(
        states,
        [
            "state_update: running".to_owned(),
            format!("state_update: idle error {error}")
        ]
    );
    child.kill().await.unwrap();
}

/// Writes a transcript that mixes conversation entries with entries the
/// replay skips: Claude's bookkeeping, a meta message and a sidechain.
fn write_conversation(temp: &Path, project: &Path, id: &str) {
    let transcripts = temp.join("empty-claude/projects/sample");
    fs::create_dir_all(&transcripts).unwrap();
    let entries = [
        json!({"type": "queue-operation", "operation": "enqueue"}),
        json!({"cwd": project, "type": "user", "message": {"role": "user", "content": "Fix the build"}}),
        json!({"type": "user", "isMeta": true, "message": {"role": "user", "content": "<system-reminder>"}}),
        json!({"type": "assistant", "message": {"content": [{"type": "thinking", "thinking": "", "signature": "x"}]}}),
        json!({"type": "assistant", "message": {"content": [{"type": "thinking", "thinking": "Checking"}]}}),
        json!({"type": "assistant", "message": {"content": [{"type": "text", "text": "Running it"}]}}),
        json!({"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": {"command": "cargo build"}}]}}),
        json!({"type": "assistant", "isSidechain": true, "message": {"content": [{"type": "text", "text": "subagent"}]}}),
        json!({"type": "user", "message": {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok"}]}}),
        json!({"type": "attachment", "attachment": {}}),
        json!({"type": "user", "message": {"role": "user", "content": [
            {"type": "text", "text": "Look"},
            {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}}
        ]}}),
        json!({"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "toolu_3", "name": "Agent", "input": {"prompt": "Read /a"}}]}}),
        json!({"type": "user", "message": {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_3", "content": [
            {"type": "text", "text": "a says hi"},
            {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}}
        ]}]}}),
        json!({"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "toolu_2", "name": "Read", "input": {"file_path": "/x"}}]}}),
    ];
    write_lines(&transcripts.join(format!("{id}.jsonl")), &entries);
    // The subagent toolu_3 started keeps its own transcript.
    let subagents = transcripts.join(id).join("subagents");
    fs::create_dir_all(&subagents).unwrap();
    fs::write(
        subagents.join("agent-a1.meta.json"),
        json!({"agentType": "general-purpose", "toolUseId": "toolu_3"}).to_string(),
    )
    .unwrap();
    write_lines(
        &subagents.join("agent-a1.jsonl"),
        &[
            json!({"type": "user", "isSidechain": true, "message": {"role": "user", "content": "Read /a"}}),
            json!({"type": "assistant", "isSidechain": true, "message": {"content": [
                {"type": "text", "text": "subagent text"},
                {"type": "tool_use", "id": "toolu_s1", "name": "Read", "input": {"file_path": "/a"}}
            ]}}),
            json!({"type": "user", "isSidechain": true, "message": {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_s1", "content": "hi"}]}}),
        ],
    );
}

fn write_lines(path: &Path, entries: &[Value]) {
    let lines: Vec<String> = entries.iter().map(Value::to_string).collect();
    fs::write(path, lines.join("\n") + "\n").unwrap();
}

/// One line per update: its kind and what it shows.
fn describe_updates(notifications: &[Value]) -> Vec<String> {
    notifications
        .iter()
        .filter(|n| n["method"] == "session/update")
        .map(|n| {
            let update = update(n);
            let kind = update["sessionUpdate"].as_str().unwrap();
            let blocks = |content: &Value| -> String {
                content
                    .as_array()
                    .into_iter()
                    .flatten()
                    .map(|c| match c["text"].as_str() {
                        Some(text) => text.to_owned(),
                        None => c["type"].as_str().unwrap().to_owned(),
                    })
                    .collect::<Vec<_>>()
                    .join(" | ")
            };
            let detail = match kind {
                "state_update" => {
                    let mut detail = update["state"].as_str().unwrap().to_owned();
                    if let Some(reason) = update["stopReason"].as_str() {
                        detail = format!("{detail} {reason}");
                    }
                    if let Some(error) = update["error"]["message"].as_str() {
                        detail = format!("{detail} {error}");
                    }
                    detail
                }
                "tool_call_update" => {
                    let mut detail = update["toolCallId"].to_string();
                    if let Some(title) = update["title"].as_str() {
                        detail = format!("{detail} {title}");
                    }
                    if let Some(status) = update["status"].as_str() {
                        detail = format!("{detail} {status}");
                    }
                    let content: Vec<String> = update["content"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .map(|c| match c["content"]["text"].as_str() {
                            Some(text) => text.to_owned(),
                            None => c["content"]["type"].as_str().unwrap().to_owned(),
                        })
                        .collect();
                    if !content.is_empty() {
                        detail = format!("{detail} [{}]", content.join(" | "));
                    }
                    detail
                }
                // Whole messages carry their blocks, and chunks one block.
                "user_message" | "agent_message" | "agent_thought" => blocks(&update["content"]),
                _ => match update["content"]["type"].as_str() {
                    Some("text") => update["content"]["text"].to_string(),
                    Some(other) => other.to_owned(),
                    None => String::new(),
                },
            };
            format!("{kind}: {}", detail.replace('"', ""))
        })
        .collect()
}

const REPLAY: [&str; 11] = [
    "user_message: Fix the build",
    "agent_thought: Checking",
    "agent_message: Running it",
    "tool_call_update: toolu_1 Bash `cargo build` pending",
    "tool_call_update: toolu_1 completed [ok]",
    "user_message: Look | image",
    "tool_call_update: toolu_3 Agent pending",
    "tool_call_update: toolu_3 [Read /a]",
    "tool_call_update: toolu_3 completed [Read /a | a says hi | image]",
    "tool_call_update: toolu_2 Read `/x` pending",
    "tool_call_update: toolu_2 failed",
];

/// The replay of `write_conversation`, then the idle state a resume sends.
fn replay_and_state() -> Vec<String> {
    REPLAY
        .iter()
        .copied()
        .chain(["state_update: idle"])
        .map(str::to_owned)
        .collect()
}

#[tokio::test]
async fn resume_replays_a_claude_session_from_its_transcript() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    write_conversation(temp.path(), &project, HISTORY_ID);
    fake_claude(
        &bin,
        &format!(
            r#"
printf '%s\n' "$*" > args
next_prompt
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":HISTORY_ID,"cwd":project,"mcpServers":[],"replayFrom":{"type":"start"}}})).await;
    let (loaded, notifications) = response(&mut reader, 2).await;
    assert!(loaded["result"].is_object(), "{loaded}");
    // The replay comes first, then the session's state, then the response.
    assert_eq!(describe_updates(&notifications), replay_and_state());

    // Without `replayFrom`, nothing replays.
    send(&mut child, json!({"jsonrpc":"2.0","id":4,"method":"session/resume","params":{"sessionId":HISTORY_ID,"cwd":project,"mcpServers":[]}})).await;
    let (resumed, notifications) = response(&mut reader, 4).await;
    assert!(resumed["result"].is_object(), "{resumed}");
    assert_eq!(describe_updates(&notifications), ["state_update: idle"]);

    // A cursor other than the start is not supported.
    send(&mut child, json!({"jsonrpc":"2.0","id":5,"method":"session/resume","params":{"sessionId":HISTORY_ID,"cwd":project,"mcpServers":[],"replayFrom":{"type":"cursor","cursor":"x"}}})).await;
    let (refused, notifications) = response(&mut reader, 5).await;
    assert_eq!(refused["error"]["code"], -32602, "{refused}");
    assert!(notifications.is_empty(), "{notifications:?}");

    let (result, _) = prompt(&mut child, &mut reader, 3, &json!(HISTORY_ID)).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    let args = fs::read_to_string(project.join("args")).unwrap();
    assert!(args.contains(&format!("--resume {HISTORY_ID}")), "{args}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn resume_replays_a_bridge_session_from_its_claude_transcript() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    write_conversation(temp.path(), &project, HISTORY_ID);
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
"#
        ),
    );
    let store = temp.path().join("store");
    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    prompt(&mut child, &mut reader, 3, &session).await;
    child.kill().await.unwrap();

    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":session,"cwd":project,"mcpServers":[],"replayFrom":{"type":"start"}}})).await;
    let (loaded, notifications) = response(&mut reader, 2).await;
    assert!(loaded["result"].is_object(), "{loaded}");
    assert!(
        notifications
            .iter()
            .all(|n| n["params"]["sessionId"] == session),
        "{notifications:?}"
    );
    assert_eq!(describe_updates(&notifications), replay_and_state());
    child.kill().await.unwrap();
}

#[tokio::test]
async fn a_session_keeps_one_claude_across_prompts() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
printf '%s\n' "$*" >> calls
turn=0
while :; do
  next_prompt
  turn=$((turn + 1))
  printf '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"turn %s"}}]}}}}\n' "$turn"
  printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
done
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    for (request_id, text) in [(3, "turn 1"), (4, "turn 2"), (5, "turn 3")] {
        let (result, updates) = prompt(&mut child, &mut reader, request_id, &session).await;
        assert_eq!(result["stopReason"], "end_turn", "{result}");
        assert!(
            updates.iter().any(|v| shown_text(v) == Some(text)),
            "missing {text} in {updates:?}"
        );
    }
    let calls = calls(&project);
    assert_eq!(calls.len(), 1, "{calls:?}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn turns_claude_starts_reach_the_client() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // After the prompt's turn, claude runs two turns of its own, as it does
    // when a background task reports back. The second one fails.
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
state running
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
state idle
state running
printf '%s\n' '{{"type":"assistant","message":{{"content":[{{"type":"tool_use","id":"tool-1","name":"Bash","input":{{"command":"pwd"}}}}]}}}}'
state requires_action
state running
printf '%s\n' '{{"type":"user","message":{{"role":"user","content":[{{"type":"tool_result","tool_use_id":"tool-1","content":""}}]}}}}'
printf '%s\n' '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"woke"}}]}}}}'
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
state idle
state running
printf '%s\n' '{{"type":"result","is_error":true,"result":"wake failed"}}'
state idle
state running
printf '%s\n' '{{"type":"result","subtype":"error_max_turns","is_error":true}}'
state idle
printf '%s\n' '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"end"}}]}}}}'
next_message
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (result, before) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    let after = until_text(&mut reader, "end").await;
    let describe = |messages: &[Value]| -> Vec<String> {
        describe_updates(messages)
            .into_iter()
            .filter(|update| !update.starts_with("session_info_update"))
            .collect()
    };
    // The client has the prompt's response before what claude does next.
    assert_eq!(
        describe(&before),
        [
            "user_message: hello",
            "state_update: running",
            "state_update: idle end_turn"
        ]
    );
    assert_eq!(
        describe(&after),
        [
            "state_update: running",
            "tool_call_update: tool-1 Bash `pwd` pending",
            "tool_call_update: tool-1 completed",
            "agent_message: woke",
            "state_update: idle end_turn",
            "state_update: running",
            "state_update: idle error wake failed",
            "state_update: running",
            "state_update: idle max_turn_requests",
            "agent_message: end",
        ]
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn a_prompt_sent_during_a_turn_claude_started_ends_with_its_own_result() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // Claude reads the second prompt while a turn of its own runs, ends that
    // turn, and only then starts the prompt's.
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
printf '%s\n' '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"waking"}}]}}}}'
next_message
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
started
printf '%s\n' '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"answer"}}]}}}}'
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
next_message
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (_, seen) = prompt(&mut child, &mut reader, 3, &session).await;
    and_until_text(seen, &mut reader, "waking").await;
    let (result, updates) = prompt(&mut child, &mut reader, 4, &session).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    assert!(
        updates.iter().any(|v| shown_text(v) == Some("answer")),
        "{updates:?}"
    );
    child.kill().await.unwrap();
}

/// The process group of `pid`.
fn process_group(pid: &str) -> String {
    let output = std::process::Command::new("ps")
        .args(["-o", "pgid=", "-p", pid])
        .output()
        .unwrap();
    String::from_utf8(output.stdout).unwrap().trim().to_owned()
}

#[tokio::test]
async fn claude_runs_in_the_bridges_process_group() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
printf '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"%s"}}]}}}}\n' "$$"
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
next_message
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (_, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    let claude = updates
        .iter()
        .filter(|v| update(v)["sessionUpdate"] == "agent_message")
        .find_map(shown_text)
        .unwrap();
    let bridge = child.id().unwrap().to_string();
    assert_eq!(process_group(claude), process_group(&bridge));
    child.kill().await.unwrap();
}

#[tokio::test]
async fn resuming_with_mcp_servers_starts_a_claude_that_has_them() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
printf '%s\n' "$*" >> calls
while :; do
  next_prompt
  printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
done
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    prompt(&mut child, &mut reader, 3, &session).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":4,"method":"session/resume","params":{
            "sessionId":session,"cwd":project,
            "mcpServers":[{"type":"stdio","name":"filesystem","command":"/usr/bin/node","args":["server.js"],"env":[]}]
        }}),
    )
    .await;
    response(&mut reader, 4).await;
    let (result, _) = prompt(&mut child, &mut reader, 5, &session).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    let calls = calls(&project);
    assert_eq!(calls.len(), 2, "{calls:?}");
    assert!(!calls[0].contains("--mcp-config"), "{calls:?}");
    assert!(
        calls[1].contains("--mcp-config") && calls[1].contains(&format!("--resume {HISTORY_ID}")),
        "{calls:?}"
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn tool_output_reaches_the_client() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"ls"}},{"type":"tool_use","id":"tool-2","name":"Read","input":{"file_path":"/a.png"}}]}}'
printf '%s\n' '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"a.png\nb.rs","is_error":true}]}}'
printf '%s\n' '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-2","content":[{"type":"text","text":"Read an image"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},{"type":"tool_reference","tool_name":"WebSearch"}]}]}}'
printf '%s\n' '{"type":"result"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (result, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    assert_eq!(
        describe_updates(&updates)[3..7],
        [
            "tool_call_update: tool-1 Bash `ls` pending",
            "tool_call_update: tool-2 Read `/a.png` pending",
            "tool_call_update: tool-1 failed [a.png\nb.rs]",
            "tool_call_update: tool-2 completed [Read an image | image | {type:tool_reference,tool_name:WebSearch}]",
        ]
    );
    let image = updates
        .iter()
        .find_map(|n| n["params"]["update"]["content"].as_array()?.get(1).cloned())
        .unwrap();
    assert_eq!(
        image["content"],
        json!({"type": "image", "mimeType": "image/png", "data": "iVBORw0KGgo="})
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn a_subagents_activity_shows_under_its_tool_call() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // The subagent's lines name the Agent tool call that started it. Its
    // Bash call asks permission, so the client has that call.
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"agent-1","name":"Agent","input":{"prompt":"Look"}}]}}'
printf '%s\n' '{"type":"user","parent_tool_use_id":"agent-1","message":{"role":"user","content":[{"type":"text","text":"Look"}]}}'
printf '%s\n' '{"type":"stream_event","parent_tool_use_id":"agent-1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"sub streamed"}}}'
printf '%s\n' '{"type":"stream_event","parent_tool_use_id":"agent-1","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"sub thinking"}}}'
printf '%s\n' '{"type":"assistant","parent_tool_use_id":"agent-1","message":{"content":[{"type":"thinking","thinking":"sub thinking"},{"type":"text","text":"sub text"},{"type":"tool_use","id":"sub-1","name":"Read","input":{"file_path":"/a"}}]}}'
printf '%s\n' '{"type":"user","parent_tool_use_id":"agent-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"sub-1","content":"a"}]}}'
printf '%s\n' '{"type":"assistant","parent_tool_use_id":"agent-1","message":{"content":[{"type":"tool_use","id":"sub-2","name":"Bash","input":{"command":"pwd"}}]}}'
printf '%s\n' '{"type":"control_request","request_id":"perm-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"pwd"},"tool_use_id":"sub-2"}}'
next_message
printf '%s\n' '{"type":"user","parent_tool_use_id":"agent-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"sub-2","content":"/"}]}}'
printf '%s\n' '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"agent-1","content":[{"type":"text","text":"a says hi"}]}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}'
printf '%s\n' '{"type":"result"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    let (permission, seen) = until_permission(&mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":permission["id"],"result":{"outcome":{"outcome":"selected","optionId":"allow_once"}}})).await;
    let (result, updates) = finish(&mut reader, 3, seen).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    assert_eq!(
        describe_updates(&updates)
            .into_iter()
            .filter(|update| !update.starts_with("session_info_update"))
            .collect::<Vec<_>>(),
        [
            "user_message: hello",
            "state_update: running",
            "tool_call_update: agent-1 Agent pending",
            "tool_call_update: agent-1 [Read /a]",
            "tool_call_update: agent-1 [Read /a | Bash pwd]",
            "tool_call_update: sub-2 Bash `pwd` pending",
            "state_update: requires_action",
            "tool_call_update: sub-2 in_progress",
            "state_update: running",
            "tool_call_update: sub-2 completed [/]",
            "tool_call_update: agent-1 completed [Read /a | Bash pwd | a says hi]",
            "agent_message: done",
            "state_update: idle end_turn",
        ]
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn claudes_echo_of_a_prompt_starts_its_turn() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // Claude says it took the prompt only by echoing it, here while the
    // turn streams, as it does for a prompt it folds into a running turn.
    fake_claude(
        &bin,
        r#"
next_message
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello "}}}'
echoed
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}'
printf '%s\n' '{"type":"result"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (result, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(result["stopReason"], "end_turn", "{result}");
    assert_eq!(
        describe_updates(&updates)
            .into_iter()
            .filter(|update| !update.starts_with("session_info_update"))
            .collect::<Vec<_>>(),
        [
            "agent_message_chunk: hello ",
            "user_message: hello",
            "state_update: running",
            "state_update: idle end_turn"
        ]
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn a_prompt_is_answered_when_it_enters_the_conversation() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
interrupted
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    // Only the prompt's own message comes before its response; the turn's
    // state and output follow it.
    let (answer, before) = response(&mut reader, 3).await;
    let kinds: Vec<&str> = before
        .iter()
        .map(|v| update(v)["sessionUpdate"].as_str().unwrap())
        .collect();
    assert_eq!(kinds, ["session_info_update", "user_message"], "{before:?}");
    assert_eq!(
        update(&before[1])["messageId"],
        answer["result"]["messageId"]
    );
    assert_eq!(update(&before[1])["content"][0]["text"], "hello");
    let after = until_text(&mut reader, "working").await;
    assert_eq!(describe_updates(&after[..1]), ["state_update: running"]);

    // A client that resumes the session while it works is told so before
    // the response.
    send(&mut child, json!({"jsonrpc":"2.0","id":4,"method":"session/resume","params":{"sessionId":session,"cwd":project,"mcpServers":[]}})).await;
    let (resumed, notifications) = response(&mut reader, 4).await;
    assert!(resumed["result"].is_object(), "{resumed}");
    assert_eq!(describe_updates(&notifications), ["state_update: running"]);

    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (idle, _) = finish(&mut reader, 3, [before, vec![answer], after].concat()).await;
    assert_eq!(idle["stopReason"], "cancelled", "{idle}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn close_stops_claude_and_the_session() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    // Claude ends when its stdin closes, after reading the interrupt.
    fake_claude(
        &bin,
        r#"
echo "$$" > pid
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
next_message
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    let seen = until_text(&mut reader, "working").await;
    let pid = fs::read_to_string(project.join("pid")).unwrap();
    let running = |pid: &str| {
        std::process::Command::new("kill")
            .args(["-0", pid.trim()])
            .stderr(Stdio::null())
            .status()
            .unwrap()
            .success()
    };
    assert!(running(&pid));

    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":4,"method":"session/close","params":{"sessionId":session}}),
    )
    .await;
    let (closed, notifications) = response(&mut reader, 4).await;
    assert!(closed["result"].is_object(), "{closed}");
    // Claude has exited, so its turn ended as cancelled before the response.
    assert!(
        notifications.iter().any(|v| is_state(v, "idle")),
        "{notifications:?}"
    );
    let (idle, _) = finish(&mut reader, 3, [seen, notifications].concat()).await;
    assert_eq!(idle["stopReason"], "cancelled", "{idle}");
    assert!(!running(&pid), "claude {pid} still runs");

    // A closed session takes no prompt until it is resumed, and closing it
    // again, or one that never existed, succeeds.
    send(&mut child, json!({"jsonrpc":"2.0","id":5,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    let (refused, _) = response(&mut reader, 5).await;
    assert_eq!(refused["error"]["code"], -32002, "{refused}");
    for (id, session) in [(6, json!(session)), (7, json!("nonexistent"))] {
        send(
            &mut child,
            json!({"jsonrpc":"2.0","id":id,"method":"session/close","params":{"sessionId":session}}),
        )
        .await;
        let (closed, _) = response(&mut reader, id).await;
        assert!(closed["result"].is_object(), "{closed}");
    }
    child.kill().await.unwrap();
}

#[tokio::test]
async fn mcp_commands_are_allowed_absolute_paths() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(&bin, "exit 0");
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    for (id, command) in [(2, "node"), (3, "/usr/bin/curl"), (4, "/usr/bin/node;rm")] {
        send(&mut child, json!({"jsonrpc":"2.0","id":id,"method":"session/new","params":{
            "cwd":project,"mcpServers":[{"type":"stdio","name":"x","command":command,"args":[],"env":[]}]
        }})).await;
        let (refused, _) = response(&mut reader, id).await;
        assert_eq!(refused["error"]["code"], -32602, "{command}: {refused}");
    }
    child.kill().await.unwrap();
}

#[tokio::test]
async fn replay_keeps_the_inserted_user_message_id() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
next_prompt
printf '%s\n' "$message" > captured.json
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
next_message
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (_, live) = prompt(&mut child, &mut reader, 3, &session).await;
    let original = live
        .iter()
        .find(|n| update(n)["sessionUpdate"] == "user_message")
        .unwrap();
    let original_id = update(original)["messageId"].clone();
    let mut retained: Value =
        serde_json::from_slice(&fs::read(project.join("captured.json")).unwrap()).unwrap();
    assert_eq!(retained["uuid"], original_id);
    retained["cwd"] = json!(project);
    let transcripts = temp.path().join("empty-claude/projects/sample");
    fs::create_dir_all(&transcripts).unwrap();
    write_lines(
        &transcripts.join(format!("{HISTORY_ID}.jsonl")),
        &[retained],
    );
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":4,"method":"session/resume","params":{
            "sessionId":session,"cwd":project,"mcpServers":[],"replayFrom":{"type":"start"}
        }}),
    )
    .await;
    let (resumed, replay) = response(&mut reader, 4).await;
    child.kill().await.unwrap();
    assert!(resumed["result"].is_object(), "{resumed}");
    let replayed = replay
        .iter()
        .find(|n| update(n)["sessionUpdate"] == "user_message")
        .unwrap();
    assert_eq!(
        update(replayed)["messageId"],
        original_id,
        "the replayed prompt keeps its message ID"
    );
}

#[tokio::test]
async fn unsupported_mcp_transport_is_rejected() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(&bin, "exit 0");
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{
        "cwd":project,"mcpServers":[{"type":"http","name":"context","url":"https://example.com/mcp","headers":[]}]
    }})).await;
    let (answer, _) = response(&mut reader, 2).await;
    child.kill().await.unwrap();
    assert_eq!(
        answer["error"]["code"], -32602,
        "unsupported MCP server silently accepted: {answer}"
    );
}

#[tokio::test]
async fn a_queued_prompt_does_not_hide_a_running_turns_error() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"first turn running"}]}}'
next_message
printf '%s\n' '{"type":"result","is_error":true,"result":"first turn failed"}'
state idle
started
printf '%s\n' '{"type":"result"}'
next_message
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{
            "sessionId":session,"prompt":[{"type":"text","text":"first"}]
        }}),
    )
    .await;
    let before = until_text(&mut reader, "first turn running").await;
    assert!(before.iter().any(|v| is_state(v, "running")), "{before:?}");
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{
            "sessionId":session,"prompt":[{"type":"text","text":"second"}]
        }}),
    )
    .await;
    let (_, after) = finished(&mut reader, 4).await;
    child.kill().await.unwrap();
    assert!(
        after
            .iter()
            .any(|v| is_state(v, "idle") && error_message(update(v)) == Some("first turn failed")),
        "running turn's error disappeared: {:?}",
        describe_updates(&after)
    );
}

#[tokio::test]
async fn cancelled_work_that_exits_reports_cancelled() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
next_prompt
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
next_message
case "$message" in *'"subtype":"interrupt"'*) ;; *) exit 21;; esac
echo 'aborting the turn failed' >&2
exit 1
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{
            "sessionId":session,"prompt":[{"type":"text","text":"hello"}]
        }}),
    )
    .await;
    let before = until_text(&mut reader, "working").await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (idle, _) = finish(&mut reader, 3, before).await;
    child.kill().await.unwrap();
    assert_eq!(
        idle["stopReason"], "cancelled",
        "cancellation was classified as a failure: {idle}"
    );
}
