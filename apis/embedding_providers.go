package apis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
)

// Server side embedding providers used to embed the MCP memories and notes.
//
// Providers are stored in _embedding_providers (managed by superusers) and read
// on every embedding call, so changes apply without restarting the server.
// The default provider is tried first, then the other enabled providers ordered
// by priority (lower first); when a provider fails the next one is used.
//
// When no provider is configured the MCP_EMBEDDING_* (or OPENAI_*) environment
// variables are used as fallback.

const (
	embeddingProvidersTable = "_embedding_providers"

	embeddingDefaultBaseUrl   = "https://api.openai.com/v1"
	embeddingDefaultPriority  = 100
	embeddingReindexMaxLimit  = 2000
	embeddingReindexBatchSize = 32
	embeddingMaxInputs        = 256
)

var embeddingProviderTypes = []string{"openai"}

func bindEmbeddingApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/embedding").Bind(RequireSuperuserAuth())

	sub.GET("/providers", embeddingProvidersList)
	sub.POST("/providers", embeddingProviderCreate)
	sub.GET("/providers/{id}", embeddingProviderView)
	sub.PATCH("/providers/{id}", embeddingProviderUpdate)
	sub.DELETE("/providers/{id}", embeddingProviderDelete)
	sub.POST("/providers/{id}/test", embeddingProviderTest)

	sub.GET("/status", embeddingStatus)
	sub.POST("/embed", embeddingEmbed)
	sub.POST("/reindex", embeddingReindex)
}

// -------------------------------------------------------------------
// Providers
// -------------------------------------------------------------------

type embeddingProvider struct {
	Id          string `db:"id"`
	Name        string `db:"name"`
	Type        string `db:"type"`
	BaseUrl     string `db:"baseUrl"`
	ApiKey      string `db:"apiKey"`
	Headers     string `db:"headers"`
	Model       string `db:"model"`
	Dimensions  int    `db:"dimensions"`
	Priority    int    `db:"priority"`
	IsDefault   bool   `db:"isDefault"`
	Enabled     bool   `db:"enabled"`
	LastError   string `db:"lastError"`
	LastErrorAt string `db:"lastErrorAt"`
	LastUsedAt  string `db:"lastUsedAt"`
	Created     string `db:"created"`
	Updated     string `db:"updated"`
}

const embeddingProviderColumns = `[[id]], [[name]], [[type]], [[baseUrl]], [[apiKey]], [[headers]], [[model]],
	[[dimensions]], [[priority]], [[isDefault]], [[enabled]], [[lastError]],
	COALESCE([[lastErrorAt]]::text, '') AS [[lastErrorAt]], COALESCE([[lastUsedAt]]::text, '') AS [[lastUsedAt]],
	[[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

// embeddingProviderOrder is the order in which the providers are tried.
const embeddingProviderOrder = "[[isDefault]] DESC, [[priority]] ASC, [[created]] ASC"

func (p *embeddingProvider) headers(app core.App) map[string]string {
	headers := map[string]string{}
	raw, err := decryptIntegrationSecret(app, p.Headers)
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &headers)
	}
	return headers
}

func (p *embeddingProvider) public(app core.App) map[string]any {
	apiKey, _ := decryptIntegrationSecret(app, p.ApiKey)
	headerNames := []string{}
	for k := range p.headers(app) {
		headerNames = append(headerNames, k)
	}
	sort.Strings(headerNames)
	return map[string]any{
		"id":          p.Id,
		"name":        p.Name,
		"type":        p.Type,
		"baseUrl":     p.BaseUrl,
		"apiKey":      maskSecret(apiKey),
		"apiKeySet":   p.ApiKey != "",
		"headers":     headerNames,
		"model":       p.Model,
		"dimensions":  p.Dimensions,
		"priority":    p.Priority,
		"isDefault":   p.IsDefault,
		"enabled":     p.Enabled,
		"lastError":   p.LastError,
		"lastErrorAt": p.LastErrorAt,
		"lastUsedAt":  p.LastUsedAt,
		"created":     p.Created,
		"updated":     p.Updated,
	}
}

func embeddingLoadProvider(app core.App, ref string) (*embeddingProvider, error) {
	p := &embeddingProvider{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[id]] = {:ref} OR LOWER([[name]]) = LOWER({:ref}) ORDER BY ([[id]] = {:ref}) DESC LIMIT 1",
		embeddingProviderColumns, embeddingProvidersTable,
	)).Bind(dbx.Params{"ref": strings.TrimSpace(ref)}).One(p)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func embeddingLoadProviders(app core.App, enabledOnly bool) ([]*embeddingProvider, error) {
	where := "TRUE"
	if enabledOnly {
		where = "[[enabled]] = TRUE"
	}
	providers := []*embeddingProvider{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY %s",
		embeddingProviderColumns, embeddingProvidersTable, where, embeddingProviderOrder,
	)).All(&providers)
	return providers, err
}

type embeddingProviderPayload struct {
	Name       *string            `json:"name"`
	Type       *string            `json:"type"`
	BaseUrl    *string            `json:"baseUrl"`
	ApiKey     *string            `json:"apiKey"`
	Headers    *map[string]string `json:"headers"`
	Model      *string            `json:"model"`
	Dimensions *int               `json:"dimensions"`
	Priority   *int               `json:"priority"`
	IsDefault  *bool              `json:"isDefault"`
	Enabled    *bool              `json:"enabled"`
}

func applyEmbeddingProviderPayload(e *core.RequestEvent, p *embeddingProvider, payload *embeddingProviderPayload) error {
	if payload.Name != nil {
		p.Name = strings.TrimSpace(*payload.Name)
	}
	if payload.Type != nil {
		p.Type = strings.ToLower(strings.TrimSpace(*payload.Type))
	}
	if payload.BaseUrl != nil {
		p.BaseUrl = strings.TrimSpace(*payload.BaseUrl)
	}
	if payload.Model != nil {
		p.Model = strings.TrimSpace(*payload.Model)
	}
	if payload.Dimensions != nil {
		p.Dimensions = *payload.Dimensions
	}
	if payload.Priority != nil {
		p.Priority = *payload.Priority
	}
	if payload.IsDefault != nil {
		p.IsDefault = *payload.IsDefault
	}
	if payload.Enabled != nil {
		p.Enabled = *payload.Enabled
	}

	if p.Type == "" {
		p.Type = "openai"
	}
	if !slicesContains(embeddingProviderTypes, p.Type) {
		return e.BadRequestError("type must be one of "+strings.Join(embeddingProviderTypes, ", ")+" (any OpenAI compatible /embeddings API).", nil)
	}
	if p.Name == "" {
		return e.BadRequestError("name is required.", nil)
	}
	if p.Model == "" {
		return e.BadRequestError("model is required.", nil)
	}
	if p.Dimensions < 0 || p.Dimensions > 16000 {
		return e.BadRequestError("dimensions must be between 0 (provider default) and 16000.", nil)
	}
	if p.BaseUrl == "" {
		p.BaseUrl = embeddingDefaultBaseUrl
	}
	baseUrl, err := validateUpstreamURL(p.BaseUrl, false)
	if err != nil {
		return e.BadRequestError("Invalid baseUrl: "+err.Error(), nil)
	}
	p.BaseUrl = baseUrl

	if payload.ApiKey != nil {
		enc, err := encryptIntegrationSecret(e.App, strings.TrimSpace(*payload.ApiKey))
		if err != nil {
			return err
		}
		p.ApiKey = enc
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
		p.Headers = enc
	}

	return nil
}

func embeddingSaveProvider(e *core.RequestEvent, p *embeddingProvider, isNew bool) error {
	params := dbx.Params{
		"name":       p.Name,
		"type":       p.Type,
		"baseUrl":    p.BaseUrl,
		"apiKey":     p.ApiKey,
		"headers":    p.Headers,
		"model":      p.Model,
		"dimensions": p.Dimensions,
		"priority":   p.Priority,
		"isDefault":  p.IsDefault,
		"enabled":    p.Enabled,
	}

	err := e.App.RunInTransaction(func(txApp core.App) error {
		if p.IsDefault {
			if _, err := txApp.DB().NewQuery(fmt.Sprintf(
				"UPDATE {{%s}} SET [[isDefault]] = FALSE WHERE [[isDefault]] = TRUE AND [[id]] <> {:id}", embeddingProvidersTable,
			)).Bind(dbx.Params{"id": p.Id}).Execute(); err != nil {
				return err
			}
		}
		if isNew {
			return txApp.DB().NewQuery(fmt.Sprintf(`
				INSERT INTO {{%s}} ([[name]], [[type]], [[baseUrl]], [[apiKey]], [[headers]], [[model]], [[dimensions]], [[priority]], [[isDefault]], [[enabled]])
				VALUES ({:name}, {:type}, {:baseUrl}, {:apiKey}, {:headers}, {:model}, {:dimensions}, {:priority}, {:isDefault}, {:enabled})
				RETURNING [[id]]`, embeddingProvidersTable)).Bind(params).Row(&p.Id)
		}
		params["id"] = p.Id
		_, err := txApp.DB().NewQuery(fmt.Sprintf(`
			UPDATE {{%s}} SET [[name]] = {:name}, [[type]] = {:type}, [[baseUrl]] = {:baseUrl}, [[apiKey]] = {:apiKey},
				[[headers]] = {:headers}, [[model]] = {:model}, [[dimensions]] = {:dimensions}, [[priority]] = {:priority},
				[[isDefault]] = {:isDefault}, [[enabled]] = {:enabled}, [[updated]] = NOW()
			WHERE [[id]] = {:id}`, embeddingProvidersTable)).Bind(params).Execute()
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "idx__embedding_providers_name") {
			return e.BadRequestError("An embedding provider named "+strconv.Quote(p.Name)+" already exists.", nil)
		}
		return e.InternalServerError("Failed to save the embedding provider.", err)
	}
	return nil
}

func embeddingProvidersList(e *core.RequestEvent) error {
	providers, err := embeddingLoadProviders(e.App, false)
	if err != nil {
		return e.InternalServerError("Failed to load the embedding providers.", err)
	}

	items := make([]map[string]any, len(providers))
	for i, p := range providers {
		items[i] = p.public(e.App)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"order":       embeddingOrderNames(e.App),
		"envFallback": embeddingEnvPublic(),
	})
}

func embeddingProviderCreate(e *core.RequestEvent) error {
	payload := &embeddingProviderPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}

	p := &embeddingProvider{Enabled: true, Priority: embeddingDefaultPriority}
	if err := applyEmbeddingProviderPayload(e, p, payload); err != nil {
		return err
	}
	if err := embeddingSaveProvider(e, p, true); err != nil {
		return err
	}

	saved, err := embeddingLoadProvider(e.App, p.Id)
	if err != nil {
		return e.InternalServerError("Failed to load the embedding provider.", err)
	}
	return e.JSON(http.StatusOK, saved.public(e.App))
}

func embeddingProviderView(e *core.RequestEvent) error {
	p, err := embeddingLoadProvider(e.App, e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("Embedding provider not found.", nil)
	}
	return e.JSON(http.StatusOK, p.public(e.App))
}

func embeddingProviderUpdate(e *core.RequestEvent) error {
	p, err := embeddingLoadProvider(e.App, e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("Embedding provider not found.", nil)
	}

	payload := &embeddingProviderPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	if err := applyEmbeddingProviderPayload(e, p, payload); err != nil {
		return err
	}
	if err := embeddingSaveProvider(e, p, false); err != nil {
		return err
	}

	saved, err := embeddingLoadProvider(e.App, p.Id)
	if err != nil {
		return e.InternalServerError("Failed to load the embedding provider.", err)
	}
	return e.JSON(http.StatusOK, saved.public(e.App))
}

func embeddingProviderDelete(e *core.RequestEvent) error {
	p, err := embeddingLoadProvider(e.App, e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("Embedding provider not found.", nil)
	}
	if _, err := e.App.DB().Delete(embeddingProvidersTable, dbx.HashExp{"id": p.Id}).Execute(); err != nil {
		return e.InternalServerError("Failed to delete the embedding provider.", err)
	}
	return e.NoContent(http.StatusNoContent)
}

func embeddingProviderTest(e *core.RequestEvent) error {
	p, err := embeddingLoadProvider(e.App, e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("Embedding provider not found.", nil)
	}

	target, err := embeddingTargetFromProvider(e.App, p)
	if err != nil {
		return e.InternalServerError("Failed to decrypt the provider credentials.", err)
	}

	started := time.Now()
	vectors, err := embeddingCall(e.Request.Context(), target, []string{"BosBase embedding provider test"})
	result := map[string]any{
		"ok":         err == nil,
		"provider":   p.Name,
		"model":      p.Model,
		"durationMs": time.Since(started).Milliseconds(),
	}
	if err != nil {
		result["error"] = err.Error()
		embeddingRecordFailure(e.App, target, err)
	} else {
		result["dimensions"] = len(vectors[0])
		embeddingRecordSuccess(e.App, target)
	}

	return e.JSON(http.StatusOK, result)
}

// -------------------------------------------------------------------
// Embedding calls
// -------------------------------------------------------------------

// embeddingTarget is a resolved (decrypted) provider ready to be called.
type embeddingTarget struct {
	Id         string
	Name       string
	BaseUrl    string
	ApiKey     string
	Headers    map[string]string
	Model      string
	Dimensions int
	// Source is "db" for the stored providers or "env" for the environment fallback.
	Source string
}

func embeddingTargetFromProvider(app core.App, p *embeddingProvider) (*embeddingTarget, error) {
	apiKey, err := decryptIntegrationSecret(app, p.ApiKey)
	if err != nil {
		return nil, err
	}
	return &embeddingTarget{
		Id:         p.Id,
		Name:       p.Name,
		BaseUrl:    p.BaseUrl,
		ApiKey:     apiKey,
		Headers:    p.headers(app),
		Model:      p.Model,
		Dimensions: p.Dimensions,
		Source:     "db",
	}, nil
}

// embeddingEnvTarget returns the provider configured with environment variables (or nil):
//
//	MCP_EMBEDDING_API_KEY    (fallback OPENAI_API_KEY)
//	MCP_EMBEDDING_BASE_URL   (fallback OPENAI_BASE_URL)
//	MCP_EMBEDDING_MODEL      (default text-embedding-3-small)
//	MCP_EMBEDDING_DIMENSIONS (optional)
func embeddingEnvTarget() *embeddingTarget {
	apiKey := firstEnv("MCP_EMBEDDING_API_KEY", "OPENAI_API_KEY")
	baseURL := firstEnv("MCP_EMBEDDING_BASE_URL", "OPENAI_BASE_URL")
	if apiKey == "" && baseURL == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = embeddingDefaultBaseUrl
	}
	model := firstEnv("MCP_EMBEDDING_MODEL")
	if model == "" {
		model = mcpDefaultEmbedModel
	}
	dims, _ := strconv.Atoi(firstEnv("MCP_EMBEDDING_DIMENSIONS"))
	return &embeddingTarget{
		Name:       "env",
		BaseUrl:    strings.TrimRight(baseURL, "/"),
		ApiKey:     apiKey,
		Model:      model,
		Dimensions: dims,
		Source:     "env",
	}
}

func embeddingEnvPublic() map[string]any {
	t := embeddingEnvTarget()
	if t == nil {
		return nil
	}
	return map[string]any{
		"baseUrl":    t.BaseUrl,
		"model":      t.Model,
		"dimensions": t.Dimensions,
		"apiKeySet":  t.ApiKey != "",
		"note":       "Used only when no embedding provider is enabled in the database.",
	}
}

// embeddingTargets returns the providers in the order they should be tried.
func embeddingTargets(app core.App) ([]*embeddingTarget, error) {
	providers, err := embeddingLoadProviders(app, true)
	if err != nil {
		return nil, err
	}

	targets := make([]*embeddingTarget, 0, len(providers))
	var decryptErr error
	for _, p := range providers {
		t, err := embeddingTargetFromProvider(app, p)
		if err != nil {
			decryptErr = fmt.Errorf("embedding provider %q: %w", p.Name, err)
			continue
		}
		targets = append(targets, t)
	}

	if len(targets) == 0 {
		if decryptErr != nil {
			return nil, decryptErr
		}
		if env := embeddingEnvTarget(); env != nil {
			targets = append(targets, env)
		}
	}

	return targets, nil
}

func embeddingOrderNames(app core.App) []string {
	targets, _ := embeddingTargets(app)
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	return names
}

// embeddingPrimary returns the first provider to be tried (or nil if none is configured).
func embeddingPrimary(app core.App) (*embeddingTarget, error) {
	targets, err := embeddingTargets(app)
	if err != nil || len(targets) == 0 {
		return nil, err
	}
	return targets[0], nil
}

var (
	embeddingHTTPClientOnce sync.Once
	embeddingHTTPClient     *http.Client
)

func getEmbeddingHTTPClient() *http.Client {
	embeddingHTTPClientOnce.Do(func() {
		// providers are configured by superusers (trusted)
		embeddingHTTPClient = newIntegrationHTTPClient(true, 0)
	})
	return embeddingHTTPClient
}

// embeddingCall calls the OpenAI compatible /embeddings endpoint of the provider.
func embeddingCall(ctx context.Context, t *embeddingTarget, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	payload := map[string]any{"model": t.Model, "input": texts}
	if t.Dimensions > 0 {
		payload["dimensions"] = t.Dimensions
	}
	body, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(ctx, mcpEmbeddingCallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.BaseUrl, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	if t.ApiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.ApiKey)
	}

	resp, err := getEmbeddingHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, err
	}

	parsed := struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error any `json:"error"`
	}{}
	jsonErr := json.Unmarshal(raw, &parsed)

	if resp.StatusCode >= 400 || parsed.Error != nil {
		msg := mcpTruncate(strings.TrimSpace(string(raw)), 300)
		if m, ok := parsed.Error.(map[string]any); ok && m["message"] != nil {
			msg = fmt.Sprint(m["message"])
		} else if s, ok := parsed.Error.(string); ok && s != "" {
			msg = s
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	if jsonErr != nil {
		return nil, fmt.Errorf("invalid embeddings response: %w", jsonErr)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(parsed.Data))
	}

	sort.SliceStable(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })
	vectors := make([][]float64, len(parsed.Data))
	for i, d := range parsed.Data {
		if len(d.Embedding) == 0 {
			return nil, errors.New("empty embedding in the response")
		}
		vectors[i] = d.Embedding
	}

	return vectors, nil
}

func embeddingRecordFailure(app core.App, t *embeddingTarget, err error) {
	app.Logger().Warn("Embedding provider failure", "provider", t.Name, "model", t.Model, "error", err)
	if t.Source != "db" {
		return
	}
	_, _ = app.DB().NewQuery(fmt.Sprintf(
		"UPDATE {{%s}} SET [[lastError]] = {:error}, [[lastErrorAt]] = NOW() WHERE [[id]] = {:id}", embeddingProvidersTable,
	)).Bind(dbx.Params{"id": t.Id, "error": mcpTruncate(err.Error(), 500)}).Execute()
}

func embeddingRecordSuccess(app core.App, t *embeddingTarget) {
	if t.Source != "db" {
		return
	}
	// throttled to avoid a write on every call
	_, _ = app.DB().NewQuery(fmt.Sprintf(
		"UPDATE {{%s}} SET [[lastUsedAt]] = NOW() WHERE [[id]] = {:id} AND ([[lastUsedAt]] IS NULL OR [[lastUsedAt]] < NOW() - INTERVAL '1 minute' OR [[lastErrorAt]] > [[lastUsedAt]])",
		embeddingProvidersTable,
	)).Bind(dbx.Params{"id": t.Id}).Execute()
}

// embedTexts embeds the texts with the first working provider (failover in priority order).
//
// It returns a nil result without error when no provider is configured.
func embedTexts(ctx context.Context, app core.App, texts []string) ([][]float64, *embeddingTarget, error) {
	targets, err := embeddingTargets(app)
	if err != nil {
		return nil, nil, err
	}
	if len(targets) == 0 {
		return nil, nil, nil
	}

	failures := []string{}
	for _, t := range targets {
		vectors, err := embeddingCall(ctx, t, texts)
		if err == nil {
			embeddingRecordSuccess(app, t)
			return vectors, t, nil
		}
		embeddingRecordFailure(app, t, err)
		failures = append(failures, t.Name+": "+err.Error())
		if ctx.Err() != nil {
			break
		}
	}

	if len(failures) == 1 {
		return nil, nil, errors.New(failures[0])
	}
	return nil, nil, errors.New("all embedding providers failed: " + strings.Join(failures, "; "))
}

// mcpEmbed returns the embedding for text (or the provided one) and the model name.
// It returns a nil vector without error when no embedding provider is configured.
func mcpEmbed(ctx context.Context, app core.App, text string, provided []float64) ([]float64, string, error) {
	if len(provided) > 0 {
		return provided, "client", nil
	}

	vectors, target, err := embedTexts(ctx, app, []string{text})
	if err != nil || target == nil {
		return nil, "", err
	}

	return vectors[0], target.Model, nil
}

// -------------------------------------------------------------------
// Status, embed and reindex
// -------------------------------------------------------------------

type embeddingModelStat struct {
	Model      string `db:"model" json:"model"`
	Dimensions int    `db:"dimensions" json:"dimensions"`
	Memories   int    `db:"memories" json:"memories"`
}

func embeddingStatusResult(app core.App) (map[string]any, error) {
	providers, err := embeddingLoadProviders(app, false)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(providers))
	for i, p := range providers {
		items[i] = p.public(app)
	}

	result := map[string]any{
		"providers":   items,
		"order":       embeddingOrderNames(app),
		"envFallback": embeddingEnvPublic(),
	}

	primary, err := embeddingPrimary(app)
	if err != nil {
		result["error"] = err.Error()
	}
	primaryModel := ""
	if primary != nil {
		primaryModel = primary.Model
		result["primary"] = map[string]any{"name": primary.Name, "model": primary.Model, "source": primary.Source}
	} else {
		result["primary"] = nil
		result["note"] = "No embedding provider is configured: memories are only searchable by keywords. Create one with POST /api/embedding/providers."
	}

	stats := []*embeddingModelStat{}
	if err := app.DB().NewQuery(fmt.Sprintf(`
		SELECT [[embeddingModel]] AS [[model]], vector_dims([[embedding]]) AS [[dimensions]], COUNT(*) AS [[memories]]
		FROM {{%s}} WHERE [[embedding]] IS NOT NULL
		GROUP BY [[embeddingModel]], vector_dims([[embedding]])
		ORDER BY [[memories]] DESC`, mcpMemoriesTable)).All(&stats); err != nil {
		return nil, err
	}

	var total, missing, mismatched int
	_ = app.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}}", mcpMemoriesTable)).Row(&total)
	_ = app.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE [[embedding]] IS NULL", mcpMemoriesTable)).Row(&missing)
	if primaryModel != "" {
		_ = app.DB().NewQuery(fmt.Sprintf(
			"SELECT COUNT(*) FROM {{%s}} WHERE [[embedding]] IS NOT NULL AND [[embeddingModel]] <> {:model}", mcpMemoriesTable,
		)).Bind(dbx.Params{"model": primaryModel}).Row(&mismatched)
	}

	result["memories"] = map[string]any{
		"total":      total,
		"missing":    missing,
		"mismatched": mismatched,
		"byModel":    stats,
	}
	if primaryModel != "" && missing+mismatched > 0 {
		result["hint"] = fmt.Sprintf("%d memories are not embedded with the primary model %q; semantic search only compares vectors of the same model. Run POST /api/embedding/reindex (MCP: embedding_reindex) to re-embed them.", missing+mismatched, primaryModel)
	}

	return result, nil
}

func embeddingStatus(e *core.RequestEvent) error {
	result, err := embeddingStatusResult(e.App)
	if err != nil {
		return e.InternalServerError("Failed to load the embedding status.", err)
	}
	return e.JSON(http.StatusOK, result)
}

type embeddingEmbedPayload struct {
	Input    any    `json:"input"`
	Provider string `json:"provider"`
}

// embeddingEmbed embeds texts with the configured providers (or a specific one).
func embeddingEmbed(e *core.RequestEvent) error {
	payload := &embeddingEmbedPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}

	texts := []string{}
	switch v := payload.Input.(type) {
	case string:
		texts = append(texts, v)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return e.BadRequestError("input must be a string or an array of strings.", nil)
			}
			texts = append(texts, s)
		}
	}
	if len(texts) == 0 {
		return e.BadRequestError("input is required.", nil)
	}
	if len(texts) > embeddingMaxInputs {
		return e.BadRequestError(fmt.Sprintf("At most %d inputs are allowed.", embeddingMaxInputs), nil)
	}

	var vectors [][]float64
	var target *embeddingTarget
	var err error
	if strings.TrimSpace(payload.Provider) != "" {
		p, loadErr := embeddingLoadProvider(e.App, payload.Provider)
		if loadErr != nil {
			return e.NotFoundError("Embedding provider not found.", nil)
		}
		if target, err = embeddingTargetFromProvider(e.App, p); err != nil {
			return e.InternalServerError("Failed to decrypt the provider credentials.", err)
		}
		if vectors, err = embeddingCall(e.Request.Context(), target, texts); err != nil {
			embeddingRecordFailure(e.App, target, err)
		} else {
			embeddingRecordSuccess(e.App, target)
		}
	} else {
		vectors, target, err = embedTexts(e.Request.Context(), e.App, texts)
		if err == nil && target == nil {
			return e.BadRequestError("No embedding provider is configured.", nil)
		}
	}
	if err != nil {
		return e.Error(http.StatusBadGateway, "Embedding failed: "+err.Error(), nil)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"provider":   target.Name,
		"model":      target.Model,
		"dimensions": len(vectors[0]),
		"embeddings": vectors,
	})
}

type embeddingReindexPayload struct {
	// Mode: "mismatched" (default; missing or embedded with another model than the primary one),
	// "missing" (without embedding) or "all".
	Mode      string `json:"mode"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
	BatchSize int    `json:"batchSize"`
	Project   string `json:"project"`
	Namespace string `json:"namespace"`
	OwnerId   string `json:"ownerId"`
	Shared    bool   `json:"shared"`
}

func embeddingReindexResult(ctx context.Context, app core.App, payload *embeddingReindexPayload) (map[string]any, error) {
	primary, err := embeddingPrimary(app)
	if err != nil {
		return nil, err
	}
	if primary == nil {
		return nil, errors.New("no embedding provider is configured")
	}

	mode := strings.ToLower(strings.TrimSpace(payload.Mode))
	if mode == "" {
		mode = "mismatched"
	}
	limit := payload.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > embeddingReindexMaxLimit {
		limit = embeddingReindexMaxLimit
	}
	batchSize := payload.BatchSize
	if batchSize <= 0 || batchSize > embeddingMaxInputs {
		batchSize = embeddingReindexBatchSize
	}
	if payload.Offset < 0 {
		payload.Offset = 0
	}

	params := dbx.Params{"model": primary.Model, "limit": limit, "offset": payload.Offset}
	filters := []string{"TRUE"}
	switch mode {
	case "missing":
		filters = append(filters, "[[embedding]] IS NULL")
	case "mismatched":
		filters = append(filters, "([[embedding]] IS NULL OR [[embeddingModel]] <> {:model})")
	case "all":
	default:
		return nil, errors.New(`mode must be "mismatched", "missing" or "all"`)
	}
	ns := strings.TrimSpace(payload.Project)
	if ns == "" {
		ns = strings.TrimSpace(payload.Namespace)
	}
	if ns != "" {
		params["namespace"] = ns
		filters = append(filters, "[[namespace]] = {:namespace}")
	}
	if payload.Shared {
		filters = append(filters, "[[owner]] = ''")
	} else if owner := strings.TrimSpace(payload.OwnerId); owner != "" {
		params["owner"] = owner
		filters = append(filters, "[[owner]] = {:owner}")
	}
	where := strings.Join(filters, " AND ")

	rows := []struct {
		Id      string `db:"id"`
		Title   string `db:"title"`
		Content string `db:"content"`
	}{}
	query := fmt.Sprintf("SELECT [[id]], [[title]], [[content]] FROM {{%s}} WHERE %s ORDER BY [[updated]] DESC, [[id]] LIMIT {:limit}", mcpMemoriesTable, where)
	if mode == "all" {
		// stable pagination since the rows keep matching the filter
		query = fmt.Sprintf("SELECT [[id]], [[title]], [[content]] FROM {{%s}} WHERE %s ORDER BY [[created]], [[id]] LIMIT {:limit} OFFSET {:offset}", mcpMemoriesTable, where)
	}
	if err := app.DB().NewQuery(query).Bind(params).All(&rows); err != nil {
		return nil, err
	}

	started := time.Now()
	processed, failed := 0, 0
	models := map[string]int{}
	errorsList := []string{}

	for start := 0; start < len(rows); start += batchSize {
		if ctx.Err() != nil {
			errorsList = append(errorsList, "interrupted: "+ctx.Err().Error())
			break
		}
		end := min(start+batchSize, len(rows))
		batch := rows[start:end]

		texts := make([]string, len(batch))
		for i, r := range batch {
			texts[i] = strings.TrimSpace(r.Title + "\n" + r.Content)
		}

		vectors, target, err := embedTexts(ctx, app, texts)
		if err != nil || target == nil {
			failed += len(batch)
			if err != nil {
				errorsList = append(errorsList, err.Error())
			}
			// stop after a failed batch (likely a provider outage)
			break
		}

		for i, r := range batch {
			_, err := app.DB().NewQuery(fmt.Sprintf(
				"UPDATE {{%s}} SET [[embedding]] = {:vector}::vector, [[embeddingModel]] = {:model} WHERE [[id]] = {:id}", mcpMemoriesTable,
			)).Bind(dbx.Params{"id": r.Id, "vector": formatVectorForPostgres(vectors[i]), "model": target.Model}).Execute()
			if err != nil {
				failed++
				errorsList = append(errorsList, r.Id+": "+err.Error())
				continue
			}
			processed++
			models[target.Model]++
		}
	}

	result := map[string]any{
		"mode":         mode,
		"primaryModel": primary.Model,
		"selected":     len(rows),
		"processed":    processed,
		"failed":       failed,
		"models":       models,
		"durationMs":   time.Since(started).Milliseconds(),
	}
	if len(errorsList) > 0 {
		if len(errorsList) > 20 {
			errorsList = errorsList[:20]
		}
		result["errors"] = errorsList
	}

	if mode == "all" {
		next := payload.Offset + len(rows)
		var total int
		_ = app.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE %s", mcpMemoriesTable, where)).Bind(params).Row(&total)
		result["remaining"] = max(total-next, 0)
		if next < total {
			result["nextOffset"] = next
		}
	} else {
		var remaining int
		_ = app.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE %s", mcpMemoriesTable, where)).Bind(params).Row(&remaining)
		result["remaining"] = remaining
	}

	return result, nil
}

func embeddingReindex(e *core.RequestEvent) error {
	payload := &embeddingReindexPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}

	result, err := embeddingReindexResult(e.Request.Context(), e.App, payload)
	if err != nil {
		return e.BadRequestError("Failed to reindex: "+err.Error(), nil)
	}
	return e.JSON(http.StatusOK, result)
}

// -------------------------------------------------------------------
// MCP tools
// -------------------------------------------------------------------

var mcpEmbeddingTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	enum := func(desc string, list ...string) map[string]any {
		return map[string]any{"type": "string", "enum": list, "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	mcpEmbeddingTools = []mcpTool{
		{
			Name: "embedding_provider",
			Description: "Superusers: manage the server side embedding providers used to embed memories and notes (any OpenAI compatible /embeddings API: OpenAI, OpenRouter, Azure gateways, Ollama, vLLM, Jina, Voyage...). " +
				"Changes apply immediately without restart. The default provider is tried first, then the other enabled providers by priority (lower first) as failover. " +
				"API keys are stored encrypted and never returned. Actions: list, get, create, update, delete, test.",
			InputSchema: obj(map[string]any{
				"action":     enum("Action.", "list", "get", "create", "update", "delete", "test"),
				"id":         str("Provider id or name (get/update/delete/test)."),
				"name":       str("Unique provider name."),
				"type":       enum("Provider API type.", embeddingProviderTypes...),
				"baseUrl":    str("API base URL including the version, eg. https://api.openai.com/v1 (default), https://openrouter.ai/api/v1, http://ollama:11434/v1."),
				"apiKey":     str("Provider API key."),
				"headers":    map[string]any{"type": "object", "description": "Extra headers sent upstream (stored encrypted)."},
				"model":      str("Embedding model, eg. text-embedding-3-small or openai/text-embedding-3-small."),
				"dimensions": integer("Optional output dimensions (models supporting it, eg. text-embedding-3-*). 0 = model default."),
				"priority":   integer("Failover order, lower first (default 100)."),
				"isDefault":  boolean("Make it the default (primary) provider."),
				"enabled":    boolean("Enable/disable the provider."),
			}, "action"),
			SuperuserOnly: true,
			NoScopeArgs:   true,
			Handler:       mcpEmbeddingProvider,
		},
		{
			Name:          "embedding_status",
			Description:   "Superusers: embedding configuration and coverage: providers (with last errors), the order they are tried, the primary model and how many memories are missing an embedding or are embedded with another model.",
			InputSchema:   obj(map[string]any{}),
			SuperuserOnly: true,
			ReadOnly:      true,
			NoScopeArgs:   true,
			Handler: func(e *core.RequestEvent, args json.RawMessage) (any, error) {
				return mcpDispatchJSON(e, http.MethodGet, "/api/embedding/status", nil, nil)
			},
		},
		{
			Name: "embedding_reindex",
			Description: "Superusers: (re)embed memories with the current primary provider, eg. after configuring a provider for the first time or switching the default model " +
				"(semantic search only compares vectors of the same model). Processes up to limit memories per call; call again while remaining > 0.",
			InputSchema: obj(map[string]any{
				"mode":      enum("mismatched (default): missing or embedded with another model; missing: without embedding; all: everything (paginate with offset).", "mismatched", "missing", "all"),
				"limit":     integer("Max memories per call (default 200, max 2000)."),
				"offset":    integer("Offset for mode all (use nextOffset from the previous call)."),
				"batchSize": integer("Texts per provider request (default 32)."),
				"project":   str("Only this project."),
				"ownerId":   str("Only the memories of this auth record."),
				"shared":    boolean("Only the shared memories."),
			}),
			SuperuserOnly: true,
			NoScopeArgs:   true,
			Handler: func(e *core.RequestEvent, args json.RawMessage) (any, error) {
				body := map[string]any{}
				if len(args) > 0 {
					if err := json.Unmarshal(args, &body); err != nil {
						return nil, mcpToolErr("Invalid arguments: " + err.Error())
					}
				}
				return mcpDispatchJSON(e, http.MethodPost, "/api/embedding/reindex", nil, body)
			},
		},
	}
}

func mcpEmbeddingProvider(e *core.RequestEvent, args json.RawMessage) (any, error) {
	action, id, body, err := mcpSplitAction(args)
	if err != nil {
		return nil, err
	}
	base := "/api/embedding/providers"

	switch action {
	case "list":
		return mcpDispatchJSON(e, http.MethodGet, base, nil, nil)
	case "create":
		return mcpDispatchJSON(e, http.MethodPost, base, nil, body)
	case "get", "update", "delete", "test":
		if err := mcpRequireId(id, action); err != nil {
			return nil, err
		}
		p := base + "/" + url.PathEscape(id)
		switch action {
		case "get":
			return mcpDispatchJSON(e, http.MethodGet, p, nil, nil)
		case "update":
			return mcpDispatchJSON(e, http.MethodPatch, p, nil, body)
		case "delete":
			if _, err := mcpDispatchJSON(e, http.MethodDelete, p, nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true, "id": id}, nil
		default:
			return mcpDispatchJSON(e, http.MethodPost, p+"/test", nil, nil)
		}
	default:
		return nil, mcpToolErr("Unknown action " + strconv.Quote(action) + ".")
	}
}
