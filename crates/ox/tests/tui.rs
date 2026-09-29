use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

struct Tmux {
    root: tempfile::TempDir,
    socket: PathBuf,
}

fn quote(text: &str) -> String {
    format!("'{}'", text.replace('\'', "'\\''"))
}

impl Tmux {
    fn new() -> Self {
        let root = tempfile::tempdir().unwrap();
        let socket = root.path().join("tmux.sock");
        let test = Self { root, socket };
        let config = test.root.path().join("config/ox");
        std::fs::create_dir_all(&config).unwrap();
        let fake = PathBuf::from(env!("CARGO_BIN_EXE_ox")).with_file_name("ox-fake-server");
        assert!(fake.exists(), "run make e2e to build the fake server");
        std::fs::write(
            config.join("config.json"),
            serde_json::to_vec(&serde_json::json!({
                "servers": [{"name": "Fake", "command": fake, "args": []}]
            }))
            .unwrap(),
        )
        .unwrap();
        let script = test.root.path().join("shell.sh");
        std::fs::write(&script, format!(
            "before=$(stty -g)\n{}\nresult=$?\n[ \"$(stty -g)\" = \"$before\" ] && echo TERMINAL_RESTORED\necho EXIT_$result\nexec /bin/sh\n",
            quote(env!("CARGO_BIN_EXE_ox")),
        )).unwrap();
        let command = format!(
            "env HOME={} XDG_CONFIG_HOME={} XDG_STATE_HOME={} /bin/sh {}",
            quote(test.root.path().to_str().unwrap()),
            quote(test.root.path().join("config").to_str().unwrap()),
            quote(test.root.path().join("state").to_str().unwrap()),
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

const APPROVAL: &str = "Would you like to allow the following?";

#[test]
#[ignore = "requires tmux; run make e2e"]
fn resume_picker_shows_saved_session_and_replays_on_enter() {
    let test = Tmux::new();
    test.prompt("title");
    test.prompt("original transcript");
    test.wait("you said: original transcript");
    test.prompt("/resume");
    test.wait("Search");
    test.wait("tallies");
    test.wait("2026-09-01");
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
fn model_picker_shows_prices_and_changes_the_model() {
    let test = Tmux::new();
    test.prompt("/model");
    test.wait("Search");
    let screen = test.screen();
    assert!(
        screen.contains(&format!(
            "    {:<51}$0.28  $0.42  131,072\n    {:<51}$0.04  $0.08   32,768\n",
            "DeepSeek: DeepSeek Reasoner", "Google: Gemma Vision"
        )),
        "{screen}"
    );
    assert!(!screen.contains("0% • $0.00"), "{screen}");
    test.keys(&["Down", "Enter"]);
    test.wait_gone("Search");
    test.wait("Ask • Google: Gemma Vision");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn resume_during_prompt_cancels_before_showing_the_picker() {
    let test = Tmux::new();
    test.prompt("running");
    test.wait("running; waiting for cancellation");
    test.prompt("/resume");
    test.wait("Search");
    test.keys(&["Escape"]);
    test.wait("cancelled");
    assert!(!test.screen().contains("you said: /resume"));
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn terminal_keys_send_interrupt_approve_scroll_and_restore_the_shell() {
    let test = Tmux::new();
    test.prompt("stream");
    test.wait("stream arrives in order");
    test.prompt("tool");
    test.wait(APPROVAL);
    let screen = test.screen();
    assert!(
        screen.contains(concat!(
            "  Would you like to allow the following?\n",
            "\n",
            "  ● count the tallies\n",
            "    every *.tally file\n",
            "\n",
            "  › 1. Go ahead\n",
            "    2. Hold off\n",
        )),
        "{screen}"
    );
    test.keys(&["Down", "Enter"]);
    test.wait("selected stop");
    test.wait_gone(APPROVAL);
    test.prompt("tool");
    test.wait("› 1. Go ahead");
    test.keys(&["Enter"]);
    test.wait("selected go");
    test.prompt("running");
    test.wait("running; waiting for cancellation");
    test.keys(&["Escape"]);
    test.wait("cancelled");
    test.prompt("running");
    test.wait("running; waiting for cancellation");
    test.prompt("interrupting");
    test.wait("you said: interrupting");
    let screen = test.screen();
    assert!(
        screen.contains("cancelled\n\n  ❯ interrupting\n\n  ● you said: interrupting"),
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
    test.wait("running; waiting for cancellation");
    test.keys(&["PageUp"]);
    test.wait_gone("running; waiting for cancellation\n");
    test.keys(&["Escape"]);
    test.wait("new activity");
    test.keys(&["End"]);
    test.wait_gone("new activity");
    test.wait("cancelled");
    test.keys(&["C-d"]);
    test.wait("TERMINAL_RESTORED");
    test.wait("EXIT_0");
    test.prompt("echo SHELL_USABLE");
    test.wait("\nSHELL_USABLE\n");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn the_transcript_view_renders_thinking_tools_and_wrapped_replies() {
    let test = Tmux::new();
    test.call(&["resize-window", "-t", "test:0", "-x", "40", "-y", "40"]);
    test.prompt("render");
    test.wait("a.tally and b.tally");
    let screen = test.screen();
    assert!(
        screen.starts_with(concat!(
            "\n",
            "  ❯ render\n",
            "\n",
            "  ● Thought for 0s\n",
            "\n",
            "  ● Shell ls\n",
            "\n",
            "  ● Read tallies/2026/september/archi…\n",
            "\n",
            "  ● Shell npm run dev &\n",
            "\n",
            "  ● Apply patch to a.tally\n",
            "\n",
            "  ● Final answer from subagent child-1\n",
            "    └ Fixed.\n",
            "\n",
            "  ● Two tallies were counted in the\n",
            "    workspace:\n",
            "\n",
            "    a.tally and b.tally\n",
        )),
        "{screen}"
    );
    let styled = test.styled_screen();
    let gray = |text: &str| styled.contains(&format!("\x1b[38;2;112;112;112m{text}"));
    assert!(gray("● Thought for 0s"), "{styled}");
    assert!(gray("  └ Fixed."), "{styled}");
    assert!(!gray("● Two tallies"), "{styled}");
    test.keys(&["C-o"]);
    test.wait("Lines 1–2 of 2");
    let screen = test.screen();
    assert!(
        screen.contains(concat!(
            "  ● Shell ls\n",
            "    └ a.tally\n",
            "      b.tally\n",
            "\n",
            "  ● Read tallies/2026/september/archi…\n",
            "    └ Lines 1–2 of 2\n",
            "\n",
            "  ● Shell npm run dev &\n",
            "    └ a.tally\n",
            "      b.tally\n",
            "\n",
            "  ● Apply patch to a.tally\n",
            "    └ Modified a.tally\n",
            "      @@ -1 +1 @@\n",
            "      -one\n",
            "      +two\n",
            "\n",
            "  ● Final answer from subagent child-1\n",
            "    └ Fixed.\n",
        )),
        "{screen}"
    );
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
    let test = Tmux::new();
    test.prompt("options");
    test.wait("Auto • DeepSeek: DeepSeek Reasoner • High");
    test.prompt("usage");
    test.wait("15% • $0.25");
    let screen = test.screen();
    let lines: Vec<&str> = screen.lines().collect();
    let [.., status, last] = lines[..] else {
        panic!("{screen}")
    };
    assert!(
        status.starts_with("  Auto • DeepSeek: DeepSeek Reasoner • High"),
        "{screen}"
    );
    assert!(status.ends_with("15% • $0.25"), "{screen}");
    assert_eq!(last.trim(), "", "{screen}");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn tab_and_shift_tab_cycle_modes_in_the_terminal() {
    let test = Tmux::new();
    test.wait("Ask");
    test.keys(&["Tab"]);
    test.wait("Auto");
    test.keys(&["S-Tab"]);
    test.wait("Ask");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn permission_survives_disconnect_and_server_failure_restores_the_shell() {
    let test = Tmux::new();
    let mut client = test.attach();
    test.prompt("before detach");
    test.wait("you said: before detach");
    test.prompt("tool");
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
    test.wait("selected stop");
    test.prompt("after reconnect");
    test.wait("you said: after reconnect");
    assert!(test.screen().contains("you said: before detach"));
    assert!(
        test.call(&["capture-pane", "-p", "-t", "test:0.1"])
            .contains("\nADJACENT_SHELL\n")
    );
    test.prompt("exit");
    test.wait("TERMINAL_RESTORED");
    test.wait("EXIT_1");
    test.prompt("echo SHELL_AFTER_FAILURE");
    test.wait("\nSHELL_AFTER_FAILURE\n");
    test.detach(&mut client);
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn pane_title_shows_status_and_keeps_unseen_results_until_focus() {
    let test = Tmux::new();
    let mut client = test.attach();
    test.call(&["split-window", "-h", "-t", "test:0.0", "/bin/sh"]);
    test.wait_title("ox: ready");
    test.prompt("running");
    test.wait_title("ox: working");
    test.keys(&["Escape"]);
    test.wait_title("ox: finished");
    test.prompt("tool");
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
    let config = root.path().join("ox");
    std::fs::create_dir(&config).unwrap();
    let marker = root.path().join("launched");
    std::fs::write(config.join("config.json"), serde_json::to_vec(&serde_json::json!({
        "servers": [{"name": "Fake", "command": "/bin/sh", "args": ["-c", format!("touch {}", quote(marker.to_str().unwrap()))]}]
    })).unwrap()).unwrap();
    for args in [
        vec!["--server", "missing"],
        vec!["/nonexistent-ox-test-directory"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_ox"))
            .env("XDG_CONFIG_HOME", root.path())
            .args(args)
            .output()
            .unwrap();
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
        assert!(!output.stderr.contains(&0x1b));
        assert!(!marker.exists());
    }
}
