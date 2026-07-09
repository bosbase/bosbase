# Collections and Records

This document covers schema management and collection record CRUD APIs.

## Collection Object

### Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Collection id. | `pbc_123456` |
| `name` | string | Collection name. | `posts` |
| `type` | string | Collection type: `base`, `auth`, or `view`. | `base` |
| `system` | boolean | Whether this is a system collection. | `false` |
| `fields` | array | Field definitions. | `[{ "name": "title", "type": "text" }]` |
| `indexes` | array | SQL index definitions. | `["CREATE INDEX idx_posts_title ON posts (title)"]` |
| `listRule` | string or null | Rule for listing records. | `status = "published"` |
| `viewRule` | string or null | Rule for viewing one record. | `id != ""` |
| `createRule` | string or null | Rule for creating records. | `@request.auth.id != ""` |
| `updateRule` | string or null | Rule for updating records. | `createdBy = @request.auth.id` |
| `deleteRule` | string or null | Rule for deleting records. | `createdBy = @request.auth.id` |
| `externalTable` | boolean | Base collection option for externally managed SQL tables. | `true` |
| `viewQuery` | string | View collection SQL query. | `SELECT id, title FROM posts` |
| `authRule` | string or null | Auth collection rule checked before issuing token. | `verified = true` |
| `manageRule` | string or null | Auth collection manager rule. | `role = "admin"` |

### Example

```json
{
  "name": "posts",
  "type": "base",
  "fields": [
    { "name": "title", "type": "text", "required": true },
    { "name": "status", "type": "select", "values": ["draft", "published"] }
  ],
  "listRule": "status = \"published\"",
  "viewRule": "status = \"published\"",
  "createRule": "@request.auth.id != \"\"",
  "updateRule": "createdBy = @request.auth.id",
  "deleteRule": "createdBy = @request.auth.id"
}
```

## GET /api/collections

### Function

Lists collections. Requires superuser authentication.

### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `page` | integer | No | Page number. | `1` |
| `perPage` | integer | No | Items per page. | `50` |
| `sort` | string | No | Sort by `id`, `created`, `updated`, `name`, `system`, or `type`. | `name` |
| `filter` | string | No | Filter expression over allowed collection fields. | `type = "auth"` |
| `skipTotal` | boolean | No | Skip total calculation if supported. | `true` |

### Response

Paginated response with collection objects in `items`.

### Response Example

```json
{
  "page": 1,
  "perPage": 30,
  "totalItems": 1,
  "totalPages": 1,
  "items": [
    { "id": "pbc_posts", "name": "posts", "type": "base", "system": false }
  ]
}
```

## POST /api/collections

### Function

Creates a collection. Requires superuser authentication.

### Request Body Fields

Use the [Collection Object](#collection-object) fields. Required fields are `name` and `type`; `fields`, rules, indexes, and type-specific options are optional.

### Request Example

```json
{
  "name": "posts",
  "type": "base",
  "fields": [
    { "name": "title", "type": "text", "required": true }
  ]
}
```

### Response

The created collection object.

## GET /api/collections/{collection}

### Function

Returns one collection by id or name. Requires superuser authentication.

### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `collection` | string | Collection id or name. | `posts` |

### Response

Collection object.

## PATCH /api/collections/{collection}

### Function

Updates a collection by id or name. Requires superuser authentication.

### Request Body Fields

Partial [Collection Object](#collection-object). Include only fields to change.

### Request Example

```json
{
  "listRule": "status = \"published\"",
  "fields": [
    { "name": "title", "type": "text", "required": true },
    { "name": "summary", "type": "text" }
  ]
}
```

### Response

Updated collection object.

## DELETE /api/collections/{collection}

### Function

Deletes a collection. Requires superuser authentication.

### Response

`204 No Content`.

## DELETE /api/collections/{collection}/truncate

### Function

Deletes all records in a collection without deleting the collection. Requires superuser authentication.

### Response

`204 No Content`.

## PUT /api/collections/import

### Function

Imports multiple collection definitions. Requires superuser authentication.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `collections` | array | Yes | Collection definitions to import. | `[{ "name": "posts", "type": "base" }]` |
| `deleteMissing` | boolean | No | Delete collections that are not present in the import payload. | `false` |

### Request Example

```json
{
  "deleteMissing": false,
  "collections": [
    { "name": "posts", "type": "base", "fields": [] }
  ]
}
```

### Response

`204 No Content`.

## SQL Table Collection Helpers

### POST /api/collections/sql

Legacy alias of `POST /api/collections/sql/import`.

### POST /api/collections/sql/import

#### Function

Creates/registers collections from SQL table definitions. Requires superuser authentication. If `sql` is provided, it is executed before registering the collection.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `tables` | array | Yes | SQL table definitions. | `[{ "name": "events", "sql": "CREATE TABLE events (...)" }]` |
| `tables[].name` | string | Yes | Table name. Must not start with `_`. | `events` |
| `tables[].sql` | string | No | SQL statement to create/prepare the table. | `CREATE TABLE events (id text primary key)` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `created` | array | Collections created from SQL tables. | `[]` |
| `skipped` | array | Table names skipped because collections already existed. | `["events"]` |

#### Example

```json
{
  "created": [
    { "name": "events", "type": "base", "externalTable": true }
  ],
  "skipped": []
}
```

### POST /api/collections/sql/tables

#### Function

Registers existing SQL tables as collections. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `tables` | array of strings | Yes | Existing SQL table names. | `["events"]` |

#### Response

Array of created collection definitions.

## Collection Metadata Endpoints

| Method | Path | Auth | Function | Response |
| --- | --- | --- | --- | --- |
| `GET` | `/api/collections/meta/scaffolds` | Superuser | Returns field/collection scaffold metadata. | Scaffold metadata object. |
| `GET` | `/api/collections/{collection}/schema` | Superuser | Returns schema info for one collection. | Schema info object. |
| `GET` | `/api/collections/schemas` | Superuser | Returns schema info for all collections. | Array/object of schema info. |

## Record CRUD

Record request and response fields depend on the collection schema. All endpoints enforce collection rules unless the caller is a superuser.

### GET /api/collections/{collection}/records

#### Function

Lists records in a collection.

#### Query Parameters

See [Common List Query Parameters](./00-overview.md#common-list-query-parameters).

#### Response Fields

Paginated response where `items` is an array of record objects.

#### Response Example

```json
{
  "page": 1,
  "perPage": 30,
  "totalItems": 1,
  "totalPages": 1,
  "items": [
    {
      "id": "post_1",
      "collectionName": "posts",
      "title": "Hello",
      "created": "2026-07-08 12:00:00.000Z",
      "updated": "2026-07-08 12:00:00.000Z"
    }
  ]
}
```

### GET /api/collections/{collection}/records/count

#### Function

Returns only the number of records matching the list rule and optional filter.

#### Query Parameters

Same as list; `filter` is the most common parameter.

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `count` | integer | Matching record count. | `42` |

#### Response Example

```json
{ "count": 42 }
```

### GET /api/collections/{collection}/records/{id}

#### Function

Returns a single record by id.

#### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `collection` | string | Collection id or name. | `posts` |
| `id` | string | Record id. | `post_1` |

#### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `expand` | string | No | Relations to expand. | `author` |
| `fields` | string | No | Field projection. | `id,title` |

#### Response

Record object.

### POST /api/collections/{collection}/records

#### Function

Creates a record. JSON and `multipart/form-data` are supported. Use multipart for file fields.

#### Request Body Fields

Dynamic collection fields. For auth collections, fields such as `email`, `password`, `passwordConfirm`, `verified`, and custom fields may exist depending on schema and access level.

| Field Pattern | Type | Meaning | Example |
| --- | --- | --- | --- |
| `{fieldName}` | mixed | Set a regular field value. | `{ "title": "Hello" }` |
| file field `{fieldName}` | file or array | Upload/replace file field values in multipart requests. | `avatar=@me.png` |
| `+{fieldName}` | file or array | Prepend files to a file field. | `+photos=@a.jpg` |
| `{fieldName}+` | file or array | Append files to a file field. | `photos+=@b.jpg` |
| `{fieldName}-` | string or array | Remove named files from a file field. | `{ "photos-": ["old.jpg"] }` |

#### Request Example

```json
{
  "title": "Hello",
  "status": "published",
  "tags": ["docs", "api"]
}
```

#### Response

Created record object.

### PATCH /api/collections/{collection}/records/{id}

#### Function

Updates a record. JSON and `multipart/form-data` are supported.

#### Request Body Fields

Partial dynamic record fields. File field modifiers are the same as create.

#### Request Example

```json
{
  "title": "Updated title",
  "tags+": ["release"]
}
```

#### Response

Updated record object.

### DELETE /api/collections/{collection}/records/{id}

#### Function

Deletes a record.

#### Response

`204 No Content`.

## POST /api/collections/{collection}/records/search

### Function

Performs PostgreSQL/pgvector nearest-neighbor search against a `vector` field in a normal record collection. The endpoint enforces the collection `listRule` and returns a normal record list response.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `field` | string | Yes | Name of the target vector field. | `embedding` |
| `queryVector` | array of numbers | Yes | Query vector. Length must match the field dimension. | `[0.1, 0.2, 0.3]` |
| `limit` | integer | No | Max nearest records. Defaults to `20`, max `200`. | `10` |
| `distance` | string | No | Metric override: `cosine`, `l2`, `inner_product`, or `l1`. | `cosine` |
| `filter` | string | No | Record filter applied before vector ordering. | `status = "published"` |
| `includeDistance` | boolean | No | Include `_distance` and `_score` fields. Defaults to true when omitted. | `true` |

### Request Example

```json
{
  "field": "embedding",
  "queryVector": [0.12, 0.25, 0.33],
  "limit": 5,
  "distance": "cosine",
  "filter": "status = \"published\"",
  "includeDistance": true
}
```

### Response Fields

Same as paginated record list. When `includeDistance` is true, each record can include:

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `_distance` | number | Raw vector distance. Lower is nearer. | `0.12` |
| `_score` | number | Normalized similarity score. Higher is more similar. | `0.88` |

### Response Example

```json
{
  "page": 1,
  "perPage": 5,
  "totalItems": 5,
  "totalPages": 1,
  "items": [
    {
      "id": "doc_1",
      "title": "Vector search guide",
      "_distance": 0.12,
      "_score": 0.88
    }
  ]
}
```
