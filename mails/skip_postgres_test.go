package mails

import (
	"os"
	"testing"
)

// Mail tests rely on SQLite fixtures; skip when a Postgres DSN is used.
func TestMain(m *testing.M) {
	if os.Getenv("SASSPB_POSTGRES_URL") != "" || os.Getenv("PB_TEST_POSTGRES_URL") != "" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}
