use crate::{
    private,
    protocol::{Reply, Request, BUILD_VERSION, PROTOCOL_VERSION},
};
use std::{
    fs,
    io::{BufRead, BufReader, Read, Write},
    os::unix::{
        fs::{FileTypeExt, MetadataExt, PermissionsExt},
        net::{UnixListener, UnixStream},
    },
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    thread::JoinHandle,
    time::Duration,
};

pub struct Control {
    stop: Arc<AtomicBool>,
    worker: Option<JoinHandle<()>>,
    path: PathBuf,
    inode: u64,
}

pub fn validate(request: &Request) -> Result<(), String> {
    if request.protocol_version != PROTOCOL_VERSION || request.build_version != BUILD_VERSION {
        return Err(
            "Running desktop is incompatible; quit it and install matching desktop/CLI builds"
                .into(),
        );
    }
    if !matches!(
        request.action.as_str(),
        "" | "inspect" | "close-window" | "quit"
    ) {
        return Err("Unsupported desktop control request".into());
    }
    let canonical = private::registry(std::path::Path::new(&request.namespace))?;
    if canonical != std::path::Path::new(&request.namespace) {
        return Err("Registry namespace must be canonical".into());
    }
    Ok(())
}

impl Control {
    pub fn start(app: tauri::AppHandle) -> Result<Self, String> {
        let dir = private::runtime_directory()?;
        let path = dir.join("master.sock");
        if path.as_os_str().len() >= 100 {
            return Err("Desktop socket path is too long; set STACKHARBOR_DESKTOP_RUNTIME_DIR to a private short directory".into());
        }
        if let Ok(st) = fs::symlink_metadata(&path) {
            // SAFETY: geteuid has no preconditions.
            if !st.file_type().is_socket()
                || st.uid() != unsafe { libc::geteuid() }
                || st.mode() & 0o777 != 0o600
            {
                return Err("Refused to replace an unsafe desktop control endpoint".into());
            }
            if UnixStream::connect(&path).is_ok() {
                return Err("Desktop control endpoint is already active".into());
            }
            fs::remove_file(&path).map_err(|e| e.to_string())?;
        }
        let listener = UnixListener::bind(&path).map_err(|e| e.to_string())?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).map_err(|e| e.to_string())?;
        let inode = fs::symlink_metadata(&path)
            .map_err(|e| e.to_string())?
            .ino();
        listener.set_nonblocking(true).map_err(|e| e.to_string())?;
        let stop = Arc::new(AtomicBool::new(false));
        let done = stop.clone();
        let worker = std::thread::spawn(move || {
            while !done.load(Ordering::Acquire) {
                match listener.accept() {
                    Ok((mut stream, _)) => {
                        let _ = stream.set_read_timeout(Some(Duration::from_secs(2)));
                        let _ = stream.set_write_timeout(Some(Duration::from_secs(2)));
                        let mut line = String::new();
                        let request = BufReader::new(&stream)
                            .take(16385)
                            .read_line(&mut line)
                            .map_err(|e| e.to_string())
                            .and_then(|_| {
                                if line.len() > 16384 {
                                    Err("Oversized desktop request".into())
                                } else {
                                    serde_json::from_str::<Request>(&line)
                                        .map_err(|e| e.to_string())
                                }
                            })
                            .and_then(|request| validate(&request).map(|_| request));
                        // CLI success means the verified gateway is ready,
                        // not merely that the native socket accepted a request.
                        // Failures return to the terminal without a focus-stealing
                        // dialog during automatic background startup.
                        let ready = request.as_ref().map_err(Clone::clone).and_then(|request| {
                            if request.action.is_empty() {
                                crate::prepare_request(&app, request).map(Some)
                            } else {
                                Ok(None)
                            }
                        });
                        let error = ready.as_ref().err().cloned();
                        let mut response = Reply::new(error);
                        if request.as_ref().is_ok_and(|r| r.action == "inspect") {
                            response.state = Some(crate::snapshot(&app));
                        }
                        if let Ok(mut reply) = serde_json::to_vec(&response) {
                            reply.push(b'\n');
                            if stream.write_all(&reply).is_ok() {
                                if let Ok(request) = request {
                                    match request.action.as_str() {
                                        "inspect" => {}
                                        "quit" => crate::quit(&app),
                                        "close-window" => {
                                            let handle = app.clone();
                                            let _ = app.run_on_main_thread(move || {
                                                if let Some(window) =
                                                    tauri::Manager::get_webview_window(
                                                        &handle,
                                                        &crate::window_label(&request.namespace),
                                                    )
                                                {
                                                    let _ = window.close();
                                                }
                                            });
                                        }
                                        _ => {
                                            if let Ok(Some(url)) = ready {
                                                crate::open_prepared(app.clone(), request, url);
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                    Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                        std::thread::sleep(Duration::from_millis(25))
                    }
                    Err(_) => break,
                }
            }
        });
        Ok(Self {
            stop,
            worker: Some(worker),
            path,
            inode,
        })
    }
}

impl Drop for Control {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
        if fs::symlink_metadata(&self.path)
            .is_ok_and(|st| st.ino() == self.inode && st.file_type().is_socket())
        {
            let _ = fs::remove_file(&self.path);
        }
    }
}
