//! A workspace's branches, which a client offers when naming the branch of a
//! new worktree, and the fetch that brings its remote branches up to date.

use std::ffi::OsStr;
use std::os::unix::ffi::OsStrExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::Duration;

use ox_protocol::{ErrorBody, LocalBranch};
use tokio::io::AsyncReadExt;

use crate::worktree::{Git, not_a_working_tree};

/// How long a fetch may run before it is stopped.
const FETCH_TIMEOUT: Duration = Duration::from_mins(1);

/// Whether `path` looks like it is in a Git working tree, without running
/// git: no component of it is `.git`, and it or an ancestor has a `.git`
/// entry, the directory of a repository or the file of a linked worktree.
pub fn git_working_tree(path: &Path) -> bool {
    !path
        .components()
        .any(|component| component.as_os_str() == ".git")
        && path.ancestors().any(|dir| dir.join(".git").exists())
}

/// The absolute common Git directory of the repository `workspace` is in,
/// which all its worktrees share. Refuses a workspace outside a Git working
/// tree.
pub async fn common_dir(git: &Git, workspace: &Path) -> Result<PathBuf, ErrorBody> {
    let output = git
        .run(
            workspace,
            &[
                &"rev-parse",
                &"--path-format=absolute",
                &"--show-toplevel",
                &"--git-common-dir",
            ],
        )
        .await
        .map_err(|error| not_a_working_tree(workspace, error.stderr()))?;
    let common_dir = output
        .split(|&byte| byte == b'\n')
        .nth(1)
        .filter(|line| !line.is_empty())
        .ok_or_else(|| not_a_working_tree(workspace, ""))?;
    Ok(PathBuf::from(OsStr::from_bytes(common_dir)))
}

/// The local and remote-tracking branches of the repository `workspace` is
/// in, most recently committed first, without symbolic refs such as
/// `origin/HEAD`. Each local branch names the worktree that has it checked
/// out; remote branches keep their remote's name.
pub async fn list(
    git: &Git,
    workspace: &Path,
) -> Result<(Vec<LocalBranch>, Vec<String>), ErrorBody> {
    let output = git
        .run(
            workspace,
            &[
                &"for-each-ref",
                &"--sort=-committerdate",
                &"--format=%(refname)%00%(symref)%00%(worktreepath)",
                &"refs/heads",
                &"refs/remotes",
            ],
        )
        .await?;
    let mut local = Vec::new();
    let mut remote = Vec::new();
    for line in output.split(|&byte| byte == b'\n') {
        let mut fields = line.split(|&byte| byte == 0);
        let (Some(name), Some(b""), Some(worktree)) = (fields.next(), fields.next(), fields.next())
        else {
            continue;
        };
        let name = String::from_utf8_lossy(name);
        if let Some(branch) = name.strip_prefix("refs/heads/") {
            local.push(LocalBranch {
                name: branch.to_owned(),
                worktree: (!worktree.is_empty())
                    .then(|| PathBuf::from(OsStr::from_bytes(worktree))),
            });
        } else if let Some(branch) = name.strip_prefix("refs/remotes/") {
            remote.push(branch.to_owned());
        }
    }
    Ok((local, remote))
}

/// Runs `git fetch --all` for the repository `workspace` is in, as the
/// user's `fetch.prune` settings say. A fetch that runs longer than
/// `FETCH_TIMEOUT` is sent `SIGTERM`, which lets git remove its lock files,
/// with every process it started. Returns git's error output, or why the
/// fetch stopped, when it fails.
pub async fn fetch(git: &Git, workspace: &Path) -> Result<(), String> {
    let mut child = git
        .command(workspace, &[&"fetch", &"--all", &"--quiet"])
        .stdout(Stdio::null())
        .process_group(0)
        .spawn()
        .map_err(|error| format!("git fetch could not start: {error}"))?;
    let group = child
        .id()
        .and_then(|id| libc::pid_t::try_from(id).ok())
        .expect("a child that was not waited for has a process ID");
    let mut stderr = child.stderr.take().expect("stderr is piped");
    let mut errors = Vec::new();
    let finished = tokio::time::timeout(FETCH_TIMEOUT, async {
        let (_, status) = tokio::join!(stderr.read_to_end(&mut errors), child.wait());
        status
    })
    .await;
    match finished {
        Ok(Ok(status)) if status.success() => Ok(()),
        Ok(Ok(_)) => Err(String::from_utf8_lossy(&errors).trim().to_owned()),
        Ok(Err(error)) => Err(format!("waiting for git fetch failed: {error}")),
        Err(_) => {
            // SAFETY: sends SIGTERM to the process group of the fetch this
            // function started, whose leader it has not yet waited for.
            unsafe { libc::killpg(group, libc::SIGTERM) };
            let _ = child.wait().await;
            Err(format!(
                "git fetch did not finish within {} seconds",
                FETCH_TIMEOUT.as_secs()
            ))
        }
    }
}
