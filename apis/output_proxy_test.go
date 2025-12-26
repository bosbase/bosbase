package apis_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bosbase/bosbase-enterprise/tests"
)

func TestOutputProxyForwardsToBooster(t *testing.T) {
	booster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/run" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("bad path"))
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"ok\":true,\"method\":\"" + r.Method + "\",\"body\":" + string(b) + "}"))
	}))
	defer booster.Close()

	t.Setenv("BOOSTER_URL", booster.URL)

	scenario := tests.ApiScenario{
		Name:           "output proxy forwards to booster",
		Method:         http.MethodPost,
		URL:            "/output/run",
		Body:           strings.NewReader(`{"name":"Sparky"}`),
		ExpectedStatus: 200,
		ExpectedContent: []string{
			`"ok":true`,
			`"method":"POST"`,
			`"name":"Sparky"`,
		},
		ExpectedEvents: map[string]int{"*": 0},
	}

	scenario.Test(t)
}
