use std::{
    collections::HashSet,
    fs, io,
    path::{Component, Path, PathBuf},
};

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
        let lines: Vec<_> = input.lines().collect();
        let error = |index: usize, reason: &str| format!("parse: line {}: {reason}", index + 1);
        if lines.first() != Some(&"*** Begin Patch") {
            return Err(error(0, "expected *** Begin Patch"));
        }
        let mut operations = Vec::new();
        let mut index = 1;
        while index < lines.len() {
            let header_index = index;
            let line = lines[index];
            if line == "*** End Patch" {
                if index + 1 != lines.len() {
                    return Err(error(index + 1, "text after *** End Patch"));
                }
                return Ok(Self(operations));
            }
            let (path, kind) = if let Some(path) = line.strip_prefix("*** Add File: ") {
                index += 1;
                let mut contents = String::new();
                while let Some(body) = lines.get(index).and_then(|line| line.strip_prefix('+')) {
                    contents.push_str(body);
                    contents.push('\n');
                    index += 1;
                }
                (path, Operation::Add(contents))
            } else if let Some(path) = line.strip_prefix("*** Delete File: ") {
                index += 1;
                (path, Operation::Delete)
            } else if let Some(path) = line.strip_prefix("*** Update File: ") {
                index += 1;
                let destination = lines
                    .get(index)
                    .and_then(|line| line.strip_prefix("*** Move to: "));
                if let Some(destination) = destination {
                    if destination.is_empty() {
                        return Err(error(index, "move destination is empty"));
                    }
                    index += 1;
                }
                let mut chunks = Vec::new();
                while let Some(header) = lines.get(index) {
                    let anchor = if *header == "@@" {
                        None
                    } else if let Some(anchor) = header.strip_prefix("@@ ") {
                        Some(anchor.to_owned())
                    } else {
                        break;
                    };
                    index += 1;
                    let mut chunk = Chunk {
                        anchor,
                        old: Vec::new(),
                        new: Vec::new(),
                    };
                    while let Some(body) = lines.get(index) {
                        match body.as_bytes().first() {
                            Some(b' ') => {
                                chunk.old.push(body[1..].to_owned());
                                chunk.new.push(body[1..].to_owned());
                            }
                            Some(b'-') => chunk.old.push(body[1..].to_owned()),
                            Some(b'+') => chunk.new.push(body[1..].to_owned()),
                            _ => break,
                        }
                        index += 1;
                    }
                    if chunk.old.is_empty() && chunk.new.is_empty() {
                        return Err(error(
                            index,
                            "chunk must contain context, removed, or added lines",
                        ));
                    }
                    chunks.push(chunk);
                }
                if destination.is_none() && chunks.is_empty() {
                    return Err(error(index, "Update File requires chunks or Move to"));
                }
                (
                    path,
                    Operation::Update {
                        destination: destination.map(str::to_owned),
                        chunks,
                    },
                )
            } else {
                return Err(error(
                    index,
                    "expected a file operation or *** End Patch; invalid header or body prefix",
                ));
            };
            if path.is_empty() {
                return Err(error(header_index, "file path is empty"));
            }
            operations.push(FileOperation {
                path: path.to_owned(),
                kind,
            });
        }
        Err(error(index, "expected *** End Patch"))
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
fn resolve(root: &Path, name: &str) -> Result<PathBuf, String> {
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

fn target(root: &Path, name: &str, seen: &mut HashSet<PathBuf>) -> Result<PathBuf, String> {
    let path = resolve(root, name)?;
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
    Write(PathBuf, Vec<u8>),
    Delete(PathBuf),
    Move {
        source: PathBuf,
        destination: PathBuf,
        contents: Option<Vec<u8>>,
    },
    Noop,
}

fn preflight(root: &Path, patch: Patch) -> Result<Vec<Prepared>, String> {
    let root = root
        .canonicalize()
        .map_err(|error| format!("workspace: {error}"))?;
    let mut seen = HashSet::new();
    let mut prepared = Vec::new();
    for operation in patch.0 {
        let name = operation.path;
        let prepare = || -> Result<Prepared, String> {
            let path = target(&root, &name, &mut seen)?;
            if !matches!(&operation.kind, Operation::Add(_))
                && !fs::metadata(&path)
                    .map_err(|error| error.to_string())?
                    .is_file()
            {
                return Err("source must be a regular file".to_owned());
            }
            let (summary, change) = match operation.kind {
                Operation::Add(contents) => {
                    require_absent(&path)?;
                    (
                        format!("A {name}"),
                        Change::Write(path, contents.into_bytes()),
                    )
                }
                Operation::Delete => (format!("D {name}"), Change::Delete(path)),
                Operation::Update {
                    destination,
                    chunks,
                } => {
                    let contents = if chunks.is_empty() {
                        None
                    } else {
                        let source =
                            fs::read_to_string(&path).map_err(|error| error.to_string())?;
                        let contents = update(&source, &chunks)?;
                        (contents != source).then(|| contents.into_bytes())
                    };
                    if let Some(destination_name) = destination {
                        let destination = target(&root, &destination_name, &mut seen)
                            .and_then(|path| {
                                require_absent(&path)?;
                                Ok(path)
                            })
                            .map_err(|error| format!("destination {destination_name}: {error}"))?;
                        (
                            format!("R {name} -> {destination_name}"),
                            Change::Move {
                                source: path,
                                destination,
                                contents,
                            },
                        )
                    } else if let Some(contents) = contents {
                        (format!("M {name}"), Change::Write(path, contents))
                    } else {
                        (format!("N {name}"), Change::Noop)
                    }
                }
            };
            Ok(Prepared { summary, change })
        };
        prepared.push(prepare().map_err(|error| format!("{name}: {error}"))?);
    }
    Ok(prepared)
}

fn create_parents(path: &Path) -> io::Result<()> {
    fs::create_dir_all(path.parent().expect("resolved file has a parent"))
}

impl Change {
    fn apply(&self) -> io::Result<()> {
        match self {
            Self::Write(path, contents) => {
                create_parents(path)?;
                fs::write(path, contents)
            }
            Self::Delete(path) => fs::remove_file(path),
            Self::Move {
                source,
                destination,
                contents,
            } => {
                create_parents(destination)?;
                if let Some(contents) = contents {
                    fs::write(destination, contents)?;
                    fs::remove_file(source)
                } else {
                    fs::rename(source, destination)
                }
            }
            Self::Noop => Ok(()),
        }
    }
}

fn apply_prepared(prepared: &[Prepared]) -> Result<String, String> {
    let mut completed = Vec::new();
    for (index, operation) in prepared.iter().enumerate() {
        if let Err(error) = operation.change.apply() {
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
    let prepared = preflight(workspace, patch).map_err(|error| format!("preflight: {error}"))?;
    apply_prepared(&prepared)
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
    fn example_creates_updates_moves_and_deletes_in_order() {
        let workspace = Workspace::new();
        fs::create_dir(workspace.0.join("src")).unwrap();
        fs::write(
            workspace.0.join("src/greeting.rs"),
            "fn greeting() -> &'static str {\n    \"Hello\"\n}\n",
        )
        .unwrap();
        fs::write(workspace.0.join("old-name.txt"), "Old text\n").unwrap();
        fs::write(workspace.0.join("obsolete.txt"), "obsolete").unwrap();
        let example = include_str!("patch-guide.txt")
            .split("```text\n")
            .nth(1)
            .unwrap()
            .split("```")
            .next()
            .unwrap();
        assert_eq!(
            apply(&workspace.0, example).unwrap(),
            "Applied patch.\nA notes.txt\nM src/greeting.rs\nR old-name.txt -> new-name.txt\nD obsolete.txt"
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
    fn empty_files_blank_lines_noops_and_byte_preserving_moves() {
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
            "Applied patch.\nN nested/blank"
        );
        fs::write(workspace.0.join("bytes"), b"\xff\r\n\x00").unwrap();
        assert_eq!(
            apply(
                &workspace.0,
                &wrapped("*** Update File: bytes\n*** Move to: moved/bytes\n")
            )
            .unwrap(),
            "Applied patch.\nR bytes -> moved/bytes"
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
        for input in [
            "",
            " *** Begin Patch\n*** End Patch",
            "*** Begin Patch",
            "*** Begin Patch\n*** End Patch\nextra",
            "*** Begin Patch\n*** Add File: first\n+ok\ninvalid\n*** End Patch",
            "*** Begin Patch\n*** Add File: first\n+ok\n*** Update File: x\n@@\n?bad\n*** End Patch",
            "*** Begin Patch\n*** Add File: first\n+ok\n*** Delete File: x\n-body\n*** End Patch",
            "*** Begin Patch\n*** Update File: x\n*** End Patch",
            "*** Begin Patch\n*** Update File: x\n@@ -1 +1 @@\n*** End Patch",
            "*** Begin Patch\n*** Update File: x\n*** Move to: \n*** End Patch",
        ] {
            assert!(
                apply(&workspace.0, input)
                    .unwrap_err()
                    .starts_with("parse: line"),
                "{input}"
            );
            assert_eq!(fs::read_dir(&workspace.0).unwrap().count(), 0);
        }
    }

    #[test]
    fn preflight_rejects_invalid_operations_without_changes() {
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
            assert!(error.starts_with("preflight:"), "{error}");
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
                    .starts_with("preflight:")
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
    }

    #[test]
    fn application_failure_reports_completed_failed_and_unattempted_operations() {
        let workspace = Workspace::new();
        // Each target is absent at preflight, but the first operation creates a
        // file where the second operation needs a parent directory.
        let error = apply(&workspace.0, &wrapped("*** Add File: parent\n+file\n*** Add File: parent/child\n+child\n*** Add File: later\n+later\n")).unwrap_err();
        assert!(
            error.starts_with("apply: failed A parent/child:"),
            "{error}"
        );
        assert!(
            error.contains("Completed:\nA parent\nNot attempted:\nA later"),
            "{error}"
        );
        assert_eq!(
            fs::read_to_string(workspace.0.join("parent")).unwrap(),
            "file\n"
        );
        assert!(!workspace.0.join("later").exists());
    }
}
