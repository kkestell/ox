mod acp;
mod auth;
mod cancellation;
mod model;
mod openai;
mod openai_auth;
mod openrouter;
mod process;
mod sessions;
mod settings;
mod shell_processes;
mod skills;
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
            .map(model::CatalogModel::qualified_id)
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
        model.qualified_id(),
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

/// Fetches the catalogs of every enabled provider in installed order.
async fn discover_catalogs(
    openrouter: Option<openrouter::Client>,
    openai: Option<openai::Client>,
) -> io::Result<(Vec<model::CatalogModel>, model::Clients)> {
    if openrouter.is_none() && openai.is_none() {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "model provider authentication required; run `ox auth login openrouter` or `ox auth login openai`",
        ));
    }
    let mut catalog = Vec::new();
    if let Some(client) = &openrouter {
        catalog.extend(client.fetch_catalog().await?);
    }
    if let Some(client) = &openai {
        catalog.extend(client.fetch_catalog().await?);
    }
    Ok((catalog, model::Clients { openrouter, openai }))
}

/// Discovers credentials, fetches and installs the enabled model catalogs, and
/// returns the settings checked against them.
async fn load_settings_and_catalog() -> io::Result<(settings::Settings, model::Clients)> {
    let openrouter = auth::api_key()?.map(openrouter::Client::new);
    let authentication = openai_auth::Authentication::new()?;
    let openai = authentication
        .has_credentials()?
        .then(|| openai::Client::with_authentication(authentication));
    let (mut catalog, clients) = discover_catalogs(openrouter, openai).await?;
    let settings = settings::load(&mut catalog)?;
    model::install_catalog(catalog);
    Ok((settings, clients))
}

/// Serves one ACP connection over stdin and stdout.
pub async fn serve() -> Result<(), Box<dyn Error>> {
    let (settings, clients) = load_settings_and_catalog().await?;
    acp::serve_stdio(settings, clients).await
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
    let (settings, clients) = load_settings_and_catalog().await?;
    let settings = settings.for_workspace(&dir)?;
    let model = resolve_model(model, settings.default_model)?;
    check_effort(&model, effort)?;
    acp::run_headless(&dir, model, effort, prompt, clients).await
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
        let chosen = model::catalog()[1].qualified_id();
        assert_eq!(resolve_model(None, default.to_owned()).unwrap(), default);
        assert_eq!(
            resolve_model(Some(chosen.clone()), default.to_owned()).unwrap(),
            chosen
        );
        assert!(
            resolve_model(Some("retired/model".to_owned()), String::new())
                .unwrap_err()
                .to_string()
                .contains("not a model")
        );
        check_effort(&chosen, EffortLevel::XHigh).unwrap();
        assert!(
            check_effort(default, EffortLevel::XHigh)
                .unwrap_err()
                .to_string()
                .contains("choose one of default, low, medium, high, max")
        );
    }

    #[tokio::test]
    async fn startup_discovers_each_enabled_provider_in_fixed_order() {
        use openrouter::fixture::{Reply, Server, catalog_reply};

        let openrouter = Server::start(vec![catalog_reply()]).await;
        let (catalog, clients) = discover_catalogs(Some(openrouter.client()), None)
            .await
            .unwrap();
        assert!(
            catalog
                .iter()
                .all(|model| model.provider == Provider::OpenRouter)
        );
        assert!(clients.openrouter.is_some());
        assert!(clients.openai.is_none());

        let openai = openai::fixture::Server::start(vec![Reply::Status(
            200,
            openai::fixture::CATALOG.to_owned(),
        )])
        .await;
        let (catalog, clients) = discover_catalogs(None, Some(openai.client()))
            .await
            .unwrap();
        assert!(
            catalog
                .iter()
                .all(|model| model.provider == Provider::OpenAI)
        );
        assert!(clients.openrouter.is_none());
        assert!(clients.openai.is_some());

        let openrouter = Server::start(vec![catalog_reply()]).await;
        let openai = openai::fixture::Server::start(vec![Reply::Status(
            200,
            openai::fixture::CATALOG.to_owned(),
        )])
        .await;
        let (catalog, clients) =
            discover_catalogs(Some(openrouter.client()), Some(openai.client()))
                .await
                .unwrap();
        let first_openai = catalog
            .iter()
            .position(|model| model.provider == Provider::OpenAI)
            .unwrap();
        assert!(
            catalog[..first_openai]
                .iter()
                .all(|model| model.provider == Provider::OpenRouter)
        );
        assert!(
            catalog[first_openai..]
                .iter()
                .all(|model| model.provider == Provider::OpenAI)
        );
        assert!(clients.openrouter.is_some());
        assert!(clients.openai.is_some());
    }

    #[tokio::test]
    async fn startup_requires_credentials_and_does_not_ignore_catalog_failures() {
        let error = match discover_catalogs(None, None).await {
            Err(error) => error,
            Ok(_) => panic!("startup without credentials succeeded"),
        };
        let message = error.to_string();
        assert!(message.contains("ox auth login openrouter"));
        assert!(message.contains("ox auth login openai"));

        let openrouter =
            openrouter::fixture::Server::start(vec![openrouter::fixture::Reply::Status(
                500,
                "{}".to_owned(),
            )])
            .await;
        let openai = openai::fixture::Server::start(vec![]).await;
        let error = match discover_catalogs(Some(openrouter.client()), Some(openai.client())).await
        {
            Err(error) => error,
            Ok(_) => panic!("startup ignored an enabled provider failure"),
        };
        assert!(
            error
                .to_string()
                .contains("OpenRouter model catalog returned 500")
        );
        assert!(openai.requests().is_empty());

        let openrouter =
            openrouter::fixture::Server::start(vec![openrouter::fixture::catalog_reply()]).await;
        let openai = openai::fixture::Server::start(vec![openrouter::fixture::Reply::Status(
            500,
            "{}".to_owned(),
        )])
        .await;
        let error = match discover_catalogs(Some(openrouter.client()), Some(openai.client())).await
        {
            Err(error) => error,
            Ok(_) => panic!("startup ignored an enabled provider failure"),
        };
        assert!(
            error
                .to_string()
                .contains("OpenAI model catalog returned 500")
        );
    }
}
