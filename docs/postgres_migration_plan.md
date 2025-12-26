# PostgreSQL Migration – Schema Inventory (Draft)

## 1. Core Tables (Primary DB)

| Table | Purpose | Current Columns (SQLite) | Proposed PostgreSQL Mapping | Notes / Decisions |
| ----- | ------- | ------------------------ | --------------------------- | ---------------------- |
| `_collections` | Stores collection metadata and rules. | `id TEXT PRIMARY KEY DEFAULT ('r'||lower(hex(randomblob(7))))`; `system BOOLEAN DEFAULT FALSE`; `type TEXT DEFAULT 'base'`; `name TEXT UNIQUE`; `fields JSON DEFAULT '[]'`; `indexes JSON DEFAULT '[]'`; `listRule TEXT`; `viewRule TEXT`; `createRule TEXT`; `updateRule TEXT`; `deleteRule TEXT`; `options JSON DEFAULT '{}'`; `created TEXT DEFAULT (strftime('%Y-%m-%d %H:%M:%fZ'))`; `updated TEXT DEFAULT (strftime('%Y-%m-%d %H:%M:%fZ'))`. | • `id` – **TBD** (UUID `gen_random_uuid()` or keep 15-char slug with application-generated default).<br>• `system` – `boolean NOT NULL DEFAULT false`.<br>• `type` – `text NOT NULL DEFAULT 'base'` with CHECK constraint on known values.<br>• `name` – `text NOT NULL UNIQUE` (consider case-insensitive unique index using `citext`).<br>• `fields`, `indexes`, `options` – `jsonb NOT NULL DEFAULT '[]'::jsonb` / `'{}'::jsonb`.<br>• Rule columns – `text` nullable.<br>• `created`, `updated` – `timestamptz NOT NULL DEFAULT now()` (trigger to maintain `updated`). | Need to confirm whether existing 15-char IDs must be preserved for compatibility. |
| `_params` | Key/value settings. | Same ID default as above; `value JSON DEFAULT NULL`; `created/updated` with `strftime`. | • `id` – same strategy as `_collections`.<br>• `value` – `jsonb` nullable.<br>• `created`, `updated` – `timestamptz` with defaults. | Introduce unique `name citext` column (non-null) for direct lookups; existing IDs retained for backwards compatibility. |
| `_externalAuths`, `_authOrigins`, `_mfas`, `_otps`, `_superusers`, `users` | Created via helper functions; share 15-char text IDs, multiple text/bool columns, auto-date fields. | Need full column inventory (see `create*Collection` helpers). | • Convert boolean-like fields to `boolean`.<br>• Password hashes remain `text`.<br>• Date columns → `timestamptz` with triggers.<br>• `lastResetSentAt`, etc. → `timestamptz`. | Many collections rely on default indexes defined via `Collection` model; must translate to Postgres expressions. |

## 2. Auxiliary DB (Logs)

| Table | Columns (SQLite) | Proposed PostgreSQL Mapping | Notes |
| ----- | ---------------- | --------------------------- | ----- |
| `_logs` | `id TEXT PRIMARY KEY DEFAULT ('r'||lower(hex(randomblob(7))))`; `level INTEGER DEFAULT 0`; `message TEXT DEFAULT ''`; `data JSON DEFAULT '{}'`; `created TEXT DEFAULT strftime(...)`; plus secondary indexes on `level`, `message`, `strftime('%Y-%m-%d %H:00:00', created)`. | • `id` – adopt global ID strategy (UUID or short ID).<br>• `level` – `smallint NOT NULL DEFAULT 0`.<br>• `message` – `text NOT NULL DEFAULT ''`.<br>• `data` – `jsonb NOT NULL DEFAULT '{}'::jsonb`.<br>• `created` – `timestamptz NOT NULL DEFAULT now()`.<br>• Indexes – `CREATE INDEX idx_logs_level ON _logs(level)`; `CREATE INDEX idx_logs_message ON _logs USING gin (message gin_trgm_ops)`; `CREATE INDEX idx_logs_created_hour ON _logs ((date_trunc('hour', created)))`. | Message index to use trigram GIN for partial matching; hourly index uses `date_trunc`. |

## 3. System Metadata Dependencies

- **Meta queries**: Numerous checks against `sqlite_master`/`sqlite_schema`. Replace with `pg_catalog.pg_tables`, `pg_indexes`, `information_schema.columns`, etc.
- **PRAGMAs**: Remove `PRAGMA optimize`, `wal_checkpoint`. PostgreSQL uses autovacuum; add optional maintenance tasks if required.
- **Date/time functions**: Replace `strftime` usage with `to_char` or rely on native `timestamptz` comparison.
- **Random IDs**: `randomblob` no longer available—must choose between:
  1. Application-generated 15-char IDs (preserves API compatibility).
  2. Database-generated UUIDs (`uuid` + `gen_random_uuid()`, requires `pgcrypto` or `uuid-ossp` extension).

## 4. Key Design Decisions (Resolved)

1. **Identifier Strategy**  
   - Preserve existing 15-character IDs for API compatibility. IDs continue to be generated in the application (via `GenerateDefaultRandomId`) and stored as `text`. Database defaults are removed; optional `BEFORE INSERT` trigger can call a shim function for direct SQL inserts.
2. **Case Sensitivity**  
   - Adopt PostgreSQL `citext` extension for columns that are currently case-insensitive in SQLite (e.g., `_collections.name`, auth emails). Unique indexes are applied on `citext` fields to mirror previous behavior.
3. **Dynamic Collections**  
   - Field type mapping is standardized:  
     - text → `text` / `citext` (where case-insensitive).  
     - number → `double precision` or `numeric` depending on precision.  
     - bool → `boolean`.  
     - date/time → `timestamptz`.  
     - file / JSON → `jsonb`.  
     - relation → `text` referencing target collection ID (FK enforced where feasible).  
   - DDL templates for base/auth collections will emit Postgres-compliant SQL with quoted identifiers.
4. **JSON Handling**  
   - All JSON columns migrate to `jsonb`. Application code will be updated to use Postgres operators (`->`, `->>`, `@>`). Indexing patterns (GIN) documented per field.
5. **Index Expressions**  
   - `dbutils.ParseIndex` will be extended to emit Postgres syntax (double-quoted identifiers, explicit ASC/DESC). Reserved keyword validation added during migration.

## 5. Next Artifacts

- **Detailed Column Matrix**: Extract full column sets from helper functions (`create*Collection`) and dynamic schema generator; document type mappings.
- **ER Diagram**: After column matrix is complete, produce ERD showing core relationships.
- **Migration Specification**: Outline sequential steps to create schema in PostgreSQL (init SQL + subsequent alters).

## 6. Action Items

1. Decide on ID generation strategy with stakeholders.  
2. Enumerate columns for auth/user-related tables via code inspection or runtime dump.  
3. Draft Postgres-compatible DDL once mappings are confirmed.  
4. Update metadata-check routines and index creation helpers.  
5. Prepare migration runner design (separate document).
