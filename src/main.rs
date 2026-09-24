mod acp;
mod auth;
mod cancellation;
mod compaction;
mod hooks;
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

use std::{
    env,
    error::Error,
    io,
    path::{Path, PathBuf},
    process::ExitCode,
};

use sessions::EffortLevel;

const USAGE: &str = "ox [run [--dir <workspace-path>] [--model <model-id>] \
                     [--effort <default|none|minimal|low|medium|high|xhigh|max>] <prompt> \
                     | auth <login|logout>]";

enum Command {
    Serve,
    Run {
        dir: Option<PathBuf>,
        model: Option<String>,
        effort: EffortLevel,
        prompt: String,
    },
    Login,
    Logout,
    Help,
}

fn command(args: impl Iterator<Item = String>) -> io::Result<Command> {
    let args = args.collect::<Vec<_>>();
    match args.as_slice() {
        [] => Ok(Command::Serve),
        [arg] if arg == "--help" || arg == "-h" => Ok(Command::Help),
        [run, rest @ ..] if run == "run" => run_command(rest),
        [auth, action] if auth == "auth" && action == "login" => Ok(Command::Login),
        [auth, action] if auth == "auth" && action == "logout" => Ok(Command::Logout),
        _ => Err(usage_error()),
    }
}

fn run_command(args: &[String]) -> io::Result<Command> {
    let mut dir = None;
    let mut model = None;
    let mut effort = None;
    let mut prompt = None;
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--dir" | "--model" | "--effort" => {
                let value = args.next().ok_or_else(usage_error)?;
                match arg.as_str() {
                    "--dir" => dir = Some(PathBuf::from(value)),
                    "--model" => model = Some(value.clone()),
                    _ => {
                        effort = Some(EffortLevel::from_id(value).ok_or_else(|| {
                            invalid_input(format!(
                                "{value} is not an effort level; choose one of {}",
                                EffortLevel::ALL.map(EffortLevel::id).join(", ")
                            ))
                        })?);
                    }
                }
            }
            _ if prompt.is_none() && !arg.trim().is_empty() => prompt = Some(arg.clone()),
            _ => return Err(usage_error()),
        }
    }
    Ok(Command::Run {
        dir,
        model,
        effort: effort.unwrap_or(EffortLevel::Default),
        prompt: prompt.ok_or_else(usage_error)?,
    })
}

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

fn usage_error() -> io::Error {
    invalid_input(format!("usage: {USAGE}"))
}

fn invalid_input(message: String) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

fn print_help() {
    println!("Usage: {USAGE}\n\nRun without arguments to start the ACP agent.");
}

fn absolute_dir(dir: Option<&Path>) -> io::Result<PathBuf> {
    let cwd = env::current_dir()?;
    let dir = match dir {
        Some(dir) if dir.is_absolute() => dir.to_path_buf(),
        Some(dir) => cwd.join(dir),
        None => cwd,
    };
    dir.canonicalize()
}

/// Fetches and installs the model catalog and returns the settings checked
/// against it.
async fn load_settings_and_catalog() -> io::Result<settings::Settings> {
    let catalog = openrouter::fetch_catalog().await?;
    let settings = settings::load(&catalog)?;
    openrouter::install_catalog(catalog);
    Ok(settings)
}

#[tokio::main]
async fn main() -> ExitCode {
    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("ox: {error}");
            ExitCode::FAILURE
        }
    }
}

async fn run() -> Result<(), Box<dyn Error>> {
    match command(env::args().skip(1))? {
        Command::Serve => acp::serve_stdio(load_settings_and_catalog().await?).await?,
        Command::Run {
            dir,
            model,
            effort,
            prompt,
        } => {
            let dir = absolute_dir(dir.as_deref())?;
            let settings = load_settings_and_catalog().await?.for_workspace(&dir)?;
            let model = resolve_model(model, settings.default_model)?;
            check_effort(&model, effort)?;
            let answer =
                acp::run_headless(&dir, model, effort, prompt, settings.global_hooks).await?;
            println!("{answer}");
        }
        Command::Login => {
            let api_key = rpassword::prompt_password("OpenRouter API key: ")?;
            let api_key = api_key.trim();
            if api_key.is_empty() {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "OpenRouter API key cannot be empty",
                )
                .into());
            }
            openrouter::Client::new(api_key.to_owned()).verify().await?;
            auth::save_api_key(api_key)?;
            println!("OpenRouter API key saved.");
        }
        Command::Logout => {
            if auth::delete_api_key()? {
                println!("OpenRouter API key removed.");
            } else {
                println!("No saved OpenRouter API key.");
            }
            if env::var_os("OPENROUTER_API_KEY").is_some() {
                eprintln!("OPENROUTER_API_KEY is still set and will continue to be used.");
            }
        }
        Command::Help => print_help(),
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::fs;

    use super::*;
    use crate::tools::fixture::Workspace;

    #[test]
    fn parses_auth_commands() {
        assert!(matches!(
            command(["auth".to_owned(), "login".to_owned()].into_iter()).unwrap(),
            Command::Login
        ));
        assert!(matches!(
            command(["auth".to_owned(), "logout".to_owned()].into_iter()).unwrap(),
            Command::Logout
        ));
    }

    fn run(args: &[&str]) -> io::Result<Command> {
        command(args.iter().map(|arg| (*arg).to_owned()))
    }

    #[test]
    fn parses_run_commands() {
        let Command::Run {
            dir,
            model,
            effort,
            prompt,
        } = run(&["run", "Hello"]).unwrap()
        else {
            panic!("expected run command");
        };
        assert_eq!(dir, None);
        assert_eq!(model, None);
        let settings = settings::Settings {
            default_model: openrouter::fixture::DEFAULT_MODEL.to_owned(),
            global_hooks: None,
        };
        let workspace = Workspace::new();
        let workspace_model = || {
            resolve_model(
                None,
                settings.for_workspace(&workspace.0).unwrap().default_model,
            )
        };
        assert_eq!(
            workspace_model().unwrap(),
            openrouter::fixture::DEFAULT_MODEL
        );
        let chosen = openrouter::catalog()[1].id.as_str();
        fs::create_dir(workspace.0.join(".ox")).unwrap();
        fs::write(
            workspace.0.join(".ox/settings.json"),
            format!(r#"{{"model":"{chosen}"}}"#),
        )
        .unwrap();
        assert_eq!(workspace_model().unwrap(), chosen);
        assert_eq!(effort, EffortLevel::Default);
        assert_eq!(prompt, "Hello");

        let Command::Run {
            dir,
            model,
            effort,
            prompt,
        } = run(&[
            "run",
            "--dir",
            "workspace",
            "Fix it",
            "--model",
            chosen,
            "--effort",
            "xhigh",
        ])
        .unwrap()
        else {
            panic!("expected run command");
        };
        assert_eq!(dir.as_deref(), Some(Path::new("workspace")));
        assert_eq!(
            resolve_model(model, openrouter::fixture::DEFAULT_MODEL.to_owned()).unwrap(),
            chosen
        );
        assert_eq!(effort, EffortLevel::XHigh);
        check_effort(chosen, effort).unwrap();
        assert_eq!(prompt, "Fix it");
    }

    #[test]
    fn rejects_unknown_commands() {
        assert!(run(&["login"]).is_err());
        assert!(run(&["run"]).is_err());
        assert!(run(&["run", ""]).is_err());
        assert!(run(&["run", "--dir"]).is_err());
        assert!(run(&["run", "Hello", "again"]).is_err());
        assert!(
            resolve_model(Some("retired/model".to_owned()), String::new())
                .unwrap_err()
                .to_string()
                .contains("not a model")
        );
        assert!(error(&["run", "--effort", "huge", "Hello"]).contains("not an effort level"));
        assert!(
            check_effort(openrouter::fixture::DEFAULT_MODEL, EffortLevel::XHigh)
                .unwrap_err()
                .to_string()
                .contains("choose one of default, low, medium, high, max")
        );
    }

    fn error(args: &[&str]) -> String {
        match run(args) {
            Ok(_) => panic!("expected an error"),
            Err(error) => error.to_string(),
        }
    }
}
