#![cfg(unix)]

use std::{fs, os::unix::fs::PermissionsExt, path::Path, process::Stdio};

use serde_json::{Value, json};
use tokio::{
    io::{AsyncBufReadExt, AsyncWriteExt, BufReader},
    process::{Child, ChildStdout, Command},
    time::{Duration, timeout},
};

fn fake_claude(dir: &Path, script: &str) {
    let path = dir.join("claude");
    fs::write(&path, format!("#!/bin/sh\n{script}\n")).unwrap();
    fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
}

fn start(dir: &Path, store: &Path) -> (Child, BufReader<ChildStdout>) {
    start_with_timeout(dir, store, None)
}

fn start_with_timeout(
    dir: &Path,
    store: &Path,
    timeout_ms: Option<u64>,
) -> (Child, BufReader<ChildStdout>) {
    let mut command = Command::new(env!("CARGO_BIN_EXE_claude-code-acp"));
    if let Some(timeout_ms) = timeout_ms {
        command.env("CLAUDE_ACP_TIMEOUT", timeout_ms.to_string());
    }
    let mut child = command
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

async fn initialize(child: &mut Child, reader: &mut BufReader<ChildStdout>) {
    send(
        child,
        json!({"jsonrpc":"2.0","id":1,"method":"initialize","params":{
            "protocolVersion":1,"clientCapabilities":{},"clientInfo":{"name":"test","version":"1"}
        }}),
    )
    .await;
    let (value, _) = response(reader, 1).await;
    assert_eq!(value["result"]["protocolVersion"], 1);
    assert_eq!(value["result"]["agentCapabilities"]["loadSession"], true);
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
    assert_eq!(mode["id"], "mode");
    assert_eq!(mode["currentValue"], "manual");
    assert_eq!(mode["options"].as_array().unwrap().len(), 4);
    assert_eq!(
        new["result"]["configOptions"][0]["currentValue"],
        "claude-opus-5-5"
    );

    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":7,"method":"session/set_config_option","params":{
            "sessionId":id,"configId":"mode","value":"plan"
        }}),
    )
    .await;
    let (set_mode, mode_updates) = response(&mut reader, 7).await;
    let mode = set_mode["result"]["configOptions"]
        .as_array()
        .unwrap()
        .iter()
        .find(|option| option["id"] == "mode")
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
            "sessionId":id,"configId":"model","value":"claude-opus-5-5"
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
    let (prompt, updates) = response(&mut reader, 4).await;
    assert_eq!(prompt["result"]["stopReason"], "end_turn");
    let texts: Vec<_> = updates
        .iter()
        .filter(|v| v["params"]["update"]["sessionUpdate"] == "agent_message_chunk")
        .map(|v| v["params"]["update"]["content"]["text"].as_str().unwrap())
        .collect();
    assert_eq!(texts, ["hello "]);
    assert!(
        updates
            .iter()
            .any(|v| v["params"]["update"]["sessionUpdate"] == "tool_call")
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

#[tokio::test]
async fn selected_mode_reaches_claude() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
mode='missing'
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--permission-mode' ]; then mode="$2"; break; fi
  shift
done
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}\n' "$mode"
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2","result":"done"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].as_str().unwrap().to_owned();

    for (index, mode) in ["manual", "auto", "acceptEdits", "plan"].iter().enumerate() {
        let request_id = 3 + index as i64 * 2;
        if index > 0 {
            send(&mut child, json!({"jsonrpc":"2.0","id":request_id,"method":"session/set_config_option","params":{"sessionId":id,"configId":"mode","value":mode}})).await;
            let (set, _) = response(&mut reader, request_id).await;
            assert_eq!(set["result"]["configOptions"][1]["currentValue"], *mode);
        }
        let prompt_id = request_id + 1;
        send(&mut child, json!({"jsonrpc":"2.0","id":prompt_id,"method":"session/prompt","params":{"sessionId":id,"prompt":[{"type":"text","text":"say mode"}]}})).await;
        let (prompt, updates) = response(&mut reader, prompt_id).await;
        assert_eq!(prompt["result"]["stopReason"], "end_turn", "{prompt}");
        assert!(
            updates
                .iter()
                .any(|update| update["params"]["update"]["content"]["text"] == *mode),
            "missing {mode} in {updates:?}"
        );
    }
    child.kill().await.unwrap();
}

#[tokio::test]
async fn cancellation_stops_an_active_prompt() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"started"}]}}'
sleep 20
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
            "sessionId":id,"prompt":[{"type":"text","text":"wait"}]
        }}),
    )
    .await;
    loop {
        let value = receive(&mut reader).await;
        if value["params"]["update"]["content"]["text"] == "started" {
            break;
        }
    }
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":id}}),
    )
    .await;
    let (prompt, _) = response(&mut reader, 3).await;
    assert_eq!(prompt["result"]["stopReason"], "cancelled");
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
IFS= read -r initial || exit 10
case "$initial" in *'"type":"user"'*) ;; *) exit 11;; esac
case "$initial" in *'"run touch"'*) ;; *) exit 11;; esac
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
    let mut before = Vec::new();
    let permission = loop {
        let value = receive(&mut reader).await;
        if value["method"] == "session/request_permission" {
            break value;
        }
        before.push(value);
    };
    assert_eq!(
        before
            .iter()
            .filter(|value| value["params"]["update"]["sessionUpdate"] == "tool_call")
            .count(),
        1
    );
    assert_eq!(permission["params"]["toolCall"]["toolCallId"], "tool-1");
    assert_eq!(
        permission["params"]["toolCall"]["title"],
        "touch /tmp/claude-approval-test"
    );
    assert_eq!(
        permission["params"]["toolCall"]["rawInput"]["command"],
        "touch /tmp/claude-approval-test"
    );
    let content = permission["params"]["toolCall"]["content"][0]["content"]["text"]
        .as_str()
        .unwrap();
    assert!(content.contains("touch /tmp/claude-approval-test"));
    assert!(content.contains(project.to_str().unwrap()));
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":permission["id"],"result":{
            "outcome":{"outcome":"selected","optionId":"allow_once"}
        }}),
    )
    .await;
    let (prompt, updates) = response(&mut reader, 3).await;
    assert_eq!(prompt["result"]["stopReason"], "end_turn");
    assert!(
        updates
            .iter()
            .any(|v| v["params"]["update"]["content"]["text"] == "approved")
    );
    let statuses: Vec<&Value> = before
        .iter()
        .chain(&updates)
        .filter(|v| v["params"]["update"]["toolCallId"] == "tool-1")
        .map(|v| &v["params"]["update"]["status"])
        .filter(|status| !status.is_null())
        .collect();
    assert_eq!(statuses, ["pending", "in_progress", "completed"]);
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
IFS= read -r initial || exit 10
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
    let first = loop {
        let value = receive(&mut reader).await;
        if value["method"] == "session/request_permission" {
            break value;
        }
    };
    assert!(
        first["params"]["options"]
            .as_array()
            .unwrap()
            .iter()
            .any(|option| option["optionId"] == "allow_always")
    );
    send(&mut child, json!({"jsonrpc":"2.0","id":first["id"],"result":{"outcome":{"outcome":"selected","optionId":"allow_always"}}})).await;
    let second = loop {
        let value = receive(&mut reader).await;
        if value["method"] == "session/request_permission" {
            break value;
        }
    };
    assert!(
        !second["params"]["options"]
            .as_array()
            .unwrap()
            .iter()
            .any(|option| option["optionId"] == "allow_always")
    );
    send(&mut child, json!({"jsonrpc":"2.0","id":second["id"],"result":{"outcome":{"outcome":"selected","optionId":"reject_once"}}})).await;
    let (prompt, _) = response(&mut reader, 3).await;
    assert_eq!(prompt["result"]["stopReason"], "end_turn", "{prompt}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn cancellation_stops_a_pending_permission() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
IFS= read -r initial || exit 10
printf '%s\n' '{"type":"control_request","request_id":"permission-1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-1","input":{"command":"pwd"}}}'
IFS= read -r answer
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].as_str().unwrap().to_owned();
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":id,"prompt":[{"type":"text","text":"run pwd"}]}})).await;
    loop {
        if receive(&mut reader).await["method"] == "session/request_permission" {
            break;
        }
    }
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":id}}),
    )
    .await;
    let (prompt, _) = response(&mut reader, 3).await;
    assert_eq!(prompt["result"]["stopReason"], "cancelled");
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
    grep -q '"command":"node"' "$2" && grep -q '"server.js"' "$2" && grep -q '"TOKEN":"test"' "$2" && found=1
    break
  fi
  shift
done
if [ "$found" -ne 1 ]; then exit 12; fi
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"mcp ok"}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2","result":"mcp ok"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{
            "cwd":project,"mcpServers":[{"name":"filesystem","command":"node","args":["server.js"],
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
    let (prompt, updates) = response(&mut reader, 3).await;
    assert_eq!(prompt["result"]["stopReason"], "end_turn");
    assert!(
        updates
            .iter()
            .any(|v| v["params"]["update"]["content"]["text"] == "mcp ok")
    );
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
    let (prompt, updates) = response(&mut reader, 4).await;
    assert_eq!(prompt["result"]["stopReason"], "end_turn");
    assert!(
        updates
            .iter()
            .any(|v| v["params"]["update"]["content"]["text"] == "resumed")
    );
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
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}'
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
        let (_, updates) = response(&mut reader, new_id + 1).await;
        assert!(!updates.iter().any(|v| {
            v["params"]["update"]["content"]["text"]
                .as_str()
                .is_some_and(|s| s.starts_with("Error:"))
        }));
    }
    child.kill().await.unwrap();

    let (mut child, mut reader) = start(&bin, &store);
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/resume","params":{"sessionId":first_id,"cwd":project,"mcpServers":[]}})).await;
    let (resumed, _) = response(&mut reader, 2).await;
    assert!(resumed.get("result").is_some(), "{resumed}");
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":first_id,"prompt":[{"type":"text","text":"continue"}]}})).await;
    response(&mut reader, 3).await;
    let calls = fs::read_to_string(project.join("calls")).unwrap();
    let calls: Vec<_> = calls.lines().collect();
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
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
printf '%s\n' "$*" >> calls
case " $* " in
  *' --resume '*)
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"partial output"}]}}'
    printf '%s\n' '{"type":"result","is_error":true,"result":"resume failed"}'
    exit 1;;
esac
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}'
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].clone();
    for request_id in [3, 4] {
        send(&mut child, json!({"jsonrpc":"2.0","id":request_id,"method":"session/prompt","params":{"sessionId":id,"prompt":[{"type":"text","text":"hello"}]}})).await;
        let (_, updates) = response(&mut reader, request_id).await;
        if request_id == 4 {
            assert!(
                updates
                    .iter()
                    .any(|v| v["params"]["update"]["content"]["text"] == "Error: resume failed")
            );
        }
    }
    assert_eq!(
        fs::read_to_string(project.join("calls"))
            .unwrap()
            .lines()
            .count(),
        2
    );
    child.kill().await.unwrap();
}

#[tokio::test]
async fn timeout_stops_a_hung_claude_and_allows_another_prompt() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(&bin, "exec sleep 10");
    let (mut child, mut reader) = start_with_timeout(&bin, &temp.path().join("store"), Some(300));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].clone();
    for request_id in [3, 4] {
        send(&mut child, json!({"jsonrpc":"2.0","id":request_id,"method":"session/prompt","params":{"sessionId":id,"prompt":[{"type":"text","text":"hello"}]}})).await;
        let (result, updates) = response(&mut reader, request_id).await;
        assert!(result.get("result").is_some(), "{result}");
        assert!(
            updates
                .iter()
                .any(|v| v["params"]["update"]["content"]["text"] == "Error: claude timed out")
        );
    }
    child.kill().await.unwrap();
}

#[tokio::test]
async fn permission_wait_does_not_count_toward_the_timeout() {
    let temp = tempfile::tempdir().unwrap();
    let bin = temp.path().join("bin");
    let project = temp.path().join("project");
    fs::create_dir_all(&bin).unwrap();
    fs::create_dir_all(&project).unwrap();
    fake_claude(
        &bin,
        r#"
IFS= read -r initial || exit 10
printf '%s\n' '{"type":"control_request","request_id":"permission-1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-1","input":{"command":"pwd"}}}'
IFS= read -r answer
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}'
printf '%s\n' '{"type":"result","session_id":"79ed6f89-b693-4192-8169-4554404b43a2"}'
"#,
    );
    let (mut child, mut reader) = start_with_timeout(&bin, &temp.path().join("store"), Some(2000));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":project,"mcpServers":[]}})).await;
    let (new, _) = response(&mut reader, 2).await;
    let id = new["result"]["sessionId"].clone();
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":id,"prompt":[{"type":"text","text":"run pwd"}]}})).await;
    let permission = loop {
        let value = receive(&mut reader).await;
        if value["method"] == "session/request_permission" {
            break value;
        }
    };
    tokio::time::sleep(Duration::from_millis(2500)).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":permission["id"],"result":{"outcome":{"outcome":"selected","optionId":"allow_once"}}})).await;
    let (result, updates) = response(&mut reader, 3).await;
    assert_eq!(result["result"]["stopReason"], "end_turn", "{result}");
    assert!(
        updates
            .iter()
            .any(|v| v["params"]["update"]["content"]["text"] == "done")
    );
    assert!(!updates.iter().any(|v| {
        v["params"]["update"]["content"]["text"]
            .as_str()
            .is_some_and(|s| s.starts_with("Error:"))
    }));
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
    response(reader, request_id).await
}

/// Reads messages until one has `text` as its update content.
async fn until_text(reader: &mut BufReader<ChildStdout>, text: &str) -> Vec<Value> {
    let mut seen = Vec::new();
    loop {
        let value = receive(reader).await;
        let done = value["params"]["update"]["content"]["text"] == text;
        seen.push(value);
        if done {
            return seen;
        }
    }
}

fn error_text(updates: &[Value]) -> Option<String> {
    updates.iter().find_map(|v| {
        v["params"]["update"]["content"]["text"]
            .as_str()
            .filter(|s| s.starts_with("Error:"))
            .map(str::to_owned)
    })
}

/// A FIFO a fake claude opens with `exec 3>'<path>'` before starting other
/// processes, which inherit it. Reading it to its end waits for claude and
/// all of them to exit.
struct ExitWatch {
    path: std::path::PathBuf,
    done: Option<tokio::task::JoinHandle<()>>,
}

impl ExitWatch {
    fn new(dir: &Path) -> Self {
        let path = dir.join("hold");
        let status = std::process::Command::new("mkfifo")
            .arg(&path)
            .status()
            .unwrap();
        assert!(status.success());
        let reader = path.clone();
        // Opening blocks until a process opens the FIFO for writing; reading
        // ends once every process holding it has exited.
        let done = tokio::task::spawn_blocking(move || {
            let mut fifo = fs::File::open(reader).unwrap();
            let mut rest = Vec::new();
            std::io::Read::read_to_end(&mut fifo, &mut rest).unwrap();
        });
        Self {
            path,
            done: Some(done),
        }
    }

    async fn exited(mut self) {
        let done = self.done.take().unwrap();
        timeout(Duration::from_secs(5), done)
            .await
            .expect("a process started by claude is still running")
            .unwrap();
    }
}

impl Drop for ExitWatch {
    /// Ends the blocking reader if nothing ever opened the FIFO, so a failing
    /// test does not hang.
    fn drop(&mut self) {
        use std::os::unix::fs::OpenOptionsExt;
        let _ = fs::OpenOptions::new()
            .write(true)
            .custom_flags(libc::O_NONBLOCK)
            .open(&self.path);
    }
}

#[tokio::test]
async fn a_process_holding_stderr_does_not_delay_the_turn() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    let background = ExitWatch::new(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
exec 3>'{}'
IFS= read -r initial
sleep 30 >/dev/null &
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
"#,
            background.path.display()
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (result, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(result["result"]["stopReason"], "end_turn", "{result}");
    assert_eq!(error_text(&updates), None);
    background.exited().await;
    child.kill().await.unwrap();
}

#[tokio::test]
async fn a_lingering_process_does_not_hold_the_turn_open() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    let background = ExitWatch::new(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
exec 3>'{}'
IFS= read -r initial
sleep 30 >/dev/null &
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
exit 3
"#,
            background.path.display()
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (result, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(result["result"]["stopReason"], "end_turn", "{result}");
    assert_eq!(error_text(&updates), None);
    background.exited().await;
    child.kill().await.unwrap();
}

#[tokio::test]
async fn cancel_stops_processes_started_by_claude() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    let background = ExitWatch::new(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
exec 3>'{}'
IFS= read -r initial
sleep 30 >/dev/null 2>&1 &
printf '%s\n' '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"started"}}]}}}}'
sleep 30
"#,
            background.path.display()
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    until_text(&mut reader, "started").await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (result, _) = response(&mut reader, 3).await;
    assert_eq!(result["result"]["stopReason"], "cancelled", "{result}");
    background.exited().await;
    child.kill().await.unwrap();
}

#[tokio::test]
async fn cancel_sent_right_after_prompt_cancels_it() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        &format!(
            r#"
IFS= read -r initial
sleep 2
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
"#
        ),
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
    let (result, _) = response(&mut reader, 3).await;
    assert_eq!(result["result"]["stopReason"], "cancelled", "{result}");
    child.kill().await.unwrap();
}

fn interrupted_claude(bin: &Path) {
    fake_claude(
        bin,
        &format!(
            r#"
printf '%s\n' "$*" >> calls
if [ "$(wc -l < calls)" -gt 1 ]; then
  printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
  exit 0
fi
IFS= read -r initial
printf '%s\n' '{{"type":"system","subtype":"init","session_id":"{HISTORY_ID}"}}'
printf '%s\n' '{{"type":"assistant","message":{{"content":[{{"type":"text","text":"working"}}]}}}}'
exec sleep 30
"#
        ),
    );
}

async fn cancel_then_prompt_again(temp: &Path, bin: &Path, project: &Path) -> Vec<String> {
    let (mut child, mut reader) = start(bin, &temp.join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    until_text(&mut reader, "working").await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (result, _) = response(&mut reader, 3).await;
    assert_eq!(result["result"]["stopReason"], "cancelled", "{result}");
    let (result, _) = prompt(&mut child, &mut reader, 4, &session).await;
    assert_eq!(result["result"]["stopReason"], "end_turn", "{result}");
    child.kill().await.unwrap();
    fs::read_to_string(project.join("calls"))
        .unwrap()
        .lines()
        .map(str::to_owned)
        .collect()
}

#[tokio::test]
async fn cancelled_turn_keeps_a_resumable_conversation() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    interrupted_claude(&bin);
    write_transcript(temp.path(), &project, HISTORY_ID);
    let calls = cancel_then_prompt_again(temp.path(), &bin, &project).await;
    assert_eq!(calls.len(), 2);
    assert!(
        calls[1].contains(&format!("--resume {HISTORY_ID}")),
        "{calls:?}"
    );
}

#[tokio::test]
async fn cancelled_turn_without_a_transcript_starts_fresh() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    interrupted_claude(&bin);
    let calls = cancel_then_prompt_again(temp.path(), &bin, &project).await;
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
IFS= read -r initial
printf '%s\n' '{{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"{HISTORY_ID}","errors":["Reached maximum number of turns (6)"]}}'
exit 1
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (result, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(
        result["result"]["stopReason"], "max_turn_requests",
        "{result}"
    );
    assert_eq!(error_text(&updates), None);
    child.kill().await.unwrap();
}

#[tokio::test]
async fn error_results_report_their_errors() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    fake_claude(
        &bin,
        r#"
IFS= read -r initial
printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["first","second"]}'
exit 1
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (_, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(
        error_text(&updates).as_deref(),
        Some("Error: first; second")
    );
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
IFS= read -r initial
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

    send(&mut child, json!({"jsonrpc":"2.0","id":5,"method":"session/load","params":{"sessionId":session,"cwd":project,"mcpServers":[]}})).await;
    let (load, _) = response(&mut reader, 5).await;
    assert!(load.get("result").is_some(), "{load}");
    send(&mut child, json!({"jsonrpc":"2.0","id":6,"method":"session/load","params":{"sessionId":session,"cwd":temp.path(),"mcpServers":[]}})).await;
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

    send(&mut b, json!({"jsonrpc":"2.0","id":3,"method":"session/load","params":{"sessionId":s1,"cwd":project,"mcpServers":[]}})).await;
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
IFS= read -r initial
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"sleep 30"}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
exec sleep 30
"#,
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":session,"prompt":[{"type":"text","text":"hello"}]}})).await;
    until_text(&mut reader, "working").await;
    send(
        &mut child,
        json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}),
    )
    .await;
    let (result, updates) = response(&mut reader, 3).await;
    assert_eq!(result["result"]["stopReason"], "cancelled", "{result}");
    assert!(
        updates.iter().any(|v| {
            v["params"]["update"]["sessionUpdate"] == "tool_call_update"
                && v["params"]["update"]["toolCallId"] == "tool-1"
                && v["params"]["update"]["status"] == "failed"
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
IFS= read -r initial || exit 10
printf '%s\n' "$initial" > '{}'
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}","result":"done"}}'
"#,
            captured.display()
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    send(
        &mut child,
        json!({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}),
    )
    .await;
    let (init, _) = response(&mut reader, 1).await;
    let capabilities = &init["result"]["agentCapabilities"]["promptCapabilities"];
    assert_eq!(capabilities["image"], true);
    assert_eq!(capabilities["embeddedContext"], true);
    assert_ne!(capabilities["audio"], true);

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
    let (result, _) = response(&mut reader, 3).await;
    assert_eq!(result["result"]["stopReason"], "end_turn", "{result}");

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
    for (index, prompt) in prompts.into_iter().enumerate() {
        let id = 10 + index as i64;
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
    let background = ExitWatch::new(temp.path());
    // The background process keeps stderr open after claude exits.
    fake_claude(
        &bin,
        &format!(
            r#"
exec 3>'{}'
IFS= read -r initial
echo "unknown option --bogus" >&2
sleep 30 >/dev/null &
exit 1
"#,
            background.path.display()
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    let session = new_session(&mut child, &mut reader, 2, &project).await;
    let (result, updates) = prompt(&mut child, &mut reader, 3, &session).await;
    assert_eq!(result["result"]["stopReason"], "end_turn", "{result}");
    let error = error_text(&updates).unwrap();
    assert!(error.ends_with(": unknown option --bogus"), "{error}");
    background.exited().await;
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
        json!({"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "toolu_2", "name": "Read", "input": {"file_path": "/x"}}]}}),
    ];
    let lines: Vec<String> = entries.iter().map(Value::to_string).collect();
    fs::write(
        transcripts.join(format!("{id}.jsonl")),
        lines.join("\n") + "\n",
    )
    .unwrap();
}

/// One line per replayed update: its kind and what it shows.
fn describe_updates(notifications: &[Value]) -> Vec<String> {
    notifications
        .iter()
        .filter(|n| n["method"] == "session/update")
        .map(|n| {
            let update = &n["params"]["update"];
            let kind = update["sessionUpdate"].as_str().unwrap();
            let detail = match kind {
                "tool_call" => format!(
                    "{} {} {}",
                    update["toolCallId"],
                    update["title"],
                    // ACP omits the default status, `pending`.
                    update["status"].as_str().unwrap_or("pending")
                ),
                "tool_call_update" => format!("{} {}", update["toolCallId"], update["status"]),
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

const REPLAY: [&str; 9] = [
    "user_message_chunk: Fix the build",
    "agent_thought_chunk: Checking",
    "agent_message_chunk: Running it",
    "tool_call: toolu_1 cargo build pending",
    "tool_call_update: toolu_1 completed",
    "user_message_chunk: Look",
    "user_message_chunk: image",
    "tool_call: toolu_2 Read /x pending",
    "tool_call_update: toolu_2 failed",
];

#[tokio::test]
async fn load_replays_a_claude_session_from_its_transcript() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    write_conversation(temp.path(), &project, HISTORY_ID);
    fake_claude(
        &bin,
        &format!(
            r#"
printf '%s\n' "$*" > args
IFS= read -r initial
printf '%s\n' '{{"type":"result","session_id":"{HISTORY_ID}"}}'
"#
        ),
    );
    let (mut child, mut reader) = start(&bin, &temp.path().join("store"));
    initialize(&mut child, &mut reader).await;
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":HISTORY_ID,"cwd":project,"mcpServers":[]}})).await;
    let (loaded, notifications) = response(&mut reader, 2).await;
    assert!(loaded["result"].is_object(), "{loaded}");
    assert_eq!(describe_updates(&notifications), REPLAY);

    let (result, _) = prompt(&mut child, &mut reader, 3, &json!(HISTORY_ID)).await;
    assert_eq!(result["result"]["stopReason"], "end_turn", "{result}");
    let args = fs::read_to_string(project.join("args")).unwrap();
    assert!(args.contains(&format!("--resume {HISTORY_ID}")), "{args}");
    child.kill().await.unwrap();
}

#[tokio::test]
async fn load_replays_a_bridge_session_from_its_claude_transcript() {
    let temp = tempfile::tempdir().unwrap();
    let (bin, project) = dirs(temp.path());
    write_conversation(temp.path(), &project, HISTORY_ID);
    fake_claude(
        &bin,
        &format!(
            r#"
IFS= read -r initial
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
    send(&mut child, json!({"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":session,"cwd":project,"mcpServers":[]}})).await;
    let (loaded, notifications) = response(&mut reader, 2).await;
    assert!(loaded["result"].is_object(), "{loaded}");
    assert!(
        notifications
            .iter()
            .all(|n| n["params"]["sessionId"] == session),
        "{notifications:?}"
    );
    assert_eq!(describe_updates(&notifications), REPLAY);
    child.kill().await.unwrap();
}
