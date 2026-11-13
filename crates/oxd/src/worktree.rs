//! Worktrees that sessions run in: resolving and creating a session's
//! branch, adding its worktree, undoing both when the worktree cannot be
//! added, and removing the worktree and the branch when the session goes.

use std::collections::BTreeMap;
use std::ffi::{OsStr, OsString};
use std::fmt;
use std::os::unix::ffi::OsStrExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;

use ox_protocol::{ErrorBody, WorktreeBranch, WorktreeInfo, code};
use tokio::process::Command;

use crate::daemon::invalid_request;
use crate::store::SavedWorktree;

/// The variables that point git at one repository, as `git rev-parse
/// --local-env-vars` lists them. Git runs in the directory it is given, so
/// none of them pass through.
const LOCAL_ENV_VARS: &[&str] = &[
    "GIT_ALTERNATE_OBJECT_DIRECTORIES",
    "GIT_CONFIG",
    "GIT_CONFIG_PARAMETERS",
    "GIT_CONFIG_COUNT",
    "GIT_OBJECT_DIRECTORY",
    "GIT_DIR",
    "GIT_WORK_TREE",
    "GIT_IMPLICIT_WORK_TREE",
    "GIT_GRAFT_FILE",
    "GIT_INDEX_FILE",
    "GIT_NO_REPLACE_OBJECTS",
    "GIT_REPLACE_REF_BASE",
    "GIT_PREFIX",
    "GIT_SHALLOW_FILE",
    "GIT_COMMON_DIR",
];

/// An argument to git.
pub type Arg<'a> = &'a (dyn AsRef<OsStr> + Sync);

/// Runs git for one operation, with the environment of the client that asked
/// for it.
#[derive(Clone)]
pub struct Git {
    env: BTreeMap<String, String>,
}

/// A git command that failed, with git's error output.
#[derive(Debug)]
pub struct GitError {
    args: Vec<String>,
    stderr: String,
}

impl GitError {
    /// Git's error output, trimmed.
    pub fn stderr(&self) -> &str {
        &self.stderr
    }
}

impl fmt::Display for GitError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "git {} failed: {}", self.args.join(" "), self.stderr)
    }
}

impl From<GitError> for ErrorBody {
    fn from(error: GitError) -> Self {
        Self {
            code: code::GIT,
            message: error.to_string(),
        }
    }
}

impl Git {
    /// Git with `env` over the daemon's environment, as the client's agent
    /// gets it, and `git` found on that environment's `PATH`.
    pub fn for_client(env: BTreeMap<String, String>) -> Self {
        Self { env }
    }

    /// Git with the daemon's own environment, for work no client asked for.
    pub fn own() -> Self {
        Self::for_client(BTreeMap::new())
    }

    /// `git -C dir args`, with this environment, stdin null, and its output
    /// piped.
    pub fn command(&self, dir: &Path, args: &[Arg<'_>]) -> Command {
        let mut command = Command::new("git");
        command
            .arg("-C")
            .arg(dir)
            .args(args.iter().map(AsRef::as_ref))
            .envs(&self.env)
            .env("GIT_TERMINAL_PROMPT", "0")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        for var in LOCAL_ENV_VARS {
            command.env_remove(var);
        }
        command
    }

    /// Runs `git -C dir args` and returns its output. Callers put
    /// `--end-of-options` before names and revisions a user supplied.
    pub async fn run(&self, dir: &Path, args: &[Arg<'_>]) -> Result<Vec<u8>, GitError> {
        let failed = |stderr: String| GitError {
            args: args
                .iter()
                .map(|arg| arg.as_ref().to_string_lossy().into_owned())
                .collect(),
            stderr,
        };
        let output = self
            .command(dir, args)
            .output()
            .await
            .map_err(|error| failed(error.to_string()))?;
        if output.status.success() {
            Ok(output.stdout)
        } else {
            Err(failed(
                String::from_utf8_lossy(&output.stderr).trim().to_owned(),
            ))
        }
    }

    /// Like `run`, with the output as text without its final newline.
    async fn text(&self, dir: &Path, args: &[Arg<'_>]) -> Result<String, GitError> {
        let output = self.run(dir, args).await?;
        let text = String::from_utf8_lossy(&output);
        Ok(text.strip_suffix('\n').unwrap_or(&text).to_owned())
    }

    /// The commit `rev` names, if it names one.
    async fn commit(&self, dir: &Path, rev: &str) -> Option<String> {
        let rev = format!("{rev}^{{commit}}");
        self.text(
            dir,
            &[
                &"rev-parse",
                &"--verify",
                &"--quiet",
                &"--end-of-options",
                &rev,
            ],
        )
        .await
        .ok()
    }
}

/// A repository named by its common Git directory. Git runs in that
/// directory with `--git-dir` set to it, so it never discovers another
/// repository, whatever has become of the checkouts.
struct Repository<'a> {
    git: &'a Git,
    common_dir: &'a Path,
    git_dir: OsString,
}

impl<'a> Repository<'a> {
    fn new(git: &'a Git, common_dir: &'a Path) -> Self {
        let mut git_dir = OsString::from("--git-dir=");
        git_dir.push(common_dir);
        Self {
            git,
            common_dir,
            git_dir,
        }
    }

    async fn run(&self, args: &[Arg<'_>]) -> Result<Vec<u8>, GitError> {
        let mut all: Vec<Arg<'_>> = vec![&self.git_dir];
        all.extend_from_slice(args);
        self.git.run(self.common_dir, &all).await
    }

    async fn text(&self, args: &[Arg<'_>]) -> Result<String, GitError> {
        let output = self.run(args).await?;
        let text = String::from_utf8_lossy(&output);
        Ok(text.strip_suffix('\n').unwrap_or(&text).to_owned())
    }

    /// The commit `rev` names, if it names one.
    async fn commit(&self, rev: &str) -> Option<String> {
        let rev = format!("{rev}^{{commit}}");
        self.text(&[
            &"rev-parse",
            &"--verify",
            &"--quiet",
            &"--end-of-options",
            &rev,
        ])
        .await
        .ok()
    }

    /// Whether git finds the repository.
    async fn found(&self) -> bool {
        self.run(&[&"rev-parse", &"--git-dir"]).await.is_ok()
    }
}

/// How a session's branch comes to exist.
#[derive(Clone, Debug, PartialEq, Eq)]
enum Kind {
    /// A branch created from `base`, with no upstream: the session's
    /// `ox/<id8>` branch, or a new branch the client named.
    New,
    /// A local branch that already exists.
    Existing,
    /// A local branch created from the remote branch of the same name on
    /// `remote`, tracking it.
    Track { remote: String },
}

/// A worktree to add for a new session, resolved and checked.
#[derive(Debug)]
pub struct WorktreePlan {
    /// The top level of the workspace's checkout, where git runs.
    top: PathBuf,
    /// The repository's absolute common Git directory.
    common_dir: PathBuf,
    /// The worktree's top level.
    path: PathBuf,
    /// The session's directory: `path`, or the subdirectory of it that the
    /// workspace is of its checkout.
    pub cwd: PathBuf,
    branch: String,
    kind: Kind,
    /// The commit the worktree starts at.
    base: String,
}

impl WorktreePlan {
    /// The worktree as the session saves it.
    pub fn saved(&self) -> SavedWorktree {
        SavedWorktree {
            info: WorktreeInfo {
                path: self.path.clone(),
                branch: self.branch.clone(),
                base: self.base.clone(),
                created_branch: self.kind != Kind::Existing,
            },
            common_dir: self.common_dir.clone(),
        }
    }
}

/// Resolves the worktree for session `session_id` in `workspace`, on
/// `branch`, or on a new branch named for the session that starts at `HEAD`
/// when there is none. Refuses, having changed nothing, a workspace outside
/// a Git working tree, an existing branch that is not there or is checked
/// out, a new branch whose name is taken, a start point that is not a
/// commit, or a worktree directory in `worktrees_dir` that exists already.
pub async fn plan(
    git: &Git,
    workspace: &Path,
    branch: Option<WorktreeBranch>,
    worktrees_dir: &Path,
    session_id: &str,
) -> Result<WorktreePlan, ErrorBody> {
    let output = git
        .run(
            workspace,
            &[
                &"rev-parse",
                &"--show-toplevel",
                &"--show-prefix",
                &"--path-format=absolute",
                &"--git-common-dir",
            ],
        )
        .await
        .map_err(|error| not_a_working_tree(workspace, error.stderr()))?;
    let mut lines = output.split(|&byte| byte == b'\n');
    let top = PathBuf::from(OsStr::from_bytes(lines.next().unwrap_or_default()));
    let prefix = lines.next().unwrap_or_default();
    let prefix = PathBuf::from(OsStr::from_bytes(
        prefix.strip_suffix(b"/").unwrap_or(prefix),
    ));
    let common_dir = PathBuf::from(OsStr::from_bytes(lines.next().unwrap_or_default()));
    let name = top
        .file_name()
        .ok_or_else(|| not_a_working_tree(workspace, ""))?;

    let id8 = &session_id[..8];
    let mut directory = OsString::from(name);
    directory.push(format!("-{id8}"));
    let path = worktrees_dir.join(directory);
    if path.symlink_metadata().is_ok() {
        return Err(invalid_request(format!(
            "{} already exists",
            path.display()
        )));
    }

    let (branch, kind, start) = match branch {
        None => (format!("ox/{id8}"), Kind::New, None),
        Some(WorktreeBranch::Existing { name }) => {
            let (name, kind) = resolve_existing(git, &top, &common_dir, name).await?;
            (name, kind, None)
        }
        Some(WorktreeBranch::New { name, start }) => {
            check_new(git, &top, &name).await?;
            (name, Kind::New, start)
        }
    };
    let base = match (&kind, start) {
        (Kind::New, Some(start)) => git
            .commit(&top, &start)
            .await
            .ok_or_else(|| invalid_request(format!("{start} is not a commit")))?,
        (Kind::New, None) => git
            .commit(&top, "HEAD")
            .await
            .ok_or_else(|| invalid_request("the repository has no commits".into()))?,
        (Kind::Existing, _) => tip(git, &top, &format!("refs/heads/{branch}")).await?,
        (Kind::Track { remote }, _) => {
            tip(git, &top, &format!("refs/remotes/{remote}/{branch}")).await?
        }
    };
    if !prefix.as_os_str().is_empty() {
        let mut object = OsString::from(format!("{base}:"));
        object.push(prefix.as_os_str());
        if git.run(&top, &[&"cat-file", &"-e", &object]).await.is_err() {
            return Err(invalid_request(format!(
                "{} does not exist at {base}",
                prefix.display()
            )));
        }
    }
    Ok(WorktreePlan {
        cwd: path.join(&prefix),
        top,
        common_dir,
        path,
        branch,
        kind,
        base,
    })
}

/// The refusal of a workspace that `git rev-parse --show-toplevel` finds in
/// no working tree: one outside any repository, in a bare repository, inside
/// a `.git` directory, or in a repository git rejects. `reason` is git's
/// error output, if it gave any.
pub fn not_a_working_tree(workspace: &Path, reason: &str) -> ErrorBody {
    let refusal = format!("{} is not in a Git working tree", workspace.display());
    invalid_request(if reason.is_empty() {
        refusal
    } else {
        format!("{refusal}: {reason}")
    })
}

/// What an existing branch the user named is: a local branch that no
/// worktree has checked out, or a remote branch, named `<branch>` or
/// `<remote>/<branch>`, to track. Refuses a name that is neither.
async fn resolve_existing(
    git: &Git,
    top: &Path,
    common_dir: &Path,
    name: String,
) -> Result<(String, Kind), ErrorBody> {
    check_branch_name(git, top, &name).await?;
    if git
        .commit(top, &format!("refs/heads/{name}"))
        .await
        .is_some()
    {
        if let Some(worktree) = checked_out_at(&Repository::new(git, common_dir), &name).await? {
            return Err(invalid_request(format!(
                "{name} is checked out at {worktree}"
            )));
        }
        return Ok((name, Kind::Existing));
    }

    let remotes = git.text(top, &[&"remote"]).await?;
    let remotes: Vec<&str> = remotes.lines().collect();
    // A remote branch picked by its full name.
    for remote in &remotes {
        if let Some(local) = name.strip_prefix(&format!("{remote}/"))
            && git
                .commit(top, &format!("refs/remotes/{remote}/{local}"))
                .await
                .is_some()
        {
            check_branch_name(git, top, local).await?;
            if git
                .commit(top, &format!("refs/heads/{local}"))
                .await
                .is_some()
            {
                return Err(invalid_request(format!(
                    "a local branch {local} exists; name it instead"
                )));
            }
            let kind = Kind::Track {
                remote: (*remote).to_owned(),
            };
            return Ok((local.to_owned(), kind));
        }
    }

    let mut found = Vec::new();
    for remote in remotes {
        if git
            .commit(top, &format!("refs/remotes/{remote}/{name}"))
            .await
            .is_some()
        {
            found.push(remote);
        }
    }
    let remote = match found[..] {
        [] => {
            return Err(invalid_request(format!(
                "no branch {name} locally or on a remote"
            )));
        }
        [remote] => remote.to_owned(),
        _ => {
            let default = git
                .text(top, &[&"config", &"--get", &"checkout.defaultRemote"])
                .await
                .ok();
            match default.filter(|default| found.contains(&default.as_str())) {
                Some(default) => default,
                None => {
                    return Err(invalid_request(format!(
                        "{name} exists on remotes {}; set checkout.defaultRemote or create \
                         the local branch first",
                        found.join(", ")
                    )));
                }
            }
        }
    };
    Ok((name, Kind::Track { remote }))
}

/// Refuses a new branch name that git does not take as written, or that a
/// local branch, a remote branch's full name `<remote>/<branch>`, or a
/// branch on any remote already has.
async fn check_new(git: &Git, top: &Path, name: &str) -> Result<(), ErrorBody> {
    check_branch_name(git, top, name).await?;
    if git
        .commit(top, &format!("refs/heads/{name}"))
        .await
        .is_some()
    {
        return Err(invalid_request(format!("a branch {name} exists")));
    }
    if git
        .commit(top, &format!("refs/remotes/{name}"))
        .await
        .is_some()
    {
        return Err(invalid_request(format!("a remote branch {name} exists")));
    }
    let remotes = git.text(top, &[&"remote"]).await?;
    for remote in remotes.lines() {
        if git
            .commit(top, &format!("refs/remotes/{remote}/{name}"))
            .await
            .is_some()
        {
            return Err(invalid_request(format!("{name} exists on {remote}")));
        }
    }
    Ok(())
}

/// Refuses a name git does not take as a branch name as written.
async fn check_branch_name(git: &Git, top: &Path, name: &str) -> Result<(), ErrorBody> {
    let checked = git
        .text(top, &[&"check-ref-format", &"--branch", &name])
        .await;
    match checked {
        Ok(checked) if checked == name => Ok(()),
        _ => Err(invalid_request(format!(
            "{name} is not a valid branch name"
        ))),
    }
}

/// The worktree that has local branch `branch` checked out, if any.
async fn checked_out_at(
    repository: &Repository<'_>,
    branch: &str,
) -> Result<Option<String>, GitError> {
    let worktrees = repository
        .text(&[&"worktree", &"list", &"--porcelain"])
        .await?;
    let checked_out = format!("branch refs/heads/{branch}");
    let mut worktree = None;
    for line in worktrees.lines() {
        if let Some(path) = line.strip_prefix("worktree ") {
            worktree = Some(path);
        } else if line == checked_out {
            return Ok(worktree.map(String::from));
        }
    }
    Ok(None)
}

/// The commit a ref that was just found points at.
async fn tip(git: &Git, top: &Path, reference: &str) -> Result<String, ErrorBody> {
    git.commit(top, reference)
        .await
        .ok_or_else(|| invalid_request(format!("{reference} no longer exists")))
}

/// Creates the plan's branch at its base, unless it exists already, with
/// the remote branch as its upstream for **Track**. Creation fails if a
/// branch of that name was created meanwhile.
pub async fn create_branch(git: &Git, plan: &WorktreePlan) -> Result<(), ErrorBody> {
    if plan.kind == Kind::Existing {
        return Ok(());
    }
    let branch = plan.branch.as_str();
    let base = plan.base.as_str();
    git.run(
        &plan.top,
        &[
            &"branch",
            &"--no-track",
            &"--end-of-options",
            &branch,
            &base,
        ],
    )
    .await
    .map_err(|error| invalid_request(error.stderr))?;
    if let Kind::Track { remote } = &plan.kind {
        let upstream = format!("--set-upstream-to=refs/remotes/{remote}/{branch}");
        if let Err(error) = git
            .run(
                &plan.top,
                &[&"branch", &upstream, &"--end-of-options", &branch],
            )
            .await
        {
            let mut error = ErrorBody::from(error);
            if let Err(failure) = roll_back(git, plan, false).await {
                error.message = format!("{}; undoing it failed: {failure}", error.message);
            }
            return Err(error);
        }
    }
    Ok(())
}

/// Adds the plan's worktree on its branch, which exists.
pub async fn add(git: &Git, plan: &WorktreePlan) -> Result<(), String> {
    if let Some(dir) = plan.path.parent() {
        tokio::fs::create_dir_all(dir)
            .await
            .map_err(|error| format!("creating {}: {error}", dir.display()))?;
    }
    git.run(
        &plan.top,
        &[
            &"worktree",
            &"add",
            &"--end-of-options",
            &plan.path,
            &plan.branch,
        ],
    )
    .await
    .map(drop)
    .map_err(|error| error.to_string())
}

/// Adds the worktree again at its saved path, on its saved branch.
pub async fn restore(git: &Git, worktree: &SavedWorktree) -> Result<(), ErrorBody> {
    let path = worktree.info.path.as_path();
    if let Some(dir) = path.parent() {
        tokio::fs::create_dir_all(dir)
            .await
            .map_err(|error| ErrorBody {
                code: code::INVALID_STATE,
                message: format!("creating {}: {error}", dir.display()),
            })?;
    }
    Repository::new(git, &worktree.common_dir)
        .run(&[
            &"worktree",
            &"add",
            &"--end-of-options",
            &path,
            &worktree.info.branch,
        ])
        .await?;
    Ok(())
}

/// Undoes `create_branch`, and a failed `add` when `added` says it ran:
/// removes the worktree if git registered it, and deletes the branch if it
/// was created, still points at its base, and no worktree has it checked
/// out. Returns what could not be undone.
pub async fn roll_back(git: &Git, plan: &WorktreePlan, added: bool) -> Result<(), String> {
    let mut failures = Vec::new();
    if added
        && plan.path.symlink_metadata().is_ok()
        && let Err(error) = git
            .run(&plan.top, &[&"worktree", &"remove", &"--force", &plan.path])
            .await
    {
        failures.push(error.to_string());
    }
    if plan.kind != Kind::Existing {
        // Another session's worktree may have checked the branch out
        // meanwhile, which is what made `add` fail.
        match checked_out_at(&Repository::new(git, &plan.common_dir), &plan.branch).await {
            Ok(Some(_)) => {}
            Ok(None) => {
                if let Err(error) = delete_branch(git, plan).await {
                    failures.push(error);
                }
            }
            Err(error) => failures.push(error.to_string()),
        }
    }
    if failures.is_empty() {
        Ok(())
    } else {
        Err(failures.join("; "))
    }
}

/// Deletes the plan's branch if it still points at its base.
async fn delete_branch(git: &Git, plan: &WorktreePlan) -> Result<(), String> {
    let reference = format!("refs/heads/{}", plan.branch);
    let base = plan.base.as_str();
    git.run(&plan.top, &[&"update-ref", &"-d", &reference, &base])
        .await
        .map(drop)
        .map_err(|error| error.to_string())
}

/// What removing the worktree would lose: uncommitted changes, commits on no
/// branch, and commits of initialized submodules on no remote branch. A
/// worktree git can no longer use, because its repository is gone or no
/// longer knows it, cannot be checked, which is listed too. A worktree
/// directory that no longer exists has nothing to lose.
pub async fn unsaved(git: &Git, worktree: &SavedWorktree) -> Result<Vec<String>, GitError> {
    let path = worktree.info.path.as_path();
    if path.symlink_metadata().is_err() {
        return Ok(Vec::new());
    }
    if let Err(error) = usable(git, path).await {
        let reason = if Repository::new(git, &worktree.common_dir).found().await {
            format!(
                "git no longer recognizes {} as a worktree, so Ox cannot check it for unsaved \
                 work: {}",
                path.display(),
                error.stderr()
            )
        } else {
            format!(
                "the repository of {} no longer exists, so Ox cannot check it for unsaved work",
                path.display()
            )
        };
        return Ok(vec![reason]);
    }
    let mut unsaved = Vec::new();
    // Ignored files are not work to keep.
    let status = git
        .run(
            path,
            &[
                &"--no-optional-locks",
                &"status",
                &"--porcelain",
                &"--untracked-files=all",
                &"--ignore-submodules=none",
            ],
        )
        .await?;
    if !status.is_empty() {
        unsaved.push(format!("uncommitted changes in {}", path.display()));
    }
    let base = worktree.info.base.as_str();
    let detached = git
        .text(
            path,
            &[
                &"rev-list",
                &"--count",
                &"HEAD",
                &"--not",
                &base,
                &"--branches",
                &"--remotes",
                &"--tags",
            ],
        )
        .await?;
    if detached != "0" {
        unsaved.push(format!("commits on no branch in {}", path.display()));
    }
    if path.join(".gitmodules").exists() {
        let submodules = git
            .run(
                path,
                &[
                    &"submodule",
                    &"foreach",
                    &"--quiet",
                    &"--recursive",
                    &"printf '%s\\0' \"$displaypath\"",
                ],
            )
            .await?;
        for submodule in submodules.split(|&byte| byte == 0) {
            if submodule.is_empty() {
                continue;
            }
            let submodule = Path::new(OsStr::from_bytes(submodule));
            let unpushed = git
                .text(
                    &path.join(submodule),
                    &[
                        &"rev-list",
                        &"--count",
                        &"HEAD",
                        &"--branches",
                        &"--not",
                        &"--remotes",
                    ],
                )
                .await?;
            if unpushed != "0" {
                unsaved.push(format!(
                    "submodule {} has commits on no remote branch",
                    submodule.display()
                ));
            }
        }
    }
    Ok(unsaved)
}

/// The refusal to remove a worktree that would lose `unsaved`.
pub fn unsaved_work(unsaved: &[String]) -> ErrorBody {
    ErrorBody {
        code: code::UNSAVED_WORK,
        message: format!("deleting the worktree would lose {}", unsaved.join("; ")),
    }
}

/// Checks that git works in the worktree at `path`, which exists.
async fn usable(git: &Git, path: &Path) -> Result<(), GitError> {
    git.run(path, &[&"rev-parse", &"--git-dir"]).await.map(drop)
}

/// Removes the worktree, ignored files included. Unless `discard` is set, a
/// worktree with anything `unsaved` lists is refused with `UNSAVED_WORK` and
/// left as it is. A worktree git can no longer use is deleted as a
/// directory. A worktree that is gone from disk is unregistered, if its
/// repository still lists it.
pub async fn remove_worktree(
    git: &Git,
    worktree: &SavedWorktree,
    discard: bool,
) -> Result<(), ErrorBody> {
    let path = worktree.info.path.as_path();
    let repository = Repository::new(git, &worktree.common_dir);
    if path.symlink_metadata().is_ok() {
        if !discard {
            let unsaved = unsaved(git, worktree).await?;
            if !unsaved.is_empty() {
                return Err(unsaved_work(&unsaved));
            }
        }
        if usable(git, path).await.is_ok() {
            // The check above stands in for git's own, which also refuses
            // any worktree with initialized submodules.
            remove_registered(&repository, path).await?;
            return Ok(());
        }
        // The directory is the daemon's own, and git cannot use it.
        tokio::fs::remove_dir_all(path)
            .await
            .map_err(|error| ErrorBody {
                code: code::INVALID_STATE,
                message: format!("removing {}: {error}", path.display()),
            })?;
    }
    if repository.found().await && registered(&repository, path).await? {
        remove_registered(&repository, path).await?;
    }
    Ok(())
}

async fn remove_registered(repository: &Repository<'_>, path: &Path) -> Result<(), GitError> {
    repository
        .run(&[&"worktree", &"remove", &"--force", &path])
        .await
        .map(drop)
}

/// Whether the repository lists a worktree at `path`, which no longer
/// exists. Git lists worktrees by their real paths, so `path` is compared
/// with its parent resolved.
async fn registered(repository: &Repository<'_>, path: &Path) -> Result<bool, GitError> {
    let worktrees = repository
        .run(&[&"worktree", &"list", &"--porcelain", &"-z"])
        .await?;
    let resolved = path
        .parent()
        .and_then(|parent| parent.canonicalize().ok())
        .zip(path.file_name())
        .map(|(parent, name)| parent.join(name));
    Ok(worktrees
        .split(|&byte| byte == 0)
        .filter_map(|field| field.strip_prefix(b"worktree "))
        .map(|listed| Path::new(OsStr::from_bytes(listed)))
        .any(|listed| listed == path || resolved.as_deref() == Some(listed)))
}

/// Applies the branch rule to the worktree's branch, in its repository.
/// Returns the branch when it is kept: a branch the daemon created is
/// deleted if every commit on it since the worktree's base is on another
/// branch, a remote branch or a tag, and no worktree has it checked out, and
/// kept otherwise. A branch the daemon cannot check, because its repository
/// is gone, is reported as kept. A branch that existed before the session,
/// and a branch that is gone, leave nothing to do.
pub async fn apply_branch_rule(
    git: &Git,
    worktree: &SavedWorktree,
) -> Result<Option<String>, GitError> {
    let WorktreeInfo {
        branch,
        base,
        created_branch,
        ..
    } = &worktree.info;
    if !created_branch {
        return Ok(None);
    }
    let repository = Repository::new(git, &worktree.common_dir);
    if !repository.found().await {
        return Ok(Some(branch.clone()));
    }
    let reference = format!("refs/heads/{branch}");
    let Some(tip) = repository.commit(&reference).await else {
        return Ok(None);
    };
    let exclude = format!("--exclude={branch}");
    let only_here = repository
        .text(&[
            &"rev-list",
            &"--count",
            &tip,
            &"--not",
            base,
            &exclude,
            &"--branches",
            &"--remotes",
            &"--tags",
        ])
        .await?;
    if only_here != "0" || checked_out_at(&repository, branch).await?.is_some() {
        return Ok(Some(branch.clone()));
    }
    // Deleting only at the tip just read keeps a commit made meanwhile.
    repository
        .run(&[&"update-ref", &"-d", &reference, &tip])
        .await?;
    Ok(None)
}
