package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

// Memory context support for _mcp_memories:
//   - pinned: core memories always given to the AI at the start of a session
//   - accessCount / lastAccessedAt: usage signals used to rank the context memories
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			statements := []string{
				`ALTER TABLE {{_mcp_memories}} ADD COLUMN IF NOT EXISTS [[pinned]] BOOLEAN DEFAULT FALSE NOT NULL`,
				`ALTER TABLE {{_mcp_memories}} ADD COLUMN IF NOT EXISTS [[accessCount]] INTEGER DEFAULT 0 NOT NULL`,
				`ALTER TABLE {{_mcp_memories}} ADD COLUMN IF NOT EXISTS [[lastAccessedAt]] TIMESTAMPTZ NULL`,
				`CREATE INDEX IF NOT EXISTS idx__mcp_memories_pinned ON {{_mcp_memories}} ([[owner]], [[updated]] DESC) WHERE [[pinned]] = TRUE`,
			}

			for _, sql := range statements {
				if _, err := txApp.DB().NewQuery(sql).Execute(); err != nil {
					return fmt.Errorf("memory context migration error: %w", err)
				}
			}

			return nil
		},
		Down: func(txApp core.App) error {
			_, err := txApp.DB().NewQuery(`
				DROP INDEX IF EXISTS idx__mcp_memories_pinned;
				ALTER TABLE {{_mcp_memories}} DROP COLUMN IF EXISTS [[pinned]];
				ALTER TABLE {{_mcp_memories}} DROP COLUMN IF EXISTS [[accessCount]];
				ALTER TABLE {{_mcp_memories}} DROP COLUMN IF EXISTS [[lastAccessedAt]];
			`).Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			columns, err := txApp.TableColumns("_mcp_memories")
			if err != nil {
				return false, err
			}
			for _, required := range []string{"pinned", "accessCount", "lastAccessedAt"} {
				found := false
				for _, c := range columns {
					if c == required {
						found = true
						break
					}
				}
				if !found {
					return true, nil
				}
			}
			return false, nil
		},
	})
}
