package migrations

import (
	"github.com/bosbase/bosbase-enterprise/core"
)

// note: this migration will be deleted in future version

func init() {
	core.SystemMigrations.Register(func(txApp core.App) error {
		// Skip if _collections table doesn't exist yet (handled by init migration)
		if !txApp.HasTable("_collections") {
			return nil
		}

		_, err := txApp.DB().NewQuery("CREATE INDEX IF NOT EXISTS idx__collections_type on {{_collections}} ([[type]]);").Execute()
		if err != nil {
			return err
		}

		// reset mfas and otps delete rule
		collectionNames := []string{core.CollectionNameMFAs, core.CollectionNameOTPs}
		for _, name := range collectionNames {
			col, err := txApp.FindCollectionByNameOrId(name)
			if err != nil {
				// Collection may not exist on fresh install, skip
				continue
			}

			if col.DeleteRule != nil {
				col.DeleteRule = nil
				err = txApp.SaveNoValidate(col)
				if err != nil {
					return err
				}
			}
		}

		return nil
	}, nil)
}
