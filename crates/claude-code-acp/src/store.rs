use std::{
    collections::HashMap,
    fs::{self, File, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

use chrono::Utc;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use crate::runner::McpServer;

const REGISTRY: &str = "acp-sessions.json";
const LOCK: &str = "acp-sessions.lock";

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Session {
    pub cwd: PathBuf,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub claude_session_id: Option<String>,
    pub updated_at: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub mode: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub title: Option<String>,
    #[serde(skip)]
    pub mcp_servers: Vec<McpServer>,
}

impl Session {
    pub fn new(cwd: PathBuf, mcp_servers: Vec<McpServer>) -> Self {
        let now = Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
        Self {
            cwd,
            claude_session_id: None,
            updated_at: now,
            mode: None,
            model: None,
            title: None,
            mcp_servers,
        }
    }
    pub fn touch(&mut self) {
        self.updated_at = Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
    }
}

/// The session registry, shared by every bridge process that uses the same
/// store directory. Each operation reads and rewrites the registry file under
/// an exclusive lock. MCP servers are kept per process.
pub struct Store {
    dir: PathBuf,
    mcp_servers: Mutex<HashMap<String, Vec<McpServer>>>,
}

impl Store {
    pub fn new(dir: PathBuf) -> Self {
        Self {
            dir,
            mcp_servers: Mutex::new(HashMap::new()),
        }
    }

    pub fn get(&self, id: &str) -> Option<Session> {
        let session = self.transaction(|entries| (entries.get(id).and_then(parse), false))?;
        Some(self.with_servers(id, session))
    }

    pub fn list(&self, cwd: Option<&Path>) -> Vec<(String, Session)> {
        let sessions: Vec<(String, Session)> = self.transaction(|entries| {
            let sessions = entries
                .iter()
                .filter_map(|(id, entry)| Some((id.clone(), parse(entry)?)))
                .filter(|(_, session)| cwd.is_none_or(|cwd| cwd == session.cwd))
                .collect();
            (sessions, false)
        });
        sessions
            .into_iter()
            .map(|(id, session)| {
                let session = self.with_servers(&id, session);
                (id, session)
            })
            .collect()
    }

    pub fn create(&self, id: String, session: Session) {
        self.mcp_servers
            .lock()
            .unwrap()
            .insert(id.clone(), session.mcp_servers.clone());
        self.transaction(|entries| {
            entries.insert(id, to_value(&session));
            ((), true)
        });
    }

    pub fn update(&self, id: &str, update: impl FnOnce(&mut Session)) {
        let servers = self.transaction(|entries| {
            let Some(session) = entries.get(id).and_then(parse) else {
                return (None, false);
            };
            let mut session = self.with_servers(id, session);
            update(&mut session);
            session.touch();
            entries.insert(id.to_owned(), to_value(&session));
            (Some(session.mcp_servers), true)
        });
        if let Some(servers) = servers {
            self.mcp_servers
                .lock()
                .unwrap()
                .insert(id.to_owned(), servers);
        }
    }

    fn with_servers(&self, id: &str, mut session: Session) -> Session {
        if let Some(servers) = self.mcp_servers.lock().unwrap().get(id) {
            session.mcp_servers = servers.clone();
        }
        session
    }

    /// Runs `operation` on the registry's entries under the registry lock and
    /// saves them when it returns `true`.
    fn transaction<T>(&self, operation: impl FnOnce(&mut Map<String, Value>) -> (T, bool)) -> T {
        let _lock = self
            .lock()
            .inspect_err(|err| eprintln!("failed to lock session registry: {err}"))
            .ok();
        let (mut entries, readable) = match self.read() {
            Ok(entries) => (entries, true),
            Err(err) => {
                eprintln!("failed to read session registry: {err}");
                (Map::new(), false)
            }
        };
        let (value, changed) = operation(&mut entries);
        if changed {
            let result = if readable {
                self.write(&entries)
            } else {
                Err(io::Error::other("the registry could not be read"))
            };
            if let Err(err) = result {
                eprintln!("failed to save session registry: {err}");
            }
        }
        value
    }

    fn lock(&self) -> io::Result<File> {
        fs::create_dir_all(&self.dir)?;
        let file = OpenOptions::new()
            .create(true)
            .truncate(false)
            .write(true)
            .open(self.dir.join(LOCK))?;
        file.lock()?;
        Ok(file)
    }

    /// Reads the registry. A registry that is not a JSON object is moved
    /// aside so its contents are kept, and reading continues with no entries.
    fn read(&self) -> io::Result<Map<String, Value>> {
        let path = self.dir.join(REGISTRY);
        let bytes = match fs::read(&path) {
            Ok(bytes) => bytes,
            Err(err) if err.kind() == io::ErrorKind::NotFound => return Ok(Map::new()),
            Err(err) => return Err(err),
        };
        match serde_json::from_slice(&bytes) {
            Ok(entries) => Ok(entries),
            Err(err) => {
                let aside = self.dir.join(format!(
                    "{REGISTRY}.corrupt-{}",
                    Utc::now().format("%Y%m%dT%H%M%S%.3fZ")
                ));
                eprintln!(
                    "session registry is unreadable ({err}); moving it to {}",
                    aside.display()
                );
                fs::rename(&path, &aside)?;
                Ok(Map::new())
            }
        }
    }

    fn write(&self, entries: &Map<String, Value>) -> io::Result<()> {
        let mut file = tempfile::NamedTempFile::new_in(&self.dir)?;
        serde_json::to_writer_pretty(&mut file, entries)?;
        file.flush()?;
        file.persist(self.dir.join(REGISTRY))
            .map_err(|err| err.error)?;
        Ok(())
    }
}

fn parse(entry: &Value) -> Option<Session> {
    serde_json::from_value(entry.clone()).ok()
}

fn to_value(session: &Session) -> Value {
    serde_json::to_value(session).expect("sessions serialize")
}
