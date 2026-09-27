use std::collections::HashSet;
use std::io::ErrorKind;
use std::path::PathBuf;

use anyhow::{Context, anyhow};
use serde::Deserialize;

/// The config file, `$XDG_CONFIG_HOME/ox/config.json`, else
/// `~/.config/ox/config.json`.
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub servers: Vec<ServerConfig>,
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
    pub fn bundled_server() -> anyhow::Result<Self> {
        let command = std::env::current_exe()?.with_file_name("ox-acp");
        Ok(Self {
            servers: vec![ServerConfig {
                name: "Ox".into(),
                command: command.to_string_lossy().into_owned(),
                args: vec![],
            }],
        })
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

    /// Reads the config file, or `None` when there is none yet.
    pub fn read() -> anyhow::Result<Option<Config>> {
        let path = path()?;
        let text = match std::fs::read_to_string(&path) {
            Ok(text) => text,
            Err(error) if error.kind() == ErrorKind::NotFound => return Ok(None),
            Err(error) => {
                return Err(error).with_context(|| format!("reading {}", path.display()));
            }
        };
        let config: Config =
            serde_json::from_str(&text).with_context(|| format!("parsing {}", path.display()))?;
        config
            .validate()
            .with_context(|| format!("validating {}", path.display()))?;
        Ok(Some(config))
    }
}

pub fn path() -> anyhow::Result<PathBuf> {
    let config = match std::env::var_os("XDG_CONFIG_HOME") {
        Some(config) => PathBuf::from(config),
        None => std::env::home_dir()
            .ok_or_else(|| anyhow!("no home directory"))?
            .join(".config"),
    };
    Ok(config.join("ox/config.json"))
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
        }
    }

    #[test]
    fn selection_requires_a_name_unless_exactly_one_server_exists() {
        let bundled = Config::bundled_server().unwrap();
        assert!(bundled.select(None).unwrap().command.ends_with("/ox-acp"));
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
    fn names_must_be_unique_and_nonempty() {
        for names in [vec!["Alpha", "Alpha"], vec![" "], vec![""]] {
            assert!(config(&names).validate().is_err());
        }
    }

    #[test]
    fn old_config_is_rejected_without_migration() {
        assert!(
            serde_json::from_str::<Config>(
                r#"{"servers":[{"id":"old","name":"Ox","command":"ox"}]}"#
            )
            .is_err()
        );
        let config: Config =
            serde_json::from_str(r#"{"servers":[{"name":"Ox","command":"ox"}]}"#).unwrap();
        assert!(config.servers[0].args.is_empty());
    }
}
