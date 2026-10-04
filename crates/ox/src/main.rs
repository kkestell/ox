mod acp;
mod config;
#[cfg(test)]
#[allow(dead_code)]
mod fixture;
mod tui;

use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};

use anyhow::Context;
use clap::{Parser, Subcommand};

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

/// The Ox server commands, which `ox-server` parses and runs.
#[derive(Subcommand)]
enum Command {
    /// Run one prompt and print the final answer.
    #[command(disable_help_flag = true)]
    Run {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Serve the Ox server over stdin and stdout.
    #[command(disable_help_flag = true)]
    Acp {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Sign in to or out of OpenRouter.
    #[command(disable_help_flag = true)]
    Auth {
        #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
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
    let (name, args) = match args.command {
        None => return client(&args.dir, args.server.as_deref()).await,
        Some(Command::Run { args }) => ("run", args),
        Some(Command::Acp { args }) => ("acp", args),
        Some(Command::Auth { args }) => ("auth", args),
    };
    let server = config::server_binary()?;
    let error = std::process::Command::new(&server)
        .arg(name)
        .args(args)
        .exec();
    Err(error).with_context(|| format!("running {}", server.display()))
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
    fn parses_client_options_and_passes_server_commands_through() {
        for args in [&["ox"][..], &["ox", "--dir", "d"], &["ox", "--server", "X"]] {
            assert!(Args::try_parse_from(args).is_ok(), "{args:?}");
        }
        for (args, expected) in [
            (&["ox", "acp"][..], ("acp", &[][..])),
            (
                &["ox", "run", "--dir", "w", "Fix", "--model", "m"],
                ("run", &["--dir", "w", "Fix", "--model", "m"]),
            ),
            (&["ox", "run", "-h"], ("run", &["-h"])),
            (
                &["ox", "acp", "auth", "login", "openrouter"],
                ("acp", &["auth", "login", "openrouter"]),
            ),
            (
                &["ox", "auth", "logout", "openrouter"],
                ("auth", &["logout", "openrouter"]),
            ),
        ] {
            let (name, args) = match Args::try_parse_from(args).unwrap().command {
                Some(Command::Run { args }) => ("run", args),
                Some(Command::Acp { args }) => ("acp", args),
                Some(Command::Auth { args }) => ("auth", args),
                None => panic!("{args:?} is not a server command"),
            };
            assert_eq!(name, expected.0);
            assert_eq!(args, expected.1);
        }
        assert!(Args::try_parse_from(["ox", "login"]).is_err());
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
