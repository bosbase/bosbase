package apis

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
)

// Long-term conversation memory.
//
// The AI saves the turns of its conversations (conversation_save) so that it can
// recall the exact past context later (conversation_recall, semantic search over
// all the past turns). Long conversations are condensed into a rolling summary
// plus durable facts/decisions/todos (conversation_summarize, also triggered
// automatically by conversation_save), and the session summaries of a project are
// consolidated into a long-term project summary (memory_consolidate).
//
// Conversations are always private to their owner.

const (
	mcpConversationsTable = "_mcp_conversations"
	mcpMessagesTable      = "_mcp_messages"

	mcpProjectSummaryTag = "project-summary"

	mcpMaxMessagesPerSave       = 200
	mcpMaxMessageLength         = 100000
	mcpMessageEmbedChars        = 8000
	mcpMessageEmbedBatch        = 64
	mcpDefaultSummarizeAfter    = 40
	mcpConversationCleanupEvery = 200
	mcpConsolidateMaxSessions   = 30
)

var mcpMessageRoles = []string{"user", "assistant", "system", "tool"}

var mcpConversationTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	mcpConversationTools = []mcpTool{
		{
			Name: "conversation_save",
			Description: "Save conversation turns to your long-term conversation memory so you can recall the exact past context in future sessions (conversation_recall). " +
				"Omit conversationId to start a new conversation, then reuse the returned id to append the next turns. When a conversation gets long it is " +
				"automatically condensed into a rolling summary and durable memories (needs an LLM channel). Never include secrets.",
			InputSchema: obj(map[string]any{
				"conversationId": str("Existing conversation id (omit to start a new conversation)."),
				"project":        str("Project of the conversation."),
				"title":          str("Conversation title (default: the first user message)."),
				"client":         str("Optional client/agent name."),
				"messages": map[string]any{
					"type":        "array",
					"description": "Turns to append, in order.",
					"items": obj(map[string]any{
						"role":     map[string]any{"type": "string", "enum": mcpMessageRoles},
						"content":  str("Message text."),
						"metadata": map[string]any{"type": "object"},
					}, "role", "content"),
				},
				"autoSummarize": boolean("Summarize automatically when enough new turns accumulated (default true; requires an LLM channel)."),
				"channel":       str("LLM channel used for the automatic summary."),
				"model":         str("Model used for the automatic summary."),
			}, "messages"),
			Handler: mcpConversationSave,
		},
		{
			Name: "conversation_recall",
			Description: "Search all your past conversations by meaning (with keyword fallback) and return the matching turns with their surrounding turns. " +
				"Use it when the user refers to something discussed before (\"as we said last week...\") or when past context may help.",
			InputSchema: obj(map[string]any{
				"query":          str("What to look for."),
				"project":        str("Only conversations of this project."),
				"conversationId": str("Only this conversation."),
				"limit":          integer("Max matches (default 5, max 20)."),
				"window":         integer("Surrounding turns returned before/after each match (default 1, max 5)."),
				"mode":           map[string]any{"type": "string", "enum": []string{"auto", "semantic", "keyword"}},
			}, "query"),
			ReadOnly: true,
			Handler:  mcpConversationRecall,
		},
		{
			Name:        "conversation_list",
			Description: "List your past conversations (newest first) with their rolling summary.",
			InputSchema: obj(map[string]any{
				"project": str("Only this project."),
				"search":  str("Text searched in the title and summary."),
				"limit":   integer("Max conversations (default 20, max 200)."),
				"offset":  integer("Offset."),
			}),
			ReadOnly: true,
			Handler:  mcpConversationList,
		},
		{
			Name:        "conversation_get",
			Description: "Read the turns of a conversation (paginated by sequence number; tail: true returns the latest turns).",
			InputSchema: obj(map[string]any{
				"conversationId": str("Conversation id."),
				"fromSeq":        integer("First sequence number (default 1)."),
				"limit":          integer("Max turns (default 50, max 500)."),
				"tail":           boolean("Return the latest turns."),
			}, "conversationId"),
			ReadOnly: true,
			Handler:  mcpConversationGet,
		},
		{
			Name: "conversation_summarize",
			Description: "Condense the not yet summarized turns of a conversation into its rolling summary (stored as a session memory) and durable facts, " +
				"decisions and todos (deduplicated memories). Requires an LLM channel.",
			InputSchema: obj(map[string]any{
				"conversationId": str("Conversation id."),
				"channel":        str("LLM channel id or name."),
				"model":          str("Model."),
			}, "conversationId"),
			Handler: mcpConversationSummarize,
		},
		{
			Name:        "conversation_delete",
			Description: "Permanently delete a conversation and its turns (the memories extracted from it are kept).",
			InputSchema: obj(map[string]any{
				"conversationId": str("Conversation id."),
			}, "conversationId"),
			Destructive: true,
			Handler:     mcpConversationDelete,
		},
		{
			Name: "memory_consolidate",
			Description: "Consolidate the session summaries of a project into its long-term project summary (one evolving memory per project, tag \"project-summary\") " +
				"that is included in your memory context. Run it periodically (eg. after several sessions). Requires an LLM channel.",
			InputSchema: obj(map[string]any{
				"project":     str("Project to consolidate (default project when omitted)."),
				"channel":     str("LLM channel id or name."),
				"model":       str("Model."),
				"maxSessions": integer("Max new sessions merged per call (default 30)."),
				"force":       boolean("Rewrite the summary even without new sessions."),
			}),
			Handler: mcpMemoryConsolidate,
		},
	}
}

// -------------------------------------------------------------------
// Rows
// -------------------------------------------------------------------

type mcpConversationRow struct {
	Id              string `db:"id"`
	Owner           string `db:"owner"`
	Namespace       string `db:"namespace"`
	Title           string `db:"title"`
	Client          string `db:"client"`
	Summary         string `db:"summary"`
	SummaryMemory   string `db:"summaryMemory"`
	SummarizedUntil int    `db:"summarizedUntil"`
	MessageCount    int    `db:"messageCount"`
	LastMessageAt   string `db:"lastMessageAt"`
	Created         string `db:"created"`
	Updated         string `db:"updated"`
}

const mcpConversationColumns = `[[id]], [[owner]], [[namespace]], [[title]], [[client]], [[summary]], [[summaryMemory]],
	[[summarizedUntil]], [[messageCount]], COALESCE([[lastMessageAt]]::text, '') AS [[lastMessageAt]],
	[[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

func (c *mcpConversationRow) toMap(summaryChars int) map[string]any {
	m := map[string]any{
		"id":              c.Id,
		"project":         c.Namespace,
		"title":           c.Title,
		"messageCount":    c.MessageCount,
		"summarizedUntil": c.SummarizedUntil,
		"unsummarized":    c.MessageCount - c.SummarizedUntil,
		"lastMessageAt":   c.LastMessageAt,
		"created":         c.Created,
	}
	if c.Summary != "" {
		m["summary"] = mcpTruncate(c.Summary, summaryChars)
	}
	if c.Client != "" {
		m["client"] = c.Client
	}
	if c.SummaryMemory != "" {
		m["summaryMemory"] = c.SummaryMemory
	}
	return m
}

type mcpMessageRow struct {
	Id           string  `db:"id"`
	Conversation string  `db:"conversation"`
	Seq          int     `db:"seq"`
	Role         string  `db:"role"`
	Content      string  `db:"content"`
	Created      string  `db:"created"`
	Score        float64 `db:"score"`
}

const mcpMessageColumns = `m.[[id]], m.[[conversation]], m.[[seq]], m.[[role]], m.[[content]], m.[[created]]::text AS [[created]]`

func (m *mcpMessageRow) toMap(maxChars int) map[string]any {
	return map[string]any{
		"seq":     m.Seq,
		"role":    m.Role,
		"content": mcpTruncate(m.Content, maxChars),
		"created": m.Created,
	}
}

func mcpLoadConversation(app core.App, owner, id string) (*mcpConversationRow, error) {
	if strings.TrimSpace(id) == "" {
		return nil, mcpToolErr("conversationId is required.")
	}
	c := &mcpConversationRow{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[id]] = {:id} AND [[owner]] = {:owner} LIMIT 1", mcpConversationColumns, mcpConversationsTable,
	)).Bind(dbx.Params{"id": strings.TrimSpace(id), "owner": owner}).One(c)
	if err != nil {
		return nil, mcpToolErr("Conversation " + strconv.Quote(id) + " was not found.")
	}
	return c, nil
}

func mcpConversationScope(e *core.RequestEvent, args json.RawMessage) (*mcpScope, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	if scope.writeOwner == "" || scope.isSpace() {
		return nil, mcpToolErr("Conversations are always private and can't be shared (use memory_remember with space to share a summary with a space).")
	}
	return scope, nil
}

func mcpSummarizeAfter() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MCP_CONVERSATION_SUMMARIZE_AFTER"))); err == nil && v > 0 {
		return v
	}
	return mcpDefaultSummarizeAfter
}

// mcpCleanupConversations deletes the turns older than MCP_CONVERSATION_RETENTION_DAYS (kept forever by default).
func mcpCleanupConversations(app core.App) {
	days, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MCP_CONVERSATION_RETENTION_DAYS")))
	if err != nil || days <= 0 || rand.IntN(mcpConversationCleanupEvery) != 0 {
		return
	}
	if _, err := app.DB().NewQuery(fmt.Sprintf(
		"DELETE FROM {{%s}} WHERE [[created]] < NOW() - ({:days} || ' days')::interval", mcpMessagesTable,
	)).Bind(dbx.Params{"days": strconv.Itoa(days)}).Execute(); err != nil {
		app.Logger().Warn("MCP conversations cleanup failure", "error", err)
	}
}

// -------------------------------------------------------------------
// Save
// -------------------------------------------------------------------

func mcpConversationSave(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpConversationScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		ConversationId string `json:"conversationId"`
		Namespace      string `json:"namespace"`
		Title          string `json:"title"`
		Client         string `json:"client"`
		Messages       []struct {
			Role     string         `json:"role"`
			Content  string         `json:"content"`
			Metadata map[string]any `json:"metadata"`
		} `json:"messages"`
		AutoSummarize *bool  `json:"autoSummarize"`
		Channel       string `json:"channel"`
		Model         string `json:"model"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	type message struct {
		role     string
		content  string
		metadata string
	}
	messages := []message{}
	for _, m := range payload.Messages {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "" {
			role = "user"
		}
		if !slicesContains(mcpMessageRoles, role) {
			return nil, mcpToolErr("role must be one of " + strings.Join(mcpMessageRoles, ", ") + ".")
		}
		if len(content) > mcpMaxMessageLength {
			content = content[:mcpMaxMessageLength]
		}
		metadata := "{}"
		if len(m.Metadata) > 0 {
			raw, _ := json.Marshal(m.Metadata)
			metadata = string(raw)
		}
		messages = append(messages, message{role: role, content: content, metadata: metadata})
	}
	if len(messages) == 0 {
		return nil, mcpToolErr("messages is required (non empty contents).")
	}
	if len(messages) > mcpMaxMessagesPerSave {
		return nil, mcpToolErr(fmt.Sprintf("Too many messages (max %d per call).", mcpMaxMessagesPerSave))
	}

	var conversation *mcpConversationRow
	if strings.TrimSpace(payload.ConversationId) != "" {
		if conversation, err = mcpLoadConversation(e.App, scope.writeOwner, payload.ConversationId); err != nil {
			return nil, err
		}
	}

	// embeddings (user/assistant turns)
	var warning string
	vectors := make([][]float64, len(messages))
	models := make([]string, len(messages))
	toEmbed := []int{}
	for i, m := range messages {
		if m.role == "user" || m.role == "assistant" {
			toEmbed = append(toEmbed, i)
		}
	}
	for start := 0; start < len(toEmbed); start += mcpMessageEmbedBatch {
		end := min(start+mcpMessageEmbedBatch, len(toEmbed))
		texts := make([]string, end-start)
		for j, idx := range toEmbed[start:end] {
			texts[j] = mcpTruncate(messages[idx].content, mcpMessageEmbedChars)
		}
		batch, target, err := embedTexts(e.Request.Context(), e.App, texts)
		if err != nil {
			warning = "Embedding failed, the turns are only searchable by keywords: " + err.Error()
			break
		}
		if target == nil {
			break // no embedding provider
		}
		for j, idx := range toEmbed[start:end] {
			vectors[idx] = batch[j]
			models[idx] = target.Model
		}
	}

	namespace := strings.TrimSpace(payload.Namespace)
	if namespace == "" {
		namespace = mcpDefaultNamespace
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" && conversation == nil {
		for _, m := range messages {
			if m.role == "user" {
				title = mcpTruncate(strings.Join(strings.Fields(m.content), " "), 80)
				break
			}
		}
	}

	var conversationId string
	var messageCount, embedded int
	err = e.App.RunInTransaction(func(txApp core.App) error {
		if conversation == nil {
			if err := txApp.DB().NewQuery(fmt.Sprintf(`
				INSERT INTO {{%s}} ([[owner]], [[ownerCollection]], [[namespace]], [[title]], [[client]])
				VALUES ({:owner}, {:ownerCollection}, {:namespace}, {:title}, {:client}) RETURNING [[id]]`, mcpConversationsTable,
			)).Bind(dbx.Params{
				"owner": scope.writeOwner, "ownerCollection": scope.ownerCollection,
				"namespace": namespace, "title": title, "client": strings.TrimSpace(payload.Client),
			}).Row(&conversationId); err != nil {
				return err
			}
		} else {
			conversationId = conversation.Id
		}

		// lock the conversation to assign the sequence numbers
		var count int
		if err := txApp.DB().NewQuery(fmt.Sprintf(
			"SELECT [[messageCount]] FROM {{%s}} WHERE [[id]] = {:id} FOR UPDATE", mcpConversationsTable,
		)).Bind(dbx.Params{"id": conversationId}).Row(&count); err != nil {
			return err
		}

		for i, m := range messages {
			params := dbx.Params{
				"conversation":   conversationId,
				"owner":          scope.writeOwner,
				"seq":            count + i + 1,
				"role":           m.role,
				"content":        m.content,
				"metadata":       m.metadata,
				"embedding":      nil,
				"embeddingModel": models[i],
			}
			if vectors[i] != nil {
				params["embedding"] = formatVectorForPostgres(vectors[i])
				embedded++
			}
			if _, err := txApp.DB().NewQuery(fmt.Sprintf(`
				INSERT INTO {{%s}} ([[conversation]], [[owner]], [[seq]], [[role]], [[content]], [[metadata]], [[embedding]], [[embeddingModel]])
				VALUES ({:conversation}, {:owner}, {:seq}, {:role}, {:content}, {:metadata}::jsonb, {:embedding}::vector, {:embeddingModel})`, mcpMessagesTable,
			)).Bind(params).Execute(); err != nil {
				return err
			}
		}

		messageCount = count + len(messages)
		sets := "[[messageCount]] = {:count}, [[lastMessageAt]] = NOW(), [[updated]] = NOW()"
		params := dbx.Params{"id": conversationId, "count": messageCount}
		if conversation != nil && payload.Title != "" {
			sets += ", [[title]] = {:title}"
			params["title"] = strings.TrimSpace(payload.Title)
		}
		_, err := txApp.DB().NewQuery(fmt.Sprintf("UPDATE {{%s}} SET %s WHERE [[id]] = {:id}", mcpConversationsTable, sets)).Bind(params).Execute()
		return err
	})
	if err != nil {
		return nil, err
	}

	mcpCleanupConversations(e.App)

	saved, err := mcpLoadConversation(e.App, scope.writeOwner, conversationId)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"conversationId": conversationId,
		"project":        saved.Namespace,
		"title":          saved.Title,
		"appended":       len(messages),
		"messageCount":   messageCount,
		"embedded":       embedded,
		"unsummarized":   saved.MessageCount - saved.SummarizedUntil,
	}

	// automatic rolling summary
	autoSummarize := payload.AutoSummarize == nil || *payload.AutoSummarize
	if autoSummarize && saved.MessageCount-saved.SummarizedUntil >= mcpSummarizeAfter() {
		if _, chErr := llmFindChannel(e, payload.Channel, payload.Model); chErr == nil {
			summary, err := mcpSummarizeConversation(e, scope, saved, payload.Channel, payload.Model)
			if err != nil {
				warning = strings.TrimSpace(warning + " Automatic summary failed: " + err.Error())
			} else {
				result["summarized"] = summary
				result["unsummarized"] = 0
			}
		} else {
			result["hint"] = "The conversation is long: configure an LLM channel to summarize it automatically, or save a summary with memory_remember."
		}
	}
	if warning != "" {
		result["warning"] = warning
	}

	return result, nil
}

// -------------------------------------------------------------------
// Recall
// -------------------------------------------------------------------

type mcpRecallOptions struct {
	Query          string
	Namespace      string
	ConversationId string
	Limit          int
	Window         int
	Mode           string
}

// mcpRecallConversations searches the owner's past turns and returns the hits with their surrounding turns.
func mcpRecallConversations(e *core.RequestEvent, owner string, opts mcpRecallOptions) ([]map[string]any, string, string, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, "", "", mcpToolErr("query is required.")
	}
	limit := clampInt(opts.Limit, 5, 20)
	window := opts.Window
	if window < 0 {
		window = 0
	}
	if window == 0 && opts.Window == 0 {
		window = 1
	}
	window = min(window, 5)

	params := dbx.Params{"owner": owner, "limit": limit}
	filters := []string{"m.[[owner]] = {:owner}", "c.[[owner]] = {:owner}"}
	if ns := strings.TrimSpace(opts.Namespace); ns != "" {
		params["namespace"] = ns
		filters = append(filters, "c.[[namespace]] = {:namespace}")
	}
	if id := strings.TrimSpace(opts.ConversationId); id != "" {
		params["conversation"] = id
		filters = append(filters, "m.[[conversation]] = {:conversation}")
	}
	where := strings.Join(filters, " AND ")

	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	usedMode := "keyword"
	var warning string
	hits := []*mcpMessageRow{}

	if mode != "keyword" {
		vector, model, err := mcpEmbed(e.Request.Context(), e.App, query, nil)
		switch {
		case err != nil:
			warning = "Embedding failed, fell back to keyword search: " + err.Error()
		case vector != nil:
			usedMode = "semantic"
			params["vector"] = formatVectorForPostgres(vector)
			params["model"] = model
			params["dims"] = len(vector)
			if err := e.App.DB().NewQuery(fmt.Sprintf(`
				SELECT %s, 1 - (m.[[embedding]] <=> {:vector}::vector) AS [[score]]
				FROM {{%s}} m JOIN {{%s}} c ON c.[[id]] = m.[[conversation]]
				WHERE %s AND m.[[embedding]] IS NOT NULL AND m.[[embeddingModel]] = {:model} AND vector_dims(m.[[embedding]]) = {:dims}
				ORDER BY m.[[embedding]] <=> {:vector}::vector LIMIT {:limit}`,
				mcpMessageColumns, mcpMessagesTable, mcpConversationsTable, where,
			)).Bind(params).All(&hits); err != nil {
				return nil, "", "", err
			}
		}
	}

	if len(hits) < limit {
		params["q"] = query
		params["like"] = "%" + escapeLike(query) + "%"
		exclude := ""
		if len(hits) > 0 {
			ids := make([]string, len(hits))
			for i, h := range hits {
				key := "hit" + strconv.Itoa(i)
				params[key] = h.Id
				ids[i] = "{:" + key + "}"
			}
			exclude = " AND m.[[id]] NOT IN (" + strings.Join(ids, ",") + ")"
		}
		keywordHits := []*mcpMessageRow{}
		if err := e.App.DB().NewQuery(fmt.Sprintf(`
			SELECT %s, ts_rank(to_tsvector('simple', m.[[content]]), plainto_tsquery('simple', {:q})) * 0.5 AS [[score]]
			FROM {{%s}} m JOIN {{%s}} c ON c.[[id]] = m.[[conversation]]
			WHERE %s%s AND (to_tsvector('simple', m.[[content]]) @@ plainto_tsquery('simple', {:q}) OR m.[[content]] ILIKE {:like})
			ORDER BY [[score]] DESC, m.[[created]] DESC LIMIT {:limit}`,
			mcpMessageColumns, mcpMessagesTable, mcpConversationsTable, where, exclude,
		)).Bind(params).All(&keywordHits); err != nil {
			return nil, "", "", err
		}
		for _, h := range keywordHits {
			if len(hits) >= limit {
				break
			}
			hits = append(hits, h)
		}
	}

	// conversations info
	titles := map[string]*mcpConversationRow{}
	results := []map[string]any{}
	covered := map[string]map[int]bool{}
	for _, h := range hits {
		if covered[h.Conversation][h.Seq] {
			continue // already returned in the window of a previous hit
		}
		conv, ok := titles[h.Conversation]
		if !ok {
			if c, err := mcpLoadConversation(e.App, owner, h.Conversation); err == nil {
				conv = c
			}
			titles[h.Conversation] = conv
		}

		contextRows := []*mcpMessageRow{}
		if window > 0 {
			if err := e.App.DB().NewQuery(fmt.Sprintf(`
				SELECT %s FROM {{%s}} m WHERE m.[[conversation]] = {:conversation} AND m.[[owner]] = {:owner}
				AND m.[[seq]] BETWEEN {:from} AND {:to} ORDER BY m.[[seq]]`, mcpMessageColumns, mcpMessagesTable,
			)).Bind(dbx.Params{"conversation": h.Conversation, "owner": owner, "from": h.Seq - window, "to": h.Seq + window}).All(&contextRows); err != nil {
				return nil, "", "", err
			}
		}
		if covered[h.Conversation] == nil {
			covered[h.Conversation] = map[int]bool{}
		}
		turns := make([]map[string]any, len(contextRows))
		for i, r := range contextRows {
			covered[h.Conversation][r.Seq] = true
			turns[i] = r.toMap(1000)
		}

		item := map[string]any{
			"conversationId": h.Conversation,
			"seq":            h.Seq,
			"role":           h.Role,
			"content":        mcpTruncate(h.Content, 2000),
			"score":          h.Score,
			"created":        h.Created,
			"context":        turns,
		}
		if conv != nil {
			item["conversationTitle"] = conv.Title
			item["project"] = conv.Namespace
		}
		results = append(results, item)
	}

	return results, usedMode, warning, nil
}

func mcpConversationRecall(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpConversationScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Query          string `json:"query"`
		Namespace      string `json:"namespace"`
		ConversationId string `json:"conversationId"`
		Limit          int    `json:"limit"`
		Window         *int   `json:"window"`
		Mode           string `json:"mode"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	window := 1
	if payload.Window != nil {
		window = *payload.Window
		if window == 0 {
			window = -1 // explicit no context
		}
	}

	results, mode, warning, err := mcpRecallConversations(e, scope.writeOwner, mcpRecallOptions{
		Query: payload.Query, Namespace: payload.Namespace, ConversationId: payload.ConversationId,
		Limit: payload.Limit, Window: window, Mode: payload.Mode,
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"mode": mode, "count": len(results), "results": results}
	if warning != "" {
		out["warning"] = warning
	}
	return out, nil
}

// -------------------------------------------------------------------
// List / get / delete
// -------------------------------------------------------------------

func mcpConversationList(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpConversationScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Namespace string `json:"namespace"`
		Search    string `json:"search"`
		Limit     int    `json:"limit"`
		Offset    int    `json:"offset"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	params := dbx.Params{"owner": scope.writeOwner, "limit": clampInt(payload.Limit, 20, 200), "offset": max(payload.Offset, 0)}
	filters := []string{"[[owner]] = {:owner}"}
	if ns := strings.TrimSpace(payload.Namespace); ns != "" {
		params["namespace"] = ns
		filters = append(filters, "[[namespace]] = {:namespace}")
	}
	if q := strings.TrimSpace(payload.Search); q != "" {
		params["like"] = "%" + escapeLike(q) + "%"
		filters = append(filters, "([[title]] ILIKE {:like} OR [[summary]] ILIKE {:like})")
	}
	where := strings.Join(filters, " AND ")

	rows := []*mcpConversationRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY [[updated]] DESC LIMIT {:limit} OFFSET {:offset}", mcpConversationColumns, mcpConversationsTable, where,
	)).Bind(params).All(&rows); err != nil {
		return nil, err
	}
	var total int
	_ = e.App.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE %s", mcpConversationsTable, where)).Bind(params).Row(&total)

	items := make([]map[string]any, len(rows))
	for i, r := range rows {
		items[i] = r.toMap(600)
	}
	return map[string]any{"total": total, "conversations": items}, nil
}

func mcpConversationGet(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpConversationScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		ConversationId string `json:"conversationId"`
		FromSeq        int    `json:"fromSeq"`
		Limit          int    `json:"limit"`
		Tail           bool   `json:"tail"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	conv, err := mcpLoadConversation(e.App, scope.writeOwner, payload.ConversationId)
	if err != nil {
		return nil, err
	}

	limit := clampInt(payload.Limit, 50, 500)
	from := max(payload.FromSeq, 1)
	if payload.Tail {
		from = max(conv.MessageCount-limit+1, 1)
	}

	rows := []*mcpMessageRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT %s FROM {{%s}} m WHERE m.[[conversation]] = {:id} AND m.[[owner]] = {:owner} AND m.[[seq]] >= {:from}
		ORDER BY m.[[seq]] LIMIT {:limit}`, mcpMessageColumns, mcpMessagesTable,
	)).Bind(dbx.Params{"id": conv.Id, "owner": scope.writeOwner, "from": from, "limit": limit}).All(&rows); err != nil {
		return nil, err
	}

	messages := make([]map[string]any, len(rows))
	for i, r := range rows {
		messages[i] = r.toMap(mcpMaxMessageLength)
	}
	result := map[string]any{"conversation": conv.toMap(100000), "messages": messages}
	if len(rows) > 0 && rows[len(rows)-1].Seq < conv.MessageCount {
		result["nextFromSeq"] = rows[len(rows)-1].Seq + 1
	}
	return result, nil
}

func mcpConversationDelete(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpConversationScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		ConversationId string `json:"conversationId"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	conv, err := mcpLoadConversation(e.App, scope.writeOwner, payload.ConversationId)
	if err != nil {
		return nil, err
	}
	if _, err := e.App.DB().Delete(mcpConversationsTable, dbx.HashExp{"id": conv.Id, "owner": scope.writeOwner}).Execute(); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "conversationId": conv.Id, "messages": conv.MessageCount}, nil
}

// -------------------------------------------------------------------
// Summaries
// -------------------------------------------------------------------

// mcpSummarizeConversation condenses the unsummarized turns into the rolling summary and durable memories.
func mcpSummarizeConversation(e *core.RequestEvent, scope *mcpScope, conv *mcpConversationRow, channel, model string) (map[string]any, error) {
	rows := []*mcpMessageRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT %s FROM {{%s}} m WHERE m.[[conversation]] = {:id} AND m.[[owner]] = {:owner} AND m.[[seq]] > {:from}
		ORDER BY m.[[seq]]`, mcpMessageColumns, mcpMessagesTable,
	)).Bind(dbx.Params{"id": conv.Id, "owner": scope.writeOwner, "from": conv.SummarizedUntil}).All(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return map[string]any{"conversationId": conv.Id, "summarizedUntil": conv.SummarizedUntil, "note": "No new turns to summarize."}, nil
	}

	var transcript strings.Builder
	if conv.Summary != "" {
		transcript.WriteString("Summary of the earlier part of this conversation:\n" + conv.Summary + "\n\nNew turns:\n")
	}
	for _, r := range rows {
		transcript.WriteString(strings.ToUpper(r.Role[:1]) + r.Role[1:] + ": " + r.Content + "\n")
	}

	instructions := "The \"summary\" must cover the whole conversation so far (merge the summary of the earlier part with the new turns). " +
		"Only extract facts, decisions and todos from the new turns."
	extracted, _, err := mcpRunExtraction(e, transcript.String(), channel, model, 0, instructions)
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(conv.Title)
	if title == "" {
		title = strings.TrimSpace(extracted.Title)
	}
	extracted.Title = title
	extracted.Namespace = conv.Namespace
	extracted.SessionMemoryId = conv.SummaryMemory
	extracted.SessionMetadata = map[string]any{"conversation": conv.Id, "summarizedUntil": rows[len(rows)-1].Seq}
	if strings.TrimSpace(extracted.Summary) == "" {
		extracted.Summary = conv.Summary
	}

	saved, err := mcpRememberSession(e, scope, extracted)
	if err != nil {
		return nil, err
	}

	sessionId := ""
	if session, ok := saved["session"].(map[string]any); ok {
		sessionId, _ = session["id"].(string)
	}
	lastSeq := rows[len(rows)-1].Seq
	if _, err := e.App.DB().NewQuery(fmt.Sprintf(`
		UPDATE {{%s}} SET [[summary]] = {:summary}, [[summarizedUntil]] = {:until}, [[summaryMemory]] = {:memory}, [[updated]] = NOW()
		WHERE [[id]] = {:id} AND [[owner]] = {:owner}`, mcpConversationsTable,
	)).Bind(dbx.Params{
		"summary": strings.TrimSpace(extracted.Summary), "until": lastSeq, "memory": sessionId,
		"id": conv.Id, "owner": scope.writeOwner,
	}).Execute(); err != nil {
		return nil, err
	}

	return map[string]any{
		"conversationId":  conv.Id,
		"summarizedUntil": lastSeq,
		"summary":         strings.TrimSpace(extracted.Summary),
		"sessionMemory":   sessionId,
		"facts":           saved["facts"],
		"created":         saved["created"],
		"updated":         saved["updated"],
		"unchanged":       saved["unchanged"],
	}, nil
}

func mcpConversationSummarize(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpConversationScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		ConversationId string `json:"conversationId"`
		Channel        string `json:"channel"`
		Model          string `json:"model"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	conv, err := mcpLoadConversation(e.App, scope.writeOwner, payload.ConversationId)
	if err != nil {
		return nil, err
	}
	return mcpSummarizeConversation(e, scope, conv, payload.Channel, payload.Model)
}

const mcpConsolidateSystemPrompt = `You maintain the long-term memory of an AI assistant for one project.
Merge the current project summary with the new session summaries into an updated project summary.

Write concise markdown (at most ~400 words) with these sections when relevant:
## Overview
## Key facts and decisions
## Current state
## Open threads

Keep stable and important information, update what changed, drop resolved or obsolete details. Never include secrets.
Use the language of the sessions. Return only the summary markdown.`

func mcpMemoryConsolidate(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpConversationScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Namespace   string `json:"namespace"`
		Channel     string `json:"channel"`
		Model       string `json:"model"`
		MaxSessions int    `json:"maxSessions"`
		Force       bool   `json:"force"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	namespace := strings.TrimSpace(payload.Namespace)
	if namespace == "" {
		namespace = mcpDefaultNamespace
	}

	current, err := mcpLoadProjectSummary(e.App, scope.writeOwner, namespace)
	if err != nil {
		return nil, err
	}
	since := ""
	if current != nil {
		since, _ = current.metadata()["consolidatedUntil"].(string)
	}

	sessionTag, _ := json.Marshal([]string{mcpSessionTag})
	params := dbx.Params{"owner": scope.writeOwner, "namespace": namespace, "tag": string(sessionTag), "limit": clampInt(payload.MaxSessions, mcpConsolidateMaxSessions, 200)}
	where := "[[owner]] = {:owner} AND [[namespace]] = {:namespace} AND [[tags]] @> {:tag}::jsonb"
	if since != "" && !payload.Force {
		params["since"] = since
		where += " AND [[updated]] > {:since}::timestamptz"
	}
	sessions := []*mcpMemoryRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY [[updated]] ASC LIMIT {:limit}", mcpMemoryColumns, mcpMemoriesTable, where,
	)).Bind(params).All(&sessions); err != nil {
		return nil, err
	}
	if len(sessions) == 0 && (current == nil || !payload.Force) {
		result := map[string]any{"project": namespace, "sessions": 0, "note": "No new sessions to consolidate."}
		if current != nil {
			result["summaryId"] = current.Id
		}
		return result, nil
	}

	var prompt strings.Builder
	prompt.WriteString("Project: " + namespace + "\n\nCurrent project summary:\n")
	if current != nil {
		prompt.WriteString(mcpStripSessionHeading(current.Content))
	} else {
		prompt.WriteString("(none yet)")
	}
	prompt.WriteString("\n\nNew session summaries (oldest first):\n")
	for _, s := range sessions {
		prompt.WriteString("\n### " + s.Title + " (" + mcpShortTime(s.Updated) + ")\n" + mcpTruncate(s.Content, 4000) + "\n")
	}

	chatArgs, _ := json.Marshal(map[string]any{
		"channel": payload.Channel, "model": payload.Model,
		"system": mcpConsolidateSystemPrompt + strings.ReplaceAll(mcpLanguageHint(prompt.String()), "the title, summary, facts, decisions and todos", "the project summary (including the section headings)"),
		"prompt": prompt.String(), "temperature": 0, "maxTokens": 2048,
	})
	out, err := mcpLLMChat(e, chatArgs)
	if err != nil {
		return nil, mcpToolErr("LLM consolidation failed (configure a channel with llm_channel): " + err.Error())
	}
	res, _ := out.(map[string]any)
	summary, _ := res["content"].(string)
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil, mcpToolErr("The LLM returned an empty summary.")
	}

	consolidatedUntil := since
	if len(sessions) > 0 {
		consolidatedUntil = sessions[len(sessions)-1].Updated
	}
	title := "Project summary: " + namespace
	content := "# " + title + "\n\n" + summary
	tags := []string{mcpProjectSummaryTag}
	metadata := map[string]any{"consolidatedUntil": consolidatedUntil, "sessions": len(sessions)}
	if current != nil {
		if n, ok := current.metadata()["totalSessions"].(float64); ok {
			metadata["totalSessions"] = int(n) + len(sessions)
		} else {
			metadata["totalSessions"] = len(sessions)
		}
	} else {
		metadata["totalSessions"] = len(sessions)
	}

	saved, warning, err := mcpSaveMemory(e, scope, current, &mcpSaveInput{
		Namespace: namespace,
		Title:     &title,
		Content:   &content,
		Tags:      &tags,
		Metadata:  &metadata,
	}, nil)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"project":   namespace,
		"summaryId": saved.Id,
		"sessions":  len(sessions),
		"summary":   summary,
		"model":     res["model"],
	}
	if warning != "" {
		result["warning"] = warning
	}
	return result, nil
}

func mcpLoadProjectSummary(app core.App, owner, namespace string) (*mcpMemoryRow, error) {
	tag, _ := json.Marshal([]string{mcpProjectSummaryTag})
	rows := []*mcpMemoryRow{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[owner]] = {:owner} AND [[namespace]] = {:namespace} AND [[tags]] @> {:tag}::jsonb ORDER BY [[updated]] DESC LIMIT 1",
		mcpMemoryColumns, mcpMemoriesTable,
	)).Bind(dbx.Params{"owner": owner, "namespace": namespace, "tag": string(tag)}).All(&rows)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}
