package apis_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/bosbase/bosbase-enterprise/tests"
)

const (
	redisSuperuserToken = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY"
	redisUserToken      = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo"
)

func newRedisTestApp(t testing.TB, requirePassword bool) (*tests.TestApp, *miniredis.Miniredis) {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start redis test server: %v", err)
	}
	t.Cleanup(srv.Close)

	if requirePassword {
		srv.RequireAuth("secret")
		t.Setenv("REDIS_PASSWORD", "secret")
	} else {
		t.Setenv("REDIS_PASSWORD", "")
	}
	t.Setenv("REDIS_URL", "redis://"+srv.Addr())

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatalf("failed to init test app: %v", err)
	}

	return app, srv
}

func TestRedisApiDisabledWhenEnvMissing(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "redis api disabled without REDIS_URL",
		Method:         http.MethodGet,
		URL:            "/api/redis/keys",
		ExpectedStatus: 404,
		ExpectedEvents: map[string]int{"*": 0},
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			t.Setenv("REDIS_URL", "")
			t.Setenv("REDIS_PASSWORD", "")

			app, err := tests.NewTestApp()
			if err != nil {
				t.Fatalf("failed to init test app: %v", err)
			}
			return app
		},
	}

	scenario.Test(t)
}

func TestRedisApiRequiresSuperuser(t *testing.T) {
	var redisSrv *miniredis.Miniredis
	scenario := tests.ApiScenario{
		Name:   "redis api requires superuser",
		Method: http.MethodGet,
		URL:    "/api/redis/keys",
		Headers: map[string]string{
			"Authorization": redisUserToken,
		},
		ExpectedStatus: 403,
		ExpectedContent: []string{
			`"data":{}`,
		},
		ExpectedEvents: map[string]int{"*": 0},
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			app, srv := newRedisTestApp(t, false)
			redisSrv = srv
			redisSrv.Set("test", `"value"`)
			return app
		},
	}

	scenario.Test(t)
	if redisSrv != nil {
		if !redisSrv.Exists("test") {
			t.Fatalf("expected redis key to remain untouched")
		}
	}
}

func TestRedisApiListKeys(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "list redis keys",
		Method:         http.MethodGet,
		URL:            "/api/redis/keys?pattern=foo:*&count=10",
		ExpectedStatus: 200,
		ExpectedContent: []string{
			`"key":"foo:1"`,
			`"cursor":"0"`,
		},
		ExpectedEvents: map[string]int{"*": 0},
		Headers: map[string]string{
			"Authorization": redisSuperuserToken,
		},
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			app, srv := newRedisTestApp(t, false)
			srv.Set("foo:1", `"a"`)
			srv.Set("foo:2", `"b"`)
			srv.Set("bar:1", `"c"`)
			return app
		},
	}

	scenario.Test(t)
}

func TestRedisApiAcceptsURLWithoutScheme(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "redis api accepts bare host url",
		Method:         http.MethodGet,
		URL:            "/api/redis/keys?count=5",
		ExpectedStatus: 200,
		ExpectedContent: []string{
			`"cursor":"0"`,
			`"key":"bare:1"`,
		},
		ExpectedEvents: map[string]int{"*": 0},
		Headers: map[string]string{
			"Authorization": redisSuperuserToken,
		},
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			srv, err := miniredis.Run()
			if err != nil {
				t.Fatalf("failed to start redis test server: %v", err)
			}
			t.Cleanup(srv.Close)

			srv.Set("bare:1", `"v"`)

			t.Setenv("REDIS_URL", srv.Addr())
			t.Setenv("REDIS_PASSWORD", "")

			app, err := tests.NewTestApp()
			if err != nil {
				t.Fatalf("failed to init test app: %v", err)
			}
			return app
		},
	}

	scenario.Test(t)
}

func TestRedisApiCreateKey(t *testing.T) {
	var redisSrv *miniredis.Miniredis
	scenario := tests.ApiScenario{
		Name:   "create redis key with ttl",
		Method: http.MethodPost,
		URL:    "/api/redis/keys",
		Headers: map[string]string{
			"Authorization": redisSuperuserToken,
		},
		Body: strings.NewReader(`{
			"key": "session:123",
			"value": { "ok": true },
			"ttlSeconds": 60
		}`),
		ExpectedStatus: 201,
		ExpectedContent: []string{
			`"key":"session:123"`,
			`"ok":true`,
			`"ttlSeconds":60`,
		},
		ExpectedEvents: map[string]int{"*": 0},
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			app, srv := newRedisTestApp(t, true)
			redisSrv = srv
			return app
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
			if redisSrv == nil {
				return
			}
			if !redisSrv.Exists("session:123") {
				t.Fatalf("expected redis key to be created")
			}
			ttl := redisSrv.TTL("session:123")
			if ttl < 45*time.Second || ttl > 60*time.Second {
				t.Fatalf("expected ttl to be near 60s, got %v", ttl)
			}
		},
	}

	scenario.Test(t)
}

func TestRedisApiUpdateKeepsTTL(t *testing.T) {
	var redisSrv *miniredis.Miniredis
	scenario := tests.ApiScenario{
		Name:   "update redis key preserves ttl",
		Method: http.MethodPut,
		URL:    "/api/redis/keys/foo",
		Headers: map[string]string{
			"Authorization": redisSuperuserToken,
		},
		Body: strings.NewReader(`{
			"value": { "count": 2 }
		}`),
		ExpectedStatus: 200,
		ExpectedContent: []string{
			`"key":"foo"`,
			`"count":2`,
		},
		ExpectedEvents: map[string]int{"*": 0},
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			app, srv := newRedisTestApp(t, false)
			redisSrv = srv
			redisSrv.Set("foo", `{"count":1}`)
			redisSrv.SetTTL("foo", 90*time.Second)
			return app
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
			if redisSrv == nil {
				return
			}
			ttl := redisSrv.TTL("foo")
			if ttl < 60*time.Second {
				t.Fatalf("expected ttl to be preserved, got %v", ttl)
			}
		},
	}

	scenario.Test(t)
}

func TestRedisApiDeleteKey(t *testing.T) {
	var redisSrv *miniredis.Miniredis
	scenario := tests.ApiScenario{
		Name:   "delete redis key",
		Method: http.MethodDelete,
		URL:    "/api/redis/keys/remove-me",
		Headers: map[string]string{
			"Authorization": redisSuperuserToken,
		},
		ExpectedStatus: 204,
		ExpectedEvents: map[string]int{"*": 0},
		TestAppFactory: func(t testing.TB) *tests.TestApp {
			app, srv := newRedisTestApp(t, false)
			redisSrv = srv
			redisSrv.Set("remove-me", `"bye"`)
			return app
		},
		AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
			if redisSrv != nil && redisSrv.Exists("remove-me") {
				t.Fatalf("expected redis key to be deleted")
			}
		},
	}

	scenario.Test(t)
}
