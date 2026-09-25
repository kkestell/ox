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
    text_file,
};

/// The format of both settings files.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SettingsFile {
    model: Option<String>,
}

/// The effective settings for one workspace.
#[derive(Clone)]
pub struct Settings {
    pub default_model: String,
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
    Ok(Settings { default_model })
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
        Ok(Self {
            default_model: file.model.unwrap_or_else(|| self.default_model.clone()),
        })
    }
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
            (r#"{"hooks":{}}"#, false),
            (r#"{"model":"M","hooks":{}}"#, false),
            (r#"{"model":"M","extra":{}}"#, false),
            ("{}", false),
            ("", false),
            ("not json", false),
        ] {
            let text = text.replace('M', DEFAULT_MODEL);
            std::fs::write(&path, &text).unwrap();
            let result = load_from(&path, catalog);
            assert_eq!(result.is_ok(), valid, "{text}: {:?}", result.as_ref().err());
            match result {
                Ok(settings) => assert_eq!(settings.default_model, DEFAULT_MODEL),
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
            (r#"{"hooks":{}}"#, "unknown field `hooks`"),
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
