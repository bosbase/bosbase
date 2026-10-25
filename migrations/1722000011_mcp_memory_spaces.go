package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

// Shared memory spaces: several users read and write the same memory library.
//
//   - _mcp_spaces: the spaces (created by a user, its owner)
//   - _mcp_space_members: memberships; joining creates a pending request that
//     the owner (or an admin) approves
//   - _mcp_memories.author / authorCollection / updatedBy: who wrote each memory
//
// The memories of a space are stored in _mcp_memories with owner "space:<id>".
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.NonconcurrentDB())
			idExpr := core.RandomIDExpr(driver)
			created := core.TimestampColumnDefinition(driver, "created")
			updated := core.TimestampColumnDefinition(driver, "updated")

			statements := []string{
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_spaces}} (
					[[id]]              TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[name]]            TEXT NOT NULL,
					[[description]]     TEXT DEFAULT '' NOT NULL,
					[[owner]]           TEXT NOT NULL,
					[[ownerCollection]] TEXT DEFAULT '' NOT NULL,
					[[listed]]          BOOLEAN DEFAULT FALSE NOT NULL,
					%s,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__mcp_spaces_name ON {{_mcp_spaces}} (LOWER([[name]]));`,
					idExpr, created, updated),
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_space_members}} (
					[[id]]               TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[space]]            TEXT NOT NULL REFERENCES {{_mcp_spaces}} ([[id]]) ON DELETE CASCADE,
					[[member]]           TEXT NOT NULL,
					[[memberCollection]] TEXT DEFAULT '' NOT NULL,
					[[role]]             TEXT DEFAULT 'member' NOT NULL,
					[[status]]           TEXT DEFAULT 'pending' NOT NULL,
					[[message]]          TEXT DEFAULT '' NOT NULL,
					[[reviewedBy]]       TEXT DEFAULT '' NOT NULL,
					%s,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__mcp_space_members_unique ON {{_mcp_space_members}} ([[space]], [[member]]);
				CREATE INDEX IF NOT EXISTS idx__mcp_space_members_member ON {{_mcp_space_members}} ([[member]], [[status]]);`,
					idExpr, created, updated),
				`ALTER TABLE {{_mcp_memories}} ADD COLUMN IF NOT EXISTS [[author]] TEXT DEFAULT '' NOT NULL`,
				`ALTER TABLE {{_mcp_memories}} ADD COLUMN IF NOT EXISTS [[authorCollection]] TEXT DEFAULT '' NOT NULL`,
				`ALTER TABLE {{_mcp_memories}} ADD COLUMN IF NOT EXISTS [[updatedBy]] TEXT DEFAULT '' NOT NULL`,
				// the personal memories were written by their owner
				`UPDATE {{_mcp_memories}} SET [[author]] = [[owner]], [[authorCollection]] = [[ownerCollection]]
					WHERE [[author]] = '' AND [[owner]] <> '' AND [[owner]] NOT LIKE 'space:%'`,
			}

			for _, sql := range statements {
				if _, err := txApp.DB().NewQuery(sql).Execute(); err != nil {
					return fmt.Errorf("memory spaces migration error: %w", err)
				}
			}

			return nil
		},
		Down: func(txApp core.App) error {
			for _, sql := range []string{
				`DELETE FROM {{_mcp_memories}} WHERE [[owner]] LIKE 'space:%'`,
				`ALTER TABLE {{_mcp_memories}} DROP COLUMN IF EXISTS [[author]]`,
				`ALTER TABLE {{_mcp_memories}} DROP COLUMN IF EXISTS [[authorCollection]]`,
				`ALTER TABLE {{_mcp_memories}} DROP COLUMN IF EXISTS [[updatedBy]]`,
				`DROP TABLE IF EXISTS {{_mcp_space_members}}`,
				`DROP TABLE IF EXISTS {{_mcp_spaces}}`,
			} {
				if _, err := txApp.DB().NewQuery(sql).Execute(); err != nil {
					return err
				}
			}
			return nil
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			if !txApp.HasTable("_mcp_spaces") || !txApp.HasTable("_mcp_space_members") {
				return true, nil
			}
			columns, err := txApp.TableColumns("_mcp_memories")
			if err != nil {
				return false, err
			}
			for _, c := range columns {
				if c == "author" {
					return false, nil
				}
			}
			return true, nil
		},
	})
}
