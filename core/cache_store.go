package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"dbx"
	"github.com/coocood/freecache"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

const (
	cacheConfigsTable = "_cache_configs"
	cacheEntriesTable = "_cache_entries"

	defaultCacheSizeBytes = 8 * 1024 * 1024
	minCacheSizeBytes     = 512 * 1024
	maxCacheSizeBytes     = 512 * 1024 * 1024

	defaultCacheTTL = 5 * time.Minute
	maxCacheTTL     = 24 * time.Hour

	defaultReadTimeout = 25 * time.Millisecond
	maxReadTimeout     = time.Second

	maxCacheKeyLength = 512
)

var (
	cacheNamePattern      = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]{3,64}$`)
	ErrCacheNotFound      = errors.New("cache not found")
	ErrCacheEntryNotFound = errors.New("cache entry not found")
)

// CacheSource indicates where the returned entry originated from.
type CacheSource string

const (
	CacheSourceMemory   CacheSource = "cache"
	CacheSourceDatabase CacheSource = "database"
)

// CacheConfig defines the persisted configuration of a cache bucket.
type CacheConfig struct {
	Name        string         `json:"name"`
	SizeBytes   int            `json:"sizeBytes"`
	DefaultTTL  time.Duration  `json:"defaultTTL"`
	ReadTimeout time.Duration  `json:"readTimeout"`
	Created     types.DateTime `json:"created"`
	Updated     types.DateTime `json:"updated"`
}

// CacheConfigPatch describes the allowed configuration mutations.
type CacheConfigPatch struct {
	SizeBytes   *int
	DefaultTTL  *time.Duration
	ReadTimeout *time.Duration
}

// CacheEntry represents a single logical key/value stored in cache.
type CacheEntry struct {
	Cache     string         `json:"cache"`
	Key       string         `json:"key"`
	Value     []byte         `json:"value"`
	ExpiresAt types.DateTime `json:"expiresAt"`
	Source    CacheSource    `json:"source"`
}

type cacheRuntime struct {
	cache  *freecache.Cache
	config CacheConfig
}

// CacheStats contains runtime statistics for a cache.
type CacheStats struct {
	EntryCount        *int64   // Number of entries in memory cache
	HitRate           *float64 // Cache hit rate (0.0 to 1.0)
	HitCount          *int64   // Total number of cache hits
	MissCount         *int64   // Total number of cache misses
	DatabaseEntryCount *int64  // Number of entries in database
}

// CacheStore manages FreeCache-backed caches with database persistence and
// cluster-friendly fallbacks.
type CacheStore struct {
	app App

	mu          sync.RWMutex
	caches      map[string]*cacheRuntime
	schemaMu    sync.Mutex
	schemaReady bool
}

// NewCacheStore initializes a CacheStore instance bound to the provided app.
func NewCacheStore(app App) *CacheStore {
	return &CacheStore{
		app:    app,
		caches: make(map[string]*cacheRuntime),
	}
}

// Warmup ensures that every persisted cache configuration has an initialized
// in-memory counterpart. It is idempotent and safe to call multiple times.
func (s *CacheStore) Warmup(ctx context.Context) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}

	configs, err := s.ListConfigs(ctx)
	if err != nil {
		return err
	}

	for _, cfg := range configs {
		if _, err := s.ensureRuntime(cfg); err != nil {
			return err
		}
	}

	return nil
}

// Close releases all in-memory cache references. It is typically invoked
// during app shutdown/reset.
func (s *CacheStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, runtime := range s.caches {
		if runtime != nil && runtime.cache != nil {
			runtime.cache.Clear()
		}
		delete(s.caches, name)
	}
}

// ListConfigs returns all persisted cache configurations.
func (s *CacheStore) ListConfigs(ctx context.Context) ([]CacheConfig, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}

	var models []cacheConfigModel
	err := s.app.DB().
		NewQuery(fmt.Sprintf(`
			SELECT
				[[name]],
				[[size]],
				[[defaultTTLSeconds]],
				[[readTimeoutMs]],
				[[created]],
				[[updated]]
			FROM {{%s}}
			ORDER BY [[name]] ASC
		`, cacheConfigsTable)).
		WithContext(ctx).
		All(&models)
	if err != nil {
		return nil, err
	}

	result := make([]CacheConfig, len(models))
	for i, m := range models {
		result[i] = m.toConfig()
	}
	return result, nil
}

// CreateCache persists and initializes a new cache configuration.
func (s *CacheStore) CreateCache(ctx context.Context, cfg CacheConfig) (CacheConfig, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return CacheConfig{}, err
	}
	if err := validateCacheName(cfg.Name); err != nil {
		return CacheConfig{}, err
	}

	cfg = sanitizeConfig(cfg)
	now := types.NowDateTime()
	cfg.Created = now
	cfg.Updated = now

	if _, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} (
			[[name]], [[size]], [[defaultTTLSeconds]], [[readTimeoutMs]], [[created]], [[updated]]
		) VALUES ({:name}, {:size}, {:ttl}, {:timeout}, {:created}, {:updated})
	`, cacheConfigsTable)).Bind(dbx.Params{
		"name":    cfg.Name,
		"size":    cfg.SizeBytes,
		"ttl":     durationToSeconds(cfg.DefaultTTL),
		"timeout": durationToMilliseconds(cfg.ReadTimeout),
		"created": cfg.Created,
		"updated": cfg.Updated,
	}).WithContext(ctx).Execute(); err != nil {
		return CacheConfig{}, err
	}

	runtime, err := s.ensureRuntime(cfg)
	if err != nil {
		return CacheConfig{}, err
	}

	return runtime.config, nil
}

// UpdateCache mutates an existing cache configuration.
func (s *CacheStore) UpdateCache(ctx context.Context, name string, patch CacheConfigPatch) (CacheConfig, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return CacheConfig{}, err
	}
	runtime, err := s.cacheRuntime(ctx, name)
	if err != nil {
		return CacheConfig{}, err
	}

	cfg := runtime.config

	if patch.SizeBytes != nil {
		cfg.SizeBytes = clampSize(*patch.SizeBytes)
	}
	if patch.DefaultTTL != nil {
		cfg.DefaultTTL = clampTTL(*patch.DefaultTTL)
	}
	if patch.ReadTimeout != nil {
		cfg.ReadTimeout = clampReadTimeout(*patch.ReadTimeout)
	}

	cfg.Updated = types.NowDateTime()

	res, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		UPDATE {{%s}}
		SET
			[[size]] = {:size},
			[[defaultTTLSeconds]] = {:ttl},
			[[readTimeoutMs]] = {:timeout},
			[[updated]] = {:updated}
		WHERE [[name]] = {:name}
	`, cacheConfigsTable)).Bind(dbx.Params{
		"size":    cfg.SizeBytes,
		"ttl":     durationToSeconds(cfg.DefaultTTL),
		"timeout": durationToMilliseconds(cfg.ReadTimeout),
		"updated": cfg.Updated,
		"name":    cfg.Name,
	}).WithContext(ctx).Execute()
	if err != nil {
		return CacheConfig{}, err
	}

	if affected, _ := res.RowsAffected(); affected == 0 {
		return CacheConfig{}, ErrCacheNotFound
	}

	runtime, err = s.ensureRuntime(cfg)
	if err != nil {
		return CacheConfig{}, err
	}

	return runtime.config, nil
}

// DeleteCache removes the cache configuration and all entries.
func (s *CacheStore) DeleteCache(ctx context.Context, name string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}

	if _, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}} WHERE [[cache]] = {:name}
	`, cacheEntriesTable)).Bind(dbx.Params{
		"name": name,
	}).WithContext(ctx).Execute(); err != nil {
		return err
	}

	res, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}} WHERE [[name]] = {:name}
	`, cacheConfigsTable)).Bind(dbx.Params{
		"name": name,
	}).WithContext(ctx).Execute()
	if err != nil {
		return err
	}

	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrCacheNotFound
	}

	s.mu.Lock()
	if runtime, ok := s.caches[name]; ok {
		if runtime.cache != nil {
			runtime.cache.Clear()
		}
		delete(s.caches, name)
	}
	s.mu.Unlock()

	return nil
}

// SetEntry saves a cache entry both in FreeCache and in the backing DB.
func (s *CacheStore) SetEntry(ctx context.Context, cacheName, key string, value []byte, ttl time.Duration) (*CacheEntry, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	if err := validateCacheKey(key); err != nil {
		return nil, err
	}

	runtime, err := s.cacheRuntime(ctx, cacheName)
	if err != nil {
		return nil, err
	}

	if len(value) == 0 {
		value = []byte("null")
	}

	if exceedLimit(len(value), runtime.config.SizeBytes) {
		return nil, freecache.ErrLargeEntry
	}

	ttl = resolveTTL(ttl, runtime.config.DefaultTTL)
	expireAtUnix := int64(0)
	if ttl > 0 {
		expireAtUnix = time.Now().Add(ttl).Unix()
	}

	if err := runtime.cache.Set([]byte(key), value, int(ttl.Seconds())); err != nil {
		return nil, err
	}

	now := types.NowDateTime()

	if _, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} (
			[[cache]], [[key]], [[value]], [[expiresAt]], [[created]], [[updated]]
		) VALUES ({:cache}, {:key}, {:value}, {:expiresAt}, {:now}, {:now})
		ON CONFLICT([[cache]], [[key]]) DO UPDATE SET
			[[value]] = excluded.[[value]],
			[[expiresAt]] = excluded.[[expiresAt]],
			[[updated]] = excluded.[[updated]]
	`, cacheEntriesTable)).Bind(dbx.Params{
		"cache":     cacheName,
		"key":       key,
		"value":     value,
		"expiresAt": expireAtUnix,
		"now":       now,
	}).WithContext(ctx).Execute(); err != nil {
		return nil, err
	}

	return &CacheEntry{
		Cache:     cacheName,
		Key:       key,
		Value:     append([]byte(nil), value...),
		ExpiresAt: unixToDateTime(expireAtUnix),
		Source:    CacheSourceMemory,
	}, nil
}

// GetEntry fetches a cache entry preferring memory and falling back to DB.
func (s *CacheStore) GetEntry(ctx context.Context, cacheName, key string) (*CacheEntry, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	runtime, err := s.cacheRuntime(ctx, cacheName)
	if err != nil {
		return nil, err
	}

	var (
		value    []byte
		expireAt uint32
	)

	if runtime.config.ReadTimeout > 0 {
		value, expireAt, err = runtime.cache.GetWithExpirationTimeout([]byte(key), runtime.config.ReadTimeout)
	} else {
		value, expireAt, err = runtime.cache.GetWithExpiration([]byte(key))
	}

	if err == nil {
		return &CacheEntry{
			Cache:     cacheName,
			Key:       key,
			Value:     append([]byte(nil), value...),
			ExpiresAt: unixToDateTime(int64(expireAt)),
			Source:    CacheSourceMemory,
		}, nil
	}

	if !errors.Is(err, freecache.ErrNotFound) && !errors.Is(err, freecache.ErrSegmentTimeout) {
		return nil, err
	}

	model, dbErr := s.loadEntry(ctx, cacheName, key)
	if dbErr != nil {
		if errors.Is(dbErr, sql.ErrNoRows) {
			return nil, ErrCacheEntryNotFound
		}
		return nil, dbErr
	}

	if model.isExpired(time.Now()) {
		_ = s.deleteEntryRow(ctx, cacheName, key)
		runtime.cache.Del([]byte(key))
		return nil, ErrCacheEntryNotFound
	}

	ttlSeconds := model.remainingTTLSeconds(time.Now())
	if err := runtime.cache.Set([]byte(key), model.Value, ttlSeconds); err != nil {
		return nil, err
	}

	return model.toEntry(CacheSourceDatabase), nil
}

// RenewEntry extends the TTL of an existing cache entry without changing its value.
// If the entry doesn't exist, it returns ErrCacheEntryNotFound.
func (s *CacheStore) RenewEntry(ctx context.Context, cacheName, key string, ttl time.Duration) (*CacheEntry, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	if err := validateCacheKey(key); err != nil {
		return nil, err
	}

	runtime, err := s.cacheRuntime(ctx, cacheName)
	if err != nil {
		return nil, err
	}

	// First, try to get the existing entry
	entry, err := s.GetEntry(ctx, cacheName, key)
	if err != nil {
		return nil, err
	}

	// Resolve TTL - if not provided, use the cache's default TTL
	ttl = resolveTTL(ttl, runtime.config.DefaultTTL)
	expireAtUnix := int64(0)
	if ttl > 0 {
		expireAtUnix = time.Now().Add(ttl).Unix()
	}

	// Update the entry in memory cache
	if err := runtime.cache.Set([]byte(key), entry.Value, int(ttl.Seconds())); err != nil {
		return nil, err
	}

	// Update the entry in the database
	now := types.NowDateTime()
	res, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		UPDATE {{%s}}
		SET [[expiresAt]] = {:expiresAt}, [[updated]] = {:updated}
		WHERE [[cache]] = {:cache} AND [[key]] = {:key}
	`, cacheEntriesTable)).Bind(dbx.Params{
		"cache":     cacheName,
		"key":       key,
		"expiresAt": expireAtUnix,
		"updated":   now,
	}).WithContext(ctx).Execute()
	if err != nil {
		return nil, err
	}

	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, ErrCacheEntryNotFound
	}

	return &CacheEntry{
		Cache:     cacheName,
		Key:       key,
		Value:     append([]byte(nil), entry.Value...),
		ExpiresAt: unixToDateTime(expireAtUnix),
		Source:    CacheSourceMemory,
	}, nil
}

// DeleteEntry removes a cache entry.
func (s *CacheStore) DeleteEntry(ctx context.Context, cacheName, key string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	runtime, err := s.cacheRuntime(ctx, cacheName)
	if err != nil {
		return err
	}

	runtime.cache.Del([]byte(key))

	res, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}} WHERE [[cache]] = {:cache} AND [[key]] = {:key}
	`, cacheEntriesTable)).Bind(dbx.Params{
		"cache": cacheName,
		"key":   key,
	}).WithContext(ctx).Execute()
	if err != nil {
		return err
	}

	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrCacheEntryNotFound
	}

	return nil
}

func (s *CacheStore) cacheRuntime(ctx context.Context, name string) (*cacheRuntime, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	runtime := s.caches[name]
	s.mu.RUnlock()

	if runtime != nil {
		return runtime, nil
	}

	model, err := s.loadConfig(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.autoCreateCache(ctx, name)
		}
		return nil, err
	}

	return s.ensureRuntime(model.toConfig())
}

// GetCacheStats returns runtime statistics for a cache if it's active.
// Returns nil if the cache is not currently active in memory.
func (s *CacheStore) GetCacheStats(ctx context.Context, name string) *CacheStats {
	if err := s.ensureSchema(ctx); err != nil {
		return nil
	}
	
	s.mu.RLock()
	runtime := s.caches[name]
	s.mu.RUnlock()
	
	if runtime == nil {
		// Try to load and ensure runtime
		var err error
		runtime, err = s.cacheRuntime(ctx, name)
		if err != nil || runtime == nil {
			return nil
		}
	}
	
	entryCount := runtime.cache.EntryCount()
	hitRate := runtime.cache.HitRate()
	hitCount := runtime.cache.HitCount()
	missCount := runtime.cache.MissCount()
	
	stats := &CacheStats{
		EntryCount: &entryCount,
		HitRate:    &hitRate,
		HitCount:    &hitCount,
		MissCount:   &missCount,
	}
	
	// Get database entry count
	var dbCount int64
	if err := s.app.DB().NewQuery(fmt.Sprintf(`
		SELECT COUNT(*) FROM {{%s}} WHERE [[cache]] = {:cache}
	`, cacheEntriesTable)).Bind(dbx.Params{
		"cache": name,
	}).WithContext(ctx).Row(&dbCount); err == nil {
		stats.DatabaseEntryCount = &dbCount
	}
	
	return stats
}

func (s *CacheStore) ensureRuntime(cfg CacheConfig) (*cacheRuntime, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.caches == nil {
		s.caches = make(map[string]*cacheRuntime)
	}

	if runtime, ok := s.caches[cfg.Name]; ok {
		if runtime.config.SizeBytes != cfg.SizeBytes {
			runtime.cache = freecache.NewCache(cfg.SizeBytes)
		}
		runtime.config = cfg
		return runtime, nil
	}

	instance := &cacheRuntime{
		cache:  freecache.NewCache(cfg.SizeBytes),
		config: cfg,
	}
	s.caches[cfg.Name] = instance
	return instance, nil
}

func (s *CacheStore) autoCreateCache(ctx context.Context, name string) (*cacheRuntime, error) {
	if err := validateCacheName(name); err != nil {
		return nil, err
	}

	cfg := CacheConfig{Name: name}
	if _, err := s.CreateCache(ctx, cfg); err != nil {
		return nil, err
	}

	s.mu.RLock()
	runtime := s.caches[name]
	s.mu.RUnlock()
	if runtime != nil {
		return runtime, nil
	}

	model, err := s.loadConfig(ctx, name)
	if err != nil {
		return nil, err
	}

	return s.ensureRuntime(model.toConfig())
}

func (s *CacheStore) loadConfig(ctx context.Context, name string) (cacheConfigModel, error) {
	model := cacheConfigModel{}
	err := s.app.DB().
		NewQuery(fmt.Sprintf(`
			SELECT
				[[name]], [[size]], [[defaultTTLSeconds]], [[readTimeoutMs]], [[created]], [[updated]]
			FROM {{%s}}
			WHERE [[name]] = {:name}
			LIMIT 1
		`, cacheConfigsTable)).
		Bind(dbx.Params{"name": name}).
		WithContext(ctx).
		One(&model)
	return model, err
}

func (s *CacheStore) loadEntry(ctx context.Context, cacheName, key string) (*cacheEntryModel, error) {
	model := new(cacheEntryModel)
	err := s.app.DB().NewQuery(fmt.Sprintf(`
		SELECT [[cache]], [[key]], [[value]], [[expiresAt]], [[created]], [[updated]]
		FROM {{%s}}
		WHERE [[cache]] = {:cache} AND [[key]] = {:key}
		LIMIT 1
	`, cacheEntriesTable)).Bind(dbx.Params{
		"cache": cacheName,
		"key":   key,
	}).WithContext(ctx).One(model)
	return model, err
}

func (s *CacheStore) deleteEntryRow(ctx context.Context, cacheName, key string) error {
	_, err := s.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}} WHERE [[cache]] = {:cache} AND [[key]] = {:key}
	`, cacheEntriesTable)).Bind(dbx.Params{
		"cache": cacheName,
		"key":   key,
	}).WithContext(ctx).Execute()
	return err
}

func (s *CacheStore) ensureSchema(ctx context.Context) error {
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()

	if s.schemaReady {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	driver := BuilderDriverName(s.app.NonconcurrentDB())
	timestampCreated := TimestampColumnDefinition(driver, "created")
	timestampUpdated := TimestampColumnDefinition(driver, "updated")

	configsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS {{%s}} (
			[[name]]                TEXT PRIMARY KEY NOT NULL,
			[[size]]                INTEGER NOT NULL,
			[[defaultTTLSeconds]]   INTEGER NOT NULL DEFAULT 0,
			[[readTimeoutMs]]       INTEGER NOT NULL DEFAULT 0,
			%s,
			%s
		);
	`, cacheConfigsTable, timestampCreated, timestampUpdated)

	entriesSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS {{%s}} (
			[[cache]]     TEXT NOT NULL,
			[[key]]       TEXT NOT NULL,
			[[value]]     BYTEA NOT NULL,
			[[expiresAt]] BIGINT NOT NULL DEFAULT 0,
			%s,
			%s,
			PRIMARY KEY ([[cache]], [[key]]),
			FOREIGN KEY([[cache]]) REFERENCES {{%s}}([[name]]) ON DELETE CASCADE
		);
	`, cacheEntriesTable, timestampCreated, timestampUpdated, cacheConfigsTable)

	indexSQL := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx__cache_entries_expires ON {{%s}} ([[expiresAt]]);
	`, cacheEntriesTable)

	statements := []string{configsSQL, entriesSQL, indexSQL}

	for _, stmt := range statements {
		if _, err := s.app.NonconcurrentDB().NewQuery(stmt).WithContext(ctx).Execute(); err != nil {
			return err
		}
	}

	s.schemaReady = true
	return nil
}

type cacheConfigModel struct {
	Name              string         `db:"name"`
	Size              int            `db:"size"`
	DefaultTTLSeconds int            `db:"defaultTTLSeconds"`
	ReadTimeoutMs     int            `db:"readTimeoutMs"`
	Created           types.DateTime `db:"created"`
	Updated           types.DateTime `db:"updated"`
}

func (m cacheConfigModel) toConfig() CacheConfig {
	return CacheConfig{
		Name:        m.Name,
		SizeBytes:   clampSize(m.Size),
		DefaultTTL:  clampTTL(time.Duration(m.DefaultTTLSeconds) * time.Second),
		ReadTimeout: clampReadTimeout(time.Duration(m.ReadTimeoutMs) * time.Millisecond),
		Created:     m.Created,
		Updated:     m.Updated,
	}
}

type cacheEntryModel struct {
	Cache     string         `db:"cache"`
	Key       string         `db:"key"`
	Value     []byte         `db:"value"`
	ExpiresAt int64          `db:"expiresAt"`
	Created   types.DateTime `db:"created"`
	Updated   types.DateTime `db:"updated"`
}

func (m *cacheEntryModel) isExpired(now time.Time) bool {
	return m.ExpiresAt > 0 && now.Unix() >= m.ExpiresAt
}

func (m *cacheEntryModel) remainingTTLSeconds(now time.Time) int {
	if m.ExpiresAt == 0 {
		return 0
	}
	diff := int(m.ExpiresAt - now.Unix())
	if diff < 1 {
		return 1
	}
	return diff
}

func (m *cacheEntryModel) toEntry(source CacheSource) *CacheEntry {
	return &CacheEntry{
		Cache:     m.Cache,
		Key:       m.Key,
		Value:     append([]byte(nil), m.Value...),
		ExpiresAt: unixToDateTime(m.ExpiresAt),
		Source:    source,
	}
}

func validateCacheName(name string) error {
	if !cacheNamePattern.MatchString(name) {
		return fmt.Errorf("invalid cache name %q", name)
	}
	return nil
}

func validateCacheKey(key string) error {
	if key == "" {
		return errors.New("cache key cannot be empty")
	}
	if len(key) > maxCacheKeyLength {
		return fmt.Errorf("cache key is limited to %d characters", maxCacheKeyLength)
	}
	return nil
}

func sanitizeConfig(cfg CacheConfig) CacheConfig {
	cfg.SizeBytes = clampSize(cfg.SizeBytes)
	cfg.DefaultTTL = clampTTL(cfg.DefaultTTL)
	cfg.ReadTimeout = clampReadTimeout(cfg.ReadTimeout)
	return cfg
}

func clampSize(size int) int {
	if size <= 0 {
		return defaultCacheSizeBytes
	}
	if size < minCacheSizeBytes {
		return minCacheSizeBytes
	}
	if size > maxCacheSizeBytes {
		return maxCacheSizeBytes
	}
	return size
}

func clampTTL(ttl time.Duration) time.Duration {
	if ttl < 0 {
		return defaultCacheTTL
	}
	if ttl == 0 {
		return 0
	}
	if ttl > maxCacheTTL {
		return maxCacheTTL
	}
	return ttl
}

func clampReadTimeout(timeout time.Duration) time.Duration {
	if timeout < 0 {
		return defaultReadTimeout
	}
	if timeout == 0 {
		return 0
	}
	if timeout > maxReadTimeout {
		return maxReadTimeout
	}
	return timeout
}

func resolveTTL(ttl time.Duration, fallback time.Duration) time.Duration {
	if ttl < 0 {
		return fallback
	}
	if ttl == 0 {
		return 0
	}
	return clampTTL(ttl)
}

func exceedLimit(valueSize int, cacheSize int) bool {
	limit := cacheSize / 1024
	if limit <= 0 {
		limit = 1
	}
	return valueSize > limit
}

func durationToSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d / time.Second)
}

func durationToMilliseconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d / time.Millisecond)
}

func unixToDateTime(unix int64) types.DateTime {
	if unix <= 0 {
		return types.DateTime{}
	}
	dt, _ := types.ParseDateTime(time.Unix(unix, 0).UTC())
	return dt
}
