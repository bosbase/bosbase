# Bosbase

**Bosbase** is a permanently open-source and free backend platform designed as a modern alternative to PocketBase and Supabase. It delivers a full-featured backend solution while embracing the AI era with AI-driven development tools and flexible extensibility.

Bosbase is completely free for personal use and for large-scale enterprise deployments. It's fully community-managed with no licensing fees or hidden costs.

Bosbase is built on the foundation of PocketBase — huge thanks to the PocketBase project for the inspiration! We extended and redesigned it from first principles to better support large-scale commercial deployments, PostgreSQL as the primary database (with pgvector for vector capabilities), zero-downtime hot-reload for complex business logic, WASM plugins, and AI-assisted operations.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Docker Image](https://img.shields.io/docker/pulls/bosbase/bosbase.svg)](https://hub.docker.com/r/bosbase/bosbase)

## Features

- **AI-driven development** — Integrate AI to automate operations, generate code, implement logic, and bring your creative ideas to life faster.
- **Complete backend in one place** — Database (PostgreSQL + pgvector), Authentication, instant REST/GraphQL-like APIs, Realtime subscriptions, File Storage, Vector embeddings support, and WASM runtime for custom plugins.
- **Hot-reload & zero-downtime updates** — Implement and modify complex business logic without restarting the service.
- **PostgreSQL backend** — Scalable, production-ready with PostgreSQL support for enterprise-grade deployments and cost reduction.
- **Extensible platform** — Designed for AI-driven automated operations and custom extensions.
- **Permanently open source & free** — Apache 2.0 licensed, completely free for personal use and large-scale enterprise deployments. Fully community-managed with no licensing fees.

## About Bosbase

Bosbase is permanently open source and free. It's completely free for personal use and for large-scale enterprise deployments. It's fully community-managed, ensuring transparency and long-term sustainability.

Bosbase helps enterprises and developers reduce backend costs while enabling powerful AI-native features.

## Quick Start

### Using Docker (Recommended)

```bash
# Start PostgreSQL with pgvector
docker compose -f docker/docker-compose.db.yml up --build -d

# Start Bosbase
docker compose -f docker/docker-compose.yml up --build
```

Official Docker image: `bosbase/bosbase:ve1`

Access the admin dashboard at `http://localhost:8090/_/` (or your configured port).
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

Apache License 2.0 — see [LICENSE](LICENSE) for details.

### Our Commitment

Bosbase is permanently open source and free. It's completely free for personal use and for large-scale enterprise deployments. It's fully community-managed with:

- ✅ No licensing fees
- ✅ No enterprise tiers or paid features
- ✅ No usage limits or restrictions
- ✅ Full transparency and community governance
- ✅ Freedom to self-host and modify

We believe in open-source software that remains free forever, empowering developers and businesses of all sizes to build amazing products without backend licensing costs.