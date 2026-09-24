//! Settings: `~/.config/ox/settings.json`, read once at process startup, and
//! the workspace settings file `.ox/settings.json`, read when a session becomes
//! active, whose keys replace the same keys from the first file.

use std::{
    io,
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{
    hooks::{HookSource, Hooks, IN_HOOK_ENV},
    openrouter::{self, CatalogModel},
    text_file,
};

/// The format of both settings files. A key left out keeps the value from the
/// settings file.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SettingsFile {
    model: Option<String>,
    hooks: Option<Hooks>,
}

/// The effective settings for one workspace.
#[derive(Clone)]
pub struct Settings {
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

/// Reads `~/.config/ox/settings.json`, which must name the default model.
pub fn load(catalog: &[CatalogModel]) -> io::Result<Settings> {
    let mut settings = load_from(&home_dir()?.join(".config/ox/settings.json"), catalog)?;
    // A hook's nested `ox run` still needs the default model but must not
    // rerun global hooks.
    if std::env::var_os(IN_HOOK_ENV).is_some() {
        settings.global_hooks = None;
    }
    Ok(settings)
}

fn load_from(path: &Path, catalog: &[CatalogModel]) -> io::Result<Settings> {
    let file = read(path, catalog)?;
    let default_model = file
        .model
        .ok_or_else(|| invalid(path, "model must name the default model"))?;
    Ok(Settings {
        default_model,
        global_hooks: file.hooks.map(|hooks| HookSource {
            hooks,
            skill: None,
            directory: path
                .parent()
                .expect("settings have a parent directory")
                .to_owned(),
        }),
    })
}

impl Settings {
    /// These settings with each key set in the workspace settings file of
    /// `workspace_path` replaced. A missing file changes nothing.
    pub fn for_workspace(&self, workspace_path: &Path) -> io::Result<Self> {
        let path = workspace_path.join(".ox/settings.json");
        let file = match read(&path, openrouter::catalog()) {
            Ok(file) => file,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(self.clone()),
            Err(error) => return Err(error),
        };
        // Hooks run commands without approval, so a workspace cannot add them.
        if file.hooks.is_some() {
            return Err(invalid(
                &path,
                "hooks are read only from ~/.config/ox/settings.json",
            ));
        }
        Ok(Self {
            default_model: file.model.unwrap_or_else(|| self.default_model.clone()),
            global_hooks: self.global_hooks.clone(),
        })
    }
}

/// Reads one settings file, rejecting a model outside `catalog` and invalid
/// hooks. Every error names the file.
fn read(path: &Path, catalog: &[CatalogModel]) -> io::Result<SettingsFile> {
    let check = || {
        let file: SettingsFile = serde_json::from_str(&text_file::read_bounded(path)?)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if let Some(model) = &file.model
            && !catalog.iter().any(|candidate| &candidate.id == model)
        {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!("model {model} is not in the OpenRouter model catalog"),
            ));
        }
        if let Some(hooks) = &file.hooks {
            hooks.validate()?;
        }
        Ok(file)
    };
    check().map_err(|error| io::Error::new(error.kind(), format!("{}: {error}", path.display())))
}

fn invalid(path: &Path, message: &str) -> io::Error {
    io::Error::new(
        io::ErrorKind::InvalidData,
        format!("{}: {message}", path.display()),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{openrouter::fixture::DEFAULT_MODEL, tools::fixture::Workspace};

    #[test]
    fn loads_settings_and_suppresses_global_hooks_inside_hooks() {
        const CHILD: &str = "OX_SETTINGS_TEST_CHILD";
        let catalog = openrouter::catalog();
        if std::env::var_os(CHILD).is_some() {
            let settings = load(catalog).unwrap();
            assert_eq!(settings.default_model, DEFAULT_MODEL);
            assert!(settings.global_hooks.is_none());
            return;
        }
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        assert!(load_from(&path, catalog).is_err());
        for (text, valid) in [
            (r#"{"model":"M"}"#, true),
            (r#"{"model":"M","hooks":null}"#, true),
            (r#"{"model":"M","hooks":{}}"#, true),
            (
                r#"{"model":"M","hooks":{"before_run":{"command":"true"}}}"#,
                true,
            ),
            (
                r#"{"model":"M","hooks":{"before_tool":{"command":" "}}}"#,
                false,
            ),
            (
                r#"{"model":"M","hooks":{"after_run":{"command":"true","extra":1}}}"#,
                false,
            ),
            (r#"{"model":"M","hooks":{"before_stop":{}}}"#, false),
            (r#"{"model":"M","extra":{}}"#, false),
            (r#"{"model":"a/b"}"#, false),
            (r#"{"model":" "}"#, false),
            (r#"{"hooks":{}}"#, false),
            ("{}", false),
            ("", false),
            ("not json", false),
        ] {
            let text = text.replace('M', DEFAULT_MODEL);
            std::fs::write(&path, &text).unwrap();
            let result = load_from(&path, catalog);
            assert_eq!(result.is_ok(), valid, "{text}: {:?}", result.as_ref().err());
            match result {
                Ok(settings) => {
                    assert_eq!(settings.default_model, DEFAULT_MODEL);
                    assert_eq!(
                        settings
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
        assert!(load_from(&path, catalog).is_err());
        let home = Workspace::new();
        std::fs::create_dir_all(home.0.join(".config/ox")).unwrap();
        std::fs::write(
            home.0.join(".config/ox/settings.json"),
            format!(
                r#"{{"model":"{DEFAULT_MODEL}","hooks":{{"before_run":{{"command":"true"}}}}}}"#
            ),
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

    #[test]
    fn workspace_settings_override_the_settings_file() {
        let global_hooks = HookSource {
            hooks: Hooks::default(),
            skill: None,
            directory: PathBuf::from("/home/.config/ox"),
        };
        let settings = Settings {
            default_model: DEFAULT_MODEL.to_owned(),
            global_hooks: Some(global_hooks.clone()),
        };
        let workspace = Workspace::new();
        let unchanged = settings.for_workspace(&workspace.0).unwrap();
        assert_eq!(unchanged.default_model, DEFAULT_MODEL, "a missing file");
        assert_eq!(unchanged.global_hooks.as_ref(), Some(&global_hooks));

        let chosen = &openrouter::catalog()[1].id;
        assert_ne!(chosen, DEFAULT_MODEL);
        let path = workspace.0.join(".ox/settings.json");
        std::fs::create_dir(workspace.0.join(".ox")).unwrap();
        for (text, model) in [
            ("{}", DEFAULT_MODEL),
            (&format!(r#"{{"model":"{chosen}"}}"#), chosen),
        ] {
            std::fs::write(&path, text).unwrap();
            let overridden = settings.for_workspace(&workspace.0).unwrap();
            assert_eq!(overridden.default_model, model, "{text}");
            assert_eq!(
                overridden.global_hooks.as_ref(),
                Some(&global_hooks),
                "{text}"
            );
        }

        for (text, error) in [
            (
                r#"{"hooks":{"before_run":{"command":"true"}}}"#,
                "hooks are read only from ~/.config/ox/settings.json",
            ),
            (
                r#"{"model":"a/b"}"#,
                "model a/b is not in the OpenRouter model catalog",
            ),
            (r#"{"extra":1}"#, "unknown field `extra`"),
            ("not json", "expected"),
        ] {
            std::fs::write(&path, text).unwrap();
            let message = settings
                .for_workspace(&workspace.0)
                .err()
                .unwrap()
                .to_string();
            assert!(
                message.starts_with(&format!("{}: ", path.display())) && message.contains(error),
                "{text}: {message}"
            );
        }
    }
}
