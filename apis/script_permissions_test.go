package apis_test

import (
	"net/http"
	"strings"
	"testing"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
	"github.com/gofrs/uuid/v5"
)

func TestScriptPermissionsApi(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:           "create permission",
			Method:         http.MethodPost,
			URL:            "/api/script-permissions",
			Headers:        map[string]string{"Authorization": scriptsSuperuserToken},
			Body:           strings.NewReader(`{"script_name":"perm.py","content":"anonymous"}`),
			ExpectedStatus: http.StatusCreated,
			ExpectedContent: []string{
				`"scriptName":"perm.py"`,
				`"content":"anonymous"`,
				`"version":1`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "get permission resolves script id",
			Method: http.MethodGet,
			URL:    "/api/script-permissions/perm.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				scriptID := seedScript(t, app, "perm.py", "print('hi')", "", 1)
				seedScriptPermission(t, app, "perm.py", "anonymous", "")
				_ = scriptID
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"scriptName":"perm.py"`,
				`"content":"anonymous"`,
				`"scriptId":"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "update permission increments version",
			Method: http.MethodPatch,
			URL:    "/api/script-permissions/perm.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body: strings.NewReader(`{"content":"user"}`),
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScriptPermission(t, app, "perm.py", "anonymous", "")
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"content":"user"`,
				`"version":2`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "delete permission",
			Method: http.MethodDelete,
			URL:    "/api/script-permissions/perm.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScriptPermission(t, app, "perm.py", "superuser", "")
			},
			ExpectedStatus: http.StatusNoContent,
			ExpectedEvents: map[string]int{"*": 0},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func seedScriptPermission(t testing.TB, app *tests.TestApp, scriptName, content, scriptID string) string {
	t.Helper()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate permission id: %v", err)
	}

	params := dbx.Params{
		"id":          id.String(),
		"script_id":   nil,
		"script_name": scriptName,
		"content":     content,
		"version":     1,
	}
	if strings.TrimSpace(scriptID) != "" {
		params["script_id"] = scriptID
	}

	_, execErr := app.NonconcurrentDB().Insert("function_script_permissions", params).Execute()
	if execErr != nil {
		t.Fatalf("failed to seed script permission: %v", execErr)
	}

	return id.String()
}
