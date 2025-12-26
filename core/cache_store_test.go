package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
)

func TestCacheStoreCreateAndList(t *testing.T) {
	t.Parallel()

	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	store := app.CacheStore()
	ctx := context.Background()

	created, err := store.CreateCache(ctx, core.CacheConfig{
		Name:        "sessions",
		SizeBytes:   2 * 1024 * 1024,
		DefaultTTL:  2 * time.Minute,
		ReadTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	if created.Name != "sessions" {
		t.Fatalf("expected cache name sessions, got %s", created.Name)
	}

	configs, err := store.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("failed to list configs: %v", err)
	}

	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}

	if configs[0].DefaultTTL != 2*time.Minute {
		t.Fatalf("expected default ttl 2m, got %v", configs[0].DefaultTTL)
	}

	if configs[0].ReadTimeout != 50*time.Millisecond {
		t.Fatalf("expected read timeout 50ms, got %v", configs[0].ReadTimeout)
	}
}

func TestCacheStoreGetEntryFallsBackToDatabase(t *testing.T) {
	t.Parallel()

	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	store := app.CacheStore()
	ctx := context.Background()

	if _, err := store.CreateCache(ctx, core.CacheConfig{Name: "ai-cache"}); err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	value := []byte(`{"hello":"world"}`)
	if _, err := store.SetEntry(ctx, "ai-cache", "dialog:1", value, 0); err != nil {
		t.Fatalf("failed to set entry: %v", err)
	}

	coldStore := core.NewCacheStore(app)
	entry, err := coldStore.GetEntry(ctx, "ai-cache", "dialog:1")
	if err != nil {
		t.Fatalf("failed to get entry: %v", err)
	}

	if entry.Source != core.CacheSourceDatabase {
		t.Fatalf("expected source database, got %s", entry.Source)
	}

	if string(entry.Value) != string(value) {
		t.Fatalf("expected value %s, got %s", value, entry.Value)
	}
}

func TestCacheStoreExpiredEntriesArePurged(t *testing.T) {
	t.Parallel()

	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	store := app.CacheStore()
	ctx := context.Background()

	if _, err := store.CreateCache(ctx, core.CacheConfig{Name: "ephemeral"}); err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	if _, err := store.SetEntry(ctx, "ephemeral", "temp", []byte(`"foo"`), time.Second); err != nil {
		t.Fatalf("failed to set entry: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	if _, err := store.GetEntry(ctx, "ephemeral", "temp"); !errors.Is(err, core.ErrCacheEntryNotFound) {
		t.Fatalf("expected ErrCacheEntryNotFound, got %v", err)
	}

	var count int
	if err := app.DB().Select("count(*)").
		From("_cache_entries").
		Where(dbx.HashExp{"cache": "ephemeral", "key": "temp"}).
		Row(&count); err != nil {
		t.Fatalf("failed to query cache entries: %v", err)
	}

	if count != 0 {
		t.Fatalf("expected cache entry row to be removed, got %d", count)
	}
}

func TestCacheStoreRenewEntry(t *testing.T) {
	t.Parallel()

	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	store := app.CacheStore()
	ctx := context.Background()

	if _, err := store.CreateCache(ctx, core.CacheConfig{
		Name:       "renew-test",
		DefaultTTL: 5 * time.Minute,
	}); err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	value := []byte(`{"test":"data"}`)
	originalTTL := 10 * time.Second

	// Set an entry with a short TTL
	entry, err := store.SetEntry(ctx, "renew-test", "key1", value, originalTTL)
	if err != nil {
		t.Fatalf("failed to set entry: %v", err)
	}

	originalExpiresAt := entry.ExpiresAt

	// Wait a bit to ensure time has passed
	time.Sleep(100 * time.Millisecond)

	// Renew the entry with a longer TTL
	newTTL := 30 * time.Second
	renewed, err := store.RenewEntry(ctx, "renew-test", "key1", newTTL)
	if err != nil {
		t.Fatalf("failed to renew entry: %v", err)
	}

	// Verify the value hasn't changed
	if string(renewed.Value) != string(value) {
		t.Fatalf("expected value %s, got %s", value, renewed.Value)
	}

	// Verify the expiration time has been extended
	if !renewed.ExpiresAt.After(originalExpiresAt) {
		t.Fatalf("expected renewed expiration to be after original, got %v vs %v", renewed.ExpiresAt, originalExpiresAt)
	}

	// Verify the entry can still be retrieved
	retrieved, err := store.GetEntry(ctx, "renew-test", "key1")
	if err != nil {
		t.Fatalf("failed to get renewed entry: %v", err)
	}

	if string(retrieved.Value) != string(value) {
		t.Fatalf("expected value %s, got %s", value, retrieved.Value)
	}
}

func TestCacheStoreRenewEntryNotFound(t *testing.T) {
	t.Parallel()

	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	store := app.CacheStore()
	ctx := context.Background()

	if _, err := store.CreateCache(ctx, core.CacheConfig{Name: "renew-test"}); err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Try to renew a non-existent entry
	_, err := store.RenewEntry(ctx, "renew-test", "nonexistent", 30*time.Second)
	if !errors.Is(err, core.ErrCacheEntryNotFound) {
		t.Fatalf("expected ErrCacheEntryNotFound, got %v", err)
	}
}

func TestCacheStoreRenewEntryUsesDefaultTTL(t *testing.T) {
	t.Parallel()

	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	store := app.CacheStore()
	ctx := context.Background()

	defaultTTL := 15 * time.Minute
	if _, err := store.CreateCache(ctx, core.CacheConfig{
		Name:       "renew-default",
		DefaultTTL: defaultTTL,
	}); err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	value := []byte(`{"test":"default"}`)
	if _, err := store.SetEntry(ctx, "renew-default", "key1", value, 10*time.Second); err != nil {
		t.Fatalf("failed to set entry: %v", err)
	}

	// Renew without specifying TTL (should use default)
	renewed, err := store.RenewEntry(ctx, "renew-default", "key1", -1*time.Second)
	if err != nil {
		t.Fatalf("failed to renew entry: %v", err)
	}

	// Verify the expiration is approximately defaultTTL from now
	expectedExpiresAt := time.Now().Add(defaultTTL)
	diff := renewed.ExpiresAt.Time().Sub(expectedExpiresAt)
	if diff < 0 {
		diff = -diff
	}
	if diff > 5*time.Second {
		t.Fatalf("expected expiration to be approximately %v from now, got %v (diff: %v)", defaultTTL, renewed.ExpiresAt, diff)
	}
}
