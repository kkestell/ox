//! Workspace skills: `.agents/skills/<name>/SKILL.md` definitions loaded into
//! a session's skill catalog when the session becomes active.

use std::{
    fs,
    io::{self, ErrorKind},
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{hooks::Hooks, system_prompt};

const SKILLS_DIR: &str = ".agents/skills";
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

/// The skill catalog for a workspace, ordered by name. A missing skills
/// directory means no skills; an invalid definition is an error naming its
/// path.
pub fn load(workspace_path: &Path) -> io::Result<Vec<Skill>> {
    let entries = match fs::read_dir(workspace_path.join(SKILLS_DIR)) {
        Ok(entries) => entries,
        Err(error) if error.kind() == ErrorKind::NotFound => return Ok(Vec::new()),
        Err(error) => {
            return Err(io::Error::new(
                error.kind(),
                format!("{SKILLS_DIR}: {error}"),
            ));
        }
    };
    let mut skills = Vec::new();
    for entry in entries {
        let entry = entry
            .map_err(|error| io::Error::new(error.kind(), format!("{SKILLS_DIR}: {error}")))?;
        let directory = entry.path();
        if !directory.is_dir() {
            continue;
        }
        let directory_name = entry.file_name();
        let path = format!(
            "{SKILLS_DIR}/{}/{FILE_NAME}",
            directory_name.to_string_lossy()
        );
        let skill = system_prompt::read_text(&directory.join(FILE_NAME))
            .and_then(|text| parse(&directory_name.to_string_lossy(), directory, &text))
            .map_err(|error| io::Error::new(error.kind(), format!("{path}: {error}")))?;
        skills.push(skill);
    }
    skills.sort_by(|left, right| left.name.cmp(&right.name));
    Ok(skills)
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
