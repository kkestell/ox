#![cfg(unix)]

//! Sessions that run in worktrees, against real repositories.

use std::collections::BTreeMap;
use std::io::Read;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::atomic::{AtomicU64, Ordering};

use ox_protocol::schema::{ContentBlock, TextContent};
use ox_protocol::{
    Branches, Checkout, Client, Deleted, Error, ErrorBody, EventKind, SessionInfo, SessionState,
    WorktreeInfo, code,
};
use tempfile::TempDir;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, Command};
use tokio::sync::oneshot;
use tokio::task::JoinHandle;
use tokio::time::{Duration, timeout};

const TIMEOUT: Duration = Duration::from_secs(10);
const OXD: &str = env!("CARGO_BIN_EXE_oxd");
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_oxd-fake-agent");

/// Runs git in `dir` without the user's or the system's configuration, and
/// returns its output without the final newline.
fn git(dir: &Path, args: &[&str]) -> String {
    let output = git_command(dir, args).output().unwrap();
    assert!(
        output.status.success(),
        "git {args:?} failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    String::from_utf8(output.stdout)
        .unwrap()
        .trim_end()
        .to_owned()
}

/// Whether git succeeds in `dir`.
fn git_succeeds(dir: &Path, args: &[&str]) -> bool {
    git_command(dir, args).output().unwrap().status.success()
}

fn git_command(dir: &Path, args: &[&str]) -> std::process::Command {
    let mut command = std::process::Command::new("git");
    command
        .arg("-C")
        .arg(dir)
        .args(args)
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .stdin(Stdio::null());
    command
}

/// A repository on branch `main` with one commit, and an identity to commit
/// with.
fn repo() -> TempDir {
    let dir = tempfile::tempdir().unwrap();
    init(dir.path());
    std::fs::write(dir.path().join("README"), "committed\n").unwrap();
    git(dir.path(), &["add", "README"]);
    git(dir.path(), &["commit", "--quiet", "-m", "first"]);
    dir
}

fn init(dir: &Path) {
    git(dir, &["init", "--quiet", "-b", "main"]);
    identify(dir);
}

/// Gives the repository at `dir` an identity, which git also writes in
/// reflogs.
fn identify(dir: &Path) {
    git(dir, &["config", "user.name", "Test"]);
    git(dir, &["config", "user.email", "test@example.com"]);
}

/// A bare remote with branches `main` and `feature`, and a clone of it with
/// local `main` only and an identity, in `clone` under the returned
/// directory.
fn clone_with_remote() -> TempDir {
    let source = repo();
    git(source.path(), &["switch", "--quiet", "-c", "feature"]);
    std::fs::write(source.path().join("feature"), "feature\n").unwrap();
    git(source.path(), &["add", "feature"]);
    git(source.path(), &["commit", "--quiet", "-m", "feature"]);
    git(source.path(), &["switch", "--quiet", "main"]);
    let dir = tempfile::tempdir().unwrap();
    let remote = dir.path().join("remote.git");
    let clone = dir.path().join("clone");
    git(
        dir.path(),
        &[
            "clone",
            "--quiet",
            "--bare",
            source.path().to_str().unwrap(),
            remote.to_str().unwrap(),
        ],
    );
    git(
        dir.path(),
        &[
            "clone",
            "--quiet",
            remote.to_str().unwrap(),
            clone.to_str().unwrap(),
        ],
    );
    identify(&clone);
    dir
}

fn clone_of(dir: &TempDir) -> PathBuf {
    dir.path().join("clone")
}

struct Daemon {
    child: Child,
    dir: TempDir,
    socket: PathBuf,
}

impl Daemon {
    async fn start() -> Self {
        Self::start_in(tempfile::tempdir().unwrap()).await
    }

    /// Starts oxd on the socket in `dir`, without the user's or the
    /// system's git configuration, and waits for it to report that it is
    /// ready.
    async fn start_in(dir: TempDir) -> Self {
        Self::start_with(dir, BTreeMap::new()).await
    }

    /// Starts oxd as `start_in` does, with `env` over its environment.
    async fn start_with(dir: TempDir, env: BTreeMap<String, String>) -> Self {
        let socket = dir.path().join("oxd.sock");
        let mut child = Command::new(OXD)
            .arg("--socket")
            .arg(&socket)
            .arg("--idle-exit-ms")
            .arg("1000")
            .env("GIT_CONFIG_GLOBAL", "/dev/null")
            .env("GIT_CONFIG_NOSYSTEM", "1")
            .envs(env)
            .stdout(Stdio::piped())
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        let mut stdout = BufReader::new(child.stdout.take().unwrap()).lines();
        let ready = timeout(TIMEOUT, stdout.next_line()).await;
        assert_eq!(ready.unwrap().unwrap().as_deref(), Some("ready"));
        Self { child, dir, socket }
    }

    async fn client(&self) -> Client {
        timeout(TIMEOUT, Client::connect(&self.socket))
            .await
            .expect("timed out connecting")
            .unwrap()
    }

    /// Stops the daemon with SIGTERM, which keeps its sessions saved, and
    /// starts another on its socket.
    async fn restart(self) -> Self {
        self.restart_with(BTreeMap::new()).await
    }

    /// Restarts oxd as `restart` does, with `env` over its environment.
    async fn restart_with(mut self, env: BTreeMap<String, String>) -> Self {
        // SAFETY: sends SIGTERM to the daemon this test started.
        unsafe { libc::kill(self.child.id().unwrap().cast_signed(), libc::SIGTERM) };
        let status = timeout(TIMEOUT, self.child.wait())
            .await
            .expect("oxd did not exit")
            .unwrap();
        assert!(status.success());
        Self::start_with(self.dir, env).await
    }

    fn worktrees(&self) -> PathBuf {
        self.dir.path().join("worktrees")
    }
}

/// A `git` for a client's `PATH` that stops at the first command whose
/// arguments contain `pattern`, until the test releases it, and otherwise
/// runs git.
struct BlockedGit {
    dir: TempDir,
}

impl BlockedGit {
    /// Stops before the command runs.
    fn new(pattern: &str) -> Self {
        Self::stopping(pattern, false)
    }

    /// Stops once the command has run, before oxd sees it finish.
    fn after(pattern: &str) -> Self {
        Self::stopping(pattern, true)
    }

    fn stopping(pattern: &str, after: bool) -> Self {
        let dir = tempfile::tempdir().unwrap();
        mkfifos(dir.path());
        let real = std::process::Command::new("sh")
            .args(["-c", "command -v git"])
            .output()
            .unwrap();
        let real = String::from_utf8(real.stdout).unwrap();
        let real = real.trim();
        let dir_path = dir.path().to_str().unwrap();
        let stop = format!(
            "case \" $* \" in\n\
             *\"{pattern}\"*)\n\
             if mkdir \"{dir_path}/once\" 2>/dev/null; then\n\
             echo blocked > \"{dir_path}/blocked\"\n\
             read _ < \"{dir_path}/release\"\n\
             fi ;;\n\
             esac\n"
        );
        let script = if after {
            format!("#!/bin/sh\n{real} \"$@\"\nstatus=$?\n{stop}exit $status\n")
        } else {
            format!("#!/bin/sh\n{stop}exec {real} \"$@\"\n")
        };
        let git = dir.path().join("git");
        std::fs::write(&git, script).unwrap();
        std::fs::set_permissions(&git, PermissionsExt::from_mode(0o755)).unwrap();
        Self { dir }
    }

    /// A client environment whose `PATH` finds this `git` first.
    fn env(&self) -> BTreeMap<String, String> {
        let path = std::env::var("PATH").unwrap();
        BTreeMap::from([(
            "PATH".to_owned(),
            format!("{}:{path}", self.dir.path().display()),
        )])
    }

    /// Waits until a git command has stopped.
    async fn blocked(&self) {
        blocked(self.dir.path()).await;
    }

    /// Lets the stopped git command run.
    async fn release(&self) {
        release(self.dir.path()).await;
    }
}

/// Makes the FIFOs `blocked` and `release` in `dir`, through which a script
/// says it has stopped and the test lets it go on.
fn mkfifos(dir: &Path) {
    for fifo in ["blocked", "release"] {
        let status = std::process::Command::new("mkfifo")
            .arg(dir.join(fifo))
            .status()
            .unwrap();
        assert!(status.success());
    }
}

/// Waits until the script using the FIFOs in `dir` has stopped.
async fn blocked(dir: &Path) {
    let fifo = dir.join("blocked");
    let read = tokio::task::spawn_blocking(move || std::fs::read_to_string(fifo));
    let read = timeout(TIMEOUT, read)
        .await
        .expect("the script never stopped");
    assert_eq!(read.unwrap().unwrap(), "blocked\n");
}

/// Lets the script using the FIFOs in `dir` go on.
async fn release(dir: &Path) {
    let fifo = dir.join("release");
    let write = tokio::task::spawn_blocking(move || std::fs::write(fifo, "go\n"));
    timeout(TIMEOUT, write)
        .await
        .expect("the script was not waiting")
        .unwrap()
        .unwrap();
}

/// An upload-pack for a clone's `origin` that counts the fetches that reach
/// it, and stops the first until the test releases it.
struct UploadPack {
    dir: TempDir,
}

impl UploadPack {
    /// Makes `origin` of the clone at `clone` fetch through this
    /// upload-pack.
    fn new(clone: &Path) -> Self {
        let dir = tempfile::tempdir().unwrap();
        mkfifos(dir.path());
        let dir_path = dir.path().to_str().unwrap();
        let script = format!(
            "#!/bin/sh\n\
             echo fetch >> \"{dir_path}/log\"\n\
             if mkdir \"{dir_path}/once\" 2>/dev/null; then\n\
             echo blocked > \"{dir_path}/blocked\"\n\
             read _ < \"{dir_path}/release\"\n\
             fi\n\
             exec git upload-pack \"$@\"\n"
        );
        let upload_pack = dir.path().join("upload-pack");
        std::fs::write(&upload_pack, script).unwrap();
        std::fs::set_permissions(&upload_pack, PermissionsExt::from_mode(0o755)).unwrap();
        git(
            clone,
            &[
                "config",
                "remote.origin.uploadpack",
                upload_pack.to_str().unwrap(),
            ],
        );
        Self { dir }
    }

    /// Waits until the first fetch has reached the remote.
    async fn blocked(&self) {
        blocked(self.dir.path()).await;
    }

    /// Lets the first fetch go on.
    async fn release(&self) {
        release(self.dir.path()).await;
    }

    /// How many fetches have reached the remote.
    fn fetches(&self) -> usize {
        std::fs::read_to_string(self.dir.path().join("log"))
            .unwrap()
            .lines()
            .count()
    }
}

/// A FIFO the fake agent holds open for as long as it runs (`--hold`), which
/// tells the test when the agent starts and when it exits.
struct ExitWatch {
    path: PathBuf,
    opened: Option<oneshot::Receiver<()>>,
    done: Option<JoinHandle<()>>,
}

impl ExitWatch {
    fn new(dir: &Path) -> Self {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        let path = dir.join(format!("hold-{}", NEXT.fetch_add(1, Ordering::Relaxed)));
        let status = std::process::Command::new("mkfifo")
            .arg(&path)
            .status()
            .unwrap();
        assert!(status.success());
        let (opened, opened_rx) = oneshot::channel();
        let reader = path.clone();
        // Opening blocks until the agent opens the FIFO; reading ends once
        // every process holding it has exited.
        let done = tokio::task::spawn_blocking(move || {
            let mut fifo = std::fs::File::open(reader).unwrap();
            let _ = opened.send(());
            let mut rest = Vec::new();
            fifo.read_to_end(&mut rest).unwrap();
        });
        Self {
            path,
            opened: Some(opened_rx),
            done: Some(done),
        }
    }

    /// The fake agent's command, holding this FIFO.
    fn command(&self) -> Vec<String> {
        vec![
            FAKE_AGENT.to_owned(),
            "--hold".to_owned(),
            self.path.to_str().unwrap().to_owned(),
        ]
    }

    /// Waits for the agent process to start.
    async fn started(&mut self) {
        let opened = self.opened.take().unwrap();
        timeout(TIMEOUT, opened)
            .await
            .expect("the agent did not start")
            .unwrap();
    }

    /// Waits for the agent process to exit.
    async fn exited(mut self) {
        let done = self.done.take().unwrap();
        timeout(TIMEOUT, done)
            .await
            .expect("the agent is still running")
            .unwrap();
    }
}

impl Drop for ExitWatch {
    /// Ends the blocking reader if no agent ever opened the FIFO, so a
    /// failing test does not hang.
    fn drop(&mut self) {
        let _ = std::fs::OpenOptions::new()
            .write(true)
            .custom_flags(libc::O_NONBLOCK)
            .open(&self.path);
    }
}

/// The ID of the workspace for `path`, which is added if there is none.
async fn workspace_id(client: &Client, path: &Path) -> String {
    let workspaces = client.list_workspaces().await.unwrap();
    match workspaces.into_iter().find(|w| w.path == path) {
        Some(workspace) => workspace.id,
        None => client.add_workspace(path.to_owned()).await.unwrap().id,
    }
}

/// The branches of the workspace for `path`.
async fn branches(
    client: &Client,
    path: &Path,
    fetch: bool,
    env: BTreeMap<String, String>,
) -> Result<Branches, Error> {
    let workspace = workspace_id(client, path).await;
    timeout(TIMEOUT, client.branches(&workspace, fetch, env))
        .await
        .expect("timed out listing branches")
}

fn in_worktree(branch: Option<&str>, start: Option<&str>) -> Checkout {
    Checkout::Worktree {
        branch: branch.map(String::from),
        start: start.map(String::from),
    }
}

/// Asks for a session of the fake agent in a workspace for `path`.
async fn new_session(
    client: &Client,
    path: &Path,
    checkout: Checkout,
) -> Result<SessionInfo, Error> {
    new_session_with(client, path, checkout, BTreeMap::new()).await
}

async fn new_session_with(
    client: &Client,
    path: &Path,
    checkout: Checkout,
    env: BTreeMap<String, String>,
) -> Result<SessionInfo, Error> {
    new_session_running(client, path, checkout, env, vec![FAKE_AGENT.into()]).await
}

async fn new_session_running(
    client: &Client,
    path: &Path,
    checkout: Checkout,
    env: BTreeMap<String, String>,
    command: Vec<String>,
) -> Result<SessionInfo, Error> {
    let workspace = workspace_id(client, path).await;
    client.new_session(&workspace, command, env, checkout).await
}

/// Starts a session of the fake agent holding `agent` in a new worktree of
/// the repository at `path` on `branch`, and waits for the agent to start.
async fn started_holding(
    client: &Client,
    path: &Path,
    branch: Option<&str>,
    agent: &mut ExitWatch,
) -> SessionInfo {
    let session = new_session_running(
        client,
        path,
        in_worktree(branch, None),
        BTreeMap::new(),
        agent.command(),
    )
    .await
    .unwrap();
    let session = started(client, &session).await;
    agent.started().await;
    session
}

/// Deletes a session, with git run with the daemon's environment.
async fn delete(client: &Client, session_id: &str, discard: bool) -> Result<Deleted, Error> {
    timeout(TIMEOUT, client.delete(session_id, discard, BTreeMap::new()))
        .await
        .expect("timed out deleting the session")
}

/// Commits a new file in the checkout at `dir`.
fn commit_file(dir: &Path, name: &str) {
    std::fs::write(dir.join(name), format!("{name}\n")).unwrap();
    git(dir, &["add", name]);
    git(dir, &["commit", "--quiet", "-m", name]);
}

fn branch_exists(dir: &Path, branch: &str) -> bool {
    git_succeeds(
        dir,
        &[
            "rev-parse",
            "--verify",
            "--quiet",
            &format!("refs/heads/{branch}"),
        ],
    )
}

/// Asserts that the worktree is gone from disk and from the repository at
/// `repo`.
fn assert_removed(repo: &Path, worktree: &WorktreeInfo) {
    assert!(
        !worktree.path.exists(),
        "{} exists",
        worktree.path.display()
    );
    let listed = worktree_paths(repo);
    let name = worktree.path.file_name().unwrap();
    assert!(
        listed.iter().all(|path| path.file_name() != Some(name)),
        "{listed:?}"
    );
}

/// Waits for the agent to answer a prompt, which shows it is running.
async fn answers(client: &Client, session_id: &str) {
    let mut events = client.subscribe(session_id, 0).await.unwrap();
    client
        .prompt(
            session_id,
            vec![ContentBlock::Text(TextContent::new("say 1 0"))],
        )
        .await
        .unwrap();
    loop {
        let event = timeout(TIMEOUT, events.next())
            .await
            .expect("the agent did not answer")
            .unwrap();
        if let EventKind::TurnEnded { error, .. } = event.kind {
            assert_eq!(error, None);
            return;
        }
    }
}

/// Waits until the session is no longer listed.
async fn until_gone(client: &Client, session_id: &str) {
    let mut watch = client.watch().await.unwrap();
    loop {
        let listing = timeout(TIMEOUT, watch.next())
            .await
            .expect("timed out waiting for the session to go")
            .unwrap();
        if listing
            .sessions
            .iter()
            .all(|session| session.id != session_id)
        {
            return;
        }
    }
}

/// Waits until the session is in `state`.
async fn until_state(client: &Client, session_id: &str, state: SessionState) {
    let mut watch = client.watch().await.unwrap();
    loop {
        let listing = timeout(TIMEOUT, watch.next())
            .await
            .expect("timed out waiting for the session's state")
            .unwrap();
        let session = listing
            .sessions
            .iter()
            .find(|session| session.id == session_id)
            .expect("the session is listed");
        if session.state == state {
            return;
        }
    }
}

/// Waits for the session's agent to start and returns the session.
async fn started(client: &Client, session: &SessionInfo) -> SessionInfo {
    assert_eq!(session.state, SessionState::Starting);
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let event = timeout(TIMEOUT, events.next())
        .await
        .expect("timed out waiting for the agent")
        .unwrap();
    assert!(
        matches!(event.kind, EventKind::AgentStarted { .. }),
        "{event:?}"
    );
    info(client, &session.id).await.unwrap()
}

/// Waits for the session's startup to fail and returns its error.
async fn exited(client: &Client, session: &SessionInfo) -> String {
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let event = timeout(TIMEOUT, events.next())
        .await
        .expect("timed out waiting for the session to exit")
        .unwrap();
    match event.kind {
        EventKind::AgentExited { error: Some(error) } => error,
        kind => panic!("expected agent_exited with an error, got {kind:?}"),
    }
}

async fn info(client: &Client, session_id: &str) -> Option<SessionInfo> {
    client
        .list()
        .await
        .unwrap()
        .into_iter()
        .find(|session| session.id == session_id)
}

fn refusal<T: std::fmt::Debug>(result: Result<T, Error>) -> ErrorBody {
    match result {
        Err(Error::Remote(body)) => body,
        other => panic!("expected a daemon error, got {other:?}"),
    }
}

fn session_worktree(session: &SessionInfo) -> &WorktreeInfo {
    session
        .worktree
        .as_ref()
        .expect("the session has a worktree")
}

/// The paths `git worktree list` shows for the repository at `dir`.
fn worktree_paths(dir: &Path) -> Vec<PathBuf> {
    git(dir, &["worktree", "list", "--porcelain"])
        .lines()
        .filter_map(|line| line.strip_prefix("worktree "))
        .map(PathBuf::from)
        .collect()
}

fn canonical(path: &Path) -> PathBuf {
    path.canonicalize().unwrap()
}

#[tokio::test(flavor = "multi_thread")]
async fn a_session_runs_in_a_new_worktree_on_a_new_branch() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let status = git(repo.path(), &["status", "--porcelain", "--ignored"]);
    let head = git(repo.path(), &["rev-parse", "HEAD"]);

    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let answered = session_worktree(&session).clone();
    let session = started(&client, &session).await;

    let id8 = &session.id[..8];
    let name = canonical(repo.path());
    let name = name.file_name().unwrap().to_str().unwrap();
    let path = daemon.worktrees().join(format!("{name}-{id8}"));
    assert_eq!(session.cwd, path);
    assert_eq!(
        session.worktree,
        Some(WorktreeInfo {
            path: path.clone(),
            branch: format!("ox/{id8}"),
            base: head.clone(),
            created_branch: true,
        })
    );
    assert_eq!(answered, *session_worktree(&session));
    assert_eq!(git(repo.path(), &["rev-parse", &format!("ox/{id8}")]), head);
    assert_eq!(
        git(&path, &["branch", "--show-current"]),
        format!("ox/{id8}")
    );
    assert!(worktree_paths(repo.path()).contains(&canonical(&path)));
    assert_eq!(
        git(repo.path(), &["status", "--porcelain", "--ignored"]),
        status
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_named_new_branch_starts_where_asked_without_an_upstream() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let session = new_session(
        &client,
        &clone,
        in_worktree(Some("topic"), Some("origin/feature")),
    )
    .await
    .unwrap();
    let session = started(&client, &session).await;
    let feature = git(&clone, &["rev-parse", "origin/feature"]);
    let worktree = session_worktree(&session);
    assert_eq!(worktree.branch, "topic");
    assert_eq!(worktree.base, feature);
    assert!(worktree.created_branch);
    assert_eq!(git(&clone, &["rev-parse", "topic"]), feature);
    assert!(!git_succeeds(
        &clone,
        &["config", "--get", "branch.topic.remote"]
    ));
}

#[tokio::test(flavor = "multi_thread")]
async fn an_existing_branch_is_checked_out_unless_a_worktree_has_it() {
    let repo = repo();
    git(repo.path(), &["branch", "other"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let session = new_session(&client, repo.path(), in_worktree(Some("other"), None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session);
    assert_eq!(worktree.branch, "other");
    assert!(!worktree.created_branch);
    assert_eq!(worktree.base, git(repo.path(), &["rev-parse", "other"]));
    assert_eq!(git(&worktree.path, &["branch", "--show-current"]), "other");

    let refused = refusal(new_session(&client, repo.path(), in_worktree(Some("main"), None)).await);
    assert_eq!(refused.code, code::INVALID_REQUEST);
    let top = canonical(repo.path());
    assert!(
        refused.message.contains(top.to_str().unwrap()),
        "{refused:?}"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_branch_only_on_a_remote_is_tracked() {
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    // By its name.
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let session = new_session(&client, &clone, in_worktree(Some("feature"), None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session);
    assert_eq!(worktree.branch, "feature");
    assert!(worktree.created_branch);
    assert_eq!(worktree.base, git(&clone, &["rev-parse", "origin/feature"]));
    assert_eq!(
        git(&clone, &["config", "--get", "branch.feature.remote"]),
        "origin"
    );
    // Named by its remote, once the local branch exists.
    let refused =
        refusal(new_session(&client, &clone, in_worktree(Some("origin/feature"), None)).await);
    assert_eq!(refused.code, code::INVALID_REQUEST);
    assert!(refused.message.contains("feature"), "{refused:?}");

    // By its remote and name.
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let session = new_session(&client, &clone, in_worktree(Some("origin/feature"), None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    assert_eq!(session_worktree(&session).branch, "feature");
    assert_eq!(
        git(&clone, &["config", "--get", "branch.feature.remote"]),
        "origin"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_branch_on_several_remotes_follows_the_default_remote() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let remote = dir.path().join("remote.git");
    git(
        &clone,
        &["remote", "add", "upstream", remote.to_str().unwrap()],
    );
    git(&clone, &["fetch", "--quiet", "upstream"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let refused = refusal(new_session(&client, &clone, in_worktree(Some("feature"), None)).await);
    assert_eq!(refused.code, code::INVALID_REQUEST);
    assert!(
        refused.message.contains("origin") && refused.message.contains("upstream"),
        "{refused:?}"
    );

    // The setting is only in the configuration the client's environment
    // names, so git runs with that environment.
    let config = dir.path().join("gitconfig");
    std::fs::write(&config, "[checkout]\n\tdefaultRemote = upstream\n").unwrap();
    let env = BTreeMap::from([(
        "GIT_CONFIG_GLOBAL".to_owned(),
        config.to_str().unwrap().to_owned(),
    )]);
    let session = new_session_with(&client, &clone, in_worktree(Some("feature"), None), env)
        .await
        .unwrap();
    started(&client, &session).await;
    assert_eq!(
        git(&clone, &["config", "--get", "branch.feature.remote"]),
        "upstream"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_start_point_is_refused_for_a_branch_that_exists() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    git(&clone, &["branch", "other"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    for branch in ["other", "feature", "origin/feature"] {
        let refused =
            refusal(new_session(&client, &clone, in_worktree(Some(branch), Some("HEAD"))).await);
        assert_eq!(refused.code, code::INVALID_REQUEST, "{branch}");
    }
    assert!(client.list().await.unwrap().is_empty());
    assert_eq!(git(&clone, &["branch", "--list"]), "* main\n  other");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_subdirectory_workspace_runs_in_the_same_subdirectory() {
    let repo = repo();
    let first = git(repo.path(), &["rev-parse", "HEAD"]);
    std::fs::create_dir(repo.path().join("sub")).unwrap();
    std::fs::write(repo.path().join("sub/file"), "in sub\n").unwrap();
    git(repo.path(), &["add", "sub"]);
    git(repo.path(), &["commit", "--quiet", "-m", "sub"]);
    let sub = canonical(&repo.path().join("sub"));
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let session = new_session(&client, &sub, in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session);
    assert_eq!(session.cwd, worktree.path.join("sub"));
    assert!(session.cwd.join("file").is_file());

    let branches = git(repo.path(), &["branch", "--list"]);
    let refused = refusal(new_session(&client, &sub, in_worktree(Some("old"), Some(&first))).await);
    assert_eq!(refused.code, code::INVALID_REQUEST);
    assert!(refused.message.contains("sub"), "{refused:?}");
    assert_eq!(git(repo.path(), &["branch", "--list"]), branches);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_request_git_cannot_satisfy_is_refused_with_nothing_created() {
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let plain = tempfile::tempdir().unwrap();
    let refused = refusal(new_session(&client, plain.path(), in_worktree(None, None)).await);
    assert_eq!(refused.code, code::INVALID_REQUEST);
    assert!(
        refused.message.contains("not in a Git working tree"),
        "{refused:?}"
    );

    let bare = tempfile::tempdir().unwrap();
    git(bare.path(), &["init", "--quiet", "--bare"]);
    let refused = refusal(new_session(&client, bare.path(), in_worktree(None, None)).await);
    assert_eq!(refused.code, code::INVALID_REQUEST);
    assert!(
        refused.message.contains("not in a Git working tree"),
        "{refused:?}"
    );

    let repo = repo();
    let branches = git(repo.path(), &["branch", "--list"]);
    let worktrees = git(repo.path(), &["worktree", "list"]);
    for checkout in [
        in_worktree(Some("-x"), None),
        in_worktree(Some("HEAD"), None),
        in_worktree(Some("a..b"), None),
        in_worktree(Some("topic"), Some("nonexistent")),
        in_worktree(None, Some("--output=/tmp/x")),
    ] {
        let refused = refusal(new_session(&client, repo.path(), checkout.clone()).await);
        assert_eq!(refused.code, code::INVALID_REQUEST, "{checkout:?}");
    }
    assert_eq!(git(repo.path(), &["branch", "--list"]), branches);
    assert_eq!(git(repo.path(), &["worktree", "list"]), worktrees);

    let unborn = tempfile::tempdir().unwrap();
    init(unborn.path());
    let refused = refusal(new_session(&client, unborn.path(), in_worktree(None, None)).await);
    assert_eq!(refused.code, code::INVALID_REQUEST);
    assert!(refused.message.contains("no commits"), "{refused:?}");
    assert_eq!(git(unborn.path(), &["branch", "--list"]), "");

    assert!(client.list().await.unwrap().is_empty());
    assert!(!daemon.worktrees().exists());
}

#[tokio::test(flavor = "multi_thread")]
async fn of_two_creations_of_one_branch_one_wins() {
    let repo = repo();
    let head = git(repo.path(), &["rev-parse", "HEAD"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    // Add the workspace first, so the two requests share it.
    client.add_workspace(repo.path().to_owned()).await.unwrap();

    let (first, second) = tokio::join!(
        new_session(&client, repo.path(), in_worktree(Some("same"), None)),
        new_session(&client, repo.path(), in_worktree(Some("same"), None)),
    );
    let (winner, loser) = match (first, second) {
        (Ok(winner), loser @ Err(_)) | (loser @ Err(_), Ok(winner)) => (winner, loser),
        other => panic!("expected one creation to succeed, got {other:?}"),
    };
    assert_eq!(refusal(loser).code, code::INVALID_REQUEST);
    let winner = started(&client, &winner).await;
    assert_eq!(git(repo.path(), &["rev-parse", "same"]), head);
    assert!(session_worktree(&winner).path.is_dir());
    assert_eq!(client.list().await.unwrap().len(), 1);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_worktree_that_cannot_be_added_is_undone() {
    let repo = repo();
    let daemon = Daemon::start().await;
    std::fs::create_dir(daemon.worktrees()).unwrap();
    std::fs::set_permissions(daemon.worktrees(), PermissionsExt::from_mode(0o555)).unwrap();
    let client = daemon.client().await;

    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    assert_eq!(session.state, SessionState::Starting);
    let branch = session_worktree(&session).branch.clone();
    let error = exited(&client, &session).await;
    assert!(error.contains("worktree add"), "{error}");
    let after = info(&client, &session.id).await.unwrap();
    assert_eq!(after.state, SessionState::Exited);
    assert_eq!(after.worktree, None);
    assert_eq!(git(repo.path(), &["branch", "--list", &branch]), "");
    std::fs::set_permissions(daemon.worktrees(), PermissionsExt::from_mode(0o755)).unwrap();

    drop(client);
    let daemon = daemon.restart().await;
    let client = daemon.client().await;
    assert!(client.list().await.unwrap().is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_linked_worktree_workspace_names_the_worktree_after_itself() {
    let repo = repo();
    let linked_dir = tempfile::tempdir().unwrap();
    let linked = linked_dir.path().join("linked");
    git(
        repo.path(),
        &[
            "worktree",
            "add",
            "--quiet",
            "-b",
            "linked",
            linked.to_str().unwrap(),
        ],
    );
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let session = new_session(&client, &linked, in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let name = session_worktree(&session).path.file_name().unwrap();
    assert_eq!(
        name.to_str().unwrap(),
        format!("linked-{}", &session.id[..8])
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn uncommitted_changes_stay_in_the_workspace() {
    let repo = repo();
    std::fs::write(repo.path().join("README"), "changed\n").unwrap();
    std::fs::write(repo.path().join("untracked"), "new\n").unwrap();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    assert_eq!(
        std::fs::read_to_string(session.cwd.join("README")).unwrap(),
        "committed\n"
    );
    assert!(!session.cwd.join("untracked").exists());
}

#[tokio::test(flavor = "multi_thread")]
async fn git_ignores_the_clients_repository_variables() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let env = BTreeMap::from([("GIT_DIR".to_owned(), "/nonexistent".to_owned())]);

    let session = new_session_with(&client, repo.path(), in_worktree(None, None), env)
        .await
        .unwrap();
    let session = started(&client, &session).await;
    assert!(session_worktree(&session).path.is_dir());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_worktree_session_survives_a_restart_and_loads_only_while_its_directory_exists() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    drop(client);

    let daemon = daemon.restart().await;
    let client = daemon.client().await;
    let restored = info(&client, &session.id).await.unwrap();
    assert_eq!(restored.state, SessionState::Exited);
    assert_eq!(restored.worktree, session.worktree);
    assert_eq!(restored.cwd, session.cwd);

    std::fs::remove_dir_all(&session.cwd).unwrap();
    let error = client
        .load_session(&session.id, BTreeMap::new())
        .await
        .unwrap_err();
    let Error::Remote(refused) = error else {
        panic!("expected a daemon error, got {error}");
    };
    assert_eq!(refused.code, code::INVALID_STATE);
    assert!(
        refused.message.contains(session.cwd.to_str().unwrap()),
        "{refused:?}"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_failed_worktree_leaves_a_branch_another_worktree_checked_out() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let blocked = BlockedGit::new(" worktree add ");

    let session = new_session_with(
        &client,
        repo.path(),
        in_worktree(Some("topic"), None),
        blocked.env(),
    )
    .await
    .unwrap();
    blocked.blocked().await;
    // Another worktree checks the new branch out before the session's
    // worktree is added, as a second session asking for it can.
    let other = tempfile::tempdir().unwrap();
    let other = other.path().join("other");
    git(
        repo.path(),
        &[
            "worktree",
            "add",
            "--quiet",
            other.to_str().unwrap(),
            "topic",
        ],
    );
    blocked.release().await;

    let error = exited(&client, &session).await;
    assert!(error.contains("worktree add"), "{error}");
    assert_eq!(info(&client, &session.id).await.unwrap().worktree, None);
    assert!(git_succeeds(
        repo.path(),
        &["rev-parse", "--verify", "--quiet", "refs/heads/topic"]
    ));
    assert_eq!(git(&other, &["branch", "--show-current"]), "topic");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_tracking_branch_starts_at_the_commit_it_records() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let base = git(&clone, &["rev-parse", "origin/feature"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let blocked = BlockedGit::new(" branch ");

    let creating = new_session_with(
        &client,
        &clone,
        in_worktree(Some("feature"), None),
        blocked.env(),
    );
    let moving = async {
        blocked.blocked().await;
        // A fetch moves the remote branch after it was resolved.
        let tree = format!("{base}^{{tree}}");
        let newer = git(&clone, &["commit-tree", &tree, "-p", &base, "-m", "newer"]);
        git(
            &clone,
            &["update-ref", "refs/remotes/origin/feature", &newer],
        );
        blocked.release().await;
    };
    let (session, ()) = tokio::join!(creating, moving);
    let session = started(&client, &session.unwrap()).await;

    let worktree = session_worktree(&session);
    assert_eq!(worktree.base, base);
    assert_eq!(git(&clone, &["rev-parse", "feature"]), base);
    assert_eq!(
        git(&clone, &["config", "--get", "branch.feature.remote"]),
        "origin"
    );
    assert_eq!(
        git(&clone, &["config", "--get", "branch.feature.merge"]),
        "refs/heads/feature"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_tracking_branch_whose_upstream_cannot_be_set_is_undone() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let blocked = BlockedGit::new(" --set-upstream-to");

    let creating = new_session_with(
        &client,
        &clone,
        in_worktree(Some("feature"), None),
        blocked.env(),
    );
    let removing = async {
        blocked.blocked().await;
        // The remote branch goes away between creating the local branch and
        // setting its upstream, as a pruning fetch can make it.
        git(&clone, &["update-ref", "-d", "refs/remotes/origin/feature"]);
        blocked.release().await;
    };
    let (refused, ()) = tokio::join!(creating, removing);
    let refused = refusal(refused);
    assert_eq!(refused.code, code::GIT, "{refused:?}");
    assert!(!git_succeeds(
        &clone,
        &["rev-parse", "--verify", "--quiet", "refs/heads/feature"]
    ));
    assert!(client.list().await.unwrap().is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_workspace_removed_during_a_creation() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let workspace = client.add_workspace(repo.path().to_owned()).await.unwrap();

    // Removed before the session is saved: the creation is refused and its
    // branch deleted.
    let blocked = BlockedGit::new(" branch --no-track ");
    let creating = new_session_with(&client, repo.path(), in_worktree(None, None), blocked.env());
    let removing = async {
        blocked.blocked().await;
        client.remove_workspace(&workspace.id).await.unwrap();
        blocked.release().await;
    };
    let (refused, ()) = tokio::join!(creating, removing);
    let refused = refusal(refused);
    assert_eq!(refused.code, code::NOT_FOUND, "{refused:?}");
    assert_eq!(git(repo.path(), &["branch", "--list"]), "* main");
    assert!(client.list().await.unwrap().is_empty());
    assert!(client.list_workspaces().await.unwrap().is_empty());

    // Removed once the session is saved: the removal is refused.
    let workspace = client.add_workspace(repo.path().to_owned()).await.unwrap();
    let blocked = BlockedGit::new(" worktree add ");
    let session = new_session_with(&client, repo.path(), in_worktree(None, None), blocked.env())
        .await
        .unwrap();
    blocked.blocked().await;
    let error = client.remove_workspace(&workspace.id).await.unwrap_err();
    let Error::Remote(refused) = error else {
        panic!("expected a daemon error, got {error}");
    };
    assert_eq!(refused.code, code::INVALID_STATE);
    blocked.release().await;
    started(&client, &session).await;
    assert_eq!(client.list_workspaces().await.unwrap(), [workspace]);
}

#[tokio::test(flavor = "multi_thread")]
async fn branches_are_listed_most_recently_committed_first() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    // A branch committed after `main`, which sorts after it by name.
    git(&clone, &["switch", "--quiet", "-c", "newer"]);
    let committed = git_command(
        &clone,
        &["commit", "--quiet", "--allow-empty", "-m", "newer"],
    )
    .env("GIT_COMMITTER_DATE", "2090-01-01T00:00:00Z")
    .status()
    .unwrap();
    assert!(committed.success());
    git(&clone, &["switch", "--quiet", "main"]);
    assert!(git_succeeds(
        &clone,
        &["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"]
    ));
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let listed = branches(&client, &clone, false, BTreeMap::new())
        .await
        .unwrap();
    assert_eq!(listed.local, ["newer", "main"]);
    let mut remote = listed.remote.clone();
    remote.sort();
    assert_eq!(remote, ["origin/feature", "origin/main"]);
    assert_eq!(listed.fetch_error, None);

    // A subdirectory of the checkout lists the same branches.
    std::fs::create_dir(clone.join("sub")).unwrap();
    let from_subdirectory = branches(&client, &clone.join("sub"), false, BTreeMap::new())
        .await
        .unwrap();
    assert_eq!(from_subdirectory, listed);
}

#[tokio::test(flavor = "multi_thread")]
async fn branches_are_refused_outside_a_working_tree() {
    let dir = clone_with_remote();
    let plain = tempfile::tempdir().unwrap();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    for path in [plain.path(), &dir.path().join("remote.git")] {
        for fetch in [false, true] {
            let refused = refusal(branches(&client, path, fetch, BTreeMap::new()).await);
            assert_eq!(refused.code, code::INVALID_REQUEST, "{refused:?}");
            assert!(refused.message.contains("is not in a Git working tree"));
        }
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn a_fetch_brings_in_new_remote_branches() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    git(&dir.path().join("remote.git"), &["branch", "later", "main"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let unfetched = branches(&client, &clone, false, BTreeMap::new())
        .await
        .unwrap();
    assert!(!unfetched.remote.contains(&"origin/later".to_owned()));
    let fetched = branches(&client, &clone, true, BTreeMap::new())
        .await
        .unwrap();
    assert_eq!(fetched.fetch_error, None);
    assert!(fetched.remote.contains(&"origin/later".to_owned()));
    assert_eq!(fetched.local, unfetched.local);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_failed_fetch_is_reported_beside_the_branches_as_they_stand() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let missing = dir.path().join("missing.git");
    git(
        &clone,
        &["config", "remote.origin.url", missing.to_str().unwrap()],
    );
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let unfetched = branches(&client, &clone, false, BTreeMap::new())
        .await
        .unwrap();
    let fetched = branches(&client, &clone, true, BTreeMap::new())
        .await
        .unwrap();
    let error = fetched.fetch_error.expect("the fetch failed");
    assert!(error.contains("missing.git"), "{error}");
    assert_eq!(fetched.local, unfetched.local);
    assert_eq!(fetched.remote, unfetched.remote);
}

#[tokio::test(flavor = "multi_thread")]
async fn requests_for_one_repository_share_its_fetch() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let upload_pack = UploadPack::new(&clone);
    // Shows when the second request has found the repository it is in,
    // after which it looks for a fetch running.
    let found = BlockedGit::after(" rev-parse ");
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let workspace = workspace_id(&client, &clone).await;

    let first = client.branches(&workspace, true, BTreeMap::new());
    let second = async {
        upload_pack.blocked().await;
        let second = client.branches(&workspace, true, found.env());
        let releasing = async {
            found.blocked().await;
            found.release().await;
            upload_pack.release().await;
        };
        let (second, ()) = tokio::join!(second, releasing);
        second
    };
    let (first, second) = timeout(TIMEOUT, async { tokio::join!(first, second) })
        .await
        .expect("timed out listing branches");
    assert_eq!(first.unwrap().fetch_error, None);
    assert_eq!(second.unwrap().fetch_error, None);
    assert_eq!(upload_pack.fetches(), 1);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_fetch_runs_with_the_clients_environment() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let remote = git(&clone, &["config", "--get", "remote.origin.url"]);
    let config = dir.path().join("gitconfig");
    let missing = dir.path().join("missing.git");
    std::fs::write(
        &config,
        format!("[url \"{}\"]\n\tinsteadOf = {remote}\n", missing.display()),
    )
    .unwrap();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    let env = BTreeMap::from([(
        "GIT_CONFIG_GLOBAL".to_owned(),
        config.to_str().unwrap().to_owned(),
    )]);
    let redirected = branches(&client, &clone, true, env).await.unwrap();
    let error = redirected.fetch_error.expect("the fetch failed");
    assert!(error.contains("missing.git"), "{error}");
    let fetched = branches(&client, &clone, true, BTreeMap::new())
        .await
        .unwrap();
    assert_eq!(fetched.fetch_error, None);
}

#[tokio::test(flavor = "multi_thread")]
async fn workspaces_say_whether_they_are_in_a_git_working_tree() {
    let repo = repo();
    std::fs::create_dir(repo.path().join("sub")).unwrap();
    let plain = tempfile::tempdir().unwrap();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    for path in [repo.path().to_owned(), repo.path().join("sub")] {
        let workspace = client.add_workspace(path).await.unwrap();
        assert!(workspace.git_working_tree, "{workspace:?}");
    }
    let workspace = client
        .add_workspace(repo.path().join(".git"))
        .await
        .unwrap();
    assert!(!workspace.git_working_tree);
    client.remove_workspace(&workspace.id).await.unwrap();
    let workspace = client.add_workspace(plain.path().to_owned()).await.unwrap();
    assert!(!workspace.git_working_tree);
    let flags = |workspaces: &[ox_protocol::WorkspaceInfo]| {
        workspaces
            .iter()
            .map(|workspace| workspace.git_working_tree)
            .collect::<Vec<_>>()
    };
    let mut watch = client.watch().await.unwrap();
    let listing = timeout(TIMEOUT, watch.next()).await.unwrap().unwrap();
    assert_eq!(flags(&listing.workspaces), [true, true, false]);

    // The flag follows `git init` in the next listing oxd sends, such as the
    // one a client connecting brings.
    init(plain.path());
    let _connected = daemon.client().await;
    let listing = timeout(TIMEOUT, watch.next())
        .await
        .expect("no listing followed the connection")
        .unwrap();
    assert_eq!(flags(&listing.workspaces), [true, true, true]);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_git_directory_git_rejects_is_refused_with_gits_reason() {
    let dir = tempfile::tempdir().unwrap();
    std::fs::write(
        dir.path().join(".git"),
        format!("gitdir: {}\n", dir.path().join("missing").display()),
    )
    .unwrap();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let workspace = client.add_workspace(dir.path().to_owned()).await.unwrap();
    assert!(workspace.git_working_tree);

    // Git words the reason differently across versions; the refusal carries
    // whatever this git says.
    let output = git_command(dir.path(), &["rev-parse", "--show-toplevel"])
        .output()
        .unwrap();
    assert!(!output.status.success());
    let reason = String::from_utf8(output.stderr).unwrap().trim().to_owned();
    assert!(!reason.is_empty());

    let refused = refusal(new_session(&client, dir.path(), in_worktree(None, None)).await);
    let listing = refusal(branches(&client, dir.path(), false, BTreeMap::new()).await);
    for refused in [refused, listing] {
        assert_eq!(refused.code, code::INVALID_REQUEST, "{refused:?}");
        assert!(
            refused
                .message
                .ends_with(&format!(" is not in a Git working tree: {reason}")),
            "{refused:?}"
        );
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn a_clean_delete_removes_the_worktree_and_the_branch() {
    let repo = repo();
    std::fs::write(repo.path().join(".gitignore"), "ignored\n").unwrap();
    git(repo.path(), &["add", ".gitignore"]);
    git(repo.path(), &["commit", "--quiet", "-m", "ignore"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let mut agent = ExitWatch::new(daemon.dir.path());
    let session = started_holding(&client, repo.path(), None, &mut agent).await;
    let worktree = session_worktree(&session).clone();
    // Ignored files go with the worktree.
    std::fs::write(worktree.path.join("ignored"), "build output\n").unwrap();
    let status = git(repo.path(), &["status", "--porcelain", "--ignored"]);
    let head = git(repo.path(), &["rev-parse", "HEAD"]);

    let deleted = delete(&client, &session.id, false).await.unwrap();
    assert_eq!(deleted, Deleted { kept_branch: None });
    agent.exited().await;
    assert_removed(repo.path(), &worktree);
    assert!(!branch_exists(repo.path(), &worktree.branch));
    assert_eq!(git(repo.path(), &["branch", "--list"]), "* main");
    assert_eq!(git(repo.path(), &["rev-parse", "HEAD"]), head);
    assert_eq!(
        git(repo.path(), &["status", "--porcelain", "--ignored"]),
        status
    );
    assert!(client.list().await.unwrap().is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_branch_merged_elsewhere_is_deleted_and_one_with_its_own_commits_is_kept() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    // Merged into `main`.
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    commit_file(&worktree.path, "merged");
    git(
        repo.path(),
        &["merge", "--quiet", "--ff-only", &worktree.branch],
    );
    let deleted = delete(&client, &session.id, false).await.unwrap();
    assert_eq!(deleted.kept_branch, None);
    assert_removed(repo.path(), &worktree);
    assert!(!branch_exists(repo.path(), &worktree.branch));

    // Its commit is on no other branch, also when the delete discards.
    for discard in [false, true] {
        let session = new_session(&client, repo.path(), in_worktree(None, None))
            .await
            .unwrap();
        let session = started(&client, &session).await;
        let worktree = session_worktree(&session).clone();
        commit_file(&worktree.path, if discard { "discarded" } else { "kept" });
        let tip = git(&worktree.path, &["rev-parse", "HEAD"]);
        let deleted = delete(&client, &session.id, discard).await.unwrap();
        assert_eq!(
            deleted.kept_branch.as_deref(),
            Some(worktree.branch.as_str())
        );
        assert_removed(repo.path(), &worktree);
        assert_eq!(git(repo.path(), &["rev-parse", &worktree.branch]), tip);
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn a_branch_that_existed_before_the_session_is_never_deleted() {
    let repo = repo();
    git(repo.path(), &["branch", "other"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(Some("other"), None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    commit_file(&worktree.path, "new");
    let tip = git(&worktree.path, &["rev-parse", "HEAD"]);

    let deleted = delete(&client, &session.id, false).await.unwrap();
    assert_eq!(deleted.kept_branch, None);
    assert_removed(repo.path(), &worktree);
    assert_eq!(git(repo.path(), &["rev-parse", "other"]), tip);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_tracking_branch_is_kept_until_its_commits_are_pushed() {
    let dir = clone_with_remote();
    let clone = clone_of(&dir);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;

    for push in [true, false] {
        let session = new_session(&client, &clone, in_worktree(Some("feature"), None))
            .await
            .unwrap();
        let session = started(&client, &session).await;
        let worktree = session_worktree(&session).clone();
        assert!(worktree.created_branch);
        commit_file(&worktree.path, if push { "pushed" } else { "unpushed" });
        if push {
            git(&worktree.path, &["push", "--quiet"]);
        }
        let deleted = delete(&client, &session.id, false).await.unwrap();
        assert_removed(&clone, &worktree);
        if push {
            assert_eq!(deleted.kept_branch, None);
            assert!(!branch_exists(&clone, "feature"));
        } else {
            assert_eq!(deleted.kept_branch.as_deref(), Some("feature"));
            assert!(branch_exists(&clone, "feature"));
        }
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn uncommitted_changes_refuse_the_delete_until_it_discards_them() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let mut agent = ExitWatch::new(daemon.dir.path());
    let session = started_holding(&client, repo.path(), None, &mut agent).await;
    let worktree = session_worktree(&session).clone();
    std::fs::write(worktree.path.join("untracked"), "work\n").unwrap();

    let refused = refusal(delete(&client, &session.id, false).await);
    assert_eq!(refused.code, code::UNSAVED_WORK, "{refused:?}");
    assert!(
        refused.message.contains("uncommitted changes")
            && refused.message.contains(worktree.path.to_str().unwrap()),
        "{refused:?}"
    );
    // Nothing was stopped or removed.
    answers(&client, &session.id).await;
    assert!(worktree.path.join("untracked").is_file());
    assert!(info(&client, &session.id).await.is_some());

    let deleted = delete(&client, &session.id, true).await.unwrap();
    assert_eq!(deleted.kept_branch, None);
    agent.exited().await;
    assert_removed(repo.path(), &worktree);
    assert!(!branch_exists(repo.path(), &worktree.branch));
    assert!(client.list().await.unwrap().is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn commits_on_no_branch_refuse_the_delete() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    git(&worktree.path, &["switch", "--quiet", "--detach"]);
    commit_file(&worktree.path, "detached");

    let refused = refusal(delete(&client, &session.id, false).await);
    assert_eq!(refused.code, code::UNSAVED_WORK, "{refused:?}");
    assert!(
        refused.message.contains("commits on no branch"),
        "{refused:?}"
    );
    assert!(worktree.path.is_dir());
}

#[tokio::test(flavor = "multi_thread")]
async fn submodule_commits_on_no_remote_branch_refuse_the_delete() {
    let submodule = repo();
    let repo = repo();
    git(
        repo.path(),
        &[
            "-c",
            "protocol.file.allow=always",
            "submodule",
            "add",
            "--quiet",
            submodule.path().to_str().unwrap(),
            "sub",
        ],
    );
    git(repo.path(), &["commit", "--quiet", "-m", "submodule"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let start = async || {
        let session = new_session(&client, repo.path(), in_worktree(None, None))
            .await
            .unwrap();
        let session = started(&client, &session).await;
        let worktree = session_worktree(&session).clone();
        git(
            &worktree.path,
            &[
                "-c",
                "protocol.file.allow=always",
                "submodule",
                "update",
                "--quiet",
                "--init",
            ],
        );
        (session, worktree)
    };

    // Initialized and clean.
    let (session, worktree) = start().await;
    delete(&client, &session.id, false).await.unwrap();
    assert_removed(repo.path(), &worktree);

    // A commit in the submodule that its remote does not have, recorded in
    // the session's branch, so nothing else is unsaved.
    let (session, worktree) = start().await;
    let inner = worktree.path.join("sub");
    identify(&inner);
    commit_file(&inner, "unpushed");
    git(&worktree.path, &["commit", "--quiet", "-am", "bump"]);
    let refused = refusal(delete(&client, &session.id, false).await);
    assert_eq!(refused.code, code::UNSAVED_WORK, "{refused:?}");
    assert_eq!(
        refused.message,
        "deleting the worktree would lose submodule sub has commits on no remote branch"
    );
    assert!(inner.is_dir());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_worktree_deleted_outside_ox_is_unregistered() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    std::fs::remove_dir_all(&worktree.path).unwrap();
    assert!(
        worktree_paths(repo.path())
            .contains(&canonical(&daemon.worktrees()).join(worktree.path.file_name().unwrap()))
    );

    let deleted = delete(&client, &session.id, false).await.unwrap();
    assert_eq!(deleted.kept_branch, None);
    assert_removed(repo.path(), &worktree);
    assert!(!branch_exists(repo.path(), &worktree.branch));
}

#[tokio::test(flavor = "multi_thread")]
async fn a_worktree_whose_repository_moved_is_removed_only_with_discard() {
    let parent = tempfile::tempdir().unwrap();
    let path = parent.path().join("repo");
    std::fs::create_dir(&path).unwrap();
    init(&path);
    commit_file(&path, "README");
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, &path, in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    std::fs::rename(&path, parent.path().join("moved")).unwrap();

    let refused = refusal(delete(&client, &session.id, false).await);
    assert_eq!(refused.code, code::UNSAVED_WORK, "{refused:?}");
    assert!(refused.message.contains("repository"), "{refused:?}");
    assert!(worktree.path.is_dir());

    // Ox cannot check the branch in a repository it cannot find, and the
    // branch is still there in the moved one.
    let deleted = delete(&client, &session.id, true).await.unwrap();
    assert_eq!(
        deleted.kept_branch.as_deref(),
        Some(worktree.branch.as_str())
    );
    assert!(!worktree.path.exists());
    assert!(client.list().await.unwrap().is_empty());
    assert!(branch_exists(
        &parent.path().join("moved"),
        &worktree.branch
    ));
}

#[tokio::test(flavor = "multi_thread")]
async fn a_locked_worktree_fails_the_delete_and_the_session_loads_again() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    // The fake agent resumes a session from the log a prompt writes in the
    // worktree, which the delete has to discard.
    answers(&client, &session.id).await;
    git(
        repo.path(),
        &["worktree", "lock", worktree.path.to_str().unwrap()],
    );

    let refused = refusal(delete(&client, &session.id, true).await);
    assert_eq!(refused.code, code::GIT, "{refused:?}");
    let after = info(&client, &session.id).await.unwrap();
    assert_eq!(after.state, SessionState::Exited);
    assert!(worktree.path.is_dir());

    git(
        repo.path(),
        &["worktree", "unlock", worktree.path.to_str().unwrap()],
    );
    client
        .load_session(&session.id, BTreeMap::new())
        .await
        .unwrap();
    until_state(&client, &session.id, SessionState::Idle).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn a_branch_another_worktree_has_checked_out_is_kept() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    git(&worktree.path, &["switch", "--quiet", "--detach"]);
    let other = tempfile::tempdir().unwrap();
    let other = other.path().join("other");
    git(
        repo.path(),
        &[
            "worktree",
            "add",
            "--quiet",
            other.to_str().unwrap(),
            &worktree.branch,
        ],
    );

    let deleted = delete(&client, &session.id, false).await.unwrap();
    assert_eq!(
        deleted.kept_branch.as_deref(),
        Some(worktree.branch.as_str())
    );
    assert_removed(repo.path(), &worktree);
    assert_eq!(git(&other, &["branch", "--show-current"]), worktree.branch);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_delete_during_startup_waits_for_the_worktree() {
    let repo = repo();
    git(repo.path(), &["branch", "other"]);
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let blocked = BlockedGit::new(" worktree add ");
    let session = new_session_with(
        &client,
        repo.path(),
        in_worktree(Some("other"), None),
        blocked.env(),
    )
    .await
    .unwrap();
    let worktree = session_worktree(&session).clone();
    blocked.blocked().await;

    let releasing = async { blocked.release().await };
    let (deleted, ()) = tokio::join!(delete(&client, &session.id, false), releasing);
    assert_eq!(deleted.unwrap().kept_branch, None);
    assert_removed(repo.path(), &worktree);
    assert!(branch_exists(repo.path(), "other"));
    assert!(client.list().await.unwrap().is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn startup_removes_the_worktrees_of_sessions_that_never_started() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let missing = vec![
        daemon
            .dir
            .path()
            .join("missing")
            .to_str()
            .unwrap()
            .to_owned(),
    ];
    let mut sessions = Vec::new();
    for _ in 0..2 {
        let session = new_session_running(
            &client,
            repo.path(),
            in_worktree(None, None),
            BTreeMap::new(),
            missing.clone(),
        )
        .await
        .unwrap();
        exited(&client, &session).await;
        sessions.push(info(&client, &session.id).await.unwrap());
    }
    let clean = session_worktree(&sessions[0]).clone();
    let dirty = session_worktree(&sessions[1]).clone();
    std::fs::write(dirty.path.join("untracked"), "work\n").unwrap();
    drop(client);

    let daemon = daemon.restart().await;
    let client = daemon.client().await;
    until_gone(&client, &sessions[0].id).await;
    assert_removed(repo.path(), &clean);
    assert!(!branch_exists(repo.path(), &clean.branch));

    let kept = info(&client, &sessions[1].id).await.unwrap();
    assert_eq!(kept.state, SessionState::Exited);
    assert_eq!(kept.worktree.as_ref(), Some(&dirty));
    let refused = refusal(client.load_session(&kept.id, BTreeMap::new()).await);
    assert_eq!(refused.code, code::INVALID_STATE, "{refused:?}");
    delete(&client, &kept.id, true).await.unwrap();
    assert_removed(repo.path(), &dirty);
    assert!(!branch_exists(repo.path(), &dirty.branch));
    assert!(client.list().await.unwrap().is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn startup_leaves_a_branch_named_like_a_worktree_that_could_not_be_added() {
    let repo = repo();
    let daemon = Daemon::start().await;
    std::fs::create_dir(daemon.worktrees()).unwrap();
    std::fs::set_permissions(daemon.worktrees(), PermissionsExt::from_mode(0o555)).unwrap();
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(Some("topic"), None))
        .await
        .unwrap();
    exited(&client, &session).await;
    std::fs::set_permissions(daemon.worktrees(), PermissionsExt::from_mode(0o755)).unwrap();
    assert!(!branch_exists(repo.path(), "topic"));
    // A branch of the same name, made after the session's was undone, is
    // not the session's to delete.
    git(repo.path(), &["branch", "topic"]);
    drop(client);

    let daemon = daemon.restart().await;
    let client = daemon.client().await;
    assert!(client.list().await.unwrap().is_empty());
    assert!(branch_exists(repo.path(), "topic"));
}

#[tokio::test(flavor = "multi_thread")]
async fn a_session_whose_workspace_subdirectory_is_gone_is_deleted() {
    let repo = repo();
    std::fs::create_dir(repo.path().join("sub")).unwrap();
    std::fs::write(repo.path().join("sub/file"), "in sub\n").unwrap();
    git(repo.path(), &["add", "sub"]);
    git(repo.path(), &["commit", "--quiet", "-m", "sub"]);
    let sub = canonical(&repo.path().join("sub"));
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let mut sessions = Vec::new();
    for _ in 0..2 {
        let session = new_session(&client, &sub, in_worktree(None, None))
            .await
            .unwrap();
        sessions.push(started(&client, &session).await);
    }
    let untouched = session_worktree(&sessions[0]).clone();
    let committed = session_worktree(&sessions[1]).clone();
    commit_file(&committed.path, "work");
    std::fs::remove_dir_all(&sub).unwrap();

    let deleted = delete(&client, &sessions[0].id, false).await.unwrap();
    assert_eq!(deleted.kept_branch, None);
    assert_removed(repo.path(), &untouched);
    assert!(!branch_exists(repo.path(), &untouched.branch));

    let deleted = delete(&client, &sessions[1].id, false).await.unwrap();
    assert_eq!(
        deleted.kept_branch.as_deref(),
        Some(committed.branch.as_str())
    );
    assert_removed(repo.path(), &committed);
    assert!(branch_exists(repo.path(), &committed.branch));
}

#[tokio::test(flavor = "multi_thread")]
async fn a_worktree_whose_registration_was_removed_is_removed_only_with_discard() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    let admin = git(&worktree.path, &["rev-parse", "--absolute-git-dir"]);
    std::fs::remove_dir_all(admin).unwrap();

    let refused = refusal(delete(&client, &session.id, false).await);
    assert_eq!(refused.code, code::UNSAVED_WORK, "{refused:?}");
    assert!(
        refused.message.contains("no longer recognizes"),
        "{refused:?}"
    );
    assert!(worktree.path.is_dir());

    delete(&client, &session.id, true).await.unwrap();
    assert!(!worktree.path.exists());
    assert!(client.list().await.unwrap().is_empty());
}

/// Makes a worktree session of an agent that cannot start, in the repository
/// at `path`, and returns it once it has exited, with its worktree in place.
async fn unstarted(daemon: &Daemon, client: &Client, path: &Path) -> SessionInfo {
    let missing = daemon.dir.path().join("missing");
    let session = new_session_running(
        client,
        path,
        in_worktree(None, None),
        BTreeMap::new(),
        vec![missing.to_str().unwrap().to_owned()],
    )
    .await
    .unwrap();
    exited(client, &session).await;
    info(client, &session.id).await.unwrap()
}

#[tokio::test(flavor = "multi_thread")]
async fn startup_cleanup_runs_after_ready_and_holds_the_session() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = unstarted(&daemon, &client, repo.path()).await;
    let worktree = session_worktree(&session).clone();
    drop(client);

    let blocked = BlockedGit::new(" worktree remove ");
    // `restart_with` returns once oxd reports `ready`.
    let daemon = daemon.restart_with(blocked.env()).await;
    blocked.blocked().await;
    let client = daemon.client().await;
    let listed = info(&client, &session.id).await.unwrap();
    assert_eq!(listed.state, SessionState::Exited);

    let loading = client.load_session(&session.id, BTreeMap::new());
    let releasing = async { blocked.release().await };
    let (loaded, ()) = tokio::join!(loading, releasing);
    assert_eq!(refusal(loaded).code, code::NOT_FOUND);
    until_gone(&client, &session.id).await;
    assert_removed(repo.path(), &worktree);
    assert!(!branch_exists(repo.path(), &worktree.branch));
}

#[tokio::test(flavor = "multi_thread")]
async fn the_daemon_stays_up_until_startup_cleanup_ends() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    unstarted(&daemon, &client, repo.path()).await;
    drop(client);

    let blocked = BlockedGit::new(" worktree remove ");
    let mut daemon = daemon.restart_with(blocked.env()).await;
    blocked.blocked().await;
    // Two idle periods pass with no client while cleanup is held.
    let waited = timeout(Duration::from_secs(2), daemon.child.wait()).await;
    assert!(waited.is_err(), "oxd exited during cleanup: {waited:?}");
    blocked.release().await;
    let status = timeout(TIMEOUT, daemon.child.wait())
        .await
        .expect("oxd did not exit once idle")
        .unwrap();
    assert!(status.success());
}

fn text(prompt: &str) -> Vec<ContentBlock> {
    vec![ContentBlock::Text(TextContent::new(prompt))]
}

async fn archive(client: &Client, session_id: &str, discard: bool) -> Result<SessionInfo, Error> {
    timeout(
        TIMEOUT,
        client.archive(session_id, discard, BTreeMap::new()),
    )
    .await
    .expect("timed out archiving the session")
}

async fn unarchive(client: &Client, session_id: &str) -> Result<SessionInfo, Error> {
    timeout(TIMEOUT, client.unarchive(session_id, BTreeMap::new()))
        .await
        .expect("timed out unarchiving the session")
}

/// Subscribes to the session after `seq` and returns its events up to the one
/// `last` accepts.
async fn events_until(
    client: &Client,
    session_id: &str,
    seq: u64,
    last: impl Fn(&EventKind) -> bool,
) -> Vec<ox_protocol::Event> {
    let mut events = client.subscribe(session_id, seq).await.unwrap();
    let mut seen = Vec::new();
    loop {
        let event = timeout(TIMEOUT, events.next())
            .await
            .expect("timed out waiting for an event")
            .unwrap();
        let done = last(&event.kind);
        seen.push(event);
        if done {
            return seen;
        }
    }
}

/// A session of the fake agent in a new worktree that has had one turn, with
/// the agent's transcript committed on its branch so that a resumed agent
/// finds it in the restored worktree.
async fn session_with_a_turn(
    client: &Client,
    repo: &Path,
) -> (SessionInfo, Vec<ox_protocol::Event>) {
    let session = new_session(client, repo, in_worktree(None, None))
        .await
        .unwrap();
    let session = started(client, &session).await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    client.prompt(&session.id, text("say 1 0")).await.unwrap();
    let mut seen = Vec::new();
    loop {
        let event = timeout(TIMEOUT, events.next())
            .await
            .expect("the agent did not answer")
            .unwrap();
        let done = matches!(event.kind, EventKind::TurnEnded { .. });
        seen.push(event);
        if done {
            break;
        }
    }
    let worktree = session_worktree(&session);
    git(&worktree.path, &["add", "."]);
    git(&worktree.path, &["commit", "--quiet", "-m", "transcript"]);
    (session, seen)
}

#[tokio::test(flavor = "multi_thread")]
async fn archiving_removes_the_worktree_and_keeps_the_branch_and_the_conversation() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let (session, turn) = session_with_a_turn(&client, repo.path()).await;
    let worktree = session_worktree(&session).clone();

    let archived = archive(&client, &session.id, false).await.unwrap();
    assert!(archived.archived);
    assert_eq!(archived.state, SessionState::Exited);
    assert_removed(repo.path(), &worktree);
    assert!(branch_exists(repo.path(), &worktree.branch));
    assert!(info(&client, &session.id).await.unwrap().archived);

    let parked = |kind: &EventKind| matches!(kind, EventKind::AgentParked);
    let history = events_until(&client, &session.id, 0, parked).await;
    assert!(history.len() > turn.len());
    assert_eq!(history[..turn.len()], turn);
    drop(client);

    let daemon = daemon.restart().await;
    let client = daemon.client().await;
    let restored = info(&client, &session.id).await.unwrap();
    assert!(restored.archived);
    assert_eq!(restored.state, SessionState::Exited);
    assert_eq!(events_until(&client, &session.id, 0, parked).await, history);
    assert_eq!(
        info(&client, &session.id).await.unwrap().state,
        SessionState::Exited
    );

    // An archived session cannot run.
    let refused = refusal(client.load_session(&session.id, BTreeMap::new()).await);
    assert_eq!(refused.code, code::INVALID_STATE);
    assert!(refused.message.contains("archived"), "{refused:?}");
    refusal(client.prompt(&session.id, text("say 1 0")).await);
    let refused = refusal(archive(&client, &session.id, false).await);
    assert_eq!(refused.code, code::INVALID_STATE);

    // Unarchiving brings the worktree back on its branch, and the session
    // resumes without replaying its history.
    let unarchived = unarchive(&client, &session.id).await.unwrap();
    assert!(!unarchived.archived);
    assert_eq!(unarchived.state, SessionState::Exited);
    assert_eq!(
        git(&worktree.path, &["branch", "--show-current"]),
        worktree.branch
    );
    client
        .load_session(&session.id, BTreeMap::new())
        .await
        .unwrap();
    let last = history.last().unwrap().seq;
    let resumed = events_until(&client, &session.id, last, |kind| {
        matches!(
            kind,
            EventKind::AgentStarted { .. } | EventKind::AgentExited { .. }
        )
    })
    .await;
    assert_eq!(resumed.len(), 1, "{resumed:?}");
    assert!(matches!(resumed[0].kind, EventKind::AgentStarted { .. }));
    let next = resumed[0].seq;
    let mut events = client.subscribe(&session.id, next).await.unwrap();
    client.prompt(&session.id, text("say 1 0")).await.unwrap();
    loop {
        let event = timeout(TIMEOUT, events.next())
            .await
            .expect("the agent did not answer")
            .unwrap();
        if let EventKind::TurnEnded { error, .. } = event.kind {
            assert_eq!(error, None);
            break;
        }
    }

    let refused = refusal(unarchive(&client, &session.id).await);
    assert_eq!(refused.code, code::INVALID_STATE);
}

#[tokio::test(flavor = "multi_thread")]
async fn archiving_refuses_unsaved_work_unless_it_is_discarded() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let mut agent = ExitWatch::new(daemon.dir.path());
    let session = started_holding(&client, repo.path(), None, &mut agent).await;
    let worktree = session_worktree(&session).clone();
    std::fs::write(worktree.path.join("unsaved"), "work\n").unwrap();

    let refused = refusal(archive(&client, &session.id, false).await);
    assert_eq!(refused.code, code::UNSAVED_WORK);
    assert!(refused.message.contains("uncommitted"), "{refused:?}");
    assert!(worktree.path.join("unsaved").exists());
    let listed = info(&client, &session.id).await.unwrap();
    assert!(!listed.archived);
    assert_eq!(listed.state, SessionState::Idle);

    let archived = archive(&client, &session.id, true).await.unwrap();
    assert!(archived.archived);
    agent.exited().await;
    assert_removed(repo.path(), &worktree);
    assert!(branch_exists(repo.path(), &worktree.branch));
}

#[tokio::test(flavor = "multi_thread")]
async fn archiving_a_running_session_ends_its_turn() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    client.prompt(&session.id, text("wait")).await.unwrap();
    until_state(&client, &session.id, SessionState::Running).await;

    archive(&client, &session.id, true).await.unwrap();
    loop {
        let event = timeout(TIMEOUT, events.next())
            .await
            .expect("the turn did not end")
            .unwrap();
        if let EventKind::TurnEnded { error, .. } = event.kind {
            assert_eq!(error.as_deref(), Some("the agent was stopped"));
            break;
        }
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn unarchiving_needs_a_branch_git_can_check_out() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    for deleted in [true, false] {
        let session = new_session(&client, repo.path(), in_worktree(None, None))
            .await
            .unwrap();
        let session = started(&client, &session).await;
        let worktree = session_worktree(&session).clone();
        archive(&client, &session.id, true).await.unwrap();
        if deleted {
            git(repo.path(), &["branch", "-D", &worktree.branch]);
        } else {
            git(repo.path(), &["checkout", "--quiet", &worktree.branch]);
        }

        let refused = refusal(unarchive(&client, &session.id).await);
        assert_eq!(refused.code, code::GIT, "{refused:?}");
        assert!(info(&client, &session.id).await.unwrap().archived);
        assert!(!worktree.path.exists());
        git(repo.path(), &["checkout", "--quiet", "main"]);
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn deleting_an_archived_session_removes_it_and_applies_the_branch_rule() {
    let repo = repo();
    let daemon = Daemon::start().await;
    let client = daemon.client().await;
    let session = new_session(&client, repo.path(), in_worktree(None, None))
        .await
        .unwrap();
    let session = started(&client, &session).await;
    let worktree = session_worktree(&session).clone();
    archive(&client, &session.id, true).await.unwrap();

    let deleted = delete(&client, &session.id, false).await.unwrap();
    assert_eq!(deleted, Deleted { kept_branch: None });
    assert!(info(&client, &session.id).await.is_none());
    assert!(!branch_exists(repo.path(), &worktree.branch));
    assert_removed(repo.path(), &worktree);
}
