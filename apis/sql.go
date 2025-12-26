package apis

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
)

func bindSQLApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/sql").Bind(RequireSuperuserAuth())
	sub.POST("/execute", sqlExecute)
}

type sqlExecuteRequest struct {
	Query string `json:"query" form:"query"`
}

type sqlExecuteResponse struct {
	Columns      []string   `json:"columns,omitempty"`
	Rows         [][]string `json:"rows,omitempty"`
	RowsAffected *int       `json:"rowsAffected,omitempty"`
}

func sqlExecute(e *core.RequestEvent) error {
	payload := new(sqlExecuteRequest)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to load the submitted data due to invalid formatting.", err)
	}

	query := strings.TrimSpace(payload.Query)
	if query == "" {
		return e.BadRequestError("Query is required.", nil)
	}

	result, err := executeSQLStatement(e.Request.Context(), e.App.DB(), query)
	if err != nil {
		return e.BadRequestError("Failed to execute SQL statement.", err)
	}

	return e.JSON(http.StatusOK, result)
}

func executeSQLStatement(ctx context.Context, builder dbx.Builder, query string) (*sqlExecuteResponse, error) {
	if shouldQueryReturnRows(query) {
		rows, err := builder.NewQuery(query).WithContext(ctx).Rows()
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		columns, err := rows.Columns()
		if err != nil {
			return nil, err
		}

		resultRows := make([][]string, 0)
		for rows.Next() {
			values, err := scanSQLRowValues(rows, len(columns))
			if err != nil {
				return nil, err
			}
			resultRows = append(resultRows, values)
		}

		if err := rows.Err(); err != nil {
			return nil, err
		}

		if len(columns) == 0 {
			columns = nil
		}
		if len(resultRows) == 0 {
			resultRows = nil
		}

		return &sqlExecuteResponse{
			Columns: columns,
			Rows:    resultRows,
		}, nil
	}

	result, err := builder.NewQuery(query).WithContext(ctx).Execute()
	if err != nil {
		return nil, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	affectedInt := int(affected)

	return &sqlExecuteResponse{
		Columns:      []string{"rows_affected"},
		Rows:         [][]string{{strconv.FormatInt(affected, 10)}},
		RowsAffected: &affectedInt,
	}, nil
}

func shouldQueryReturnRows(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}

	upper := strings.ToUpper(trimmed)
	if strings.Contains(upper, "RETURNING") {
		return true
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}

	first := strings.TrimLeft(fields[0], "(")
	switch strings.ToUpper(first) {
	case "SELECT", "WITH", "SHOW", "PRAGMA", "EXPLAIN", "TABLE", "VALUES", "DESCRIBE":
		return true
	default:
		return false
	}
}
