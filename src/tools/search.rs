use std::{
    ffi::OsStr,
    os::unix::ffi::OsStrExt,
    path::{Component, Path, PathBuf},
    process::Stdio,
};

use regex::bytes::Regex;
use serde::Deserialize;
use tokio::{
    io::{AsyncBufReadExt, AsyncRead, AsyncReadExt, BufReader},
    process::Command,
};

use super::{BODY_LIMIT, GLOB, truncate, workspace::Workspace};

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
        .resolve_existing(Path::new(&scope))
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
        let mut output = String::new();
        let mut local_errors = String::new();
        let mut truncated = false;
        while reader.read_until(0, &mut candidate).await? != 0 {
            let name = candidate.strip_suffix(&[0]).unwrap_or(&candidate);
            let path = Path::new(OsStr::from_bytes(name));
            if let Ok(relative) = workspace.resolve_existing(path) {
                if let Some(matcher) = matcher {
                    match workspace.read_file(&relative) {
                        Ok(file) => match scan_file(file, path, matcher, &mut output).await {
                            Ok(done) => truncated = done,
                            Err(error) if local_errors.len() < DIAGNOSTICS_LIMIT => {
                                local_errors.push_str(&format!("{}: {error}\n", path.display()));
                            }
                            Err(_) => {}
                        },
                        Err(error) => {
                            if local_errors.len() < DIAGNOSTICS_LIMIT {
                                local_errors.push_str(&format!("{}: {error}\n", path.display()));
                            }
                        }
                    }
                } else if workspace.regular_file(&relative).unwrap_or(false) {
                    truncated = append_match(&mut output, &format!("{}\n", path.display()));
                }
            }
            candidate.clear();
            if truncated {
                child.kill().await?;
                break;
            }
        }
        let status = child.wait().await?;
        Ok::<_, std::io::Error>((output, truncated, status, local_errors))
    };
    let (result, errors) =
        tokio::try_join!(collect, drain_errors(stderr)).map_err(|e| e.to_string())?;
    let (mut output, truncated, status, local_errors) = result;
    if !truncated && !matches!(status.code(), Some(0 | 1)) && output.is_empty() {
        return Err(format!(
            "rg failed ({status}); check the glob or search path"
        ));
    }
    if truncated {
        if !output.ends_with('\n') {
            output.push_str(" [partial line]\n");
        }
        output.push_str("Output truncated. Narrow the search path or pattern.");
    } else if output.is_empty() {
        output.push_str("No matches found.");
    }
    if !errors.is_empty() || !local_errors.is_empty() {
        // Ripgrep may enumerate a path that changed during traversal. Its raw
        // diagnostics can name files outside the workspace after such a swap.
        let mut diagnostics = if errors.is_empty() {
            String::new()
        } else {
            "Ripgrep could not enumerate some paths.\n".to_owned()
        };
        diagnostics.push_str(&local_errors);
        truncate(&mut diagnostics, DIAGNOSTICS_LIMIT);
        if !output.ends_with('\n') {
            output.push('\n');
        }
        output.push_str("Some paths could not be searched:\n");
        output.push_str(diagnostics.trim_end());
        output.push('\n');
    }
    Ok(output)
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
        let locked = workspace.0.join("locked");
        std::fs::create_dir(&locked).unwrap();
        std::fs::write(locked.join("b.rs"), "needle\n").unwrap();
        std::fs::set_permissions(&locked, Permissions::from_mode(0o000)).unwrap();
        let result = search(&workspace, GREP, json!({"pattern":"needle"})).await;
        std::fs::set_permissions(&locked, Permissions::from_mode(0o755)).unwrap();
        let output = result.unwrap();
        assert!(output.contains("./a.rs:1:needle"));
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
        let validated = pinned.resolve_existing(Path::new("link")).unwrap();
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
