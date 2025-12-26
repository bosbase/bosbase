package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tests"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

func TestSuperuserAuthAllowsTrial(t *testing.T) {
	// Clear cache before test to ensure fresh state
	core.InvalidateActivationStatusCache()

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatalf("failed to init test app: %v", err)
	}
	defer app.Cleanup()

	// ensure trial is active
	app.Settings().StartTrialIfUnset(time.Now().Add(-24 * time.Hour))

	event := &core.RecordAuthRequestEvent{
		RequestEvent: &core.RequestEvent{App: app},
	}

	err = app.OnRecordAuthRequest(core.CollectionNameSuperusers).Trigger(event, func(e *core.RecordAuthRequestEvent) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected trial login to succeed, got error: %v", err)
	}
}

func TestSuperuserAuthBlockedAfterExpiry(t *testing.T) {
	// Clear cache before test to ensure fresh state
	core.InvalidateActivationStatusCache()

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatalf("failed to init test app: %v", err)
	}
	defer app.Cleanup()

	now := time.Now()
	var started, expired types.DateTime
	_ = started.Scan(now.Add(-48 * time.Hour))
	_ = expired.Scan(now.Add(-1 * time.Hour))

	app.Settings().Activation.TrialStartedAt = started
	app.Settings().Activation.TrialExpiresAt = expired
	app.Settings().Activation.SubscriptionExpires = expired

	event := &core.RecordAuthRequestEvent{
		RequestEvent: &core.RequestEvent{App: app},
	}

	err = app.OnRecordAuthRequest(core.CollectionNameSuperusers).Trigger(event, func(e *core.RecordAuthRequestEvent) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected expiry to block login, got nil error")
	}

	apiErr, ok := err.(*router.ApiError)
	if !ok {
		t.Fatalf("expected router.ApiError, got %T", err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("expected status 400, got %d", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message, "Activation expired") {
		t.Fatalf("unexpected error message: %s", apiErr.Message)
	}
}

func TestActivationStatusCaching(t *testing.T) {
	// Clear cache before test
	core.InvalidateActivationStatusCache()

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatalf("failed to init test app: %v", err)
	}
	defer app.Cleanup()

	// Start trial
	app.Settings().StartTrialIfUnset(time.Now().Add(-24 * time.Hour))

	// First call should cache the status
	event1 := &core.RecordAuthRequestEvent{
		RequestEvent: &core.RequestEvent{App: app},
	}
	err = app.OnRecordAuthRequest(core.CollectionNameSuperusers).Trigger(event1, func(e *core.RecordAuthRequestEvent) error {
		return nil
	})
	if err != nil {
		t.Fatalf("first auth request failed: %v", err)
	}

	// Second call should use cached value (we can't directly check internal cache from external test)
	event2 := &core.RecordAuthRequestEvent{
		RequestEvent: &core.RequestEvent{App: app},
	}
	err = app.OnRecordAuthRequest(core.CollectionNameSuperusers).Trigger(event2, func(e *core.RecordAuthRequestEvent) error {
		return nil
	})
	if err != nil {
		t.Fatalf("second auth request failed: %v", err)
	}

	// Test cache invalidation
	core.InvalidateActivationStatusCache()
}
