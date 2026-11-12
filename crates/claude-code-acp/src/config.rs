use std::{env, ffi::OsString, path::PathBuf};

#[derive(Clone, Debug)]
pub struct Config {
    pub store_dir: PathBuf,
    pub claude_dir: PathBuf,
}

impl Config {
    pub fn from_env() -> Self {
        let home = dirs::home_dir();
        Self {
            store_dir: store_dir_from(
                env::var_os("CLAUDE_ACP_STORE_DIR"),
                env::var_os("XDG_DATA_HOME"),
                home.clone(),
            ),
            claude_dir: env::var_os("CLAUDE_CONFIG_DIR")
                .map(PathBuf::from)
                .unwrap_or_else(|| home.unwrap_or_else(|| PathBuf::from(".")).join(".claude")),
        }
    }
}

/// The store directory: `$CLAUDE_ACP_STORE_DIR`, else
/// `$XDG_DATA_HOME/claude-code-acp` when `XDG_DATA_HOME` is an absolute path,
/// else `~/.local/share/claude-code-acp`, else `<temp dir>/claude-code-acp`
/// when there is no home directory.
fn store_dir_from(
    store: Option<OsString>,
    xdg: Option<OsString>,
    home: Option<PathBuf>,
) -> PathBuf {
    if let Some(store) = store {
        return PathBuf::from(store);
    }
    let base = match (xdg.map(PathBuf::from), home) {
        (Some(xdg), _) if xdg.is_absolute() => xdg,
        (_, Some(home)) => home.join(".local/share"),
        (_, None) => env::temp_dir(),
    };
    base.join("claude-code-acp")
}

#[cfg(test)]
mod tests {
    use std::path::Path;

    use super::*;

    fn home() -> PathBuf {
        PathBuf::from("/home/user")
    }

    #[test]
    fn store_dir_is_claude_acp_store_dir_when_set() {
        assert_eq!(
            store_dir_from(Some("store".into()), Some("/data".into()), Some(home())),
            Path::new("store")
        );
    }

    #[test]
    fn store_dir_is_under_an_absolute_xdg_data_home() {
        assert_eq!(
            store_dir_from(None, Some("/data".into()), Some(home())),
            Path::new("/data/claude-code-acp")
        );
    }

    #[test]
    fn store_dir_ignores_a_relative_xdg_data_home() {
        assert_eq!(
            store_dir_from(None, Some("data".into()), Some(home())),
            Path::new("/home/user/.local/share/claude-code-acp")
        );
    }

    #[test]
    fn store_dir_ignores_an_empty_xdg_data_home() {
        assert_eq!(
            store_dir_from(None, Some("".into()), Some(home())),
            Path::new("/home/user/.local/share/claude-code-acp")
        );
        assert_eq!(
            store_dir_from(None, None, Some(home())),
            Path::new("/home/user/.local/share/claude-code-acp")
        );
    }

    #[test]
    fn store_dir_without_a_home_is_in_the_temp_dir() {
        assert_eq!(
            store_dir_from(None, None, None),
            env::temp_dir().join("claude-code-acp")
        );
    }
}
