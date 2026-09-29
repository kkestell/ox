mod acp;
mod auth;
mod cancellation;
mod compaction;
mod openrouter;
mod process;
mod sessions;
mod settings;
mod shell_processes;
mod skills;
mod subagents;
mod system_prompt;
mod text_file;
mod tools;

use std::{env, error::Error, io, path::Path};

pub use sessions::EffortLevel;
pub use settings::{global_path, home_dir};

/// Resolves a `--model` choice against the installed model catalog.
fn resolve_model(model: Option<String>, default_model: String) -> io::Result<String> {
    let Some(model) = model else {
        return Ok(default_model);
    };
    if openrouter::catalog_model(&model).is_some() {
        return Ok(model);
    }
    Err(invalid_input(format!(
        "{model} is not a model; choose one of {}",
        openrouter::catalog()
            .iter()
            .map(|model| model.id.as_str())
            .collect::<Vec<_>>()
            .join(", ")
    )))
}

/// Rejects an effort level the chosen model does not list.
fn check_effort(model: &str, effort: EffortLevel) -> io::Result<()> {
    let model = openrouter::catalog_model(model).expect("a resolved model is in the catalog");
    if model.supports(effort) {
        return Ok(());
    }
    Err(invalid_input(format!(
        "{} does not accept effort {}; choose one of {}",
        model.id,
        effort.id(),
        model
            .efforts
            .iter()
            .map(|effort| effort.id())
            .collect::<Vec<_>>()
            .join(", ")
    )))
}

fn invalid_input(message: String) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

/// Fetches and installs the model catalog and returns the settings checked
/// against it.
async fn load_settings_and_catalog() -> io::Result<settings::Settings> {
    let catalog = openrouter::fetch_catalog().await?;
    let settings = settings::load(&catalog)?;
    openrouter::install_catalog(catalog);
    Ok(settings)
}

/// Serves one ACP connection over stdin and stdout.
pub async fn serve() -> Result<(), Box<dyn Error>> {
    acp::serve_stdio(load_settings_and_catalog().await?).await
}

/// Runs one prompt in `dir` and returns the final answer.
pub async fn run(
    dir: &Path,
    model: Option<String>,
    effort: EffortLevel,
    prompt: String,
) -> Result<String, Box<dyn Error>> {
    let dir = dir
        .canonicalize()
        .map_err(|error| format!("opening {}: {error}", dir.display()))?;
    let settings = load_settings_and_catalog().await?.for_workspace(&dir)?;
    let model = resolve_model(model, settings.default_model)?;
    check_effort(&model, effort)?;
    acp::run_headless(&dir, model, effort, prompt).await
}

/// Prompts for an OpenRouter API key, verifies it, and saves it in the
/// keyring.
pub async fn login() -> Result<(), Box<dyn Error>> {
    let api_key = rpassword::prompt_password("OpenRouter API key: ")?;
    let api_key = api_key.trim();
    if api_key.is_empty() {
        return Err(invalid_input("OpenRouter API key cannot be empty".to_owned()).into());
    }
    openrouter::Client::new(api_key.to_owned()).verify().await?;
    auth::save_api_key(api_key)?;
    println!("OpenRouter API key saved.");
    Ok(())
}

/// Removes the saved OpenRouter API key.
pub fn logout() -> io::Result<()> {
    if auth::delete_api_key()? {
        println!("OpenRouter API key removed.");
    } else {
        println!("No saved OpenRouter API key.");
    }
    if env::var_os("OPENROUTER_API_KEY").is_some() {
        eprintln!("OPENROUTER_API_KEY is still set and will continue to be used.");
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn model_and_effort_choices_must_be_in_the_catalog() {
        let default = openrouter::fixture::DEFAULT_MODEL;
        let chosen = openrouter::catalog()[1].id.as_str();
        assert_eq!(resolve_model(None, default.to_owned()).unwrap(), default);
        assert_eq!(
            resolve_model(Some(chosen.to_owned()), default.to_owned()).unwrap(),
            chosen
        );
        assert!(
            resolve_model(Some("retired/model".to_owned()), String::new())
                .unwrap_err()
                .to_string()
                .contains("not a model")
        );
        check_effort(chosen, EffortLevel::XHigh).unwrap();
        assert!(
            check_effort(default, EffortLevel::XHigh)
                .unwrap_err()
                .to_string()
                .contains("choose one of default, low, medium, high, max")
        );
    }
}
