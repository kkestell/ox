use std::{
    collections::HashSet,
    fs, io,
    io::Read,
    path::{Component, Path, PathBuf},
};

use super::workspace::Workspace;

struct Patch(Vec<FileOperation>);

struct FileOperation {
    path: String,
    kind: Operation,
}

enum Operation {
    Add(String),
    Delete,
    Update {
        destination: Option<String>,
        chunks: Vec<Chunk>,
    },
}

struct Chunk {
    anchor: Option<String>,
    old: Vec<String>,
    new: Vec<String>,
}

impl Patch {
    fn parse(input: &str) -> Result<Self, String> {
        Parser {
            lines: input.lines().map(str::to_owned).collect(),
            index: 0,
        }
        .parse()
    }
}

/// A cursor over a patch's lines. Each method reads the part of the patch
/// format it names and leaves `index` on the first line after it.
struct Parser {
    lines: Vec<String>,
    index: usize,
}

impl Parser {
    fn parse(mut self) -> Result<Patch, String> {
        if self.line() != Some("*** Begin Patch") {
            return Err(Self::error(0, "expected *** Begin Patch"));
        }
        self.index += 1;
        let mut operations = Vec::new();
        while let Some(line) = self.line() {
            if line == "*** End Patch" {
                if self.index + 1 != self.lines.len() {
                    return Err(Self::error(self.index + 1, "text after *** End Patch"));
                }
                return Ok(Patch(operations));
            }
            operations.push(self.file_operation()?);
        }
        Err(Self::error(self.index, "expected *** End Patch"))
    }

    fn line(&self) -> Option<&str> {
        self.lines.get(self.index).map(String::as_str)
    }

    fn file_operation(&mut self) -> Result<FileOperation, String> {
        let header_index = self.index;
        let header = self.lines[header_index].clone();
        self.index += 1;
        let (path, kind) = if let Some(path) = header.strip_prefix("*** Add File: ") {
            (path, self.add_file())
        } else if let Some(path) = header.strip_prefix("*** Delete File: ") {
            (path, Operation::Delete)
        } else if let Some(path) = header.strip_prefix("*** Update File: ") {
            (path, self.update_file()?)
        } else {
            return Err(Self::error(
                header_index,
                "expected a file operation or *** End Patch; invalid header or body prefix",
            ));
        };
        if path.is_empty() {
            return Err(Self::error(header_index, "file path is empty"));
        }
        Ok(FileOperation {
            path: path.to_owned(),
            kind,
        })
    }

    fn add_file(&mut self) -> Operation {
        let mut contents = String::new();
        while let Some(body) = self.line().and_then(|line| line.strip_prefix('+')) {
            contents.push_str(body);
            contents.push('\n');
            self.index += 1;
        }
        Operation::Add(contents)
    }

    fn update_file(&mut self) -> Result<Operation, String> {
        let destination = self
            .line()
            .and_then(|line| line.strip_prefix("*** Move to: "))
            .map(str::to_owned);
        if let Some(destination) = &destination {
            if destination.is_empty() {
                return Err(Self::error(self.index, "move destination is empty"));
            }
            self.index += 1;
        }
        let mut chunks = Vec::new();
        while let Some(chunk) = self.chunk()? {
            chunks.push(chunk);
        }
        if destination.is_none() && chunks.is_empty() {
            return Err(Self::error(
                self.index,
                "Update File requires chunks or Move to",
            ));
        }
        Ok(Operation::Update {
            destination,
            chunks,
        })
    }

    /// Reads one chunk, or returns `None` when the next line is not a chunk
    /// header.
    fn chunk(&mut self) -> Result<Option<Chunk>, String> {
        let anchor = match self.line() {
            Some("@@") => None,
            Some(line) if line.starts_with("@@ ") => Some(line["@@ ".len()..].to_owned()),
            _ => return Ok(None),
        };
        self.index += 1;
        let mut chunk = Chunk {
            anchor,
            old: Vec::new(),
            new: Vec::new(),
        };
        while let Some(body) = self.line() {
            match body.as_bytes().first() {
                Some(b' ') => {
                    chunk.old.push(body[1..].to_owned());
                    chunk.new.push(body[1..].to_owned());
                }
                Some(b'-') => chunk.old.push(body[1..].to_owned()),
                Some(b'+') => chunk.new.push(body[1..].to_owned()),
                _ => break,
            }
            self.index += 1;
        }
        if chunk.old.is_empty() && chunk.new.is_empty() {
            return Err(Self::error(
                self.index,
                "chunk must contain context, removed, or added lines",
            ));
        }
        Ok(Some(chunk))
    }

    fn error(index: usize, reason: &str) -> String {
        format!("parse: line {}: {reason}", index + 1)
    }
}

fn update(source: &str, chunks: &[Chunk]) -> Result<String, String> {
    let ending = match source.split_once('\n') {
        Some((first, _)) if first.ends_with('\r') => "\r\n",
        _ => "\n",
    };
    let mut lines: Vec<String> = source.lines().map(str::to_owned).collect();
    let mut cursor = 0;
    for (index, chunk) in chunks.iter().enumerate() {
        if let Some(anchor) = &chunk.anchor {
            cursor += lines[cursor..]
                .iter()
                .position(|line| line == anchor)
                .ok_or_else(|| format!("chunk {}: anchor not found", index + 1))?
                + 1;
        }
        let start = if chunk.old.is_empty() {
            if index == 0 && chunk.anchor.is_none() {
                lines.len()
            } else {
                cursor
            }
        } else {
            cursor
                + lines[cursor..]
                    .windows(chunk.old.len())
                    .position(|window| window == chunk.old)
                    .ok_or_else(|| format!("chunk {}: exact context not found", index + 1))?
        };
        lines.splice(start..start + chunk.old.len(), chunk.new.clone());
        cursor = start + chunk.new.len();
    }
    let mut contents = lines.join(ending);
    if !lines.is_empty() && source.ends_with('\n') {
        contents.push_str(ending);
    }
    Ok(contents)
}

// Resolve existing components individually so even a symlink followed by a
// nonexistent child cannot lead outside the workspace.
fn workspace_path_rejecting_links(root: &Path, name: &str) -> Result<PathBuf, String> {
    let mut path = root.to_path_buf();
    let mut has_name = false;
    let mut symlink = false;
    for component in Path::new(name).components() {
        let component = match component {
            Component::Normal(component) => component,
            Component::CurDir => continue,
            _ => return Err("expected a relative path without parent traversal".to_owned()),
        };
        has_name = true;
        path.push(component);
        match fs::symlink_metadata(&path) {
            Ok(metadata) => {
                symlink = metadata.is_symlink();
                path = path.canonicalize().map_err(|error| error.to_string())?;
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => symlink = false,
            Err(error) => return Err(error.to_string()),
        }
        if !path.starts_with(root) {
            return Err("path resolves outside the workspace".to_owned());
        }
    }
    if !has_name || path == root {
        return Err("path names the workspace root".to_owned());
    }
    if symlink {
        return Err("path is a symbolic link".to_owned());
    }
    Ok(path)
}

fn target(
    workspace: &Workspace,
    name: &str,
    seen: &mut HashSet<PathBuf>,
) -> Result<PathBuf, String> {
    let path = workspace_path_rejecting_links(workspace.root(), name)?;
    let path = workspace
        .relative(&path)
        .map_err(|error| error.to_string())?;
    if !seen.insert(path.clone()) {
        return Err("duplicate target".to_owned());
    }
    Ok(path)
}

fn require_absent(path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error.to_string()),
        Ok(_) => Err("destination already exists".to_owned()),
    }
}

struct Prepared {
    summary: String,
    change: Change,
}

enum Change {
    Write {
        path: PathBuf,
        contents: Vec<u8>,
        create: bool,
    },
    Delete(PathBuf),
    Move {
        source: PathBuf,
        destination: PathBuf,
        contents: Option<Vec<u8>>,
    },
    Unchanged,
}

fn prepare(root: &Path, patch: Patch) -> Result<(Workspace, Vec<Prepared>), String> {
    let workspace = Workspace::open(root).map_err(|error| format!("workspace: {error}"))?;
    let mut seen = HashSet::new();
    let mut prepared = Vec::new();
    for operation in patch.0 {
        let name = operation.path;
        let prepare_operation = || -> Result<Prepared, String> {
            let path = target(&workspace, &name, &mut seen)?;
            let mut source_file = match &operation.kind {
                Operation::Add(_) => None,
                _ => Some(
                    workspace
                        .read_file(&path)
                        .map_err(|error| error.to_string())?,
                ),
            };
            let (summary, change) = match operation.kind {
                Operation::Add(contents) => {
                    require_absent(&workspace.root().join(&path))?;
                    (
                        format!("Added {name}"),
                        Change::Write {
                            path,
                            contents: contents.into_bytes(),
                            create: true,
                        },
                    )
                }
                Operation::Delete => (format!("Deleted {name}"), Change::Delete(path)),
                Operation::Update {
                    destination,
                    chunks,
                } => {
                    let contents = if chunks.is_empty() {
                        None
                    } else {
                        let mut source = String::new();
                        source_file
                            .as_mut()
                            .expect("an update has an opened source file")
                            .read_to_string(&mut source)
                            .map_err(|error| error.to_string())?;
                        let contents = update(&source, &chunks)?;
                        (contents != source).then(|| contents.into_bytes())
                    };
                    if let Some(destination_name) = destination {
                        let destination = target(&workspace, &destination_name, &mut seen)
                            .and_then(|path| {
                                require_absent(&workspace.root().join(&path))?;
                                Ok(path)
                            })
                            .map_err(|error| format!("destination {destination_name}: {error}"))?;
                        (
                            format!("Moved {name} -> {destination_name}"),
                            Change::Move {
                                source: path,
                                destination,
                                contents,
                            },
                        )
                    } else if let Some(contents) = contents {
                        (
                            format!("Modified {name}"),
                            Change::Write {
                                path,
                                contents,
                                create: false,
                            },
                        )
                    } else {
                        (format!("Unchanged {name}"), Change::Unchanged)
                    }
                }
            };
            Ok(Prepared { summary, change })
        };
        prepared.push(prepare_operation().map_err(|error| format!("{name}: {error}"))?);
    }
    Ok((workspace, prepared))
}

impl Change {
    fn apply(&self, workspace: &Workspace) -> io::Result<()> {
        match self {
            Self::Write {
                path,
                contents,
                create,
            } => workspace.write_file(path, contents, *create),
            Self::Delete(path) => workspace.remove_file(path),
            Self::Move {
                source,
                destination,
                contents,
            } => {
                if let Some(contents) = contents {
                    workspace.write_file(destination, contents, true)?;
                    workspace.remove_file(source)
                } else {
                    workspace.move_file(source, destination)
                }
            }
            Self::Unchanged => Ok(()),
        }
    }
}

fn apply_prepared(workspace: &Workspace, prepared: &[Prepared]) -> Result<String, String> {
    let mut completed = Vec::new();
    for (index, operation) in prepared.iter().enumerate() {
        if let Err(error) = operation.change.apply(workspace) {
            let remaining: Vec<_> = prepared[index + 1..]
                .iter()
                .map(|op| op.summary.as_str())
                .collect();
            return Err(format!(
                "apply: failed {}: {error}\nThe failed operation may be partially applied.\nCompleted:\n{}\nNot attempted:\n{}",
                operation.summary,
                if completed.is_empty() {
                    "(none)".to_owned()
                } else {
                    completed.join("\n")
                },
                if remaining.is_empty() {
                    "(none)".to_owned()
                } else {
                    remaining.join("\n")
                },
            ));
        }
        completed.push(operation.summary.as_str());
    }
    let mut summary = "Applied patch.".to_owned();
    for line in completed {
        summary.push('\n');
        summary.push_str(line);
    }
    Ok(summary)
}

/// The paths a patch names, for display. A patch that does not parse names
/// none; the failure is reported when the patch runs.
pub(super) fn changed_paths(input: &str) -> Vec<String> {
    match Patch::parse(input) {
        Ok(patch) => patch.0.into_iter().map(|file| file.path).collect(),
        Err(_) => Vec::new(),
    }
}

pub(super) fn apply(workspace: &Path, input: &str) -> Result<String, String> {
    let patch = Patch::parse(input)?;
    let (workspace, prepared) =
        prepare(workspace, patch).map_err(|error| format!("prepare: {error}"))?;
    apply_prepared(&workspace, &prepared)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::fixture::Workspace;

    fn wrapped(body: &str) -> String {
        format!("*** Begin Patch\n{body}*** End Patch\n")
    }

    fn edit(source: &str, body: &str) -> Result<String, String> {
        let patch = Patch::parse(&wrapped(&format!("*** Update File: file\n{body}"))).unwrap();
        let Operation::Update { chunks, .. } = &patch.0[0].kind else {
            panic!()
        };
        update(source, chunks)
    }

    #[test]
    fn creates_updates_moves_and_deletes_in_order() {
        let workspace = Workspace::new();
        fs::create_dir(workspace.0.join("src")).unwrap();
        fs::write(
            workspace.0.join("src/greeting.rs"),
            "fn greeting() -> &'static str {\n    \"Hello\"\n}\n",
        )
        .unwrap();
        fs::write(workspace.0.join("old-name.txt"), "Old text\n").unwrap();
        fs::write(workspace.0.join("obsolete.txt"), "obsolete").unwrap();
        let patch = "*** Begin Patch
*** Add File: notes.txt
+New notes.
*** Update File: src/greeting.rs
@@ fn greeting() -> &'static str {
-    \"Hello\"
+    \"Hello, world\"
 }
*** Update File: old-name.txt
*** Move to: new-name.txt
@@
-Old text
+New text
*** Delete File: obsolete.txt
*** End Patch
";
        assert_eq!(
            apply(&workspace.0, patch).unwrap(),
            "Applied patch.\nAdded notes.txt\nModified src/greeting.rs\nMoved old-name.txt -> new-name.txt\nDeleted obsolete.txt"
        );
        assert_eq!(
            fs::read_to_string(workspace.0.join("notes.txt")).unwrap(),
            "New notes.\n"
        );
        assert_eq!(
            fs::read_to_string(workspace.0.join("src/greeting.rs")).unwrap(),
            "fn greeting() -> &'static str {\n    \"Hello, world\"\n}\n"
        );
        assert_eq!(
            fs::read_to_string(workspace.0.join("new-name.txt")).unwrap(),
            "New text\n"
        );
        assert!(!workspace.0.join("old-name.txt").exists());
        assert!(!workspace.0.join("obsolete.txt").exists());
    }

    #[test]
    fn empty_files_blank_lines_unchanged_updates_and_byte_preserving_moves() {
        let workspace = Workspace::new();
        assert_eq!(apply(&workspace.0, &wrapped("")).unwrap(), "Applied patch.");
        apply(
            &workspace.0,
            &wrapped("*** Add File: empty\n*** Add File: nested/blank\n+\n+*** End Patch\n"),
        )
        .unwrap();
        assert_eq!(fs::read(workspace.0.join("empty")).unwrap(), b"");
        assert_eq!(
            fs::read(workspace.0.join("nested/blank")).unwrap(),
            b"\n*** End Patch\n"
        );
        assert_eq!(
            apply(
                &workspace.0,
                &wrapped("*** Update File: nested/blank\n@@\n \n *** End Patch\n")
            )
            .unwrap(),
            "Applied patch.\nUnchanged nested/blank"
        );
        fs::write(workspace.0.join("bytes"), b"\xff\r\n\x00").unwrap();
        assert_eq!(
            apply(
                &workspace.0,
                &wrapped("*** Update File: bytes\n*** Move to: moved/bytes\n")
            )
            .unwrap(),
            "Applied patch.\nMoved bytes -> moved/bytes"
        );
        assert_eq!(
            fs::read(workspace.0.join("moved/bytes")).unwrap(),
            b"\xff\r\n\x00"
        );
        assert!(!workspace.0.join("bytes").exists());
    }

    #[test]
    fn matching_is_exact_literal_and_forward() {
        assert_eq!(
            edit("x\nx\nx\n", "@@\n-x\n+y\n@@\n-x\n+z\n").unwrap(),
            "y\nz\nx\n"
        );
        assert_eq!(
            edit("x\n.*\nx\nx\n", "@@ .*\n-x\n+y\n").unwrap(),
            "x\n.*\ny\nx\n"
        );
        assert_eq!(edit("a\nb\n", "@@\n+end\n").unwrap(), "a\nb\nend\n");
        assert_eq!(
            edit("a\nb\n", "@@ a\n+middle\n@@\n+next\n").unwrap(),
            "a\nmiddle\nnext\nb\n"
        );
        assert_eq!(
            edit("a\nb\n", "@@\n-a\n+A\n@@\n+middle\n").unwrap(),
            "A\nmiddle\nb\n"
        );
        for (source, body) in [
            (" x\n", "@@\n-x\n+y\n"),
            ("X\n", "@@\n-x\n+y\n"),
            ("a\nb\n", "@@\n-b\n+B\n@@\n-a\n+A\n"),
            ("a\nb\n", "@@ missing\n+b\n"),
            ("a\nb\n", "@@ a\n-a\n+A\n"),
        ] {
            assert!(edit(source, body).unwrap_err().contains("chunk"));
        }
    }

    #[test]
    fn updates_preserve_line_endings_and_final_newline() {
        for source in ["a\nb\n", "a\nb", "a\r\nb\r\n", "a\r\nb"] {
            assert_eq!(
                edit(source, "@@\n-a\n+A\n").unwrap(),
                source.replacen('a', "A", 1)
            );
        }
        // A mixed file is rewritten in the style of its first line ending.
        assert_eq!(edit("a\nb\r\nc\n", "@@\n-a\n+A\n").unwrap(), "A\nb\nc\n");
        assert_eq!(
            edit("a\r\nb\nc\r\n", "@@\n-a\n+A\n").unwrap(),
            "A\r\nb\r\nc\r\n"
        );
        assert_eq!(edit("a\r\n", "@@\n-a\n").unwrap(), "");
        assert_eq!(edit("", "@@\n+new\n").unwrap(), "new");
        assert_eq!(edit("a\n", "@@\n-a\n+\n").unwrap(), "\n");
    }

    #[test]
    fn malformed_patches_report_lines_and_never_write() {
        let workspace = Workspace::new();
        for (input, error) in [
            ("", "line 1: expected *** Begin Patch"),
            (
                " *** Begin Patch\n*** End Patch",
                "line 1: expected *** Begin Patch",
            ),
            ("*** Begin Patch", "line 2: expected *** End Patch"),
            (
                "*** Begin Patch\n*** End Patch\nextra",
                "line 3: text after *** End Patch",
            ),
            (
                "*** Begin Patch\n*** Add File: first\n+ok\ninvalid\n*** End Patch",
                "line 4: expected a file operation or *** End Patch; invalid header or body prefix",
            ),
            (
                "*** Begin Patch\n*** Add File: first\n+ok\n*** Update File: x\n@@\n?bad\n*** End Patch",
                "line 6: chunk must contain context, removed, or added lines",
            ),
            (
                "*** Begin Patch\n*** Add File: first\n+ok\n*** Delete File: x\n-body\n*** End Patch",
                "line 5: expected a file operation or *** End Patch; invalid header or body prefix",
            ),
            (
                "*** Begin Patch\n*** Add File: \n*** End Patch",
                "line 2: file path is empty",
            ),
            (
                "*** Begin Patch\n*** Update File: x\n*** End Patch",
                "line 3: Update File requires chunks or Move to",
            ),
            (
                "*** Begin Patch\n*** Update File: x\n@@ -1 +1 @@\n*** End Patch",
                "line 4: chunk must contain context, removed, or added lines",
            ),
            (
                "*** Begin Patch\n*** Update File: x\n*** Move to: \n*** End Patch",
                "line 3: move destination is empty",
            ),
        ] {
            assert_eq!(
                apply(&workspace.0, input).unwrap_err(),
                format!("parse: {error}"),
                "{input}"
            );
            assert_eq!(fs::read_dir(&workspace.0).unwrap().count(), 0);
        }
    }

    #[test]
    fn preparation_rejects_invalid_operations_without_changes() {
        let workspace = Workspace::new();
        fs::write(workspace.0.join("existing"), "original\n").unwrap();
        fs::write(workspace.0.join("binary"), b"\xff").unwrap();
        fs::create_dir(workspace.0.join("directory")).unwrap();
        for body in [
            "*** Add File: /outside\n+x\n",
            "*** Add File: ../outside\n+x\n",
            "*** Add File: directory/../outside\n+x\n",
            "*** Add File: .\n+x\n",
            "*** Delete File: missing\n",
            "*** Update File: missing\n@@\n-x\n+y\n",
            "*** Update File: existing\n@@\n-wrong\n+y\n",
            "*** Update File: binary\n@@\n+x\n",
            "*** Add File: existing\n+x\n",
            "*** Delete File: directory\n",
            "*** Update File: directory\n*** Move to: new\n",
            "*** Update File: existing\n*** Move to: directory\n",
            "*** Update File: existing\n*** Move to: existing\n",
            "*** Add File: duplicate\n*** Add File: ./duplicate\n",
            "*** Update File: existing\n*** Move to: dest\n*** Add File: dest\n",
        ] {
            let input = wrapped(&format!("*** Add File: first\n+ok\n{body}"));
            let error = apply(&workspace.0, &input).unwrap_err();
            assert!(error.starts_with("prepare:"), "{error}");
            assert!(!workspace.0.join("first").exists());
            assert_eq!(
                fs::read_to_string(workspace.0.join("existing")).unwrap(),
                "original\n"
            );
            assert_eq!(fs::read_dir(&workspace.0).unwrap().count(), 3);
        }
    }

    #[cfg(unix)]
    #[test]
    fn symlinks_are_never_targets_and_cannot_escape_or_hide_duplicates() {
        use std::os::unix::fs::symlink;
        let workspace = Workspace::new();
        let outside = Workspace::new();
        fs::write(outside.0.join("file"), "outside").unwrap();
        symlink(&outside.0, workspace.0.join("escape")).unwrap();
        symlink(outside.0.join("file"), workspace.0.join("file-link")).unwrap();
        symlink(outside.0.join("missing"), workspace.0.join("dangling")).unwrap();
        symlink(&workspace.0, workspace.0.join("root-link")).unwrap();
        fs::write(workspace.0.join("inside"), "inside\n").unwrap();
        symlink(workspace.0.join("inside"), workspace.0.join("inside-link")).unwrap();
        for body in [
            "*** Add File: escape/new/nested\n+x\n",
            "*** Delete File: file-link\n",
            "*** Add File: dangling\n+x\n",
            "*** Delete File: root-link\n",
            "*** Add File: name\n*** Add File: root-link/name\n",
            "*** Delete File: inside-link\n",
            "*** Update File: inside-link\n@@\n-inside\n+changed\n",
            "*** Update File: inside-link\n*** Move to: moved\n",
        ] {
            assert!(
                apply(&workspace.0, &wrapped(body))
                    .unwrap_err()
                    .starts_with("prepare:")
            );
        }
        assert_eq!(
            fs::read_to_string(outside.0.join("file")).unwrap(),
            "outside"
        );
        assert_eq!(fs::read_dir(&outside.0).unwrap().count(), 1);
        assert_eq!(
            fs::read_to_string(workspace.0.join("inside")).unwrap(),
            "inside\n"
        );
        assert!(fs::symlink_metadata(workspace.0.join("inside-link")).is_ok());

        let patch =
            Patch::parse(&wrapped("*** Update File: inside\n@@\n-inside\n+changed\n")).unwrap();
        let (root, prepared) = prepare(&workspace.0, patch).unwrap();
        fs::remove_file(workspace.0.join("inside")).unwrap();
        symlink(outside.0.join("file"), workspace.0.join("inside")).unwrap();
        assert!(apply_prepared(&root, &prepared).is_err());
        assert_eq!(
            fs::read_to_string(outside.0.join("file")).unwrap(),
            "outside"
        );
    }

    #[test]
    fn application_failure_reports_completed_failed_and_unattempted_operations() {
        let workspace = Workspace::new();
        // Each target is absent during preparation, but the first operation
        // creates a file where the second operation needs a parent directory.
        let error = apply(&workspace.0, &wrapped("*** Add File: parent\n+file\n*** Add File: parent/child\n+child\n*** Add File: later\n+later\n")).unwrap_err();
        assert!(
            error.starts_with("apply: failed Added parent/child:"),
            "{error}"
        );
        assert!(
            error.contains("Completed:\nAdded parent\nNot attempted:\nAdded later"),
            "{error}"
        );
        assert_eq!(
            fs::read_to_string(workspace.0.join("parent")).unwrap(),
            "file\n"
        );
        assert!(!workspace.0.join("later").exists());
    }
}
