use std::process::Command;

fn main() {
    for name in ["BUILD_COMMIT", "BUILD_DATE"] {
        println!("cargo:rerun-if-env-changed={name}");
        let value = std::env::var(name).unwrap_or_else(|_| "development".into());
        println!("cargo:rustc-env={name}={value}");
    }
    let rustc = std::env::var("RUSTC").expect("Cargo supplies RUSTC");
    let version = Command::new(rustc)
        .arg("--version")
        .output()
        .expect("read compiler version");
    assert!(version.status.success(), "rustc --version failed");
    let version = String::from_utf8(version.stdout).expect("UTF-8 compiler version");
    println!("cargo:rustc-env=RUSTC_VERSION={}", version.trim());
}
