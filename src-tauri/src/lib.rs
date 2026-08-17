use std::{
    net::{SocketAddr, TcpStream},
    sync::Mutex,
    time::Duration,
};

use tauri::{Manager, RunEvent};
use tauri_plugin_shell::{process::CommandChild, ShellExt};

struct ProxyProcess(Mutex<Option<CommandChild>>);

fn proxy_is_running() -> bool {
    let address: SocketAddr = "127.0.0.1:15721".parse().expect("valid proxy address");
    TcpStream::connect_timeout(&address, Duration::from_millis(250)).is_ok()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(ProxyProcess(Mutex::new(None)))
        .setup(|app| {
            if !proxy_is_running() {
                let command = app.shell().sidecar("kimi-switch-proxy")?.arg("serve");
                let (mut events, child) = command.spawn()?;
                *app.state::<ProxyProcess>().0.lock().expect("proxy state lock") = Some(child);

                tauri::async_runtime::spawn(async move {
                    while events.recv().await.is_some() {}
                });
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building KimiSwitch");

    app.run(|app_handle, event| {
        if matches!(event, RunEvent::Exit | RunEvent::ExitRequested { .. }) {
            if let Some(child) = app_handle
                .state::<ProxyProcess>()
                .0
                .lock()
                .expect("proxy state lock")
                .take()
            {
                let _ = child.kill();
            }
        }
    });
}
