use crate::{
    private,
    protocol::{BackendReady, BUILD_VERSION, PROTOCOL_VERSION},
};
use std::{
    collections::HashMap,
    io::{BufRead, BufReader, Read},
    path::{Path, PathBuf},
    process::{Child, ChildStdin, Command, Stdio},
    sync::{mpsc, Mutex},
    time::{Duration, Instant},
};

struct Backend {
    child: Option<Child>,
    parent: Option<ChildStdin>,
    url: tauri::Url,
}

impl Backend {
    fn running(&mut self) -> bool {
        self.child
            .as_mut()
            .is_some_and(|child| matches!(child.try_wait(), Ok(None)))
    }
}

impl Drop for Backend {
    fn drop(&mut self) {
        self.parent.take();
        if let Some(child) = self.child.as_mut() {
            // Closing the parent pipe lets Go perform graceful HTTP shutdown.
            let until = Instant::now() + Duration::from_secs(6);
            while matches!(child.try_wait(), Ok(None)) && Instant::now() < until {
                std::thread::sleep(Duration::from_millis(25));
            }
            if matches!(child.try_wait(), Ok(None)) {
                let _ = child.kill();
            }
            let _ = child.wait();
        }
    }
}

pub struct Backends {
    executable: PathBuf,
    entries: Mutex<HashMap<PathBuf, Backend>>,
}

impl Backends {
    pub fn new(executable: PathBuf) -> Self {
        Self {
            executable,
            entries: Mutex::new(HashMap::new()),
        }
    }

    pub fn ensure(&self, namespace: &Path) -> Result<tauri::Url, String> {
        let namespace = private::registry(namespace)?;
        let mut entries = self.entries.lock().map_err(|e| e.to_string())?;
        if let Some(backend) = entries.get_mut(&namespace) {
            if backend.running() {
                return Ok(backend.url.clone());
            }
        }
        entries.remove(&namespace);
        let mut last = String::new();
        for attempt in 0..3 {
            match self.start(&namespace) {
                Ok(backend) => {
                    let url = backend.url.clone();
                    entries.insert(namespace.clone(), backend);
                    return Ok(url);
                }
                Err(e) => last = e,
            }
            if attempt < 2 {
                std::thread::sleep(Duration::from_millis(200 * (attempt + 1)));
            }
        }
        Err(last)
    }

    fn start(&self, namespace: &Path) -> Result<Backend, String> {
        let mut child = Command::new(&self.executable)
            .args([
                "desktop-backend",
                "--protocol",
                &PROTOCOL_VERSION.to_string(),
                "--expected-version",
                BUILD_VERSION,
                "--namespace",
            ])
            .arg(namespace)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .env_remove("STACKHARBOR_CACHE_DIR")
            .spawn()
            .map_err(|e| format!("Could not start bundled CLI: {e}"))?;
        let parent = child.stdin.take();
        let output = child.stdout.take().ok_or("Missing sidecar stdout")?;
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            let mut line = String::new();
            let result = BufReader::new(output)
                .take(16385)
                .read_line(&mut line)
                .map_err(|e| e.to_string())
                .and_then(|_| {
                    if line.len() > 16384 {
                        Err("Oversized sidecar handshake".into())
                    } else {
                        serde_json::from_str::<BackendReady>(&line)
                            .map_err(|e| format!("Invalid sidecar handshake: {e}"))
                    }
                });
            let _ = tx.send(result);
        });
        let ready = rx
            .recv_timeout(Duration::from_secs(8))
            .map_err(|e| format!("Gateway startup timed out: {e}"))
            .and_then(|v| v)
            .and_then(|ready| ready.validate(namespace).map(|url| (ready, url)));
        match ready {
            Ok((ready, url)) => {
                if ready.owned {
                    Ok(Backend {
                        child: Some(child),
                        parent,
                        url,
                    })
                } else {
                    // A reused gateway is independently owned. Reap only the
                    // short-lived helper; never send a signal to the gateway.
                    drop(parent);
                    let _ = child.wait();
                    Ok(Backend {
                        child: None,
                        parent: None,
                        url,
                    })
                }
            }
            Err(error) => {
                drop(parent);
                let _ = child.kill();
                let _ = child.wait();
                Err(error)
            }
        }
    }

    pub fn shutdown(&self) {
        if let Ok(mut entries) = self.entries.lock() {
            // Signal all owned backends before waiting for any one of them.
            for backend in entries.values_mut() {
                backend.parent.take();
            }
            entries.clear();
        }
    }

    pub fn snapshot(&self) -> Vec<crate::protocol::GatewaySnapshot> {
        self.entries
            .lock()
            .map(|entries| {
                entries
                    .iter()
                    .map(|(namespace, backend)| crate::protocol::GatewaySnapshot {
                        namespace: namespace.to_string_lossy().into_owned(),
                        url: backend.url.to_string(),
                        owned: backend.child.is_some(),
                        pid: backend.child.as_ref().map(Child::id),
                    })
                    .collect()
            })
            .unwrap_or_default()
    }
}
