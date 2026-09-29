mod acp;
mod config;
mod tui;

use std::path::{Path, PathBuf};

use anyhow::{Context, anyhow};
use clap::{Parser, Subcommand};
use ox_server::EffortLevel;

#[derive(Parser)]
#[command(
    version,
    about = "A coding agent and its terminal ACP client",
    args_conflicts_with_subcommands = true
)]
struct Args {
    #[command(subcommand)]
    command: Option<Command>,
    #[arg(long)]
    server: Option<String>,
    #[arg(long, default_value = ".")]
    dir: PathBuf,
}

#[derive(Subcommand)]
enum Command {
    /// Run one prompt and print the final answer.
    Run {
        #[arg(long)]
        dir: Option<PathBuf>,
        #[arg(long)]
        model: Option<String>,
        #[arg(long, default_value = "default", value_parser = effort)]
        effort: EffortLevel,
        #[arg(value_parser = prompt)]
        prompt: String,
    },
    /// Serve the Ox server over stdin and stdout.
    Acp {
        #[command(subcommand)]
        command: Option<AcpCommand>,
    },
    /// Save or remove the OpenRouter API key.
    #[command(subcommand)]
    Auth(Auth),
}

/// ACP terminal authentication appends `auth login` to the server's command.
#[derive(Subcommand)]
enum AcpCommand {
    /// Save or remove the OpenRouter API key.
    #[command(subcommand)]
    Auth(Auth),
}

#[derive(Clone, Copy, Subcommand)]
enum Auth {
    /// Save an OpenRouter API key in the system keyring.
    Login,
    /// Remove the saved OpenRouter API key.
    Logout,
}

fn effort(value: &str) -> Result<EffortLevel, String> {
    EffortLevel::from_id(value).ok_or_else(|| {
        format!(
            "{value} is not an effort level; choose one of {}",
            EffortLevel::ALL.map(EffortLevel::id).join(", ")
        )
    })
}

fn prompt(value: &str) -> Result<String, String> {
    if value.trim().is_empty() {
        return Err("the prompt is blank".to_owned());
    }
    Ok(value.to_owned())
}

fn workspace(path: &Path) -> anyhow::Result<PathBuf> {
    let path = path
        .canonicalize()
        .with_context(|| format!("opening {}", path.display()))?;
    anyhow::ensure!(path.is_dir(), "{} is not a directory", path.display());
    Ok(path)
}

#[tokio::main]
async fn main() {
    if let Err(error) = start().await {
        eprintln!(
            "ox: {}",
            tui::escape_control_characters(&format!("{error:#}"))
        );
        std::process::exit(1);
    }
}

async fn start() -> anyhow::Result<()> {
    let args = Args::parse();
    match args.command {
        None => client(&args.dir, args.server.as_deref()).await,
        Some(Command::Run {
            dir,
            model,
            effort,
            prompt,
        }) => {
            let answer = ox_server::run(dir.as_deref(), model, effort, prompt)
                .await
                .map_err(|error| anyhow!("{error}"))?;
            println!("{answer}");
            Ok(())
        }
        Some(Command::Acp { command: None }) => {
            ox_server::serve().await.map_err(|error| anyhow!("{error}"))
        }
        Some(
            Command::Auth(auth)
            | Command::Acp {
                command: Some(AcpCommand::Auth(auth)),
            },
        ) => match auth {
            Auth::Login => ox_server::login().await.map_err(|error| anyhow!("{error}")),
            Auth::Logout => Ok(ox_server::logout()?),
        },
    }
}

async fn client(directory: &Path, server: Option<&str>) -> anyhow::Result<()> {
    let directory = workspace(directory)?;
    let config_path = config::path()?;
    let config = config::Config::read(&config_path)?;
    let server = config.select(server)?;
    acp::start(server, directory, config.favorites.clone(), config_path).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_commands() {
        for args in [
            &["ox"][..],
            &["ox", "--dir", "d"],
            &["ox", "--server", "X"],
            &["ox", "acp"],
            &["ox", "acp", "auth", "login"],
            &["ox", "auth", "login"],
            &["ox", "auth", "logout"],
        ] {
            assert!(Args::try_parse_from(args).is_ok(), "{args:?}");
        }
        for (args, expected) in [
            (
                &["ox", "run", "Hello"][..],
                (None, None, EffortLevel::Default, "Hello"),
            ),
            (
                &[
                    "ox", "run", "--dir", "w", "Fix", "--model", "m", "--effort", "xhigh",
                ],
                (Some("w"), Some("m"), EffortLevel::XHigh, "Fix"),
            ),
        ] {
            let Some(Command::Run {
                dir,
                model,
                effort,
                prompt,
            }) = Args::try_parse_from(args).unwrap().command
            else {
                panic!("{args:?} is not a run");
            };
            assert_eq!(
                (dir.as_deref(), model.as_deref(), effort, prompt.as_str()),
                (
                    expected.0.map(Path::new),
                    expected.1,
                    expected.2,
                    expected.3
                ),
                "{args:?}"
            );
        }
        for (args, error) in [
            (&["ox", "login"][..], ""),
            (&["ox", "run"], ""),
            (&["ox", "run", ""], "blank"),
            (&["ox", "run", "--dir"], ""),
            (&["ox", "run", "Hello", "again"], ""),
            (
                &["ox", "run", "--effort", "huge", "Hello"],
                "not an effort level",
            ),
        ] {
            let Err(message) = Args::try_parse_from(args) else {
                panic!("{args:?} parsed");
            };
            assert!(message.to_string().contains(error), "{args:?}: {message}");
        }
    }

    #[test]
    fn requires_an_existing_directory() {
        let temp = tempfile::tempdir().unwrap();
        assert!(workspace(temp.path()).is_ok());
        assert!(workspace(&temp.path().join("missing")).is_err());
        let file = temp.path().join("file");
        std::fs::write(&file, "").unwrap();
        assert!(workspace(&file).is_err());
    }
}
