use std::{
    path::{Component, Path, PathBuf},
    process::Stdio,
};

use serde::Deserialize;
use tokio::{
    io::{AsyncRead, AsyncReadExt},
    process::Command,
};

use super::{BODY_LIMIT, GLOB, existing_path, truncate};

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
    let mut command = Command::new("rg");
    command.arg("--no-config");
    let scope = if name == GLOB {
        let args: GlobArgs =
            serde_json::from_str(arguments).map_err(|e| format!("arguments: {e}"))?;
        command.arg("--files").arg("--glob").arg(args.pattern);
        args.path
    } else {
        let args: GrepArgs =
            serde_json::from_str(arguments).map_err(|e| format!("arguments: {e}"))?;
        command.args([
            "--line-number",
            "--with-filename",
            "--no-heading",
            "--color",
            "never",
        ]);
        command.arg("--regexp").arg(args.pattern);
        if let Some(glob) = args.glob {
            command.arg("--glob").arg(glob);
        }
        args.path
    };
    let path = existing_path(root, &scope).await?;
    let metadata = tokio::fs::metadata(&path)
        .await
        .map_err(|e| e.to_string())?;
    if name == GLOB && !metadata.is_dir() {
        return Err("glob path must name a directory".to_owned());
    }
    if !metadata.is_dir() && !metadata.is_file() {
        return Err("search path must name a regular file or directory".to_owned());
    }
    // Preserve the supplied path (and therefore explicit-path filtering), after validation.
    command.current_dir(root).arg("--").arg(
        Path::new(".").join(
            Path::new(&scope)
                .components()
                .filter(|part| matches!(part, Component::Normal(_)))
                .collect::<PathBuf>(),
        ),
    );
    run(command).await
}

// Leave room in the output budget for the truncation and diagnostics notices.
const NOTICE_LIMIT: usize = 1024;
const MATCH_LIMIT: usize = BODY_LIMIT - NOTICE_LIMIT;
const DIAGNOSTICS_LIMIT: usize = 512;

async fn run(mut command: Command) -> Result<String, String> {
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
        let mut bytes = Vec::new();
        stdout
            .take((MATCH_LIMIT + 1) as u64)
            .read_to_end(&mut bytes)
            .await?;
        let truncated = bytes.len() > MATCH_LIMIT;
        if truncated {
            child.kill().await?;
        }
        let status = child.wait().await?;
        Ok::<_, std::io::Error>((bytes, truncated, status))
    };
    let (result, errors) =
        tokio::try_join!(collect, drain_errors(stderr)).map_err(|e| e.to_string())?;
    let (mut bytes, truncated, status) = result;
    // ripgrep exits with an error status when it cannot read a single path,
    // even though it still reports every match it did find. Fail only when
    // there are no matches to return, and describe the unread paths otherwise.
    if !truncated && !matches!(status.code(), Some(0 | 1)) && bytes.is_empty() {
        return Err(format!(
            "rg failed ({status}): {}",
            String::from_utf8_lossy(&errors)
        ));
    }
    if truncated {
        bytes.truncate(MATCH_LIMIT);
        if let Err(error) = std::str::from_utf8(&bytes)
            && error.error_len().is_none()
        {
            bytes.truncate(error.valid_up_to());
        }
    }
    let mut output = String::from_utf8_lossy(&bytes).into_owned();
    // Lossy decoding can expand bytes, so apply the budget after decoding too.
    let truncated = truncated || output.len() > MATCH_LIMIT;
    if truncated {
        truncate(&mut output, MATCH_LIMIT);
        if !output.ends_with('\n') {
            output.push_str(" [partial line]\n");
        }
        output.push_str("Output truncated. Narrow the search path or pattern.");
    } else if output.is_empty() {
        output.push_str("No matches found.");
    }
    if !errors.is_empty() {
        let mut diagnostics = String::from_utf8_lossy(&errors).into_owned();
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
        assert!(output.contains("locked"));
    }

    #[tokio::test]
    async fn diagnostics_are_drained_but_not_retained_without_limit() {
        let bytes = vec![b'x'; 100_000];
        assert_eq!(
            drain_errors(bytes.as_slice()).await.unwrap(),
            vec![b'x'; 4096]
        );
        let error = run(Command::new("/nonexistent/ox-test-rg"))
            .await
            .unwrap_err();
        assert!(error.contains("install ripgrep"));
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn subprocesses_drain_stderr_and_stop_on_truncation_or_cancellation() {
        use std::time::Duration;
        let mut command = Command::new("sh");
        command.args([
            "-c",
            "while :; do printf '%01000d' 0 >&2; printf '%01000d' 0; done",
        ]);
        let output = tokio::time::timeout(Duration::from_secs(5), run(command))
            .await
            .unwrap()
            .unwrap();
        assert!(output.contains("Output truncated"));

        let workspace = Workspace::new();
        let pid_path = workspace.0.join("pid");
        let mut command = Command::new("sh");
        command
            .args(["-c", "echo $$ > \"$1\"; exec sleep 60", "sh"])
            .arg(&pid_path);
        let task = tokio::spawn(run(command));
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
}
