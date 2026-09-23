//! Global hook settings, read once at process startup.

use std::{
    io,
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{
    hooks::{Hooks, IN_HOOK_ENV, RunHooks},
    system_prompt,
};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Settings {
    hooks: Option<Hooks>,
}

pub fn load() -> io::Result<Option<RunHooks>> {
    if std::env::var_os(IN_HOOK_ENV).is_some() {
        return Ok(None);
    }
    let home = std::env::var_os("HOME")
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "HOME is not set"))?;
    load_from(&PathBuf::from(home).join(".config/ox/settings.json"))
}

fn load_from(path: &Path) -> io::Result<Option<RunHooks>> {
    let read = || {
        let text = match system_prompt::read_text(path) {
            Ok(text) => text,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
            Err(error) => return Err(error),
        };
        let settings: Settings = serde_json::from_str(&text)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        let Some(hooks) = settings.hooks else {
            return Ok(None);
        };
        hooks.validate()?;
        Ok(Some(RunHooks {
            hooks,
            skill: None,
            directory: path
                .parent()
                .expect("settings have a parent directory")
                .to_owned(),
        }))
    };
    read().map_err(|error| io::Error::new(error.kind(), format!("{}: {error}", path.display())))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::fixture::Workspace;

    #[test]
    fn loads_global_hooks_and_suppresses_them_inside_hooks() {
        const CHILD: &str = "OX_SETTINGS_TEST_CHILD";
        if std::env::var_os(CHILD).is_some() {
            assert!(load().unwrap().is_none());
            return;
        }
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        assert!(load_from(&path).unwrap().is_none());
        for (text, valid) in [
            ("{}", true),
            (r#"{"hooks": null}"#, true),
            (r#"{"hooks": {}}"#, true),
            (r#"{"hooks":{"before_run":{"command":"true"}}}"#, true),
            (r#"{"hooks":{"before_tool":{"command":" "}}}"#, false),
            (
                r#"{"hooks":{"after_run":{"command":"true","extra":1}}}"#,
                false,
            ),
            (r#"{"hooks":{"before_stop":{}}}"#, false),
            (r#"{"extra":{}}"#, false),
            ("", false),
            ("not json", false),
        ] {
            std::fs::write(&path, text).unwrap();
            let result = load_from(&path);
            assert_eq!(result.is_ok(), valid, "{text}: {result:?}");
            if let Ok(Some(hooks)) = result {
                assert_eq!(hooks.directory, directory.0);
                assert!(hooks.skill.is_none());
                assert_eq!(
                    hooks.hooks.before_run.map(|hook| hook.command),
                    text.contains("before_run").then(|| "true".to_owned())
                );
            } else if let Err(error) = result {
                assert!(error.to_string().contains(path.to_str().unwrap()));
            } else {
                assert!(!text.contains("before_run"));
            }
        }
        std::fs::remove_file(&path).unwrap();
        std::fs::create_dir(&path).unwrap();
        assert!(load_from(&path).is_err());
        let output = std::process::Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "settings::tests::loads_global_hooks_and_suppresses_them_inside_hooks",
            ])
            .env(CHILD, "1")
            .env(IN_HOOK_ENV, "1")
            .env_remove("HOME")
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stdout)
        );
    }
}
