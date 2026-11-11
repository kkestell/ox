use std::{env, path::PathBuf};

#[derive(Clone, Debug)]
pub struct Config {
    pub store_dir: PathBuf,
    pub claude_dir: PathBuf,
}

impl Config {
    pub fn from_env() -> Self {
        let home = dirs::home_dir().unwrap_or_else(|| PathBuf::from("."));
        Self {
            store_dir: env::var_os("CLAUDE_ACP_STORE_DIR")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join(".local/share/claude-code-acp")),
            claude_dir: env::var_os("CLAUDE_CONFIG_DIR")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.join(".claude")),
        }
    }
}
