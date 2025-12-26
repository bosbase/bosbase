package apis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/redis/rueidis"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/hook"
	"github.com/bosbase/bosbase-enterprise/tools/router"
)

const (
	redisStoreKey    = "__pbRedisClient__"
	redisURLEnvKey   = "REDIS_URL"
	redisPassEnvKey  = "REDIS_PASSWORD"
	redisMaxScanHint = 1000
)

var errRedisKeyNotFound = errors.New("redis key not found")

type redisService struct {
	url      string
	password string
	cli      rueidis.Client
	mu       sync.Mutex
}

type redisKeySummary struct {
	Key string `json:"key"`
}

type redisListResponse struct {
	Cursor string            `json:"cursor"`
	Items  []redisKeySummary `json:"items"`
}

type redisEntryResponse struct {
	Key        string `json:"key"`
	Value      any    `json:"value"`
	TTLSeconds *int   `json:"ttlSeconds,omitempty"`
}

type redisCreatePayload struct {
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	TTLSeconds *int            `json:"ttlSeconds"`
}

type redisUpdatePayload struct {
	Value      json.RawMessage `json:"value"`
	TTLSeconds *int            `json:"ttlSeconds"`
}

func bindRedisApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL == "" {
		return
	}

	service := ensureRedisService(app, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey)))

	sub := rg.Group("/redis").Bind(RequireSuperuserAuth())
	sub.GET("/keys", redisListKeys(service))
	sub.POST("/keys", redisCreateKey(service))

	keyGroup := sub.Group("/keys/{key}")
	keyGroup.GET("", redisGetKey(service))
	keyGroup.PUT("", redisUpdateKey(service))
	keyGroup.DELETE("", redisDeleteKey(service))
}

func ensureRedisService(app core.App, url, password string) *redisService {
	url = normalizeRedisURL(url)

	if cached, ok := app.Store().Get(redisStoreKey).(*redisService); ok && cached != nil {
		if cached.url == url && cached.password == password {
			return cached
		}
		cached.close()
	}

	service := &redisService{url: url, password: password}
	app.Store().Set(redisStoreKey, service)

	app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{
		Id: "__pbRedisClose__",
		Func: func(e *core.TerminateEvent) error {
			service.close()
			return e.Next()
		},
	})

	return service
}

func (s *redisService) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cli != nil {
		s.cli.Close()
		s.cli = nil
	}
}

func (s *redisService) getClient() (rueidis.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cli != nil {
		return s.cli, nil
	}

	opts, err := rueidis.ParseURL(s.url)
	if err != nil {
		return nil, err
	}
	if s.password != "" && opts.Password == "" {
		opts.Password = s.password
	}

	client, err := rueidis.NewClient(opts)
	if err != nil {
		return nil, err
	}

	s.cli = client
	return client, nil
}

func (s *redisService) ttlSeconds(ctx context.Context, client rueidis.Client, key string) (*int, error) {
	ttl, err := client.Do(ctx, client.B().Ttl().Key(key).Build()).AsInt64()
	if err != nil {
		return nil, err
	}

	switch ttl {
	case -2:
		return nil, errRedisKeyNotFound
	case -1:
		return nil, nil
	default:
		value := int(ttl)
		return &value, nil
	}
}

func redisListKeys(service *redisService) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		client, err := service.getClient()
		if err != nil {
			return e.InternalServerError("Failed to initialize Redis client.", err)
		}

		cursorRaw := strings.TrimSpace(e.Request.URL.Query().Get("cursor"))
		var cursor uint64
		if cursorRaw != "" {
			cursor, err = strconv.ParseUint(cursorRaw, 10, 64)
			if err != nil {
				return e.BadRequestError("Invalid cursor value.", err)
			}
		}

		countRaw := strings.TrimSpace(e.Request.URL.Query().Get("count"))
		var count int64
		if countRaw != "" {
			count, err = strconv.ParseInt(countRaw, 10, 64)
			if err != nil || count <= 0 {
				return e.BadRequestError("Invalid count value.", err)
			}
			if count > redisMaxScanHint {
				count = redisMaxScanHint
			}
		}

		pattern := strings.TrimSpace(e.Request.URL.Query().Get("pattern"))
		scan := client.B().Scan().Cursor(cursor)

		var scanCmd rueidis.Completed
		switch {
		case pattern != "" && count > 0:
			scanCmd = scan.Match(pattern).Count(count).Build()
		case pattern != "":
			scanCmd = scan.Match(pattern).Build()
		case count > 0:
			scanCmd = scan.Count(count).Build()
		default:
			scanCmd = scan.Build()
		}

		result, err := client.Do(e.Request.Context(), scanCmd).AsScanEntry()
		if err != nil {
			return e.InternalServerError("Failed to list Redis keys.", err)
		}

		items := make([]redisKeySummary, len(result.Elements))
		for i, key := range result.Elements {
			items[i] = redisKeySummary{Key: key}
		}

		return e.JSON(http.StatusOK, redisListResponse{
			Cursor: strconv.FormatUint(result.Cursor, 10),
			Items:  items,
		})
	}
}

func redisCreateKey(service *redisService) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		payload := new(redisCreatePayload)
		if err := e.BindBody(payload); err != nil {
			return e.BadRequestError("An error occurred while loading the submitted data.", err)
		}

		key := strings.TrimSpace(payload.Key)
		if key == "" {
			return e.BadRequestError("Key is required.", nil)
		}
		if len(payload.Value) == 0 {
			return e.BadRequestError("Key value is required.", nil)
		}

		ttl, err := normalizeTTLSeconds(payload.TTLSeconds)
		if err != nil {
			return e.BadRequestError(err.Error(), nil)
		}

		client, err := service.getClient()
		if err != nil {
			return e.InternalServerError("Failed to initialize Redis client.", err)
		}

		base := client.B().Set().Key(key).Value(string(payload.Value))

		var completed rueidis.Completed
		if ttl != nil && *ttl > 0 {
			completed = base.Nx().ExSeconds(int64(*ttl)).Build()
		} else {
			completed = base.Nx().Build()
		}

		if _, err := client.Do(e.Request.Context(), completed).ToString(); err != nil {
			if rueidis.IsRedisNil(err) {
				return e.Error(http.StatusConflict, "Key already exists.", nil)
			}
			return e.InternalServerError("Failed to create Redis key.", err)
		}

		value, err := decodeRedisValue(payload.Value)
		if err != nil {
			return e.InternalServerError("Failed to decode Redis value.", err)
		}

		ttlSeconds, err := service.ttlSeconds(e.Request.Context(), client, key)
		if err != nil && !errors.Is(err, errRedisKeyNotFound) {
			return e.InternalServerError("Failed to read Redis key TTL.", err)
		}

		return e.JSON(http.StatusCreated, redisEntryResponse{
			Key:        key,
			Value:      value,
			TTLSeconds: ttlSeconds,
		})
	}
}

func redisGetKey(service *redisService) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		key := strings.TrimSpace(e.Request.PathValue("key"))
		if key == "" {
			return e.BadRequestError("Key is required.", nil)
		}

		client, err := service.getClient()
		if err != nil {
			return e.InternalServerError("Failed to initialize Redis client.", err)
		}

		rawValue, err := client.Do(e.Request.Context(), client.B().Get().Key(key).Build()).AsBytes()
		if err != nil {
			if rueidis.IsRedisNil(err) {
				return e.NotFoundError("Key not found.", nil)
			}
			return e.InternalServerError("Failed to read Redis key.", err)
		}

		value, err := decodeRedisValue(json.RawMessage(rawValue))
		if err != nil {
			return e.InternalServerError("Failed to decode Redis value.", err)
		}

		ttlSeconds, err := service.ttlSeconds(e.Request.Context(), client, key)
		if err != nil && !errors.Is(err, errRedisKeyNotFound) {
			return e.InternalServerError("Failed to read Redis key TTL.", err)
		}

		return e.JSON(http.StatusOK, redisEntryResponse{
			Key:        key,
			Value:      value,
			TTLSeconds: ttlSeconds,
		})
	}
}

func redisUpdateKey(service *redisService) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		key := strings.TrimSpace(e.Request.PathValue("key"))
		if key == "" {
			return e.BadRequestError("Key is required.", nil)
		}

		payload := new(redisUpdatePayload)
		if err := e.BindBody(payload); err != nil {
			return e.BadRequestError("An error occurred while loading the submitted data.", err)
		}
		if len(payload.Value) == 0 {
			return e.BadRequestError("Key value is required.", nil)
		}

		ttl, err := normalizeTTLSeconds(payload.TTLSeconds)
		if err != nil {
			return e.BadRequestError(err.Error(), nil)
		}

		client, err := service.getClient()
		if err != nil {
			return e.InternalServerError("Failed to initialize Redis client.", err)
		}

		base := client.B().Set().Key(key).Value(string(payload.Value))

		var completed rueidis.Completed
		switch {
		case ttl == nil:
			completed = base.Xx().Keepttl().Build()
		case *ttl > 0:
			completed = base.Xx().ExSeconds(int64(*ttl)).Build()
		default:
			// ttlSeconds == 0, do not set expiration to remove TTL
			completed = base.Xx().Build()
		}

		if _, err := client.Do(e.Request.Context(), completed).ToString(); err != nil {
			if rueidis.IsRedisNil(err) {
				return e.NotFoundError("Key not found.", nil)
			}
			return e.InternalServerError("Failed to update Redis key.", err)
		}

		value, err := decodeRedisValue(payload.Value)
		if err != nil {
			return e.InternalServerError("Failed to decode Redis value.", err)
		}

		ttlSeconds, err := service.ttlSeconds(e.Request.Context(), client, key)
		if err != nil {
			if errors.Is(err, errRedisKeyNotFound) {
				return e.NotFoundError("Key not found.", nil)
			}
			return e.InternalServerError("Failed to read Redis key TTL.", err)
		}

		return e.JSON(http.StatusOK, redisEntryResponse{
			Key:        key,
			Value:      value,
			TTLSeconds: ttlSeconds,
		})
	}
}

func redisDeleteKey(service *redisService) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		key := strings.TrimSpace(e.Request.PathValue("key"))
		if key == "" {
			return e.BadRequestError("Key is required.", nil)
		}

		client, err := service.getClient()
		if err != nil {
			return e.InternalServerError("Failed to initialize Redis client.", err)
		}

		deleted, err := client.Do(e.Request.Context(), client.B().Del().Key(key).Build()).AsInt64()
		if err != nil {
			return e.InternalServerError("Failed to delete Redis key.", err)
		}
		if deleted == 0 {
			return e.NotFoundError("Key not found.", nil)
		}

		return e.NoContent(http.StatusNoContent)
	}
}

func normalizeTTLSeconds(value *int) (*int, error) {
	if value == nil {
		return nil, nil
	}
	if *value < 0 {
		return nil, errors.New("ttlSeconds must be greater or equal to 0")
	}

	v := *value
	return &v, nil
}

func normalizeRedisURL(url string) string {
	if url == "" {
		return ""
	}

	if strings.Contains(url, "://") {
		return url
	}

	return "redis://" + url
}

func decodeRedisValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("value is required")
	}

	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}

	return value, nil
}
