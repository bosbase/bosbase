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
			CREATE TABLE IF NOT EXISTS {{pb_hooks}} (
				[[id]]       TEXT PRIMARY KEY DEFAULT %s NOT NULL,
				[[filename]] TEXT UNIQUE NOT NULL,
				[[content]]  TEXT NOT NULL,
				%s,
				%s
			);
		`,
				core.RandomIDExpr(driver),
				core.TimestampColumnDefinition(driver, "created"),
				core.TimestampColumnDefinition(driver, "updated"),
			)

			if _, err := txApp.DB().NewQuery(createSQL).Execute(); err != nil {
				return fmt.Errorf("pb_hooks exec error: %w", err)
			}

			return nil
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().DropTable("pb_hooks").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, filename string) (bool, error) {
			return !txApp.HasTable("pb_hooks"), nil
		},
	})
}
