package apis

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bosbase/bosbase-enterprise/core"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type sqlTableDefinition struct {
	Name string `json:"name" form:"name"`
	SQL  string `json:"sql" form:"sql"`
}

type sqlTableNamesRequest struct {
	Tables []string `json:"tables" form:"tables"`
}

type sqlTablesRequest struct {
	Tables []sqlTableDefinition `json:"tables" form:"tables"`
}

func collectionsRegisterSQLTables(e *core.RequestEvent) error {
	payload := new(sqlTableNamesRequest)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to load the submitted data due to invalid formatting.", err)
	}

	if len(payload.Tables) == 0 {
		return e.BadRequestError("At least one table name must be provided.", nil)
	}

	created := make([]*core.Collection, 0, len(payload.Tables))

	err := e.App.RunInTransaction(func(txApp core.App) error {
		seen := map[string]struct{}{}

		for i, rawName := range payload.Tables {
			tableName, err := normalizeSqlTableName(rawName, i)
			if err != nil {
				return err
			}

			key := strings.ToLower(tableName)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			if existing, _ := txApp.FindCollectionByNameOrId(tableName); existing != nil {
				continue
			}

			if err := ensureDefaultAuditColumns(txApp, tableName); err != nil {
				return err
			}

			collection, err := registerCollectionFromTable(txApp, tableName)
			if err != nil {
				return err
			}

			created = append(created, collection)
		}

		return nil
	})
	if err != nil {
		return firstApiError(err, e.BadRequestError("Failed to register SQL tables: "+err.Error(), err))
	}

	return e.JSON(http.StatusOK, created)
}

func collectionsImportFromSQLTables(e *core.RequestEvent) error {
	payload := new(sqlTablesRequest)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to load the submitted data due to invalid formatting.", err)
	}

	if len(payload.Tables) == 0 {
		return e.BadRequestError("At least one table definition must be provided.", nil)
	}

	created := make([]*core.Collection, 0, len(payload.Tables))
	skipped := make([]string, 0)

	err := e.App.RunInTransaction(func(txApp core.App) error {
		seen := map[string]struct{}{}

		for i, tbl := range payload.Tables {
			tableName, err := normalizeSqlTableName(tbl.Name, i)
			if err != nil {
				return err
			}

			key := strings.ToLower(tableName)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			if existing, _ := txApp.FindCollectionByNameOrId(tableName); existing != nil {
				skipped = append(skipped, tableName)
				continue
			}

			if sql := strings.TrimSpace(tbl.SQL); sql != "" {
				if _, err := txApp.NonconcurrentDB().NewQuery(sql).Execute(); err != nil {
					return fmt.Errorf("failed to execute SQL for %s: %w", tableName, err)
				}
			}

			if err := ensureDefaultAuditColumns(txApp, tableName); err != nil {
				return err
			}

			collection, err := registerCollectionFromTable(txApp, tableName)
			if err != nil {
				return err
			}

			created = append(created, collection)
		}
		return nil
	})
	if err != nil {
		return firstApiError(err, e.BadRequestError("Failed to import SQL tables: "+err.Error(), err))
	}

	return e.JSON(http.StatusOK, map[string]any{
		"created": created,
		"skipped": skipped,
	})
}

func normalizeSqlTableName(raw string, idx int) (string, error) {
	tableName := strings.TrimSpace(raw)
	if tableName == "" {
		return "", validation.Errors{
			"tables": validation.Errors{
				strconv.Itoa(idx): validation.NewError("validation_invalid_table_name", "Table name cannot be empty."),
			},
		}
	}

	if strings.HasPrefix(tableName, "_") {
		return "", validation.Errors{
			"tables": validation.Errors{
				strconv.Itoa(idx): validation.NewError("validation_reserved_table_name", "System tables cannot be registered as external collections."),
			},
		}
	}

	return tableName, nil
}

// ensureDefaultAuditColumns adds the standard audit columns to the provided table
// if they are missing so that the default API rules remain valid.
func ensureDefaultAuditColumns(txApp core.App, tableName string) error {
	columns, err := txApp.TableColumns(tableName)
	if err != nil {
		return fmt.Errorf("failed to inspect %s columns: %w", tableName, err)
	}

	existing := make(map[string]struct{}, len(columns))
	for _, col := range columns {
		existing[strings.ToLower(col)] = struct{}{}
	}

	type defaultColumn struct {
		name  string
		field core.Field
	}

	defaults := []defaultColumn{
		{
			name: core.FieldNameCreated,
			field: &core.AutodateField{
				Name:     core.FieldNameCreated,
				System:   true,
				OnCreate: true,
			},
		},
		{
			name: core.FieldNameUpdated,
			field: &core.AutodateField{
				Name:     core.FieldNameUpdated,
				System:   true,
				OnCreate: true,
				OnUpdate: true,
			},
		},
		{
			name: core.FieldNameCreatedBy,
			field: &core.TextField{
				Name:   core.FieldNameCreatedBy,
				System: true,
			},
		},
		{
			name: core.FieldNameUpdatedBy,
			field: &core.TextField{
				Name:   core.FieldNameUpdatedBy,
				System: true,
			},
		},
	}

	for _, col := range defaults {
		if _, ok := existing[strings.ToLower(col.name)]; ok {
			continue
		}

		if _, err := txApp.DB().AddColumn(tableName, col.name, col.field.ColumnType(txApp)).Execute(); err != nil {
			return fmt.Errorf("failed to add %s column to %s: %w", col.name, tableName, err)
		}
	}

	return nil
}

func registerCollectionFromTable(txApp core.App, tableName string) (*core.Collection, error) {
	collection, err := core.CollectionFromTableSchema(txApp, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to build collection for %s: %w", tableName, err)
	}

	if err := txApp.Save(collection); err != nil {
		return nil, fmt.Errorf("failed to save collection for %s: %w", tableName, err)
	}

	return collection, nil
}
