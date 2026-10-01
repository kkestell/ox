use std::{
    io::{self, Read},
    path::{Path, PathBuf},
};

use serde::Deserialize;
use serde_json::{Value, json};

use super::{EDIT_FILE, WRITE_FILE, workspace::Workspace};
use crate::sessions::ToolContent;

pub(super) fn write_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": WRITE_FILE,
            "description": "Create or overwrite one UTF-8 regular file in the workspace, creating missing parent directories. Paths are relative to the workspace or absolute inside it, without parent traversal or symbolic links. Content is literal: line endings and final newlines are supplied by you. Empty content creates or truncates an empty file. Example: {\"path\":\"notes/today.txt\",\"content\":\"Hello\\n\"}.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "File path relative to the workspace or absolute inside it."},
                    "content": {"type": "string", "description": "Complete file content."}
                },
                "required": ["path", "content"],
                "additionalProperties": false
            }
        }
    })
}

pub(super) fn edit_schema() -> Value {
    json!({
        "type": "function",
        "function": {
            "name": EDIT_FILE,
            "description": "Replace exactly one occurrence of old_text in an existing UTF-8 regular file. old_text must be nonempty and unique; include surrounding context if ambiguous. Empty new_text deletes the match. Strings are literal, with no regular expressions, escape interpretation, or newline normalization. Paths are relative to the workspace or absolute inside it, without parent traversal or symbolic links. Example: {\"path\":\"notes/today.txt\",\"old_text\":\"Hello\",\"new_text\":\"Goodbye\"}.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "File path relative to the workspace or absolute inside it."},
                    "old_text": {"type": "string", "description": "Nonempty exact text that occurs once."},
                    "new_text": {"type": "string", "description": "Literal replacement text."}
                },
                "required": ["path", "old_text", "new_text"],
                "additionalProperties": false
            }
        }
    })
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WriteArgs {
    path: String,
    content: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EditArgs {
    path: String,
    old_text: String,
    new_text: String,
}

fn target(root: &Path, name: &str) -> Result<(Workspace, PathBuf), String> {
    let workspace = Workspace::open(root).map_err(|error| format!("workspace: {error}"))?;
    let path = Workspace::normalize_path(workspace.relative_name(Path::new(name)))
        .map_err(|error| format!("{name}: {error}"))?;
    Ok((workspace, path))
}

fn read(workspace: &Workspace, path: &Path) -> io::Result<String> {
    let mut text = String::new();
    workspace.read_file(path)?.read_to_string(&mut text)?;
    Ok(text)
}

fn write_result(
    workspace: &Workspace,
    path: &Path,
    old_text: Option<String>,
    new_text: String,
) -> Result<(String, Vec<ToolContent>), String> {
    if old_text.as_ref() == Some(&new_text) {
        let summary = format!("Unchanged {}", path.display());
        return Ok((summary.clone(), vec![ToolContent::Text(summary)]));
    }
    let created = old_text.is_none();
    workspace
        .write_file(path, new_text.as_bytes(), created)
        .map_err(|error| format!("{}: {error}", path.display()))?;
    let action = if created { "Added" } else { "Modified" };
    let summary = format!("{action} {}", path.display());
    let content = vec![
        ToolContent::Text(summary.clone()),
        ToolContent::Diff {
            path: workspace.root().join(path),
            old_text,
            new_text,
        },
    ];
    Ok((summary, content))
}

pub(super) fn write(root: &Path, arguments: &str) -> Result<(String, Vec<ToolContent>), String> {
    let arguments: serde_json::Map<String, Value> =
        serde_json::from_str(arguments).map_err(|error| format!("arguments: {error}"))?;
    let args: WriteArgs = serde_json::from_value(Value::Object(arguments))
        .map_err(|error| format!("arguments: {error}"))?;
    let (workspace, path) = target(root, &args.path)?;
    let old_text = match read(&workspace, &path) {
        Ok(text) => Some(text),
        Err(error) if error.kind() == io::ErrorKind::NotFound => None,
        Err(error) => return Err(format!("{}: {error}", path.display())),
    };
    write_result(&workspace, &path, old_text, args.content)
}

pub(super) fn edit(root: &Path, arguments: &str) -> Result<(String, Vec<ToolContent>), String> {
    let arguments: serde_json::Map<String, Value> =
        serde_json::from_str(arguments).map_err(|error| format!("arguments: {error}"))?;
    let args: EditArgs = serde_json::from_value(Value::Object(arguments))
        .map_err(|error| format!("arguments: {error}"))?;
    if args.old_text.is_empty() {
        return Err("old_text must be nonempty".to_owned());
    }
    let (workspace, path) = target(root, &args.path)?;
    let old_text =
        read(&workspace, &path).map_err(|error| format!("{}: {error}", path.display()))?;
    let start = old_text
        .find(&args.old_text)
        .ok_or_else(|| format!("{}: old_text was absent", path.display()))?;
    if old_text.rfind(&args.old_text) != Some(start) {
        return Err(format!(
            "{}: old_text was ambiguous; include more surrounding context",
            path.display()
        ));
    }
    let mut new_text = old_text.clone();
    new_text.replace_range(start..start + args.old_text.len(), &args.new_text);
    write_result(&workspace, &path, Some(old_text), new_text)
}

#[cfg(test)]
mod tests {
    use std::fs;

    use super::*;
    use crate::tools::fixture::Workspace as TempWorkspace;

    fn write_args(path: &str, content: &str) -> String {
        json!({"path": path, "content": content}).to_string()
    }

    fn edit_args(path: &str, old_text: &str, new_text: &str) -> String {
        json!({"path": path, "old_text": old_text, "new_text": new_text}).to_string()
    }

    fn assert_change(
        workspace: &TempWorkspace,
        path: &str,
        result: (String, Vec<ToolContent>),
        action: &str,
        old: Option<&str>,
        new: &str,
    ) {
        let summary = format!("{action} {path}");
        let mut content = vec![ToolContent::Text(summary.clone())];
        if action != "Unchanged" {
            content.push(ToolContent::Diff {
                path: workspace.0.canonicalize().unwrap().join(path),
                old_text: old.map(str::to_owned),
                new_text: new.to_owned(),
            });
        }
        assert_eq!(result, (summary, content));
        assert_eq!(fs::read(workspace.0.join(path)).unwrap(), new.as_bytes());
    }

    #[test]
    fn write_creates_missing_parents_and_preserves_literal_content() {
        let workspace = TempWorkspace::new();
        for (path, content) in [("nested/file", "雪\r\n\t*** End Patch\n\\n"), ("empty", "")] {
            let result = write(&workspace.0, &write_args(&format!("./{path}"), content)).unwrap();
            assert_change(&workspace, path, result, "Added", None, content);
        }
    }

    #[test]
    fn write_overwrites_truncates_and_reports_unchanged_content() {
        let workspace = TempWorkspace::new();
        fs::write(workspace.0.join("file"), "original\r\n").unwrap();
        for (old, new, action) in [
            ("original\r\n", "new", "Modified"),
            ("new", "new", "Unchanged"),
            ("new", "", "Modified"),
            ("", "", "Unchanged"),
        ] {
            let result = write(&workspace.0, &write_args("file", new)).unwrap();
            assert_change(&workspace, "file", result, action, Some(old), new);
        }
    }

    #[test]
    fn edit_replaces_one_literal_match_and_preserves_every_other_byte() {
        let workspace = TempWorkspace::new();
        for (source, old, new, expected) in [
            (
                "left middle right",
                "middle",
                "inserted middle",
                "left inserted middle right",
            ),
            ("left middle right", "middle ", "", "left right"),
            ("x x", "x x", "x y", "x y"),
            ("a\nb\n", "b", "B", "a\nB\n"),
            ("a\nb", "b", "B", "a\nB"),
            ("a\r\nb\r\n", "b", "B\n", "a\r\nB\n\r\n"),
            ("a\r\nb", "b", "B", "a\r\nB"),
            ("a\nb\r\nc\n", "b", "B", "a\nB\r\nc\n"),
            ("\t 雪 \n", " 雪 ", "☀", "\t☀\n"),
            (".* \\n *** End Patch", ".*", "$1", "$1 \\n *** End Patch"),
        ] {
            fs::write(workspace.0.join("file"), source).unwrap();
            let result = edit(&workspace.0, &edit_args("file", old, new)).unwrap();
            assert_change(
                &workspace,
                "file",
                result,
                "Modified",
                Some(source),
                expected,
            );
        }
    }

    #[test]
    fn edit_rejects_absent_ambiguous_and_empty_matches_without_writing() {
        let workspace = TempWorkspace::new();
        for (source, old, error) in [
            (" a ", "a\n", "absent"),
            ("雪", "☀", "absent"),
            ("x x", "x", "ambiguous"),
            ("aaa", "aa", "ambiguous"),
            ("", "", "nonempty"),
        ] {
            fs::write(workspace.0.join("file"), source).unwrap();
            let result = edit(&workspace.0, &edit_args("file", old, "changed"));
            assert!(result.unwrap_err().contains(error), "{source:?}, {old:?}");
            assert_eq!(
                fs::read_to_string(workspace.0.join("file")).unwrap(),
                source
            );
        }
    }

    #[test]
    fn identical_edits_check_uniqueness_and_return_no_diff() {
        let workspace = TempWorkspace::new();
        fs::write(workspace.0.join("file"), "one\n").unwrap();
        let result = edit(&workspace.0, &edit_args("file", "one", "one")).unwrap();
        assert_change(
            &workspace,
            "file",
            result,
            "Unchanged",
            Some("one\n"),
            "one\n",
        );
        assert!(edit(&workspace.0, &edit_args("file", "absent", "absent")).is_err());
        fs::write(workspace.0.join("file"), "one one").unwrap();
        assert!(edit(&workspace.0, &edit_args("file", "one", "one")).is_err());
        assert_eq!(
            fs::read_to_string(workspace.0.join("file")).unwrap(),
            "one one"
        );
    }

    #[test]
    fn file_tools_reject_malformed_arguments_before_accessing_the_workspace() {
        for (execute, valid) in [
            (
                write as fn(&Path, &str) -> Result<(String, Vec<ToolContent>), String>,
                json!({"path":"file", "content":"text"}),
            ),
            (
                edit as fn(&Path, &str) -> Result<(String, Vec<ToolContent>), String>,
                json!({"path":"file", "old_text":"text", "new_text":"new"}),
            ),
        ] {
            let mut cases = vec![
                "{".to_owned(),
                "{}".to_owned(),
                "[]".to_owned(),
                json!(["file", "text"]).to_string(),
                json!(["file", "text", "new"]).to_string(),
            ];
            for field in valid.as_object().unwrap().keys() {
                let mut args = valid.clone();
                args.as_object_mut().unwrap().remove(field);
                cases.push(args.to_string());
                let mut args = valid.clone();
                args[field] = json!(3);
                cases.push(args.to_string());
            }
            let mut args = valid;
            args["extra"] = json!(true);
            cases.push(args.to_string());
            for arguments in cases {
                assert!(
                    execute(Path::new("/unused"), &arguments)
                        .unwrap_err()
                        .starts_with("arguments:"),
                    "{arguments}"
                );
            }
        }
    }

    #[test]
    fn file_tools_reject_invalid_paths_without_changing_files() {
        let workspace = TempWorkspace::new();
        for path in [
            "",
            ".",
            "./",
            "/outside",
            "../outside",
            "nested/../outside",
            "file/..",
        ] {
            for result in [
                write(&workspace.0, &write_args(path, "changed")),
                edit(&workspace.0, &edit_args(path, "text", "changed")),
            ] {
                assert!(result.is_err(), "{path:?}");
            }
            assert_eq!(fs::read_dir(&workspace.0).unwrap().count(), 0);
        }
        assert!(edit(&workspace.0, &edit_args("missing", "text", "changed")).is_err());
        assert_eq!(fs::read_dir(&workspace.0).unwrap().count(), 0);
    }

    #[test]
    fn file_tools_reject_nonregular_and_non_utf8_files_without_writing() {
        let workspace = TempWorkspace::new();
        fs::create_dir(workspace.0.join("directory")).unwrap();
        fs::write(workspace.0.join("binary"), b"\xff").unwrap();
        assert!(
            std::process::Command::new("mkfifo")
                .arg(workspace.0.join("fifo"))
                .status()
                .unwrap()
                .success()
        );
        for path in ["directory", "binary", "fifo"] {
            assert!(
                write(&workspace.0, &write_args(path, "changed")).is_err(),
                "{path}"
            );
            assert!(
                edit(&workspace.0, &edit_args(path, "text", "changed")).is_err(),
                "{path}"
            );
        }
        assert_eq!(fs::read(workspace.0.join("binary")).unwrap(), b"\xff");
        assert!(workspace.0.join("directory").is_dir());
    }

    #[cfg(unix)]
    #[test]
    fn file_tools_reject_symbolic_links_in_files_and_parents() {
        use std::os::unix::fs::symlink;
        let workspace = TempWorkspace::new();
        let outside = TempWorkspace::new();
        fs::write(outside.0.join("file"), "outside").unwrap();
        fs::write(workspace.0.join("inside"), "inside").unwrap();
        symlink(&outside.0, workspace.0.join("escape")).unwrap();
        symlink(outside.0.join("file"), workspace.0.join("file-link")).unwrap();
        symlink(outside.0.join("missing"), workspace.0.join("dangling")).unwrap();
        symlink(workspace.0.join("inside"), workspace.0.join("inside-link")).unwrap();
        symlink(&workspace.0, workspace.0.join("root-link")).unwrap();
        for path in [
            "escape/file",
            "escape/new/nested",
            "file-link",
            "dangling",
            "inside-link",
            "root-link/inside",
        ] {
            assert!(
                write(&workspace.0, &write_args(path, "changed")).is_err(),
                "{path}"
            );
            assert!(
                edit(&workspace.0, &edit_args(path, "inside", "changed")).is_err(),
                "{path}"
            );
        }
        assert_eq!(
            fs::read_to_string(outside.0.join("file")).unwrap(),
            "outside"
        );
        assert_eq!(fs::read_dir(&outside.0).unwrap().count(), 1);
        assert_eq!(
            fs::read_to_string(workspace.0.join("inside")).unwrap(),
            "inside"
        );
    }
}
