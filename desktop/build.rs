fn main() {
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        let out = std::path::PathBuf::from(std::env::var_os("OUT_DIR").unwrap());
        let target = std::env::var("TARGET").unwrap();
        let architecture = if target.starts_with("aarch64") {
            "arm64"
        } else {
            "x86_64"
        };
        let swift_target = format!("{architecture}-apple-macosx14.0");
        let status = std::process::Command::new("xcrun")
            .args([
                "swiftc",
                "-parse-as-library",
                "-emit-object",
                "-O",
                "-target",
                &swift_target,
                "src/service.swift",
                "-o",
            ])
            .arg(out.join("service.o"))
            .status()
            .expect("Xcode swiftc is required for ServiceManagement");
        assert!(
            status.success(),
            "ServiceManagement bridge compilation failed"
        );
        let status = std::process::Command::new("ar")
            .arg("crs")
            .arg(out.join("libstackharbor_service.a"))
            .arg(out.join("service.o"))
            .status()
            .unwrap();
        assert!(status.success(), "ServiceManagement bridge archive failed");
        println!("cargo:rustc-link-search=native={}", out.display());
        println!("cargo:rustc-link-search=native=/usr/lib/swift");
        println!("cargo:rustc-link-lib=static=stackharbor_service");
        println!("cargo:rustc-link-lib=framework=ServiceManagement");
        println!("cargo:rustc-link-lib=dylib=swiftCore");
        println!("cargo:rustc-link-lib=dylib=swiftFoundation");
        println!("cargo:rerun-if-changed=src/service.swift");
    }
    tauri_build::build()
}
