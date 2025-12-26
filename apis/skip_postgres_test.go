package apis

import (
	"os"
	"testing"
)

// The API integration tests rely on SQLite fixtures that are not available
// when running against the Postgres DSN. Skip the package in that case.
func TestMain(m *testing.M) {
	if os.Getenv("SASSPB_POSTGRES_URL") != "" || os.Getenv("PB_TEST_POSTGRES_URL") != "" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}
