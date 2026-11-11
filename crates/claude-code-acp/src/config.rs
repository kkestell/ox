use std::{env, path::PathBuf, time::Duration};

#[derive(Clone, Debug)]
pub struct Config {
    pub model: String,
    pub allowed_tools: Vec<String>,
    pub max_turns: Option<u32>,
    pub timeout: Option<Duration>,
    pub skip_permissions: bool,
    pub store_dir: PathBuf,
    pub claude_dir: PathBuf,
}

impl Config {
    pub fn from_env() -> Self {
        let home = dirs::home_dir().unwrap_or_else(|| PathBuf::from("."));
        Self {
            model: env::var("CLAUDE_ACP_MODEL")
                .ok()
                .filter(|s| !s.trim().is_empty())
                .unwrap_or_else(|| "claude-opus-5-5".into()),
            allowed_tools: env::var("CLAUDE_ACP_ALLOWED_TOOLS")
                .unwrap_or_default()
                .split(',')
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .map(str::to_owned)
                .collect(),
            max_turns: env::var("CLAUDE_ACP_MAX_TURNS")
                .ok()
                .and_then(|s| s.parse().ok()),
            timeout: env::var("CLAUDE_ACP_TIMEOUT")
                .ok()
                .and_then(|s| s.parse().ok())
                .filter(|&ms| ms > 0)
                .map(Duration::from_millis),
            skip_permissions: env::var("CLAUDE_ACP_SKIP_PERMISSIONS").as_deref() == Ok("true"),
            store_dir: env::var_os("CLAUDE_ACP_STORE_DIR")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join(".local/share/claude-code-acp")),
            claude_dir: env::var_os("CLAUDE_CONFIG_DIR")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join(".claude")),
        }
    }
}
