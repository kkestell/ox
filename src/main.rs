mod acp;
mod auth;
mod openrouter;
mod sessions;
mod tools;

use std::{env, error::Error, io, process::ExitCode};

enum Command {
    Serve,
    Login,
    Logout,
    Help,
}

fn command(args: impl Iterator<Item = String>) -> io::Result<Command> {
    let args = args.collect::<Vec<_>>();
    match args.as_slice() {
        [] => Ok(Command::Serve),
        [arg] if arg == "--help" || arg == "-h" => Ok(Command::Help),
        [auth, action] if auth == "auth" && action == "login" => Ok(Command::Login),
        [auth, action] if auth == "auth" && action == "logout" => Ok(Command::Logout),
        _ => Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "usage: ox [auth <login|logout>]",
        )),
    }
}

fn print_help() {
    println!("Usage: ox [auth <login|logout>]\n\nRun without arguments to start the ACP agent.");
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
        Command::Serve => acp::run().await?,
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
    fn rejects_unknown_commands() {
        assert!(command(["login".to_owned()].into_iter()).is_err());
    }
}
