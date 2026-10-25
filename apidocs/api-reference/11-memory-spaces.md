# Memory Spaces API

Shared memory spaces let several users read and write the same memory library (eg. a team), through these HTTP endpoints or the MCP tools `space`, `space_member` and the `space` argument of the memory tools (see [MCP Server](./09-mcp.md#shared-memory-spaces)).

All endpoints require an auth token. `{space}` is the space id or name. Roles: `owner` (the creator: decides who joins and who is removed), `admin` (moderates the content), `member`. Membership status: `pending` (join request), `active`, `rejected`. Only active members (and superusers) can access the memories of a space; every memory records its `author`.

## Spaces

### GET /api/memory/spaces

#### Function

Lists the spaces of the caller (any membership status) with `myRole` / `myStatus`. Query: `discover=true` lists the listed (discoverable) spaces, `search`, `all=true` (superusers).

### POST /api/memory/spaces

#### Function

Creates a space; the caller becomes its owner.

#### Request Example

```json
{ "name": "product-team", "description": "Product and engineering shared memory", "listed": true }
```

### GET /api/memory/spaces/{space}

#### Function

Returns a space (members, owner, `pendingRequests` for owners/admins). Visible to its members, or to everyone when listed.

### PATCH /api/memory/spaces/{space}

#### Function

Updates `name`, `description`, `listed` (owner/admin).

### DELETE /api/memory/spaces/{space}

#### Function

Deletes the space with all its memories (owner).

### POST /api/memory/spaces/{space}/join

#### Function

Requests to join the space (`{message}` optional). The request stays `pending` until the owner (creator) approves it; a rejected user can request again.

### POST /api/memory/spaces/{space}/leave

#### Function

Leaves the space (the owner must transfer the ownership or delete the space instead).

## Members

### GET /api/memory/spaces/{space}/members

#### Function

Lists the active members (members); the owner also sees the pending/rejected requests (`?status=pending` filters).

### POST /api/memory/spaces/{space}/members/{member}/approve

#### Function

Approves a pending join request (owner only).

### POST /api/memory/spaces/{space}/members/{member}/reject

#### Function

Rejects a pending join request (owner only).

### PATCH /api/memory/spaces/{space}/members/{member}

#### Function

Changes the role of an active member: `{role: "admin" | "member" | "owner"}` (owner only; `owner` transfers the ownership and the previous owner becomes admin).

### DELETE /api/memory/spaces/{space}/members/{member}

#### Function

Removes a member (owner only). Removing yourself leaves the space.

## Space memories

The same rules as the MCP memory tools apply: members create memories; only the author or an owner/admin modifies or deletes another member's memory.

### GET /api/memory/spaces/{space}/memories

#### Function

Lists the memories of the space (newest first) or searches them with `q` (semantic + keyword). Query: `q`, `project`, `tags`, `limit`, `offset`. Items include `author`, `authorName`, `updatedBy`.

### POST /api/memory/spaces/{space}/memories

#### Function

Stores a memory in the space (body like the `memory_store` tool: `content`, `title`, `project`, `tags`, `metadata`, `pinned`, `dedupe`).

### GET /api/memory/spaces/{space}/memories/{id}

#### Function

Returns a memory of the space with its relations and author.

### PATCH /api/memory/spaces/{space}/memories/{id}

#### Function

Updates a memory (`content`, `title`, `tags`, `metadata`, `project`, `pinned`): the author or an owner/admin.

### DELETE /api/memory/spaces/{space}/memories/{id}

#### Function

Deletes a memory: the author or an owner/admin.
