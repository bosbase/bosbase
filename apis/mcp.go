package apis

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
)

// MCP (Model Context Protocol) server exposed over the "Streamable HTTP" transport.
//
// The server is stateless: every JSON-RPC request is answered with a single
// application/json response (no SSE stream is opened) and no session id is issued.
// Clients authenticate with a regular BosBase auth token:
//
//	Authorization: Bearer <token>
//
// Both regular auth records and superusers are accepted. Superusers get
// additional tools and can manage the shared knowledge base.

const (
	mcpLatestProtocolVersion = "2025-06-18"
	mcpServerName            = "bosbase"

	mcpErrParse          = -32700
	mcpErrInvalidRequest = -32600
	mcpErrMethodNotFound = -32601
	mcpErrInvalidParams  = -32602
	mcpErrInternal       = -32603
)

var mcpSupportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const mcpServerInstructions = `BosBase memory server: your persistent long-term memory, backed by PostgreSQL + pgvector.
Use it proactively: whatever you learn that may matter later (user preferences, facts, decisions, conventions, solutions, todos) should be stored here, and you should search it before answering questions that may depend on earlier knowledge.

Remembering context across conversations:
1. At the start of a conversation (and when the task or project changes) call memory_context with the user's request as query to recall what you know.
2. During the conversation store durable facts with memory_store (dedupe: true avoids duplicates); pin stable user preferences/identity and key project facts with pinned: true so they are always given to you.
3. At the end of a task or conversation call memory_remember with a summary, the facts learned, the decisions and the open todos. Mark finished todos with memory_update (add the "done" tag).
4. Long-term conversation memory: save the turns of important conversations with conversation_save (reuse the returned conversationId; long conversations are summarized automatically) and use conversation_recall to find what was said in past conversations. memory_consolidate condenses the sessions of a project into its long-term project summary.
Never store secrets (passwords, API keys, tokens).
- Memories are organised in projects. Pass "project" (eg. the repository or topic name) when storing, searching and listing; memory_projects lists the existing ones.
- memory_store saves a memory; prefer small atomic memories (one fact/decision each) with a short title and tags. Pass "relations" to link it to existing memories.
- memory_search finds memories by meaning (with keyword fallback); set expandDepth to also get related memories.
- memory_update / memory_delete keep the memory accurate: update outdated memories instead of storing duplicates.
- memory_link / memory_unlink maintain typed relations between memories (eg. "related_to", "part_of", "depends_on", "causes", "contradicts", "supersedes"), also across projects.
- memory_graph explores the neighbourhood of memories and memory_path finds how two memories are connected.
Privacy: everything you store is private to the authenticated user and isolated from other users. Personal memories are private; to share memories with other users use a shared memory space (space tool: create/join, the owner approves the members; pass space to the memory and note tools; every memory records its author). Knowledge bases (projects of notes) are private by default; public knowledge bases are managed by superusers (shared: true) and are readable by everyone (results marked shared: true).

Knowledge vault (Obsidian style): notes are markdown files with a vault relative path inside a project.
- note_write creates/edits notes (modes overwrite/create/append/prepend); frontmatter properties, #tags and [[wikilinks]] are parsed automatically and the links become graph edges ("links_to").
- note_read returns a note with its resolved links and backlinks; note_list browses folders and filters by tags/properties; note_backlinks also finds unlinked mentions.
- note_move renames a note and updates the links in the other notes; note_delete deletes it; note_history / note_restore give access to the previous versions.
- note_daily is the daily journal; note_tags lists the tags. memory_search, memory_graph and memory_path also work on notes.
- Knowledge graph: knowledge_associate retrieves related knowledge by association (links, backlinks, typed relations, semantic neighbours, shared tags) and explains each path; note_graph shows the graph of a vault (hubs, orphans, clusters); knowledge_suggest_links proposes (or creates) links between related notes.

Activity memory: every tool call is journaled. Call activity_recent at the beginning of a session to recall what you did before, and activity_log to record a summary of what you did, decided or plan next.

Object storage: storage_provider manages S3 compatible storages (AWS S3, Cloudflare R2, GCS, MinIO...), storage_upload / storage_presign_upload store files and storage_objects queries the upload history.
LLM relay: llm_channel manages third party LLM channels (stored encrypted), llm_chat / llm_embed call them through BosBase and llm_usage reports the token usage.
Diagnostics (superusers): diagnostics_health, diagnostics_errors, diagnostics_log_files and diagnostics_report help troubleshooting a failing instance.

BosBase backend API: the whole BosBase HTTP API is available with the same token and permissions.
- api_endpoints lists the endpoints you can call, api_docs returns the documentation of an endpoint (or a topic such as "overview" for the filter/sort syntax) and api_request calls any endpoint.
- records_list / record_get / record_create / record_update / record_delete are shortcuts for the record CRUD API (collection API rules apply).
- collections_list / collection_get (superusers) describe the collections schema.`

func bindMcpApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/mcp").Bind(RequireAuth())
	sub.POST("", mcpHandle)
	sub.GET("", mcpMethodNotAllowed)
	sub.DELETE("", mcpMethodNotAllowed)
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

func (r *mcpRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

func mcpMethodNotAllowed(e *core.RequestEvent) error {
	e.Response.Header().Set("Allow", http.MethodPost)
	return e.JSON(http.StatusMethodNotAllowed, mcpErrorResponse(nil, mcpErrInvalidRequest, "This MCP endpoint is stateless and only accepts POST requests.", nil))
}

func mcpErrorResponse(id json.RawMessage, code int, message string, data any) *mcpResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpRPCError{Code: code, Message: message, Data: data},
	}
}

func mcpHandle(e *core.RequestEvent) error {
	body, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return e.JSON(http.StatusBadRequest, mcpErrorResponse(nil, mcpErrParse, "Failed to read request body.", nil))
	}

	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return e.JSON(http.StatusBadRequest, mcpErrorResponse(nil, mcpErrInvalidRequest, "Empty request body.", nil))
	}

	// batch request (supported by the 2025-03-26 protocol revision)
	if body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			return e.JSON(http.StatusBadRequest, mcpErrorResponse(nil, mcpErrParse, "Invalid JSON.", nil))
		}
		if len(batch) == 0 {
			return e.JSON(http.StatusBadRequest, mcpErrorResponse(nil, mcpErrInvalidRequest, "Empty batch.", nil))
		}

		responses := make([]*mcpResponse, 0, len(batch))
		for _, raw := range batch {
			if resp := mcpDispatchRaw(e, raw); resp != nil {
				responses = append(responses, resp)
			}
		}
		if len(responses) == 0 {
			return e.NoContent(http.StatusAccepted)
		}
		return e.JSON(http.StatusOK, responses)
	}

	resp := mcpDispatchRaw(e, body)
	if resp == nil {
		return e.NoContent(http.StatusAccepted)
	}
	return e.JSON(http.StatusOK, resp)
}

// mcpDispatchRaw handles a single JSON-RPC message and returns nil for notifications/responses.
func mcpDispatchRaw(e *core.RequestEvent, raw json.RawMessage) *mcpResponse {
	req := &mcpRequest{}
	if err := json.Unmarshal(raw, req); err != nil {
		return mcpErrorResponse(nil, mcpErrParse, "Invalid JSON-RPC message.", nil)
	}

	// client responses (eg. to server initiated requests) are not used by this server
	if req.Method == "" {
		if req.isNotification() {
			return mcpErrorResponse(nil, mcpErrInvalidRequest, "Missing method.", nil)
		}
		return nil
	}

	if req.JSONRPC != "2.0" {
		if req.isNotification() {
			return nil
		}
		return mcpErrorResponse(req.ID, mcpErrInvalidRequest, "jsonrpc must be \"2.0\".", nil)
	}

	result, rpcErr := mcpDispatch(e, req)
	if req.isNotification() {
		return nil
	}
	if rpcErr != nil {
		return mcpErrorResponse(req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
	}

	return &mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func mcpDispatch(e *core.RequestEvent, req *mcpRequest) (any, *mcpRPCError) {
	switch req.Method {
	case "initialize":
		return mcpInitialize(e, req)
	case "ping":
		return map[string]any{}, nil
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		return nil, nil
	case "tools/list":
		return map[string]any{"tools": mcpToolsFor(e)}, nil
	case "tools/call":
		return mcpCallTool(e, req.Params)
	case "resources/list":
		return mcpResourcesList(), nil
	case "resources/read":
		return mcpResourcesRead(e, req.Params)
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []any{}}, nil
	case "prompts/list":
		return mcpPromptsList(), nil
	case "prompts/get":
		return mcpPromptsGet(e, req.Params)
	default:
		return nil, &mcpRPCError{Code: mcpErrMethodNotFound, Message: "Method not found: " + req.Method}
	}
}

// mcpInstructions returns the initialize instructions: the server usage plus
// what the AI remembers (memory context) and its latest actions.
func mcpInstructions(e *core.RequestEvent) string {
	instructions := mcpServerInstructions
	if mcpMemoryAutoContextEnabled() {
		instructions += mcpContextInstructions(e)
	}
	return instructions + mcpSpaceInstructions(e) + mcpActivityInstructions(e)
}

func mcpInitialize(e *core.RequestEvent, req *mcpRequest) (any, *mcpRPCError) {
	params := struct {
		ProtocolVersion string `json:"protocolVersion"`
	}{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Invalid initialize params."}
		}
	}

	version := mcpLatestProtocolVersion
	if slices.Contains(mcpSupportedProtocolVersions, params.ProtocolVersion) {
		version = params.ProtocolVersion
	}

	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"prompts":   map[string]any{"listChanged": false},
			"resources": map[string]any{"listChanged": false, "subscribe": false},
		},
		"serverInfo": map[string]any{
			"name":    mcpServerName,
			"version": "1.0.0",
		},
		"instructions": mcpInstructions(e),
	}, nil
}

// mcpAllTools returns all registered MCP tools.
func mcpAllTools() []mcpTool {
	tools := make([]mcpTool, 0, len(mcpMemoryTools)+len(mcpContextTools)+len(mcpConversationTools)+len(mcpKnowledgeTools)+len(mcpSpaceTools)+len(mcpChatTools)+len(mcpNoteTools)+len(mcpActivityTools)+len(mcpIntegrationTools)+len(mcpEmbeddingTools)+len(mcpDiagnosticsTools)+len(mcpApiTools))
	tools = append(tools, mcpMemoryTools...)
	tools = append(tools, mcpContextTools...)
	tools = append(tools, mcpConversationTools...)
	tools = append(tools, mcpKnowledgeTools...)
	tools = append(tools, mcpSpaceTools...)
	tools = append(tools, mcpChatTools...)
	tools = append(tools, mcpNoteTools...)
	tools = append(tools, mcpActivityTools...)
	tools = append(tools, mcpIntegrationTools...)
	tools = append(tools, mcpEmbeddingTools...)
	tools = append(tools, mcpDiagnosticsTools...)
	tools = append(tools, mcpApiTools...)
	return tools
}

// mcpTool describes a single MCP tool and its handler.
type mcpTool struct {
	Name          string
	Description   string
	InputSchema   map[string]any
	SuperuserOnly bool
	// NoScopeArgs disables the superuser ownerId/shared memory scope arguments.
	NoScopeArgs bool
	// ReadOnly marks tools that don't modify any data (readOnlyHint).
	ReadOnly bool
	// Destructive marks tools that permanently delete data (destructiveHint).
	Destructive bool
	// NoLog excludes the tool calls from the activity journal.
	NoLog   bool
	Handler func(e *core.RequestEvent, args json.RawMessage) (any, error)
}

func mcpToolsFor(e *core.RequestEvent) []map[string]any {
	superuser := e.HasSuperuserAuth()

	tools := mcpAllTools()
	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if t.SuperuserOnly && !superuser {
			continue
		}
		schema := t.InputSchema
		if !t.SuperuserOnly && !t.NoScopeArgs {
			schema = mcpWithSpaceProps(schema)
			if superuser {
				schema = mcpWithSuperuserProps(schema)
			}
		}
		result = append(result, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
			"annotations": map[string]any{
				"readOnlyHint":    t.ReadOnly,
				"destructiveHint": t.Destructive,
				"openWorldHint":   false,
			},
		})
	}

	return result
}

// mcpToolError is a tool execution error that is reported back to the model
// (isError=true) instead of as a protocol level error.
type mcpToolError struct {
	msg string
}

func (err *mcpToolError) Error() string { return err.msg }

func mcpToolErr(msg string) error { return &mcpToolError{msg: msg} }

func mcpCallTool(e *core.RequestEvent, rawParams json.RawMessage) (any, *mcpRPCError) {
	params := struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{}
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Name == "" {
		return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Invalid tools/call params."}
	}

	var tool *mcpTool
	tools := mcpAllTools()
	for i := range tools {
		if tools[i].Name == params.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil || (tool.SuperuserOnly && !e.HasSuperuserAuth()) {
		return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Unknown tool: " + params.Name}
	}

	args, err := mcpNormalizeArgs(params.Arguments)
	if err != nil {
		return nil, &mcpRPCError{Code: mcpErrInvalidParams, Message: "Tool arguments must be a JSON object."}
	}

	started := time.Now()
	out, err := tool.Handler(e, args)
	mcpRecordActivity(e, tool, args, out, err, time.Since(started))
	if err != nil {
		var toolErr *mcpToolError
		if !errors.As(err, &toolErr) {
			e.App.Logger().Error("MCP tool failure", "tool", params.Name, "error", err)
			err = mcpToolErr("Internal error while executing " + params.Name + ".")
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		}, nil
	}

	text, _ := json.MarshalIndent(out, "", "  ")
	result := map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
		"isError": false,
	}
	// structuredContent must be a JSON object
	if _, ok := out.(map[string]any); ok {
		result["structuredContent"] = out
	}

	return result, nil
}

// mcpWithSuperuserProps returns a copy of schema extended with the superuser-only arguments.
// mcpWithSpaceProps adds the shared memory space arguments.
func mcpWithSpaceProps(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	newProps := make(map[string]any, len(props)+2)
	for k, v := range props {
		newProps[k] = v
	}
	newProps["space"] = map[string]any{
		"type":        "string",
		"description": "Shared memory space (id or name) to operate in instead of your private memory (you must be an active member).",
	}
	if _, ok := props["includeSpaces"]; !ok {
		newProps["includeSpaces"] = map[string]any{
			"type":        "boolean",
			"description": "When not operating in a space: also read the memories of the spaces you belong to (default true).",
		}
	}
	newSchema := make(map[string]any, len(schema))
	for k, v := range schema {
		newSchema[k] = v
	}
	newSchema["properties"] = newProps
	return newSchema
}

func mcpWithSuperuserProps(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)

	newProps := make(map[string]any, len(props)+2)
	for k, v := range props {
		newProps[k] = v
	}
	newProps["ownerId"] = map[string]any{
		"type":        "string",
		"description": "Superuser only: act on the private memory space of this auth record id instead of your own.",
	}
	newProps["shared"] = map[string]any{
		"type":        "boolean",
		"description": "Superuser only: operate on the public knowledge base (notes readable by all users) instead of a private space. Memories can't be shared: only notes (with a path) can be written there.",
	}

	newSchema := make(map[string]any, len(schema))
	for k, v := range schema {
		newSchema[k] = v
	}
	newSchema["properties"] = newProps

	return newSchema
}

// mcpNormalizeArgs makes sure the tool arguments are a JSON object and maps
// the user facing "project" argument to the internal "namespace" storage field.
func mcpNormalizeArgs(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}"), nil
	}

	args := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}

	project, ok := args["project"]
	if !ok {
		return raw, nil
	}
	if _, exists := args["namespace"]; !exists {
		args["namespace"] = project
	}
	delete(args, "project")

	return json.Marshal(args)
}
