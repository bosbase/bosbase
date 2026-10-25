package core

import (
	"fmt"
	"strings"

	"dbx"
)

var _ dbx.Expression = (*multiMatchSubquery)(nil)

// join defines the specification for a single SQL JOIN clause.
type join struct {
	tableName  string
	tableAlias string
	on         dbx.Expression
}

// lateral returns the join table and condition for PostgreSQL: the subquery joins
// without condition (eg. the jsonb_array_elements of a multiple relation field)
// reference the outer table, so they must be LATERAL with an ON TRUE condition.
func (j *join) lateral() (string, dbx.Expression) {
	if j.on == nil && strings.HasPrefix(strings.TrimSpace(j.tableName), "(") {
		return "LATERAL " + j.tableName, dbx.NewExp("TRUE")
	}
	return j.tableName, j.on
}

// multiMatchSubquery defines a record multi-match subquery expression.
type multiMatchSubquery struct {
	baseTableAlias  string
	fromTableName   string
	fromTableAlias  string
	valueIdentifier string
	joins           []*join
	params          dbx.Params
}

// Build converts the expression into a SQL fragment.
//
// Implements [dbx.Expression] interface.
func (m *multiMatchSubquery) Build(db *dbx.DB, params dbx.Params) string {
	if m.baseTableAlias == "" || m.fromTableName == "" || m.fromTableAlias == "" {
		return "0=1"
	}

	if params == nil {
		params = m.params
	} else {
		// merge by updating the parent params
		for k, v := range m.params {
			params[k] = v
		}
	}

	var mergedJoins strings.Builder
	for i, j := range m.joins {
		if i > 0 {
			mergedJoins.WriteString(" ")
		}
		tableName, on := j.lateral()
		mergedJoins.WriteString("LEFT JOIN ")
		mergedJoins.WriteString(db.QuoteTableName(tableName))
		mergedJoins.WriteString(" ")
		mergedJoins.WriteString(db.QuoteTableName(j.tableAlias))
		if on != nil {
			mergedJoins.WriteString(" ON ")
			mergedJoins.WriteString(on.Build(db, params))
		}
	}

	return fmt.Sprintf(
		`SELECT %s as [[multiMatchValue]] FROM %s %s %s WHERE %s = %s`,
		db.QuoteColumnName(m.valueIdentifier),
		db.QuoteTableName(m.fromTableName),
		db.QuoteTableName(m.fromTableAlias),
		mergedJoins.String(),
		db.QuoteColumnName(m.fromTableAlias+".id"),
		db.QuoteColumnName(m.baseTableAlias+".id"),
	)
}
