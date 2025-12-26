package apis

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
)

const boosterURLEnvKey = "BOOSTER_URL"

func bindOutputProxy(app core.App, r *router.Router[*core.RequestEvent]) {
	boosterURL := strings.TrimSpace(os.Getenv(boosterURLEnvKey))
	if boosterURL == "" {
		boosterURL = "http://127.0.0.1:2678"
	}

	target, err := url.Parse(boosterURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		app.Logger().Warn("Output proxy disabled: invalid BOOSTER_URL", "BOOSTER_URL", boosterURL)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)

		// Some upstream middleware may read/replace req.Body without updating the
		// Content-Length header. If we forward a stale Content-Length, Go's transport
		// can fail with errors like "ContentLength=X with Body length Y".
		// Clearing it forces the client to use chunked transfer encoding.
		req.ContentLength = -1
		req.Header.Del("Content-Length")

		// Strip the /output prefix.
		path := strings.TrimPrefix(req.URL.Path, "/output")
		if path == "" {
			path = "/"
		}
		req.URL.Path = path
		req.Host = target.Host
	}

	// /output and /output/... (any method)
	r.Any("/output", WrapStdHandler(proxy))
	r.Any("/output/{path...}", WrapStdHandler(proxy))
}
