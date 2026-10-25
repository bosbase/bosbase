package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

// _embedding_providers: the server side embedding providers (OpenAI compatible
// /embeddings APIs) used to embed the MCP memories and notes.
//
// They are managed by superusers and read on every embedding call, so changes
// apply without restarting the server. The default provider is tried first,
// then the other enabled providers ordered by priority (lower first).
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.NonconcurrentDB())
			idExpr := core.RandomIDExpr(driver)
			created := core.TimestampColumnDefinition(driver, "created")
			updated := core.TimestampColumnDefinition(driver, "updated")

			statements := []string{
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_embedding_providers}} (
					[[id]]         TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[name]]       TEXT NOT NULL,
					[[type]]       TEXT DEFAULT 'openai' NOT NULL,
					[[baseUrl]]    TEXT NOT NULL,
					[[apiKey]]     TEXT DEFAULT '' NOT NULL,
					[[headers]]    TEXT DEFAULT '' NOT NULL,
					[[model]]      TEXT NOT NULL,
					[[dimensions]] INTEGER DEFAULT 0 NOT NULL,
					[[priority]]   INTEGER DEFAULT 100 NOT NULL,
					[[isDefault]]  BOOLEAN DEFAULT FALSE NOT NULL,
					[[enabled]]    BOOLEAN DEFAULT TRUE NOT NULL,
					[[lastError]]  TEXT DEFAULT '' NOT NULL,
					[[lastErrorAt]] TIMESTAMPTZ NULL,
					[[lastUsedAt]] TIMESTAMPTZ NULL,
					%s,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__embedding_providers_name ON {{_embedding_providers}} (LOWER([[name]]));
				CREATE UNIQUE INDEX IF NOT EXISTS idx__embedding_providers_default ON {{_embedding_providers}} ([[isDefault]]) WHERE [[isDefault]] = TRUE;`,
					idExpr, created, updated),
			}

			for _, sql := range statements {
				if _, err := txApp.DB().NewQuery(sql).Execute(); err != nil {
					return fmt.Errorf("embedding providers migration error: %w", err)
				}
			}

			return nil
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().DropTable("_embedding_providers").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			return !txApp.HasTable("_embedding_providers"), nil
		},
	})
}
