package apis_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
)

const (
	cacheSuperuserToken = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY"
	cacheUserToken      = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IjRxMXhsY2xtZmxva3UzMyIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoiX3BiX3VzZXJzX2F1dGhfIiwiZXhwIjoyNTI0NjA0NDYxLCJyZWZyZXNoYWJsZSI6dHJ1ZX0.ZT3F0Z3iM-xbGgSG3LEKiEzHrPHr8t8IuHLZGGNuxLo"
)

func TestCacheApi(t *testing.T) {
	t.Parallel()

	scenarios := []tests.ApiScenario{
		{
			Name:           "list caches unauthenticated",
			Method:         http.MethodGet,
			URL:            "/api/cache",
			ExpectedStatus: 401,
			ExpectedContent: []string{
				`"data":{}`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "list caches as regular auth record",
			Method: http.MethodGet,
			URL:    "/api/cache",
			Headers: map[string]string{
				"Authorization": cacheUserToken,
			},
			ExpectedStatus: 403,
			ExpectedContent: []string{
				`"data":{}`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "list caches as superuser",
			Method: http.MethodGet,
			URL:    "/api/cache",
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "primary")
				// Add an entry to generate some statistics
				_, err := app.CacheStore().SetEntry(context.Background(), "primary", "test-key", []byte(`"test-value"`), 0)
				if err != nil {
					t.Fatalf("failed to set cache entry: %v", err)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"name":"primary"`,
				`"sizeBytes"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "create cache",
			Method: http.MethodPost,
			URL:    "/api/cache",
			Body: strings.NewReader(`{
				"name": "ai-cache",
				"sizeBytes": 1048576,
				"defaultTTLSeconds": 120,
				"readTimeoutMs": 10
			}`),
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			ExpectedStatus: 201,
			ExpectedContent: []string{
				`"name":"ai-cache"`,
				`"defaultTTLSeconds":120`,
				`"readTimeoutMs":10`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "update cache",
			Method: http.MethodPatch,
			URL:    "/api/cache/ai-cache",
			Body:   strings.NewReader(`{"defaultTTLSeconds":90}`),
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"name":"ai-cache"`,
				`"defaultTTLSeconds":90`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "delete cache",
			Method: http.MethodDelete,
			URL:    "/api/cache/ai-cache",
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
			},
			ExpectedStatus: 204,
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "set cache entry",
			Method: http.MethodPut,
			URL:    "/api/cache/ai-cache/entries/dialog%3A1",
			Body:   strings.NewReader(`{"value":{"hello":"world"},"ttlSeconds":60}`),
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"key":"dialog:1"`,
				`"source":"cache"`,
				`"hello":"world"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "get cache entry from memory",
			Method: http.MethodGet,
			URL:    "/api/cache/ai-cache/entries/dialog%3A1",
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
				_, err := app.CacheStore().SetEntry(context.Background(), "ai-cache", "dialog:1", []byte(`{"hello":"cache"}`), 0)
				if err != nil {
					t.Fatalf("failed to seed cache entry: %v", err)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"source":"cache"`,
				`"hello":"cache"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "get cache entry from database",
			Method: http.MethodGet,
			URL:    "/api/cache/ai-cache/entries/db%3A1",
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
				insertCacheEntryRow(t, app, "ai-cache", "db:1", `{"source":"database"}`, time.Now().Add(5*time.Minute))
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"source":"database"`,
				`"database"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "renew cache entry",
			Method: http.MethodPatch,
			URL:    "/api/cache/ai-cache/entries/dialog%3A1",
			Body:   strings.NewReader(`{"ttlSeconds":120}`),
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
				_, err := app.CacheStore().SetEntry(context.Background(), "ai-cache", "dialog:1", []byte(`{"hello":"renew"}`), 10*time.Second)
				if err != nil {
					t.Fatalf("failed to seed cache entry: %v", err)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"key":"dialog:1"`,
				`"source":"cache"`,
				`"hello":"renew"`,
				`"expiresAt"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "renew cache entry without TTL",
			Method: http.MethodPatch,
			URL:    "/api/cache/ai-cache/entries/dialog%3A1",
			Body:   strings.NewReader(`{}`),
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
				_, err := app.CacheStore().SetEntry(context.Background(), "ai-cache", "dialog:1", []byte(`{"hello":"renew-default"}`), 10*time.Second)
				if err != nil {
					t.Fatalf("failed to seed cache entry: %v", err)
				}
			},
			ExpectedStatus: 200,
			ExpectedContent: []string{
				`"key":"dialog:1"`,
				`"hello":"renew-default"`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "renew non-existent cache entry",
			Method: http.MethodPatch,
			URL:    "/api/cache/ai-cache/entries/nonexistent",
			Body:   strings.NewReader(`{"ttlSeconds":60}`),
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
			},
			ExpectedStatus: 404,
			ExpectedContent: []string{
				`"data":{}`,
			},
			ExpectedEvents: map[string]int{"*": 0},
		},
		{
			Name:   "delete cache entry",
			Method: http.MethodDelete,
			URL:    "/api/cache/ai-cache/entries/dialog%3A1",
			Headers: map[string]string{
				"Authorization": cacheSuperuserToken,
			},
			BeforeTestFunc: func(t testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				ensureTestCache(t, app, "ai-cache")
				_, err := app.CacheStore().SetEntry(context.Background(), "ai-cache", "dialog:1", []byte(`"to-delete"`), 0)
				if err != nil {
					t.Fatalf("failed to seed cache entry: %v", err)
				}
			},
			ExpectedStatus: 204,
			ExpectedEvents: map[string]int{"*": 0},
		},
	}

	for _, scenario := range scenarios {
		scenario.Test(t)
	}
}

func ensureTestCache(t testing.TB, app *tests.TestApp, name string) {
	t.Helper()
	_, err := app.CacheStore().CreateCache(context.Background(), core.CacheConfig{Name: name})
	if err != nil {
		t.Fatalf("failed to create cache %s: %v", name, err)
	}
}

func insertCacheEntryRow(t testing.TB, app *tests.TestApp, cache, key, value string, expiresAt time.Time) {
	t.Helper()
	_, err := app.DB().NewQuery(`
		INSERT INTO {{_cache_entries}} ([[cache]], [[key]], [[value]], [[expiresAt]])
		VALUES ({:cache}, {:key}, {:value}, {:expiresAt})
	`).Bind(dbx.Params{
		"cache":     cache,
		"key":       key,
		"value":     []byte(value),
		"expiresAt": expiresAt.Unix(),
	}).Execute()
	if err != nil {
		t.Fatalf("failed to insert cache row: %v", err)
	}
}
