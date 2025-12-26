# Redis integration (optional)

Redis support in `booster` is optional.

If Redis is not enabled, `booster` still starts normally and serves HTTP requests, but the `bosbase_redis.*` host functions will return error codes.

## Enable Redis

Redis is disabled by default.

To enable Redis, set `REDIS_URL` in the environment of the `booster` process.

Examples:

```sh
export REDIS_URL=redis://127.0.0.1:6379
cargo run --manifest-path sasspb/booster/Cargo.toml
```

If you prefer to pass host/port without a scheme, `booster` also accepts:

```sh
export REDIS_URL=127.0.0.1:6379
```

## Configure connection pool

`booster` uses a `bb8-redis` connection pool.

Tune pool size:

- `BOOSTER_REDIS_POOL_MAX`
  - default: `32`
  - description: maximum number of Redis connections in the pool

Example:

```sh
export REDIS_URL=redis://127.0.0.1:6379
export BOOSTER_REDIS_POOL_MAX=128
cargo run --manifest-path sasspb/booster/Cargo.toml
```

## WASM host imports

Imported module name:

`bosbase_redis`

Imported functions:

- `redis_set(kptr: i32, klen: i32, vptr: i32, vlen: i32) -> i32`
- `redis_set_ex(kptr: i32, klen: i32, vptr: i32, vlen: i32, ttl_s: i64) -> i32`
- `redis_get(kptr: i32, klen: i32, out_ptr: i32, out_len: i32) -> i32`
- `redis_exists(kptr: i32, klen: i32) -> i32`
- `redis_del(kptr: i32, klen: i32) -> i32`

All pointers/lengths refer to guest linear memory (the export named `memory`). Keys are UTF-8 strings. Values are raw bytes.

## Return codes

When Redis is enabled and reachable, the functions behave as expected.

When Redis is disabled (`REDIS_URL` not set), all functions will fail.

- `redis_set`: `0` on success, negative on error
- `redis_set_ex`: `0` on success, negative on error
- `redis_get`:
  - `>= 0`: number of bytes written to `out_ptr`
  - `-1`: key not found
  - `-2`: output buffer too small
  - `-3`: invalid guest pointer/len or non-UTF8 key
  - `-4`: redis error / connection error (includes "redis disabled")
- `redis_exists`: `1` exists, `0` does not exist, `-1` error
- `redis_del`: `>= 0` number of keys removed, `-1` error

## Testing

The booster repo contains an opt-in test that validates the Redis host-import wiring.

Enable it with:

```sh
export BOOSTER_TEST_REDIS=1
export REDIS_URL=redis://127.0.0.1:6379
cargo test --manifest-path sasspb/booster/Cargo.toml
```
