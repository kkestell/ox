use std::path::{Component, Path, PathBuf};

pub fn cwd(path: &Path) -> Result<PathBuf, String> {
    if !path.is_absolute() {
        return Err(format!("cwd must be an absolute path: {}", path.display()));
    }
    let mut resolved = PathBuf::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::ParentDir => {
                resolved.pop();
            }
            other => resolved.push(other.as_os_str()),
        }
    }
    if !resolved.is_dir() {
        return Err(format!("cwd is not a directory: {}", resolved.display()));
    }
    Ok(resolved)
}

/// An MCP server command is an absolute path, as ACP v2 types it.
pub fn mcp_command(command: &Path) -> Result<(), String> {
    if command.is_absolute() {
        Ok(())
    } else {
        Err(format!(
            "MCP server command must be an absolute path: {}",
            command.display()
        ))
    }
}
