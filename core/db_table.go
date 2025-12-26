package core

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"dbx"

	"github.com/coocood/freecache"
)

var (
	tableExistsCacheOnce sync.Once
	tableExistsCache     *freecache.Cache
)

func getTableExistsCache() *freecache.Cache {
	tableExistsCacheOnce.Do(func() {
		// Small cache is enough since the key-space is the set of table names.
		// Values are a single byte (0/1). 256KB
		tableExistsCache = freecache.NewCache(256 * 1024)
	})

	return tableExistsCache
}

func tableExistsCacheKey(app *BaseApp, tableName string) []byte {
	// Include app.DataDir() in case multiple app instances exist in the same process.
	return []byte(strings.ToLower(tableName))
}

// TableColumns returns all column names of a single table by its name.
func (app *BaseApp) TableColumns(tableName string) ([]string, error) {
	columns := []string{}

	err := app.ConcurrentDB().
		Select("column_name").
		From("information_schema.columns").
		AndWhere(dbx.NewExp("table_schema = current_schema()")).
		AndWhere(dbx.NewExp("LOWER(table_name) = LOWER({:tableName})", dbx.Params{"tableName": tableName})).
		OrderBy("ordinal_position").
		Column(&columns)

	return columns, err
}

type TableInfoRow struct {
	// the `db:"pk"` tag has special semantic so we cannot rename
	// the original field without specifying a custom mapper
	PK int

	Index        int            `db:"cid"`
	Name         string         `db:"name"`
	Type         string         `db:"type"`
	NotNull      bool           `db:"notnull"`
	DefaultValue sql.NullString `db:"dflt_value"`
}

// TableInfo returns the "table_info" pragma result for the specified table.
func (app *BaseApp) TableInfo(tableName string) ([]*TableInfoRow, error) {
	info := []*TableInfoRow{}

	query := `
SELECT
    att.attnum - 1 AS cid,
    att.attname AS name,
    pg_catalog.format_type(att.atttypid, att.atttypmod) AS type,
    att.attnotnull AS notnull,
    pg_get_expr(def.adbin, def.adrelid) AS dflt_value,
    CASE WHEN con.contype = 'p' THEN 1 ELSE 0 END AS pk
FROM pg_catalog.pg_attribute AS att
JOIN pg_catalog.pg_class AS cls ON cls.oid = att.attrelid
JOIN pg_catalog.pg_namespace AS ns ON ns.oid = cls.relnamespace
LEFT JOIN pg_catalog.pg_attrdef AS def ON def.adrelid = att.attrelid AND def.adnum = att.attnum
LEFT JOIN pg_catalog.pg_constraint AS con
    ON con.conrelid = att.attrelid
   AND con.contype = 'p'
   AND att.attnum = ANY(con.conkey)
WHERE cls.relkind IN ('r', 'p', 'v', 'm')
  AND ns.nspname = current_schema()
  AND LOWER(cls.relname) = LOWER({:tableName})
  AND att.attnum > 0
  AND NOT att.attisdropped
ORDER BY att.attnum`

	err := app.ConcurrentDB().NewQuery(query).
		Bind(dbx.Params{"tableName": tableName}).
		All(&info)
	if err != nil {
		return nil, err
	}

	// Postgres returns an empty result for invalid or missing tables,
	// so we additionally have to check whether the loaded info result is nonempty.
	if len(info) == 0 {
		return nil, fmt.Errorf("empty table info probably due to invalid or missing table %s", tableName)
	}

	return info, nil
}

// TableIndexes returns a name grouped map with all non empty index of the specified table.
//
// Note: This method doesn't return an error on nonexisting table.
func (app *BaseApp) TableIndexes(tableName string) (map[string]string, error) {
	indexes := []struct {
		Name string `db:"indexname"`
		Sql  string `db:"indexdef"`
	}{}

	err := app.ConcurrentDB().
		Select("indexname", "indexdef").
		From("pg_indexes").
		AndWhere(dbx.NewExp("schemaname = current_schema()")).
		AndWhere(dbx.NewExp("LOWER(tablename) = LOWER({:tableName})", dbx.Params{"tableName": tableName})).
		All(&indexes)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(indexes))

	for _, idx := range indexes {
		result[idx.Name] = idx.Sql
	}

	return result, nil
}

// DeleteTable drops the specified table.
//
// This method is a no-op if a table with the provided name doesn't exist.
//
// NB! Be aware that this method is vulnerable to SQL injection and the
// "tableName" argument must come only from trusted input!
func (app *BaseApp) DeleteTable(tableName string) error {
	_, err := app.NonconcurrentDB().NewQuery(fmt.Sprintf(
		"DROP TABLE IF EXISTS {{%s}}",
		tableName,
	)).Execute()
	if err == nil {
		// Best-effort cache invalidation.
		getTableExistsCache().Del(tableExistsCacheKey(app, tableName))
	}

	return err
}

// HasTable checks if a table (or view) with the provided name exists (case insensitive).
// in the data.db.
func (app *BaseApp) HasTable(tableName string) bool {
	return app.hasTable(app.ConcurrentDB(), tableName)
}

// AuxHasTable checks if a table (or view) with the provided name exists (case insensitive)
// in the auixiliary.db.
func (app *BaseApp) AuxHasTable(tableName string) bool {
	return app.hasTable(app.AuxConcurrentDB(), tableName)
}

func (app *BaseApp) hasTable(db dbx.Builder, tableName string) bool {
	cache := getTableExistsCache()
	key := tableExistsCacheKey(app, tableName)
	if cached, err := cache.Get(key); err == nil && len(cached) > 0 {
		return cached[0] == 1
	}

	var exists int

	err := db.Select("(1)").
		From("information_schema.tables").
		AndWhere(dbx.NewExp("table_schema = current_schema()")).
		AndWhere(dbx.NewExp("LOWER(table_name)=LOWER({:tableName})", dbx.Params{"tableName": tableName})).
		Limit(1).
		Row(&exists)

	found := err == nil && exists > 0
	if found {
		// Positive results are stable (tables typically only get added).
		_ = cache.Set(key, []byte{1}, 0)
	} else {
		// Cache negatives briefly to reduce hot-loop lookups during bootstrap/migrations,
		// while still allowing new tables to be observed soon after creation.
		_ = cache.Set(key, []byte{0}, 2)
	}

	return found
}

// Vacuum executes VACUUM on the data.db in order to reclaim unused data db disk space.
func (app *BaseApp) Vacuum() error {
	return app.vacuum(app.NonconcurrentDB())
}

// AuxVacuum executes VACUUM on the auxiliary.db in order to reclaim unused auxiliary db disk space.
func (app *BaseApp) AuxVacuum() error {
	return app.vacuum(app.AuxNonconcurrentDB())
}

func (app *BaseApp) vacuum(db dbx.Builder) error {
	_, err := db.NewQuery("VACUUM").Execute()

	return err
}
