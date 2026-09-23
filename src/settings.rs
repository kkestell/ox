//! The model catalog and global hooks, read once at process startup.

use std::{
    collections::HashSet,
    io,
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{
    hooks::{Hooks, IN_HOOK_ENV, RunHooks},
    openrouter::CatalogModel,
    system_prompt,
};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Settings {
    models: Vec<CatalogModel>,
    hooks: Option<Hooks>,
}

pub struct Loaded {
    pub models: Vec<CatalogModel>,
    pub global_hooks: Option<RunHooks>,
}

pub fn load() -> io::Result<Loaded> {
    let home = std::env::var_os("HOME")
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "HOME is not set"))?;
    let mut loaded = load_from(&PathBuf::from(home).join(".config/ox/settings.json"))?;
    // A hook's nested `ox run` still needs models but must not rerun global hooks.
    if std::env::var_os(IN_HOOK_ENV).is_some() {
        loaded.global_hooks = None;
    }
    Ok(loaded)
}

fn load_from(path: &Path) -> io::Result<Loaded> {
    let read = || {
        let text = system_prompt::read_text(path)?;
        let settings: Settings = serde_json::from_str(&text)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if settings.models.is_empty() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "models must list at least one model",
            ));
        }
        let mut ids = HashSet::new();
        for model in &settings.models {
            model.validate()?;
            if !ids.insert(&model.id) {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    format!("model {} is listed twice", model.id),
                ));
            }
        }
        let global_hooks = match settings.hooks {
            Some(hooks) => {
                hooks.validate()?;
                Some(RunHooks {
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
            models: settings.models,
            global_hooks,
        })
    };
    read().map_err(|error| io::Error::new(error.kind(), format!("{}: {error}", path.display())))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::fixture::Workspace;

    const MODEL: &str = r#"{"id":"a/b","name":"B","context_limit":8001,"effort_mapping":{"low":"low","medium":"high","high":"max"}}"#;

    #[test]
    fn loads_settings_and_suppresses_global_hooks_inside_hooks() {
        const CHILD: &str = "OX_SETTINGS_TEST_CHILD";
        if std::env::var_os(CHILD).is_some() {
            let loaded = load().unwrap();
            assert_eq!(loaded.models[0].id, "a/b");
            assert!(loaded.global_hooks.is_none());
            return;
        }
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        assert!(load_from(&path).is_err());
        for (text, valid) in [
            (format!(r#"{{"models":[{MODEL}]}}"#), true),
            (format!(r#"{{"models":[{MODEL}],"hooks":null}}"#), true),
            (format!(r#"{{"models":[{MODEL}],"hooks":{{}}}}"#), true),
            (
                format!(r#"{{"models":[{MODEL}],"hooks":{{"before_run":{{"command":"true"}}}}}}"#),
                true,
            ),
            (
                format!(r#"{{"models":[{MODEL}],"hooks":{{"before_tool":{{"command":" "}}}}}}"#),
                false,
            ),
            (
                format!(
                    r#"{{"models":[{MODEL}],"hooks":{{"after_run":{{"command":"true","extra":1}}}}}}"#
                ),
                false,
            ),
            (
                format!(r#"{{"models":[{MODEL}],"hooks":{{"before_stop":{{}}}}}}"#),
                false,
            ),
            (format!(r#"{{"models":[{MODEL}],"extra":{{}}}}"#), false),
            (format!(r#"{{"models":[{MODEL},{MODEL}]}}"#), false),
            (
                format!(r#"{{"models":[{}]}}"#, MODEL.replace("8001", "8000")),
                false,
            ),
            (
                format!(
                    r#"{{"models":[{}]}}"#,
                    MODEL.replace(r#""low":"low""#, r#""low":" ""#)
                ),
                false,
            ),
            (
                format!(
                    r#"{{"models":[{}]}}"#,
                    MODEL.replace(r#""name""#, r#""extra":1,"name""#)
                ),
                false,
            ),
            (r#"{"models":[]}"#.to_owned(), false),
            ("{}".to_owned(), false),
            (String::new(), false),
            ("not json".to_owned(), false),
        ] {
            std::fs::write(&path, &text).unwrap();
            let result = load_from(&path);
            assert_eq!(result.is_ok(), valid, "{text}: {:?}", result.as_ref().err());
            match result {
                Ok(loaded) => {
                    assert_eq!(loaded.models.len(), 1);
                    assert_eq!(
                        loaded
                            .global_hooks
                            .and_then(|hooks| {
                                assert_eq!(hooks.directory, directory.0);
                                assert!(hooks.skill.is_none());
                                hooks.hooks.before_run
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
            format!(r#"{{"models":[{MODEL}],"hooks":{{"before_run":{{"command":"true"}}}}}}"#),
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
