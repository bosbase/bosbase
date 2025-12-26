package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.NonconcurrentDB())

			createSQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{function_scripts}} (
					[[id]]          TEXT NOT NULL,
					[[name]]        TEXT PRIMARY KEY,
					[[content]]     TEXT NOT NULL,
					[[description]] TEXT DEFAULT '',
					[[version]]     INTEGER NOT NULL DEFAULT 1,
					%s,
					%s
				);
			`,
				core.TimestampColumnDefinition(driver, "created"),
				core.TimestampColumnDefinition(driver, "updated"),
			)

			indexSQL := `
				CREATE UNIQUE INDEX IF NOT EXISTS idx__function_scripts_id ON {{function_scripts}} ([[id]]);
			`

			if _, err := txApp.DB().NewQuery(createSQL).Execute(); err != nil {
				return fmt.Errorf("function_scripts table init error: %w", err)
			}

			if _, err := txApp.DB().NewQuery(indexSQL).Execute(); err != nil {
				return fmt.Errorf("function_scripts index error: %w", err)
			}

			return nil
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().DropTable("function_scripts").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, filename string) (bool, error) {
			return !txApp.HasTable("function_scripts"), nil
		},
	})
}
