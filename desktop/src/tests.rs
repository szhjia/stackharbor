use crate::{
    private,
    protocol::{BackendReady, BUILD_VERSION},
};
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::PathBuf,
    sync::atomic::{AtomicUsize, Ordering},
};

fn dir() -> PathBuf {
    static NEXT: AtomicUsize = AtomicUsize::new(0);
    let root = std::env::temp_dir().canonicalize().unwrap().join(format!(
        "sh-desktop-unit-{}-{}",
        std::process::id(),
        NEXT.fetch_add(1, Ordering::Relaxed)
    ));
    private::directory(&root).unwrap()
}

#[test]
fn readiness_requires_matching_build_namespace_and_exact_loopback() {
    let namespace = dir();
    let mut ready = BackendReady {
        protocol_version: 1,
        build_version: BUILD_VERSION.into(),
        session_protocol: 1,
        namespace: namespace.to_string_lossy().into_owned(),
        url: "http://127.0.0.1:16800/".into(),
        owned: true,
        error: None,
    };
    assert!(ready.validate(&namespace).is_ok());
    for url in [
        "https://127.0.0.1:16800/",
        "http://localhost:16800/",
        "http://127.0.0.1:16800/evil",
        "http://user@127.0.0.1:16800/",
        "http://127.0.0.1:16800/#token=x",
        "http://127.0.0.1:16800/?x=y",
    ] {
        ready.url = url.into();
        assert!(ready.validate(&namespace).is_err(), "{url}");
    }
    ready.url = "http://127.0.0.1:16800/".into();
    ready.build_version = "old".into();
    assert!(ready.validate(&namespace).is_err());
    ready.build_version = BUILD_VERSION.into();
    ready.session_protocol = 99;
    assert!(ready.validate(&namespace).is_err());
    fs::remove_dir(namespace).unwrap();
}

#[test]
fn control_rejects_incompatible_versions_and_unsafe_namespace() {
    let namespace = dir();
    let mut request = crate::protocol::Request {
        protocol_version: 1,
        build_version: BUILD_VERSION.into(),
        namespace: namespace.to_string_lossy().into_owned(),
        background: true,
        action: String::new(),
    };
    assert!(crate::control::validate(&request).is_ok());
    request.protocol_version = 99;
    assert!(crate::control::validate(&request).is_err());
    request.protocol_version = 1;
    request.action = "execute-arbitrary-command".into();
    assert!(crate::control::validate(&request).is_err());
    request.action.clear();
    fs::set_permissions(&namespace, fs::Permissions::from_mode(0o777)).unwrap();
    assert!(crate::control::validate(&request).is_err());
    fs::remove_dir(namespace).unwrap();
}

#[test]
fn private_directory_rejects_symlink_without_changing_target() {
    let namespace = dir();
    let link = namespace.with_extension("link");
    std::os::unix::fs::symlink(&namespace, &link).unwrap();
    assert!(private::directory(&link).is_err());
    assert_eq!(
        fs::metadata(&namespace).unwrap().permissions().mode() & 0o777,
        0o700
    );
    fs::remove_file(link).unwrap();
    fs::remove_dir(namespace).unwrap();
}

#[test]
fn console_origin_is_exact() {
    let origin = tauri::Url::parse("http://127.0.0.1:16800/").unwrap();
    assert!(crate::protocol::same_origin(
        &origin,
        &origin.join("workspaces?q=x").unwrap()
    ));
    assert!(!crate::protocol::same_origin(
        &origin,
        &tauri::Url::parse("http://127.0.0.1:16801/").unwrap()
    ));
    assert!(!crate::protocol::same_origin(
        &origin,
        &tauri::Url::parse("file:///tmp/a").unwrap()
    ));
}

#[test]
fn verified_replacement_updates_existing_navigation_guard() {
    let old = tauri::Url::parse("http://127.0.0.1:16800/").unwrap();
    let replacement = tauri::Url::parse("http://127.0.0.1:16801/").unwrap();
    let guard = crate::origin::ConsoleOrigin::new(old.clone());
    let navigation_callback = guard.clone();
    assert!(guard.loaded_url().is_none());
    guard.page_load(&old, true);
    assert_eq!(guard.loaded_url(), Some(old.clone()));
    assert!(!navigation_callback.allows(&replacement));
    guard.replace_verified(replacement.clone()).unwrap();
    assert!(guard.loaded_url().is_none());
    // Late completion from the old gateway cannot claim the replacement loaded.
    guard.page_load(&old, true);
    assert!(guard.loaded_url().is_none());
    guard.page_load(&replacement, false);
    assert!(guard.loaded_url().is_none());
    guard.page_load(&replacement, true);
    assert_eq!(guard.loaded_url(), Some(replacement.clone()));
    assert!(navigation_callback.allows(&replacement.join("workspaces").unwrap()));
    assert!(!navigation_callback.allows(&old));
    assert!(!navigation_callback.allows(&tauri::Url::parse("https://example.com/").unwrap()));
}

#[test]
fn readable_registry_is_compatible_but_runtime_stays_private() {
    let root = dir();
    fs::set_permissions(&root, fs::Permissions::from_mode(0o755)).unwrap();
    assert!(private::registry(&root).is_ok());
    assert!(private::directory(&root).is_err());
    fs::set_permissions(&root, fs::Permissions::from_mode(0o777)).unwrap();
    assert!(private::registry(&root).is_err());
    fs::remove_dir(root).unwrap();
}
