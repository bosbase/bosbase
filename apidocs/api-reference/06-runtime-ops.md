# Cache, Script Permissions, Scripts, WASM, Redis, and Output Proxy

This document covers runtime operational HTTP APIs.

## Cache APIs

All cache endpoints require superuser authentication.

### Cache Config Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `name` | string | Cache name. | `sessions` |
| `sizeBytes` | integer | Cache size in bytes. | `1048576` |
| `defaultTTLSeconds` | integer | Default entry TTL in seconds. | `300` |
| `readTimeoutMs` | integer | Read timeout in milliseconds. | `50` |
| `created` | string | Creation timestamp. | `2026-07-08 12:00:00Z` |
| `updated` | string | Update timestamp. | `2026-07-08 12:05:00Z` |
| `entryCount` | integer | Optional active in-memory entry count. | `10` |
| `hitRate` | number | Optional cache hit rate. | `0.93` |
| `hitCount` | integer | Optional cache hit count. | `100` |
| `missCount` | integer | Optional cache miss count. | `7` |
| `databaseEntryCount` | integer | Optional persisted entry count. | `12` |

### Cache Entry Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `cache` | string | Cache name. | `sessions` |
| `key` | string | Entry key. | `user_1` |
| `value` | mixed JSON | Stored JSON value. | `{ "role": "admin" }` |
| `source` | string | Cache source used by store. | `memory` |
| `expiresAt` | string | Expiration timestamp, when applicable. | `2026-07-08T12:10:00Z` |

### GET /api/cache

#### Function

Lists configured caches and optional runtime statistics.

#### Response Example

```json
{
  "items": [
    {
      "name": "sessions",
      "sizeBytes": 1048576,
      "defaultTTLSeconds": 300,
      "readTimeoutMs": 50,
      "entryCount": 10,
      "hitRate": 0.93
    }
  ]
}
```

### POST /api/cache

#### Function

Creates a cache configuration.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `name` | string | Yes | Cache name. | `sessions` |
| `sizeBytes` | integer | No | Cache size in bytes. | `1048576` |
| `defaultTTLSeconds` | integer | No | Default TTL in seconds. Omitted means store default. | `300` |
| `readTimeoutMs` | integer | No | Read timeout in milliseconds. Omitted means store default. | `50` |

#### Response

Created cache config object.

### PATCH /api/cache/{name}

#### Function

Updates cache configuration.

#### Request Body Fields

At least one of `sizeBytes`, `defaultTTLSeconds`, or `readTimeoutMs` is required.

#### Response

Updated cache config object.

### DELETE /api/cache/{name}

#### Function

Deletes a cache configuration.

#### Response

`204 No Content`.

### PUT /api/cache/{name}/entries/{key}

#### Function

Sets a cache entry.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `value` | mixed JSON | Yes | JSON value to store. | `{ "role": "admin" }` |
| `ttlSeconds` | integer | No | Entry TTL in seconds. Omitted uses cache default. | `60` |

#### Response

Cache entry object.

### GET /api/cache/{name}/entries/{key}

#### Function

Reads a cache entry.

#### Response

Cache entry object.

### PATCH /api/cache/{name}/entries/{key}

#### Function

Renews an existing cache entry TTL.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `ttlSeconds` | integer | No | New TTL in seconds. Omitted uses cache default. | `120` |

#### Response

Renewed cache entry object.

### DELETE /api/cache/{name}/entries/{key}

#### Function

Deletes a cache entry.

#### Response

`204 No Content`.

## Script Permissions

All script permission management endpoints require superuser authentication.

### Script Permission Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Permission record id. | `perm_1` |
| `script_id` | string | Script record id, if linked. | `script_1` |
| `script_name` | string | Script name. | `hello.py` |
| `content` | string | Permission expression/configuration. | `@request.auth.id != ""` |
| `created` | string | Creation timestamp. | `2026-07-08T12:00:00Z` |
| `updated` | string | Update timestamp. | `2026-07-08T12:00:00Z` |

### POST /api/script-permissions

#### Function

Creates script execution permission metadata.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `script_id` | string | No | Existing script id. | `script_1` |
| `script_name` | string | No | Script name. Required if `script_id` is not enough to resolve the script. | `hello.py` |
| `content` | string | No | Permission content/expression. | `@request.auth.id != ""` |

#### Response

Created script permission object.

### GET /api/script-permissions/{name}

#### Function

Returns permission metadata by script name.

#### Response

Script permission object.

### PATCH /api/script-permissions/{name}

#### Function

Updates permission metadata by script name.

#### Request Body Fields

Partial permission payload with `script_id`, `script_name`, and/or `content`.

#### Response

Updated script permission object.

### DELETE /api/script-permissions/{name}

#### Function

Deletes permission metadata by script name.

#### Response

`204 No Content`.

## Scripts

Script management and shell command endpoints require superuser authentication. Execution endpoints are permission-based.

### Script Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Script id. | `018fe...` |
| `name` | string | Script name. | `hello.py` |
| `content` | string | Script source code. | `def main(): return "hi"` |
| `description` | string | Optional description. | `Greets users` |
| `version` | integer | Version counter. | `1` |
| `created` | string | Creation timestamp. | `2026-07-08T12:00:00Z` |
| `updated` | string | Update timestamp. | `2026-07-08T12:05:00Z` |

### GET /api/scripts

#### Function

Lists stored scripts. Requires superuser authentication.

#### Response

Array/list of script objects.

### POST /api/scripts

#### Function

Creates a stored script.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `name` | string | Yes | Script name. | `hello.py` |
| `content` | string | Yes | Script source code. | `def main(): return "hi"` |
| `description` | string | No | Description. | `Greets users` |

#### Response

Created script object.

### GET /api/scripts/{name}

#### Function

Returns a script by name. Requires superuser authentication.

#### Response

Script object.

### PATCH /api/scripts/{name}

#### Function

Updates a script by name. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `content` | string | No | New script content. | `def main(): return "updated"` |
| `description` | string | No | New description. | `Updated script` |

#### Response

Updated script object.

### DELETE /api/scripts/{name}

#### Function

Deletes a script by name. Requires superuser authentication.

#### Response

`204 No Content`.

### POST /api/scripts/upload

#### Function

Creates or updates a script from a local file path on the server. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `path` | string | Yes | Server-local file path to read. | `/pb/functions/hello.py` |

#### Response

Created or updated script object.

## Script Commands

### POST /api/scripts/command

#### Function

Runs a shell command in the configured execution directory. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `command` | string | Yes | Shell command passed to `bash -c`. | `echo hi` |
| `async` | boolean | No | Run asynchronously when true. | `true` |

#### Response Examples

Synchronous:

```json
{ "output": "hi\n" }
```

Asynchronous:

```json
{ "id": "job_1", "status": "running" }
```

### GET /api/scripts/command/{id}

### POST /api/scripts/command/status

### GET /api/scripts/command/status/{id}

#### Function

Returns command job status. Requires superuser authentication. The POST variant accepts the id in the body.

#### Request Body Fields for POST

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `id` | string | Yes | Job id. | `job_1` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Job id. | `job_1` |
| `command` | string | Command string. | `echo hi` |
| `status` | string | `running`, `done`, or `error`. | `done` |
| `output` | string | Combined command output when finished. | `hi\n` |
| `error` | string | Error text when failed. | `exit status 1` |
| `startedAt` | string | Start timestamp. | `2026-07-08T12:00:00Z` |
| `finishedAt` | string | Finish timestamp when complete. | `2026-07-08T12:00:01Z` |

## Script Execution

### POST /api/scripts/{name}/execute

#### Function

Executes a stored Python script through the functioncall runtime. Permission-based access.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `args` | array of strings | No | Positional arguments passed to the function. | `["10", "20"]` |
| `arguments` | array of strings | No | Backward-compatible alias for `args`. | `["10"]` |
| `function_name` | string | No | Function to call. Defaults to `main`. | `main` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `output` | mixed | Script output returned by runtime. | `30` |

### POST /api/scripts/async/{name}/execute

#### Function

Starts asynchronous script execution. Permission-based access.

#### Request Body Fields

Same as synchronous script execution.

#### Response

```json
{ "id": "job_1", "status": "running" }
```

### GET /api/scripts/async/{id}

#### Function

Returns asynchronous script execution status.

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Job id. | `job_1` |
| `scriptName` | string | Script name. | `hello.py` |
| `functionName` | string | Function name. | `main` |
| `args` | array of strings | Arguments. | `["10"]` |
| `status` | string | `running`, `done`, or `error`. | `done` |
| `startedAt` | string | Start timestamp. | `2026-07-08T12:00:00Z` |
| `finishedAt` | string | Finish timestamp, when complete. | `2026-07-08T12:00:02Z` |
| `output` | string | Output when complete. | `ok` |
| `error` | string | Error when failed. | `traceback...` |

## WASM Execution

### POST /api/scripts/wasm

#### Function

Executes a WASM module. Permission-based access.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `wasm` | string | Yes | WASM module name/path under execution directory. | `demo.wasm` |
| `params` | string | No | Space-separated parameters. Numeric values are converted where possible. | `10 20` |
| `options` | string | No | Function selector/options. `--func=name`, `-f name`, or bare function name are supported. | `--func=add` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `output` | string | Final combined output or return values. | `30` |
| `stdout` | string | Captured stdout. | `30\n` |
| `stderr` | string | Captured stderr. | `` |
| `duration` | string | Runtime duration. | `12ms` |

### POST /api/scripts/wasm/async

#### Function

Starts asynchronous WASM execution. Permission-based access.

#### Request Body Fields

Same as synchronous WASM execution.

#### Response

```json
{ "id": "job_1", "status": "running" }
```

### GET /api/scripts/wasm/async/{id}

#### Function

Returns asynchronous WASM job status.

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Job id. | `job_1` |
| `wasmName` | string | WASM module name. | `demo.wasm` |
| `status` | string | `running`, `done`, or `error`. | `done` |
| `startedAt` | string | Start timestamp. | `2026-07-08T12:00:00Z` |
| `finishedAt` | string | Finish timestamp, when complete. | `2026-07-08T12:00:01Z` |
| `function` | string | Called function. | `add` |
| `options` | string | Original options string. | `--func=add` |
| `parameters` | string | Original params string. | `10 20` |
| `duration` | string | Runtime duration. | `15ms` |
| `output` | string | Output when complete. | `30` |
| `stdout` | string | Captured stdout. | `30\n` |
| `stderr` | string | Captured stderr. | `` |
| `error` | string | Error when failed. | `function not found` |

## Redis

All Redis endpoints require superuser authentication.

### GET /api/redis/keys

#### Function

Scans Redis keys.

#### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `cursor` | string | No | Redis scan cursor. | `0` |
| `pattern` | string | No | Match pattern. | `user:*` |
| `count` | integer | No | Scan count hint, capped by server. | `100` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `cursor` | string | Next cursor. `0` means complete. | `0` |
| `items` | array | Key summaries. | `[{ "key": "user:1" }]` |
| `items[].key` | string | Redis key. | `user:1` |

### POST /api/redis/keys

#### Function

Creates a Redis key only if it does not already exist.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `key` | string | Yes | Redis key. | `user:1` |
| `value` | mixed JSON | Yes | JSON value. Stored as JSON bytes. | `{ "name": "Alice" }` |
| `ttlSeconds` | integer | No | Expiration in seconds. Omitted or `0` means no expiration on create. | `60` |

#### Response

Redis entry object.

### GET /api/redis/keys/{key}

#### Function

Reads a Redis key.

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `key` | string | Redis key. | `user:1` |
| `value` | mixed JSON | Decoded JSON value. | `{ "name": "Alice" }` |
| `ttlSeconds` | integer | Remaining TTL in seconds. Omitted for no TTL. | `60` |

### PUT /api/redis/keys/{key}

#### Function

Updates an existing Redis key.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `value` | mixed JSON | Yes | New JSON value. | `{ "name": "Alice" }` |
| `ttlSeconds` | integer | No | Omit to keep TTL; `0` removes expiration; positive value sets expiration. | `120` |

#### Response

Redis entry object.

### DELETE /api/redis/keys/{key}

#### Function

Deletes a Redis key.

#### Response

`204 No Content`.

## Output Proxy

### ANY /output

### ANY /output/{path...}

#### Function

Reverse-proxies requests to `BOOSTER_URL`, defaulting to `http://127.0.0.1:2678`. The `/output` prefix is stripped before forwarding.

#### Request

Any method and body supported by the upstream booster service.

#### Response

Upstream proxy response.
