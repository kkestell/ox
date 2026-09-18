//! File-backed session storage.
//!
//! Every session is one JSONL file, all of them side by side:
//! `{data}/sessions/{session_id}.jsonl`
//!
//! `{data}` is `$XDG_DATA_HOME/ox`, defaulting to `~/.local/share/ox`
//! (`$OX_DATA_DIR` overrides the data dir, mainly for tests).
//!
//! Each session file opens with a header line naming the workspace it belongs
//! to and the UTC time it was created:
//!
//! ```jsonl
//! {"type":"session","cwd":"/Users/kyle/projects/ox","ts":"2026-09-18T17:04:31.482Z"}
//! ```
//!
//! Alongside the session files sits `{data}/sessions/index.json`, a JSON array
//! with one object per session:
//!
//! ```json
//! [{"sessionId":"...","cwd":"/Users/kyle/projects/ox","title":"Rejigger sessions",
//!   "createdAt":"2026-09-18T17:04:31.482Z","updatedAt":"2026-09-18T17:09:02.118Z"}]
//! ```
//!
//! The index is what `session/list` answers from, so listing never has to open
//! a session file; the header line is what lets a session file still say which
//! workspace it came from on its own, without the index. `createdAt` and
//! `updatedAt` are the ISO 8601 timestamps ACP reports as last activity.
//!
//! The index is rewritten whole, through a temporary file and a rename, so a
//! crash mid-write leaves the previous index intact. Two ox processes writing
//! at once is still last-writer-wins; sessions are per-user and that race is
//! not worth a lock file.
//!
//! Beyond the header line this module only manages the files themselves:
//! create, list, touch and delete. Conversation records are future work.

use std::fs;
use std::io::{self, ErrorKind, Write};
use std::path::{Path, PathBuf};

use agent_client_protocol::schema::v1::{SessionId, SessionInfo};
use chrono::{SecondsFormat, Utc};
use serde::{Deserialize, Serialize};

/// Environment variable overriding the data directory (mainly for tests).
pub const DATA_DIR_ENV: &str = "OX_DATA_DIR";

/// Extension for session files.
const SESSION_EXTENSION: &str = "jsonl";

/// Name of the index file listing every session.
const INDEX_FILE: &str = "index.json";

/// Name of the temporary file the index is written to before being renamed.
const INDEX_TEMP_FILE: &str = "index.json.tmp";

/// Longest title derived from a prompt, in characters.
const MAX_TITLE_CHARS: usize = 80;

/// Header line that opens every session file.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "type", rename = "session")]
struct SessionHeader {
    cwd: PathBuf,
    /// RFC 3339 UTC time this record was written, the `ts` every transcript
    /// event carries.
    ts: String,
}

/// One session's entry in `index.json`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct IndexEntry {
    session_id: String,
    cwd: PathBuf,
    /// Human-readable title, absent until a prompt gives the session one.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    title: Option<String>,
    created_at: String,
    updated_at: String,
}

fn invalid_input(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidInput, message.into())
}

/// The current time as an RFC 3339 UTC string.
///
/// Millisecond precision so that sessions created in quick succession still
/// sort in the order they happened.
fn now() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true)
}

/// Resolve the directory holding the session files and the index.
pub fn sessions_root() -> io::Result<PathBuf> {
    if let Ok(dir) = std::env::var(DATA_DIR_ENV)
        && !dir.is_empty()
    {
        return Ok(PathBuf::from(dir).join("sessions"));
    }
    if let Ok(xdg) = std::env::var("XDG_DATA_HOME")
        && !xdg.is_empty()
    {
        return Ok(PathBuf::from(xdg).join("ox/sessions"));
    }
    let home = std::env::var("HOME").map_err(|_| invalid_input("HOME is not set"))?;
    Ok(PathBuf::from(home).join(".local/share/ox/sessions"))
}

/// Reject session ids that could name something other than a file in the root.
fn validate_session_id(session_id: &SessionId) -> io::Result<()> {
    let id = session_id.to_string();
    if id.is_empty()
        || id == "."
        || id == ".."
        || id.contains('/')
        || id.contains('\\')
        || id.contains('\0')
    {
        return Err(invalid_input(format!("invalid session id: {id:?}")));
    }
    Ok(())
}

fn session_path(root: &Path, session_id: &SessionId) -> io::Result<PathBuf> {
    validate_session_id(session_id)?;
    Ok(root.join(format!("{session_id}.{SESSION_EXTENSION}")))
}

/// Read `index.json`.
///
/// A missing index is an empty one. An unreadable or malformed index is also
/// treated as empty rather than as an error: `session/list` returning nothing
/// is recoverable, refusing to start a session is not, and the next write
/// replaces the damaged file.
fn read_index(root: &Path) -> Vec<IndexEntry> {
    let Ok(contents) = fs::read_to_string(root.join(INDEX_FILE)) else {
        return Vec::new();
    };
    serde_json::from_str(&contents).unwrap_or_default()
}

/// Replace `index.json` with `entries`, through a temporary file and a rename.
fn write_index(root: &Path, entries: &[IndexEntry]) -> io::Result<()> {
    fs::create_dir_all(root)?;
    let json = serde_json::to_string_pretty(entries).map_err(io::Error::other)?;
    let temp = root.join(INDEX_TEMP_FILE);
    let mut file = fs::File::create(&temp)?;
    file.write_all(json.as_bytes())?;
    file.write_all(b"\n")?;
    file.sync_all()?;
    drop(file);
    fs::rename(&temp, root.join(INDEX_FILE))
}

/// Create the `{session_id}.jsonl` file for a new session in `cwd`.
///
/// The file holds one header line recording `cwd` and the creation time, and
/// the session gains an index entry. Both steps are idempotent: re-creating an
/// existing session neither truncates its file nor duplicates its entry.
pub fn create_session(cwd: &Path, session_id: &SessionId) -> io::Result<PathBuf> {
    create_session_in(&sessions_root()?, cwd, session_id)
}

fn create_session_in(root: &Path, cwd: &Path, session_id: &SessionId) -> io::Result<PathBuf> {
    if cwd.as_os_str().is_empty() {
        return Err(invalid_input("workspace path is empty"));
    }
    let path = session_path(root, session_id)?;
    fs::create_dir_all(root)?;
    let ts = now();
    match fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&path)
    {
        Ok(mut file) => write_header(&mut file, cwd, &ts)?,
        Err(err) if err.kind() == ErrorKind::AlreadyExists => {}
        Err(err) => return Err(err),
    }

    let id = session_id.to_string();
    let mut entries = read_index(root);
    if !entries.iter().any(|entry| entry.session_id == id) {
        entries.push(IndexEntry {
            session_id: id,
            cwd: cwd.to_path_buf(),
            title: None,
            created_at: ts.clone(),
            updated_at: ts,
        });
        write_index(root, &entries)?;
    }
    Ok(path)
}

fn write_header(file: &mut fs::File, cwd: &Path, ts: &str) -> io::Result<()> {
    let header = SessionHeader {
        cwd: cwd.to_path_buf(),
        ts: ts.to_owned(),
    };
    let line = serde_json::to_string(&header).map_err(io::Error::other)?;
    writeln!(file, "{line}")
}

/// Check whether `cwd` has a session with this id.
pub fn session_exists(cwd: &Path, session_id: &SessionId) -> bool {
    let Ok(root) = sessions_root() else {
        return false;
    };
    session_exists_in(&root, cwd, session_id)
}

fn session_exists_in(root: &Path, cwd: &Path, session_id: &SessionId) -> bool {
    let Ok(path) = session_path(root, session_id) else {
        return false;
    };
    let id = session_id.to_string();
    read_index(root)
        .iter()
        .any(|entry| entry.session_id == id && entry.cwd == cwd)
        && path.is_file()
}

/// List sessions from the index, optionally limited to one workspace.
///
/// Most recently updated first, ties broken by id so the order is stable.
/// Entries whose session file has gone missing are skipped, so everything
/// listed can also be loaded.
pub fn list_sessions(cwd: Option<&Path>) -> io::Result<Vec<SessionInfo>> {
    list_sessions_in(&sessions_root()?, cwd)
}

fn list_sessions_in(root: &Path, cwd: Option<&Path>) -> io::Result<Vec<SessionInfo>> {
    let mut entries: Vec<IndexEntry> = read_index(root)
        .into_iter()
        .filter(|entry| cwd.is_none_or(|cwd| entry.cwd == cwd))
        .filter(|entry| {
            session_path(root, &SessionId::new(entry.session_id.clone()))
                .is_ok_and(|path| path.is_file())
        })
        .collect();
    entries.sort_by(|a, b| {
        b.updated_at
            .cmp(&a.updated_at)
            .then_with(|| a.session_id.cmp(&b.session_id))
    });
    Ok(entries
        .into_iter()
        .map(|entry| {
            SessionInfo::new(SessionId::new(entry.session_id), entry.cwd)
                .title(entry.title)
                .updated_at(entry.updated_at)
        })
        .collect())
}

/// Record activity on a session: bump `updatedAt`, and adopt `title` if the
/// session has not been given one yet.
///
/// A session missing from the index is left alone rather than resurrected.
pub fn record_activity(session_id: &SessionId, title: Option<String>) -> io::Result<()> {
    record_activity_in(&sessions_root()?, session_id, title)
}

fn record_activity_in(
    root: &Path,
    session_id: &SessionId,
    title: Option<String>,
) -> io::Result<()> {
    validate_session_id(session_id)?;
    let id = session_id.to_string();
    let mut entries = read_index(root);
    let Some(entry) = entries.iter_mut().find(|entry| entry.session_id == id) else {
        return Ok(());
    };
    entry.updated_at = now();
    if entry.title.is_none() {
        entry.title = title;
    }
    write_index(root, &entries)
}

/// Turn the first line of a prompt into a session title.
///
/// `None` when there is nothing worth showing, so an empty prompt does not
/// claim the one chance a session has to be titled.
pub fn title_from_prompt(text: &str) -> Option<String> {
    let line = text.lines().find(|line| !line.trim().is_empty())?.trim();
    let mut title: String = line.chars().take(MAX_TITLE_CHARS).collect();
    if line.chars().count() > MAX_TITLE_CHARS {
        title.push('…');
    }
    Some(title)
}

/// Delete a session's file and its index entry.
///
/// Returns `Ok(true)` if either was there to remove.
pub fn delete_session(session_id: &SessionId) -> io::Result<bool> {
    delete_session_in(&sessions_root()?, session_id)
}

fn delete_session_in(root: &Path, session_id: &SessionId) -> io::Result<bool> {
    let path = session_path(root, session_id)?;
    let removed_file = match fs::remove_file(&path) {
        Ok(()) => true,
        Err(err) if err.kind() == ErrorKind::NotFound => false,
        Err(err) => return Err(err),
    };

    let id = session_id.to_string();
    let mut entries = read_index(root);
    let before = entries.len();
    entries.retain(|entry| entry.session_id != id);
    let removed_entry = entries.len() != before;
    if removed_entry {
        write_index(root, &entries)?;
    }
    Ok(removed_file || removed_entry)
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::DateTime;

    const CWD: &str = "/Users/kyle/projects/ox";
    const OTHER_CWD: &str = "/Users/kyle/projects/other";

    fn test_root(name: &str) -> PathBuf {
        std::env::temp_dir().join(format!("ox-sessions-test-{}-{}", std::process::id(), name))
    }

    fn fresh_root(name: &str) -> PathBuf {
        let root = test_root(name);
        let _ = fs::remove_dir_all(&root);
        root
    }

    fn test_id(id: &str) -> SessionId {
        SessionId::new(id.to_owned())
    }

    fn cwd() -> &'static Path {
        Path::new(CWD)
    }

    fn ids(sessions: &[SessionInfo]) -> Vec<String> {
        sessions
            .iter()
            .map(|session| session.session_id.to_string())
            .collect()
    }

    #[test]
    fn session_files_sit_directly_in_the_sessions_root() {
        let root = fresh_root("flat");
        let id = test_id("11111111-2222-4333-8444-555555555555");

        let path = create_session_in(&root, cwd(), &id).unwrap();
        assert_eq!(path, root.join(format!("{id}.jsonl")));
        assert_eq!(path.parent(), Some(root.as_path()));

        let mut names: Vec<String> = fs::read_dir(&root)
            .unwrap()
            .flatten()
            .map(|entry| entry.file_name().to_string_lossy().into_owned())
            .collect();
        names.sort();
        assert_eq!(names, vec![format!("{id}.jsonl"), INDEX_FILE.to_owned()]);
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn records_workspace_path_in_session_file() {
        let root = fresh_root("header");
        let id = test_id("11111111-2222-4333-8444-555555555555");
        let path = create_session_in(&root, cwd(), &id).unwrap();

        let contents = fs::read_to_string(&path).unwrap();
        let (line, rest) = contents
            .split_once('\n')
            .expect("one header line, newline-terminated for the records to come");
        assert!(rest.is_empty());
        let header: SessionHeader = serde_json::from_str(line).unwrap();
        assert_eq!(header.cwd, cwd());
        assert!(
            DateTime::parse_from_rfc3339(&header.ts).is_ok(),
            "ts is RFC 3339: {:?}",
            header.ts
        );
        assert!(
            line.starts_with(&format!("{{\"type\":\"session\",\"cwd\":\"{CWD}\",\"ts\":")),
            "field order stays readable: {line}"
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn index_holds_one_object_per_session() {
        let root = fresh_root("index");
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&root, cwd(), &id).unwrap();

        let raw = fs::read_to_string(root.join(INDEX_FILE)).unwrap();
        let value: serde_json::Value = serde_json::from_str(&raw).unwrap();
        let array = value.as_array().expect("index.json is an array");
        assert_eq!(array.len(), 1);
        let entry = &array[0];
        assert_eq!(entry["sessionId"], serde_json::json!(id.to_string()));
        assert_eq!(entry["cwd"], serde_json::json!(CWD));
        assert!(entry.get("title").is_none(), "untitled until a prompt");
        for key in ["createdAt", "updatedAt"] {
            let ts = entry[key]
                .as_str()
                .unwrap_or_else(|| panic!("{key} is a string"));
            assert!(
                DateTime::parse_from_rfc3339(ts).is_ok(),
                "{key} is RFC 3339"
            );
        }
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn create_list_delete_session() {
        let root = fresh_root("crud");
        let id = test_id("11111111-2222-4333-8444-555555555555");

        let path = create_session_in(&root, cwd(), &id).unwrap();
        assert!(session_exists_in(&root, cwd(), &id));

        let sessions = list_sessions_in(&root, Some(cwd())).unwrap();
        assert_eq!(sessions.len(), 1);
        assert_eq!(sessions[0].session_id.to_string(), id.to_string());
        assert_eq!(sessions[0].cwd, cwd());
        assert_eq!(sessions[0].title, None);
        assert!(
            sessions[0].updated_at.is_some(),
            "ACP reports last activity"
        );

        assert!(delete_session_in(&root, &id).unwrap());
        assert!(!path.exists());
        assert!(!session_exists_in(&root, cwd(), &id));
        assert!(list_sessions_in(&root, Some(cwd())).unwrap().is_empty());
        assert_eq!(read_index(&root), Vec::new(), "the index entry goes too");
        assert!(
            !delete_session_in(&root, &id).unwrap(),
            "second delete finds nothing"
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn sessions_are_scoped_per_workspace() {
        let root = fresh_root("scoped");
        let other = Path::new(OTHER_CWD);
        let mine = test_id("aaaaaaaa-0000-4000-8000-000000000001");
        let theirs = test_id("bbbbbbbb-0000-4000-8000-000000000002");

        create_session_in(&root, cwd(), &mine).unwrap();
        create_session_in(&root, other, &theirs).unwrap();

        assert!(session_exists_in(&root, cwd(), &mine));
        assert!(
            !session_exists_in(&root, other, &mine),
            "a session belongs to the workspace it was created in"
        );

        assert_eq!(
            ids(&list_sessions_in(&root, Some(cwd())).unwrap()),
            vec![mine.to_string()]
        );
        assert_eq!(
            ids(&list_sessions_in(&root, Some(other)).unwrap()),
            vec![theirs.to_string()]
        );

        let all = list_sessions_in(&root, None).unwrap();
        assert_eq!(all.len(), 2, "unfiltered list spans every workspace");
        assert!(all.iter().any(|s| s.cwd == cwd()));
        assert!(all.iter().any(|s| s.cwd == other));

        assert!(delete_session_in(&root, &theirs).unwrap());
        assert_eq!(list_sessions_in(&root, None).unwrap().len(), 1);
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn list_is_most_recently_updated_first() {
        let root = fresh_root("order");
        let first = test_id("aaaaaaaa-0000-4000-8000-000000000001");
        let second = test_id("bbbbbbbb-0000-4000-8000-000000000002");
        create_session_in(&root, cwd(), &first).unwrap();
        create_session_in(&root, cwd(), &second).unwrap();

        // Rewrite the timestamps rather than sleeping: two creates in the same
        // millisecond would otherwise leave the order to the id tie-break.
        let mut entries = read_index(&root);
        for entry in &mut entries {
            entry.updated_at = if entry.session_id == first.to_string() {
                "2026-09-18T10:00:00.000Z".to_owned()
            } else {
                "2026-09-18T09:00:00.000Z".to_owned()
            };
        }
        write_index(&root, &entries).unwrap();

        assert_eq!(
            ids(&list_sessions_in(&root, None).unwrap()),
            vec![first.to_string(), second.to_string()]
        );

        record_activity_in(&root, &second, None).unwrap();
        assert_eq!(
            ids(&list_sessions_in(&root, None).unwrap()),
            vec![second.to_string(), first.to_string()],
            "activity moves a session to the front"
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn first_prompt_titles_the_session() {
        let root = fresh_root("title");
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&root, cwd(), &id).unwrap();
        let created = read_index(&root)[0].created_at.clone();

        record_activity_in(&root, &id, Some("Rejigger sessions".to_owned())).unwrap();
        let entry = read_index(&root).remove(0);
        assert_eq!(entry.title.as_deref(), Some("Rejigger sessions"));
        assert_eq!(entry.created_at, created, "creation time does not move");
        assert!(entry.updated_at >= created);

        record_activity_in(&root, &id, Some("Something else".to_owned())).unwrap();
        assert_eq!(
            read_index(&root)[0].title.as_deref(),
            Some("Rejigger sessions"),
            "a later prompt does not rename the session"
        );

        let listed = list_sessions_in(&root, None).unwrap();
        assert_eq!(listed[0].title.as_deref(), Some("Rejigger sessions"));

        record_activity_in(&root, &test_id("not-a-session"), None)
            .expect("touching an unknown session is a no-op, not an error");
        assert_eq!(read_index(&root).len(), 1);
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn titles_come_from_the_first_nonblank_line() {
        assert_eq!(title_from_prompt("  hello  "), Some("hello".to_owned()));
        assert_eq!(
            title_from_prompt("\n\nfirst line\nsecond line"),
            Some("first line".to_owned())
        );
        assert_eq!(title_from_prompt(""), None);
        assert_eq!(title_from_prompt("   \n\t "), None);

        let long = "x".repeat(MAX_TITLE_CHARS + 10);
        let title = title_from_prompt(&long).unwrap();
        assert_eq!(
            title.chars().count(),
            MAX_TITLE_CHARS + 1,
            "plus the ellipsis"
        );
        assert!(title.ends_with('…'));

        let wide = "é".repeat(MAX_TITLE_CHARS);
        assert_eq!(
            title_from_prompt(&wide),
            Some(wide),
            "counts chars, not bytes"
        );
    }

    #[test]
    fn list_skips_entries_whose_file_is_gone() {
        let root = fresh_root("orphan");
        let kept = test_id("aaaaaaaa-0000-4000-8000-000000000001");
        let orphan = test_id("bbbbbbbb-0000-4000-8000-000000000002");
        create_session_in(&root, cwd(), &kept).unwrap();
        create_session_in(&root, cwd(), &orphan).unwrap();
        fs::remove_file(root.join(format!("{orphan}.jsonl"))).unwrap();

        assert_eq!(
            ids(&list_sessions_in(&root, None).unwrap()),
            vec![kept.to_string()],
            "everything listed can also be loaded"
        );
        assert!(!session_exists_in(&root, cwd(), &orphan));
        assert!(
            delete_session_in(&root, &orphan).unwrap(),
            "delete still clears the stale entry"
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn unusable_index_reads_as_empty() {
        let root = fresh_root("damaged");
        let id = test_id("11111111-2222-4333-8444-555555555555");
        create_session_in(&root, cwd(), &id).unwrap();
        fs::write(root.join(INDEX_FILE), "{not json").unwrap();

        assert!(list_sessions_in(&root, None).unwrap().is_empty());
        assert!(!session_exists_in(&root, cwd(), &id));

        // The next write replaces the damaged file.
        let other = test_id("bbbbbbbb-0000-4000-8000-000000000002");
        create_session_in(&root, cwd(), &other).unwrap();
        assert_eq!(
            ids(&list_sessions_in(&root, None).unwrap()),
            vec![other.to_string()]
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn list_on_missing_root_is_empty() {
        let root = test_root("missing").join("does-not-exist");
        assert!(!root.exists());
        assert!(list_sessions_in(&root, None).unwrap().is_empty());
        assert!(list_sessions_in(&root, Some(cwd())).unwrap().is_empty());
        assert!(!session_exists_in(&root, cwd(), &test_id("some-id")));
        assert!(!delete_session_in(&root, &test_id("some-id")).unwrap());
    }

    #[test]
    fn rejects_bad_session_ids() {
        let root = fresh_root("bad");
        fs::create_dir_all(&root).unwrap();
        for bad in ["", ".", "..", "a/b", "a\\b"] {
            let err = create_session_in(&root, cwd(), &test_id(bad)).unwrap_err();
            assert_eq!(err.kind(), ErrorKind::InvalidInput, "reject {bad:?}");
            assert!(!session_exists_in(&root, cwd(), &test_id(bad)));
            assert!(delete_session_in(&root, &test_id(bad)).is_err());
            assert!(record_activity_in(&root, &test_id(bad), None).is_err());
        }
        assert_eq!(
            create_session_in(&root, Path::new(""), &test_id("ok"))
                .unwrap_err()
                .kind(),
            ErrorKind::InvalidInput,
            "a session needs a workspace"
        );
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn create_is_idempotent() {
        let root = fresh_root("idempotent");
        let id = test_id("some-id");
        let path = create_session_in(&root, cwd(), &id).unwrap();
        record_activity_in(&root, &id, Some("Keep me".to_owned())).unwrap();
        let after_first = fs::read_to_string(&path).unwrap();
        let index_after_first = read_index(&root);

        // Second create leaves the existing file alone instead of truncating
        // it or writing a second header, and does not clear the title.
        create_session_in(&root, cwd(), &id).unwrap();
        assert_eq!(fs::read_to_string(&path).unwrap(), after_first);
        assert_eq!(read_index(&root), index_after_first);
        assert_eq!(list_sessions_in(&root, Some(cwd())).unwrap().len(), 1);
        let _ = fs::remove_dir_all(&root);
    }
}
