package apis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
)

// Memory context: helps the AI to actually use its memory across conversations.
//
//   - pinned (core) memories, the last session summary and the open todos are
//     given to the AI in the initialize instructions of every MCP session
//   - memory_context returns a briefing for the current task
//   - memory_remember saves the context of a conversation (summary, facts,
//     decisions, todos) and updates similar memories instead of duplicating them
//   - memory_extract lets the server extract the memories from a conversation
//     text with a configured LLM channel
//   - the "recall"/"remember" prompts and the bosbase://memory/* resources expose
//     the same features to the clients supporting MCP prompts and resources
//
// Everything is scoped to the private space of the authenticated user.

const (
	mcpDedupeThreshold       = 0.9
	mcpSessionTag            = "session"
	mcpTodoTag               = "todo"
	mcpDoneTag               = "done"
	mcpDecisionTag           = "decision"
	mcpContextDefaultChars   = 8000
	mcpContextMaxChars       = 50000
	mcpInstructionsMaxChars  = 3000
	mcpExtractMaxInputChars  = 60000
	mcpExtractDefaultFacts   = 20
	mcpContextPinnedLimit    = 20
	mcpContextSessionsLimit  = 2
	mcpContextTodosLimit     = 10
	mcpContextRecentLimit    = 5
	mcpContextRelevantLimit  = 8
	mcpContextActivityLimit  = 5
	mcpContextItemMaxChars   = 400
	mcpContextSessionMaxChar = 1200
)

var mcpContextTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	strArr := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	factItems := map[string]any{
		"description": "Facts to remember: strings or {title, content, tags, pinned} objects. Each fact should be atomic and self-contained.",
		"type":        "array",
		"items": map[string]any{
			"anyOf": []any{
				map[string]any{"type": "string"},
				obj(map[string]any{
					"title":   str("Short title."),
					"content": str("The fact."),
					"tags":    strArr("Tags."),
					"pinned":  boolean("Pin it as a core memory (stable user preferences/identity, key project facts)."),
				}, "content"),
			},
		},
	}

	mcpContextTools = []mcpTool{
		{
			Name: "memory_context",
			Description: "Recall the context relevant to the current conversation in one call: pinned core memories, the memories most relevant to the query, " +
				"the latest session summaries, open todos, recently updated memories and your latest actions. Call it at the start of a conversation " +
				"(with the user's request as query) and whenever you switch task or project.",
			InputSchema: obj(map[string]any{
				"query":         str("What the conversation is about (eg. the user's request). Used to find the relevant memories."),
				"project":       str("Focus on a project (its sessions, todos and memories)."),
				"limit":         integer("Max relevant memories (default 8, max 50)."),
				"maxChars":      integer("Size budget of the briefing (default 8000, max 50000)."),
				"includeShared": boolean("Also search the public knowledge bases for relevant notes (default true)."),
			}),
			ReadOnly: true,
			Handler:  mcpMemoryContext,
		},
		{
			Name: "memory_remember",
			Description: "Save the context of the current conversation for future sessions: a session summary plus the durable facts, decisions and todos. " +
				"Facts similar to existing memories update them instead of creating duplicates. Call it at the end of a task or conversation " +
				"(and before the context gets long). Never store secrets (passwords, API keys, tokens).",
			InputSchema: obj(map[string]any{
				"project":   str("Project of the conversation (default project when omitted)."),
				"title":     str("Short title of the session."),
				"summary":   str("What happened in the conversation: goal, what was done, outcome, open questions."),
				"facts":     factItems,
				"decisions": strArr("Decisions taken (stored as memories tagged \"decision\")."),
				"todos":     strArr("Open follow-ups (stored as memories tagged \"todo\"; mark them done with memory_update adding the \"done\" tag or delete them)."),
				"tags":      strArr("Tags added to all the saved memories."),
				"dedupe":    boolean("Update similar existing memories instead of creating duplicates (default true)."),
			}),
			Handler: mcpMemoryRemember,
		},
		{
			Name: "memory_extract",
			Description: "Let the server extract the memories worth keeping (summary, atomic facts, decisions, todos) from a conversation transcript or any text " +
				"using a configured LLM channel, and save them like memory_remember (with deduplication). Requires an LLM channel (llm_channel).",
			InputSchema: obj(map[string]any{
				"text":         str("Conversation transcript or text to extract memories from."),
				"project":      str("Project of the memories."),
				"channel":      str("LLM channel id or name (default channel when empty)."),
				"model":        str("Model (the channel default model when empty)."),
				"save":         boolean("Save the extracted memories (default true). false only returns them."),
				"maxFacts":     integer("Max facts to extract (default 20, max 50)."),
				"instructions": str("Extra extraction instructions (eg. what to focus on)."),
				"tags":         strArr("Tags added to all the saved memories."),
			}, "text"),
			Handler: mcpMemoryExtract,
		},
	}
}

// -------------------------------------------------------------------
// Context
// -------------------------------------------------------------------

type mcpContextOptions struct {
	Query         string
	Namespace     string
	Limit         int
	MaxChars      int
	IncludeShared *bool
	// for the initialize instructions
	SkipActivity bool
	SkipRecent   bool
}

func mcpContextItem(r *mcpMemoryRow, maxChars int) map[string]any {
	item := map[string]any{
		"id":      r.Id,
		"title":   r.Title,
		"project": r.Namespace,
		"tags":    r.tags(),
		"content": mcpTruncate(r.Content, maxChars),
		"updated": r.Updated,
	}
	if r.Pinned {
		item["pinned"] = true
	}
	if r.Path != "" {
		item["path"] = r.Path
	}
	if r.Owner == "" {
		item["shared"] = true
	}
	return item
}

func mcpLoadContextRows(app core.App, owner string, where string, params dbx.Params, order string, limit int) ([]*mcpMemoryRow, error) {
	params["ctxOwner"] = owner
	params["ctxLimit"] = limit
	rows := []*mcpMemoryRow{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[owner]] = {:ctxOwner} AND %s ORDER BY %s LIMIT {:ctxLimit}",
		mcpMemoryColumns, mcpMemoriesTable, where, order,
	)).Bind(params).All(&rows)
	return rows, err
}

// mcpBuildContext builds the memory briefing of the scope write owner.
func mcpBuildContext(e *core.RequestEvent, scope *mcpScope, args json.RawMessage, opts mcpContextOptions) (map[string]any, error) {
	owner := scope.writeOwner
	ns := strings.TrimSpace(opts.Namespace)
	maxChars := clampInt(opts.MaxChars, mcpContextDefaultChars, mcpContextMaxChars)
	seen := map[string]bool{}

	nsFilter := "TRUE"
	params := func() dbx.Params {
		p := dbx.Params{}
		if ns != "" {
			p["ctxNs"] = ns
		}
		return p
	}
	if ns != "" {
		nsFilter = "[[namespace]] = {:ctxNs}"
	}
	tagParam := func(p dbx.Params, key, tag string) string {
		raw, _ := json.Marshal([]string{tag})
		p[key] = string(raw)
		return "[[tags]] @> {:" + key + "}::jsonb"
	}

	// pinned (project matching first)
	pinnedParams := params()
	pinnedOrder := "[[updated]] DESC"
	if ns != "" {
		pinnedOrder = "([[namespace]] = {:ctxNs}) DESC, [[updated]] DESC"
	}
	pinnedRows, err := mcpLoadContextRows(e.App, owner, "[[pinned]] = TRUE", pinnedParams, pinnedOrder, mcpContextPinnedLimit)
	if err != nil {
		return nil, err
	}

	// latest session summaries
	sessionParams := params()
	sessionRows, err := mcpLoadContextRows(e.App, owner, nsFilter+" AND "+tagParam(sessionParams, "ctxSession", mcpSessionTag), sessionParams, "[[created]] DESC", mcpContextSessionsLimit)
	if err != nil {
		return nil, err
	}

	// open todos
	todoParams := params()
	todoWhere := nsFilter + " AND " + tagParam(todoParams, "ctxTodo", mcpTodoTag) + " AND NOT " + tagParam(todoParams, "ctxDone", mcpDoneTag)
	todoRows, err := mcpLoadContextRows(e.App, owner, todoWhere, todoParams, "[[created]] DESC", mcpContextTodosLimit)
	if err != nil {
		return nil, err
	}

	// long-term project summaries (the focused project, or the most recent ones)
	summaryParams := params()
	summaryLimit := 3
	if ns != "" {
		summaryLimit = 1
	}
	summaryRows, err := mcpLoadContextRows(e.App, owner, nsFilter+" AND "+tagParam(summaryParams, "ctxSummary", mcpProjectSummaryTag), summaryParams, "[[updated]] DESC", summaryLimit)
	if err != nil {
		return nil, err
	}

	for _, rows := range [][]*mcpMemoryRow{pinnedRows, summaryRows, sessionRows, todoRows} {
		for _, r := range rows {
			seen[r.Id] = true
		}
	}

	// relevant memories (semantic + keyword search)
	relevant := []map[string]any{}
	searchMode := ""
	if q := strings.TrimSpace(opts.Query); q != "" {
		searchArgs := map[string]any{}
		_ = json.Unmarshal(args, &searchArgs)
		for _, k := range []string{"query", "limit", "maxChars", "project", "namespace", "includeShared"} {
			delete(searchArgs, k)
		}
		searchArgs["query"] = q
		searchArgs["limit"] = clampInt(opts.Limit, mcpContextRelevantLimit, 50) + len(seen)
		if ns != "" {
			searchArgs["namespace"] = ns
		}
		if opts.IncludeShared != nil {
			searchArgs["includeShared"] = *opts.IncludeShared
		}
		rawArgs, _ := json.Marshal(searchArgs)
		out, err := mcpMemorySearch(e, rawArgs)
		if err != nil {
			return nil, err
		}
		if res, ok := out.(map[string]any); ok {
			searchMode, _ = res["mode"].(string)
			items, _ := res["results"].([]map[string]any)
			for _, item := range items {
				id, _ := item["id"].(string)
				if seen[id] || len(relevant) >= clampInt(opts.Limit, mcpContextRelevantLimit, 50) {
					continue
				}
				seen[id] = true
				if content, ok := item["content"].(string); ok {
					item["content"] = mcpTruncate(content, mcpContextItemMaxChars)
				}
				for _, k := range []string{"metadata", "owner", "created", "hasEmbedding", "links", "properties"} {
					delete(item, k)
				}
				relevant = append(relevant, item)
			}
		}
	}

	// knowledge associated with the relevant memories through the graph
	associated := []*mcpAssocNode{}
	assocTitles := map[string]string{}
	if len(relevant) > 0 {
		seedIds := []string{}
		for _, item := range relevant {
			if id, _ := item["id"].(string); id != "" && len(seedIds) < 5 {
				seedIds = append(seedIds, id)
				title, _ := item["title"].(string)
				assocTitles[id] = title
			}
		}
		includeShared := opts.IncludeShared == nil || *opts.IncludeShared
		_, nodes, _, err := mcpAssociate(e, scope.readOwners(includeShared), mcpAssocOptions{
			Ids: seedIds, Namespace: ns, Depth: 1, Limit: 4 + len(seen), Semantic: false, Tags: true,
		})
		if err == nil {
			for _, n := range nodes {
				if seen[n.row.Id] || len(associated) >= 4 {
					continue
				}
				seen[n.row.Id] = true
				associated = append(associated, n)
			}
		}
	}

	// relevant turns of past conversations
	conversations := []map[string]any{}
	if q := strings.TrimSpace(opts.Query); q != "" && owner != "" {
		hits, _, _, err := mcpRecallConversations(e, owner, mcpRecallOptions{Query: q, Namespace: ns, Limit: 3, Window: 1})
		if err == nil {
			for _, h := range hits {
				if score, _ := h["score"].(float64); score < 0.3 {
					continue // weak semantic matches only add noise
				}
				conversations = append(conversations, h)
			}
		}
	}

	// recently updated memories
	recentRows := []*mcpMemoryRow{}
	if !opts.SkipRecent {
		recentParams := params()
		rows, err := mcpLoadContextRows(e.App, owner, nsFilter+" AND NOT "+tagParam(recentParams, "ctxRecentSession", mcpSessionTag)+" AND NOT "+tagParam(recentParams, "ctxRecentSummary", mcpProjectSummaryTag), recentParams, "[[updated]] DESC", mcpContextRecentLimit+len(seen))
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !seen[r.Id] && len(recentRows) < mcpContextRecentLimit {
				seen[r.Id] = true
				recentRows = append(recentRows, r)
			}
		}
	}

	// latest actions
	activity := []*mcpActivityRow{}
	if !opts.SkipActivity && owner != "" {
		activity, _ = mcpLoadActivity(e.App, owner, []string{"[[readOnly]] = FALSE"}, dbx.Params{}, mcpContextActivityLimit)
	}

	// briefing
	b := &mcpBriefing{max: maxChars}
	toItems := func(rows []*mcpMemoryRow, maxItemChars int) []map[string]any {
		items := make([]map[string]any, len(rows))
		for i, r := range rows {
			items[i] = mcpContextItem(r, maxItemChars)
		}
		return items
	}

	if len(pinnedRows) > 0 {
		b.section("Core memories (pinned)")
		for _, r := range pinnedRows {
			b.line(mcpBriefingLine(r.Title, r.Content, r.Namespace, mcpContextItemMaxChars))
		}
	}
	if len(summaryRows) > 0 {
		b.section("Project summaries (long-term)")
		for _, r := range summaryRows {
			b.line("- " + r.Namespace + ":\n  " + strings.ReplaceAll(mcpTruncate(mcpStripHeadingOnly(r.Content), 1500), "\n", "\n  "))
		}
	}
	if len(sessionRows) > 0 {
		b.section("Latest sessions")
		for _, r := range sessionRows {
			b.line(fmt.Sprintf("- %s [%s, %s]\n  %s", r.Title, r.Namespace, mcpShortTime(r.Created),
				strings.ReplaceAll(mcpTruncate(mcpStripSessionHeading(r.Content), mcpContextSessionMaxChar), "\n", "\n  ")))
		}
	}
	if len(todoRows) > 0 {
		b.section("Open todos")
		for _, r := range todoRows {
			b.line(mcpBriefingLine(r.Title, r.Content, r.Namespace, 200) + " (id " + r.Id + ")")
		}
	}
	if len(relevant) > 0 {
		b.section("Relevant memories")
		for _, item := range relevant {
			title, _ := item["title"].(string)
			content, _ := item["content"].(string)
			project, _ := item["project"].(string)
			line := mcpBriefingLine(title, content, project, mcpContextItemMaxChars)
			if shared, _ := item["shared"].(bool); shared {
				line += " (public knowledge base)"
			} else if spaceName, _ := item["spaceName"].(string); spaceName != "" {
				line += " (space " + spaceName
				if author, _ := item["authorName"].(string); author != "" {
					line += ", by " + author
				}
				line += ")"
			}
			b.line(line)
		}
	}
	if len(associated) > 0 {
		b.section("Related through the knowledge graph")
		for _, n := range associated {
			line := mcpBriefingLine(n.row.Title, n.row.Content, n.row.Namespace, 250)
			if t := assocTitles[n.via]; t != "" {
				line += " (" + n.reason + " " + t + ")"
			}
			b.line(line)
		}
	}
	if len(conversations) > 0 {
		b.section("From past conversations")
		for _, h := range conversations {
			title, _ := h["conversationTitle"].(string)
			created, _ := h["created"].(string)
			line := "- [" + title + ", " + mcpShortTime(created) + "]"
			if turns, ok := h["context"].([]map[string]any); ok && len(turns) > 0 {
				for _, t := range turns {
					role, _ := t["role"].(string)
					content, _ := t["content"].(string)
					line += "\n  " + role + ": " + mcpTruncate(strings.Join(strings.Fields(content), " "), 300)
				}
			} else {
				role, _ := h["role"].(string)
				content, _ := h["content"].(string)
				line += " " + role + ": " + mcpTruncate(strings.Join(strings.Fields(content), " "), 300)
			}
			b.line(line)
		}
	}
	if len(recentRows) > 0 {
		b.section("Recently updated")
		for _, r := range recentRows {
			b.line(mcpBriefingLine(r.Title, r.Content, r.Namespace, 200))
		}
	}
	if len(activity) > 0 {
		b.section("Your latest actions")
		for _, a := range activity {
			line := "- " + mcpShortTime(a.Created) + " " + a.Tool
			if a.Namespace != "" {
				line += " [" + a.Namespace + "]"
			}
			if a.Summary != "" {
				line += ": " + mcpTruncate(a.Summary, 160)
			}
			b.line(line)
		}
	}

	activityItems := make([]map[string]any, len(activity))
	for i, a := range activity {
		activityItems[i] = a.toMap()
	}

	result := map[string]any{
		"briefing":         b.String(),
		"truncated":        b.truncated,
		"pinned":           toItems(pinnedRows, mcpContextItemMaxChars),
		"projectSummaries": toItems(summaryRows, 3000),
		"conversations":    conversations,
		"associated":       mcpAssocItems(associated, assocTitles),
		"sessions":         toItems(sessionRows, mcpContextSessionMaxChar),
		"todos":            toItems(todoRows, mcpContextItemMaxChars),
		"relevant":         relevant,
		"recent":           toItems(recentRows, mcpContextItemMaxChars),
		"activity":         activityItems,
	}
	if searchMode != "" {
		result["searchMode"] = searchMode
	}
	if b.empty() {
		result["briefing"] = "No memories yet. Store what matters with memory_store (pin stable preferences with pinned: true) and save each conversation with memory_remember."
	}

	// usage signals of the memories given to the AI
	ids := []string{}
	for id := range seen {
		ids = append(ids, id)
	}
	mcpTouchMemories(e.App, owner, ids)

	return result, nil
}

// mcpStripHeadingOnly returns the content without its leading "# " heading.
func mcpStripHeadingOnly(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "# ") {
		if i := strings.Index(content, "\n"); i > 0 {
			return strings.TrimSpace(content[i+1:])
		}
		return ""
	}
	return content
}

// mcpStripSessionHeading returns the summary of a session memory without its
// heading and its decisions/todos/facts sections (available as separate memories).
func mcpStripSessionHeading(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "# ") {
		if i := strings.Index(content, "\n"); i > 0 {
			content = strings.TrimSpace(content[i+1:])
		} else {
			return ""
		}
	}
	if i := strings.Index(content, "\n## "); i >= 0 {
		content = strings.TrimSpace(content[:i])
	}
	return content
}

func mcpBriefingLine(title, content, project string, maxChars int) string {
	content = strings.Join(strings.Fields(content), " ")
	line := "- "
	if title != "" && !strings.HasPrefix(content, title) {
		line += title + ": "
	}
	line += mcpTruncate(content, maxChars)
	if project != "" && project != mcpDefaultNamespace {
		line += " [" + project + "]"
	}
	return line
}

type mcpBriefing struct {
	b         strings.Builder
	max       int
	truncated bool
	lines     int
}

func (m *mcpBriefing) write(s string) bool {
	if m.truncated {
		return false
	}
	if m.b.Len()+len(s) > m.max {
		m.truncated = true
		return false
	}
	m.b.WriteString(s)
	return true
}

func (m *mcpBriefing) section(title string) {
	prefix := "\n\n"
	if m.b.Len() == 0 {
		prefix = ""
	}
	m.write(prefix + "## " + title)
}

func (m *mcpBriefing) line(s string) {
	if m.write("\n" + s) {
		m.lines++
	}
}

func (m *mcpBriefing) empty() bool { return m.lines == 0 }

func (m *mcpBriefing) String() string { return m.b.String() }

// mcpTouchMemories records that the memories were used (owned rows only).
func mcpTouchMemories(app core.App, owner string, ids []string) {
	if owner == "" || len(ids) == 0 {
		return
	}
	params := dbx.Params{"owner": owner}
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		key := "id" + strconv.Itoa(i)
		params[key] = id
		placeholders[i] = "{:" + key + "}"
	}
	_, _ = app.DB().NewQuery(fmt.Sprintf(
		"UPDATE {{%s}} SET [[accessCount]] = [[accessCount]] + 1, [[lastAccessedAt]] = NOW() WHERE [[owner]] = {:owner} AND [[id]] IN (%s)",
		mcpMemoriesTable, strings.Join(placeholders, ","),
	)).Bind(params).Execute()
}

func mcpMemoryContext(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	if scope.writeOwner == "" {
		return nil, mcpToolErr("memory_context works on a private memory space (memories are always private).")
	}

	payload := struct {
		Query         string `json:"query"`
		Namespace     string `json:"namespace"`
		Limit         int    `json:"limit"`
		MaxChars      int    `json:"maxChars"`
		IncludeShared *bool  `json:"includeShared"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	return mcpBuildContext(e, scope, args, mcpContextOptions{
		Query:         payload.Query,
		Namespace:     payload.Namespace,
		Limit:         payload.Limit,
		MaxChars:      payload.MaxChars,
		IncludeShared: payload.IncludeShared,
	})
}

// mcpContextInstructions returns the memory context appended to the initialize instructions.
func mcpContextInstructions(e *core.RequestEvent) string {
	if e.Auth == nil {
		return ""
	}
	scope := &mcpScope{writeOwner: e.Auth.Id, ownerCollection: e.Auth.Collection().Name}
	ctx, err := mcpBuildContext(e, scope, nil, mcpContextOptions{
		MaxChars:     mcpInstructionsMaxChars,
		SkipActivity: true, // already listed by mcpActivityInstructions
		SkipRecent:   true,
	})
	if err != nil {
		return ""
	}
	briefing, _ := ctx["briefing"].(string)
	pinned, _ := ctx["pinned"].([]map[string]any)
	sessions, _ := ctx["sessions"].([]map[string]any)
	todos, _ := ctx["todos"].([]map[string]any)
	summaries, _ := ctx["projectSummaries"].([]map[string]any)
	if len(pinned)+len(sessions)+len(todos)+len(summaries) == 0 {
		return "\n\nYour memory is empty for now: store durable facts with memory_store and save each conversation with memory_remember."
	}
	return "\n\nWhat you remember about this user (call memory_context with the user's request for the relevant details):\n" + briefing
}

// -------------------------------------------------------------------
// Remember
// -------------------------------------------------------------------

type mcpFactInput struct {
	Title    string         `json:"title"`
	Content  string         `json:"content"`
	Tags     []string       `json:"tags"`
	Pinned   *bool          `json:"pinned"`
	Metadata map[string]any `json:"metadata"`
}

func (f *mcpFactInput) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		f.Content = s
		return nil
	}
	type alias mcpFactInput
	return json.Unmarshal(b, (*alias)(f))
}

func mcpNormalizeFactText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// mcpFindDuplicate finds an existing private memory (not a note or session) of the
// project that is equal or semantically very similar to the provided text.
//
// It also returns the computed embedding so that it can be reused when saving.
func mcpFindDuplicate(e *core.RequestEvent, scope *mcpScope, namespace, text string, threshold float64) (*mcpMemoryRow, float64, []float64, string, error) {
	sessionTag, _ := json.Marshal([]string{mcpSessionTag})
	base := "[[owner]] = {:owner} AND [[namespace]] = {:namespace} AND [[path]] = '' AND NOT [[tags]] @> {:sessionTag}::jsonb"
	params := dbx.Params{"owner": scope.writeOwner, "namespace": namespace, "sessionTag": string(sessionTag), "text": mcpNormalizeFactText(text)}

	// exact match
	rows := []*mcpMemoryRow{}
	err := e.App.DB().NewQuery(fmt.Sprintf(
		`SELECT %s, 1.0 AS [[score]] FROM {{%s}} WHERE %s
		AND LOWER(regexp_replace(TRIM([[title]] || ' ' || [[content]]), '\s+', ' ', 'g')) = {:text}
		OR (%s AND LOWER(regexp_replace(TRIM([[content]]), '\s+', ' ', 'g')) = {:text})
		LIMIT 1`, mcpMemoryColumns, mcpMemoriesTable, base, base,
	)).Bind(params).All(&rows)
	if err != nil {
		return nil, 0, nil, "", err
	}

	vector, model, embedErr := mcpEmbed(e.Request.Context(), e.App, text, nil)
	if embedErr != nil {
		e.App.Logger().Warn("MCP embedding failure", "error", embedErr)
	}
	if len(rows) > 0 {
		return rows[0], 1, vector, model, nil
	}
	if vector == nil {
		return nil, 0, nil, "", nil
	}

	params["vector"] = formatVectorForPostgres(vector)
	params["model"] = model
	params["dims"] = len(vector)
	err = e.App.DB().NewQuery(fmt.Sprintf(
		`SELECT %s, 1 - ([[embedding]] <=> {:vector}::vector) AS [[score]] FROM {{%s}}
		WHERE %s AND [[embedding]] IS NOT NULL AND [[embeddingModel]] = {:model} AND vector_dims([[embedding]]) = {:dims}
		ORDER BY [[embedding]] <=> {:vector}::vector LIMIT 1`, mcpMemoryColumns, mcpMemoriesTable, base,
	)).Bind(params).All(&rows)
	if err != nil {
		return nil, 0, nil, "", err
	}
	if len(rows) > 0 && rows[0].Score >= threshold {
		return rows[0], rows[0].Score, vector, model, nil
	}
	return nil, 0, vector, model, nil
}

// mcpRememberFact stores a fact or updates the existing similar memory.
func mcpRememberFact(e *core.RequestEvent, scope *mcpScope, namespace string, fact mcpFactInput, threshold float64) (map[string]any, error) {
	content := strings.TrimSpace(fact.Content)
	if content == "" {
		return nil, mcpToolErr("fact content is required.")
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = mcpDefaultNamespace
	}
	title := strings.TrimSpace(fact.Title)
	if title == "" {
		title = mcpParseMarkdown(content).Title
	}
	tags := normalizeTags(fact.Tags)

	var dup *mcpMemoryRow
	var score float64
	var vector []float64
	var model string
	if threshold > 0 {
		var err error
		dup, score, vector, model, err = mcpFindDuplicate(e, scope, namespace, strings.TrimSpace(title+"\n"+content), threshold)
		if err != nil {
			return nil, err
		}
	}

	// in a shared space a member doesn't rewrite the memories of other members
	if dup != nil && !scope.canModify(dup, false) {
		if mcpNormalizeFactText(dup.Content) == mcpNormalizeFactText(content) {
			return map[string]any{"id": dup.Id, "title": dup.Title, "action": "unchanged", "similarity": score, "author": dup.Author}, nil
		}
		dup = nil
	}

	if dup != nil {
		mergedTags := normalizeTags(append(dup.tags(), tags...))
		sameContent := mcpNormalizeFactText(dup.Content) == mcpNormalizeFactText(content)
		sameTags := len(mergedTags) == len(dup.tags())
		samePinned := fact.Pinned == nil || *fact.Pinned == dup.Pinned
		if sameContent && sameTags && samePinned {
			mcpTouchMemories(e.App, scope.writeOwner, []string{dup.Id})
			return map[string]any{"id": dup.Id, "title": dup.Title, "action": "unchanged", "similarity": score, "author": dup.Author}, nil
		}

		in := &mcpSaveInput{Tags: &mergedTags, Pinned: fact.Pinned}
		if !sameContent {
			in.Content = &content
			if fact.Title != "" {
				in.Title = &title
			}
			if vector != nil && model != "" {
				in.Embedding, in.EmbeddingModel = vector, model
			}
		}
		saved, warning, err := mcpSaveMemory(e, scope, dup, in, nil)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"id": saved.Id, "title": saved.Title, "action": "updated", "similarity": score}
		if !sameContent {
			result["previous"] = mcpTruncate(dup.Content, 300)
		}
		if warning != "" {
			result["warning"] = warning
		}
		return result, nil
	}

	metadata := fact.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	in := &mcpSaveInput{
		Namespace: namespace,
		Title:     &title,
		Content:   &content,
		Tags:      &tags,
		Metadata:  &metadata,
		Pinned:    fact.Pinned,
	}
	if vector != nil && model != "" {
		in.Embedding, in.EmbeddingModel = vector, model
	}
	saved, warning, err := mcpSaveMemory(e, scope, nil, in, nil)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"id": saved.Id, "title": saved.Title, "action": "created", "hasEmbedding": saved.HasEmbedding}
	if saved.Pinned {
		result["pinned"] = true
	}
	if warning != "" {
		result["warning"] = warning
	}
	return result, nil
}

type mcpSessionInput struct {
	Namespace string         `json:"namespace"`
	Title     string         `json:"title"`
	Summary   string         `json:"summary"`
	Facts     []mcpFactInput `json:"facts"`
	Decisions []string       `json:"decisions"`
	Todos     []string       `json:"todos"`
	Tags      []string       `json:"tags"`
	Dedupe    *bool          `json:"dedupe"`

	// SessionMemoryId updates this session memory instead of creating a new one
	// (eg. the rolling summary of a conversation).
	SessionMemoryId string `json:"-"`
	// SessionMetadata is stored in the session memory metadata.
	SessionMetadata map[string]any `json:"-"`
}

func mcpRememberSession(e *core.RequestEvent, scope *mcpScope, in *mcpSessionInput) (map[string]any, error) {
	if scope.writeOwner == "" {
		return nil, mcpToolErr("Memories are always private and can't be shared.")
	}
	namespace := strings.TrimSpace(in.Namespace)
	if namespace == "" {
		namespace = mcpDefaultNamespace
	}
	summary := strings.TrimSpace(in.Summary)

	facts := []mcpFactInput{}
	for _, f := range in.Facts {
		if strings.TrimSpace(f.Content) != "" {
			f.Tags = append(append([]string{}, f.Tags...), in.Tags...)
			facts = append(facts, f)
		}
	}
	for _, d := range in.Decisions {
		if d = strings.TrimSpace(d); d != "" {
			facts = append(facts, mcpFactInput{Content: d, Tags: append([]string{mcpDecisionTag}, in.Tags...)})
		}
	}
	for _, t := range in.Todos {
		if t = strings.TrimSpace(t); t != "" {
			facts = append(facts, mcpFactInput{Content: t, Tags: append([]string{mcpTodoTag}, in.Tags...)})
		}
	}
	if summary == "" && len(facts) == 0 {
		return nil, mcpToolErr("Nothing to remember: provide a summary, facts, decisions or todos.")
	}
	if len(facts) > 100 {
		return nil, mcpToolErr("Too many facts (max 100).")
	}

	threshold := mcpDedupeThreshold
	if in.Dedupe != nil && !*in.Dedupe {
		threshold = 0
	}

	results := []map[string]any{}
	counts := map[string]int{"created": 0, "updated": 0, "unchanged": 0}
	factIds := []string{}
	for _, f := range facts {
		r, err := mcpRememberFact(e, scope, namespace, f, threshold)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
		if action, _ := r["action"].(string); action != "" {
			counts[action]++
		}
		if id, _ := r["id"].(string); id != "" {
			factIds = append(factIds, id)
		}
	}

	result := map[string]any{
		"project":   namespace,
		"facts":     results,
		"created":   counts["created"],
		"updated":   counts["updated"],
		"unchanged": counts["unchanged"],
	}

	if summary != "" {
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = mcpTruncate(strings.Join(strings.Fields(summary), " "), 60)
		}
		title = "Session: " + title

		var content strings.Builder
		content.WriteString("# " + title + "\n\n" + summary)
		if len(in.Decisions) > 0 {
			content.WriteString("\n\n## Decisions")
			for _, d := range in.Decisions {
				if d = strings.TrimSpace(d); d != "" {
					content.WriteString("\n- " + d)
				}
			}
		}
		if len(in.Todos) > 0 {
			content.WriteString("\n\n## Todos")
			for _, t := range in.Todos {
				if t = strings.TrimSpace(t); t != "" {
					content.WriteString("\n- [ ] " + t)
				}
			}
		}
		if len(in.Facts) > 0 {
			content.WriteString("\n\n## Facts")
			for _, f := range in.Facts {
				if c := strings.TrimSpace(f.Content); c != "" {
					content.WriteString("\n- " + strings.Join(strings.Fields(c), " "))
				}
			}
		}

		body := content.String()
		tags := normalizeTags(append([]string{mcpSessionTag}, in.Tags...))
		metadata := map[string]any{"facts": len(factIds)}
		for k, v := range in.SessionMetadata {
			metadata[k] = v
		}

		var current *mcpMemoryRow
		if in.SessionMemoryId != "" {
			if row, err := mcpLoadWritableMemory(e.App, scope, in.SessionMemoryId); err == nil {
				current = row
			}
		}

		session, warning, err := mcpSaveMemory(e, scope, current, &mcpSaveInput{
			Namespace: namespace,
			Title:     &title,
			Content:   &body,
			Tags:      &tags,
			Metadata:  &metadata,
		}, func(txApp core.App, id string) error {
			for _, factId := range factIds {
				if _, err := mcpUpsertEdge(txApp, scope, factId, id, "noted_in", 1, nil); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sessionInfo := map[string]any{"id": session.Id, "title": session.Title}
		if warning != "" {
			sessionInfo["warning"] = warning
		}
		result["session"] = sessionInfo
	}

	return result, nil
}

func mcpMemoryRemember(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	in := &mcpSessionInput{}
	if err := mcpBindArgs(args, in); err != nil {
		return nil, err
	}
	return mcpRememberSession(e, scope, in)
}

// -------------------------------------------------------------------
// Extract
// -------------------------------------------------------------------

const mcpExtractSystemPrompt = `You maintain the long-term memory of an AI assistant. From the conversation or text provided, extract what is worth remembering in future conversations.

Return ONLY a JSON object (no markdown fences, no comments) with this shape:
{"title": "short session title", "summary": "2-5 sentences: goal, what was done, outcome, open questions", "facts": [{"title": "short title", "content": "one atomic self-contained fact", "tags": ["tag"], "pinned": false}], "decisions": ["decision taken"], "todos": ["open follow-up"]}

Rules:
- Facts must be durable and useful later: user preferences and identity, people, projects, conventions, architecture, environments, problems and their solutions. Skip greetings, chit-chat and transient details.
- Each fact is atomic and understandable on its own (use names instead of pronouns; refer to the user as "The user").
- Set "pinned": true only for stable core facts about the user (name, role, language, strong preferences) or essential project facts.
- NEVER include secrets: passwords, API keys, tokens, private keys, credentials.
- Use the language of the conversation.
- Empty arrays are fine when there is nothing to extract.`

func mcpMemoryExtract(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Text         string   `json:"text"`
		Namespace    string   `json:"namespace"`
		Channel      string   `json:"channel"`
		Model        string   `json:"model"`
		Save         *bool    `json:"save"`
		MaxFacts     int      `json:"maxFacts"`
		Instructions string   `json:"instructions"`
		Tags         []string `json:"tags"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	if strings.TrimSpace(payload.Text) == "" {
		return nil, mcpToolErr("text is required.")
	}

	extracted, res, err := mcpRunExtraction(e, payload.Text, payload.Channel, payload.Model, payload.MaxFacts, payload.Instructions)
	if err != nil {
		return nil, err
	}
	extracted.Namespace = payload.Namespace
	extracted.Tags = payload.Tags

	if payload.Save != nil && !*payload.Save {
		return map[string]any{
			"saved":     false,
			"title":     extracted.Title,
			"summary":   extracted.Summary,
			"facts":     extracted.Facts,
			"decisions": extracted.Decisions,
			"todos":     extracted.Todos,
			"model":     res["model"],
			"channel":   res["channel"],
		}, nil
	}

	saved, err := mcpRememberSession(e, scope, extracted)
	if err != nil {
		return nil, err
	}
	saved["saved"] = true
	saved["summary"] = extracted.Summary
	saved["model"] = res["model"]
	saved["channel"] = res["channel"]
	return saved, nil
}

// mcpRunExtraction asks an LLM channel to extract the memories from text.
func mcpRunExtraction(e *core.RequestEvent, text, channel, model string, maxFacts int, instructions string) (*mcpSessionInput, map[string]any, error) {
	text = strings.TrimSpace(text)
	if len(text) > mcpExtractMaxInputChars {
		// keep the most recent part of long conversations
		text = "[...]\n" + text[len(text)-mcpExtractMaxInputChars:]
	}
	maxFacts = clampInt(maxFacts, mcpExtractDefaultFacts, 50)

	system := mcpExtractSystemPrompt + fmt.Sprintf("\n- Extract at most %d facts.", maxFacts) + mcpLanguageHint(text)
	if strings.TrimSpace(instructions) != "" {
		system += "\n- " + strings.TrimSpace(instructions)
	}

	extracted := &mcpSessionInput{}
	res, err := mcpLLMJSON(e, channel, model, system, "Text to extract memories from:\n\n"+text, 4096, extracted)
	if err != nil {
		return nil, nil, err
	}
	if len(extracted.Facts) > maxFacts {
		extracted.Facts = extracted.Facts[:maxFacts]
	}
	return extracted, res, nil
}

// mcpLLMJSON asks an LLM channel for a JSON object and decodes it into dst.
//
// JSON mode is requested from the OpenAI compatible channels (and dropped when the
// provider rejects it), the common formatting defects of LLM outputs are repaired
// and the request is retried once when the answer still isn't valid JSON.
func mcpLLMJSON(e *core.RequestEvent, channel, model, system, prompt string, maxTokens int, dst any) (map[string]any, error) {
	c, err := llmFindChannel(e, channel, model)
	if err != nil {
		return nil, mcpToolErr("LLM call failed (configure a channel with llm_channel): " + err.Error())
	}

	call := func(prompt string, jsonMode bool) (map[string]any, string, error) {
		args := map[string]any{
			"channel": c.Id, "model": model, "system": system, "prompt": prompt,
			"temperature": 0, "maxTokens": maxTokens,
		}
		if jsonMode {
			args["extra"] = map[string]any{"response_format": map[string]any{"type": "json_object"}}
		}
		raw, _ := json.Marshal(args)
		out, err := mcpLLMChat(e, raw)
		if err != nil {
			return nil, "", err
		}
		res, _ := out.(map[string]any)
		answer, _ := res["content"].(string)
		return res, answer, nil
	}

	jsonMode := c.Type == "openai"
	res, answer, err := call(prompt, jsonMode)
	if err != nil && jsonMode {
		// the provider may not support response_format
		jsonMode = false
		res, answer, err = call(prompt, false)
	}
	if err != nil {
		return nil, mcpToolErr("LLM call failed: " + err.Error())
	}

	parseErr := mcpDecodeLLMJSON(answer, dst)
	if parseErr != nil {
		retryPrompt := prompt + "\n\nIMPORTANT: return ONLY one valid JSON object (strings escaped, no markdown, no comments, no text after it)."
		if res2, answer2, err := call(retryPrompt, jsonMode); err == nil {
			if parseErr = mcpDecodeLLMJSON(answer2, dst); parseErr == nil {
				return res2, nil
			}
			answer = answer2
		}
		return nil, mcpToolErr(fmt.Sprintf("The LLM didn't return valid JSON (%v): %s", parseErr, mcpTruncate(answer, 300)))
	}
	return res, nil
}

// mcpDecodeLLMJSON decodes the first JSON object of an LLM answer, tolerating markdown
// fences, text around the object and raw control characters inside the strings.
func mcpDecodeLLMJSON(answer string, dst any) error {
	start := strings.Index(answer, "{")
	if start < 0 {
		return errors.New("no JSON object in the answer")
	}
	raw := mcpEscapeJSONControlChars(answer[start:])
	return json.NewDecoder(strings.NewReader(raw)).Decode(dst)
}

// mcpEscapeJSONControlChars escapes the control characters (eg. raw new lines)
// that appear inside JSON strings.
func mcpEscapeJSONControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case inString && r == '\\':
			escaped = true
		case r == '"':
			inString = !inString
		case inString && r < 0x20:
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mcpLanguageHint returns an explicit output language instruction for texts mainly
// written in Chinese, Japanese or Korean (models tend to answer in the language of
// the prompt instructions otherwise).
func mcpLanguageHint(text string) string {
	var han, kana, hangul, latin int
	for _, r := range text {
		switch {
		case r >= 0x3040 && r <= 0x30ff:
			kana++
		case r >= 0xac00 && r <= 0xd7af:
			hangul++
		case r >= 0x4e00 && r <= 0x9fff:
			han++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			latin++
		}
	}
	// CJK characters carry about one word each, latin words ~5 letters
	words := latin / 5
	switch {
	case kana > 0 && kana+han > words:
		return "\n- IMPORTANT: write the title, summary, facts, decisions and todos in Japanese (日本語), like the conversation."
	case hangul > words:
		return "\n- IMPORTANT: write the title, summary, facts, decisions and todos in Korean (한국어), like the conversation."
	case han > words:
		return "\n- IMPORTANT: write the title, summary, facts, decisions and todos in Chinese (中文), like the conversation."
	default:
		return ""
	}
}

// -------------------------------------------------------------------
// Prompts and resources
// -------------------------------------------------------------------

const (
	mcpContextResourceURI = "bosbase://memory/context"
	mcpPinnedResourceURI  = "bosbase://memory/pinned"
)

func mcpPromptsList() map[string]any {
	return map[string]any{"prompts": []any{
		map[string]any{
			"name":        "recall",
			"title":       "Recall memory context",
			"description": "Load what you remember (core memories, latest sessions, todos and the memories relevant to a topic) into the conversation.",
			"arguments": []any{
				map[string]any{"name": "query", "description": "Topic or task to recall memories about.", "required": false},
				map[string]any{"name": "project", "description": "Project to focus on.", "required": false},
			},
		},
		map[string]any{
			"name":        "remember",
			"title":       "Remember this conversation",
			"description": "Ask the assistant to save the context of the current conversation (summary, facts, decisions, todos) to its memory.",
			"arguments": []any{
				map[string]any{"name": "project", "description": "Project of the conversation.", "required": false},
			},
		},
	}}
}

func mcpPromptsGet(e *core.RequestEvent, rawParams json.RawMessage) (any, *mcpRPCError) {
	params := struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}{}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Invalid prompts/get params."}
	}
	project := strings.TrimSpace(params.Arguments["project"])

	textMessage := func(text string) map[string]any {
		return map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": text}}
	}

	switch params.Name {
	case "recall":
		scope := &mcpScope{writeOwner: e.Auth.Id, ownerCollection: e.Auth.Collection().Name}
		ctx, err := mcpBuildContext(e, scope, nil, mcpContextOptions{Query: params.Arguments["query"], Namespace: project})
		if err != nil {
			return nil, &mcpRPCError{Code: mcpErrInternal, Message: err.Error()}
		}
		briefing, _ := ctx["briefing"].(string)
		return map[string]any{
			"description": "Memory context",
			"messages": []any{textMessage("Here is what you remember from previous conversations (BosBase memory). Use it as context for this conversation; " +
				"search for more with memory_search and keep it up to date.\n\n" + briefing)},
		}, nil
	case "remember":
		target := ""
		if project != "" {
			target = " (project \"" + project + "\")"
		}
		return map[string]any{
			"description": "Save the conversation to memory",
			"messages": []any{textMessage("Save the context of this conversation to your long-term memory" + target + ": call memory_remember with " +
				"a short title, a summary (goal, what was done, outcome, open questions), the durable facts learned (atomic and self-contained; " +
				"pin stable user preferences), the decisions taken and the open todos. Don't store secrets. Then confirm briefly what was saved.")},
		}, nil
	default:
		return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Unknown prompt " + strconv.Quote(params.Name) + "."}
	}
}

func mcpResourcesList() map[string]any {
	return map[string]any{"resources": []any{
		map[string]any{
			"uri":         mcpContextResourceURI,
			"name":        "memory-context",
			"title":       "Memory context",
			"description": "Your core memories, latest sessions, open todos and recently updated memories.",
			"mimeType":    "text/markdown",
		},
		map[string]any{
			"uri":         mcpPinnedResourceURI,
			"name":        "pinned-memories",
			"title":       "Pinned memories",
			"description": "Your pinned core memories.",
			"mimeType":    "text/markdown",
		},
	}}
}

func mcpResourcesRead(e *core.RequestEvent, rawParams json.RawMessage) (any, *mcpRPCError) {
	params := struct {
		URI string `json:"uri"`
	}{}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Invalid resources/read params."}
	}

	scope := &mcpScope{writeOwner: e.Auth.Id, ownerCollection: e.Auth.Collection().Name}
	var text string
	switch params.URI {
	case mcpContextResourceURI:
		ctx, err := mcpBuildContext(e, scope, nil, mcpContextOptions{})
		if err != nil {
			return nil, &mcpRPCError{Code: mcpErrInternal, Message: err.Error()}
		}
		text, _ = ctx["briefing"].(string)
	case mcpPinnedResourceURI:
		rows, err := mcpLoadContextRows(e.App, scope.writeOwner, "[[pinned]] = TRUE", dbx.Params{}, "[[updated]] DESC", 200)
		if err != nil {
			return nil, &mcpRPCError{Code: mcpErrInternal, Message: err.Error()}
		}
		var b strings.Builder
		b.WriteString("# Pinned memories\n")
		if len(rows) == 0 {
			b.WriteString("\nNo pinned memories (pin core facts with memory_store/memory_update pinned: true).")
		}
		for _, r := range rows {
			b.WriteString("\n" + mcpBriefingLine(r.Title, r.Content, r.Namespace, 2000))
		}
		text = b.String()
	default:
		return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Unknown resource " + strconv.Quote(params.URI) + "."}
	}

	return map[string]any{"contents": []any{
		map[string]any{"uri": params.URI, "mimeType": "text/markdown", "text": text},
	}}, nil
}

// mcpMemoryAutoContextEnabled reports whether the memory context is added to the
// initialize instructions (MCP_MEMORY_AUTO_CONTEXT=false disables it).
func mcpMemoryAutoContextEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("MCP_MEMORY_AUTO_CONTEXT")))
	return v != "false" && v != "0" && v != "no"
}
