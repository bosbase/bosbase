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
				CREATE TABLE IF NOT EXISTS {{function_script_permissions}} (
					[[id]]          TEXT PRIMARY KEY NOT NULL,
					[[script_id]]   TEXT,
					[[script_name]] TEXT NOT NULL UNIQUE,
					[[content]]     TEXT NOT NULL,
					[[version]]     INTEGER NOT NULL DEFAULT 1,
					%s,
					%s
				);
			`,
				core.TimestampColumnDefinition(driver, "created"),
				core.TimestampColumnDefinition(driver, "updated"),
			)

			if _, err := txApp.DB().NewQuery(createSQL).Execute(); err != nil {
				return fmt.Errorf("function_script_permissions table init error: %w", err)
			}

			return nil
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().DropTable("function_script_permissions").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, filename string) (bool, error) {
			return !txApp.HasTable("function_script_permissions"), nil
		},
	})
}
