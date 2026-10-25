package apis

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/spf13/cast"
)

// LLM relay: users (or AI agents) store third party LLM channels (base URL + API key)
// and call them through BosBase with their BosBase token. BosBase injects the
// channel credentials, streams the response back and records the usage.
//
// Any OpenAI compatible SDK can be pointed to {bosbase}/api/llm/v1 using the
// BosBase auth token as API key.

const (
	llmChannelsTable = "_llm_channels"
	llmRequestsTable = "_llm_requests"

	llmChannelHeader       = "X-Bosbase-Channel"
	llmDefaultTimeout      = 10 * time.Minute
	llmMaxBodySize         = 64 << 20
	llmUsageCaptureLimit   = 4 << 20
	llmDefaultAnthropicVer = "2023-06-01"
)

var llmChannelTypes = []string{"openai", "anthropic"}

func bindLLMRelayApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/llm").Bind(RequireAuth())

	sub.GET("/channels", llmChannelsList)
	sub.POST("/channels", llmChannelCreate)
	sub.GET("/channels/{id}", llmChannelView)
	sub.PATCH("/channels/{id}", llmChannelUpdate)
	sub.DELETE("/channels/{id}", llmChannelDelete)
	sub.POST("/channels/{id}/test", llmChannelTest)
	sub.Any("/channels/{id}/proxy/{path...}", llmChannelProxy).Bind(BodyLimit(llmMaxBodySize))

	sub.GET("/requests", llmRequestsList)
	sub.GET("/usage", llmUsage)

	sub.GET("/v1/models", llmModels)
	sub.Any("/v1/{path...}", llmRelay).Bind(BodyLimit(llmMaxBodySize))
}

func llmRelayTimeout() time.Duration {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("LLM_RELAY_TIMEOUT_SECONDS"))); err == nil && v > 0 {
		return time.Duration(v) * time.Second
	}
	return llmDefaultTimeout
}

// -------------------------------------------------------------------
// Channels
// -------------------------------------------------------------------

type llmChannel struct {
	Id              string `db:"id"`
	Owner           string `db:"owner"`
	OwnerCollection string `db:"ownerCollection"`
	Name            string `db:"name"`
	Type            string `db:"type"`
	BaseUrl         string `db:"baseUrl"`
	ApiKey          string `db:"apiKey"`
	Headers         string `db:"headers"`
	Models          string `db:"models"`
	DefaultModel    string `db:"defaultModel"`
	IsDefault       bool   `db:"isDefault"`
	Enabled         bool   `db:"enabled"`
	Created         string `db:"created"`
	Updated         string `db:"updated"`
}

const llmChannelColumns = `[[id]], [[owner]], [[ownerCollection]], [[name]], [[type]], [[baseUrl]], [[apiKey]], [[headers]],
	[[models]]::text AS [[models]], [[defaultModel]], [[isDefault]], [[enabled]],
	[[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

func (c *llmChannel) models() []string {
	models := []string{}
	_ = json.Unmarshal([]byte(c.Models), &models)
	return models
}

func (c *llmChannel) headers(app core.App) map[string]string {
	headers := map[string]string{}
	raw, err := decryptIntegrationSecret(app, c.Headers)
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &headers)
	}
	return headers
}

func (c *llmChannel) public(app core.App) map[string]any {
	apiKey, _ := decryptIntegrationSecret(app, c.ApiKey)
	headerNames := []string{}
	for k := range c.headers(app) {
		headerNames = append(headerNames, k)
	}
	return map[string]any{
		"id":           c.Id,
		"name":         c.Name,
		"type":         c.Type,
		"baseUrl":      c.BaseUrl,
		"apiKey":       maskSecret(apiKey),
		"apiKeySet":    c.ApiKey != "",
		"headers":      headerNames,
		"models":       c.models(),
		"defaultModel": c.DefaultModel,
		"isDefault":    c.IsDefault,
		"enabled":      c.Enabled,
		"shared":       c.Owner == "",
		"owner":        c.Owner,
		"created":      c.Created,
		"updated":      c.Updated,
	}
}

func llmLoadChannel(app core.App, where string, params dbx.Params) (*llmChannel, error) {
	c := &llmChannel{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s LIMIT 1", llmChannelColumns, llmChannelsTable, where,
	)).Bind(params).One(c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// llmFindChannel resolves the channel to use for a request.
//
// ref (id or name) has priority, then a channel whose models list contains the model,
// then the default channel (own defaults win over the shared ones).
func llmFindChannel(e *core.RequestEvent, ref string, model string) (*llmChannel, error) {
	params := dbx.Params{"me": e.Auth.Id, "ref": strings.TrimSpace(ref), "model": model}
	visible := "([[owner]] = {:me} OR [[owner]] = '') AND [[enabled]] = TRUE"
	if e.HasSuperuserAuth() && ref != "" {
		visible = "[[enabled]] = TRUE"
	}

	if strings.TrimSpace(ref) != "" {
		c, err := llmLoadChannel(e.App, visible+" AND ([[id]] = {:ref} OR LOWER([[name]]) = LOWER({:ref})) ORDER BY ([[id]] = {:ref}) DESC, ([[owner]] = {:me}) DESC", params)
		if err != nil {
			return nil, e.NotFoundError("LLM channel "+strconv.Quote(ref)+" was not found or is disabled.", nil)
		}
		return c, nil
	}

	if model != "" {
		if c, err := llmLoadChannel(e.App, visible+" AND [[models]] @> jsonb_build_array({:model}::text) ORDER BY ([[owner]] = {:me}) DESC, [[isDefault]] DESC, [[created]]", params); err == nil {
			return c, nil
		}
	}

	c, err := llmLoadChannel(e.App, visible+" ORDER BY ([[owner]] = {:me}) DESC, [[isDefault]] DESC, [[created]]", params)
	if err != nil {
		return nil, e.BadRequestError("No LLM channel is configured. Create one with POST /api/llm/channels.", nil)
	}
	return c, nil
}

type llmChannelPayload struct {
	Name         *string            `json:"name"`
	Type         *string            `json:"type"`
	BaseUrl      *string            `json:"baseUrl"`
	ApiKey       *string            `json:"apiKey"`
	Headers      *map[string]string `json:"headers"`
	Models       *[]string          `json:"models"`
	DefaultModel *string            `json:"defaultModel"`
	IsDefault    *bool              `json:"isDefault"`
	Enabled      *bool              `json:"enabled"`
	Shared       bool               `json:"shared"`
	OwnerId      string             `json:"ownerId"`
}

func applyLLMChannelPayload(e *core.RequestEvent, c *llmChannel, payload *llmChannelPayload) error {
	if payload.Name != nil {
		c.Name = strings.TrimSpace(*payload.Name)
	}
	if payload.Type != nil {
		c.Type = strings.ToLower(strings.TrimSpace(*payload.Type))
	}
	if payload.BaseUrl != nil {
		c.BaseUrl = strings.TrimSpace(*payload.BaseUrl)
	}
	if payload.DefaultModel != nil {
		c.DefaultModel = strings.TrimSpace(*payload.DefaultModel)
	}
	if payload.IsDefault != nil {
		c.IsDefault = *payload.IsDefault
	}
	if payload.Enabled != nil {
		c.Enabled = *payload.Enabled
	}
	if payload.Models != nil {
		raw, _ := json.Marshal(normalizeTags(*payload.Models))
		c.Models = string(raw)
	}
	if c.Models == "" {
		c.Models = "[]"
	}

	if c.Type == "" {
		c.Type = "openai"
	}
	if !slicesContains(llmChannelTypes, c.Type) {
		return e.BadRequestError("type must be one of "+strings.Join(llmChannelTypes, ", ")+".", nil)
	}
	if c.Name == "" {
		return e.BadRequestError("name is required.", nil)
	}
	if c.BaseUrl == "" && c.Type == "anthropic" {
		c.BaseUrl = "https://api.anthropic.com/v1"
	}
	baseUrl, err := validateUpstreamURL(c.BaseUrl, false)
	if err != nil {
		return e.BadRequestError("Invalid baseUrl: "+err.Error(), nil)
	}
	c.BaseUrl = baseUrl

	if payload.ApiKey != nil {
		enc, err := encryptIntegrationSecret(e.App, strings.TrimSpace(*payload.ApiKey))
		if err != nil {
			return err
		}
		c.ApiKey = enc
	}
	if payload.Headers != nil {
		raw := ""
		if len(*payload.Headers) > 0 {
			b, _ := json.Marshal(*payload.Headers)
			raw = string(b)
		}
		enc, err := encryptIntegrationSecret(e.App, raw)
		if err != nil {
			return err
		}
		c.Headers = enc
	}

	return nil
}

func llmSaveChannel(e *core.RequestEvent, c *llmChannel, isNew bool) error {
	params := dbx.Params{
		"owner":           c.Owner,
		"ownerCollection": c.OwnerCollection,
		"name":            c.Name,
		"type":            c.Type,
		"baseUrl":         c.BaseUrl,
		"apiKey":          c.ApiKey,
		"headers":         c.Headers,
		"models":          c.Models,
		"defaultModel":    c.DefaultModel,
		"isDefault":       c.IsDefault,
		"enabled":         c.Enabled,
	}

	err := e.App.RunInTransaction(func(txApp core.App) error {
		if c.IsDefault {
			if _, err := txApp.DB().Update(llmChannelsTable, dbx.Params{"isDefault": false}, dbx.HashExp{"owner": c.Owner}).Execute(); err != nil {
				return err
			}
		}
		if isNew {
			return txApp.DB().NewQuery(fmt.Sprintf(`
				INSERT INTO {{%s}} ([[owner]], [[ownerCollection]], [[name]], [[type]], [[baseUrl]], [[apiKey]], [[headers]], [[models]], [[defaultModel]], [[isDefault]], [[enabled]])
				VALUES ({:owner}, {:ownerCollection}, {:name}, {:type}, {:baseUrl}, {:apiKey}, {:headers}, {:models}::jsonb, {:defaultModel}, {:isDefault}, {:enabled})
				RETURNING [[id]]`, llmChannelsTable)).Bind(params).Row(&c.Id)
		}
		params["id"] = c.Id
		_, err := txApp.DB().NewQuery(fmt.Sprintf(`
			UPDATE {{%s}} SET [[name]] = {:name}, [[type]] = {:type}, [[baseUrl]] = {:baseUrl}, [[apiKey]] = {:apiKey},
				[[headers]] = {:headers}, [[models]] = {:models}::jsonb, [[defaultModel]] = {:defaultModel},
				[[isDefault]] = {:isDefault}, [[enabled]] = {:enabled}, [[updated]] = NOW()
			WHERE [[id]] = {:id}`, llmChannelsTable)).Bind(params).Execute()
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "idx__llm_channels_name") {
			return e.BadRequestError("An LLM channel named "+strconv.Quote(c.Name)+" already exists.", nil)
		}
		return e.InternalServerError("Failed to save the LLM channel.", err)
	}
	return nil
}

func llmChannelsList(e *core.RequestEvent) error {
	params := dbx.Params{"me": e.Auth.Id}
	where := "[[owner]] = {:me} OR [[owner]] = ''"
	if e.HasSuperuserAuth() && e.Request.URL.Query().Get("all") == "true" {
		where = "TRUE"
	}

	channels := []*llmChannel{}
	err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY ([[owner]] = {:me}) DESC, [[isDefault]] DESC, LOWER([[name]])",
		llmChannelColumns, llmChannelsTable, where,
	)).Bind(params).All(&channels)
	if err != nil {
		return e.InternalServerError("Failed to load the LLM channels.", err)
	}

	items := make([]map[string]any, len(channels))
	for i, c := range channels {
		items[i] = c.public(e.App)
	}
	return e.JSON(http.StatusOK, map[string]any{"items": items})
}

func llmChannelCreate(e *core.RequestEvent) error {
	payload := &llmChannelPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}

	owner, err := resolveIntegrationOwner(e, payload.Shared, payload.OwnerId)
	if err != nil {
		return err
	}

	c := &llmChannel{Owner: owner.id, OwnerCollection: owner.collection, Enabled: true}
	if err := applyLLMChannelPayload(e, c, payload); err != nil {
		return err
	}
	if err := llmSaveChannel(e, c, true); err != nil {
		return err
	}

	saved, err := llmLoadChannel(e.App, "[[id]] = {:id}", dbx.Params{"id": c.Id})
	if err != nil {
		return e.InternalServerError("Failed to load the LLM channel.", err)
	}
	return e.JSON(http.StatusOK, saved.public(e.App))
}

func llmManagedChannel(e *core.RequestEvent) (*llmChannel, error) {
	c, err := llmLoadChannel(e.App, "[[id]] = {:id}", dbx.Params{"id": e.Request.PathValue("id")})
	if err != nil || (!canManageIntegration(e, c.Owner) && c.Owner != "") {
		return nil, e.NotFoundError("LLM channel not found.", nil)
	}
	if !canManageIntegration(e, c.Owner) {
		return nil, e.ForbiddenError("Only superusers can modify shared LLM channels.", nil)
	}
	return c, nil
}

func llmChannelView(e *core.RequestEvent) error {
	c, err := llmLoadChannel(e.App, "[[id]] = {:id}", dbx.Params{"id": e.Request.PathValue("id")})
	if err != nil || !(c.Owner == "" || c.Owner == e.Auth.Id || e.HasSuperuserAuth()) {
		return e.NotFoundError("LLM channel not found.", nil)
	}
	return e.JSON(http.StatusOK, c.public(e.App))
}

func llmChannelUpdate(e *core.RequestEvent) error {
	c, err := llmManagedChannel(e)
	if err != nil {
		return err
	}

	payload := &llmChannelPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	if err := applyLLMChannelPayload(e, c, payload); err != nil {
		return err
	}
	if err := llmSaveChannel(e, c, false); err != nil {
		return err
	}

	saved, err := llmLoadChannel(e.App, "[[id]] = {:id}", dbx.Params{"id": c.Id})
	if err != nil {
		return e.InternalServerError("Failed to load the LLM channel.", err)
	}
	return e.JSON(http.StatusOK, saved.public(e.App))
}

func llmChannelDelete(e *core.RequestEvent) error {
	c, err := llmManagedChannel(e)
	if err != nil {
		return err
	}
	if _, err := e.App.DB().Delete(llmChannelsTable, dbx.HashExp{"id": c.Id}).Execute(); err != nil {
		return e.InternalServerError("Failed to delete the LLM channel.", err)
	}
	return e.NoContent(http.StatusNoContent)
}

// -------------------------------------------------------------------
// Relay
// -------------------------------------------------------------------

var llmForwardRequestHeaders = []string{
	"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta", "Openai-Organization", "Openai-Project",
	"Http-Referer", "X-Title",
}

var llmForwardResponseHeaders = []string{
	"Content-Type", "Cache-Control", "Retry-After", "X-Request-Id", "Request-Id", "Openai-Processing-Ms",
	"X-Ratelimit-Limit-Requests", "X-Ratelimit-Remaining-Requests", "X-Ratelimit-Limit-Tokens", "X-Ratelimit-Remaining-Tokens",
	"Anthropic-Ratelimit-Requests-Remaining", "Anthropic-Ratelimit-Tokens-Remaining",
}

type llmUsageInfo struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func (u *llmUsageInfo) merge(raw map[string]any) {
	if raw == nil {
		return
	}
	pick := func(keys ...string) int {
		for _, k := range keys {
			if v, ok := raw[k]; ok && v != nil {
				return cast.ToInt(v)
			}
		}
		return 0
	}
	if v := pick("prompt_tokens", "input_tokens"); v > u.PromptTokens {
		u.PromptTokens = v
	}
	if v := pick("completion_tokens", "output_tokens"); v > u.CompletionTokens {
		u.CompletionTokens = v
	}
	if v := pick("total_tokens"); v > u.TotalTokens {
		u.TotalTokens = v
	}
}

// parseLLMUsage extracts the token usage from a JSON or SSE response body.
func parseLLMUsage(body []byte, stream bool) llmUsageInfo {
	usage := llmUsageInfo{}

	extract := func(raw []byte) {
		obj := map[string]any{}
		if json.Unmarshal(raw, &obj) != nil {
			return
		}
		if u, ok := obj["usage"].(map[string]any); ok {
			usage.merge(u)
		}
		// anthropic message_start event
		if msg, ok := obj["message"].(map[string]any); ok {
			if u, ok := msg["usage"].(map[string]any); ok {
				usage.merge(u)
			}
		}
	}

	if stream {
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 64*1024), 4<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				data = strings.TrimSpace(data)
				if data != "" && data != "[DONE]" && strings.Contains(data, "usage") {
					extract([]byte(data))
				}
			}
		}
	} else {
		extract(body)
	}

	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}

// llmCaptureWriter keeps the first N bytes written (used to parse the usage).
type llmCaptureWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *llmCaptureWriter) Write(p []byte) (int, error) {
	if remaining := w.limit - w.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			w.buf.Write(p[:remaining])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

// llmForward sends the request to the channel and streams the response back to the client.
func llmForward(e *core.RequestEvent, c *llmChannel, upstreamPath string, body []byte, model string, stream bool) error {
	started := time.Now()

	apiKey, err := decryptIntegrationSecret(e.App, c.ApiKey)
	if err != nil {
		return e.InternalServerError("Failed to decrypt the channel API key.", err)
	}

	target := strings.TrimRight(c.BaseUrl, "/") + "/" + strings.TrimLeft(upstreamPath, "/")
	query := e.Request.URL.Query()
	query.Del("channel")
	query.Del("token")
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	ctx, cancel := requestContextWithTimeout(e, llmRelayTimeout())
	defer cancel()

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, e.Request.Method, target, reqBody)
	if err != nil {
		return e.BadRequestError("Invalid upstream request.", err)
	}
	for _, h := range llmForwardRequestHeaders {
		if v := e.Request.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.headers(e.App) {
		req.Header.Set(k, v)
	}
	switch c.Type {
	case "anthropic":
		if apiKey != "" {
			req.Header.Set("x-api-key", apiKey)
		}
		if req.Header.Get("Anthropic-Version") == "" {
			req.Header.Set("Anthropic-Version", llmDefaultAnthropicVer)
		}
	default:
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	client := newIntegrationHTTPClient(isTrustedIntegrationOwner(c.Owner, c.OwnerCollection), 0)
	resp, err := client.Do(req)
	if err != nil {
		llmLogRequest(e, c, upstreamPath, model, 0, stream, llmUsageInfo{}, time.Since(started), err.Error())
		if isPrivateUpstreamErr(err) {
			return e.BadRequestError("The channel points to a private network address which is not allowed (set BOSBASE_ALLOW_PRIVATE_UPSTREAMS=true to allow it).", nil)
		}
		return router.NewApiError(http.StatusBadGateway, "Failed to reach the LLM channel: "+err.Error(), nil)
	}
	defer resp.Body.Close()

	for _, h := range llmForwardResponseHeaders {
		if v := resp.Header.Get(h); v != "" {
			e.Response.Header().Set(h, v)
		}
	}
	e.Response.Header().Set("X-Bosbase-Channel", c.Name)
	isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	if isSSE {
		e.Response.Header().Set("X-Accel-Buffering", "no")
	}
	e.Response.WriteHeader(resp.StatusCode)

	capture := &llmCaptureWriter{limit: llmUsageCaptureLimit}
	flusher, _ := e.Response.(http.Flusher)
	buf := make([]byte, 32*1024)
	var copyErr error
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			capture.Write(chunk)
			if _, err := e.Response.Write(chunk); err != nil {
				copyErr = err
				break
			}
			if isSSE && flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				copyErr = readErr
			}
			break
		}
	}

	usage := parseLLMUsage(capture.buf.Bytes(), isSSE)
	errMsg := ""
	if resp.StatusCode >= 400 {
		errMsg = mcpTruncate(capture.buf.String(), 1000)
	} else if copyErr != nil {
		errMsg = copyErr.Error()
	}
	llmLogRequest(e, c, upstreamPath, model, resp.StatusCode, stream || isSSE, usage, time.Since(started), errMsg)

	return nil
}

func llmLogRequest(e *core.RequestEvent, c *llmChannel, endpoint, model string, status int, stream bool, usage llmUsageInfo, d time.Duration, errMsg string) {
	_, err := e.App.DB().Insert(llmRequestsTable, dbx.Params{
		"owner":            e.Auth.Id,
		"ownerCollection":  e.Auth.Collection().Name,
		"channel":          c.Id,
		"channelName":      c.Name,
		"model":            model,
		"endpoint":         "/" + strings.TrimLeft(endpoint, "/"),
		"status":           status,
		"stream":           stream,
		"promptTokens":     usage.PromptTokens,
		"completionTokens": usage.CompletionTokens,
		"totalTokens":      usage.TotalTokens,
		"durationMs":       d.Milliseconds(),
		"error":            errMsg,
	}).Execute()
	if err != nil {
		e.App.Logger().Warn("Failed to log the LLM relay request", "error", err)
	}
}

// llmPrepareBody reads the JSON body, picks the channel and applies the model defaults.
func llmPrepareBody(e *core.RequestEvent, channelRef string) (*llmChannel, []byte, string, bool, error) {
	var body []byte
	if e.Request.Method != http.MethodGet && e.Request.Method != http.MethodHead && e.Request.Body != nil {
		raw, err := io.ReadAll(e.Request.Body)
		if err != nil {
			return nil, nil, "", false, e.BadRequestError("Failed to read the request body.", err)
		}
		body = raw
	}

	if channelRef == "" {
		channelRef = e.Request.Header.Get(llmChannelHeader)
	}
	if channelRef == "" {
		channelRef = e.Request.URL.Query().Get("channel")
	}

	var payload map[string]any
	isJSON := len(body) > 0 && json.Unmarshal(body, &payload) == nil && payload != nil
	model, stream := "", false
	if isJSON {
		if ch, ok := payload["channel"].(string); ok {
			if channelRef == "" {
				channelRef = ch
			}
			delete(payload, "channel")
		}
		model, _ = payload["model"].(string)
		stream, _ = payload["stream"].(bool)
	}

	c, err := llmFindChannel(e, channelRef, model)
	if err != nil {
		return nil, nil, "", false, err
	}

	if isJSON {
		if model == "" && c.DefaultModel != "" {
			model = c.DefaultModel
			payload["model"] = model
		}
		if models := c.models(); model != "" && len(models) > 0 && !slicesContains(models, model) {
			return nil, nil, "", false, e.BadRequestError("Model "+strconv.Quote(model)+" is not enabled for channel "+strconv.Quote(c.Name)+".", nil)
		}
		body, _ = json.Marshal(payload)
	}

	return c, body, model, stream, nil
}

func llmRelay(e *core.RequestEvent) error {
	c, body, model, stream, err := llmPrepareBody(e, "")
	if err != nil {
		return err
	}
	return llmForward(e, c, e.Request.PathValue("path"), body, model, stream)
}

func llmChannelProxy(e *core.RequestEvent) error {
	c, body, model, stream, err := llmPrepareBody(e, e.Request.PathValue("id"))
	if err != nil {
		return err
	}
	return llmForward(e, c, e.Request.PathValue("path"), body, model, stream)
}

func llmModels(e *core.RequestEvent) error {
	ref := e.Request.Header.Get(llmChannelHeader)
	if ref == "" {
		ref = e.Request.URL.Query().Get("channel")
	}
	c, err := llmFindChannel(e, ref, "")
	if err != nil {
		return err
	}

	// channels with an explicit allow list don't need an upstream call
	if models := c.models(); len(models) > 0 {
		data := make([]map[string]any, len(models))
		for i, m := range models {
			data[i] = map[string]any{"id": m, "object": "model", "owned_by": c.Name}
		}
		return e.JSON(http.StatusOK, map[string]any{"object": "list", "data": data})
	}

	return llmForward(e, c, "models", nil, "", false)
}

func llmChannelTest(e *core.RequestEvent) error {
	c, err := llmFindChannel(e, e.Request.PathValue("id"), "")
	if err != nil {
		return err
	}

	apiKey, err := decryptIntegrationSecret(e.App, c.ApiKey)
	if err != nil {
		return e.InternalServerError("Failed to decrypt the channel API key.", err)
	}

	ctx, cancel := requestContextWithTimeout(e, 30*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseUrl, "/")+"/models", nil)
	for k, v := range c.headers(e.App) {
		req.Header.Set(k, v)
	}
	if c.Type == "anthropic" {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("Anthropic-Version", llmDefaultAnthropicVer)
	} else if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	started := time.Now()
	resp, err := newIntegrationHTTPClient(isTrustedIntegrationOwner(c.Owner, c.OwnerCollection), 0).Do(req)
	if err != nil {
		return e.JSON(http.StatusOK, map[string]any{"ok": false, "channel": c.Name, "error": err.Error()})
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	result := map[string]any{
		"ok":         resp.StatusCode < 400,
		"channel":    c.Name,
		"status":     resp.StatusCode,
		"durationMs": time.Since(started).Milliseconds(),
	}
	if resp.StatusCode >= 400 {
		result["error"] = mcpTruncate(string(raw), 500)
	} else {
		parsed := struct {
			Data []struct {
				Id string `json:"id"`
			} `json:"data"`
		}{}
		if json.Unmarshal(raw, &parsed) == nil {
			result["models"] = len(parsed.Data)
			sample := []string{}
			for i, m := range parsed.Data {
				if i >= 20 {
					break
				}
				sample = append(sample, m.Id)
			}
			result["sampleModels"] = sample
		}
	}

	return e.JSON(http.StatusOK, result)
}

// -------------------------------------------------------------------
// Usage
// -------------------------------------------------------------------

type llmRequestRow struct {
	Id               string `db:"id" json:"id"`
	Owner            string `db:"owner" json:"owner"`
	Channel          string `db:"channel" json:"channel"`
	ChannelName      string `db:"channelName" json:"channelName"`
	Model            string `db:"model" json:"model"`
	Endpoint         string `db:"endpoint" json:"endpoint"`
	Status           int    `db:"status" json:"status"`
	Stream           bool   `db:"stream" json:"stream"`
	PromptTokens     int    `db:"promptTokens" json:"promptTokens"`
	CompletionTokens int    `db:"completionTokens" json:"completionTokens"`
	TotalTokens      int    `db:"totalTokens" json:"totalTokens"`
	DurationMs       int    `db:"durationMs" json:"durationMs"`
	Error            string `db:"error" json:"error,omitempty"`
	Created          string `db:"created" json:"created"`
}

func llmRequestFilters(e *core.RequestEvent) (string, dbx.Params, error) {
	q := e.Request.URL.Query()
	params := dbx.Params{}
	filters := []string{}
	if !(e.HasSuperuserAuth() && q.Get("all") == "true") {
		params["me"] = e.Auth.Id
		filters = append(filters, "[[owner]] = {:me}")
	}
	if v := q.Get("channel"); v != "" {
		params["channel"] = v
		filters = append(filters, "([[channel]] = {:channel} OR LOWER([[channelName]]) = LOWER({:channel}))")
	}
	if v := q.Get("model"); v != "" {
		params["model"] = v
		filters = append(filters, "[[model]] = {:model}")
	}
	if v := q.Get("since"); v != "" {
		t, err := mcpParseTimeArg(v)
		if err != nil {
			return "", nil, e.BadRequestError("Invalid since.", nil)
		}
		params["since"] = t
		filters = append(filters, "[[created]] >= {:since}")
	}
	if q.Get("errorsOnly") == "true" {
		filters = append(filters, "([[status]] >= 400 OR [[status]] = 0)")
	}
	if len(filters) == 0 {
		return "TRUE", params, nil
	}
	return strings.Join(filters, " AND "), params, nil
}

func llmRequestsList(e *core.RequestEvent) error {
	where, params, err := llmRequestFilters(e)
	if err != nil {
		return err
	}
	q := e.Request.URL.Query()
	page := max(cast.ToInt(q.Get("page")), 1)
	perPage := cast.ToInt(q.Get("perPage"))
	if perPage <= 0 {
		perPage = 30
	}
	perPage = min(perPage, 500)
	params["limit"] = perPage
	params["offset"] = (page - 1) * perPage

	var total int
	if err := e.App.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE %s", llmRequestsTable, where)).Bind(params).Row(&total); err != nil {
		return e.InternalServerError("Failed to count the requests.", err)
	}

	rows := []*llmRequestRow{}
	err = e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT [[id]], [[owner]], [[channel]], [[channelName]], [[model]], [[endpoint]], [[status]], [[stream]],
			[[promptTokens]], [[completionTokens]], [[totalTokens]], [[durationMs]], [[error]], [[created]]::text AS [[created]]
		FROM {{%s}} WHERE %s ORDER BY [[created]] DESC, [[id]] DESC LIMIT {:limit} OFFSET {:offset}`, llmRequestsTable, where)).
		Bind(params).All(&rows)
	if err != nil {
		return e.InternalServerError("Failed to load the requests.", err)
	}

	return e.JSON(http.StatusOK, map[string]any{"page": page, "perPage": perPage, "totalItems": total, "items": rows})
}

func llmUsage(e *core.RequestEvent) error {
	where, params, err := llmRequestFilters(e)
	if err != nil {
		return err
	}

	rows := []struct {
		ChannelName      string `db:"channelName" json:"channel"`
		Model            string `db:"model" json:"model"`
		Requests         int    `db:"requests" json:"requests"`
		Errors           int    `db:"errors" json:"errors"`
		PromptTokens     int    `db:"promptTokens" json:"promptTokens"`
		CompletionTokens int    `db:"completionTokens" json:"completionTokens"`
		TotalTokens      int    `db:"totalTokens" json:"totalTokens"`
		AvgDurationMs    int    `db:"avgDurationMs" json:"avgDurationMs"`
	}{}
	err = e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT [[channelName]], [[model]], COUNT(*) AS [[requests]],
			COUNT(*) FILTER (WHERE [[status]] >= 400 OR [[status]] = 0) AS [[errors]],
			COALESCE(SUM([[promptTokens]]), 0) AS [[promptTokens]],
			COALESCE(SUM([[completionTokens]]), 0) AS [[completionTokens]],
			COALESCE(SUM([[totalTokens]]), 0) AS [[totalTokens]],
			COALESCE(AVG([[durationMs]]), 0)::int AS [[avgDurationMs]]
		FROM {{%s}} WHERE %s
		GROUP BY [[channelName]], [[model]]
		ORDER BY COUNT(*) DESC`, llmRequestsTable, where)).Bind(params).All(&rows)
	if err != nil {
		return e.InternalServerError("Failed to aggregate the usage.", err)
	}

	return e.JSON(http.StatusOK, map[string]any{"items": rows})
}
