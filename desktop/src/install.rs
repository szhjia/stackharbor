//! Resumable, per-user integration. The manifest is written before each mutation;
//! removal checks the exact recorded bytes/target before touching an artifact.
use crate::protocol::BUILD_VERSION;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::{BTreeMap, HashMap},
    fs,
    io::{Read, Write},
    os::{
        fd::AsRawFd,
        unix::fs::{symlink, MetadataExt, PermissionsExt},
    },
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{Mutex, OnceLock},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
pub const BUNDLE_ID: &str = "com.xahacktech.stackharbor";
pub const AGENT_LABEL: &str = "com.xahacktech.stackharbor.engine";
const TEAM: &str = "28CG45X4J3";
const PUBLISHER: &str = "Xi'an HengKe InfoTech Co.,Ltd";
const AGREEMENT_VERSION: &str = "2026-11-14";
const PATH_BLOCK: &str = "# >>> StackHarbor managed PATH >>>\ncase \":$PATH:\" in\n  *\":$HOME/.local/bin:\"*) ;;\n  *) export PATH=\"$HOME/.local/bin:$PATH\" ;;\nesac\n# <<< StackHarbor managed PATH <<<\n";
static INSTALL_LOCK: Mutex<()> = Mutex::new(());
#[derive(Clone, Serialize)]
pub struct Agreement {
    version: &'static str,
    hash: String,
    text: String,
    publisher: &'static str,
}
#[derive(Clone, Serialize, Deserialize, Default)]
pub struct AgreementState {
    version: String,
    hash: String,
    accepted: bool,
}
#[derive(Serialize)]
pub struct ServiceState {
    enabled: bool,
    status: String,
}
#[derive(Serialize)]
pub struct BackgroundState {
    status: String,
}
#[derive(Clone, Serialize)]
pub struct Conflict {
    path: String,
    reason: String,
}
#[derive(Serialize)]
pub struct SetupStatus {
    complete: bool,
    version: &'static str,
    bundle_id: &'static str,
    installed_path: String,
    cli_path: String,
    skill_path: String,
    path_configured: bool,
    agreement: AgreementState,
    autostart: ServiceState,
    background: BackgroundState,
    conflicts: Vec<Conflict>,
    issues: Vec<String>,
}
#[derive(Clone, Default, Serialize, Deserialize)]
struct Artifact {
    path: PathBuf,
    kind: String,
    fingerprint: String,
    #[serde(default)]
    previous_fingerprint: Option<String>,
    #[serde(default)]
    backup: Option<PathBuf>,
    #[serde(default)]
    applied: bool,
}
#[derive(Default, Serialize, Deserialize)]
struct Manifest {
    #[serde(default)]
    installed_version: String,
    #[serde(default)]
    accepted_at: u64,
    #[serde(default)]
    agreement: AgreementState,
    #[serde(default)]
    artifacts: BTreeMap<String, Artifact>,
    #[serde(default)]
    current: Option<PathBuf>,
    #[serde(default)]
    previous: Option<PathBuf>,
    #[serde(default)]
    complete: bool,
}
#[derive(Clone)]
pub struct Layout {
    pub home: PathBuf,
    pub bundle: PathBuf,
    pub resources: PathBuf,
    pub state: PathBuf,
}
impl Layout {
    pub fn current() -> Result<Self, String> {
        let home = crate::protocol::home_dir()?;
        let executable = std::env::current_exe().map_err(|e| e.to_string())?;
        let bundle = executable
            .parent()
            .and_then(Path::parent)
            .and_then(Path::parent)
            .ok_or("Invalid application bundle")?
            .to_path_buf();
        Ok(Self {
            state: home.join("Library/Application Support").join(BUNDLE_ID),
            resources: bundle.join("Contents/Resources"),
            home,
            bundle,
        })
    }
    pub fn cli(&self) -> PathBuf {
        self.home.join(".local/bin/stackharbor")
    }
    pub fn skill(&self) -> PathBuf {
        self.home.join(".agents/skills/stackharbor")
    }
    pub fn bundled_binary(&self) -> PathBuf {
        self.bundle.join("Contents/MacOS/stackharbor")
    }
    pub fn installed(&self) -> bool {
        let parent = self.bundle.parent();
        (parent == Some(Path::new("/Applications"))
            || parent == Some(self.home.join("Applications").as_path()))
            && self.bundle.extension().is_some_and(|s| s == "app")
            && fs::canonicalize(&self.bundle).ok().as_ref() == Some(&self.bundle)
    }
    fn manifest(&self) -> Result<Manifest, String> {
        if let Ok(st) = fs::symlink_metadata(&self.state) {
            if !st.is_dir()
                || st.uid() != unsafe { libc::geteuid() }
                || st.mode() & 0o777 != 0o700
                || fs::canonicalize(&self.state).map_err(|e| e.to_string())? != self.state
            {
                return Err("Unsafe installation state directory".into());
            }
        }
        let path = self.state.join("install.json");
        match fs::symlink_metadata(&path) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Manifest::default()),
            Err(e) => Err(e.to_string()),
            Ok(st)
                if st.is_file()
                    && st.uid() == unsafe { libc::geteuid() }
                    && st.mode() & 0o077 == 0 =>
            {
                if st.len() > 2 * 1024 * 1024 {
                    return Err("Oversized install manifest".into());
                }
                serde_json::from_slice(&fs::read(path).map_err(|e| e.to_string())?)
                    .map_err(|e| e.to_string())
            }
            _ => Err("Unsafe installation manifest".into()),
        }
    }
    fn save(&self, manifest: &Manifest) -> Result<(), String> {
        crate::private::directory(&self.state)?;
        atomic_write(
            &self.state.join("install.json"),
            &serde_json::to_vec_pretty(manifest).map_err(|e| e.to_string())?,
            0o600,
        )
    }
}
fn hash(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}
pub fn agreement() -> Agreement {
    let text = format!(
        r#"StackHarbor Desktop — Local Installation Agreement ({AGREEMENT_VERSION})

Publisher: {PUBLISHER} (Developer ID team {TEAM}).

StackHarbor is a local development tool. This installation is for the current macOS user only. It does not install a privileged daemon, collect telemetry, upload project files or send command output to the publisher. Projects may invoke their own network services, containers and commands according to your workspace YAML and explicit actions.

By accepting and selecting Install, you authorize StackHarbor to install the bundled command line program at ~/.local/bin/stackharbor and the complete bundled agent skill at ~/.agents/skills/stackharbor. When ~/.codex or ~/.claude already exists, a compatibility link is added in its skills directory. These integrations are required. Existing unowned entries are preserved unless you explicitly select backed-up migration; backups remain in your local Application Support directory.

The application registers an unprivileged, on-demand background engine through macOS ServiceManagement ({AGENT_LABEL}). The engine starts when a client needs it, and project services start only after your explicit start action. Closing a window, terminal or the application does not stop running projects. Login and reboot do not replay project starts. Use explicit workspace stop/close controls to stop services. macOS may require approval in System Settings > General > Login Items & Extensions.

Open at login is optional and defaults off. It starts the application, not project services. You can change it in Settings. The separate background engine authorization is required for the desktop installation.

Optional shell PATH configuration appends this reversible managed block to ~/.zprofile (zsh) and ~/.bash_profile (bash):

{PATH_BLOCK}
Only this exact block is removed during uninstall; unrelated shell content is preserved. New terminal sessions may be needed.

StackHarbor stores an installation manifest, agreement version/hash/time, backed-up conflicts and verified versioned command line binaries under ~/Library/Application Support/{BUNDLE_ID}. A cached prior client is retained so that running sessions remain manageable after replacing the app. The engine stores local workspace and recovery records. The application does not automatically restart uncertain sessions.

Uninstall integrations requires an idle engine. It unregisters the background agent and optional login item and removes only unchanged artifacts recorded in the manifest. Modified entries remain for you to review. Project YAML, project files and local user history are preserved. Moving the application to Trash does not run an uninstaller.

License: StackHarbor remains distributed under the MIT License. Copyright (c) 2026 StackHarbor contributors. Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the “Software”), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions: The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software. THE SOFTWARE IS PROVIDED “AS IS”, WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

The original MIT license and all bundled third-party notices are available in the application’s Resources/licenses directory. This agreement does not reduce your open-source license rights.
"#
    );
    Agreement {
        version: AGREEMENT_VERSION,
        hash: hash(text.as_bytes()),
        text,
        publisher: PUBLISHER,
    }
}

extern "C" {
    fn stackharbor_service(operation: i32) -> *mut libc::c_char;
}
pub fn service(operation: i32) -> Result<String, String> {
    #[cfg(debug_assertions)]
    if let Some(status) = crate::native::smoke_service(operation) {
        return Ok(status);
    }
    let pointer = unsafe { stackharbor_service(operation) };
    if pointer.is_null() {
        return Err("ServiceManagement returned no status".into());
    }
    let bytes = unsafe { std::ffi::CStr::from_ptr(pointer).to_bytes().to_vec() };
    unsafe { libc::free(pointer as *mut _) };
    let value: serde_json::Value = serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
    if let Some(error) = value["error"].as_str() {
        return Err(error.into());
    }
    Ok(value["status"].as_str().unwrap_or("unknown").into())
}

/// A bounded, timed process invocation used only with native-selected binaries
/// and fixed argument vocabularies. The UI cannot select a command or path.
pub fn command(program: &Path, args: &[&str], timeout: Duration) -> Result<String, String> {
    let mut child = Command::new(program)
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| e.to_string())?;
    let stdout = child.stdout.take().unwrap();
    let stderr = child.stderr.take().unwrap();
    let out = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        let result = stdout.take(256 * 1024 + 1).read_to_end(&mut bytes);
        (result, bytes)
    });
    let err = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        let result = stderr.take(256 * 1024 + 1).read_to_end(&mut bytes);
        (result, bytes)
    });
    let deadline = Instant::now() + timeout;
    let status = loop {
        match child.try_wait().map_err(|e| e.to_string())? {
            Some(status) => break Ok(status),
            None if Instant::now() >= deadline => {
                let _ = child.kill();
                let _ = child.wait();
                break Err("Native helper deadline exceeded".to_string());
            }
            None => std::thread::sleep(Duration::from_millis(20)),
        }
    };
    let (out_result, out) = out.join().map_err(|_| "Native output reader failed")?;
    let (err_result, err) = err.join().map_err(|_| "Native error reader failed")?;
    out_result.map_err(|e| e.to_string())?;
    err_result.map_err(|e| e.to_string())?;
    if out.len() > 256 * 1024 || err.len() > 256 * 1024 {
        return Err("Native helper output exceeded limit".into());
    }
    if !status?.success() {
        return Err(String::from_utf8_lossy(&err).trim().to_string());
    }
    Ok(String::from_utf8_lossy(&out).into_owned() + &String::from_utf8_lossy(&err))
}
fn existing_owned_parent(path: &Path) -> Result<(), String> {
    let mut current = PathBuf::new();
    for component in path.parent().ok_or("Missing parent")?.components() {
        current.push(component);
        match fs::symlink_metadata(&current) {
            Ok(st) if !st.is_dir() || st.file_type().is_symlink() => {
                return Err(format!(
                    "Unsafe integration ancestor: {}",
                    current.display()
                ))
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => break,
            Err(e) => return Err(e.to_string()),
            _ => (),
        }
    }
    Ok(())
}
fn owned_parent(path: &Path) -> Result<(), String> {
    let parent = path.parent().ok_or("Missing parent")?;
    let mut current = PathBuf::new();
    for component in parent.components() {
        current.push(component);
        if let Ok(st) = fs::symlink_metadata(&current) {
            if !st.is_dir() || st.file_type().is_symlink() {
                return Err(format!("Unsafe integration parent: {}", current.display()));
            }
        } else {
            fs::create_dir(&current).map_err(|e| e.to_string())?;
        }
    }
    let st = fs::metadata(parent).map_err(|e| e.to_string())?;
    if st.uid() != unsafe { libc::geteuid() } || st.mode() & 0o022 != 0 {
        return Err("Integration directory is not privately owned".into());
    }
    Ok(())
}
fn atomic_write(path: &Path, bytes: &[u8], mode: u32) -> Result<(), String> {
    owned_parent(path)?;
    if fs::symlink_metadata(path)
        .is_ok_and(|st| !st.is_file() || st.uid() != unsafe { libc::geteuid() })
    {
        return Err(format!("Unsafe file: {}", path.display()));
    }
    let temporary = path.with_file_name(format!(".stackharbor-{}-{}", std::process::id(), now()));
    let result = (|| {
        let mut f = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&temporary)
            .map_err(|e| e.to_string())?;
        f.set_permissions(fs::Permissions::from_mode(mode))
            .map_err(|e| e.to_string())?;
        f.write_all(bytes).map_err(|e| e.to_string())?;
        f.sync_all().map_err(|e| e.to_string())?;
        fs::rename(&temporary, path).map_err(|e| e.to_string())
    })();
    let _ = fs::remove_file(&temporary);
    result
}
fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}
fn fingerprint(path: &Path, kind: &str) -> Result<String, String> {
    let st = fs::symlink_metadata(path).map_err(|e| e.to_string())?;
    if st.uid() != unsafe { libc::geteuid() } {
        return Err("Integration belongs to another user".into());
    }
    match kind {
        "link" if st.file_type().is_symlink() => Ok(fs::read_link(path)
            .map_err(|e| e.to_string())?
            .to_string_lossy()
            .into_owned()),
        "tree" if st.is_dir() => tree_hash(path),
        "path" if st.is_file() && st.len() < 2 * 1024 * 1024 => {
            let text = fs::read_to_string(path).map_err(|e| e.to_string())?;
            if text.matches(PATH_BLOCK).count() == 1 {
                Ok(hash(PATH_BLOCK.as_bytes()))
            } else {
                Err("Managed PATH block changed or missing".into())
            }
        }
        _ => Err("Integration type changed".into()),
    }
}
fn tree_hash(path: &Path) -> Result<String, String> {
    fn walk(
        root: &Path,
        path: &Path,
        digest: &mut Sha256,
        count: &mut usize,
    ) -> Result<(), String> {
        let mut entries = fs::read_dir(path)
            .map_err(|e| e.to_string())?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| e.to_string())?;
        entries.sort_by_key(|e| e.file_name());
        for entry in entries {
            *count += 1;
            if *count > 4096 {
                return Err("Skill tree exceeds file count limit".into());
            }
            let p = entry.path();
            let st = fs::symlink_metadata(&p).map_err(|e| e.to_string())?;
            if st.file_type().is_symlink() || !(st.is_dir() || st.is_file()) {
                return Err("Skill contains unsupported entries".into());
            }
            digest.update(p.strip_prefix(root).unwrap().to_string_lossy().as_bytes());
            digest.update(if st.is_dir() { b"d" } else { b"f" });
            if st.is_dir() {
                walk(root, &p, digest, count)?;
            } else {
                if st.len() > 8 * 1024 * 1024 {
                    return Err("Skill file exceeds limit".into());
                }
                digest.update(fs::read(p).map_err(|e| e.to_string())?);
            }
        }
        Ok(())
    }
    let mut digest = Sha256::new();
    walk(path, path, &mut digest, &mut 0)?;
    Ok(format!("{:x}", digest.finalize()))
}
fn copy_tree(source: &Path, target: &Path) -> Result<(), String> {
    fs::create_dir(target).map_err(|e| e.to_string())?;
    for entry in fs::read_dir(source).map_err(|e| e.to_string())? {
        let entry = entry.map_err(|e| e.to_string())?;
        let st = entry.file_type().map_err(|e| e.to_string())?;
        if st.is_dir() {
            copy_tree(&entry.path(), &target.join(entry.file_name()))?;
        } else if st.is_file() {
            fs::copy(entry.path(), target.join(entry.file_name())).map_err(|e| e.to_string())?;
        } else {
            return Err("Skill contains unsupported entries".into());
        }
    }
    Ok(())
}
fn matches(artifact: &Artifact) -> bool {
    fingerprint(&artifact.path, &artifact.kind).is_ok_and(|v| v == artifact.fingerprint)
}
fn current_owned(artifact: &Artifact) -> bool {
    fingerprint(&artifact.path, &artifact.kind).is_ok_and(|actual| {
        actual == artifact.fingerprint
            || !artifact.applied && artifact.previous_fingerprint.as_ref() == Some(&actual)
    })
}
fn lock(layout: &Layout) -> Result<fs::File, String> {
    crate::private::directory(&layout.state)?;
    let path = layout.state.join("install.lock");
    if fs::symlink_metadata(&path)
        .is_ok_and(|s| !s.is_file() || s.uid() != unsafe { libc::geteuid() })
    {
        return Err("Unsafe installation lock".into());
    }
    let file = fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(path)
        .map_err(|e| e.to_string())?;
    file.set_permissions(fs::Permissions::from_mode(0o600))
        .map_err(|e| e.to_string())?;
    if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } != 0 {
        return Err("Another installation operation is in progress".into());
    }
    Ok(file)
}

pub fn binary(layout: &Layout) -> Result<PathBuf, String> {
    let manifest = layout.manifest()?;
    let candidate = manifest
        .current
        .ok_or("Complete desktop setup before starting the engine")?;
    if !candidate.starts_with(layout.state.join("versions")) {
        return Err("Invalid cached CLI path".into());
    }
    verify_binary(layout, &candidate)?;
    Ok(candidate)
}
fn verify_binary(layout: &Layout, path: &Path) -> Result<(), String> {
    type Identity = (u64, u64, u64, i64, i64, i64, i64);
    static VERIFIED: OnceLock<Mutex<HashMap<PathBuf, Identity>>> = OnceLock::new();
    let mut verified = VERIFIED
        .get_or_init(|| Mutex::new(HashMap::new()))
        .lock()
        .map_err(|e| e.to_string())?;
    let st = fs::symlink_metadata(path).map_err(|e| e.to_string())?;
    if !st.is_file()
        || st.mode() & 0o022 != 0
        || fs::canonicalize(path).map_err(|e| e.to_string())? != path
    {
        return Err("Unsafe cached CLI".into());
    }
    let identity = (
        st.dev(),
        st.ino(),
        st.len(),
        st.mtime(),
        st.mtime_nsec(),
        st.ctime(),
        st.ctime_nsec(),
    );
    if verified.get(path) == Some(&identity) {
        return Ok(());
    }
    command(
        Path::new("/usr/bin/codesign"),
        &[
            "--verify",
            "--strict",
            path.to_str().ok_or("Invalid CLI path")?,
        ],
        Duration::from_secs(10),
    )?;
    let signature = command(
        Path::new("/usr/bin/codesign"),
        &["-d", "--verbose=4", path.to_str().unwrap()],
        Duration::from_secs(10),
    )?;
    let metadata = fs::read(layout.resources.join("build.json"))
        .ok()
        .and_then(|b| serde_json::from_slice::<serde_json::Value>(&b).ok());
    let internal = cfg!(debug_assertions)
        || metadata.as_ref().and_then(|v| v["distribution"].as_str()) == Some("internal");
    if !(signature.contains(&format!("TeamIdentifier={TEAM}"))
        || internal && signature.contains("Signature=adhoc"))
    {
        return Err(
            "CLI publisher signature does not match the application's Developer ID team".into(),
        );
    }
    // A version directory carries the immutable content digest, not just a name.
    if path.starts_with(layout.state.join("versions")) {
        let bytes = fs::read(path).map_err(|e| e.to_string())?;
        let suffix = format!("-{}", hash(&bytes));
        if path
            .parent()
            .and_then(Path::file_name)
            .is_none_or(|s| !s.to_string_lossy().ends_with(&suffix))
        {
            return Err("Cached CLI content hash does not match its version".into());
        }
    }
    if verified.len() >= 32 {
        verified.clear();
    }
    verified.insert(path.to_path_buf(), identity);
    Ok(())
}
fn cache(layout: &Layout, manifest: &mut Manifest) -> Result<PathBuf, String> {
    let source = layout.bundled_binary();
    verify_binary(layout, &source)?;
    let bytes = fs::read(&source).map_err(|e| e.to_string())?;
    let destination = layout
        .state
        .join("versions")
        .join(format!("{BUILD_VERSION}-{}", hash(&bytes)))
        .join("stackharbor");
    let incompatible = crate::engine::Engine::existing()?
        .is_some_and(|endpoint| endpoint.build_version != BUILD_VERSION);
    if incompatible
        || manifest
            .current
            .as_ref()
            .is_some_and(|current| current != &destination)
    {
        crate::engine::Engine::idle_shutdown()?;
    }
    if !destination.exists() {
        atomic_write(&destination, &bytes, 0o755)?;
    }
    verify_binary(layout, &destination)?;
    if manifest.current.as_ref() != Some(&destination) {
        manifest.previous = manifest.current.take();
        manifest.current = Some(destination.clone());
        layout.save(manifest)?;
    }
    // Cache GC is intentionally conservative: all former clients remain usable
    // for live engine recovery until an explicit idle uninstall.
    Ok(destination)
}
fn compatibility(layout: &Layout) -> Vec<(String, PathBuf)> {
    [".codex", ".claude"]
        .into_iter()
        .filter(|name| layout.home.join(name).is_dir())
        .map(|name| {
            (
                name.into(),
                layout.home.join(name).join("skills/stackharbor"),
            )
        })
        .collect()
}
fn desired(
    layout: &Layout,
    cached: &Path,
    configure_path: bool,
) -> Result<Vec<(String, Artifact)>, String> {
    let skill_hash = tree_hash(&layout.resources.join("skill"))?;
    let mut items = vec![
        (
            "cli".into(),
            Artifact {
                path: layout.cli(),
                kind: "link".into(),
                fingerprint: cached.to_string_lossy().into_owned(),
                ..Default::default()
            },
        ),
        (
            "skill".into(),
            Artifact {
                path: layout.skill(),
                kind: "tree".into(),
                fingerprint: skill_hash,
                ..Default::default()
            },
        ),
    ];
    for (name, path) in compatibility(layout) {
        items.push((
            name,
            Artifact {
                path,
                kind: "link".into(),
                fingerprint: layout.skill().to_string_lossy().into_owned(),
                ..Default::default()
            },
        ));
    }
    if configure_path {
        for name in [".zprofile", ".bash_profile"] {
            items.push((
                name.into(),
                Artifact {
                    path: layout.home.join(name),
                    kind: "path".into(),
                    fingerprint: hash(PATH_BLOCK.as_bytes()),
                    ..Default::default()
                },
            ));
        }
    }
    Ok(items)
}
fn conflicts(layout: &Layout, manifest: &Manifest) -> Vec<Conflict> {
    let mut found = Vec::new();
    let paths = [
        ("cli".into(), layout.cli()),
        ("skill".into(), layout.skill()),
    ]
    .into_iter()
    .chain(compatibility(layout));
    for (key, path) in paths {
        if fs::symlink_metadata(&path).is_ok()
            && !manifest.artifacts.get(&key).is_some_and(current_owned)
        {
            found.push(Conflict { path: path.to_string_lossy().into_owned(), reason: "Existing entry is unowned or has been modified; explicit backed-up migration is required".into() });
        }
    }
    found
}
pub fn status(layout: &Layout) -> Result<SetupStatus, String> {
    let manifest = layout.manifest()?;
    let accepted = manifest.accepted_at > 0
        && manifest.agreement.accepted
        && manifest.agreement.hash == agreement().hash
        && manifest.agreement.version == AGREEMENT_VERSION;
    let background = service(0).unwrap_or_else(|e| format!("error: {e}"));
    let autostart = service(10).unwrap_or_else(|e| format!("error: {e}"));
    let found = conflicts(layout, &manifest);
    let mut required_keys = vec!["cli".to_string(), "skill".to_string()];
    required_keys.extend(compatibility(layout).into_iter().map(|(name, _)| name));
    let required = required_keys
        .iter()
        .all(|key| manifest.artifacts.get(key).is_some_and(matches));
    let path_configured = [".zprofile", ".bash_profile"]
        .iter()
        .all(|name| manifest.artifacts.get(*name).is_some_and(matches));
    let mut issues = Vec::new();
    if !layout.installed() {
        issues.push("Move StackHarbor.app to /Applications or ~/Applications and reopen it before installation. Mounted DMG and temporary copies cannot install integrations.".into());
    }
    if background != "enabled" {
        issues.push(format!("Background engine status: {background}. Review System Settings > General > Login Items & Extensions."));
    }
    if let Some(current) = &manifest.current {
        if let Err(e) = verify_binary(layout, current) {
            issues.push(e);
        }
    } else {
        issues.push("Required cached command line program is not installed".into());
    }
    if manifest.artifacts.values().any(|a| !matches(a)) {
        issues.push("Some integrations are missing or changed; repair is required".into());
    }
    Ok(SetupStatus {
        complete: manifest.complete
            && manifest.installed_version == BUILD_VERSION
            && accepted
            && required
            && found.is_empty()
            && issues.is_empty(),
        version: BUILD_VERSION,
        bundle_id: BUNDLE_ID,
        installed_path: layout.bundle.to_string_lossy().into_owned(),
        cli_path: layout.cli().to_string_lossy().into_owned(),
        skill_path: layout.skill().to_string_lossy().into_owned(),
        path_configured,
        agreement: AgreementState {
            accepted,
            ..manifest.agreement
        },
        autostart: ServiceState {
            enabled: autostart == "enabled",
            status: autostart,
        },
        background: BackgroundState { status: background },
        conflicts: found,
        issues,
    })
}
fn rename_exclusive(from: &Path, to: &Path) -> Result<(), String> {
    use std::os::unix::ffi::OsStrExt;
    let from = std::ffi::CString::new(from.as_os_str().as_bytes()).map_err(|e| e.to_string())?;
    let to = std::ffi::CString::new(to.as_os_str().as_bytes()).map_err(|e| e.to_string())?;
    if unsafe { libc::renamex_np(from.as_ptr(), to.as_ptr(), libc::RENAME_EXCL) } == 0 {
        Ok(())
    } else {
        Err(std::io::Error::last_os_error().to_string())
    }
}
fn stage_artifact(
    layout: &Layout,
    manifest: &mut Manifest,
    key: &str,
    mut artifact: Artifact,
    migrate: bool,
) -> Result<(), String> {
    owned_parent(&artifact.path)?;
    if let Some(previous) = manifest.artifacts.get(key) {
        artifact.backup = previous.backup.clone();
        if matches(previous)
            && previous.kind == artifact.kind
            && previous.fingerprint == artifact.fingerprint
        {
            artifact.applied = true;
            manifest.artifacts.insert(key.into(), artifact);
            layout.save(manifest)?;
            return Ok(());
        }
    }
    // PATH files are shared files: preserve all user text outside our exact block.
    if artifact.kind == "path" {
        let st = fs::symlink_metadata(&artifact.path).ok();
        if st.as_ref().is_some_and(|s| {
            !s.is_file() || s.uid() != unsafe { libc::geteuid() } || s.len() > 2 * 1024 * 1024
        }) {
            return Err(format!("Unsafe shell profile: {}", artifact.path.display()));
        }
        let text = if st.is_some() {
            fs::read_to_string(&artifact.path).map_err(|e| e.to_string())?
        } else {
            String::new()
        };
        let owned = manifest.artifacts.get(key).is_some_and(matches);
        if text.contains("StackHarbor managed PATH") && !owned && !migrate {
            return Err("Existing PATH block requires explicit backed-up migration".into());
        }
        if text.contains("StackHarbor managed PATH") && text.matches(PATH_BLOCK).count() != 1 {
            return Err("Shell profile has a modified or duplicate PATH block; preserve it and repair the block manually".into());
        }
        if !owned && text.contains(PATH_BLOCK) {
            let backup = layout
                .state
                .join("backups")
                .join(format!("{}-{}", now(), key));
            atomic_write(&backup, text.as_bytes(), 0o600)?;
            artifact.backup = Some(backup);
        }
        manifest.artifacts.insert(key.into(), artifact.clone());
        manifest.complete = false;
        layout.save(manifest)?;
        if !text.contains(PATH_BLOCK) {
            let next = format!(
                "{text}{}{PATH_BLOCK}",
                if text.is_empty() || text.ends_with('\n') {
                    ""
                } else {
                    "\n"
                }
            );
            atomic_write(
                &artifact.path,
                next.as_bytes(),
                st.map_or(0o600, |s| s.mode() & 0o777),
            )?;
        }
    } else {
        let previous = manifest
            .artifacts
            .get(key)
            .filter(|p| current_owned(p))
            .cloned();
        artifact.previous_fingerprint = previous
            .as_ref()
            .and_then(|p| fingerprint(&p.path, &p.kind).ok());
        let exists = fs::symlink_metadata(&artifact.path).is_ok();
        if exists && previous.is_none() && !migrate {
            return Err(format!(
                "Unowned integration was preserved: {}",
                artifact.path.display()
            ));
        }
        if exists && previous.is_none() {
            let backup = layout
                .state
                .join("backups")
                .join(format!("{}-{}", now(), key));
            owned_parent(&backup)?;
            artifact.backup = Some(backup);
        }
        manifest.artifacts.insert(key.into(), artifact.clone());
        manifest.complete = false;
        layout.save(manifest)?;
        if exists {
            if previous.is_some() {
                if !previous.as_ref().is_some_and(current_owned) {
                    return Err("Integration changed during installation".into());
                }
                if artifact.kind == "tree" {
                    let backup =
                        layout
                            .state
                            .join("backups")
                            .join(format!("{}-owned-{}", now(), key));
                    owned_parent(&backup)?;
                    rename_exclusive(&artifact.path, &backup)?;
                } else {
                    fs::remove_file(&artifact.path).map_err(|e| e.to_string())?;
                }
            } else {
                rename_exclusive(&artifact.path, artifact.backup.as_ref().unwrap())?;
            }
        }
        if artifact.kind == "link" {
            symlink(Path::new(&artifact.fingerprint), &artifact.path).map_err(|e| e.to_string())?;
        } else {
            let staging = layout.state.join(format!("skill-staging-{}", now()));
            copy_tree(&layout.resources.join("skill"), &staging)?;
            if tree_hash(&staging)? != artifact.fingerprint {
                return Err("Bundled skill changed during installation".into());
            }
            rename_exclusive(&staging, &artifact.path)?;
        }
    }
    if !matches(&artifact) {
        return Err(format!(
            "Integration did not verify: {}",
            artifact.path.display()
        ));
    }
    artifact.applied = true;
    artifact.previous_fingerprint = None;
    manifest.artifacts.insert(key.into(), artifact);
    layout.save(manifest)
}
pub fn setup(
    layout: &Layout,
    accept: bool,
    agreement_hash: &str,
    auto_start: bool,
    migrate: bool,
    configure_path: bool,
) -> Result<SetupStatus, String> {
    let _guard = INSTALL_LOCK.lock().map_err(|e| e.to_string())?;
    if !layout.installed() {
        return Err("Move StackHarbor.app to /Applications or ~/Applications and reopen it before installation; DMG and temporary copies are refused".into());
    }
    let agreement = agreement();
    if !accept || agreement_hash != agreement.hash {
        return Err(
            "Accept the current complete installation agreement before installing integrations"
                .into(),
        );
    }
    let _lock = lock(layout)?;
    let mut manifest = layout.manifest()?;
    manifest.agreement = AgreementState {
        version: AGREEMENT_VERSION.into(),
        hash: agreement.hash,
        accepted: true,
    };
    manifest.accepted_at = now();
    manifest.complete = false;
    layout.save(&manifest)?;
    let result = (|| {
        let cached = cache(layout, &mut manifest)?;
        let desired = desired(layout, &cached, configure_path)?;
        if !migrate {
            let found = conflicts(layout, &manifest);
            if !found.is_empty() {
                return Err("Existing integrations require explicit backed-up migration".into());
            }
        }
        for (key, artifact) in desired {
            stage_artifact(layout, &mut manifest, &key, artifact, migrate)?;
        }
        let background = service(1)?;
        if background != "enabled" {
            return Err(format!("Background service requires macOS authorization ({background}); approve it in Login Items & Extensions, then resume setup"));
        }
        let autostart = service(if auto_start { 11 } else { 12 })?;
        if auto_start && autostart != "enabled" {
            return Err(format!(
                "Login startup authorization is pending ({autostart})"
            ));
        }
        manifest.installed_version = BUILD_VERSION.into();
        manifest.complete = true;
        layout.save(&manifest)
    })();
    let mut status = status(layout)?;
    if let Err(error) = result {
        status.complete = false;
        status.issues.push(error);
    }
    Ok(status)
}
pub fn set_autostart(layout: &Layout, enabled: bool) -> Result<SetupStatus, String> {
    let _guard = INSTALL_LOCK.lock().map_err(|e| e.to_string())?;
    let manifest = layout.manifest()?;
    if manifest.accepted_at == 0
        || !manifest.agreement.accepted
        || manifest.agreement.hash != agreement().hash
        || manifest.agreement.version != AGREEMENT_VERSION
    {
        return Err("Complete the current installation agreement first".into());
    }
    let _lock = lock(layout)?;
    service(if enabled { 11 } else { 12 })?;
    status(layout)
}
pub fn uninstall(
    layout: &Layout,
    engine: &crate::engine::Engine,
    namespace: &str,
) -> Result<SetupStatus, String> {
    let _guard = INSTALL_LOCK.lock().map_err(|e| e.to_string())?;
    let mut manifest = layout.manifest()?;
    if manifest.accepted_at == 0
        || !manifest.agreement.accepted
        || manifest.agreement.hash != agreement().hash
        || manifest.agreement.version != AGREEMENT_VERSION
    {
        return Err(
            "Accept the current installation agreement before uninstalling integrations".into(),
        );
    }
    // Validate every ancestor before any engine or filesystem mutation. A user
    // may have moved an owned tree and replaced its parent with a symlink.
    for artifact in manifest.artifacts.values() {
        existing_owned_parent(&artifact.path)?;
    }
    let _lock = lock(layout)?;
    // The engine is the authority for idleness. Never signal its PID or any
    // workspace process; a refused idle shutdown makes uninstall a no-op.
    let _ = (engine, namespace);
    crate::engine::Engine::idle_shutdown()?;
    service(2)?;
    service(12)?;
    let mut issues = Vec::new();
    for (key, artifact) in manifest.artifacts.clone().into_iter().rev() {
        if !current_owned(&artifact) {
            if fs::symlink_metadata(&artifact.path).is_ok() {
                issues.push(format!(
                    "Modified entry preserved: {}",
                    artifact.path.display()
                ));
                continue;
            }
        } else {
            let result = match artifact.kind.as_str() {
                "path" => fs::read_to_string(&artifact.path)
                    .map_err(|e| e.to_string())
                    .and_then(|text| {
                        let mode = fs::metadata(&artifact.path)
                            .map_err(|e| e.to_string())?
                            .mode()
                            & 0o777;
                        atomic_write(
                            &artifact.path,
                            text.replacen(PATH_BLOCK, "", 1).as_bytes(),
                            mode,
                        )
                    }),
                "tree" => fs::remove_dir_all(&artifact.path).map_err(|e| e.to_string()),
                "link" => fs::remove_file(&artifact.path).map_err(|e| e.to_string()),
                _ => Err("Unknown owned artifact type".into()),
            };
            if let Err(error) = result {
                issues.push(error);
                continue;
            }
        }
        // Backups remain on disk; the status identifies their location. We do
        // not overwrite newer user edits when undoing an explicit migration.
        if let Some(backup) = &artifact.backup {
            issues.push(format!("Original backup preserved: {}", backup.display()));
        }
        manifest.artifacts.remove(&key);
        manifest.complete = false;
        layout.save(&manifest)?;
    }
    manifest.complete = false;
    layout.save(&manifest)?;
    let mut status = status(layout)?;
    status.complete = false;
    status.issues.extend(issues);
    Ok(status)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};
    fn layout() -> Layout {
        static NEXT: AtomicUsize = AtomicUsize::new(0);
        let home = std::env::temp_dir().canonicalize().unwrap().join(format!(
            "sh-install-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        crate::private::directory(&home).unwrap();
        let bundle = home.join("Applications/StackHarbor.app");
        fs::create_dir_all(bundle.join("Contents/Resources/skill/references")).unwrap();
        fs::write(
            bundle.join("Contents/Resources/skill/SKILL.md"),
            "full skill",
        )
        .unwrap();
        fs::write(
            bundle.join("Contents/Resources/skill/references/help.md"),
            "full reference",
        )
        .unwrap();
        Layout {
            resources: bundle.join("Contents/Resources"),
            state: home.join("support"),
            bundle,
            home,
        }
    }
    #[test]
    fn installer_refuses_dmg_and_requires_current_agreement_before_writes() {
        let mut layout = layout();
        assert!(layout.installed());
        assert!(setup(&layout, false, "", false, false, false).is_err());
        assert!(!layout.state.exists());
        assert!(!layout.cli().exists());
        assert!(setup(&layout, true, "wrong", false, false, false).is_err());
        layout.bundle = PathBuf::from("/Volumes/StackHarbor/StackHarbor.app");
        assert!(!layout.installed());
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn unaccepted_autostart_and_uninstall_do_not_create_state() {
        let layout = layout();
        assert!(set_autostart(&layout, true).is_err());
        assert!(!layout.state.exists());
        assert!(uninstall(&layout, &crate::engine::Engine::default(), "/unused").is_err());
        assert!(!layout.state.exists());
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn moved_integration_parent_is_refused_before_uninstall() {
        let layout = layout();
        let _lock = lock(&layout).unwrap();
        let mut manifest = Manifest {
            accepted_at: now(),
            agreement: AgreementState {
                version: AGREEMENT_VERSION.into(),
                hash: agreement().hash,
                accepted: true,
            },
            ..Default::default()
        };
        let artifact = desired(&layout, Path::new("/cache/stackharbor"), false)
            .unwrap()
            .remove(1)
            .1;
        stage_artifact(&layout, &mut manifest, "skill", artifact, false).unwrap();
        let moved = layout.home.join("moved-agents");
        fs::rename(layout.home.join(".agents"), &moved).unwrap();
        symlink(&moved, layout.home.join(".agents")).unwrap();
        drop(_lock);
        let error = uninstall(&layout, &crate::engine::Engine::default(), "/unused")
            .err()
            .unwrap();
        assert!(error.contains("Unsafe integration ancestor"));
        assert!(moved.join("skills/stackharbor/SKILL.md").exists());
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn acceptance_timestamp_survives_resumable_manifest_save() {
        let layout = layout();
        let _lock = lock(&layout).unwrap();
        let mut manifest = Manifest {
            accepted_at: now(),
            ..Default::default()
        };
        manifest.agreement = AgreementState {
            version: AGREEMENT_VERSION.into(),
            hash: agreement().hash,
            accepted: true,
        };
        layout.save(&manifest).unwrap();
        let saved = layout.manifest().unwrap();
        assert_eq!(saved.accepted_at, manifest.accepted_at);
        assert!(saved.agreement.accepted);
        assert_eq!(saved.agreement.hash, agreement().hash);
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn unowned_cli_is_preserved_until_explicit_backup_and_resume_is_idempotent() {
        let layout = layout();
        let _lock = lock(&layout).unwrap();
        owned_parent(&layout.cli()).unwrap();
        fs::write(layout.cli(), "user cli").unwrap();
        let cached = layout.state.join("versions/test/stackharbor");
        let artifact = Artifact {
            path: layout.cli(),
            kind: "link".into(),
            fingerprint: cached.to_string_lossy().into_owned(),
            ..Default::default()
        };
        let mut manifest = Manifest::default();
        assert!(stage_artifact(&layout, &mut manifest, "cli", artifact.clone(), false).is_err());
        assert_eq!(fs::read_to_string(layout.cli()).unwrap(), "user cli");
        stage_artifact(&layout, &mut manifest, "cli", artifact.clone(), true).unwrap();
        let backup = manifest.artifacts["cli"].backup.clone().unwrap();
        assert_eq!(fs::read_to_string(backup).unwrap(), "user cli");
        assert_eq!(fs::read_link(layout.cli()).unwrap(), cached);
        stage_artifact(&layout, &mut manifest, "cli", artifact, false).unwrap();
        assert!(matches(&manifest.artifacts["cli"]));
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn interrupted_owned_cli_upgrade_retains_prior_ownership_for_resume() {
        let layout = layout();
        let _lock = lock(&layout).unwrap();
        let mut manifest = Manifest::default();
        let first = Artifact {
            path: layout.cli(),
            kind: "link".into(),
            fingerprint: "/cache/first/stackharbor".into(),
            ..Default::default()
        };
        stage_artifact(&layout, &mut manifest, "cli", first.clone(), false).unwrap();
        let staged = Artifact {
            fingerprint: "/cache/second/stackharbor".into(),
            previous_fingerprint: Some(first.fingerprint),
            applied: false,
            ..first
        };
        manifest.artifacts.insert("cli".into(), staged.clone());
        layout.save(&manifest).unwrap();
        let mut resumed = layout.manifest().unwrap();
        assert!(conflicts(&layout, &resumed).is_empty());
        stage_artifact(&layout, &mut resumed, "cli", staged, false).unwrap();
        assert_eq!(
            fs::read_link(layout.cli()).unwrap(),
            Path::new("/cache/second/stackharbor")
        );
        assert!(resumed.artifacts["cli"].applied);
        assert!(resumed.artifacts["cli"].previous_fingerprint.is_none());
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn complete_skill_copied_and_modifications_are_not_owned() {
        let layout = layout();
        let _lock = lock(&layout).unwrap();
        let mut manifest = Manifest::default();
        let artifact = desired(&layout, Path::new("/cache/stackharbor"), false)
            .unwrap()
            .remove(1)
            .1;
        stage_artifact(&layout, &mut manifest, "skill", artifact, false).unwrap();
        assert!(layout.skill().join("references/help.md").is_file());
        assert!(matches(&manifest.artifacts["skill"]));
        fs::write(layout.skill().join("SKILL.md"), "user modified").unwrap();
        assert!(!matches(&manifest.artifacts["skill"]));
        assert_eq!(conflicts(&layout, &manifest).len(), 1);
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn shell_block_preserves_other_content_and_detects_modified_block() {
        let layout = layout();
        let _lock = lock(&layout).unwrap();
        let mut manifest = Manifest::default();
        let path = layout.home.join(".zprofile");
        fs::write(&path, "export ORIGINAL=1\n").unwrap();
        let artifact = Artifact {
            path: path.clone(),
            kind: "path".into(),
            fingerprint: hash(PATH_BLOCK.as_bytes()),
            ..Default::default()
        };
        stage_artifact(&layout, &mut manifest, ".zprofile", artifact.clone(), false).unwrap();
        let text = fs::read_to_string(&path).unwrap();
        assert!(text.starts_with("export ORIGINAL=1\n"));
        fs::write(&path, format!("{text}export LATER=2\n")).unwrap();
        stage_artifact(&layout, &mut manifest, ".zprofile", artifact.clone(), false).unwrap();
        assert_eq!(
            fs::read_to_string(&path)
                .unwrap()
                .matches(PATH_BLOCK)
                .count(),
            1
        );
        fs::write(
            &path,
            fs::read_to_string(&path)
                .unwrap()
                .replace("export PATH=", "export ALTERED="),
        )
        .unwrap();
        assert!(!matches(&manifest.artifacts[".zprofile"]));
        assert!(stage_artifact(&layout, &mut manifest, ".zprofile", artifact, false).is_err());
        assert!(fs::read_to_string(&path)
            .unwrap()
            .contains("export LATER=2"));
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn symlink_parents_and_malicious_skill_links_are_refused() {
        let layout = layout();
        let elsewhere = layout.home.join("elsewhere");
        fs::create_dir(&elsewhere).unwrap();
        symlink(&elsewhere, layout.home.join(".local")).unwrap();
        assert!(owned_parent(&layout.cli()).is_err());
        symlink("/etc/passwd", layout.resources.join("skill/reference-link")).unwrap();
        assert!(tree_hash(&layout.resources.join("skill")).is_err());
        fs::remove_dir_all(layout.home).unwrap();
    }
    #[test]
    fn agreement_hash_covers_full_publisher_license_and_path_preview() {
        let agreement = agreement();
        assert_eq!(agreement.hash, hash(agreement.text.as_bytes()));
        assert!(agreement.text.contains(PUBLISHER));
        assert!(agreement.text.contains(PATH_BLOCK));
        assert!(agreement.text.contains("MIT License"));
        assert!(agreement.text.contains("~/.agents/skills/stackharbor"));
    }
}
