package migrations

import (
	"fmt"

	"github.com/bosbase/bosbase-enterprise/core"
)

// User configurable integrations:
//   - _storage_providers: S3 compatible object storages (AWS S3, Cloudflare R2, GCS, MinIO...)
//   - _storage_objects: history of the objects uploaded to them
//   - _llm_channels: third party LLM providers (OpenAI compatible / Anthropic) used by the LLM relay
//   - _llm_requests: LLM relay request log (usage)
//
// An empty owner marks a shared config managed by superusers and usable by everyone.
// Secrets are stored encrypted when an encryption key is configured.
func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			driver := core.BuilderDriverName(txApp.NonconcurrentDB())
			idExpr := core.RandomIDExpr(driver)
			created := core.TimestampColumnDefinition(driver, "created")
			updated := core.TimestampColumnDefinition(driver, "updated")

			statements := []string{
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_storage_providers}} (
					[[id]]              TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]           TEXT DEFAULT '' NOT NULL,
					[[ownerCollection]] TEXT DEFAULT '' NOT NULL,
					[[name]]            TEXT NOT NULL,
					[[type]]            TEXT DEFAULT 's3' NOT NULL,
					[[endpoint]]        TEXT DEFAULT '' NOT NULL,
					[[region]]          TEXT DEFAULT '' NOT NULL,
					[[bucket]]          TEXT NOT NULL,
					[[accessKey]]       TEXT DEFAULT '' NOT NULL,
					[[secretKey]]       TEXT DEFAULT '' NOT NULL,
					[[forcePathStyle]]  BOOLEAN DEFAULT FALSE NOT NULL,
					[[publicUrl]]       TEXT DEFAULT '' NOT NULL,
					[[prefix]]          TEXT DEFAULT '' NOT NULL,
					[[isDefault]]       BOOLEAN DEFAULT FALSE NOT NULL,
					[[enabled]]         BOOLEAN DEFAULT TRUE NOT NULL,
					%s,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__storage_providers_name ON {{_storage_providers}} ([[owner]], LOWER([[name]]));`,
					idExpr, created, updated),
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_storage_objects}} (
					[[id]]              TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]           TEXT NOT NULL,
					[[ownerCollection]] TEXT DEFAULT '' NOT NULL,
					[[provider]]        TEXT DEFAULT '' NOT NULL,
					[[providerName]]    TEXT DEFAULT '' NOT NULL,
					[[bucket]]          TEXT DEFAULT '' NOT NULL,
					[[key]]             TEXT NOT NULL,
					[[filename]]        TEXT DEFAULT '' NOT NULL,
					[[contentType]]     TEXT DEFAULT '' NOT NULL,
					[[size]]            BIGINT DEFAULT 0 NOT NULL,
					[[etag]]            TEXT DEFAULT '' NOT NULL,
					[[url]]             TEXT DEFAULT '' NOT NULL,
					[[status]]          TEXT DEFAULT 'uploaded' NOT NULL,
					[[source]]          TEXT DEFAULT '' NOT NULL,
					[[metadata]]        JSONB DEFAULT '{}'::jsonb NOT NULL,
					[[error]]           TEXT DEFAULT '' NOT NULL,
					[[deletedAt]]       TIMESTAMPTZ NULL,
					%s,
					%s
				);
				CREATE INDEX IF NOT EXISTS idx__storage_objects_owner ON {{_storage_objects}} ([[owner]], [[created]] DESC);
				CREATE INDEX IF NOT EXISTS idx__storage_objects_provider ON {{_storage_objects}} ([[provider]], [[key]]);`,
					idExpr, created, updated),
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_llm_channels}} (
					[[id]]              TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]           TEXT DEFAULT '' NOT NULL,
					[[ownerCollection]] TEXT DEFAULT '' NOT NULL,
					[[name]]            TEXT NOT NULL,
					[[type]]            TEXT DEFAULT 'openai' NOT NULL,
					[[baseUrl]]         TEXT NOT NULL,
					[[apiKey]]          TEXT DEFAULT '' NOT NULL,
					[[headers]]         TEXT DEFAULT '' NOT NULL,
					[[models]]          JSONB DEFAULT '[]'::jsonb NOT NULL,
					[[defaultModel]]    TEXT DEFAULT '' NOT NULL,
					[[isDefault]]       BOOLEAN DEFAULT FALSE NOT NULL,
					[[enabled]]         BOOLEAN DEFAULT TRUE NOT NULL,
					%s,
					%s
				);
				CREATE UNIQUE INDEX IF NOT EXISTS idx__llm_channels_name ON {{_llm_channels}} ([[owner]], LOWER([[name]]));`,
					idExpr, created, updated),
				fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS {{_llm_requests}} (
					[[id]]               TEXT PRIMARY KEY DEFAULT %s NOT NULL,
					[[owner]]            TEXT NOT NULL,
					[[ownerCollection]]  TEXT DEFAULT '' NOT NULL,
					[[channel]]          TEXT DEFAULT '' NOT NULL,
					[[channelName]]      TEXT DEFAULT '' NOT NULL,
					[[model]]            TEXT DEFAULT '' NOT NULL,
					[[endpoint]]         TEXT DEFAULT '' NOT NULL,
					[[status]]           INTEGER DEFAULT 0 NOT NULL,
					[[stream]]           BOOLEAN DEFAULT FALSE NOT NULL,
					[[promptTokens]]     INTEGER DEFAULT 0 NOT NULL,
					[[completionTokens]] INTEGER DEFAULT 0 NOT NULL,
					[[totalTokens]]      INTEGER DEFAULT 0 NOT NULL,
					[[durationMs]]       INTEGER DEFAULT 0 NOT NULL,
					[[error]]            TEXT DEFAULT '' NOT NULL,
					%s
				);
				CREATE INDEX IF NOT EXISTS idx__llm_requests_owner ON {{_llm_requests}} ([[owner]], [[created]] DESC);
				CREATE INDEX IF NOT EXISTS idx__llm_requests_channel ON {{_llm_requests}} ([[channel]], [[created]] DESC);`,
					idExpr, created),
			}

			for _, sql := range statements {
				if _, err := txApp.DB().NewQuery(sql).Execute(); err != nil {
					return fmt.Errorf("integrations migration error: %w", err)
				}
			}

			return nil
		},
		Down: func(txApp core.App) error {
			for _, table := range []string{"_llm_requests", "_llm_channels", "_storage_objects", "_storage_providers"} {
				if _, err := txApp.DB().DropTable(table).Execute(); err != nil {
					return err
				}
			}
			return nil
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			for _, table := range []string{"_storage_providers", "_storage_objects", "_llm_channels", "_llm_requests"} {
				if !txApp.HasTable(table) {
					return true, nil
				}
			}
			return false, nil
		},
	})
}
