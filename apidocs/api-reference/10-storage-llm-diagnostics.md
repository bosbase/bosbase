# Object Storage, LLM Relay, Embedding Providers and Diagnostics

These APIs require an auth token (`Authorization: Bearer <token>`). The same features are available as MCP tools (see [MCP Server](./09-mcp.md)).

Configs belong to the auth record that created them. Superusers can create **shared** configs (`"shared": true`) that every user can use but only superusers can modify, or configs for another auth record (`"ownerId"`). Secrets (`accessKey`, `secretKey`, `apiKey`, `headers`) are stored encrypted with the app encryption key (when configured, eg. `BS_ENCRYPTION_KEY`) and are never returned (only masked hints).

Encryption key: `BS_ENCRYPTION_KEY` must be exactly 32 characters (16 or 24 are also accepted). When it is missing or has an invalid format, encryption is disabled (a warning is logged and `diagnostics_health` reports it) and the secrets are stored unencrypted instead of failing. Secrets encrypted earlier can't be used until the original key is restored (the related calls return an error; the server keeps running).

SSRF protection: configs owned by regular users cannot reach loopback/private/link-local addresses (set `BOSBASE_ALLOW_PRIVATE_UPSTREAMS=true` to allow it). Shared and superuser configs are trusted.

## Object Storage

Any S3 compatible storage: AWS S3 (`aws`), Cloudflare R2 (`r2`), Google Cloud Storage with HMAC keys (`gcs`, endpoint `https://storage.googleapis.com`), MinIO (`minio`, use `forcePathStyle`), Backblaze B2 (`b2`) or any other (`s3`/`other`).

Superusers configure shared providers (`"shared": true`, one of them `isDefault`) used by everyone. Uploads without `provider` use the caller's own default provider, then the shared default, then the caller's other providers, then the other shared ones. Set `STORAGE_ALLOW_USER_PROVIDERS=false` to let only the superusers configure providers (users still upload to the shared ones).

Public links: every uploaded object is returned with a `url` usable from the internet and its `urlType`: `public` (permanent: `publicUrl` base URL, or direct object URL with `publicRead`) or `presigned` (presigned GET link valid `urlExpiresSeconds`, default `STORAGE_URL_EXPIRES_SECONDS` or 7 days, the S3 maximum; `urlExpiresAt`). `GET /api/storage/objects/{id}` returns a fresh link.

### Storage Provider Object

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Provider id. |
| `name` | string | Unique name (per owner); can be used instead of the id. |
| `type` | string | `s3`, `aws`, `r2`, `gcs`, `minio`, `b2`, `other`. |
| `endpoint` | string | S3 endpoint URL (defaults for `aws`/`gcs`). |
| `region` | string | Region (`auto` for R2/GCS, default `us-east-1`). |
| `bucket` | string | Bucket name. |
| `accessKey` | string | Masked access key (write: the plain key). |
| `secretKeySet` | bool | Whether a secret key is stored (write: `secretKey`). |
| `forcePathStyle` | bool | Path style URLs. |
| `publicUrl` | string | Optional public/CDN base URL used to build the permanent object `url`. |
| `publicRead` | bool | The bucket objects are publicly readable: the direct object URL is returned as permanent `url`. |
| `prefix` | string | Key prefix applied to all uploads. |
| `isDefault` | bool | Used when no provider is specified. |
| `enabled` | bool | Disabled providers can't be used for uploads. |
| `shared` | bool | Shared config (owner = `""`). |

### GET /api/storage/providers

#### Function

Lists the providers usable by the caller (own + shared). Superusers can pass `?all=true`.

### POST /api/storage/providers

#### Function

Creates a storage provider. Body: the provider fields plus `accessKey`, `secretKey` and optionally `shared` / `ownerId` (superusers).

#### Request Example

```json
{
  "name": "r2",
  "type": "r2",
  "endpoint": "https://<account_id>.r2.cloudflarestorage.com",
  "bucket": "media",
  "accessKey": "...",
  "secretKey": "...",
  "publicUrl": "https://media.example.com",
  "isDefault": true
}
```

### GET /api/storage/providers/{id}

#### Function

Returns a provider (secrets masked).

### PATCH /api/storage/providers/{id}

#### Function

Updates a provider. Omitted secrets are kept.

### DELETE /api/storage/providers/{id}

#### Function

Deletes a provider (the upload history is kept).

### POST /api/storage/providers/{id}/test

#### Function

Checks the connection by uploading, reading and deleting a small probe object. `{id}` can also be the provider name.

#### Response Example

```json
{ "ok": true, "provider": "r2", "bucket": "media", "steps": { "upload": true, "head": true, "delete": true }, "durationMs": 230 }
```

### Storage Object Object

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | History record id. |
| `provider` / `providerName` / `bucket` | string | Where the object is stored. |
| `key` | string | Object key (default `<provider prefix>/<prefix>/YYYY/MM/DD/<random>_<filename>`). |
| `filename` | string | Original file name. |
| `contentType` | string | MIME type. |
| `size` | number | Size in bytes. |
| `etag` | string | ETag returned by the storage. |
| `url` | string | Public URL (when the provider has `publicUrl`). |
| `status` | string | `pending` (presigned, not completed), `uploaded`, `deleted`. |
| `source` | string | `http` or `mcp`. |
| `metadata` | object | Custom metadata. |
| `deletedAt` | string | Deletion time. |

### POST /api/storage/objects

#### Function

Uploads one or more files and records them in the history.

- `multipart/form-data`: files in `file` (repeatable) or `files`, optional fields `provider`, `key` (single file only), `prefix`, `metadata` (JSON string), `urlExpiresSeconds`.
- `application/json`: `{provider, filename, contentBase64 | text, contentType, key, prefix, metadata, urlExpiresSeconds}`.
- The response includes `url`, `urlType` and (for presigned links) `urlExpiresAt`.

The max size is `STORAGE_MAX_UPLOAD_MB` (default 512). Returns the object (or `{items: [...]}` for several files).

### POST /api/storage/objects/presign

#### Function

Creates a `pending` history record and returns a presigned `PUT` URL to upload large files directly to the storage. Body: `{provider, filename, contentType, key, prefix, expiresSeconds, metadata}`. After uploading call `POST /api/storage/objects/{id}/complete`.

#### Response Example

```json
{
  "object": { "id": "r1...", "status": "pending", "key": "videos/2026/10/24/abc_movie.mp4" },
  "uploadUrl": "https://...&X-Amz-Signature=...",
  "method": "PUT",
  "headers": { "Content-Type": "video/mp4" },
  "expiresAt": "2026-10-24T12:00:00Z"
}
```

### POST /api/storage/objects/{id}/complete

#### Function

Verifies that the presigned upload exists in the bucket and marks it `uploaded` (size/etag/content type are read from the storage).

### GET /api/storage/objects

#### Function

Lists the upload history (newest first). Query: `provider`, `status`, `prefix` (key prefix), `contentType` (prefix, eg. `image/`), `search`, `includeDeleted`, `page`, `perPage`, `all=true` (superusers).

### GET /api/storage/objects/{id}

#### Function

Returns an object with a temporary `downloadUrl` (presigned GET, 1 hour).

### GET /api/storage/objects/{id}/download

#### Function

Streams the object content through BosBase.

### DELETE /api/storage/objects/{id}

#### Function

Deletes the remote object and marks the history record as `deleted` (the history is kept). Use `?force=true` to mark it deleted when its provider no longer exists.

## LLM Relay

BosBase relays requests to third party LLM providers using stored channel credentials. Any OpenAI compatible SDK can use `https://<bosbase>/api/llm/v1` as base URL with the **BosBase auth token** as API key.

Channel types:

- `openai` — any OpenAI compatible API (OpenAI, OpenRouter, DeepSeek, Qwen/DashScope, Groq, Together, Gemini OpenAI endpoint, Ollama/vLLM `/v1`...). Credentials are sent as `Authorization: Bearer`.
- `anthropic` — Anthropic Messages API (`x-api-key` + `anthropic-version`). Default base URL `https://api.anthropic.com/v1`.

Channel selection for relay requests: the `X-Bosbase-Channel` header, the `?channel=` query param or a `"channel"` body field (id or name, removed before forwarding); otherwise a channel whose `models` list contains the requested model; otherwise the default channel (own default first, then the shared default). When the body has no `model`, the channel `defaultModel` is used. If a channel has a `models` list, other models are rejected.

### LLM Channel Object

| Field | Type | Meaning |
| --- | --- | --- |
| `id` / `name` | string | Channel id and unique name. |
| `type` | string | `openai` or `anthropic`. |
| `baseUrl` | string | API base URL including the version path (eg. `https://openrouter.ai/api/v1`). |
| `apiKey` | string | Masked key (write: the plain key). |
| `headers` | array | Names of the extra upstream headers (write: an object; stored encrypted). |
| `models` | array | Optional model allow list (also used for routing). |
| `defaultModel` | string | Model used when none is given. |
| `isDefault` / `enabled` / `shared` | bool | Flags. |

### GET /api/llm/channels

#### Function

Lists the channels usable by the caller (own + shared; `?all=true` for superusers).

### POST /api/llm/channels

#### Function

Creates a channel.

#### Request Example

```json
{ "name": "openrouter", "baseUrl": "https://openrouter.ai/api/v1", "apiKey": "sk-or-...", "defaultModel": "openai/gpt-4o-mini", "isDefault": true }
```

### GET /api/llm/channels/{id}

#### Function

Returns a channel (key masked).

### PATCH /api/llm/channels/{id}

#### Function

Updates a channel. Omitted secrets are kept.

### DELETE /api/llm/channels/{id}

#### Function

Deletes a channel.

### POST /api/llm/channels/{id}/test

#### Function

Calls the upstream `GET /models` to check the credentials. `{id}` can be the channel name.

### GET /api/llm/v1/models

#### Function

Returns the channel `models` list (OpenAI format) or relays `GET /models` upstream.

### POST /api/llm/v1/{path...}

#### Function

Relays any request to `<channel baseUrl>/{path}` with the channel credentials, eg. `/api/llm/v1/chat/completions`, `/api/llm/v1/embeddings`, `/api/llm/v1/images/generations` or `/api/llm/v1/messages` (Anthropic channels). Streaming (`"stream": true`, SSE) is passed through. The token usage is recorded (for streamed OpenAI responses include `"stream_options": {"include_usage": true}`).

#### Request Example

```sh
curl https://<bosbase>/api/llm/v1/chat/completions \
  -H "Authorization: Bearer <bosbase token>" -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"Hello"}]}'
```

### POST /api/llm/channels/{id}/proxy/{path...}

#### Function

Same as the relay but with an explicit channel in the path (any HTTP method).

### GET /api/llm/requests

#### Function

Request log (newest first): channel, model, endpoint, status, stream, tokens, duration, error. Query: `channel`, `model`, `since`, `errorsOnly`, `page`, `perPage`, `all=true` (superusers).

### GET /api/llm/usage

#### Function

Aggregated requests, errors and tokens per channel and model. Same filters as `/api/llm/requests`.

## Embedding Providers

Superusers only. Server side embedding providers used to embed the MCP memories and notes (any OpenAI compatible `/embeddings` API). They are read on every embedding call, so changes apply without restart.

The default provider is tried first, then the other enabled providers ordered by `priority` (lower first); when a provider fails the next one is used and the error is stored in `lastError`. When no provider is enabled the `MCP_EMBEDDING_*` environment variables are used as fallback.

Semantic search only compares vectors of the same model, so after switching the default model call `POST /api/embedding/reindex` to re-embed the existing memories (memories embedded with another model stay searchable by keywords meanwhile).

### Embedding Provider Object

| Field | Type | Meaning |
| --- | --- | --- |
| `id` / `name` | string | Provider id and unique name (the name can be used instead of the id). |
| `type` | string | `openai` (any OpenAI compatible `/embeddings` API). |
| `baseUrl` | string | API base URL including the version (default `https://api.openai.com/v1`). |
| `apiKey` | string | Masked key (write: the plain key). |
| `headers` | array | Names of the extra upstream headers (write: an object; stored encrypted). |
| `model` | string | Embedding model, eg. `text-embedding-3-small` or `openai/text-embedding-3-small` (OpenRouter). |
| `dimensions` | number | Requested output dimensions (`0` = model default; only for models supporting it). |
| `priority` | number | Failover order, lower first (default 100). |
| `isDefault` | bool | Primary provider (only one). |
| `enabled` | bool | Disabled providers are skipped. |
| `lastError` / `lastErrorAt` / `lastUsedAt` | string | Last failure and last successful use. |

### GET /api/embedding/providers

#### Function

Lists all the providers, the `order` in which they are tried and the environment fallback (`envFallback`, or null).

### POST /api/embedding/providers

#### Function

Creates an embedding provider.

#### Request Example

```json
{ "name": "openrouter", "baseUrl": "https://openrouter.ai/api/v1", "apiKey": "sk-or-...", "model": "openai/text-embedding-3-small", "isDefault": true }
```

### GET /api/embedding/providers/{id}

#### Function

Returns a provider (key masked).

### PATCH /api/embedding/providers/{id}

#### Function

Updates a provider (eg. `{"isDefault": true}`, `{"enabled": false}`, `{"priority": 10}`). Omitted secrets are kept.

### DELETE /api/embedding/providers/{id}

#### Function

Deletes a provider.

### POST /api/embedding/providers/{id}/test

#### Function

Embeds a test sentence with this provider.

#### Response Example

```json
{ "ok": true, "provider": "openrouter", "model": "openai/text-embedding-3-small", "dimensions": 1536, "durationMs": 420 }
```

### GET /api/embedding/status

#### Function

Providers, try `order`, `primary` provider/model and memory coverage: `total`, `missing` (no embedding), `mismatched` (embedded with another model than the primary one) and counts `byModel`.

### POST /api/embedding/embed

#### Function

Embeds texts with the configured providers (failover) or a specific one. Body: `{input: string | string[], provider?}` (max 256 inputs). Returns `{provider, model, dimensions, embeddings}`.

### POST /api/embedding/reindex

#### Function

(Re)embeds memories with the current primary provider. Body: `{mode, limit, offset, batchSize, project, ownerId, shared}`:

- `mode`: `mismatched` (default; missing or embedded with another model), `missing` (no embedding) or `all` (paginate with `offset` / `nextOffset`).
- `limit`: max memories per call (default 200, max 2000); call again while `remaining > 0`.
- `batchSize`: texts per provider request (default 32).

#### Response Example

```json
{ "mode": "mismatched", "primaryModel": "text-embedding-3-small", "selected": 200, "processed": 200, "failed": 0, "remaining": 35, "models": { "text-embedding-3-small": 200 }, "durationMs": 5120 }
```

## Diagnostics

Superusers only.

Error log files: every log with level ≥ `BOSBASE_ERROR_LOG_LEVEL` (default `ERROR`) is also appended to `{dataDir}/logs/errors-YYYY-MM-DD.jsonl` (kept `BOSBASE_ERROR_LOG_MAX_DAYS`, default 30; max `BOSBASE_ERROR_LOG_MAX_MB` per day, default 100; disable with `BOSBASE_ERROR_LOG_FILE=false`). Sensitive request body fields (passwords, tokens, secrets, API keys) are redacted in all request logs.

### GET /api/diagnostics/health

#### Function

Health snapshot: build, runtime (uptime, goroutines, memory), database (ping, version, pgvector, size, connections, pool, migrations), aux database, settings summary, encryption key status (`encryption`: invalid key, settings that can't be decrypted), error counts, integrations (incl. failing embedding providers) and a list of detected `problems`.

### GET /api/diagnostics/errors

#### Function

Grouped and latest error logs. Query: `since` (`24h`, RFC3339, `YYYY-MM-DD`), `until`, `level` (`error`, `warn`, `info`, `all`), `status` (`500`, `5xx`, `4xx`), `search`, `source` (`db`, `file`; default reads the database and falls back to the files), `limit`.

### GET /api/diagnostics/files

#### Function

Lists the error log files, or returns the last lines of one file with `?file=errors-2026-10-24.jsonl&tail=200&search=...`.

### POST /api/diagnostics/report

#### Function

Generates a markdown diagnostics report (`{since, save}`) and saves it under `{dataDir}/logs/reports/` when `save` is not false.
