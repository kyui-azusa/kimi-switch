use std::{
    net::{SocketAddr, TcpStream},
    sync::Mutex,
    time::Duration,
};

use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Manager, RunEvent, WindowEvent,
};
use tauri_plugin_shell::{process::CommandChild, ShellExt};

const MAIN_WINDOW_LABEL: &str = "main";
const TRAY_SHOW_ID: &str = "tray-show";
const TRAY_QUIT_ID: &str = "tray-quit";

struct ProxyProcess(Mutex<Option<CommandChild>>);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum LifecycleAction {
    ShowMainWindow,
    HideMainWindow,
    Exit,
    Ignore,
}

fn tray_menu_action(id: &str) -> LifecycleAction {
    match id {
        TRAY_SHOW_ID => LifecycleAction::ShowMainWindow,
        TRAY_QUIT_ID => LifecycleAction::Exit,
        _ => LifecycleAction::Ignore,
    }
}

fn window_close_action(label: &str) -> LifecycleAction {
    if label == MAIN_WINDOW_LABEL {
        LifecycleAction::HideMainWindow
    } else {
        LifecycleAction::Ignore
    }
}

fn show_main_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
    }
}

fn apply_lifecycle_action(app: &AppHandle, action: LifecycleAction) {
    match action {
        LifecycleAction::ShowMainWindow => show_main_window(app),
        LifecycleAction::HideMainWindow => {
            if let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) {
                let _ = window.hide();
            }
        }
        LifecycleAction::Exit => app.exit(0),
        LifecycleAction::Ignore => {}
    }
}

fn setup_tray(app: &tauri::App) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, TRAY_SHOW_ID, "打开 KimiSwitch", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, TRAY_QUIT_ID, "退出 KimiSwitch", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &quit])?;
    let mut tray = TrayIconBuilder::with_id("main-tray")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .tooltip("KimiSwitch")
        .on_menu_event(|app, event| {
            apply_lifecycle_action(app, tray_menu_action(event.id().as_ref()));
        })
        .on_tray_icon_event(|tray, event| {
            if matches!(
                event,
                TrayIconEvent::Click {
                    button: MouseButton::Left,
                    button_state: MouseButtonState::Up,
                    ..
                }
            ) {
                apply_lifecycle_action(tray.app_handle(), LifecycleAction::ShowMainWindow);
            }
        });
    if let Some(icon) = app.default_window_icon() {
        tray = tray.icon(icon.clone());
    }
    tray.build(app)?;
    Ok(())
}

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
            setup_tray(app)?;
            if !proxy_is_running() {
                let command = app.shell().sidecar("kimi-switch-proxy")?.arg("serve");
                let (mut events, child) = command.spawn()?;
                *app.state::<ProxyProcess>()
                    .0
                    .lock()
                    .expect("proxy state lock") = Some(child);

                tauri::async_runtime::spawn(async move { while events.recv().await.is_some() {} });
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building KimiSwitch");

    app.run(|app_handle, event| {
        if let RunEvent::WindowEvent {
            label,
            event: WindowEvent::CloseRequested { api, .. },
            ..
        } = &event
        {
            let action = window_close_action(label);
            if action == LifecycleAction::HideMainWindow {
                api.prevent_close();
                apply_lifecycle_action(app_handle, action);
            }
        }
        #[cfg(target_os = "macos")]
        if matches!(
            event,
            RunEvent::Reopen {
                has_visible_windows: false,
                ..
            }
        ) {
            apply_lifecycle_action(app_handle, LifecycleAction::ShowMainWindow);
        }
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn closing_main_window_hides_it_instead_of_exiting() {
        assert_eq!(
            window_close_action(MAIN_WINDOW_LABEL),
            LifecycleAction::HideMainWindow
        );
        assert_eq!(window_close_action("settings"), LifecycleAction::Ignore);
    }

    #[test]
    fn tray_open_restores_the_existing_main_window() {
        assert_eq!(
            tray_menu_action(TRAY_SHOW_ID),
            LifecycleAction::ShowMainWindow
        );
    }

    #[test]
    fn only_explicit_tray_quit_exits_the_application() {
        assert_eq!(tray_menu_action(TRAY_QUIT_ID), LifecycleAction::Exit);
        assert_eq!(tray_menu_action("unknown"), LifecycleAction::Ignore);
    }
}
