# Wasmtime memory tuning (booster)

`booster` runs WASI WebAssembly modules using `wasmtime`. Under high concurrency stress tests you may encounter mmap/map-count related failures. `booster` includes a conservative default configuration to improve stability.

## Default behavior

If `BOOSTER_WASMTIME_TUNE_DEFAULTS` is **unset**, `booster` applies the following Wasmtime memory settings:

- `memory_guard_size = 65536`
- `memory_reservation = 0`
- `memory_reservation_for_growth = 1048576`

These values are intended to reduce the chance of failures like:

- `mmap failed to reserve 0x104000000 bytes`

## Disable the tuned defaults

To disable the tuned defaults and use Wasmtime's upstream defaults, set:

```sh
BOOSTER_WASMTIME_TUNE_DEFAULTS=false
```

Accepted false values: `0`, `false`, `FALSE`, `no`, `NO`.

## Explicit override variables

Regardless of `BOOSTER_WASMTIME_TUNE_DEFAULTS`, these environment variables take precedence when set:

- `BOOSTER_WASMTIME_MEMORY_GUARD_SIZE`
- `BOOSTER_WASMTIME_MEMORY_RESERVATION`
- `BOOSTER_WASMTIME_MEMORY_RESERVATION_FOR_GROWTH`

Example:

```sh
BOOSTER_WASMTIME_MEMORY_RESERVATION=0 \
BOOSTER_WASMTIME_MEMORY_GUARD_SIZE=65536 \
BOOSTER_WASMTIME_MEMORY_RESERVATION_FOR_GROWTH=1048576 \
  cargo run
```

## Host sysctl: vm.max_map_count

For mmap-heavy workloads (including Wasmtime under stress), Linux `vm.max_map_count` may need to be increased.

Many distributions default this to `65530`, which can be too low for high-concurrency Wasmtime workloads.

Check current value:

```sh
sysctl vm.max_map_count
# or
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

## Concurrency and ENOMEM (os error 12)

`booster` limits how many WASM requests can execute concurrently via `BOOSTER_POOL_MAX` (default: `8`).

Increasing `BOOSTER_POOL_MAX` increases Wasmtime resource usage (mmap'd regions, address space pressure, and per-instance overhead). Under heavy stress (for example `stress_run.py --concurrency 1000`) setting `BOOSTER_POOL_MAX` too high may trigger errors like:

```
Cannot allocate memory (os error 12)
```

Mitigations:

- **Reduce `BOOSTER_POOL_MAX`**: start with `8` or `16` and scale up gradually.
- **Increase `vm.max_map_count`** (see above), especially on mmap-heavy workloads (for example from the default `65530` to `262144`).
- **Keep Wasmtime tuned defaults enabled** (`BOOSTER_WASMTIME_TUNE_DEFAULTS` unset or true).
- **Reduce client concurrency**: if `BOOSTER_POOL_MAX` is 8–16, a client `--concurrency` of 1000 primarily increases queueing pressure and OS resource usage.
