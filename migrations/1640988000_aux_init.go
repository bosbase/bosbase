package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.AuxNonconcurrentDB())
			jsonType := core.JSONColumnType(driver)

			// This project uses PostgreSQL exclusively
			// Note: date_trunc is STABLE (not IMMUTABLE), so we create a simple index on created
			// and use date_trunc in queries. For hourly grouping, a B-tree index on created works well.
			createdIndexSQL := `CREATE INDEX IF NOT EXISTS idx_logs_created on {{_logs}} ([[created]]);`

			sql := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_logs}} (
					[[id]]      TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[level]]   INTEGER DEFAULT 0 NOT NULL,
					[[message]] TEXT DEFAULT '' NOT NULL,
					[[data]]    %s DEFAULT %s NOT NULL,
					%s
				);

				CREATE INDEX IF NOT EXISTS idx_logs_level on {{_logs}} ([[level]]);
				CREATE INDEX IF NOT EXISTS idx_logs_message on {{_logs}} ([[message]]);
				%s
			`,
				core.RandomIDExpr(driver),
				jsonType,
				core.JSONDefaultLiteral(driver, "{}"),
				core.TimestampColumnDefinition(driver, "created"),
				createdIndexSQL,
			)

			_, execErr := txApp.AuxDB().NewQuery(sql).Execute()

			return execErr
		},
		Down: func(txApp core.App) error {
			_, err := txApp.AuxDB().DropTable("_logs").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			// reapply only if the _logs table doesn't exist
			exists := txApp.AuxHasTable("_logs")
			return !exists, nil
		},
	})
}
