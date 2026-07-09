# Overview, Authentication, Errors, and Shared Types

## Base URL and Prefixes

| Item | Value | Notes |
| --- | --- | --- |
| Default local base URL | `http://127.0.0.1:8090` | Used when the server is started without explicit domains. |
| Main API prefix | `/api` | Most HTTP/SSE/WebSocket integration endpoints are under this prefix. |
| Admin UI assets | `/_/{path...}` | Static admin UI resources, not intended as an API surface. |
| Output proxy | `/output` and `/output/{path...}` | Reverse proxy to `BOOSTER_URL` or `http://127.0.0.1:2678`. |

## Authentication

BosBase loads a record auth token from the first available source below.

| Source | Type | Example | Meaning |
| --- | --- | --- | --- |
| `Authorization` header | string | `Authorization: <token>` | Raw JWT/auth token. |
| `Authorization` header with bearer prefix | string | `Authorization: Bearer <token>` | Bearer prefix is accepted for compatibility. |
| Query parameter | string | `?token=<token>` | Useful for file downloads and some callback flows. |
| `pb_auth` cookie | URL-escaped JSON string | `%7B%22token%22%3A%22...%22%7D` | Cookie JSON must contain a `token` field. |

### Auth Levels

| Level | Meaning |
| --- | --- |
| Public | No token is required. Collection rules may still restrict access where applicable. |
| Auth | Any valid auth record token is required. |
| Same auth collection | The token must belong to the same auth collection named by the `{collection}` path parameter. |
| Superuser | The token must belong to the `_superusers` auth collection. |
| Permission-based | The endpoint checks stored script/WASM permissions and may allow guests, regular auth records, or superusers depending on configuration. |

## Standard Error Response

All standard API errors are returned as JSON.

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `status` | integer | HTTP status code. | `400` |
| `message` | string | Human-readable error message. | `Something went wrong while processing your request.` |
| `data` | object | Validation or safe error details. Empty object when no public details are available. | `{ "email": { "code": "validation_required", "message": "Cannot be blank." } }` |

### Example

```json
{
  "status": 400,
  "message": "Something went wrong while processing your request.",
  "data": {}
}
```

## Common List Query Parameters

These query parameters are used by record lists and several administrative list endpoints.

| Field | Type | Required | Default | Meaning | Example |
| --- | --- | --- | --- | --- | --- |
| `page` | integer | No | `1` | Page number. | `2` |
| `perPage` | integer | No | `30` | Items per page. Search provider max is `1000`. | `100` |
| `sort` | string | No | endpoint-defined | Comma-separated fields; prefix with `-` for descending. | `-created,name` |
| `filter` | string | No | none | Filter expression. | `status = "active"` |
| `skipTotal` | boolean | No | `false` | Skip total count calculation when supported. | `true` |
| `expand` | string | No | none | Comma-separated relation fields to expand. | `author,comments.user` |
| `fields` | string | No | all allowed fields | Optional output field selection when enrichment supports it. | `id,title,expand.author.name` |

## Paginated Response Shape

### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `page` | integer | Current page number. | `1` |
| `perPage` | integer | Page size. | `30` |
| `totalItems` | integer | Total matching items. May be `-1` or omitted for skip-total behavior depending on endpoint. | `123` |
| `totalPages` | integer | Total pages. | `5` |
| `items` | array | Returned items. | `[]` |

### Example

```json
{
  "page": 1,
  "perPage": 30,
  "totalItems": 123,
  "totalPages": 5,
  "items": []
}
```

## Record Object Shape

Record fields are dynamic and are defined by the target collection schema. Standard system fields are commonly present.

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Record id. | `abc123` |
| `collectionId` | string | Collection id. | `pbc_123` |
| `collectionName` | string | Collection name. | `posts` |
| `created` | string | Creation timestamp. | `2026-07-08 12:00:00.000Z` |
| `updated` | string | Update timestamp. | `2026-07-08 12:10:00.000Z` |
| `expand` | object | Expanded relations, when requested. | `{ "author": { "id": "u1" } }` |
| Custom fields | mixed | Collection-defined fields. | `{ "title": "Hello" }` |

## Collection Rule Behavior

Collection-scoped endpoints honor the collection API rules.

| Rule | Affects |
| --- | --- |
| `listRule` | Record list, count, realtime collection subscriptions, vector search over records. |
| `viewRule` | Record view, file access through protected collection rules. |
| `createRule` | Record creation. |
| `updateRule` | Record update. |
| `deleteRule` | Record deletion. |
| `authRule` | Final auth response issuance. |
| `manageRule` | Elevated auth record management permissions. |

`nil` rules are superuser-only. Empty string rules allow the action. Superusers bypass collection rules.
