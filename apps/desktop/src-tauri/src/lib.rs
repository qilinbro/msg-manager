#[tauri::command]
fn open_finfold_handoff(url: String) -> Result<(), String> {
    let parsed = tauri::Url::parse(&url).map_err(|_| "Finfold 导入链接无效。")?;
    let ticket = parsed.fragment().unwrap_or_default();
    if parsed.scheme() != "https"
        || !matches!(
            parsed.host_str(),
            Some("www.finfold.app" | "staging.finfold.app")
        )
        || parsed.port().is_some()
        || !parsed.username().is_empty()
        || parsed.password().is_some()
        || parsed.path() != "/workbench/import"
        || parsed.query().is_some()
        || ticket.len() != 43
        || !ticket
            .bytes()
            .all(|ch| ch.is_ascii_alphanumeric() || ch == b'_' || ch == b'-')
    {
        return Err("Finfold 导入链接无效。".into());
    }

    #[cfg(target_os = "windows")]
    let mut command = {
        let root = std::env::var_os("SystemRoot").ok_or("无法定位系统浏览器。")?;
        let handler = std::path::PathBuf::from(root)
            .join("System32")
            .join("rundll32.exe");
        let mut command = std::process::Command::new(handler);
        command.arg("url.dll,FileProtocolHandler");
        command
    };
    #[cfg(target_os = "macos")]
    let mut command = std::process::Command::new("/usr/bin/open");
    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    let mut command = std::process::Command::new("xdg-open");

    command
        .arg(parsed.as_str())
        .spawn()
        .map(|_| ())
        .map_err(|_| "无法打开系统浏览器。".into())
}

pub fn run() {
    // sociald runs independently. Closing this window does not stop the backend.
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![open_finfold_handoff])
        .run(tauri::generate_context!())
        .expect("failed to run Social Intelligence OS desktop shell");
}
