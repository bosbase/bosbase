---
name: bosbase
description: Operate a BosBase backend through its HTTP API and its MCP server (/api/mcp). Use when you need to manage collections and records, authentication, files, realtime, AI memory and knowledge bases, conversation memory, knowledge graph retrieval, shared memory spaces, agent group chat, object storage, the LLM relay, embedding providers, diagnostics, backups, scripts or any other BosBase feature, as a regular user or as a superuser.
---

# BosBase

BosBase is a PocketBase-based backend on PostgreSQL + pgvector. Everything is available over the **HTTP API**; AI-oriented features are also exposed as **MCP tools** on `POST /api/mcp`. Both use the same tokens and the same permissions.

Complete inventories (generated from a live server):

- [reference/http-endpoints.md](reference/http-endpoints.md): every HTTP endpoint with its access level (public / user / superuser).
- [reference/mcp-tools.md](reference/mcp-tools.md): every MCP tool with its access level and arguments.

Full request/response schemas: the `api_docs` MCP tool, or `apidocs/api-reference/` in this repository.

## 1. Connect and authenticate

```sh
# regular user
curl -X POST $BASE/api/collections/users/auth-with-password \
  -H 'Content-Type: application/json' -d '{"identity":"me@example.com","password":"..."}'
# superuser
curl -X POST $BASE/api/collections/_superusers/auth-with-password \
  -H 'Content-Type: application/json' -d '{"identity":"admin@example.com","password":"..."}'
```

The response contains `token` and `record`. Send it as `Authorization: <token>` or `Authorization: Bearer <token>` (HTTP), and `Authorization: Bearer <token>` for MCP.

MCP client configuration:

```sh
claude mcp add --transport http bosbase $BASE/api/mcp --header "Authorization: Bearer <token>"
```

Raw MCP call (JSON-RPC 2.0, Streamable HTTP, stateless):

```sh
curl -X POST $BASE/api/mcp -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"deployment"}}}'
```

Tool errors come back as `result.isError: true` with the message in `result.content[0].text`.

## 2. Know your role

- Authenticated against `_superusers` → **superuser**: full access to everything, including settings, collections schema, logs, backups, SQL/GraphQL, diagnostics, embedding providers and other users' data.
- Authenticated against an auth collection such as `users` → **regular user**: access defined by the collection API rules and the per-feature ownership rules below.
- Over MCP, `tools/list` tells you: superusers see the superuser-only tools (`diagnostics_*`, `embedding_*`, `user_create`, `collections_list`, `collection_get`, `memory_stats`).

Never use a superuser token where a user identity matters (chat messages, personal memories): create a dedicated user account per agent instead (`user_create`).

## 3. Role matrix

| Area | Regular user | Superuser only |
| --- | --- | --- |
| Collections & records | CRUD records allowed by each collection's API rules | Create/alter/delete collections, bypass rules, import/export schema |
| Auth | Sign in (password, OAuth2, OTP), refresh, verify email, reset password, change email | Impersonate users, create accounts with `user_create` / `POST /api/admin/users` |
| Files | Upload via record file fields, request file tokens | — |
| Realtime | Subscribe (events filtered by the API rules), WebSocket pub/sub | — |
| Batch / GraphQL / SQL | Batch requests when enabled in the settings | GraphQL, raw SQL, enabling batch |
| Settings, logs, backups, cron | — | All |
| Memory, notes, knowledge graph | Own private space; read public knowledge bases | Any user's space (`ownerId`), write public knowledge bases (`shared: true`), `memory_stats` |
| Conversations, memory context | Own (always private) | Any user's (`ownerId`) |
| Shared memory spaces | Create spaces, request to join; the **owner** approves/rejects/removes members; authors or owner/admins edit and delete content | Access any space (`space`) |
| Group chat | Create rooms, add members (owner), send/read messages as yourself | Read rooms; chats through regular agent accounts |
| Object storage | Upload to own or shared providers, own history, own providers unless `STORAGE_ALLOW_USER_PROVIDERS=false` | Shared providers and the default one, all histories (`all: true`) |
| LLM relay | Own channels + shared ones, own usage | Shared channels, all usage |
| Embedding providers | — (used automatically) | Configure, test, set default/priority, reindex |
| Vectors, LLM documents, LangChainGo, cache, Redis, scripts, WASM | See the access column in `reference/http-endpoints.md` (most are superuser) | — |
| Diagnostics | — | Health, error logs, log files, reports |

## 4. Collections and records (HTTP)

```sh
# superuser: create a collection
curl -X POST $BASE/api/collections -H "Authorization: $SU" -H 'Content-Type: application/json' -d '{
  "name":"posts","type":"base","listRule":"","viewRule":"","createRule":"@request.auth.id != \"\"",
  "updateRule":"@request.auth.id != \"\"","deleteRule":"@request.auth.id != \"\"",
  "fields":[{"name":"title","type":"text","required":true},{"name":"author","type":"relation","collectionId":"_pb_users_auth_","maxSelect":1}]}'
# anyone allowed by the rules: list with filter, sort, expand
curl "$BASE/api/collections/posts/records?filter=(title~'go')&sort=-created&expand=author&perPage=20" -H "Authorization: $TOKEN"
# create / update / delete
curl -X POST  $BASE/api/collections/posts/records -H "Authorization: $TOKEN" -H 'Content-Type: application/json' -d '{"title":"Hello"}'
curl -X PATCH $BASE/api/collections/posts/records/$ID -H "Authorization: $TOKEN" -H 'Content-Type: application/json' -d '{"title":"Hi"}'
curl -X DELETE $BASE/api/collections/posts/records/$ID -H "Authorization: $TOKEN"
```

- Filter syntax: `=`, `!=`, `>`, `>=`, `<`, `<=`, `~` (contains), `!~`, `&&`, `||`, parentheses; `?=` / `?~` match any item of a multiple field (eg. `tags.title ?= "go"`); modifiers `:length`, `:each`; `@request.auth.id` in rules.
- Rules: `null` = superusers only, `""` = everyone, otherwise a filter expression.
- Relation modifiers on update: `"members+": [ids]`, `"members-": [ids]`.
- Over MCP use `records_list`, `record_get`, `record_create`, `record_update`, `record_delete` (same rules), or `api_request` for any endpoint.

## 5. Realtime

```text
GET  /api/realtime                       -> first event PB_CONNECT {"clientId": "..."}
POST /api/realtime {"clientId":"...","subscriptions":["posts","chat_messages"]}  (with the user token)
```

Events (`{action: create|update|delete, record}`) are only delivered for records the token may view. JS SDK: `pb.collection("posts").subscribe("*", cb, {filter: ...})`. Realtime and WebSocket endpoints can't be used through `api_request`.

## 6. AI memory and knowledge (MCP)

Start every conversation with `memory_context` (query = the user's request). The `initialize` instructions already contain the pinned memories, latest sessions, open todos and project summaries.

| Goal | Tool |
| --- | --- |
| Store a fact (avoid duplicates, pin stable preferences) | `memory_store {content, title, project, tags, dedupe: true, pinned}` |
| Find memories | `memory_search {query, project, mode}` |
| Update / delete | `memory_update {id, ...}`, `memory_delete {ids}` |
| Relate memories, explore the graph | `memory_link`, `memory_unlink`, `memory_graph`, `memory_path` |
| Save the conversation outcome | `memory_remember {summary, facts, decisions, todos, project}` |
| Let the server extract memories from a transcript (needs an LLM channel) | `memory_extract {text, project}` |
| Keep conversation turns and recall them later | `conversation_save {messages, conversationId}`, `conversation_recall {query}` |
| Condense long conversations / a project | `conversation_summarize`, `memory_consolidate {project}` |
| Obsidian-like notes | `note_write {project, path, content, mode}`, `note_read`, `note_list`, `note_move`, `note_backlinks`, `note_daily`, `note_history`, `note_restore` |
| Associative retrieval | `knowledge_associate {query, project}` (results explain their `via` path), `note_graph`, `knowledge_suggest_links` |
| What did I do before? | `activity_recent`, `activity_log` |

Notes are Markdown: frontmatter properties, `#tags` and `[[wikilinks]]` (they become `links_to` graph edges). Mark todos done with `memory_update` adding the tag `done`.

## 7. Shared memory spaces

```json
{"name":"space","arguments":{"action":"create","name":"product-team","listed":true}}
{"name":"space","arguments":{"action":"join","space":"product-team","message":"Backend engineer"}}
{"name":"space_member","arguments":{"action":"approve","space":"product-team","memberId":"<user id>"}}
{"name":"memory_store","arguments":{"space":"product-team","content":"v2.0 ships on Dec 1"}}
```

- Only the **owner** (creator) approves, rejects, removes members and sets roles; admins moderate content; pending/rejected users have no access.
- Every memory records `author` / `authorName` / `updatedBy`. Personal searches include the user's spaces (`includeSpaces`).
- HTTP: `/api/memory/spaces`, `/api/memory/spaces/{space}/join`, `/members/{member}/approve|reject`, `DELETE /members/{member}`, `/memories`.

## 8. Group chat between agents

1. Superuser creates one account per agent: `user_create {"name":"Planner"}` (or `POST /api/admin/users`) → `email`, `password`, `token`.
2. An agent creates a room: `chat_room {"action":"create","name":"release","members":["coder@example.com"]}`.
3. Loop: `chat_read {room}` (get `cursor`) → `chat_send {room, content}` → `chat_read {room, after: cursor, wait: 30}` (returns as soon as someone writes).
4. Apps follow the room with a realtime subscription to `chat_messages`.

Only members read/post; you can only post as yourself; messages are immutable.

## 9. Object storage

```json
{"name":"storage_provider","arguments":{"action":"create","name":"r2","type":"r2","endpoint":"https://<account>.r2.cloudflarestorage.com","bucket":"media","accessKey":"...","secretKey":"...","publicUrl":"https://cdn.example.com","shared":true,"isDefault":true}}
{"name":"storage_upload","arguments":{"filename":"logo.png","contentBase64":"iVBOR..."}}
```

- `storage_upload` returns `url` with `urlType`: `public` (permanent: `publicUrl` or `publicRead`) or `presigned` (default 7 days, `urlExpiresSeconds`). Large files: `storage_presign_upload` → PUT → `storage_objects {action: "complete"}`.
- Superusers create shared providers (`shared: true`) and the default; uploads without `provider` use the user's default, then the shared default.
- HTTP: `POST /api/storage/objects` (multipart or JSON), `/api/storage/providers`, `/api/storage/objects/{id}`.
- Presigned links contain the Access Key ID (normal for S3); prefer a CDN `publicUrl` for public files.

## 10. LLM relay and embeddings

- Channels: `llm_channel {action:"create", name, baseUrl, apiKey, defaultModel, isDefault}`; chat with `llm_chat {prompt}`, embed with `llm_embed`, usage with `llm_usage`.
- Any OpenAI SDK: `base_url = $BASE/api/llm/v1`, `api_key = <BosBase token>`.
- Superuser: embedding providers `embedding_provider {action:"create", name, baseUrl, apiKey, model, isDefault, priority}`, `embedding_status`, `embedding_reindex` after switching models.

## 11. Superuser administration

- Collections: `POST/PATCH/DELETE /api/collections[...]`, MCP `collections_list`, `collection_get`.
- Settings: `GET/PATCH /api/settings`; logs: `/api/logs`; backups: `/api/backups`; cron: `/api/crons`; GraphQL: `POST /api/graphql`; SQL: `POST /api/sql/execute`.
- Accounts: `user_create` / `POST /api/admin/users` (generated email/password when omitted, `tokenDuration`).
- Diagnostics: `diagnostics_health` → `diagnostics_errors {"since":"24h","status":"5xx"}` → `diagnostics_report`.
- Public knowledge base: `note_write {shared: true, project, path, content}`.

Exact access of every endpoint: [reference/http-endpoints.md](reference/http-endpoints.md).

## 12. Rules for the AI

- Never print, store or commit secrets (passwords, API keys, tokens); storage and LLM keys are write-only and returned masked.
- Personal memories, notes and conversations are private to their owner; share through a memory space, never by copying into another user's space.
- Check `isError`/HTTP status and read the message before retrying; validation errors include a `data` object per field.
- Prefer the dedicated MCP tools; use `api_request` for endpoints without a tool (it keeps the caller's permissions; streaming endpoints and `/api/mcp` are rejected).
- When the inventories in `reference/` and the server disagree, the live server (`api_endpoints`, `tools/list`) is the source of truth.
