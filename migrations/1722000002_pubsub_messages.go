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

			createSQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_pubsub_messages}} (
					[[id]]        TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[topic]]     TEXT NOT NULL,
					[[payload]]   %s NOT NULL,
					[[origin]]    TEXT NOT NULL,
					[[createdBy]] TEXT DEFAULT NULL,
					%s
				);

				CREATE INDEX IF NOT EXISTS idx__pubsub_messages_created ON {{_pubsub_messages}} ([[created]]);
				CREATE INDEX IF NOT EXISTS idx__pubsub_messages_topic_created ON {{_pubsub_messages}} ([[topic]], [[created]]);
			`,
				core.RandomIDExpr(driver),
				jsonType,
				core.TimestampColumnDefinition(driver, "created"),
			)

			_, err := txApp.DB().NewQuery(createSQL).Execute()
			return err
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().DropTable("_pubsub_messages").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, filename string) (bool, error) {
			return !txApp.HasTable("_pubsub_messages"), nil
		},
	})
}
