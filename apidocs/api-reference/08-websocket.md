# WebSocket APIs

This document covers BosBase WebSocket endpoints.

## GET /api/pubsub

### Function

Opens a WebSocket connection to the BosBase pub/sub hub. Clients can subscribe to topics, unsubscribe, ping, and publish JSON messages.

### Authentication

Optional for connecting and subscribing. Publishing requires an authenticated client.

### Connection URL

```text
ws://127.0.0.1:8090/api/pubsub
```

Use `wss://` when served over HTTPS.

### Initial Server Message

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `type` | string | Message type. | `ready` |
| `clientId` | string | Pub/sub client id. | `client_123` |

```json
{ "type": "ready", "clientId": "client_123" }
```

## Pub/Sub Client Messages

All client messages are JSON text frames.

### ping

#### Function

Checks connection liveness.

#### Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `type` | string | Yes | Must be `ping`. | `ping` |
| `requestId` | string | No | Client correlation id echoed by server. | `req_1` |

#### Example

```json
{ "type": "ping", "requestId": "req_1" }
```

#### Server Response

```json
{ "type": "pong", "requestId": "req_1" }
```

### subscribe

#### Function

Subscribes the WebSocket client to a topic.

#### Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `type` | string | Yes | Must be `subscribe`. | `subscribe` |
| `topic` | string | Yes | Topic name. | `chat:general` |
| `requestId` | string | No | Client correlation id. | `req_2` |

#### Example

```json
{ "type": "subscribe", "topic": "chat:general", "requestId": "req_2" }
```

#### Server Response

```json
{ "type": "subscribed", "requestId": "req_2" }
```

### unsubscribe

#### Function

Unsubscribes from one topic, or from all topics if `topic` is omitted or empty.

#### Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `type` | string | Yes | Must be `unsubscribe`. | `unsubscribe` |
| `topic` | string | No | Topic name. Omit to clear all subscriptions. | `chat:general` |
| `requestId` | string | No | Client correlation id. | `req_3` |

#### Server Response

```json
{ "type": "unsubscribed", "requestId": "req_3" }
```

### publish

#### Function

Publishes a JSON payload to a topic. Requires authenticated WebSocket connection. Payload max is 256 KiB.

#### Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `type` | string | Yes | Must be `publish`. | `publish` |
| `topic` | string | Yes | Topic name. | `chat:general` |
| `data` | JSON | No | JSON payload. Defaults to `null` if omitted. | `{ "text": "Hello" }` |
| `requestId` | string | No | Client correlation id. | `req_4` |

#### Example

```json
{
  "type": "publish",
  "topic": "chat:general",
  "data": { "text": "Hello" },
  "requestId": "req_4"
}
```

#### Server Acknowledgement

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `type` | string | Message type. | `published` |
| `requestId` | string | Correlation id. | `req_4` |
| `id` | string | Persisted message id. | `msg_123` |
| `topic` | string | Topic name. | `chat:general` |
| `created` | string | Creation timestamp. | `2026-07-08 12:00:00Z` |

```json
{
  "type": "published",
  "requestId": "req_4",
  "id": "msg_123",
  "topic": "chat:general",
  "created": "2026-07-08 12:00:00Z"
}
```

## Pub/Sub Broadcast Message

Subscribers receive published messages as JSON text frames.

### Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `type` | string | Always `message`. | `message` |
| `id` | string | Message id. | `msg_123` |
| `topic` | string | Topic name. | `chat:general` |
| `data` | JSON | Published payload. | `{ "text": "Hello" }` |
| `created` | string | Creation timestamp. | `2026-07-08 12:00:00Z` |

### Example

```json
{
  "type": "message",
  "id": "msg_123",
  "topic": "chat:general",
  "data": { "text": "Hello" },
  "created": "2026-07-08 12:00:00Z"
}
```

## Pub/Sub Error Message

### Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `type` | string | Always `error`. | `error` |
| `requestId` | string | Correlation id when available. | `req_4` |
| `message` | string | Error message. | `authentication required to publish` |

### Example

```json
{
  "type": "error",
  "requestId": "req_4",
  "message": "authentication required to publish"
}
```

## GET /api/scripts/{name}/execute/ws

### Function

Opens a WebSocket, executes a stored script, sends one JSON result or error, then closes the connection.

### Authentication

Permission-based. Access depends on script permission configuration.

### Connection URL

```text
ws://127.0.0.1:8090/api/scripts/hello.py/execute/ws
```

### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `name` | string | Script name. | `hello.py` |

### Query Parameters

Execution parameters may be supplied in the URL query. If no execution parameters are present, the server waits for one JSON text or binary frame from the client.

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `args` | string or repeated values | No | Script args. | `10` |
| `arguments` | string or repeated values | No | Legacy args alias. | `10` |
| `function_name` | string | No | Function to call. Defaults to `main`. | `main` |

### Client Message Fields

Send this only when execution parameters were not supplied through query.

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `args` | array of strings | No | Positional arguments. | `["10", "20"]` |
| `arguments` | array of strings | No | Backward-compatible alias for `args`. | `["10"]` |
| `function_name` | string | No | Function to call. Defaults to `main`. | `main` |

### Client Message Example

```json
{
  "args": ["10", "20"],
  "function_name": "main"
}
```

### Success Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `output` | mixed | Script output. | `30` |

### Success Response Example

```json
{ "output": 30 }
```

### Error Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `status` | integer | HTTP-like error status. | `400` |
| `message` | string | Error message. | `Invalid websocket payload.` |
| `data` | object | Validation details, if any. | `{}` |

### Error Response Example

```json
{
  "status": 400,
  "message": "Invalid websocket payload.",
  "data": {}
}
```
