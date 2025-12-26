package core

import (
	"fmt"
	"strings"
)

// CollectionFromTableSchema builds a new base collection using an existing SQL table definition.
// The returned collection is marked as externally managed to prevent automatic schema sync.
func CollectionFromTableSchema(app App, tableName string) (*Collection, error) {
	info, err := app.TableInfo(tableName)
	if err != nil {
		return nil, err
	}

	fields := make([]Field, 0, len(info))

	var hasId bool
	var idRow *TableInfoRow
	pkColumns := make([]string, 0)

	for _, row := range info {
		if row.PK > 0 {
			pkColumns = append(pkColumns, row.Name)
		}

		field, err := fieldFromTableInfoRow(row)
		if err != nil {
			return nil, err
		}

		if field.GetName() == FieldNameId {
			hasId = true
			idRow = row
		}

		fields = append(fields, field)
	}

	if !hasId {
		return nil, fmt.Errorf("table %s is missing required id column", tableName)
	}

	if len(pkColumns) != 1 || !strings.EqualFold(pkColumns[0], FieldNameId) {
		return nil, fmt.Errorf("table %s must have a single primary key column named %s", tableName, FieldNameId)
	}

	if idRow != nil && !isTextColumnType(idRow.Type) {
		return nil, fmt.Errorf("table %s id column must be a TEXT primary key", tableName)
	}

	collection := NewCollection(CollectionTypeBase, tableName)
	collection.Fields = NewFieldsList(fields...)
	collection.collectionBaseOptions.ExternalTable = true
	collection.SkipDefaultFields = true
	collection.SkipTableSync = true

	return collection, nil
}

func fieldFromTableInfoRow(row *TableInfoRow) (Field, error) {
	loweredType := strings.ToLower(row.Type)

	switch row.Name {
	case FieldNameId:
		return &TextField{
			Name:       FieldNameId,
			System:     true,
			Hidden:     false,
			PrimaryKey: true,
			Required:   true,
			Pattern:    `^[\w-]+$`,
		}, nil
	case FieldNameCreated:
		return &AutodateField{
			Name:     FieldNameCreated,
			System:   true,
			Hidden:   false,
			OnCreate: true,
			OnUpdate: false,
		}, nil
	case FieldNameUpdated:
		return &AutodateField{
			Name:     FieldNameUpdated,
			System:   true,
			Hidden:   false,
			OnCreate: true,
			OnUpdate: true,
		}, nil
	case FieldNameCreatedBy:
		return &TextField{
			Name:   FieldNameCreatedBy,
			System: true,
			Hidden: false,
		}, nil
	case FieldNameUpdatedBy:
		return &TextField{
			Name:   FieldNameUpdatedBy,
			System: true,
			Hidden: false,
		}, nil
	}

	switch {
	case strings.Contains(loweredType, "bool"):
		return &BoolField{Name: row.Name}, nil
	case strings.Contains(loweredType, "int"), strings.Contains(loweredType, "serial"):
		return &NumberField{
			Name:     row.Name,
			OnlyInt:  true,
			Required: false,
		}, nil
	case strings.Contains(loweredType, "numeric"),
		strings.Contains(loweredType, "decimal"),
		strings.Contains(loweredType, "real"),
		strings.Contains(loweredType, "double"):
		return &NumberField{Name: row.Name}, nil
	case strings.Contains(loweredType, "json"):
		return &JSONField{Name: row.Name}, nil
	case strings.Contains(loweredType, "time"), strings.Contains(loweredType, "date"):
		return &DateField{Name: row.Name}, nil
	default:
		return &TextField{
			Name: row.Name,
		}, nil
	}
}

func isTextColumnType(columnType string) bool {
	lowered := strings.ToLower(columnType)

	return strings.Contains(lowered, "text") || strings.Contains(lowered, "char")
}
