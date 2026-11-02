#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[cfg(not(any(target_os = "macos", target_os = "linux")))]
compile_error!(
    "StackHarbor desktop v1 supports macOS and Linux; Windows backend port is not implemented"
);

mod backend;
mod control;
mod install;
mod origin;
mod private;
mod protocol;

#[cfg(test)]
mod tests;

use backend::Backends;
use protocol::{Request, BUILD_VERSION, PROTOCOL_VERSION};
use std::{
    collections::{hash_map::DefaultHasher, HashMap},
    hash::{Hash, Hasher},
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Mutex,
    },
};
use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    tray::TrayIconBuilder,
    Manager, WebviewUrl, WebviewWindowBuilder,
};
use tauri_plugin_dialog::DialogExt;

struct Master {
    backends: Backends,
    control: Mutex<Option<control::Control>>,
    origins: Mutex<HashMap<String, origin::ConsoleOrigin>>,
    quitting: AtomicBool,
}

fn default_request(background: bool) -> Result<Request, String> {
    let path = private::registry(&protocol::default_namespace()?)?;
    Ok(Request {
        protocol_version: PROTOCOL_VERSION,
        build_version: BUILD_VERSION.into(),
        namespace: path.to_string_lossy().into_owned(),
        background,
        action: String::new(),
    })
}

fn show_error(app: &tauri::AppHandle, error: impl Into<String>) {
    let error = error.into();
    eprintln!("StackHarbor desktop: {error}");
    app.dialog()
        .message(error)
        .title("StackHarbor")
        .show(|_| {});
}

fn open_default(app: &tauri::AppHandle) {
    match default_request(false) {
        Ok(request) => service_request(app.clone(), request),
        Err(e) => show_error(app, e),
    }
}

fn service_request(app: tauri::AppHandle, request: Request) {
    std::thread::spawn(move || match prepare_request(&app, &request) {
        Ok(url) => open_prepared(app, request, url),
        Err(e) if !request.background => show_error(
            &app,
            format!("{e}\n\nProject terminals remain available. Use Open console to retry."),
        ),
        Err(e) => eprintln!("StackHarbor desktop: {e}"),
    });
}

fn prepare_request(app: &tauri::AppHandle, request: &Request) -> Result<tauri::Url, String> {
    let master = app.state::<Master>();
    if master.quitting.load(Ordering::Acquire) {
        return Err("Desktop is quitting; retry after it has exited".into());
    }
    let result = master.backends.ensure(&PathBuf::from(&request.namespace));
    if master.quitting.load(Ordering::Acquire) {
        master.backends.shutdown();
        return Err("Desktop is quitting; project terminals remain available".into());
    }
    result
}

fn open_prepared(app: tauri::AppHandle, request: Request, url: tauri::Url) {
    if request.background {
        return;
    }
    let handle = app.clone();
    let _ = app.run_on_main_thread(move || {
        if let Err(e) = open_window(&handle, &request.namespace, url) {
            show_error(&handle, e);
        }
    });
}

fn external_url(url: &tauri::Url) {
    if !matches!(url.scheme(), "http" | "https") {
        return;
    }
    let command = if cfg!(target_os = "macos") {
        "open"
    } else {
        "xdg-open"
    };
    if let Ok(mut child) = std::process::Command::new(command)
        .arg(url.as_str())
        .spawn()
    {
        std::thread::spawn(move || {
            let _ = child.wait();
        });
    }
}

fn open_window(app: &tauri::AppHandle, namespace: &str, url: tauri::Url) -> Result<(), String> {
    let label = window_label(namespace);
    #[cfg(target_os = "macos")]
    app.set_activation_policy(tauri::ActivationPolicy::Regular)
        .map_err(|e| e.to_string())?;
    if let Some(window) = app.get_webview_window(&label) {
        let guard = app
            .state::<Master>()
            .origins
            .lock()
            .map_err(|e| e.to_string())?
            .get(&label)
            .cloned()
            .ok_or("Console origin guard is unavailable")?;
        if !guard.allows(&url) || guard.loaded_url().is_none() {
            guard.replace_verified(url.clone())?;
            window.navigate(url).map_err(|e| e.to_string())?;
        }
        window.unminimize().map_err(|e| e.to_string())?;
        window.show().map_err(|e| e.to_string())?;
        return window.set_focus().map_err(|e| e.to_string());
    }
    create_window(app, label, url)
}

fn window_label(namespace: &str) -> String {
    let mut hasher = DefaultHasher::new();
    namespace.hash(&mut hasher);
    format!("console-{:016x}", hasher.finish())
}

fn snapshot(app: &tauri::AppHandle) -> protocol::MasterSnapshot {
    let master = app.state::<Master>();
    // Never retain a state lock across native main-thread window queries.
    let origins = master.origins.lock().map(|m| m.clone()).unwrap_or_default();
    protocol::MasterSnapshot {
        gateways: app.state::<Master>().backends.snapshot(),
        windows: app
            .webview_windows()
            .values()
            .map(|w| protocol::WindowSnapshot {
                label: w.label().into(),
                visible: w.is_visible().unwrap_or(false),
                // WKWebView.URL may be nil during failed/provisional loads;
                // wry's synchronous URL accessor currently unwraps that nil.
                // Report only a URL observed in a native completed-load event.
                url: origins
                    .get(w.label())
                    .and_then(origin::ConsoleOrigin::loaded_url)
                    .map(|url| url.to_string())
                    .unwrap_or_default(),
            })
            .collect(),
    }
}

fn create_window(app: &tauri::AppHandle, label: String, url: tauri::Url) -> Result<(), String> {
    let guard = origin::ConsoleOrigin::new(url.clone());
    let allowed = guard.clone();
    let loaded = guard.clone();
    let window = WebviewWindowBuilder::new(app, &label, WebviewUrl::External(url))
        .title("StackHarbor")
        .inner_size(1180.0, 800.0)
        .min_inner_size(720.0, 480.0)
        .on_navigation(move |target| {
            if allowed.allows(target) {
                true
            } else {
                external_url(target);
                false
            }
        })
        .on_page_load(move |_, payload| {
            loaded.page_load(
                payload.url(),
                payload.event() == tauri::webview::PageLoadEvent::Finished,
            );
        })
        .on_new_window(|target, _| {
            external_url(&target);
            tauri::webview::NewWindowResponse::Deny
        })
        .build()
        .map_err(|e| e.to_string())?;
    app.state::<Master>()
        .origins
        .lock()
        .map_err(|e| e.to_string())?
        .insert(label, guard);
    let handle = window.clone();
    window.on_window_event(move |event| {
        if let tauri::WindowEvent::CloseRequested { api, .. } = event {
            api.prevent_close();
            let _ = handle.hide();
        }
    });
    Ok(())
}

fn quit(app: &tauri::AppHandle) {
    let master = app.state::<Master>();
    if master.quitting.swap(true, Ordering::AcqRel) {
        return;
    }
    let handle = app.clone();
    std::thread::spawn(move || {
        let master = handle.state::<Master>();
        if let Ok(mut control) = master.control.lock() {
            control.take();
        }
        master.backends.shutdown();
        handle.exit(0);
    });
}

fn menu_event(app: &tauri::AppHandle, event: tauri::menu::MenuEvent) {
    match event.id.as_ref() {
        "open" => open_default(app),
        "quit" => quit(app),
        #[cfg(target_os = "macos")]
        "install-cli" => match install::cli() {
            Ok(message) => app
                .dialog()
                .message(message)
                .title("StackHarbor CLI")
                .show(|_| {}),
            Err(e) => show_error(app, e),
        },
        _ => {}
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let background = std::env::args().any(|arg| arg == "--background");
    let context = tauri::generate_context!();
    // macOS always retains the application singleton identity, including when
    // a caller selects another runtime path. Runtime overrides cannot create
    // a second resident StackHarbor application.
    #[cfg(target_os = "linux")]
    let context = {
        let mut context = context;
        let runtime = private::runtime_directory()?;
        let mut hasher = DefaultHasher::new();
        runtime.hash(&mut hasher);
        context.config_mut().identifier =
            format!("com.szhjia.stackharbor.instance_{:016x}", hasher.finish());
        context
    };
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, args, _| {
            if !args.iter().any(|arg| arg == "--background") {
                open_default(app);
            }
        }))
        .plugin(tauri_plugin_dialog::init())
        .setup(move |app| {
            let executable = std::env::current_exe()?
                .parent()
                .ok_or("Missing executable directory")?
                .join("stackharbor");
            app.manage(Master {
                backends: Backends::new(executable),
                control: Mutex::new(None),
                origins: Mutex::new(HashMap::new()),
                quitting: AtomicBool::new(false),
            });
            #[cfg(target_os = "macos")]
            if background {
                app.set_activation_policy(tauri::ActivationPolicy::Accessory);
            }
            let open = MenuItem::with_id(app, "open", "Open console", true, Some("CmdOrCtrl+O"))?;
            let exit =
                MenuItem::with_id(app, "quit", "Quit StackHarbor", true, Some("CmdOrCtrl+Q"))?;
            let separator = PredefinedMenuItem::separator(app)?;
            let tray_menu = Menu::with_items(app, &[&open, &separator, &exit])?;
            let application = Submenu::with_items(
                app,
                "StackHarbor",
                true,
                &[
                    &open,
                    #[cfg(target_os = "macos")]
                    &MenuItem::with_id(
                        app,
                        "install-cli",
                        "Install command line tool…",
                        true,
                        None::<&str>,
                    )?,
                    &separator,
                    &exit,
                ],
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
            let mut tray = TrayIconBuilder::new()
                .tooltip("StackHarbor")
                .menu(&tray_menu);
            if let Some(icon) = app.default_window_icon() {
                tray = tray.icon(icon.clone());
            }
            // Launcher and application menu remain usable on Linux desktops
            // that do not provide a system tray.
            if let Err(e) = tray.build(app) {
                eprintln!("System tray unavailable: {e}");
            }
            let control =
                control::Control::start(app.handle().clone()).map_err(std::io::Error::other)?;
            *app.state::<Master>()
                .control
                .lock()
                .map_err(|e| std::io::Error::other(e.to_string()))? = Some(control);
            if !background {
                open_default(app.handle());
            }
            Ok(())
        })
        .on_menu_event(menu_event)
        .build(context)?;
    app.run(|app, event| match event {
        tauri::RunEvent::ExitRequested { api, .. }
            if !app.state::<Master>().quitting.load(Ordering::Acquire) =>
        {
            api.prevent_exit();
            quit(app);
        }
        #[cfg(target_os = "macos")]
        tauri::RunEvent::Reopen { .. } => open_default(app),
        tauri::RunEvent::Exit => app.state::<Master>().backends.shutdown(),
        _ => {}
    });
    Ok(())
}

fn main() {
    if std::env::args().any(|arg| arg == "--desktop-version") {
        println!("{BUILD_VERSION}");
        return;
    }
    if let Err(e) = run() {
        eprintln!("StackHarbor desktop failed: {e}");
        std::process::exit(1);
    }
}
