use anyhow::Error;
use axum::{
    Json, Router,
    extract::State,
    http::StatusCode,
    routing::{get, post},
};
use notify::{RecursiveMode, Watcher};
mod pool;
mod postgres;
mod redis;
use pool::WasmPool;
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Instant;
use uuid::Uuid;
use wasmtime::{Config, Engine, Linker, Module};
use wasmtime_wasi::p1::WasiP1Ctx;

#[tokio::main]
async fn main() -> Result<(), Error> {
    let state = AppState::new().await?;

    start_wasm_watcher(state.pool.clone());

    let app = Router::new()
        .route("/health", get(health_handler))
        .route("/run", post(run_handler))
        .with_state(state);

    let listener = tokio::net::TcpListener::bind("0.0.0.0:2678").await?;
    axum::serve(listener, app).await?;
    Ok(())
}

#[derive(Clone)]
struct AppState {
    pool: WasmPool,
}

impl AppState {
    async fn new() -> Result<Self, Error> {
        let mut config = Config::new();
        config.async_support(true);
        config.consume_fuel(true);

        let tune_defaults = std::env::var("BOOSTER_WASMTIME_TUNE_DEFAULTS")
            .ok()
            .as_deref()
            .map(|v| !matches!(v, "0" | "false" | "FALSE" | "no" | "NO"))
            .unwrap_or(true);

        if tune_defaults {
            config.memory_guard_size(65536);
            config.memory_reservation(0);
            config.memory_reservation_for_growth(1048576);
        }

        if let Some(v) = std::env::var("BOOSTER_WASMTIME_MEMORY_GUARD_SIZE")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
        {
            config.memory_guard_size(v);
        }
        if let Some(v) = std::env::var("BOOSTER_WASMTIME_MEMORY_RESERVATION")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
        {
            config.memory_reservation(v);
        }
        if let Some(v) = std::env::var("BOOSTER_WASMTIME_MEMORY_RESERVATION_FOR_GROWTH")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
        {
            config.memory_reservation_for_growth(v);
        }

        let engine = Engine::new(&config)?;
        let wasm_path = default_wasm_path();
        let module = load_best_module(&engine, &wasm_path)?;

        let mut linker: Linker<WasiP1Ctx> = Linker::new(&engine);
        wasmtime_wasi::p1::add_to_linker_async(&mut linker, |cx| cx)?;

        let redis_host = Arc::new(redis::RedisHost::new_from_env().await);
        redis::add_redis_to_linker(&mut linker, redis_host)?;

        let pg_host = Arc::new(postgres::PostgresHost::new_from_env().await);
        postgres::add_postgres_to_linker(&mut linker, pg_host)?;

        let max_concurrency = std::env::var("BOOSTER_POOL_MAX")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .unwrap_or(8);

        let pool = WasmPool::new(engine, Arc::new(linker), module, max_concurrency);
        Ok(Self { pool })
    }
}

#[derive(Deserialize)]
struct RunRequest {
    name: String,
}

#[derive(Serialize)]
struct RunResponse {
    stdout: String,
    stderr: String,
    cost: String,
    trace_id: String,
}

#[derive(Serialize)]
struct HealthResponse {
    status: &'static str,
}

async fn health_handler() -> (StatusCode, Json<HealthResponse>) {
    (StatusCode::OK, Json(HealthResponse { status: "ok" }))
}

async fn run_handler(
    State(state): State<AppState>,
    Json(req): Json<RunRequest>,
) -> Result<Json<RunResponse>, (StatusCode, String)> {
    let trace_id = Uuid::now_v7().simple().to_string();

    let started = Instant::now();
    let (stdout, stderr) = state
        .pool
        .run(req.name)
        .await
        .map_err(|err| {
            eprintln!("/run failed trace_id={trace_id} err={err:?}");
            let msg = err.to_string();
            if msg.contains("cannot create a memfd") {
                let hint = "cannot create a memfd (EPERM): memfd_create was denied. This is usually caused by seccomp/AppArmor/systemd sandboxing or a restricted container environment. If running under a service/container, allow the memfd_create syscall (or relax the sandbox) and retry.";
                (StatusCode::INTERNAL_SERVER_ERROR, format!("{msg} ({hint})"))
            } else {
                (StatusCode::INTERNAL_SERVER_ERROR, msg)
            }
        })?;
    Ok(Json(RunResponse {
        stdout,
        stderr,
        cost: format!("{}ms", started.elapsed().as_millis()),
        trace_id,
    }))
}

fn default_wasm_path() -> String {
    std::env::var("BOOSTER_PATH").unwrap_or_else(|_| {
        let base_dir = "components/target/wasm32-wasip1/debug/";
        format!("{base_dir}")
    })
}

fn list_wasm_candidates(path: &Path) -> Result<Vec<PathBuf>, Error> {
    if path.is_dir() {
        let mut out = Vec::new();
        for entry in std::fs::read_dir(path)? {
            let entry = entry?;
            let p = entry.path();
            if p.extension().and_then(|s| s.to_str()) == Some("wasm") {
                out.push(p);
            }
        }
        Ok(out)
    } else {
        Ok(vec![path.to_path_buf()])
    }
}

fn load_best_module(engine: &Engine, wasm_path: &str) -> Result<Module, Error> {
    let path = Path::new(wasm_path);
    let candidates = list_wasm_candidates(path)?;

    let mut with_mtime: Vec<(std::time::SystemTime, PathBuf)> = Vec::new();
    for p in candidates {
        if let Ok(meta) = std::fs::metadata(&p) {
            if let Ok(mtime) = meta.modified() {
                with_mtime.push((mtime, p));
            } else {
                with_mtime.push((std::time::SystemTime::UNIX_EPOCH, p));
            }
        }
    }

    with_mtime.sort_by(|a, b| b.0.cmp(&a.0));
    for (_mtime, p) in with_mtime {
        match Module::from_file(engine, &p) {
            Ok(m) => return Ok(m),
            Err(e) => {
                eprintln!("Skipping wasm file {:?}: {e}", p);
            }
        }
    }

    Err(anyhow::anyhow!("no valid wasm modules found under {wasm_path}"))
}

fn start_wasm_watcher(pool: WasmPool) {
    let wasm_path = default_wasm_path();
    let watch_root = {
        let p = PathBuf::from(&wasm_path);
        if p.is_dir() {
            p
        } else {
            p.parent().unwrap_or(Path::new(".")).to_path_buf()
        }
    };

    let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel::<()>();

    std::thread::spawn(move || {
        let mut watcher = match notify::recommended_watcher(move |res: notify::Result<notify::Event>| {
            if res.is_ok() {
                let _ = tx.send(());
            }
        }) {
            Ok(w) => w,
            Err(e) => {
                eprintln!("failed to create watcher: {e}");
                return;
            }
        };

        if let Err(e) = watcher.watch(&watch_root, RecursiveMode::NonRecursive) {
            eprintln!("failed to watch {:?}: {e}", watch_root);
            return;
        }

        loop {
            std::thread::sleep(std::time::Duration::from_secs(3600));
        }
    });

    tokio::spawn(async move {
        while rx.recv().await.is_some() {
            tokio::time::sleep(std::time::Duration::from_millis(200)).await;
            while rx.try_recv().is_ok() {}

            match load_best_module(pool.engine(), &wasm_path) {
                Ok(new_module) => {
                    pool.update_module(new_module).await;
                }
                Err(e) => {
                    eprintln!("WASM reload skipped: {e}");
                }
            }
        }
    });
}