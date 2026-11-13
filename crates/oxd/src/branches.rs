//! A workspace's branches: listing them for a client naming the branch of a
//! new worktree, the fetch that brings its remote branches up to date,
//! comparing a session's branch with its comparison ref, and moving a branch
//! along with the checkout that has it.

use std::ffi::OsStr;
use std::os::unix::ffi::OsStrExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::Duration;

use ox_protocol::{Comparison, ErrorBody, LocalBranch, code};
use tokio::io::AsyncReadExt;

use crate::daemon::invalid_request;
use crate::store::SavedWorktree;
use crate::worktree::{Git, Repository, checked_out_at, not_a_working_tree};

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

/// How the worktree's branch compares with its comparison ref, in its
/// repository: the commits on each, and how many commits each has that the
/// other lacks. Unset when the worktree has no comparison ref.
pub async fn compare(git: &Git, worktree: &SavedWorktree) -> Option<Comparison> {
    let compare = worktree.info.compare.as_deref()?;
    let branch = &worktree.info.branch;
    let repository = Repository::new(git, &worktree.common_dir);
    let failed = |error: String| Some(Comparison::Failed { error });
    let Some(tip) = repository.commit(&format!("refs/heads/{branch}")).await else {
        return failed(format!("branch {branch} is not a commit"));
    };
    let Some(compare_tip) = repository.commit(compare).await else {
        return failed(format!("{compare} is not a commit"));
    };
    let range = format!("{compare_tip}...{tip}");
    let counts = match repository
        .text(&[&"rev-list", &"--left-right", &"--count", &range])
        .await
    {
        Ok(counts) => counts,
        Err(error) => return failed(error.to_string()),
    };
    let mut counts = counts.split_whitespace().map(str::parse::<u64>);
    let (Some(Ok(behind)), Some(Ok(ahead))) = (counts.next(), counts.next()) else {
        return failed(format!("git rev-list printed no counts for {range}"));
    };
    Some(Comparison::Compared {
        tip,
        compare_tip,
        ahead,
        behind,
    })
}

/// Sets local branch `branch` of the repository whose common Git directory
/// is `common_dir` to commit `to`, when it is at commit `expected` and,
/// unless `force` is set, `to` descends from `expected`. When a checkout
/// has the branch, `busy` is asked whether the checkout's agents allow the
/// move, tracked files there must have no uncommitted changes, and the
/// checkout's index and files move with the branch. A move git cannot make
/// in the checkout, such as one that would overwrite an untracked file, puts
/// the branch back.
pub async fn move_branch(
    git: &Git,
    common_dir: &Path,
    branch: &str,
    to: &str,
    expected: &str,
    force: bool,
    busy: impl FnOnce(&Path) -> Result<(), ErrorBody>,
) -> Result<(), ErrorBody> {
    let repository = Repository::new(git, common_dir);
    let reference = format!("refs/heads/{branch}");
    if repository
        .run(&[&"check-ref-format", &reference])
        .await
        .is_err()
    {
        return Err(invalid_request(format!(
            "{branch} is not a valid branch name"
        )));
    }
    let not_a_commit = |rev: &str| invalid_request(format!("{rev} is not a commit"));
    let target = repository
        .commit(to)
        .await
        .ok_or_else(|| not_a_commit(to))?;
    let expected = repository
        .commit(expected)
        .await
        .ok_or_else(|| not_a_commit(expected))?;
    let current = repository
        .commit(&reference)
        .await
        .ok_or_else(|| invalid_request(format!("no branch {branch}")))?;
    if current != expected {
        return Err(ErrorBody {
            code: code::INVALID_STATE,
            message: format!("{branch} is at {current}, not {expected}"),
        });
    }
    if !force
        && repository
            .run(&[&"merge-base", &"--is-ancestor", &expected, &target])
            .await
            .is_err()
    {
        return Err(invalid_request(format!(
            "{to} is not a fast-forward of {branch}"
        )));
    }
    let checkout = checked_out_at(&repository, branch)
        .await?
        .map(PathBuf::from);
    if let Some(checkout) = &checkout {
        busy(checkout)?;
        let status = git
            .run(
                checkout,
                &[
                    &"--no-optional-locks",
                    &"status",
                    &"--porcelain",
                    &"--untracked-files=no",
                    &"--ignore-submodules=none",
                ],
            )
            .await?;
        if !status.is_empty() {
            return Err(ErrorBody {
                code: code::UNSAVED_WORK,
                message: format!("{} has uncommitted changes", checkout.display()),
            });
        }
        git.run(checkout, &[&"update-index", &"-q", &"--refresh"])
            .await?;
    }
    let message = format!("ox: move {branch}");
    repository
        .run(&[
            &"update-ref",
            &"-m",
            &message,
            &reference,
            &target,
            &expected,
        ])
        .await?;
    if let Some(checkout) = &checkout
        && let Err(error) = git
            .run(checkout, &[&"read-tree", &"-u", &"-m", &expected, &target])
            .await
    {
        let mut error = ErrorBody::from(error);
        if let Err(failure) = repository
            .run(&[
                &"update-ref",
                &"-m",
                &message,
                &reference,
                &expected,
                &target,
            ])
            .await
        {
            error.message = format!(
                "{}; putting {branch} back at {expected} failed: {failure}",
                error.message
            );
        }
        return Err(error);
    }
    Ok(())
}
