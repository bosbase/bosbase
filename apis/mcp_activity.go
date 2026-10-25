package apis

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
)

// MCP activity journal: every tool call is recorded so that the AI can recall
// what it did in previous sessions (activity_recent) and write explicit journal
// entries (activity_log). The latest activity is also included in the
// initialize instructions so that a new session starts with context.

const (
	mcpActivityTable          = "_mcp_activity"
	mcpActivityMaxStringLen   = 300
	mcpActivityDefaultRetain  = 180 // days
	mcpActivityCleanupChance  = 200 // 1 in N inserts triggers the cleanup
	mcpActivityJournalTool    = "journal"
	mcpActivityInstructionMax = 10
)

var mcpActivityTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	mcpActivityTools = []mcpTool{
		{
			Name:        "activity_recent",
			Description: "Recall what you did before: returns your recent MCP tool calls and journal entries (newest first). Call it at the start of a session to restore context.",
			InputSchema: obj(map[string]any{
				"limit":      map[string]any{"type": "integer", "description": "Max entries (default 30, max 500)."},
				"project":    str("Only the activity of this project."),
				"tool":       str("Only the calls of this tool (\"journal\" for the activity_log entries)."),
				"since":      str("Only entries after this time (RFC3339 or YYYY-MM-DD)."),
				"until":      str("Only entries before this time (RFC3339 or YYYY-MM-DD)."),
				"search":     str("Text searched in the summary and arguments."),
				"writesOnly": map[string]any{"type": "boolean", "description": "Exclude read-only calls. Defaults to false."},
			}),
			ReadOnly: true,
			NoLog:    true,
			Handler:  mcpActivityRecent,
		},
		{
			Name:        "activity_log",
			Description: "Write an explicit journal entry about what you did, decided or plan to do next (eg. at the end of a task), so that future sessions can recall it.",
			InputSchema: obj(map[string]any{
				"summary": str("Short summary of what was done."),
				"details": str("Optional details (decisions, next steps, links to notes...)."),
				"project": str("Optional project."),
			}, "summary"),
			NoLog:   true,
			Handler: mcpActivityLog,
		},
	}
}

// mcpActivityOwner returns the owner id used for the activity of the request.
func mcpActivityOwner(e *core.RequestEvent, args json.RawMessage) string {
	if e.HasSuperuserAuth() {
		sa := mcpScopeArgs{}
		_ = json.Unmarshal(args, &sa)
		if sa.OwnerId != nil && *sa.OwnerId != "" {
			return *sa.OwnerId
		}
	}
	return e.Auth.Id
}

// mcpRecordActivity stores a tool call in the activity journal.
func mcpRecordActivity(e *core.RequestEvent, tool *mcpTool, args json.RawMessage, out any, callErr error, duration time.Duration) {
	if tool.NoLog || e.Auth == nil {
		return
	}

	var argsMap map[string]any
	_ = json.Unmarshal(args, &argsMap)
	namespace, _ := argsMap["namespace"].(string)
	sanitized := mcpSanitizeActivityValue("", argsMap)
	argsJSON, _ := json.Marshal(sanitized)

	summary := ""
	success := callErr == nil
	if callErr != nil {
		summary = mcpTruncate(callErr.Error(), mcpActivityMaxStringLen)
	} else {
		summary = mcpActivitySummary(out)
	}

	readOnly := tool.ReadOnly
	if tool.Name == "api_request" {
		method, _ := argsMap["method"].(string)
		readOnly = strings.EqualFold(method, "GET")
		if m, ok := out.(map[string]any); ok {
			if ok, _ := m["ok"].(bool); !ok {
				success = false
			}
		}
	}

	_, err := e.App.DB().Insert(mcpActivityTable, dbx.Params{
		"owner":           mcpActivityOwner(e, args),
		"ownerCollection": e.Auth.Collection().Name,
		"tool":            tool.Name,
		"namespace":       namespace,
		"arguments":       string(argsJSON),
		"success":         success,
		"summary":         summary,
		"durationMs":      duration.Milliseconds(),
		"client":          mcpTruncate(e.Request.Header.Get("User-Agent"), 200),
		"readOnly":        readOnly,
	}).Execute()
	if err != nil {
		e.App.Logger().Warn("MCP activity log failure", "error", err)
		return
	}

	if rand.IntN(mcpActivityCleanupChance) == 0 {
		mcpCleanupActivity(e.App)
	}
}

func mcpCleanupActivity(app core.App) {
	days := mcpActivityDefaultRetain
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MCP_ACTIVITY_RETENTION_DAYS"))); err == nil && v > 0 {
		days = v
	}
	_, err := app.DB().NewQuery(fmt.Sprintf(
		"DELETE FROM {{%s}} WHERE [[created]] < NOW() - ({:days} || ' days')::interval", mcpActivityTable,
	)).Bind(dbx.Params{"days": strconv.Itoa(days)}).Execute()
	if err != nil {
		app.Logger().Warn("MCP activity cleanup failure", "error", err)
	}
}

func mcpTruncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + fmt.Sprintf("…(%d chars)", len(r))
}

var mcpSensitiveKeys = []string{"password", "token", "secret", "apikey", "api_key", "authorization", "credential", "accesskey", "privatekey"}

// mcpSanitizeActivityValue redacts secrets and truncates large values before logging.
func mcpSanitizeActivityValue(key string, v any) any {
	lowerKey := strings.ToLower(key)
	for _, s := range mcpSensitiveKeys {
		if strings.Contains(lowerKey, s) {
			return "***"
		}
	}

	switch vv := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(vv))
		for k, item := range vv {
			result[k] = mcpSanitizeActivityValue(k, item)
		}
		return result
	case []any:
		if lowerKey == "embedding" || lowerKey == "vector" || lowerKey == "queryvector" {
			return fmt.Sprintf("<vector %d>", len(vv))
		}
		limit := min(len(vv), 20)
		result := make([]any, 0, limit+1)
		for _, item := range vv[:limit] {
			result = append(result, mcpSanitizeActivityValue("", item))
		}
		if len(vv) > limit {
			result = append(result, fmt.Sprintf("…(%d items)", len(vv)))
		}
		return result
	case string:
		if lowerKey == "contentbase64" || lowerKey == "base64" {
			return fmt.Sprintf("<%d base64 chars>", len(vv))
		}
		return mcpTruncate(vv, mcpActivityMaxStringLen)
	default:
		return v
	}
}

// mcpActivitySummary extracts a short human readable summary from a tool result.
func mcpActivitySummary(out any) string {
	m, ok := out.(map[string]any)
	if !ok {
		return ""
	}

	parts := []string{}
	for _, key := range []string{"status", "id", "path", "title", "project", "count", "total", "deleted", "deletedMemories", "found", "mode"} {
		v, ok := m[key]
		if !ok || v == nil || v == "" {
			continue
		}
		switch vv := v.(type) {
		case string:
			parts = append(parts, key+"="+mcpTruncate(vv, 80))
		case json.RawMessage:
			continue
		default:
			raw, _ := json.Marshal(vv)
			parts = append(parts, key+"="+mcpTruncate(string(raw), 80))
		}
	}
	return strings.Join(parts, " ")
}

type mcpActivityRow struct {
	Id         string `db:"id" json:"id"`
	Tool       string `db:"tool" json:"tool"`
	Namespace  string `db:"namespace" json:"project,omitempty"`
	Arguments  string `db:"arguments" json:"-"`
	Success    bool   `db:"success" json:"success"`
	ReadOnly   bool   `db:"readOnly" json:"readOnly"`
	Summary    string `db:"summary" json:"summary,omitempty"`
	DurationMs int    `db:"durationMs" json:"durationMs"`
	Client     string `db:"client" json:"client,omitempty"`
	Created    string `db:"created" json:"created"`
}

func (r *mcpActivityRow) toMap() map[string]any {
	raw, _ := json.Marshal(r)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	args := map[string]any{}
	_ = json.Unmarshal([]byte(r.Arguments), &args)
	m["arguments"] = args
	return m
}

func mcpParseTimeArg(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	return time.Time{}, mcpToolErr("invalid time " + strconv.Quote(v) + " (expected RFC3339 or YYYY-MM-DD).")
}

func mcpLoadActivity(app core.App, owner string, filters []string, params dbx.Params, limit int) ([]*mcpActivityRow, error) {
	params["owner"] = owner
	params["limit"] = limit
	where := append([]string{"[[owner]] = {:owner}"}, filters...)

	rows := []*mcpActivityRow{}
	err := app.DB().NewQuery(fmt.Sprintf(`
		SELECT [[id]], [[tool]], [[namespace]], [[arguments]]::text AS [[arguments]], [[success]], [[readOnly]],
			[[summary]], [[durationMs]], [[client]], [[created]]::text AS [[created]]
		FROM {{%s}} WHERE %s ORDER BY [[created]] DESC, [[id]] DESC LIMIT {:limit}`,
		mcpActivityTable, strings.Join(where, " AND "))).Bind(params).All(&rows)
	return rows, err
}

func mcpActivityRecent(e *core.RequestEvent, args json.RawMessage) (any, error) {
	if _, err := mcpResolveScope(e, args); err != nil {
		return nil, err
	}

	payload := struct {
		Limit      int    `json:"limit"`
		Namespace  string `json:"namespace"`
		Tool       string `json:"tool"`
		Since      string `json:"since"`
		Until      string `json:"until"`
		Search     string `json:"search"`
		WritesOnly bool   `json:"writesOnly"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	params := dbx.Params{}
	filters := []string{}
	if ns := strings.TrimSpace(payload.Namespace); ns != "" {
		params["namespace"] = ns
		filters = append(filters, "[[namespace]] = {:namespace}")
	}
	if tool := strings.TrimSpace(payload.Tool); tool != "" {
		params["tool"] = tool
		filters = append(filters, "[[tool]] = {:tool}")
	}
	if payload.Since != "" {
		t, err := mcpParseTimeArg(payload.Since)
		if err != nil {
			return nil, err
		}
		params["since"] = t
		filters = append(filters, "[[created]] >= {:since}")
	}
	if payload.Until != "" {
		t, err := mcpParseTimeArg(payload.Until)
		if err != nil {
			return nil, err
		}
		params["until"] = t
		filters = append(filters, "[[created]] < {:until}")
	}
	if s := strings.TrimSpace(payload.Search); s != "" {
		params["search"] = "%" + escapeLike(s) + "%"
		filters = append(filters, "([[summary]] ILIKE {:search} OR [[arguments]]::text ILIKE {:search})")
	}
	if payload.WritesOnly {
		filters = append(filters, "[[readOnly]] = FALSE")
	}

	rows, err := mcpLoadActivity(e.App, mcpActivityOwner(e, args), filters, params, clampInt(payload.Limit, 30, 500))
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, len(rows))
	for i, r := range rows {
		items[i] = r.toMap()
	}

	return map[string]any{"count": len(items), "activity": items}, nil
}

func mcpActivityLog(e *core.RequestEvent, args json.RawMessage) (any, error) {
	if _, err := mcpResolveScope(e, args); err != nil {
		return nil, err
	}

	payload := struct {
		Summary   string `json:"summary"`
		Details   string `json:"details"`
		Namespace string `json:"namespace"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(payload.Summary)
	if summary == "" {
		return nil, mcpToolErr("summary is required.")
	}

	argsJSON, _ := json.Marshal(map[string]any{"details": mcpTruncate(strings.TrimSpace(payload.Details), 10000)})

	var id string
	err := e.App.DB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} ([[owner]], [[ownerCollection]], [[tool]], [[namespace]], [[arguments]], [[summary]], [[client]], [[readOnly]])
		VALUES ({:owner}, {:ownerCollection}, {:tool}, {:namespace}, {:arguments}::jsonb, {:summary}, {:client}, FALSE)
		RETURNING [[id]]`, mcpActivityTable)).Bind(dbx.Params{
		"owner":           mcpActivityOwner(e, args),
		"ownerCollection": e.Auth.Collection().Name,
		"tool":            mcpActivityJournalTool,
		"namespace":       strings.TrimSpace(payload.Namespace),
		"arguments":       string(argsJSON),
		"summary":         mcpTruncate(summary, 2000),
		"client":          mcpTruncate(e.Request.Header.Get("User-Agent"), 200),
	}).Row(&id)
	if err != nil {
		return nil, err
	}

	return map[string]any{"id": id, "logged": true}, nil
}

// mcpActivityInstructions returns a short digest of the latest activity
// that is appended to the initialize instructions.
func mcpActivityInstructions(e *core.RequestEvent) string {
	if e.Auth == nil {
		return ""
	}

	rows, err := mcpLoadActivity(e.App, e.Auth.Id, []string{"[[readOnly]] = FALSE"}, dbx.Params{}, mcpActivityInstructionMax)
	if err != nil || len(rows) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\nYour latest recorded actions (newest first, use activity_recent for more):")
	for _, r := range rows {
		status := "ok"
		if !r.Success {
			status = "failed"
		}
		line := fmt.Sprintf("\n- %s %s", mcpShortTime(r.Created), r.Tool)
		if r.Namespace != "" {
			line += " [" + r.Namespace + "]"
		}
		if r.Summary != "" {
			line += ": " + mcpTruncate(r.Summary, 160)
		}
		line += " (" + status + ")"
		b.WriteString(line)
	}
	return b.String()
}

func mcpShortTime(pgTime string) string {
	if len(pgTime) >= 16 {
		return pgTime[:16]
	}
	return pgTime
}
