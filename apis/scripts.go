package apis

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"dbx"

	"ws"
	"ws/wsutil"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/functioncall"
	"github.com/bosbase/bosbase-enterprise/tools/filesystem"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/types"
	"github.com/bosbase/bosbase-enterprise/wasmplugin"
	"github.com/coocood/freecache"
	"github.com/gofrs/uuid/v5"
	"github.com/redis/rueidis"
)

const functionScriptsTable = "function_scripts"
const defaultExecutePath = "/pb/functions"
const scriptVersionCacheSize = 10 * 1024 * 1024 // 10MB cache for script versions
const scriptRecordCacheSize = 20 * 1024 * 1024
const scriptRecordNotFoundTTLSeconds = 60
const scriptsSchemaCacheSize = 256 * 1024

var (
	scriptsSchemaCacheOnce sync.Once
	scriptsSchemaCache     *freecache.Cache
	scriptsSchemaInitMu    sync.Mutex
	wasmManagerOnce        sync.Once
	wasmManager            *wasmplugin.WasmManager
	wasmManagerErr         error
	functionCallClientMu   sync.Mutex
	functionCallClient     *functioncall.Client
	scriptVersionCache     *freecache.Cache
	scriptVersionCacheOnce sync.Once
	scriptRecordCacheOnce  sync.Once
	scriptRecordCache      *freecache.Cache
	scriptsJobsSchemaOnce  sync.Once
	scriptsJobsSchemaErr   error
)

func getScriptsSchemaCache() *freecache.Cache {
	scriptsSchemaCacheOnce.Do(func() {
		scriptsSchemaCache = freecache.NewCache(scriptsSchemaCacheSize)
	})

	return scriptsSchemaCache
}

func scriptsSchemaCacheKey(app core.App) []byte {
	return []byte(functionScriptsTable)
}

func getScriptRecordCache() *freecache.Cache {
	scriptRecordCacheOnce.Do(func() {
		scriptRecordCache = freecache.NewCache(scriptRecordCacheSize)
	})

	return scriptRecordCache
}

func scriptRecordCacheKey(app core.App, name string) []byte {
	return []byte("script:" + name)
}

type scriptRecord struct {
	ID          string         `json:"id" db:"id"`
	Name        string         `json:"name" db:"name"`
	Content     string         `json:"content" db:"content"`
	Description string         `json:"description,omitempty" db:"description"`
	Version     int            `json:"version" db:"version"`
	Created     types.DateTime `json:"created,omitempty" db:"created"`
	Updated     types.DateTime `json:"updated,omitempty" db:"updated"`
}

type scriptCreatePayload struct {
	Name        string `json:"name" form:"name"`
	Content     string `json:"content" form:"content"`
	Description string `json:"description" form:"description"`
}

type scriptUpdatePayload struct {
	Content     *string `json:"content" form:"content"`
	Description *string `json:"description" form:"description"`
}

type scriptCommandPayload struct {
	Command string `json:"command" form:"command"`
	Async   *bool  `json:"async" form:"async"`
}

type scriptCommandStatusPayload struct {
	Id string `json:"id" form:"id"`
}

type scriptExecuteStatusPayload struct {
	Id string `json:"id" form:"id"`
}

type scriptsCommandJob struct {
	Id       string     `db:"id"`
	Command  string     `db:"command"`
	Status   string     `db:"status"`
	Output   string     `db:"output"`
	Error    string     `db:"error"`
	Started  time.Time  `db:"started"`
	Finished *time.Time `db:"finished"`
}

type scriptsCommandRunner struct {
	mu   sync.RWMutex
	jobs map[string]*scriptsCommandJob
}

type scriptsExecuteJob struct {
	Id           string     `db:"id"`
	ScriptName   string     `db:"script_name"`
	FunctionName string     `db:"function_name"`
	Args         []string   `db:"-"`
	ArgsRaw      string     `db:"args"`
	Status       string     `db:"status"`
	Output       string     `db:"output"`
	Error        string     `db:"error"`
	Started      time.Time  `db:"started"`
	Finished     *time.Time `db:"finished"`
}

type scriptsExecuteRunner struct {
	mu   sync.RWMutex
	jobs map[string]*scriptsExecuteJob
}

type scriptsWasmJob struct {
	Id         string        `db:"id"`
	WasmName   string        `db:"wasm_name"`
	Status     string        `db:"status"`
	Output     string        `db:"output"`
	Stdout     string        `db:"stdout"`
	Stderr     string        `db:"stderr"`
	Error      string        `db:"error"`
	Duration   time.Duration `db:"-"`
	DurationNs int64         `db:"duration_ns"`
	Started    time.Time     `db:"started"`
	Finished   *time.Time    `db:"finished"`
	Function   string        `db:"function_name"`
	Params     string        `db:"params"`
	Options    string        `db:"options"`
}

type scriptsWasmRunner struct {
	mu   sync.RWMutex
	jobs map[string]*scriptsWasmJob
}

const scriptsCommandRunnerStoreKey = "__pbScriptsCommandRunner__"

const scriptsExecuteRunnerStoreKey = "__pbScriptsExecuteRunner__"

const scriptsWasmRunnerStoreKey = "__pbScriptsWasmRunner__"

const scriptsCommandJobsTable = "function_script_command_jobs"
const scriptsExecuteJobsTable = "function_script_execute_jobs"
const scriptsWasmJobsTable = "function_script_wasm_jobs"

func ensureScriptsCommandRunner(app core.App) *scriptsCommandRunner {
	if cached, ok := app.Store().Get(scriptsCommandRunnerStoreKey).(*scriptsCommandRunner); ok && cached != nil {
		return cached
	}

	r := &scriptsCommandRunner{jobs: make(map[string]*scriptsCommandJob)}
	app.Store().Set(scriptsCommandRunnerStoreKey, r)
	return r
}

func ensureScriptsExecuteRunner(app core.App) *scriptsExecuteRunner {
	if cached, ok := app.Store().Get(scriptsExecuteRunnerStoreKey).(*scriptsExecuteRunner); ok && cached != nil {
		return cached
	}

	r := &scriptsExecuteRunner{jobs: make(map[string]*scriptsExecuteJob)}
	app.Store().Set(scriptsExecuteRunnerStoreKey, r)
	return r
}

func ensureScriptsWasmRunner(app core.App) *scriptsWasmRunner {
	if cached, ok := app.Store().Get(scriptsWasmRunnerStoreKey).(*scriptsWasmRunner); ok && cached != nil {
		return cached
	}

	r := &scriptsWasmRunner{jobs: make(map[string]*scriptsWasmJob)}
	app.Store().Set(scriptsWasmRunnerStoreKey, r)
	return r
}

type scriptUploadPayload struct {
	Path string `json:"path" form:"path"`
}

type scriptWasmPayload struct {
	Options string `json:"options" form:"options"`
	Wasm    string `json:"wasm" form:"wasm"`
	Params  string `json:"params" form:"params"`
}

type scriptExecutePayload struct {
	Args         []string `json:"args" form:"args"`
	Arguments    []string `json:"arguments" form:"arguments"` // backward compat key
	FunctionName string   `json:"function_name" form:"function_name"`
}

func bindScriptsApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/scripts")

	super := sub.Bind(RequireSuperuserAuth())
	super.GET("", scriptsList)
	super.POST("", scriptsCreate)
	super.POST("/command", scriptsCommand)
	super.GET("/command/{id}", scriptsCommandStatus)
	super.POST("/command/status", scriptsCommandStatus)
	super.GET("/command/status/{id}", scriptsCommandStatus)
	super.POST("/upload", scriptsUpload)

	singleSuper := super.Group("/{name}")
	singleSuper.GET("", scriptsGet)
	singleSuper.PATCH("", scriptsUpdate)
	singleSuper.DELETE("", scriptsDelete)

	// execute is permission-based (may allow non-superuser)
	sub.POST("/{name}/execute", scriptsExecute)
	sub.GET("/{name}/execute/sse", scriptsExecuteSSE)
	sub.GET("/{name}/execute/ws", scriptsExecuteWebsocket)
	sub.POST("/async/{name}/execute", scriptsExecuteAsync)
	sub.GET("/async/{id}", scriptsExecuteAsyncStatus)
	sub.POST("/wasm", scriptsWasm)
	sub.POST("/wasm/async", scriptsWasmAsync)
	sub.GET("/wasm/async/{id}", scriptsWasmAsyncStatus)
}

func scriptsExecuteAsync(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}
	if err := ensureScriptsJobsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts jobs storage.", err)
	}

	name := strings.TrimSpace(e.Request.PathValue("name"))
	name = SafeScript(name)
	if name == "" {
		return e.BadRequestError("Script name is required.", nil)
	}

	payload := new(scriptExecutePayload)
	_ = e.BindBody(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	script, err := findScriptByName(e.App, ctx, name)
	if err != nil {
		return e.InternalServerError("Failed to load script.", err)
	}
	if script == nil {
		return e.NotFoundError("Script not found.", nil)
	}

	if err := backfillScriptID(e.App, ctx, script); err != nil {
		return e.InternalServerError("Failed to normalize script.", err)
	}

	if err := requireExecutePermission(e, script); err != nil {
		return err
	}

	filename, err := validateScriptFileName(script.Name)
	if err != nil {
		return e.BadRequestError("Invalid script name.", err)
	}

	scriptVersionCacheOnce.Do(func() {
		scriptVersionCache = freecache.NewCache(scriptVersionCacheSize)
	})

	cacheKey := []byte("script_version:" + script.Name)
	cachedVersionBytes, err := scriptVersionCache.Get(cacheKey)
	shouldWrite := true

	if err == nil && len(cachedVersionBytes) == 8 {
		cachedVersion := int(binary.BigEndian.Uint64(cachedVersionBytes))
		if cachedVersion == script.Version {
			shouldWrite = false
		}
	}

	scriptFileName := filename
	if !strings.HasSuffix(scriptFileName, ".py") {
		scriptFileName = scriptFileName + ".py"
	}

	executePath := resolveExecutePath()

	if shouldWrite {
		if err := os.MkdirAll(executePath, 0o755); err != nil {
			return e.InternalServerError("Failed to prepare execution directory.", err)
		}

		scriptsPath := filepath.Join(executePath, "scripts")
		if err := os.MkdirAll(scriptsPath, 0o755); err != nil {
			return e.InternalServerError("Failed to prepare scripts directory.", err)
		}

		targetFile := filepath.Join(scriptsPath, scriptFileName)
		if err := os.WriteFile(targetFile, []byte(script.Content), 0o644); err != nil {
			return e.InternalServerError("Failed to write script file.", err)
		}

		versionBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(versionBytes, uint64(script.Version))
		_ = scriptVersionCache.Set(cacheKey, versionBytes, 86400)
	}

	functionCallClientMu.Lock()
	if functionCallClient == nil {
		config := functioncall.DefaultConfig()
		client, err := functioncall.NewClient(config)
		if err != nil {
			functionCallClientMu.Unlock()
			return e.InternalServerError("Failed to create functioncall client.", err)
		}
		functionCallClient = client
	}
	client := functionCallClient
	functionCallClientMu.Unlock()

	args := payload.Args
	if len(args) == 0 && len(payload.Arguments) > 0 {
		args = payload.Arguments
	}

	argsInterface := make([]interface{}, len(args))
	for i, arg := range args {
		argsInterface[i] = arg
	}

	scriptPath := filepath.Join(executePath, "scripts", scriptFileName)

	functionName := strings.TrimSpace(payload.FunctionName)
	if functionName == "" {
		functionName = "main"
	}

	req := functioncall.ScriptExecuteRequest{
		ScriptPath:   scriptPath,
		ScriptName:   name,
		FunctionName: functionName,
		Args:         argsInterface,
		Kwargs:       nil,
	}

	jobId := ""
	if id, err := uuid.NewV7(); err == nil {
		jobId = id.String()
	} else {
		jobId = core.GenerateDefaultRandomId()
	}

	job := &scriptsExecuteJob{
		Id:           jobId,
		ScriptName:   name,
		FunctionName: functionName,
		Args:         args,
		Status:       "running",
	}

	if err := insertScriptsExecuteJob(e.App, job); err != nil {
		return e.InternalServerError("Failed to store execution job.", err)
	}

	go func() {
		resp, execErr := client.ExecuteScript(context.Background(), req)

		finished := time.Now()
		job.Finished = &finished
		if execErr != nil {
			job.Status = "error"
			job.Error = strings.TrimSpace(execErr.Error())
			_ = updateScriptsExecuteJob(e.App, job)
			return
		}
		if resp == nil {
			job.Status = "error"
			job.Error = "empty response"
			_ = updateScriptsExecuteJob(e.App, job)
			return
		}
		if !resp.Success {
			job.Status = "error"
			job.Error = strings.TrimSpace(resp.Error)
			_ = updateScriptsExecuteJob(e.App, job)
			return
		}

		var output string
		if resp.Result != nil {
			if resultStr, ok := resp.Result.(string); ok {
				output = resultStr
			} else {
				resultJSON, err := json.Marshal(resp.Result)
				if err != nil {
					output = fmt.Sprintf("%v", resp.Result)
				} else {
					output = string(resultJSON)
				}
			}
		}
		job.Output = output
		job.Status = "done"
		_ = updateScriptsExecuteJob(e.App, job)
	}()

	return e.JSON(http.StatusAccepted, map[string]any{"id": jobId, "status": "running"})
}

func scriptsExecuteAsyncStatus(e *core.RequestEvent) error {
	if err := ensureScriptsJobsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts jobs storage.", err)
	}

	jobId := strings.TrimSpace(e.Request.PathValue("id"))
	if jobId == "" {
		payload := new(scriptExecuteStatusPayload)
		if err := e.BindBody(payload); err != nil {
			return e.BadRequestError("An error occurred while loading the submitted data.", err)
		}
		jobId = strings.TrimSpace(payload.Id)
	}

	if jobId == "" {
		return e.BadRequestError("id is required.", nil)
	}

	job, err := findScriptsExecuteJob(e.App, e.Request.Context(), jobId)
	if err != nil {
		return e.InternalServerError("Failed to load execution job.", err)
	}
	if job == nil {
		return e.NotFoundError("Job not found.", nil)
	}

	resp := map[string]any{
		"id":     job.Id,
		"status": job.Status,
	}
	if job.Status == "done" {
		resp["output"] = job.Output
	}
	if job.Status == "error" {
		resp["error"] = job.Error
		resp["output"] = job.Output
	}

	return e.JSON(http.StatusOK, resp)
}

func scriptsCreate(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}

	payload := new(scriptCreatePayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	name := strings.TrimSpace(payload.Name)
	name = SafeScript(name)
	if name == "" {
		return e.BadRequestError("Script name is required.", nil)
	}

	trimmedContent := strings.TrimSpace(payload.Content)
	if trimmedContent == "" {
		return e.BadRequestError("Script content is required.", nil)
	}

	description := strings.TrimSpace(payload.Description)

	// Prevent duplicates by name.
	exists := 0
	if err := e.App.DB().
		Select("COUNT(*)").
		From(functionScriptsTable).
		Where(dbx.HashExp{"name": name}).
		WithContext(e.Request.Context()).
		Row(&exists); err != nil {
		return e.InternalServerError("Failed to check existing scripts.", err)
	}
	if exists > 0 {
		return e.BadRequestError("Script already exists.", nil)
	}

	insertSQL := fmt.Sprintf(`
		INSERT INTO {{%s}} ([[id]], [[name]], [[content]], [[description]], [[version]])
		VALUES ({:id}, {:name}, {:content}, {:description}, 1)
		RETURNING [[id]], [[name]], [[content]], [[description]], [[version]], [[created]], [[updated]];
	`, functionScriptsTable)

	record := new(scriptRecord)
	params := dbx.Params{
		"id":          generateScriptID(),
		"name":        name,
		"content":     payload.Content, // preserve formatting
		"description": description,
	}

	if err := e.App.NonconcurrentDB().
		NewQuery(insertSQL).
		Bind(params).
		WithContext(e.Request.Context()).
		One(record); err != nil {
		return e.InternalServerError("Failed to create script.", err)
	}

	// Update Redis cache if configured.
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL != "" {
		if service := ensureRedisService(e.App, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
			if client, err := service.getClient(); err == nil {
				redisKey := "script:" + name
				if data, err := json.Marshal(record); err == nil {
					_ = client.Do(e.Request.Context(), client.B().Set().Key(redisKey).Value(rueidis.BinaryString(data)).Build()).Error()
				}
			}
		}
	}

	// Update local cache.
	if data, err := json.Marshal(record); err == nil {
		_ = getScriptRecordCache().Set(scriptRecordCacheKey(e.App, name), data, 0)
	}

	return e.JSON(http.StatusCreated, record)
}

func scriptsGet(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}

	name := strings.TrimSpace(e.Request.PathValue("name"))
	if name == "" {
		return e.BadRequestError("Script name is required.", nil)
	}

	script, err := findScriptByName(e.App, e.Request.Context(), name)
	if err != nil {
		return e.InternalServerError("Failed to load script.", err)
	}
	if script == nil {
		return e.NotFoundError("Script not found.", nil)
	}

	if err := backfillScriptID(e.App, e.Request.Context(), script); err != nil {
		return e.InternalServerError("Failed to normalize script.", err)
	}

	return e.JSON(http.StatusOK, script)
}

func scriptsList(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}

	scripts := make([]scriptRecord, 0)

	if err := e.App.DB().
		Select("{{" + functionScriptsTable + "}}.*").
		From(functionScriptsTable).
		OrderBy("[[name]] ASC").
		WithContext(e.Request.Context()).
		All(&scripts); err != nil {
		return e.InternalServerError("Failed to list scripts.", err)
	}

	if err := backfillScriptIDs(e.App, e.Request.Context(), scripts); err != nil {
		return e.InternalServerError("Failed to normalize scripts.", err)
	}

	return e.JSON(http.StatusOK, map[string]any{"items": scripts})
}

func scriptsUpdate(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}

	name := strings.TrimSpace(e.Request.PathValue("name"))
	if name == "" {
		return e.BadRequestError("Script name is required.", nil)
	}

	payload := new(scriptUpdatePayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	hasContent := payload.Content != nil
	hasDescription := payload.Description != nil
	if !hasContent && !hasDescription {
		return e.BadRequestError("At least one of content or description must be provided.", nil)
	}

	params := dbx.Params{
		"name":        name,
		"content":     nil,
		"description": nil,
		"generatedId": generateScriptID(),
	}

	if hasContent {
		trimmed := strings.TrimSpace(*payload.Content)
		if trimmed == "" {
			return e.BadRequestError("Script content cannot be empty.", nil)
		}
		params["content"] = *payload.Content // preserve formatting
	}

	if hasDescription {
		desc := ""
		if payload.Description != nil {
			desc = strings.TrimSpace(*payload.Description)
		}
		params["description"] = desc
	}

	updateSQL := fmt.Sprintf(`
		UPDATE {{%s}}
		SET
			[[content]] = COALESCE({:content}, [[content]]),
			[[description]] = COALESCE({:description}, [[description]]),
			[[version]] = [[version]] + 1,
			[[updated]] = CURRENT_TIMESTAMP,
			[[id]] = COALESCE(NULLIF([[id]], ''), {:generatedId})
		WHERE [[name]] = {:name}
		RETURNING [[id]], [[name]], [[content]], [[description]], [[version]], [[created]], [[updated]];
	`, functionScriptsTable)

	updated := new(scriptRecord)
	err := e.App.NonconcurrentDB().
		NewQuery(updateSQL).
		Bind(params).
		WithContext(e.Request.Context()).
		One(updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.NotFoundError("Script not found.", nil)
		}
		return e.InternalServerError("Failed to update script.", err)
	}

	// Update Redis cache if configured.
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL != "" {
		if service := ensureRedisService(e.App, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
			if client, err := service.getClient(); err == nil {
				redisKey := "script:" + name
				if data, err := json.Marshal(updated); err == nil {
					_ = client.Do(e.Request.Context(), client.B().Set().Key(redisKey).Value(rueidis.BinaryString(data)).Build()).Error()
				}
			}
		}
	}

	// Update local cache.
	if data, err := json.Marshal(updated); err == nil {
		_ = getScriptRecordCache().Set(scriptRecordCacheKey(e.App, name), data, 0)
	}

	return e.JSON(http.StatusOK, updated)
}

func scriptsDelete(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}

	name := strings.TrimSpace(e.Request.PathValue("name"))
	if name == "" {
		return e.BadRequestError("Script name is required.", nil)
	}

	deleteSQL := fmt.Sprintf(`
		DELETE FROM {{%s}}
		WHERE [[name]] = {:name}
		RETURNING [[id]];
	`, functionScriptsTable)

	var deletedID string
	err := e.App.NonconcurrentDB().
		NewQuery(deleteSQL).
		Bind(dbx.Params{"name": name}).
		WithContext(e.Request.Context()).
		Row(&deletedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.NotFoundError("Script not found.", nil)
		}
		return e.InternalServerError("Failed to delete script.", err)
	}

	// Remove Redis cache if configured.
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))
	if redisURL != "" {
		if service := ensureRedisService(e.App, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey))); service != nil {
			if client, err := service.getClient(); err == nil {
				redisKey := "script:" + name
				_ = client.Do(e.Request.Context(), client.B().Del().Key(redisKey).Build()).Error()
			}
		}
	}

	// Remove local cache.
	getScriptRecordCache().Del(scriptRecordCacheKey(e.App, name))

	return e.NoContent(http.StatusNoContent)
}

func scriptsCommand(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}
	if err := ensureScriptsJobsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts jobs storage.", err)
	}

	payload := new(scriptCommandPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	cmdStr := strings.TrimSpace(payload.Command)
	if cmdStr == "" {
		return e.BadRequestError("Command is required.", nil)
	}
	async := false
	if payload.Async != nil {
		async = *payload.Async
	}

	execDir := resolveExecutePath()
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		return e.InternalServerError("Failed to prepare execution directory.", err)
	}

	jobId := ""
	if id, err := uuid.NewV7(); err == nil {
		jobId = id.String()
	} else {
		jobId = core.GenerateDefaultRandomId()
	}

	job := &scriptsCommandJob{
		Id:      jobId,
		Command: cmdStr,
		Status:  "running",
	}

	if err := insertScriptsCommandJob(e.App, job); err != nil {
		return e.InternalServerError("Failed to store command job.", err)
	}

	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
		cmd.Dir = execDir
		cmd.Env = os.Environ()

		output, execErr := cmd.CombinedOutput()

		job.Output = string(output)
		finished := time.Now()
		job.Finished = &finished
		if execErr != nil {
			job.Status = "error"
			job.Error = strings.TrimSpace(fmt.Sprintf("%v", execErr))
			_ = updateScriptsCommandJob(e.App, job)
			return
		}
		job.Status = "done"
		_ = updateScriptsCommandJob(e.App, job)
	}

	if async {
		go run()
		return e.JSON(http.StatusOK, map[string]any{"id": jobId, "status": "running"})
	}

	run()

	if job.Status == "error" {
		return e.InternalServerError("Failed to execute command.", fmt.Errorf("%s: %s", job.Error, strings.TrimSpace(job.Output)))
	}

	return e.JSON(http.StatusOK, map[string]any{"output": job.Output})
}

func scriptsCommandStatus(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}
	if err := ensureScriptsJobsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts jobs storage.", err)
	}

	jobId := strings.TrimSpace(e.Request.PathValue("id"))
	if jobId == "" {
		payload := new(scriptCommandStatusPayload)
		if err := e.BindBody(payload); err != nil {
			return e.BadRequestError("An error occurred while loading the submitted data.", err)
		}
		jobId = strings.TrimSpace(payload.Id)
	}

	if jobId == "" {
		return e.BadRequestError("id is required.", nil)
	}

	job, err := findScriptsCommandJob(e.App, e.Request.Context(), jobId)
	if err != nil {
		return e.InternalServerError("Failed to load command job.", err)
	}
	if job == nil {
		return e.NotFoundError("Command not found.", nil)
	}

	resp := map[string]any{
		"id":      job.Id,
		"status":  job.Status,
		"command": job.Command,
	}
	if job.Status == "done" {
		resp["output"] = job.Output
	}
	if job.Status == "error" {
		resp["output"] = job.Output
		resp["error"] = job.Error
	}

	return e.JSON(http.StatusOK, resp)
}

func scriptsUpload(e *core.RequestEvent) error {
	if err := ensureScriptsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts storage.", err)
	}
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}

	payload := new(scriptUploadPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	files, err := e.FindUploadedFiles("file")
	if err != nil || len(files) == 0 {
		return e.BadRequestError("File is required.", err)
	}
	uploadFile := files[0]

	path := strings.TrimSpace(payload.Path)
	if path == "" {
		path = strings.TrimSpace(uploadFile.OriginalName)
	}
	if path == "" {
		return e.BadRequestError("Path is required.", nil)
	}

	normalizedPath, err := sanitizeUploadPath(path)
	if err != nil {
		return e.BadRequestError("Invalid path.", err)
	}

	execDir := resolveExecutePath()
	absolutePath := filepath.Join(execDir, normalizedPath)
	if !strings.HasPrefix(absolutePath, execDir+string(filepath.Separator)) && absolutePath != execDir {
		return e.BadRequestError("Path must be within the EXECUTE_PATH directory.", nil)
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return e.InternalServerError("Failed to prepare upload destination.", err)
	}

	if err := writeUploadFile(absolutePath, uploadFile); err != nil {
		return e.InternalServerError("Failed to store uploaded file.", err)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"output": fmt.Sprintf("uploaded %s to %s", normalizedPath, execDir),
		"path":   absolutePath,
	})
}

func scriptsWasm(e *core.RequestEvent) error {
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}

	payload := new(scriptWasmPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	wasmName := strings.TrimSpace(payload.Wasm)
	wasmName = SafeScript(wasmName)
	if wasmName == "" {
		return e.BadRequestError("wasm is required.", nil)
	}

	normalizedWasm, err := sanitizeUploadPath(wasmName)
	if err != nil {
		return e.BadRequestError("Invalid wasm name.", err)
	}

	if err := requireWasmPermission(e, normalizedWasm); err != nil {
		return err
	}

	execDir := resolveExecutePath()
	if info, err := os.Stat(execDir); err == nil {
		if !info.IsDir() {
			return e.InternalServerError("Failed to prepare execution directory.", fmt.Errorf("%q exists but is not a directory", execDir))
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(execDir, 0o755); err != nil {
			return e.InternalServerError("Failed to prepare execution directory.", err)
		}
	} else {
		return e.InternalServerError("Failed to prepare execution directory.", err)
	}

	mgr, err := getWasmManager(execDir)
	if err != nil {
		return e.InternalServerError("Failed to get WASM manager.", err)
	}

	// Ensure module is loaded (only loads if not already loaded)
	if err := mgr.EnsureModuleLoaded(normalizedWasm); err != nil {
		return e.InternalServerError("Failed to load WASM module.", err)
	}

	// Determine function name (default to "_start" for command-like execution)
	// Support "main" as an alias for "_start" for Rust programs
	funcName := "_start"
	options := strings.TrimSpace(payload.Options)
	if options != "" {
		// Try to extract function name from options if specified
		// Format: --func=function_name or -f function_name
		// Also support just passing the function name directly
		if strings.HasPrefix(options, "--func=") {
			funcName = strings.TrimPrefix(options, "--func=")
			funcName = strings.Fields(funcName)[0] // Take first word
		} else if strings.HasPrefix(options, "-f ") {
			parts := strings.Fields(options)
			for i, part := range parts {
				if part == "-f" && i+1 < len(parts) {
					funcName = parts[i+1]
					break
				}
			}
		} else if !strings.HasPrefix(options, "-") && !strings.HasPrefix(options, "--") {
			// If options doesn't start with - or --, treat it as a function name
			funcName = strings.Fields(options)[0] // Take first word
		}
	}

	// Map "main" to "_start" for Rust programs with main() function
	if funcName == "main" {
		funcName = "_start"
	}

	// Parse params into []interface{}
	// Try to convert numeric strings to appropriate types for wasmedge-bindgen
	var callParams []interface{}
	params := strings.TrimSpace(payload.Params)
	if params != "" {
		// Split params by space and convert to interface slice
		paramParts := strings.Fields(params)
		callParams = make([]interface{}, len(paramParts))
		for i, p := range paramParts {
			// Try to convert to int32 first (common for WASM functions)
			if intVal, err := strconv.ParseInt(p, 10, 32); err == nil {
				callParams[i] = int32(intVal)
			} else if floatVal, err := strconv.ParseFloat(p, 64); err == nil {
				// Try float64 if int conversion fails
				callParams[i] = floatVal
			} else {
				// Keep as string if not numeric
				callParams[i] = p
			}
		}
	}

	// Call the function
	callResult, err := mgr.CallFunction(normalizedWasm, funcName, callParams)
	if err != nil {
		// Provide more context in error message
		moduleInfo, hasInfo := mgr.GetModuleInfo(normalizedWasm)
		errorDetails := err.Error()
		if hasInfo && len(moduleInfo.ExportedFuncs) > 0 {
			errorDetails += fmt.Sprintf(". Available functions: %v", moduleInfo.ExportedFuncs)
		}
		return e.InternalServerError("Failed to execute WASM function.", fmt.Errorf("%s", errorDetails))
	}

	if !callResult.Success {
		// Provide more context in error message
		moduleInfo, hasInfo := mgr.GetModuleInfo(normalizedWasm)
		errorDetails := callResult.Error
		if hasInfo && len(moduleInfo.ExportedFuncs) > 0 {
			errorDetails += fmt.Sprintf(". Available functions: %v", moduleInfo.ExportedFuncs)
		}
		return e.InternalServerError("WASM execution failed.", fmt.Errorf("%s", errorDetails))
	}

	// Convert results to output string
	var output strings.Builder
	for i, result := range callResult.Results {
		if i > 0 {
			output.WriteString(" ")
		}
		output.WriteString(fmt.Sprintf("%v", result))
	}

	// Combine stdout and stderr (stdout takes precedence, stderr appended if present)
	combinedOutput := callResult.Stdout
	if callResult.Stderr != "" {
		if combinedOutput != "" {
			combinedOutput += "\n"
		}
		combinedOutput += callResult.Stderr
	}

	// If we have captured output, use it; otherwise use function return values
	finalOutput := combinedOutput
	if finalOutput == "" {
		finalOutput = output.String()
	}

	return e.JSON(http.StatusOK, map[string]any{
		"output":   finalOutput,
		"stdout":   callResult.Stdout,
		"stderr":   callResult.Stderr,
		"duration": callResult.Duration.String(),
	})
}

func scriptsWasmAsync(e *core.RequestEvent) error {
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize script permissions storage.", err)
	}
	if err := ensureScriptsJobsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts jobs storage.", err)
	}

	payload := new(scriptWasmPayload)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	wasmName := strings.TrimSpace(payload.Wasm)
	wasmName = SafeScript(wasmName)
	if wasmName == "" {
		return e.BadRequestError("wasm is required.", nil)
	}

	normalizedWasm, err := sanitizeUploadPath(wasmName)
	if err != nil {
		return e.BadRequestError("Invalid wasm name.", err)
	}

	if err := requireWasmPermission(e, normalizedWasm); err != nil {
		return err
	}

	execDir := resolveExecutePath()
	if info, err := os.Stat(execDir); err == nil {
		if !info.IsDir() {
			return e.InternalServerError("Failed to prepare execution directory.", fmt.Errorf("%q exists but is not a directory", execDir))
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(execDir, 0o755); err != nil {
			return e.InternalServerError("Failed to prepare execution directory.", err)
		}
	} else {
		return e.InternalServerError("Failed to prepare execution directory.", err)
	}

	mgr, err := getWasmManager(execDir)
	if err != nil {
		return e.InternalServerError("Failed to get WASM manager.", err)
	}

	// Ensure module is loaded (only loads if not already loaded)
	if err := mgr.EnsureModuleLoaded(normalizedWasm); err != nil {
		return e.InternalServerError("Failed to load WASM module.", err)
	}

	funcName := "_start"
	options := strings.TrimSpace(payload.Options)
	if options != "" {
		if strings.HasPrefix(options, "--func=") {
			funcName = strings.TrimPrefix(options, "--func=")
			funcName = strings.Fields(funcName)[0]
		} else if strings.HasPrefix(options, "-f ") {
			parts := strings.Fields(options)
			for i, part := range parts {
				if part == "-f" && i+1 < len(parts) {
					funcName = parts[i+1]
					break
				}
			}
		} else if !strings.HasPrefix(options, "-") && !strings.HasPrefix(options, "--") {
			funcName = strings.Fields(options)[0]
		}
	}

	if funcName == "main" {
		funcName = "_start"
	}

	var callParams []interface{}
	params := strings.TrimSpace(payload.Params)
	if params != "" {
		paramParts := strings.Fields(params)
		callParams = make([]interface{}, len(paramParts))
		for i, p := range paramParts {
			if intVal, err := strconv.ParseInt(p, 10, 32); err == nil {
				callParams[i] = int32(intVal)
			} else if floatVal, err := strconv.ParseFloat(p, 64); err == nil {
				callParams[i] = floatVal
			} else {
				callParams[i] = p
			}
		}
	}

	jobId := ""
	if id, err := uuid.NewV7(); err == nil {
		jobId = id.String()
	} else {
		jobId = core.GenerateDefaultRandomId()
	}

	job := &scriptsWasmJob{
		Id:       jobId,
		WasmName: normalizedWasm,
		Status:   "running",
		Function: funcName,
		Params:   params,
		Options:  options,
	}

	if err := insertScriptsWasmJob(e.App, job); err != nil {
		return e.InternalServerError("Failed to store wasm job.", err)
	}

	go func() {
		callResult, callErr := mgr.CallFunction(normalizedWasm, funcName, callParams)

		var output strings.Builder
		for i, result := range callResult.Results {
			if i > 0 {
				output.WriteString(" ")
			}
			output.WriteString(fmt.Sprintf("%v", result))
		}

		combinedOutput := callResult.Stdout
		if callResult.Stderr != "" {
			if combinedOutput != "" {
				combinedOutput += "\n"
			}
			combinedOutput += callResult.Stderr
		}

		finalOutput := combinedOutput
		if finalOutput == "" {
			finalOutput = output.String()
		}

		duration := callResult.Duration
		if duration <= 0 {
			duration = time.Since(job.Started)
		}

		finished := time.Now()
		job.Finished = &finished
		job.Duration = duration
		job.Stdout = callResult.Stdout
		job.Stderr = callResult.Stderr
		job.Output = finalOutput
		job.DurationNs = int64(duration)

		if callErr != nil {
			moduleInfo, hasInfo := mgr.GetModuleInfo(normalizedWasm)
			errorDetails := callErr.Error()
			if hasInfo && len(moduleInfo.ExportedFuncs) > 0 {
				errorDetails += fmt.Sprintf(". Available functions: %v", moduleInfo.ExportedFuncs)
			}
			job.Status = "error"
			job.Error = strings.TrimSpace(errorDetails)
			_ = updateScriptsWasmJob(e.App, job)
			return
		}

		if !callResult.Success {
			moduleInfo, hasInfo := mgr.GetModuleInfo(normalizedWasm)
			errorDetails := callResult.Error
			if hasInfo && len(moduleInfo.ExportedFuncs) > 0 {
				errorDetails += fmt.Sprintf(". Available functions: %v", moduleInfo.ExportedFuncs)
			}
			job.Status = "error"
			job.Error = strings.TrimSpace(errorDetails)
			_ = updateScriptsWasmJob(e.App, job)
			return
		}

		job.Status = "done"
		_ = updateScriptsWasmJob(e.App, job)
	}()

	return e.JSON(http.StatusAccepted, map[string]any{"id": jobId, "status": "running"})
}

func scriptsWasmAsyncStatus(e *core.RequestEvent) error {
	if err := ensureScriptsJobsSchema(e.App); err != nil {
		return e.InternalServerError("Failed to initialize scripts jobs storage.", err)
	}

	jobId := strings.TrimSpace(e.Request.PathValue("id"))
	if jobId == "" {
		payload := new(scriptExecuteStatusPayload)
		if err := e.BindBody(payload); err != nil {
			return e.BadRequestError("An error occurred while loading the submitted data.", err)
		}
		jobId = strings.TrimSpace(payload.Id)
	}

	if jobId == "" {
		return e.BadRequestError("id is required.", nil)
	}

	job, err := findScriptsWasmJob(e.App, e.Request.Context(), jobId)
	if err != nil {
		return e.InternalServerError("Failed to load wasm job.", err)
	}
	if job == nil {
		return e.NotFoundError("Job not found.", nil)
	}

	resp := map[string]any{
		"id":         job.Id,
		"wasmName":   job.WasmName,
		"status":     job.Status,
		"startedAt":  job.Started.Format(time.RFC3339),
		"function":   job.Function,
		"options":    job.Options,
		"parameters": job.Params,
	}

	if job.Finished != nil {
		resp["finishedAt"] = job.Finished.Format(time.RFC3339)
	}

	if job.Duration > 0 {
		resp["duration"] = job.Duration.String()
	}

	if job.Status == "done" || job.Status == "error" {
		resp["output"] = job.Output
		resp["stdout"] = job.Stdout
		resp["stderr"] = job.Stderr
	}

	if job.Status == "error" {
		resp["error"] = job.Error
	}

	return e.JSON(http.StatusOK, resp)
}

func scriptsExecute(e *core.RequestEvent) error {
	payload := new(scriptExecutePayload)
	// ignore bind errors to allow empty body
	_ = e.BindBody(payload)

	script, safeName, apiErr := loadExecutableScript(e, e.Request.PathValue("name"))
	if apiErr != nil {
		return apiErr
	}

	output, apiErr := executeScriptForRecord(e, script, safeName, payload)
	if apiErr != nil {
		return apiErr
	}

	return e.JSON(http.StatusOK, map[string]any{
		"output": output,
	})
}

func scriptsExecuteSSE(e *core.RequestEvent) error {
	payload := scriptExecutePayloadFromQuery(e.Request.URL.Query())

	script, safeName, apiErr := loadExecutableScript(e, e.Request.PathValue("name"))
	if apiErr != nil {
		return apiErr
	}

	output, apiErr := executeScriptForRecord(e, script, safeName, payload)
	if apiErr != nil {
		return apiErr
	}

	data, err := json.Marshal(map[string]any{"output": output})
	if err != nil {
		return e.InternalServerError("Failed to marshal script output.", err)
	}

	e.Response.Header().Set("Content-Type", "text/event-stream")
	e.Response.Header().Set("Cache-Control", "no-store")
	e.Response.Header().Set("X-Accel-Buffering", "no")

	if _, err := e.Response.Write([]byte("data:" + string(data) + "\n\n")); err != nil {
		return e.InternalServerError("Failed to write SSE payload.", err)
	}

	return e.Flush()
}

func scriptsExecuteWebsocket(e *core.RequestEvent) error {
	payload := scriptExecutePayloadFromQuery(e.Request.URL.Query())

	script, safeName, apiErr := loadExecutableScript(e, e.Request.PathValue("name"))
	if apiErr != nil {
		return apiErr
	}

	conn, _, _, err := ws.UpgradeHTTP(e.Request, e.Response)
	if err != nil {
		return e.InternalServerError("Failed to establish websocket connection.", err)
	}
	defer conn.Close()

	if !hasScriptExecuteParams(payload) {
		msg, op, err := wsutil.ReadClientData(conn)
		if err != nil {
			return nil
		}

		if op == ws.OpClose {
			return nil
		}

		if op != ws.OpText && op != ws.OpBinary {
			return nil
		}

		incoming := new(scriptExecutePayload)
		if err := json.Unmarshal(msg, incoming); err != nil {
			_ = writeScriptExecuteWebsocketError(conn, router.NewBadRequestError("Invalid websocket payload.", err))
			return nil
		}

		payload = mergeScriptExecutePayload(payload, incoming)
	}

	output, apiErr := executeScriptForRecord(e, script, safeName, payload)
	if apiErr != nil {
		_ = writeScriptExecuteWebsocketError(conn, apiErr)
		return nil
	}

	resp, err := json.Marshal(map[string]any{"output": output})
	if err != nil {
		_ = writeScriptExecuteWebsocketError(conn, e.InternalServerError("Failed to marshal script output.", err))
		return nil
	}

	_ = wsutil.WriteServerMessage(conn, ws.OpText, resp)

	return nil
}

func loadExecutableScript(e *core.RequestEvent, rawName string) (*scriptRecord, string, *router.ApiError) {
	if err := ensureScriptsSchema(e.App); err != nil {
		return nil, "", e.InternalServerError("Failed to initialize scripts storage.", err)
	}
	if err := ensureScriptPermissionsSchema(e.App); err != nil {
		return nil, "", e.InternalServerError("Failed to initialize script permissions storage.", err)
	}

	name := SafeScript(strings.TrimSpace(rawName))
	if name == "" {
		return nil, "", e.BadRequestError("Script name is required.", nil)
	}

	script, err := findScriptByName(e.App, e.Request.Context(), name)
	if err != nil {
		return nil, "", e.InternalServerError("Failed to load script.", err)
	}
	if script == nil {
		return nil, "", e.NotFoundError("Script not found.", nil)
	}

	if err := backfillScriptID(e.App, e.Request.Context(), script); err != nil {
		return nil, "", e.InternalServerError("Failed to normalize script.", err)
	}

	if err := requireExecutePermission(e, script); err != nil {
		return nil, "", router.ToApiError(err)
	}

	return script, name, nil
}

func executeScriptForRecord(
	e *core.RequestEvent,
	script *scriptRecord,
	scriptName string,
	payload *scriptExecutePayload,
) (string, *router.ApiError) {
	if payload == nil {
		payload = new(scriptExecutePayload)
	}

	filename, err := validateScriptFileName(script.Name)
	if err != nil {
		return "", e.BadRequestError("Invalid script name.", err)
	}

	// Initialize script version cache
	scriptVersionCacheOnce.Do(func() {
		scriptVersionCache = freecache.NewCache(scriptVersionCacheSize)
	})

	// Check if script version has changed using cache
	cacheKey := []byte("script_version:" + script.Name)
	cachedVersionBytes, err := scriptVersionCache.Get(cacheKey)
	shouldWrite := true

	if err == nil && len(cachedVersionBytes) == 8 {
		// Cache hit - check if version matches
		cachedVersion := int(binary.BigEndian.Uint64(cachedVersionBytes))
		if cachedVersion == script.Version {
			// Version hasn't changed, skip file operations
			shouldWrite = false
		}
	}

	// Prepare script file name (needed for scriptPath later)
	scriptFileName := filename
	if !strings.HasSuffix(scriptFileName, ".py") {
		scriptFileName = scriptFileName + ".py"
	}

	// Get executePath for scriptPath construction (needed for functioncall)
	executePath := resolveExecutePath()

	// Only create directories and write file if version changed
	if shouldWrite {
		if err := os.MkdirAll(executePath, 0o755); err != nil {
			return "", e.InternalServerError("Failed to prepare execution directory.", err)
		}

		scriptsPath := filepath.Join(executePath, "scripts")
		if err := os.MkdirAll(scriptsPath, 0o755); err != nil {
			return "", e.InternalServerError("Failed to prepare scripts directory.", err)
		}

		targetFile := filepath.Join(scriptsPath, scriptFileName)
		if err := os.WriteFile(targetFile, []byte(script.Content), 0o644); err != nil {
			return "", e.InternalServerError("Failed to write script file.", err)
		}

		// Update cache with new version
		versionBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(versionBytes, uint64(script.Version))
		// Cache for 24 hours (86400 seconds)
		_ = scriptVersionCache.Set(cacheKey, versionBytes, 86400)
	}

	// Get or create functioncall client
	functionCallClientMu.Lock()
	if functionCallClient == nil {
		config := functioncall.DefaultConfig()
		client, err := functioncall.NewClient(config)
		if err != nil {
			functionCallClientMu.Unlock()
			return "", e.InternalServerError("Failed to create functioncall client.", err)
		}
		functionCallClient = client
	}
	client := functionCallClient
	functionCallClientMu.Unlock()

	// Convert arguments from []string to []interface{}
	args := payload.Args
	if len(args) == 0 && len(payload.Arguments) > 0 {
		args = payload.Arguments
	}

	argsInterface := make([]interface{}, len(args))
	for i, arg := range args {
		argsInterface[i] = arg
	}

	// Prepare script path
	scriptPath := filepath.Join(executePath, "scripts", scriptFileName)

	// Get function name from payload, default to "main" if not provided
	functionName := strings.TrimSpace(payload.FunctionName)
	if functionName == "" {
		functionName = "main"
	}

	// Create execution request
	req := functioncall.ScriptExecuteRequest{
		ScriptPath:   scriptPath,
		ScriptName:   scriptName,
		FunctionName: functionName,
		Args:         argsInterface,
		Kwargs:       nil,
	}

	// Execute script via functioncall client
	ctx := context.Background()
	resp, err := client.ExecuteScript(ctx, req)
	if err != nil {
		return "", e.InternalServerError("Failed to execute script via functioncall.", err)
	}

	if !resp.Success {
		return "", e.InternalServerError("Script execution failed.", fmt.Errorf("error: %s", resp.Error))
	}

	// Format output
	var output string
	if resp.Result != nil {
		if resultStr, ok := resp.Result.(string); ok {
			output = resultStr
		} else {
			// Convert result to JSON string if it's not a string
			resultJSON, err := json.Marshal(resp.Result)
			if err != nil {
				output = fmt.Sprintf("%v", resp.Result)
			} else {
				output = string(resultJSON)
			}
		}
	}

	return output, nil
}

func scriptExecutePayloadFromQuery(values url.Values) *scriptExecutePayload {
	payload := &scriptExecutePayload{}
	if values == nil {
		return payload
	}

	if args := values["arguments"]; len(args) > 0 {
		payload.Arguments = args
	}
	if len(payload.Arguments) == 0 {
		if args := values["args"]; len(args) > 0 {
			payload.Args = args
		}
	}

	fn := strings.TrimSpace(values.Get("function_name"))
	if fn == "" {
		fn = strings.TrimSpace(values.Get("functionName"))
	}
	if fn != "" {
		payload.FunctionName = fn
	}

	return payload
}

func hasScriptExecuteParams(payload *scriptExecutePayload) bool {
	if payload == nil {
		return false
	}

	if len(payload.Args) > 0 || len(payload.Arguments) > 0 {
		return true
	}

	return strings.TrimSpace(payload.FunctionName) != ""
}

func mergeScriptExecutePayload(base, incoming *scriptExecutePayload) *scriptExecutePayload {
	if incoming == nil {
		return base
	}

	if base == nil {
		return incoming
	}

	if len(incoming.Args) > 0 {
		base.Args = incoming.Args
	}

	if len(incoming.Arguments) > 0 {
		base.Arguments = incoming.Arguments
	}

	if strings.TrimSpace(incoming.FunctionName) != "" {
		base.FunctionName = incoming.FunctionName
	}

	return base
}

func writeScriptExecuteWebsocketError(conn io.Writer, apiErr *router.ApiError) error {
	if apiErr == nil {
		return nil
	}

	payload := map[string]any{
		"status":  apiErr.Status,
		"message": apiErr.Message,
	}

	if apiErr.Data != nil {
		payload["data"] = apiErr.Data
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return wsutil.WriteServerMessage(conn, ws.OpText, data)
}

func resolveExecutePath() string {
	if val := strings.TrimSpace(os.Getenv("EXECUTE_PATH")); val != "" {
		return filepath.Clean(val)
	}
	return defaultExecutePath
}

func getWasmManager(executePath string) (*wasmplugin.WasmManager, error) {
	wasmManagerOnce.Do(func() {
		config := wasmplugin.Config{
			WatchDir:     executePath,
			AutoReload:   true,
			MaxInstances: 5,
			HealthCheck:  5 * time.Minute,
			AllowedPaths: []string{executePath},
		}
		wasmManager, wasmManagerErr = wasmplugin.NewManager(config)
	})
	return wasmManager, wasmManagerErr
}

func validateScriptFileName(name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." {
		return "", errors.New("invalid name")
	}
	if cleaned != filepath.Base(cleaned) {
		return "", errors.New("script name must not contain path separators")
	}
	return cleaned, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func sanitizeUploadPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	cleaned := filepath.Clean(trimmed)

	if cleaned == "" || cleaned == "." {
		return "", errors.New("path cannot be empty")
	}

	if filepath.IsAbs(cleaned) {
		return "", errors.New("path must be relative to EXECUTE_PATH")
	}

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("path cannot traverse outside EXECUTE_PATH")
	}

	return cleaned, nil
}

func writeUploadFile(destination string, file *filesystem.File) error {
	reader, err := file.Reader.Open()
	if err != nil {
		return err
	}
	defer reader.Close()

	dst, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, reader); err != nil {
		return err
	}

	if err := dst.Sync(); err != nil {
		return err
	}

	return os.Chmod(destination, 0o755)
}

func requireExecutePermission(e *core.RequestEvent, script *scriptRecord) error {
	isSuper := e.Auth != nil && e.Auth.IsSuperuser()

	perm, err := findScriptPermissionByScript(e.Request.Context(), e.App, script.ID, script.Name)
	if err != nil {
		return e.InternalServerError("Failed to load script permissions.", err)
	}

	if perm == nil {
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires superuser or allowed user authorization token.", nil)
		}
		return e.ForbiddenError("You are not allowed to execute this script.", nil)
	}

	switch strings.ToLower(perm.Content) {
	case "anonymous":
		return nil
	case "user":
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires valid record authorization token.", nil)
		}
		if isRegularUserAuth(e.Auth.Collection().Name) {
			return nil
		}
		return e.ForbiddenError("You are not allowed to execute this script.", nil)
	case "superuser":
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires superuser authorization token.", nil)
		}
		return e.ForbiddenError("You are not allowed to execute this script.", nil)
	default:
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires superuser authorization token.", nil)
		}
		return e.ForbiddenError("You are not allowed to execute this script.", nil)
	}
}

func isRegularUserAuth(collectionName string) bool {
	name := strings.ToLower(strings.TrimSpace(collectionName))
	return name == "users" || name == "_pb_users_auth_"
}

func requireWasmPermission(e *core.RequestEvent, wasmName string) error {
	isSuper := e.Auth != nil && e.Auth.IsSuperuser()

	perm, err := findScriptPermission(e.Request.Context(), e.App, wasmName)
	if err != nil {
		return e.InternalServerError("Failed to load wasm permissions.", err)
	}

	if perm == nil {
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires superuser authorization token.", nil)
		}
		return e.ForbiddenError("You are not allowed to execute this wasm.", nil)
	}

	switch strings.ToLower(perm.Content) {
	case "anonymous":
		return nil
	case "user":
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires valid record authorization token.", nil)
		}
		if isRegularUserAuth(e.Auth.Collection().Name) {
			return nil
		}
		return e.ForbiddenError("You are not allowed to execute this wasm.", nil)
	case "superuser":
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires superuser authorization token.", nil)
		}
		return e.ForbiddenError("You are not allowed to execute this wasm.", nil)
	default:
		if isSuper {
			return nil
		}
		if e.Auth == nil {
			return e.UnauthorizedError("The request requires superuser authorization token.", nil)
		}
		return e.ForbiddenError("You are not allowed to execute this wasm.", nil)
	}
}

func ensureScriptsJobsSchema(app core.App) error {
	scriptsJobsSchemaOnce.Do(func() {
		commandTableSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS {{%s}} (
				[[id]]        TEXT PRIMARY KEY,
				[[command]]   TEXT NOT NULL,
				[[status]]    TEXT NOT NULL,
				[[output]]    TEXT DEFAULT '',
				[[error]]     TEXT DEFAULT '',
				[[started]]   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				[[finished]]  TIMESTAMPTZ
			);
		`, scriptsCommandJobsTable)

		executeTableSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS {{%s}} (
				[[id]]             TEXT PRIMARY KEY,
				[[script_name]]    TEXT NOT NULL,
				[[function_name]]  TEXT NOT NULL,
				[[args]]           TEXT DEFAULT '',
				[[status]]         TEXT NOT NULL,
				[[output]]         TEXT DEFAULT '',
				[[error]]          TEXT DEFAULT '',
				[[started]]        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				[[finished]]       TIMESTAMPTZ
			);
		`, scriptsExecuteJobsTable)

		wasmTableSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS {{%s}} (
				[[id]]            TEXT PRIMARY KEY,
				[[wasm_name]]     TEXT NOT NULL,
				[[function_name]] TEXT NOT NULL,
				[[params]]        TEXT DEFAULT '',
				[[options]]       TEXT DEFAULT '',
				[[status]]        TEXT NOT NULL,
				[[output]]        TEXT DEFAULT '',
				[[stdout]]        TEXT DEFAULT '',
				[[stderr]]        TEXT DEFAULT '',
				[[error]]         TEXT DEFAULT '',
				[[duration_ns]]   BIGINT DEFAULT 0,
				[[started]]       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				[[finished]]      TIMESTAMPTZ
			);
		`, scriptsWasmJobsTable)

		for _, stmt := range []string{commandTableSQL, executeTableSQL, wasmTableSQL} {
			if _, err := app.NonconcurrentDB().NewQuery(stmt).WithContext(context.Background()).Execute(); err != nil {
				scriptsJobsSchemaErr = err
				return
			}
		}
	})

	return scriptsJobsSchemaErr
}

func insertScriptsCommandJob(app core.App, job *scriptsCommandJob) error {
	job.Started = time.Now()
	insertSQL := fmt.Sprintf(`
		INSERT INTO {{%s}} ([[id]], [[command]], [[status]], [[output]], [[error]], [[started]], [[finished]])
		VALUES ({:id}, {:command}, {:status}, {:output}, {:error}, {:started}, {:finished});
	`, scriptsCommandJobsTable)

	_, err := app.NonconcurrentDB().
		NewQuery(insertSQL).
		Bind(dbx.Params{
			"id":       job.Id,
			"command":  job.Command,
			"status":   job.Status,
			"output":   job.Output,
			"error":    job.Error,
			"started":  job.Started,
			"finished": job.Finished,
		}).
		WithContext(context.Background()).
		Execute()

	return err
}

func updateScriptsCommandJob(app core.App, job *scriptsCommandJob) error {
	updateSQL := fmt.Sprintf(`
		UPDATE {{%s}}
		SET [[status]] = {:status}, [[output]] = {:output}, [[error]] = {:error}, [[finished]] = {:finished}
		WHERE [[id]] = {:id};
	`, scriptsCommandJobsTable)

	_, err := app.NonconcurrentDB().
		NewQuery(updateSQL).
		Bind(dbx.Params{
			"id":       job.Id,
			"status":   job.Status,
			"output":   job.Output,
			"error":    job.Error,
			"finished": job.Finished,
		}).
		WithContext(context.Background()).
		Execute()

	return err
}

func findScriptsCommandJob(app core.App, ctx context.Context, id string) (*scriptsCommandJob, error) {
	job := new(scriptsCommandJob)
	err := app.DB().
		Select("*").
		From(scriptsCommandJobsTable).
		Where(dbx.HashExp{"id": id}).
		WithContext(ctx).
		One(job)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return job, nil
}

func insertScriptsExecuteJob(app core.App, job *scriptsExecuteJob) error {
	job.Started = time.Now()
	argsJSON, _ := json.Marshal(job.Args)
	job.ArgsRaw = string(argsJSON)

	insertSQL := fmt.Sprintf(`
		INSERT INTO {{%s}} ([[id]], [[script_name]], [[function_name]], [[args]], [[status]], [[output]], [[error]], [[started]], [[finished]])
		VALUES ({:id}, {:script_name}, {:function_name}, {:args}, {:status}, {:output}, {:error}, {:started}, {:finished});
	`, scriptsExecuteJobsTable)

	_, err := app.NonconcurrentDB().
		NewQuery(insertSQL).
		Bind(dbx.Params{
			"id":            job.Id,
			"script_name":   job.ScriptName,
			"function_name": job.FunctionName,
			"args":          job.ArgsRaw,
			"status":        job.Status,
			"output":        job.Output,
			"error":         job.Error,
			"started":       job.Started,
			"finished":      job.Finished,
		}).
		WithContext(context.Background()).
		Execute()

	return err
}

func updateScriptsExecuteJob(app core.App, job *scriptsExecuteJob) error {
	updateSQL := fmt.Sprintf(`
		UPDATE {{%s}}
		SET [[status]] = {:status}, [[output]] = {:output}, [[error]] = {:error}, [[finished]] = {:finished}
		WHERE [[id]] = {:id};
	`, scriptsExecuteJobsTable)

	_, err := app.NonconcurrentDB().
		NewQuery(updateSQL).
		Bind(dbx.Params{
			"id":       job.Id,
			"status":   job.Status,
			"output":   job.Output,
			"error":    job.Error,
			"finished": job.Finished,
		}).
		WithContext(context.Background()).
		Execute()

	return err
}

func findScriptsExecuteJob(app core.App, ctx context.Context, id string) (*scriptsExecuteJob, error) {
	job := new(scriptsExecuteJob)
	err := app.DB().
		Select("*").
		From(scriptsExecuteJobsTable).
		Where(dbx.HashExp{"id": id}).
		WithContext(ctx).
		One(job)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if strings.TrimSpace(job.ArgsRaw) != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(job.ArgsRaw), &parsed); err == nil {
			job.Args = parsed
		}
	}

	return job, nil
}

func insertScriptsWasmJob(app core.App, job *scriptsWasmJob) error {
	job.Started = time.Now()
	job.DurationNs = int64(job.Duration)

	insertSQL := fmt.Sprintf(`
		INSERT INTO {{%s}} ([[id]], [[wasm_name]], [[function_name]], [[params]], [[options]], [[status]], [[output]], [[stdout]], [[stderr]], [[error]], [[duration_ns]], [[started]], [[finished]])
		VALUES ({:id}, {:wasm_name}, {:function_name}, {:params}, {:options}, {:status}, {:output}, {:stdout}, {:stderr}, {:error}, {:duration_ns}, {:started}, {:finished});
	`, scriptsWasmJobsTable)

	_, err := app.NonconcurrentDB().
		NewQuery(insertSQL).
		Bind(dbx.Params{
			"id":            job.Id,
			"wasm_name":     job.WasmName,
			"function_name": job.Function,
			"params":        job.Params,
			"options":       job.Options,
			"status":        job.Status,
			"output":        job.Output,
			"stdout":        job.Stdout,
			"stderr":        job.Stderr,
			"error":         job.Error,
			"duration_ns":   job.DurationNs,
			"started":       job.Started,
			"finished":      job.Finished,
		}).
		WithContext(context.Background()).
		Execute()

	return err
}

func updateScriptsWasmJob(app core.App, job *scriptsWasmJob) error {
	job.DurationNs = int64(job.Duration)

	updateSQL := fmt.Sprintf(`
		UPDATE {{%s}}
		SET [[status]] = {:status}, [[output]] = {:output}, [[stdout]] = {:stdout}, [[stderr]] = {:stderr}, [[error]] = {:error}, [[duration_ns]] = {:duration_ns}, [[finished]] = {:finished}
		WHERE [[id]] = {:id};
	`, scriptsWasmJobsTable)

	_, err := app.NonconcurrentDB().
		NewQuery(updateSQL).
		Bind(dbx.Params{
			"id":          job.Id,
			"status":      job.Status,
			"output":      job.Output,
			"stdout":      job.Stdout,
			"stderr":      job.Stderr,
			"error":       job.Error,
			"duration_ns": job.DurationNs,
			"finished":    job.Finished,
		}).
		WithContext(context.Background()).
		Execute()

	return err
}

func findScriptsWasmJob(app core.App, ctx context.Context, id string) (*scriptsWasmJob, error) {
	job := new(scriptsWasmJob)
	err := app.DB().
		Select("*").
		From(scriptsWasmJobsTable).
		Where(dbx.HashExp{"id": id}).
		WithContext(ctx).
		One(job)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	job.Duration = time.Duration(job.DurationNs)

	return job, nil
}

func ensureScriptsSchema(app core.App) error {
	cache := getScriptsSchemaCache()
	cacheKey := scriptsSchemaCacheKey(app)
	if _, err := cache.Get(cacheKey); err == nil {
		return nil
	}

	scriptsSchemaInitMu.Lock()
	defer scriptsSchemaInitMu.Unlock()

	if _, err := cache.Get(cacheKey); err == nil {
		return nil
	}

	driver := core.BuilderDriverName(app.NonconcurrentDB())
	timestampCreated := core.TimestampColumnDefinition(driver, "created")
	timestampUpdated := core.TimestampColumnDefinition(driver, "updated")

	createSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS {{%s}} (
				[[id]]          TEXT NOT NULL,
				[[name]]        TEXT PRIMARY KEY,
				[[content]]     TEXT NOT NULL,
				[[description]] TEXT DEFAULT '',
				[[version]]     INTEGER NOT NULL DEFAULT 1,
				%s,
				%s
			);
		`, functionScriptsTable, timestampCreated, timestampUpdated)

	indexSQL := fmt.Sprintf(`
			CREATE UNIQUE INDEX IF NOT EXISTS idx__%s_id ON {{%s}} ([[id]]);
		`, functionScriptsTable, functionScriptsTable)

	for _, stmt := range []string{createSQL, indexSQL} {
		if _, err := app.NonconcurrentDB().NewQuery(stmt).Execute(); err != nil {
			return err
		}
	}

	_ = cache.Set(cacheKey, []byte{1}, 0)

	return nil
}

func generateScriptID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return core.GenerateDefaultRandomId()
}

func findScriptByName(app core.App, ctx context.Context, name string) (*scriptRecord, error) {
	script := new(scriptRecord)
	name = SafeScript(name)
	// try freecache
	if name == "" {
		return nil, nil
	}

	cache := getScriptRecordCache()
	cacheKey := scriptRecordCacheKey(app, name)
	if data, err := cache.Get(cacheKey); err == nil {
		if len(data) == 1 && data[0] == 0 {
			return nil, nil
		}
		if unmarshalErr := json.Unmarshal(data, script); unmarshalErr == nil {
			return script, nil
		}
	}

	// Try Redis first if configured
	redisURL := normalizeRedisURL(strings.TrimSpace(os.Getenv(redisURLEnvKey)))

	if redisURL != "" {
		service := ensureRedisService(app, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey)))
		client, err := service.getClient()
		if err == nil {
			redisKey := "script:" + name

			if data, err := client.Do(ctx, client.B().Get().Key(redisKey).Build()).AsBytes(); err == nil {
				if unmarshalErr := json.Unmarshal(data, script); unmarshalErr == nil {
					_ = cache.Set(cacheKey, data, 0)
					return script, nil
				}
			}
		}
	}

	// Fallback to DB
	err := app.DB().
		Select("{{" + functionScriptsTable + "}}.*").
		From(functionScriptsTable).
		Where(dbx.HashExp{"name": name}).
		WithContext(ctx).
		One(script)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = cache.Set(cacheKey, []byte{0}, scriptRecordNotFoundTTLSeconds)
			return nil, nil
		}
		return nil, err
	}
	// update redis
	var cachedData []byte
	if data, err := json.Marshal(script); err == nil {
		cachedData = data
		if redisURL != "" {
			service := ensureRedisService(app, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey)))
			client, err := service.getClient()
			if err == nil {
				redisKey := "script:" + name
				_ = client.Do(ctx, client.B().Set().Key(redisKey).Value(rueidis.BinaryString(data)).Build()).Error()
			}
		}
	}
	//update freecache
	if len(cachedData) > 0 {
		_ = cache.Set(cacheKey, cachedData, 0)
	}

	return script, nil
}

func backfillScriptID(app core.App, ctx context.Context, script *scriptRecord) error {
	if script == nil || strings.TrimSpace(script.ID) != "" {
		return nil
	}

	newID := generateScriptID()
	now := types.NowDateTime()

	updateSQL := fmt.Sprintf(`
		UPDATE {{%s}}
		SET [[id]] = {:id}, [[updated]] = {:updated}
		WHERE [[name]] = {:name};
	`, functionScriptsTable)

	if _, err := app.NonconcurrentDB().
		NewQuery(updateSQL).
		Bind(dbx.Params{
			"id":      newID,
			"updated": now,
			"name":    script.Name,
		}).
		WithContext(ctx).
		Execute(); err != nil {
		return err
	}

	script.ID = newID
	script.Updated = now

	return nil
}

func backfillScriptIDs(app core.App, ctx context.Context, scripts []scriptRecord) error {
	for i := range scripts {
		if err := backfillScriptID(app, ctx, &scripts[i]); err != nil {
			return err
		}
	}
	return nil
}

// ShellSafe
func ShellSafe(input string) (string, bool) {
	if input == "" {
		return "", false
	}
	orig := input
	lower := strings.ToLower(input)

	ultimateBlock := []string{

		"passwd", "chpasswd", "openssl passwd",
		"useradd", "adduser", "usermod", "userdel", "deluser", "groupadd", "groupdel",
		"vipw", "vigr", "chage",

		"openssh", "ssh", "sshd", "ssh-keygen", "ssh-copy-id",
		"authorized_keys", ".ssh/", "ssh-rsa ", "ssh-ed25519 ",
		"service ", "systemctl ", "start ssh", "enable ssh", "restart ssh",

		"sudoers", "visudo", "tee /etc/sudoers", "chmod 440", "chown root",

		"/etc/passwd", "/etc/shadow", "/etc/group", "/etc/sudoers",
		"/root/.", "/home/", "~/.ssh/", "known_hosts",

		"reboot", "shutdown", "init ", "poweroff", "halt",
		"mkfs", "fdisk", "parted", "dd of=", "mount ", "umount ",
		"insmod", "rmmod", "modprobe", "crontab ", "at ", "chmod 4", "chmod 6", "chmod 7",
		"chown ", "chgrp ", "ln -s ", "mknod ", "mkfifo ",

		"install", "apt ", "yum ", "dnf ", "apk ", "apt-get", "pip ", "npm ", "curl ", "wget ",
		"rm ", "whoami", "id ", "uname ", "ps ", "netstat", "cat /", "env ", "history ",
		";", "||", "|", "`", "$(", "${", "<", ">", ">>", "#", "*", "?", "[", "]",

		"http", "https", "echo", "git", "cat", "ls",
	}
	for _, bad := range ultimateBlock {
		if strings.Contains(lower, bad) {
			return orig, false
		}
	}

	// ========= 第2层：只允许极少数纯净业务命令 =========
	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return "", false
	}

	cmd := strings.ToLower(tokens[0])

	// ========= 第3层：特殊命令深度限制 =========
	switch cmd {
	case "git":
		allowedGit := map[string]bool{
			"pull": true, "fetch": true, "status": true, "log": true,
			"diff": true, "show": true, "checkout": true, "branch": true,
		}
		if len(tokens) > 1 {
			sub := strings.ToLower(tokens[1])
			if !allowedGit[sub] {
				return orig, false
			}
		}
	case "ls", "cat":
		// 禁止任何绝对路径和 .. 路径穿越
		for _, t := range tokens[1:] {
			if strings.HasPrefix(t, "/") || strings.Contains(t, "..") || strings.Contains(t, "~") {
				return orig, false
			}
		}
	case "echo":
		// echo 允许，但禁止重定向写文件
		if strings.Contains(lower, ">") || strings.Contains(lower, ">>") || strings.Contains(lower, "|") {
			return orig, false
		}
	}

	// ========= 第5层：长度与字符集终极限制 =========
	if len(input) > 200 {
		return orig, false
	}

	return input, true
}

// 下面的函数保持不变（和之前一样）
func isSafeOption(s string) bool {
	if len(s) < 2 {
		return false
	}
	if strings.HasPrefix(s, "--") {
		return isSafeIdentifier(s[2:])
	}
	if strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "--") {
		rest := s[1:]
		if rest == "" {
			return false
		}
		for _, r := range rest {
			if !unicode.IsLetter(r) {
				return false
			}
		}
		return true
	}
	return false
}

func isSafeArgument(s string) bool {
	return isSafeIdentifier(s)
}

func isSafeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}
