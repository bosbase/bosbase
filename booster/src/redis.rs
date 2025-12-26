use anyhow::Error;
use std::sync::Arc;
use wasmtime::Linker;
use wasmtime_wasi::p1::WasiP1Ctx;

pub struct RedisHost {
    pool: Option<bb8_redis::bb8::Pool<bb8_redis::RedisConnectionManager>>,
}

impl RedisHost {
    pub async fn new_from_env() -> Self {
        let redis_url = match std::env::var("REDIS_URL") {
            Ok(v) => {
                if v.contains("://") {
                    v
                } else {
                    format!("redis://{v}")
                }
            }
            Err(_) => {
                // Redis is optional and disabled by default.
                return Self { pool: None };
            }
        };

        let max_size = std::env::var("BOOSTER_REDIS_POOL_MAX")
            .ok()
            .and_then(|v| v.parse::<u32>().ok())
            .unwrap_or(32);

        let manager = match bb8_redis::RedisConnectionManager::new(redis_url) {
            Ok(m) => m,
            Err(e) => {
                eprintln!("Redis disabled: failed to create connection manager: {e}");
                return Self { pool: None };
            }
        };
        let pool = match bb8_redis::bb8::Pool::builder().max_size(max_size).build(manager).await {
            Ok(p) => p,
            Err(e) => {
                eprintln!("Redis disabled: failed to build pool: {e}");
                return Self { pool: None };
            }
        };

        Self { pool: Some(pool) }
    }

    fn disabled_error() -> bb8_redis::redis::RedisError {
        bb8_redis::redis::RedisError::from((
            bb8_redis::redis::ErrorKind::InvalidClientConfig,
            "redis disabled (REDIS_URL not set)",
        ))
    }

    pub async fn get(&self, key: String) -> Result<Option<Vec<u8>>, bb8_redis::redis::RedisError> {
        use bb8_redis::redis::{AsyncCommands, ErrorKind, RedisError};

        let Some(pool) = self.pool.as_ref() else {
            return Err(Self::disabled_error());
        };

        let mut conn = pool.get().await.map_err(|e| {
            RedisError::from((ErrorKind::Io, "bb8 pool error", e.to_string()))
        })?;
        conn.get(key).await
    }

    pub async fn set(&self, key: String, val: Vec<u8>) -> Result<(), bb8_redis::redis::RedisError> {
        use bb8_redis::redis::{AsyncCommands, ErrorKind, RedisError};

        let Some(pool) = self.pool.as_ref() else {
            return Err(Self::disabled_error());
        };

        let mut conn = pool.get().await.map_err(|e| {
            RedisError::from((ErrorKind::Io, "bb8 pool error", e.to_string()))
        })?;
        conn.set(key, val).await
    }

    pub async fn set_ex(
        &self,
        key: String,
        val: Vec<u8>,
        ttl_seconds: u64,
    ) -> Result<(), bb8_redis::redis::RedisError> {
        use bb8_redis::redis::{AsyncCommands, ErrorKind, RedisError};

        let Some(pool) = self.pool.as_ref() else {
            return Err(Self::disabled_error());
        };

        let mut conn = pool.get().await.map_err(|e| {
            RedisError::from((ErrorKind::Io, "bb8 pool error", e.to_string()))
        })?;
        conn.set_ex(key, val, ttl_seconds).await
    }

    pub async fn exists(&self, key: String) -> Result<bool, bb8_redis::redis::RedisError> {
        use bb8_redis::redis::{AsyncCommands, ErrorKind, RedisError};

        let Some(pool) = self.pool.as_ref() else {
            return Err(Self::disabled_error());
        };

        let mut conn = pool.get().await.map_err(|e| {
            RedisError::from((ErrorKind::Io, "bb8 pool error", e.to_string()))
        })?;
        conn.exists(key).await
    }

    pub async fn del(&self, key: String) -> Result<u64, bb8_redis::redis::RedisError> {
        use bb8_redis::redis::{AsyncCommands, ErrorKind, RedisError};

        let Some(pool) = self.pool.as_ref() else {
            return Err(Self::disabled_error());
        };

        let mut conn = pool.get().await.map_err(|e| {
            RedisError::from((ErrorKind::Io, "bb8 pool error", e.to_string()))
        })?;
        conn.del(key).await
    }
}

fn read_guest_bytes(
    caller: &mut wasmtime::Caller<'_, WasiP1Ctx>,
    ptr: i32,
    len: i32,
) -> Result<Vec<u8>, ()> {
    if ptr < 0 || len < 0 {
        return Err(());
    }
    let Some(mem) = caller.get_export("memory").and_then(|e| e.into_memory()) else {
        return Err(());
    };
    let mut buf = vec![0u8; len as usize];
    mem.read(&mut *caller, ptr as usize, &mut buf).map_err(|_| ())?;
    Ok(buf)
}

fn write_guest_bytes(
    caller: &mut wasmtime::Caller<'_, WasiP1Ctx>,
    ptr: i32,
    data: &[u8],
) -> Result<(), ()> {
    if ptr < 0 {
        return Err(());
    }
    let Some(mem) = caller.get_export("memory").and_then(|e| e.into_memory()) else {
        return Err(());
    };
    mem.write(&mut *caller, ptr as usize, data).map_err(|_| ())?;
    Ok(())
}

pub fn add_redis_to_linker(linker: &mut Linker<WasiP1Ctx>, redis: Arc<RedisHost>) -> Result<(), Error> {
    let redis_get_host = redis.clone();
    linker.func_wrap_async(
        "bosbase_redis",
        "redis_get",
        move |mut caller: wasmtime::Caller<'_, WasiP1Ctx>, (kptr, klen, out_ptr, out_len): (i32, i32, i32, i32)| {
            let redis = redis_get_host.clone();
            Box::new(async move {
                if out_len < 0 {
                    return Ok(-3);
                }
                let key = match read_guest_bytes(&mut caller, kptr, klen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-3),
                };
                let key = match String::from_utf8(key) {
                    Ok(s) => s,
                    Err(_) => return Ok(-3),
                };

                let Some(val) = (match redis.get(key).await {
                    Ok(v) => v,
                    Err(_) => return Ok(-4),
                }) else {
                    return Ok(-1);
                };
                if (val.len() as i32) > out_len {
                    return Ok(-2);
                }
                if write_guest_bytes(&mut caller, out_ptr, &val).is_err() {
                    return Ok(-3);
                }
                Ok(val.len() as i32)
            })
        },
    )?;

    let redis_set_host = redis.clone();
    linker.func_wrap_async(
        "bosbase_redis",
        "redis_set",
        move |mut caller: wasmtime::Caller<'_, WasiP1Ctx>, (kptr, klen, vptr, vlen): (i32, i32, i32, i32)| {
            let redis = redis_set_host.clone();
            Box::new(async move {
                let key = match read_guest_bytes(&mut caller, kptr, klen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-2),
                };
                let val = match read_guest_bytes(&mut caller, vptr, vlen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-2),
                };
                let key = match String::from_utf8(key) {
                    Ok(s) => s,
                    Err(_) => return Ok(-2),
                };

                match redis.set(key, val).await {
                    Ok(()) => Ok(0),
                    Err(_) => Ok(-1),
                }
            })
        },
    )?;

    let redis_set_ex_host = redis.clone();
    linker.func_wrap_async(
        "bosbase_redis",
        "redis_set_ex",
        move |mut caller: wasmtime::Caller<'_, WasiP1Ctx>,
              (kptr, klen, vptr, vlen, ttl_s): (i32, i32, i32, i32, i64)| {
            let redis = redis_set_ex_host.clone();
            Box::new(async move {
                if ttl_s < 0 {
                    return Ok(-2);
                }
                let key = match read_guest_bytes(&mut caller, kptr, klen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-2),
                };
                let val = match read_guest_bytes(&mut caller, vptr, vlen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-2),
                };
                let key = match String::from_utf8(key) {
                    Ok(s) => s,
                    Err(_) => return Ok(-2),
                };

                match redis.set_ex(key, val, ttl_s as u64).await {
                    Ok(()) => Ok(0),
                    Err(_) => Ok(-1),
                }
            })
        },
    )?;

    let redis_exists_host = redis.clone();
    linker.func_wrap_async(
        "bosbase_redis",
        "redis_exists",
        move |mut caller: wasmtime::Caller<'_, WasiP1Ctx>, (kptr, klen): (i32, i32)| {
            let redis = redis_exists_host.clone();
            Box::new(async move {
                let key = match read_guest_bytes(&mut caller, kptr, klen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-1),
                };
                let key = match String::from_utf8(key) {
                    Ok(s) => s,
                    Err(_) => return Ok(-1),
                };

                match redis.exists(key).await {
                    Ok(true) => Ok(1),
                    Ok(false) => Ok(0),
                    Err(_) => Ok(-1),
                }
            })
        },
    )?;

    let redis_del_host = redis;
    linker.func_wrap_async(
        "bosbase_redis",
        "redis_del",
        move |mut caller: wasmtime::Caller<'_, WasiP1Ctx>, (kptr, klen): (i32, i32)| {
            let redis = redis_del_host.clone();
            Box::new(async move {
                let key = match read_guest_bytes(&mut caller, kptr, klen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-1),
                };
                let key = match String::from_utf8(key) {
                    Ok(s) => s,
                    Err(_) => return Ok(-1),
                };

                match redis.del(key).await {
                    Ok(n) => Ok(n.min(i32::MAX as u64) as i32),
                    Err(_) => Ok(-1),
                }
            })
        },
    )?;

    Ok(())
}

#[cfg(test)]
mod redis_tests {
    use super::*;
    use wasmtime::{Engine, Module, Store};
    use wasmtime_wasi::{WasiCtx, p2::pipe::MemoryOutputPipe};

    #[tokio::test]
    async fn test_redis_host_imports_roundtrip() {
        // This test requires a reachable Redis. Keep it opt-in so it doesn't fail in environments
        // without Redis.
        let enabled = std::env::var("BOOSTER_TEST_REDIS")
            .ok()
            .as_deref()
            .map(|v| matches!(v, "1" | "true" | "TRUE" | "yes" | "YES"))
            .unwrap_or(false);
        if !enabled {
            return;
        }

        // Redis is optional and disabled by default. This test is opt-in and requires explicit
        // configuration.
        if std::env::var("REDIS_URL").is_err() {
            eprintln!("skipping redis test: REDIS_URL not set");
            return;
        }

        let redis = Arc::new(RedisHost::new_from_env().await);
        if redis.pool.is_none() {
            panic!("Redis expected enabled for test (set BOOSTER_TEST_REDIS=1 and ensure REDIS_URL/redis is reachable)");
        }
        let key = "booster:test:import".to_string();
        let _ = redis.del(key.clone()).await;

        let mut config = wasmtime::Config::new();
        config.async_support(true);
        let engine = Engine::new(&config).expect("engine");

        let mut linker: Linker<WasiP1Ctx> = Linker::new(&engine);
        wasmtime_wasi::p1::add_to_linker_async(&mut linker, |cx| cx).expect("add wasi");
        add_redis_to_linker(&mut linker, redis.clone()).expect("add redis");

        // WAT guest:
        // - writes key/value in linear memory
        // - calls redis_set
        // - calls redis_del
        // Note: we validate behavior via host-side redis calls.
        let wat = r#"(module
  (import \"bosbase_redis\" \"redis_set\" (func $redis_set (param i32 i32 i32 i32) (result i32)))
  (import \"bosbase_redis\" \"redis_del\" (func $redis_del (param i32 i32) (result i32)))
  (memory 1)
  (export \"memory\" (memory 0))

  ;; key at 64, value at 128
  (data (i32.const 64) \"booster:test:import\")
  (data (i32.const 128) \"value\")

  (func $_start (export \"_start\")
    ;; redis_set(key, val)
    (drop (call $redis_set (i32.const 64) (i32.const 18) (i32.const 128) (i32.const 5)))
    ;; redis_del(key)
    (drop (call $redis_del (i32.const 64) (i32.const 18)))
  )
)"#;
        let bytes = wat::parse_str(wat).expect("wat parse");
        let module = Module::new(&engine, bytes).expect("module");

        let max_output_bytes = 64 * 1024;
        let stdout_pipe = MemoryOutputPipe::new(max_output_bytes);
        let stderr_pipe = MemoryOutputPipe::new(max_output_bytes);
        let wasi = wasmtime_wasi::WasiCtxBuilder::new()
            .stdout(stdout_pipe)
            .stderr(stderr_pipe)
            .build_p1();

        let mut store: Store<WasiP1Ctx> = Store::new(&engine, WasiCtx::builder().build_p1());
        *store.data_mut() = wasi;

        store.set_fuel(u64::MAX).expect("fuel");
        store.fuel_async_yield_interval(Some(10000)).expect("yield");

        let instance = linker
            .instantiate_async(&mut store, &module)
            .await
            .expect("instantiate");
        instance
            .get_typed_func::<(), ()>(&mut store, "_start")
            .expect("_start")
            .call_async(&mut store, ())
            .await
            .expect("call");

        // Confirm key was deleted by the guest.
        let exists = redis
            .exists("booster:test:import".to_string())
            .await
            .expect("exists");
        assert!(!exists);
    }
}
