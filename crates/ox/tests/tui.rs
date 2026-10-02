use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

use ox_server::fixture::{
    DEFAULT_MODEL, Reply, Server, catalog_reply, delta, echo_reply, shell_reply, sse, text_reply,
    usage,
};
use serde_json::json;

struct Tmux {
    root: tempfile::TempDir,
    socket: PathBuf,
    workspace: PathBuf,
    _runtime: tokio::runtime::Runtime,
    _openrouter: Server,
}

fn quote(text: &str) -> String {
    format!("'{}'", text.replace('\'', "'\\''"))
}

impl Tmux {
    fn new(replies: Vec<Reply>) -> Self {
        Self::with_skill(replies, false)
    }

    fn with_skill(mut replies: Vec<Reply>, skill: bool) -> Self {
        let root = tempfile::tempdir().unwrap();
        let socket = root.path().join("tmux.sock");
        let workspace = root.path().join("workspace");
        std::fs::create_dir(&workspace).unwrap();
        let config = root.path().join(".config/ox");
        std::fs::create_dir_all(&config).unwrap();
        std::fs::write(
            config.join("settings.json"),
            serde_json::to_vec(&serde_json::json!({"model": DEFAULT_MODEL})).unwrap(),
        )
        .unwrap();
        if skill {
            let skill = config.join("skills/tally");
            std::fs::create_dir_all(&skill).unwrap();
            std::fs::write(
                skill.join("SKILL.md"),
                "---\nname: tally\ndescription: Count tallies.\n---\nCount them.\n",
            )
            .unwrap();
        }
        replies.insert(0, catalog_reply());
        let runtime = tokio::runtime::Runtime::new().unwrap();
        let openrouter = runtime.block_on(Server::start(replies));
        let endpoint = openrouter.endpoint().to_owned();
        let test = Self {
            root,
            socket,
            workspace,
            _runtime: runtime,
            _openrouter: openrouter,
        };
        let script = test.root.path().join("shell.sh");
        std::fs::write(&script, format!(
            "before=$(stty -g)\n{} --dir {}\nresult=$?\n[ \"$(stty -g)\" = \"$before\" ] && echo TERMINAL_RESTORED\necho EXIT_$result\nexec /bin/sh\n",
            quote(env!("CARGO_BIN_EXE_ox")),
            quote(test.workspace.to_str().unwrap()),
        )).unwrap();
        let command = format!(
            "env HOME={} XDG_STATE_HOME={} OX_DATA_DIR={} OPENROUTER_API_KEY=test-key OX_OPENROUTER_ENDPOINT={} /bin/sh {}",
            quote(test.root.path().to_str().unwrap()),
            quote(test.root.path().join("state").to_str().unwrap()),
            quote(test.root.path().join("data").to_str().unwrap()),
            quote(&endpoint),
            quote(script.to_str().unwrap())
        );
        let conf = test.root.path().join("tmux.conf");
        std::fs::write(
            &conf,
            "set -g focus-events on\nset -g extended-keys always\nset -g extended-keys-format csi-u\n",
        )
        .unwrap();
        test.call(&[
            "-f",
            conf.to_str().unwrap(),
            "new-session",
            "-d",
            "-s",
            "test",
            "-x",
            "80",
            "-y",
            "24",
            &command,
        ]);
        test.wait("0% • $0.00");
        test
    }

    fn kill_server(&self) {
        let pane = self
            .call(&["display-message", "-p", "-t", "test:0.0", "#{pane_pid}"])
            .trim()
            .to_owned();
        let child = |pid: &str| {
            let output = Command::new("pgrep").args(["-P", pid]).output().unwrap();
            assert!(output.status.success(), "no child process of {pid}");
            String::from_utf8(output.stdout)
                .unwrap()
                .lines()
                .next()
                .unwrap()
                .to_owned()
        };
        let client = child(&pane);
        let server = child(&client);
        assert!(Command::new("kill").arg(server).status().unwrap().success());
    }

    fn command(&self) -> Command {
        let mut cmd = Command::new("tmux");
        cmd.args(["-S", self.socket.to_str().unwrap()])
            .env("HOME", self.root.path())
            .env_remove("NO_COLOR")
            .env_remove("TMUX");
        cmd
    }

    fn call(&self, args: &[&str]) -> String {
        let output = self
            .command()
            .args(args)
            .output()
            .expect("tmux is required for make e2e");
        assert!(
            output.status.success(),
            "tmux {args:?}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        String::from_utf8(output.stdout).unwrap()
    }

    /// The pane's current screen, one line per row.
    fn screen(&self) -> String {
        self.call(&["capture-pane", "-p", "-t", "test:0.0"])
    }

    /// The pane's current screen with its ANSI attributes.
    fn styled_screen(&self) -> String {
        self.call(&["capture-pane", "-p", "-e", "-t", "test:0.0"])
    }

    fn wait(&self, text: &str) {
        self.wait_for(text, true);
    }

    fn wait_gone(&self, text: &str) {
        self.wait_for(text, false);
    }

    fn wait_for(&self, text: &str, present: bool) {
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            let screen = self.screen();
            if screen.contains(text) == present {
                return;
            }
            assert!(
                Instant::now() < deadline,
                "{} {text:?}:\n{screen}",
                if present { "missing" } else { "still showing" }
            );
            std::thread::sleep(Duration::from_millis(30));
        }
    }

    fn wait_title(&self, title: &str) {
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            let current = self.call(&["display-message", "-p", "-t", "test:0.0", "#{pane_title}"]);
            if current.trim_end() == title {
                return;
            }
            assert!(
                Instant::now() < deadline,
                "title {current:?} is not {title:?}"
            );
            std::thread::sleep(Duration::from_millis(30));
        }
    }

    fn keys(&self, keys: &[&str]) {
        let mut args = vec!["send-keys", "-t", "test:0.0"];
        args.extend_from_slice(keys);
        self.call(&args);
    }

    fn type_text(&self, text: &str) {
        self.call(&["send-keys", "-t", "test:0.0", "-l", text]);
    }

    fn prompt(&self, text: &str) {
        self.type_text(text);
        self.keys(&["Enter"]);
    }

    fn attach(&self) -> Child {
        let child = self
            .command()
            .args(["-C", "attach-session", "-t", "test"])
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let deadline = Instant::now() + Duration::from_secs(5);
        while self.call(&["list-clients"]).trim().is_empty() {
            assert!(Instant::now() < deadline, "tmux client did not attach");
            std::thread::sleep(Duration::from_millis(30));
        }
        child
    }

    fn detach(&self, child: &mut Child) {
        self.call(&["detach-client", "-s", "test"]);
        let deadline = Instant::now() + Duration::from_secs(5);
        while child.try_wait().unwrap().is_none() {
            assert!(Instant::now() < deadline, "tmux client did not detach");
            std::thread::sleep(Duration::from_millis(30));
        }
    }
}

impl Drop for Tmux {
    fn drop(&mut self) {
        let _ = self.command().arg("kill-server").output();
    }
}

const APPROVAL: &str = "Would you like to run the following command?";

fn hang(text: &str) -> Reply {
    Reply::Hang(format!(
        "data: {}\n\n",
        delta(json!({"role":"assistant", "content":text}), None)
    ))
}

fn streamed(parts: &[&str]) -> Reply {
    let last = parts.len() - 1;
    Reply::Stream(sse(&parts
        .iter()
        .enumerate()
        .map(|(index, text)| {
            delta(
                json!({"role":"assistant", "content":text}),
                (index == last).then_some("stop"),
            )
        })
        .collect::<Vec<_>>()))
}

fn render_replies() -> Vec<Reply> {
    let calls = vec![
        json!({
            "index":0, "id":"run-1", "type":"function",
            "function":{"name":"shell", "arguments":json!({"command":"ls"}).to_string()}
        }),
        json!({
            "index":1, "id":"read-1", "type":"function",
            "function":{"name":"read_file", "arguments":json!({"path":"tallies/2026/september/archive/a.tally"}).to_string()}
        }),
        json!({
            "index":2, "id":"run-2", "type":"function",
            "function":{"name":"shell", "arguments":json!({"command":"printf 'a.tally\\nb.tally\\n'", "background":true}).to_string()}
        }),
        json!({
            "index":3, "id":"patch-1", "type":"function",
            "function":{"name":"apply_patch", "arguments":json!({"patch":"*** Begin Patch\n*** Update File: a.tally\n@@\n-one\n+two\n*** End Patch"}).to_string()}
        }),
    ];
    vec![
        Reply::Stream(sse(&[
            delta(
                json!({"role":"assistant", "reasoning":"weighing the tallies"}),
                None,
            ),
            delta(
                json!({"role":"assistant", "tool_calls":calls}),
                Some("tool_calls"),
            ),
        ])),
        text_reply("Two tallies were counted in the workspace:\n\na.tally and b.tally"),
    ]
}

fn create_tallies(test: &Tmux) {
    std::fs::write(test.workspace.join("a.tally"), "one\n").unwrap();
    std::fs::write(test.workspace.join("b.tally"), "two\n").unwrap();
    let archive = test.workspace.join("tallies/2026/september/archive");
    std::fs::create_dir_all(&archive).unwrap();
    std::fs::write(archive.join("a.tally"), "one\ntwo\n").unwrap();
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn resume_picker_shows_saved_session_and_replays_on_enter() {
    let test = Tmux::new(vec![echo_reply(), echo_reply()]);
    test.prompt("original transcript");
    test.wait("you said: original transcript");
    test.prompt("/resume");
    test.wait("Search");
    test.wait("original transcript");
    test.wait(&chrono::Utc::now().format("%Y-%m-%d").to_string());
    test.keys(&["Escape"]);
    test.wait_gone("Search");
    test.wait("you said: original transcript");
    test.prompt("/resume");
    test.wait("Search");
    test.keys(&["Enter"]);
    test.wait_gone("Search");
    test.wait("you said: original transcript");
    assert!(test.screen().contains("❯ original transcript"));
    test.prompt("after resume");
    test.wait("you said: after resume");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn model_picker_shows_providers_and_prices_and_changes_the_model() {
    let test = Tmux::new(vec![]);
    test.prompt("/model");
    test.wait("Search");
    let screen = test.screen();
    assert!(
        screen.contains("DeepSeek V4.1 Flash                  OpenRouter  $0.03  $0.60  1,048,576"),
        "{screen}"
    );
    assert!(
        screen.contains("GLM 5.3 Flash                        OpenRouter  $0.04  $0.14  1,310,720"),
        "{screen}"
    );
    assert!(!screen.contains("0% • $0.00"), "{screen}");
    assert!(!screen.contains("Favorites / All"), "{screen}");
    test.keys(&["Down", "C-f"]);
    test.wait("Favorites / All");
    let screen = test.screen();
    assert!(
        screen.contains(&format!("    {:<57}Favorites / All\n", "Search")),
        "{screen}"
    );
    let config = test.root.path().join(".config/ox/settings.json");
    let config: serde_json::Value =
        serde_json::from_slice(&std::fs::read(config).unwrap()).unwrap();
    assert_eq!(config["model"], DEFAULT_MODEL);
    assert_eq!(
        config["favorites"],
        serde_json::json!(["openrouter:z-ai/glm-5.3-flash"])
    );
    let styled = test.styled_screen();
    assert!(
        styled.contains("\x1b[38;2;112;112;112mFavorites / \x1b[38;2;255;255;255mAll"),
        "{styled}"
    );
    assert!(styled.contains("\x1b[1m"), "{styled}");
    test.keys(&["Left"]);
    test.wait_gone("DeepSeek V4.1 Flash");
    let styled = test.styled_screen();
    assert!(
        styled.contains("\x1b[38;2;255;255;255mFavorites\x1b[38;2;112;112;112m / All"),
        "{styled}"
    );
    assert!(styled.contains("GLM 5.3 Flash"), "{styled}");
    assert!(!styled.contains("\x1b[1m"), "{styled}");
    test.keys(&["Enter"]);
    test.wait_gone("Search");
    test.wait("Ask • GLM 5.3 Flash");
    test.prompt("/model");
    test.wait("Search");
    let screen = test.screen();
    assert!(screen.contains("GLM 5.3 Flash"), "{screen}");
    assert!(
        !screen.contains("DeepSeek V4.1 Flash"),
        "the picker opens on Favorites:\n{screen}"
    );
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn resume_during_prompt_cancels_before_showing_the_picker() {
    let test = Tmux::new(vec![hang("running; waiting for cancellation")]);
    test.prompt("running");
    test.wait("running; waiting for cancellation");
    test.prompt("/resume");
    test.wait("Search");
    test.keys(&["Escape"]);
    assert!(!test.screen().contains("you said: /resume"));
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn terminal_keys_send_interrupt_approve_scroll_and_restore_the_shell() {
    let test = Tmux::new(vec![
        streamed(&["stream ", "arrives ", "in order\n"]),
        shell_reply(&[("printf denied", 10)]),
        text_reply("selected deny"),
        shell_reply(&[("printf approved", 10)]),
        text_reply("selected approve"),
        hang("running one; waiting for cancellation"),
        hang("running two; waiting for cancellation"),
        echo_reply(),
        echo_reply(),
        echo_reply(),
        hang("running three; waiting for cancellation"),
    ]);
    test.prompt("stream");
    test.wait("stream arrives in order");
    test.prompt("tool");
    test.wait(APPROVAL);
    let screen = test.screen();
    for text in [
        APPROVAL,
        "● Shell printf denied",
        "Working directory:",
        "Command:",
        "printf denied",
        "› 1. Yes",
        "2. No",
    ] {
        assert!(screen.contains(text), "missing {text:?}:\n{screen}");
    }
    test.keys(&["Down", "Enter"]);
    test.wait("selected deny");
    test.wait_gone(APPROVAL);
    test.prompt("tool");
    test.wait("› 1. Yes");
    test.keys(&["Enter"]);
    test.wait("selected approve");
    test.prompt("running");
    test.wait("running one; waiting for cancellation");
    test.keys(&["Escape"]);
    test.prompt("running");
    test.wait("running two; waiting for cancellation");
    test.prompt("interrupting");
    test.wait("you said: interrupting");
    let screen = test.screen();
    assert!(
        screen.contains("❯ interrupting\n\n  ● you said: interrupting"),
        "{screen}"
    );
    test.type_text("first");
    test.keys(&["S-Enter"]);
    test.type_text("second");
    test.wait("  ❯ first\n    second\n");
    test.keys(&["Enter"]);
    test.wait("  ● you said: first\n    second\n");
    test.call(&["set-buffer", "pasted界\nthird line"]);
    test.call(&["paste-buffer", "-p", "-t", "test:0.0"]);
    test.wait("  ❯ pasted界\n    third line\n");
    assert!(!test.screen().contains("you said: pasted"));
    test.keys(&["Enter"]);
    test.wait("  ● you said: pasted界\n    third line\n");
    test.prompt("running");
    test.wait("running three; waiting for cancellation");
    test.keys(&["PageUp"]);
    test.wait_gone("running three; waiting for cancellation");
    test.keys(&["End"]);
    test.wait("running three; waiting for cancellation");
    test.keys(&["Escape"]);
    test.keys(&["C-d"]);
    test.wait("TERMINAL_RESTORED");
    test.wait("EXIT_0");
    test.prompt("echo SHELL_USABLE");
    test.wait("\nSHELL_USABLE\n");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn mouse_wheel_scrolls_the_transcript_one_line_per_event() {
    let test = Tmux::new(render_replies());
    create_tallies(&test);
    test.keys(&["Tab"]);
    test.wait("Auto");
    test.call(&["resize-window", "-t", "test:0", "-x", "80", "-y", "12"]);
    test.prompt("render");
    test.wait("a.tally and b.tally");
    let before = test.screen();
    // SGR mouse wheel up at column 5, row 3.
    test.call(&[
        "send-keys",
        "-t",
        "test:0.0",
        "-H",
        "1b",
        "5b",
        "3c",
        "36",
        "34",
        "3b",
        "35",
        "3b",
        "33",
        "4d",
    ]);
    test.wait_gone("a.tally and b.tally");
    let after = test.screen();
    assert_eq!(before.lines().nth(1), after.lines().nth(2), "{after}");
    // SGR mouse wheel down at the same position.
    test.call(&[
        "send-keys",
        "-t",
        "test:0.0",
        "-H",
        "1b",
        "5b",
        "3c",
        "36",
        "35",
        "3b",
        "35",
        "3b",
        "33",
        "4d",
    ]);
    test.wait("a.tally and b.tally");
    assert_eq!(test.screen(), before);
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn the_transcript_view_renders_thinking_tools_and_wrapped_replies() {
    let test = Tmux::new(render_replies());
    create_tallies(&test);
    test.keys(&["Tab"]);
    test.wait("Auto");
    test.call(&["resize-window", "-t", "test:0", "-x", "40", "-y", "80"]);
    test.prompt("render");
    test.wait("a.tally and b.tally");
    let screen = test.screen();
    let mut position = 0;
    for text in [
        "❯ render",
        "● Thought for 0s",
        "● Shell ls",
        "● Read tallies/2026/september/archi…",
        "● Shell printf 'a.tally\\nb.tally\\n'…",
        "● Apply patch to a.tally",
        "● Two tallies were counted in the",
        "a.tally and b.tally",
    ] {
        let found = screen[position..]
            .find(text)
            .unwrap_or_else(|| panic!("missing {text:?}:\n{screen}"));
        position += found + text.len();
    }
    let styled = test.styled_screen();
    let gray = |text: &str| styled.contains(&format!("\x1b[38;2;112;112;112m{text}"));
    assert!(gray("● Thought for 0s"), "{styled}");
    assert!(!gray("● Two tallies"), "{styled}");
    test.keys(&["C-o"]);
    test.wait("Lines 1–2 of 2");
    let screen = test.screen();
    for text in [
        "● Shell ls\n    └ Exit code: 0",
        "      a.tally\n      b.tally\n      tallies",
        "● Read tallies/2026/september/archi…\n    └ Lines 1–2 of 2",
        "● Apply patch to a.tally\n    └ Modified a.tally",
        "      @@ -1 +1 @@\n      -one\n      +two",
    ] {
        assert!(screen.contains(text), "missing {text:?}:\n{screen}");
    }
    let styled = test.styled_screen();
    assert!(
        styled.contains("\x1b[38;2;152;195;121m    +two"),
        "{styled}"
    );
    test.keys(&["C-o", "C-o"]);
    test.wait_gone("Lines 1–2 of 2");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn the_status_line_shows_the_session_settings_and_usage() {
    let reply = Reply::Stream(sse(&[
        delta(
            json!({"role":"assistant", "content":"usage recorded"}),
            Some("stop"),
        ),
        usage(157_286, 1, 0.25),
    ]));
    let test = Tmux::new(vec![reply]);
    test.keys(&["Tab", "C-e", "C-e", "C-e"]);
    test.wait("Auto • DeepSeek V4.1 Flash • High");
    test.prompt("usage");
    test.wait("15% • $0.25");
    let screen = test.screen();
    let lines: Vec<&str> = screen.lines().collect();
    let [.., status, last] = lines[..] else {
        panic!("{screen}")
    };
    assert!(
        status.starts_with("  Auto • DeepSeek V4.1 Flash • High"),
        "{screen}"
    );
    assert!(status.ends_with("15% • $0.25"), "{screen}");
    assert_eq!(last.trim(), "", "{screen}");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn tab_and_shift_tab_cycle_modes_in_the_terminal() {
    let test = Tmux::new(vec![]);
    test.wait("Ask");
    test.keys(&["Tab"]);
    test.wait("Auto");
    test.keys(&["S-Tab"]);
    test.wait("Ask");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn control_e_cycles_effort_in_the_terminal() {
    let test = Tmux::new(vec![]);
    test.wait("DeepSeek V4.1 Flash • Default");
    test.keys(&["C-e"]);
    test.wait("DeepSeek V4.1 Flash • Low");
    test.keys(&["C-e"]);
    test.wait("DeepSeek V4.1 Flash • Medium");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn tab_completes_a_slash_command_from_ghost_text() {
    let test = Tmux::with_skill(vec![echo_reply()], true);
    test.wait("Ask");
    test.keys(&["/", "t", "a"]);
    test.wait("/tally");
    test.keys(&["Tab"]);
    test.keys(&["Enter"]);
    test.wait("you said: Skill /tally invoked.");
    assert!(test.screen().contains("Ask"));
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn permission_survives_disconnect_and_server_failure_restores_the_shell() {
    let test = Tmux::new(vec![
        echo_reply(),
        shell_reply(&[("printf pending", 10)]),
        text_reply("selected deny"),
        echo_reply(),
    ]);
    let mut client = test.attach();
    test.prompt("before detach");
    test.wait("you said: before detach");
    test.prompt("run a command");
    test.wait(APPROVAL);
    test.call(&["split-window", "-h", "-t", "test:0.0", "/bin/sh"]);
    test.call(&[
        "send-keys",
        "-t",
        "test:0.1",
        "echo ADJACENT_SHELL",
        "Enter",
    ]);
    test.detach(&mut client);
    assert!(test.call(&["list-clients"]).trim().is_empty());
    let mut client = test.attach();
    test.keys(&["Down", "Enter"]);
    test.wait("selected deny");
    test.prompt("after reconnect");
    test.wait("you said: after reconnect");
    assert!(test.screen().contains("you said: before detach"));
    assert!(
        test.call(&["capture-pane", "-p", "-t", "test:0.1"])
            .contains("\nADJACENT_SHELL\n")
    );
    test.kill_server();
    test.wait("TERMINAL_RESTORED");
    test.wait("EXIT_1");
    test.prompt("echo SHELL_AFTER_FAILURE");
    test.wait("\nSHELL_AFTER_FAILURE\n");
    test.detach(&mut client);
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn pane_title_shows_status_and_keeps_unseen_results_until_focus() {
    let test = Tmux::new(vec![
        hang("running"),
        shell_reply(&[("printf denied", 10)]),
        text_reply("denied"),
        Reply::Status(400, "{}".into()),
        streamed(&["stream ", "arrives ", "in order"]),
    ]);
    let mut client = test.attach();
    test.call(&["split-window", "-h", "-t", "test:0.0", "/bin/sh"]);
    test.wait_title("ox: ready");
    test.prompt("running");
    test.wait_title("ox: working");
    test.keys(&["Escape"]);
    test.wait_title("ox: finished");
    test.prompt("run a command");
    test.wait_title("ox: needs permission");
    test.keys(&["Down", "Enter"]);
    test.wait_title("ox: finished");
    test.prompt("fail");
    test.wait_title("ox: turn error");
    test.call(&["select-pane", "-t", "test:0.0"]);
    test.wait_title("ox: ready");
    test.prompt("stream");
    test.wait("stream arrives in order");
    test.wait_title("ox: ready");
    test.keys(&["C-d"]);
    test.wait("EXIT_0");
    test.wait_title("");
    test.detach(&mut client);
}

#[test]
fn invalid_startup_does_not_launch_a_server_or_change_terminal_mode() {
    let root = tempfile::tempdir().unwrap();
    let config = root.path().join(".config/ox");
    std::fs::create_dir_all(&config).unwrap();
    let marker = root.path().join("launched");
    std::fs::write(config.join("settings.json"), serde_json::to_vec(&serde_json::json!({
        "servers": [{"name": "Fake", "command": "/bin/sh", "args": ["-c", format!("touch {}", quote(marker.to_str().unwrap()))]}]
    })).unwrap()).unwrap();
    for args in [
        vec!["--server", "missing"],
        vec!["--dir", "/nonexistent-ox-test-directory"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_ox"))
            .env("HOME", root.path())
            .args(args)
            .output()
            .unwrap();
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
        assert!(!output.stderr.contains(&0x1b));
        assert!(!marker.exists());
    }
}
