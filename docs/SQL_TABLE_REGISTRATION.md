# Register SQL Tables as Collections

Superusers can map existing Postgres tables to BosBase collections and instantly get REST APIs without changing the underlying schema.

## Quick Start (existing tables)
```js
import BosBase from "pocketbase";

const pb = new BosBase("http://localhost:8090");
pb.authStore.save(SUPERUSER_JWT); // must be a superuser token

const collections = await pb.collections.registerSqlTables([
  "projects",
  "accounts",
]);

console.log(collections.map((c) => c.name));
// => ["projects", "accounts"]
```

### With Request Options
```js
const collections = await pb.collections.registerSqlTables(
  ["legacy_orders"],
  {
    headers: { "x-trace-id": "reg-123" },
    q: 1, // adds ?q=1
  },
);
```

## Create-if-missing via SQL
You can supply SQL to create the table before registering it.
```js
await pb.collections.importSqlTables([
  {
    name: "legacy_orders",
    sql: `
      CREATE TABLE IF NOT EXISTS legacy_orders (
        id TEXT PRIMARY KEY,
        created TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
        updated TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
        customer_email TEXT NOT NULL,
        total NUMERIC NOT NULL
      );
    `,
  },
  { name: "reporting_view" }, // assumes table already exists
]);
```

## What It Does
- Creates BosBase collection metadata for the provided tables.
- Generates REST endpoints for CRUD against those tables.
- Leaves existing SQL schema and data untouched (no automatic field mutations or table sync).
- Returns `{ created, skipped }`, where `skipped` are table names that already have collections.

## Types
- `SqlTableDefinition { name: string; sql?: string; }`
- `SqlTableImportResult { created: CollectionModel[]; skipped: string[]; }`
- `CollectionModel` includes optional `externalTable?: boolean`.

## Notes
- Tables must include an `id` column (TEXT primary key) for collection mapping.
- `externalTable` collections skip default audit field injection and schema sync; include any desired audit fields in your SQL.
- Only superusers may call these endpoints.
