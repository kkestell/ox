use std::{
    ffi::OsStr,
    os::unix::ffi::OsStrExt,
    path::{Component, Path, PathBuf},
    process::{ExitStatus, Stdio},
};

use regex::bytes::Regex;
use serde::Deserialize;
use serde_json::{Value, json};
use tokio::{
    io::{AsyncBufReadExt, AsyncRead, AsyncReadExt, BufReader},
    process::Command,
};

use super::{BODY_LIMIT, GLOB, GREP, truncate, workspace::Workspace};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct GlobArgs {
    pattern: String,
    #[serde(default = "default_path")]
    path: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct GrepArgs {
    pattern: String,
    #[serde(default = "default_path")]
    path: String,
    glob: Option<String>,
}

fn default_path() -> String {
    ".".to_owned()
}

pub(super) fn glob_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": GLOB,
            "description": "Find files inside the workspace using a ripgrep glob. Returns `./`-prefixed workspace-relative paths, at most 16 KiB. Narrow the pattern or path if truncated. Uses ripgrep's normal hidden-file and ignore filtering, including glob overrides; does not follow symlinks during traversal. Example: {\"pattern\":\"*.rs\",\"path\":\"src\"}.",
            "parameters": {
                "type": "object",
                "properties": {
                    "pattern": { "type": "string", "description": "Ripgrep glob, e.g. *.rs or src/**/*.rs." },
                    "path": { "type": "string", "default": ".", "description": "Workspace-relative directory to search. Globs are relative to the workspace." }
                },
                "required": ["pattern"],
                "additionalProperties": false
            }
        }
    })
}

pub(super) fn grep_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": GREP,
            "description": "Search workspace text files with a case-sensitive Rust regex; use (?i) for case-insensitivity. Returns path:line:content, at most 16 KiB. Narrow the pattern or path if truncated. Uses ripgrep's normal hidden-file and ignore filtering for file discovery, including explicit-path and glob overrides; does not follow symlinks during traversal. Example: {\"pattern\":\"fn main\",\"path\":\"src\",\"glob\":\"*.rs\"}.",
            "parameters": {
                "type": "object",
                "properties": {
                    "pattern": { "type": "string", "description": "Ripgrep regular expression." },
                    "path": { "type": "string", "default": ".", "description": "Workspace-relative file or directory to search." },
                    "glob": { "type": "string", "description": "Optional filename glob, relative to the workspace, e.g. *.rs." }
                },
                "required": ["pattern"],
                "additionalProperties": false
            }
        }
    })
}

pub(super) async fn execute(root: &Path, name: &str, arguments: &str) -> Result<String, String> {
    let workspace = Workspace::open(root).map_err(|e| e.to_string())?;
    let mut command = Command::new("rg");
    command.args(["--no-config", "--files", "--null"]);
    let (scope, matcher) = if name == GLOB {
        let args: GlobArgs =
            serde_json::from_str(arguments).map_err(|e| format!("arguments: {e}"))?;
        command.arg("--glob").arg(args.pattern);
        (args.path, None)
    } else {
        let args: GrepArgs =
            serde_json::from_str(arguments).map_err(|e| format!("arguments: {e}"))?;
        let matcher = Regex::new(&args.pattern).map_err(|e| format!("pattern: {e}"))?;
        if let Some(glob) = args.glob {
            command.arg("--glob").arg(glob);
        }
        (args.path, Some(matcher))
    };
    let path = workspace
        .resolve_allowing_link_target(Path::new(&scope))
        .map_err(|e| format!("{scope}: {e}"))?;
    let directory = workspace.directory(&path).is_ok();
    if name == GLOB && !directory {
        return Err("glob path must name a directory".to_owned());
    }
    if !directory && workspace.read_file(&path).is_err() {
        return Err("search path must name a regular file or directory".to_owned());
    }
    // Ripgrep supplies candidate names and ignore filtering. Ox opens each
    // candidate through the workspace descriptor before reading or returning it.
    command.current_dir(root).arg("--").arg(
        Path::new(".").join(
            Path::new(&scope)
                .components()
                .filter(|part| matches!(part, Component::Normal(_)))
                .collect::<PathBuf>(),
        ),
    );
    run(command, &workspace, matcher.as_ref()).await
}

// Leave room in the output budget for the truncation and diagnostics notices.
const NOTICE_LIMIT: usize = 1024;
const MATCH_LIMIT: usize = BODY_LIMIT - NOTICE_LIMIT;
const DIAGNOSTICS_LIMIT: usize = 512;

async fn run(
    mut command: Command,
    workspace: &Workspace,
    matcher: Option<&Regex>,
) -> Result<String, String> {
    let mut child = command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .map_err(|e| format!("could not run rg; install ripgrep and ensure rg is on PATH: {e}"))?;
    let stdout = child.stdout.take().expect("piped stdout");
    let stderr = child.stderr.take().expect("piped stderr");
    let collect = async {
        let mut reader = BufReader::new(stdout);
        let mut candidate = Vec::new();
        let mut output = SearchOutput::default();
        while !output.truncated && reader.read_until(0, &mut candidate).await? != 0 {
            let name = candidate.strip_suffix(&[0]).unwrap_or(&candidate);
            output
                .add_candidate(workspace, Path::new(OsStr::from_bytes(name)), matcher)
                .await;
            candidate.clear();
        }
        if output.truncated {
            child.kill().await?;
        }
        let status = child.wait().await?;
        Ok::<_, std::io::Error>((output, status))
    };
    let ((output, status), errors) =
        tokio::try_join!(collect, drain_errors(stderr)).map_err(|e| e.to_string())?;
    output.finish(status, !errors.is_empty())
}

#[derive(Default)]
struct SearchOutput {
    output: String,
    truncated: bool,
    local_errors: String,
}

impl SearchOutput {
    async fn add_candidate(&mut self, workspace: &Workspace, path: &Path, matcher: Option<&Regex>) {
        let Ok(relative) = workspace.resolve_allowing_link_target(path) else {
            return;
        };
        match matcher {
            Some(matcher) => self.grep_file(workspace, &relative, path, matcher).await,
            None => {
                if workspace.regular_file(&relative).unwrap_or(false) {
                    self.truncated =
                        append_match(&mut self.output, &format!("{}\n", path.display()));
                }
            }
        }
    }

    async fn grep_file(
        &mut self,
        workspace: &Workspace,
        relative: &Path,
        path: &Path,
        matcher: &Regex,
    ) {
        let scanned = match workspace.read_file(relative) {
            Ok(file) => scan_file(file, path, matcher, &mut self.output).await,
            Err(error) => Err(error),
        };
        match scanned {
            Ok(truncated) => self.truncated = truncated,
            Err(error) => self.record_error(path, &error),
        }
    }

    fn record_error(&mut self, path: &Path, error: &std::io::Error) {
        if self.local_errors.len() < DIAGNOSTICS_LIMIT {
            self.local_errors
                .push_str(&format!("{}: {error}\n", path.display()));
        }
    }

    fn finish(mut self, status: ExitStatus, enumeration_failed: bool) -> Result<String, String> {
        if !self.truncated && !matches!(status.code(), Some(0 | 1)) && self.output.is_empty() {
            return Err(format!(
                "rg failed ({status}); check the glob or search path"
            ));
        }
        if self.truncated {
            if !self.output.ends_with('\n') {
                self.output.push_str(" [partial line]\n");
            }
            self.output
                .push_str("Output truncated. Narrow the search path or pattern.");
        } else if self.output.is_empty() {
            self.output.push_str("No matches found.");
        }
        if enumeration_failed || !self.local_errors.is_empty() {
            append_diagnostics(&mut self.output, enumeration_failed, &self.local_errors);
        }
        Ok(self.output)
    }
}

fn append_diagnostics(output: &mut String, enumeration_failed: bool, local_errors: &str) {
    // Ripgrep may enumerate a path that changed during traversal. Its raw
    // diagnostics can name files outside the workspace after such a swap.
    let mut diagnostics = if enumeration_failed {
        "Ripgrep could not enumerate some paths.\n".to_owned()
    } else {
        String::new()
    };
    diagnostics.push_str(local_errors);
    truncate(&mut diagnostics, DIAGNOSTICS_LIMIT);
    if !output.ends_with('\n') {
        output.push('\n');
    }
    output.push_str("Some paths could not be searched:\n");
    output.push_str(diagnostics.trim_end());
    output.push('\n');
}

fn append_match(output: &mut String, line: &str) -> bool {
    output.push_str(line);
    if output.len() > MATCH_LIMIT {
        truncate(output, MATCH_LIMIT);
        true
    } else {
        false
    }
}

async fn scan_file(
    file: std::fs::File,
    path: &Path,
    matcher: &Regex,
    output: &mut String,
) -> std::io::Result<bool> {
    let mut reader = BufReader::new(tokio::fs::File::from_std(file));
    let mut line = Vec::new();
    let mut number = 0_u64;
    while reader.read_until(b'\n', &mut line).await? != 0 {
        number += 1;
        let searchable = line.strip_suffix(b"\n").unwrap_or(&line);
        if !searchable.contains(&0) && matcher.is_match(searchable) {
            let content = String::from_utf8_lossy(&line);
            let separator = if line.ends_with(b"\n") { "" } else { "\n" };
            if append_match(
                output,
                &format!("{}:{number}:{content}{separator}", path.display()),
            ) {
                return Ok(true);
            }
        }
        line.clear();
    }
    Ok(false)
}

// Keep diagnostics bounded while continuing to drain the pipe to avoid deadlock.
async fn drain_errors(mut reader: impl AsyncRead + Unpin) -> std::io::Result<Vec<u8>> {
    const LIMIT: usize = 4096;
    let mut saved = Vec::new();
    let mut buffer = [0; 4096];
    loop {
        let count = reader.read(&mut buffer).await?;
        if count == 0 {
            return Ok(saved);
        }
        saved.extend_from_slice(&buffer[..count.min(LIMIT - saved.len())]);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::{GREP, OUTPUT_LIMIT, fixture::Workspace};
    use serde_json::json;

    async fn search(
        workspace: &Workspace,
        name: &str,
        args: serde_json::Value,
    ) -> Result<String, String> {
        execute(&workspace.0, name, &args.to_string()).await
    }

    #[tokio::test]
    async fn real_searches_filter_files_and_handle_literal_arguments() {
        let workspace = Workspace::new();
        std::fs::create_dir(workspace.0.join("src")).unwrap();
        std::fs::write(workspace.0.join(".ignore"), "ignored\n").unwrap();
        for (path, text) in [
            ("src/a.rs", "Hello\nneedle\n"),
            ("src/b.txt", "needle\n"),
            ("ignored", "needle\n"),
            (".hidden", "needle\n"),
            ("- file", "-needle\n"),
            ("-", "dash\n"),
        ] {
            std::fs::write(workspace.0.join(path), text).unwrap();
        }
        let files = search(&workspace, GLOB, json!({"pattern":"*.rs", "path":"src"}))
            .await
            .unwrap();
        assert_eq!(files, "./src/a.rs\n");
        let matches = search(
            &workspace,
            GREP,
            json!({"pattern":"(?i)hello", "glob":"*.rs"}),
        )
        .await
        .unwrap();
        assert_eq!(matches, "./src/a.rs:1:Hello\n");
        let matches = search(&workspace, GREP, json!({"pattern":"needle"}))
            .await
            .unwrap();
        assert!(!matches.contains("ignored"));
        assert!(!matches.contains(".hidden"));
        assert!(matches.contains("./src/b.txt:1:needle"));
        assert_eq!(
            search(
                &workspace,
                GREP,
                json!({"pattern":"-needle", "path":"- file"})
            )
            .await
            .unwrap(),
            "./- file:1:-needle\n"
        );
        assert_eq!(
            search(&workspace, GREP, json!({"pattern":"dash", "path":"-"}))
                .await
                .unwrap(),
            "./-:1:dash\n"
        );
        assert_eq!(
            search(
                &workspace,
                GREP,
                json!({"pattern":"^needle$", "path":"src/a.rs"})
            )
            .await
            .unwrap(),
            "./src/a.rs:2:needle\n"
        );
        assert!(
            search(
                &workspace,
                GREP,
                json!({"pattern":"needle", "path":".hidden"})
            )
            .await
            .unwrap()
            .contains("needle")
        );
        assert!(
            search(&workspace, GLOB, json!({"pattern":"ignored"}))
                .await
                .unwrap()
                .contains("ignored")
        );
        for name in [GLOB, GREP] {
            assert_eq!(
                search(&workspace, name, json!({"pattern":"nothing-matches"}))
                    .await
                    .unwrap(),
                "No matches found."
            );
            assert!(
                search(&workspace, name, json!({"pattern":"["}))
                    .await
                    .is_err()
            );
        }
    }

    #[tokio::test]
    async fn real_searches_cap_paths_and_matching_lines() {
        let workspace = Workspace::new();
        for index in 0..300 {
            std::fs::write(
                workspace.0.join(format!("{index}-{}", "x".repeat(100))),
                "雪".repeat(100),
            )
            .unwrap();
        }
        for name in [GLOB, GREP] {
            let pattern = if name == GLOB { "*" } else { "雪" };
            let output = search(&workspace, name, json!({"pattern":pattern}))
                .await
                .unwrap();
            assert!(output.len() <= OUTPUT_LIMIT);
            assert!(output.ends_with("Output truncated. Narrow the search path or pattern."));
            assert!(!output.contains('\u{fffd}'));
        }
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn unreadable_paths_are_reported_alongside_the_matches_that_were_found() {
        use std::{fs::Permissions, os::unix::fs::PermissionsExt};
        let workspace = Workspace::new();
        std::fs::write(workspace.0.join("a.rs"), "needle\n").unwrap();
        let unreadable = workspace.0.join("c.rs");
        std::fs::write(&unreadable, "needle\n").unwrap();
        std::fs::set_permissions(&unreadable, Permissions::from_mode(0o000)).unwrap();
        let locked = workspace.0.join("locked");
        std::fs::create_dir(&locked).unwrap();
        std::fs::write(locked.join("b.rs"), "needle\n").unwrap();
        std::fs::set_permissions(&locked, Permissions::from_mode(0o000)).unwrap();
        let result = search(&workspace, GREP, json!({"pattern":"needle"})).await;
        std::fs::set_permissions(&locked, Permissions::from_mode(0o755)).unwrap();
        let output = result.unwrap();
        assert!(output.contains("./a.rs:1:needle"));
        assert!(output.contains("./c.rs: "));
        assert!(output.contains("Some paths could not be searched:"));
        assert!(output.contains("Ripgrep could not enumerate some paths."));
    }

    #[tokio::test]
    async fn search_subprocess_diagnostics_and_cancellation() {
        use std::time::Duration;

        let bytes = vec![b'x'; 100_000];
        assert_eq!(
            drain_errors(bytes.as_slice()).await.unwrap(),
            vec![b'x'; 4096]
        );
        let workspace = Workspace::new();
        let pinned = super::Workspace::open(&workspace.0).unwrap();
        let error = run(Command::new("/nonexistent/ox-test-rg"), &pinned, None)
            .await
            .unwrap_err();
        assert!(error.contains("install ripgrep"));

        let pid_path = workspace.0.join("pid");
        let mut command = Command::new("sh");
        command
            .args(["-c", "echo $$ > \"$1\"; exec sleep 60", "sh"])
            .arg(&pid_path);
        let task = tokio::spawn(async move { run(command, &pinned, None).await });
        let pid = tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                if let Ok(pid) = tokio::fs::read_to_string(&pid_path).await
                    && !pid.trim().is_empty()
                {
                    break pid;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        task.abort();
        assert!(task.await.unwrap_err().is_cancelled());
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                let probe = Command::new("kill")
                    .args(["-0", pid.trim()])
                    .output()
                    .await
                    .unwrap();
                if !probe.status.success() {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("cancelled child was terminated and reaped");
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn candidate_paths_are_checked_against_the_workspace() {
        use std::os::unix::fs::symlink;
        let workspace = Workspace::new();
        let outside = Workspace::new();
        std::fs::write(outside.0.join("secret"), "secret\n").unwrap();
        std::fs::create_dir(workspace.0.join("link")).unwrap();
        let pinned = super::Workspace::open(&workspace.0).unwrap();
        let validated = pinned
            .resolve_allowing_link_target(Path::new("link"))
            .unwrap();
        assert!(pinned.directory(&validated).is_ok());
        std::fs::remove_dir(workspace.0.join("link")).unwrap();
        symlink(&outside.0, workspace.0.join("link")).unwrap();
        for matcher in [None, Some(Regex::new("secret").unwrap())] {
            let mut command = Command::new("sh");
            command.args(["-c", "printf 'link/secret\\000'"]);
            assert_eq!(
                run(command, &pinned, matcher.as_ref()).await.unwrap(),
                "No matches found."
            );
        }
    }
}
