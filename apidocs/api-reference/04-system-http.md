# Backups, Batch, GraphQL, SQL, Logs, and Cron

This document covers system-oriented HTTP APIs.

## Backups

### Backup File Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `key` | string | Backup file key/name. | `backup_20260708.zip` |
| `modified` | string | Last modified timestamp. | `2026-07-08 12:00:00Z` |
| `size` | integer | File size in bytes. | `1048576` |

### GET /api/backups

#### Function

Lists backup files. Requires superuser authentication.

#### Response

Array of backup file objects.

```json
[
  {
    "key": "backup_20260708.zip",
    "modified": "2026-07-08 12:00:00Z",
    "size": 1048576
  }
]
```

### POST /api/backups

#### Function

Creates a new backup. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `name` | string | No | Backup file name. If omitted, the server generates one. | `manual_backup.zip` |

#### Response

`204 No Content`.

### POST /api/backups/upload

#### Function

Uploads a backup archive. Requires superuser authentication.

#### Request Body Fields

`multipart/form-data` with a backup file field. The server validates uniqueness by original file name.

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| file field | file | Yes | Backup archive to upload. | `backup.zip` |

#### Response

`204 No Content`.

### GET /api/backups/{key}

#### Function

Downloads a backup file. Requires a valid superuser file token in the `token` query parameter.

#### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `key` | string | Backup key/name. | `backup_20260708.zip` |

#### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `token` | string | Yes | Superuser file token. | `eyJ...` |

#### Response

Binary backup archive response.

### DELETE /api/backups/{key}

#### Function

Deletes a backup file. Requires superuser authentication.

#### Response

`204 No Content`.

### POST /api/backups/{key}/restore

#### Function

Restores the selected backup. Requires superuser authentication.

#### Response

`204 No Content` on accepted restore.

## Batch

### POST /api/batch

#### Function

Runs multiple internal record mutation requests in one transaction.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `requests` | array | Yes | Internal request list. | `[{ "method": "POST", "url": "/api/collections/posts/records", "body": {} }]` |
| `requests[].method` | string | Yes | Internal HTTP method. Supported: `PUT`, `POST`, `PATCH`, `DELETE`. | `POST` |
| `requests[].url` | string | Yes | Internal API URL. Only record create/update/delete URLs are supported. | `/api/collections/posts/records` |
| `requests[].headers` | object | No | Internal request headers. | `{ "Content-Type": "application/json" }` |
| `requests[].body` | object | No | Internal request body. | `{ "title": "Hello" }` |

#### Supported Internal URLs

| Method | URL | Meaning |
| --- | --- | --- |
| `PUT` or `POST` | `/api/collections/{collection}/records` | Create record. |
| `PATCH` | `/api/collections/{collection}/records/{id}` | Update record. |
| `DELETE` | `/api/collections/{collection}/records/{id}` | Delete record. |

#### Request Example

```json
{
  "requests": [
    {
      "method": "POST",
      "url": "/api/collections/posts/records",
      "body": { "title": "Created in batch" }
    },
    {
      "method": "PATCH",
      "url": "/api/collections/posts/records/post_1",
      "body": { "title": "Updated in batch" }
    }
  ]
}
```

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `status` | integer | Per-request HTTP status. | `200` |
| `body` | mixed | Per-request response body, when any. | `{ "id": "post_1" }` |

#### Response Example

```json
[
  { "status": 200, "body": { "id": "post_1", "title": "Created in batch" } },
  { "status": 200, "body": { "id": "post_1", "title": "Updated in batch" } }
]
```

## GraphQL

### POST /api/graphql

#### Function

Executes GraphQL queries and mutations over record collections. Auth is optional but collection rules are enforced.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `query` | string | Yes | GraphQL operation. | `query { records(collection: "posts") { items { id } } }` |
| `variables` | object | No | GraphQL variables. | `{ "collection": "posts" }` |

#### Built-in Operations

| Operation | Arguments | Meaning |
| --- | --- | --- |
| `records` | `collection`, `page`, `perPage`, `sort`, `filter`, `skipTotal`, `expand` | List records. |
| `record` | `collection`, `id`, `expand` | Get one record. |
| `createRecord` | `collection`, `data`, `expand` | Create record. |
| `updateRecord` | `collection`, `id`, `data`, `expand` | Update record. |
| `deleteRecord` | `collection`, `id` | Delete record. |

#### Request Example

```json
{
  "query": "query Posts($collection: String!) { records(collection: $collection, perPage: 10) { page totalItems items { id data } } }",
  "variables": { "collection": "posts" }
}
```

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `data` | object | GraphQL data result. | `{ "records": { "items": [] } }` |
| `errors` | array | GraphQL errors, if any. | `[]` |

#### Response Example

```json
{
  "data": {
    "records": {
      "page": 1,
      "totalItems": 0,
      "items": []
    }
  }
}
```

## SQL

### POST /api/sql/execute

#### Function

Executes a raw SQL statement. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `query` | string | Yes | SQL statement to execute. | `SELECT id, email FROM users LIMIT 10` |

#### Request Example

```json
{
  "query": "SELECT id, email FROM users LIMIT 10"
}
```

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `columns` | array of strings | Column names for returned rows. For non-row statements this contains `rows_affected`. | `["id", "email"]` |
| `rows` | array of string arrays | Rows formatted as strings. | `[["u1", "a@example.com"]]` |
| `rowsAffected` | integer | Present for non-row-returning statements. | `3` |

#### Response Example

```json
{
  "columns": ["id", "email"],
  "rows": [["u1", "a@example.com"]]
}
```

## Logs

### GET /api/logs

#### Function

Lists request/activity logs. Requires superuser authentication.

#### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `page` | integer | No | Page number. | `1` |
| `perPage` | integer | No | Page size. | `50` |
| `sort` | string | No | Sort log fields. | `-created` |
| `filter` | string | No | Filter over `id`, `created`, `updated`, `level`, `message`, or `data`. | `level = "error"` |
| `skipTotal` | boolean | No | Skip total count when supported. | `false` |

#### Response

Paginated log entry list.

### GET /api/logs/stats

#### Function

Returns aggregated log statistics. Requires superuser authentication.

#### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `filter` | string | No | Optional filter expression. | `created >= "2026-07-01"` |

#### Response

Stats object keyed by aggregation buckets.

### GET /api/logs/{id}

#### Function

Returns one log entry by id. Requires superuser authentication.

#### Response

Log entry object.

## Cron

### GET /api/crons

#### Function

Lists registered cron jobs. Requires superuser authentication.

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Cron job id. | `backup` |
| `expression` | string | Cron expression. | `0 2 * * *` |
| `next` | string | Next scheduled run, when available. | `2026-07-09T02:00:00Z` |

### POST /api/crons/{id}

#### Function

Runs a cron job immediately. Requires superuser authentication.

#### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Cron job id. | `backup` |

#### Response

`204 No Content`.
