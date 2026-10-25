package apis

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/spf13/cast"
)

// Superuser diagnostics: health snapshot, error log queries (database + on disk
// error files) and markdown error reports, available over HTTP and MCP so that
// an AI agent can troubleshoot a running BosBase instance.

var processStartedAt = time.Now()

const diagnosticsReportsDir = "reports"

func bindDiagnosticsApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/diagnostics").Bind(RequireSuperuserAuth())
	sub.GET("/health", func(e *core.RequestEvent) error { return e.JSON(http.StatusOK, diagnosticsHealth(e.App)) })
	sub.GET("/errors", diagnosticsErrorsHandler)
	sub.GET("/files", diagnosticsFilesHandler)
	sub.POST("/report", diagnosticsReportHandler)
}

// -------------------------------------------------------------------
// Health
// -------------------------------------------------------------------

func diagnosticsHealth(app core.App) map[string]any {
	result := map[string]any{"checkedAt": time.Now().UTC().Format(time.RFC3339)}
	problems := []string{}

	// runtime
	mem := runtime.MemStats{}
	runtime.ReadMemStats(&mem)
	build := map[string]any{"goVersion": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}
	if info, ok := debug.ReadBuildInfo(); ok {
		build["module"] = info.Main.Path
		build["moduleVersion"] = info.Main.Version
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" || s.Key == "vcs.time" || s.Key == "vcs.modified" {
				build[s.Key] = s.Value
			}
		}
	}
	result["build"] = build
	result["runtime"] = map[string]any{
		"uptime":       time.Since(processStartedAt).Round(time.Second).String(),
		"startedAt":    processStartedAt.UTC().Format(time.RFC3339),
		"goroutines":   runtime.NumGoroutine(),
		"heapAllocMB":  mem.HeapAlloc >> 20,
		"sysMB":        mem.Sys >> 20,
		"numGC":        mem.NumGC,
		"cpus":         runtime.NumCPU(),
		"dataDir":      app.DataDir(),
		"isDev":        app.IsDev(),
		"bootstrapped": app.IsBootstrapped(),
	}

	// database
	db := map[string]any{}
	started := time.Now()
	var one int
	if err := app.DB().NewQuery("SELECT 1").Row(&one); err != nil {
		db["ok"] = false
		db["error"] = err.Error()
		problems = append(problems, "Main database is not reachable: "+err.Error())
	} else {
		db["ok"] = true
		db["pingMs"] = time.Since(started).Milliseconds()

		var version, size string
		_ = app.DB().NewQuery("SELECT version()").Row(&version)
		_ = app.DB().NewQuery("SELECT pg_size_pretty(pg_database_size(current_database()))").Row(&size)
		db["serverVersion"] = version
		db["size"] = size

		var vectorVersion string
		if err := app.DB().NewQuery("SELECT extversion FROM pg_extension WHERE extname = 'vector'").Row(&vectorVersion); err != nil {
			db["pgvector"] = "not installed"
			problems = append(problems, "The pgvector extension is not installed in the database.")
		} else {
			db["pgvector"] = vectorVersion
		}

		var connections int
		_ = app.DB().NewQuery("SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database()").Row(&connections)
		db["serverConnections"] = connections

		var lastMigration string
		var migrations int
		_ = app.DB().NewQuery("SELECT COUNT(*) FROM {{_migrations}}").Row(&migrations)
		_ = app.DB().NewQuery("SELECT [[file]] FROM {{_migrations}} ORDER BY [[applied]] DESC, [[file]] DESC LIMIT 1").Row(&lastMigration)
		db["appliedMigrations"] = migrations
		db["lastMigration"] = lastMigration
	}
	if sqlDB, ok := app.DB().(interface{ DB() *dbx.DB }); ok && sqlDB.DB() != nil {
		stats := sqlDB.DB().DB().Stats()
		db["pool"] = map[string]any{
			"open":         stats.OpenConnections,
			"inUse":        stats.InUse,
			"idle":         stats.Idle,
			"waitCount":    stats.WaitCount,
			"waitDuration": stats.WaitDuration.String(),
		}
	}
	result["database"] = db

	aux := map[string]any{}
	if err := app.AuxDB().NewQuery("SELECT 1").Row(&one); err != nil {
		aux["ok"] = false
		aux["error"] = err.Error()
		problems = append(problems, "Logs (aux) database is not reachable: "+err.Error())
	} else {
		aux["ok"] = true
	}
	result["auxDatabase"] = aux

	// settings summary
	settings := app.Settings()
	result["settings"] = map[string]any{
		"appName":       settings.Meta.AppName,
		"appURL":        settings.Meta.AppURL,
		"logsMaxDays":   settings.Logs.MaxDays,
		"logsMinLevel":  settings.Logs.MinLevel,
		"batchEnabled":  settings.Batch.Enabled,
		"s3Enabled":     settings.S3.Enabled,
		"smtpEnabled":   settings.SMTP.Enabled,
		"backupsCron":   settings.Backups.Cron,
		"rateLimits":    settings.RateLimits.Enabled,
		"trustedProxy":  settings.TrustedProxy.Headers,
		"dbLogsEnabled": os.Getenv("ENABLE_DB_LOG") == "" || cast.ToBool(os.Getenv("ENABLE_DB_LOG")),
	}

	// encryption key
	encryption := map[string]any{"env": app.EncryptionEnv(), "enabled": core.EncryptionKey(app) != ""}
	if raw := os.Getenv(app.EncryptionEnv()); app.EncryptionEnv() != "" && raw != "" && !core.IsValidEncryptionKey(raw) {
		encryption["invalidKeyLength"] = len(raw)
		problems = append(problems, fmt.Sprintf("The encryption key %s is invalid (%d characters, 32 expected): encryption is disabled and secrets are stored unencrypted.", app.EncryptionEnv(), len(raw)))
	} else if encryption["enabled"] == false {
		encryption["note"] = "No encryption key configured: settings and integration secrets are stored unencrypted (set BS_ENCRYPTION_KEY to a 32 characters value)."
	}
	if core.IsSettingsUnreadable(app) {
		encryption["settingsUnreadable"] = true
		problems = append(problems, "The stored settings can't be decrypted (missing, invalid or different encryption key): the app runs with the default settings and saving the settings is blocked. Restore the original key and restart, or set "+core.AllowSettingsResetEnv+"=true to overwrite them.")
	}
	result["encryption"] = encryption

	// recent errors
	errorsInfo := map[string]any{}
	var lastHour, lastDay, server5xx int
	hourAgo := time.Now().Add(-time.Hour)
	dayAgo := time.Now().Add(-24 * time.Hour)
	_ = app.AuxDB().NewQuery("SELECT COUNT(*) FROM {{_logs}} WHERE [[level]] >= 8 AND [[created]] >= {:t}").Bind(dbx.Params{"t": hourAgo}).Row(&lastHour)
	_ = app.AuxDB().NewQuery("SELECT COUNT(*) FROM {{_logs}} WHERE [[level]] >= 8 AND [[created]] >= {:t}").Bind(dbx.Params{"t": dayAgo}).Row(&lastDay)
	_ = app.AuxDB().NewQuery("SELECT COUNT(*) FROM {{_logs}} WHERE [[created]] >= {:t} AND ([[data]]->>'status') ~ '^5'").Bind(dbx.Params{"t": dayAgo}).Row(&server5xx)
	errorsInfo["lastHour"] = lastHour
	errorsInfo["last24h"] = lastDay
	errorsInfo["server5xxLast24h"] = server5xx
	if server5xx > 0 {
		problems = append(problems, fmt.Sprintf("%d server errors (5xx) in the last 24h, see diagnostics_errors with status 5xx.", server5xx))
	}
	files := diagnosticsListErrorFiles(app)
	errorsInfo["errorLogFiles"] = len(files)
	errorsInfo["errorLogDir"] = filepath.Join(app.DataDir(), core.ErrorLogsDirName)
	result["errors"] = errorsInfo

	// integrations
	integrations := map[string]any{}
	primary, embedErr := embeddingPrimary(app)
	integrations["embeddingConfigured"] = primary != nil
	integrations["embeddingOrder"] = embeddingOrderNames(app)
	if primary != nil {
		integrations["embeddingModel"] = primary.Model
		integrations["embeddingSource"] = primary.Source
	}
	if embedErr != nil {
		integrations["embeddingError"] = embedErr.Error()
		problems = append(problems, "The embedding providers can't be loaded: "+embedErr.Error())
	}
	if providers, err := embeddingLoadProviders(app, true); err == nil {
		for _, p := range providers {
			// failing since its last success
			if p.LastError != "" && p.LastErrorAt != "" && (p.LastUsedAt == "" || p.LastErrorAt > p.LastUsedAt) {
				problems = append(problems, fmt.Sprintf("Embedding provider %q is failing (%s): %s. See embedding_status.", p.Name, p.LastErrorAt, p.LastError))
			}
		}
	}
	for table, key := range map[string]string{
		mcpMemoriesTable:        "memories",
		mcpEdgesTable:           "memoryRelations",
		mcpActivityTable:        "mcpActivity",
		storageProvidersTable:   "storageProviders",
		storageObjectsTable:     "storageObjects",
		llmChannelsTable:        "llmChannels",
		llmRequestsTable:        "llmRequests",
		embeddingProvidersTable: "embeddingProviders",
	} {
		var count int
		if err := app.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}}", table)).Row(&count); err != nil {
			integrations[key] = "missing table"
			problems = append(problems, "Table "+table+" is missing (migrations not applied?).")
		} else {
			integrations[key] = count
		}
	}
	var legacySharedMemories int
	_ = app.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE [[owner]] = '' AND [[path]] = ''", mcpMemoriesTable)).Row(&legacySharedMemories)
	if legacySharedMemories > 0 {
		integrations["hiddenSharedMemories"] = legacySharedMemories
		problems = append(problems, fmt.Sprintf("%d shared memories without a note path exist (created before memories became always private); they are hidden from everyone. Convert them to public knowledge base notes or delete them.", legacySharedMemories))
	}
	var llmErrors int
	_ = app.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE [[created]] >= {:t} AND ([[status]] >= 400 OR [[status]] = 0)", llmRequestsTable)).
		Bind(dbx.Params{"t": dayAgo}).Row(&llmErrors)
	integrations["llmErrorsLast24h"] = llmErrors
	result["integrations"] = integrations

	result["problems"] = problems
	result["healthy"] = len(problems) == 0

	return result
}

// -------------------------------------------------------------------
// Errors
// -------------------------------------------------------------------

type diagnosticsErrorQuery struct {
	Since  string `json:"since"`
	Until  string `json:"until"`
	Level  string `json:"level"`
	Search string `json:"search"`
	Status string `json:"status"`
	Source string `json:"source"`
	Limit  int    `json:"limit"`
}

type diagnosticsLogEntry struct {
	Id      string         `json:"id,omitempty"`
	Time    string         `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Status  string         `json:"status,omitempty"`
	Error   string         `json:"error,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Source  string         `json:"source"`
}

func diagnosticsLevelValue(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "all", "debug":
		return -4
	case "info":
		return 0
	case "warn", "warning":
		return 4
	default:
		return 8
	}
}

func diagnosticsLevelName(level int) string {
	switch {
	case level >= 8:
		return "ERROR"
	case level >= 4:
		return "WARN"
	case level >= 0:
		return "INFO"
	default:
		return "DEBUG"
	}
}

func diagnosticsRange(q *diagnosticsErrorQuery) (time.Time, time.Time, error) {
	since := time.Now().Add(-24 * time.Hour)
	until := time.Now().Add(time.Minute)
	if q.Since != "" {
		if h, err := strconv.Atoi(strings.TrimSuffix(q.Since, "h")); err == nil && strings.HasSuffix(q.Since, "h") {
			since = time.Now().Add(-time.Duration(h) * time.Hour)
		} else {
			t, err := mcpParseTimeArg(q.Since)
			if err != nil {
				return since, until, err
			}
			since = t
		}
	}
	if q.Until != "" {
		t, err := mcpParseTimeArg(q.Until)
		if err != nil {
			return since, until, err
		}
		until = t
	}
	return since, until, nil
}

func diagnosticsStatusMatch(filter string, status string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	if strings.HasSuffix(filter, "xx") && len(filter) == 3 {
		return strings.HasPrefix(status, filter[:1])
	}
	return status == filter
}

func diagnosticsQueryErrors(app core.App, q *diagnosticsErrorQuery) ([]*diagnosticsLogEntry, error) {
	since, until, err := diagnosticsRange(q)
	if err != nil {
		return nil, err
	}
	limit := clampInt(q.Limit, 50, 1000)
	minLevel := diagnosticsLevelValue(q.Level)

	source := strings.ToLower(q.Source)
	if source == "file" || source == "files" {
		return diagnosticsReadErrorFiles(app, q, since, until, minLevel, limit)
	}

	params := dbx.Params{"since": since, "until": until, "level": minLevel, "limit": limit}
	filters := []string{"[[level]] >= {:level}", "[[created]] >= {:since}", "[[created]] < {:until}"}
	if s := strings.TrimSpace(q.Search); s != "" {
		params["search"] = "%" + escapeLike(s) + "%"
		filters = append(filters, "([[message]] ILIKE {:search} OR [[data]]::text ILIKE {:search})")
	}
	if st := strings.ToLower(strings.TrimSpace(q.Status)); st != "" {
		if strings.HasSuffix(st, "xx") && len(st) == 3 {
			params["status"] = "^" + st[:1]
			filters = append(filters, "([[data]]->>'status') ~ {:status}")
		} else {
			params["status"] = st
			filters = append(filters, "([[data]]->>'status') = {:status}")
		}
	}

	rows := []struct {
		Id      string `db:"id"`
		Level   int    `db:"level"`
		Message string `db:"message"`
		Data    string `db:"data"`
		Created string `db:"created"`
	}{}
	err = app.AuxDB().NewQuery(fmt.Sprintf(
		"SELECT [[id]], [[level]], [[message]], [[data]]::text AS [[data]], [[created]]::text AS [[created]] FROM {{_logs}} WHERE %s ORDER BY [[created]] DESC LIMIT {:limit}",
		strings.Join(filters, " AND "),
	)).Bind(params).All(&rows)
	if err != nil {
		if source == "db" {
			return nil, err
		}
		// the database may be the problem -> fallback to the files
		return diagnosticsReadErrorFiles(app, q, since, until, minLevel, limit)
	}

	result := make([]*diagnosticsLogEntry, len(rows))
	for i, r := range rows {
		data := map[string]any{}
		_ = json.Unmarshal([]byte(r.Data), &data)
		result[i] = diagnosticsEntry(r.Id, r.Created, diagnosticsLevelName(r.Level), r.Message, data, "db")
	}
	return result, nil
}

func diagnosticsEntry(id, t, level, message string, data map[string]any, source string) *diagnosticsLogEntry {
	entry := &diagnosticsLogEntry{Id: id, Time: t, Level: level, Message: message, Data: data, Source: source}
	if data != nil {
		if v, ok := data["status"]; ok {
			entry.Status = cast.ToString(v)
		}
		if v, ok := data["error"]; ok {
			entry.Error = cast.ToString(v)
		}
	}
	return entry
}

func diagnosticsListErrorFiles(app core.App) []map[string]any {
	dir := filepath.Join(app.DataDir(), core.ErrorLogsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]any{}
	}
	result := []map[string]any{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, map[string]any{
			"name":     e.Name(),
			"size":     info.Size(),
			"modified": info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i]["name"].(string) > result[j]["name"].(string) })
	return result
}

func diagnosticsReadErrorFiles(app core.App, q *diagnosticsErrorQuery, since, until time.Time, minLevel, limit int) ([]*diagnosticsLogEntry, error) {
	result := []*diagnosticsLogEntry{}
	search := strings.ToLower(strings.TrimSpace(q.Search))

	for day := until.UTC(); !day.Before(since.UTC().Truncate(24 * time.Hour)); day = day.AddDate(0, 0, -1) {
		f, err := os.Open(core.ErrorLogFilePath(app.DataDir(), day))
		if err != nil {
			continue
		}
		dayEntries := []*diagnosticsLogEntry{}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			raw := struct {
				Time    string         `json:"time"`
				Level   string         `json:"level"`
				Message string         `json:"message"`
				Data    map[string]any `json:"data"`
			}{}
			if json.Unmarshal(line, &raw) != nil {
				continue
			}
			t, err := time.Parse(time.RFC3339Nano, raw.Time)
			if err != nil || t.Before(since) || !t.Before(until) {
				continue
			}
			if diagnosticsLevelValue(raw.Level) < minLevel && !(minLevel == 8 && strings.HasPrefix(raw.Level, "ERROR")) {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(string(line)), search) {
				continue
			}
			entry := diagnosticsEntry("", raw.Time, raw.Level, raw.Message, raw.Data, "file")
			if !diagnosticsStatusMatch(q.Status, entry.Status) {
				continue
			}
			dayEntries = append(dayEntries, entry)
		}
		f.Close()

		for i := len(dayEntries) - 1; i >= 0 && len(result) < limit; i-- {
			result = append(result, dayEntries[i])
		}
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

type diagnosticsErrorGroup struct {
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
	Count   int    `json:"count"`
	First   string `json:"first"`
	Last    string `json:"last"`
}

// diagnosticsGroupErrors groups the entries by (normalized message, status, error).
func diagnosticsGroupErrors(entries []*diagnosticsLogEntry) []*diagnosticsErrorGroup {
	groups := map[string]*diagnosticsErrorGroup{}
	order := []string{}
	for _, e := range entries {
		msg := diagnosticsNormalizeMessage(e.Message)
		key := msg + "|" + e.Status + "|" + e.Error
		g, ok := groups[key]
		if !ok {
			g = &diagnosticsErrorGroup{Message: msg, Status: e.Status, Error: mcpTruncate(e.Error, 300), First: e.Time, Last: e.Time}
			groups[key] = g
			order = append(order, key)
		}
		g.Count++
		if e.Time < g.First {
			g.First = e.Time
		}
		if e.Time > g.Last {
			g.Last = e.Time
		}
	}
	result := make([]*diagnosticsErrorGroup, 0, len(order))
	for _, k := range order {
		result = append(result, groups[k])
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Count > result[j].Count })
	return result
}

// diagnosticsNormalizeMessage replaces the id-like path segments and the query string of request messages.
func diagnosticsNormalizeMessage(msg string) string {
	if i := strings.Index(msg, "?"); i >= 0 {
		msg = msg[:i]
	}
	parts := strings.Split(msg, "/")
	for i, p := range parts {
		if len(p) >= 12 && strings.IndexFunc(p, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 && !strings.Contains(p, " ") {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

func diagnosticsErrorsResult(app core.App, q *diagnosticsErrorQuery) (map[string]any, error) {
	entries, err := diagnosticsQueryErrors(app, q)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"count":   len(entries),
		"groups":  diagnosticsGroupErrors(entries),
		"entries": entries,
	}, nil
}

func diagnosticsErrorsHandler(e *core.RequestEvent) error {
	query := e.Request.URL.Query()
	q := &diagnosticsErrorQuery{
		Since:  query.Get("since"),
		Until:  query.Get("until"),
		Level:  query.Get("level"),
		Search: query.Get("search"),
		Status: query.Get("status"),
		Source: query.Get("source"),
		Limit:  cast.ToInt(query.Get("limit")),
	}
	result, err := diagnosticsErrorsResult(e.App, q)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	return e.JSON(http.StatusOK, result)
}

// -------------------------------------------------------------------
// Error log files
// -------------------------------------------------------------------

func diagnosticsReadFile(app core.App, name string, tail int, search string) (map[string]any, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if !strings.HasSuffix(name, ".jsonl") {
		return nil, mcpToolErr("Invalid file name (use the names returned by the list action).")
	}
	f, err := os.Open(filepath.Join(app.DataDir(), core.ErrorLogsDirName, name))
	if err != nil {
		return nil, mcpToolErr("File " + strconv.Quote(name) + " was not found.")
	}
	defer f.Close()

	tail = clampInt(tail, 200, 5000)
	search = strings.ToLower(strings.TrimSpace(search))
	lines := []string{}
	total := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if search != "" && !strings.Contains(strings.ToLower(line), search) {
			continue
		}
		total++
		lines = append(lines, line)
		if len(lines) > tail {
			lines = lines[1:]
		}
	}

	return map[string]any{"file": name, "matchedLines": total, "lines": lines}, nil
}

func diagnosticsFilesHandler(e *core.RequestEvent) error {
	q := e.Request.URL.Query()
	if name := q.Get("file"); name != "" {
		result, err := diagnosticsReadFile(e.App, name, cast.ToInt(q.Get("tail")), q.Get("search"))
		if err != nil {
			return e.NotFoundError(err.Error(), nil)
		}
		return e.JSON(http.StatusOK, result)
	}
	return e.JSON(http.StatusOK, map[string]any{
		"dir":   filepath.Join(e.App.DataDir(), core.ErrorLogsDirName),
		"files": diagnosticsListErrorFiles(e.App),
	})
}

// -------------------------------------------------------------------
// Report
// -------------------------------------------------------------------

func diagnosticsReport(app core.App, since string, save bool) (map[string]any, error) {
	if since == "" {
		since = "24h"
	}
	health := diagnosticsHealth(app)
	errorsResult, err := diagnosticsErrorsResult(app, &diagnosticsErrorQuery{Since: since, Limit: 500})
	if err != nil {
		return nil, err
	}
	serverErrors, _ := diagnosticsErrorsResult(app, &diagnosticsErrorQuery{Since: since, Status: "5xx", Limit: 20})

	var b strings.Builder
	now := time.Now().UTC()
	fmt.Fprintf(&b, "# BosBase diagnostics report\n\nGenerated: %s  \nPeriod: last %s\n\n", now.Format(time.RFC3339), since)

	b.WriteString("## Summary\n\n")
	if problems, _ := health["problems"].([]string); len(problems) > 0 {
		for _, p := range problems {
			b.WriteString("- ⚠️ " + p + "\n")
		}
	} else {
		b.WriteString("- ✅ No health problems detected.\n")
	}
	fmt.Fprintf(&b, "- Errors in period: %v\n", errorsResult["count"])
	b.WriteString("\n")

	writeJSONSection := func(title string, v any) {
		raw, _ := json.MarshalIndent(v, "", "  ")
		fmt.Fprintf(&b, "## %s\n\n```json\n%s\n```\n\n", title, raw)
	}
	writeJSONSection("Build", health["build"])
	writeJSONSection("Runtime", health["runtime"])
	writeJSONSection("Database", health["database"])
	writeJSONSection("Settings", health["settings"])
	writeJSONSection("Integrations", health["integrations"])

	b.WriteString("## Top error groups\n\n")
	groups, _ := errorsResult["groups"].([]*diagnosticsErrorGroup)
	if len(groups) == 0 {
		b.WriteString("No errors recorded in the period.\n\n")
	} else {
		b.WriteString("| Count | Status | Message | Error | Last seen |\n| --- | --- | --- | --- | --- |\n")
		for i, g := range groups {
			if i >= 20 {
				break
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n", g.Count, g.Status, mdEscape(g.Message), mdEscape(mcpTruncate(g.Error, 120)), g.Last)
		}
		b.WriteString("\n")
	}

	if serverErrors != nil {
		if entries, _ := serverErrors["entries"].([]*diagnosticsLogEntry); len(entries) > 0 {
			b.WriteString("## Latest server errors (5xx)\n\n")
			for i, entry := range entries {
				if i >= 10 {
					break
				}
				raw, _ := json.MarshalIndent(entry, "", "  ")
				fmt.Fprintf(&b, "```json\n%s\n```\n\n", raw)
			}
		}
	}

	b.WriteString("## Error log files\n\n")
	for _, f := range diagnosticsListErrorFiles(app) {
		fmt.Fprintf(&b, "- %s (%v bytes)\n", f["name"], f["size"])
	}

	report := b.String()
	result := map[string]any{"markdown": report, "healthy": health["healthy"], "errors": errorsResult["count"]}

	if save {
		dir := filepath.Join(app.DataDir(), core.ErrorLogsDirName, diagnosticsReportsDir)
		if err := os.MkdirAll(dir, os.ModePerm); err == nil {
			p := filepath.Join(dir, "bosbase-report-"+now.Format("20060102-150405")+".md")
			if err := os.WriteFile(p, []byte(report), 0o640); err == nil {
				result["savedTo"] = p
			}
		}
	}

	return result, nil
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

func diagnosticsReportHandler(e *core.RequestEvent) error {
	payload := struct {
		Since string `json:"since"`
		Save  *bool  `json:"save"`
	}{}
	_ = e.BindBody(&payload)
	save := payload.Save == nil || *payload.Save
	result, err := diagnosticsReport(e.App, payload.Since, save)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	return e.JSON(http.StatusOK, result)
}

// -------------------------------------------------------------------
// MCP tools
// -------------------------------------------------------------------

var mcpDiagnosticsTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	mcpDiagnosticsTools = []mcpTool{
		{
			Name:          "diagnostics_health",
			Description:   "Superusers: health snapshot of the BosBase instance (build, runtime, database/pgvector, connection pool, settings, recent error counts, integrations) with a list of detected problems. Start troubleshooting here.",
			InputSchema:   obj(map[string]any{}),
			SuperuserOnly: true,
			ReadOnly:      true,
			NoScopeArgs:   true,
			Handler: func(e *core.RequestEvent, args json.RawMessage) (any, error) {
				return diagnosticsHealth(e.App), nil
			},
		},
		{
			Name:        "diagnostics_errors",
			Description: "Superusers: query the error logs (grouped + latest entries). Reads the database logs and falls back to the on disk error files when the database is unavailable.",
			InputSchema: obj(map[string]any{
				"since":  str("Start of the period: \"24h\", \"2h\", RFC3339 or YYYY-MM-DD (default 24h)."),
				"until":  str("End of the period (RFC3339 or YYYY-MM-DD)."),
				"level":  map[string]any{"type": "string", "enum": []string{"error", "warn", "info", "all"}},
				"status": str("HTTP status filter, eg. 500 or 5xx."),
				"search": str("Text searched in the message and data."),
				"source": map[string]any{"type": "string", "enum": []string{"auto", "db", "file"}, "description": "Log source (default auto)."},
				"limit":  map[string]any{"type": "integer", "description": "Max entries (default 50, max 1000)."},
			}),
			SuperuserOnly: true,
			ReadOnly:      true,
			NoScopeArgs:   true,
			Handler: func(e *core.RequestEvent, args json.RawMessage) (any, error) {
				q := &diagnosticsErrorQuery{}
				if err := mcpBindArgs(args, q); err != nil {
					return nil, err
				}
				result, err := diagnosticsErrorsResult(e.App, q)
				if err != nil {
					if _, ok := err.(*mcpToolError); ok {
						return nil, err
					}
					return nil, mcpToolErr(err.Error())
				}
				return result, nil
			},
		},
		{
			Name:        "diagnostics_log_files",
			Description: "Superusers: list the on disk error log files ({dataDir}/logs/errors-YYYY-MM-DD.jsonl) or read the tail of one of them.",
			InputSchema: obj(map[string]any{
				"file":   str("File name to read (lists the files when empty)."),
				"tail":   map[string]any{"type": "integer", "description": "Number of last lines to return (default 200)."},
				"search": str("Only lines containing this text."),
			}),
			SuperuserOnly: true,
			ReadOnly:      true,
			NoScopeArgs:   true,
			Handler: func(e *core.RequestEvent, args json.RawMessage) (any, error) {
				payload := struct {
					File   string `json:"file"`
					Tail   int    `json:"tail"`
					Search string `json:"search"`
				}{}
				if err := mcpBindArgs(args, &payload); err != nil {
					return nil, err
				}
				if payload.File == "" {
					return map[string]any{
						"dir":   filepath.Join(e.App.DataDir(), core.ErrorLogsDirName),
						"files": diagnosticsListErrorFiles(e.App),
					}, nil
				}
				return diagnosticsReadFile(e.App, payload.File, payload.Tail, payload.Search)
			},
		},
		{
			Name:        "diagnostics_report",
			Description: "Superusers: generate a markdown error report (health, top error groups, latest server errors) and save it under {dataDir}/logs/reports so it can be attached to a bug report.",
			InputSchema: obj(map[string]any{
				"since": str("Period, eg. \"24h\" (default) or \"7d\" style dates via YYYY-MM-DD."),
				"save":  map[string]any{"type": "boolean", "description": "Save the report to disk (default true)."},
			}),
			SuperuserOnly: true,
			NoScopeArgs:   true,
			Handler: func(e *core.RequestEvent, args json.RawMessage) (any, error) {
				payload := struct {
					Since string `json:"since"`
					Save  *bool  `json:"save"`
				}{}
				if err := mcpBindArgs(args, &payload); err != nil {
					return nil, err
				}
				result, err := diagnosticsReport(e.App, payload.Since, payload.Save == nil || *payload.Save)
				if err != nil {
					return nil, mcpToolErr(err.Error())
				}
				return result, nil
			},
		},
	}
}
