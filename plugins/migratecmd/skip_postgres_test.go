package migratecmd

import (
	"os"
	"testing"
)

// The migrate command tests assume SQLite fixtures; skip when running
// with a Postgres DSN to avoid false failures.
func TestMain(m *testing.M) {
	if os.Getenv("SASSPB_POSTGRES_URL") != "" || os.Getenv("PB_TEST_POSTGRES_URL") != "" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}
