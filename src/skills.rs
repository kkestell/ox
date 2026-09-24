//! Skill definitions: `<name>/SKILL.md` entries in the skills directories,
//! loaded into a session's skill catalog when the session becomes active.

use std::{
    collections::HashSet,
    fs,
    io::{self, ErrorKind},
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{hooks::Hooks, text_file};

const FILE_NAME: &str = "SKILL.md";

/// One skill definition. The instructions are the Markdown after the
/// frontmatter.
#[derive(Debug, Clone, PartialEq)]
pub struct Skill {
    pub name: String,
    pub description: String,
    pub argument_hint: Option<String>,
    pub instructions: String,
    /// The skill directory, where its hook commands run.
    pub directory: PathBuf,
    pub hooks: Hooks,
}

/// Other frontmatter keys belong to other agents and are ignored.
#[derive(Deserialize)]
struct Frontmatter {
    name: String,
    description: String,
    #[serde(rename = "argument-hint")]
    argument_hint: Option<String>,
    hooks: Option<Hooks>,
}

/// A loaded skill catalog with one message for each skipped definition or
/// skills directory.
pub struct Loaded {
    pub skills: Vec<Skill>,
    pub skipped: Vec<String>,
}

/// The skill catalog for a session in `workspace_path`, ordered by name. The
/// skills directories are `~/.config/ox/skills`, `~/.agents/skills`, and the
/// workspace's `.agents/skills`, highest priority first, and a skill replaces
/// any lower-priority skill with the same name. A missing skills directory
/// means no skills from it.
///
/// An invalid definition is skipped with a message naming its path. It still
/// takes its directory name, so a lower-priority skill never runs under the
/// name of a broken one.
pub fn load(home: &Path, workspace_path: &Path) -> Loaded {
    let directories = [
        (home.join(".config/ox/skills"), "~/.config/ox/skills"),
        (home.join(".agents/skills"), "~/.agents/skills"),
        (workspace_path.join(".agents/skills"), ".agents/skills"),
    ];
    let mut names = HashSet::new();
    let mut skills = Vec::new();
    let mut skipped = Vec::new();
    for (path, shown) in directories {
        let entries =
            match fs::read_dir(&path).and_then(|entries| entries.collect::<io::Result<Vec<_>>>()) {
                Ok(entries) => entries,
                Err(error) if error.kind() == ErrorKind::NotFound => continue,
                Err(error) => {
                    skipped.push(format!("{shown}: {error}"));
                    continue;
                }
            };
        for entry in entries {
            let directory = entry.path();
            if !directory.is_dir() {
                continue;
            }
            let directory_name = entry.file_name().to_string_lossy().into_owned();
            let parsed = text_file::read_bounded(&directory.join(FILE_NAME))
                .and_then(|text| parse(&directory_name, directory, &text));
            let unclaimed = names.insert(directory_name.clone());
            match parsed {
                Ok(skill) if unclaimed => skills.push(skill),
                Ok(_) => {}
                Err(error) => {
                    skipped.push(format!("{shown}/{directory_name}/{FILE_NAME}: {error}"));
                }
            }
        }
    }
    skills.sort_by(|left, right| left.name.cmp(&right.name));
    Loaded { skills, skipped }
}

fn parse(directory_name: &str, directory: PathBuf, text: &str) -> io::Result<Skill> {
    let (yaml, body) = split_frontmatter(text)?;
    let frontmatter: Frontmatter =
        yaml_serde::from_str(yaml).map_err(|error| invalid(format!("frontmatter: {error}")))?;
    let name = frontmatter.name;
    if name.is_empty()
        || !name
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
    {
        return Err(invalid(format!(
            "name {name:?} may contain only lowercase letters, digits, and hyphens"
        )));
    }
    if name != directory_name {
        return Err(invalid(format!(
            "name {name:?} does not match its directory"
        )));
    }
    if name == "compact" {
        return Err(invalid("compact is the built-in /compact command"));
    }
    if frontmatter.description.trim().is_empty() {
        return Err(invalid("description is blank"));
    }
    let instructions = body.trim();
    if instructions.is_empty() {
        return Err(invalid("instructions are blank"));
    }
    let hooks = frontmatter.hooks.unwrap_or_default();
    hooks.validate()?;
    Ok(Skill {
        name,
        description: frontmatter.description,
        argument_hint: frontmatter.argument_hint,
        instructions: instructions.to_owned(),
        directory,
        hooks,
    })
}

/// Splits `---` delimited YAML frontmatter from the Markdown that follows it.
fn split_frontmatter(text: &str) -> io::Result<(&str, &str)> {
    let missing = || invalid("does not begin with --- delimited YAML frontmatter");
    let rest = text
        .strip_prefix("---\n")
        .or_else(|| text.strip_prefix("---\r\n"))
        .ok_or_else(missing)?;
    let mut offset = 0;
    for line in rest.split_inclusive('\n') {
        if line.trim_end_matches(['\n', '\r']) == "---" {
            return Ok((&rest[..offset], &rest[offset + line.len()..]));
        }
        offset += line.len();
    }
    Err(missing())
}

fn invalid(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidData, message.into())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::fixture::Workspace;

    fn write_skill(skills_directory: &Path, name: &str, text: &str) {
        let directory = skills_directory.join(name);
        fs::create_dir_all(&directory).unwrap();
        if !text.is_empty() {
            fs::write(directory.join(FILE_NAME), text).unwrap();
        }
    }

    fn valid(name: &str) -> String {
        format!("---\nname: {name}\ndescription: Valid.\n---\nBody\n")
    }

    fn names(loaded: &Loaded) -> Vec<&str> {
        loaded
            .skills
            .iter()
            .map(|skill| skill.name.as_str())
            .collect()
    }

    #[test]
    fn invalid_skill_definitions_are_skipped_with_their_path() {
        for (name, text, error) in [
            (
                "compact",
                "---\nname: compact\ndescription: Shadow.\n---\nBody\n",
                "compact is the built-in /compact command",
            ),
            (
                "Goal",
                "---\nname: Goal\ndescription: Goal.\n---\nBody\n",
                "may contain only lowercase letters, digits, and hyphens",
            ),
            (
                "other",
                "---\nname: goal\ndescription: Goal.\n---\nBody\n",
                "does not match its directory",
            ),
            (
                "blank",
                "---\nname: blank\ndescription: \" \"\n---\nBody\n",
                "description is blank",
            ),
            (
                "empty",
                "---\nname: empty\ndescription: Empty.\n---\n\n",
                "instructions are blank",
            ),
            (
                "plain",
                "No frontmatter.\n",
                "does not begin with --- delimited YAML",
            ),
            (
                "hooked",
                "---\nname: hooked\ndescription: Hooked.\nhooks:\n  before_stop:\n    command: 'true'\n    extra: true\n---\nBody\n",
                "unknown field `extra`",
            ),
            (
                "incomplete",
                "---\nname: incomplete\ndescription: Incomplete.\nhooks:\n  before_stop: {}\n---\nBody\n",
                "missing field `command`",
            ),
            (
                "unhooked",
                "---\nname: unhooked\ndescription: Unhooked.\nhooks:\n  after_run:\n    command: \" \"\n---\nBody\n",
                "after_run hook command is blank",
            ),
            (
                "extra",
                "---\nname: extra\ndescription: Extra.\nhooks:\n  before_tool:\n    command: 'true'\n    tools: [shell]\n---\nBody\n",
                "unknown field `tools`",
            ),
            ("missing", "", "No such file or directory"),
        ] {
            let home = Workspace::new();
            let workspace = Workspace::new();
            let skills_directory = workspace.0.join(".agents/skills");
            write_skill(&skills_directory, name, text);
            write_skill(&skills_directory, "neighbor", &valid("neighbor"));
            let loaded = load(&home.0, &workspace.0);
            assert_eq!(names(&loaded), ["neighbor"], "{name}");
            assert!(
                matches!(&loaded.skipped[..], [message]
                    if message.starts_with(&format!(".agents/skills/{name}/SKILL.md: "))
                        && message.contains(error)),
                "{name}: {:?}",
                loaded.skipped
            );
        }
    }

    #[test]
    fn a_skipped_definition_keeps_its_name_from_lower_priority_skills() {
        let home = Workspace::new();
        let workspace = Workspace::new();
        let ox = home.0.join(".config/ox/skills");
        let agents = home.0.join(".agents/skills");
        let local = workspace.0.join(".agents/skills");
        write_skill(&agents, "careful", "No frontmatter.\n");
        write_skill(&local, "careful", &valid("careful"));
        write_skill(&ox, "goal", &valid("goal"));
        write_skill(&local, "goal", "No frontmatter.\n");
        let loaded = load(&home.0, &workspace.0);
        assert_eq!(
            loaded
                .skills
                .iter()
                .map(|skill| &skill.directory)
                .collect::<Vec<_>>(),
            [&ox.join("goal")],
            "a broken ~/.agents/skills/careful keeps the workspace careful out"
        );
        let mut skipped = loaded.skipped;
        skipped.sort();
        assert!(
            matches!(&skipped[..], [local_goal, agents_careful]
                if local_goal.starts_with(".agents/skills/goal/SKILL.md: ")
                    && agents_careful.starts_with("~/.agents/skills/careful/SKILL.md: ")),
            "a replaced definition is still reported: {skipped:?}"
        );
    }
}
