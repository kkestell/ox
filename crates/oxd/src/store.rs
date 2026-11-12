//! The daemon's saved workspaces and sessions, in one `SQLite` database.

use std::path::{Path, PathBuf};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Mutex, MutexGuard};

use ox_protocol::{Event, EventKind, WorktreeInfo};
use rusqlite::{Connection, params};
use tokio::sync::oneshot;

/// A workspace as the database holds it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SavedWorkspace {
    pub id: String,
    pub path: PathBuf,
}

/// A session as the database holds it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SavedSession {
    pub number: u64,
    pub id: String,
    pub workspace_id: String,
    pub command: Vec<String>,
    pub cwd: PathBuf,
    pub title: Option<String>,
    pub agent_session_id: Option<String>,
    pub worktree: Option<SavedWorktree>,
    /// The seq of the session's last stored event, 0 if it has none.
    pub last_seq: u64,
    /// Whether the session is archived.
    pub archived: bool,
}

/// A session's worktree as the database holds it: what clients see, and the
/// repository it belongs to.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SavedWorktree {
    pub info: WorktreeInfo,
    /// The repository's absolute common Git directory.
    pub common_dir: PathBuf,
}

/// What the event writer thread is sent.
enum Write {
    Events(Vec<Event>),
    /// Acknowledged once everything sent before it is committed.
    Flush(oneshot::Sender<()>),
}

/// One connection to the database. Every query is a small local read or
/// write, so it runs inline. Events are written by a thread of their own, so
/// recording one never waits on the database.
pub struct Store {
    connection: Arc<Mutex<Connection>>,
    writer: Sender<Write>,
}

impl Store {
    /// Opens the database at `path`, creating it and its tables if they do
    /// not exist. The schema has no migrations: a schema change means
    /// deleting the database.
    pub fn open(path: &Path) -> rusqlite::Result<Self> {
        Self::with(Connection::open(path)?)
    }

    fn with(connection: Connection) -> rusqlite::Result<Self> {
        connection.execute_batch(
            "PRAGMA journal_mode = WAL;
            PRAGMA synchronous = NORMAL;
            PRAGMA auto_vacuum = FULL;
            PRAGMA foreign_keys = ON;
            CREATE TABLE IF NOT EXISTS workspaces (
                id   TEXT PRIMARY KEY,
                path TEXT NOT NULL UNIQUE
            );
            CREATE TABLE IF NOT EXISTS sessions (
                number           INTEGER PRIMARY KEY AUTOINCREMENT,
                id               TEXT NOT NULL UNIQUE,
                workspace_id     TEXT NOT NULL REFERENCES workspaces(id),
                command          TEXT NOT NULL,  -- JSON array
                cwd              TEXT NOT NULL,
                title            TEXT,
                agent_session_id TEXT
            );
            CREATE TABLE IF NOT EXISTS worktrees (
                session_id     TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
                path           TEXT NOT NULL,
                branch         TEXT NOT NULL,
                base           TEXT NOT NULL,
                created_branch INTEGER NOT NULL,
                common_dir     TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS events (
                session_id TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
                seq        INTEGER NOT NULL,
                kind       TEXT    NOT NULL,  -- JSON of EventKind
                PRIMARY KEY (session_id, seq)
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS archived (
                session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
            );",
        )?;
        let connection = Arc::new(Mutex::new(connection));
        let (writer, queue) = mpsc::channel();
        let target = connection.clone();
        std::thread::spawn(move || write_events(&target, &queue));
        Ok(Self { connection, writer })
    }

    /// Queues events for writing and returns at once. Events of a session
    /// deleted before they are written are dropped.
    pub fn log(&self, events: Vec<Event>) {
        let _ = self.writer.send(Write::Events(events));
    }

    /// Returns once every event logged before the call is committed.
    pub async fn flush(&self) {
        let (done, committed) = oneshot::channel();
        if self.writer.send(Write::Flush(done)).is_ok() {
            let _ = committed.await;
        }
    }

    /// A session's stored events, in order.
    pub fn events(&self, session_id: &str) -> rusqlite::Result<Vec<Event>> {
        let connection = self.lock();
        let mut query = connection
            .prepare("SELECT seq, kind FROM events WHERE session_id = ?1 ORDER BY seq")?;
        query
            .query_map([session_id], |row| {
                let kind: String = row.get(1)?;
                Ok(Event {
                    session_id: session_id.into(),
                    seq: u64::try_from(row.get::<_, i64>(0)?).expect("seqs are positive"),
                    kind: serde_json::from_str::<EventKind>(&kind).map_err(|error| {
                        rusqlite::Error::FromSqlConversionFailure(
                            1,
                            rusqlite::types::Type::Text,
                            Box::new(error),
                        )
                    })?,
                })
            })?
            .collect()
    }

    fn lock(&self) -> MutexGuard<'_, Connection> {
        self.connection.lock().unwrap()
    }

    /// Every workspace, in the order they were added.
    pub fn workspaces(&self) -> rusqlite::Result<Vec<SavedWorkspace>> {
        let connection = self.lock();
        let mut query = connection.prepare("SELECT id, path FROM workspaces ORDER BY rowid")?;
        query
            .query_map([], |row| {
                Ok(SavedWorkspace {
                    id: row.get(0)?,
                    path: PathBuf::from(row.get::<_, String>(1)?),
                })
            })?
            .collect()
    }

    /// Saves a workspace. Returns false, saving nothing, if another
    /// workspace has its path.
    pub fn add_workspace(&self, workspace: &SavedWorkspace) -> rusqlite::Result<bool> {
        let added = self.lock().execute(
            "INSERT INTO workspaces (id, path) VALUES (?1, ?2) ON CONFLICT (path) DO NOTHING",
            params![workspace.id, workspace.path.to_string_lossy()],
        )?;
        Ok(added == 1)
    }

    pub fn remove_workspace(&self, id: &str) -> rusqlite::Result<()> {
        self.lock()
            .execute("DELETE FROM workspaces WHERE id = ?1", [id])?;
        Ok(())
    }

    /// Every session, in the order they were created.
    pub fn sessions(&self) -> rusqlite::Result<Vec<SavedSession>> {
        let connection = self.lock();
        let mut query = connection.prepare(
            "SELECT number, id, workspace_id, command, cwd, title, agent_session_id,
                worktrees.path, branch, base, created_branch, common_dir,
                (SELECT COALESCE(MAX(seq), 0) FROM events WHERE session_id = sessions.id),
                EXISTS (SELECT 1 FROM archived WHERE session_id = sessions.id)
            FROM sessions LEFT JOIN worktrees ON worktrees.session_id = sessions.id
            ORDER BY number",
        )?;
        query
            .query_map([], |row| {
                let command: String = row.get(3)?;
                Ok(SavedSession {
                    number: u64::try_from(row.get::<_, i64>(0)?).expect("rowids are positive"),
                    id: row.get(1)?,
                    workspace_id: row.get(2)?,
                    command: serde_json::from_str(&command).map_err(|error| {
                        rusqlite::Error::FromSqlConversionFailure(
                            3,
                            rusqlite::types::Type::Text,
                            Box::new(error),
                        )
                    })?,
                    cwd: PathBuf::from(row.get::<_, String>(4)?),
                    title: row.get(5)?,
                    agent_session_id: row.get(6)?,
                    worktree: match row.get::<_, Option<String>>(7)? {
                        Some(path) => Some(SavedWorktree {
                            info: WorktreeInfo {
                                path: PathBuf::from(path),
                                branch: row.get(8)?,
                                base: row.get(9)?,
                                created_branch: row.get(10)?,
                            },
                            common_dir: PathBuf::from(row.get::<_, String>(11)?),
                        }),
                        None => None,
                    },
                    last_seq: u64::try_from(row.get::<_, i64>(12)?).expect("seqs are positive"),
                    archived: row.get(13)?,
                })
            })?
            .collect()
    }

    /// Saves a session that has not started, with its worktree if it has
    /// one, and returns its number, which is greater than the number of
    /// every session saved before it.
    pub fn add_session(
        &self,
        id: &str,
        workspace_id: &str,
        command: &[String],
        cwd: &Path,
        worktree: Option<&SavedWorktree>,
    ) -> rusqlite::Result<u64> {
        let command = serde_json::to_string(command).expect("a list of strings serializes");
        let mut connection = self.lock();
        let transaction = connection.transaction()?;
        transaction.execute(
            "INSERT INTO sessions (id, workspace_id, command, cwd) VALUES (?1, ?2, ?3, ?4)",
            params![id, workspace_id, command, cwd.to_string_lossy()],
        )?;
        // `number` is the table's rowid.
        let number = u64::try_from(transaction.last_insert_rowid()).expect("rowids are positive");
        if let Some(SavedWorktree { info, common_dir }) = worktree {
            transaction.execute(
                "INSERT INTO worktrees (session_id, path, branch, base, created_branch, common_dir)
                VALUES (?1, ?2, ?3, ?4, ?5, ?6)",
                params![
                    id,
                    info.path.to_string_lossy(),
                    info.branch,
                    info.base,
                    info.created_branch,
                    common_dir.to_string_lossy()
                ],
            )?;
        }
        transaction.commit()?;
        Ok(number)
    }

    /// Forgets a session's worktree, keeping the session.
    pub fn remove_worktree(&self, session_id: &str) -> rusqlite::Result<()> {
        self.lock()
            .execute("DELETE FROM worktrees WHERE session_id = ?1", [session_id])?;
        Ok(())
    }

    pub fn workspace_has_sessions(&self, workspace_id: &str) -> rusqlite::Result<bool> {
        self.lock().query_row(
            "SELECT EXISTS (SELECT 1 FROM sessions WHERE workspace_id = ?1)",
            [workspace_id],
            |row| row.get(0),
        )
    }

    pub fn set_title(&self, id: &str, title: Option<&str>) -> rusqlite::Result<()> {
        self.lock().execute(
            "UPDATE sessions SET title = ?2 WHERE id = ?1",
            params![id, title],
        )?;
        Ok(())
    }

    pub fn set_agent_session_id(&self, id: &str, agent_session_id: &str) -> rusqlite::Result<()> {
        self.lock().execute(
            "UPDATE sessions SET agent_session_id = ?2 WHERE id = ?1",
            params![id, agent_session_id],
        )?;
        Ok(())
    }

    /// Marks a session archived, or not.
    pub fn set_archived(&self, id: &str, archived: bool) -> rusqlite::Result<()> {
        let sql = if archived {
            "INSERT OR IGNORE INTO archived (session_id) VALUES (?1)"
        } else {
            "DELETE FROM archived WHERE session_id = ?1"
        };
        self.lock().execute(sql, [id])?;
        Ok(())
    }

    pub fn remove_session(&self, id: &str) -> rusqlite::Result<()> {
        self.lock()
            .execute("DELETE FROM sessions WHERE id = ?1", [id])?;
        Ok(())
    }
}

/// Writes each batch the queue holds in one transaction, until every sender
/// is dropped. A failed write goes to oxd's log and the thread carries on.
fn write_events(connection: &Mutex<Connection>, queue: &Receiver<Write>) {
    while let Ok(first) = queue.recv() {
        let mut batch = vec![first];
        batch.extend(queue.try_iter());
        let mut flushes = Vec::new();
        let mut connection = connection.lock().unwrap();
        let result = (|| {
            let transaction = connection.transaction()?;
            {
                let mut insert = transaction.prepare_cached(
                    "INSERT INTO events SELECT ?1, ?2, ?3
                    WHERE EXISTS (SELECT 1 FROM sessions WHERE id = ?1)",
                )?;
                for write in &batch {
                    if let Write::Events(events) = write {
                        for event in events {
                            let seq = i64::try_from(event.seq).expect("seqs fit an i64");
                            let kind = serde_json::to_string(&event.kind)
                                .expect("an event kind serializes");
                            insert.execute(params![event.session_id, seq, kind])?;
                        }
                    }
                }
            }
            transaction.commit()
        })();
        drop(connection);
        if let Err(error) = result {
            eprintln!("oxd: writing events: {error}");
        }
        for write in batch {
            if let Write::Flush(done) = write {
                flushes.push(done);
            }
        }
        for done in flushes {
            let _ = done.send(());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn store() -> Store {
        Store::with(Connection::open_in_memory().unwrap()).unwrap()
    }

    fn workspace(id: &str, path: &str) -> SavedWorkspace {
        SavedWorkspace {
            id: id.into(),
            path: path.into(),
        }
    }

    fn command() -> Vec<String> {
        vec!["agent".into(), "--flag".into()]
    }

    #[test]
    fn workspaces_round_trip_in_the_order_they_were_added() {
        let store = store();
        assert!(store.workspaces().unwrap().is_empty());
        let second = workspace("b", "/second");
        let first = workspace("a", "/first");
        assert!(store.add_workspace(&second).unwrap());
        assert!(store.add_workspace(&first).unwrap());
        assert_eq!(store.workspaces().unwrap(), [second, first.clone()]);

        store.remove_workspace("b").unwrap();
        assert_eq!(store.workspaces().unwrap(), [first]);
    }

    #[test]
    fn a_workspace_path_is_saved_once() {
        let store = store();
        assert!(store.add_workspace(&workspace("a", "/work")).unwrap());
        assert!(!store.add_workspace(&workspace("b", "/work")).unwrap());
        assert_eq!(store.workspaces().unwrap(), [workspace("a", "/work")]);
    }

    #[test]
    fn sessions_round_trip_in_the_order_they_were_created() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        assert!(store.sessions().unwrap().is_empty());
        let first = store
            .add_session("one", "w", &command(), Path::new("/work"), None)
            .unwrap();
        let second = store
            .add_session("two", "w", &command(), Path::new("/work"), None)
            .unwrap();
        assert!(first < second);
        assert_eq!(
            store.sessions().unwrap(),
            [
                SavedSession {
                    number: first,
                    id: "one".into(),
                    workspace_id: "w".into(),
                    command: command(),
                    cwd: "/work".into(),
                    title: None,
                    agent_session_id: None,
                    worktree: None,
                    last_seq: 0,
                    archived: false,
                },
                SavedSession {
                    number: second,
                    id: "two".into(),
                    workspace_id: "w".into(),
                    command: command(),
                    cwd: "/work".into(),
                    title: None,
                    agent_session_id: None,
                    worktree: None,
                    last_seq: 0,
                    archived: false,
                },
            ]
        );
    }

    #[test]
    fn archiving_is_saved_and_deleted_with_the_session() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store
            .add_session("one", "w", &command(), Path::new("/work"), None)
            .unwrap();
        assert!(!store.sessions().unwrap()[0].archived);
        store.set_archived("one", true).unwrap();
        assert!(store.sessions().unwrap()[0].archived);
        store.set_archived("one", false).unwrap();
        assert!(!store.sessions().unwrap()[0].archived);
        store.set_archived("one", true).unwrap();
        store.remove_session("one").unwrap();
        let rows: i64 = store
            .lock()
            .query_row("SELECT COUNT(*) FROM archived", [], |row| row.get(0))
            .unwrap();
        assert_eq!(rows, 0);
    }

    #[test]
    fn title_and_agent_session_id_updates_are_saved() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store
            .add_session("s", "w", &command(), Path::new("/work"), None)
            .unwrap();
        store.set_title("s", Some("Fix the build")).unwrap();
        store.set_agent_session_id("s", "agent").unwrap();
        let saved = &store.sessions().unwrap()[0];
        assert_eq!(saved.title.as_deref(), Some("Fix the build"));
        assert_eq!(saved.agent_session_id.as_deref(), Some("agent"));

        store.set_title("s", None).unwrap();
        assert_eq!(store.sessions().unwrap()[0].title, None);
    }

    #[test]
    fn a_deleted_session_is_gone_and_its_number_is_not_reused() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        let first = store
            .add_session("one", "w", &command(), Path::new("/work"), None)
            .unwrap();
        store.remove_session("one").unwrap();
        assert!(store.sessions().unwrap().is_empty());
        let second = store
            .add_session("two", "w", &command(), Path::new("/work"), None)
            .unwrap();
        assert!(second > first);
    }

    #[test]
    fn a_workspace_with_sessions_cannot_be_deleted() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store
            .add_session("s", "w", &command(), Path::new("/work"), None)
            .unwrap();
        assert!(store.remove_workspace("w").is_err());
        store.remove_session("s").unwrap();
        store.remove_workspace("w").unwrap();
    }

    fn worktree() -> SavedWorktree {
        SavedWorktree {
            info: WorktreeInfo {
                path: "/worktrees/work-01234567".into(),
                branch: "ox/01234567".into(),
                base: "abc".into(),
                created_branch: true,
            },
            common_dir: "/repos/work/.git".into(),
        }
    }

    /// The worktree, with its repository's location, comes back as saved.
    #[test]
    fn a_session_round_trips_with_its_worktree() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store
            .add_session("plain", "w", &command(), Path::new("/work"), None)
            .unwrap();
        store
            .add_session(
                "worktree",
                "w",
                &command(),
                Path::new("/worktrees/work-01234567/sub"),
                Some(&worktree()),
            )
            .unwrap();
        let sessions = store.sessions().unwrap();
        assert_eq!(sessions[0].worktree, None);
        assert_eq!(sessions[1].worktree, Some(worktree()));
        assert_eq!(sessions[1].cwd, Path::new("/worktrees/work-01234567/sub"));
    }

    #[test]
    fn a_session_whose_worktree_row_fails_is_not_saved() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store.lock().execute("DROP TABLE worktrees", []).unwrap();
        store
            .add_session("s", "w", &command(), Path::new("/work"), Some(&worktree()))
            .unwrap_err();
        let sessions: i64 = store
            .lock()
            .query_row("SELECT COUNT(*) FROM sessions", [], |row| row.get(0))
            .unwrap();
        assert_eq!(sessions, 0);
    }

    #[test]
    fn a_worktree_row_goes_with_its_session_or_alone() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        for id in ["one", "two"] {
            store
                .add_session(id, "w", &command(), Path::new("/work"), Some(&worktree()))
                .unwrap();
        }
        store.remove_worktree("one").unwrap();
        let sessions = store.sessions().unwrap();
        assert_eq!(sessions[0].worktree, None);
        assert_eq!(sessions[1].worktree, Some(worktree()));

        store.remove_session("two").unwrap();
        let rows: i64 = store
            .lock()
            .query_row("SELECT COUNT(*) FROM worktrees", [], |row| row.get(0))
            .unwrap();
        assert_eq!(rows, 0);
    }

    #[test]
    fn whether_a_workspace_has_sessions() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store.add_workspace(&workspace("v", "/other")).unwrap();
        assert!(!store.workspace_has_sessions("w").unwrap());
        store
            .add_session("s", "w", &command(), Path::new("/work"), None)
            .unwrap();
        assert!(store.workspace_has_sessions("w").unwrap());
        assert!(!store.workspace_has_sessions("v").unwrap());
    }

    fn event(session: &str, seq: u64, kind: EventKind) -> Event {
        Event {
            session_id: session.into(),
            seq,
            kind,
        }
    }

    fn exited(session: &str, seq: u64) -> Event {
        event(session, seq, EventKind::AgentExited { error: None })
    }

    fn store_with_session() -> Store {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store
            .add_session("s", "w", &command(), Path::new("/work"), None)
            .unwrap();
        store
    }

    #[tokio::test]
    async fn events_round_trip_in_order_across_batches() {
        let store = store_with_session();
        store.log(vec![exited("s", 1), exited("s", 2)]);
        store.log(vec![exited("s", 3)]);
        store.flush().await;
        let seqs: Vec<_> = store
            .events("s")
            .unwrap()
            .into_iter()
            .map(|event| event.seq)
            .collect();
        assert_eq!(seqs, [1, 2, 3]);
        assert_eq!(store.sessions().unwrap()[0].last_seq, 3);
        assert_eq!(store.events("s").unwrap()[0], exited("s", 1));
    }

    #[tokio::test]
    async fn events_of_a_deleted_session_are_dropped() {
        let store = store_with_session();
        store.remove_session("s").unwrap();
        store.log(vec![exited("s", 1)]);
        store.flush().await;
        assert!(store.events("s").unwrap().is_empty());
    }

    #[tokio::test]
    async fn deleting_a_session_removes_its_events() {
        let store = store_with_session();
        store
            .add_session("t", "w", &command(), Path::new("/work"), None)
            .unwrap();
        store.log(vec![exited("s", 1), exited("t", 1)]);
        store.flush().await;
        store.remove_session("s").unwrap();
        assert!(store.events("s").unwrap().is_empty());
        assert_eq!(store.events("t").unwrap().len(), 1);
    }
}
