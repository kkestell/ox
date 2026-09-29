//! Bounded UTF-8 reads for workspace instructions, skill definitions, and
//! settings.

use std::{
    fs::File,
    io::{self, ErrorKind, Read},
    path::Path,
};

pub const MAX_BYTES: u64 = 32 * 1024;

/// Reads a UTF-8 text file of at most 32 KiB.
pub fn read_bounded(path: &Path) -> io::Result<String> {
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
