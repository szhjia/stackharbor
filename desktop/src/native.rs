use crate::{
    engine::{Engine, Event, Response},
    install::{self, Layout, SetupStatus},
    protocol::BUILD_VERSION,
};
use std::{
    collections::HashMap,
    net::Shutdown,
    os::unix::net::UnixStream,
    sync::{
        atomic::{AtomicBool, AtomicU64, Ordering},
        Arc, Mutex,
    },
};
use tauri::{
    ipc::Channel,
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    tray::TrayIconBuilder,
    Manager, WebviewUrl, WebviewWindow, WebviewWindowBuilder,
};
use tauri_plugin_dialog::DialogExt;
#[cfg(debug_assertions)]
struct Smoke {
    root: std::path::PathBuf,
    token: String,
    phase: String,
    background: AtomicBool,
    autostart: AtomicBool,
}
#[cfg(debug_assertions)]
static SMOKE: std::sync::OnceLock<Smoke> = std::sync::OnceLock::new();
#[cfg(debug_assertions)]
fn configure_smoke() -> Result<(), String> {
    use std::os::unix::fs::MetadataExt;
    if !std::env::args().any(|arg| arg == "--native-smoke") {
        return Ok(());
    }
    let root = std::path::PathBuf::from(
        std::env::var_os("STACKHARBOR_NATIVE_SMOKE_DIR")
            .ok_or("An isolated smoke root is required")?,
    );
    let st = std::fs::symlink_metadata(&root).map_err(|e| e.to_string())?;
    let config = root.join("smoke.json");
    let file = std::fs::symlink_metadata(&config).map_err(|e| e.to_string())?;
    if !st.is_dir()
        || st.uid() != unsafe { libc::geteuid() }
        || st.mode() & 0o777 != 0o700
        || std::fs::canonicalize(&root).map_err(|e| e.to_string())? != root
        || crate::protocol::home_dir()? != root.join("home")
        || !file.is_file()
        || file.mode() & 0o777 != 0o600
        || file.len() > 4096
    {
        return Err("Unsafe native smoke fixture; no native service was changed".into());
    }
    let value: serde_json::Value =
        serde_json::from_slice(&std::fs::read(config).map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())?;
    let token = value["token"]
        .as_str()
        .filter(|token| token.len() == 64 && token.bytes().all(|b| b.is_ascii_hexdigit()))
        .ok_or("Invalid smoke token")?;
    let phase = value["phase"]
        .as_str()
        .filter(|p| matches!(*p, "install" | "uninstall"))
        .ok_or("Invalid smoke phase")?;
    SMOKE
        .set(Smoke {
            root,
            token: token.into(),
            phase: phase.into(),
            background: AtomicBool::new(phase == "uninstall"),
            autostart: AtomicBool::new(false),
        })
        .map_err(|_| "Smoke already configured".to_string())
}
#[cfg(debug_assertions)]
pub(crate) fn smoke_active() -> bool {
    SMOKE.get().is_some()
}
#[cfg(debug_assertions)]
pub(crate) fn smoke_service(operation: i32) -> Option<String> {
    SMOKE.get().map(|fixture| {
        let status = if operation >= 10 {
            &fixture.autostart
        } else {
            &fixture.background
        };
        match operation % 10 {
            1 => status.store(true, Ordering::Release),
            2 => status.store(false, Ordering::Release),
            _ => (),
        }
        if status.load(Ordering::Acquire) {
            "enabled"
        } else {
            "not_registered"
        }
        .into()
    })
}
#[cfg(debug_assertions)]
#[tauri::command]
fn desktop_smoke_complete(
    window: WebviewWindow,
    app: tauri::AppHandle,
    token: String,
    mut result: serde_json::Value,
) -> Result<bool, String> {
    use std::{
        io::{ErrorKind, Write},
        os::unix::fs::OpenOptionsExt,
    };
    authorized(&window)?;
    let fixture = SMOKE
        .get()
        .filter(|s| s.token == token)
        .ok_or("Smoke completion is unavailable")?;
    let mut namespaces = app
        .state::<Native>()
        .namespaces
        .lock()
        .map_err(|e| e.to_string())?
        .clone();
    let windows = app.webview_windows();
    namespaces.retain(|label, _| windows.contains_key(label));
    let second = fixture.root.join("namespace-two");
    if fixture.phase == "install"
        && result["ok"].as_bool() == Some(true)
        && !namespaces
            .values()
            .any(|namespace| second == std::path::Path::new(namespace))
    {
        // The first WebView has reached native IPC. Let the fixture driver
        // request the second window while this process stays alive.
        match std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(fixture.root.join("forward-ready"))
        {
            Ok(_) => (),
            Err(error) if error.kind() == ErrorKind::AlreadyExists => (),
            Err(error) => return Err(error.to_string()),
        }
        return Ok(false);
    }
    result["window_namespaces"] = serde_json::to_value(namespaces).map_err(|e| e.to_string())?;
    let bytes = serde_json::to_vec_pretty(&result).map_err(|e| e.to_string())?;
    if bytes.len() > 65536 {
        return Err("Oversized smoke result".into());
    }
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(fixture.root.join("result.json"))
        .map_err(|e| e.to_string())?;
    file.write_all(&bytes).map_err(|e| e.to_string())?;
    file.sync_all().map_err(|e| e.to_string())?;
    quit(&app);
    Ok(true)
}
struct Subscription {
    window: String,
    stream: UnixStream,
}
struct Native {
    engine: Arc<Engine>,
    layout: Layout,
    namespaces: Mutex<HashMap<String, String>>,
    subscriptions: Arc<Mutex<HashMap<u64, Subscription>>>,
    next: AtomicU64,
    quitting: AtomicBool,
}
fn authorized(window: &WebviewWindow) -> Result<(), String> {
    if !window.label().starts_with("main-") {
        return Err("Native integration is limited to the packed main view".into());
    }
    let url = window.url().map_err(|e| e.to_string())?;
    if allowed(&url) {
        Ok(())
    } else {
        Err("Remote content cannot invoke native integration".into())
    }
}
fn allowed(url: &tauri::Url) -> bool {
    (url.scheme() == "tauri"
        && url.host_str() == Some("localhost")
        && url.port().is_none()
        && url.username().is_empty()
        && url.password().is_none())
        || (cfg!(debug_assertions)
            && tauri::is_dev()
            && url.scheme() == "http"
            && url.host_str() == Some("localhost")
            && url.port() == Some(5173)
            && url.username().is_empty()
            && url.password().is_none())
}
fn window_namespace(window: &WebviewWindow, app: &tauri::AppHandle) -> Result<String, String> {
    authorized(window)?;
    app.state::<Native>()
        .namespaces
        .lock()
        .map_err(|e| e.to_string())?
        .get(window.label())
        .cloned()
        .ok_or_else(|| "Native window namespace is unavailable".into())
}
fn selected_namespace(args: &[String]) -> Result<String, String> {
    use std::os::unix::fs::MetadataExt;
    if let Some(index) = args.iter().position(|arg| arg == "--namespace") {
        let path = std::path::Path::new(
            args.get(index + 1)
                .ok_or("--namespace requires an absolute canonical path")?,
        );
        let st = std::fs::symlink_metadata(path).map_err(|e| e.to_string())?;
        if !path.is_absolute()
            || !st.is_dir()
            || st.uid() != unsafe { libc::geteuid() }
            || st.mode() & 0o022 != 0
            || std::fs::canonicalize(path).map_err(|e| e.to_string())? != path
        {
            return Err("Unsafe native namespace; existing windows were preserved".into());
        }
        return Ok(path.to_string_lossy().into_owned());
    }
    Ok(crate::protocol::default_namespace()?
        .to_string_lossy()
        .into_owned())
}
async fn blocking<T: Send + 'static>(
    f: impl FnOnce() -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(f)
        .await
        .map_err(|e| e.to_string())?
}
#[tauri::command]
async fn desktop_request(
    window: WebviewWindow,
    app: tauri::AppHandle,
    method: String,
    path: String,
    body: Option<String>,
) -> Result<Response, String> {
    authorized(&window)?;
    let state = app.state::<Native>();
    let engine = state.engine.clone();
    let layout = state.layout.clone();
    let namespace = window_namespace(&window, &app)?;
    blocking(move || {
        engine.request(
            &install::binary(&layout)?,
            &namespace,
            &method,
            &path,
            body.as_deref(),
        )
    })
    .await
}
#[tauri::command]
async fn desktop_subscribe(
    window: WebviewWindow,
    app: tauri::AppHandle,
    path: String,
    on_event: Channel<Event>,
) -> Result<u64, String> {
    authorized(&window)?;
    let state = app.state::<Native>();
    let engine = state.engine.clone();
    let layout = state.layout.clone();
    let namespace = window_namespace(&window, &app)?;
    let subscriptions = state.subscriptions.clone();
    let id = state.next.fetch_add(1, Ordering::Relaxed);
    // Reserve before connecting so concurrent invocations cannot exceed the cap.
    if subscriptions.lock().map_err(|e| e.to_string())?.len() >= 16 {
        return Err("Too many event subscriptions".into());
    }
    let mut body =
        blocking(move || engine.subscribe(&install::binary(&layout)?, &namespace, &path)).await?;
    authorized(&window)?;
    let mut locked = subscriptions.lock().map_err(|e| e.to_string())?;
    if locked.len() >= 16 {
        return Err("Too many event subscriptions".into());
    }
    locked.insert(
        id,
        Subscription {
            window: window.label().into(),
            stream: body.cancellation()?,
        },
    );
    drop(locked);
    std::thread::spawn(move || {
        loop {
            match body.event() {
                Ok(Some(event)) => {
                    if on_event.send(event).is_err() {
                        break;
                    }
                }
                result => {
                    let message = result
                        .err()
                        .unwrap_or_else(|| "Engine event stream closed".into());
                    let _ = on_event.send(Event {
                        event: "error".into(),
                        id: String::new(),
                        data: message,
                    });
                    break;
                }
            }
        }
        if let Ok(mut subscriptions) = subscriptions.lock() {
            subscriptions.remove(&id);
        }
    });
    Ok(id)
}
#[tauri::command]
fn desktop_unsubscribe(
    window: WebviewWindow,
    app: tauri::AppHandle,
    id: u64,
) -> Result<(), String> {
    authorized(&window)?;
    let state = app.state::<Native>();
    let mut subscriptions = state.subscriptions.lock().map_err(|e| e.to_string())?;
    if subscriptions
        .get(&id)
        .is_some_and(|subscription| subscription.window != window.label())
    {
        return Err("Subscription belongs to another native namespace window".into());
    }
    if let Some(subscription) = subscriptions.remove(&id) {
        let _ = subscription.stream.shutdown(Shutdown::Both);
    }
    Ok(())
}
#[tauri::command]
async fn desktop_status(
    window: WebviewWindow,
    app: tauri::AppHandle,
) -> Result<SetupStatus, String> {
    authorized(&window)?;
    let layout = app.state::<Native>().layout.clone();
    blocking(move || install::status(&layout)).await
}
#[tauri::command]
fn desktop_agreement(window: WebviewWindow) -> Result<install::Agreement, String> {
    authorized(&window)?;
    Ok(install::agreement())
}
#[tauri::command]
async fn desktop_setup(
    window: WebviewWindow,
    app: tauri::AppHandle,
    accept_agreement: bool,
    agreement_hash: String,
    auto_start: bool,
    migrate_conflicts: bool,
    configure_path: bool,
) -> Result<SetupStatus, String> {
    authorized(&window)?;
    let layout = app.state::<Native>().layout.clone();
    blocking(move || {
        install::setup(
            &layout,
            accept_agreement,
            &agreement_hash,
            auto_start,
            migrate_conflicts,
            configure_path,
        )
    })
    .await
}
#[tauri::command]
async fn desktop_set_autostart(
    window: WebviewWindow,
    app: tauri::AppHandle,
    enabled: bool,
) -> Result<SetupStatus, String> {
    authorized(&window)?;
    let layout = app.state::<Native>().layout.clone();
    blocking(move || install::set_autostart(&layout, enabled)).await
}
#[tauri::command]
async fn desktop_pick_workspace(
    window: WebviewWindow,
    app: tauri::AppHandle,
) -> Result<Option<String>, String> {
    authorized(&window)?;
    blocking(move || {
        app.dialog()
            .file()
            .set_title("Choose a StackHarbor workspace")
            .blocking_pick_folder()
            .map(|p| {
                p.into_path()
                    .map_err(|e| e.to_string())
                    .and_then(|p| std::fs::canonicalize(p).map_err(|e| e.to_string()))
                    .map(|p| p.to_string_lossy().into_owned())
            })
            .transpose()
    })
    .await
}
#[tauri::command]
async fn desktop_uninstall(
    window: WebviewWindow,
    app: tauri::AppHandle,
) -> Result<SetupStatus, String> {
    authorized(&window)?;
    let state = app.state::<Native>();
    let layout = state.layout.clone();
    let engine = state.engine.clone();
    let namespace = window_namespace(&window, &app)?;
    blocking(move || install::uninstall(&layout, &engine, &namespace)).await
}
fn cancel_subscriptions(app: &tauri::AppHandle, window: Option<&str>) {
    if let Ok(mut subscriptions) = app.state::<Native>().subscriptions.lock() {
        subscriptions.retain(|_, subscription| {
            if window.is_none_or(|label| label == subscription.window) {
                let _ = subscription.stream.shutdown(Shutdown::Both);
                false
            } else {
                true
            }
        });
    }
}
fn open_external(url: &tauri::Url) {
    if matches!(url.scheme(), "http" | "https") {
        let value = url.to_string();
        std::thread::spawn(move || {
            let _ = install::command(
                std::path::Path::new("/usr/bin/open"),
                &[&value],
                std::time::Duration::from_secs(10),
            );
        });
    }
}
fn open(app: &tauri::AppHandle, namespace: String) -> Result<(), String> {
    use std::hash::{Hash, Hasher};
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    namespace.hash(&mut hasher);
    let label = format!("main-{:016x}", hasher.finish());
    app.set_activation_policy(tauri::ActivationPolicy::Regular)
        .map_err(|e| e.to_string())?;
    if let Some(window) = app.get_webview_window(&label) {
        window.show().map_err(|e| e.to_string())?;
        return window.set_focus().map_err(|e| e.to_string());
    }
    app.state::<Native>()
        .namespaces
        .lock()
        .map_err(|e| e.to_string())?
        .insert(label.clone(), namespace.clone());
    let initialization = format!("Object.defineProperty(window,'__STACKHARBOR_DESKTOP__',{{value:Object.freeze({}),writable:false,configurable:false}});", serde_json::json!({"namespace": namespace}));
    #[cfg(debug_assertions)]
    let initialization = {
        let mut initialization = initialization;
        if let Some(fixture) = SMOKE
            .get()
            .filter(|fixture| namespace == fixture.root.join("namespace").to_string_lossy())
        {
            let javascript = include_str!("smoke.js")
                .replace(
                    "__SMOKE_TOKEN__",
                    &serde_json::to_string(&fixture.token).unwrap(),
                )
                .replace(
                    "__SMOKE_PHASE__",
                    &serde_json::to_string(&fixture.phase).unwrap(),
                );
            initialization.push_str(&format!(
                "window.addEventListener('DOMContentLoaded',()=>{{{javascript}}},{{once:true}});"
            ));
        }
        initialization
    };
    WebviewWindowBuilder::new(app, &label, WebviewUrl::App("index.html".into()))
        .title("StackHarbor")
        .inner_size(1180.0, 800.0)
        .min_inner_size(720.0, 480.0)
        .initialization_script(&initialization)
        .on_navigation(|url| {
            if allowed(url) {
                true
            } else {
                open_external(url);
                false
            }
        })
        .on_new_window(|url, _| {
            open_external(&url);
            tauri::webview::NewWindowResponse::Deny
        })
        .build()
        .map_err(|e| e.to_string())?;
    // Default close destroys the WebView. Tray stays alive; no project or engine
    // lifecycle command is issued by window close, application exit or Cmd-Q.
    Ok(())
}
fn quit(app: &tauri::AppHandle) {
    app.state::<Native>()
        .quitting
        .store(true, Ordering::Release);
    cancel_subscriptions(app, None);
    app.exit(0);
}
fn run() -> Result<(), Box<dyn std::error::Error>> {
    let background = std::env::args().any(|v| v == "--background");
    #[cfg(debug_assertions)]
    configure_smoke()?;
    let context = tauri::generate_context!();
    #[cfg(debug_assertions)]
    let context = {
        let mut context = context;
        if let Some(fixture) = SMOKE.get() {
            use std::hash::{Hash, Hasher};
            let mut hasher = std::collections::hash_map::DefaultHasher::new();
            fixture.root.hash(&mut hasher);
            context.config_mut().identifier =
                format!("com.xahacktech.stackharbor.smoke.{:x}", hasher.finish());
        }
        context
    };
    let builder = tauri::Builder::default();
    #[cfg(debug_assertions)]
    let builder = builder.invoke_handler(tauri::generate_handler![
        desktop_request,
        desktop_subscribe,
        desktop_unsubscribe,
        desktop_status,
        desktop_agreement,
        desktop_setup,
        desktop_set_autostart,
        desktop_pick_workspace,
        desktop_uninstall,
        desktop_smoke_complete
    ]);
    #[cfg(not(debug_assertions))]
    let builder = builder.invoke_handler(tauri::generate_handler![
        desktop_request,
        desktop_subscribe,
        desktop_unsubscribe,
        desktop_status,
        desktop_agreement,
        desktop_setup,
        desktop_set_autostart,
        desktop_pick_workspace,
        desktop_uninstall
    ]);
    let app = builder
        .plugin(tauri_plugin_single_instance::init(|app, args, _| {
            if !args.iter().any(|v| v == "--background") {
                if let Ok(namespace) = selected_namespace(&args) {
                    let _ = open(app, namespace);
                }
            }
        }))
        .plugin(tauri_plugin_dialog::init())
        .setup(move |app| {
            let namespace = selected_namespace(&std::env::args().collect::<Vec<_>>())?;
            app.manage(Native {
                engine: Arc::new(Engine::default()),
                layout: Layout::current()?,
                namespaces: Mutex::new(HashMap::new()),
                subscriptions: Arc::new(Mutex::new(HashMap::new())),
                next: AtomicU64::new(1),
                quitting: AtomicBool::new(false),
            });
            if background {
                app.set_activation_policy(tauri::ActivationPolicy::Accessory);
            }
            let open_item =
                MenuItem::with_id(app, "open", "Open console", true, Some("CmdOrCtrl+O"))?;
            let quit_item =
                MenuItem::with_id(app, "quit", "Quit StackHarbor", true, Some("CmdOrCtrl+Q"))?;
            let separator = PredefinedMenuItem::separator(app)?;
            let application = Submenu::with_items(
                app,
                "StackHarbor",
                true,
                &[&open_item, &separator, &quit_item],
            )?;
            let edit = Submenu::with_items(
                app,
                "Edit",
                true,
                &[
                    &PredefinedMenuItem::undo(app, None)?,
                    &PredefinedMenuItem::redo(app, None)?,
                    &PredefinedMenuItem::cut(app, None)?,
                    &PredefinedMenuItem::copy(app, None)?,
                    &PredefinedMenuItem::paste(app, None)?,
                    &PredefinedMenuItem::select_all(app, None)?,
                ],
            )?;
            app.set_menu(Menu::with_items(app, &[&application, &edit])?)?;
            let tray_menu = Menu::with_items(app, &[&open_item, &separator, &quit_item])?;
            let mut tray = TrayIconBuilder::new()
                .tooltip("StackHarbor")
                .menu(&tray_menu);
            if let Some(icon) = app.default_window_icon() {
                tray = tray.icon(icon.clone());
            }
            tray.build(app)?;
            if !background {
                open(app.handle(), namespace).map_err(std::io::Error::other)?;
            }
            Ok(())
        })
        .on_menu_event(|app, event| match event.id.as_ref() {
            "open" => {
                let _ = open(
                    app,
                    crate::protocol::default_namespace()
                        .map(|p| p.to_string_lossy().into_owned())
                        .unwrap_or_default(),
                );
            }
            "quit" => quit(app),
            _ => (),
        })
        .on_window_event(|window, event| {
            if matches!(event, tauri::WindowEvent::Destroyed) {
                let app = window.app_handle();
                cancel_subscriptions(app, Some(window.label()));
                if let Ok(mut namespaces) = app.state::<Native>().namespaces.lock() {
                    namespaces.remove(window.label());
                    if namespaces.is_empty() {
                        let _ = app.set_activation_policy(tauri::ActivationPolicy::Accessory);
                    }
                }
            }
        })
        .build(context)?;
    app.run(|app, event| match event {
        tauri::RunEvent::ExitRequested { api, .. }
            if !app.state::<Native>().quitting.load(Ordering::Acquire) =>
        {
            api.prevent_exit()
        }
        tauri::RunEvent::Reopen { .. } => {
            let _ = open(
                app,
                crate::protocol::default_namespace()
                    .map(|p| p.to_string_lossy().into_owned())
                    .unwrap_or_default(),
            );
        }
        tauri::RunEvent::Exit => cancel_subscriptions(app, None),
        _ => (),
    });
    Ok(())
}
pub fn main() {
    if std::env::args().any(|v| v == "--desktop-smoke-supported") {
        println!("{}", cfg!(debug_assertions));
        std::process::exit(if cfg!(debug_assertions) { 0 } else { 2 });
    }
    #[cfg(not(debug_assertions))]
    if std::env::args().any(|v| v == "--native-smoke") {
        eprintln!("Native smoke instrumentation is compiled only into debug test hosts");
        std::process::exit(2);
    }
    if std::env::args().any(|v| v == "--desktop-version") {
        println!("{BUILD_VERSION}");
        return;
    }
    if let Err(error) = run() {
        eprintln!("StackHarbor desktop failed: {error}");
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn explicit_namespace_requires_existing_canonical_owned_directory() {
        use std::os::unix::fs::PermissionsExt;
        let path = std::env::temp_dir()
            .canonicalize()
            .unwrap()
            .join(format!("sh-native-namespace-{}", std::process::id()));
        crate::private::directory(&path).unwrap();
        let args = vec![
            "stackharbor".into(),
            "--namespace".into(),
            path.to_string_lossy().into_owned(),
        ];
        assert_eq!(
            super::selected_namespace(&args).unwrap(),
            path.to_string_lossy()
        );
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o777)).unwrap();
        assert!(super::selected_namespace(&args).is_err());
        std::fs::remove_dir(path).unwrap();
        assert!(super::selected_namespace(&["--namespace".into()]).is_err());
    }
    #[test]
    fn production_origin_does_not_grant_remote_native_access() {
        for url in [
            "https://example.com/",
            "http://127.0.0.1:5173/",
            "tauri://evil/",
            "file:///tmp/index.html",
            "tauri://user@localhost/",
        ] {
            assert!(!super::allowed(&tauri::Url::parse(url).unwrap()), "{url}");
        }
        assert!(super::allowed(
            &tauri::Url::parse("tauri://localhost/index.html").unwrap()
        ));
    }
}
