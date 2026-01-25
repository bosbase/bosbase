[BosBase](https://github.com/bosbase/bosbase) is a completely open source alternative to Pocketbase and Supabase. It is a Go backend that provides:

- **AI-driven development**: Use AI to work with Bosbase and bring your creative ideas to life
- **Development platform**: Bosbase is a platform for AI-driven automated operations
- **Complete backend solution**: Start your project with a database, Authentication, instant APIs, Realtime subscriptions, Storage, and Vector and Wasm

## Multi-node deployments with PostgreSQL

BosBase can delegate all database operations to an external [PostgreSQL](https://www.postgresql.org/) instance. When enabled, every BosBase node becomes stateless – you can run multiple application nodes against the same PostgreSQL database while sharing files via object storage.

### Enabling the PostgreSQL driver

- Provision a PostgreSQL server (managed instance or self-hosted). Using a managed service with built-in replication is recommended for production.
- Point BosBase at the database via the new CLI flag:
  ```sh
  bosbase serve --postgres-url postgres://user:pass@my-postgres:5432/bosbase?sslmode=disable
  ```
- Alternatively, set the environment variable `SASSPB_POSTGRES_URL` (useful for container deployments or systemd units).
- Ensure the DSN includes the target database name and credentials. The connection string is forwarded directly to the `pgx` driver.

> [!IMPORTANT]
> When running multiple BosBase instances, you must configure shared object storage (for example S3). Local file storage is disabled automatically when PostgreSQL mode is enabled.

### Quick start with Docker Compose

An example compose file is available at [`docker/docker-compose.postgres.yml`](docker/docker-compose.postgres.yml). It launches a PostgreSQL instance and a BosBase container that points at it. To try it out:

```sh
docker compose -f docker/docker-compose.db.yml up --build
docker compose -f docker/docker-compose.yml up --build
```

The application node will be reachable on `http://localhost:8090`, while PostgreSQL listens on `localhost:5432` (default credentials: `postgres` / `postgres`). Update the compose file to match your production settings.

## API SDK clients

The easiest way to interact with the BosBase Web APIs is to use one of the official SDK

## Overview

### Use as standalone app

You could download the prebuilt executable for your platform from the [Releases page](https://github.com/bosbase/bosbase/releases).
Once downloaded, extract the archive and run `./pocketbase serve` in the extracted directory.

The prebuilt executables are based on the [`examples/base/main.go` file](https://github.com/bosbase/bosbase/blob/master/examples/base/main.go) and comes with the JS VM plugin enabled by default which allows to extend BosBase with JavaScript (_for more details please refer to [Extend with JavaScript](https://pocketbase.io/docs/js-overview/)_).

### Use as a Go framework/toolkit

BosBase is distributed as a regular Go library package which allows you to build
your own custom app specific business logic and still have a single portable executable at the end.

Here is a minimal example:

0. [Install Go 1.23+](https://go.dev/doc/install) (_if you haven't already_)

1. Create a new project directory with the following `main.go` file inside it:
    ```go
    package main

    import (
        "log"

        "github.com/bosbase/bosbase-enterprise"
        "github.com/bosbase/bosbase-enterprise/core"
    )

    func main() {
        app := pocketbase.New()

        app.OnServe().BindFunc(func(se *core.ServeEvent) error {
            // registers new "GET /hello" route
            se.Router.GET("/hello", func(re *core.RequestEvent) error {
                return re.String(200, "Hello world!")
            })

            return se.Next()
        })

        if err := app.Start(); err != nil {
            log.Fatal(err)
        }
    }
    ```

2. To init the dependencies, run `go mod init myapp && go mod tidy`.

3. To start the application, run `go run main.go serve`.

4. To build a statically linked executable, you can run `CGO_ENABLED=0 go build` and then start the created executable with `./myapp serve`.

_For more details please refer to [Extend with Go](https://pocketbase.io/docs/go-overview/)._

### Building and running the repo main.go example

To build the minimal standalone executable, like the prebuilt ones in the releases page, you can simply run `go build` inside the `examples/base` directory:

0. [Install Go 1.23+](https://go.dev/doc/install) (_if you haven't already_)
1. Clone/download the repo
2. Navigate to `examples/base`
3. Run `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build`
   (_https://go.dev/doc/install/source#environment_)
4. Start the created executable by running `./base serve`.

Note that the supported build targets by the pure Go SQLite driver at the moment are:

```
darwin  amd64
darwin  arm64
freebsd amd64
freebsd arm64
linux   386
linux   amd64
linux   arm
linux   arm64
linux   ppc64le
linux   riscv64
linux   s390x
windows amd64
windows arm64
```

### Testing

BosBase comes with mixed bag of unit and integration tests.
To run them, use the standard `go test` command:

```sh
go test ./...
```

Check also the [Testing guide](http://pocketbase.io/docs/testing) to learn how to write your own custom application tests.

## Security

If you discover a security vulnerability within BosBase, please send an e-mail  support@bosbase.com

All reports will be promptly addressed and you'll be credited in the fix release notes.

## Contributing

BosBase is free and open source project licensed under the [MIT License](LICENSE.md).
You are free to do whatever you want with it


 
base on BosBase v0.30.0
build linux product

set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build

chmod 777 base
docker build -t pbsassdb:1 .
