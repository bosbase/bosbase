use anyhow::Error;
use base64::Engine as _;
use bb8_postgres::bb8;
use bb8_postgres::PostgresConnectionManager;
use serde_json::Value as JsonValue;
use std::collections::BTreeMap;
use std::sync::Arc;
use tokio_postgres::NoTls;
use tokio_postgres::types::Type;
use wasmtime::Linker;
use wasmtime_wasi::p1::WasiP1Ctx;

pub struct PostgresHost {
    pool: Option<bb8::Pool<PostgresConnectionManager<NoTls>>>,
}

impl PostgresHost {
    pub async fn new_from_env() -> Self {
        let pg_url = std::env::var("SASSPB_POSTGRES_URL")
            .or_else(|_| std::env::var("POSTGRES_URL"));

        let pg_url = match pg_url {
            Ok(v) => v,
            Err(_) => {
                // Postgres is optional and disabled by default.
                return Self { pool: None };
            }
        };

        let max_size = std::env::var("BOOSTER_PG_POOL_MAX")
            .ok()
            .and_then(|v| v.parse::<u32>().ok())
            .unwrap_or(16);

        let manager = match PostgresConnectionManager::new_from_stringlike(pg_url, NoTls) {
            Ok(m) => m,
            Err(e) => {
                eprintln!("Postgres disabled: failed to create connection manager: {e}");
                return Self { pool: None };
            }
        };

        let pool = match bb8::Pool::builder().max_size(max_size).build(manager).await {
            Ok(p) => p,
            Err(e) => {
                eprintln!("Postgres disabled: failed to build pool: {e}");
                return Self { pool: None };
            }
        };

        Self { pool: Some(pool) }
    }

    fn disabled_error() -> &'static str {
        "postgres disabled (POSTGRES_URL or SASSPB_POSTGRES_URL not set)"
    }

    pub async fn exec(&self, sql: String) -> Result<u64, String> {
        let Some(pool) = self.pool.as_ref() else {
            return Err(Self::disabled_error().to_string());
        };

        let conn = pool
            .get()
            .await
            .map_err(|e| format!("bb8 pool error: {e}"))?;

        conn.execute(sql.as_str(), &[])
            .await
            .map_err(|e| e.to_string())
    }

    pub async fn query_json(&self, sql: String) -> Result<Vec<JsonValue>, String> {
        let Some(pool) = self.pool.as_ref() else {
            return Err(Self::disabled_error().to_string());
        };

        let conn = pool
            .get()
            .await
            .map_err(|e| format!("bb8 pool error: {e}"))?;

        let rows = conn.query(sql.as_str(), &[]).await.map_err(|e| e.to_string())?;
        let mut out: Vec<JsonValue> = Vec::with_capacity(rows.len());

        for row in rows {
            let mut obj: BTreeMap<String, JsonValue> = BTreeMap::new();
            for (i, col) in row.columns().iter().enumerate() {
                let name = col.name().to_string();
                let ty = col.type_();

                let v = match *ty {
                    Type::BOOL => row
                        .try_get::<_, Option<bool>>(i)
                        .ok()
                        .flatten()
                        .map(JsonValue::from)
                        .unwrap_or(JsonValue::Null),

                    Type::INT2 => row
                        .try_get::<_, Option<i16>>(i)
                        .ok()
                        .flatten()
                        .map(JsonValue::from)
                        .unwrap_or(JsonValue::Null),
                    Type::INT4 => row
                        .try_get::<_, Option<i32>>(i)
                        .ok()
                        .flatten()
                        .map(JsonValue::from)
                        .unwrap_or(JsonValue::Null),
                    Type::INT8 => row
                        .try_get::<_, Option<i64>>(i)
                        .ok()
                        .flatten()
                        .map(JsonValue::from)
                        .unwrap_or(JsonValue::Null),

                    Type::FLOAT4 => row
                        .try_get::<_, Option<f32>>(i)
                        .ok()
                        .flatten()
                        .map(|f| JsonValue::from(f as f64))
                        .unwrap_or(JsonValue::Null),
                    Type::FLOAT8 => row
                        .try_get::<_, Option<f64>>(i)
                        .ok()
                        .flatten()
                        .map(JsonValue::from)
                        .unwrap_or(JsonValue::Null),

                    Type::TEXT | Type::VARCHAR | Type::BPCHAR | Type::NAME => row
                        .try_get::<_, Option<String>>(i)
                        .ok()
                        .flatten()
                        .map(JsonValue::from)
                        .unwrap_or(JsonValue::Null),

                    Type::UUID => row
                        .try_get::<_, Option<uuid::Uuid>>(i)
                        .ok()
                        .flatten()
                        .map(|u| JsonValue::from(u.to_string()))
                        .unwrap_or(JsonValue::Null),

                    Type::JSON | Type::JSONB => row
                        .try_get::<_, Option<JsonValue>>(i)
                        .ok()
                        .flatten()
                        .unwrap_or(JsonValue::Null),

                    Type::BYTEA => {
                        let bytes = row.try_get::<_, Option<Vec<u8>>>(i).ok().flatten();
                        match bytes {
                            Some(b) => {
                                let s = base64::engine::general_purpose::STANDARD.encode(b);
                                JsonValue::from(s)
                            }
                            None => JsonValue::Null,
                        }
                    }

                    _ => row
                        .try_get::<_, Option<String>>(i)
                        .ok()
                        .flatten()
                        .map(JsonValue::from)
                        .unwrap_or(JsonValue::Null),
                };

                obj.insert(name, v);
            }
            out.push(JsonValue::Object(obj.into_iter().collect()));
        }

        Ok(out)
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

#[cfg(test)]
mod postgres_tests {
    use super::*;
    use wasmtime::{Engine, Linker, Module, Store};
    use wasmtime_wasi::{WasiCtx, p1::WasiP1Ctx, p2::pipe::MemoryOutputPipe};

    #[tokio::test]
    async fn test_postgres_host_imports_roundtrip() {
        let enabled = std::env::var("BOOSTER_TEST_POSTGRES")
            .ok()
            .as_deref()
            .map(|v| matches!(v, "1" | "true" | "TRUE" | "yes" | "YES"))
            .unwrap_or(false);
        if !enabled {
            return;
        }

        if std::env::var("POSTGRES_URL").is_err() && std::env::var("SASSPB_POSTGRES_URL").is_err() {
            eprintln!("skipping postgres test: POSTGRES_URL/SASSPB_POSTGRES_URL not set");
            return;
        }

        let pg = Arc::new(PostgresHost::new_from_env().await);
        if pg.pool.is_none() {
            panic!("Postgres expected enabled for test (set BOOSTER_TEST_POSTGRES=1 and ensure POSTGRES_URL/SASSPB_POSTGRES_URL is reachable)");
        }

        let suffix = uuid::Uuid::now_v7().simple().to_string();
        let table = format!("booster_import_test_{suffix}");
        let value = format!("hello-{suffix}");

        let create_sql = format!(
            "CREATE TABLE IF NOT EXISTS {table} (id SERIAL PRIMARY KEY, v TEXT NOT NULL)"
        );
        let insert_sql = format!("INSERT INTO {table} (v) VALUES ('{value}')");
        let select_sql = format!("SELECT v FROM {table} ORDER BY id DESC LIMIT 1");
        let drop_sql = format!("DROP TABLE IF EXISTS {table}");

        let mut config = wasmtime::Config::new();
        config.async_support(true);
        let engine = Engine::new(&config).expect("engine");

        let mut linker: Linker<WasiP1Ctx> = Linker::new(&engine);
        wasmtime_wasi::p1::add_to_linker_async(&mut linker, |cx| cx).expect("add wasi");
        add_postgres_to_linker(&mut linker, pg.clone()).expect("add postgres");

        // Layout:
        // - i32 result len stored at 0
        // - create_sql at 256
        // - insert_sql at 1024
        // - select_sql at 1792
        // - drop_sql at 2560
        // - query output buffer at 4096
        let out_ptr: i32 = 4096;
        let out_len: i32 = 16 * 1024;

        let wat = format!(
            r#"(module
  (import \"bosbase_postgres\" \"pg_exec\" (func $pg_exec (param i32 i32) (result i32)))
  (import \"bosbase_postgres\" \"pg_query\" (func $pg_query (param i32 i32 i32 i32) (result i32)))
  (memory 2)
  (export \"memory\" (memory 0))

  (data (i32.const 256) \"{create_sql}\")
  (data (i32.const 1024) \"{insert_sql}\")
  (data (i32.const 1792) \"{select_sql}\")
  (data (i32.const 2560) \"{drop_sql}\")

  (func $_start (export \"_start\")
    ;; CREATE
    (drop (call $pg_exec (i32.const 256) (i32.const {create_len})))
    ;; INSERT
    (drop (call $pg_exec (i32.const 1024) (i32.const {insert_len})))
    ;; QUERY -> store len at 0
    (i32.store (i32.const 0)
      (call $pg_query (i32.const 1792) (i32.const {select_len}) (i32.const {out_ptr}) (i32.const {out_len})))
    ;; DROP
    (drop (call $pg_exec (i32.const 2560) (i32.const {drop_len})))
  )
)"#,
            create_sql = create_sql,
            insert_sql = insert_sql,
            select_sql = select_sql,
            drop_sql = drop_sql,
            create_len = create_sql.as_bytes().len(),
            insert_len = insert_sql.as_bytes().len(),
            select_len = select_sql.as_bytes().len(),
            drop_len = drop_sql.as_bytes().len(),
            out_ptr = out_ptr,
            out_len = out_len,
        );

        let bytes = wat::parse_str(&wat).expect("wat parse");
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

        let mem = instance
            .get_memory(&mut store, "memory")
            .expect("memory");

        let mut len_buf = [0u8; 4];
        mem.read(&mut store, 0, &mut len_buf).expect("read len");
        let n = i32::from_le_bytes(len_buf);
        assert!(n > 0, "pg_query returned {n}");

        let mut out = vec![0u8; n as usize];
        mem.read(&mut store, out_ptr as usize, &mut out)
            .expect("read out");

        let json: serde_json::Value = serde_json::from_slice(&out).expect("json parse");
        let got = json
            .as_array()
            .and_then(|a| a.first())
            .and_then(|o| o.get("v"))
            .and_then(|v| v.as_str())
            .unwrap_or("");
        assert_eq!(got, value);
    }
}

pub fn add_postgres_to_linker(linker: &mut Linker<WasiP1Ctx>, pg: Arc<PostgresHost>) -> Result<(), Error> {
    let pg_exec_host = pg.clone();
    linker.func_wrap_async(
        "bosbase_postgres",
        "pg_exec",
        move |mut caller: wasmtime::Caller<'_, WasiP1Ctx>, (sptr, slen): (i32, i32)| {
            let pg = pg_exec_host.clone();
            Box::new(async move {
                let sql_bytes = match read_guest_bytes(&mut caller, sptr, slen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-2),
                };
                let sql = match String::from_utf8(sql_bytes) {
                    Ok(s) => s,
                    Err(_) => return Ok(-2),
                };

                match pg.exec(sql).await {
                    Ok(n) => Ok((n.min(i32::MAX as u64)) as i32),
                    Err(_) => Ok(-1),
                }
            })
        },
    )?;

    let pg_query_host = pg;
    linker.func_wrap_async(
        "bosbase_postgres",
        "pg_query",
        move |mut caller: wasmtime::Caller<'_, WasiP1Ctx>,
              (sptr, slen, out_ptr, out_len): (i32, i32, i32, i32)| {
            let pg = pg_query_host.clone();
            Box::new(async move {
                if out_len < 0 {
                    return Ok(-3);
                }

                let sql_bytes = match read_guest_bytes(&mut caller, sptr, slen) {
                    Ok(b) => b,
                    Err(_) => return Ok(-3),
                };
                let sql = match String::from_utf8(sql_bytes) {
                    Ok(s) => s,
                    Err(_) => return Ok(-3),
                };

                let rows = match pg.query_json(sql).await {
                    Ok(r) => r,
                    Err(_) => return Ok(-1),
                };

                let payload = match serde_json::to_vec(&rows) {
                    Ok(v) => v,
                    Err(_) => return Ok(-1),
                };

                if (payload.len() as i32) > out_len {
                    return Ok(-2);
                }
                if write_guest_bytes(&mut caller, out_ptr, &payload).is_err() {
                    return Ok(-3);
                }
                Ok(payload.len() as i32)
            })
        },
    )?;

    Ok(())
}
