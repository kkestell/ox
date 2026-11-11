use std::{
    env,
    path::{Component, Path, PathBuf},
};

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

pub fn mcp_command(command: &Path) -> Result<(), String> {
    let name = command.to_string_lossy();
    if name.is_empty() || name.contains(['/', '\\']) || has_metacharacters(&name) {
        return Err(format!("invalid MCP server command: {name}"));
    }
    let allowed = env::var("CLAUDE_ACP_MCP_ALLOWED_COMMANDS")
        .unwrap_or_else(|_| "node,npx,python3,python,deno,bun".into());
    if !allowed
        .split(',')
        .map(str::trim)
        .any(|candidate| candidate == name)
    {
        return Err(format!("MCP server command not allowed: {name}"));
    }
    Ok(())
}

pub fn mcp_arg(arg: &str) -> Result<(), String> {
    if has_metacharacters(arg) {
        Err(format!(
            "MCP server argument contains shell metacharacters: {arg}"
        ))
    } else {
        Ok(())
    }
}

fn has_metacharacters(value: &str) -> bool {
    value.chars().any(|c| ";|&$`(){}!<>".contains(c))
}
