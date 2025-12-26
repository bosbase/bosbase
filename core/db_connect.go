//go:build !no_default_driver

package core

import (
	"fmt"
	"os"
	"strings"

	"dbx"
)

// DefaultDBConnect establishes a new PostgreSQL connection using the
// SASSPB_POSTGRES_URL environment variable as the DSN. The project targets
// PostgreSQL exclusively, so the environment variable must be present.
func DefaultDBConnect(string) (*dbx.DB, error) {
	dsn := strings.TrimSpace(os.Getenv("SASSPB_POSTGRES_URL"))
	if dsn == "" {
		return nil, fmt.Errorf("missing required SASSPB_POSTGRES_URL environment variable")
	}

	return dbx.Open("pgx", dsn)
}
