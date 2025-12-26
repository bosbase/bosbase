package apis_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
	"github.com/bosbase/bosbase-enterprise/tools/security"
)

func TestRecordBindToken(t *testing.T) {
	t.Parallel()

	token := "my-app-token"

	scenarios := []tests.ApiScenario{
		{
			Name:   "bind token success",
			Method: http.MethodPost,
			URL:    "/api/collections/users/bind-token",
			Body: strings.NewReader(`{
				"email":"test@example.com",
				"password":"1234567890",
				"token":"` + token + `"
			}`),
			ExpectedStatus: http.StatusNoContent,
			ExpectedEvents: map[string]int{
				"OnRecordBindTokenRequest": 1,
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				if !app.HasTable(core.TokenBindingsTableName) {
					t.Fatalf("expected %q table to exist", core.TokenBindingsTableName)
				}

				user, err := app.FindAuthRecordByEmail("users", "test@example.com")
				if err != nil {
					t.Fatalf("failed to load auth record: %v", err)
				}

				binding := core.TokenBinding{}

				err = app.DB().
					Select("id", "collectionRef", "recordRef", "tokenHash").
					From(core.TokenBindingsTableName).
					AndWhere(dbx.HashExp{
						"collectionRef": user.Collection().Id,
						"recordRef":     user.Id,
					}).
					Limit(1).
					One(&binding)
				if err != nil {
					t.Fatalf("expected binding to be created: %v", err)
				}

				if binding.TokenHash != security.SHA256(token) {
					t.Fatalf("expected token hash %q, got %q", security.SHA256(token), binding.TokenHash)
				}
				if binding.RecordRef != user.Id || binding.CollectionRef != user.Collection().Id {
					t.Fatalf("unexpected binding target: %v", binding)
				}
			},
		},
		{
			Name:   "bind token invalid credentials",
			Method: http.MethodPost,
			URL:    "/api/collections/users/bind-token",
			Body: strings.NewReader(`{
				"email":"test@example.com",
				"password":"bad",
				"token":"` + token + `"
			}`),
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"message":"Failed to bind token."`,
			},
			ExpectedEvents: map[string]int{
				"OnRecordBindTokenRequest": 0,
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				if app.HasTable(core.TokenBindingsTableName) {
					t.Fatalf("expected %q table to not be created on failure", core.TokenBindingsTableName)
				}
			},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestRecordUnbindToken(t *testing.T) {
	t.Parallel()

	token := "unbind-token"

	scenarios := []tests.ApiScenario{
		{
			Name:   "unbind token success",
			Method: http.MethodPost,
			URL:    "/api/collections/users/unbind-token",
			Body: strings.NewReader(`{
				"email":"test@example.com",
				"password":"1234567890",
				"token":"` + token + `"
			}`),
			ExpectedStatus: http.StatusNoContent,
			ExpectedEvents: map[string]int{
				"OnRecordUnbindTokenRequest": 1,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				user, err := app.FindAuthRecordByEmail("users", "test@example.com")
				if err != nil {
					t.Fatalf("failed to load auth record: %v", err)
				}
				if err := app.BindCustomToken(user, token); err != nil {
					t.Fatalf("failed to pre-bind token: %v", err)
				}
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				user, err := app.FindAuthRecordByEmail("users", "test@example.com")
				if err != nil {
					t.Fatalf("failed to load auth record: %v", err)
				}

				var exists int
				err = app.DB().
					Select("(1)").
					From(core.TokenBindingsTableName).
					AndWhere(dbx.HashExp{
						"collectionRef": user.Collection().Id,
						"recordRef":     user.Id,
					}).
					Limit(1).
					Row(&exists)
				if err == nil {
					t.Fatalf("expected binding to be deleted, found a row")
				}
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("unexpected error while checking deleted binding: %v", err)
				}
			},
		},
		{
			Name:   "unbind token invalid credentials",
			Method: http.MethodPost,
			URL:    "/api/collections/users/unbind-token",
			Body: strings.NewReader(`{
				"email":"test@example.com",
				"password":"bad",
				"token":"` + token + `"
			}`),
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"message":"Failed to unbind token."`,
			},
			ExpectedEvents: map[string]int{
				"OnRecordUnbindTokenRequest": 0,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				user, err := app.FindAuthRecordByEmail("users", "test@example.com")
				if err != nil {
					t.Fatalf("failed to load auth record: %v", err)
				}
				if err := app.BindCustomToken(user, token); err != nil {
					t.Fatalf("failed to pre-bind token: %v", err)
				}
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				user, err := app.FindAuthRecordByEmail("users", "test@example.com")
				if err != nil {
					t.Fatalf("failed to load auth record: %v", err)
				}

				var exists string
				err = app.DB().
					Select("id").
					From(core.TokenBindingsTableName).
					AndWhere(dbx.HashExp{
						"collectionRef": user.Collection().Id,
						"recordRef":     user.Id,
					}).
					Limit(1).
					Row(&exists)
				if err != nil {
					t.Fatalf("expected binding to remain on failure: %v", err)
				}
			},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func TestRecordAuthWithToken(t *testing.T) {
	t.Parallel()

	userToken := "login-token"
	superToken := "super-login-token"

	scenarios := []tests.ApiScenario{
		{
			Name:           "auth with token (user)",
			Method:         http.MethodPost,
			URL:            "/api/collections/users/auth-with-token",
			Body:           strings.NewReader(`{"token":"` + userToken + `"}`),
			ExpectedStatus: http.StatusOK,
			ExpectedEvents: map[string]int{
				"OnRecordAuthWithTokenRequest": 1,
				"OnRecordAuthRequest":          1,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				user, err := app.FindAuthRecordByEmail("users", "test@example.com")
				if err != nil {
					t.Fatalf("failed to load auth record: %v", err)
				}
				if err := app.BindCustomToken(user, userToken); err != nil {
					t.Fatalf("failed to pre-bind token: %v", err)
				}
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				user, err := app.FindAuthRecordByEmail("users", "test@example.com")
				if err != nil {
					t.Fatalf("failed to load auth record: %v", err)
				}

				result := struct {
					Token  string `json:"token"`
					Record struct {
						Id             string `json:"id"`
						CollectionName string `json:"collectionName"`
					} `json:"record"`
				}{}

				if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
					t.Fatalf("failed to decode auth response: %v", err)
				}

				if result.Token == "" {
					t.Fatalf("expected auth token to be returned")
				}
				if result.Record.Id != user.Id || result.Record.CollectionName != user.Collection().Name {
					t.Fatalf("unexpected record in auth response: %#v", result.Record)
				}
			},
		},
		{
			Name:           "auth with token (superuser)",
			Method:         http.MethodPost,
			URL:            "/api/collections/_superusers/auth-with-token",
			Body:           strings.NewReader(`{"token":"` + superToken + `"}`),
			ExpectedStatus: http.StatusOK,
			ExpectedEvents: map[string]int{
				"OnRecordAuthWithTokenRequest": 1,
				"OnRecordAuthRequest":          1,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
				record, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "test@example.com")
				if err != nil {
					t.Fatalf("failed to load superuser: %v", err)
				}
				if err := app.BindCustomToken(record, superToken); err != nil {
					t.Fatalf("failed to pre-bind token: %v", err)
				}
			},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				record, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "test@example.com")
				if err != nil {
					t.Fatalf("failed to load superuser: %v", err)
				}

				result := struct {
					Token  string `json:"token"`
					Record struct {
						Id             string `json:"id"`
						CollectionName string `json:"collectionName"`
					} `json:"record"`
				}{}

				if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
					t.Fatalf("failed to decode auth response: %v", err)
				}

				if result.Token == "" {
					t.Fatalf("expected auth token to be returned")
				}
				if result.Record.Id != record.Id || result.Record.CollectionName != record.Collection().Name {
					t.Fatalf("unexpected record in auth response: %#v", result.Record)
				}
			},
		},
		{
			Name:           "auth with token missing binding",
			Method:         http.MethodPost,
			URL:            "/api/collections/users/auth-with-token",
			Body:           strings.NewReader(`{"token":"does-not-exist"}`),
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"message":"Failed to authenticate."`,
			},
			ExpectedEvents: map[string]int{
				"OnRecordAuthWithTokenRequest": 0,
				"OnRecordAuthRequest":          0,
			},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}
