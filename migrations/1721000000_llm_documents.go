package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.NonconcurrentDB())
			jsonType := core.JSONColumnType(driver)
			jsonDefault := core.JSONDefaultLiteral(driver, "{}")

			createSQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_llm_collections}} (
					[[id]]         TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[name]]       TEXT UNIQUE NOT NULL,
					[[table_name]] TEXT UNIQUE,
					[[dimension]]  INTEGER DEFAULT 0 NOT NULL,
					[[metadata]]   %s DEFAULT %s NOT NULL,
					%s,
					%s
				);
			`,
				core.RandomIDExpr(driver),
				jsonType, jsonDefault,
				core.TimestampColumnDefinition(driver, "created"),
				core.TimestampColumnDefinition(driver, "updated"),
			)

			if _, err := txApp.DB().NewQuery(createSQL).Execute(); err != nil {
				return fmt.Errorf("_llm_collections exec error: %w", err)
			}

			return nil
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().DropTable("_llm_collections").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			return !txApp.HasTable("_llm_collections"), nil
		},
	})
}
