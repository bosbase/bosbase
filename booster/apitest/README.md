# booster API test (Python)

This directory contains a simple Python script that calls the running `booster` HTTP server.

## Prerequisites

- `booster` running (listening on `http://127.0.0.1:5678`)
- Python 3.10+

For high-concurrency stress tests (for example `--concurrency 1000`), you may also need:

- **Higher file descriptor limit** for the `booster` process (`ulimit -n`), otherwise you may hit `Too many open files (os error 24)`.
- **Higher `vm.max_map_count`** on the host for Wasmtime under stress, otherwise you may hit `Cannot allocate memory (os error 12)`.
- **Appropriate `BOOSTER_POOL_MAX`**: this controls actual in-host WASM execution concurrency.

## Run

```sh
python3 apitest/test_run.py
```

## Postgres test

This test expects `booster` to be running with the `tokio-postgres` WASM module and Postgres enabled.

If `booster` is running the default `tokio-redis` module, this test will fail (it looks for `pg_exec`/`pg_query` markers in stdout). Ensure `booster` is started with:

```sh
BOOSTER_PATH=components/target/wasm32-wasip1/debug/tokio-postgres.wasm
```

Note: `POSTGRES_URL` (or `SASSPB_POSTGRES_URL`) must be set for the running `booster` process (host). It does not have to be set in the shell where you run these Python scripts.

Run:

```sh
python3 apitest/test_postgres.py
```

Optional flags:

```sh
python3 apitest/test_run.py --name Sparky
python3 apitest/test_run.py --url http://127.0.0.1:5678/run
python3 apitest/test_postgres.py --name Sparky
python3 apitest/test_postgres.py --url http://127.0.0.1:2678/run
```

stress test

```sh
python3 stress_run.py
python3 stress_run.py --total 100000 --concurrency 1000 --timeout 10
```

Postgres stress test

```sh
python3 apitest/stress_postgres.py

python3 apitest/stress_postgres.py --total 100000 --concurrency 200 --timeout 15
```
