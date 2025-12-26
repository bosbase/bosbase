package apis_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bosbase/bosbase-enterprise/apis"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
	"github.com/bosbase/bosbase-enterprise/tools/router"
)

func setupApiTestApp(t testing.TB) (*tests.TestApp, *router.Router[*core.RequestEvent]) {
	t.Helper()

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatalf("Failed to initialize the test app instance: %v", err)
	}

	baseRouter, err := apis.NewRouter(app)
	if err != nil {
		app.Cleanup()
		t.Fatalf("Failed to initialize router: %v", err)
	}

	serveEvent := &core.ServeEvent{App: app, Router: baseRouter}
	if err := app.OnServe().Trigger(serveEvent, func(e *core.ServeEvent) error {
		return e.Next()
	}); err != nil {
		app.Cleanup()
		t.Fatalf("Failed to trigger app serve hook: %v", err)
	}

	return app, baseRouter
}

func performApiRequest(
	t testing.TB,
	baseRouter *router.Router[*core.RequestEvent],
	method string,
	url string,
	body io.Reader,
	headers map[string]string,
) (int, string) {
	t.Helper()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, body)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	mux, err := baseRouter.BuildMux()
	if err != nil {
		t.Fatalf("Failed to build router mux: %v", err)
	}
	mux.ServeHTTP(recorder, req)

	return recorder.Result().StatusCode, recorder.Body.String()
}

func TestScriptsCacheCaseSensitive(t *testing.T) {
	t.Parallel()

	app, baseRouter := setupApiTestApp(t)
	defer app.Cleanup()

	headers := map[string]string{"Authorization": scriptsSuperuserToken}

	status, body := performApiRequest(t, baseRouter, http.MethodPost, "/api/scripts",
		strings.NewReader(`{"name":"Hello.py","content":"print('hi')","description":"upper"}`),
		headers,
	)
	if status != http.StatusCreated {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusCreated, status, body)
	}

	status, body = performApiRequest(t, baseRouter, http.MethodPost, "/api/scripts",
		strings.NewReader(`{"name":"hello.py","content":"print('hello')","description":"lower"}`),
		headers,
	)
	if status != http.StatusCreated {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusCreated, status, body)
	}

	status, body = performApiRequest(t, baseRouter, http.MethodGet, "/api/scripts/Hello.py", nil, headers)
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusOK, status, body)
	}
	if !strings.Contains(body, `"name":"Hello.py"`) || !strings.Contains(body, `"description":"upper"`) {
		t.Fatalf("expected Hello.py script, got: %s", body)
	}

	status, body = performApiRequest(t, baseRouter, http.MethodGet, "/api/scripts/hello.py", nil, headers)
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusOK, status, body)
	}
	if !strings.Contains(body, `"name":"hello.py"`) || !strings.Contains(body, `"description":"lower"`) {
		t.Fatalf("expected hello.py script, got: %s", body)
	}
}

func TestScriptPermissionsCacheCaseSensitive(t *testing.T) {
	t.Parallel()

	app, baseRouter := setupApiTestApp(t)
	defer app.Cleanup()

	headers := map[string]string{"Authorization": scriptsSuperuserToken}

	status, body := performApiRequest(t, baseRouter, http.MethodPost, "/api/script-permissions",
		strings.NewReader(`{"script_name":"Hello.py","content":"anonymous"}`),
		headers,
	)
	if status != http.StatusCreated {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusCreated, status, body)
	}

	status, body = performApiRequest(t, baseRouter, http.MethodPost, "/api/script-permissions",
		strings.NewReader(`{"script_name":"hello.py","content":"user"}`),
		headers,
	)
	if status != http.StatusCreated {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusCreated, status, body)
	}

	status, body = performApiRequest(t, baseRouter, http.MethodGet, "/api/script-permissions/Hello.py", nil, headers)
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusOK, status, body)
	}
	if !strings.Contains(body, `"scriptName":"Hello.py"`) || !strings.Contains(body, `"content":"anonymous"`) {
		t.Fatalf("expected Hello.py permission, got: %s", body)
	}

	status, body = performApiRequest(t, baseRouter, http.MethodGet, "/api/script-permissions/hello.py", nil, headers)
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d (%s)", http.StatusOK, status, body)
	}
	if !strings.Contains(body, `"scriptName":"hello.py"`) || !strings.Contains(body, `"content":"user"`) {
		t.Fatalf("expected hello.py permission, got: %s", body)
	}
}
