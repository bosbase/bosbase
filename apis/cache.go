package apis

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coocood/freecache"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

func bindCacheApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/cache").Bind(RequireSuperuserAuth())
	sub.GET("", cacheList)
	sub.POST("", cacheCreate)

	cacheGroup := sub.Group("/{name}")
	cacheGroup.PATCH("", cacheUpdate)
	cacheGroup.DELETE("", cacheDelete)

	entriesGroup := cacheGroup.Group("/entries/{key}")
	entriesGroup.PUT("", cacheSetEntry)
	entriesGroup.GET("", cacheGetEntry)
	entriesGroup.PATCH("", cacheRenewEntry)
	entriesGroup.DELETE("", cacheDeleteEntry)
}

type cacheConfigPayload struct {
	Name              string `json:"name"`
	SizeBytes         *int   `json:"sizeBytes"`
	DefaultTTLSeconds *int   `json:"defaultTTLSeconds"`
	ReadTimeoutMs     *int   `json:"readTimeoutMs"`
}

type cacheConfigSummary struct {
	Name              string         `json:"name"`
	SizeBytes         int            `json:"sizeBytes"`
	DefaultTTLSeconds int            `json:"defaultTTLSeconds"`
	ReadTimeoutMs     int            `json:"readTimeoutMs"`
	Created           types.DateTime `json:"created"`
	Updated           types.DateTime `json:"updated"`
	// Statistics (optional, only included if cache is active)
	EntryCount        *int64   `json:"entryCount,omitempty"`
	HitRate           *float64 `json:"hitRate,omitempty"`
	HitCount          *int64   `json:"hitCount,omitempty"`
	MissCount         *int64   `json:"missCount,omitempty"`
	DatabaseEntryCount *int64  `json:"databaseEntryCount,omitempty"`
}

type cacheEntryPayload struct {
	Value      json.RawMessage `json:"value"`
	TTLSeconds *int            `json:"ttlSeconds"`
}

type cacheEntryResponse struct {
	Cache     string           `json:"cache"`
	Key       string           `json:"key"`
	Value     any              `json:"value"`
	Source    core.CacheSource `json:"source"`
	ExpiresAt *string          `json:"expiresAt,omitempty"`
}

func cacheList(e *core.RequestEvent) error {
	configs, err := e.App.CacheStore().ListConfigs(e.Request.Context())
	if err != nil {
		return e.InternalServerError("Failed to list caches.", err)
	}

	items := make([]cacheConfigSummary, len(configs))
	for i, cfg := range configs {
		summary := newCacheConfigSummary(cfg)
		
		// Try to get runtime statistics if cache is active
		stats := e.App.CacheStore().GetCacheStats(e.Request.Context(), cfg.Name)
		if stats != nil {
			summary.EntryCount = stats.EntryCount
			summary.HitRate = stats.HitRate
			summary.HitCount = stats.HitCount
			summary.MissCount = stats.MissCount
			summary.DatabaseEntryCount = stats.DatabaseEntryCount
		}
		
		items[i] = summary
	}

	return e.JSON(http.StatusOK, map[string]any{"items": items})
}

func cacheCreate(e *core.RequestEvent) error {
	payload := new(cacheConfigPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return e.BadRequestError("Cache name is required.", nil)
	}

	cfg := core.CacheConfig{
		Name:        name,
		DefaultTTL:  parseTTLDuration(payload.DefaultTTLSeconds),
		ReadTimeout: parseReadTimeoutDuration(payload.ReadTimeoutMs),
	}

	if payload.SizeBytes != nil {
		cfg.SizeBytes = *payload.SizeBytes
	}

	created, err := e.App.CacheStore().CreateCache(e.Request.Context(), cfg)
	if err != nil {
		return e.BadRequestError("Failed to create cache.", err)
	}

	return e.JSON(http.StatusCreated, newCacheConfigSummary(created))
}

func cacheUpdate(e *core.RequestEvent) error {
	name := cacheNameFromRequest(e)
	if name == "" {
		return e.BadRequestError("Cache name is required.", nil)
	}

	payload := new(cacheConfigPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	patch := core.CacheConfigPatch{}
	if payload.SizeBytes != nil {
		patch.SizeBytes = payload.SizeBytes
	}
	if payload.DefaultTTLSeconds != nil {
		value := time.Duration(*payload.DefaultTTLSeconds) * time.Second
		patch.DefaultTTL = &value
	}
	if payload.ReadTimeoutMs != nil {
		value := time.Duration(*payload.ReadTimeoutMs) * time.Millisecond
		patch.ReadTimeout = &value
	}

	if patch.SizeBytes == nil && patch.DefaultTTL == nil && patch.ReadTimeout == nil {
		return e.BadRequestError("At least one field must be provided for update.", nil)
	}

	updated, err := e.App.CacheStore().UpdateCache(e.Request.Context(), name, patch)
	if err != nil {
		if errors.Is(err, core.ErrCacheNotFound) {
			return e.NotFoundError("Cache not found.", nil)
		}
		return e.BadRequestError("Failed to update cache.", err)
	}

	return e.JSON(http.StatusOK, newCacheConfigSummary(updated))
}

func cacheDelete(e *core.RequestEvent) error {
	name := cacheNameFromRequest(e)
	if name == "" {
		return e.BadRequestError("Cache name is required.", nil)
	}

	if err := e.App.CacheStore().DeleteCache(e.Request.Context(), name); err != nil {
		if errors.Is(err, core.ErrCacheNotFound) {
			return e.NotFoundError("Cache not found.", nil)
		}
		return e.InternalServerError("Failed to delete cache.", err)
	}

	return e.NoContent(http.StatusNoContent)
}

func cacheSetEntry(e *core.RequestEvent) error {
	name, key, err := cacheEntryParams(e)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	payload := new(cacheEntryPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	if len(payload.Value) == 0 {
		return e.BadRequestError("Cache entry value is required.", nil)
	}

	ttl := parseTTLDuration(payload.TTLSeconds)
	valueCopy := append([]byte(nil), payload.Value...)

	entry, err := e.App.CacheStore().SetEntry(e.Request.Context(), name, key, valueCopy, ttl)
	if err != nil {
		if errors.Is(err, core.ErrCacheNotFound) {
			return e.NotFoundError("Cache not found.", nil)
		}
		if errors.Is(err, freecache.ErrLargeEntry) {
			return e.BadRequestError("The provided value exceeds the cache entry size limit.", err)
		}
		return e.InternalServerError("Failed to store cache entry.", err)
	}

	resp, err := buildCacheEntryResponse(entry)
	if err != nil {
		return e.InternalServerError("Failed to decode cache entry.", err)
	}

	return e.JSON(http.StatusOK, resp)
}

func cacheGetEntry(e *core.RequestEvent) error {
	name, key, err := cacheEntryParams(e)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	entry, err := e.App.CacheStore().GetEntry(e.Request.Context(), name, key)
	if err != nil {
		if errors.Is(err, core.ErrCacheNotFound) || errors.Is(err, core.ErrCacheEntryNotFound) {
			return e.NotFoundError("Cache entry not found.", nil)
		}
		return e.InternalServerError("Failed to load cache entry.", err)
	}

	resp, err := buildCacheEntryResponse(entry)
	if err != nil {
		return e.InternalServerError("Failed to decode cache entry.", err)
	}

	return e.JSON(http.StatusOK, resp)
}

func cacheRenewEntry(e *core.RequestEvent) error {
	name, key, err := cacheEntryParams(e)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	payload := new(cacheEntryPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	ttl := parseTTLDuration(payload.TTLSeconds)

	entry, err := e.App.CacheStore().RenewEntry(e.Request.Context(), name, key, ttl)
	if err != nil {
		if errors.Is(err, core.ErrCacheNotFound) || errors.Is(err, core.ErrCacheEntryNotFound) {
			return e.NotFoundError("Cache entry not found.", nil)
		}
		return e.InternalServerError("Failed to renew cache entry.", err)
	}

	resp, err := buildCacheEntryResponse(entry)
	if err != nil {
		return e.InternalServerError("Failed to decode cache entry.", err)
	}

	return e.JSON(http.StatusOK, resp)
}

func cacheDeleteEntry(e *core.RequestEvent) error {
	name, key, err := cacheEntryParams(e)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	if err := e.App.CacheStore().DeleteEntry(e.Request.Context(), name, key); err != nil {
		if errors.Is(err, core.ErrCacheNotFound) || errors.Is(err, core.ErrCacheEntryNotFound) {
			return e.NotFoundError("Cache entry not found.", nil)
		}
		return e.InternalServerError("Failed to delete cache entry.", err)
	}

	return e.NoContent(http.StatusNoContent)
}

func newCacheConfigSummary(cfg core.CacheConfig) cacheConfigSummary {
	return cacheConfigSummary{
		Name:              cfg.Name,
		SizeBytes:         cfg.SizeBytes,
		DefaultTTLSeconds: int(cfg.DefaultTTL / time.Second),
		ReadTimeoutMs:     int(cfg.ReadTimeout / time.Millisecond),
		Created:           cfg.Created,
		Updated:           cfg.Updated,
	}
}

func parseTTLDuration(value *int) time.Duration {
	if value == nil {
		return -1 * time.Second
	}
	return time.Duration(*value) * time.Second
}

func parseReadTimeoutDuration(value *int) time.Duration {
	if value == nil {
		return -1 * time.Millisecond
	}
	return time.Duration(*value) * time.Millisecond
}

func cacheNameFromRequest(e *core.RequestEvent) string {
	return strings.TrimSpace(e.Request.PathValue("name"))
}

func cacheEntryParams(e *core.RequestEvent) (string, string, error) {
	name := cacheNameFromRequest(e)
	if name == "" {
		return "", "", errors.New("Cache name is required.")
	}

	key := strings.TrimSpace(e.Request.PathValue("key"))
	if key == "" {
		return "", "", errors.New("Cache entry key is required.")
	}

	return name, key, nil
}

func buildCacheEntryResponse(entry *core.CacheEntry) (*cacheEntryResponse, error) {
	var decoded any
	if len(entry.Value) > 0 {
		if err := json.Unmarshal(entry.Value, &decoded); err != nil {
			return nil, err
		}
	}

	resp := &cacheEntryResponse{
		Cache:  entry.Cache,
		Key:    entry.Key,
		Value:  decoded,
		Source: entry.Source,
	}

	if !entry.ExpiresAt.IsZero() {
		value := entry.ExpiresAt.String()
		resp.ExpiresAt = &value
	}

	return resp, nil
}
