# Postgres host functions for WASM

The booster host can expose a small Postgres SQL API to WASI Preview1 guest modules via custom imports.

Postgres is optional and disabled by default. Set `POSTGRES_URL` (or `SASSPB_POSTGRES_URL`) in the booster environment to enable it.

## Build the WASI module (`components`)

The WASI demo module is built from the `booster/components/` crate as a `wasm32-wasip1` binary named `tokio-postgres`.

```sh
rustup target add wasm32-wasip1
cargo build --manifest-path components/Cargo.toml --target wasm32-wasip1
```

Expected output (debug):

`booster/components/target/wasm32-wasip1/debug/tokio-postgres.wasm`

## Run the host (`booster`)

By default, `booster` loads the module from:

`components/target/wasm32-wasip1/debug/tokio-redis.wasm`

To run the Postgres demo module, set `BOOSTER_PATH`:

```sh
POSTGRES_URL='postgres://user:pass@host:5432/dbname' \
BOOSTER_PATH=components/target/wasm32-wasip1/debug/tokio-postgres.wasm \
  cargo run --manifest-path Cargo.toml
```

You can tune the Postgres connection pool size with `BOOSTER_PG_POOL_MAX` (default: `16`).

## Imported module name

`bosbase_postgres`

## Imported functions

- `pg_exec(sql_ptr: i32, sql_len: i32) -> i32`
- `pg_query(sql_ptr: i32, sql_len: i32, out_ptr: i32, out_len: i32) -> i32`

All pointers/lengths refer to guest linear memory (the export named `memory`). SQL is UTF-8.

### `pg_exec`

Executes a single SQL statement.

Return codes:

- `>= 0`: number of rows affected (clamped to `i32::MAX`)
- `-1`: Postgres error / connection error
- `-2`: invalid guest pointer/len or non-UTF8 SQL

### `pg_query`

Executes a SQL query and returns rows as JSON.

The host serializes query results as:

- JSON array of objects: `[{"col": value, ...}, ...]`

Return codes:

- `>= 0`: number of bytes written to `out_ptr` (JSON bytes)
- `-1`: Postgres error / serialization error
- `-2`: output buffer too small
- `-3`: invalid guest pointer/len or non-UTF8 SQL

Notes:

- Values are best-effort decoded. Unsupported types fall back to string (if possible) or `null`.
- `BYTEA` is encoded as base64 string.

## Component demo

The demo guest module is:

- `components/src/tokio-postgres.rs`

It exercises:

- `CREATE TABLE`
- `INSERT`
- `SELECT` (prints returned JSON)
- `DELETE`

## Tests

There is an opt-in integration test that validates the WASM imports with a small WAT guest module.

Environment:

- `BOOSTER_TEST_POSTGRES=1`
- `POSTGRES_URL=...`

Run:

```sh
BOOSTER_TEST_POSTGRES=1 \
POSTGRES_URL='postgres://user:pass@host:5432/dbname' \
  cargo test --manifest-path Cargo.toml
```
