#[cfg(target_os = "macos")]
pub fn cli() -> Result<String, String> {
    use std::{fs, os::unix::fs::symlink};
    let source = std::env::current_exe()
        .map_err(|e| e.to_string())?
        .parent()
        .ok_or("Missing application bundle directory")?
        .join("stackharbor");
    let source = fs::canonicalize(source).map_err(|e| e.to_string())?;
    let dir = crate::protocol::home_dir()?.join(".local/bin");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let target = dir.join("stackharbor");
    // symlink is an atomic no-replace operation, including dangling symlinks.
    symlink(&source, &target).map_err(|e| format!("{} was not replaced: {e}", target.display()))?;
    Ok(format!("Installed {}\n\nAdd {} to your shell PATH if needed. Existing terminals may need to be reopened.", target.display(), dir.display()))
}
