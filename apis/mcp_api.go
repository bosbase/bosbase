package apis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bosbase/bosbase-enterprise/apidocs"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/spf13/cast"
)

// MCP tools that expose the BosBase HTTP API to AI agents.
//
// The API requests are dispatched in-process through the same router that serves
// the regular HTTP traffic and with the caller's auth token, so all collection
// API rules, middlewares, hooks and validations apply exactly as for a direct
// HTTP call (including the custom routes registered with OnServe).

const (
	mcpApiStoreKey = "@mcpApiTarget"

	mcpApiRequestTimeout   = 120 * time.Second
	mcpApiMaxResponseBytes = 8 << 20
	mcpApiMaxTextBytes     = 1 << 20
	mcpApiMaxBinaryBytes   = 512 << 10
	mcpApiMaxDocBytes      = 60000
	mcpApiInternalHeader   = "X-Bosbase-Mcp"
)

// mcpApiTarget holds the router and the http handler used for the in-process API calls.
type mcpApiTarget struct {
	router  *router.Router[*core.RequestEvent]
	handler http.Handler
}

// setMcpApiTarget registers the router/handler pair used by the MCP API tools.
func setMcpApiTarget(app core.App, r *router.Router[*core.RequestEvent], handler http.Handler) {
	app.Store().Set(mcpApiStoreKey, &mcpApiTarget{router: r, handler: handler})
}

var mcpApiFallbackMu sync.Mutex

func getMcpApiTarget(app core.App) (*mcpApiTarget, error) {
	if t, ok := app.Store().Get(mcpApiStoreKey).(*mcpApiTarget); ok && t != nil {
		return t, nil
	}

	// the app was not started with apis.Serve (eg. tests) -> build a standalone router
	mcpApiFallbackMu.Lock()
	defer mcpApiFallbackMu.Unlock()

	if t, ok := app.Store().Get(mcpApiStoreKey).(*mcpApiTarget); ok && t != nil {
		return t, nil
	}

	r, err := NewRouter(app)
	if err != nil {
		return nil, err
	}
	handler, err := r.BuildMux()
	if err != nil {
		return nil, err
	}
	setMcpApiTarget(app, r, handler)

	return app.Store().Get(mcpApiStoreKey).(*mcpApiTarget), nil
}

// -------------------------------------------------------------------
// Tool definitions
// -------------------------------------------------------------------

var mcpApiTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	object := func(desc string) map[string]any { return map[string]any{"type": "object", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	filesProp := map[string]any{
		"type":        "array",
		"description": "Files to upload (sent as multipart/form-data; the JSON body is sent as the @jsonPayload field). Use \"field+\" to append to or \"field-\" to remove from a multi-file field.",
		"items": obj(map[string]any{
			"field":         str("Form field name (eg. the record file field)."),
			"filename":      str("File name."),
			"contentBase64": str("Base64 encoded file content."),
			"text":          str("Plain text file content (alternative to contentBase64)."),
			"contentType":   str("Optional MIME type."),
		}, "field", "filename"),
	}

	listProps := map[string]any{
		"collection": str("Collection name or id."),
		"page":       integer("Page number (default 1)."),
		"perPage":    integer("Items per page (default 30, max 1000)."),
		"filter":     str("Filter expression, eg. `status = \"active\" && created > \"2024-01-01\"`."),
		"sort":       str("Sort expression, eg. `-created,title`."),
		"expand":     str("Relations to expand, eg. `author,comments_via_post`."),
		"fields":     str("Comma separated fields to return."),
		"skipTotal":  boolean("Skip the total counts query (faster)."),
	}

	mcpApiTools = []mcpTool{
		{
			Name:        "api_endpoints",
			Description: "List the BosBase HTTP API endpoints available to the current token (method, path, required auth and summary). Use api_docs for the full documentation of an endpoint and api_request to call it.",
			InputSchema: obj(map[string]any{
				"search": str("Optional case-insensitive text matched against the path and summary (eg. \"records\", \"backup\", \"vector\")."),
			}),
			NoScopeArgs: true,
			ReadOnly:    true,
			Handler:     mcpApiEndpoints,
		},
		{
			Name:        "api_docs",
			Description: "Read the BosBase API reference. Pass path (and optionally method) for the documentation of specific endpoints, or topic for a whole chapter (\"overview\" documents auth, errors, filter/sort syntax and list query parameters).",
			InputSchema: obj(map[string]any{
				"path":   str("Endpoint path, eg. /api/collections/{collection}/records (concrete values like /api/collections/posts/records also work)."),
				"method": str("Optional HTTP method."),
				"topic": map[string]any{
					"type":        "string",
					"description": "Chapter to return when no path is given.",
					"enum":        mcpApiDocTopicNames(),
				},
			}),
			NoScopeArgs: true,
			ReadOnly:    true,
			Handler:     mcpApiDocs,
		},
		{
			Name:        "api_request",
			Description: "Call any BosBase HTTP API endpoint with the current auth token (records, collections, auth, files, batch, graphql, sql, settings, logs, backups, crons, vectors, llm-documents, langchaingo, cache, redis, scripts, custom routes...). Streaming endpoints (realtime/pubsub/SSE/WebSocket) are not supported.",
			InputSchema: obj(map[string]any{
				"method":  map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
				"path":    str("Request path starting with /, eg. /api/collections/posts/records. May contain a query string."),
				"query":   object("Query parameters (values may be strings, numbers, booleans or arrays)."),
				"body":    map[string]any{"description": "JSON request body."},
				"files":   filesProp,
				"headers": object("Optional extra request headers."),
			}, "method", "path"),
			NoScopeArgs: true,
			Handler:     mcpApiRequest,
		},
		{
			Name:        "records_list",
			Description: "List/search the records of a collection (respects the collection list rule).",
			InputSchema: obj(listProps, "collection"),
			NoScopeArgs: true,
			ReadOnly:    true,
			Handler:     mcpRecordsList,
		},
		{
			Name:        "record_get",
			Description: "Get a single record by id (respects the collection view rule).",
			InputSchema: obj(map[string]any{
				"collection": str("Collection name or id."),
				"id":         str("Record id."),
				"expand":     str("Relations to expand."),
				"fields":     str("Comma separated fields to return."),
			}, "collection", "id"),
			NoScopeArgs: true,
			ReadOnly:    true,
			Handler:     mcpRecordGet,
		},
		{
			Name:        "record_create",
			Description: "Create a record (respects the collection create rule). For auth collections include password and passwordConfirm.",
			InputSchema: obj(map[string]any{
				"collection": str("Collection name or id."),
				"data":       object("Record field values."),
				"files":      filesProp,
				"expand":     str("Relations to expand in the response."),
			}, "collection", "data"),
			NoScopeArgs: true,
			Handler:     mcpRecordCreate,
		},
		{
			Name:        "record_update",
			Description: "Update a record (respects the collection update rule). Supports field modifiers like \"count+\": 1 or \"tags-\": [\"x\"].",
			InputSchema: obj(map[string]any{
				"collection": str("Collection name or id."),
				"id":         str("Record id."),
				"data":       object("Field values to change."),
				"files":      filesProp,
				"expand":     str("Relations to expand in the response."),
			}, "collection", "id"),
			NoScopeArgs: true,
			Handler:     mcpRecordUpdate,
		},
		{
			Name:        "record_delete",
			Description: "Delete a record (respects the collection delete rule).",
			InputSchema: obj(map[string]any{
				"collection": str("Collection name or id."),
				"id":         str("Record id."),
			}, "collection", "id"),
			NoScopeArgs: true,
			Destructive: true,
			Handler:     mcpRecordDelete,
		},
		{
			Name:        "collections_list",
			Description: "List the collections with their fields, rules and indexes.",
			InputSchema: obj(map[string]any{
				"filter":  str("Filter expression, eg. `type = \"auth\"`."),
				"fields":  str("Comma separated fields to return, eg. `id,name,type`."),
				"page":    integer("Page number."),
				"perPage": integer("Items per page."),
			}),
			SuperuserOnly: true,
			NoScopeArgs:   true,
			ReadOnly:      true,
			Handler:       mcpCollectionsList,
		},
		{
			Name:        "collection_get",
			Description: "Get a collection definition (fields, rules, indexes) by name or id.",
			InputSchema: obj(map[string]any{
				"collection": str("Collection name or id."),
			}, "collection"),
			SuperuserOnly: true,
			NoScopeArgs:   true,
			ReadOnly:      true,
			Handler:       mcpCollectionGet,
		},
	}
}

// -------------------------------------------------------------------
// In-process dispatch
// -------------------------------------------------------------------

type mcpApiFile struct {
	Field         string  `json:"field"`
	Filename      string  `json:"filename"`
	ContentBase64 string  `json:"contentBase64"`
	Text          *string `json:"text"`
	ContentType   string  `json:"contentType"`
}

type mcpApiCall struct {
	Method  string
	Path    string
	Query   map[string]any
	Body    json.RawMessage
	Files   []mcpApiFile
	Headers map[string]string
}

// mcpResponseRecorder is a minimal buffered http.ResponseWriter.
type mcpResponseRecorder struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	truncated bool
}

func (r *mcpResponseRecorder) Header() http.Header { return r.header }

func (r *mcpResponseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *mcpResponseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if remaining := mcpApiMaxResponseBytes - r.body.Len(); remaining < len(b) {
		r.truncated = true
		if remaining > 0 {
			r.body.Write(b[:remaining])
		}
		return len(b), nil
	}
	return r.body.Write(b)
}

func (r *mcpResponseRecorder) Flush() {}

var mcpApiStreamingRoutes = regexp.MustCompile(`^/api/(realtime|pubsub)/?$|/execute/(sse|ws)/?$`)

func mcpApiValidatePath(method, path string) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return mcpToolErr("path must start with a single /, eg. /api/health.")
	}
	clean := strings.TrimRight(path, "/")
	if clean == "/api/mcp" || strings.HasPrefix(clean, "/api/mcp/") {
		return mcpToolErr("The MCP endpoint cannot be called through api_request.")
	}
	if method == http.MethodGet && mcpApiStreamingRoutes.MatchString(clean) {
		return mcpToolErr("Streaming endpoints (realtime, pubsub, SSE, WebSocket) are not supported over MCP.")
	}
	return nil
}

func mcpApiDispatch(e *core.RequestEvent, call *mcpApiCall) (map[string]any, error) {
	method := strings.ToUpper(strings.TrimSpace(call.Method))
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, mcpToolErr("method must be one of GET, POST, PUT, PATCH, DELETE.")
	}

	u, err := url.Parse(strings.TrimSpace(call.Path))
	if err != nil || u.Scheme != "" || u.Host != "" {
		return nil, mcpToolErr("path must be a relative URL path, eg. /api/health.")
	}
	if err := mcpApiValidatePath(method, u.Path); err != nil {
		return nil, err
	}

	q := u.Query()
	for k, v := range call.Query {
		switch vv := v.(type) {
		case []any:
			for _, item := range vv {
				q.Add(k, mcpApiQueryValue(item))
			}
		default:
			q.Set(k, mcpApiQueryValue(v))
		}
	}
	u.RawQuery = q.Encode()

	var body bytes.Buffer
	contentType := ""
	hasBody := len(call.Body) > 0 && string(call.Body) != "null"

	if len(call.Files) > 0 {
		mp := multipart.NewWriter(&body)
		if hasBody {
			if err := mp.WriteField(router.JSONPayloadKey, string(call.Body)); err != nil {
				return nil, err
			}
		}
		for i, f := range call.Files {
			if f.Field == "" || f.Filename == "" {
				return nil, mcpToolErr(fmt.Sprintf("files[%d]: field and filename are required.", i))
			}
			var content []byte
			if f.Text != nil {
				content = []byte(*f.Text)
			} else if content, err = base64.StdEncoding.DecodeString(f.ContentBase64); err != nil {
				return nil, mcpToolErr(fmt.Sprintf("files[%d]: invalid base64 content.", i))
			}
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, mcpEscapeQuotes(f.Field), mcpEscapeQuotes(f.Filename)))
			if f.ContentType != "" {
				h.Set("Content-Type", f.ContentType)
			} else {
				h.Set("Content-Type", http.DetectContentType(content))
			}
			part, err := mp.CreatePart(h)
			if err != nil {
				return nil, err
			}
			if _, err := part.Write(content); err != nil {
				return nil, err
			}
		}
		if err := mp.Close(); err != nil {
			return nil, err
		}
		contentType = mp.FormDataContentType()
	} else if hasBody {
		body.Write(call.Body)
		contentType = "application/json"
	}

	target, err := getMcpApiTarget(e.App)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(e.Request.Context(), mcpApiRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, u.RequestURI(), &body)
	if err != nil {
		return nil, mcpToolErr("Invalid request: " + err.Error())
	}
	req.Host = e.Request.Host
	req.RemoteAddr = e.Request.RemoteAddr
	req.TLS = e.Request.TLS
	for _, h := range []string{"User-Agent", "X-Forwarded-For", "X-Real-Ip", "X-Forwarded-Proto", "Accept-Language"} {
		if v := e.Request.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	for k, v := range call.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Authorization") == "" {
		if token := getAuthTokenFromRequest(e); token != "" {
			req.Header.Set("Authorization", token)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(mcpApiInternalHeader, "1")
	req.Header.Del("Accept-Encoding")

	rec := &mcpResponseRecorder{header: http.Header{}}
	target.handler.ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}

	return mcpApiResponseToMap(rec), nil
}

func mcpEscapeQuotes(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func mcpApiQueryValue(v any) string {
	switch vv := v.(type) {
	case string:
		return vv
	case map[string]any:
		raw, _ := json.Marshal(vv)
		return string(raw)
	default:
		return cast.ToString(v)
	}
}

func mcpApiResponseToMap(rec *mcpResponseRecorder) map[string]any {
	ct := rec.header.Get("Content-Type")
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	raw := rec.body.Bytes()

	result := map[string]any{
		"status":      rec.status,
		"ok":          rec.status >= 200 && rec.status < 300,
		"contentType": ct,
	}
	if cd := rec.header.Get("Content-Disposition"); cd != "" {
		result["contentDisposition"] = cd
	}

	truncated := rec.truncated
	switch {
	case len(raw) == 0:
		// no body
	case strings.Contains(mediaType, "json") && !rec.truncated && json.Valid(raw):
		result["body"] = json.RawMessage(raw)
	case mediaType == "" || strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "xml") ||
		strings.Contains(mediaType, "javascript") || strings.Contains(mediaType, "json"):
		if len(raw) > mcpApiMaxTextBytes {
			raw = raw[:mcpApiMaxTextBytes]
			truncated = true
		}
		if utf8.Valid(raw) {
			result["text"] = string(raw)
			break
		}
		fallthrough
	default:
		result["size"] = len(raw)
		if len(raw) > mcpApiMaxBinaryBytes {
			raw = raw[:mcpApiMaxBinaryBytes]
			truncated = true
		}
		result["base64"] = base64.StdEncoding.EncodeToString(raw)
	}
	if truncated {
		result["truncated"] = true
	}

	return result
}

// -------------------------------------------------------------------
// Endpoint catalog & docs
// -------------------------------------------------------------------

type mcpApiDocSection struct {
	Method  string
	Path    string
	Summary string
	Body    string
	Topic   string
}

type mcpApiDocIndex struct {
	sections []*mcpApiDocSection
	topics   map[string]string
}

var (
	mcpApiDocsOnce sync.Once
	mcpApiDocsIdx  *mcpApiDocIndex

	mcpApiDocHeadingRegex  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mcpApiEndpointRegex    = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE)\s+(/\S+)`)
	mcpApiPathParamRegex   = regexp.MustCompile(`\{[^}/]*\}`)
	mcpApiDocTopicFileName = regexp.MustCompile(`^\d+-(.+)\.md$`)
)

func mcpApiDocTopicNames() []string {
	idx := mcpLoadApiDocs()
	names := make([]string, 0, len(idx.topics))
	for name := range idx.topics {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func mcpLoadApiDocs() *mcpApiDocIndex {
	mcpApiDocsOnce.Do(func() {
		idx := &mcpApiDocIndex{topics: map[string]string{}}
		entries, _ := fs.ReadDir(apidocs.Reference, "api-reference")
		for _, entry := range entries {
			m := mcpApiDocTopicFileName.FindStringSubmatch(entry.Name())
			if m == nil {
				continue
			}
			raw, err := fs.ReadFile(apidocs.Reference, "api-reference/"+entry.Name())
			if err != nil {
				continue
			}
			idx.topics[m[1]] = string(raw)
			idx.sections = append(idx.sections, mcpParseDocSections(m[1], string(raw))...)
		}
		mcpApiDocsIdx = idx
	})
	return mcpApiDocsIdx
}

// mcpParseDocSections extracts the "## METHOD /path" sections of a markdown document.
func mcpParseDocSections(topic, content string) []*mcpApiDocSection {
	var result []*mcpApiDocSection

	var current *mcpApiDocSection
	var currentLevel int
	var buf strings.Builder

	flush := func() {
		if current == nil {
			return
		}
		current.Body = strings.TrimSpace(buf.String())
		current.Summary = mcpDocSummary(current.Body)
		result = append(result, current)
		current = nil
		buf.Reset()
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	inCode := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
		}
		if !inCode {
			if h := mcpApiDocHeadingRegex.FindStringSubmatch(line); h != nil {
				level := len(h[1])
				if current != nil && level <= currentLevel {
					flush()
				}
				if ep := mcpApiEndpointRegex.FindStringSubmatch(strings.TrimSpace(h[2])); ep != nil {
					current = &mcpApiDocSection{Method: ep[1], Path: ep[2], Topic: topic}
					currentLevel = level
				}
			}
		}
		if current != nil {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()

	return result
}

// mcpDocSummary returns the first paragraph line that is not a heading.
func mcpDocSummary(body string) string {
	lines := strings.Split(body, "\n")
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "|") || strings.HasPrefix(l, "```") {
			continue
		}
		if len(l) > 200 {
			l = l[:200] + "..."
		}
		return l
	}
	return ""
}

var mcpApiWildcardParamRegex = regexp.MustCompile(`\{[^}/]*\.\.\.\}`)

// mcpNormalizeRoutePath replaces the path parameter names so that
// "/a/{id}" and "/a/{recordId}" are considered equal.
func mcpNormalizeRoutePath(path string) string {
	path = mcpApiWildcardParamRegex.ReplaceAllString(path, "{...}")
	path = mcpApiPathParamRegex.ReplaceAllStringFunc(path, func(m string) string {
		if m == "{...}" || m == "{$}" {
			return m
		}
		return "{}"
	})
	path = strings.TrimSuffix(path, "{$}")
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

// mcpRoutePathMatches reports whether path (a concrete path or a route template)
// matches the route pattern.
func mcpRoutePathMatches(pattern, path string) bool {
	pp := strings.Split(mcpNormalizeRoutePath(pattern), "/")
	cp := strings.Split(mcpNormalizeRoutePath(path), "/")

	for i, seg := range pp {
		if seg == "{...}" {
			return len(cp) > i
		}
		if i >= len(cp) {
			return false
		}
		if seg != "{}" && seg != cp[i] {
			return false
		}
	}

	return len(pp) == len(cp)
}

func mcpRouteAuth(info router.RouteInfo) string {
	auth := "public"
	for _, id := range info.MiddlewareIds {
		switch id {
		case DefaultRequireSuperuserAuthMiddlewareId:
			return "superuser"
		case DefaultRequireSuperuserOrOwnerAuthMiddlewareId:
			auth = "superuser-or-owner"
		case DefaultRequireAuthMiddlewareId:
			if auth == "public" {
				auth = "auth"
			}
		case DefaultRequireGuestOnlyMiddlewareId:
			auth = "guest"
		}
	}
	return auth
}

type mcpApiEndpoint struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Auth       string `json:"auth"`
	Summary    string `json:"summary,omitempty"`
	Documented bool   `json:"documented"`
}

func mcpApiEndpointList(e *core.RequestEvent) ([]mcpApiEndpoint, error) {
	target, err := getMcpApiTarget(e.App)
	if err != nil {
		return nil, err
	}

	docs := mcpLoadApiDocs()
	superuser := e.HasSuperuserAuth()
	seen := map[string]struct{}{}
	result := []mcpApiEndpoint{}

	for _, info := range target.router.Routes() {
		if !strings.HasPrefix(info.Path, "/api/") {
			continue
		}
		methods := []string{info.Method}
		if info.Method == "" {
			methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
		}
		for _, method := range methods {
			if method == http.MethodHead || mcpApiValidatePath(method, info.Path) != nil {
				continue
			}
			key := method + " " + mcpNormalizeRoutePath(info.Path)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			auth := mcpRouteAuth(info)
			if auth == "superuser" && !superuser {
				continue
			}

			ep := mcpApiEndpoint{Method: method, Path: info.Path, Auth: auth}
			normalized := mcpNormalizeRoutePath(info.Path)
			for _, s := range docs.sections {
				if mcpNormalizeRoutePath(s.Path) != normalized {
					continue
				}
				// prefer the section of the same method, fallback to any method of the same path
				if s.Method == method || !ep.Documented {
					ep.Summary = s.Summary
					ep.Documented = true
				}
				if s.Method == method {
					break
				}
			}
			result = append(result, ep)
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Method < result[j].Method
		}
		return result[i].Path < result[j].Path
	})

	return result, nil
}

// -------------------------------------------------------------------
// Handlers
// -------------------------------------------------------------------

func mcpApiEndpoints(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Search string `json:"search"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	all, err := mcpApiEndpointList(e)
	if err != nil {
		return nil, err
	}

	search := strings.ToLower(strings.TrimSpace(payload.Search))
	items := make([]mcpApiEndpoint, 0, len(all))
	for _, ep := range all {
		if search != "" &&
			!strings.Contains(strings.ToLower(ep.Path), search) &&
			!strings.Contains(strings.ToLower(ep.Summary), search) &&
			!strings.Contains(strings.ToLower(ep.Method), search) {
			continue
		}
		items = append(items, ep)
	}

	return map[string]any{
		"count":     len(items),
		"endpoints": items,
		"authLevels": map[string]string{
			"public":             "no token required (collection API rules may still apply)",
			"auth":               "any authenticated record",
			"superuser":          "superusers only",
			"superuser-or-owner": "superusers or the owner of the resource",
			"guest":              "only without auth token",
		},
	}, nil
}

func mcpApiDocs(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Path   string `json:"path"`
		Method string `json:"method"`
		Topic  string `json:"topic"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	docs := mcpLoadApiDocs()

	path := strings.TrimSpace(payload.Path)
	if path == "" {
		topic := strings.TrimSpace(payload.Topic)
		if topic == "" {
			topic = "overview"
		}
		content, ok := docs.topics[topic]
		if !ok {
			return nil, mcpToolErr("Unknown topic. Available topics: " + strings.Join(mcpApiDocTopicNames(), ", "))
		}
		return mcpTruncateDoc(map[string]any{"topic": topic, "markdown": content}), nil
	}

	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	method := strings.ToUpper(strings.TrimSpace(payload.Method))

	var parts []string
	for _, s := range docs.sections {
		if method != "" && s.Method != method {
			continue
		}
		if mcpRoutePathMatches(s.Path, path) {
			parts = append(parts, s.Body)
		}
	}

	if len(parts) == 0 {
		// fallback to the live route info for undocumented endpoints
		all, err := mcpApiEndpointList(e)
		if err != nil {
			return nil, err
		}
		var matches []mcpApiEndpoint
		for _, ep := range all {
			if (method == "" || ep.Method == method) && mcpRoutePathMatches(ep.Path, path) {
				matches = append(matches, ep)
			}
		}
		if len(matches) == 0 {
			return nil, mcpToolErr("No endpoint matches " + strings.TrimSpace(method+" "+path) + ". Use api_endpoints to list the available endpoints.")
		}
		return map[string]any{
			"path":      path,
			"endpoints": matches,
			"markdown":  "No detailed documentation is available for this endpoint. See topic \"overview\" for the common conventions.",
		}, nil
	}

	return mcpTruncateDoc(map[string]any{"path": path, "markdown": strings.Join(parts, "\n\n---\n\n")}), nil
}

func mcpTruncateDoc(result map[string]any) map[string]any {
	if md, _ := result["markdown"].(string); len(md) > mcpApiMaxDocBytes {
		result["markdown"] = md[:mcpApiMaxDocBytes]
		result["truncated"] = true
	}
	return result
}

func mcpApiRequest(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Query   map[string]any    `json:"query"`
		Body    json.RawMessage   `json:"body"`
		Files   []mcpApiFile      `json:"files"`
		Headers map[string]string `json:"headers"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	return mcpApiDispatch(e, &mcpApiCall{
		Method:  payload.Method,
		Path:    payload.Path,
		Query:   payload.Query,
		Body:    payload.Body,
		Files:   payload.Files,
		Headers: payload.Headers,
	})
}

// mcpApiResult unwraps a dispatched response for the typed tools:
// successful responses return their JSON body, failures are reported as tool errors.
func mcpApiResult(resp map[string]any) (any, error) {
	if ok, _ := resp["ok"].(bool); !ok {
		msg := fmt.Sprintf("Request failed with status %v", resp["status"])
		if body, ok := resp["body"].(json.RawMessage); ok {
			msg += ": " + string(body)
		} else if text, ok := resp["text"].(string); ok && text != "" {
			msg += ": " + text
		}
		return nil, mcpToolErr(msg)
	}

	body, ok := resp["body"].(json.RawMessage)
	if !ok {
		return map[string]any{"status": resp["status"]}, nil
	}

	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if _, isMap := out.(map[string]any); !isMap {
		return map[string]any{"items": out}, nil
	}
	return out, nil
}

func mcpCollectionPath(collection string, rest ...string) (string, error) {
	collection = strings.TrimSpace(collection)
	if collection == "" {
		return "", mcpToolErr("collection is required.")
	}
	p := "/api/collections/" + url.PathEscape(collection)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p, nil
}

func mcpRecordsList(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Collection string `json:"collection"`
		Page       int    `json:"page"`
		PerPage    int    `json:"perPage"`
		Filter     string `json:"filter"`
		Sort       string `json:"sort"`
		Expand     string `json:"expand"`
		Fields     string `json:"fields"`
		SkipTotal  bool   `json:"skipTotal"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	path, err := mcpCollectionPath(payload.Collection)
	if err != nil {
		return nil, err
	}

	query := map[string]any{}
	if payload.Page > 0 {
		query["page"] = payload.Page
	}
	if payload.PerPage > 0 {
		query["perPage"] = payload.PerPage
	}
	for k, v := range map[string]string{"filter": payload.Filter, "sort": payload.Sort, "expand": payload.Expand, "fields": payload.Fields} {
		if v != "" {
			query[k] = v
		}
	}
	if payload.SkipTotal {
		query["skipTotal"] = true
	}

	resp, err := mcpApiDispatch(e, &mcpApiCall{Method: http.MethodGet, Path: path + "/records", Query: query})
	if err != nil {
		return nil, err
	}
	return mcpApiResult(resp)
}

func mcpRecordGet(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Collection string `json:"collection"`
		Id         string `json:"id"`
		Expand     string `json:"expand"`
		Fields     string `json:"fields"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if payload.Id == "" {
		return nil, mcpToolErr("id is required.")
	}
	path, err := mcpCollectionPath(payload.Collection, "records", payload.Id)
	if err != nil {
		return nil, err
	}

	query := map[string]any{}
	if payload.Expand != "" {
		query["expand"] = payload.Expand
	}
	if payload.Fields != "" {
		query["fields"] = payload.Fields
	}

	resp, err := mcpApiDispatch(e, &mcpApiCall{Method: http.MethodGet, Path: path, Query: query})
	if err != nil {
		return nil, err
	}
	return mcpApiResult(resp)
}

func mcpRecordWrite(e *core.RequestEvent, args json.RawMessage, update bool) (any, error) {
	payload := struct {
		Collection string          `json:"collection"`
		Id         string          `json:"id"`
		Data       json.RawMessage `json:"data"`
		Files      []mcpApiFile    `json:"files"`
		Expand     string          `json:"expand"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	method := http.MethodPost
	rest := []string{"records"}
	if update {
		if payload.Id == "" {
			return nil, mcpToolErr("id is required.")
		}
		method = http.MethodPatch
		rest = append(rest, payload.Id)
	}
	path, err := mcpCollectionPath(payload.Collection, rest...)
	if err != nil {
		return nil, err
	}

	query := map[string]any{}
	if payload.Expand != "" {
		query["expand"] = payload.Expand
	}

	resp, err := mcpApiDispatch(e, &mcpApiCall{Method: method, Path: path, Query: query, Body: payload.Data, Files: payload.Files})
	if err != nil {
		return nil, err
	}
	return mcpApiResult(resp)
}

func mcpRecordCreate(e *core.RequestEvent, args json.RawMessage) (any, error) {
	return mcpRecordWrite(e, args, false)
}

func mcpRecordUpdate(e *core.RequestEvent, args json.RawMessage) (any, error) {
	return mcpRecordWrite(e, args, true)
}

func mcpRecordDelete(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Collection string `json:"collection"`
		Id         string `json:"id"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if payload.Id == "" {
		return nil, mcpToolErr("id is required.")
	}
	path, err := mcpCollectionPath(payload.Collection, "records", payload.Id)
	if err != nil {
		return nil, err
	}

	resp, err := mcpApiDispatch(e, &mcpApiCall{Method: http.MethodDelete, Path: path})
	if err != nil {
		return nil, err
	}
	if _, err := mcpApiResult(resp); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": payload.Id}, nil
}

func mcpCollectionsList(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Filter  string `json:"filter"`
		Fields  string `json:"fields"`
		Page    int    `json:"page"`
		PerPage int    `json:"perPage"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	query := map[string]any{}
	if payload.Filter != "" {
		query["filter"] = payload.Filter
	}
	if payload.Fields != "" {
		query["fields"] = payload.Fields
	}
	if payload.Page > 0 {
		query["page"] = payload.Page
	}
	if payload.PerPage > 0 {
		query["perPage"] = payload.PerPage
	}

	resp, err := mcpApiDispatch(e, &mcpApiCall{Method: http.MethodGet, Path: "/api/collections", Query: query})
	if err != nil {
		return nil, err
	}
	return mcpApiResult(resp)
}

func mcpCollectionGet(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Collection string `json:"collection"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	path, err := mcpCollectionPath(payload.Collection)
	if err != nil {
		return nil, err
	}

	resp, err := mcpApiDispatch(e, &mcpApiCall{Method: http.MethodGet, Path: path})
	if err != nil {
		return nil, err
	}
	return mcpApiResult(resp)
}
