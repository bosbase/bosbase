package migrations

import (
	"github.com/bosbase/bosbase-enterprise/core"
)

// _storage_providers.publicRead: the bucket objects are publicly readable, so the
// direct object URL can be returned as a permanent public link.
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			_, err := txApp.DB().NewQuery(
				`ALTER TABLE {{_storage_providers}} ADD COLUMN IF NOT EXISTS [[publicRead]] BOOLEAN DEFAULT FALSE NOT NULL`,
			).Execute()
			return err
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().NewQuery(`ALTER TABLE {{_storage_providers}} DROP COLUMN IF EXISTS [[publicRead]]`).Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			columns, err := txApp.TableColumns("_storage_providers")
			if err != nil {
				return false, err
			}
			for _, c := range columns {
				if c == "publicRead" {
					return false, nil
				}
			}
			return true, nil
		},
	})
}
