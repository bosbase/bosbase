package apis_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbx"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
	"github.com/gofrs/uuid/v5"
)

const (
	scriptsSuperuserToken = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY"
	scriptsUserToken      = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo"
)

func TestScriptsApi(t *testing.T) {
	t.Parallel()

	uploadBody := new(bytes.Buffer)
	uploadScenario := tests.ApiScenario{
		Name:   "upload script file into execute path",
		Method: http.MethodPost,
		URL:    "/api/scripts/upload",
		Headers: map[string]string{
			"Authorization": scriptsSuperuserToken,
		},
		Body:           uploadBody,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"output":"uploaded subdir/upload.sh to `,
			`"path":"`,
		},
		ExpectedEvents: map[string]int{"*": 0},
	}

	uploadScenario.BeforeTestFunc = func(t testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
		execDir := t.TempDir()
		setExecEnv(t, execDir)

		// seed an existing file to ensure overwrite
		existingPath := filepath.Join(execDir, "subdir", "upload.sh")
		if err := os.MkdirAll(filepath.Dir(existingPath), 0o755); err != nil {
			t.Fatalf("failed to prepare upload dir: %v", err)
		}
		if err := os.WriteFile(existingPath, []byte("old"), 0o600); err != nil {
			t.Fatalf("failed to seed existing file: %v", err)
		}

		uploadBody.Reset()

		writer := multipart.NewWriter(uploadBody)
		part, err := writer.CreateFormFile("file", "upload.sh")
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		if _, err := part.Write([]byte("#!/bin/sh\necho upload-ok\n")); err != nil {
			t.Fatalf("failed to write form file: %v", err)
		}
		if err := writer.WriteField("path", "subdir/upload.sh"); err != nil {
			t.Fatalf("failed to write path field: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("failed to close multipart writer: %v", err)
		}

		uploadScenario.Headers["Content-Type"] = writer.FormDataContentType()
	}

	uploadScenario.AfterTestFunc = func(t testing.TB, _ *tests.TestApp, _ *http.Response) {
		execDir := os.Getenv("EXECUTE_PATH")
		target := filepath.Join(execDir, "subdir", "upload.sh")

		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("failed to read uploaded file: %v", err)
		}
		if !strings.Contains(string(data), "upload-ok") {
			t.Fatalf("unexpected uploaded content: %s", data)
		}

		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("failed to stat uploaded file: %v", err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("expected permissions 755, got %v", info.Mode().Perm())
		}
	}

	wasmScenario := tests.ApiScenario{
		Name:   "execute wasm with superuser by default",
		Method: http.MethodPost,
		URL:    "/api/scripts/wasm",
		Headers: map[string]string{
			"Authorization": scriptsSuperuserToken,
		},
		Body:           strings.NewReader(`{"options":"--reactor","wasm":"demo.wasm","params":"fib 10"}`),
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"output":"wasm-runner --reactor demo.wasm fib 10`,
		},
		ExpectedEvents: map[string]int{"*": 0},
		BeforeTestFunc: func(t testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			execDir := setupWasmExecEnv(t, "demo.wasm")
			setExecEnv(t, execDir)
		},
	}

	wasmUserScenario := tests.ApiScenario{
		Name:   "execute wasm with user permission",
		Method: http.MethodPost,
		URL:    "/api/scripts/wasm",
		Headers: map[string]string{
			"Authorization": scriptsUserToken,
		},
		Body:           strings.NewReader(`{"wasm":"user.wasm","params":"arg1 arg2"}`),
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"output":"wasm-runner user.wasm arg1 arg2`,
		},
		ExpectedEvents: map[string]int{"*": 0},
		BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			execDir := setupWasmExecEnv(t, "user.wasm")
			setExecEnv(t, execDir)
			seedScriptPermissionRecord(t, app, "user.wasm", "user", "")
		},
	}

	scenarios := []tests.ApiScenario{
		{
			Name:           "list scripts unauthenticated",
			Method:         http.MethodGet,
			URL:            "/api/scripts",
			ExpectedStatus: http.StatusUnauthorized,
			ExpectedContent: []string{
				`"data":{}`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "list scripts as regular user is forbidden",
			Method: http.MethodGet,
			URL:    "/api/scripts",
			Headers: map[string]string{
				"Authorization": scriptsUserToken,
			},
			ExpectedStatus: http.StatusForbidden,
			ExpectedContent: []string{
				`"data":{}`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "create script",
			Method: http.MethodPost,
			URL:    "/api/scripts",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body: strings.NewReader(`{
				"name": "hello.py",
				"content": "print('hi')",
				"description": "greeting"
			}`),
			ExpectedStatus: http.StatusCreated,
			ExpectedContent: []string{
				`"name":"hello.py"`,
				`"description":"greeting"`,
				`"version":1`,
				`"id":"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "get script",
			Method: http.MethodGet,
			URL:    "/api/scripts/hello.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScript(t, app, "hello.py", "print('hi')", "greeting", 1)
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"name":"hello.py"`,
				`"version":1`,
				`"id":"`,
				`"content":"print('hi')"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "update script bumps version",
			Method: http.MethodPatch,
			URL:    "/api/scripts/hello.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body: strings.NewReader(`{
				"description": "updated",
				"content": "print('updated')"
			}`),
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScript(t, app, "hello.py", "print('hi')", "greeting", 1)
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"name":"hello.py"`,
				`"version":2`,
				`"description":"updated"`,
				`"content":"print('updated')"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "delete script",
			Method: http.MethodDelete,
			URL:    "/api/scripts/hello.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScript(t, app, "hello.py", "print('hi')", "greeting", 1)
			},
			ExpectedStatus: http.StatusNoContent,
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "command runs in execute path",
			Method: http.MethodPost,
			URL:    "/api/scripts/command",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body: strings.NewReader(`{"command": "pwd"}`),
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := t.TempDir()
				old := os.Getenv("EXECUTE_PATH")
				if err := os.Setenv("EXECUTE_PATH", execDir); err != nil {
					t.Fatalf("failed to set EXECUTE_PATH: %v", err)
				}
				t.Cleanup(func() {
					_ = os.Setenv("EXECUTE_PATH", old)
				})
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"output":"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "command async start",
			Method: http.MethodPost,
			URL:    "/api/scripts/command",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body:           strings.NewReader(`{"command": "echo hi", "async": true}`),
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"id":"`,
				`"status":"running"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		uploadScenario,
		wasmScenario,
		wasmUserScenario,
		{
			Name:   "execute script returns output",
			Method: http.MethodPost,
			URL:    "/api/scripts/hello.py/execute",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := setupFakeExecEnv(t)
				seedScript(t, app, "hello.py", "print('hi')", "greeting", 1)
				setExecEnv(t, execDir)
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"output":"executed hello.py`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "execute async returns job id",
			Method: http.MethodPost,
			URL:    "/api/scripts/async/hello.py/execute",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := setupFakeExecEnv(t)
				seedScript(t, app, "hello.py", "print('hi')", "greeting", 1)
				setExecEnv(t, execDir)
			},
			ExpectedStatus: http.StatusAccepted,
			ExpectedContent: []string{
				`"id":"`,
				`"status":"running"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "execute async status endpoint returns running/done",
			Method: http.MethodGet,
			URL:    "/api/scripts/async/test-id",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				// Ensure jobs table exists for the status endpoint.
				_, err := app.NonconcurrentDB().NewQuery(`
					CREATE TABLE IF NOT EXISTS {{function_script_execute_jobs}} (
						[[id]]             TEXT PRIMARY KEY,
						[[script_name]]    TEXT NOT NULL,
						[[function_name]]  TEXT NOT NULL,
						[[args]]           TEXT DEFAULT '',
						[[status]]         TEXT NOT NULL,
						[[output]]         TEXT DEFAULT '',
						[[error]]          TEXT DEFAULT '',
						[[started]]        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
						[[finished]]       TIMESTAMPTZ
					);
				`).Execute()
				if err != nil {
					t.Fatalf("failed to ensure scripts execute jobs table: %v", err)
				}

				_, err = app.NonconcurrentDB().Insert("function_script_execute_jobs", dbx.Params{
					"id":            "test-id",
					"script_name":   "hello.py",
					"function_name": "main",
					"args":          `[]`,
					"status":        "running",
					"output":        "",
					"error":         "",
					"started":       time.Now(),
					"finished":      nil,
				}).Execute()
				if err != nil {
					t.Fatalf("failed to seed scripts execute job: %v", err)
				}
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"id":"test-id"`,
				`"status":"running"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "execute script appends arguments",
			Method: http.MethodPost,
			URL:    "/api/scripts/args.py/execute",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body: strings.NewReader(`{"args":["10","20"]}`),
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := setupFakeExecEnv(t)
				seedScript(t, app, "args.py", "print('hi')", "args", 1)
				setExecEnv(t, execDir)
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"output":"executed args.py 10 20`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "script permission create",
			Method: http.MethodPost,
			URL:    "/api/script-permissions",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body:           strings.NewReader(`{"script_name":"hello.py","content":"anonymous"}`),
			ExpectedStatus: http.StatusCreated,
			ExpectedContent: []string{
				`"scriptName":"hello.py"`,
				`"content":"anonymous"`,
				`"version":1`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "script permission get",
			Method: http.MethodGet,
			URL:    "/api/script-permissions/hello.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScriptPermissionRecord(t, app, "hello.py", "anonymous", "")
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"scriptName":"hello.py"`,
				`"content":"anonymous"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "script permission update bumps version",
			Method: http.MethodPatch,
			URL:    "/api/script-permissions/hello.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			Body: strings.NewReader(`{"content":"user"}`),
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScriptPermissionRecord(t, app, "hello.py", "anonymous", "")
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"content":"user"`,
				`"version":2`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "script permission delete",
			Method: http.MethodDelete,
			URL:    "/api/script-permissions/hello.py",
			Headers: map[string]string{
				"Authorization": scriptsSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				seedScriptPermissionRecord(t, app, "hello.py", "superuser", "")
			},
			ExpectedStatus: http.StatusNoContent,
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "execute anonymous allows unauthenticated",
			Method: http.MethodPost,
			URL:    "/api/scripts/anon.py/execute",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := setupFakeExecEnv(t)
				seedScript(t, app, "anon.py", "print('hi')", "greeting", 1)
				seedScriptPermissionRecord(t, app, "anon.py", "anonymous", "")
				setExecEnv(t, execDir)
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"output":"executed anon.py`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "execute user requires auth record",
			Method: http.MethodPost,
			URL:    "/api/scripts/user.py/execute",
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := setupFakeExecEnv(t)
				seedScript(t, app, "user.py", "print('hi')", "greeting", 1)
				seedScriptPermissionRecord(t, app, "user.py", "user", "")
				setExecEnv(t, execDir)
			},
			ExpectedStatus: http.StatusUnauthorized,
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "execute user allows user token",
			Method: http.MethodPost,
			URL:    "/api/scripts/user2.py/execute",
			Headers: map[string]string{
				"Authorization": scriptsUserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := setupFakeExecEnv(t)
				seedScript(t, app, "user2.py", "print('hi')", "greeting", 1)
				seedScriptPermissionRecord(t, app, "user2.py", "user", "")
				setExecEnv(t, execDir)
			},
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"output":"executed user2.py`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "execute superuser blocks regular user",
			Method: http.MethodPost,
			URL:    "/api/scripts/restricted.py/execute",
			Headers: map[string]string{
				"Authorization": scriptsUserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				execDir := setupFakeExecEnv(t)
				seedScript(t, app, "restricted.py", "print('hi')", "greeting", 1)
				seedScriptPermissionRecord(t, app, "restricted.py", "superuser", "")
				setExecEnv(t, execDir)
			},
			ExpectedStatus: http.StatusForbidden,
			ExpectedEvents: map[string]int{"*": 0},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func seedScript(t testing.TB, app *tests.TestApp, name, content, description string, version int) string {
	t.Helper()

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate script id: %v", err)
	}

	if version <= 0 {
		version = 1
	}

	_, execErr := app.NonconcurrentDB().Insert("function_scripts", dbx.Params{
		"id":          id.String(),
		"name":        name,
		"content":     content,
		"description": description,
		"version":     version,
	}).Execute()
	if execErr != nil {
		t.Fatalf("failed to seed script: %v", execErr)
	}

	return id.String()
}

func seedScriptPermissionRecord(t testing.TB, app *tests.TestApp, scriptName, content, scriptID string) string {
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

func setupFakeExecEnv(t testing.TB) string {
	t.Helper()

	execDir := t.TempDir()
	venvBin := filepath.Join(execDir, ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatalf("failed to prepare fake venv: %v", err)
	}

	activate := filepath.Join(venvBin, "activate")
	activateContent := "#!/usr/bin/env bash\nexport PATH=\"" + venvBin + ":$PATH\"\n"
	if err := os.WriteFile(activate, []byte(activateContent), 0o755); err != nil {
		t.Fatalf("failed to write activate: %v", err)
	}

	pythonStub := filepath.Join(venvBin, "python")
	pythonContent := "#!/usr/bin/env bash\nscript=$(basename \"$1\")\nshift\nif [ \"$#\" -gt 0 ]; then\n  echo \"executed $script $@\"\nelse\n  echo \"executed $script\"\nfi\n"
	if err := os.WriteFile(pythonStub, []byte(pythonContent), 0o755); err != nil {
		t.Fatalf("failed to write python stub: %v", err)
	}

	return execDir
}

func setupWasmExecEnv(t testing.TB, wasmName string) string {
	t.Helper()

	execDir := t.TempDir()

	wasmedge := filepath.Join(execDir, "wasmedge")
	wasmedgeContent := "#!/usr/bin/env bash\necho \"wasm-runner $@\"\n"
	if err := os.WriteFile(wasmedge, []byte(wasmedgeContent), 0o755); err != nil {
		t.Fatalf("failed to write wasmedge stub: %v", err)
	}

	if strings.TrimSpace(wasmName) != "" {
		wasmPath := filepath.Join(execDir, wasmName)
		if err := os.MkdirAll(filepath.Dir(wasmPath), 0o755); err != nil {
			t.Fatalf("failed to create wasm dir: %v", err)
		}
		if err := os.WriteFile(wasmPath, []byte("00"), 0o644); err != nil {
			t.Fatalf("failed to write wasm placeholder: %v", err)
		}
	}

	return execDir
}

func setExecEnv(t testing.TB, execDir string) {
	t.Helper()
	old := os.Getenv("EXECUTE_PATH")
	if err := os.Setenv("EXECUTE_PATH", execDir); err != nil {
		t.Fatalf("failed to set EXECUTE_PATH: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("EXECUTE_PATH", old)
	})
}
