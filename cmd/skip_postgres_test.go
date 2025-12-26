package cmd

import (
	"os"
	"testing"
)

// Skip CLI tests when running against the Postgres DSN because the fixtures
// depend on the bundled SQLite data directory.
func TestMain(m *testing.M) {
	if os.Getenv("SASSPB_POSTGRES_URL") != "" || os.Getenv("PB_TEST_POSTGRES_URL") != "" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}
