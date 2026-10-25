package apis

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/filesystem"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/security"
	"github.com/spf13/cast"
)

// User configurable S3 compatible object storages (AWS S3, Cloudflare R2,
// Google Cloud Storage, MinIO, Backblaze B2...) with an upload history.

const (
	storageProvidersTable = "_storage_providers"
	storageObjectsTable   = "_storage_objects"

	storageDefaultMaxUploadMB = 512
	storageRequestTimeout     = 10 * time.Minute
	storageDownloadURLExpiry  = time.Hour

	storageMaxURLExpirySeconds = 7 * 24 * 3600
)

var storageProviderTypes = []string{"s3", "aws", "r2", "gcs", "minio", "b2", "other"}

func bindStorageApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/storage").Bind(RequireAuth())

	sub.GET("/providers", storageProvidersList)
	sub.POST("/providers", storageProviderCreate)
	sub.GET("/providers/{id}", storageProviderView)
	sub.PATCH("/providers/{id}", storageProviderUpdate)
	sub.DELETE("/providers/{id}", storageProviderDelete)
	sub.POST("/providers/{id}/test", storageProviderTest)

	sub.GET("/objects", storageObjectsList)
	sub.POST("/objects", storageObjectUpload).Bind(BodyLimit(storageMaxUploadBytes()))
	sub.POST("/objects/presign", storageObjectPresign)
	sub.GET("/objects/{id}", storageObjectView)
	sub.POST("/objects/{id}/complete", storageObjectComplete)
	sub.GET("/objects/{id}/download", storageObjectDownload)
	sub.DELETE("/objects/{id}", storageObjectDelete)
}

func storageMaxUploadBytes() int64 {
	mb := storageDefaultMaxUploadMB
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("STORAGE_MAX_UPLOAD_MB"))); err == nil && v > 0 {
		mb = v
	}
	return int64(mb) << 20
}

// -------------------------------------------------------------------
// Providers
// -------------------------------------------------------------------

type storageProvider struct {
	Id              string `db:"id"`
	Owner           string `db:"owner"`
	OwnerCollection string `db:"ownerCollection"`
	Name            string `db:"name"`
	Type            string `db:"type"`
	Endpoint        string `db:"endpoint"`
	Region          string `db:"region"`
	Bucket          string `db:"bucket"`
	AccessKey       string `db:"accessKey"`
	SecretKey       string `db:"secretKey"`
	ForcePathStyle  bool   `db:"forcePathStyle"`
	PublicUrl       string `db:"publicUrl"`
	PublicRead      bool   `db:"publicRead"`
	Prefix          string `db:"prefix"`
	IsDefault       bool   `db:"isDefault"`
	Enabled         bool   `db:"enabled"`
	Created         string `db:"created"`
	Updated         string `db:"updated"`
}

const storageProviderColumns = `[[id]], [[owner]], [[ownerCollection]], [[name]], [[type]], [[endpoint]], [[region]], [[bucket]],
	[[accessKey]], [[secretKey]], [[forcePathStyle]], [[publicUrl]], [[publicRead]], [[prefix]], [[isDefault]], [[enabled]],
	[[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

func (p *storageProvider) public(app core.App) map[string]any {
	accessKey, _ := decryptIntegrationSecret(app, p.AccessKey)
	return map[string]any{
		"id":             p.Id,
		"name":           p.Name,
		"type":           p.Type,
		"endpoint":       p.Endpoint,
		"region":         p.Region,
		"bucket":         p.Bucket,
		"accessKey":      maskSecret(accessKey),
		"secretKeySet":   p.SecretKey != "",
		"forcePathStyle": p.ForcePathStyle,
		"publicUrl":      p.PublicUrl,
		"publicRead":     p.PublicRead,
		"prefix":         p.Prefix,
		"isDefault":      p.IsDefault,
		"enabled":        p.Enabled,
		"shared":         p.Owner == "",
		"owner":          p.Owner,
		"created":        p.Created,
		"updated":        p.Updated,
	}
}

func (p *storageProvider) s3Config(app core.App) (filesystem.S3Config, error) {
	accessKey, err := decryptIntegrationSecret(app, p.AccessKey)
	if err != nil {
		return filesystem.S3Config{}, err
	}
	secretKey, err := decryptIntegrationSecret(app, p.SecretKey)
	if err != nil {
		return filesystem.S3Config{}, err
	}
	return filesystem.S3Config{
		Bucket:         p.Bucket,
		Region:         p.Region,
		Endpoint:       p.Endpoint,
		AccessKey:      accessKey,
		SecretKey:      secretKey,
		ForcePathStyle: p.ForcePathStyle,
		HTTPClient:     newIntegrationHTTPClient(isTrustedIntegrationOwner(p.Owner, p.OwnerCollection), storageRequestTimeout),
	}, nil
}

func (p *storageProvider) filesystem(app core.App) (*filesystem.System, error) {
	cfg, err := p.s3Config(app)
	if err != nil {
		return nil, err
	}
	return filesystem.NewS3FromConfig(cfg)
}

// objectURL returns the permanent public URL of an object: the public/CDN base URL
// when configured, otherwise the direct object URL for publicly readable buckets.
func (p *storageProvider) objectURL(key string) string {
	if p.PublicUrl == "" {
		if p.PublicRead {
			return filesystem.S3ObjectURL(filesystem.S3Config{
				Bucket: p.Bucket, Region: p.Region, Endpoint: p.Endpoint, ForcePathStyle: p.ForcePathStyle,
			}, key)
		}
		return ""
	}
	segments := strings.Split(key, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.TrimRight(p.PublicUrl, "/") + "/" + strings.Join(segments, "/")
}

func storageLoadProvider(app core.App, where string, params dbx.Params) (*storageProvider, error) {
	p := &storageProvider{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s LIMIT 1", storageProviderColumns, storageProvidersTable, where,
	)).Bind(params).One(p)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// storageFindProvider finds a provider usable by the request auth by id or name
// (or the default provider when ref is empty).
func storageFindProvider(e *core.RequestEvent, ref string) (*storageProvider, error) {
	ref = strings.TrimSpace(ref)
	params := dbx.Params{"me": e.Auth.Id, "ref": ref}
	visible := "([[owner]] = {:me} OR [[owner]] = '')"
	if e.HasSuperuserAuth() {
		visible = "TRUE"
	}

	var where string
	if ref == "" {
		where = visible + " AND [[enabled]] = TRUE ORDER BY ([[owner]] = {:me} AND [[isDefault]]) DESC, ([[owner]] = '' AND [[isDefault]]) DESC, ([[owner]] = {:me}) DESC, [[created]] ASC"
	} else {
		where = visible + " AND ([[id]] = {:ref} OR LOWER([[name]]) = LOWER({:ref})) ORDER BY ([[id]] = {:ref}) DESC, ([[owner]] = {:me}) DESC"
	}

	p, err := storageLoadProvider(e.App, where, params)
	if err != nil {
		if ref == "" {
			return nil, e.BadRequestError("No storage provider is configured. Create one with POST /api/storage/providers.", nil)
		}
		return nil, e.NotFoundError("Storage provider "+strconv.Quote(ref)+" was not found.", nil)
	}
	return p, nil
}

type storageProviderPayload struct {
	Name           *string `json:"name"`
	Type           *string `json:"type"`
	Endpoint       *string `json:"endpoint"`
	Region         *string `json:"region"`
	Bucket         *string `json:"bucket"`
	AccessKey      *string `json:"accessKey"`
	SecretKey      *string `json:"secretKey"`
	ForcePathStyle *bool   `json:"forcePathStyle"`
	PublicUrl      *string `json:"publicUrl"`
	PublicRead     *bool   `json:"publicRead"`
	Prefix         *string `json:"prefix"`
	IsDefault      *bool   `json:"isDefault"`
	Enabled        *bool   `json:"enabled"`
	Shared         bool    `json:"shared"`
	OwnerId        string  `json:"ownerId"`
}

// applyStorageProviderPayload merges the payload into p and validates the result.
func applyStorageProviderPayload(e *core.RequestEvent, p *storageProvider, payload *storageProviderPayload) error {
	str := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	str(&p.Name, payload.Name)
	str(&p.Type, payload.Type)
	str(&p.Endpoint, payload.Endpoint)
	str(&p.Region, payload.Region)
	str(&p.Bucket, payload.Bucket)
	str(&p.PublicUrl, payload.PublicUrl)
	str(&p.Prefix, payload.Prefix)
	if payload.ForcePathStyle != nil {
		p.ForcePathStyle = *payload.ForcePathStyle
	}
	if payload.PublicRead != nil {
		p.PublicRead = *payload.PublicRead
	}
	if payload.IsDefault != nil {
		p.IsDefault = *payload.IsDefault
	}
	if payload.Enabled != nil {
		p.Enabled = *payload.Enabled
	}

	p.Type = strings.ToLower(p.Type)
	if p.Type == "" {
		p.Type = "s3"
	}
	if !slicesContains(storageProviderTypes, p.Type) {
		return e.BadRequestError("type must be one of "+strings.Join(storageProviderTypes, ", ")+".", nil)
	}

	// provider presets
	switch p.Type {
	case "gcs":
		if p.Endpoint == "" {
			p.Endpoint = "https://storage.googleapis.com"
		}
		if p.Region == "" {
			p.Region = "auto"
		}
	case "r2":
		if p.Region == "" {
			p.Region = "auto"
		}
	case "aws":
		if p.Region == "" {
			p.Region = "us-east-1"
		}
		if p.Endpoint == "" {
			p.Endpoint = "https://s3." + p.Region + ".amazonaws.com"
		}
	}
	if p.Region == "" {
		p.Region = "us-east-1"
	}

	if p.Name == "" {
		return e.BadRequestError("name is required.", nil)
	}
	if p.Bucket == "" {
		return e.BadRequestError("bucket is required.", nil)
	}
	endpoint, err := validateUpstreamURL(p.Endpoint, false)
	if err != nil {
		return e.BadRequestError("Invalid endpoint: "+err.Error(), nil)
	}
	p.Endpoint = endpoint
	if p.PublicUrl != "" {
		publicUrl, err := validateUpstreamURL(p.PublicUrl, true)
		if err != nil {
			return e.BadRequestError("Invalid publicUrl: "+err.Error(), nil)
		}
		p.PublicUrl = publicUrl
	}
	p.Prefix = strings.Trim(p.Prefix, "/")

	if payload.AccessKey != nil {
		enc, err := encryptIntegrationSecret(e.App, strings.TrimSpace(*payload.AccessKey))
		if err != nil {
			return err
		}
		p.AccessKey = enc
	}
	if payload.SecretKey != nil {
		enc, err := encryptIntegrationSecret(e.App, strings.TrimSpace(*payload.SecretKey))
		if err != nil {
			return err
		}
		p.SecretKey = enc
	}
	if p.AccessKey == "" || p.SecretKey == "" {
		return e.BadRequestError("accessKey and secretKey are required.", nil)
	}

	return nil
}

func slicesContains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func storageSaveProvider(e *core.RequestEvent, p *storageProvider, isNew bool) error {
	params := dbx.Params{
		"owner":           p.Owner,
		"ownerCollection": p.OwnerCollection,
		"name":            p.Name,
		"type":            p.Type,
		"endpoint":        p.Endpoint,
		"region":          p.Region,
		"bucket":          p.Bucket,
		"accessKey":       p.AccessKey,
		"secretKey":       p.SecretKey,
		"forcePathStyle":  p.ForcePathStyle,
		"publicUrl":       p.PublicUrl,
		"publicRead":      p.PublicRead,
		"prefix":          p.Prefix,
		"isDefault":       p.IsDefault,
		"enabled":         p.Enabled,
	}

	err := e.App.RunInTransaction(func(txApp core.App) error {
		if p.IsDefault {
			if _, err := txApp.DB().Update(storageProvidersTable, dbx.Params{"isDefault": false}, dbx.HashExp{"owner": p.Owner}).Execute(); err != nil {
				return err
			}
		}
		if isNew {
			return txApp.DB().NewQuery(fmt.Sprintf(`
				INSERT INTO {{%s}} ([[owner]], [[ownerCollection]], [[name]], [[type]], [[endpoint]], [[region]], [[bucket]], [[accessKey]], [[secretKey]], [[forcePathStyle]], [[publicUrl]], [[publicRead]], [[prefix]], [[isDefault]], [[enabled]])
				VALUES ({:owner}, {:ownerCollection}, {:name}, {:type}, {:endpoint}, {:region}, {:bucket}, {:accessKey}, {:secretKey}, {:forcePathStyle}, {:publicUrl}, {:publicRead}, {:prefix}, {:isDefault}, {:enabled})
				RETURNING [[id]]`, storageProvidersTable)).Bind(params).Row(&p.Id)
		}
		params["updated"] = time.Now()
		delete(params, "owner")
		delete(params, "ownerCollection")
		_, err := txApp.DB().Update(storageProvidersTable, params, dbx.HashExp{"id": p.Id}).Execute()
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "idx__storage_providers_name") {
			return e.BadRequestError("A storage provider named "+strconv.Quote(p.Name)+" already exists.", nil)
		}
		return e.InternalServerError("Failed to save the storage provider.", err)
	}
	return nil
}

func storageProvidersList(e *core.RequestEvent) error {
	params := dbx.Params{"me": e.Auth.Id}
	where := "[[owner]] = {:me} OR [[owner]] = ''"
	if e.HasSuperuserAuth() && e.Request.URL.Query().Get("all") == "true" {
		where = "TRUE"
	}

	providers := []*storageProvider{}
	err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY ([[owner]] = {:me}) DESC, [[isDefault]] DESC, LOWER([[name]])",
		storageProviderColumns, storageProvidersTable, where,
	)).Bind(params).All(&providers)
	if err != nil {
		return e.InternalServerError("Failed to load the storage providers.", err)
	}

	items := make([]map[string]any, len(providers))
	for i, p := range providers {
		items[i] = p.public(e.App)
	}
	return e.JSON(http.StatusOK, map[string]any{"items": items})
}

func storageProviderCreate(e *core.RequestEvent) error {
	payload := &storageProviderPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}

	if !e.HasSuperuserAuth() && !storageUserProvidersAllowed() {
		return e.ForbiddenError("Storage providers are configured by the superusers (use the shared providers).", nil)
	}

	owner, err := resolveIntegrationOwner(e, payload.Shared, payload.OwnerId)
	if err != nil {
		return err
	}

	p := &storageProvider{Owner: owner.id, OwnerCollection: owner.collection, Enabled: true}
	if err := applyStorageProviderPayload(e, p, payload); err != nil {
		return err
	}
	if err := storageSaveProvider(e, p, true); err != nil {
		return err
	}

	saved, err := storageLoadProvider(e.App, "[[id]] = {:id}", dbx.Params{"id": p.Id})
	if err != nil {
		return e.InternalServerError("Failed to load the storage provider.", err)
	}
	return e.JSON(http.StatusOK, saved.public(e.App))
}

func storageManagedProvider(e *core.RequestEvent) (*storageProvider, error) {
	p, err := storageLoadProvider(e.App, "[[id]] = {:id}", dbx.Params{"id": e.Request.PathValue("id")})
	if err != nil || (!canManageIntegration(e, p.Owner) && p.Owner != "") {
		return nil, e.NotFoundError("Storage provider not found.", nil)
	}
	if !canManageIntegration(e, p.Owner) {
		return nil, e.ForbiddenError("Only superusers can modify shared storage providers.", nil)
	}
	if !e.HasSuperuserAuth() && !storageUserProvidersAllowed() {
		return nil, e.ForbiddenError("Storage providers are configured by the superusers.", nil)
	}
	return p, nil
}

// storageUserProvidersAllowed reports whether regular users can configure their own
// storage providers (STORAGE_ALLOW_USER_PROVIDERS=false restricts it to the superusers).
func storageUserProvidersAllowed() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_ALLOW_USER_PROVIDERS")))
	return v != "false" && v != "0" && v != "no"
}

// storageURLExpiry returns the validity of the presigned links (default 7 days, the S3 maximum).
func storageURLExpiry(requested int) time.Duration {
	seconds := requested
	if seconds <= 0 {
		seconds, _ = strconv.Atoi(strings.TrimSpace(os.Getenv("STORAGE_URL_EXPIRES_SECONDS")))
	}
	if seconds <= 0 {
		seconds = storageMaxURLExpirySeconds
	}
	return time.Duration(min(max(seconds, 60), storageMaxURLExpirySeconds)) * time.Second
}

// storageWithAccessURL makes sure that the object map has a link usable from the internet:
// the permanent public URL (public/CDN base URL or publicly readable bucket) or a presigned GET URL.
func storageWithAccessURL(e *core.RequestEvent, p *storageProvider, m map[string]any, key string, expiresSeconds int) map[string]any {
	if u, _ := m["url"].(string); u != "" {
		m["urlType"] = "public"
		return m
	}
	cfg, err := p.s3Config(e.App)
	if err != nil {
		return m
	}
	expiry := storageURLExpiry(expiresSeconds)
	if u, err := filesystem.PresignS3URL(cfg, http.MethodGet, key, expiry); err == nil {
		m["url"] = u
		m["urlType"] = "presigned"
		m["urlExpiresAt"] = time.Now().Add(expiry).UTC().Format(time.RFC3339)
	}
	return m
}

func storageProviderView(e *core.RequestEvent) error {
	p, err := storageLoadProvider(e.App, "[[id]] = {:id}", dbx.Params{"id": e.Request.PathValue("id")})
	if err != nil || !(p.Owner == "" || p.Owner == e.Auth.Id || e.HasSuperuserAuth()) {
		return e.NotFoundError("Storage provider not found.", nil)
	}
	return e.JSON(http.StatusOK, p.public(e.App))
}

func storageProviderUpdate(e *core.RequestEvent) error {
	p, err := storageManagedProvider(e)
	if err != nil {
		return err
	}

	payload := &storageProviderPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	if err := applyStorageProviderPayload(e, p, payload); err != nil {
		return err
	}
	if err := storageSaveProvider(e, p, false); err != nil {
		return err
	}

	saved, err := storageLoadProvider(e.App, "[[id]] = {:id}", dbx.Params{"id": p.Id})
	if err != nil {
		return e.InternalServerError("Failed to load the storage provider.", err)
	}
	return e.JSON(http.StatusOK, saved.public(e.App))
}

func storageProviderDelete(e *core.RequestEvent) error {
	p, err := storageManagedProvider(e)
	if err != nil {
		return err
	}
	if _, err := e.App.DB().Delete(storageProvidersTable, dbx.HashExp{"id": p.Id}).Execute(); err != nil {
		return e.InternalServerError("Failed to delete the storage provider.", err)
	}
	return e.NoContent(http.StatusNoContent)
}

func storageUpstreamError(e *core.RequestEvent, action string, err error) error {
	if isPrivateUpstreamErr(err) {
		return e.BadRequestError(action+" failed: "+errPrivateUpstream.Error()+" (set BOSBASE_ALLOW_PRIVATE_UPSTREAMS=true to allow it).", nil)
	}
	return e.BadRequestError(action+" failed: "+err.Error(), nil)
}

func storageProviderTest(e *core.RequestEvent) error {
	p, err := storageFindProvider(e, e.Request.PathValue("id"))
	if err != nil {
		return err
	}

	fsys, err := p.filesystem(e.App)
	if err != nil {
		return storageUpstreamError(e, "Storage connection", err)
	}
	defer fsys.Close()

	started := time.Now()
	key := strings.TrimLeft(path.Join(p.Prefix, ".bosbase-test-"+security.RandomString(10)+".txt"), "/")
	content := []byte("BosBase storage connectivity test " + time.Now().UTC().Format(time.RFC3339))

	steps := map[string]any{}
	if err := fsys.UploadReader(bytes.NewReader(content), key, "text/plain"); err != nil {
		return storageUpstreamError(e, "Upload", err)
	}
	steps["upload"] = true

	attrs, err := fsys.Attributes(key)
	if err != nil {
		return storageUpstreamError(e, "Read attributes", err)
	}
	steps["head"] = attrs.Size == int64(len(content))

	if err := fsys.Delete(key); err != nil {
		return storageUpstreamError(e, "Delete", err)
	}
	steps["delete"] = true

	return e.JSON(http.StatusOK, map[string]any{
		"ok":         true,
		"provider":   p.Name,
		"bucket":     p.Bucket,
		"steps":      steps,
		"durationMs": time.Since(started).Milliseconds(),
	})
}

// -------------------------------------------------------------------
// Objects
// -------------------------------------------------------------------

type storageObject struct {
	Id           string  `db:"id"`
	Owner        string  `db:"owner"`
	Provider     string  `db:"provider"`
	ProviderName string  `db:"providerName"`
	Bucket       string  `db:"bucket"`
	Key          string  `db:"key"`
	Filename     string  `db:"filename"`
	ContentType  string  `db:"contentType"`
	Size         int64   `db:"size"`
	Etag         string  `db:"etag"`
	Url          string  `db:"url"`
	Status       string  `db:"status"`
	Source       string  `db:"source"`
	Metadata     string  `db:"metadata"`
	Error        string  `db:"error"`
	DeletedAt    *string `db:"deletedAt"`
	Created      string  `db:"created"`
	Updated      string  `db:"updated"`
}

const storageObjectColumns = `[[id]], [[owner]], [[provider]], [[providerName]], [[bucket]], [[key]], [[filename]], [[contentType]],
	[[size]], [[etag]], [[url]], [[status]], [[source]], [[metadata]]::text AS [[metadata]], [[error]],
	[[deletedAt]]::text AS [[deletedAt]], [[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

func (o *storageObject) public() map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(o.Metadata), &metadata)
	m := map[string]any{
		"id":           o.Id,
		"owner":        o.Owner,
		"provider":     o.Provider,
		"providerName": o.ProviderName,
		"bucket":       o.Bucket,
		"key":          o.Key,
		"filename":     o.Filename,
		"contentType":  o.ContentType,
		"size":         o.Size,
		"etag":         o.Etag,
		"url":          o.Url,
		"status":       o.Status,
		"source":       o.Source,
		"metadata":     metadata,
		"created":      o.Created,
		"updated":      o.Updated,
	}
	if o.Error != "" {
		m["error"] = o.Error
	}
	if o.DeletedAt != nil {
		m["deletedAt"] = *o.DeletedAt
	}
	return m
}

func storageLoadObject(e *core.RequestEvent) (*storageObject, error) {
	o := &storageObject{}
	err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[id]] = {:id}", storageObjectColumns, storageObjectsTable,
	)).Bind(dbx.Params{"id": e.Request.PathValue("id")}).One(o)
	if err != nil || (o.Owner != e.Auth.Id && !e.HasSuperuserAuth()) {
		return nil, e.NotFoundError("Object not found.", nil)
	}
	return o, nil
}

func storageLoadObjectById(app core.App, id string) (*storageObject, error) {
	o := &storageObject{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[id]] = {:id}", storageObjectColumns, storageObjectsTable,
	)).Bind(dbx.Params{"id": id}).One(o)
	return o, err
}

var storageUnsafeNameChars = regexp.MustCompile(`[^\p{L}\p{N}._\-]+`)

func storageSanitizeFilename(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	name = storageUnsafeNameChars.ReplaceAllString(name, "_")
	if r := []rune(name); len(r) > 150 {
		name = string(r[len(r)-150:])
	}
	return name
}

// storageObjectKey returns the explicit key (sanitized) or generates a unique one.
func storageObjectKey(p *storageProvider, explicitKey, prefix, filename string) (string, error) {
	if k := strings.TrimSpace(explicitKey); k != "" {
		k = strings.TrimLeft(path.Clean("/"+strings.ReplaceAll(k, "\\", "/")), "/")
		if k == "" || k == "." {
			return "", errors.New("invalid key")
		}
		if p.Prefix != "" && !strings.HasPrefix(k, p.Prefix+"/") {
			k = p.Prefix + "/" + k
		}
		return k, nil
	}

	parts := []string{}
	if p.Prefix != "" {
		parts = append(parts, p.Prefix)
	}
	if prefix = strings.Trim(path.Clean("/"+strings.TrimSpace(prefix)), "/"); prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, time.Now().UTC().Format("2006/01/02"), security.RandomString(10)+"_"+storageSanitizeFilename(filename))

	return strings.Join(parts, "/"), nil
}

func storageRecordObject(e *core.RequestEvent, p *storageProvider, key, filename, contentType string, size int64, etag, status, source string, metadata map[string]any) (*storageObject, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, _ := json.Marshal(metadata)

	var id string
	err := e.App.DB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} ([[owner]], [[ownerCollection]], [[provider]], [[providerName]], [[bucket]], [[key]], [[filename]], [[contentType]], [[size]], [[etag]], [[url]], [[status]], [[source]], [[metadata]])
		VALUES ({:owner}, {:ownerCollection}, {:provider}, {:providerName}, {:bucket}, {:key}, {:filename}, {:contentType}, {:size}, {:etag}, {:url}, {:status}, {:source}, {:metadata}::jsonb)
		RETURNING [[id]]`, storageObjectsTable)).Bind(dbx.Params{
		"owner":           e.Auth.Id,
		"ownerCollection": e.Auth.Collection().Name,
		"provider":        p.Id,
		"providerName":    p.Name,
		"bucket":          p.Bucket,
		"key":             key,
		"filename":        filename,
		"contentType":     contentType,
		"size":            size,
		"etag":            etag,
		"url":             p.objectURL(key),
		"status":          status,
		"source":          source,
		"metadata":        string(metadataJSON),
	}).Row(&id)
	if err != nil {
		return nil, err
	}

	return storageLoadObjectById(e.App, id)
}

func storageRequestSource(e *core.RequestEvent) string {
	if e.Request.Header.Get(mcpApiInternalHeader) != "" {
		return "mcp"
	}
	return "http"
}

type storageUploadItem struct {
	filename    string
	contentType string
	open        func() (readerCloser, int64, error)
}

type readerCloser interface {
	Read(p []byte) (int, error)
	Close() error
}

type nopReadCloser struct{ *bytes.Reader }

func (nopReadCloser) Close() error { return nil }

func storageObjectUpload(e *core.RequestEvent) error {
	var (
		providerRef, key, prefix string
		metadata                 map[string]any
		items                    []storageUploadItem
		urlExpires               int
	)

	ct := strings.ToLower(e.Request.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := e.Request.ParseMultipartForm(32 << 20); err != nil {
			return e.BadRequestError("Failed to parse the multipart form.", err)
		}
		form := e.Request.MultipartForm
		get := func(name string) string {
			if v := form.Value[name]; len(v) > 0 {
				return v[0]
			}
			return ""
		}
		providerRef, key, prefix = get("provider"), get("key"), get("prefix")
		urlExpires, _ = strconv.Atoi(get("urlExpiresSeconds"))
		if raw := get("metadata"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
				return e.BadRequestError("metadata must be a JSON object.", nil)
			}
		}
		for _, field := range []string{"file", "files"} {
			for _, fh := range form.File[field] {
				fh := fh
				items = append(items, storageUploadItem{
					filename:    fh.Filename,
					contentType: fh.Header.Get("Content-Type"),
					open: func() (readerCloser, int64, error) {
						f, err := fh.Open()
						return f, fh.Size, err
					},
				})
			}
		}
	} else {
		payload := struct {
			Provider      string         `json:"provider"`
			Filename      string         `json:"filename"`
			ContentBase64 string         `json:"contentBase64"`
			Text          *string        `json:"text"`
			ContentType   string         `json:"contentType"`
			Key           string         `json:"key"`
			Prefix        string         `json:"prefix"`
			Metadata      map[string]any `json:"metadata"`
			URLExpires    int            `json:"urlExpiresSeconds"`
		}{}
		if err := e.BindBody(&payload); err != nil {
			return e.BadRequestError("Failed to read the request body.", err)
		}
		var content []byte
		if payload.Text != nil {
			content = []byte(*payload.Text)
		} else {
			decoded, err := base64.StdEncoding.DecodeString(payload.ContentBase64)
			if err != nil || payload.ContentBase64 == "" {
				return e.BadRequestError("contentBase64 (or text) is required and must be valid base64.", nil)
			}
			content = decoded
		}
		if payload.Filename == "" {
			return e.BadRequestError("filename is required.", nil)
		}
		providerRef, key, prefix, metadata = payload.Provider, payload.Key, payload.Prefix, payload.Metadata
		urlExpires = payload.URLExpires
		items = append(items, storageUploadItem{
			filename:    payload.Filename,
			contentType: payload.ContentType,
			open: func() (readerCloser, int64, error) {
				return nopReadCloser{bytes.NewReader(content)}, int64(len(content)), nil
			},
		})
	}

	if len(items) == 0 {
		return e.BadRequestError("No file was submitted (use the \"file\" multipart field or contentBase64).", nil)
	}
	if key != "" && len(items) > 1 {
		return e.BadRequestError("key can be used only when uploading a single file.", nil)
	}

	p, err := storageFindProvider(e, providerRef)
	if err != nil {
		return err
	}
	if !p.Enabled {
		return e.BadRequestError("The storage provider is disabled.", nil)
	}

	fsys, err := p.filesystem(e.App)
	if err != nil {
		return storageUpstreamError(e, "Storage connection", err)
	}
	defer fsys.Close()

	ctx, cancel := requestContextWithTimeout(e, storageRequestTimeout)
	defer cancel()
	fsys.SetContext(ctx)

	results := make([]map[string]any, 0, len(items))
	for _, item := range items {
		objectKey, err := storageObjectKey(p, key, prefix, item.filename)
		if err != nil {
			return e.BadRequestError("Invalid key.", nil)
		}

		r, size, err := item.open()
		if err != nil {
			return e.BadRequestError("Failed to read the uploaded file.", err)
		}
		uploadErr := fsys.UploadReader(r, objectKey, item.contentType)
		r.Close()
		if uploadErr != nil {
			return storageUpstreamError(e, "Upload of "+item.filename, uploadErr)
		}

		contentType, etag := item.contentType, ""
		if attrs, err := fsys.Attributes(objectKey); err == nil {
			size = attrs.Size
			etag = strings.Trim(attrs.ETag, `"`)
			if contentType == "" {
				contentType = attrs.ContentType
			}
		}

		obj, err := storageRecordObject(e, p, objectKey, item.filename, contentType, size, etag, "uploaded", storageRequestSource(e), metadata)
		if err != nil {
			return e.InternalServerError("The file was uploaded but recording it failed.", err)
		}
		results = append(results, storageWithAccessURL(e, p, obj.public(), objectKey, urlExpires))
	}

	if len(results) == 1 {
		return e.JSON(http.StatusOK, results[0])
	}
	return e.JSON(http.StatusOK, map[string]any{"items": results})
}

func storageObjectPresign(e *core.RequestEvent) error {
	payload := struct {
		Provider       string         `json:"provider"`
		Filename       string         `json:"filename"`
		ContentType    string         `json:"contentType"`
		Key            string         `json:"key"`
		Prefix         string         `json:"prefix"`
		ExpiresSeconds int            `json:"expiresSeconds"`
		Metadata       map[string]any `json:"metadata"`
	}{}
	if err := e.BindBody(&payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	if strings.TrimSpace(payload.Filename) == "" {
		return e.BadRequestError("filename is required.", nil)
	}

	p, err := storageFindProvider(e, payload.Provider)
	if err != nil {
		return err
	}
	if !p.Enabled {
		return e.BadRequestError("The storage provider is disabled.", nil)
	}

	objectKey, err := storageObjectKey(p, payload.Key, payload.Prefix, payload.Filename)
	if err != nil {
		return e.BadRequestError("Invalid key.", nil)
	}

	cfg, err := p.s3Config(e.App)
	if err != nil {
		return storageUpstreamError(e, "Presign", err)
	}
	expires := time.Duration(payload.ExpiresSeconds) * time.Second
	if expires <= 0 {
		expires = time.Hour
	}
	uploadURL, err := filesystem.PresignS3URL(cfg, http.MethodPut, objectKey, expires)
	if err != nil {
		return storageUpstreamError(e, "Presign", err)
	}

	obj, err := storageRecordObject(e, p, objectKey, payload.Filename, payload.ContentType, 0, "", "pending", storageRequestSource(e), payload.Metadata)
	if err != nil {
		return e.InternalServerError("Failed to record the object.", err)
	}

	headers := map[string]string{}
	if payload.ContentType != "" {
		headers["Content-Type"] = payload.ContentType
	}

	return e.JSON(http.StatusOK, map[string]any{
		"object":    obj.public(),
		"uploadUrl": uploadURL,
		"method":    http.MethodPut,
		"headers":   headers,
		"expiresAt": time.Now().Add(expires).UTC().Format(time.RFC3339),
		"next":      "PUT the file content to uploadUrl, then call POST /api/storage/objects/" + obj.Id + "/complete",
	})
}

func storageObjectProvider(e *core.RequestEvent, o *storageObject) (*storageProvider, error) {
	p, err := storageLoadProvider(e.App, "[[id]] = {:id}", dbx.Params{"id": o.Provider})
	if err != nil {
		return nil, e.BadRequestError("The storage provider of the object no longer exists.", nil)
	}
	return p, nil
}

func storageObjectComplete(e *core.RequestEvent) error {
	o, err := storageLoadObject(e)
	if err != nil {
		return err
	}
	if o.Status == "deleted" {
		return e.BadRequestError("The object was deleted.", nil)
	}

	p, err := storageObjectProvider(e, o)
	if err != nil {
		return err
	}
	fsys, err := p.filesystem(e.App)
	if err != nil {
		return storageUpstreamError(e, "Storage connection", err)
	}
	defer fsys.Close()

	attrs, err := fsys.Attributes(o.Key)
	if err != nil {
		_, _ = e.App.DB().Update(storageObjectsTable, dbx.Params{"error": "object not found in bucket", "updated": time.Now()}, dbx.HashExp{"id": o.Id}).Execute()
		return e.BadRequestError("The object was not found in the bucket. Upload it to the presigned URL first.", nil)
	}

	contentType := o.ContentType
	if contentType == "" {
		contentType = attrs.ContentType
	}
	_, err = e.App.DB().Update(storageObjectsTable, dbx.Params{
		"status":      "uploaded",
		"size":        attrs.Size,
		"etag":        strings.Trim(attrs.ETag, `"`),
		"contentType": contentType,
		"error":       "",
		"updated":     time.Now(),
	}, dbx.HashExp{"id": o.Id}).Execute()
	if err != nil {
		return e.InternalServerError("Failed to update the object.", err)
	}

	updated, err := storageLoadObjectById(e.App, o.Id)
	if err != nil {
		return e.InternalServerError("Failed to load the object.", err)
	}
	return e.JSON(http.StatusOK, storageWithAccessURL(e, p, updated.public(), updated.Key, cast.ToInt(e.Request.URL.Query().Get("urlExpiresSeconds"))))
}

func storageObjectsList(e *core.RequestEvent) error {
	q := e.Request.URL.Query()
	page := max(cast.ToInt(q.Get("page")), 1)
	perPage := cast.ToInt(q.Get("perPage"))
	if perPage <= 0 {
		perPage = 30
	}
	perPage = min(perPage, 500)

	params := dbx.Params{"limit": perPage, "offset": (page - 1) * perPage}
	filters := []string{}
	if !(e.HasSuperuserAuth() && q.Get("all") == "true") {
		params["me"] = e.Auth.Id
		filters = append(filters, "[[owner]] = {:me}")
	}
	if v := q.Get("provider"); v != "" {
		params["provider"] = v
		filters = append(filters, "([[provider]] = {:provider} OR LOWER([[providerName]]) = LOWER({:provider}))")
	}
	if v := q.Get("status"); v != "" {
		params["status"] = v
		filters = append(filters, "[[status]] = {:status}")
	} else if q.Get("includeDeleted") != "true" {
		filters = append(filters, "[[status]] <> 'deleted'")
	}
	if v := q.Get("prefix"); v != "" {
		params["prefix"] = escapeLike(v) + "%"
		filters = append(filters, "[[key]] LIKE {:prefix}")
	}
	if v := q.Get("contentType"); v != "" {
		params["contentType"] = escapeLike(v) + "%"
		filters = append(filters, "[[contentType]] LIKE {:contentType}")
	}
	if v := q.Get("search"); v != "" {
		params["search"] = "%" + escapeLike(v) + "%"
		filters = append(filters, "([[filename]] ILIKE {:search} OR [[key]] ILIKE {:search})")
	}
	where := "TRUE"
	if len(filters) > 0 {
		where = strings.Join(filters, " AND ")
	}

	var total int
	if err := e.App.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE %s", storageObjectsTable, where)).Bind(params).Row(&total); err != nil {
		return e.InternalServerError("Failed to count the objects.", err)
	}

	objects := []*storageObject{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY [[created]] DESC, [[id]] DESC LIMIT {:limit} OFFSET {:offset}",
		storageObjectColumns, storageObjectsTable, where,
	)).Bind(params).All(&objects); err != nil {
		return e.InternalServerError("Failed to load the objects.", err)
	}

	items := make([]map[string]any, len(objects))
	for i, o := range objects {
		items[i] = o.public()
	}

	return e.JSON(http.StatusOK, map[string]any{
		"page":       page,
		"perPage":    perPage,
		"totalItems": total,
		"items":      items,
	})
}

func storageObjectView(e *core.RequestEvent) error {
	o, err := storageLoadObject(e)
	if err != nil {
		return err
	}

	result := o.public()
	if o.Status == "uploaded" {
		if p, err := storageObjectProvider(e, o); err == nil {
			result = storageWithAccessURL(e, p, result, o.Key, cast.ToInt(e.Request.URL.Query().Get("urlExpiresSeconds")))
			if cfg, err := p.s3Config(e.App); err == nil {
				if u, err := filesystem.PresignS3URL(cfg, http.MethodGet, o.Key, storageDownloadURLExpiry); err == nil {
					result["downloadUrl"] = u
					result["downloadUrlExpiresAt"] = time.Now().Add(storageDownloadURLExpiry).UTC().Format(time.RFC3339)
				}
			}
		}
	}

	return e.JSON(http.StatusOK, result)
}

func storageObjectDownload(e *core.RequestEvent) error {
	o, err := storageLoadObject(e)
	if err != nil {
		return err
	}
	if o.Status != "uploaded" {
		return e.BadRequestError("The object is "+o.Status+".", nil)
	}

	p, err := storageObjectProvider(e, o)
	if err != nil {
		return err
	}
	fsys, err := p.filesystem(e.App)
	if err != nil {
		return storageUpstreamError(e, "Storage connection", err)
	}
	defer fsys.Close()

	if err := fsys.Serve(e.Response, e.Request, o.Key, o.Filename); err != nil {
		return storageUpstreamError(e, "Download", err)
	}
	return nil
}

func storageObjectDelete(e *core.RequestEvent) error {
	o, err := storageLoadObject(e)
	if err != nil {
		return err
	}
	if o.Status == "deleted" {
		return e.NoContent(http.StatusNoContent)
	}

	note := ""
	if p, err := storageObjectProvider(e, o); err == nil {
		fsys, err := p.filesystem(e.App)
		if err != nil {
			return storageUpstreamError(e, "Storage connection", err)
		}
		defer fsys.Close()
		if err := fsys.Delete(o.Key); err != nil && o.Status == "uploaded" {
			return storageUpstreamError(e, "Delete", err)
		}
	} else if e.Request.URL.Query().Get("force") != "true" {
		return err
	} else {
		note = "provider no longer exists, the remote object was not deleted"
	}

	_, err = e.App.DB().NewQuery(fmt.Sprintf(
		"UPDATE {{%s}} SET [[status]] = 'deleted', [[deletedAt]] = NOW(), [[updated]] = NOW(), [[error]] = {:note} WHERE [[id]] = {:id}",
		storageObjectsTable,
	)).Bind(dbx.Params{"id": o.Id, "note": note}).Execute()
	if err != nil {
		return e.InternalServerError("Failed to update the object history.", err)
	}

	return e.NoContent(http.StatusNoContent)
}
