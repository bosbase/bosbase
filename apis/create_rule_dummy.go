package apis

import (
	"sort"
	"strings"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/inflector"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

// buildDummyParamsAndSelects prepares typed params and select expressions for the
// temporary create-rule CTE so Postgres can infer correct parameter types.
func buildDummyParamsAndSelects(collection *core.Collection, dummyExport map[string]any) (dbx.Params, []string) {
	keys := make([]string, 0, len(dummyExport))
	for k := range dummyExport {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	params := make(dbx.Params, len(dummyExport))
	selects := make([]string, 0, len(dummyExport))

	for _, key := range keys {
		colName := inflector.Columnify(key)
		param := "__pb_create__" + colName
		params[param] = normalizeDummyValue(collection.Fields.GetByName(key), dummyExport[key])

		cast := dummyParamCast(collection.Fields.GetByName(key))
		selects = append(selects, "{:"+param+"}"+cast+" AS [["+colName+"]]")
	}

	return params, selects
}

func dummyParamCast(field core.Field) string {
	switch f := field.(type) {
	case *core.NumberField:
		return "::NUMERIC"
	case *core.BoolField:
		return "::BOOLEAN"
	case *core.AutodateField, *core.DateField:
		return "::TIMESTAMPTZ"
	case *core.JSONField:
		return "::JSONB"
	case *core.GeoPointField:
		return "::JSONB"
	case *core.SelectField:
		if f.IsMultiple() {
			return "::JSONB"
		}
	case *core.RelationField:
		if f.IsMultiple() {
			return "::JSONB"
		}
	}

	return ""
}

// normalizeDummyValue ensures temporary create-rule params resemble real values.
func normalizeDummyValue(field core.Field, value any) any {
	switch f := field.(type) {
	case *core.AutodateField, *core.DateField:
		return normalizeDummyDateTime(value)
	case *core.NumberField:
		// rely on prepared statement typing; nothing to change
		return value
	case *core.BoolField:
		return value
	case *core.JSONField, *core.GeoPointField:
		return value
	case *core.SelectField:
		if f.IsMultiple() {
			return value
		}
		return value
	case *core.RelationField:
		return value
	default:
		return value
	}
}

func normalizeDummyDateTime(value any) any {
	switch v := value.(type) {
	case types.DateTime:
		if v.IsZero() {
			return types.NowDateTime()
		}
		return v
	case time.Time:
		if v.IsZero() {
			return time.Now()
		}
		return v
	case string:
		if strings.TrimSpace(v) == "" {
			return types.NowDateTime()
		}
		if parsed, err := types.ParseDateTime(v); err == nil {
			return parsed
		}
		return v
	default:
		return value
	}
}
