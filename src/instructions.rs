//! Reads the workspace's standing model guidance from `AGENTS.md`.

use std::{
    fs::File,
    io::{self, ErrorKind, Read},
    path::Path,
};

const FILE_NAME: &str = "AGENTS.md";
const MAX_BYTES: u64 = 32 * 1024;

/// Reads `<workspace>/AGENTS.md` as text. `None` when the file is missing or
/// blank. A file that cannot be read, is not UTF-8, or exceeds the limit is an
/// error rather than a silently ignored file.
pub fn read(workspace_path: &Path) -> io::Result<Option<String>> {
    match read_text(&workspace_path.join(FILE_NAME)) {
        Ok(text) if text.trim().is_empty() => Ok(None),
        Ok(text) => Ok(Some(text)),
        Err(error) if error.kind() == ErrorKind::NotFound => Ok(None),
        Err(error) => Err(io::Error::new(
            error.kind(),
            format!("{FILE_NAME}: {error}"),
        )),
    }
}

/// Reads a UTF-8 text file of at most `MAX_BYTES`.
fn read_text(path: &Path) -> io::Result<String> {
    let mut bytes = Vec::new();
    File::open(path)?
        .take(MAX_BYTES + 1)
        .read_to_end(&mut bytes)?;
    if bytes.len() as u64 > MAX_BYTES {
        return Err(io::Error::new(
            ErrorKind::InvalidData,
            format!("larger than {} KiB", MAX_BYTES / 1024),
        ));
    }
    String::from_utf8(bytes).map_err(|_| io::Error::new(ErrorKind::InvalidData, "not UTF-8 text"))
}

#[cfg(test)]
mod tests {
    use std::fs;

    use super::*;
    use crate::tools::fixture::Workspace;

    #[test]
    fn reads_bounded_utf8_text_and_treats_blank_as_missing() {
        let workspace = Workspace::new();
        let path = workspace.0.join(FILE_NAME);
        let limit = usize::try_from(MAX_BYTES).unwrap();
        let full = "a".repeat(limit);
        let cases = [
            (None, Ok(None)),
            (
                Some(b"Answer in French.\n".to_vec()),
                Ok(Some("Answer in French.\n")),
            ),
            (Some(b" \n\t\n".to_vec()), Ok(None)),
            (Some(full.clone().into_bytes()), Ok(Some(full.as_str()))),
            (Some(vec![0xff, 0xfe]), Err("AGENTS.md: not UTF-8 text")),
            (
                Some(vec![b'a'; limit + 1]),
                Err("AGENTS.md: larger than 32 KiB"),
            ),
        ];
        for (contents, expected) in cases {
            match &contents {
                Some(bytes) => fs::write(&path, bytes).unwrap(),
                None => assert!(!path.exists()),
            }
            let result = read(&workspace.0);
            match expected {
                Ok(text) => assert_eq!(result.unwrap().as_deref(), text),
                Err(message) => assert_eq!(result.unwrap_err().to_string(), message),
            }
        }
    }
}
