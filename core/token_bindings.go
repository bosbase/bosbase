package core

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/tools/hook"
	"github.com/bosbase/bosbase-enterprise/tools/security"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

const (
	// TokenBindingsTableName stores the database table name used for custom token bindings.
	TokenBindingsTableName      = "_token_bindings"
	tokenBindingsSchemaStoreKey = "__pb_token_bindings_schema__"
)

var legacyTokenBindingsTableNames = []string{"_tokenBindings", "tokenBindings"}

// TokenBinding represents a single token binding row.
type TokenBinding struct {
	Id            string         `db:"id"`
	CollectionRef string         `db:"collectionRef"`
	RecordRef     string         `db:"recordRef"`
	TokenHash     string         `db:"tokenHash"`
	Created       types.DateTime `db:"created"`
	Updated       types.DateTime `db:"updated"`
}

// BindCustomToken creates or updates a token binding for the provided auth record.
//
// The token value is stored as a hash and never persisted in plain text.
func (app *BaseApp) BindCustomToken(authRecord *Record, token string) error {
	if authRecord == nil || !authRecord.Collection().IsAuth() {
		return errors.New("missing or invalid auth record")
	}
	if token == "" {
		return errors.New("missing token")
	}

	if err := ensureTokenBindingsSchema(app); err != nil {
		return err
	}

	return app.saveTokenBinding(authRecord.Collection().Id, authRecord.Id, hashToken(token))
}

// UnbindCustomToken removes a token binding from the provided auth record.
func (app *BaseApp) UnbindCustomToken(authRecord *Record, token string) error {
	if authRecord == nil || !authRecord.Collection().IsAuth() {
		return errors.New("missing or invalid auth record")
	}
	if token == "" {
		return errors.New("missing token")
	}

	if !tokenBindingsTableExists(app) {
		return nil
	}

	if err := ensureTokenBindingsSchema(app); err != nil {
		return err
	}

	return app.deleteTokenBinding(authRecord.Collection().Id, authRecord.Id, hashToken(token))
}

// FindAuthRecordByCustomToken returns the auth record associated with the provided token binding.
func (app *BaseApp) FindAuthRecordByCustomToken(collectionModelOrIdentifier any, token string) (*Record, error) {
	collection, err := getCollectionByModelOrIdentifier(app, collectionModelOrIdentifier)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch auth collection: %w", err)
	}
	if !collection.IsAuth() {
		return nil, fmt.Errorf("%q is not an auth collection", collection.Name)
	}
	if token == "" {
		return nil, errors.New("missing token")
	}

	if !tokenBindingsTableExists(app) {
		return nil, sql.ErrNoRows
	}

	if err := ensureTokenBindingsSchema(app); err != nil {
		return nil, err
	}

	recordId, err := app.findTokenBindingRecordId(collection.Id, hashToken(token))
	if err != nil {
		return nil, err
	}

	return app.FindRecordById(collection.Id, recordId)
}

// DeleteAllTokenBindingsByRecord removes all bindings associated to the provided auth record.
func (app *BaseApp) DeleteAllTokenBindingsByRecord(authRecord *Record) error {
	if authRecord == nil || !authRecord.Collection().IsAuth() {
		return errors.New("missing or invalid auth record")
	}

	if !tokenBindingsTableExists(app) {
		return nil
	}

	if err := ensureTokenBindingsSchema(app); err != nil {
		return err
	}

	_, err := app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}}
		WHERE [[collectionRef]] = {:collection} AND [[recordRef]] = {:record}
	`, TokenBindingsTableName)).Bind(dbx.Params{
		"collection": authRecord.Collection().Id,
		"record":     authRecord.Id,
	}).Execute()

	return err
}

// DeleteAllTokenBindingsByCollection removes all bindings associated to the provided auth collection.
func (app *BaseApp) DeleteAllTokenBindingsByCollection(collection *Collection) error {
	if collection == nil || !collection.IsAuth() {
		return errors.New("missing or invalid auth collection")
	}

	if !tokenBindingsTableExists(app) {
		return nil
	}

	if err := ensureTokenBindingsSchema(app); err != nil {
		return err
	}

	_, err := app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}}
		WHERE [[collectionRef]] = {:collection}
	`, TokenBindingsTableName)).Bind(dbx.Params{
		"collection": collection.Id,
	}).Execute()

	return err
}

func (app *BaseApp) saveTokenBinding(collectionId, recordId, tokenHash string) error {
	_, err := app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} ([[collectionRef]], [[recordRef]], [[tokenHash]])
		VALUES ({:collection}, {:record}, {:token})
		ON CONFLICT ([[collectionRef]], [[tokenHash]])
		DO UPDATE SET [[recordRef]] = EXCLUDED.[[recordRef]], [[updated]] = CURRENT_TIMESTAMP
	`, TokenBindingsTableName)).Bind(dbx.Params{
		"collection": collectionId,
		"record":     recordId,
		"token":      tokenHash,
	}).Execute()

	return err
}

func (app *BaseApp) deleteTokenBinding(collectionId, recordId, tokenHash string) error {
	_, err := app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}}
		WHERE [[collectionRef]] = {:collection} AND [[recordRef]] = {:record} AND [[tokenHash]] = {:token}
	`, TokenBindingsTableName)).Bind(dbx.Params{
		"collection": collectionId,
		"record":     recordId,
		"token":      tokenHash,
	}).Execute()

	return err
}

func (app *BaseApp) findTokenBindingRecordId(collectionId, tokenHash string) (string, error) {
	var recordId string

	err := app.ConcurrentDB().NewQuery(fmt.Sprintf(`
		SELECT [[recordRef]]
		FROM {{%s}}
		WHERE [[collectionRef]] = {:collection} AND [[tokenHash]] = {:token}
		LIMIT 1
	`, TokenBindingsTableName)).Bind(dbx.Params{
		"collection": collectionId,
		"token":      tokenHash,
	}).Row(&recordId)

	return recordId, err
}

func ensureTokenBindingsSchema(app App) error {
	once, _ := app.Store().GetOrSet(tokenBindingsSchemaStoreKey, func() any {
		return &sync.Once{}
	}).(*sync.Once)

	if once == nil {
		return errors.New("failed to initialize token bindings schema guard")
	}

	var ensureErr error

	once.Do(func() {
		ensureErr = setupTokenBindingsSchema(app)
		if ensureErr != nil {
			app.Store().Remove(tokenBindingsSchemaStoreKey)
		}
	})

	return ensureErr
}

func setupTokenBindingsSchema(app App) error {
	if err := renameLegacyTokenBindingsTable(app); err != nil {
		return err
	}

	driver := BuilderDriverName(app.NonconcurrentDB())
	timestampCreated := TimestampColumnDefinition(driver, "created")
	timestampUpdated := TimestampColumnDefinition(driver, "updated")

	tableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS {{%s}} (
			[[id]]            TEXT PRIMARY KEY DEFAULT %s,
			[[collectionRef]] TEXT NOT NULL,
			[[recordRef]]     TEXT NOT NULL,
			[[tokenHash]]     TEXT NOT NULL,
			%s,
			%s
		);
	`, TokenBindingsTableName, RandomIDExpr(driver), timestampCreated, timestampUpdated)

	uniqueTokenIndexSQL := fmt.Sprintf(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx__token_bindings_token ON {{%s}} ([[collectionRef]], [[tokenHash]]);
	`, TokenBindingsTableName)

	recordIndexSQL := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx__token_bindings_record ON {{%s}} ([[collectionRef]], [[recordRef]]);
	`, TokenBindingsTableName)

	for _, stmt := range []string{tableSQL, uniqueTokenIndexSQL, recordIndexSQL} {
		if _, err := app.NonconcurrentDB().NewQuery(stmt).Execute(); err != nil {
			return err
		}
	}

	return nil
}

func renameLegacyTokenBindingsTable(app App) error {
	if app.HasTable(TokenBindingsTableName) {
		return nil
	}

	for _, name := range legacyTokenBindingsTableNames {
		if app.HasTable(name) {
			_, err := app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
				ALTER TABLE IF EXISTS {{%s}} RENAME TO {{%s}}
			`, name, TokenBindingsTableName)).Execute()
			return err
		}
	}

	return nil
}

func tokenBindingsTableExists(app App) bool {
	if app.HasTable(TokenBindingsTableName) {
		return true
	}

	for _, name := range legacyTokenBindingsTableNames {
		if app.HasTable(name) {
			return true
		}
	}

	return false
}

func hashToken(token string) string {
	return security.SHA256(token)
}

func (app *BaseApp) registerTokenBindingsHooks() {
	app.OnRecordAfterDeleteSuccess().Bind(&hook.Handler[*RecordEvent]{
		Func: func(e *RecordEvent) error {
			if e.Record != nil && e.Record.Collection().IsAuth() {
				if err := e.App.DeleteAllTokenBindingsByRecord(e.Record); err != nil {
					e.App.Logger().Warn(
						"Failed to delete token bindings for record",
						"error", err,
						"recordId", e.Record.Id,
						"collectionId", e.Record.Collection().Id,
					)
				}
			}

			return e.Next()
		},
		Priority: 99,
	})

	app.OnCollectionAfterDeleteSuccess().Bind(&hook.Handler[*CollectionEvent]{
		Func: func(e *CollectionEvent) error {
			if e.Collection != nil && e.Collection.IsAuth() {
				if err := e.App.DeleteAllTokenBindingsByCollection(e.Collection); err != nil {
					e.App.Logger().Warn(
						"Failed to delete token bindings for collection",
						"error", err,
						"collectionId", e.Collection.Id,
						"collectionName", e.Collection.Name,
					)
				}
			}

			return e.Next()
		},
		Priority: 99,
	})
}
