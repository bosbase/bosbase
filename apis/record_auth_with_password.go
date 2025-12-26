package apis

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"dbx"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/dbutils"
	"github.com/bosbase/bosbase-enterprise/tools/hook"
	"github.com/bosbase/bosbase-enterprise/tools/list"
	"github.com/coocood/freecache"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

const (
	// identityCacheTTL is the TTL for identity->recordID cache entries.
	// Using 5 minutes as a balance between performance and data freshness.
	identityCacheTTL = 5 * time.Minute
)

var (
	identityCacheOnce sync.Once
	identityCache     *freecache.Cache
)

func getIdentityCache() *freecache.Cache {
	identityCacheOnce.Do(func() {
		// 1MB cache for identity lookups
		identityCache = freecache.NewCache(1024 * 1024)
	})
	return identityCache
}

// buildIdentityCacheKey creates a cache key for identity lookups.
func buildIdentityCacheKey(collectionID, field string, value any) []byte {
	return []byte(collectionID + ":" + field + ":" + strings.ToLower(fmt.Sprintf("%v", value)))
}

// InvalidateIdentityCache clears all cached identity lookups.
func InvalidateIdentityCache() {
	getIdentityCache().Clear()
}

// InvalidateIdentityCacheEntry removes a specific identity cache entry.
func InvalidateIdentityCacheEntry(collectionID, field string, value any) {
	getIdentityCache().Del(buildIdentityCacheKey(collectionID, field, value))
}

// RegisterIdentityCacheHooks registers hooks to invalidate identity cache on password/identity changes.
func RegisterIdentityCacheHooks(app core.App) {
	app.OnRecordAfterUpdateSuccess().Bind(&hook.Handler[*core.RecordEvent]{
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}

			if !e.Record.Collection().IsAuth() {
				return nil
			}

			// Check if password changed
			oldHash := e.Record.Original().GetString(core.FieldNamePassword + ":hash")
			newHash := e.Record.GetString(core.FieldNamePassword + ":hash")
			if oldHash != newHash {
				// Invalidate all identity field cache entries for this record
				for _, field := range e.Record.Collection().PasswordAuth.IdentityFields {
					oldValue := e.Record.Original().Get(field)
					if oldValue != nil {
						InvalidateIdentityCacheEntry(e.Record.Collection().Id, field, oldValue)
					}
				}
			}

			// Also invalidate if any identity field value changed
			for _, field := range e.Record.Collection().PasswordAuth.IdentityFields {
				oldValue := e.Record.Original().Get(field)
				newValue := e.Record.Get(field)
				if fmt.Sprintf("%v", oldValue) != fmt.Sprintf("%v", newValue) {
					if oldValue != nil {
						InvalidateIdentityCacheEntry(e.Record.Collection().Id, field, oldValue)
					}
				}
			}

			return nil
		},
		Priority: 99,
	})

	// Also invalidate on record delete
	app.OnRecordAfterDeleteSuccess().Bind(&hook.Handler[*core.RecordEvent]{
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}

			if !e.Record.Collection().IsAuth() {
				return nil
			}

			// Invalidate all identity field cache entries for this record
			for _, field := range e.Record.Collection().PasswordAuth.IdentityFields {
				value := e.Record.Get(field)
				if value != nil {
					InvalidateIdentityCacheEntry(e.Record.Collection().Id, field, value)
				}
			}

			return nil
		},
		Priority: 99,
	})
}

func recordAuthWithPassword(e *core.RequestEvent) error {
	collection, err := findAuthCollection(e)
	if err != nil {
		return err
	}

	if !collection.PasswordAuth.Enabled {
		return e.ForbiddenError("The collection is not configured to allow password authentication.", nil)
	}

	form := &authWithPasswordForm{}
	if err = e.BindBody(form); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while loading the submitted data.", err))
	}
	if err = form.validate(collection); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while validating the submitted data.", err))
	}

	e.Set(core.RequestEventKeyInfoContext, core.RequestInfoContextPasswordAuth)

	var foundRecord *core.Record
	var foundErr error

	if form.IdentityField != "" {
		foundRecord, foundErr = findRecordByIdentityField(e.App, collection, form.IdentityField, form.Identity)
	} else {
		// prioritize email lookup
		isEmail := is.EmailFormat.Validate(form.Identity) == nil
		if isEmail && list.ExistInSlice(core.FieldNameEmail, collection.PasswordAuth.IdentityFields) {
			foundRecord, foundErr = findRecordByIdentityField(e.App, collection, core.FieldNameEmail, form.Identity)
		}

		// search by the other identity fields
		if !isEmail || foundErr != nil {
			for _, name := range collection.PasswordAuth.IdentityFields {
				if !isEmail && name == core.FieldNameEmail {
					continue // no need to search by the email field if it is not an email
				}

				foundRecord, foundErr = findRecordByIdentityField(e.App, collection, name, form.Identity)
				if foundErr == nil {
					break
				}
			}
		}
	}

	// ignore not found errors to allow custom record find implementations
	if foundErr != nil && !errors.Is(foundErr, sql.ErrNoRows) {
		return e.InternalServerError("", foundErr)
	}

	event := new(core.RecordAuthWithPasswordRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Record = foundRecord
	event.Identity = form.Identity
	event.Password = form.Password
	event.IdentityField = form.IdentityField

	return e.App.OnRecordAuthWithPasswordRequest().Trigger(event, func(e *core.RecordAuthWithPasswordRequestEvent) error {
		if e.Record == nil || !e.Record.ValidatePassword(e.Password) {
			return e.BadRequestError("Failed to authenticate.", errors.New("invalid login credentials"))
		}

		return RecordAuthResponse(e.RequestEvent, e.Record, core.MFAMethodPassword, nil)
	})
}

// -------------------------------------------------------------------

type authWithPasswordForm struct {
	Identity string `form:"identity" json:"identity"`
	Password string `form:"password" json:"password"`

	// IdentityField specifies the field to use to search for the identity
	// (leave it empty for "auto" detection).
	IdentityField string `form:"identityField" json:"identityField"`
}

func (form *authWithPasswordForm) validate(collection *core.Collection) error {
	return validation.ValidateStruct(form,
		validation.Field(&form.Identity, validation.Required, validation.Length(1, 255)),
		validation.Field(&form.Password, validation.Required, validation.Length(1, 255)),
		validation.Field(
			&form.IdentityField,
			validation.Length(1, 255),
			validation.In(list.ToInterfaceSlice(collection.PasswordAuth.IdentityFields)...),
		),
	)
}

func findRecordByIdentityField(app core.App, collection *core.Collection, field string, value any) (*core.Record, error) {
	if !slices.Contains(collection.PasswordAuth.IdentityFields, field) {
		return nil, errors.New("invalid identity field " + field)
	}

	index, ok := dbutils.FindSingleColumnUniqueIndex(collection.Indexes, field)
	if !ok {
		return nil, errors.New("missing " + field + " unique index constraint")
	}

	// Check cache first
	cache := getIdentityCache()
	cacheKey := buildIdentityCacheKey(collection.Id, field, value)
	if cachedID, err := cache.Get(cacheKey); err == nil && len(cachedID) > 0 {
		// Found in cache, fetch record by ID
		record, err := app.FindRecordById(collection, string(cachedID))
		if err == nil {
			return record, nil
		}
		// Cache entry is stale (record deleted), remove it
		cache.Del(cacheKey)
	}

	driver := core.BuilderDriverName(app.NonconcurrentDB())
	var expr dbx.Expression
	if strings.EqualFold(index.Columns[0].Collate, "nocase") || strings.HasPrefix(strings.ToUpper(strings.TrimSpace(index.Columns[0].Name)), "LOWER(") {
		// case-insensitive search
		expr = dbx.NewExp(core.CaseInsensitiveEqExpr(field, "{:identity}", driver), dbx.Params{"identity": value})
	} else {
		expr = dbx.HashExp{field: value}
	}

	record := &core.Record{}

	err := app.RecordQuery(collection).AndWhere(expr).Limit(1).One(record)
	if err != nil {
		return nil, err
	}

	// Cache the result
	_ = cache.Set(cacheKey, []byte(record.Id), int(identityCacheTTL.Seconds()))

	return record, nil
}
