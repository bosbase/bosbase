# BosBase MCP Guide

> Chinese translation: [mcp-guide.zh-CN.md](./mcp-guide.zh-CN.md)

BosBase ships a built-in remote MCP (Model Context Protocol) server. An AI client (Claude Code, Claude Desktop, Cursor, your own agent...) connects with a BosBase token and can:

- **Long-term memory**: store memories in PostgreSQL + pgvector, organized by project, with semantic search and full CRUD
- **Knowledge vault (Obsidian-like)**: Markdown notes, folders, `[[wikilinks]]`, backlinks, `#tags`, frontmatter properties, daily notes, renames that update links, version history, and graph queries between notes
- **Remember what it did**: every tool call is recorded, and a new session starts with a summary of the latest actions
- **Object storage**: configure several S3 compatible storages (AWS S3, Cloudflare R2, Google Cloud Storage, MinIO, Backblaze B2...), upload images, videos and other files, with the upload history kept in Postgres
- **LLM relay**: configure several LLM channels and keys (OpenAI compatible / Anthropic), call them through BosBase and track usage
- **Error diagnostics** (superusers): health checks, error log queries (database + files on disk), error reports
- **The whole HTTP API**: collections, records, auth, files, batch, GraphQL, SQL, settings, logs, backups...

All the tables are created by system migrations **on first start**. No manual setup is needed.

---

## 1. Setup

### 1.1 Start BosBase

```sh
export SASSPB_POSTGRES_URL="postgres://user:pass@host:5432/db?sslmode=disable"   # pgvector must be installed
export BS_ENCRYPTION_KEY="<random 32 character string>"                           # recommended: encrypts stored secrets

./bosbase serve
```

`BS_ENCRYPTION_KEY` must be exactly 32 characters. If it is missing or has an invalid format, BosBase keeps running and stores secrets unencrypted (a warning is logged and `diagnostics_health` reports it).

Then configure an embedding provider (superuser, no restart needed, see [section 4](#4-embedding-providers-superusers)). Without one, memories are still saved and search falls back to full-text keyword search.

### 1.2 Get a token

```sh
# regular user
curl -X POST https://your-host/api/collections/users/auth-with-password \
  -H 'Content-Type: application/json' -d '{"identity":"me@example.com","password":"..."}'

# superuser issuing a long-lived token (1 year) for an agent
curl -X POST https://your-host/api/collections/users/impersonate/<userId> \
  -H "Authorization: <superuser token>" -H 'Content-Type: application/json' -d '{"duration":31536000}'
```

### 1.3 Connect

Claude Code:

```sh
claude mcp add --transport http bosbase https://your-host/api/mcp \
  --header "Authorization: Bearer <token>"
```

Claude Desktop, Cursor and other clients (`mcp.json`):

```json
{
  "mcpServers": {
    "bosbase": {
      "type": "http",
      "url": "https://your-host/api/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

Transport: Streamable HTTP (stateless, `POST /api/mcp`). Supported protocol versions: `2025-06-18`, `2025-03-26`, `2024-11-05`.

---

## 2. Privacy and Permissions

Everything is **private by default** and isolated between users: other users can't search, read, list, link or modify your memories and notes, and `[[wikilinks]]` only resolve inside your own vault.

- **Personal memories are private**: there are no globally public memories (storing one with `shared: true` is rejected).
- **Shared memory spaces** let several users share memories: the creator approves who joins, and every memory records its author (see [3.3](#33-shared-memory-spaces)).
- **Knowledge bases** are private by default. **Public knowledge bases** are written by superusers with `note_write` + `shared: true` and are readable by everyone (results show `shared: true`).

| Data | Regular user | Superuser |
| --- | --- | --- |
| Memories | Own (private) + the spaces they are an active member of | Any user's with `ownerId`, any space with `space` (admin) |
| Knowledge base notes / relations | Own private notes + read public knowledge bases | Any user's with `ownerId`; write public knowledge bases with `shared: true` |
| Activity log | Own | Own, or another user's with `ownerId` |
| Storage providers / LLM channels | Own + use shared ones | Create shared configs (`shared: true`) or configs for others (`ownerId`) |
| Upload history / LLM request log | Own | Everything (`all: true`) |
| HTTP API (`api_request`) | Exactly the same permissions and collection rules as calling HTTP directly | Same |
| Embedding providers, diagnostics, `memory_stats`, `collections_list`, `collection_get` | — | ✓ |

Regular users see 59 tools, superusers see 70.

---

## 3. Memory

| Tool | Purpose |
| --- | --- |
| `memory_store` | Save a memory (embedded automatically); `relations` links it to existing memories; `pinned: true` makes it a core memory; `dedupe: true` updates a similar memory instead of duplicating |
| `memory_search` | Semantic search plus keyword matches; `expandDepth` also returns graph neighbours; `folder` restricts to a note folder |
| `memory_get` / `memory_list` | Read / list |
| `memory_update` / `memory_delete` | Update (re-embedded, previous version kept) / delete |
| `memory_link` / `memory_unlink` | Create / remove typed directed relations, eg. `depends_on`, `part_of`, `supersedes` |
| `memory_graph` | Expand the graph from one or more memories (up to 5 hops, filter by relation and direction) |
| `memory_path` | Shortest relation path between two memories |
| `memory_projects` / `memory_project_rename` / `memory_project_delete` | List / rename or merge / delete projects (requires `confirm: true`) |

Example:

```json
{"name":"memory_store","arguments":{"project":"bosbase","title":"Deployment conventions","content":"Production runs on #docker, the database is described in [[PostgreSQL]]","tags":["ops"]}}
{"name":"memory_search","arguments":{"query":"how is production deployed","project":"bosbase","expandDepth":1}}
```

Memory content is Markdown too: `#tags` and `[[wikilinks]]` are detected automatically.

### 3.1 Remembering context across conversations

| Feature | What it does |
| --- | --- |
| Automatic context | Every new MCP session receives, in the server instructions, what the AI remembers about the user: pinned memories, the latest session summaries and the open todos (≈3000 characters; `MCP_MEMORY_AUTO_CONTEXT=false` disables it) |
| `memory_context` | One call briefing for the current task: pinned memories + memories relevant to the query + latest sessions + open todos + recently updated memories + latest actions |
| `memory_remember` | Saves the conversation: a session summary plus the facts, decisions (tag `decision`) and todos (tag `todo`); similar existing memories are updated instead of duplicated |
| `memory_extract` | The server extracts the memories from a conversation transcript with an LLM channel (secrets excluded) and saves them like `memory_remember` |
| Pinned memories | `pinned: true` on `memory_store` / `memory_update`: always given to the AI (preferences, identity, key project facts) |
| Todos | Memories tagged `todo` stay in the context until they get the `done` tag |
| Prompts / resources | Prompts `recall` and `remember`, resources `bosbase://memory/context` and `bosbase://memory/pinned` (in Claude Code: `/mcp__bosbase__recall`, `/mcp__bosbase__remember`, `@bosbase:bosbase://memory/context`) |

```json
{"name":"memory_context","arguments":{"query":"fix the checkout bug","project":"shop"}}
{"name":"memory_remember","arguments":{"project":"shop","title":"Checkout bug","summary":"Found and fixed the double charge in checkout.","facts":["Checkout retries must use idempotency keys"],"decisions":["Use Stripe idempotency keys for all payment calls"],"todos":["Add a regression test for double charges"]}}
{"name":"memory_store","arguments":{"content":"The user prefers concise answers in Chinese","pinned":true}}
```

Recommended workflow: `memory_context` at the start, `memory_store` (`dedupe`, `pinned`) during the conversation, `memory_remember` at the end.

### 3.2 Long-term conversation memory

| Tool | What it does |
| --- | --- |
| `conversation_save` | Saves the turns of a conversation (embedded). Omit `conversationId` to start one, then reuse the returned id. Every 40 new turns (`MCP_CONVERSATION_SUMMARIZE_AFTER`) the conversation is summarized automatically when an LLM channel is configured |
| `conversation_recall` | Searches all past conversations by meaning and returns the matching turns with their surrounding turns ("what did we say about X last month?") |
| `conversation_list` / `conversation_get` | Lists the conversations with their summary / reads the turns |
| `conversation_summarize` | Condenses the new turns into the rolling summary (one session memory per conversation, updated in place) and durable facts, decisions and todos |
| `memory_consolidate` | Merges the session summaries of a project into its long-term **project summary**, included in every session context |
| `conversation_delete` | Deletes a conversation (the memories extracted from it are kept) |

Summaries are written in the language of the conversation. `memory_context` adds the project summaries and the relevant past turns. Conversations are always private.

```json
{"name":"conversation_save","arguments":{"project":"travel","messages":[{"role":"user","content":"I'm allergic to seafood"},{"role":"assistant","content":"Noted, I'll avoid seafood restaurants."}]}}
{"name":"conversation_recall","arguments":{"query":"food restrictions of the user"}}
{"name":"memory_consolidate","arguments":{"project":"travel"}}
```

### 3.3 Shared memory spaces

Several users can share one memory library (eg. a team) through their AIs:

1. A user creates a space: `space` `{"action":"create","name":"product-team","listed":true}` (they become its owner).
2. Others find it (`discover`) and request to join: `{"action":"join","space":"product-team","message":"..."}`. The request stays **pending**; the owner is told at the start of their next session.
3. The creator (owner) approves — only the owner approves, rejects or removes members: `space_member` `{"action":"approve","space":"product-team","memberId":"<user id>"}` (or `reject`).
4. Members pass `space` to the memory and note tools. Every memory records its **author** (`author`, `authorName`, `updatedBy`); personal searches also include the spaces (`includeSpaces`, results show `spaceName` and `authorName`).

| Rule | |
| --- | --- |
| Access | Only active members (pending/rejected users have no access); personal memories stay private |
| Write | Members add memories and edit notes collaboratively |
| Modify / delete | Another member's memory: its author or an owner/admin; deleting a note: its author or an owner/admin |
| Roles | `owner` = the creator: approves/rejects join requests, removes members, sets roles (transfer with `set_role owner`); `admin`: moderates the content; `member` |
| HTTP API | `/api/memory/spaces/...` for spaces, members and space memories ([reference](../apidocs/api-reference/11-memory-spaces.md)) |

---

### 3.4 Group chat between AI agents

1. The superuser creates an account for each agent: `user_create` `{"name":"Planner"}` (or `POST /api/admin/users`) returns `email`, `password` (generated when omitted) and `token`; the token is the agent's MCP Bearer token.
2. An agent creates a room and adds the others: `chat_room` `{"action":"create","name":"release","members":["coder@agents.test"]}`.
3. Agents talk with `chat_send` and listen with `chat_read` (`after` = the last cursor, `wait` up to 60 s returns as soon as someone writes).
4. Apps and humans follow the same room in realtime by subscribing to the `chat_messages` collection (`/api/realtime`); members only receive the messages of their rooms.

```json
{"name":"chat_send","arguments":{"room":"release","content":"Backend is done, @Reviewer please check","mentions":["reviewer@agents.test"]}}
{"name":"chat_read","arguments":{"room":"release","after":"2026-10-25 03:10:04.151Z|01a1...","wait":30}}
```

See [`apidocs/api-reference/12-chat-accounts.md`](../apidocs/api-reference/12-chat-accounts.md).

---

## 4. Embedding Providers (superusers)

Memories and notes are embedded by providers stored in the database (any OpenAI compatible `/embeddings` API: OpenAI, OpenRouter, Azure gateways, Ollama, vLLM, Jina, Voyage...). Changes apply immediately, without restarting the server.

- **Default and priority**: the default provider is tried first, then the other enabled providers by `priority` (lower first).
- **Failover**: when a provider fails, the next one is used and the error is recorded on the provider (`lastError`, also reported by `diagnostics_health`).
- **Switching models**: vectors of different models aren't comparable, so semantic search only compares memories embedded with the query's model (the others are still found by keywords). After changing the default model, run `embedding_reindex`.
- API keys are stored encrypted and never returned.

| Tool | Purpose |
| --- | --- |
| `embedding_provider` | `list` / `get` / `create` / `update` / `delete` / `test` |
| `embedding_status` | Providers, the order they are tried, the primary model, memories missing an embedding or embedded with another model |
| `embedding_reindex` | Re-embed memories with the primary provider (`mode`: `mismatched` default, `missing`, `all`); call again while `remaining > 0` |

```json
{"name":"embedding_provider","arguments":{"action":"create","name":"openrouter","baseUrl":"https://openrouter.ai/api/v1","apiKey":"sk-or-...","model":"openai/text-embedding-3-small","isDefault":true}}
{"name":"embedding_provider","arguments":{"action":"create","name":"local-ollama","baseUrl":"http://ollama:11434/v1","model":"nomic-embed-text","priority":10}}
{"name":"embedding_provider","arguments":{"action":"test","id":"openrouter"}}
{"name":"embedding_reindex","arguments":{}}
```

Over HTTP: `/api/embedding/providers` (CRUD + `/{id}/test`), `/api/embedding/status`, `/api/embedding/embed`, `/api/embedding/reindex`. See [`apidocs/api-reference/10-storage-llm-diagnostics.md`](../apidocs/api-reference/10-storage-llm-diagnostics.md#embedding-providers).

When no provider is enabled, the `MCP_EMBEDDING_API_KEY` / `MCP_EMBEDDING_BASE_URL` / `MCP_EMBEDDING_MODEL` environment variables are used as fallback.

---

## 5. Knowledge Vault (Obsidian-like)

A project is a vault. Every note has a unique relative path, eg. `projects/bosbase.md`.

| Feature | Details |
| --- | --- |
| Frontmatter properties | The leading YAML block is parsed into `properties`; `title`, `tags` and `aliases` are recognized |
| Title | Explicit `title` > frontmatter `title` > first `# Heading` > file name |
| Tags | Frontmatter tags + inline `#tag` and `#nested/tag` (ignores `#123` and code blocks) |
| Wikilinks | `[[Note]]`, `[[folder/Note]]`, `[[Note\|alias]]`, `[[Note#Heading]]`, `![[embed]]`, `[text](note.md)`; resolved by path, file name, title or alias into `links_to` graph relations; links to notes created later are filled in automatically |
| Backlinks | Notes linking to this note, plus unlinked mentions |
| Rename | `note_move` rewrites the links pointing to the note in other notes |
| Version history | Every change keeps the previous version (last 50), which can be viewed and restored |

| Tool | Purpose |
| --- | --- |
| `note_write` | Create or update a note; `mode`: `overwrite` (default), `create` (fails if it exists), `append`, `prepend` (frontmatter stays on top) |
| `note_read` | Read a note with its properties, tags, outgoing links (resolved or not) and backlinks |
| `note_list` | Browse a folder (notes + subfolders), filter by tag or property (eg. `{"status":"done"}`) |
| `note_move` / `note_delete` | Move or rename / delete |
| `note_backlinks` | Backlinks + unlinked mentions |
| `note_daily` | Daily note `daily/YYYY-MM-DD.md`; `append` adds a timestamped entry |
| `note_tags` | Tag counts |
| `note_history` / `note_restore` | List versions or view one / restore a version |

Example:

```json
{"name":"note_write","arguments":{"project":"kb","path":"tech/pgvector.md","content":"---\naliases: [vector extension]\nstatus: active\n---\n# pgvector\nVector extension for PostgreSQL, used by [[BosBase]] #database"}}
{"name":"note_move","arguments":{"project":"kb","path":"tech/pgvector.md","newPath":"db/pgvector.md"}}
{"name":"memory_path","arguments":{"source":"<note A id>","target":"<note B id>"}}
```

### 5.1 Knowledge graph and associative retrieval

| Tool | Purpose |
| --- | --- |
| `knowledge_associate` | Finds the knowledge related to a question even when it doesn't share its words: starts from the best hits and spreads along wikilinks, backlinks, typed relations, semantic neighbours and shared tags (2 hops by default). Each result explains its path (`via`: from which note, through which relation) |
| `note_graph` | Graph view of a vault (like Obsidian): nodes, edges, hubs, orphans, connected clusters |
| `knowledge_suggest_links` | Finds related notes that aren't linked yet; `apply: true` creates the `related_to` relations |

```json
{"name":"knowledge_associate","arguments":{"query":"why are payments rejected","project":"kb"}}
```

Example result: `rules-engine (hops 2, via risk-service: links to)`, `refund-process (via payment-service: shared tags #payment)`. `memory_context` also includes a "Related through the knowledge graph" section.

---

## 6. Remembering What the AI Did

- Every tool call is recorded in `_mcp_activity`: tool name, project, arguments (passwords, tokens and keys are redacted, large values truncated), success, summary and duration.
- On `initialize`, the server instructions end with the **last 10 write actions**.
- `activity_recent`: query past actions by time, tool, project or keyword; `writesOnly: true` returns only writes.
- `activity_log`: the AI writes a journal entry (what it did, decisions made, next steps).
- History is kept for 180 days by default (`MCP_ACTIVITY_RETENTION_DAYS`).

Suggested system prompt for the AI (the server instructions already contain the same guidance):

> At the start of a conversation call `memory_context` with the user's request. Save durable information with `memory_store` (`dedupe: true`; `pinned: true` for stable preferences). At the end of a task call `memory_remember` with a summary, facts, decisions and todos.

---

## 7. Object Storage

| Tool | Purpose |
| --- | --- |
| `storage_provider` | Manage storage configs: `list` / `get` / `create` / `update` / `delete` / `test` (uploads, reads and deletes a probe object) |
| `storage_upload` | Upload a file (base64 or text), record it in the history and return a `url` usable from the internet |
| `storage_presign_upload` | For large files (videos...): get a presigned PUT URL for direct upload, then call `storage_objects` with `complete` |
| `storage_objects` | Upload history: `list` (filter by provider, status, prefix, content type, keyword), `get` (with a 1 hour download URL), `complete`, `delete` (deletes the remote object, the history is kept as `deleted`) |

Common configs:

| Service | type | endpoint | region |
| --- | --- | --- | --- |
| AWS S3 | `aws` | Optional (generated) | eg. `ap-northeast-1` |
| Cloudflare R2 | `r2` | `https://<account_id>.r2.cloudflarestorage.com` | `auto` (default) |
| Google Cloud Storage (HMAC keys) | `gcs` | Optional (`https://storage.googleapis.com`) | `auto` |
| MinIO / self-hosted | `minio` | `http://minio:9000`, with `forcePathStyle: true` | — |

```json
{"name":"storage_provider","arguments":{"action":"create","name":"r2","type":"r2","endpoint":"https://xxx.r2.cloudflarestorage.com","bucket":"media","accessKey":"...","secretKey":"...","publicUrl":"https://media.example.com","isDefault":true}}
{"name":"storage_upload","arguments":{"filename":"cover.png","contentBase64":"iVBORw0...","prefix":"images"}}
```

Public links: every upload returns `url` with `urlType` `public` (permanent, when the provider has `publicUrl` or `publicRead: true`) or `presigned` (valid 7 days by default, `urlExpiresSeconds`).

Superusers configure shared providers (`shared: true`) and the default one; users without their own default upload to the shared default. `STORAGE_ALLOW_USER_PROVIDERS=false` lets only superusers configure providers.

The same features are available over HTTP: `POST /api/storage/objects` accepts multipart uploads with several files. See [`apidocs/api-reference/10-storage-llm-diagnostics.md`](../apidocs/api-reference/10-storage-llm-diagnostics.md).

---

## 8. LLM Relay

| Tool | Purpose |
| --- | --- |
| `llm_channel` | Manage channels: `list` / `get` / `create` / `update` / `delete` / `test` |
| `llm_chat` | Chat through a channel (`prompt` or `messages`, optional `system`, `model`, `temperature`, `maxTokens`) |
| `llm_embed` | Create embeddings through a channel |
| `llm_usage` | Usage per channel and model (`usage`) or the request log (`requests`) |

Channel types:

- `openai`: any OpenAI compatible API, eg. OpenAI, OpenRouter, DeepSeek, Qwen, Groq, the Gemini OpenAI endpoint, Ollama or vLLM `/v1`
- `anthropic`: the Anthropic Messages API

Channel selection: explicit channel (`X-Bosbase-Channel` header, `channel` query param or body field) > a channel whose `models` list contains the requested model > the default channel (own first, then shared).

**Any OpenAI SDK can use BosBase as a relay**:

```python
from openai import OpenAI
client = OpenAI(base_url="https://your-host/api/llm/v1", api_key="<BosBase token>")
client.chat.completions.create(model="openai/gpt-4o-mini", messages=[{"role":"user","content":"Hello"}])
```

Streaming is supported (SSE passthrough). To track usage of streamed requests, include `"stream_options": {"include_usage": true}`.

---

## 9. Error Diagnostics (superusers)

- Besides the database, every ERROR log is also **written to disk**: `{dataDir}/logs/errors-YYYY-MM-DD.jsonl`, so problems can be investigated even when the database is down. Environment variables: `BOSBASE_ERROR_LOG_LEVEL`, `BOSBASE_ERROR_LOG_MAX_DAYS` (default 30), `BOSBASE_ERROR_LOG_MAX_MB` (default 100), `BOSBASE_ERROR_LOG_FILE=false` (disable).
- Passwords, tokens, keys and similar fields are redacted in request logs.
- If the stored settings were encrypted and the key is now missing, wrong or invalid, BosBase still starts with the default settings, logs an error and blocks saving the settings (so the encrypted ones aren't overwritten). Restore the original key and restart, or set `BOSBASE_ALLOW_SETTINGS_RESET=true` to overwrite them.

| Tool | Purpose |
| --- | --- |
| `diagnostics_health` | Health snapshot: version, runtime, database and pgvector, connection pool, migrations, settings, encryption key status, recent error counts, integrations status (incl. failing embedding providers) and a list of detected problems |
| `diagnostics_errors` | Error logs (grouped by message, status and error + latest entries), filter by time, status (eg. `5xx`) or keyword; reads the files on disk when the database is unavailable |
| `diagnostics_log_files` | List or read the error log files on disk |
| `diagnostics_report` | Generate a Markdown error report saved under `{dataDir}/logs/reports/`, ready to attach to a bug report |

Recommended flow: `diagnostics_health` → `diagnostics_errors {"since":"24h","status":"5xx"}` → `diagnostics_report`.

---

## 10. The Whole HTTP API

| Tool | Purpose |
| --- | --- |
| `api_endpoints` | Endpoints callable with the current token (read live from the router, custom routes included), with their auth level |
| `api_docs` | API documentation (by path, or by topic such as `overview`) |
| `api_request` | Call any endpoint in-process with the current token; supports query params, JSON bodies and file uploads |
| `records_*` / `record_*` | Shortcuts for record CRUD (collection API rules apply) |
| `collections_list` / `collection_get` | Collection schemas (superusers) |

Streaming endpoints (realtime, pubsub, SSE, WebSocket) can't be called through MCP.

---

## 11. Environment Variables

| Variable | Purpose | Default |
| --- | --- | --- |
| `MCP_EMBEDDING_API_KEY` / `_BASE_URL` / `_MODEL` / `_DIMENSIONS` | Embedding fallback, used only when no embedding provider is enabled | Falls back to `OPENAI_*`; model `text-embedding-3-small` |
| `MCP_ACTIVITY_RETENTION_DAYS` | Activity log retention in days | 180 |
| `MCP_MEMORY_AUTO_CONTEXT` | Add the memory context to the initialize instructions | true |
| `MCP_CONVERSATION_SUMMARIZE_AFTER` | New turns before the automatic conversation summary | 40 |
| `MCP_CONVERSATION_RETENTION_DAYS` | Conversation turns retention (0 = forever) | 0 |
| `BS_ENCRYPTION_KEY` (or `BOSBASE_ENCRYPTION_KEY`) | Key used to encrypt the settings and stored secrets (exactly 32 characters) | Unset or invalid: stored unencrypted, the server keeps running |
| `BOSBASE_ALLOW_SETTINGS_RESET` | Allow overwriting settings that were encrypted with a lost key | false |
| `BOSBASE_ALLOW_PRIVATE_UPSTREAMS` | Allow regular users' configs to reach private network addresses | false |
| `STORAGE_MAX_UPLOAD_MB` | Max upload size | 512 |
| `STORAGE_URL_EXPIRES_SECONDS` | Validity of the presigned links returned by uploads | 604800 (7 days) |
| `STORAGE_ALLOW_USER_PROVIDERS` | Regular users can configure their own storage providers | true |
| `LLM_RELAY_TIMEOUT_SECONDS` | LLM relay timeout | 600 |
| `BOSBASE_ERROR_LOG_FILE` / `_LEVEL` / `_MAX_DAYS` / `_MAX_MB` | Error log files on disk | on / ERROR / 30 / 100 |

## 12. Tables

| Table | Purpose |
| --- | --- |
| `_mcp_memories` | Memories and notes (content, tags, properties, links, embedding) |
| `_mcp_memory_edges` | Relations between memories (`links_to` is maintained from wikilinks) |
| `_mcp_memory_versions` | Version history |
| `_mcp_activity` | AI activity log |
| `_storage_providers` / `_storage_objects` | Storage configs / upload history |
| `_llm_channels` / `_llm_requests` | LLM channels / request log |
| `_embedding_providers` | Embedding providers |
| `_mcp_conversations` / `_mcp_messages` | Conversations / turns |
| `_mcp_spaces` / `_mcp_space_members` | Shared memory spaces / members |
| `chat_rooms` / `chat_messages` | Group chat (regular collections, realtime) |
