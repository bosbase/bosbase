# BosBase API Reference

This folder contains the BosBase backend API documentation organized by interface category and functional area. All documents are written for direct HTTP/SSE/WebSocket integration without requiring a generated SDK.

## General

- [Overview, Authentication, Errors, and Shared Types](./00-overview.md)

## HTTP APIs

- [Health, Settings, and Admin Utilities](./01-admin.md)
- [Collections and Records](./02-collections-records.md)
- [Authentication and Files](./03-auth-files.md)
- [Backups, Batch, GraphQL, SQL, Logs, and Cron](./04-system-http.md)
- [Vector, LLM Documents, and LangChainGo](./05-ai-vector-llm.md)
- [Cache, Script Permissions, Scripts, WASM, Redis, and Output Proxy](./06-runtime-ops.md)

## Streaming APIs

- [Server-Sent Events APIs](./07-sse.md)
- [WebSocket APIs](./08-websocket.md)

## AI Agent APIs

- [MCP Server (AI memory, knowledge vault, full API access)](./09-mcp.md)
- [Object Storage, LLM Relay, Embedding Providers and Diagnostics](./10-storage-llm-diagnostics.md)
- [Memory Spaces (shared memories)](./11-memory-spaces.md)
- [Group Chat and Agent Accounts](./12-chat-accounts.md)

MCP user guide: [docs/mcp-guide.md](../../docs/mcp-guide.md) ([Chinese translation](../../docs/mcp-guide.zh-CN.md))

## Route Coverage

The documentation was derived from the Go route bindings in `apis/base.go` and all `bind*Api` functions under `apis/`.
