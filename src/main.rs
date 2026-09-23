mod acp;
mod auth;
mod cancellation;
mod compaction;
mod hooks;
mod openrouter;
mod process;
mod sessions;
mod settings;
mod skills;
mod system_prompt;
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
                     [--effort <default|low|medium|high>] <prompt> | auth <login|logout>]";

enum Command {
    Serve,
    Run {
        dir: Option<PathBuf>,
        model: String,
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
                    "--model" => {
                        model = Some(openrouter::catalog_model(value).ok_or_else(|| {
                            invalid_input(format!(
                                "{value} is not a model; choose one of {}",
                                openrouter::MODEL_CATALOG
                                    .iter()
                                    .map(|model| model.id)
                                    .collect::<Vec<_>>()
                                    .join(", ")
                            ))
                        })?);
                    }
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
        model: model
            .map_or(openrouter::DEFAULT_MODEL, |model| model.id)
            .to_owned(),
        effort: effort.unwrap_or(EffortLevel::Default),
        prompt: prompt.ok_or_else(usage_error)?,
    })
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
        Command::Serve => acp::serve_stdio().await?,
        Command::Run {
            dir,
            model,
            effort,
            prompt,
        } => {
            let answer =
                acp::run_headless(&absolute_dir(dir.as_deref())?, model, effort, prompt).await?;
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
    use super::*;

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
        assert_eq!(model, openrouter::DEFAULT_MODEL);
        assert_eq!(effort, EffortLevel::Default);
        assert_eq!(prompt, "Hello");

        let chosen = openrouter::MODEL_CATALOG[1].id;
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
            "high",
        ])
        .unwrap()
        else {
            panic!("expected run command");
        };
        assert_eq!(dir.as_deref(), Some(Path::new("workspace")));
        assert_eq!(model, chosen);
        assert_eq!(effort, EffortLevel::High);
        assert_eq!(prompt, "Fix it");
    }

    #[test]
    fn rejects_unknown_commands() {
        assert!(run(&["login"]).is_err());
        assert!(run(&["run"]).is_err());
        assert!(run(&["run", ""]).is_err());
        assert!(run(&["run", "--dir"]).is_err());
        assert!(run(&["run", "Hello", "again"]).is_err());
        assert!(error(&["run", "--model", "retired/model", "Hello"]).contains("not a model"));
        assert!(error(&["run", "--effort", "max", "Hello"]).contains("not an effort level"));
    }

    fn error(args: &[&str]) -> String {
        match run(args) {
            Ok(_) => panic!("expected an error"),
            Err(error) => error.to_string(),
        }
    }
}
