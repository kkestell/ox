mod acp;
mod config;
mod tui;

use std::path::{Path, PathBuf};

use anyhow::Context;
use clap::Parser;

#[derive(Parser)]
#[command(version, about = "An interactive ACP client")]
struct Args {
    #[arg(long)]
    server: Option<String>,
    #[arg(default_value = ".")]
    directory: PathBuf,
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
        eprintln!("ox: {}", tui::escape(&format!("{error:#}")));
        std::process::exit(1);
    }
}

async fn start() -> anyhow::Result<()> {
    let args = Args::parse();
    let directory = workspace(&args.directory)?;
    let config = match config::Config::read()? {
        Some(config) => config,
        None => config::Config::bundled_server()?,
    };
    let server = config.select(args.server.as_deref())?;
    acp::start(server, directory).await
}

#[cfg(test)]
mod tests {
    use super::*;

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
