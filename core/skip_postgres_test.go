package core

import (
	"os"
	"testing"
)

// The core package tests rely on SQLite fixtures. Skip them when running
// with a Postgres DSN to avoid false negatives in environments without
// the seeded data.
func TestMain(m *testing.M) {
	if os.Getenv("SASSPB_POSTGRES_URL") != "" || os.Getenv("PB_TEST_POSTGRES_URL") != "" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}
