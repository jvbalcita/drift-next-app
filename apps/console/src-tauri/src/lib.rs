use serde::{Deserialize, Serialize};
use std::fs;
use tauri::Manager;

#[derive(Debug, Deserialize)]
struct StoredRuntimeConfig {
    control_plane_address: String,
    operator_id: String,
    service_token: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeConfig {
    control_plane_url: String,
    operator_id: String,
    service_token: String,
}

#[tauri::command]
fn get_runtime_config(app: tauri::AppHandle) -> Result<RuntimeConfig, String> {
    let path = app
        .path()
        .app_config_dir()
        .map_err(|error| format!("resolve application data directory: {error}"))?
        .join("runtime.json");
    let bytes = fs::read(&path).map_err(|error| format!("read runtime configuration: {error}"))?;
    let stored: StoredRuntimeConfig = serde_json::from_slice(&bytes)
        .map_err(|error| format!("parse runtime configuration: {error}"))?;
    if stored.control_plane_address.trim().is_empty()
        || stored.operator_id.trim().is_empty()
        || stored.service_token.trim().is_empty()
    {
        return Err("runtime configuration is incomplete".to_string());
    }
    Ok(RuntimeConfig {
        control_plane_url: format!("http://{}", stored.control_plane_address),
        operator_id: stored.operator_id,
        service_token: stored.service_token,
    })
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![get_runtime_config])
        .setup(|app| {
            if cfg!(debug_assertions) {
                app.handle().plugin(
                    tauri_plugin_log::Builder::default()
                        .level(log::LevelFilter::Info)
                        .build(),
                )?;
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
