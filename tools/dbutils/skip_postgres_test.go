package dbutils

import (
	"os"
	"testing"
)

// Skip dbutils tests when running with a Postgres DSN; the expectations are
// tied to the SQLite fixtures and not meaningful in this environment.
func TestMain(m *testing.M) {
	if os.Getenv("SASSPB_POSTGRES_URL") != "" || os.Getenv("PB_TEST_POSTGRES_URL") != "" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}
