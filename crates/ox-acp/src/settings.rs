//! Settings: `~/.config/ox/settings.json`, read once at process startup, and
//! the workspace settings file `.ox/settings.json`, read when a session becomes
//! active, whose keys replace the same keys from the first file.

use std::{
    io,
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{
    openrouter::{self, CatalogModel},
    sessions::{EffortLevel, SessionMode, SessionSettings},
    text_file,
};

/// The format of both settings files.
#[derive(Deserialize)]
struct SettingsFile {
    model: Option<String>,
    effort: Option<String>,
    mode: Option<String>,
}

/// The effective settings for one workspace.
#[derive(Clone)]
pub struct Settings {
    pub default_model: String,
    pub default_effort: EffortLevel,
    pub default_mode: SessionMode,
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
    load_from(&home_dir()?.join(".config/ox/settings.json"), catalog)
}

fn load_from(path: &Path, catalog: &[CatalogModel]) -> io::Result<Settings> {
    let file = read(path, catalog)?;
    let default_model = file
        .model
        .ok_or_else(|| invalid(path, "model must name the default model"))?;
    let settings = Settings {
        default_model,
        default_effort: effort(path, file.effort)?,
        default_mode: mode(path, file.mode)?,
    };
    settings.validate(path, catalog)?;
    Ok(settings)
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
        let settings = Self {
            default_model: file.model.unwrap_or_else(|| self.default_model.clone()),
            default_effort: file
                .effort
                .map(|value| effort(&path, Some(value)))
                .transpose()?
                .unwrap_or(self.default_effort),
            default_mode: file
                .mode
                .map(|value| mode(&path, Some(value)))
                .transpose()?
                .unwrap_or(self.default_mode),
        };
        settings.validate(&path, openrouter::catalog())?;
        Ok(settings)
    }

    fn validate(&self, path: &Path, catalog: &[CatalogModel]) -> io::Result<()> {
        let model = catalog
            .iter()
            .find(|model| model.id == self.default_model)
            .expect("the selected model was validated while reading settings");
        if !model.supports(self.default_effort) {
            return Err(invalid(
                path,
                &format!(
                    "effort {} is not supported by model {}",
                    self.default_effort.id(),
                    self.default_model
                ),
            ));
        }
        Ok(())
    }
}

fn effort(path: &Path, value: Option<String>) -> io::Result<EffortLevel> {
    match value {
        Some(value) => EffortLevel::from_id(&value)
            .ok_or_else(|| invalid(path, &format!("unknown effort {value}"))),
        None => Ok(EffortLevel::Default),
    }
}

fn mode(path: &Path, value: Option<String>) -> io::Result<SessionMode> {
    match value {
        Some(value) => SessionMode::from_id(&value)
            .ok_or_else(|| invalid(path, &format!("unknown mode {value}"))),
        None => Ok(SessionMode::Ask),
    }
}

/// Saves all current session settings in the workspace file when it exists,
/// otherwise in the global file. Returns whether the global file was written.
pub fn save(home: &Path, workspace: &Path, selected: &SessionSettings) -> io::Result<bool> {
    let workspace_path = workspace.join(".ox/settings.json");
    let global = !workspace_path.try_exists()?;
    let path = if global {
        home.join(".config/ox/settings.json")
    } else {
        workspace_path
    };
    write(&path, selected)?;
    Ok(global)
}

fn write(path: &Path, selected: &SessionSettings) -> io::Result<()> {
    let write_file = || {
        let mut file: serde_json::Value = match text_file::read_bounded(path) {
            Ok(text) => serde_json::from_str(&text).map_err(io::Error::other)?,
            Err(error) if error.kind() == io::ErrorKind::NotFound => serde_json::json!({}),
            Err(error) => return Err(error),
        };
        let fields = file.as_object_mut().ok_or_else(|| {
            io::Error::new(io::ErrorKind::InvalidData, "settings must be an object")
        })?;
        fields.insert("model".into(), selected.model.clone().into());
        fields.insert("effort".into(), selected.effort.id().into());
        fields.insert("mode".into(), selected.mode.id().into());
        let mut text = serde_json::to_string_pretty(&file).map_err(io::Error::other)?;
        text.push('\n');
        let directory = path.parent().expect("settings path has a parent");
        std::fs::create_dir_all(directory)?;
        let temporary = directory.join(format!(".settings-{}.tmp", uuid::Uuid::new_v4()));
        std::fs::write(&temporary, text)?;
        let result = std::fs::rename(&temporary, path);
        if result.is_err() {
            let _ = std::fs::remove_file(&temporary);
        }
        result
    };
    write_file()
        .map_err(|error| io::Error::new(error.kind(), format!("{}: {error}", path.display())))
}

/// Reads one settings file, rejecting a model outside `catalog`.
/// Every error names the file.
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
    fn settings_require_a_known_default_model() {
        let catalog = openrouter::catalog();
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        assert!(load_from(&path, catalog).is_err());
        for (text, valid) in [
            (r#"{"model":"M"}"#, true),
            (r#"{"model":"a/b"}"#, false),
            (r#"{"model":" "}"#, false),
            (r#"{"model":"M","extra":{}}"#, true),
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
                    assert_eq!(settings.default_effort, EffortLevel::Default);
                    assert_eq!(settings.default_mode, SessionMode::Ask);
                }
                Err(error) => assert!(error.to_string().contains(path.to_str().unwrap())),
            }
        }
        std::fs::remove_file(&path).unwrap();
        std::fs::create_dir(&path).unwrap();
        assert!(load_from(&path, catalog).is_err());
    }

    #[test]
    fn workspace_settings_override_the_default_model() {
        let settings = Settings {
            default_model: DEFAULT_MODEL.to_owned(),
            default_effort: EffortLevel::Default,
            default_mode: SessionMode::Ask,
        };
        let workspace = Workspace::new();
        let unchanged = settings.for_workspace(&workspace.0).unwrap();
        assert_eq!(unchanged.default_model, DEFAULT_MODEL);

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
        }

        for (text, error) in [
            (
                r#"{"model":"a/b"}"#,
                "model a/b is not in the OpenRouter model catalog",
            ),
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

    #[test]
    fn settings_validate_effort_and_mode_with_the_effective_model() {
        let catalog = openrouter::catalog();
        let home = Workspace::new();
        let path = home.0.join("settings.json");
        std::fs::write(
            &path,
            format!(r#"{{"model":"{DEFAULT_MODEL}","effort":"high","mode":"auto"}}"#),
        )
        .unwrap();
        let settings = load_from(&path, catalog).unwrap();
        assert_eq!(settings.default_effort, EffortLevel::High);
        assert_eq!(settings.default_mode, SessionMode::Auto);

        let workspace = Workspace::new();
        let workspace_path = workspace.0.join(".ox/settings.json");
        std::fs::create_dir(workspace.0.join(".ox")).unwrap();
        for (text, expected) in [
            (
                r#"{"effort":"low","mode":"ask"}"#,
                (EffortLevel::Low, SessionMode::Ask),
            ),
            (r#"{"mode":"ask"}"#, (EffortLevel::High, SessionMode::Ask)),
        ] {
            std::fs::write(&workspace_path, text).unwrap();
            let effective = settings.for_workspace(&workspace.0).unwrap();
            assert_eq!((effective.default_effort, effective.default_mode), expected);
        }
        for (text, error) in [
            (
                r#"{"effort":"unknown"}"#.to_owned(),
                "unknown effort unknown",
            ),
            (r#"{"mode":"unknown"}"#.to_owned(), "unknown mode unknown"),
            (
                format!(r#"{{"model":"{}","effort":"max"}}"#, catalog[2].id),
                "effort max is not supported",
            ),
        ] {
            std::fs::write(&workspace_path, &text).unwrap();
            let message = settings
                .for_workspace(&workspace.0)
                .err()
                .unwrap()
                .to_string();
            assert!(
                message.starts_with(&format!("{}: ", workspace_path.display()))
                    && message.contains(error),
                "{text}: {message}"
            );
        }
        for (field, value, error) in [
            ("effort", "unknown", "unknown effort unknown"),
            ("mode", "unknown", "unknown mode unknown"),
            ("effort", "xhigh", "effort xhigh is not supported"),
        ] {
            std::fs::write(
                &path,
                format!(r#"{{"model":"{DEFAULT_MODEL}","{field}":"{value}"}}"#),
            )
            .unwrap();
            let message = load_from(&path, catalog).err().unwrap().to_string();
            assert!(
                message.starts_with(&format!("{}: ", path.display())) && message.contains(error),
                "{field}: {message}"
            );
        }
    }

    #[test]
    fn saving_session_settings_preserves_other_fields_and_uses_the_workspace_file_when_present() {
        let home = Workspace::new();
        let workspace = Workspace::new();
        let global = home.0.join(".config/ox/settings.json");
        let local = workspace.0.join(".ox/settings.json");
        let selected =
            SessionSettings::new(DEFAULT_MODEL, EffortLevel::Low).with_mode(SessionMode::Auto);
        assert!(save(&home.0, &workspace.0, &selected).unwrap());
        assert_eq!(
            load_from(&global, openrouter::catalog())
                .unwrap()
                .default_effort,
            EffortLevel::Low
        );
        std::fs::create_dir(workspace.0.join(".ox")).unwrap();
        std::fs::write(&local, r#"{"model":"z-ai/glm-5.3-flash","other":42}"#).unwrap();
        assert!(!save(&home.0, &workspace.0, &selected).unwrap());
        let saved: serde_json::Value =
            serde_json::from_slice(&std::fs::read(&local).unwrap()).unwrap();
        assert_eq!(saved["other"], 42);
        assert_eq!(saved["model"], DEFAULT_MODEL);
        assert_eq!(saved["effort"], "low");
        assert_eq!(saved["mode"], "auto");
        let reloaded = load_from(&global, openrouter::catalog())
            .unwrap()
            .for_workspace(&workspace.0)
            .unwrap();
        assert_eq!(reloaded.default_model, DEFAULT_MODEL);
        assert_eq!(reloaded.default_effort, EffortLevel::Low);
        assert_eq!(reloaded.default_mode, SessionMode::Auto);
        assert_eq!(
            load_from(&global, openrouter::catalog())
                .unwrap()
                .default_mode,
            SessionMode::Auto
        );
    }
}
