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
			jsonObjectDefault := core.JSONDefaultLiteral(driver, "{}")

			// Enable pgvector extension
			enableVectorSQL := `CREATE EXTENSION IF NOT EXISTS vector;`
			_, err := txApp.DB().NewQuery(enableVectorSQL).Execute()
			if err != nil {
				return fmt.Errorf("failed to enable vector extension: %w", err)
			}

			// Create vector_collections table to track vector collections metadata
			vectorCollectionsSQL := fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_vector_collections}} (
					[[id]]         TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[name]]       TEXT UNIQUE NOT NULL,
					[[dimension]]  INTEGER DEFAULT 384 NOT NULL,
					[[distance]]   TEXT DEFAULT 'cosine' NOT NULL,
					[[options]]    %s DEFAULT %s NOT NULL,
					%s,
					%s
				);
			`,
				core.RandomIDExpr(driver),
				jsonType, jsonObjectDefault,
				core.TimestampColumnDefinition(driver, "created"),
				core.TimestampColumnDefinition(driver, "updated"),
			)

			_, execErr := txApp.DB().NewQuery(vectorCollectionsSQL).Execute()
			if execErr != nil {
				return fmt.Errorf("_vector_collections exec error: %w", execErr)
			}

			return nil
		},
		Down: func(txApp core.App) error {
			// Drop vector collections table
			_, err := txApp.DB().DropTable("_vector_collections").Execute()
			if err != nil {
				return err
			}

			// Note: We don't drop the vector extension as it might be used by other tables
			return nil
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			// Reapply only if the _vector_collections table doesn't exist
			exists := txApp.HasTable("_vector_collections")
			return !exists, nil
		},
	})
}

