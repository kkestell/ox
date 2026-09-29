use std::collections::HashSet;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use anyhow::{Context, anyhow};
use serde::Deserialize;

/// The config file, `$XDG_CONFIG_HOME/ox/tui.json`, else
/// `~/.config/ox/tui.json`.
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    #[serde(default)]
    pub servers: Vec<ServerConfig>,
    /// The favorite OpenRouter model IDs, in the order they were added.
    #[serde(default)]
    pub favorites: Vec<String>,
}

/// A named server executable.
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ServerConfig {
    pub name: String,
    pub command: String,
    #[serde(default)]
    pub args: Vec<String>,
}

impl Config {
    fn bundled_server() -> anyhow::Result<ServerConfig> {
        let command = std::env::current_exe()?.with_file_name("ox-acp");
        Ok(ServerConfig {
            name: "Ox".into(),
            command: command.to_string_lossy().into_owned(),
            args: vec![],
        })
    }

    /// Parses the file's text. Omitted servers mean the bundled server.
    fn parse(text: &str) -> anyhow::Result<Self> {
        let mut config: Config = serde_json::from_str(text)?;
        config.validate()?;
        if config.servers.is_empty() {
            config.servers.push(Self::bundled_server()?);
        }
        Ok(config)
    }

    pub fn validate(&self) -> anyhow::Result<()> {
        let mut names = HashSet::new();
        for server in &self.servers {
            if server.name.trim().is_empty() || !names.insert(&server.name) {
                anyhow::bail!("server names must be nonempty and unique: {}", server.name);
            }
        }
        Ok(())
    }

    pub fn select(&self, name: Option<&str>) -> anyhow::Result<&ServerConfig> {
        match name {
            Some(name) => self
                .servers
                .iter()
                .find(|server| server.name == name)
                .ok_or_else(|| anyhow!("no server named {name}; available: {}", self.names())),
            None if self.servers.len() == 1 => Ok(&self.servers[0]),
            None => anyhow::bail!("choose a server with --server; available: {}", self.names()),
        }
    }

    fn names(&self) -> String {
        if self.servers.is_empty() {
            "none".into()
        } else {
            self.servers
                .iter()
                .map(|server| server.name.as_str())
                .collect::<Vec<_>>()
                .join(", ")
        }
    }

    /// Reads the config file at `path`, or the defaults when there is none
    /// yet.
    pub fn read(path: &Path) -> anyhow::Result<Config> {
        let text = match std::fs::read_to_string(path) {
            Ok(text) => text,
            Err(error) if error.kind() == ErrorKind::NotFound => "{}".to_owned(),
            Err(error) => {
                return Err(error).with_context(|| format!("reading {}", path.display()));
            }
        };
        Self::parse(&text).with_context(|| format!("reading {}", path.display()))
    }
}

/// Replaces `favorites` in the config file at `path`, keeping its other
/// fields.
pub fn save_favorites(path: &Path, favorites: &[String]) -> anyhow::Result<()> {
    write_favorites(path, favorites).with_context(|| format!("writing {}", path.display()))
}

fn write_favorites(path: &Path, favorites: &[String]) -> anyhow::Result<()> {
    let mut config = match std::fs::read_to_string(path) {
        Ok(text) => serde_json::from_str(&text)?,
        Err(error) if error.kind() == ErrorKind::NotFound => serde_json::json!({}),
        Err(error) => return Err(error.into()),
    };
    let Some(fields) = config.as_object_mut() else {
        anyhow::bail!("the config is not a JSON object");
    };
    fields.insert("favorites".into(), favorites.into());
    let mut text = serde_json::to_string_pretty(&config)?;
    text.push('\n');
    let directory = path
        .parent()
        .ok_or_else(|| anyhow!("no config directory"))?;
    std::fs::create_dir_all(directory)?;
    let temporary = path.with_extension("json.tmp");
    std::fs::write(&temporary, text)?;
    std::fs::rename(&temporary, path)?;
    Ok(())
}

pub fn path() -> anyhow::Result<PathBuf> {
    let config_home = match std::env::var_os("XDG_CONFIG_HOME") {
        Some(config_home) => PathBuf::from(config_home),
        None => std::env::home_dir()
            .ok_or_else(|| anyhow!("no home directory"))?
            .join(".config"),
    };
    Ok(config_home.join("ox/tui.json"))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config(names: &[&str]) -> Config {
        Config {
            servers: names
                .iter()
                .map(|name| ServerConfig {
                    name: (*name).into(),
                    command: "server".into(),
                    args: vec![],
                })
                .collect(),
            favorites: vec![],
        }
    }

    #[test]
    fn selection_requires_a_name_unless_exactly_one_server_exists() {
        assert!(config(&[]).select(None).is_err());
        assert_eq!(config(&["Alpha"]).select(None).unwrap().name, "Alpha");
        let several = config(&["Alpha", "Beta"]);
        assert!(
            several
                .select(None)
                .unwrap_err()
                .to_string()
                .contains("Alpha, Beta")
        );
        assert_eq!(several.select(Some("Beta")).unwrap().name, "Beta");
        assert!(several.select(Some("missing")).is_err());
    }

    #[test]
    fn omitted_servers_mean_the_bundled_server_and_omitted_favorites_are_empty() {
        for (text, server, favorites) in [
            ("{}", "ox-acp", vec![]),
            (
                r#"{"favorites":["a/b","c/d"]}"#,
                "ox-acp",
                vec!["a/b", "c/d"],
            ),
            (
                r#"{"servers":[{"name":"Alpha","command":"alpha"}]}"#,
                "alpha",
                vec![],
            ),
            (
                r#"{"servers":[{"name":"Alpha","command":"alpha"}],"favorites":["a/b"]}"#,
                "alpha",
                vec!["a/b"],
            ),
        ] {
            let config = Config::parse(text).unwrap();
            assert!(
                config.select(None).unwrap().command.ends_with(server),
                "{text}"
            );
            assert_eq!(config.favorites, favorites, "{text}");
        }
    }

    #[test]
    fn names_must_be_unique_and_nonempty() {
        for names in [vec!["Alpha", "Alpha"], vec![" "], vec![""]] {
            assert!(config(&names).validate().is_err());
        }
    }

    #[test]
    fn saving_favorites_replaces_them_and_keeps_the_servers() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("ox/tui.json");
        write_favorites(&path, &["a/b".into()]).unwrap();
        let servers = r#"{"servers":[{"name":"Alpha","command":"alpha"}],"favorites":["a/b"]}"#;
        std::fs::write(&path, servers).unwrap();
        write_favorites(&path, &["c/d".into(), "a/b".into()]).unwrap();
        let config = Config::parse(&std::fs::read_to_string(&path).unwrap()).unwrap();
        assert_eq!(config.servers[0].name, "Alpha");
        assert_eq!(config.favorites, ["c/d", "a/b"]);
        let files: Vec<_> = std::fs::read_dir(path.parent().unwrap()).unwrap().collect();
        assert_eq!(files.len(), 1);
    }

    #[test]
    fn old_config_is_rejected_without_migration() {
        assert!(Config::parse(r#"{"servers":[{"id":"old","name":"Ox","command":"ox"}]}"#).is_err());
        let config = Config::parse(r#"{"servers":[{"name":"Ox","command":"ox"}]}"#).unwrap();
        assert!(config.servers[0].args.is_empty());
    }
}
