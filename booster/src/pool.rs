use anyhow::Error;
use std::sync::{
    Arc,
    atomic::{AtomicU64, Ordering},
    Mutex,
};
use tokio::sync::{RwLock, Semaphore, OwnedSemaphorePermit};
use wasmtime::{Engine, Linker, Module, Store};
use wasmtime_wasi::{WasiCtx, WasiCtxBuilder, p1::WasiP1Ctx, p2::pipe::MemoryOutputPipe};

#[derive(Clone)]
pub struct WasmPool {
    engine: Engine,
    linker: Arc<Linker<WasiP1Ctx>>,
    module: Arc<RwLock<Arc<Module>>>,
    generation: Arc<AtomicU64>,
    free: Arc<Mutex<Vec<PooledStore>>>,
    semaphore: Arc<Semaphore>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use tokio::time::Duration;

    fn new_engine() -> Engine {
        let mut config = wasmtime::Config::new();
        config.async_support(true);
        config.consume_fuel(true);
        Engine::new(&config).expect("engine")
    }

    fn new_linker(engine: &Engine) -> Arc<Linker<WasiP1Ctx>> {
        let mut linker = Linker::new(engine);
        wasmtime_wasi::p1::add_to_linker_async(&mut linker, |cx| cx).expect("add wasi");
        Arc::new(linker)
    }

    fn compile_wasi_module(engine: &Engine, prefix: &str) -> Module {
        // Minimal WASI module that prints NAME and exits.
        // We avoid relying on libc/start glue here and instead call fd_write.
        let wat = format!(
            r#"(module
  (import "wasi_snapshot_preview1" "fd_write"
    (func $fd_write (param i32 i32 i32 i32) (result i32)))

  (memory 1)
  (export "memory" (memory 0))

  (data (i32.const 8) "{prefix}")

  (func $_start (export "_start")
    ;; iovec[0] = {{ ptr=8, len={len} }}
    (i32.store (i32.const 0) (i32.const 8))
    (i32.store (i32.const 4) (i32.const {len}))
    ;; fd_write(1, &iovec, 1, &nwritten)
    (drop (call $fd_write (i32.const 1) (i32.const 0) (i32.const 1) (i32.const 20)))
  )
)"#,
            prefix = prefix,
            len = prefix.as_bytes().len(),
        );

        let bytes = wat::parse_str(&wat).expect("wat parse");
        Module::new(engine, bytes).expect("module")
    }

    #[tokio::test]
    async fn test_pool_run_returns_output() {
        let engine = new_engine();
        let linker = new_linker(&engine);
        let module = compile_wasi_module(&engine, "hello");

        let pool = WasmPool::new(engine, linker, module, 8);
        let (stdout, stderr) = pool.run("Sparky".to_owned()).await.expect("run");
        assert!(stdout.contains("hello"));
        assert_eq!(stderr, "");
    }

    #[tokio::test]
    async fn test_update_module_takes_effect() {
        let engine = new_engine();
        let linker = new_linker(&engine);

        let module1 = compile_wasi_module(&engine, "one");
        let module2 = compile_wasi_module(&engine, "two");
        let pool = WasmPool::new(engine, linker, module1, 8);

        let (stdout1, _) = pool.run("A".to_owned()).await.expect("run1");
        assert!(stdout1.contains("one"));

        pool.update_module(module2).await;

        let (stdout2, _) = pool.run("B".to_owned()).await.expect("run2");
        assert!(stdout2.contains("two"));
    }

    #[tokio::test]
    async fn test_concurrency_is_limited() {
        let engine = new_engine();
        let linker = new_linker(&engine);
        let module = compile_wasi_module(&engine, "x");

        let pool = WasmPool::new(engine, linker, module, 2);

        let active = Arc::new(AtomicUsize::new(0));
        let peak = Arc::new(AtomicUsize::new(0));

        let mut tasks = Vec::new();
        for i in 0..10 {
            let pool = pool.clone();
            let active = active.clone();
            let peak = peak.clone();
            tasks.push(tokio::spawn(async move {
                let _lease = pool.lease().await.expect("lease");
                let cur = active.fetch_add(1, Ordering::SeqCst) + 1;
                loop {
                    let prev = peak.load(Ordering::SeqCst);
                    if cur > prev {
                        if peak
                            .compare_exchange(prev, cur, Ordering::SeqCst, Ordering::SeqCst)
                            .is_ok()
                        {
                            break;
                        }
                    } else {
                        break;
                    }
                }
                tokio::time::sleep(Duration::from_millis(100 + (i % 3) as u64 * 10)).await;
                active.fetch_sub(1, Ordering::SeqCst);
            }));
        }

        for t in tasks {
            t.await.expect("join");
        }

        assert!(peak.load(Ordering::SeqCst) <= 2);
    }
}

struct PooledStore {
    generation: u64,
    instantiations: u32,
    store: Store<WasiP1Ctx>,
}

pub struct Lease {
    pool: WasmPool,
    generation: u64,
    instantiations: u32,
    store: Option<Store<WasiP1Ctx>>,
    _permit: OwnedSemaphorePermit,
}

impl WasmPool {
    pub fn new(engine: Engine, linker: Arc<Linker<WasiP1Ctx>>, module: Module, max_concurrency: usize) -> Self {
        let max = max_concurrency.max(1);
        Self {
            engine,
            linker,
            module: Arc::new(RwLock::new(Arc::new(module))),
            generation: Arc::new(AtomicU64::new(0)),
            free: Arc::new(Mutex::new(Vec::new())),
            semaphore: Arc::new(Semaphore::new(max)),
        }
    }

    pub fn engine(&self) -> &Engine {
        &self.engine
    }

    pub async fn update_module(&self, module: Module) {
        *self.module.write().await = Arc::new(module);
        self.generation.fetch_add(1, Ordering::SeqCst);
        self.free.lock().unwrap().clear();
    }

    pub async fn lease(&self) -> Result<Lease, Error> {
        let permit = self.semaphore.clone().acquire_owned().await?;
        let generation = self.generation.load(Ordering::SeqCst);

        let mut free = self.free.lock().unwrap();
        let (store, instantiations) = match free.pop() {
            Some(pooled) if pooled.generation == generation => (pooled.store, pooled.instantiations),
            Some(_) => (Store::new(&self.engine, WasiCtx::builder().build_p1()), 0),
            None => (Store::new(&self.engine, WasiCtx::builder().build_p1()), 0),
        };

        Ok(Lease {
            pool: self.clone(),
            generation,
            instantiations,
            store: Some(store),
            _permit: permit,
        })
    }

    pub async fn run(&self, name: String) -> Result<(String, String), Error> {
        let mut lease = self.lease().await?;

        // Wasmtime has an internal per-Store limit on how many instances can be created.
        // Because we reuse Stores for performance, we must periodically recycle them
        // to avoid long-run failures under stress.
        const MAX_STORE_INSTANTIATIONS: u32 = 1_000;
        if lease.instantiations >= MAX_STORE_INSTANTIATIONS {
            lease.store = Some(Store::new(&self.engine, WasiCtx::builder().build_p1()));
            lease.instantiations = 0;
        }

        let max_output_bytes = std::env::var("BOOSTER_MAX_OUTPUT_BYTES")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .unwrap_or(1 << 20);
        let stdout_pipe = MemoryOutputPipe::new(max_output_bytes);
        let stderr_pipe = MemoryOutputPipe::new(max_output_bytes);

        let wasi = WasiCtxBuilder::new()
            .stdout(stdout_pipe.clone())
            .stderr(stderr_pipe.clone())
            .env("NAME", &name)
            .build_p1();

        let store = lease.store.as_mut().expect("store present");
        *store.data_mut() = wasi;

        store.set_fuel(u64::MAX)?;
        store.fuel_async_yield_interval(Some(10000))?;

        let module = self.module.read().await.clone();
        let instance = match self.linker.instantiate_async(&mut *store, &*module).await {
            Ok(i) => i,
            Err(e) => {
                let msg = e.to_string();
                if msg.contains("instance count too high") {
                    // Recycle store and retry once.
                    *store = Store::new(&self.engine, WasiCtx::builder().build_p1());
                    lease.instantiations = 0;
                    self.linker.instantiate_async(&mut *store, &*module).await?
                } else {
                    return Err(e.into());
                }
            }
        };
        lease.instantiations = lease.instantiations.saturating_add(1);
        instance
            .get_typed_func::<(), ()>(&mut *store, "_start")?
            .call_async(&mut *store, ())
            .await?;

        let out = String::from_utf8_lossy(stdout_pipe.contents().as_ref()).to_string();
        let err = String::from_utf8_lossy(stderr_pipe.contents().as_ref()).to_string();
        Ok((out, err))
    }
}

impl Drop for Lease {
    fn drop(&mut self) {
        let store = match self.store.take() {
            Some(s) => s,
            None => return,
        };

        let mut free = self.pool.free.lock().unwrap();
        free.push(PooledStore {
            generation: self.generation,
            instantiations: self.instantiations,
            store,
        });
    }
}
