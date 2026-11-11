//! Prompt content built from local files.

use std::io;
use std::os::unix::ffi::OsStrExt;
use std::path::Path;

use agent_client_protocol::v1::schema::{ContentBlock, ResourceLink};

/// A resource link to a file, named by its file name. Every agent accepts
/// resource links.
pub fn file_link(path: &Path) -> io::Result<ContentBlock> {
    let path = path
        .canonicalize()
        .map_err(|error| io::Error::new(error.kind(), format!("{}: {error}", path.display())))?;
    let name = path
        .file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| path.display().to_string());
    Ok(ContentBlock::ResourceLink(ResourceLink::new(
        name,
        file_uri(&path),
    )))
}

/// A `file://` URI for an absolute path, percent-encoding every byte other
/// than unreserved characters and `/`.
fn file_uri(path: &Path) -> String {
    let mut uri = String::from("file://");
    for &byte in path.as_os_str().as_bytes() {
        if byte.is_ascii_alphanumeric() || b"-._~/".contains(&byte) {
            uri.push(byte as char);
        } else {
            uri.push_str(&format!("%{byte:02X}"));
        }
    }
    uri
}

#[cfg(test)]
mod tests {
    use std::ffi::OsStr;

    use super::*;

    #[test]
    fn file_uri_percent_encodes_reserved_and_non_ascii_bytes() {
        let path = Path::new(OsStr::from_bytes(b"/a b/r\xc3\xa9sum\xc3\xa9#1.md"));
        assert_eq!(file_uri(path), "file:///a%20b/r%C3%A9sum%C3%A9%231.md");
        assert_eq!(file_uri(Path::new("/x/A-z_0.~")), "file:///x/A-z_0.~");
    }

    #[test]
    fn file_link_names_the_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("notes.md");
        std::fs::write(&path, "").unwrap();
        let ContentBlock::ResourceLink(link) = file_link(&path).unwrap() else {
            panic!("not a resource link");
        };
        assert_eq!(link.name, "notes.md");
        assert_eq!(link.uri, file_uri(&path.canonicalize().unwrap()));
        assert!(file_link(&dir.path().join("missing")).is_err());
    }
}
