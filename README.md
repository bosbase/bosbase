# Bosbase

**Bosbase** is a completely open-source backend platform designed as a modern alternative to PocketBase and Supabase. it delivers a full-featured backend solution while embracing the AI era with AI-driven development tools and flexible extensibility.

Bosbase is built on the foundation of PocketBase — huge thanks to the PocketBase project for the inspiration! We extended and redesigned it from first principles to better support large-scale commercial deployments, PostgreSQL as the primary database (with pgvector for vector capabilities), zero-downtime hot-reload for complex business logic, WASM plugins, and AI-assisted operations.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Docker Image](https://img.shields.io/docker/pulls/bosbase/bosbase.svg)](https://hub.docker.com/r/bosbase/bosbase)

## Features

- **AI-driven development** — Integrate AI to automate operations, generate code, implement logic, and bring your creative ideas to life faster.
- **Complete backend in one place** — Database (PostgreSQL + pgvector), Authentication, instant REST/GraphQL-like APIs, Realtime subscriptions, File Storage, Vector embeddings support, and WASM runtime for custom plugins.
- **Hot-reload & zero-downtime updates** — Implement and modify complex business logic without restarting the service.
- **PostgreSQL backend** — Scalable, production-ready with PostgreSQL support for enterprise-grade deployments and cost reduction.
- **Extensible platform** — Designed for AI-driven automated operations and custom extensions.
- **Open source & commercial friendly** — MIT licensed, fully open, supports self-hosting and large-scale use.

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

## Acknowledgments

Bosbase would not exist without [PocketBase](https://pocketbase.io/) — thank you for the amazing foundation!

## License

MIT License — see [LICENSE](LICENSE) for details.

Happy building! 🚀
```

This README is structured, concise, and incorporates all the key points from your provided content. It includes badges for visual appeal, clear sections, code blocks, links, and a professional tone suitable for GitHub. You can copy-paste this directly into your `README.md` file. If you have additional sections (e.g., architecture diagram, contribution guidelines, or SDK links), they can be added easily.