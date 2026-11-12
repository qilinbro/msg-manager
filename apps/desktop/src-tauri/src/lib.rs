pub fn run() {
    // sociald runs independently. Closing this window does not stop the backend.
    tauri::Builder::default()
        .run(tauri::generate_context!())
        .expect("failed to run Social Intelligence OS desktop shell");
}
