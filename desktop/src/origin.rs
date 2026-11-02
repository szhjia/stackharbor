use std::sync::{Arc, RwLock};

// Only the native owner can replace this guard, after sidecar readiness has
// validated the replacement gateway's namespace and exact loopback endpoint.
#[derive(Clone)]
pub struct ConsoleOrigin(Arc<RwLock<Navigation>>);

struct Navigation {
    verified: tauri::Url,
    loaded: Option<tauri::Url>,
}

impl ConsoleOrigin {
    pub fn new(url: tauri::Url) -> Self {
        Self(Arc::new(RwLock::new(Navigation {
            verified: url,
            loaded: None,
        })))
    }
    pub fn replace_verified(&self, url: tauri::Url) -> Result<(), String> {
        *self.0.write().map_err(|e| e.to_string())? = Navigation {
            verified: url,
            loaded: None,
        };
        Ok(())
    }
    pub fn allows(&self, url: &tauri::Url) -> bool {
        self.0
            .read()
            .is_ok_and(|state| crate::protocol::same_origin(&state.verified, url))
    }
    pub fn page_load(&self, url: &tauri::Url, finished: bool) {
        if let Ok(mut state) = self.0.write() {
            if crate::protocol::same_origin(&state.verified, url) {
                state.loaded = finished.then(|| url.clone());
            }
        }
    }
    pub fn loaded_url(&self) -> Option<tauri::Url> {
        self.0.read().ok().and_then(|state| state.loaded.clone())
    }
}
