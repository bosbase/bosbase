package apis

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/security"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/spf13/cast"
)

// Group chat for AI agents (and users) on top of the chat_rooms/chat_messages
// collections: the MCP tools use the records API (so the API rules apply) and
// the realtime subscriptions push the messages to the subscribed clients.
// chat_read can wait (long poll) for the next message of a room.
//
// Superusers can also create user accounts (eg. for agents) and get back the
// email, the password and an auth token (POST /api/admin/users, MCP user_create).

const (
	chatRoomsCollection    = "chat_rooms"
	chatMessagesCollection = "chat_messages"

	chatDefaultReadLimit = 50
	chatMaxReadLimit     = 200
	chatMaxWaitSeconds   = 60

	adminUserGeneratedDomain = "bosbase.local"
	adminPasswordAlphabet    = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// chatNotifier wakes up the chat_read calls waiting for a new message of a room.
type chatNotifier struct {
	mu      sync.Mutex
	waiters map[string][]chan struct{}
}

func (n *chatNotifier) subscribe(room string) (chan struct{}, func()) {
	ch := make(chan struct{})
	n.mu.Lock()
	n.waiters[room] = append(n.waiters[room], ch)
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		list := n.waiters[room]
		for i, c := range list {
			if c == ch {
				n.waiters[room] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(n.waiters[room]) == 0 {
			delete(n.waiters, room)
		}
	}
}

func (n *chatNotifier) notify(room string) {
	n.mu.Lock()
	list := n.waiters[room]
	delete(n.waiters, room)
	n.mu.Unlock()
	for _, ch := range list {
		close(ch)
	}
}

const chatNotifierStoreKey = "bosbaseChatNotifier"

func getChatNotifier(app core.App) *chatNotifier {
	n, _ := app.Store().Get(chatNotifierStoreKey).(*chatNotifier)
	return n
}

func bindChatApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	// one notifier (and hook) per app
	app.Store().GetOrSet(chatNotifierStoreKey, func() any {
		n := &chatNotifier{waiters: map[string][]chan struct{}{}}
		app.OnRecordAfterCreateSuccess(chatMessagesCollection).BindFunc(func(e *core.RecordEvent) error {
			n.notify(e.Record.GetString("room"))
			return e.Next()
		})
		return n
	})

	rg.POST("/admin/users", adminUserCreate).Bind(RequireSuperuserAuth())
}

// -------------------------------------------------------------------
// Users created by superusers
// -------------------------------------------------------------------

func adminUserCreate(e *core.RequestEvent) error {
	payload := struct {
		Email         string `json:"email"`
		Password      string `json:"password"`
		Name          string `json:"name"`
		Collection    string `json:"collection"`
		Verified      *bool  `json:"verified"`
		TokenDuration int    `json:"tokenDuration"`
	}{}
	if err := e.BindBody(&payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}

	collectionName := strings.TrimSpace(payload.Collection)
	if collectionName == "" {
		collectionName = "users"
	}
	collection, err := e.App.FindCollectionByNameOrId(collectionName)
	if err != nil || !collection.IsAuth() {
		return e.BadRequestError("collection must be an existing auth collection.", nil)
	}
	if collection.Name == core.CollectionNameSuperusers {
		return e.BadRequestError("This endpoint creates regular users (not superusers).", nil)
	}

	email := strings.ToLower(strings.TrimSpace(payload.Email))
	if email == "" {
		email = "agent-" + security.RandomStringWithAlphabet(10, "abcdefghijklmnopqrstuvwxyz0123456789") + "@" + adminUserGeneratedDomain
	}
	password := payload.Password
	generated := password == ""
	if generated {
		password = security.RandomStringWithAlphabet(20, adminPasswordAlphabet)
	}

	record := core.NewRecord(collection)
	record.SetEmail(email)
	record.SetPassword(password)
	record.SetVerified(payload.Verified == nil || *payload.Verified)
	if name := strings.TrimSpace(payload.Name); name != "" && collection.Fields.GetByName("name") != nil {
		record.Set("name", name)
	}
	if err := e.App.Save(record); err != nil {
		if strings.Contains(err.Error(), "duplicate key") && strings.Contains(err.Error(), "email") {
			return e.BadRequestError("A user with the email "+strconv.Quote(email)+" already exists.", validation.Errors{
				"email": validation.NewError("validation_not_unique", "Value must be unique."),
			})
		}
		return e.BadRequestError("Failed to create the user.", err)
	}

	var token string
	expires := time.Duration(collection.AuthToken.Duration) * time.Second
	if payload.TokenDuration > 0 {
		expires = time.Duration(payload.TokenDuration) * time.Second
		token, err = record.NewStaticAuthToken(expires)
	} else {
		token, err = record.NewAuthToken()
	}
	if err != nil {
		return e.InternalServerError("The user was created but the token generation failed.", err)
	}

	e.Response.Header().Set("Cache-Control", "no-store")
	return e.JSON(http.StatusOK, map[string]any{
		"id":                record.Id,
		"collection":        collection.Name,
		"email":             email,
		"password":          password,
		"passwordGenerated": generated,
		"name":              record.GetString("name"),
		"verified":          record.Verified(),
		"token":             token,
		"tokenExpiresAt":    time.Now().Add(expires).UTC().Format(time.RFC3339),
		"record":            record,
	})
}

// -------------------------------------------------------------------
// MCP tools
// -------------------------------------------------------------------

var mcpChatTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	strArr := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	enum := func(desc string, list ...string) map[string]any {
		return map[string]any{"type": "string", "enum": list, "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	mcpChatTools = []mcpTool{
		{
			Name: "chat_room",
			Description: "Group chat rooms shared by AI agents and users (stored in the chat_rooms collection; apps can follow the messages with the realtime subscriptions). " +
				"Actions: create (you become the owner), list (your rooms with their last message), get, add_members / remove_members (owner), leave, delete (owner).",
			InputSchema: obj(map[string]any{
				"action":      enum("Action.", "create", "list", "get", "add_members", "remove_members", "leave", "delete"),
				"room":        str("Room id or name (get/add_members/remove_members/leave/delete)."),
				"name":        str("Room name (create)."),
				"description": str("Room description (create)."),
				"members":     strArr("Members: user ids or emails (create/add_members/remove_members)."),
			}, "action"),
			NoScopeArgs: true,
			Handler:     mcpChatRoom,
		},
		{
			Name:        "chat_send",
			Description: "Post a message in a chat room (as yourself). The members receive it through chat_read or the realtime subscriptions. Returns the message and its cursor.",
			InputSchema: obj(map[string]any{
				"room":     str("Room id or name."),
				"content":  str("Message text."),
				"replyTo":  str("Optional id of the message you reply to."),
				"mentions": strArr("Optional mentioned users (ids or emails)."),
				"metadata": map[string]any{"type": "object", "description": "Optional JSON metadata."},
			}, "room", "content"),
			NoScopeArgs: true,
			Handler:     mcpChatSend,
		},
		{
			Name: "chat_read",
			Description: "Read the messages of a chat room. Without after it returns the latest messages; with after (the cursor returned by chat_read/chat_send) only the newer ones. " +
				"wait (seconds, max 60) blocks until a new message arrives, so an agent can wait for the replies of the others.",
			InputSchema: obj(map[string]any{
				"room":    str("Room id or name."),
				"after":   str("Cursor: return only the messages after it."),
				"limit":   integer("Max messages (default 50, max 200)."),
				"wait":    integer("Seconds to wait for a new message when there is none (0-60, default 0)."),
				"skipOwn": boolean("Don't return your own messages (default false)."),
			}, "room"),
			ReadOnly:    true,
			NoScopeArgs: true,
			Handler:     mcpChatRead,
		},
		{
			Name: "user_create",
			Description: "Superusers: create a regular user account (eg. for an AI agent) and return its email, password and an auth token " +
				"(the token can be used directly as MCP Bearer token). Email and password are generated when omitted.",
			InputSchema: obj(map[string]any{
				"email":         str("Email (generated agent-xxx@bosbase.local when omitted)."),
				"password":      str("Password (a random 20 characters password is generated when omitted)."),
				"name":          str("Display name."),
				"collection":    str("Auth collection (default users)."),
				"verified":      boolean("Mark the email as verified (default true)."),
				"tokenDuration": integer("Validity of the returned token in seconds (default: the collection auth token duration)."),
			}),
			SuperuserOnly: true,
			NoScopeArgs:   true,
			NoLog:         true, // the result contains credentials
			Handler: func(e *core.RequestEvent, args json.RawMessage) (any, error) {
				body := map[string]any{}
				if err := mcpBindArgs(args, &body); err != nil {
					return nil, err
				}
				return mcpDispatchJSON(e, http.MethodPost, "/api/admin/users", nil, body)
			},
		},
	}
}

func chatFilterString(v string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

// chatResolveRoom returns the room record (map) visible to the request auth by id or name.
func chatResolveRoom(e *core.RequestEvent, ref string) (map[string]any, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, mcpToolErr("room is required.")
	}
	out, err := mcpDispatchJSON(e, http.MethodGet, "/api/collections/"+chatRoomsCollection+"/records", map[string]any{
		"filter":  "id = " + chatFilterString(ref) + " || name = " + chatFilterString(ref),
		"perPage": 2,
		"sort":    "-updated",
	}, nil)
	if err != nil {
		return nil, err
	}
	res, _ := out.(map[string]any)
	items, _ := res["items"].([]any)
	if len(items) == 0 {
		return nil, mcpToolErr("Room " + strconv.Quote(ref) + " was not found (or you are not a member).")
	}
	room, _ := items[0].(map[string]any)
	return room, nil
}

// chatResolveUsers resolves user ids or emails of the users collection.
func chatResolveUsers(app core.App, refs []string) ([]string, error) {
	ids := []string{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		var record *core.Record
		var err error
		if strings.Contains(ref, "@") {
			record, err = app.FindAuthRecordByEmail("users", ref)
		} else {
			record, err = app.FindRecordById("users", ref)
		}
		if err != nil {
			return nil, mcpToolErr("User " + strconv.Quote(ref) + " was not found.")
		}
		ids = append(ids, record.Id)
	}
	return ids, nil
}

// chatDisplayNames returns display names (name, or a short id) without exposing emails.
func chatDisplayNames(app core.App, ids []string) map[string]string {
	names := map[string]string{}
	for _, id := range ids {
		if _, ok := names[id]; ok || id == "" {
			continue
		}
		names[id] = "user-" + mcpTruncate(id, 8)
		if record, err := app.FindRecordById("users", id); err == nil {
			if n := strings.TrimSpace(record.GetString("name")); n != "" {
				names[id] = n
			}
		}
	}
	return names
}

func chatRoomSummary(e *core.RequestEvent, room map[string]any) map[string]any {
	members := cast.ToStringSlice(room["members"])
	owner := cast.ToString(room["owner"])
	names := chatDisplayNames(e.App, append([]string{owner}, members...))
	memberList := make([]map[string]any, 0, len(members))
	for _, id := range members {
		memberList = append(memberList, map[string]any{"id": id, "name": names[id]})
	}
	return map[string]any{
		"id":          room["id"],
		"name":        room["name"],
		"description": room["description"],
		"owner":       map[string]any{"id": owner, "name": names[owner]},
		"members":     memberList,
		"created":     room["created"],
	}
}

func mcpChatRoom(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Action      string   `json:"action"`
		Room        string   `json:"room"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Members     []string `json:"members"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if e.HasSuperuserAuth() && payload.Action != "list" && payload.Action != "get" {
		return nil, mcpToolErr("Chat rooms are used with regular user accounts (create one for each agent with user_create).")
	}
	base := "/api/collections/" + chatRoomsCollection + "/records"

	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "create":
		if strings.TrimSpace(payload.Name) == "" {
			return nil, mcpToolErr("name is required.")
		}
		members, err := chatResolveUsers(e.App, payload.Members)
		if err != nil {
			return nil, err
		}
		members = append([]string{e.Auth.Id}, members...)
		out, err := mcpDispatchJSON(e, http.MethodPost, base, nil, map[string]any{
			"name": strings.TrimSpace(payload.Name), "description": strings.TrimSpace(payload.Description),
			"owner": e.Auth.Id, "members": members,
		})
		if err != nil {
			return nil, err
		}
		room, _ := out.(map[string]any)
		return chatRoomSummary(e, room), nil

	case "list":
		out, err := mcpDispatchJSON(e, http.MethodGet, base, map[string]any{"sort": "-updated", "perPage": 50}, nil)
		if err != nil {
			return nil, err
		}
		res, _ := out.(map[string]any)
		items, _ := res["items"].([]any)
		rooms := make([]map[string]any, 0, len(items))
		for _, item := range items {
			room, _ := item.(map[string]any)
			summary := chatRoomSummary(e, room)
			if last, err := mcpDispatchJSON(e, http.MethodGet, "/api/collections/"+chatMessagesCollection+"/records", map[string]any{
				"filter": "room = " + chatFilterString(cast.ToString(room["id"])), "sort": "-created,-id", "perPage": 1, "skipTotal": 1,
			}, nil); err == nil {
				if lr, _ := last.(map[string]any); lr != nil {
					if msgs, _ := lr["items"].([]any); len(msgs) > 0 {
						msg, _ := msgs[0].(map[string]any)
						names := chatDisplayNames(e.App, []string{cast.ToString(msg["author"])})
						summary["lastMessage"] = map[string]any{
							"author": names[cast.ToString(msg["author"])], "content": mcpTruncate(cast.ToString(msg["content"]), 200), "created": msg["created"],
						}
					}
				}
			}
			rooms = append(rooms, summary)
		}
		return map[string]any{"rooms": rooms}, nil

	case "get":
		room, err := chatResolveRoom(e, payload.Room)
		if err != nil {
			return nil, err
		}
		return chatRoomSummary(e, room), nil

	case "add_members", "remove_members":
		room, err := chatResolveRoom(e, payload.Room)
		if err != nil {
			return nil, err
		}
		members, err := chatResolveUsers(e.App, payload.Members)
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			return nil, mcpToolErr("members is required.")
		}
		key := "members+"
		if payload.Action == "remove_members" {
			key = "members-"
		}
		out, err := mcpDispatchJSON(e, http.MethodPatch, base+"/"+url.PathEscape(cast.ToString(room["id"])), nil, map[string]any{key: members})
		if err != nil {
			return nil, err
		}
		updated, _ := out.(map[string]any)
		return chatRoomSummary(e, updated), nil

	case "leave":
		room, err := chatResolveRoom(e, payload.Room)
		if err != nil {
			return nil, err
		}
		if cast.ToString(room["owner"]) == e.Auth.Id {
			return nil, mcpToolErr("The owner can't leave the room (delete it instead).")
		}
		record, err := e.App.FindRecordById(chatRoomsCollection, cast.ToString(room["id"]))
		if err != nil {
			return nil, err
		}
		record.Set("members-", e.Auth.Id)
		if err := e.App.Save(record); err != nil {
			return nil, err
		}
		return map[string]any{"left": true, "room": room["name"]}, nil

	case "delete":
		room, err := chatResolveRoom(e, payload.Room)
		if err != nil {
			return nil, err
		}
		if _, err := mcpDispatchJSON(e, http.MethodDelete, base+"/"+url.PathEscape(cast.ToString(room["id"])), nil, nil); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "room": room["name"]}, nil

	default:
		return nil, mcpToolErr("Unknown action " + strconv.Quote(payload.Action) + ".")
	}
}

func chatMessageItem(msg map[string]any, names map[string]string) map[string]any {
	author := cast.ToString(msg["author"])
	item := map[string]any{
		"id":         msg["id"],
		"author":     author,
		"authorName": names[author],
		"content":    msg["content"],
		"created":    msg["created"],
		"cursor":     cast.ToString(msg["created"]) + "|" + cast.ToString(msg["id"]),
	}
	if r := cast.ToString(msg["replyTo"]); r != "" {
		item["replyTo"] = r
	}
	if m := cast.ToStringSlice(msg["mentions"]); len(m) > 0 {
		item["mentions"] = m
	}
	return item
}

func mcpChatSend(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Room     string         `json:"room"`
		Content  string         `json:"content"`
		ReplyTo  string         `json:"replyTo"`
		Mentions []string       `json:"mentions"`
		Metadata map[string]any `json:"metadata"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if e.HasSuperuserAuth() {
		return nil, mcpToolErr("Chat messages are posted with regular user accounts (create one for each agent with user_create).")
	}
	if strings.TrimSpace(payload.Content) == "" {
		return nil, mcpToolErr("content is required.")
	}
	room, err := chatResolveRoom(e, payload.Room)
	if err != nil {
		return nil, err
	}
	mentions, err := chatResolveUsers(e.App, payload.Mentions)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"room": room["id"], "author": e.Auth.Id, "content": payload.Content, "mentions": mentions}
	if payload.ReplyTo != "" {
		body["replyTo"] = payload.ReplyTo
	}
	if payload.Metadata != nil {
		body["metadata"] = payload.Metadata
	}
	out, err := mcpDispatchJSON(e, http.MethodPost, "/api/collections/"+chatMessagesCollection+"/records", nil, body)
	if err != nil {
		return nil, err
	}
	msg, _ := out.(map[string]any)
	item := chatMessageItem(msg, chatDisplayNames(e.App, []string{e.Auth.Id}))
	item["room"] = room["name"]
	return item, nil
}

func mcpChatRead(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Room    string `json:"room"`
		After   string `json:"after"`
		Limit   int    `json:"limit"`
		Wait    int    `json:"wait"`
		SkipOwn bool   `json:"skipOwn"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	room, err := chatResolveRoom(e, payload.Room)
	if err != nil {
		return nil, err
	}
	roomId := cast.ToString(room["id"])
	limit := clampInt(payload.Limit, chatDefaultReadLimit, chatMaxReadLimit)
	wait := min(max(payload.Wait, 0), chatMaxWaitSeconds)

	after := strings.TrimSpace(payload.After)
	var afterCreated, afterId string
	if after != "" {
		parts := strings.SplitN(after, "|", 2)
		if len(parts) != 2 {
			return nil, mcpToolErr("Invalid cursor (use the cursor returned by chat_read or chat_send).")
		}
		afterCreated, afterId = parts[0], parts[1]
	}

	query := func() ([]map[string]any, bool, error) {
		filter := "room = " + chatFilterString(roomId)
		if payload.SkipOwn {
			filter += " && author != " + chatFilterString(e.Auth.Id)
		}
		q := map[string]any{"perPage": limit + 1, "skipTotal": 1}
		if after != "" {
			filter += " && (created > " + chatFilterString(afterCreated) + " || (created = " + chatFilterString(afterCreated) + " && id > " + chatFilterString(afterId) + "))"
			q["sort"] = "created,id"
		} else {
			q["sort"] = "-created,-id" // latest messages
		}
		q["filter"] = filter
		out, err := mcpDispatchJSON(e, http.MethodGet, "/api/collections/"+chatMessagesCollection+"/records", q, nil)
		if err != nil {
			return nil, false, err
		}
		res, _ := out.(map[string]any)
		items, _ := res["items"].([]any)
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		msgs := make([]map[string]any, len(items))
		for i, it := range items {
			msgs[i], _ = it.(map[string]any)
		}
		if after == "" {
			// chronological order
			for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
				msgs[i], msgs[j] = msgs[j], msgs[i]
			}
		}
		return msgs, hasMore, nil
	}

	msgs, hasMore, err := query()
	if err != nil {
		return nil, err
	}
	waited := false
	if len(msgs) == 0 && wait > 0 {
		if after == "" {
			// empty room: wait for the messages posted from now on
			afterCreated, afterId = time.Now().UTC().Format("2006-01-02 15:04:05.000Z"), ""
			after = afterCreated + "|"
		}
		if n := getChatNotifier(e.App); n != nil {
			ch, cancel := n.subscribe(roomId)
			defer func() { cancel() }()
			// a message may have arrived between the query and the subscription
			if msgs, hasMore, err = query(); err != nil {
				return nil, err
			}
			deadline := time.NewTimer(time.Duration(wait) * time.Second)
			defer deadline.Stop()
		waitLoop:
			for len(msgs) == 0 {
				select {
				case <-ch:
					waited = true
					if msgs, hasMore, err = query(); err != nil {
						return nil, err
					}
					if len(msgs) == 0 { // eg. own message with skipOwn: keep waiting
						cancel()
						ch, cancel = n.subscribe(roomId)
					}
				case <-deadline.C:
					break waitLoop
				case <-e.Request.Context().Done():
					break waitLoop
				}
			}
		}
	}

	authors := []string{}
	for _, m := range msgs {
		authors = append(authors, cast.ToString(m["author"]))
	}
	names := chatDisplayNames(e.App, authors)
	items := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		items[i] = chatMessageItem(m, names)
	}

	cursor := after
	if len(items) > 0 {
		cursor = cast.ToString(items[len(items)-1]["cursor"])
	} else if cursor == "" {
		cursor = time.Now().UTC().Format("2006-01-02 15:04:05.000Z") + "|"
	}

	result := map[string]any{"room": room["name"], "roomId": roomId, "messages": items, "count": len(items), "cursor": cursor, "hasMore": hasMore}
	if wait > 0 {
		result["waited"] = waited
	}
	return result, nil
}
