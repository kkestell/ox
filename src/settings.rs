//! The default model and global hooks, read once at process startup.

use std::{
    io,
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{
    hooks::{HookSource, Hooks, IN_HOOK_ENV},
    text_file,
};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Settings {
    model: String,
    hooks: Option<Hooks>,
}

pub struct Loaded {
    pub path: PathBuf,
    pub default_model: String,
    pub global_hooks: Option<HookSource>,
}

/// The home directory in `$HOME`, which holds `~/.config/ox` and the user
/// skills directories.
pub fn home_dir() -> io::Result<PathBuf> {
    std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "HOME is not set"))
}

pub fn load() -> io::Result<Loaded> {
    let mut loaded = load_from(&home_dir()?.join(".config/ox/settings.json"))?;
    // A hook's nested `ox run` still needs the default model but must not
    // rerun global hooks.
    if std::env::var_os(IN_HOOK_ENV).is_some() {
        loaded.global_hooks = None;
    }
    Ok(loaded)
}

fn load_from(path: &Path) -> io::Result<Loaded> {
    let read = || {
        let text = text_file::read_bounded(path)?;
        let settings: Settings = serde_json::from_str(&text)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if settings.model.trim().is_empty() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "model must name the default model",
            ));
        }
        let global_hooks = match settings.hooks {
            Some(hooks) => {
                hooks.validate()?;
                Some(HookSource {
                    hooks,
                    skill: None,
                    directory: path
                        .parent()
                        .expect("settings have a parent directory")
                        .to_owned(),
                })
            }
            None => None,
        };
        Ok(Loaded {
            path: path.to_owned(),
            default_model: settings.model,
            global_hooks,
        })
    };
    read().map_err(|error| io::Error::new(error.kind(), format!("{}: {error}", path.display())))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::fixture::Workspace;

    #[test]
    fn loads_settings_and_suppresses_global_hooks_inside_hooks() {
        const CHILD: &str = "OX_SETTINGS_TEST_CHILD";
        if std::env::var_os(CHILD).is_some() {
            let loaded = load().unwrap();
            assert_eq!(loaded.default_model, "a/b");
            assert!(loaded.global_hooks.is_none());
            return;
        }
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        assert!(load_from(&path).is_err());
        for (text, valid) in [
            (r#"{"model":"a/b"}"#, true),
            (r#"{"model":"a/b","hooks":null}"#, true),
            (r#"{"model":"a/b","hooks":{}}"#, true),
            (
                r#"{"model":"a/b","hooks":{"before_run":{"command":"true"}}}"#,
                true,
            ),
            (
                r#"{"model":"a/b","hooks":{"before_tool":{"command":" "}}}"#,
                false,
            ),
            (
                r#"{"model":"a/b","hooks":{"after_run":{"command":"true","extra":1}}}"#,
                false,
            ),
            (r#"{"model":"a/b","hooks":{"before_stop":{}}}"#, false),
            (r#"{"model":"a/b","extra":{}}"#, false),
            (r#"{"model":" "}"#, false),
            (r#"{"hooks":{}}"#, false),
            ("{}", false),
            ("", false),
            ("not json", false),
        ] {
            std::fs::write(&path, text).unwrap();
            let result = load_from(&path);
            assert_eq!(result.is_ok(), valid, "{text}: {:?}", result.as_ref().err());
            match result {
                Ok(loaded) => {
                    assert_eq!(loaded.default_model, "a/b");
                    assert_eq!(loaded.path, path);
                    assert_eq!(
                        loaded
                            .global_hooks
                            .and_then(|source| {
                                assert_eq!(source.directory, directory.0);
                                assert!(source.skill.is_none());
                                source.hooks.before_run
                            })
                            .map(|hook| hook.command),
                        text.contains("before_run").then(|| "true".to_owned())
                    );
                }
                Err(error) => assert!(error.to_string().contains(path.to_str().unwrap())),
            }
        }
        std::fs::remove_file(&path).unwrap();
        std::fs::create_dir(&path).unwrap();
        assert!(load_from(&path).is_err());
        let home = Workspace::new();
        std::fs::create_dir_all(home.0.join(".config/ox")).unwrap();
        std::fs::write(
            home.0.join(".config/ox/settings.json"),
            r#"{"model":"a/b","hooks":{"before_run":{"command":"true"}}}"#,
        )
        .unwrap();
        let output = std::process::Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "settings::tests::loads_settings_and_suppresses_global_hooks_inside_hooks",
            ])
            .env(CHILD, "1")
            .env(IN_HOOK_ENV, "1")
            .env("HOME", &home.0)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stdout)
        );
    }
}
