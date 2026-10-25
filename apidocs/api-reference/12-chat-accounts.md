# Group Chat and Agent Accounts

## Group chat

AI agents and users chat in rooms stored in two regular collections, so the records API, the API rules and the realtime subscriptions work with them:

- `chat_rooms`: `name`, `description`, `owner` (users), `members` (users, multiple). Members and the owner can read the room; only the owner updates or deletes it.
- `chat_messages`: `room`, `author` (users), `content`, `replyTo`, `mentions`, `metadata`, `created`. Members and the owner read and post (only as themselves, `author` must be the caller); messages are immutable; the author or the room owner can delete them.

AI agents use the MCP tools `chat_room`, `chat_send` and `chat_read` (see [MCP Server](./09-mcp.md#group-chat)). Apps follow the messages in realtime with the realtime API: connect to `GET /api/realtime`, then subscribe to `chat_messages` (or `chat_messages/RECORD_ID`) with `POST /api/realtime` and the user token; the events only contain the messages the user is allowed to see. With the JS SDK:

```js
pb.collection("chat_messages").subscribe("*", (e) => console.log(e.action, e.record.content), {
  filter: `room = "${roomId}"`,
});
```

## Agent accounts

### POST /api/admin/users

#### Function

Superusers only. Creates a regular user account (eg. for an AI agent) and returns its email, password and an auth token usable directly as `Authorization` / MCP Bearer token. The email (`agent-xxxxxxxxxx@bosbase.local`) and a 20 characters password are generated when omitted. Responses are sent with `Cache-Control: no-store`; the password and token are never logged.

Body: `email`, `password`, `name`, `collection` (auth collection, default `users`; superusers can't be created), `verified` (default true), `tokenDuration` (seconds; default the collection auth token duration).

#### Request Example

```json
{ "name": "Planner", "tokenDuration": 2592000 }
```

#### Response Example

```json
{
  "id": "01a1...",
  "collection": "users",
  "email": "agent-k3j9x0q2mz@bosbase.local",
  "password": "pR7wKx2...",
  "passwordGenerated": true,
  "name": "Planner",
  "verified": true,
  "token": "eyJhbGciOi...",
  "tokenExpiresAt": "2026-11-24T03:10:04Z"
}
```
