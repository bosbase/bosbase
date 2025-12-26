package core

import (
	"fmt"
	"strconv"
	"strings"

	"dbx"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/bosbase/bosbase-enterprise/tools/dbutils"
	"github.com/bosbase/bosbase-enterprise/tools/security"
)

// SyncRecordTableSchema compares the two provided collections
// and applies the necessary related record table changes.
//
// If oldCollection is null, then only newCollection is used to create the record table.
//
// This method is automatically invoked as part of a collection create/update/delete operation.
func (app *BaseApp) SyncRecordTableSchema(newCollection *Collection, oldCollection *Collection) error {
	if newCollection.IsView() {
		return nil // nothing to sync since views don't have records table
	}

	txErr := app.RunInTransaction(func(txApp App) error {
		// create
		// -----------------------------------------------------------
		if oldCollection == nil || !app.HasTable(oldCollection.Name) {
			tableName := newCollection.Name

			fields := newCollection.Fields

			cols := make(map[string]string, len(fields))

			// add fields definition
			for _, field := range fields {
				cols[field.GetName()] = field.ColumnType(app)
			}

			// create table
			if _, err := txApp.DB().CreateTable(tableName, cols).Execute(); err != nil {
				return err
			}

			return createCollectionIndexes(txApp, newCollection)
		}

		// update
		// -----------------------------------------------------------
		oldTableName := oldCollection.Name
		newTableName := newCollection.Name
		oldFields := oldCollection.Fields
		newFields := newCollection.Fields

		needTableRename := !strings.EqualFold(oldTableName, newTableName)

		var needIndexesUpdate bool
		if needTableRename ||
			oldFields.String() != newFields.String() ||
			oldCollection.Indexes.String() != newCollection.Indexes.String() {
			needIndexesUpdate = true
		}

		if needIndexesUpdate {
			// drop old indexes (if any)
			if err := dropCollectionIndexes(txApp, oldCollection); err != nil {
				return err
			}
		}

		// check for renamed table
		if needTableRename {
			_, err := txApp.DB().RenameTable("{{"+oldTableName+"}}", "{{"+newTableName+"}}").Execute()
			if err != nil {
				return err
			}
		}

		// check for deleted columns
		for _, oldField := range oldFields {
			if f := newFields.GetById(oldField.GetId()); f != nil {
				continue // exist
			}

			_, err := txApp.DB().DropColumn(newTableName, oldField.GetName()).Execute()
			if err != nil {
				return fmt.Errorf("failed to drop column %s - %w", oldField.GetName(), err)
			}
		}

		// check for new or renamed columns
		toRename := map[string]string{}
		for _, field := range newFields {
			oldField := oldFields.GetById(field.GetId())
			// Note:
			// We are using a temporary column name when adding or renaming columns
			// to ensure that there are no name collisions in case there is
			// names switch/reuse of existing columns (eg. name, title -> title, name).
			// This way we are always doing 1 more rename operation but it provides better less ambiguous experience.

			if oldField == nil {
				tempName := field.GetName() + security.PseudorandomString(5)
				toRename[tempName] = field.GetName()

				// add
				_, err := txApp.DB().AddColumn(newTableName, tempName, field.ColumnType(txApp)).Execute()
				if err != nil {
					return fmt.Errorf("failed to add column %s - %w", field.GetName(), err)
				}
			} else if oldField.GetName() != field.GetName() {
				tempName := field.GetName() + security.PseudorandomString(5)
				toRename[tempName] = field.GetName()

				// rename
				_, err := txApp.DB().RenameColumn(newTableName, oldField.GetName(), tempName).Execute()
				if err != nil {
					return fmt.Errorf("failed to rename column %s - %w", oldField.GetName(), err)
				}
			}
		}

		// set the actual columns name
		for tempName, actualName := range toRename {
			_, err := txApp.DB().RenameColumn(newTableName, tempName, actualName).Execute()
			if err != nil {
				return err
			}
		}

		if err := normalizeSingleVsMultipleFieldChanges(txApp, newCollection, oldCollection); err != nil {
			return err
		}

		if needIndexesUpdate {
			return createCollectionIndexes(txApp, newCollection)
		}

		return nil
	})
	if txErr != nil {
		return txErr
	}
	return nil
}

func normalizeSingleVsMultipleFieldChanges(app App, newCollection *Collection, oldCollection *Collection) error {
	if newCollection.IsView() || oldCollection == nil {
		return nil // view or not an update
	}

	return app.RunInTransaction(func(txApp App) error {
		for _, newField := range newCollection.Fields {
			var err error
			// allow to continue even if there is no old field for the cases
			// when a new field is added and there are already inserted data
			var isOldMultiple bool
			if oldField := oldCollection.Fields.GetById(newField.GetId()); oldField != nil {
				if mv, ok := oldField.(MultiValuer); ok {
					isOldMultiple = mv.IsMultiple()
				}
			}

			var isNewMultiple bool
			if mv, ok := newField.(MultiValuer); ok {
				isNewMultiple = mv.IsMultiple()
			}

			if isOldMultiple == isNewMultiple {
				continue // no change
			}

			// -------------------------------------------------------
			// update the field column definition
			// -------------------------------------------------------

			originalName := newField.GetName()
			oldTempName := "_" + newField.GetName() + security.PseudorandomString(5)

			// rename temporary the original column to something else to allow inserting a new one in its place
			_, err = txApp.DB().RenameColumn(newCollection.Name, originalName, oldTempName).Execute()
			if err != nil {
				return err
			}

			// reinsert the field column with the new type
			_, err = txApp.DB().AddColumn(newCollection.Name, originalName, newField.ColumnType(txApp)).Execute()
			if err != nil {
				return err
			}

			var copyQuery *dbx.Query

			switch {
			case !isOldMultiple && isNewMultiple:
				// This project uses PostgreSQL exclusively
				copyQuery = txApp.DB().NewQuery(fmt.Sprintf(
					`UPDATE {{%s}} SET [[%s]] = (
                        CASE
                            WHEN COALESCE([[%s]]::text, '') = ''
                                THEN '[]'::jsonb
                            ELSE jsonb_build_array(to_jsonb([[%s]]::text))
                        END
                    )`,
					newCollection.Name,
					originalName,
					oldTempName,
					oldTempName,
				))
			case isOldMultiple && !isNewMultiple:
				// This project uses PostgreSQL exclusively
				copyQuery = txApp.DB().NewQuery(fmt.Sprintf(
					`UPDATE {{%s}} SET [[%s]] = (
                        CASE
                            WHEN [[%s]] IS NULL OR jsonb_typeof([[%s]]::jsonb) <> 'array' OR jsonb_array_length([[%s]]::jsonb) = 0
                                THEN ''
                            ELSE ([[%s]]::jsonb ->> (jsonb_array_length([[%s]]::jsonb) - 1))
                        END
                    )`,
					newCollection.Name,
					originalName,
					oldTempName,
					oldTempName,
					oldTempName,
					oldTempName,
					oldTempName,
				))
			}

			if copyQuery != nil {
				if _, err := copyQuery.Execute(); err != nil {
					return err
				}
			}

			// drop the original column
			_, err = txApp.DB().DropColumn(newCollection.Name, oldTempName).Execute()
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func dropCollectionIndexes(app App, collection *Collection) error {
	if collection.IsView() {
		return nil // views don't have indexes
	}

	return app.RunInTransaction(func(txApp App) error {
		for _, raw := range collection.Indexes {
			parsed := dbutils.ParseIndex(raw)

			if !parsed.IsValid() {
				continue
			}

			_, err := txApp.DB().NewQuery(fmt.Sprintf("DROP INDEX IF EXISTS [[%s]]", parsed.IndexName)).Execute()
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func createCollectionIndexes(app App, collection *Collection) error {
	if collection.IsView() {
		return nil // views don't have indexes
	}

	return app.RunInTransaction(func(txApp App) error {
		// upsert new indexes
		//
		// note: we are returning validation errors because the indexes cannot be
		//       easily validated in a form, aka. before persisting the related
		//       collection record table changes
		errs := validation.Errors{}
		driver := BuilderDriverName(txApp.NonconcurrentDB())

		for i, idx := range collection.Indexes {
			parsed := dbutils.ParseIndex(idx)

			// ensure that the index is always for the current collection
			parsed.TableName = collection.Name

			if !parsed.IsValid() {
				errs[strconv.Itoa(i)] = validation.NewError(
					"validation_invalid_index_expression",
					"Invalid CREATE INDEX expression.",
				)
				continue
			}

			if _, err := txApp.DB().NewQuery(parsed.BuildForDriver(driver)).Execute(); err != nil {
				errs[strconv.Itoa(i)] = validation.NewError(
					"validation_invalid_index_expression",
					fmt.Sprintf("Failed to create index %s - %v.", parsed.IndexName, err.Error()),
				)
				continue
			}
		}

		if len(errs) > 0 {
			return validation.Errors{"indexes": errs}
		}

		return nil
	})
}
