package apis

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/bosbase/bosbase-enterprise/core"
)

// MCP tools for the object storages and the LLM relay.
//
// They are thin wrappers around the /api/storage and /api/llm HTTP APIs
// (dispatched in-process), so permissions, validation and history are shared.

var mcpIntegrationTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	object := func(desc string) map[string]any { return map[string]any{"type": "object", "description": desc} }
	strArr := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	actions := func(list ...string) map[string]any { return map[string]any{"type": "string", "enum": list} }
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	mcpIntegrationTools = []mcpTool{
		{
			Name: "storage_provider",
			Description: "Manage the S3 compatible object storage providers (AWS S3, Cloudflare R2, Google Cloud Storage HMAC, MinIO, Backblaze B2...). " +
				"Secrets are stored encrypted and never returned. Actions: list, get, create, update, delete, test (upload/head/delete a probe object).",
			InputSchema: obj(map[string]any{
				"action":         actions("list", "get", "create", "update", "delete", "test"),
				"id":             str("Provider id (get/update/delete/test; test also accepts the name)."),
				"name":           str("Unique provider name."),
				"type":           actions(storageProviderTypes...),
				"endpoint":       str("S3 endpoint, eg. https://<account>.r2.cloudflarestorage.com (optional for aws/gcs)."),
				"region":         str("Region (\"auto\" for R2/GCS)."),
				"bucket":         str("Bucket name."),
				"accessKey":      str("Access key id."),
				"secretKey":      str("Secret access key."),
				"forcePathStyle": boolean("Use path style URLs (MinIO)."),
				"publicUrl":      str("Optional public base URL of the bucket (CDN/custom domain) used to build permanent object URLs."),
				"publicRead":     boolean("The bucket objects are publicly readable: the direct object URL is returned as permanent link."),
				"prefix":         str("Optional key prefix for all uploads."),
				"isDefault":      boolean("Use it when no provider is specified."),
				"enabled":        boolean("Enable/disable the provider."),
				"shared":         boolean("Superusers only: create a provider shared with all users."),
				"ownerId":        str("Superusers only: create the provider for another auth record."),
			}, "action"),
			NoScopeArgs: true,
			Handler:     mcpStorageProvider,
		},
		{
			Name:        "storage_upload",
			Description: "Upload a file (image, video, document...) to an object storage provider and record it in the upload history. Returns \"url\": a link usable from the internet (permanent when the provider has a public/CDN URL or a public bucket, otherwise a presigned link valid up to 7 days, see urlType/urlExpiresAt). For files larger than a few MB use storage_presign_upload instead.",
			InputSchema: obj(map[string]any{
				"provider":          str("Provider id or name (the default provider when empty)."),
				"filename":          str("File name."),
				"contentBase64":     str("Base64 encoded file content."),
				"text":              str("Plain text content (alternative to contentBase64)."),
				"contentType":       str("MIME type (detected when empty)."),
				"key":               str("Explicit object key (generated as <prefix>/YYYY/MM/DD/<random>_<filename> when empty)."),
				"prefix":            str("Folder prefix for the generated key."),
				"metadata":          object("Custom JSON metadata stored in the history."),
				"urlExpiresSeconds": integer("Validity of the returned presigned link when the bucket isn't public (default 7 days, max 604800)."),
			}, "filename"),
			NoScopeArgs: true,
			Handler:     mcpStorageUpload,
		},
		{
			Name:        "storage_presign_upload",
			Description: "Get a presigned PUT URL to upload a (large) file directly to the object storage, then call storage_objects with action complete.",
			InputSchema: obj(map[string]any{
				"provider":       str("Provider id or name (the default provider when empty)."),
				"filename":       str("File name."),
				"contentType":    str("MIME type."),
				"key":            str("Explicit object key."),
				"prefix":         str("Folder prefix for the generated key."),
				"expiresSeconds": integer("URL validity (default 3600)."),
				"metadata":       object("Custom JSON metadata."),
			}, "filename"),
			NoScopeArgs: true,
			Handler:     mcpStoragePresign,
		},
		{
			Name: "storage_objects",
			Description: "Query and manage the upload history. Actions: list (filters provider/status/prefix/contentType/search), " +
				"get (with a temporary download URL), complete (finalize a presigned upload), delete (removes the remote object, keeps the history).",
			InputSchema: obj(map[string]any{
				"action":         actions("list", "get", "complete", "delete"),
				"id":             str("Object id (get/complete/delete)."),
				"provider":       str("Filter by provider id or name."),
				"status":         actions("pending", "uploaded", "deleted"),
				"prefix":         str("Filter by key prefix."),
				"contentType":    str("Filter by content type prefix, eg. image/ or video/."),
				"search":         str("Search in the file names and keys."),
				"includeDeleted": boolean("Include deleted objects in list."),
				"page":           integer("Page (list)."),
				"perPage":        integer("Items per page (list)."),
				"all":            boolean("Superusers only: list the objects of all users."),
			}, "action"),
			NoScopeArgs: true,
			Handler:     mcpStorageObjects,
		},
		{
			Name: "llm_channel",
			Description: "Manage the third party LLM channels used by the BosBase LLM relay (OpenAI compatible APIs such as OpenAI, OpenRouter, DeepSeek, Qwen, Groq, Ollama, vLLM, Gemini; or Anthropic). " +
				"API keys are stored encrypted and never returned. Actions: list, get, create, update, delete, test.",
			InputSchema: obj(map[string]any{
				"action":       actions("list", "get", "create", "update", "delete", "test"),
				"id":           str("Channel id (get/update/delete/test; test also accepts the name)."),
				"name":         str("Unique channel name."),
				"type":         actions(llmChannelTypes...),
				"baseUrl":      str("API base URL including the version, eg. https://api.openai.com/v1, https://openrouter.ai/api/v1, https://api.anthropic.com/v1."),
				"apiKey":       str("Provider API key."),
				"headers":      object("Extra headers sent upstream (stored encrypted)."),
				"models":       strArr("Optional allow list of models (also used to route requests by model)."),
				"defaultModel": str("Model used when the request doesn't specify one."),
				"isDefault":    boolean("Use it when no channel is specified."),
				"enabled":      boolean("Enable/disable the channel."),
				"shared":       boolean("Superusers only: create a channel shared with all users."),
				"ownerId":      str("Superusers only: create the channel for another auth record."),
			}, "action"),
			NoScopeArgs: true,
			Handler:     mcpLLMChannel,
		},
		{
			Name:        "llm_chat",
			Description: "Call a chat model through a configured LLM channel and return the answer (usage is recorded).",
			InputSchema: obj(map[string]any{
				"channel":     str("Channel id or name (default/model based routing when empty)."),
				"model":       str("Model name (the channel default model when empty)."),
				"prompt":      str("User prompt (alternative to messages)."),
				"system":      str("Optional system prompt."),
				"messages":    map[string]any{"type": "array", "description": "Chat messages [{role, content}].", "items": map[string]any{"type": "object"}},
				"temperature": map[string]any{"type": "number"},
				"maxTokens":   integer("Max output tokens."),
				"extra":       object("Extra provider specific request fields merged into the request body."),
			}),
			NoScopeArgs: true,
			Handler:     mcpLLMChat,
		},
		{
			Name:        "llm_embed",
			Description: "Create embeddings through a configured OpenAI compatible LLM channel.",
			InputSchema: obj(map[string]any{
				"channel": str("Channel id or name."),
				"model":   str("Embedding model, eg. text-embedding-3-small."),
				"input":   map[string]any{"description": "Text or array of texts."},
			}, "input"),
			NoScopeArgs: true,
			Handler:     mcpLLMEmbed,
		},
		{
			Name:        "llm_usage",
			Description: "LLM relay usage: aggregated tokens/requests per channel and model (action usage) or the individual request log (action requests).",
			InputSchema: obj(map[string]any{
				"action":     actions("usage", "requests"),
				"channel":    str("Filter by channel id or name."),
				"model":      str("Filter by model."),
				"since":      str("Only requests after this time (RFC3339 or YYYY-MM-DD)."),
				"errorsOnly": boolean("Only failed requests."),
				"page":       integer("Page (requests)."),
				"perPage":    integer("Items per page (requests)."),
				"all":        boolean("Superusers only: include all users."),
			}),
			ReadOnly:    true,
			NoScopeArgs: true,
			Handler:     mcpLLMUsage,
		},
	}
}

// mcpSplitAction extracts the action/id arguments and returns the remaining fields as body.
func mcpSplitAction(args json.RawMessage) (string, string, map[string]any, error) {
	body := map[string]any{}
	if err := json.Unmarshal(args, &body); err != nil {
		return "", "", nil, mcpToolErr("Invalid arguments.")
	}
	action, _ := body["action"].(string)
	id, _ := body["id"].(string)
	delete(body, "action")
	delete(body, "id")
	return strings.ToLower(strings.TrimSpace(action)), strings.TrimSpace(id), body, nil
}

func mcpDispatchJSON(e *core.RequestEvent, method, path string, query map[string]any, body any) (any, error) {
	call := &mcpApiCall{Method: method, Path: path, Query: query}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		call.Body = raw
	}
	resp, err := mcpApiDispatch(e, call)
	if err != nil {
		return nil, err
	}
	return mcpApiResult(resp)
}

func mcpQueryFromBody(body map[string]any, keys ...string) map[string]any {
	query := map[string]any{}
	for _, k := range keys {
		if v, ok := body[k]; ok && v != nil && v != "" && v != false {
			query[k] = v
		}
	}
	return query
}

func mcpRequireId(id, action string) error {
	if id == "" {
		return mcpToolErr("id is required for action " + action + ".")
	}
	return nil
}

func mcpStorageProvider(e *core.RequestEvent, args json.RawMessage) (any, error) {
	action, id, body, err := mcpSplitAction(args)
	if err != nil {
		return nil, err
	}
	base := "/api/storage/providers"

	switch action {
	case "list":
		return mcpDispatchJSON(e, http.MethodGet, base, nil, nil)
	case "create":
		return mcpDispatchJSON(e, http.MethodPost, base, nil, body)
	case "get", "update", "delete", "test":
		if err := mcpRequireId(id, action); err != nil {
			return nil, err
		}
		p := base + "/" + url.PathEscape(id)
		switch action {
		case "get":
			return mcpDispatchJSON(e, http.MethodGet, p, nil, nil)
		case "update":
			return mcpDispatchJSON(e, http.MethodPatch, p, nil, body)
		case "delete":
			if _, err := mcpDispatchJSON(e, http.MethodDelete, p, nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true, "id": id}, nil
		default:
			return mcpDispatchJSON(e, http.MethodPost, p+"/test", nil, nil)
		}
	default:
		return nil, mcpToolErr("action must be one of list, get, create, update, delete, test.")
	}
}

func mcpStorageUpload(e *core.RequestEvent, args json.RawMessage) (any, error) {
	body := map[string]any{}
	if err := mcpBindArgs(args, &body); err != nil {
		return nil, err
	}
	return mcpDispatchJSON(e, http.MethodPost, "/api/storage/objects", nil, body)
}

func mcpStoragePresign(e *core.RequestEvent, args json.RawMessage) (any, error) {
	body := map[string]any{}
	if err := mcpBindArgs(args, &body); err != nil {
		return nil, err
	}
	return mcpDispatchJSON(e, http.MethodPost, "/api/storage/objects/presign", nil, body)
}

func mcpStorageObjects(e *core.RequestEvent, args json.RawMessage) (any, error) {
	action, id, body, err := mcpSplitAction(args)
	if err != nil {
		return nil, err
	}
	base := "/api/storage/objects"

	switch action {
	case "list":
		return mcpDispatchJSON(e, http.MethodGet, base,
			mcpQueryFromBody(body, "provider", "status", "prefix", "contentType", "search", "includeDeleted", "page", "perPage", "all"), nil)
	case "get", "complete", "delete":
		if err := mcpRequireId(id, action); err != nil {
			return nil, err
		}
		p := base + "/" + url.PathEscape(id)
		switch action {
		case "get":
			return mcpDispatchJSON(e, http.MethodGet, p, nil, nil)
		case "complete":
			return mcpDispatchJSON(e, http.MethodPost, p+"/complete", nil, nil)
		default:
			if _, err := mcpDispatchJSON(e, http.MethodDelete, p, nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true, "id": id}, nil
		}
	default:
		return nil, mcpToolErr("action must be one of list, get, complete, delete.")
	}
}

func mcpLLMChannel(e *core.RequestEvent, args json.RawMessage) (any, error) {
	action, id, body, err := mcpSplitAction(args)
	if err != nil {
		return nil, err
	}
	base := "/api/llm/channels"

	switch action {
	case "list":
		return mcpDispatchJSON(e, http.MethodGet, base, nil, nil)
	case "create":
		return mcpDispatchJSON(e, http.MethodPost, base, nil, body)
	case "get", "update", "delete", "test":
		if err := mcpRequireId(id, action); err != nil {
			return nil, err
		}
		p := base + "/" + url.PathEscape(id)
		switch action {
		case "get":
			return mcpDispatchJSON(e, http.MethodGet, p, nil, nil)
		case "update":
			return mcpDispatchJSON(e, http.MethodPatch, p, nil, body)
		case "delete":
			if _, err := mcpDispatchJSON(e, http.MethodDelete, p, nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": true, "id": id}, nil
		default:
			return mcpDispatchJSON(e, http.MethodPost, p+"/test", nil, nil)
		}
	default:
		return nil, mcpToolErr("action must be one of list, get, create, update, delete, test.")
	}
}

func mcpLLMChat(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Channel     string           `json:"channel"`
		Model       string           `json:"model"`
		Prompt      string           `json:"prompt"`
		System      string           `json:"system"`
		Messages    []map[string]any `json:"messages"`
		Temperature *float64         `json:"temperature"`
		MaxTokens   int              `json:"maxTokens"`
		Extra       map[string]any   `json:"extra"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	messages := payload.Messages
	if payload.Prompt != "" {
		messages = append(messages, map[string]any{"role": "user", "content": payload.Prompt})
	}
	if len(messages) == 0 {
		return nil, mcpToolErr("prompt or messages is required.")
	}

	c, err := llmFindChannel(e, payload.Channel, payload.Model)
	if err != nil {
		return nil, mcpToolErr(err.Error())
	}

	body := map[string]any{}
	for k, v := range payload.Extra {
		body[k] = v
	}
	if payload.Model != "" {
		body["model"] = payload.Model
	}
	if payload.Temperature != nil {
		body["temperature"] = *payload.Temperature
	}
	body["stream"] = false
	body["channel"] = c.Id

	endpoint := "/api/llm/v1/chat/completions"
	if c.Type == "anthropic" {
		endpoint = "/api/llm/v1/messages"
		if payload.System != "" {
			body["system"] = payload.System
		}
		maxTokens := payload.MaxTokens
		if maxTokens <= 0 {
			maxTokens = 1024
		}
		body["max_tokens"] = maxTokens
		body["messages"] = messages
	} else {
		if payload.System != "" {
			messages = append([]map[string]any{{"role": "system", "content": payload.System}}, messages...)
		}
		if payload.MaxTokens > 0 {
			body["max_tokens"] = payload.MaxTokens
		}
		body["messages"] = messages
	}

	out, err := mcpDispatchJSON(e, http.MethodPost, endpoint, nil, body)
	if err != nil {
		return nil, err
	}
	raw, _ := out.(map[string]any)

	result := map[string]any{"channel": c.Name, "model": raw["model"], "usage": raw["usage"]}
	if c.Type == "anthropic" {
		var text strings.Builder
		if blocks, ok := raw["content"].([]any); ok {
			for _, b := range blocks {
				if block, ok := b.(map[string]any); ok && block["type"] == "text" {
					text.WriteString(cast2string(block["text"]))
				}
			}
		}
		result["content"] = text.String()
		result["finishReason"] = raw["stop_reason"]
	} else if choices, ok := raw["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				result["content"] = msg["content"]
				if tc, ok := msg["tool_calls"]; ok && tc != nil {
					result["toolCalls"] = tc
				}
			}
			result["finishReason"] = choice["finish_reason"]
		}
	}

	return result, nil
}

func cast2string(v any) string {
	s, _ := v.(string)
	return s
}

func mcpLLMEmbed(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Channel string `json:"channel"`
		Model   string `json:"model"`
		Input   any    `json:"input"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if payload.Input == nil || payload.Input == "" {
		return nil, mcpToolErr("input is required.")
	}

	body := map[string]any{"input": payload.Input}
	if payload.Model != "" {
		body["model"] = payload.Model
	}
	if payload.Channel != "" {
		body["channel"] = payload.Channel
	}

	out, err := mcpDispatchJSON(e, http.MethodPost, "/api/llm/v1/embeddings", nil, body)
	if err != nil {
		return nil, err
	}
	raw, _ := out.(map[string]any)

	embeddings := []any{}
	dims := 0
	if data, ok := raw["data"].([]any); ok {
		for _, d := range data {
			if item, ok := d.(map[string]any); ok {
				if vec, ok := item["embedding"].([]any); ok {
					embeddings = append(embeddings, vec)
					dims = len(vec)
				}
			}
		}
	}

	return map[string]any{
		"model":      raw["model"],
		"dimensions": dims,
		"embeddings": embeddings,
		"usage":      raw["usage"],
	}, nil
}

func mcpLLMUsage(e *core.RequestEvent, args json.RawMessage) (any, error) {
	action, _, body, err := mcpSplitAction(args)
	if err != nil {
		return nil, err
	}
	query := mcpQueryFromBody(body, "channel", "model", "since", "errorsOnly", "page", "perPage", "all")

	switch action {
	case "", "usage":
		return mcpDispatchJSON(e, http.MethodGet, "/api/llm/usage", query, nil)
	case "requests":
		return mcpDispatchJSON(e, http.MethodGet, "/api/llm/requests", query, nil)
	default:
		return nil, mcpToolErr("action must be usage or requests.")
	}
}
