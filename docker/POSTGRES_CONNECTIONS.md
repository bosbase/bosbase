# Postgres connections (max connections & current usage)

## Check max connections

### From `psql`

```sql
SHOW max_connections;
```

Or with `pg_settings`:

```sql
SELECT name, setting, unit
FROM pg_settings
WHERE name = 'max_connections';
```

### From Docker (docker compose)

```bash
docker compose -f docker/docker-compose.db.yml exec postgres-db \
  psql -U postgres -d pbosbase -c "SHOW max_connections;"
```

## Check current usage vs max

```sql
SELECT
  (SELECT setting::int FROM pg_settings WHERE name='max_connections') AS max_connections,
  (SELECT count(*) FROM pg_stat_activity) AS current_connections;
```

Active only:

```sql
SELECT count(*)
FROM pg_stat_activity
WHERE state = 'active';
```

## Notes

- `max_connections` is a server-side limit on concurrent sessions.
- “Connection pool size” is a client/app concern (or handled by a pooler like PgBouncer), not a Postgres server setting.

```sql
SELECT
  (SELECT setting::int FROM pg_settings WHERE name='max_connections') AS max_connections,
  (SELECT count(*) FROM pg_stat_activity) AS current_connections;
```