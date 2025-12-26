package core

import (
	"fmt"
	"strings"

	"dbx"
)

// BuilderDriverName returns the driver name associated with the provided builder.
func BuilderDriverName(builder dbx.Builder) string {
	if builder == nil {
		return ""
	}

	if tx, ok := builder.(*dbx.Tx); ok {
		return BuilderDriverName(tx.Builder)
	}

	if dbGetter, ok := builder.(interface{ DB() *dbx.DB }); ok {
		if db := dbGetter.DB(); db != nil {
			return db.DriverName()
		}
	}

	if namer, ok := builder.(interface{ DriverName() string }); ok {
		return namer.DriverName()
	}

	return ""
}

// IsPostgresDriver checks whether the provided driver string refers to Postgres.
func IsPostgresDriver(driver string) bool {
	return strings.EqualFold(driver, "postgres") || strings.EqualFold(driver, "pgx")
}

// RandomIDExpr returns a SQL expression for generating random record IDs.
// This project uses PostgreSQL exclusively.
func RandomIDExpr(driver string) string {
	return "('r'||substr(md5(random()::text || clock_timestamp()::text), 1, 14))"
}

// JSONColumnType returns the appropriate JSON column type.
// This project uses PostgreSQL exclusively, so it always returns JSONB.
func JSONColumnType(driver string) string {
	return "JSONB"
}

// JSONDefaultLiteral returns the PostgreSQL JSON default literal for the provided JSON value.
func JSONDefaultLiteral(driver string, literal string) string {
	return fmt.Sprintf("'%s'::jsonb", literal)
}

// TimestampColumnDefinition returns a PostgreSQL column definition for timestamp columns.
func TimestampColumnDefinition(driver string, column string) string {
	return "[[" + column + "]] TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL"
}

// LogsHourGroupingExpr returns a PostgreSQL SQL expression used for grouping logs by hour.
func LogsHourGroupingExpr(driver string, column string) string {
	return "date_trunc('hour', [[" + column + "]])"
}

// LogsHourGroupingIndexExpr returns a PostgreSQL SQL expression suitable for indexes
// that matches the logic used in LogsHourGroupingExpr.
func LogsHourGroupingIndexExpr(driver string, column string) string {
	return "(date_trunc('hour', [[" + column + "]]))"
}

// CaseInsensitiveEqExpr returns a PostgreSQL expression that compares a column to a value case-insensitively.
func CaseInsensitiveEqExpr(column string, paramPlaceholder string, driver string) string {
	return fmt.Sprintf("LOWER([[%s]]) = LOWER(%s)", column, paramPlaceholder)
}
