use std::{
    collections::HashMap,
    fs::{self, File},
    io::{BufRead, BufReader, Read},
    path::{Path, PathBuf},
};

use chrono::{DateTime, Utc};
use serde_json::Value;
use uuid::Uuid;

#[derive(Clone, Debug)]
pub struct HistoryEntry {
    pub id: String,
    pub cwd: PathBuf,
    pub title: Option<String>,
    pub updated_at: String,
}

pub struct History {
    root: PathBuf,
}

impl History {
    pub fn new(claude_dir: &Path) -> Self {
        Self {
            root: claude_dir.join("projects"),
        }
    }

    pub fn list(&self, cwd: Option<&Path>) -> Vec<HistoryEntry> {
        let mut entries = Vec::new();
        let Ok(projects) = fs::read_dir(&self.root) else {
            return entries;
        };
        for project in projects.flatten().filter(|e| e.path().is_dir()) {
            let Ok(files) = fs::read_dir(project.path()) else {
                continue;
            };
            for file in files.flatten() {
                let path = file.path();
                let Some(id) = path.file_stem().and_then(|s| s.to_str()) else {
                    continue;
                };
                if path.extension().and_then(|s| s.to_str()) != Some("jsonl")
                    || Uuid::parse_str(id).is_err()
                {
                    continue;
                }
                if let Some(entry) = read_entry(&path, id)
                    && cwd.is_none_or(|cwd| cwd == entry.cwd)
                {
                    entries.push(entry);
                }
            }
        }
        entries.sort_by(|a, b| b.updated_at.cmp(&a.updated_at).then(a.id.cmp(&b.id)));
        entries
    }

    pub fn find(&self, id: &str, cwd: &Path) -> bool {
        self.transcript(id, cwd).is_some()
    }

    /// The `user` and `assistant` entries of a session's transcript, in
    /// order. The transcript's other entries are Claude's bookkeeping, and
    /// its sidechain entries belong to subagents.
    pub fn messages(&self, id: &str, cwd: &Path) -> Vec<Value> {
        self.transcript(id, cwd)
            .map(|path| {
                messages(&path)
                    .into_iter()
                    .filter(|entry| entry["isSidechain"] != true)
                    .collect()
            })
            .unwrap_or_default()
    }

    /// The `user` and `assistant` entries of each subagent of a session, by
    /// the tool call that started it. Claude keeps a subagent's transcript
    /// in `<session>/subagents/agent-<id>.jsonl`, beside a `.meta.json` that
    /// names the tool call as `toolUseId`.
    pub fn subagents(&self, id: &str, cwd: &Path) -> HashMap<String, Vec<Value>> {
        let Some(dir) = self
            .transcript(id, cwd)
            .map(|path| path.with_extension("").join("subagents"))
        else {
            return HashMap::new();
        };
        let Ok(files) = fs::read_dir(&dir) else {
            return HashMap::new();
        };
        files
            .flatten()
            .filter_map(|file| {
                let path = file.path();
                let name = path.file_name()?.to_str()?.strip_suffix(".meta.json")?;
                let meta: Value = serde_json::from_slice(&fs::read(&path).ok()?).ok()?;
                let tool_use_id = meta["toolUseId"].as_str()?.to_owned();
                Some((tool_use_id, messages(&dir.join(format!("{name}.jsonl")))))
            })
            .collect()
    }

    /// The transcript of session `id` started in `cwd`.
    fn transcript(&self, id: &str, cwd: &Path) -> Option<PathBuf> {
        Uuid::parse_str(id).ok()?;
        let projects = fs::read_dir(&self.root).ok()?;
        projects
            .flatten()
            .map(|project| project.path().join(format!("{id}.jsonl")))
            .find(|path| read_entry(path, id).is_some_and(|entry| entry.cwd == cwd))
    }
}

/// The `user` and `assistant` entries of the transcript at `path`.
fn messages(path: &Path) -> Vec<Value> {
    let Ok(file) = File::open(path) else {
        return Vec::new();
    };
    BufReader::new(file)
        .lines()
        .map_while(Result::ok)
        .filter_map(|line| serde_json::from_str::<Value>(&line).ok())
        .filter(|entry| matches!(entry["type"].as_str(), Some("user" | "assistant")))
        .collect()
}

fn read_entry(path: &Path, id: &str) -> Option<HistoryEntry> {
    let metadata = path.metadata().ok()?;
    if !metadata.is_file() {
        return None;
    }
    let file = File::open(path).ok()?;
    let mut cwd = None;
    let mut title = None;
    let mut reader = BufReader::new(file).take(1024 * 1024);
    let mut line = String::new();
    while reader.read_line(&mut line).ok()? > 0 {
        if let Ok(entry) = serde_json::from_str::<Value>(&line) {
            if cwd.is_none() {
                cwd = entry.get("cwd").and_then(Value::as_str).map(PathBuf::from);
            }
            if title.is_none()
                && entry.get("type").and_then(Value::as_str) == Some("user")
                && entry.get("isSidechain").and_then(Value::as_bool) != Some(true)
            {
                let content = &entry["message"]["content"];
                let text = content.as_str().or_else(|| {
                    content.as_array().and_then(|blocks| {
                        blocks
                            .iter()
                            .find(|block| block["type"] == "text")
                            .and_then(|block| block["text"].as_str())
                    })
                });
                title = text
                    .map(|s| s.split_whitespace().collect::<Vec<_>>().join(" "))
                    .filter(|s| !s.is_empty())
                    .map(|s| s.chars().take(120).collect());
            }
            if cwd.is_some() && title.is_some() {
                break;
            }
        }
        line.clear();
    }
    Some(HistoryEntry {
        id: id.into(),
        cwd: cwd?,
        title,
        updated_at: DateTime::<Utc>::from(metadata.modified().ok()?)
            .to_rfc3339_opts(chrono::SecondsFormat::Millis, true),
    })
}
