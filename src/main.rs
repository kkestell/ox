mod acp;
mod auth;
mod openrouter;
mod sessions;
mod system_prompt;
mod tools;

use std::{
    env,
    error::Error,
    io,
    path::{Path, PathBuf},
    process::ExitCode,
};

enum Command {
    Serve,
    Run {
        dir: Option<PathBuf>,
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
        [run, prompt] if run == "run" && prompt != "--dir" && !prompt.trim().is_empty() => {
            Ok(Command::Run {
                dir: None,
                prompt: prompt.clone(),
            })
        }
        [run, flag, dir, prompt]
            if run == "run" && flag == "--dir" && !prompt.trim().is_empty() =>
        {
            Ok(Command::Run {
                dir: Some(PathBuf::from(dir)),
                prompt: prompt.clone(),
            })
        }
        [auth, action] if auth == "auth" && action == "login" => Ok(Command::Login),
        [auth, action] if auth == "auth" && action == "logout" => Ok(Command::Logout),
        _ => Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "usage: ox [run [--dir <workspace-path>] <prompt> | auth <login|logout>]",
        )),
    }
}

fn print_help() {
    println!(
        "Usage: ox [run [--dir <workspace-path>] <prompt> | auth <login|logout>]\n\n\
         Run without arguments to start the ACP agent."
    );
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
        Command::Run { dir, prompt } => {
            acp::run_headless(&absolute_dir(dir.as_deref())?, prompt).await?
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

    #[test]
    fn parses_run_commands() {
        let Command::Run { dir, prompt } =
            command(["run".to_owned(), "Hello".to_owned()].into_iter()).unwrap()
        else {
            panic!("expected run command");
        };
        assert_eq!(dir, None);
        assert_eq!(prompt, "Hello");

        let Command::Run { dir, prompt } = command(
            ["run", "--dir", "workspace", "Fix it"]
                .map(str::to_owned)
                .into_iter(),
        )
        .unwrap() else {
            panic!("expected run command");
        };
        assert_eq!(dir.as_deref(), Some(Path::new("workspace")));
        assert_eq!(prompt, "Fix it");
    }

    #[test]
    fn rejects_unknown_commands() {
        assert!(command(["login".to_owned()].into_iter()).is_err());
        assert!(command(["run".to_owned(), "".to_owned()].into_iter()).is_err());
    }
}
