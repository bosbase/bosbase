package apis

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode"

	"dbx"

	"github.com/coocood/freecache"
	"github.com/redis/rueidis"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/types"
	"github.com/gofrs/uuid/v5"
)

const functionScriptPermissionsTable = "function_script_permissions"
const scriptPermSchemaCacheSize = 50 * 1024 * 1024
const scriptPermissionRecordCacheSize = 50 * 1024 * 1024
const scriptPermissionRecordNotFoundTTLSeconds = 1 * 60 * 60

var (
	scriptPermSchemaCacheOnce       sync.Once
	scriptPermSchemaCache           *freecache.Cache
	scriptPermSchemaInitMu          sync.Mutex
	scriptPermissionRecordCacheOnce sync.Once
	scriptPermissionRecordCache     *freecache.Cache
)

func getScriptPermSchemaCache() *freecache.Cache {
	scriptPermSchemaCacheOnce.Do(func() {
		scriptPermSchemaCache = freecache.NewCache(scriptPermSchemaCacheSize)
	})

	return scriptPermSchemaCache
}

func scriptPermSchemaCacheKey(app core.App) []byte {
	return []byte(functionScriptPermissionsTable)
}

func getScriptPermissionRecordCache() *freecache.Cache {
	scriptPermissionRecordCacheOnce.Do(func() {
		scriptPermissionRecordCache = freecache.NewCache(scriptPermissionRecordCacheSize)
	})

	return scriptPermissionRecordCache
}

func scriptPermissionRecordCacheKey(app core.App, scriptName string) []byte {
	return []byte("script-permission:" + scriptName)
}

func scriptPermissionRecordCacheKeyByScriptID(app core.App, scriptID string) []byte {
	return []byte("script-permission-id:" + scriptID)
}

type scriptPermissionRecord struct {
	ID         string         `json:"id" db:"id"`
	ScriptID   *string        `json:"scriptId,omitempty" db:"script_id"`
	ScriptName string         `json:"scriptName" db:"script_name"`
	Content    string         `json:"content" db:"content"`
	Version    int            `json:"version" db:"version"`
	Created    types.DateTime `json:"created,omitempty" db:"created"`
	Updated    types.DateTime `json:"updated,omitempty" db:"updated"`
}

type scriptPermissionPayload struct {
	ScriptID   *string `json:"script_id" form:"script_id"`
	ScriptName *string `json:"script_name" form:"script_name"`
	Content    *string `json:"content" form:"content"`
}

func bindScriptPermissionsApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/script-permissions").Bind(RequireSuperuserAuth())
	sub.POST("", scriptPermissionCreate)
	sub.GET("/{name}", scriptPermissionGet)
	sub.PATCH("/{name}", scriptPermissionUpdate)
	sub.DELETE("/{name}", scriptPermissionDelete)
}

func scriptPermissionCreate(e *core.RequestEvent) error {
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}

	payload := new(scriptPermissionPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	name := strings.TrimSpace(stringOrEmpty(payload.ScriptName))
	if name == "" {
		return e.BadRequestError("script_name is required.", nil)
	}
	content := strings.TrimSpace(stringOrEmpty(payload.Content))
	if content == "" {
		return e.BadRequestError("content is required.", nil)
	}

	if !isValidPermissionContent(content) {
		return e.BadRequestError("invalid content value", nil)
	}

	scriptID := strings.TrimSpace(stringOrEmpty(payload.ScriptID))

	exists := 0
	if err := e.App.DB().
		Select("COUNT(*)").
		From(functionScriptPermissionsTable).
		Where(dbx.HashExp{"script_name": name}).
		WithContext(e.Request.Context()).
		Row(&exists); err != nil {
		return e.InternalServerError("Failed to check existing permissions.", err)
	}
	if exists > 0 {
		return e.BadRequestError("Permission already exists for this script.", nil)
	}

	insertSQL := fmt.Sprintf(`
		INSERT INTO {{%s}} ([[id]], [[script_id]], [[script_name]], [[content]], [[version]])
		VALUES ({:id}, {:script_id}, {:script_name}, {:content}, 1)
		RETURNING [[id]], [[script_id]], [[script_name]], [[content]], [[version]], [[created]], [[updated]];
	`, functionScriptPermissionsTable)

	record := new(scriptPermissionRecord)
	params := dbx.Params{
		"id":          generatePermissionID(),
		"script_id":   nullIfEmpty(scriptID),
		"script_name": name,
		"content":     content,
	}

	if err := e.App.NonconcurrentDB().
		NewQuery(insertSQL).
		Bind(params).
		WithContext(e.Request.Context()).
		One(record); err != nil {
		return e.InternalServerError("Failed to create permission.", err)
	}

	// local freecache
	var cachedData []byte
	if data, err := json.Marshal(record); err == nil {
		cachedData = data
		_ = getScriptPermissionRecordCache().Set(scriptPermissionRecordCacheKey(e.App, name), data, 0)
		if record.ScriptID != nil && strings.TrimSpace(*record.ScriptID) != "" {
			_ = getScriptPermissionRecordCache().Set(scriptPermissionRecordCacheKeyByScriptID(e.App, strings.TrimSpace(*record.ScriptID)), data, 0)
		}
	}

	// Update Redis cache if configured.
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL != "" && len(cachedData) > 0 {
		if service := ensureRedisService(e.App, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
			if client, err := service.getClient(); err == nil {
				redisKey := "script-permission:" + name
				_ = client.Do(e.Request.Context(), client.B().Set().Key(redisKey).Value(rueidis.BinaryString(cachedData)).Build()).Error()
			}
		}
	}

	return e.JSON(http.StatusCreated, record)
}

func scriptPermissionGet(e *core.RequestEvent) error {
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}
	// allow resolving script id if missing
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}

	name := strings.TrimSpace(e.Request.PathValue("name"))
	if name == "" {
		return e.BadRequestError("script name is required.", nil)
	}

	// track whether script_id was missing before resolving it so we can populate the script_id cache key if needed
	record, err := findScriptPermission(e.Request.Context(), e.App, name)
	if err != nil {
		return e.InternalServerError("Failed to load permission.", err)
	}
	if record == nil {
		return e.NotFoundError("Permission not found.", nil)
	}
	missingScriptIDBefore := record.ScriptID == nil || strings.TrimSpace(*record.ScriptID) == ""

	if err := fillPermissionScriptID(e.Request.Context(), e.App, record); err != nil {
		return e.InternalServerError("Failed to resolve script reference.", err)
	}

	if missingScriptIDBefore {
		if record.ScriptID != nil && strings.TrimSpace(*record.ScriptID) != "" {
			if data, err := json.Marshal(record); err == nil {
				_ = getScriptPermissionRecordCache().Set(scriptPermissionRecordCacheKeyByScriptID(e.App, strings.TrimSpace(*record.ScriptID)), data, 0)
			}
		}
	}

	return e.JSON(http.StatusOK, record)
}

func scriptPermissionUpdate(e *core.RequestEvent) error {
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}

	pathName := strings.TrimSpace(e.Request.PathValue("name"))
	if pathName == "" {
		return e.BadRequestError("script name is required.", nil)
	}

	// best-effort read to invalidate the old script_id cache key (if any)
	oldRecord, _ := findScriptPermission(e.Request.Context(), e.App, pathName)

	payload := new(scriptPermissionPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	hasContent := payload.Content != nil
	hasScriptID := payload.ScriptID != nil
	hasScriptName := payload.ScriptName != nil
	if !hasContent && !hasScriptID && !hasScriptName {
		return e.BadRequestError("At least one field must be provided.", nil)
	}

	values := dbx.Params{
		"script_name_param": pathName,
		"script_id":         nil,
		"script_name":       nil,
		"content":           nil,
	}

	if hasScriptID {
		values["script_id"] = nullIfEmpty(strings.TrimSpace(stringOrEmpty(payload.ScriptID)))
	}
	if hasScriptName {
		name := strings.TrimSpace(stringOrEmpty(payload.ScriptName))
		if name == "" {
			return e.BadRequestError("script_name cannot be empty.", nil)
		}
		values["script_name"] = name
	}
	if hasContent {
		content := strings.TrimSpace(stringOrEmpty(payload.Content))
		if !isValidPermissionContent(content) {
			return e.BadRequestError("invalid content value", nil)
		}
		values["content"] = content
	}

	updateSQL := fmt.Sprintf(`
		UPDATE {{%s}}
		SET
			[[script_id]] = COALESCE({:script_id}, [[script_id]]),
			[[script_name]] = COALESCE({:script_name}, [[script_name]]),
			[[content]] = COALESCE({:content}, [[content]]),
			[[version]] = [[version]] + 1,
			[[updated]] = CURRENT_TIMESTAMP
		WHERE [[script_name]] = {:script_name_param}
		RETURNING [[id]], [[script_id]], [[script_name]], [[content]], [[version]], [[created]], [[updated]];
	`, functionScriptPermissionsTable)

	record := new(scriptPermissionRecord)
	err := e.App.NonconcurrentDB().
		NewQuery(updateSQL).
		Bind(values).
		WithContext(e.Request.Context()).
		One(record)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.NotFoundError("Permission not found.", nil)
		}
		return e.InternalServerError("Failed to update permission.", err)
	}

	// local freecache
	var cachedData []byte
	if data, err := json.Marshal(record); err == nil {
		cachedData = data
		if pathName != record.ScriptName {
			getScriptPermissionRecordCache().Del(scriptPermissionRecordCacheKey(e.App, pathName))
		}
		if oldRecord != nil && oldRecord.ScriptID != nil && strings.TrimSpace(*oldRecord.ScriptID) != "" {
			oldSID := strings.TrimSpace(*oldRecord.ScriptID)
			newSID := ""
			if record.ScriptID != nil {
				newSID = strings.TrimSpace(*record.ScriptID)
			}
			if oldSID != newSID {
				getScriptPermissionRecordCache().Del(scriptPermissionRecordCacheKeyByScriptID(e.App, oldSID))
			}
		}
		_ = getScriptPermissionRecordCache().Set(scriptPermissionRecordCacheKey(e.App, record.ScriptName), data, 0)
		if record.ScriptID != nil && strings.TrimSpace(*record.ScriptID) != "" {
			_ = getScriptPermissionRecordCache().Set(scriptPermissionRecordCacheKeyByScriptID(e.App, strings.TrimSpace(*record.ScriptID)), data, 0)
		}
	}

	// Update Redis cache if configured (clear old key when name changes).
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL != "" && len(cachedData) > 0 {
		if service := ensureRedisService(e.App, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
			if client, err := service.getClient(); err == nil {
				oldKey := "script-permission:" + pathName
				newKey := "script-permission:" + record.ScriptName

				if pathName != record.ScriptName {
					_ = client.Do(e.Request.Context(), client.B().Del().Key(oldKey).Build()).Error()
				}

				_ = client.Do(e.Request.Context(), client.B().Set().Key(newKey).Value(rueidis.BinaryString(cachedData)).Build()).Error()
			}
		}
	}

	return e.JSON(http.StatusOK, record)
}

func scriptPermissionDelete(e *core.RequestEvent) error {
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}

	name := strings.TrimSpace(e.Request.PathValue("name"))
	if name == "" {
		return e.BadRequestError("script name is required.", nil)
	}

	// best-effort read to invalidate the script_id cache key (if any)
	oldRecord, _ := findScriptPermission(e.Request.Context(), e.App, name)

	deleteSQL := fmt.Sprintf(`
		DELETE FROM {{%s}}
		WHERE [[script_name]] = {:name}
		RETURNING [[id]];
	`, functionScriptPermissionsTable)

	var deletedID string
	err := e.App.NonconcurrentDB().
		NewQuery(deleteSQL).
		Bind(dbx.Params{"name": name}).
		WithContext(e.Request.Context()).
		Row(&deletedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.NotFoundError("Permission not found.", nil)
		}
		return e.InternalServerError("Failed to delete permission.", err)
	}

	// Remove Redis cache if configured.
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL != "" {
		if service := ensureRedisService(e.App, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
			if client, err := service.getClient(); err == nil {
				redisKey := "script-permission:" + name
				_ = client.Do(e.Request.Context(), client.B().Del().Key(redisKey).Build()).Error()
			}
		}
	}

	getScriptPermissionRecordCache().Del(scriptPermissionRecordCacheKey(e.App, name))
	if oldRecord != nil && oldRecord.ScriptID != nil && strings.TrimSpace(*oldRecord.ScriptID) != "" {
		getScriptPermissionRecordCache().Del(scriptPermissionRecordCacheKeyByScriptID(e.App, strings.TrimSpace(*oldRecord.ScriptID)))
	}

	return e.NoContent(http.StatusNoContent)
}

func ensureScriptPermissionsSchema(app core.App) error {
	cache := getScriptPermSchemaCache()
	cacheKey := scriptPermSchemaCacheKey(app)
	if _, err := cache.Get(cacheKey); err == nil && app.HasTable(functionScriptPermissionsTable) {
		return nil
	}

	scriptPermSchemaInitMu.Lock()
	defer scriptPermSchemaInitMu.Unlock()

	if _, err := cache.Get(cacheKey); err == nil && app.HasTable(functionScriptPermissionsTable) {
		return nil
	}

	driver := core.BuilderDriverName(app.NonconcurrentDB())
	timestampCreated := core.TimestampColumnDefinition(driver, "created")
	timestampUpdated := core.TimestampColumnDefinition(driver, "updated")

	createSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS {{%s}} (
				[[id]]          TEXT PRIMARY KEY NOT NULL,
				[[script_id]]   TEXT,
				[[script_name]] TEXT NOT NULL UNIQUE,
				[[content]]     TEXT NOT NULL,
				[[version]]     INTEGER NOT NULL DEFAULT 1,
				%s,
				%s
			);
		`, functionScriptPermissionsTable, timestampCreated, timestampUpdated)

	if _, err := app.NonconcurrentDB().NewQuery(createSQL).Execute(); err != nil {
		return err
	}

	_ = cache.Set(cacheKey, []byte{1}, 0)

	return nil
}

func findScriptPermission(ctx context.Context, app core.App, scriptName string) (*scriptPermissionRecord, error) {
	record := new(scriptPermissionRecord)

	// try local freecache
	scriptName = strings.TrimSpace(scriptName)
	if scriptName == "" {
		return nil, nil
	}
	cache := getScriptPermissionRecordCache()
	cacheKey := scriptPermissionRecordCacheKey(app, scriptName)
	if data, err := cache.Get(cacheKey); err == nil {
		if len(data) == 1 && data[0] == 0 {
			return nil, nil
		}
		if unmarshalErr := json.Unmarshal(data, record); unmarshalErr == nil {
			return record, nil
		}
	}

	// Try Redis first if configured.
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	var redisClient rueidis.Client
	var redisKey string

	if redisURL != "" {
		service := ensureRedisService(app, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey)))
		client, err := service.getClient()
		if err == nil {
			redisClient = client
			redisKey = "script-permission:" + scriptName

			if data, err := client.Do(ctx, client.B().Get().Key(redisKey).Build()).AsBytes(); err == nil {
				if unmarshalErr := json.Unmarshal(data, record); unmarshalErr == nil {
					_ = cache.Set(cacheKey, data, 0)
					return record, nil
				}
			} else if err != nil && !errors.Is(err, rueidis.Nil) {
				// error not return , query db
			}
		}
	}

	// Fallback to DB.
	err := app.DB().
		Select("{{" + functionScriptPermissionsTable + "}}.*").
		From(functionScriptPermissionsTable).
		Where(dbx.HashExp{"script_name": scriptName}).
		WithContext(ctx).
		One(record)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if redisClient != nil && redisKey != "" {
				_ = redisClient.Do(ctx, redisClient.B().Del().Key(redisKey).Build()).Error()
			}
			_ = cache.Set(cacheKey, []byte{0}, scriptPermissionRecordNotFoundTTLSeconds)
			return nil, nil
		}
		return nil, err
	}

	// Cache into Redis if configured and fetch succeeded.
	var cachedData []byte
	if data, err := json.Marshal(record); err == nil {
		cachedData = data
		if redisClient != nil && redisKey != "" {
			// Store without expiration; failures are ignored to keep request flow.
			_ = redisClient.Do(ctx, redisClient.B().Set().Key(redisKey).Value(rueidis.BinaryString(data)).Build()).Error()
		}
	}
	if len(cachedData) > 0 {
		_ = cache.Set(cacheKey, cachedData, 0)
	}

	return record, nil
}

func findScriptPermissionByScript(ctx context.Context, app core.App, scriptID, scriptName string) (*scriptPermissionRecord, error) {
	record := new(scriptPermissionRecord)

	scriptID = strings.TrimSpace(scriptID)
	scriptName = strings.TrimSpace(scriptName)

	// try local freecache (by script_name)
	cache := getScriptPermissionRecordCache()
	if scriptName != "" {
		cacheKey := scriptPermissionRecordCacheKey(app, scriptName)
		if data, err := cache.Get(cacheKey); err == nil {
			if len(data) == 1 && data[0] == 0 {
				// not found by name; fallback to id lookup (if provided)
			} else if unmarshalErr := json.Unmarshal(data, record); unmarshalErr == nil {
				if err := fillPermissionScriptID(ctx, app, record); err != nil {
					return nil, err
				}
				// also store under script_id key if available
				if record.ScriptID != nil && strings.TrimSpace(*record.ScriptID) != "" {
					_ = cache.Set(scriptPermissionRecordCacheKeyByScriptID(app, strings.TrimSpace(*record.ScriptID)), data, 0)
				}
				return record, nil
			}
		}
	}

	// try local freecache (by script_id)
	if scriptID != "" {
		cacheKey := scriptPermissionRecordCacheKeyByScriptID(app, scriptID)
		if data, err := cache.Get(cacheKey); err == nil {
			if len(data) == 1 && data[0] == 0 {
				return nil, nil
			}
			if unmarshalErr := json.Unmarshal(data, record); unmarshalErr == nil {
				if err := fillPermissionScriptID(ctx, app, record); err != nil {
					return nil, err
				}
				// keep name cache warm too
				if strings.TrimSpace(record.ScriptName) != "" {
					_ = cache.Set(scriptPermissionRecordCacheKey(app, strings.TrimSpace(record.ScriptName)), data, 0)
				}
				return record, nil
			}
		}
	}

	// try redis
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL != "" && scriptName != "" {
		if service := ensureRedisService(app, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
			if client, err := service.getClient(); err == nil {
				redisKey := "script-permission:" + scriptName
				if data, err := client.Do(ctx, client.B().Get().Key(redisKey).Build()).AsBytes(); err == nil {
					if unmarshalErr := json.Unmarshal(data, record); unmarshalErr == nil {
						if err := fillPermissionScriptID(ctx, app, record); err != nil {
							return nil, err
						}

						_ = cache.Set(scriptPermissionRecordCacheKey(app, strings.TrimSpace(record.ScriptName)), data, 0)
						if record.ScriptID != nil && strings.TrimSpace(*record.ScriptID) != "" {
							_ = cache.Set(scriptPermissionRecordCacheKeyByScriptID(app, strings.TrimSpace(*record.ScriptID)), data, 0)
						}

						return record, nil
					}
				}
			}
		}
	}

	conditions := dbx.NewExp("[[script_name]] = {:script_name}")
	params := dbx.Params{"script_name": scriptName}

	if scriptID != "" {
		conditions = dbx.NewExp("([[script_name]] = {:script_name}) OR ([[script_id]] = {:script_id})")
		params["script_id"] = scriptID
	}

	err := app.DB().
		Select("{{" + functionScriptPermissionsTable + "}}.*").
		From(functionScriptPermissionsTable).
		Where(conditions).
		Bind(params).
		WithContext(ctx).
		One(record)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if scriptName != "" {
				_ = cache.Set(scriptPermissionRecordCacheKey(app, scriptName), []byte{0}, scriptPermissionRecordNotFoundTTLSeconds)
			}
			if scriptID != "" {
				_ = cache.Set(scriptPermissionRecordCacheKeyByScriptID(app, scriptID), []byte{0}, scriptPermissionRecordNotFoundTTLSeconds)
			}
			return nil, nil
		}
		return nil, err
	}

	if err := fillPermissionScriptID(ctx, app, record); err != nil {
		return nil, err
	}

	//add cache to redis

	// warm caches
	if data, mErr := json.Marshal(record); mErr == nil {
		if redisURL != "" {
			if service := ensureRedisService(app, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
				if client, err := service.getClient(); err == nil {
					redisKey := "script-permission:" + strings.TrimSpace(record.ScriptName)
					if redisKey != "script-permission:" {
						_ = client.Do(ctx, client.B().Set().Key(redisKey).Value(rueidis.BinaryString(data)).Build()).Error()
					}
				}
			}
		}

		if strings.TrimSpace(record.ScriptName) != "" {
			_ = cache.Set(scriptPermissionRecordCacheKey(app, strings.TrimSpace(record.ScriptName)), data, 0)
		}
		if record.ScriptID != nil && strings.TrimSpace(*record.ScriptID) != "" {
			_ = cache.Set(scriptPermissionRecordCacheKeyByScriptID(app, strings.TrimSpace(*record.ScriptID)), data, 0)
		}
	}

	return record, nil
}

func generatePermissionID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return core.GenerateDefaultRandomId()
}

func isValidPermissionContent(val string) bool {
	switch strings.ToLower(val) {
	case "anonymous", "user", "superuser":
		return true
	default:
		return false
	}
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func stringOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func fillPermissionScriptID(ctx context.Context, app core.App, perm *scriptPermissionRecord) error {
	if perm == nil {
		return nil
	}

	if perm.ScriptID != nil && strings.TrimSpace(*perm.ScriptID) != "" {
		return nil
	}

	// Ensure scripts table exists before lookup.
	if err := ensureScriptsSchema(app); err != nil {
		return err
	}

	var foundID string
	err := app.DB().
		Select("[[id]]").
		From(functionScriptsTable).
		Where(dbx.HashExp{"name": perm.ScriptName}).
		WithContext(ctx).
		Row(&foundID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	if foundID == "" {
		return nil
	}

	// Update in-memory
	perm.ScriptID = &foundID

	// Persist back so next reads don't need to resolve.
	_, _ = app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		UPDATE {{%s}}
		SET [[script_id]] = {:script_id}
		WHERE [[script_name]] = {:script_name} AND ([[script_id]] IS NULL OR [[script_id]] = '');
	`, functionScriptPermissionsTable)).
		Bind(dbx.Params{
			"script_id":   foundID,
			"script_name": perm.ScriptName,
		}).
		WithContext(ctx).
		Execute()

	return nil
}

func SafeScript(name string) string {
	if name == "" {
		return ""
	}

	name = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, name)

	var sb strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-' || r == ':' {
			sb.WriteRune(r)
		}
	}

	clean := sb.String()

	if clean == "" {
		return ""
	}

	lower := strings.ToLower(clean)
	dangerous := []string{
		"or", "and", "union", "select", "insert", "update", "delete",
		"drop", "alter", "create", "truncate", "exec", "execute",
		"--", "/*", "*/", "xp_", "information_schema", "sleep(", "benchmark(",
		"waitfor", "1=1", "'or", "or'", "1 or", "or 1",
	}
	for _, kw := range dangerous {
		if strings.Contains(lower, kw) {
			return ""
		}
	}

	if len(clean) > 128 {
		clean = clean[:128]
	}

	return clean
}
