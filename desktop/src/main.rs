#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]
#[cfg(not(any(target_os = "macos", target_os = "linux")))]
compile_error!("StackHarbor desktop supports macOS and Linux");
#[cfg(target_os = "linux")]
mod backend;
#[cfg(target_os = "linux")]
mod control;
#[cfg(target_os = "linux")]
mod legacy;
#[cfg(any(target_os = "linux", test))]
mod origin;
mod private;
mod protocol;
#[cfg(target_os = "linux")]
pub(crate) use legacy::{open_prepared, prepare_request, quit, snapshot, window_label};
#[cfg(target_os = "macos")]
mod engine;
#[cfg(target_os = "macos")]
mod install;
#[cfg(target_os = "macos")]
mod native;
#[cfg(test)]
mod tests;
fn main() {
    #[cfg(target_os = "linux")]
    legacy::main();
    #[cfg(target_os = "macos")]
    native::main();
}
