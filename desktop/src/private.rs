use std::{
    fs, io,
    os::unix::fs::{DirBuilderExt, MetadataExt, PermissionsExt},
    path::{Path, PathBuf},
};

pub fn directory(path: &Path) -> Result<PathBuf, String> {
    ensure(path, true)
}

pub fn registry(path: &Path) -> Result<PathBuf, String> {
    ensure(path, false)
}

pub fn runtime_directory() -> Result<PathBuf, String> {
    let path = std::env::var_os("STACKHARBOR_DESKTOP_RUNTIME_DIR")
        .map(PathBuf::from)
        .unwrap_or(crate::protocol::cache_dir()?.join("stackharbor/desktop-run"));
    directory(&path)
}

fn ensure(path: &Path, strict: bool) -> Result<PathBuf, String> {
    if !path.is_absolute() {
        return Err("Private directory must be absolute".into());
    }
    if let Err(e) = fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(path)
    {
        if e.kind() != io::ErrorKind::AlreadyExists {
            return Err(e.to_string());
        }
    }
    let st = fs::symlink_metadata(path).map_err(|e| e.to_string())?;
    // SAFETY: geteuid has no preconditions and reads only the process identity.
    if !st.is_dir()
        || st.uid() != unsafe { libc::geteuid() }
        || if strict {
            st.permissions().mode() & 0o777 != 0o700
        } else {
            st.permissions().mode() & 0o022 != 0
        }
    {
        return Err(format!("Unsafe private directory: {}", path.display()));
    }
    let real = fs::canonicalize(path).map_err(|e| e.to_string())?;
    if strict && real != path {
        return Err("Private directory must not contain symlinks".into());
    }
    Ok(real)
}
