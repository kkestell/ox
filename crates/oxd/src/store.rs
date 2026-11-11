//! The daemon's saved workspaces and sessions, in one `SQLite` database.

use std::path::{Path, PathBuf};
use std::sync::{Mutex, MutexGuard};

use ox_protocol::WorkspaceInfo;
use rusqlite::{Connection, params};

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
}

/// One connection to the database. Every query is a small local read or
/// write, so it runs inline.
pub struct Store {
    connection: Mutex<Connection>,
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
            "PRAGMA foreign_keys = ON;
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
            );",
        )?;
        Ok(Self {
            connection: Mutex::new(connection),
        })
    }

    fn lock(&self) -> MutexGuard<'_, Connection> {
        self.connection.lock().unwrap()
    }

    /// Every workspace, in the order they were added.
    pub fn workspaces(&self) -> rusqlite::Result<Vec<WorkspaceInfo>> {
        let connection = self.lock();
        let mut query = connection.prepare("SELECT id, path FROM workspaces ORDER BY rowid")?;
        query
            .query_map([], |row| {
                Ok(WorkspaceInfo {
                    id: row.get(0)?,
                    path: PathBuf::from(row.get::<_, String>(1)?),
                })
            })?
            .collect()
    }

    /// Saves a workspace. Returns false, saving nothing, if another
    /// workspace has its path.
    pub fn add_workspace(&self, workspace: &WorkspaceInfo) -> rusqlite::Result<bool> {
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
            "SELECT number, id, workspace_id, command, cwd, title, agent_session_id
            FROM sessions ORDER BY number",
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
                })
            })?
            .collect()
    }

    /// Saves a session that has not started, and returns its number, which
    /// is greater than the number of every session saved before it.
    pub fn add_session(
        &self,
        id: &str,
        workspace_id: &str,
        command: &[String],
        cwd: &Path,
    ) -> rusqlite::Result<u64> {
        let command = serde_json::to_string(command).expect("a list of strings serializes");
        let connection = self.lock();
        connection.execute(
            "INSERT INTO sessions (id, workspace_id, command, cwd) VALUES (?1, ?2, ?3, ?4)",
            params![id, workspace_id, command, cwd.to_string_lossy()],
        )?;
        // `number` is the table's rowid.
        Ok(u64::try_from(connection.last_insert_rowid()).expect("rowids are positive"))
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

    pub fn remove_session(&self, id: &str) -> rusqlite::Result<()> {
        self.lock()
            .execute("DELETE FROM sessions WHERE id = ?1", [id])?;
        Ok(())
    }

    /// Deletes the sessions that never started, which have no
    /// `agent_session_id` to load.
    pub fn remove_unstarted_sessions(&self) -> rusqlite::Result<()> {
        self.lock()
            .execute("DELETE FROM sessions WHERE agent_session_id IS NULL", [])?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn store() -> Store {
        Store::with(Connection::open_in_memory().unwrap()).unwrap()
    }

    fn workspace(id: &str, path: &str) -> WorkspaceInfo {
        WorkspaceInfo {
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
            .add_session("one", "w", &command(), Path::new("/work"))
            .unwrap();
        let second = store
            .add_session("two", "w", &command(), Path::new("/work"))
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
                },
                SavedSession {
                    number: second,
                    id: "two".into(),
                    workspace_id: "w".into(),
                    command: command(),
                    cwd: "/work".into(),
                    title: None,
                    agent_session_id: None,
                },
            ]
        );
    }

    #[test]
    fn title_and_agent_session_id_updates_are_saved() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store
            .add_session("s", "w", &command(), Path::new("/work"))
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
            .add_session("one", "w", &command(), Path::new("/work"))
            .unwrap();
        store.remove_session("one").unwrap();
        assert!(store.sessions().unwrap().is_empty());
        let second = store
            .add_session("two", "w", &command(), Path::new("/work"))
            .unwrap();
        assert!(second > first);
    }

    #[test]
    fn sessions_that_never_started_are_deleted() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        for id in ["started", "unstarted"] {
            store
                .add_session(id, "w", &command(), Path::new("/work"))
                .unwrap();
        }
        store.set_agent_session_id("started", "agent").unwrap();
        store.remove_unstarted_sessions().unwrap();
        let ids: Vec<String> = store
            .sessions()
            .unwrap()
            .into_iter()
            .map(|session| session.id)
            .collect();
        assert_eq!(ids, ["started"]);
    }

    #[test]
    fn a_workspace_with_sessions_cannot_be_deleted() {
        let store = store();
        store.add_workspace(&workspace("w", "/work")).unwrap();
        store
            .add_session("s", "w", &command(), Path::new("/work"))
            .unwrap();
        assert!(store.remove_workspace("w").is_err());
        store.remove_session("s").unwrap();
        store.remove_workspace("w").unwrap();
    }
}
