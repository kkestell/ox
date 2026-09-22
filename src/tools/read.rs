use std::path::Path;

use serde::Deserialize;
use tokio::io::{AsyncBufReadExt, BufReader};

use super::{BODY_LIMIT, truncate, workspace_path_allowing_link_target};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Args {
    path: String,
    #[serde(default = "default_offset")]
    offset: u64,
    #[serde(default = "default_limit")]
    limit: usize,
}

fn default_offset() -> u64 {
    1
}
fn default_limit() -> usize {
    200
}

pub(super) async fn execute(root: &Path, arguments: &str) -> Result<String, String> {
    let args: Args = serde_json::from_str(arguments).map_err(|e| format!("arguments: {e}"))?;
    if args.offset == 0 || !(1..=1000).contains(&args.limit) {
        return Err("offset must be at least 1 and limit must be between 1 and 1000".to_owned());
    }
    let path = workspace_path_allowing_link_target(root, &args.path).await?;
    if !tokio::fs::metadata(&path)
        .await
        .map_err(|e| e.to_string())?
        .is_file()
    {
        return Err("path must name a regular file".to_owned());
    }
    let file = tokio::fs::File::open(path)
        .await
        .map_err(|e| e.to_string())?;
    let mut reader = BufReader::new(file);
    for _ in 1..args.offset {
        if line(&mut reader, 0).await?.is_none() {
            return Ok("Offset is past end of file.".to_owned());
        }
    }
    let mut output = String::new();
    let mut next = args.offset;
    let mut deferred = false;
    let mut preview = false;
    for _ in 0..args.limit {
        let Some((text, omitted)) = line(&mut reader, BODY_LIMIT + 2).await? else {
            break;
        };
        let mut numbered = format!("{next}: {text}");
        if !output.is_empty() && (omitted || output.len() + numbered.len() + 1 > BODY_LIMIT) {
            deferred = true;
            break;
        }
        if omitted || numbered.len() + 1 > BODY_LIMIT {
            truncate(&mut numbered, BODY_LIMIT - 1);
            preview = true;
        }
        output.push_str(&numbered);
        output.push('\n');
        next = next.checked_add(1).expect("file line number fits in u64");
        if preview {
            break;
        }
    }
    if output.is_empty() {
        return Ok(if args.offset == 1 {
            "File is empty."
        } else {
            "Offset is past end of file."
        }
        .to_owned());
    }
    if preview {
        output.push_str(
            "[Line truncated. The omitted portion cannot be retrieved through line pagination.]\n",
        );
    }
    if deferred
        || !reader
            .fill_buf()
            .await
            .map_err(|e| e.to_string())?
            .is_empty()
    {
        output.push_str(&format!(
            "More content remains. Continue with offset={next}.\n"
        ));
    }
    Ok(output)
}

// Validate the entire line but retain only its prefix, even for enormous lines.
// The small unfinished UTF-8 suffix is carried across reader buffer boundaries.
async fn line(
    reader: &mut BufReader<tokio::fs::File>,
    keep: usize,
) -> Result<Option<(String, bool)>, String> {
    let mut prefix = Vec::new();
    let mut utf8 = Vec::new();
    let mut length = 0usize;
    let mut newline = false;
    loop {
        let buffer = reader.fill_buf().await.map_err(|e| e.to_string())?;
        if buffer.is_empty() {
            break;
        }
        let count = match buffer.iter().position(|&b| b == b'\n') {
            Some(index) => {
                newline = true;
                index + 1
            }
            None => buffer.len(),
        };
        let chunk = &buffer[..count];
        if chunk.contains(&0) {
            return Err("unsupported text file: contains NUL bytes".to_owned());
        }
        utf8.extend_from_slice(chunk);
        let valid = match std::str::from_utf8(&utf8) {
            Ok(_) => utf8.len(),
            Err(error) if error.error_len().is_none() => error.valid_up_to(),
            Err(_) => return Err("unsupported text file: invalid UTF-8".to_owned()),
        };
        utf8.drain(..valid);
        prefix.extend_from_slice(&chunk[..chunk.len().min(keep - prefix.len())]);
        length = length.saturating_add(count);
        reader.consume(count);
        if newline {
            break;
        }
    }
    if !utf8.is_empty() {
        return Err("unsupported text file: incomplete UTF-8 character".to_owned());
    }
    if length == 0 {
        return Ok(None);
    }
    let omitted = length > prefix.len();
    if !omitted && newline {
        prefix.pop();
        if prefix.last() == Some(&b'\r') {
            prefix.pop();
        }
    }
    let end = match std::str::from_utf8(&prefix) {
        Ok(_) => prefix.len(),
        Err(error) => error.valid_up_to(),
    };
    prefix.truncate(end);
    Ok(Some((
        String::from_utf8(prefix).expect("validated UTF-8 prefix"),
        omitted,
    )))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::{OUTPUT_LIMIT, fixture::Workspace};
    use serde_json::json;

    async fn read(workspace: &Workspace, offset: u64, limit: usize) -> Result<String, String> {
        execute(
            &workspace.0,
            &json!({"path":"text", "offset":offset, "limit":limit}).to_string(),
        )
        .await
    }

    #[tokio::test]
    async fn pages_preserve_lines_and_report_eof() {
        let workspace = Workspace::new();
        std::fs::write(workspace.0.join("text"), "one\r\n雪\nlast").unwrap();
        assert_eq!(
            read(&workspace, 1, 2).await.unwrap(),
            "1: one\n2: 雪\nMore content remains. Continue with offset=3.\n"
        );
        assert_eq!(read(&workspace, 3, 2).await.unwrap(), "3: last\n");
        assert_eq!(
            read(&workspace, 4, 2).await.unwrap(),
            "Offset is past end of file."
        );
        std::fs::write(workspace.0.join("text"), "").unwrap();
        assert_eq!(read(&workspace, 1, 2).await.unwrap(), "File is empty.");
        std::fs::write(workspace.0.join("text"), "\n").unwrap();
        assert_eq!(read(&workspace, 1, 2).await.unwrap(), "1: \n");
    }

    #[tokio::test]
    async fn byte_budget_defers_whole_lines_and_previews_oversized_lines() {
        let workspace = Workspace::new();
        let text = "x".repeat(BODY_LIMIT - 4);
        std::fs::write(workspace.0.join("text"), format!("{text}\nend\n")).unwrap();
        let first = read(&workspace, 1, 200).await.unwrap();
        assert!(first.starts_with(&format!("1: {text}\n")));
        assert!(first.ends_with("offset=2.\n"));
        assert!(!first.contains("truncated"));
        assert!(first.len() <= OUTPUT_LIMIT);
        assert_eq!(read(&workspace, 2, 200).await.unwrap(), "2: end\n");

        std::fs::write(
            workspace.0.join("text"),
            format!("small\n{}\nend", "雪".repeat(20_000)),
        )
        .unwrap();
        assert!(
            read(&workspace, 1, 200)
                .await
                .unwrap()
                .ends_with("offset=2.\n")
        );
        let preview = read(&workspace, 2, 200).await.unwrap();
        assert!(preview.contains("Line truncated"));
        assert!(preview.ends_with("offset=3.\n"));
        assert!(!preview.contains('\u{fffd}'));
        assert!(preview.len() <= OUTPUT_LIMIT);
        assert_eq!(read(&workspace, 3, 200).await.unwrap(), "3: end\n");
    }

    #[tokio::test]
    async fn validates_skipped_and_omitted_content_and_handles_split_unicode() {
        let workspace = Workspace::new();
        for bad in [vec![0], vec![255], vec![0xe2, 0x82]] {
            let mut contents = vec![b'x'; BODY_LIMIT * 2];
            contents.extend(bad);
            contents.extend_from_slice(b"\nnext");
            std::fs::write(workspace.0.join("text"), contents).unwrap();
            for offset in [1, 2] {
                assert!(
                    read(&workspace, offset, 200)
                        .await
                        .unwrap_err()
                        .contains("unsupported text file")
                );
            }
        }
        let contents = format!("{}雪\n", "a".repeat(8191));
        std::fs::write(workspace.0.join("text"), &contents).unwrap();
        assert_eq!(
            read(&workspace, 1, 200).await.unwrap(),
            format!("1: {contents}")
        );
    }
}
