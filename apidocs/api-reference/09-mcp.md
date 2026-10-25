# MCP Server (AI Memory, Knowledge Vault and Full API Access)

BosBase exposes a remote [Model Context Protocol](https://modelcontextprotocol.io) server. With a BosBase auth token an AI agent (Claude Code, Claude Desktop, Cursor, custom agents...) can:

- store and recall long-term memories (PostgreSQL + pgvector semantic search) organised in projects,
- maintain an Obsidian-like markdown knowledge vault (folders, `[[wikilinks]]`, backlinks, `#tags`, frontmatter properties, daily notes, version history) whose links form a queryable graph,
- remember what it did in previous sessions (activity journal),
- store files in user configured object storages (S3, Cloudflare R2, GCS, MinIO...),
- call third party LLMs through user configured channels (LLM relay),
- troubleshoot the instance (superusers),
- call every BosBase HTTP API endpoint.

All the MCP tables are created automatically by the system migrations on the first start.

## Endpoint

| Item | Value |
| --- | --- |
| URL | `POST /api/mcp` |
| Transport | Streamable HTTP, stateless (each request gets one `application/json` response; `GET`/`DELETE` return `405`) |
| Protocol versions | `2025-06-18`, `2025-03-26`, `2024-11-05` |
| Auth | `Authorization: Bearer <token>` — any auth record token (regular users and superusers) |

JSON-RPC batches are accepted, notifications are answered with `202 Accepted`. Tools carry `readOnlyHint`/`destructiveHint` annotations. The `initialize` response contains usage instructions plus a digest of the caller's latest recorded actions.

### Client configuration

Claude Code:

```sh
claude mcp add --transport http bosbase https://your-bosbase.example.com/api/mcp \
  --header "Authorization: Bearer <token>"
```

Generic `mcp.json` (Claude Desktop, Cursor...):

```json
{
  "mcpServers": {
    "bosbase": {
      "type": "http",
      "url": "https://your-bosbase.example.com/api/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

Tokens come from the regular auth endpoints (eg. `POST /api/collections/users/auth-with-password`). For agents a superuser can issue a long-lived token with `POST /api/collections/{collection}/impersonate/{id}` and `{"duration": 31536000}`.

## Access model

Everything an AI stores is **private by default** and isolated per auth record: other users can't search, read, list, link or modify it, and wikilinks only resolve inside the same owner and project.

- **Personal memories are private.** There are no globally public memories: storing a memory in the public space (`shared: true`) is rejected.
- **Shared memory spaces** let several users share a memory library: a user creates a space, others request to join and the creator (owner) approves them; only the owner approves, rejects or removes members. Active members read and write the space with the `space` argument of the memory/note tools; every memory records its `author` (`authorName`) and `updatedBy`. See [Shared memory spaces](#shared-memory-spaces).
- **Knowledge bases** (projects of notes) are private by default. **Public knowledge bases** are notes in the shared space: written by superusers (`shared: true`), readable by everyone and marked `shared: true` in results. `memory_search` includes them by default (`includeShared: false` excludes them).
- A user's own relations to public notes stay private to that user.

| Data | Regular users | Superusers |
| --- | --- | --- |
| Memories | own (private) + the spaces they are an active member of | any user's via `ownerId`, any space via `space` (admin) |
| Knowledge base notes / relations | own private notes + read the public knowledge bases | any user's via `ownerId`, write public knowledge bases via `shared: true` |
| Activity journal | own | own, or another user's via `ownerId` |
| Storage providers / LLM channels | own configs + use the shared ones | create shared configs (`shared: true`) or configs for others (`ownerId`) |
| Uploaded objects / LLM requests | own | all (`all: true`) |
| HTTP API (`api_request`) | same permissions as a direct HTTP call | same permissions as a direct HTTP call |
| Embedding providers, diagnostics, `memory_stats`, `collections_list`, `collection_get` | — | yes |

## Embeddings

Memories and notes are embedded server side through **embedding providers** stored in the `_embedding_providers` table (any OpenAI compatible `/embeddings` API: OpenAI, OpenRouter, Azure gateways, Ollama, vLLM, Jina, Voyage...). Superusers manage them with the `embedding_provider` tool or the `/api/embedding` HTTP API ([reference](./10-storage-llm-diagnostics.md#embedding-providers)). Changes apply immediately, no restart needed.

- The **default** provider is tried first, then the other enabled providers by `priority` (lower first). When a provider fails the next one is used (failover) and the error is recorded on the provider (`lastError`).
- Vectors of different models aren't comparable: semantic search only compares memories embedded with the same model as the query; the other memories are still returned by the keyword search. After switching the default model run `embedding_reindex` to re-embed the existing memories.
- API keys are stored encrypted (when an encryption key is configured) and never returned.

When no provider is enabled, these environment variables are used as fallback:

| Env variable | Fallback | Default |
| --- | --- | --- |
| `MCP_EMBEDDING_API_KEY` | `OPENAI_API_KEY` | — |
| `MCP_EMBEDDING_BASE_URL` | `OPENAI_BASE_URL` | OpenAI |
| `MCP_EMBEDDING_MODEL` | — | `text-embedding-3-small` |
| `MCP_EMBEDDING_DIMENSIONS` | — | model default |

Without any provider, clients may pass precomputed `embedding` arrays; otherwise memories remain searchable by keywords (PostgreSQL full text + `ILIKE`). The embedding column is dimensionless, so different models can coexist.

### Embedding tools (superusers)

| Tool | Description | Key arguments |
| --- | --- | --- |
| `embedding_provider` | Manage the providers: `list`, `get`, `create`, `update`, `delete`, `test`. | `action`*, `id` (id or name), `name`, `baseUrl`, `apiKey`, `headers`, `model`, `dimensions`, `priority`, `isDefault`, `enabled` |
| `embedding_status` | Providers (with last errors), the order they are tried, the primary model and memories missing an embedding or embedded with another model. | — |
| `embedding_reindex` | (Re)embed memories with the current primary provider. Call again while `remaining > 0`. | `mode` (`mismatched` default, `missing`, `all` + `offset`), `limit`, `batchSize`, `project`, `ownerId`, `shared` |

## Memory tools

Memories are organised in projects (`project`, default `"default"`; the legacy name `namespace` is accepted as an alias). Memory content is markdown: inline `#tags` and `[[wikilinks]]` are detected like in notes.

| Tool | Description | Key arguments |
| --- | --- | --- |
| `memory_store` | Store a memory (embedded). | `content`*, `title`, `project`, `tags`, `metadata`, `embedding`, `pinned`, `dedupe` (update a similar memory instead of duplicating), `relations[{target, relation, weight, incoming}]` |
| `memory_search` | Semantic search (cosine) with keyword fallback. | `query`*, `project`, `folder`, `tags`, `limit`, `minScore`, `mode` (`auto`/`semantic`/`keyword`), `includeShared` (default true), `expandDepth` (0-2) |
| `memory_get` | Get memories with incoming/outgoing relations. | `ids`* |
| `memory_list` | List memories, newest first. | `project`, `tags`, `limit`, `offset`, `includeShared` |
| `memory_update` | Update a memory (re-embedded, previous content kept in the history). | `id`*, `content`, `title`, `project`, `tags`, `metadata`, `pinned` |
| `memory_delete` | Delete memories (relations cascade). | `ids`* |
| `memory_link` / `memory_unlink` | Create/remove typed directed relations. | `source`*, `target`*, `relation`*, `weight`, `metadata` |
| `memory_graph` | Bounded BFS traversal. | `ids`*, `depth` (≤5), `relations`, `direction` (`out`/`in`/`both`), `limit` |
| `memory_path` | Shortest relation path between two memories. | `source`*, `target`*, `maxDepth`, `relations`, `direction` |
| `memory_projects` | Projects with memory/relation counts. | `includeShared` |
| `memory_project_rename` | Rename (or merge) a project. | `project`*, `newName`* |
| `memory_project_delete` | Delete a project with all its memories. | `project`*, `confirm: true`* |
| `memory_stats` | Superusers: counts per owner/project/relation. | `ownerId` |

The relation `links_to` is reserved: those edges are maintained automatically from the `[[wikilinks]]`.

## Memory context (remembering across conversations)

The server helps the AI to actually use its memory between conversations (always in the private space of the token's user):

- **Automatic context**: the `initialize` instructions include what the AI remembers about the user: the **pinned** (core) memories, the latest session summaries and the open todos (≈3000 characters max; disable with `MCP_MEMORY_AUTO_CONTEXT=false`), followed by its latest actions.
- **Pinned memories** (`pinned: true` on `memory_store` / `memory_update`): stable user preferences/identity and key project facts given at the start of every session.
- **Deduplication**: `memory_remember`, `memory_extract` and `memory_store` with `dedupe: true` update an existing memory of the same project when the new fact is identical or semantically very similar (cosine ≥ 0.9), merging the tags and keeping the previous content in the history.
- **Todos**: memories tagged `todo` are listed as open todos until they get the `done` tag.
- Usage signals (`accessCount`, `lastAccessedAt`) are recorded when memories are returned by `memory_search`, `memory_get` or `memory_context`.

| Tool | Description | Key arguments |
| --- | --- | --- |
| `memory_context` | Briefing for the current conversation: pinned memories, memories relevant to the query, latest sessions, open todos, recently updated memories, latest actions. Returns `briefing` (markdown) plus the structured sections. | `query`, `project`, `limit`, `maxChars` (default 8000), `includeShared` |
| `memory_remember` | Save the conversation context: a session memory (tag `session`) with the summary, plus each fact, decision (tag `decision`) and todo (tag `todo`) as deduplicated memories linked to the session (`noted_in`). | `summary`, `title`, `project`, `facts` (strings or `{title, content, tags, pinned}`), `decisions`, `todos`, `tags`, `dedupe` (default true) |
| `memory_extract` | Extract the memories (summary, facts, decisions, todos) from a transcript with an LLM channel and save them like `memory_remember`. Secrets are excluded by the extraction prompt. | `text`*, `project`, `channel`, `model`, `save` (default true), `maxFacts`, `instructions`, `tags` |

Recommended workflow: `memory_context` (with the user's request) at the start → `memory_store` (`dedupe`, `pinned`) during the conversation → `memory_remember` at the end.

### Prompts and resources

| Kind | Name / URI | Description |
| --- | --- | --- |
| Prompt | `recall` (`query`, `project`) | Inserts the memory briefing into the conversation. |
| Prompt | `remember` (`project`) | Asks the assistant to save the conversation with `memory_remember`. |
| Resource | `bosbase://memory/context` | Markdown briefing (core memories, sessions, todos, recent memories). |
| Resource | `bosbase://memory/pinned` | Markdown list of the pinned memories. |

In Claude Code the prompts are available as `/mcp__bosbase__recall` and `/mcp__bosbase__remember`, and the resources with `@bosbase:bosbase://memory/context`.

## Long-term conversation memory

The turns of the conversations can be saved (always private) so the AI recalls the exact past context in future sessions. Each turn is embedded for semantic recall. Long conversations are condensed automatically into a **rolling summary** (one session memory per conversation, updated in place) plus durable facts/decisions/todos, and `memory_consolidate` merges the sessions of a project into an evolving **project summary** (tag `project-summary`) included in the memory context.

| Tool | Description | Key arguments |
| --- | --- | --- |
| `conversation_save` | Append turns (omit `conversationId` to start a conversation; reuse the returned id). Summarizes automatically every `MCP_CONVERSATION_SUMMARIZE_AFTER` (default 40) new turns when an LLM channel is available. | `messages`* (`[{role, content}]`), `conversationId`, `project`, `title`, `client`, `autoSummarize` (default true), `channel`, `model` |
| `conversation_recall` | Semantic (+ keyword) search over all the past turns, with the surrounding turns. | `query`*, `project`, `conversationId`, `limit`, `window` (default 1), `mode` |
| `conversation_list` / `conversation_get` | List the conversations with their summary / read the turns (paginated, `tail`). | `project`, `search` / `conversationId`*, `fromSeq`, `limit`, `tail` |
| `conversation_summarize` | Condense the new turns into the rolling summary and durable memories. | `conversationId`*, `channel`, `model` |
| `conversation_delete` | Delete a conversation and its turns (extracted memories are kept). | `conversationId`* |
| `memory_consolidate` | Merge the new session summaries of a project into its long-term project summary (incremental). | `project`, `channel`, `model`, `maxSessions`, `force` |

The extraction and summaries are written in the language of the conversation. `memory_context` adds the project summaries and, for a query, the relevant past turns ("From past conversations"). Turns are kept forever unless `MCP_CONVERSATION_RETENTION_DAYS` is set.

## Knowledge graph and associative retrieval

| Tool | Description | Key arguments |
| --- | --- | --- |
| `knowledge_associate` | Associative retrieval: seeds are the best hits of the query (or the given ids); activation spreads along wikilinks, typed relations, backlinks, implicit semantic neighbours (cosine ≥ 0.5) and shared topical tags, with a 0.7 decay per hop. Each result has `activation`, `hops` and `via` (from which note and through which relation). | `query` or `ids`, `project`, `depth` (default 2, max 3), `limit`, `includeShared`, `semantic`, `tags`, `relations` |
| `note_graph` | Graph view of a vault: nodes (with in/out degree), edges, hubs, orphans and connected clusters. | `project`, `folder`, `includeShared`, `memories`, `limit` |
| `knowledge_suggest_links` | Semantically close notes/memories that aren't linked yet; `apply: true` creates the relations. | `project`, `id`, `minScore` (default 0.75), `limit`, `apply`, `relation` (default `related_to`) |

`memory_context` adds a "Related through the knowledge graph" section with the knowledge associated to the relevant memories.

## Shared memory spaces

| Tool | Description | Key arguments |
| --- | --- | --- |
| `space` | `create`, `list` (your spaces), `discover` (listed spaces), `get`, `update` (owner/admin), `delete` (owner), `join` (request, pending until approved), `leave`. | `action`*, `space` (id or name), `name`, `description`, `listed`, `message`, `search` |
| `space_member` | `list` (the owner also sees the pending requests), `approve`, `reject`, `remove`, `set_role` — owner only (`admin` moderates the content, `owner` transfers the ownership). | `action`*, `space`*, `memberId`, `role` |

Every memory/note tool accepts `space` (operate in the space) and `includeSpaces` (default true: personal reads also include the spaces the user belongs to; results carry `space`, `spaceName`, `author`, `authorName`).

Rules: only active members access a space (pending/rejected users don't); members create memories and edit notes collaboratively; modifying another member's memory, or deleting another member's memory/note, requires the author or an owner/admin; renaming/deleting a project of the space requires an owner/admin; deduplication never rewrites another member's memory; conversations stay personal. Membership decisions (approve, reject, remove, roles) belong to the owner (the creator, or whoever received the ownership); owners see the pending join requests in the `initialize` instructions. The same operations are available over HTTP: [Memory Spaces API](./11-memory-spaces.md).

## Knowledge vault tools (Obsidian style)

A project is a vault; every note has a unique vault relative path such as `projects/bosbase.md`.

- **Frontmatter properties** — a leading YAML block (scalars, quoted strings, `[a, b]` and `- item` lists) becomes `properties`; `title`, `tags`/`tag` and `aliases`/`alias` are honoured.
- **Title** — explicit `title` > frontmatter `title` > first `# heading` > file name.
- **Tags** — frontmatter tags + inline `#tag` / `#nested/tag` (numbers like `#123`, code blocks and inline code are ignored).
- **Links** — `[[Note]]`, `[[folder/Note]]`, `[[Note|alias]]`, `[[Note#Heading]]`, `![[embed]]` and `[text](note.md)` are resolved inside the same owner and project by full path, file name, title or alias. Resolved links become `links_to` graph edges; unresolved links are re-resolved automatically when a matching note is created or renamed.
- **History** — every content/path change stores the previous version (last 50 kept).

| Tool | Description | Key arguments |
| --- | --- | --- |
| `note_write` | Create/modify a note. | `path`*, `content`*, `project`, `mode` (`overwrite` default, `create`, `append`, `prepend` — prepend keeps the frontmatter on top), `title`, `tags`, `metadata` |
| `note_read` | Note with properties, tags, links (resolved/unresolved), backlinks and manual relations. | `path` or `id`, `project` |
| `note_list` | Browse a folder (notes + sub folders). | `project`, `folder`, `recursive` (default true), `tags`, `properties` (eg. `{"status":"done"}`), `sort` (`updated`/`created`/`path`/`title`), `limit`, `offset` |
| `note_move` | Rename/move; rewrites the links in the linking notes. | `path` or `id`, `newPath`*, `updateLinks` (default true) |
| `note_delete` | Delete a note (links to it become unresolved). | `path` or `id` |
| `note_backlinks` | Linking notes + unlinked mentions of the title/aliases. | `path` or `id`, `includeUnlinked` (default true) |
| `note_daily` | Daily note `daily/YYYY-MM-DD.md` (created on demand), optional `- HH:MM text` entry. | `date`, `folder`, `append` |
| `note_tags` | Tags with usage counts. | `project`, `includeShared` |
| `note_history` | List versions or return one version's content. | `path` or `id`, `version` |
| `note_restore` | Restore a version (the current content becomes a new version). | `path` or `id`, `version`* |

`memory_search`, `memory_graph` and `memory_path` work on notes too (`folder` restricts a search to a vault folder).

## Activity memory

Every tool call is journaled in `_mcp_activity` (tool, project, sanitized arguments — secrets are redacted and large values truncated —, success, summary, duration, client). Old entries are purged after `MCP_ACTIVITY_RETENTION_DAYS` (default 180).

| Tool | Description | Key arguments |
| --- | --- | --- |
| `activity_recent` | Recall previous actions, newest first. | `limit`, `project`, `tool` (`journal` for explicit entries), `since`, `until`, `search`, `writesOnly` |
| `activity_log` | Write an explicit journal entry (what was done/decided/next steps). | `summary`*, `details`, `project` |

## Object storage tools

See [Object Storage, LLM Relay and Diagnostics](./10-storage-llm-diagnostics.md) for the underlying HTTP API.

| Tool | Description | Key arguments |
| --- | --- | --- |
| `storage_provider` | Manage providers. Actions `list`, `get`, `create`, `update`, `delete`, `test`. | `action`*, `id`, `name`, `type` (`s3`/`aws`/`r2`/`gcs`/`minio`/`b2`/`other`), `endpoint`, `region`, `bucket`, `accessKey`, `secretKey`, `forcePathStyle`, `publicUrl`, `prefix`, `isDefault`, `enabled`, `shared`, `ownerId` |
| `storage_upload` | Upload a file (base64 or text) and record it. | `filename`*, `contentBase64` or `text`, `provider`, `contentType`, `key`, `prefix`, `metadata` |
| `storage_presign_upload` | Presigned PUT URL for large files (video...). | `filename`*, `provider`, `contentType`, `key`, `prefix`, `expiresSeconds` |
| `storage_objects` | Upload history. Actions `list`, `get` (with temporary download URL), `complete`, `delete`. | `action`*, `id`, `provider`, `status`, `prefix`, `contentType`, `search`, `page`, `perPage`, `all` |

## LLM relay tools

| Tool | Description | Key arguments |
| --- | --- | --- |
| `llm_channel` | Manage channels. Actions `list`, `get`, `create`, `update`, `delete`, `test`. | `action`*, `id`, `name`, `type` (`openai`/`anthropic`), `baseUrl`, `apiKey`, `headers`, `models`, `defaultModel`, `isDefault`, `enabled`, `shared`, `ownerId` |
| `llm_chat` | Chat completion through a channel. | `prompt` or `messages`, `system`, `channel`, `model`, `temperature`, `maxTokens`, `extra` |
| `llm_embed` | Embeddings through an OpenAI compatible channel. | `input`*, `channel`, `model` |
| `llm_usage` | Usage per channel/model (`usage`) or request log (`requests`). | `action`, `channel`, `model`, `since`, `errorsOnly`, `page`, `perPage`, `all` |

## Diagnostics tools (superusers)

| Tool | Description | Key arguments |
| --- | --- | --- |
| `diagnostics_health` | Build, runtime, database/pgvector, pool, settings, encryption key status, error counts, integrations (incl. failing embedding providers) and detected problems. | — |
| `diagnostics_errors` | Grouped + latest error logs (database, falls back to the on disk files). | `since` (`24h`, RFC3339, `YYYY-MM-DD`), `until`, `level`, `status` (`500`, `5xx`), `search`, `source` (`auto`/`db`/`file`), `limit` |
| `diagnostics_log_files` | List/read `{dataDir}/logs/errors-YYYY-MM-DD.jsonl`. | `file`, `tail`, `search` |
| `diagnostics_report` | Markdown report saved to `{dataDir}/logs/reports/`. | `since`, `save` |

## HTTP API tools

| Tool | Description | Key arguments |
| --- | --- | --- |
| `api_endpoints` | Live list of the endpoints available to the token (from the router, incl. custom routes) with auth level and summary. | `search` |
| `api_docs` | This API reference: endpoint sections by path/method or whole topics (`overview` documents filters, sorting, errors). | `path`, `method`, `topic` |
| `api_request` | Call any endpoint in-process with the caller's token. Streaming endpoints and `/api/mcp` are rejected. | `method`*, `path`*, `query`, `body`, `files[{field, filename, contentBase64 \| text, contentType}]`, `headers` |
| `records_list` / `record_get` / `record_create` / `record_update` / `record_delete` | Record CRUD shortcuts (collection API rules apply). | `collection`*, `id`, `filter`, `sort`, `expand`, `fields`, `data`, `files` |
| `collections_list` / `collection_get` | Superusers: collection schemas. | `filter`, `fields`, `collection` |

`api_request` responses: `{status, ok, contentType, body}` for JSON, `text` for text, `base64` (+`size`, first 512KB) for binary content.

## Storage tables

| Table | Purpose |
| --- | --- |
| `_mcp_memories` | memories and notes: `owner` (user id, `""` public, `space:<id>`), `author`, `updatedBy`, `namespace` (= project), `path`, `title`, `content`, `tags`, `metadata`, `properties`, `links`, `embedding` (`vector`), `embeddingModel`, `pinned`, `accessCount`, `lastAccessedAt` |
| `_mcp_memory_edges` | relations (`links_to` edges are automatic), cascade deleted with the memories |
| `_mcp_memory_versions` | previous versions of memories/notes |
| `_mcp_activity` | tool call journal |
| `_mcp_conversations` / `_mcp_messages` | conversation threads (rolling summary) / turns (embedded) |
| `_mcp_spaces` / `_mcp_space_members` | shared memory spaces / memberships (role, status) |
| `_embedding_providers` | server side embedding providers (superusers) |
