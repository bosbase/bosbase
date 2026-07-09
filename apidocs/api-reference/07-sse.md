# Server-Sent Events APIs

This document covers BosBase SSE endpoints.

## GET /api/realtime

### Function

Opens a realtime Server-Sent Events connection. The server registers a subscription client and sends an initial `PB_CONNECT` event containing a `clientId`. Use this `clientId` with `POST /api/realtime` to configure subscriptions.

### Authentication

Optional. If a token is supplied, it is associated with the realtime client and used by realtime rule checks. A client may be upgraded from guest to auth during subscription update if the previous auth state is compatible.

### Request

No body.

### Response Headers

| Header | Value | Meaning |
| --- | --- | --- |
| `Content-Type` | `text/event-stream` | SSE stream. |
| `Cache-Control` | `no-store` | Prevent caching. |
| `X-Accel-Buffering` | `no` | Disable proxy buffering for NGINX-compatible proxies. |

### Initial Event Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| event name | string | Initial event name. | `PB_CONNECT` |
| event id | string | SSE id, same as client id. | `client_123` |
| `data.clientId` | string | Realtime client id used for subscriptions. | `client_123` |

### Initial Event Example

```text
event: PB_CONNECT
id: client_123
data: {"clientId":"client_123"}

```

## POST /api/realtime

### Function

Sets or replaces subscriptions for an existing realtime SSE client.

### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `clientId` | string | Yes | Client id received from `PB_CONNECT`. | `client_123` |
| `subscriptions` | array of strings | No | Subscription topics. Previous subscriptions are replaced. | `["posts", "posts/post_1"]` |

### Subscription Names

| Pattern | Meaning | Example |
| --- | --- | --- |
| `{collection}` | Subscribe to create/update/delete events for accessible records in a collection. | `posts` |
| `{collection}/{recordId}` | Subscribe to changes for one record. | `posts/post_1` |
| `@oauth2` | Receive OAuth2 redirect callback data. | `@oauth2` |

### Request Example

```json
{
  "clientId": "client_123",
  "subscriptions": ["posts", "posts/post_1", "@oauth2"]
}
```

### Response

`204 No Content`.

## Realtime Message Format

Realtime messages are SSE events. Exact event names depend on the internal subscription message name. Record events include an action and record payload.

### Common Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| event name | string | Message/subscription event name. | `posts` |
| event id | string | Client id as written by the subscription message writer. | `client_123` |
| `data.action` | string | Record action where applicable: `create`, `update`, `delete`. | `update` |
| `data.record` | object | Record payload where applicable. | `{ "id": "post_1" }` |

### Record Event Example

```text
event: posts
id: client_123
data: {"action":"update","record":{"id":"post_1","title":"Updated"}}

```

### OAuth2 Redirect Event Example

```text
event: @oauth2
id: client_123
data: {"state":"client_123","code":"4/0Ab..."}

```

## GET /api/scripts/{name}/execute/sse

### Function

Executes a stored script and returns the final result as one SSE message. This endpoint does not stream incremental output; it uses SSE framing for the final result.

### Authentication

Permission-based. Access depends on script permission configuration.

### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `name` | string | Script name. | `hello.py` |

### Query Parameters

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `args` | string or repeated values | No | Script arguments, parsed by helper from query. | `10` |
| `arguments` | string or repeated values | No | Legacy alias for args. | `10` |
| `function_name` | string | No | Function to execute. Defaults to `main`. | `main` |

### Response Headers

| Header | Value |
| --- | --- |
| `Content-Type` | `text/event-stream` |
| `Cache-Control` | `no-store` |
| `X-Accel-Buffering` | `no` |

### Response Data Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `output` | mixed | Script output. | `30` |

### Response Example

```text
data:{"output":30}

```
