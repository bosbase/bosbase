package apis_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
)

func TestSQLExecute(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:            "guest request",
			Method:          http.MethodPost,
			URL:             "/api/sql/execute",
			Body:            strings.NewReader(`{"query":"SELECT 1"}`),
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "select returns rows",
			Method: http.MethodPost,
			URL:    "/api/sql/execute",
			Body:   strings.NewReader(`{"query":"SELECT id, note FROM sql_exec_items ORDER BY id"}`),
			Headers: map[string]string{
				"Authorization": superuserAuthToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				_, err := app.NonconcurrentDB().NewQuery(`
					CREATE TABLE sql_exec_items (
						id TEXT PRIMARY KEY,
						note TEXT
					);
				`).Execute()
				if err != nil {
					t.Fatalf("failed to create table: %v", err)
				}
				_, err = app.NonconcurrentDB().NewQuery(`
					INSERT INTO sql_exec_items (id, note) VALUES
						('item_a', 'hello'),
						('item_b', 'world');
				`).Execute()
				if err != nil {
					t.Fatalf("failed to seed data: %v", err)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"columns":["id","note"]`,
				`"rows":[["item_a","hello"],["item_b","world"]]`,
			},
		},
		{
			Name:   "update returns rows affected",
			Method: http.MethodPost,
			URL:    "/api/sql/execute",
			Body:   strings.NewReader(`{"query":"UPDATE sql_exec_updates SET note='updated via api' WHERE id='update_1'"}`),
			Headers: map[string]string{
				"Authorization": superuserAuthToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				_, err := app.NonconcurrentDB().NewQuery(`
					CREATE TABLE sql_exec_updates (
						id TEXT PRIMARY KEY,
						note TEXT
					);
				`).Execute()
				if err != nil {
					t.Fatalf("failed to create update table: %v", err)
				}
				_, err = app.NonconcurrentDB().NewQuery(`
					INSERT INTO sql_exec_updates (id, note) VALUES ('update_1', 'initial');
				`).Execute()
				if err != nil {
					t.Fatalf("failed to seed update table: %v", err)
				}
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				var note string
				if err := app.DB().NewQuery("SELECT note FROM sql_exec_updates WHERE id='update_1'").Row(&note); err != nil {
					t.Fatalf("failed to load updated row: %v", err)
				}
				if note != "updated via api" {
					t.Fatalf("expected updated note, got %q", note)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"rowsAffected":1`,
				`"columns":["rows_affected"]`,
				`"rows":[["1"]]`,
			},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}
