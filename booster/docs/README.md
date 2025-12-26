# Booster WASI example (Rust)

This directory documents the small Rust demo under `sasspb/booster/` that runs a WASI WebAssembly module using `wasmtime` + `tokio`.

## Build the WASI module (`components`)

The WASI module is built from the `sasspb/booster/components/` crate as a `wasm32-wasip1` binary named `tokio-redis`.

```sh
rustup target add wasm32-wasip1
cargo build --manifest-path components/Cargo.toml --target wasm32-wasip1
```

Expected output (debug):

`sasspb/booster/components/target/wasm32-wasip1/debug/tokio-redis.wasm`

## Run the host (`booster`)

By default, `booster` loads the module from:

`components/target/wasm32-wasip1/debug/tokio-redis.wasm`

You can override this with `BOOSTER_PATH`:

```sh
BOOSTER_PATH=components/target/wasm32-wasip1/release/tokio-redis.wasm \
  cargo run --manifest-path sasspb/booster/Cargo.toml
```

`booster` starts an HTTP server listening on `0.0.0.0:2678`.

### WASM path monitoring / hot reload

`BOOSTER_PATH` can point to either:

- a single `.wasm` file, or
- a directory containing one or more `.wasm` files

When `BOOSTER_PATH` is a directory, `booster` watches the directory and automatically reloads the active module whenever a `.wasm` file changes.

- **Fault tolerant**: files that fail to compile are skipped.
- **Last-known-good**: if reload fails, `booster` continues serving the previous working module.
- **Selection rule**: the most recently modified valid `.wasm` is used.

### Run WASM over HTTP

`POST /run` accepts JSON:

```json
{ "name": "Sparky" }
```

Example:

```sh
curl -sS -X POST http://127.0.0.1:2678/run \
  -H 'content-type: application/json' \
  -d '{"name":"Sparky"}'
```

Response:

```json
{ "stdout": "...", "stderr": "..." }
```

### Redis host functions for WASM

The booster host exposes a small Redis API to WASI Preview1 guest modules via custom imports.

Redis is optional and disabled by default. Set `REDIS_URL` in the booster environment to enable it.

See `docs/REDIS.md` for detailed Redis configuration and pool tuning.

You can tune the Redis connection pool size with `BOOSTER_REDIS_POOL_MAX` (default: `32`).

Imported module name:

`bosbase_redis`

Imported functions:

- `redis_set(kptr: i32, klen: i32, vptr: i32, vlen: i32) -> i32`
- `redis_set_ex(kptr: i32, klen: i32, vptr: i32, vlen: i32, ttl_s: i64) -> i32`
- `redis_get(kptr: i32, klen: i32, out_ptr: i32, out_len: i32) -> i32`
- `redis_exists(kptr: i32, klen: i32) -> i32`
- `redis_del(kptr: i32, klen: i32) -> i32`

All pointers/lengths refer to guest linear memory (the export named `memory`). Keys are UTF-8 strings. Values are raw bytes.

Return codes:

- `redis_set`: `0` on success, negative on error
- `redis_set_ex`: `0` on success, negative on error
- `redis_get`:
  - `>= 0`: number of bytes written to `out_ptr`
  - `-1`: key not found
  - `-2`: output buffer too small
  - `-3`: invalid guest pointer/len or non-UTF8 key
  - `-4`: redis error / connection error
- `redis_exists`: `1` exists, `0` does not exist, `-1` error
- `redis_del`: `>= 0` number of keys removed, `-1` error

### Stress test / mmap OutOfMemory

When running high concurrency stress tests you may hit a panic like:

```
munmap failed: Os { code: 12, kind: OutOfMemory, message: "Cannot allocate memory" }
```

This is commonly caused by Linux `vm.max_map_count` being too low for mmap-heavy workloads.

Check:

```sh
cat /proc/sys/vm/max_map_count
```

Temporarily increase (until reboot):

```sh
sudo sysctl -w vm.max_map_count=262144
```

Persist across reboots:

```sh
echo "vm.max_map_count=262144" | sudo tee /etc/sysctl.d/99-wasmtime.conf
sudo sysctl --system
```

You can also reduce mmap pressure by tuning Wasmtime memory configuration via env vars:

```sh
export BOOSTER_WASMTIME_MEMORY_GUARD_SIZE=65536
export BOOSTER_WASMTIME_MEMORY_RESERVATION=0
export BOOSTER_WASMTIME_MEMORY_RESERVATION_FOR_GROWTH=1048576
```

### Stress test / Too many open files

If you run the stress tests with high concurrency (for example `--concurrency 1000`) you may hit:

```
Too many open files (os error 24)
```

This means the process hit the Linux per-process file descriptor limit (`ulimit -n`).

Check:

```sh
ulimit -n
```

Temporarily increase for the current shell (then restart `booster` from that shell):

```sh
ulimit -n 65535
```

If running `booster` under systemd, set a higher limit in the service unit (then `daemon-reload` + restart):

```
LimitNOFILE=65535
```

run test
cargo test --manifest-path Cargo.toml


sysctl vm.max_map_count