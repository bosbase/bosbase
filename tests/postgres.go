package tests

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dbx"
)

// PrepareTestSchema creates (or recreates) a dedicated schema for tests and returns
// a DSN configured to use it via search_path alongside a cleanup function that drops it.
func PrepareTestSchema(schema string) (string, func(), error) {
	baseDSN, err := testPostgresDSN()
	if err != nil {
		return "", nil, err
	}

	schemaName := SanitizeSchemaName(schema)
	if schemaName == "" {
		schemaName = fmt.Sprintf("pbtest_%d", time.Now().UnixNano())
	}

	if err := recreateTestSchema(baseDSN, schemaName); err != nil {
		return "", nil, err
	}

	schemaDSN, err := schemaWithSearchPath(baseDSN, schemaName)
	if err != nil {
		_ = dropTestSchema(baseDSN, schemaName)
		return "", nil, err
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = dropTestSchema(baseDSN, schemaName)
		})
	}

	return schemaDSN, cleanup, nil
}

// OpenTestSchemaDB prepares a new schema-backed Postgres connection and returns it with a cleanup callback.
func OpenTestSchemaDB(schema string) (*dbx.DB, func(), error) {
	dsn, cleanup, err := PrepareTestSchema(schema)
	if err != nil {
		return nil, nil, err
	}

	conn, err := dbx.Open("pgx", dsn)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	return conn, cleanup, nil
}

// SanitizeSchemaName converts an arbitrary string into a safe Postgres schema identifier.
func SanitizeSchemaName(name string) string {
	base := strings.TrimSpace(name)
	if base == "" {
		return ""
	}

	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ToLower(base)

	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('_')
	}

	return strings.Trim(b.String(), "_")
}

func testPostgresDSN() (string, error) {
	candidates := []string{
		os.Getenv("PB_TEST_POSTGRES_URL"),
		os.Getenv("SASSPB_POSTGRES_URL"),
	}

	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed, nil
		}
	}

	return "", fmt.Errorf("missing PB_TEST_POSTGRES_URL or SASSPB_POSTGRES_URL environment variable")
}

func recreateTestSchema(baseDSN, schema string) error {
	db, err := dbx.Open("pgx", baseDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	quoted := quoteIdentifier(schema)

	if _, err := db.NewQuery("DROP SCHEMA IF EXISTS " + quoted + " CASCADE").Execute(); err != nil {
		return err
	}

	_, err = db.NewQuery("CREATE SCHEMA " + quoted).Execute()
	return err
}

func dropTestSchema(baseDSN, schema string) error {
	db, err := dbx.Open("pgx", baseDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	quoted := quoteIdentifier(schema)
	_, err = db.NewQuery("DROP SCHEMA IF EXISTS " + quoted + " CASCADE").Execute()
	return err
}

func schemaWithSearchPath(baseDSN, schema string) (string, error) {
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		return "", err
	}

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

func quoteIdentifier(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}
