package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

// MCP memory knowledge base:
//   - _mcp_memories stores memories/notes (markdown content + optional pgvector embedding)
//   - _mcp_memory_edges stores typed relations between memories (knowledge graph)
//   - _mcp_memory_versions stores the previous versions of the memories content
//   - _mcp_activity stores the MCP tool calls journal (what the AI did)
//
// An empty owner marks a shared memory/edge that is readable by every
// authenticated user and writable only by superusers.
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.NonconcurrentDB())

			if _, err := txApp.DB().NewQuery(`CREATE EXTENSION IF NOT EXISTS vector;`).Execute(); err != nil {
				return fmt.Errorf("failed to enable vector extension: %w", err)
			}

			// note: the embedding column is intentionally dimensionless so that
			// different embedding models can coexist (searches filter by vector_dims)
			memoriesSQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_memories}} (
					[[id]]              TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]           TEXT DEFAULT '' NOT NULL,
					[[ownerCollection]] TEXT DEFAULT '' NOT NULL,
					[[namespace]]       TEXT DEFAULT 'default' NOT NULL,
					[[path]]            TEXT DEFAULT '' NOT NULL,
					[[title]]           TEXT DEFAULT '' NOT NULL,
					[[content]]         TEXT NOT NULL,
					[[tags]]            JSONB DEFAULT '[]'::jsonb NOT NULL,
					[[metadata]]        JSONB DEFAULT '{}'::jsonb NOT NULL,
					[[properties]]      JSONB DEFAULT '{}'::jsonb NOT NULL,
					[[links]]           JSONB DEFAULT '[]'::jsonb NOT NULL,
					[[embedding]]       vector NULL,
					[[embeddingModel]]  TEXT DEFAULT '' NOT NULL,
					%s,
					%s
				);
				CREATE INDEX IF NOT EXISTS idx__mcp_memories_owner_ns ON {{_mcp_memories}} ([[owner]], [[namespace]]);
				CREATE INDEX IF NOT EXISTS idx__mcp_memories_tags ON {{_mcp_memories}} USING gin ([[tags]]);
				CREATE INDEX IF NOT EXISTS idx__mcp_memories_properties ON {{_mcp_memories}} USING gin ([[properties]]);
				CREATE INDEX IF NOT EXISTS idx__mcp_memories_title ON {{_mcp_memories}} ([[owner]], [[namespace]], LOWER([[title]]));
				CREATE UNIQUE INDEX IF NOT EXISTS idx__mcp_memories_path ON {{_mcp_memories}} ([[owner]], [[namespace]], LOWER([[path]])) WHERE [[path]] <> '';
				CREATE INDEX IF NOT EXISTS idx__mcp_memories_fts ON {{_mcp_memories}} USING gin (to_tsvector('simple', [[title]] || ' ' || [[content]]));
			`,
				core.RandomIDExpr(driver),
				core.TimestampColumnDefinition(driver, "created"),
				core.TimestampColumnDefinition(driver, "updated"),
			)
			if _, err := txApp.DB().NewQuery(memoriesSQL).Execute(); err != nil {
				return fmt.Errorf("_mcp_memories exec error: %w", err)
			}

			edgesSQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_memory_edges}} (
					[[id]]       TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]    TEXT DEFAULT '' NOT NULL,
					[[source]]   TEXT NOT NULL REFERENCES {{_mcp_memories}} ([[id]]) ON DELETE CASCADE,
					[[target]]   TEXT NOT NULL REFERENCES {{_mcp_memories}} ([[id]]) ON DELETE CASCADE,
					[[relation]] TEXT NOT NULL,
					[[weight]]   DOUBLE PRECISION DEFAULT 1 NOT NULL,
					[[metadata]] JSONB DEFAULT '{}'::jsonb NOT NULL,
					%s,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__mcp_memory_edges_unique ON {{_mcp_memory_edges}} ([[owner]], [[source]], [[target]], [[relation]]);
				CREATE INDEX IF NOT EXISTS idx__mcp_memory_edges_source ON {{_mcp_memory_edges}} ([[source]]);
				CREATE INDEX IF NOT EXISTS idx__mcp_memory_edges_target ON {{_mcp_memory_edges}} ([[target]]);
			`,
				core.RandomIDExpr(driver),
				core.TimestampColumnDefinition(driver, "created"),
				core.TimestampColumnDefinition(driver, "updated"),
			)
			if _, err := txApp.DB().NewQuery(edgesSQL).Execute(); err != nil {
				return fmt.Errorf("_mcp_memory_edges exec error: %w", err)
			}

			versionsSQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_memory_versions}} (
					[[id]]         TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[memory]]     TEXT NOT NULL REFERENCES {{_mcp_memories}} ([[id]]) ON DELETE CASCADE,
					[[version]]    INTEGER NOT NULL,
					[[path]]       TEXT DEFAULT '' NOT NULL,
					[[title]]      TEXT DEFAULT '' NOT NULL,
					[[content]]    TEXT NOT NULL,
					[[tags]]       JSONB DEFAULT '[]'::jsonb NOT NULL,
					[[metadata]]   JSONB DEFAULT '{}'::jsonb NOT NULL,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__mcp_memory_versions_memory ON {{_mcp_memory_versions}} ([[memory]], [[version]]);
			`,
				core.RandomIDExpr(driver),
				core.TimestampColumnDefinition(driver, "created"),
			)
			if _, err := txApp.DB().NewQuery(versionsSQL).Execute(); err != nil {
				return fmt.Errorf("_mcp_memory_versions exec error: %w", err)
			}

			activitySQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_mcp_activity}} (
					[[id]]              TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]           TEXT NOT NULL,
					[[ownerCollection]] TEXT DEFAULT '' NOT NULL,
					[[tool]]            TEXT NOT NULL,
					[[namespace]]       TEXT DEFAULT '' NOT NULL,
					[[arguments]]       JSONB DEFAULT '{}'::jsonb NOT NULL,
					[[success]]         BOOLEAN DEFAULT TRUE NOT NULL,
					[[summary]]         TEXT DEFAULT '' NOT NULL,
					[[durationMs]]      INTEGER DEFAULT 0 NOT NULL,
					[[client]]          TEXT DEFAULT '' NOT NULL,
					[[readOnly]]        BOOLEAN DEFAULT FALSE NOT NULL,
					%s
				);
				CREATE INDEX IF NOT EXISTS idx__mcp_activity_owner ON {{_mcp_activity}} ([[owner]], [[created]] DESC);
			`,
				core.RandomIDExpr(driver),
				core.TimestampColumnDefinition(driver, "created"),
			)
			if _, err := txApp.DB().NewQuery(activitySQL).Execute(); err != nil {
				return fmt.Errorf("_mcp_activity exec error: %w", err)
			}

			return nil
		},
		Down: func(txApp core.App) error {
			if _, err := txApp.DB().DropTable("_mcp_activity").Execute(); err != nil {
				return err
			}
			if _, err := txApp.DB().DropTable("_mcp_memory_versions").Execute(); err != nil {
				return err
			}
			if _, err := txApp.DB().DropTable("_mcp_memory_edges").Execute(); err != nil {
				return err
			}
			_, err := txApp.DB().DropTable("_mcp_memories").Execute()
			return err
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			for _, table := range []string{"_mcp_memories", "_mcp_memory_edges", "_mcp_memory_versions", "_mcp_activity"} {
				if !txApp.HasTable(table) {
					return true, nil
				}
			}
			return false, nil
		},
	})
}
