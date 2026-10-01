mod acp;
mod auth;
mod cancellation;
mod compaction;
mod model;
mod openai;
mod openai_auth;
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

pub use model::Provider;
pub use sessions::EffortLevel;
pub use settings::{global_path, home_dir};

#[cfg(any(test, feature = "test-support"))]
pub mod fixture {
    pub use crate::acp::fixture::serve_connection;
    pub use crate::openrouter::fixture::*;
}

/// Resolves a `--model` choice against the installed model catalog.
fn resolve_model(model: Option<String>, default_model: String) -> io::Result<String> {
    let Some(model) = model else {
        return Ok(default_model);
    };
    if model::catalog_model(&model).is_some() {
        return Ok(model);
    }
    Err(invalid_input(format!(
        "{model} is not a model; choose one of {}",
        model::catalog()
            .iter()
            .map(|model| model.id.as_str())
            .collect::<Vec<_>>()
            .join(", ")
    )))
}

/// Rejects an effort level the chosen model does not list.
fn check_effort(model: &str, effort: EffortLevel) -> io::Result<()> {
    let model = model::catalog_model(model).expect("a resolved model is in the catalog");
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
async fn load_settings_and_catalog() -> io::Result<(settings::Settings, Option<model::Client>)> {
    let provider = settings::provider()?;
    let (mut catalog, client) = match provider {
        Provider::OpenRouter => (openrouter::fetch_catalog().await?, None),
        Provider::OpenAI => {
            let client = openai::Client::new()?;
            let catalog = client.fetch_catalog().await?;
            (catalog, Some(model::Client::OpenAI(client)))
        }
    };
    let settings = settings::load(&mut catalog)?;
    model::install_catalog(catalog);
    Ok((settings, client))
}

/// Serves one ACP connection over stdin and stdout.
pub async fn serve() -> Result<(), Box<dyn Error>> {
    let (settings, client) = load_settings_and_catalog().await?;
    acp::serve_stdio(settings, client).await
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
    let (settings, client) = load_settings_and_catalog().await?;
    let settings = settings.for_workspace(&dir)?;
    let model = resolve_model(model, settings.default_model)?;
    check_effort(&model, effort)?;
    let client = match client {
        Some(client) => client,
        None => openrouter::Client::new(auth::api_key()?.ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::PermissionDenied,
                "OpenRouter authentication required; run `ox auth login openrouter`",
            )
        })?)
        .into(),
    };
    acp::run_headless(&dir, model, effort, prompt, client).await
}

/// Authenticates and saves credentials for the named provider.
pub async fn login(provider: Provider) -> Result<(), Box<dyn Error>> {
    if provider == Provider::OpenAI {
        openai_auth::Authentication::new()?.login().await?;
        println!("OpenAI credentials saved.");
        return Ok(());
    }
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

/// Removes the named provider's saved credentials.
pub async fn logout(provider: Provider) -> io::Result<()> {
    if provider == Provider::OpenAI {
        if openai_auth::Authentication::new()?.logout().await? {
            println!("OpenAI credentials removed.");
        } else {
            println!("No saved OpenAI credentials.");
        }
        return Ok(());
    }
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
        let chosen = model::catalog()[1].id.as_str();
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
