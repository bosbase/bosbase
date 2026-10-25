package apis

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/spf13/cast"
)

// Shared memory spaces.
//
// A user creates a space (its owner) and other users request to join it; the
// owner (or an admin) approves the requests. The active members read and write
// the memories and notes of the space through the MCP tools (space argument) or
// the HTTP API, and every memory records its author.
//
// The memories of a space are stored in _mcp_memories with owner "space:<id>",
// so all the memory features (search, graph, notes, versions, context...) work
// inside a space. Personal memories stay private.

const (
	mcpSpacesTable       = "_mcp_spaces"
	mcpSpaceMembersTable = "_mcp_space_members"
	mcpSpaceOwnerPrefix  = "space:"

	mcpSpaceRoleOwner  = "owner"
	mcpSpaceRoleAdmin  = "admin"
	mcpSpaceRoleMember = "member"

	mcpSpaceStatusPending  = "pending"
	mcpSpaceStatusActive   = "active"
	mcpSpaceStatusRejected = "rejected"
)

type mcpSpaceMembership struct {
	SpaceId   string
	SpaceName string
	Role      string
	Status    string
}

func mcpSpaceOwnerKey(spaceId string) string { return mcpSpaceOwnerPrefix + spaceId }

func mcpSpaceIdFromOwner(owner string) string {
	if strings.HasPrefix(owner, mcpSpaceOwnerPrefix) {
		return strings.TrimPrefix(owner, mcpSpaceOwnerPrefix)
	}
	return ""
}

// mcpActiveSpaceKeys returns the owner keys of the spaces where userId is an active member.
func mcpActiveSpaceKeys(app core.App, userId string) []string {
	if userId == "" {
		return nil
	}
	ids := []string{}
	_ = app.DB().NewQuery(fmt.Sprintf(
		"SELECT [[space]] FROM {{%s}} WHERE [[member]] = {:member} AND [[status]] = {:status}", mcpSpaceMembersTable,
	)).Bind(dbx.Params{"member": userId, "status": mcpSpaceStatusActive}).Column(&ids)
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = mcpSpaceOwnerKey(id)
	}
	return keys
}

type mcpSpaceRow struct {
	Id              string `db:"id"`
	Name            string `db:"name"`
	Description     string `db:"description"`
	Owner           string `db:"owner"`
	OwnerCollection string `db:"ownerCollection"`
	Listed          bool   `db:"listed"`
	Created         string `db:"created"`
	Updated         string `db:"updated"`
}

const mcpSpaceColumns = `[[id]], [[name]], [[description]], [[owner]], [[ownerCollection]], [[listed]],
	[[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

type mcpMemberRow struct {
	Id               string `db:"id"`
	Space            string `db:"space"`
	Member           string `db:"member"`
	MemberCollection string `db:"memberCollection"`
	Role             string `db:"role"`
	Status           string `db:"status"`
	Message          string `db:"message"`
	ReviewedBy       string `db:"reviewedBy"`
	Created          string `db:"created"`
	Updated          string `db:"updated"`
}

const mcpMemberColumns = `[[id]], [[space]], [[member]], [[memberCollection]], [[role]], [[status]], [[message]], [[reviewedBy]],
	[[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

func mcpLoadSpace(app core.App, ref string) (*mcpSpaceRow, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("space is required")
	}
	s := &mcpSpaceRow{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[id]] = {:ref} OR LOWER([[name]]) = LOWER({:ref}) ORDER BY ([[id]] = {:ref}) DESC LIMIT 1",
		mcpSpaceColumns, mcpSpacesTable,
	)).Bind(dbx.Params{"ref": ref}).One(s)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func mcpLoadMember(app core.App, spaceId, member string) (*mcpMemberRow, error) {
	m := &mcpMemberRow{}
	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE [[space]] = {:space} AND [[member]] = {:member} LIMIT 1", mcpMemberColumns, mcpSpaceMembersTable,
	)).Bind(dbx.Params{"space": spaceId, "member": member}).One(m)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// mcpResolveSpaceMembership returns the active membership of the request auth in the space
// (superusers have access to every space).
func mcpResolveSpaceMembership(e *core.RequestEvent, ref string) (*mcpSpaceMembership, error) {
	space, err := mcpLoadSpace(e.App, ref)
	if err != nil {
		return nil, mcpToolErr("Space " + strconv.Quote(ref) + " was not found.")
	}
	if e.HasSuperuserAuth() {
		return &mcpSpaceMembership{SpaceId: space.Id, SpaceName: space.Name, Role: mcpSpaceRoleOwner, Status: mcpSpaceStatusActive}, nil
	}
	member, err := mcpLoadMember(e.App, space.Id, e.Auth.Id)
	if err != nil || member.Status != mcpSpaceStatusActive {
		status := "not a member"
		if err == nil {
			status = "membership " + member.Status
		}
		return nil, mcpToolErr("You don't have access to space " + strconv.Quote(space.Name) + " (" + status + "). Request to join it with the space tool (action join); the owner must approve.")
	}
	return &mcpSpaceMembership{SpaceId: space.Id, SpaceName: space.Name, Role: member.Role, Status: member.Status}, nil
}

// mcpSpaceScopeForRow returns the scope of the space owning row (the actor must be an active member).
func mcpSpaceScopeForRow(e *core.RequestEvent, scope *mcpScope, row *mcpMemoryRow) (*mcpScope, error) {
	spaceId := mcpSpaceIdFromOwner(row.Owner)
	if spaceId == "" {
		return nil, mcpToolErr("Memory " + strconv.Quote(row.Id) + " is not writable.")
	}
	membership, err := mcpResolveSpaceMembership(e, spaceId)
	if err != nil {
		return nil, err
	}
	spaceScope := *scope
	spaceScope.space = membership
	spaceScope.writeOwner = row.Owner
	spaceScope.ownerCollection = mcpSpacesTable
	spaceScope.memberSpaces = nil
	return &spaceScope, nil
}

// mcpScopeForMemoryId switches a personal scope to the space owning the memory id (if any).
func mcpScopeForMemoryId(e *core.RequestEvent, scope *mcpScope, id string) (*mcpScope, error) {
	if scope.isSpace() || len(scope.memberSpaces) == 0 || strings.TrimSpace(id) == "" {
		return scope, nil
	}
	if own, err := mcpLoadMemories(e.App, []string{id}, []string{scope.writeOwner}); err == nil && len(own) > 0 {
		return scope, nil
	}
	rows, err := mcpLoadMemories(e.App, []string{id}, scope.memberSpaces)
	if err != nil || len(rows) == 0 {
		return scope, nil
	}
	return mcpSpaceScopeForRow(e, scope, rows[0])
}

// mcpAuthorNames resolves display names of auth records (name, username or email).
func mcpAuthorNames(app core.App, ids []string) map[string]string {
	names := map[string]string{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := names[id]; ok {
			continue
		}
		names[id] = ""
		record, err := mcpFindAuthRecord(app, id)
		if err != nil {
			continue
		}
		for _, field := range []string{"name", "username"} {
			if v := strings.TrimSpace(record.GetString(field)); v != "" {
				names[id] = v
				break
			}
		}
		if names[id] == "" {
			names[id] = record.Email()
		}
	}
	return names
}

// mcpAnnotateAuthors adds the author/space display names to memory result items.
func mcpAnnotateAuthors(app core.App, items []map[string]any) {
	authorIds := []string{}
	spaceIds := []string{}
	for _, item := range items {
		if id, _ := item["author"].(string); id != "" {
			authorIds = append(authorIds, id)
		}
		if id, _ := item["updatedBy"].(string); id != "" {
			authorIds = append(authorIds, id)
		}
		if id, _ := item["space"].(string); id != "" {
			spaceIds = append(spaceIds, id)
		}
	}
	if len(authorIds) == 0 && len(spaceIds) == 0 {
		return
	}
	names := mcpAuthorNames(app, authorIds)
	spaceNames := map[string]string{}
	if len(spaceIds) > 0 {
		rows := []*mcpSpaceRow{}
		_ = app.DB().Select("id", "name").From(mcpSpacesTable).Where(dbx.In("id", toAnySlice(spaceIds)...)).All(&rows)
		for _, r := range rows {
			spaceNames[r.Id] = r.Name
		}
	}
	for _, item := range items {
		if id, _ := item["author"].(string); id != "" && names[id] != "" {
			item["authorName"] = names[id]
		}
		if id, _ := item["updatedBy"].(string); id != "" && names[id] != "" {
			item["updatedByName"] = names[id]
		}
		if id, _ := item["space"].(string); id != "" && spaceNames[id] != "" {
			item["spaceName"] = spaceNames[id]
		}
	}
}

// mcpSpaceInstructions lists the spaces of the user and the pending join requests to review.
func mcpSpaceInstructions(e *core.RequestEvent) string {
	if e.Auth == nil {
		return ""
	}
	spaces := []struct {
		Name string `db:"name"`
		Role string `db:"role"`
		Id   string `db:"id"`
	}{}
	_ = e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT s.[[id]], s.[[name]], m.[[role]] FROM {{%s}} m JOIN {{%s}} s ON s.[[id]] = m.[[space]]
		WHERE m.[[member]] = {:me} AND m.[[status]] = 'active' ORDER BY s.[[name]] LIMIT 50`, mcpSpaceMembersTable, mcpSpacesTable,
	)).Bind(dbx.Params{"me": e.Auth.Id}).All(&spaces)
	if len(spaces) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\nShared memory spaces you belong to (pass space to store/read there; memory_search includes them): ")
	moderated := []string{}
	for i, s := range spaces {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s.Name + " (" + s.Role + ")")
		if s.Role == mcpSpaceRoleOwner {
			moderated = append(moderated, s.Id)
		}
	}

	if len(moderated) > 0 {
		pending := []struct {
			Space   string `db:"space"`
			Member  string `db:"member"`
			Message string `db:"message"`
		}{}
		params := dbx.Params{}
		placeholders := make([]string, len(moderated))
		for i, id := range moderated {
			key := "s" + strconv.Itoa(i)
			params[key] = id
			placeholders[i] = "{:" + key + "}"
		}
		_ = e.App.DB().NewQuery(fmt.Sprintf(`
			SELECT s.[[name]] AS [[space]], m.[[member]], m.[[message]] FROM {{%s}} m JOIN {{%s}} s ON s.[[id]] = m.[[space]]
			WHERE m.[[status]] = 'pending' AND m.[[space]] IN (%s) ORDER BY m.[[created]] LIMIT 20`,
			mcpSpaceMembersTable, mcpSpacesTable, strings.Join(placeholders, ","),
		)).Bind(params).All(&pending)
		if len(pending) > 0 {
			ids := []string{}
			for _, p := range pending {
				ids = append(ids, p.Member)
			}
			names := mcpAuthorNames(e.App, ids)
			b.WriteString("\nPending requests to join your spaces (tell the user and review them with space_member approve/reject):")
			for _, p := range pending {
				line := "\n- " + names[p.Member] + " (" + p.Member + ") → " + p.Space
				if p.Message != "" {
					line += ": " + mcpTruncate(p.Message, 120)
				}
				b.WriteString(line)
			}
		}
	}
	return b.String()
}

// -------------------------------------------------------------------
// HTTP API
// -------------------------------------------------------------------

func bindMemorySpacesApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/memory/spaces").Bind(RequireAuth())

	sub.GET("", memorySpacesList)
	sub.POST("", memorySpaceCreate)
	sub.GET("/{space}", memorySpaceView)
	sub.PATCH("/{space}", memorySpaceUpdate)
	sub.DELETE("/{space}", memorySpaceDelete)
	sub.POST("/{space}/join", memorySpaceJoin)
	sub.POST("/{space}/leave", memorySpaceLeave)

	sub.GET("/{space}/members", memorySpaceMembers)
	sub.POST("/{space}/members/{member}/approve", memorySpaceReview(true))
	sub.POST("/{space}/members/{member}/reject", memorySpaceReview(false))
	sub.PATCH("/{space}/members/{member}", memorySpaceMemberUpdate)
	sub.DELETE("/{space}/members/{member}", memorySpaceMemberRemove)

	sub.GET("/{space}/memories", memorySpaceMemoriesList)
	sub.POST("/{space}/memories", memorySpaceMemoryCreate)
	sub.GET("/{space}/memories/{id}", memorySpaceMemoryView)
	sub.PATCH("/{space}/memories/{id}", memorySpaceMemoryUpdate)
	sub.DELETE("/{space}/memories/{id}", memorySpaceMemoryDelete)
}

func mcpCountMembers(app core.App, spaceId, status string) int {
	var n int
	_ = app.DB().NewQuery(fmt.Sprintf(
		"SELECT COUNT(*) FROM {{%s}} WHERE [[space]] = {:space} AND [[status]] = {:status}", mcpSpaceMembersTable,
	)).Bind(dbx.Params{"space": spaceId, "status": status}).Row(&n)
	return n
}

func memorySpacePublic(e *core.RequestEvent, s *mcpSpaceRow, my *mcpMemberRow) map[string]any {
	names := mcpAuthorNames(e.App, []string{s.Owner})
	result := map[string]any{
		"id":          s.Id,
		"name":        s.Name,
		"description": s.Description,
		"owner":       s.Owner,
		"ownerName":   names[s.Owner],
		"listed":      s.Listed,
		"members":     mcpCountMembers(e.App, s.Id, mcpSpaceStatusActive),
		"created":     s.Created,
	}
	if my != nil {
		result["myRole"] = my.Role
		result["myStatus"] = my.Status
		if my.Status == mcpSpaceStatusActive && my.Role == mcpSpaceRoleOwner {
			result["pendingRequests"] = mcpCountMembers(e.App, s.Id, mcpSpaceStatusPending)
		}
	} else if e.HasSuperuserAuth() {
		result["pendingRequests"] = mcpCountMembers(e.App, s.Id, mcpSpaceStatusPending)
	}
	return result
}

// memorySpaceAccess loads the space of the request with the membership of the request auth.
func memorySpaceAccess(e *core.RequestEvent) (*mcpSpaceRow, *mcpMemberRow, error) {
	s, err := mcpLoadSpace(e.App, e.Request.PathValue("space"))
	if err != nil {
		return nil, nil, e.NotFoundError("Space not found.", nil)
	}
	my, err := mcpLoadMember(e.App, s.Id, e.Auth.Id)
	if err != nil {
		my = nil
	}
	return s, my, nil
}

// memberIsOwner reports whether the request auth is the owner (creator) of the space:
// the owner decides who joins and who is removed.
func memberIsOwner(e *core.RequestEvent, my *mcpMemberRow) bool {
	if e.HasSuperuserAuth() {
		return true
	}
	return my != nil && my.Status == mcpSpaceStatusActive && my.Role == mcpSpaceRoleOwner
}

func memberCanModerate(e *core.RequestEvent, my *mcpMemberRow) bool {
	if e.HasSuperuserAuth() {
		return true
	}
	return my != nil && my.Status == mcpSpaceStatusActive && (my.Role == mcpSpaceRoleOwner || my.Role == mcpSpaceRoleAdmin)
}

func memorySpacesList(e *core.RequestEvent) error {
	q := e.Request.URL.Query()
	params := dbx.Params{"me": e.Auth.Id}
	var where string
	switch {
	case q.Get("discover") == "true":
		where = "s.[[listed]] = TRUE"
	case q.Get("all") == "true" && e.HasSuperuserAuth():
		where = "TRUE"
	default:
		where = "m.[[member]] IS NOT NULL"
	}
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		params["like"] = "%" + escapeLike(search) + "%"
		where += " AND (s.[[name]] ILIKE {:like} OR s.[[description]] ILIKE {:like})"
	}

	rows := []struct {
		mcpSpaceRow
		MyRole   *string `db:"myRole"`
		MyStatus *string `db:"myStatus"`
	}{}
	err := e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT s.[[id]], s.[[name]], s.[[description]], s.[[owner]], s.[[ownerCollection]], s.[[listed]],
			s.[[created]]::text AS [[created]], s.[[updated]]::text AS [[updated]], m.[[role]] AS [[myRole]], m.[[status]] AS [[myStatus]]
		FROM {{%s}} s LEFT JOIN {{%s}} m ON m.[[space]] = s.[[id]] AND m.[[member]] = {:me}
		WHERE %s ORDER BY LOWER(s.[[name]]) LIMIT 500`, mcpSpacesTable, mcpSpaceMembersTable, where,
	)).Bind(params).All(&rows)
	if err != nil {
		return e.InternalServerError("Failed to load the spaces.", err)
	}

	items := make([]map[string]any, len(rows))
	for i := range rows {
		r := rows[i]
		var my *mcpMemberRow
		if r.MyRole != nil {
			my = &mcpMemberRow{Role: *r.MyRole, Status: cast.ToString(r.MyStatus)}
		}
		items[i] = memorySpacePublic(e, &r.mcpSpaceRow, my)
		if r.Description != "" {
			items[i]["description"] = mcpTruncate(r.Description, 300)
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"items": items})
}

type memorySpacePayload struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Listed      *bool   `json:"listed"`
}

func validateSpaceName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if len([]rune(name)) > 64 {
		return errors.New("name must be at most 64 characters")
	}
	if strings.ContainsAny(name, "/\\?#%") || strings.HasPrefix(name, mcpSpaceOwnerPrefix) {
		return errors.New("name contains invalid characters")
	}
	return nil
}

func memorySpaceCreate(e *core.RequestEvent) error {
	payload := &memorySpacePayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	name := strings.TrimSpace(cast.ToString(payload.Name))
	if err := validateSpaceName(name); err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	var id string
	err := e.App.RunInTransaction(func(txApp core.App) error {
		if err := txApp.DB().NewQuery(fmt.Sprintf(`
			INSERT INTO {{%s}} ([[name]], [[description]], [[owner]], [[ownerCollection]], [[listed]])
			VALUES ({:name}, {:description}, {:owner}, {:ownerCollection}, {:listed}) RETURNING [[id]]`, mcpSpacesTable,
		)).Bind(dbx.Params{
			"name": name, "description": strings.TrimSpace(cast.ToString(payload.Description)),
			"owner": e.Auth.Id, "ownerCollection": e.Auth.Collection().Name, "listed": payload.Listed != nil && *payload.Listed,
		}).Row(&id); err != nil {
			return err
		}
		_, err := txApp.DB().NewQuery(fmt.Sprintf(`
			INSERT INTO {{%s}} ([[space]], [[member]], [[memberCollection]], [[role]], [[status]], [[reviewedBy]])
			VALUES ({:space}, {:member}, {:collection}, 'owner', 'active', {:member})`, mcpSpaceMembersTable,
		)).Bind(dbx.Params{"space": id, "member": e.Auth.Id, "collection": e.Auth.Collection().Name}).Execute()
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "idx__mcp_spaces_name") {
			return e.BadRequestError("A space named "+strconv.Quote(name)+" already exists.", nil)
		}
		return e.InternalServerError("Failed to create the space.", err)
	}

	s, _ := mcpLoadSpace(e.App, id)
	my, _ := mcpLoadMember(e.App, id, e.Auth.Id)
	return e.JSON(http.StatusOK, memorySpacePublic(e, s, my))
}

func memorySpaceView(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	if my == nil && !s.Listed && !e.HasSuperuserAuth() {
		return e.NotFoundError("Space not found.", nil)
	}
	return e.JSON(http.StatusOK, memorySpacePublic(e, s, my))
}

func memorySpaceUpdate(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	if !memberCanModerate(e, my) {
		return e.ForbiddenError("Only the space owner or an admin can update the space.", nil)
	}
	payload := &memorySpacePayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	if payload.Name != nil {
		name := strings.TrimSpace(*payload.Name)
		if err := validateSpaceName(name); err != nil {
			return e.BadRequestError(err.Error(), nil)
		}
		s.Name = name
	}
	if payload.Description != nil {
		s.Description = strings.TrimSpace(*payload.Description)
	}
	if payload.Listed != nil {
		s.Listed = *payload.Listed
	}
	if _, err := e.App.DB().NewQuery(fmt.Sprintf(
		"UPDATE {{%s}} SET [[name]] = {:name}, [[description]] = {:description}, [[listed]] = {:listed}, [[updated]] = NOW() WHERE [[id]] = {:id}", mcpSpacesTable,
	)).Bind(dbx.Params{"name": s.Name, "description": s.Description, "listed": s.Listed, "id": s.Id}).Execute(); err != nil {
		if strings.Contains(err.Error(), "idx__mcp_spaces_name") {
			return e.BadRequestError("A space named "+strconv.Quote(s.Name)+" already exists.", nil)
		}
		return e.InternalServerError("Failed to update the space.", err)
	}
	return e.JSON(http.StatusOK, memorySpacePublic(e, s, my))
}

func memorySpaceDelete(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	if !e.HasSuperuserAuth() && (my == nil || my.Role != mcpSpaceRoleOwner || my.Status != mcpSpaceStatusActive) {
		return e.ForbiddenError("Only the space owner can delete the space.", nil)
	}
	var deleted int64
	err = e.App.RunInTransaction(func(txApp core.App) error {
		res, err := txApp.DB().Delete(mcpMemoriesTable, dbx.HashExp{"owner": mcpSpaceOwnerKey(s.Id)}).Execute()
		if err != nil {
			return err
		}
		deleted, _ = res.RowsAffected()
		if _, err := txApp.DB().Delete(mcpEdgesTable, dbx.HashExp{"owner": mcpSpaceOwnerKey(s.Id)}).Execute(); err != nil {
			return err
		}
		_, err = txApp.DB().Delete(mcpSpacesTable, dbx.HashExp{"id": s.Id}).Execute()
		return err
	})
	if err != nil {
		return e.InternalServerError("Failed to delete the space.", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"deleted": true, "id": s.Id, "memories": deleted})
}

func memorySpaceJoin(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	payload := struct {
		Message string `json:"message"`
	}{}
	_ = e.BindBody(&payload)
	message := mcpTruncate(strings.TrimSpace(payload.Message), 500)

	switch {
	case my != nil && my.Status == mcpSpaceStatusActive:
		return e.BadRequestError("You are already a member of this space.", nil)
	case my != nil && my.Status == mcpSpaceStatusPending:
		return e.JSON(http.StatusOK, map[string]any{"space": s.Name, "status": mcpSpaceStatusPending, "note": "Your request is waiting for the approval of the space owner."})
	case my != nil:
		// rejected before: request again
		_, err = e.App.DB().NewQuery(fmt.Sprintf(
			"UPDATE {{%s}} SET [[status]] = 'pending', [[message]] = {:message}, [[reviewedBy]] = '', [[updated]] = NOW() WHERE [[id]] = {:id}", mcpSpaceMembersTable,
		)).Bind(dbx.Params{"message": message, "id": my.Id}).Execute()
	default:
		_, err = e.App.DB().NewQuery(fmt.Sprintf(`
			INSERT INTO {{%s}} ([[space]], [[member]], [[memberCollection]], [[role]], [[status]], [[message]])
			VALUES ({:space}, {:member}, {:collection}, 'member', 'pending', {:message})`, mcpSpaceMembersTable,
		)).Bind(dbx.Params{"space": s.Id, "member": e.Auth.Id, "collection": e.Auth.Collection().Name, "message": message}).Execute()
	}
	if err != nil {
		return e.InternalServerError("Failed to request to join the space.", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"space": s.Name, "spaceId": s.Id, "status": mcpSpaceStatusPending, "note": "Request sent: the space owner (or an admin) must approve it."})
}

func memorySpaceLeave(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	if my == nil {
		return e.BadRequestError("You are not a member of this space.", nil)
	}
	if my.Role == mcpSpaceRoleOwner {
		return e.BadRequestError("The owner can't leave the space: transfer the ownership (set_role owner) or delete the space.", nil)
	}
	if _, err := e.App.DB().Delete(mcpSpaceMembersTable, dbx.HashExp{"id": my.Id}).Execute(); err != nil {
		return e.InternalServerError("Failed to leave the space.", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"left": true, "space": s.Name})
}

func memorySpaceMembers(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	if !e.HasSuperuserAuth() && (my == nil || my.Status != mcpSpaceStatusActive) {
		return e.ForbiddenError("Only the members can see the members of the space.", nil)
	}
	where := "[[space]] = {:space} AND [[status]] = 'active'"
	if memberIsOwner(e, my) {
		where = "[[space]] = {:space}"
		if status := e.Request.URL.Query().Get("status"); status != "" {
			where += " AND [[status]] = {:status}"
		}
	}
	rows := []*mcpMemberRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY ([[status]] = 'pending') DESC, ([[role]] = 'owner') DESC, [[created]]", mcpMemberColumns, mcpSpaceMembersTable, where,
	)).Bind(dbx.Params{"space": s.Id, "status": e.Request.URL.Query().Get("status")}).All(&rows); err != nil {
		return e.InternalServerError("Failed to load the members.", err)
	}

	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.Member
	}
	names := mcpAuthorNames(e.App, ids)
	items := make([]map[string]any, len(rows))
	for i, r := range rows {
		items[i] = map[string]any{
			"member": r.Member, "name": names[r.Member], "role": r.Role, "status": r.Status, "created": r.Created,
		}
		if r.Message != "" {
			items[i]["message"] = r.Message
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"space": s.Name, "items": items})
}

func memorySpaceTargetMember(e *core.RequestEvent, s *mcpSpaceRow) (*mcpMemberRow, error) {
	target, err := mcpLoadMember(e.App, s.Id, e.Request.PathValue("member"))
	if err != nil {
		return nil, e.NotFoundError("Member (or join request) not found.", nil)
	}
	return target, nil
}

func memorySpaceReview(approve bool) func(e *core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		s, my, err := memorySpaceAccess(e)
		if err != nil {
			return err
		}
		if !memberIsOwner(e, my) {
			return e.ForbiddenError("Only the creator (owner) of the space can approve or reject the join requests.", nil)
		}
		target, err := memorySpaceTargetMember(e, s)
		if err != nil {
			return err
		}
		if target.Status != mcpSpaceStatusPending {
			return e.BadRequestError("There is no pending request for this user (status "+target.Status+").", nil)
		}
		status := mcpSpaceStatusRejected
		if approve {
			status = mcpSpaceStatusActive
		}
		if _, err := e.App.DB().NewQuery(fmt.Sprintf(
			"UPDATE {{%s}} SET [[status]] = {:status}, [[reviewedBy]] = {:me}, [[updated]] = NOW() WHERE [[id]] = {:id}", mcpSpaceMembersTable,
		)).Bind(dbx.Params{"status": status, "me": e.Auth.Id, "id": target.Id}).Execute(); err != nil {
			return e.InternalServerError("Failed to review the request.", err)
		}
		names := mcpAuthorNames(e.App, []string{target.Member})
		return e.JSON(http.StatusOK, map[string]any{"space": s.Name, "member": target.Member, "name": names[target.Member], "status": status})
	}
}

func memorySpaceMemberUpdate(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	isOwner := e.HasSuperuserAuth() || (my != nil && my.Role == mcpSpaceRoleOwner && my.Status == mcpSpaceStatusActive)
	if !isOwner {
		return e.ForbiddenError("Only the space owner can change the roles.", nil)
	}
	target, err := memorySpaceTargetMember(e, s)
	if err != nil {
		return err
	}
	if target.Status != mcpSpaceStatusActive {
		return e.BadRequestError("Only active members can get a role (approve the request first).", nil)
	}
	payload := struct {
		Role string `json:"role"`
	}{}
	if err := e.BindBody(&payload); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	role := strings.ToLower(strings.TrimSpace(payload.Role))
	if role != mcpSpaceRoleAdmin && role != mcpSpaceRoleMember && role != mcpSpaceRoleOwner {
		return e.BadRequestError("role must be admin, member or owner (ownership transfer).", nil)
	}
	if target.Role == mcpSpaceRoleOwner {
		return e.BadRequestError("Transfer the ownership to another member first.", nil)
	}

	err = e.App.RunInTransaction(func(txApp core.App) error {
		if role == mcpSpaceRoleOwner {
			// ownership transfer: the previous owner becomes admin
			if _, err := txApp.DB().NewQuery(fmt.Sprintf(
				"UPDATE {{%s}} SET [[role]] = 'admin', [[updated]] = NOW() WHERE [[space]] = {:space} AND [[role]] = 'owner'", mcpSpaceMembersTable,
			)).Bind(dbx.Params{"space": s.Id}).Execute(); err != nil {
				return err
			}
			if _, err := txApp.DB().NewQuery(fmt.Sprintf(
				"UPDATE {{%s}} SET [[owner]] = {:owner}, [[ownerCollection]] = {:collection}, [[updated]] = NOW() WHERE [[id]] = {:space}", mcpSpacesTable,
			)).Bind(dbx.Params{"owner": target.Member, "collection": target.MemberCollection, "space": s.Id}).Execute(); err != nil {
				return err
			}
		}
		_, err := txApp.DB().NewQuery(fmt.Sprintf(
			"UPDATE {{%s}} SET [[role]] = {:role}, [[updated]] = NOW() WHERE [[id]] = {:id}", mcpSpaceMembersTable,
		)).Bind(dbx.Params{"role": role, "id": target.Id}).Execute()
		return err
	})
	if err != nil {
		return e.InternalServerError("Failed to update the member.", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"space": s.Name, "member": target.Member, "role": role})
}

func memorySpaceMemberRemove(e *core.RequestEvent) error {
	s, my, err := memorySpaceAccess(e)
	if err != nil {
		return err
	}
	target, err := memorySpaceTargetMember(e, s)
	if err != nil {
		return err
	}
	if target.Member == e.Auth.Id {
		return memorySpaceLeave(e)
	}
	if !memberIsOwner(e, my) {
		return e.ForbiddenError("Only the creator (owner) of the space can remove members.", nil)
	}
	if target.Role == mcpSpaceRoleOwner {
		return e.BadRequestError("The owner can't be removed.", nil)
	}
	if _, err := e.App.DB().Delete(mcpSpaceMembersTable, dbx.HashExp{"id": target.Id}).Execute(); err != nil {
		return e.InternalServerError("Failed to remove the member.", err)
	}
	return e.JSON(http.StatusOK, map[string]any{"removed": true, "space": s.Name, "member": target.Member})
}

// -------------------------------------------------------------------
// Space memories over HTTP (same rules as the MCP tools)
// -------------------------------------------------------------------

func memorySpaceCall(e *core.RequestEvent, handler func(*core.RequestEvent, json.RawMessage) (any, error), args map[string]any) (any, error) {
	args["space"] = e.Request.PathValue("space")
	raw, _ := json.Marshal(args)
	normalized, err := mcpNormalizeArgs(raw)
	if err != nil {
		return nil, e.BadRequestError("Invalid arguments.", nil)
	}
	out, err := handler(e, normalized)
	if err != nil {
		var toolErr *mcpToolError
		if errors.As(err, &toolErr) {
			msg := toolErr.Error()
			if strings.Contains(msg, "don't have access") || strings.Contains(msg, "Only the author") {
				return nil, e.ForbiddenError(msg, nil)
			}
			if strings.Contains(msg, "was not found") {
				return nil, e.NotFoundError(msg, nil)
			}
			return nil, e.BadRequestError(msg, nil)
		}
		return nil, e.InternalServerError("Request failed.", err)
	}
	return out, nil
}

func memorySpaceMemoriesList(e *core.RequestEvent) error {
	q := e.Request.URL.Query()
	args := map[string]any{"limit": cast.ToInt(q.Get("limit"))}
	if p := q.Get("project"); p != "" {
		args["project"] = p
	}
	if tags := q["tags"]; len(tags) > 0 {
		args["tags"] = tags
	}
	handler := mcpMemoryList
	if query := strings.TrimSpace(q.Get("q")); query != "" {
		args["query"] = query
		handler = mcpMemorySearch
	} else {
		args["offset"] = cast.ToInt(q.Get("offset"))
	}
	out, err := memorySpaceCall(e, handler, args)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, out)
}

func memorySpaceMemoryCreate(e *core.RequestEvent) error {
	args := map[string]any{}
	if err := e.BindBody(&args); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	out, err := memorySpaceCall(e, mcpMemoryStore, args)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, out)
}

func memorySpaceMemoryView(e *core.RequestEvent) error {
	out, err := memorySpaceCall(e, mcpMemoryGet, map[string]any{"ids": []string{e.Request.PathValue("id")}})
	if err != nil {
		return err
	}
	res, _ := out.(map[string]any)
	items, _ := res["items"].([]map[string]any)
	if len(items) == 0 {
		return e.NotFoundError("Memory not found.", nil)
	}
	return e.JSON(http.StatusOK, items[0])
}

func memorySpaceMemoryUpdate(e *core.RequestEvent) error {
	args := map[string]any{}
	if err := e.BindBody(&args); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}
	args["id"] = e.Request.PathValue("id")
	out, err := memorySpaceCall(e, mcpMemoryUpdate, args)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, out)
}

func memorySpaceMemoryDelete(e *core.RequestEvent) error {
	out, err := memorySpaceCall(e, mcpMemoryDelete, map[string]any{"ids": []string{e.Request.PathValue("id")}})
	if err != nil {
		return err
	}
	res, _ := out.(map[string]any)
	if denied, ok := res["denied"].([]string); ok && len(denied) > 0 {
		return e.ForbiddenError("Only the author or a space admin can delete this memory.", nil)
	}
	if deleted, _ := res["deleted"].(int64); deleted == 0 {
		return e.NotFoundError("Memory not found.", nil)
	}
	return e.NoContent(http.StatusNoContent)
}

// -------------------------------------------------------------------
// MCP tools
// -------------------------------------------------------------------

var mcpSpaceTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
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

	mcpSpaceTools = []mcpTool{
		{
			Name: "space",
			Description: "Shared memory spaces: memory libraries shared by several users (eg. a team). Create a space, discover the listed spaces, request to join one " +
				"(the owner must approve), leave it. Once a member, pass space to the memory/note tools to read and write the shared memories; every memory records its author. " +
				"Actions: create, list (your spaces), discover (listed spaces), get, update, delete (owner), join, leave.",
			InputSchema: obj(map[string]any{
				"action":      enum("Action.", "create", "list", "discover", "get", "update", "delete", "join", "leave"),
				"space":       str("Space id or name (get/update/delete/join/leave)."),
				"name":        str("Space name (create/update)."),
				"description": str("Description (create/update)."),
				"listed":      boolean("Discoverable by the other users (default false)."),
				"message":     str("Message to the owner when requesting to join."),
				"search":      str("Text searched in the names/descriptions (list/discover)."),
			}, "action"),
			NoScopeArgs: true,
			Handler:     mcpSpaceTool,
		},
		{
			Name:        "space_member",
			Description: "Manage the members of a shared memory space. The creator (owner) of the space decides: approve or reject the join requests, remove members, change roles (admin = can moderate the content; owner transfers the ownership). Members can list the active members; the owner also sees the pending requests.",
			InputSchema: obj(map[string]any{
				"action":   enum("Action.", "list", "approve", "reject", "remove", "set_role"),
				"space":    str("Space id or name."),
				"memberId": str("Auth record id of the member/requester."),
				"role":     enum("New role (set_role).", "admin", "member", "owner"),
			}, "action", "space"),
			NoScopeArgs: true,
			Handler:     mcpSpaceMemberTool,
		},
	}
}

func mcpSpaceTool(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := map[string]any{}
	if err := json.Unmarshal(args, &payload); err != nil {
		return nil, mcpToolErr("Invalid arguments.")
	}
	action := strings.ToLower(strings.TrimSpace(cast.ToString(payload["action"])))
	ref := strings.TrimSpace(cast.ToString(payload["space"]))
	delete(payload, "action")
	delete(payload, "space")
	base := "/api/memory/spaces"
	p := base + "/" + url.PathEscape(ref)

	needRef := func() error {
		if ref == "" {
			return mcpToolErr("space is required for action " + action + ".")
		}
		return nil
	}

	switch action {
	case "create":
		return mcpDispatchJSON(e, http.MethodPost, base, nil, payload)
	case "list":
		return mcpDispatchJSON(e, http.MethodGet, base, mcpQueryFromBody(payload, "search"), nil)
	case "discover":
		query := mcpQueryFromBody(payload, "search")
		query["discover"] = "true"
		return mcpDispatchJSON(e, http.MethodGet, base, query, nil)
	case "get", "update", "delete", "join", "leave":
		if err := needRef(); err != nil {
			return nil, err
		}
		switch action {
		case "get":
			return mcpDispatchJSON(e, http.MethodGet, p, nil, nil)
		case "update":
			return mcpDispatchJSON(e, http.MethodPatch, p, nil, payload)
		case "delete":
			return mcpDispatchJSON(e, http.MethodDelete, p, nil, nil)
		case "join":
			return mcpDispatchJSON(e, http.MethodPost, p+"/join", nil, payload)
		default:
			return mcpDispatchJSON(e, http.MethodPost, p+"/leave", nil, map[string]any{})
		}
	default:
		return nil, mcpToolErr("Unknown action " + strconv.Quote(action) + ".")
	}
}

func mcpSpaceMemberTool(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		Action   string `json:"action"`
		Space    string `json:"space"`
		MemberId string `json:"memberId"`
		Role     string `json:"role"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.Space) == "" {
		return nil, mcpToolErr("space is required.")
	}
	base := "/api/memory/spaces/" + url.PathEscape(strings.TrimSpace(payload.Space)) + "/members"
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if action == "list" {
		return mcpDispatchJSON(e, http.MethodGet, base, nil, nil)
	}
	if strings.TrimSpace(payload.MemberId) == "" {
		return nil, mcpToolErr("memberId is required for action " + action + ".")
	}
	p := base + "/" + url.PathEscape(strings.TrimSpace(payload.MemberId))
	switch action {
	case "approve":
		return mcpDispatchJSON(e, http.MethodPost, p+"/approve", nil, map[string]any{})
	case "reject":
		return mcpDispatchJSON(e, http.MethodPost, p+"/reject", nil, map[string]any{})
	case "remove":
		return mcpDispatchJSON(e, http.MethodDelete, p, nil, nil)
	case "set_role":
		return mcpDispatchJSON(e, http.MethodPatch, p, nil, map[string]any{"role": payload.Role})
	default:
		return nil, mcpToolErr("Unknown action " + strconv.Quote(action) + ".")
	}
}
