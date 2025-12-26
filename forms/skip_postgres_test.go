package forms

import (
	"os"
	"testing"
)

// Forms tests rely on SQLite-backed fixtures; skip when using Postgres DSN.
func TestMain(m *testing.M) {
	if os.Getenv("SASSPB_POSTGRES_URL") != "" || os.Getenv("PB_TEST_POSTGRES_URL") != "" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}
