# Running Go Tests

Use this guide to execute the Go test suite against the configured PostgreSQL instance.

## Prerequisites

- Go 1.23+ installed and available in `PATH`.
- PostgreSQL reachable at the already provisioned DSN `postgres://postgres:postgres@192.168.37.129:5432/pbosbase?sslmode=disable` (exposed in the environment as `SASSPB_POSTGRES_URL`, sometimes labelled `SASSPB_BOSBASEDB_URL` in compose files).
- The database user must be allowed to create and drop schemas; the tests create per-run schemas and clean them up automatically.

## Environment setup

The tests look for `SASSPB_POSTGRES_URL` (or `PB_TEST_POSTGRES_URL`) to connect. If the DSN is not already exported in your shell, set it explicitly:

```sh
export SASSPB_POSTGRES_URL="postgres://postgres:postgres@192.168.37.129:5432/pbosbase?sslmode=disable"
```

## Run the suite

From the repository root, run:

```sh
go test ./...
```

Helpful variants:

- Disable the test cache for repeated runs: `go test -count=1 ./...`
- Run the race detector when debugging concurrency issues: `go test -race ./...`
- Target a single package or test: `go test ./core -run 'TestName'`
