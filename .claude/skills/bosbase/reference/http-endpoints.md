# HTTP endpoints

Generated from the live router of a running BosBase server (same data as the `api_endpoints` MCP tool). Access levels:

- `public`: no token required at the route level (for records, the collection API rules decide what is allowed; some handlers add their own checks, noted in the table).
- `user`: any authenticated record (regular user or superuser).
- **superuser**: superuser token required.

Total: 176 endpoints. Request/response details: the `api_docs` MCP tool or `apidocs/api-reference/`.

## Health and settings

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/health` | public | Checks whether the API is healthy. The endpoint is public. If a valid superuser token is supplied, the response includes extra operational diagnostics. |
| GET | `/api/settings` | **superuser** | Returns the current application settings. Requires superuser authentication. |
| PATCH | `/api/settings` | **superuser** | Updates application settings. Requires superuser authentication. The submitted body is bound on top of the current settings object and then saved. |
| POST | `/api/settings/apple/generate-client-secret` | **superuser** | Generates a Sign in with Apple client secret JWT. Requires superuser authentication. |
| POST | `/api/settings/test/email` | **superuser** | Sends a test email for one of the configured auth email templates. Requires superuser authentication. |
| POST | `/api/settings/test/s3` | **superuser** | Tests the configured S3 filesystem by uploading and deleting a temporary object. Requires superuser authentication. |

## Authentication

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/collections/{collection}/auth-methods` | public | Returns enabled auth methods for an auth collection. Public endpoint. |
| POST | `/api/collections/{collection}/auth-refresh` | user (checked by the handler) | Refreshes or validates the current auth token for the same auth collection. Requires same auth collection token. |
| POST | `/api/collections/{collection}/auth-with-oauth2` | public | Authenticates or signs up a record using an OAuth2 provider. Public endpoint; a current auth token may be supplied for linking flows. |
| POST | `/api/collections/{collection}/auth-with-otp` | public | Authenticates with an OTP id and OTP password/code. Public endpoint. |
| POST | `/api/collections/{collection}/auth-with-password` | public | Authenticates an auth record using password auth. Public endpoint. |
| POST | `/api/collections/{collection}/auth-with-token` | public | Authenticates using a previously bound custom token. |
| POST | `/api/collections/{collection}/bind-token` | public | Binds a custom token to an auth record after verifying email and password. Public endpoint. |
| POST | `/api/collections/{collection}/confirm-email-change` | public | Confirms an email change token and verifies the current password. |
| POST | `/api/collections/{collection}/confirm-password-reset` | public | Confirms a password reset token and sets a new password. |
| POST | `/api/collections/{collection}/confirm-verification` | public | Confirms an email verification token. |
| POST | `/api/collections/{collection}/impersonate/{id}` | **superuser** | Creates an auth token for another auth record. Requires superuser authentication. |
| POST | `/api/collections/{collection}/request-email-change` | user (checked by the handler) | Requests an email change for the current auth record. Requires same auth collection token. |
| POST | `/api/collections/{collection}/request-otp` | public | Creates and sends an OTP for an auth record email. Public endpoint. |
| POST | `/api/collections/{collection}/request-password-reset` | public | Sends a password reset email. Public endpoint. |
| POST | `/api/collections/{collection}/request-verification` | public | Sends a verification email. Public endpoint. |
| POST | `/api/collections/{collection}/unbind-token` | public | Removes a custom token binding after verifying email and password. |
| GET | `/api/oauth2-redirect` | public | See `api_docs`. |
| POST | `/api/oauth2-redirect` | public | Receives OAuth2 provider callbacks and forwards the callback data to a realtime client subscribed to `@oauth2`. The endpoint redirects to the bundled UI success/failure … |

## Collections

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/collections` | **superuser** | Lists collections. Requires superuser authentication. |
| POST | `/api/collections` | **superuser** | Creates a collection. Requires superuser authentication. |
| PUT | `/api/collections/import` | **superuser** | Imports multiple collection definitions. Requires superuser authentication. |
| GET | `/api/collections/meta/scaffolds` | **superuser** | Superuser only. Returns the default `base`, `auth` and `view` collection scaffolds (default fields, indexes and options) keyed by collection type. Useful as a template … |
| GET | `/api/collections/schemas` | **superuser** | Superuser only. Returns `{"collections": [...]}` with the simplified schema of every collection. |
| POST | `/api/collections/sql` | **superuser** | Legacy alias of `POST /api/collections/sql/import`. |
| POST | `/api/collections/sql/import` | **superuser** | Creates/registers collections from SQL table definitions. Requires superuser authentication. If `sql` is provided, it is executed before registering the collection. |
| POST | `/api/collections/sql/tables` | **superuser** | Registers existing SQL tables as collections. Requires superuser authentication. |
| DELETE | `/api/collections/{collection}` | **superuser** | Deletes a collection. Requires superuser authentication. |
| GET | `/api/collections/{collection}` | **superuser** | Returns one collection by id or name. Requires superuser authentication. |
| PATCH | `/api/collections/{collection}` | **superuser** | Updates a collection by id or name. Requires superuser authentication. |
| GET | `/api/collections/{collection}/schema` | **superuser** | Superuser only. Returns a simplified schema of one collection (name, type and fields with their types and options), suitable for AI/code generation. |
| DELETE | `/api/collections/{collection}/truncate` | **superuser** | Deletes all records in a collection without deleting the collection. Requires superuser authentication. |

## Records

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/collections/{collection}/records` | public | Lists records in a collection. |
| POST | `/api/collections/{collection}/records` | public | Creates a record. JSON and `multipart/form-data` are supported. Use multipart for file fields. |
| GET | `/api/collections/{collection}/records/count` | public | Returns only the number of records matching the list rule and optional filter. |
| POST | `/api/collections/{collection}/records/search` | public | Performs PostgreSQL/pgvector nearest-neighbor search against a `vector` field in a normal record collection. The endpoint enforces the collection `listRule` and returns … |
| DELETE | `/api/collections/{collection}/records/{id}` | public | Deletes a record. |
| GET | `/api/collections/{collection}/records/{id}` | public | Returns a single record by id. |
| PATCH | `/api/collections/{collection}/records/{id}` | public | Updates a record. JSON and `multipart/form-data` are supported. |

## Files

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| POST | `/api/files/token` | user | Creates a short-lived file token for the currently authenticated record. Requires any valid auth token. |
| GET | `/api/files/{collection}/{recordId}/{filename}` | public | Downloads a stored record file. Access is public only if collection file/view rules allow it; otherwise a valid auth/file token is required. |

## Realtime and pub/sub

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| POST | `/api/realtime` | public | Sets or replaces subscriptions for an existing realtime SSE client. |

## Batch, GraphQL and SQL

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| POST | `/api/batch` | public | Runs multiple internal record mutation requests in one transaction. |
| POST | `/api/graphql` | **superuser** (checked by the handler) | Executes GraphQL queries and mutations over record collections. Auth is optional but collection rules are enforced. |
| POST | `/api/sql/execute` | **superuser** | Executes a raw SQL statement. Requires superuser authentication. |

## Logs, backups and cron

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/backups` | **superuser** | Lists backup files. Requires superuser authentication. |
| POST | `/api/backups` | **superuser** | Creates a new backup. Requires superuser authentication. |
| POST | `/api/backups/upload` | **superuser** | Uploads a backup archive. Requires superuser authentication. |
| DELETE | `/api/backups/{key}` | **superuser** | Deletes a backup file. Requires superuser authentication. |
| GET | `/api/backups/{key}` | **superuser** file token (`?token=`, from `POST /api/files/token`) | Downloads a backup file. Requires a valid superuser file token in the `token` query parameter. |
| POST | `/api/backups/{key}/restore` | **superuser** | Restores the selected backup. Requires superuser authentication. |
| GET | `/api/crons` | **superuser** | Lists registered cron jobs. Requires superuser authentication. |
| POST | `/api/crons/{id}` | **superuser** | Runs a cron job immediately. Requires superuser authentication. |
| GET | `/api/logs` | **superuser** | Lists request/activity logs. Requires superuser authentication. |
| GET | `/api/logs/stats` | **superuser** | Returns aggregated log statistics. Requires superuser authentication. |
| GET | `/api/logs/{id}` | **superuser** | Returns one log entry by id. Requires superuser authentication. |

## Vectors

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/vectors/collections` | **superuser** | Lists vector collections and document counts. |
| DELETE | `/api/vectors/collections/{name}` | **superuser** | Deletes a vector collection/table. Requires superuser authentication. |
| PATCH | `/api/vectors/collections/{name}` | **superuser** | Updates vector collection metadata/configuration. Requires superuser authentication. |
| POST | `/api/vectors/collections/{name}` | **superuser** | Creates a vector collection/table. Requires superuser authentication. |
| GET | `/api/vectors/{collection}` | **superuser** | Lists vector documents. Requires superuser authentication. |
| POST | `/api/vectors/{collection}` | **superuser** | Inserts a vector document into a vector collection. Requires superuser authentication. |
| POST | `/api/vectors/{collection}/documents/batch` | **superuser** | Inserts multiple vector documents. Requires superuser authentication. |
| POST | `/api/vectors/{collection}/documents/search` | **superuser** | Searches vector documents by embedding similarity. Requires superuser authentication. |
| DELETE | `/api/vectors/{collection}/{id}` | **superuser** | Deletes one vector document. Requires superuser authentication. |
| GET | `/api/vectors/{collection}/{id}` | **superuser** | Returns one vector document by id. Requires superuser authentication. |
| PATCH | `/api/vectors/{collection}/{id}` | **superuser** | Updates one vector document. Requires superuser authentication. |

## LLM documents and LangChainGo

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| POST | `/api/langchaingo/completions` | user | Runs an LLM completion/chat request. |
| POST | `/api/langchaingo/documents/query` | user | Queries documents through LangChainGo and optionally returns generated answer/sources. |
| POST | `/api/langchaingo/rag` | user | Runs retrieval-augmented generation over an LLM document collection. |
| POST | `/api/langchaingo/sql` | user | Uses an LLM to generate and execute SQL over selected database tables. Requires auth. |
| GET | `/api/llm-documents/collections` | **superuser** | Lists LLM document collections. |
| DELETE | `/api/llm-documents/collections/{name}` | **superuser** | Deletes an LLM document collection. |
| POST | `/api/llm-documents/collections/{name}` | **superuser** | Creates an LLM document collection. |
| GET | `/api/llm-documents/{collection}` | **superuser** | Lists documents in an LLM collection. |
| POST | `/api/llm-documents/{collection}` | **superuser** | Inserts an LLM document. Either `content` or `embedding` is required. |
| POST | `/api/llm-documents/{collection}/documents/query` | **superuser** | Queries documents by text and/or embedding, with optional negative query options. |
| DELETE | `/api/llm-documents/{collection}/{id}` | **superuser** | Deletes an LLM document. |
| GET | `/api/llm-documents/{collection}/{id}` | **superuser** | Returns an LLM document by id. |
| PATCH | `/api/llm-documents/{collection}/{id}` | **superuser** | Updates an LLM document. If `content` changes and `embedding` is omitted, embedding is cleared so it can be recalculated by the store. |

## LLM relay

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/llm/channels` | user | Lists the channels usable by the caller (own + shared; `?all=true` for superusers). |
| POST | `/api/llm/channels` | user | Creates a channel. |
| DELETE | `/api/llm/channels/{id}` | user | Deletes a channel. |
| GET | `/api/llm/channels/{id}` | user | Returns a channel (key masked). |
| PATCH | `/api/llm/channels/{id}` | user | Updates a channel. Omitted secrets are kept. |
| DELETE | `/api/llm/channels/{id}/proxy/{path...}` | user | Same as the relay but with an explicit channel in the path (any HTTP method). |
| GET | `/api/llm/channels/{id}/proxy/{path...}` | user | Same as the relay but with an explicit channel in the path (any HTTP method). |
| PATCH | `/api/llm/channels/{id}/proxy/{path...}` | user | Same as the relay but with an explicit channel in the path (any HTTP method). |
| POST | `/api/llm/channels/{id}/proxy/{path...}` | user | Same as the relay but with an explicit channel in the path (any HTTP method). |
| PUT | `/api/llm/channels/{id}/proxy/{path...}` | user | Same as the relay but with an explicit channel in the path (any HTTP method). |
| POST | `/api/llm/channels/{id}/test` | user | Calls the upstream `GET /models` to check the credentials. `{id}` can be the channel name. |
| GET | `/api/llm/requests` | user | Request log (newest first): channel, model, endpoint, status, stream, tokens, duration, error. Query: `channel`, `model`, `since`, `errorsOnly`, `page`, `perPage`, … |
| GET | `/api/llm/usage` | user | Aggregated requests, errors and tokens per channel and model. Same filters as `/api/llm/requests`. |
| GET | `/api/llm/v1/models` | user | Returns the channel `models` list (OpenAI format) or relays `GET /models` upstream. |
| DELETE | `/api/llm/v1/{path...}` | user | Relays any request to `<channel baseUrl>/{path}` with the channel credentials, eg. `/api/llm/v1/chat/completions`, `/api/llm/v1/embeddings`, … |
| GET | `/api/llm/v1/{path...}` | user | Relays any request to `<channel baseUrl>/{path}` with the channel credentials, eg. `/api/llm/v1/chat/completions`, `/api/llm/v1/embeddings`, … |
| PATCH | `/api/llm/v1/{path...}` | user | Relays any request to `<channel baseUrl>/{path}` with the channel credentials, eg. `/api/llm/v1/chat/completions`, `/api/llm/v1/embeddings`, … |
| POST | `/api/llm/v1/{path...}` | user | Relays any request to `<channel baseUrl>/{path}` with the channel credentials, eg. `/api/llm/v1/chat/completions`, `/api/llm/v1/embeddings`, … |
| PUT | `/api/llm/v1/{path...}` | user | Relays any request to `<channel baseUrl>/{path}` with the channel credentials, eg. `/api/llm/v1/chat/completions`, `/api/llm/v1/embeddings`, … |

## Embedding providers

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| POST | `/api/embedding/embed` | **superuser** | Embeds texts with the configured providers (failover) or a specific one. Body: `{input: string \| string[], provider?}` (max 256 inputs). Returns `{provider, model, … |
| GET | `/api/embedding/providers` | **superuser** | Lists all the providers, the `order` in which they are tried and the environment fallback (`envFallback`, or null). |
| POST | `/api/embedding/providers` | **superuser** | Creates an embedding provider. |
| DELETE | `/api/embedding/providers/{id}` | **superuser** | Deletes a provider. |
| GET | `/api/embedding/providers/{id}` | **superuser** | Returns a provider (key masked). |
| PATCH | `/api/embedding/providers/{id}` | **superuser** | Updates a provider (eg. `{"isDefault": true}`, `{"enabled": false}`, `{"priority": 10}`). Omitted secrets are kept. |
| POST | `/api/embedding/providers/{id}/test` | **superuser** | Embeds a test sentence with this provider. |
| POST | `/api/embedding/reindex` | **superuser** | (Re)embeds memories with the current primary provider. Body: `{mode, limit, offset, batchSize, project, ownerId, shared}`: |
| GET | `/api/embedding/status` | **superuser** | Providers, try `order`, `primary` provider/model and memory coverage: `total`, `missing` (no embedding), `mismatched` (embedded with another model than the primary one) … |

## Object storage

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/storage/objects` | user | Lists the upload history (newest first). Query: `provider`, `status`, `prefix` (key prefix), `contentType` (prefix, eg. `image/`), `search`, `includeDeleted`, `page`, … |
| POST | `/api/storage/objects` | user | Uploads one or more files and records them in the history. |
| POST | `/api/storage/objects/presign` | user | Creates a `pending` history record and returns a presigned `PUT` URL to upload large files directly to the storage. Body: `{provider, filename, contentType, key, prefix, … |
| DELETE | `/api/storage/objects/{id}` | user | Deletes the remote object and marks the history record as `deleted` (the history is kept). Use `?force=true` to mark it deleted when its provider no longer exists. |
| GET | `/api/storage/objects/{id}` | user | Returns an object with a temporary `downloadUrl` (presigned GET, 1 hour). |
| POST | `/api/storage/objects/{id}/complete` | user | Verifies that the presigned upload exists in the bucket and marks it `uploaded` (size/etag/content type are read from the storage). |
| GET | `/api/storage/objects/{id}/download` | user | Streams the object content through BosBase. |
| GET | `/api/storage/providers` | user | Lists the providers usable by the caller (own + shared). Superusers can pass `?all=true`. |
| POST | `/api/storage/providers` | user | Creates a storage provider. Body: the provider fields plus `accessKey`, `secretKey` and optionally `shared` / `ownerId` (superusers). |
| DELETE | `/api/storage/providers/{id}` | user | Deletes a provider (the upload history is kept). |
| GET | `/api/storage/providers/{id}` | user | Returns a provider (secrets masked). |
| PATCH | `/api/storage/providers/{id}` | user | Updates a provider. Omitted secrets are kept. |
| POST | `/api/storage/providers/{id}/test` | user | Checks the connection by uploading, reading and deleting a small probe object. `{id}` can also be the provider name. |

## Memory spaces

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/memory/spaces` | user | Lists the spaces of the caller (any membership status) with `myRole` / `myStatus`. Query: `discover=true` lists the listed (discoverable) spaces, `search`, `all=true` … |
| POST | `/api/memory/spaces` | user | Creates a space; the caller becomes its owner. |
| DELETE | `/api/memory/spaces/{space}` | user | Deletes the space with all its memories (owner). |
| GET | `/api/memory/spaces/{space}` | user | Returns a space (members, owner, `pendingRequests` for owners/admins). Visible to its members, or to everyone when listed. |
| PATCH | `/api/memory/spaces/{space}` | user | Updates `name`, `description`, `listed` (owner/admin). |
| POST | `/api/memory/spaces/{space}/join` | user | Requests to join the space (`{message}` optional). The request stays `pending` until the owner (creator) approves it; a rejected user can request again. |
| POST | `/api/memory/spaces/{space}/leave` | user | Leaves the space (the owner must transfer the ownership or delete the space instead). |
| GET | `/api/memory/spaces/{space}/members` | user | Lists the active members (members); the owner also sees the pending/rejected requests (`?status=pending` filters). |
| DELETE | `/api/memory/spaces/{space}/members/{member}` | user | Removes a member (owner only). Removing yourself leaves the space. |
| PATCH | `/api/memory/spaces/{space}/members/{member}` | user | Changes the role of an active member: `{role: "admin" \| "member" \| "owner"}` (owner only; `owner` transfers the ownership and the previous owner becomes admin). |
| POST | `/api/memory/spaces/{space}/members/{member}/approve` | user | Approves a pending join request (owner only). |
| POST | `/api/memory/spaces/{space}/members/{member}/reject` | user | Rejects a pending join request (owner only). |
| GET | `/api/memory/spaces/{space}/memories` | user | Lists the memories of the space (newest first) or searches them with `q` (semantic + keyword). Query: `q`, `project`, `tags`, `limit`, `offset`. Items include `author`, … |
| POST | `/api/memory/spaces/{space}/memories` | user | Stores a memory in the space (body like the `memory_store` tool: `content`, `title`, `project`, `tags`, `metadata`, `pinned`, `dedupe`). |
| DELETE | `/api/memory/spaces/{space}/memories/{id}` | user | Deletes a memory: the author or an owner/admin. |
| GET | `/api/memory/spaces/{space}/memories/{id}` | user | Returns a memory of the space with its relations and author. |
| PATCH | `/api/memory/spaces/{space}/memories/{id}` | user | Updates a memory (`content`, `title`, `tags`, `metadata`, `project`, `pinned`): the author or an owner/admin. |

## Agent accounts

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| POST | `/api/admin/users` | **superuser** | Superusers only. Creates a regular user account (eg. for an AI agent) and returns its email, password and an auth token usable directly as `Authorization` / MCP Bearer … |

## Diagnostics

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/diagnostics/errors` | **superuser** | Grouped and latest error logs. Query: `since` (`24h`, RFC3339, `YYYY-MM-DD`), `until`, `level` (`error`, `warn`, `info`, `all`), `status` (`500`, `5xx`, `4xx`), … |
| GET | `/api/diagnostics/files` | **superuser** | Lists the error log files, or returns the last lines of one file with `?file=errors-2026-10-24.jsonl&tail=200&search=...`. |
| GET | `/api/diagnostics/health` | **superuser** | Health snapshot: build, runtime (uptime, goroutines, memory), database (ping, version, pgvector, size, connections, pool, migrations), aux database, settings summary, … |
| POST | `/api/diagnostics/report` | **superuser** | Generates a markdown diagnostics report (`{since, save}`) and saves it under `{dataDir}/logs/reports/` when `save` is not false. |

## Cache and Redis

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/cache` | **superuser** | Lists configured caches and optional runtime statistics. |
| POST | `/api/cache` | **superuser** | Creates a cache configuration. |
| DELETE | `/api/cache/{name}` | **superuser** | Deletes a cache configuration. |
| PATCH | `/api/cache/{name}` | **superuser** | Updates cache configuration. |
| DELETE | `/api/cache/{name}/entries/{key}` | **superuser** | Deletes a cache entry. |
| GET | `/api/cache/{name}/entries/{key}` | **superuser** | Reads a cache entry. |
| PATCH | `/api/cache/{name}/entries/{key}` | **superuser** | Renews an existing cache entry TTL. |
| PUT | `/api/cache/{name}/entries/{key}` | **superuser** | Sets a cache entry. |

## Scripts and WASM

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| POST | `/api/script-permissions` | **superuser** | Creates script execution permission metadata. |
| DELETE | `/api/script-permissions/{name}` | **superuser** | Deletes permission metadata by script name. |
| GET | `/api/script-permissions/{name}` | **superuser** | Returns permission metadata by script name. |
| PATCH | `/api/script-permissions/{name}` | **superuser** | Updates permission metadata by script name. |
| GET | `/api/scripts` | **superuser** | Lists stored scripts. Requires superuser authentication. |
| POST | `/api/scripts` | **superuser** | Creates a stored script. |
| GET | `/api/scripts/async/{id}` | **superuser** | Returns asynchronous script execution status. |
| POST | `/api/scripts/async/{name}/execute` | **superuser** | Starts asynchronous script execution. Permission-based access. |
| POST | `/api/scripts/command` | **superuser** | Runs a shell command in the configured execution directory. Requires superuser authentication. |
| POST | `/api/scripts/command/status` | **superuser** | See `api_docs`. |
| GET | `/api/scripts/command/status/{id}` | **superuser** | Returns command job status. Requires superuser authentication. The POST variant accepts the id in the body. |
| GET | `/api/scripts/command/{id}` | **superuser** | See `api_docs`. |
| POST | `/api/scripts/upload` | **superuser** | Creates or updates a script from a local file path on the server. Requires superuser authentication. |
| POST | `/api/scripts/wasm` | **superuser** | Executes a WASM module. Permission-based access. |
| POST | `/api/scripts/wasm/async` | **superuser** | Starts asynchronous WASM execution. Permission-based access. |
| GET | `/api/scripts/wasm/async/{id}` | **superuser** | Returns asynchronous WASM job status. |
| DELETE | `/api/scripts/{name}` | **superuser** | Deletes a script by name. Requires superuser authentication. |
| GET | `/api/scripts/{name}` | **superuser** | Returns a script by name. Requires superuser authentication. |
| PATCH | `/api/scripts/{name}` | **superuser** | Updates a script by name. Requires superuser authentication. |
| POST | `/api/scripts/{name}/execute` | **superuser** | Executes a stored Python script through the functioncall runtime. Permission-based access. |

## Streaming, MCP and proxy endpoints

Not listed by `api_endpoints` and not callable through `api_request`; use them directly.

| Method | Path | Access | Summary |
| --- | --- | --- | --- |
| GET | `/api/realtime` | public (optional token) | Opens the realtime SSE connection; the first `PB_CONNECT` event returns the `clientId` used with `POST /api/realtime`. Events respect the collection API rules of the client's token. |
| GET | `/api/pubsub` | public (publishing requires a token) | WebSocket pub/sub channel. |
| GET | `/api/scripts/{name}/execute/sse` | script permissions | Executes a script and streams its output over SSE. |
| GET | `/api/scripts/{name}/execute/ws` | script permissions | Executes a script and streams its output over WebSocket. |
| POST | `/api/mcp` | user (Bearer token) | MCP server (Streamable HTTP, JSON-RPC 2.0): `initialize`, `tools/list`, `tools/call`, `prompts/*`, `resources/*`. |
| ANY | `/output`, `/output/{path...}` | public | Reverse proxy to the booster service (`BOOSTER_URL`), the `/output` prefix is stripped. |
