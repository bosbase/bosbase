package apis_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/bosbase/bosbase-enterprise/apis"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
	"github.com/bosbase/bosbase-enterprise/tools/logger"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

func TestPanicRecover(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:   "panic from route",
			Method: http.MethodGet,
			URL:    "/my/test",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					panic("123")
				})
			},
			ExpectedStatus:  500,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "panic from middleware",
			Method: http.MethodGet,
			URL:    "/my/test",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(http.StatusOK, "test")
				}).BindFunc(func(e *core.RequestEvent) error {
					panic(123)
				})
			},
			ExpectedStatus:  500,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestRequireGuestOnly(t *testing.T) {
	t.Parallel()

	beforeTestFunc := func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
		e.Router.GET("/my/test", func(e *core.RequestEvent) error {
			return e.String(200, "test123")
		}).Bind(apis.RequireGuestOnly())
	}

	scenarios := []tests.ApiScenario{
		{
			Name:   "valid regular user token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc:  beforeTestFunc,
			ExpectedStatus:  400,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid superuser auth token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY",
			},
			BeforeTestFunc:  beforeTestFunc,
			ExpectedStatus:  400,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "expired/invalid token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoxNjQwOTkxNjYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.2D3tmqPn3vc5LoqqCz8V-iCDVXo9soYiH0d32G7FQT4",
			},
			BeforeTestFunc:  beforeTestFunc,
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:            "guest",
			Method:          http.MethodGet,
			URL:             "/my/test",
			BeforeTestFunc:  beforeTestFunc,
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
			ExpectedEvents:  map[string]int{"*": 0},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestRequireAuth(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:   "guest",
			Method: http.MethodGet,
			URL:    "/my/test",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth())
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "expired token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoxNjQwOTkxNjYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.2D3tmqPn3vc5LoqqCz8V-iCDVXo9soYiH0d32G7FQT4",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth())
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "invalid token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsImV4cCI6MjUyNDYwNDQ2MSwidHlwZSI6ImZpbGUiLCJjb2xsZWN0aW9uSWQiOiJwYmNfMzE0MjYzNTgyMyJ9.Lupz541xRvrktwkrl55p5pPCF77T69ZRsohsIcb2dxc",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth())
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token with no collection restrictions",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				// regular user
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth())
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
		{
			Name:   "valid token passed as query parameter",
			Method: http.MethodGet,
			URL:    "/my/test?token=eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth())
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
		{
			Name:   "valid record static auth token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				// regular user
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6ZmFsc2V9.4IsO6YMsR19crhwl_YWzvRH8pfq2Ri4Gv2dzGyneLak",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth())
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
		{
			Name:   "valid record auth token with collection not in the restricted list",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				// superuser
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth("users", "demo1"))
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token with collection in the restricted list",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				// superuser
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireAuth("users", core.CollectionNameSuperusers))
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestActivityLoggerStoresSuccessRequestsAboveMinLevel(t *testing.T) {
	t.Parallel()

	scenario := tests.ApiScenario{
		Name:   "request logs stored above min level",
		Method: http.MethodGet,
		URL:    "/my/test",
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			app.Settings().Logs.MaxDays = 5
			app.Settings().Logs.MinLevel = int(slog.LevelError)
			if err := app.Save(app.Settings()); err != nil {
				t.Fatalf("failed to update settings: %v", err)
			}

			if _, err := app.AuxDB().NewQuery("DELETE FROM {{_logs}}").Execute(); err != nil {
				t.Fatalf("failed to cleanup logs: %v", err)
			}

			e.Router.GET("/my/test", func(e *core.RequestEvent) error {
				return e.String(http.StatusOK, "ok")
			})
		},
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{"ok"},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
			handler, ok := app.Logger().Handler().(*logger.BatchHandler)
			if !ok {
				t.Fatalf("expected BatchHandler, got %T", app.Logger().Handler())
			}

			if err := handler.WriteAll(context.Background()); err != nil {
				t.Fatalf("failed to flush logs: %v", err)
			}

			type logEntry struct {
				Level   int                `db:"level"`
				Message string             `db:"message"`
				Data    types.JSONMap[any] `db:"data"`
			}

			var entries []logEntry
			err := app.AuxDB().
				Select("level", "message", "data").
				From(core.LogsTableName).
				All(&entries)
			if err != nil {
				t.Fatalf("failed to load logs: %v", err)
			}

			if len(entries) != 1 {
				t.Fatalf("expected 1 log entry, got %d", len(entries))
			}

			entry := entries[0]
			if entry.Message != "GET /my/test" {
				t.Fatalf("expected log message %q, got %q", "GET /my/test", entry.Message)
			}

			if entry.Level != int(slog.LevelError) {
				t.Fatalf("expected log level %d, got %d", int(slog.LevelError), entry.Level)
			}

			originalLevel, _ := entry.Data["originalLevel"].(string)
			if originalLevel != slog.LevelInfo.String() {
				t.Fatalf("expected originalLevel %q, got %q", slog.LevelInfo.String(), originalLevel)
			}
		},
	}

	scenario.Test(t)
}

func TestActivityLoggerStoresPostBody(t *testing.T) {
	t.Parallel()

	payload := `{"title":"example","count":2}`

	scenario := tests.ApiScenario{
		Name:   "request logs capture post body",
		Method: http.MethodPost,
		URL:    "/my/test",
		Body:   strings.NewReader(payload),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
			app.Settings().Logs.MaxDays = 5
			app.Settings().Logs.MinLevel = int(slog.LevelInfo)
			if err := app.Save(app.Settings()); err != nil {
				t.Fatalf("failed to update settings: %v", err)
			}

			if _, err := app.AuxDB().NewQuery("DELETE FROM {{_logs}}").Execute(); err != nil {
				t.Fatalf("failed to cleanup logs: %v", err)
			}

			e.Router.POST("/my/test", func(e *core.RequestEvent) error {
				return e.NoContent(http.StatusOK)
			})
		},
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
			handler, ok := app.Logger().Handler().(*logger.BatchHandler)
			if !ok {
				t.Fatalf("expected BatchHandler, got %T", app.Logger().Handler())
			}

			if err := handler.WriteAll(context.Background()); err != nil {
				t.Fatalf("failed to flush logs: %v", err)
			}

			type logEntry struct {
				Message string             `db:"message"`
				Data    types.JSONMap[any] `db:"data"`
			}

			var entries []logEntry
			if err := app.AuxDB().
				Select("message", "data").
				From(core.LogsTableName).
				All(&entries); err != nil {
				t.Fatalf("failed to load logs: %v", err)
			}

			if len(entries) != 1 {
				t.Fatalf("expected 1 log entry, got %d", len(entries))
			}

			entry := entries[0]
			if entry.Message != "POST /my/test" {
				t.Fatalf("expected log message %q, got %q", "POST /my/test", entry.Message)
			}

			body, ok := entry.Data["body"].(map[string]any)
			if !ok {
				t.Fatalf("expected body map in log data, got %T", entry.Data["body"])
			}

			if got := body["title"]; got != "example" {
				t.Fatalf("expected body.title to be %q, got %v", "example", got)
			}

			count, ok := body["count"]
			if !ok {
				t.Fatalf("expected body.count to exist, got %v", body)
			}
			countNumber, ok := count.(float64)
			if !ok || int(countNumber) != 2 {
				t.Fatalf("expected body.count to be 2, got %v", count)
			}
		},
	}

	scenario.Test(t)
}

func TestActivityLoggerSkipsHealthEndpoint(t *testing.T) {
	t.Parallel()

	scenario := tests.ApiScenario{
		Name:   "health endpoint not logged",
		Method: http.MethodGet,
		URL:    "/api/health",
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			app.Settings().Logs.MaxDays = 5
			app.Settings().Logs.MinLevel = int(slog.LevelInfo)
			if err := app.Save(app.Settings()); err != nil {
				t.Fatalf("failed to update settings: %v", err)
			}

			if _, err := app.AuxDB().NewQuery("DELETE FROM {{_logs}}").Execute(); err != nil {
				t.Fatalf("failed to cleanup logs: %v", err)
			}
		},
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"status":"ok"`,
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
			handler, ok := app.Logger().Handler().(*logger.BatchHandler)
			if !ok {
				t.Fatalf("expected BatchHandler, got %T", app.Logger().Handler())
			}

			if err := handler.WriteAll(context.Background()); err != nil {
				t.Fatalf("failed to flush logs: %v", err)
			}

			var count int
			if err := app.AuxDB().
				Select("COUNT(*)").
				From(core.LogsTableName).
				Row(&count); err != nil {
				t.Fatalf("failed to count logs: %v", err)
			}

			if count != 0 {
				t.Fatalf("expected no log entries, got %d", count)
			}
		},
	}

	scenario.Test(t)
}

func TestRequireSuperuserAuth(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:   "guest",
			Method: http.MethodGet,
			URL:    "/my/test",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserAuth())
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "expired/invalid token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjE2NDA5OTE2NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.0pDcBPGDpL2Khh76ivlRi7ugiLBSYvasct3qpHV3rfs",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserAuth())
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid regular user auth token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserAuth())
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid superuser auth token",
			Method: http.MethodGet,
			URL:    "/my/test",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserAuth())
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestRequireSuperuserOrOwnerAuth(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:   "guest",
			Method: http.MethodGet,
			URL:    "/my/test/4q1xlclmfloku33",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{id}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth(""))
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "expired/invalid token",
			Method: http.MethodGet,
			URL:    "/my/test/4q1xlclmfloku33",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjE2NDA5OTE2NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.0pDcBPGDpL2Khh76ivlRi7ugiLBSYvasct3qpHV3rfs",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{id}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth(""))
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token (different user)",
			Method: http.MethodGet,
			URL:    "/my/test/oap640cot4yru2s",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{id}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth(""))
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token (owner)",
			Method: http.MethodGet,
			URL:    "/my/test/4q1xlclmfloku33",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{id}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth(""))
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
		{
			Name:   "valid record auth token (owner + non-matching custom owner param)",
			Method: http.MethodGet,
			URL:    "/my/test/4q1xlclmfloku33",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{id}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth("test"))
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token (owner + matching custom owner param)",
			Method: http.MethodGet,
			URL:    "/my/test/4q1xlclmfloku33",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{test}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth("test"))
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
		{
			Name:   "valid superuser auth token",
			Method: http.MethodGet,
			URL:    "/my/test/4q1xlclmfloku33",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{id}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth(""))
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestRequireSameCollectionContextAuth(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:   "guest",
			Method: http.MethodGet,
			URL:    "/my/test/_pb_users_auth_",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{collection}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSameCollectionContextAuth(""))
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "expired/invalid token",
			Method: http.MethodGet,
			URL:    "/my/test/_pb_users_auth_",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoxNjQwOTkxNjYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.2D3tmqPn3vc5LoqqCz8V-iCDVXo9soYiH0d32G7FQT4",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{collection}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSameCollectionContextAuth(""))
			},
			ExpectedStatus:  401,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token (different collection)",
			Method: http.MethodGet,
			URL:    "/my/test/clients",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{collection}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSameCollectionContextAuth(""))
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token (same collection)",
			Method: http.MethodGet,
			URL:    "/my/test/_pb_users_auth_",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{collection}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSameCollectionContextAuth(""))
			},
			ExpectedStatus:  200,
			ExpectedContent: []string{"test123"},
		},
		{
			Name:   "valid record auth token (non-matching/missing collection param)",
			Method: http.MethodGet,
			URL:    "/my/test/_pb_users_auth_",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{id}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth(""))
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "valid record auth token (matching custom collection param)",
			Method: http.MethodGet,
			URL:    "/my/test/_pb_users_auth_",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{test}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSuperuserOrOwnerAuth("test"))
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
		{
			Name:   "superuser no exception check",
			Method: http.MethodGet,
			URL:    "/my/test/_pb_users_auth_",
			Headers: map[string]string{
				"Authorization": "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY",
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				e.Router.GET("/my/test/{collection}", func(e *core.RequestEvent) error {
					return e.String(200, "test123")
				}).Bind(apis.RequireSameCollectionContextAuth(""))
			},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
			ExpectedEvents:  map[string]int{"*": 0},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}
