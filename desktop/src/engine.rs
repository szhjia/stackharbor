//! A deliberately narrow HTTP client. No HTTP listener or arbitrary filesystem API
//! is exposed to a WebView; each connection verifies the engine's Unix peer.
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fs,
    io::{BufRead, BufReader, Read, Write},
    os::{
        fd::AsRawFd,
        unix::{
            fs::{FileTypeExt, MetadataExt},
            net::UnixStream,
        },
    },
    path::{Path, PathBuf},
    sync::Mutex,
    time::{Duration, Instant},
};

const MAX_HEADERS: usize = 32 * 1024;
const MAX_BODY: usize = 8 * 1024 * 1024;
pub const MAX_EVENT: usize = 1024 * 1024;
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Endpoint {
    pub socket_path: PathBuf,
    pub pid: i32,
    pub process_created_millis: u64,
    pub protocol_version: u32,
    pub build_version: String,
}
#[derive(Serialize)]
pub struct Response {
    pub status: u16,
    pub body: String,
}
#[derive(Clone, Serialize)]
pub struct Event {
    pub event: String,
    pub id: String,
    pub data: String,
}
#[derive(Default)]
pub struct Engine {
    endpoint: Mutex<Option<Endpoint>>,
}

pub fn validate_path(method: &str, path: &str) -> Result<(), String> {
    let route = path.split('?').next().unwrap_or("");
    let lower = route.to_ascii_lowercase();
    if path.len() > 8192
        || !path.is_ascii()
        || path.bytes().any(|b| b <= 32 || b == 127)
        || path.contains(['\\', '#'])
        || route.contains("//")
        || lower.contains("%2e")
        || lower.contains("%2f")
        || lower.contains("%5c")
        || lower.contains("%00")
        || route.split('/').any(|p| p == "." || p == "..")
        || !matches!(method, "GET" | "POST" | "PUT" | "PATCH" | "DELETE")
    {
        return Err("Invalid native API request".into());
    }
    if route.starts_with("/api/v1/") || (method == "GET" && route == "/engine/v1/identity") {
        Ok(())
    } else {
        Err("Native API route is not allowed".into())
    }
}

pub fn process_matches(endpoint: &Endpoint) -> bool {
    if endpoint.pid <= 0 {
        return false;
    }
    // PROC_PIDTBSDINFO reports kernel start time, so a recycled PID is rejected.
    let mut info: libc::proc_bsdinfo = unsafe { std::mem::zeroed() };
    let n = unsafe {
        libc::proc_pidinfo(
            endpoint.pid,
            libc::PROC_PIDTBSDINFO,
            0,
            &mut info as *mut _ as *mut _,
            std::mem::size_of_val(&info) as i32,
        )
    };
    n as usize == std::mem::size_of_val(&info)
        && info.pbi_uid == unsafe { libc::geteuid() }
        && info.pbi_start_tvsec * 1000 + info.pbi_start_tvusec / 1000
            == endpoint.process_created_millis
}

fn connect(endpoint: &Endpoint) -> Result<UnixStream, String> {
    let parent = endpoint
        .socket_path
        .parent()
        .ok_or("Engine socket has no parent")?;
    if fs::canonicalize(parent).map_err(|e| e.to_string())? != parent {
        return Err("Engine runtime path contains a symlink".into());
    }
    let parent_st = fs::symlink_metadata(parent).map_err(|e| e.to_string())?;
    let st = fs::symlink_metadata(&endpoint.socket_path).map_err(|e| e.to_string())?;
    let uid = unsafe { libc::geteuid() };
    if !parent_st.is_dir()
        || parent_st.uid() != uid
        || parent_st.mode() & 0o077 != 0
        || !st.file_type().is_socket()
        || st.uid() != uid
        || st.mode() & 0o077 != 0
        || endpoint.protocol_version != 1
        || !process_matches(endpoint)
    {
        return Err("Unsafe or stale engine endpoint; existing sessions were preserved".into());
    }
    let stream = UnixStream::connect(&endpoint.socket_path).map_err(|e| e.to_string())?;
    let mut peer_uid = 0;
    let mut peer_gid = 0;
    let mut peer_pid: libc::pid_t = 0;
    let mut length = std::mem::size_of_val(&peer_pid) as libc::socklen_t;
    if unsafe { libc::getpeereid(stream.as_raw_fd(), &mut peer_uid, &mut peer_gid) } != 0
        || peer_uid != uid
        || unsafe {
            libc::getsockopt(
                stream.as_raw_fd(),
                0,
                2,
                &mut peer_pid as *mut _ as *mut _,
                &mut length,
            )
        } != 0
        || peer_pid != endpoint.pid
        || !process_matches(endpoint)
    {
        return Err("Engine Unix peer identity changed".into());
    }
    stream
        .set_read_timeout(Some(Duration::from_secs(30)))
        .map_err(|e| e.to_string())?;
    stream
        .set_write_timeout(Some(Duration::from_secs(5)))
        .map_err(|e| e.to_string())?;
    Ok(stream)
}
fn runtime_path(
    cache: &Path,
    override_path: Option<&Path>,
    temporary: &Path,
    uid: u32,
) -> Result<PathBuf, String> {
    fn clean(path: &Path) -> PathBuf {
        let mut cleaned = PathBuf::new();
        for component in path.components() {
            match component {
                std::path::Component::CurDir => (),
                std::path::Component::ParentDir => {
                    cleaned.pop();
                }
                part => cleaned.push(part),
            }
        }
        cleaned
    }
    if let Some(path) = override_path {
        if !path.is_absolute() {
            return Err("Engine runtime directory must be absolute".into());
        }
        return Ok(clean(path));
    }
    let base = clean(&cache.join("stackharbor/engine-run"));
    use std::os::unix::ffi::OsStrExt;
    if base.join("engine.sock").as_os_str().as_bytes().len() >= 100 {
        let temporary = fs::canonicalize(temporary).map_err(|e| e.to_string())?;
        let digest = Sha256::digest(base.as_os_str().as_bytes());
        let suffix = digest[..6]
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>();
        return Ok(temporary.join(format!("sh-engine-{uid}-{suffix}")));
    }
    Ok(base)
}
impl Engine {
    pub fn endpoint(&self, binary: &Path) -> Result<Endpoint, String> {
        let mut cached = self.endpoint.lock().map_err(|e| e.to_string())?;
        if let Some(endpoint) = cached.as_ref() {
            if process_matches(endpoint) {
                return Ok(endpoint.clone());
            }
        }
        let action = "ensure";
        #[cfg(debug_assertions)]
        let action = if crate::native::smoke_active() {
            "status"
        } else {
            action
        };
        let output = crate::install::command(
            binary,
            &["engine", action, "--json"],
            Duration::from_secs(20),
        )?;
        let endpoint: Endpoint =
            serde_json::from_str(&output).map_err(|e| format!("Invalid engine readiness: {e}"))?;
        let stream = connect(&endpoint)?;
        let response =
            request_stream(stream, "GET", "/engine/v1/identity", None, None)?.finish()?;
        let identity: serde_json::Value =
            serde_json::from_str(&response.body).map_err(|e| e.to_string())?;
        let actual = identity.get("data").unwrap_or(&identity);
        let verified: Endpoint = serde_json::from_value(actual.clone())
            .map_err(|e| format!("Invalid engine identity: {e}"))?;
        if verified.socket_path != endpoint.socket_path
            || verified.pid != endpoint.pid
            || verified.process_created_millis != endpoint.process_created_millis
            || verified.protocol_version != 1
            || verified.build_version != crate::protocol::BUILD_VERSION
        {
            return Err(format!("Running engine is incompatible. Preserve sessions and use the cached CLI at {} to stop them before upgrading.", binary.display()));
        }
        *cached = Some(endpoint.clone());
        Ok(endpoint)
    }
    /// Read-only endpoint lookup, used for upgrade/uninstall. Never registers or
    /// kickstarts an engine merely to inspect whether it is idle.
    pub fn existing() -> Result<Option<Endpoint>, String> {
        let override_path = std::env::var_os("STACKHARBOR_ENGINE_RUNTIME_DIR")
            .filter(|s| !s.is_empty())
            .map(PathBuf::from);
        let temporary = std::env::var_os("TMPDIR")
            .filter(|s| !s.is_empty())
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from("/tmp"));
        let runtime = runtime_path(
            &crate::protocol::cache_dir()?,
            override_path.as_deref(),
            &temporary,
            unsafe { libc::getuid() },
        )?;
        Self::existing_in(&runtime)
    }
    fn existing_in(runtime: &Path) -> Result<Option<Endpoint>, String> {
        let runtime = match fs::canonicalize(runtime) {
            Ok(path) => path,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(e) => return Err(e.to_string()),
        };
        let directory = fs::symlink_metadata(&runtime).map_err(|e| e.to_string())?;
        if !directory.is_dir()
            || directory.uid() != unsafe { libc::geteuid() }
            || directory.mode() & 0o777 != 0o700
        {
            return Err("Unsafe engine runtime; preserved integrations".into());
        }
        let path = runtime.join("engine.json");
        let st = match fs::symlink_metadata(&path) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                if fs::symlink_metadata(runtime.join("engine.sock")).is_ok() {
                    return Err(
                        "Engine socket exists without identity metadata; preserved integrations"
                            .into(),
                    );
                }
                return Ok(None);
            }
            Err(e) => return Err(e.to_string()),
            Ok(st) => st,
        };
        if !st.is_file()
            || st.uid() != unsafe { libc::geteuid() }
            || st.mode() & 0o777 != 0o600
            || st.len() > 16384
        {
            return Err("Unsafe engine metadata; preserved integrations".into());
        }
        let endpoint: Endpoint =
            serde_json::from_slice(&fs::read(path).map_err(|e| e.to_string())?)
                .map_err(|e| e.to_string())?;
        if endpoint.socket_path != runtime.join("engine.sock") {
            return Err("Engine socket path changed".into());
        }
        if unsafe { libc::kill(endpoint.pid, 0) } != 0
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return Ok(None);
        }
        connect(&endpoint)?;
        Ok(Some(endpoint))
    }
    pub fn existing_request(
        endpoint: &Endpoint,
        method: &str,
        path: &str,
    ) -> Result<Response, String> {
        request_stream(connect(endpoint)?, method, path, None, None)?.finish()
    }
    pub fn idle_shutdown() -> Result<(), String> {
        if let Some(endpoint) = Self::existing()? {
            let identity = Self::existing_request(&endpoint, "GET", "/engine/v1/identity")?;
            let value: serde_json::Value =
                serde_json::from_str(&identity.body).map_err(|e| e.to_string())?;
            let data = value.get("data").unwrap_or(&value);
            if identity.status != 200
                || data["active_processes"].as_u64() != Some(0)
                || data["operations_running"].as_u64() != Some(0)
            {
                return Err("Stop all running projects and operations before upgrading or uninstalling. Existing projects and cached clients were preserved.".into());
            }
            let response = Self::existing_request(&endpoint, "POST", "/engine/v1/shutdown")?;
            if response.status >= 300 {
                return Err(format!("Engine refused idle shutdown: {}", response.body));
            }
            let deadline = Instant::now() + Duration::from_secs(5);
            while process_matches(&endpoint) {
                if Instant::now() >= deadline {
                    return Err(
                        "Engine has not completed idle shutdown; retry after it exits".into(),
                    );
                }
                std::thread::sleep(Duration::from_millis(25));
            }
        }
        Ok(())
    }
    pub fn request(
        &self,
        binary: &Path,
        namespace: &str,
        method: &str,
        path: &str,
        body: Option<&str>,
    ) -> Result<Response, String> {
        validate_path(method, path)?;
        self.internal(binary, namespace, method, path, body)
    }
    pub fn internal(
        &self,
        binary: &Path,
        namespace: &str,
        method: &str,
        path: &str,
        body: Option<&str>,
    ) -> Result<Response, String> {
        request_stream(
            connect(&self.endpoint(binary)?)?,
            method,
            path,
            body,
            Some(namespace),
        )?
        .finish()
    }
    pub fn subscribe(
        &self,
        binary: &Path,
        namespace: &str,
        path: &str,
    ) -> Result<HttpBody, String> {
        validate_path("GET", path)?;
        let body = request_stream(
            connect(&self.endpoint(binary)?)?,
            "GET",
            path,
            None,
            Some(namespace),
        )?;
        if body.status != 200 || !body.event_stream {
            return Err("Engine did not return an event stream".into());
        }
        Ok(body)
    }
}

pub struct HttpBody {
    reader: BufReader<UnixStream>,
    status: u16,
    event_stream: bool,
    chunked: bool,
    remaining: Option<usize>,
    chunk_remaining: usize,
    ended: bool,
    deadline: Instant,
}
fn wait_readable(reader: &BufReader<UnixStream>, deadline: Instant) -> Result<(), String> {
    if !reader.buffer().is_empty() {
        return Ok(());
    }
    loop {
        let left = deadline
            .checked_duration_since(Instant::now())
            .ok_or("Engine response deadline exceeded")?;
        let mut descriptor = libc::pollfd {
            fd: reader.get_ref().as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        let result = unsafe {
            libc::poll(
                &mut descriptor,
                1,
                (left.as_millis() + 1).min(i32::MAX as u128) as i32,
            )
        };
        if result > 0 {
            return Ok(());
        }
        if result == 0 {
            return Err("Engine response deadline exceeded".into());
        }
        let error = std::io::Error::last_os_error();
        if error.kind() != std::io::ErrorKind::Interrupted {
            return Err(error.to_string());
        }
    }
}
fn line(
    reader: &mut BufReader<UnixStream>,
    limit: usize,
    deadline: Instant,
) -> Result<String, String> {
    let mut bytes = Vec::new();
    loop {
        wait_readable(reader, deadline)?;
        let buffer = reader.fill_buf().map_err(|e| e.to_string())?;
        if buffer.is_empty() {
            break;
        }
        let count = buffer
            .iter()
            .position(|b| *b == b'\n')
            .map_or(buffer.len(), |n| n + 1);
        if bytes.len() + count > limit {
            return Err("Engine HTTP line exceeds limit".into());
        }
        bytes.extend_from_slice(&buffer[..count]);
        reader.consume(count);
        if bytes.last() == Some(&b'\n') {
            break;
        }
    }
    String::from_utf8(bytes).map_err(|e| e.to_string())
}
fn request_stream(
    mut stream: UnixStream,
    method: &str,
    path: &str,
    body: Option<&str>,
    namespace: Option<&str>,
) -> Result<HttpBody, String> {
    let body = body.unwrap_or("");
    if body.len() > 2 * 1024 * 1024 {
        return Err("Native request body exceeds 2 MiB".into());
    }
    let namespace = namespace.unwrap_or("");
    if namespace.contains(['\r', '\n']) || namespace.len() > 4096 {
        return Err("Invalid namespace".into());
    }
    write!(stream, "{method} {path} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Type: application/json\r\nX-StackHarbor-Namespace: {namespace}\r\nContent-Length: {}\r\n\r\n{body}", body.len()).map_err(|e| e.to_string())?;
    let deadline = Instant::now() + Duration::from_secs(30);
    let mut reader = BufReader::new(stream);
    let status_line = line(&mut reader, 1024, deadline)?;
    let status = status_line
        .split_whitespace()
        .nth(1)
        .and_then(|v| v.parse::<u16>().ok())
        .filter(|v| (100..600).contains(v))
        .ok_or("Invalid HTTP response status")?;
    let mut total = status_line.len();
    let mut remaining = None;
    let mut chunked = false;
    let mut event_stream = false;
    loop {
        let header = line(&mut reader, MAX_HEADERS, deadline)?;
        total += header.len();
        if total > MAX_HEADERS {
            return Err("Engine response headers exceed limit".into());
        }
        if header == "\r\n" {
            break;
        }
        if header.is_empty() {
            return Err("Truncated engine headers".into());
        }
        if let Some((key, value)) = header.split_once(':') {
            match key.to_ascii_lowercase().as_str() {
                "content-length" => {
                    remaining = Some(value.trim().parse::<usize>().map_err(|e| e.to_string())?);
                }
                "transfer-encoding" => {
                    if value.trim() != "chunked" {
                        return Err("Unsupported transfer encoding".into());
                    }
                    chunked = true;
                }
                "content-type" => event_stream = value.trim().starts_with("text/event-stream"),
                _ => (),
            }
        }
    }
    if remaining.is_some() && chunked {
        return Err("Ambiguous engine HTTP framing".into());
    }
    Ok(HttpBody {
        reader,
        status,
        event_stream,
        remaining,
        chunked,
        chunk_remaining: 0,
        ended: false,
        deadline,
    })
}
impl HttpBody {
    pub fn cancellation(&self) -> Result<UnixStream, String> {
        self.reader.get_ref().try_clone().map_err(|e| e.to_string())
    }
    fn byte(&mut self) -> Result<Option<u8>, String> {
        if self.ended {
            return Ok(None);
        }
        if self.chunked && self.chunk_remaining == 0 {
            let size = line(&mut self.reader, 128, self.deadline)?;
            let value = size.trim().split(';').next().unwrap_or("");
            self.chunk_remaining = usize::from_str_radix(value, 16).map_err(|e| e.to_string())?;
            if self.chunk_remaining > MAX_BODY {
                return Err("Engine chunk exceeds limit".into());
            }
            if self.chunk_remaining == 0 {
                self.ended = true;
                return Ok(None);
            }
        }
        if !self.chunked && self.remaining == Some(0) {
            self.ended = true;
            return Ok(None);
        }
        wait_readable(&self.reader, self.deadline)?;
        let mut byte = [0];
        match self.reader.read(&mut byte).map_err(|e| e.to_string())? {
            0 if self.chunked || self.remaining.is_some_and(|n| n > 0) => {
                return Err("Truncated engine body".into())
            }
            0 => {
                self.ended = true;
                return Ok(None);
            }
            _ => (),
        }
        if self.chunked {
            self.chunk_remaining -= 1;
            if self.chunk_remaining == 0 {
                let mut end = [0; 2];
                for byte in &mut end {
                    wait_readable(&self.reader, self.deadline)?;
                    self.reader
                        .read_exact(std::slice::from_mut(byte))
                        .map_err(|e| e.to_string())?;
                }
                if end != *b"\r\n" {
                    return Err("Invalid engine chunk delimiter".into());
                }
            }
        } else if let Some(n) = &mut self.remaining {
            *n -= 1;
        }
        Ok(Some(byte[0]))
    }
    fn finish(mut self) -> Result<Response, String> {
        if self.remaining.is_some_and(|n| n > MAX_BODY) {
            return Err("Engine response exceeds 8 MiB".into());
        }
        let mut bytes = Vec::new();
        while let Some(b) = self.byte()? {
            if bytes.len() == MAX_BODY || Instant::now() > self.deadline {
                return Err("Engine response limit or deadline exceeded".into());
            }
            bytes.push(b);
        }
        Ok(Response {
            status: self.status,
            body: String::from_utf8(bytes).map_err(|e| e.to_string())?,
        })
    }
    pub fn event(&mut self) -> Result<Option<Event>, String> {
        self.deadline = Instant::now() + Duration::from_secs(30);
        let mut event = Event {
            event: "message".into(),
            id: String::new(),
            data: String::new(),
        };
        let mut bytes = Vec::new();
        let mut count = 0;
        loop {
            match self.byte()? {
                None => return Ok(None),
                Some(b) => {
                    count += 1;
                    if count > MAX_EVENT {
                        return Err("Event exceeds 1 MiB".into());
                    }
                    if b != b'\n' {
                        bytes.push(b);
                        continue;
                    }
                }
            }
            let text = String::from_utf8(std::mem::take(&mut bytes)).map_err(|e| e.to_string())?;
            let text = text.trim_end_matches('\r');
            if text.is_empty() {
                if !event.data.is_empty() {
                    event.data.pop();
                    return Ok(Some(event));
                }
                count = 0;
                continue;
            }
            let (field, value) = text.split_once(':').unwrap_or((text, ""));
            let value = value.strip_prefix(' ').unwrap_or(value);
            match field {
                "data" => {
                    event.data.push_str(value);
                    event.data.push('\n');
                }
                "id" if !value.contains('\0') => event.id = value.into(),
                "event" => event.event = value.into(),
                _ => (),
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn response(raw: Vec<u8>) -> Result<HttpBody, String> {
        let (client, mut server) = UnixStream::pair().unwrap();
        std::thread::spawn(move || {
            let mut request = Vec::new();
            loop {
                let mut byte = [0];
                if server.read(&mut byte).unwrap_or(0) == 0 {
                    return;
                }
                request.push(byte[0]);
                if request.ends_with(b"\r\n\r\n") {
                    break;
                }
            }
            let _ = server.write_all(&raw);
        });
        request_stream(client, "GET", "/api/v1/events", None, Some("/test"))
    }
    #[test]
    fn route_scope_rejects_traversal_and_engine_lifecycle() {
        assert!(validate_path("GET", "/api/v1/workspaces?name=a%20b").is_ok());
        for path in [
            "//api/v1/foo",
            "/api/v1/../secret",
            "/api/v1/%2e%2e/secret",
            "/api/v1/a%2fb",
            "/api/v1/a#x",
            "/api/v1/a\r\nX:a",
            "https://evil/api/v1/foo",
            "/engine/v1/shutdown",
            "/api/v10/test",
            "/api/v1/a\\b",
        ] {
            assert!(validate_path("GET", path).is_err(), "{path}");
        }
        assert!(validate_path("CONNECT", "/api/v1/foo").is_err());
    }
    #[test]
    fn bounded_chunked_http_and_sse_multiline() {
        let frame = "id: 123\nevent: changed\ndata: one\ndata: two\n\n";
        let raw = format!("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Type: text/event-stream\r\n\r\n{:x}\r\n{}\r\n0\r\n\r\n", frame.len(), frame);
        let mut body = response(raw.into_bytes()).unwrap();
        let event = body.event().unwrap().unwrap();
        assert_eq!(event.id, "123");
        assert_eq!(event.event, "changed");
        assert_eq!(event.data, "one\ntwo");
        assert!(body.event().unwrap().is_none());
    }
    #[test]
    fn truncated_and_ambiguous_http_are_refused() {
        assert!(
            response(b"HTTP/1.1 200 OK\r\nContent-Length: 20\r\n\r\nshort".to_vec())
                .unwrap()
                .finish()
                .is_err()
        );
        assert!(response(
            b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n".to_vec()
        )
        .is_err());
        assert!(response(
            format!(
                "HTTP/1.1 200 OK\r\nContent-Length: {}\r\n\r\n",
                MAX_BODY + 1
            )
            .into_bytes()
        )
        .unwrap()
        .finish()
        .is_err());
    }
    #[test]
    fn content_length_finishes_without_waiting_for_peer_close() {
        let (client, mut server) = UnixStream::pair().unwrap();
        let (release, wait) = std::sync::mpsc::channel();
        let worker = std::thread::spawn(move || {
            let mut request = Vec::new();
            while !request.ends_with(b"\r\n\r\n") {
                let mut b = [0];
                server.read_exact(&mut b).unwrap();
                request.push(b[0]);
            }
            server
                .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
                .unwrap();
            let _ = wait.recv();
        });
        let mut body = request_stream(client, "GET", "/api/v1/test", None, None).unwrap();
        body.deadline = Instant::now() + Duration::from_millis(50);
        let result = body.finish();
        release.send(()).unwrap();
        worker.join().unwrap();
        assert_eq!(result.unwrap().body, "ok");
    }
    #[test]
    fn stream_deadlines_and_cancellation_are_bounded() {
        let (client, _server) = UnixStream::pair().unwrap();
        let mut reader = BufReader::new(client);
        let start = Instant::now();
        assert!(line(&mut reader, 1024, start + Duration::from_millis(20)).is_err());
        assert!(start.elapsed() < Duration::from_secs(1));
        let (client, mut server) = UnixStream::pair().unwrap();
        let (release, wait) = std::sync::mpsc::channel();
        let worker = std::thread::spawn(move || {
            let mut request = Vec::new();
            while !request.ends_with(b"\r\n\r\n") {
                let mut b = [0];
                server.read_exact(&mut b).unwrap();
                request.push(b[0]);
            }
            server
                .write_all(b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n")
                .unwrap();
            let _ = wait.recv();
        });
        let mut body = request_stream(client, "GET", "/api/v1/events", None, None).unwrap();
        body.cancellation()
            .unwrap()
            .shutdown(std::net::Shutdown::Both)
            .unwrap();
        assert!(body.event().unwrap().is_none());
        release.send(()).unwrap();
        worker.join().unwrap();
    }
    #[test]
    fn actual_unix_socket_peer_is_verified_and_unsafe_modes_are_refused() {
        use std::os::unix::{fs::PermissionsExt, net::UnixListener};
        let runtime = std::env::temp_dir()
            .canonicalize()
            .unwrap()
            .join(format!("sh-peer-{}", std::process::id()));
        crate::private::directory(&runtime).unwrap();
        let socket_path = runtime.join("engine.sock");
        let listener = UnixListener::bind(&socket_path).unwrap();
        fs::set_permissions(&socket_path, fs::Permissions::from_mode(0o600)).unwrap();
        let (release, wait) = std::sync::mpsc::channel();
        let worker = std::thread::spawn(move || {
            let (_peer, _) = listener.accept().unwrap();
            let _ = wait.recv();
        });
        let mut info: libc::proc_bsdinfo = unsafe { std::mem::zeroed() };
        unsafe {
            libc::proc_pidinfo(
                std::process::id() as i32,
                libc::PROC_PIDTBSDINFO,
                0,
                &mut info as *mut _ as *mut _,
                std::mem::size_of_val(&info) as i32,
            )
        };
        let mut endpoint = Endpoint {
            socket_path: socket_path.clone(),
            pid: std::process::id() as i32,
            process_created_millis: info.pbi_start_tvsec * 1000 + info.pbi_start_tvusec / 1000,
            protocol_version: 1,
            build_version: "fixture".into(),
        };
        assert!(connect(&endpoint).is_ok());
        endpoint.process_created_millis += 1;
        assert!(connect(&endpoint).is_err());
        endpoint.process_created_millis -= 1;
        fs::set_permissions(&socket_path, fs::Permissions::from_mode(0o666)).unwrap();
        assert!(connect(&endpoint).is_err());
        release.send(()).unwrap();
        worker.join().unwrap();
        fs::remove_dir_all(runtime).unwrap();
    }
    #[test]
    fn long_home_runtime_fallback_matches_go_and_override_remains_explicit() {
        let cache = PathBuf::from(format!("/Users/{}/Library/Caches", "long-user".repeat(12)));
        let resolved = runtime_path(&cache, None, Path::new("/tmp"), 501).unwrap();
        let digest = Sha256::digest(
            cache
                .join("stackharbor/engine-run")
                .as_os_str()
                .as_encoded_bytes(),
        );
        assert_eq!(
            resolved,
            fs::canonicalize("/tmp").unwrap().join(format!(
                "sh-engine-501-{}",
                digest[..6]
                    .iter()
                    .map(|b| format!("{b:02x}"))
                    .collect::<String>()
            ))
        );
        assert_eq!(
            runtime_path(
                &cache,
                Some(Path::new("/tmp/a/../explicit")),
                Path::new("/tmp"),
                501
            )
            .unwrap(),
            Path::new("/tmp/explicit")
        );
        assert!(runtime_path(&cache, Some(Path::new("relative")), Path::new("/tmp"), 501).is_err());
    }
    #[test]
    fn unsafe_runtime_or_orphan_socket_is_never_treated_as_idle() {
        use std::os::unix::{fs::PermissionsExt, net::UnixListener};
        let root = std::env::temp_dir()
            .canonicalize()
            .unwrap()
            .join(format!("sh-native-unsafe-{}", std::process::id()));
        crate::private::directory(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o755)).unwrap();
        assert!(Engine::existing_in(&root).is_err());
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let _listener = UnixListener::bind(root.join("engine.sock")).unwrap();
        assert!(Engine::existing_in(&root).is_err());
        fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn read_only_missing_endpoint_does_not_create_runtime() {
        let runtime = std::env::temp_dir()
            .canonicalize()
            .unwrap()
            .join(format!("sh-missing-runtime-{}", std::process::id()));
        assert!(!runtime.exists());
        assert!(Engine::existing_in(&runtime).unwrap().is_none());
        assert!(!runtime.exists());
    }
    #[test]
    fn reused_process_identity_is_rejected() {
        let mut endpoint = Endpoint {
            socket_path: PathBuf::new(),
            pid: std::process::id() as i32,
            process_created_millis: 1,
            protocol_version: 1,
            build_version: "test".into(),
        };
        assert!(!process_matches(&endpoint));
        let mut info: libc::proc_bsdinfo = unsafe { std::mem::zeroed() };
        unsafe {
            libc::proc_pidinfo(
                endpoint.pid,
                libc::PROC_PIDTBSDINFO,
                0,
                &mut info as *mut _ as *mut _,
                std::mem::size_of_val(&info) as i32,
            )
        };
        endpoint.process_created_millis =
            info.pbi_start_tvsec * 1000 + info.pbi_start_tvusec / 1000;
        assert!(process_matches(&endpoint));
        endpoint.process_created_millis += 1;
        assert!(!process_matches(&endpoint));
    }
}
