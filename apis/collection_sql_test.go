package apis_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
)

const superuserAuthToken = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY"

func TestCollectionsRegisterSQLTables(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:            "guest request",
			Method:          http.MethodPost,
			URL:             "/api/collections/sql/tables",
			Body:            strings.NewReader(`{"tables":["legacy_projects"]}`),
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "registers external table",
			Method: http.MethodPost,
			URL:    "/api/collections/sql/tables",
			Body:   strings.NewReader(`{"tables":["legacy_projects"]}`),
			Headers: map[string]string{
				"Authorization": superuserAuthToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				_, err := app.NonconcurrentDB().NewQuery(`
					CREATE TABLE legacy_projects (
						id TEXT PRIMARY KEY,
						title TEXT NOT NULL
					);
				`).Execute()
				if err != nil {
					t.Fatalf("failed to create legacy_projects table: %v", err)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"name":"legacy_projects"`,
				`"externalTable":true`,
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				collection, err := app.FindCollectionByNameOrId("legacy_projects")
				if err != nil {
					t.Fatalf("failed to load collection: %v", err)
				}
				if collection == nil {
					t.Fatalf("expected collection to be created")
				}
				if !collection.ExternalTable || !collection.SkipTableSync || !collection.SkipDefaultFields {
					t.Fatalf("expected external table flags to be set")
				}
				if collection.Fields.GetByName(core.FieldNameCreated) == nil ||
					collection.Fields.GetByName(core.FieldNameUpdated) == nil ||
					collection.Fields.GetByName(core.FieldNameCreatedBy) == nil ||
					collection.Fields.GetByName(core.FieldNameUpdatedBy) == nil {
					t.Fatalf("expected audit fields to be auto-added")
				}
				if len(collection.Fields) != 6 {
					t.Fatalf("expected 6 fields (id + title + audit fields), got %d", len(collection.Fields))
				}
				if collection.UpdateRule == nil || *collection.UpdateRule == "" {
					t.Fatalf("expected update rule to be set")
				}
				if collection.DeleteRule == nil || *collection.DeleteRule == "" {
					t.Fatalf("expected delete rule to be set")
				}

				cols, err := app.TableColumns("legacy_projects")
				if err != nil {
					t.Fatalf("failed to inspect table columns: %v", err)
				}
				colMap := map[string]struct{}{}
				for _, col := range cols {
					colMap[strings.ToLower(col)] = struct{}{}
				}
				for _, name := range []string{
					core.FieldNameId,
					"title",
					core.FieldNameCreated,
					core.FieldNameUpdated,
					core.FieldNameCreatedBy,
					core.FieldNameUpdatedBy,
				} {
					if _, ok := colMap[strings.ToLower(name)]; !ok {
						t.Fatalf("expected %s column to exist on the table", name)
					}
				}
			},
		},
		{
			Name:   "rejects invalid id definition",
			Method: http.MethodPost,
			URL:    "/api/collections/sql/tables",
			Body:   strings.NewReader(`{"tables":["invalid_projects"]}`),
			Headers: map[string]string{
				"Authorization": superuserAuthToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				_, err := app.NonconcurrentDB().NewQuery(`
					CREATE TABLE invalid_projects (
						id INTEGER PRIMARY KEY,
						title TEXT NOT NULL
					);
				`).Execute()
				if err != nil {
					t.Fatalf("failed to create invalid_projects table: %v", err)
				}
			},
			ExpectedStatus: 400,
			ExpectedContent: []string{
				`"message":"Failed to register SQL tables: table invalid_projects id column must be a TEXT primary key"`,
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				if c, _ := app.FindCollectionByNameOrId("invalid_projects"); c != nil {
					t.Fatalf("expected no collection to be created for invalid table")
				}
			},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestCollectionsImportSQLTables(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:   "imports table and skips existing collection",
			Method: http.MethodPost,
			URL:    "/api/collections/sql/import",
			Body: strings.NewReader(`{
				"tables": [
					{
						"name": "imported_orders",
						"sql": "CREATE TABLE IF NOT EXISTS imported_orders (id TEXT PRIMARY KEY, total NUMERIC NOT NULL)"
					},
					{
						"name": "existing_external",
						"sql": "CREATE TABLE IF NOT EXISTS existing_external (id TEXT PRIMARY KEY, note TEXT)"
					}
				]
			}`),
			Headers: map[string]string{
				"Authorization": superuserAuthToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				existing := core.NewBaseCollection("existing_external")
				if err := app.Save(existing); err != nil {
					t.Fatalf("failed to seed existing collection: %v", err)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"created":[{`,
				`"name":"imported_orders"`,
				`"externalTable":true`,
				`"skipped":["existing_external"]`,
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				imported, err := app.FindCollectionByNameOrId("imported_orders")
				if err != nil || imported == nil {
					t.Fatalf("expected imported_orders collection to be created, err: %v", err)
				}
				if !imported.ExternalTable {
					t.Fatalf("expected imported_orders to be marked as external")
				}
				if imported.Fields.GetByName(core.FieldNameCreated) == nil ||
					imported.Fields.GetByName(core.FieldNameUpdated) == nil ||
					imported.Fields.GetByName(core.FieldNameCreatedBy) == nil ||
					imported.Fields.GetByName(core.FieldNameUpdatedBy) == nil {
					t.Fatalf("expected audit fields to be auto-added for imported_orders")
				}
				if imported.UpdateRule == nil || *imported.UpdateRule == "" {
					t.Fatalf("expected imported_orders update rule to be set")
				}
				if imported.DeleteRule == nil || *imported.DeleteRule == "" {
					t.Fatalf("expected imported_orders delete rule to be set")
				}
				if len(imported.Fields) != 6 {
					t.Fatalf("expected 6 fields (id + total + audit fields), got %d", len(imported.Fields))
				}

				existing, err := app.FindCollectionByNameOrId("existing_external")
				if err != nil || existing == nil {
					t.Fatalf("expected existing_external collection to remain, err: %v", err)
				}

				cols, err := app.TableColumns("imported_orders")
				if err != nil {
					t.Fatalf("failed to inspect imported_orders columns: %v", err)
				}
				colMap := map[string]struct{}{}
				for _, col := range cols {
					colMap[strings.ToLower(col)] = struct{}{}
				}
				for _, name := range []string{
					core.FieldNameId,
					"total",
					core.FieldNameCreated,
					core.FieldNameUpdated,
					core.FieldNameCreatedBy,
					core.FieldNameUpdatedBy,
				} {
					if _, ok := colMap[strings.ToLower(name)]; !ok {
						t.Fatalf("expected %s column to exist on imported_orders table", name)
					}
				}
			},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}
