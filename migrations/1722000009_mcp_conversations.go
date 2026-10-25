package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

// Long-term conversation memory (always private to the owner):
//   - _mcp_conversations: conversation threads with their rolling summary
//   - _mcp_messages: the conversation turns, embedded for semantic recall
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.NonconcurrentDB())
			idExpr := core.RandomIDExpr(driver)
			created := core.TimestampColumnDefinition(driver, "created")
			updated := core.TimestampColumnDefinition(driver, "updated")

			statements := []string{
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_conversations}} (
					[[id]]              TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]           TEXT NOT NULL,
					[[ownerCollection]] TEXT DEFAULT '' NOT NULL,
					[[namespace]]       TEXT DEFAULT 'default' NOT NULL,
					[[title]]           TEXT DEFAULT '' NOT NULL,
					[[client]]          TEXT DEFAULT '' NOT NULL,
					[[summary]]         TEXT DEFAULT '' NOT NULL,
					[[summaryMemory]]   TEXT DEFAULT '' NOT NULL,
					[[summarizedUntil]] INTEGER DEFAULT 0 NOT NULL,
					[[messageCount]]    INTEGER DEFAULT 0 NOT NULL,
					[[lastMessageAt]]   TIMESTAMPTZ NULL,
					%s,
					%s
				);
				CREATE INDEX IF NOT EXISTS idx__mcp_conversations_owner ON {{_mcp_conversations}} ([[owner]], [[namespace]], [[updated]] DESC);`,
					idExpr, created, updated),
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_messages}} (
					[[id]]             TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[conversation]]   TEXT NOT NULL REFERENCES {{_mcp_conversations}} ([[id]]) ON DELETE CASCADE,
					[[owner]]          TEXT NOT NULL,
					[[seq]]            INTEGER NOT NULL,
					[[role]]           TEXT DEFAULT 'user' NOT NULL,
					[[content]]        TEXT DEFAULT '' NOT NULL,
					[[metadata]]       JSONB DEFAULT '{}'::jsonb NOT NULL,
					[[embedding]]      vector NULL,
					[[embeddingModel]] TEXT DEFAULT '' NOT NULL,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__mcp_messages_seq ON {{_mcp_messages}} ([[conversation]], [[seq]]);
				CREATE INDEX IF NOT EXISTS idx__mcp_messages_owner ON {{_mcp_messages}} ([[owner]], [[created]] DESC);
				CREATE INDEX IF NOT EXISTS idx__mcp_messages_fts ON {{_mcp_messages}} USING gin (to_tsvector('simple', [[content]]));`,
					idExpr, created),
			}

			for _, sql := range statements {
				if _, err := txApp.DB().NewQuery(sql).Execute(); err != nil {
					return fmt.Errorf("conversations migration error: %w", err)
				}
			}

			return nil
		},
		Down: func(txApp core.App) error {
			for _, table := range []string{"_mcp_messages", "_mcp_conversations"} {
				if _, err := txApp.DB().DropTable(table).Execute(); err != nil {
					return err
				}
			}
			return nil
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			return !txApp.HasTable("_mcp_conversations") || !txApp.HasTable("_mcp_messages"), nil
		},
	})
}
