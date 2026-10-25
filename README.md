# Bosbase

**Bosbase** is a permanently open-source and free backend platform designed as a modern alternative to PocketBase and Supabase. It delivers a full-featured backend solution while embracing the AI era with AI-driven development tools and flexible extensibility.

Bosbase is completely free for personal use and for large-scale enterprise deployments. It's fully community-managed with no licensing fees or hidden costs.

Bosbase is built on the foundation of PocketBase — huge thanks to the PocketBase project for the inspiration! We extended and redesigned it from first principles to better support large-scale commercial deployments, PostgreSQL as the primary database (with pgvector for vector capabilities), zero-downtime hot-reload for complex business logic, WASM plugins, and AI-assisted operations.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Docker Image](https://img.shields.io/docker/pulls/bosbase/bosbase.svg)](https://hub.docker.com/r/bosbase/bosbase)

## Features

### Complete backend

- **PostgreSQL first** — Production-ready PostgreSQL with pgvector for vectors; all system tables are created automatically by migrations on first start.
- **Instant APIs** — Collections and records with a REST API (filtering, sorting, relation expansion, views), API rules for row-level access control, batch requests, GraphQL and raw SQL for superusers.
- **Authentication** — Password, OAuth2 and OTP sign-in, email verification, password reset, email change, impersonation and custom token binding.
- **Files** — File fields stored locally or on S3, with protected file access tokens.
- **Realtime** — Server-Sent Events subscriptions filtered by the API rules, and WebSocket pub/sub.
- **Operations** — Admin dashboard, settings, backups, request logs and cron jobs.

### Extensibility and runtime

- **Hot reload, zero downtime** — Create and change business logic scripts without restarting the service; run them synchronously, asynchronously or streamed over SSE/WebSocket, with per-script permissions.
- **WASM plugins** — WASM runtime for custom plugins and high-performance logic.
- **Cache and Redis** — Cache and Redis key APIs for fast shared state.

### AI native

- **Vectors and RAG** — Vector collections with semantic search, LLM documents, and LangChainGo helpers for completions, RAG, document querying and text-to-SQL.
- **LLM relay** — Store several LLM channels and keys (OpenAI compatible or Anthropic) and call them through BosBase: any OpenAI SDK works with `/api/llm/v1` and a BosBase token, with streaming and usage tracking per channel and model.
- **Embedding providers** — Superusers configure several embedding providers in the database with a default, priorities and automatic failover, applied without restart; existing memories can be re-embedded after switching models.

### Remote MCP server for AI agents

AI agents connect to `/api/mcp` (MCP Streamable HTTP) with a BosBase token and get a persistent brain:

- **Long-term memory** — Memories organized in projects, stored in PostgreSQL + pgvector, with semantic and keyword search, typed relations, graph traversal and shortest paths.
- **Context across conversations** — Pinned core memories, latest session summaries and open todos are given to the agent automatically when it connects; `memory_context` briefs it for the task, `memory_remember` saves what it learned with deduplication, and `memory_extract` lets the server extract memories from a transcript with an LLM.
- **Conversation memory** — Conversation turns are saved and recalled by meaning, condensed into rolling summaries and consolidated into long-term project summaries.
- **Obsidian-like knowledge vault** — Markdown notes with folders, frontmatter properties, tags, `[[wikilinks]]`, backlinks, renames that rewrite links, daily notes and version history.
- **Knowledge graph** — Associative retrieval that spreads from the best matches along links, backlinks, relations, semantic neighbours and shared tags and explains each path; graph view with hubs, orphans and clusters; link suggestions.
- **Private by default** — Each user's memories and notes are isolated; public knowledge bases are maintained by superusers.
- **Shared memory spaces** — Teams share a memory library: joining requires the creator's approval and every memory records its author (MCP and HTTP API).
- **Group chat between agents** — Agents chat in rooms (`chat_room`, `chat_send`, `chat_read` with long polling), and apps follow the same rooms through the realtime subscriptions.
- **Agent accounts** — Superusers create user accounts over MCP or HTTP and get back the email, password and token to hand to each agent.
- **Activity journal** — Every tool call is recorded so the agent remembers what it did in previous sessions.
- **The whole HTTP API** — Agents discover, read the documentation of and call every endpoint with the caller's own permissions.

```sh
claude mcp add --transport http bosbase https://your-host/api/mcp --header "Authorization: Bearer <token>"
```

See the [MCP guide](docs/mcp-guide.md) and the [MCP API reference](apidocs/api-reference/09-mcp.md).

### Object storage

- **Multiple S3-compatible providers** — AWS S3, Cloudflare R2, Google Cloud Storage, MinIO, Backblaze B2 or any S3-compatible service, configured in the database; superusers set the shared default provider.
- **Public links** — Uploads over HTTP or MCP return a link usable from the internet: permanent (CDN domain or public bucket) or presigned; large files upload directly to the storage with presigned URLs.
- **Upload history** — Every object is recorded in PostgreSQL per user, and kept after deletion.

### Security and diagnostics

- **Secrets encrypted at rest** — Storage keys, LLM and embedding API keys are encrypted with the app encryption key and never returned; passwords, tokens and keys are redacted from the request logs.
- **Safe upstreams** — Regular users' storage and LLM configurations can't reach private network addresses (SSRF protection).
- **Diagnostics for AI** — Superusers (and their agents) get a health snapshot, grouped error logs from the database or the error log files written to disk, and Markdown error reports.

### Open source

- **Permanently open source and free** — MIT licensed, completely free for personal use and large-scale enterprise deployments, fully community-managed with no licensing fees.

## About Bosbase

Bosbase is permanently open source and free. It's completely free for personal use and for large-scale enterprise deployments. It's fully community-managed, ensuring transparency and long-term sustainability.

Bosbase helps enterprises and developers reduce backend costs while enabling powerful AI-native features.

## Quick Start

### Using Docker (Recommended)

Create a `docker-compose.yml` file with the following services, then run `docker compose up -d`:

```yaml
services:
  bosbase-node:
    image: bosbase/bosbase:latest
    restart: unless-stopped
    environment:
      SASSPB_POSTGRES_URL: postgres://postgres:postgres@postgres-db:5432/pbosbase?sslmode=disable
      BS_ENCRYPTION_KEY: 0123456789abcdef0123456789abcdef
      # OPENAI_BASE_URL: ${OPENAI_BASE_URL:-https://api.chatanywhere.org/v1}
      # REDIS_URL: ${REDIS_URL:-192.168.1.60:6379}
      # REDIS_PASSWORD: ${REDIS_PASSWORD:-}
      WASM_ENABLE: ${WASM_ENABLE:-true}
      WASM_INSTANCE_NUM: ${WASM_INSTANCE_NUM:-2}
      SCRIPT_CONCURRENCY: ${SCRIPT_CONCURRENCY:-2}
      FUNCTION_CONN_NUM: ${FUNCTION_CONN_NUM:-10}
      EXECUTE_PATH: ${EXECUTE_PATH:-/pb/functions}
      # BOOSTER_PATH: ${BOOSTER_PATH:-/pb/booster-wasm}
      # BOOSTER_POOL_MAX: ${BOOSTER_POOL_MAX:-2}
      # BOOSTER_WASMTIME_MEMORY_GUARD_SIZE: ${BOOSTER_WASMTIME_MEMORY_GUARD_SIZE:-65536}
      # BOOSTER_WASMTIME_MEMORY_RESERVATION: ${BOOSTER_WASMTIME_MEMORY_RESERVATION:-0}
      # BOOSTER_WASMTIME_MEMORY_RESERVATION_FOR_GROWTH: ${BOOSTER_WASMTIME_MEMORY_RESERVATION_FOR_GROWTH:-1048576}
      PB_DATA_MAX_OPEN_CONNS: ${PB_DATA_MAX_OPEN_CONNS:-30}
      PB_DATA_MAX_IDLE_CONNS: ${PB_DATA_MAX_IDLE_CONNS:-15}
      PB_AUX_MAX_OPEN_CONNS: ${PB_AUX_MAX_OPEN_CONNS:-10}
      PB_AUX_MAX_IDLE_CONNS: ${PB_AUX_MAX_IDLE_CONNS:-3}
      PB_QUERY_TIMEOUT: ${PB_QUERY_TIMEOUT:-300s}
    ports:
      - "8090:8090"
      - "2678:2678"
    volumes:
      - ./bosbase-data:/pb/pb_data
      - ./pb_hooks:/pb_hooks
    depends_on:
      postgres-db:
        condition: service_healthy
    networks:
      - basenode
    command: ["sh", "-c", "sleep 5 && /pb/bosbase serve --http=0.0.0.0:8090 --encryptionEnv BS_ENCRYPTION_KEY"]
    healthcheck:
      test: ["CMD-SHELL", "wget --quiet --tries=1 --spider http://localhost:8090/api/health || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 30s

  postgres-db:
    image: pgvector/pgvector:pg16
    restart: unless-stopped
    environment:
      POSTGRES_DB: pbosbase
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    volumes:
      - ./postgres-data:/var/lib/postgresql/data
    networks:
      - basenode
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d pbosbase"]
      interval: 2s
      timeout: 5s
      retries: 10
      start_period: 10s

networks:
  basenode:
    name: basenode
```

Access the admin dashboard at `http://localhost:8090/_/`.
API endpoint: `http://localhost:8090`

### Try Bosbase Online (No Installation)

- Dashboard: https://try.bosbase.com/_/
- Superuser email: `try@bosbase.com`
- Superuser password: `bosbasepass`
- API endpoint: https://try.bosbase.com

Feel free to explore and test features!

## Self-Hosting

For production-grade self-hosting (including PostgreSQL setup, reverse proxy with SSL, backups, monitoring, etc.), see the dedicated guide:

→ [bosbase/self-host](https://github.com/bosbase/self-host)

Includes one-click install scripts for Ubuntu/Rocky Linux, Docker Compose configurations, Caddy/NGINX reverse proxy, and more.

## Support

Questions, feedback, or issues? Reach out at **support@bosbase.com**

[Join discord](https://discord.gg/wJ7VxzXf)
## Docs & SDKs

### Official Documentation
[Bosbase docs](https://docs.bosbase.com/docs/sdk/)

### API Reference

The backend HTTP, SSE, and WebSocket interface documentation is available in the local [`apidocs/`](apidocs/) directory:

- Start here: [`apidocs/API_REFERENCE.md`](apidocs/API_REFERENCE.md)
- Categorized API docs: [`apidocs/api-reference/README.md`](apidocs/api-reference/README.md)

These documents describe request bodies, field meanings, types, examples, response formats, and endpoint usage for direct API integration.

### Frontend Templates & SDKs

Bosbase provides ready-to-use templates and SDKs for popular frameworks:

- **[bosbase/bosbase-nextjs](https://github.com/bosbase/bosbase-nextjs)** — Next.js template with Bosbase JavaScript SDK integration
  - Includes TypeScript support
  - Docker-ready configuration
  - Complete SDK documentation included
  - Google Auth setup guide
  - Locale switcher usage examples

- **[bosbase/bosbase_flutter](https://github.com/bosbase/bosbase_flutter)** — Flutter template with Bosbase Dart SDK
  - Cross-platform mobile app (Android, iOS, Web, Windows)
  - Complete CRUD examples (songs collection)
  - Offline mock data fallback
  - Flutter SDK 3.35.7 compatible
  - Step-by-step setup guide
  - JDK 17 configuration for Android

Both templates include:
- Pre-configured Bosbase client setup
- Authentication helpers
- CRUD operation examples
- Docker deployment support
- Complete documentation

See the individual repositories for detailed setup instructions and examples.

## How to use

1. **Copy the SDK Docs Path to Your Project**  
   To keep documentation handy (e.g., for reference or offline use):  
   - Clone or download the relevant SDK repository (e.g., `js-sdk` for JavaScript/TypeScript, `python-sdk` for Python, etc.) from https://github.com/orgs/bosbase/repositories.  
   - Copy the `docs/` folder (or README.md + any markdown docs) from the SDK repo into your project's `docs/sdk/` directory (create it if needed).  
   - Alternatively, link to the official docs: https://docs.bosbase.com/docs/sdk for the latest versions across all SDKs.

2. **Configure Bosbase Connection**  
   Use the following credentials and endpoint for testing/demo purposes (from the official Bosbase try instance). For production, deploy your own Bosbase instance and update these values.

   - **Superuser email:** try@bosbase.com  
   - **Superuser password:** bosbasepass  
   - **API endpoint (base URL):** https://try.bosbase.com  
    - **Admin URL:** https://try.bosbase.com/_/

   **Note on API endpoint to AI:** Bosbase supports AI-assisted development through its SDKs (see AI Development Guides in https://docs.bosbase.com/docs/sdk). The base API endpoint above handles all requests, including any AI-related features or integrations (no separate AI-specific endpoint is required; use the standard SDK methods for collections, auth, etc.). For AI-specific examples, refer to the guides in the docs (e.g., for JS SDK: https://docs.bosbase.com/docs/sdk/js-docs/ai-development-guide).

## Acknowledgments

Bosbase would not exist without [PocketBase](https://pocketbase.io/) — thank you for the amazing foundation!

## License

MIT License — see [LICENSE](LICENSE) for details.

### Our Commitment

Bosbase is permanently open source and free. It's completely free for personal use and for large-scale enterprise deployments. It's fully community-managed with:

- ✅ No licensing fees
- ✅ No enterprise tiers or paid features
- ✅ No usage limits or restrictions
- ✅ Full transparency and community governance
- ✅ Freedom to self-host and modify

We believe in open-source software that remains free forever, empowering developers and businesses of all sizes to build amazing products without backend licensing costs.