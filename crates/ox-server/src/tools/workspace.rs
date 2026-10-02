//! Opens files relative to an owned workspace directory. File operations walk
//! parent directories through descriptors without following links, so a link
//! swapped into a validated path cannot redirect a read or write.

use std::{
    ffi::OsString,
    fs::File,
    io::{self, Write},
    path::{Component, Path, PathBuf},
};

use rustix::fs::{self, AtFlags, Mode, OFlags, RenameFlags};

pub(super) struct Workspace {
    /// The workspace path as Ox was given it.
    given: PathBuf,
    root: PathBuf,
    dir: File,
}

impl Workspace {
    pub fn open(path: &Path) -> io::Result<Self> {
        let root = path.canonicalize()?;
        let dir = fs::openat(
            fs::CWD,
            &root,
            OFlags::RDONLY | OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
            Mode::empty(),
        )?;
        Ok(Self {
            given: path.to_path_buf(),
            root,
            dir: File::from(dir),
        })
    }

    /// `name` relative to the workspace when it is an absolute path inside
    /// the workspace, under the path Ox was given or its canonical form, and
    /// otherwise `name` itself. The workspace itself becomes `.`.
    pub fn relative_name<'a>(&self, name: &'a Path) -> &'a Path {
        match [&self.given, &self.root]
            .into_iter()
            .find_map(|root| name.strip_prefix(root).ok())
        {
            Some(relative) if relative.as_os_str().is_empty() => Path::new("."),
            Some(relative) => relative,
            None => name,
        }
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    pub fn relative(&self, path: &Path) -> io::Result<PathBuf> {
        path.strip_prefix(&self.root)
            .map(Path::to_path_buf)
            .map_err(|_| {
                io::Error::new(
                    io::ErrorKind::PermissionDenied,
                    "path resolves outside the workspace",
                )
            })
    }

    /// Resolves a directly named link, then uses only its in-workspace target.
    pub fn resolve_allowing_link_target(&self, name: &Path) -> io::Result<PathBuf> {
        if name.as_os_str().is_empty() || !is_relative_path(name) {
            return Err(invalid_relative_path());
        }
        self.relative(&self.root.join(name).canonicalize()?)
    }

    fn parent(&self, path: &Path, create: bool) -> io::Result<(File, OsString)> {
        let mut parts = path.components().peekable();
        let mut dir = self.dir.try_clone()?;
        while let Some(part) = parts.next() {
            let Component::Normal(name) = part else {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "invalid workspace path",
                ));
            };
            if parts.peek().is_none() {
                return Ok((dir, name.to_os_string()));
            }
            let flags = OFlags::RDONLY | OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC;
            let next = match fs::openat(&dir, name, flags, Mode::empty()) {
                Ok(next) => next,
                Err(rustix::io::Errno::NOENT) if create => {
                    match fs::mkdirat(&dir, name, Mode::from_raw_mode(0o777)) {
                        Ok(()) | Err(rustix::io::Errno::EXIST) => {}
                        Err(error) => return Err(error.into()),
                    }
                    fs::openat(&dir, name, flags, Mode::empty())?
                }
                Err(error) => return Err(error.into()),
            };
            dir = File::from(next);
        }
        Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "path names the workspace root",
        ))
    }

    pub fn read_file(&self, path: &Path) -> io::Result<File> {
        let (dir, name) = self.parent(path, false)?;
        let file = File::from(fs::openat(
            &dir,
            &name,
            OFlags::RDONLY | OFlags::NOFOLLOW | OFlags::CLOEXEC | OFlags::NONBLOCK,
            Mode::empty(),
        )?);
        if !file.metadata()?.is_file() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "path must name a regular file",
            ));
        }
        Ok(file)
    }

    pub fn directory(&self, path: &Path) -> io::Result<File> {
        if path.as_os_str().is_empty() {
            return self.dir.try_clone();
        }
        let (dir, name) = self.parent(path, false)?;
        Ok(File::from(fs::openat(
            &dir,
            &name,
            OFlags::RDONLY | OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
            Mode::empty(),
        )?))
    }

    pub fn regular_file(&self, path: &Path) -> io::Result<bool> {
        let (dir, name) = self.parent(path, false)?;
        let stat = fs::statat(&dir, &name, AtFlags::SYMLINK_NOFOLLOW)?;
        Ok(fs::FileType::from_raw_mode(stat.st_mode).is_file())
    }

    pub fn write_file(&self, path: &Path, contents: &[u8], create: bool) -> io::Result<()> {
        let (dir, name) = self.parent(path, create)?;
        let mut flags = OFlags::WRONLY | OFlags::NOFOLLOW | OFlags::CLOEXEC | OFlags::NONBLOCK;
        if create {
            flags |= OFlags::CREATE | OFlags::EXCL;
        }
        let mut file = File::from(fs::openat(&dir, &name, flags, Mode::from_raw_mode(0o666))?);
        if !file.metadata()?.is_file() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "path must name a regular file",
            ));
        }
        file.set_len(0)?;
        file.write_all(contents)
    }

    pub fn remove_file(&self, path: &Path) -> io::Result<()> {
        let (dir, name) = self.parent(path, false)?;
        fs::unlinkat(&dir, &name, AtFlags::empty())?;
        Ok(())
    }

    pub fn move_file(&self, source: &Path, destination: &Path) -> io::Result<()> {
        let (source_dir, source_name) = self.parent(source, false)?;
        let (destination_dir, destination_name) = self.parent(destination, true)?;
        fs::renameat_with(
            &source_dir,
            &source_name,
            &destination_dir,
            &destination_name,
            RenameFlags::NOREPLACE,
        )?;
        Ok(())
    }
}

/// Whether every component is a normal directory name or `.`. An empty path
/// has no components and is relative.
fn is_relative_path(name: &Path) -> bool {
    name.components()
        .all(|part| matches!(part, Component::Normal(_) | Component::CurDir))
}

fn invalid_relative_path() -> io::Error {
    io::Error::new(
        io::ErrorKind::InvalidInput,
        "expected a path inside the workspace without parent traversal; use the shell tool for other paths",
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tools::fixture::Workspace as TempWorkspace;

    #[cfg(unix)]
    #[test]
    fn a_link_swapped_into_a_validated_target_cannot_redirect_a_write() {
        use std::os::unix::fs::symlink;
        let workspace = TempWorkspace::new();
        let outside = TempWorkspace::new();
        std::fs::write(workspace.0.join("inside"), "inside").unwrap();
        std::fs::write(outside.0.join("file"), "outside").unwrap();
        let pinned = Workspace::open(&workspace.0).unwrap();
        let path = Path::new("inside");
        pinned.read_file(path).unwrap();
        std::fs::remove_file(workspace.0.join("inside")).unwrap();
        symlink(outside.0.join("file"), workspace.0.join("inside")).unwrap();
        assert!(pinned.write_file(path, b"changed", false).is_err());
        assert_eq!(
            std::fs::read_to_string(outside.0.join("file")).unwrap(),
            "outside"
        );
    }

    #[cfg(unix)]
    #[test]
    fn a_link_swapped_into_a_validated_target_cannot_redirect_a_delete() {
        use std::os::unix::fs::symlink;
        let workspace = TempWorkspace::new();
        let outside = TempWorkspace::new();
        std::fs::write(workspace.0.join("inside"), "inside").unwrap();
        std::fs::write(outside.0.join("file"), "outside").unwrap();
        let pinned = Workspace::open(&workspace.0).unwrap();
        let path = Path::new("inside");
        pinned.read_file(path).unwrap();
        std::fs::remove_file(workspace.0.join("inside")).unwrap();
        symlink(outside.0.join("file"), workspace.0.join("inside")).unwrap();
        pinned.remove_file(path).unwrap();
        assert!(!workspace.0.join("inside").exists());
        assert_eq!(
            std::fs::read_to_string(outside.0.join("file")).unwrap(),
            "outside"
        );
    }

    #[cfg(unix)]
    #[test]
    fn a_link_swapped_into_a_validated_destination_cannot_redirect_a_move() {
        use std::os::unix::fs::symlink;
        let workspace = TempWorkspace::new();
        let outside = TempWorkspace::new();
        std::fs::write(workspace.0.join("source"), "inside").unwrap();
        std::fs::create_dir(workspace.0.join("destination")).unwrap();
        let pinned = Workspace::open(&workspace.0).unwrap();
        pinned.directory(Path::new("destination")).unwrap();
        std::fs::remove_dir(workspace.0.join("destination")).unwrap();
        symlink(&outside.0, workspace.0.join("destination")).unwrap();
        assert!(
            pinned
                .move_file(Path::new("source"), Path::new("destination/file"))
                .is_err()
        );
        assert_eq!(
            std::fs::read_to_string(workspace.0.join("source")).unwrap(),
            "inside"
        );
        assert!(!outside.0.join("file").exists());
    }

    #[test]
    fn exclusive_creation_does_not_overwrite_a_file_created_after_reading() {
        let workspace = TempWorkspace::new();
        let pinned = Workspace::open(&workspace.0).unwrap();
        let path = Path::new("file");
        assert_eq!(
            pinned.read_file(path).unwrap_err().kind(),
            io::ErrorKind::NotFound
        );
        std::fs::write(workspace.0.join("file"), "original").unwrap();
        assert!(pinned.write_file(path, b"changed", true).is_err());
        assert_eq!(
            std::fs::read_to_string(workspace.0.join("file")).unwrap(),
            "original"
        );
    }
}
