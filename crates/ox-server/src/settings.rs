//! Settings: the global settings file `~/.config/ox/settings.json`, read once
//! at process startup, and the workspace settings file `.ox/settings.json`, read
//! when a session becomes active, whose keys replace the same keys from the
//! first file. Neither file is required. Only the global settings file can set
//! `models`, the OpenRouter provider pins. The Ox client reads and writes other
//! fields of the global settings file.

use std::{
    collections::BTreeMap,
    io,
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{
    model::{self, CatalogModel, Provider},
    sessions::{EffortLevel, SessionMode, SessionSettings},
    text_file,
};

/// The model used when no settings file sets `model`. An OpenRouter alias for
/// the latest DeepSeek Flash model, so the catalog's release-date filter does
/// not drop it as a fixed model ID eventually would.
const BUILT_IN_MODEL: &str = "openrouter:~deepseek/deepseek-flash-latest";

/// The format of both settings files.
#[derive(Deserialize, Default)]
struct SettingsFile {
    #[serde(default, deserialize_with = "present")]
    provider: bool,
    model: Option<String>,
    effort: Option<String>,
    mode: Option<String>,
    models: Option<BTreeMap<String, ModelSettings>>,
}

/// One entry under `models`, keyed by model ID.
#[derive(Deserialize)]
struct ModelSettings {
    providers: Vec<String>,
}

fn present<'de, D: serde::Deserializer<'de>>(deserializer: D) -> Result<bool, D::Error> {
    serde::de::IgnoredAny::deserialize(deserializer)?;
    Ok(true)
}

/// The default model, effort, and mode: the global settings file's values,
/// or, after `for_workspace`, with the workspace settings file's keys applied.
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

/// The global settings file in `home`.
pub fn global_path(home: &Path) -> PathBuf {
    home.join(".config/ox/settings.json")
}

/// Reads the global settings file and sets each pinned OpenRouter model's
/// providers. Without `model`, selects the installed providers' default model.
pub fn load(catalog: &mut [CatalogModel]) -> io::Result<Settings> {
    load_from(&global_path(&home_dir()?), catalog)
}

fn load_from(path: &Path, catalog: &mut [CatalogModel]) -> io::Result<Settings> {
    let file = match read(path) {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => SettingsFile::default(),
        Err(error) => return Err(error),
    };
    reject_provider(path, file.provider)?;
    let default_model = file.model.unwrap_or_else(|| {
        if catalog
            .iter()
            .any(|model| model.provider == Provider::OpenRouter)
        {
            BUILT_IN_MODEL.to_owned()
        } else {
            catalog
                .first()
                .expect("the catalog has usable models")
                .qualified_id()
        }
    });
    let settings = Settings {
        default_model,
        default_effort: effort(path, file.effort)?,
        default_mode: mode(path, file.mode)?,
    };
    settings.validate(path, catalog)?;
    for (id, pin) in file.models.unwrap_or_default() {
        let model = Provider::split_qualified_model_id(&id)
            .filter(|(provider, _)| *provider == Provider::OpenRouter)
            .and_then(|(provider, provider_id)| {
                catalog
                    .iter_mut()
                    .find(|model| model.provider == provider && model.id == provider_id)
            })
            .ok_or_else(|| {
                invalid(
                    path,
                    &format!("model {id} in models is not in the OpenRouter model catalog"),
                )
            })?;
        if pin.providers.is_empty() {
            return Err(invalid(path, &format!("model {id} lists no providers")));
        }
        model.providers = pin.providers;
    }
    Ok(settings)
}

impl Settings {
    /// These settings with each key set in the workspace settings file of
    /// `workspace_path` replaced. A missing file changes nothing.
    pub fn for_workspace(&self, workspace_path: &Path) -> io::Result<Self> {
        let path = workspace_path.join(".ox/settings.json");
        let file = match read(&path) {
            Ok(file) => file,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(self.clone()),
            Err(error) => return Err(error),
        };
        reject_provider(&path, file.provider)?;
        if file.models.is_some() {
            return Err(invalid(
                &path,
                "models can be set only in the global settings file",
            ));
        }
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
        settings.validate(&path, model::catalog())?;
        Ok(settings)
    }

    fn validate(&self, path: &Path, catalog: &[CatalogModel]) -> io::Result<()> {
        let model = model::catalog_model_in(catalog, &self.default_model).ok_or_else(|| {
            invalid(
                path,
                &format!("model {} is not in the model catalog", self.default_model),
            )
        })?;
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

fn reject_provider(path: &Path, provider: bool) -> io::Result<()> {
    if provider {
        return Err(invalid(
            path,
            "provider is no longer supported; use a provider-qualified model ID",
        ));
    }
    Ok(())
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
pub fn save(home: &Path, workspace_path: &Path, selected: &SessionSettings) -> io::Result<bool> {
    let workspace_file = workspace_path.join(".ox/settings.json");
    let global = !workspace_file.try_exists()?;
    let path = if global {
        global_path(home)
    } else {
        workspace_file
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

/// Reads one settings file. Every error names the file.
fn read(path: &Path) -> io::Result<SettingsFile> {
    let parse = || {
        serde_json::from_str(&text_file::read_bounded(path)?)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))
    };
    parse().map_err(|error| io::Error::new(error.kind(), format!("{}: {error}", path.display())))
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
    use crate::{
        openrouter,
        openrouter::fixture::{DEFAULT_MODEL, NOW},
        tools::fixture::Workspace,
    };

    /// A model catalog `load_from` can set provider pins on.
    fn catalog() -> Vec<CatalogModel> {
        openrouter::parse_catalog(openrouter::fixture::CATALOG, NOW).unwrap()
    }

    #[test]
    fn settings_default_to_the_built_in_model_without_a_file_or_model() {
        let built_in_provider_id = BUILT_IN_MODEL.split_once(':').unwrap().1;
        let mut with_built_in = openrouter::parse_catalog(
            &format!(
                r#"{{"data": [{{"id": "{built_in_provider_id}", "name": "Built In",
                 "context_length": 1048576, "created": {NOW},
                 "pricing": {{"prompt": "0", "completion": "0"}},
                 "architecture": {{"input_modalities": ["text"], "output_modalities": ["text"]}},
                 "supported_parameters": ["tools"]}}]}}"#
            ),
            NOW,
        )
        .unwrap();
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        for text in [None, Some("{}")] {
            if let Some(text) = text {
                std::fs::write(&path, text).unwrap();
            }
            let settings = load_from(&path, &mut with_built_in).unwrap();
            assert_eq!(settings.default_model, BUILT_IN_MODEL, "{text:?}");
            assert_eq!(settings.default_effort, EffortLevel::Default, "{text:?}");
            assert_eq!(settings.default_mode, SessionMode::Ask, "{text:?}");
        }

        std::fs::remove_file(&path).unwrap();
        for text in [None, Some("{}")] {
            if let Some(text) = text {
                std::fs::write(&path, text).unwrap();
            }
            let message = load_from(&path, &mut catalog()).err().unwrap().to_string();
            assert_eq!(
                message,
                format!(
                    "{}: model {BUILT_IN_MODEL} is not in the model catalog",
                    path.display()
                ),
                "{text:?}"
            );
        }
    }

    #[test]
    fn settings_files_must_be_valid_json_naming_a_known_model() {
        let mut catalog = catalog();
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        for (text, valid) in [
            (r#"{"model":"M"}"#, true),
            (r#"{"model":"a/b"}"#, false),
            (r#"{"model":"unknown:a/b"}"#, false),
            (r#"{"model":"openrouter:"}"#, false),
            (r#"{"model":" "}"#, false),
            (r#"{"model":"M","extra":{}}"#, true),
            ("", false),
            ("not json", false),
        ] {
            let text = text.replace('M', DEFAULT_MODEL);
            std::fs::write(&path, &text).unwrap();
            let result = load_from(&path, &mut catalog);
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
        assert!(load_from(&path, &mut catalog).is_err());
    }

    #[test]
    fn settings_accept_qualified_models_from_each_installed_provider() {
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        let mut models = catalog();
        models.extend(crate::openai::parse_catalog(crate::openai::fixture::CATALOG).unwrap());
        for provider in [Provider::OpenRouter, Provider::OpenAI] {
            let chosen = models
                .iter()
                .find(|model| model.provider == provider)
                .unwrap()
                .qualified_id();
            std::fs::write(&path, format!(r#"{{"model":"{chosen}"}}"#)).unwrap();
            assert_eq!(load_from(&path, &mut models).unwrap().default_model, chosen);
        }
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

        let chosen = model::catalog()[1].qualified_id();
        assert_ne!(chosen, DEFAULT_MODEL);
        let path = workspace.0.join(".ox/settings.json");
        std::fs::create_dir(workspace.0.join(".ox")).unwrap();
        for (text, model) in [
            ("{}", DEFAULT_MODEL),
            (&format!(r#"{{"model":"{chosen}"}}"#), chosen.as_str()),
        ] {
            std::fs::write(&path, text).unwrap();
            let overridden = settings.for_workspace(&workspace.0).unwrap();
            assert_eq!(overridden.default_model, model, "{text}");
        }

        for (text, error) in [
            (
                r#"{"provider":"openai"}"#,
                "provider is no longer supported",
            ),
            (
                r#"{"model":"a/b"}"#,
                "model a/b is not in the model catalog",
            ),
            ("not json", "expected"),
            (
                r#"{"models":{}}"#,
                "models can be set only in the global settings file",
            ),
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
        let mut catalog = catalog();
        let home = Workspace::new();
        let path = home.0.join("settings.json");
        std::fs::write(
            &path,
            format!(r#"{{"model":"{DEFAULT_MODEL}","effort":"high","mode":"auto"}}"#),
        )
        .unwrap();
        let settings = load_from(&path, &mut catalog).unwrap();
        assert_eq!(settings.default_effort, EffortLevel::High);
        assert_eq!(settings.default_mode, SessionMode::Auto);

        let workspace = Workspace::new();
        let workspace_file = workspace.0.join(".ox/settings.json");
        std::fs::create_dir(workspace.0.join(".ox")).unwrap();
        for (text, expected) in [
            (
                r#"{"effort":"low","mode":"ask"}"#,
                (EffortLevel::Low, SessionMode::Ask),
            ),
            (r#"{"mode":"ask"}"#, (EffortLevel::High, SessionMode::Ask)),
        ] {
            std::fs::write(&workspace_file, text).unwrap();
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
                format!(
                    r#"{{"model":"{}","effort":"max"}}"#,
                    catalog[2].qualified_id()
                ),
                "effort max is not supported",
            ),
        ] {
            std::fs::write(&workspace_file, &text).unwrap();
            let message = settings
                .for_workspace(&workspace.0)
                .err()
                .unwrap()
                .to_string();
            assert!(
                message.starts_with(&format!("{}: ", workspace_file.display()))
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
            let message = load_from(&path, &mut catalog).err().unwrap().to_string();
            assert!(
                message.starts_with(&format!("{}: ", path.display())) && message.contains(error),
                "{field}: {message}"
            );
        }
    }

    #[test]
    fn global_settings_pin_providers_for_models() {
        let directory = Workspace::new();
        let path = directory.0.join("settings.json");
        let mut pinned_catalog = catalog();
        let pinned = pinned_catalog[1].qualified_id();
        std::fs::write(
            &path,
            format!(
                r#"{{"model":"{DEFAULT_MODEL}","models":{{"{pinned}":{{"providers":["b","a"]}}}}}}"#
            ),
        )
        .unwrap();
        load_from(&path, &mut pinned_catalog).unwrap();
        for model in &pinned_catalog {
            let expected: &[&str] = if model.qualified_id() == pinned {
                &["b", "a"]
            } else {
                &[]
            };
            assert_eq!(model.providers, expected, "{}", model.id);
        }

        for (models, error) in [
            (
                r#"{"a/b":{"providers":["a"]}}"#.to_owned(),
                "model a/b in models is not in the OpenRouter model catalog".to_owned(),
            ),
            (
                format!(r#"{{"{pinned}":{{"providers":[]}}}}"#),
                format!("model {pinned} lists no providers"),
            ),
        ] {
            std::fs::write(
                &path,
                format!(r#"{{"model":"{DEFAULT_MODEL}","models":{models}}}"#),
            )
            .unwrap();
            let message = load_from(&path, &mut catalog()).err().unwrap().to_string();
            assert_eq!(message, format!("{}: {error}", path.display()), "{models}");
        }
    }

    #[test]
    fn saving_session_settings_preserves_other_fields_and_uses_the_workspace_file_when_present() {
        let home = Workspace::new();
        let workspace = Workspace::new();
        let global = global_path(&home.0);
        let local = workspace.0.join(".ox/settings.json");
        let selected =
            SessionSettings::new(DEFAULT_MODEL, EffortLevel::Low).with_mode(SessionMode::Auto);
        std::fs::create_dir_all(global.parent().unwrap()).unwrap();
        let client_fields =
            r#"{"servers":[{"name":"Alpha","command":"alpha"}],"favorites":["openrouter:a/b"]}"#;
        std::fs::write(&global, client_fields).unwrap();
        assert!(save(&home.0, &workspace.0, &selected).unwrap());
        assert_eq!(
            load_from(&global, &mut catalog()).unwrap().default_effort,
            EffortLevel::Low
        );
        let saved: serde_json::Value =
            serde_json::from_slice(&std::fs::read(&global).unwrap()).unwrap();
        assert_eq!(saved["servers"][0]["name"], "Alpha");
        assert_eq!(saved["favorites"], serde_json::json!(["openrouter:a/b"]));
        assert!(saved.get("provider").is_none());
        std::fs::create_dir(workspace.0.join(".ox")).unwrap();
        std::fs::write(
            &local,
            r#"{"model":"openrouter:z-ai/glm-5.3-flash","other":42}"#,
        )
        .unwrap();
        assert!(!save(&home.0, &workspace.0, &selected).unwrap());
        let saved: serde_json::Value =
            serde_json::from_slice(&std::fs::read(&local).unwrap()).unwrap();
        assert_eq!(saved["other"], 42);
        assert_eq!(saved["model"], DEFAULT_MODEL);
        assert_eq!(saved["effort"], "low");
        assert_eq!(saved["mode"], "auto");
        let reloaded = load_from(&global, &mut catalog())
            .unwrap()
            .for_workspace(&workspace.0)
            .unwrap();
        assert_eq!(reloaded.default_model, DEFAULT_MODEL);
        assert_eq!(reloaded.default_effort, EffortLevel::Low);
        assert_eq!(reloaded.default_mode, SessionMode::Auto);
        assert_eq!(
            load_from(&global, &mut catalog()).unwrap().default_mode,
            SessionMode::Auto
        );
    }

    #[test]
    fn obsolete_provider_is_rejected_with_qualified_model_guidance() {
        let home = Workspace::new();
        let path = home.0.join("settings.json");
        for value in ["openrouter", "openai", "unknown"] {
            std::fs::write(&path, format!(r#"{{"provider":"{value}"}}"#)).unwrap();
            let error = match load_from(&path, &mut catalog()) {
                Err(error) => error,
                Ok(_) => panic!("obsolete provider was accepted"),
            };
            assert!(
                error
                    .to_string()
                    .contains("use a provider-qualified model ID"),
                "{value}: {error}"
            );
        }
    }

    #[test]
    fn openai_only_defaults_to_its_first_model() {
        let home = Workspace::new();
        let path = home.0.join("settings.json");
        std::fs::write(&path, "{}").unwrap();
        let mut models = crate::openai::parse_catalog(crate::openai::fixture::CATALOG).unwrap();
        let selected = load_from(&path, &mut models).unwrap();
        assert_eq!(selected.default_model, models[0].qualified_id());
    }
}
