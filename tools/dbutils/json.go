package dbutils

import (
	"fmt"
	"strings"
)

// JSONEach returns a PostgreSQL jsonb_array_elements expression with
// fallbacks for NULL or scalar values.
func JSONEach(column string) string {
	return fmt.Sprintf(
		`(SELECT jsonb_array_elements_text(CASE WHEN [[%[1]s]] IS NULL OR [[%[1]s]]::text = 'null' THEN '[]'::jsonb WHEN jsonb_typeof([[%[1]s]]::jsonb) = 'array' THEN [[%[1]s]]::jsonb ELSE jsonb_build_array([[%[1]s]]::jsonb) END) AS value)`,
		column)
}

// JSONArrayLength returns a PostgreSQL jsonb_array_length expression with
// fallbacks for NULL or scalar values.
func JSONArrayLength(column string) string {
	return fmt.Sprintf(
		`jsonb_array_length(CASE WHEN [[%[1]s]] IS NULL OR [[%[1]s]]::text = 'null' THEN '[]'::jsonb WHEN jsonb_typeof([[%[1]s]]::jsonb) = 'array' THEN [[%[1]s]]::jsonb ELSE jsonb_build_array([[%[1]s]]::jsonb) END)`,
		column)
}

// JSONExtract returns a PostgreSQL jsonb_path_query_first expression.
func JSONExtract(column string, path string) string {
	jsonPath := "$"
	if path != "" {
		if strings.HasPrefix(path, "[") {
			jsonPath += path
		} else {
			jsonPath += "." + path
		}
	}

	return fmt.Sprintf(
		"jsonb_path_query_first(COALESCE([[%s]]::jsonb, 'null'::jsonb), '%s')",
		column,
		strings.ReplaceAll(jsonPath, "'", "''"),
	)
}
