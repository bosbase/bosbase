package core

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/bosbase/bosbase-enterprise/tools/hook"
	"github.com/coocood/freecache"
)

const (
	activationStatusCacheTTL = 24 * time.Hour
	activationStatusCacheKey = "activation_status"
)

var (
	activationCacheOnce sync.Once
	activationCache     *freecache.Cache
)

func getActivationCache() *freecache.Cache {
	activationCacheOnce.Do(func() {
		// Small cache (512KB min) - only stores one activation status entry
		activationCache = freecache.NewCache(512 * 1024)
	})
	return activationCache
}

// getCachedActivationStatus returns cached status if available and not expired.
func getCachedActivationStatus() (*ActivationStatus, bool) {
	cache := getActivationCache()
	data, err := cache.Get([]byte(activationStatusCacheKey))
	if err != nil {
		return nil, false
	}
	var status ActivationStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, false
	}
	return &status, true
}

// cacheActivationStatus stores the activation status in cache for 24h.
func cacheActivationStatus(status ActivationStatus) {
	cache := getActivationCache()
	data, err := json.Marshal(status)
	if err != nil {
		return
	}
	_ = cache.Set([]byte(activationStatusCacheKey), data, int(activationStatusCacheTTL.Seconds()))
}

// InvalidateActivationStatusCache clears the cached activation status.
// Call this when activation settings change.
func InvalidateActivationStatusCache() {
	getActivationCache().Del([]byte(activationStatusCacheKey))
}

// registerActivationHooks wires activation lifecycle helpers.
func (app *BaseApp) registerActivationHooks() {
	app.OnRecordAuthRequest(CollectionNameSuperusers).Bind(&hook.Handler[*RecordAuthRequestEvent]{
		Id: "__pbActivationCheck__",
		Func: func(e *RecordAuthRequestEvent) error {
			settings := e.App.Settings()
			now := time.Now()

			// Start trial if not already started
			changed := settings.StartTrialIfUnset(now)
			if changed {
				if err := e.App.Save(settings); err != nil {
					return err
				}
			}

			// Get status from cache or compute fresh
			var status ActivationStatus
			if cached, ok := getCachedActivationStatus(); ok && !changed {
				status = *cached
			} else {
				status = settings.CurrentActivationStatus(now)
				cacheActivationStatus(status)
			}
			if e.Meta == nil {
				e.Meta = status
			}

			// Allow during trial; enforce only after trial ends or subscription expires.
			if status.IsTrial || status.Activated {
				return e.Next()
			}

			if status.RequiresActivation {
				return e.BadRequestError("Activation expired. Please purchase a product.", nil)
			}

			return e.Next()
		},
		Priority: 10,
	})
}
