use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};

pub const PROTOCOL_VERSION: u32 = 1;
pub const BUILD_VERSION: &str = env!("CARGO_PKG_VERSION");

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub protocol_version: u32,
    pub build_version: String,
    pub namespace: String,
    pub background: bool,
    #[serde(default)]
    pub action: String,
}

#[derive(Debug, Serialize)]
pub struct Reply {
    pub pid: u32,
    pub protocol_version: u32,
    pub build_version: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state: Option<MasterSnapshot>,
}

impl Reply {
    pub fn new(error: Option<String>) -> Self {
        Self {
            pid: std::process::id(),
            protocol_version: PROTOCOL_VERSION,
            build_version: BUILD_VERSION,
            error,
            state: None,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct GatewaySnapshot {
    pub namespace: String,
    pub url: String,
    pub owned: bool,
    pub pid: Option<u32>,
}

#[derive(Debug, Serialize)]
pub struct WindowSnapshot {
    pub label: String,
    pub visible: bool,
    pub url: String,
}

#[derive(Debug, Serialize)]
pub struct MasterSnapshot {
    pub gateways: Vec<GatewaySnapshot>,
    pub windows: Vec<WindowSnapshot>,
}

#[derive(Debug, Deserialize)]
pub struct BackendReady {
    #[serde(default)]
    pub protocol_version: u32,
    #[serde(default)]
    pub build_version: String,
    #[serde(default)]
    pub session_protocol: u32,
    #[serde(default)]
    pub namespace: String,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub owned: bool,
    pub error: Option<String>,
}

impl BackendReady {
    pub fn validate(&self, namespace: &Path) -> Result<tauri::Url, String> {
        if let Some(error) = &self.error {
            return Err(error.clone());
        }
        if self.protocol_version != PROTOCOL_VERSION
            || self.build_version != BUILD_VERSION
            || self.session_protocol != 1
        {
            return Err(
                "Desktop, CLI and gateway builds are incompatible; existing owners were preserved"
                    .into(),
            );
        }
        if Path::new(&self.namespace) != namespace {
            return Err("Gateway namespace changed during startup".into());
        }
        let url = tauri::Url::parse(&self.url).map_err(|e| e.to_string())?;
        if url.scheme() != "http"
            || url.host_str() != Some("127.0.0.1")
            || url.port().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/"
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err("Gateway did not return an exact loopback console URL".into());
        }
        Ok(url)
    }
}

pub fn cache_dir() -> Result<PathBuf, String> {
    #[cfg(target_os = "macos")]
    let path = home_dir()?.join("Library/Caches");
    #[cfg(target_os = "linux")]
    let path = std::env::var_os("XDG_CACHE_HOME")
        .map(PathBuf::from)
        .unwrap_or(home_dir()?.join(".cache"));
    Ok(path)
}

pub fn home_dir() -> Result<PathBuf, String> {
    std::env::var_os("HOME")
        .map(PathBuf::from)
        .filter(|p| p.is_absolute())
        .ok_or_else(|| "An absolute HOME is required".into())
}

pub fn default_namespace() -> Result<PathBuf, String> {
    Ok(cache_dir()?.join("stackharbor"))
}

pub fn same_origin(a: &tauri::Url, b: &tauri::Url) -> bool {
    a.origin() == b.origin() && b.username().is_empty() && b.password().is_none()
}
