# MCP tools

Generated from `tools/list` of a running BosBase server: 59 tools for regular users, 70 for superusers (11 superuser-only).

Every memory/note tool also accepts `space` (operate in a shared memory space you are an active member of) and `includeSpaces`; superusers additionally get `ownerId` (act on another user's private space) and `shared` (public knowledge base). `project` is the memory/vault project (alias `namespace`).

## Memory context

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `memory_consolidate` | user | write | Consolidate the session summaries of a project into its long-term project summary (one evolving memory per project, tag "project-summary") that is included in your memory context. Run it periodically (eg. after several sessions). Requires an LLM channel. | `channel`, `force`, `maxSessions`, `model`, `project` |
| `memory_context` | user | read-only | Recall the context relevant to the current conversation in one call: pinned core memories, the memories most relevant to the query, the latest session summaries, open todos, recently updated memories and your latest actions. Call it at the start of a … | `includeShared`, `limit`, `maxChars`, `project`, `query` |
| `memory_extract` | user | write | Let the server extract the memories worth keeping (summary, atomic facts, decisions, todos) from a conversation transcript or any text using a configured LLM channel, and save them like memory_remember (with deduplication). Requires an LLM channel … | `channel`, `instructions`, `maxFacts`, `model`, `project`, `save`, `tags`, `text`* |
| `memory_remember` | user | write | Save the context of the current conversation for future sessions: a session summary plus the durable facts, decisions and todos. Facts similar to existing memories update them instead of creating duplicates. Call it at the end of a task or conversation (and … | `decisions`, `dedupe`, `facts`, `project`, `summary`, `tags`, `title`, `todos` |

## Memory

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `memory_delete` | user | destructive | Delete memories by id (their relations are removed too). | `ids`* |
| `memory_get` | user | read-only | Get memories by id, including their direct relations. | `ids`* |
| `memory_graph` | user | read-only | Traverse the knowledge graph starting from one or more memories and return the reachable nodes and edges (breadth-first). | `depth`, `direction`, `ids`*, `limit`, `relations` |
| `memory_link` | user | write | Create or update a typed, directed relation (graph edge) between two memories. | `metadata`, `relation`*, `source`*, `target`*, `weight` |
| `memory_list` | user | read-only | List memories (newest first). | `includeShared`, `limit`, `offset`, `project`, `tags` |
| `memory_path` | user | read-only | Find the shortest relation path between two memories. | `direction`, `maxDepth`, `relations`, `source`*, `target`* |
| `memory_project_delete` | user | destructive | Permanently delete a project and all of its memories and relations. | `confirm`*, `project`* |
| `memory_project_rename` | user | write | Rename a project (or merge it into another one when the new name already exists). | `newName`*, `project`* |
| `memory_projects` | user | read-only | List the projects of the memory space with their memory/relation counts and last update time. | `includeShared` |
| `memory_search` | user | read-only | Search memories by meaning (vector similarity) with keyword fallback. Optionally expand each hit with its graph neighbours. | `embedding`, `expandDepth`, `folder`, `includeShared`, `limit`, `minScore`, `mode`, `project`, `query`*, `tags` |
| `memory_stats` | **superuser** | read-only | Knowledge base statistics: memory and relation counts per owner and project. | — |
| `memory_store` | user | write | Store a new private memory (fact, decision, preference, document chunk...). Memories are always private to the authenticated user. The content is embedded into pgvector for semantic search. Optionally link it to existing memories via relations. | `content`*, `dedupe`, `embedding`, `metadata`, `pinned`, `project`, `relations`, `tags`, `title` |
| `memory_unlink` | user | write | Remove relations between two memories (all relation types when relation is omitted). | `relation`, `source`*, `target`* |
| `memory_update` | user | write | Update an existing memory. Changing the content re-computes its embedding. | `content`, `embedding`, `id`*, `metadata`, `pinned`, `project`, `tags`, `title` |

## Conversations

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `conversation_delete` | user | destructive | Permanently delete a conversation and its turns (the memories extracted from it are kept). | `conversationId`* |
| `conversation_get` | user | read-only | Read the turns of a conversation (paginated by sequence number; tail: true returns the latest turns). | `conversationId`*, `fromSeq`, `limit`, `tail` |
| `conversation_list` | user | read-only | List your past conversations (newest first) with their rolling summary. | `limit`, `offset`, `project`, `search` |
| `conversation_recall` | user | read-only | Search all your past conversations by meaning (with keyword fallback) and return the matching turns with their surrounding turns. Use it when the user refers to something discussed before ("as we said last week...") or when past context may help. | `conversationId`, `limit`, `mode`, `project`, `query`*, `window` |
| `conversation_save` | user | write | Save conversation turns to your long-term conversation memory so you can recall the exact past context in future sessions (conversation_recall). Omit conversationId to start a new conversation, then reuse the returned id to append the next turns. When a … | `autoSummarize`, `channel`, `client`, `conversationId`, `messages`*, `model`, `project`, `title` |
| `conversation_summarize` | user | write | Condense the not yet summarized turns of a conversation into its rolling summary (stored as a session memory) and durable facts, decisions and todos (deduplicated memories). Requires an LLM channel. | `channel`, `conversationId`*, `model` |

## Knowledge vault (notes)

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `note_backlinks` | user | read-only | Find the notes linking to a note, plus unlinked mentions of its title/aliases. | `id`, `includeUnlinked`, `path`, `project` |
| `note_daily` | user | write | Get (and create if missing) the daily note of a date; optionally append a timestamped entry. Useful as a work journal. | `append`, `date`, `folder`, `project` |
| `note_delete` | user | destructive | Delete a note (links to it become unresolved). | `id`, `path`, `project` |
| `note_graph` | user | read-only | Graph view of a knowledge base (like the Obsidian graph): notes as nodes, links/relations as edges, with the hubs (most connected), the orphans (unlinked) and the connected clusters. | `folder`, `includeShared`, `limit`, `memories`, `project` |
| `note_history` | user | read-only | List the saved versions of a note or memory, or return the content of a specific version. | `id`, `path`, `project`, `version` |
| `note_list` | user | read-only | Browse the vault: list the notes and sub folders of a folder, optionally filtered by tags or frontmatter properties. | `folder`, `includeShared`, `limit`, `offset`, `project`, `properties`, `recursive`, `sort`, `tags` |
| `note_move` | user | write | Rename/move a note. Links pointing to it from other notes are updated automatically (like Obsidian). | `id`, `newPath`*, `path`, `project`, `updateLinks` |
| `note_read` | user | read-only | Read a note with its properties, tags, outgoing links (resolved/unresolved) and backlinks. | `id`, `path`, `project` |
| `note_restore` | user | write | Restore a note or memory to a previous version (the current content is kept as a new version). | `id`, `path`, `project`, `version`* |
| `note_tags` | user | read-only | List the tags used in a project with their usage counts. | `includeShared`, `project` |
| `note_write` | user | write | Create or modify a markdown note (Obsidian style). Supports YAML frontmatter properties, #tags, [[wikilinks]] (kept in sync as graph links), and keeps the previous content in the version history. Modes: overwrite (default, creates when missing), create (fails … | `content`*, `metadata`, `mode`, `path`*, `project`, `tags`, `title` |

## Knowledge graph

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `knowledge_associate` | user | read-only | Associative retrieval over the knowledge graph: finds the knowledge related to a question (or to some notes/memories) even when it doesn't match the words, by spreading from the best hits along wikilinks, typed relations, backlinks, semantic neighbours and … | `depth`, `ids`, `includeShared`, `limit`, `project`, `query`, `relations`, `semantic`, `tags` |
| `knowledge_suggest_links` | user | write | Find notes/memories of a knowledge base that are semantically close but not linked yet (link suggestions). With apply: true the suggested relations are created, enriching the knowledge graph for knowledge_associate. | `apply`, `id`, `limit`, `minScore`, `project`, `relation` |

## Shared memory spaces

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `space` | user | write | Shared memory spaces: memory libraries shared by several users (eg. a team). Create a space, discover the listed spaces, request to join one (the owner must approve), leave it. Once a member, pass space to the memory/note tools to read and write the shared … | `action`*, `description`, `listed`, `message`, `name`, `search` |
| `space_member` | user | write | Manage the members of a shared memory space. The creator (owner) of the space decides: approve or reject the join requests, remove members, change roles (admin = can moderate the content; owner transfers the ownership). Members can list the active members; … | `action`*, `memberId`, `role` |

## Group chat and agent accounts

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `chat_read` | user | read-only | Read the messages of a chat room. Without after it returns the latest messages; with after (the cursor returned by chat_read/chat_send) only the newer ones. wait (seconds, max 60) blocks until a new message arrives, so an agent can wait for the replies of the … | `after`, `limit`, `room`*, `skipOwn`, `wait` |
| `chat_room` | user | write | Group chat rooms shared by AI agents and users (stored in the chat_rooms collection; apps can follow the messages with the realtime subscriptions). Actions: create (you become the owner), list (your rooms with their last message), get, add_members / … | `action`*, `description`, `members`, `name`, `room` |
| `chat_send` | user | write | Post a message in a chat room (as yourself). The members receive it through chat_read or the realtime subscriptions. Returns the message and its cursor. | `content`*, `mentions`, `metadata`, `replyTo`, `room`* |
| `user_create` | **superuser** | write | Superusers: create a regular user account (eg. for an AI agent) and return its email, password and an auth token (the token can be used directly as MCP Bearer token). Email and password are generated when omitted. | `collection`, `email`, `name`, `password`, `tokenDuration`, `verified` |

## Activity journal

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `activity_log` | user | write | Write an explicit journal entry about what you did, decided or plan to do next (eg. at the end of a task), so that future sessions can recall it. | `details`, `project`, `summary`* |
| `activity_recent` | user | read-only | Recall what you did before: returns your recent MCP tool calls and journal entries (newest first). Call it at the start of a session to restore context. | `limit`, `project`, `search`, `since`, `tool`, `until`, `writesOnly` |

## Object storage

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `storage_objects` | user | write | Query and manage the upload history. Actions: list (filters provider/status/prefix/contentType/search), get (with a temporary download URL), complete (finalize a presigned upload), delete (removes the remote object, keeps the history). | `action`*, `all`, `contentType`, `id`, `includeDeleted`, `page`, `perPage`, `prefix`, `provider`, `search`, `status` |
| `storage_presign_upload` | user | write | Get a presigned PUT URL to upload a (large) file directly to the object storage, then call storage_objects with action complete. | `contentType`, `expiresSeconds`, `filename`*, `key`, `metadata`, `prefix`, `provider` |
| `storage_provider` | user | write | Manage the S3 compatible object storage providers (AWS S3, Cloudflare R2, Google Cloud Storage HMAC, MinIO, Backblaze B2...). Secrets are stored encrypted and never returned. Actions: list, get, create, update, delete, test (upload/head/delete a probe object). | `accessKey`, `action`*, `bucket`, `enabled`, `endpoint`, `forcePathStyle`, `id`, `isDefault`, `name`, `prefix`, `publicRead`, `publicUrl`, `region`, `secretKey`, `type` |
| `storage_upload` | user | write | Upload a file (image, video, document...) to an object storage provider and record it in the upload history. Returns "url": a link usable from the internet (permanent when the provider has a public/CDN URL or a public bucket, otherwise a presigned link valid … | `contentBase64`, `contentType`, `filename`*, `key`, `metadata`, `prefix`, `provider`, `text`, `urlExpiresSeconds` |

## LLM relay

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `llm_channel` | user | write | Manage the third party LLM channels used by the BosBase LLM relay (OpenAI compatible APIs such as OpenAI, OpenRouter, DeepSeek, Qwen, Groq, Ollama, vLLM, Gemini; or Anthropic). API keys are stored encrypted and never returned. Actions: list, get, create, … | `action`*, `apiKey`, `baseUrl`, `defaultModel`, `enabled`, `headers`, `id`, `isDefault`, `models`, `name`, `type` |
| `llm_chat` | user | write | Call a chat model through a configured LLM channel and return the answer (usage is recorded). | `channel`, `extra`, `maxTokens`, `messages`, `model`, `prompt`, `system`, `temperature` |
| `llm_embed` | user | write | Create embeddings through a configured OpenAI compatible LLM channel. | `channel`, `input`*, `model` |
| `llm_usage` | user | read-only | LLM relay usage: aggregated tokens/requests per channel and model (action usage) or the individual request log (action requests). | `action`, `all`, `channel`, `errorsOnly`, `model`, `page`, `perPage`, `since` |

## Embedding providers

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `embedding_provider` | **superuser** | write | Superusers: manage the server side embedding providers used to embed memories and notes (any OpenAI compatible /embeddings API: OpenAI, OpenRouter, Azure gateways, Ollama, vLLM, Jina, Voyage...). Changes apply immediately without restart. The default provider … | `action`*, `apiKey`, `baseUrl`, `dimensions`, `enabled`, `headers`, `id`, `isDefault`, `model`, `name`, `priority`, `type` |
| `embedding_reindex` | **superuser** | write | Superusers: (re)embed memories with the current primary provider, eg. after configuring a provider for the first time or switching the default model (semantic search only compares vectors of the same model). Processes up to limit memories per call; call again … | `batchSize`, `limit`, `mode`, `offset`, `project` |
| `embedding_status` | **superuser** | read-only | Superusers: embedding configuration and coverage: providers (with last errors), the order they are tried, the primary model and how many memories are missing an embedding or are embedded with another model. | — |

## Diagnostics

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `diagnostics_errors` | **superuser** | read-only | Superusers: query the error logs (grouped + latest entries). Reads the database logs and falls back to the on disk error files when the database is unavailable. | `level`, `limit`, `search`, `since`, `source`, `status`, `until` |
| `diagnostics_health` | **superuser** | read-only | Superusers: health snapshot of the BosBase instance (build, runtime, database/pgvector, connection pool, settings, recent error counts, integrations) with a list of detected problems. Start troubleshooting here. | — |
| `diagnostics_log_files` | **superuser** | read-only | Superusers: list the on disk error log files ({dataDir}/logs/errors-YYYY-MM-DD.jsonl) or read the tail of one of them. | `file`, `search`, `tail` |
| `diagnostics_report` | **superuser** | write | Superusers: generate a markdown error report (health, top error groups, latest server errors) and save it under {dataDir}/logs/reports so it can be attached to a bug report. | `save`, `since` |

## HTTP API access

| Tool | Access | Kind | What it does | Arguments |
| --- | --- | --- | --- | --- |
| `api_docs` | user | read-only | Read the BosBase API reference. Pass path (and optionally method) for the documentation of specific endpoints, or topic for a whole chapter ("overview" documents auth, errors, filter/sort syntax and list query parameters). | `method`, `path`, `topic` |
| `api_endpoints` | user | read-only | List the BosBase HTTP API endpoints available to the current token (method, path, required auth and summary). Use api_docs for the full documentation of an endpoint and api_request to call it. | `search` |
| `api_request` | user | write | Call any BosBase HTTP API endpoint with the current auth token (records, collections, auth, files, batch, graphql, sql, settings, logs, backups, crons, vectors, llm-documents, langchaingo, cache, redis, scripts, custom routes...). Streaming endpoints … | `body`, `files`, `headers`, `method`*, `path`*, `query` |
| `collection_get` | **superuser** | read-only | Get a collection definition (fields, rules, indexes) by name or id. | `collection`* |
| `collections_list` | **superuser** | read-only | List the collections with their fields, rules and indexes. | `fields`, `filter`, `page`, `perPage` |
| `record_create` | user | write | Create a record (respects the collection create rule). For auth collections include password and passwordConfirm. | `collection`*, `data`*, `expand`, `files` |
| `record_delete` | user | destructive | Delete a record (respects the collection delete rule). | `collection`*, `id`* |
| `record_get` | user | read-only | Get a single record by id (respects the collection view rule). | `collection`*, `expand`, `fields`, `id`* |
| `record_update` | user | write | Update a record (respects the collection update rule). Supports field modifiers like "count+": 1 or "tags-": ["x"]. | `collection`*, `data`, `expand`, `files`, `id`* |
| `records_list` | user | read-only | List/search the records of a collection (respects the collection list rule). | `collection`*, `expand`, `fields`, `filter`, `page`, `perPage`, `skipTotal`, `sort` |

`*` = required argument.