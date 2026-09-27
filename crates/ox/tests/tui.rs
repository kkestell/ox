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
        std::fs::write(&conf, "set -g focus-events on\n").unwrap();
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
        test.wait("Ctrl-D: quit");
        test
    }

    fn command(&self) -> Command {
        let mut cmd = Command::new("tmux");
        cmd.args(["-S", self.socket.to_str().unwrap()])
            .env("HOME", self.root.path())
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

    fn capture(&self) -> String {
        self.call(&["capture-pane", "-p", "-J", "-S", "-", "-t", "test:0.0"])
    }

    fn wait(&self, text: &str) {
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            let screen = self.capture();
            if screen.contains(text) {
                return;
            }
            assert!(Instant::now() < deadline, "missing {text:?}:\n{screen}");
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

    fn prompt(&self, text: &str) {
        self.call(&["send-keys", "-t", "test:0.0", "-l", text]);
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

#[test]
#[ignore = "requires tmux; run make e2e"]
fn terminal_keys_stream_paste_resize_cancel_and_restore_the_shell() {
    let test = Tmux::new();
    test.prompt("stream");
    test.wait("stream arrives in order");
    test.wait("Turn finished");
    test.prompt("tool");
    test.wait("Permission required");
    test.keys(&["9", "Enter"]);
    test.wait("Enter one of the supplied option numbers");
    test.keys(&["1", "Enter"]);
    test.wait("selected go");
    test.prompt("running");
    test.wait("running; waiting for cancellation");
    test.keys(&["C-c"]);
    test.wait("cancelled");
    test.call(&["resize-window", "-t", "test:0", "-x", "24", "-y", "12"]);
    test.call(&["set-buffer", "pasted界\nsecond line"]);
    test.call(&["paste-buffer", "-p", "-t", "test:0.0"]);
    test.wait("second line");
    assert!(!test.capture().contains("you said: pasted"));
    test.call(&["resize-window", "-t", "test:0", "-x", "80", "-y", "24"]);
    test.keys(&["Enter"]);
    test.wait("you said: pasted界");
    test.wait("second line");
    test.keys(&["C-d"]);
    test.wait("TERMINAL_RESTORED");
    test.wait("EXIT_0");
    test.prompt("echo SHELL_USABLE");
    test.wait("\nSHELL_USABLE\n");
}

#[test]
#[ignore = "requires tmux; run make e2e"]
fn permission_survives_disconnect_and_server_failure_restores_the_shell() {
    let test = Tmux::new();
    let mut client = test.attach();
    test.prompt("before detach");
    test.wait("you said: before detach");
    test.prompt("tool");
    test.wait("Permission required");
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
    test.keys(&["2", "Enter"]);
    test.wait("selected stop");
    test.prompt("after reconnect");
    test.wait("you said: after reconnect");
    assert!(test.capture().contains("you said: before detach"));
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
    test.keys(&["C-c"]);
    test.wait_title("ox: finished");
    test.prompt("tool");
    test.wait_title("ox: needs permission");
    test.keys(&["2", "Enter"]);
    test.wait_title("ox: finished");
    test.prompt("fail");
    test.wait_title("ox: turn error");
    test.call(&["select-pane", "-t", "test:0.0"]);
    test.wait_title("ox: ready");
    test.prompt("stream");
    test.wait("Turn finished");
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
