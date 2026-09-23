// Generates the framework list, port ranges, languages and report schemas from config/config.go
// and benchcli-go/report/*.go, with the same script benchcli-uwscpp builds its header with, so
// that a framework or a report field added on the Go side reaches this client on its next build.
use std::path::PathBuf;
use std::process::Command;

fn main() {
    let manifest = PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").unwrap());
    let root = manifest.parent().unwrap();
    let script = root.join("benchcli-uwscpp/generate_metadata.py");
    let out = PathBuf::from(std::env::var("OUT_DIR").unwrap()).join("metadata.json");
    for input in [
        script.clone(),
        root.join("config/config.go"),
        root.join("benchcli-go/report"),
    ] {
        println!("cargo:rerun-if-changed={}", input.display());
    }
    let python = std::env::var("PYTHON").unwrap_or_else(|_| "python3".to_string());
    let status = Command::new(&python)
        .arg(&script)
        .arg(&out)
        .status()
        .unwrap_or_else(|e| panic!("running {python} {}: {e}", script.display()));
    assert!(status.success(), "{} failed", script.display());
}
