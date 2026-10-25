package apis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
)

const (
	mcpMemoriesTable = "_mcp_memories"
	mcpEdgesTable    = "_mcp_memory_edges"

	mcpDefaultNamespace     = "default"
	mcpDefaultEmbedModel    = "text-embedding-3-small"
	mcpDefaultSearchLimit   = 10
	mcpMaxSearchLimit       = 100
	mcpDefaultListLimit     = 50
	mcpMaxListLimit         = 500
	mcpMaxContentLength     = 100000
	mcpDefaultGraphDepth    = 2
	mcpMaxGraphDepth        = 5
	mcpDefaultGraphNodes    = 50
	mcpMaxGraphNodes        = 500
	mcpMaxEdgesPerLevel     = 2000
	mcpEmbeddingCallTimeout = 30 * time.Second
)

// -------------------------------------------------------------------
// Tool definitions
// -------------------------------------------------------------------

var mcpMemoryTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	strArr := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	numArr := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}

	projectProp := str("Project the memory belongs to (eg. a repository, product or topic name). Defaults to \"default\".")
	directionProp := map[string]any{"type": "string", "enum": []string{"out", "in", "both"}, "description": "Edge direction to follow. Defaults to \"both\"."}

	mcpMemoryTools = []mcpTool{
		{
			Name:        "memory_store",
			Description: "Store a new private memory (fact, decision, preference, document chunk...). Memories are always private to the authenticated user. The content is embedded into pgvector for semantic search. Optionally link it to existing memories via relations.",
			InputSchema: obj(map[string]any{
				"content":   str("The memory text to store."),
				"title":     str("Optional short title/summary."),
				"project":   projectProp,
				"tags":      strArr("Optional tags used for filtering."),
				"metadata":  map[string]any{"type": "object", "description": "Optional arbitrary JSON metadata."},
				"embedding": numArr("Optional precomputed embedding. When omitted the server computes it (if an embedding provider is configured)."),
				"pinned":    map[string]any{"type": "boolean", "description": "Pin it as a core memory: pinned memories are given to you at the start of every session (use for stable user preferences, identity, key project facts)."},
				"dedupe":    map[string]any{"type": "boolean", "description": "If a very similar memory already exists in the project, update it instead of storing a duplicate (default false)."},
				"relations": map[string]any{
					"type":        "array",
					"description": "Optional relations to create from the new memory to existing ones.",
					"items": obj(map[string]any{
						"target":   str("Id of the existing memory."),
						"relation": str("Relation type, eg. \"related_to\", \"part_of\", \"depends_on\"."),
						"weight":   map[string]any{"type": "number"},
						"incoming": map[string]any{"type": "boolean", "description": "If true the edge points from target to the new memory."},
					}, "target", "relation"),
				},
			}, "content"),
			Handler: mcpMemoryStore,
		},
		{
			Name:        "memory_search",
			Description: "Search memories by meaning (vector similarity) with keyword fallback. Optionally expand each hit with its graph neighbours.",
			InputSchema: obj(map[string]any{
				"query":         str("Natural language query."),
				"project":       str("Restrict to a project (all projects when omitted)."),
				"tags":          strArr("Only return memories having all of these tags."),
				"limit":         integer("Max results (default 10, max 100)."),
				"minScore":      map[string]any{"type": "number", "description": "Minimum cosine similarity (0..1) for semantic hits."},
				"mode":          map[string]any{"type": "string", "enum": []string{"auto", "semantic", "keyword"}, "description": "Search mode. Defaults to \"auto\"."},
				"embedding":     numArr("Optional precomputed query embedding."),
				"includeShared": map[string]any{"type": "boolean", "description": "Also search the public knowledge bases (shared notes). Defaults to true."},
				"expandDepth":   integer("If > 0, also return graph neighbours of the hits up to this depth (max 2)."),
				"folder":        str("Only notes inside this vault folder."),
			}, "query"),
			ReadOnly: true,
			Handler:  mcpMemorySearch,
		},
		{
			Name:        "memory_get",
			Description: "Get memories by id, including their direct relations.",
			InputSchema: obj(map[string]any{
				"ids": strArr("Memory ids."),
			}, "ids"),
			ReadOnly: true,
			Handler:  mcpMemoryGet,
		},
		{
			Name:        "memory_list",
			Description: "List memories (newest first).",
			InputSchema: obj(map[string]any{
				"project":       str("Restrict to a project."),
				"tags":          strArr("Only memories having all of these tags."),
				"limit":         integer("Max results (default 50, max 500)."),
				"offset":        integer("Pagination offset."),
				"includeShared": map[string]any{"type": "boolean", "description": "Include the public knowledge bases (shared notes). Defaults to false."},
			}),
			ReadOnly: true,
			Handler:  mcpMemoryList,
		},
		{
			Name:        "memory_update",
			Description: "Update an existing memory. Changing the content re-computes its embedding.",
			InputSchema: obj(map[string]any{
				"id":        str("Memory id."),
				"content":   str("New content."),
				"title":     str("New title."),
				"project":   str("Move the memory to another project."),
				"tags":      strArr("Replace tags."),
				"metadata":  map[string]any{"type": "object", "description": "Replace metadata."},
				"embedding": numArr("Optional precomputed embedding for the new content."),
				"pinned":    map[string]any{"type": "boolean", "description": "Pin/unpin it as a core memory given to you at the start of every session."},
			}, "id"),
			Handler: mcpMemoryUpdate,
		},
		{
			Name:        "memory_delete",
			Description: "Delete memories by id (their relations are removed too).",
			InputSchema: obj(map[string]any{
				"ids": strArr("Memory ids."),
			}, "ids"),
			Destructive: true,
			Handler:     mcpMemoryDelete,
		},
		{
			Name:        "memory_link",
			Description: "Create or update a typed, directed relation (graph edge) between two memories.",
			InputSchema: obj(map[string]any{
				"source":   str("Source memory id."),
				"target":   str("Target memory id."),
				"relation": str("Relation type, eg. \"related_to\", \"part_of\", \"depends_on\", \"causes\", \"contradicts\"."),
				"weight":   map[string]any{"type": "number", "description": "Edge weight (default 1)."},
				"metadata": map[string]any{"type": "object"},
			}, "source", "target", "relation"),
			Handler: mcpMemoryLink,
		},
		{
			Name:        "memory_unlink",
			Description: "Remove relations between two memories (all relation types when relation is omitted).",
			InputSchema: obj(map[string]any{
				"source":   str("Source memory id."),
				"target":   str("Target memory id."),
				"relation": str("Optional relation type."),
			}, "source", "target"),
			Handler: mcpMemoryUnlink,
		},
		{
			Name:        "memory_graph",
			Description: "Traverse the knowledge graph starting from one or more memories and return the reachable nodes and edges (breadth-first).",
			InputSchema: obj(map[string]any{
				"ids":       strArr("Start memory ids."),
				"depth":     integer("Max traversal depth (default 2, max 5)."),
				"relations": strArr("Only follow these relation types."),
				"direction": directionProp,
				"limit":     integer("Max nodes to return (default 50, max 500)."),
			}, "ids"),
			ReadOnly: true,
			Handler:  mcpMemoryGraph,
		},
		{
			Name:        "memory_path",
			Description: "Find the shortest relation path between two memories.",
			InputSchema: obj(map[string]any{
				"source":    str("Start memory id."),
				"target":    str("Destination memory id."),
				"maxDepth":  integer("Max path length (default 4, max 5)."),
				"relations": strArr("Only follow these relation types."),
				"direction": directionProp,
			}, "source", "target"),
			ReadOnly: true,
			Handler:  mcpMemoryPath,
		},
		{
			Name:        "memory_projects",
			Description: "List the projects of the memory space with their memory/relation counts and last update time.",
			InputSchema: obj(map[string]any{
				"includeShared": map[string]any{"type": "boolean", "description": "Include the public knowledge bases. Defaults to false."},
			}),
			ReadOnly: true,
			Handler:  mcpMemoryProjects,
		},
		{
			Name:        "memory_project_rename",
			Description: "Rename a project (or merge it into another one when the new name already exists).",
			InputSchema: obj(map[string]any{
				"project": str("Current project name."),
				"newName": str("New project name."),
			}, "project", "newName"),
			Handler: mcpMemoryProjectRename,
		},
		{
			Name:        "memory_project_delete",
			Description: "Permanently delete a project and all of its memories and relations.",
			InputSchema: obj(map[string]any{
				"project": str("Project name."),
				"confirm": map[string]any{"type": "boolean", "description": "Must be true."},
			}, "project", "confirm"),
			Destructive: true,
			Handler:     mcpMemoryProjectDelete,
		},
		{
			Name:        "memory_stats",
			Description: "Knowledge base statistics: memory and relation counts per owner and project.",
			InputSchema: obj(map[string]any{
				"ownerId": str("Restrict to one owner (\"\" for the shared knowledge base)."),
			}),
			SuperuserOnly: true,
			ReadOnly:      true,
			Handler:       mcpMemoryStats,
		},
	}
}

// -------------------------------------------------------------------
// Scope / access control
// -------------------------------------------------------------------

// mcpScope describes on whose behalf a tool call operates.
type mcpScope struct {
	// writeOwner is the owner assigned to new/modified rows
	// ("" = public knowledge base, notes only: memories are always private).
	writeOwner      string
	ownerCollection string
	// sharedOnly restricts reads to the shared knowledge base.
	sharedOnly bool

	// actor is the authenticated user (the author of the written memories).
	actor           string
	actorCollection string
	superuser       bool
	// space is set when operating inside a shared memory space.
	space *mcpSpaceMembership
	// memberSpaces are the owner keys ("space:<id>") of the active spaces of the
	// user, included in the reads of the personal scope.
	memberSpaces []string
}

type mcpScopeArgs struct {
	OwnerId       *string `json:"ownerId"`
	Shared        bool    `json:"shared"`
	Space         string  `json:"space"`
	IncludeSpaces *bool   `json:"includeSpaces"`
}

// isSpace reports whether the scope is a shared memory space.
func (s *mcpScope) isSpace() bool { return s.space != nil }

// canModerate reports whether the actor can modify/delete the content of other members.
func (s *mcpScope) canModerate() bool {
	if s.superuser {
		return true
	}
	return s.space != nil && (s.space.Role == mcpSpaceRoleOwner || s.space.Role == mcpSpaceRoleAdmin)
}

// canModify reports whether the actor can modify (or delete) the provided row.
// Personal rows are always modifiable by their owner; in a space the members can
// edit the notes collaboratively but only the author or a moderator can modify a
// memory, and deleting requires the author or a moderator.
func (s *mcpScope) canModify(row *mcpMemoryRow, deleting bool) bool {
	if !s.isSpace() || s.canModerate() || row.Author == s.actor {
		return true
	}
	return !deleting && row.Path != ""
}

func mcpResolveScope(e *core.RequestEvent, args json.RawMessage) (*mcpScope, error) {
	sa := mcpScopeArgs{}
	_ = json.Unmarshal(args, &sa)

	scope := &mcpScope{
		writeOwner:      e.Auth.Id,
		ownerCollection: e.Auth.Collection().Name,
		actor:           e.Auth.Id,
		actorCollection: e.Auth.Collection().Name,
		superuser:       e.HasSuperuserAuth(),
	}

	if ref := strings.TrimSpace(sa.Space); ref != "" {
		if sa.Shared || (sa.OwnerId != nil && *sa.OwnerId != "" && *sa.OwnerId != e.Auth.Id) {
			return nil, mcpToolErr("space can't be combined with ownerId/shared.")
		}
		membership, err := mcpResolveSpaceMembership(e, ref)
		if err != nil {
			return nil, err
		}
		scope.space = membership
		scope.writeOwner = mcpSpaceOwnerKey(membership.SpaceId)
		scope.ownerCollection = mcpSpacesTable
		return scope, nil
	}

	includeSpaces := sa.IncludeSpaces == nil || *sa.IncludeSpaces

	if !e.HasSuperuserAuth() {
		if sa.Shared || (sa.OwnerId != nil && *sa.OwnerId != e.Auth.Id) {
			return nil, mcpToolErr("Only superusers can use the ownerId/shared arguments.")
		}
		if includeSpaces {
			scope.memberSpaces = mcpActiveSpaceKeys(e.App, e.Auth.Id)
		}
		return scope, nil
	}

	if sa.Shared {
		scope.writeOwner = ""
		scope.ownerCollection = ""
		scope.sharedOnly = true
		return scope, nil
	}

	if sa.OwnerId != nil && *sa.OwnerId != "" && *sa.OwnerId != e.Auth.Id {
		record, err := mcpFindAuthRecord(e.App, *sa.OwnerId)
		if err != nil {
			return nil, mcpToolErr("Auth record " + strconv.Quote(*sa.OwnerId) + " was not found.")
		}
		scope.writeOwner = record.Id
		scope.ownerCollection = record.Collection().Name
	}
	if includeSpaces {
		scope.memberSpaces = mcpActiveSpaceKeys(e.App, scope.writeOwner)
	}

	return scope, nil
}

func mcpFindAuthRecord(app core.App, id string) (*core.Record, error) {
	collections, err := app.FindAllCollections(core.CollectionTypeAuth)
	if err != nil {
		return nil, err
	}
	for _, c := range collections {
		if record, err := app.FindRecordById(c, id); err == nil {
			return record, nil
		}
	}
	return nil, errors.New("not found")
}

// readOwners returns the owners whose rows are visible to the scope.
func (s *mcpScope) readOwners(includeShared bool) []string {
	if s.sharedOnly {
		return []string{""}
	}
	owners := []string{s.writeOwner}
	if includeShared {
		owners = append(owners, "")
	}
	if !s.isSpace() {
		owners = append(owners, s.memberSpaces...)
	}
	return owners
}

// mcpOwnerParams returns the visibility filter of the memories table rows for owners.
//
// Private rows are visible only to their owner. The shared space ("" owner) only
// contains public knowledge base notes: memories are always private, so shared rows
// without a note path (eg. legacy public memories) are never visible.
func mcpOwnerParams(owners []string, params dbx.Params) string {
	return mcpOwnerParamsAlias(owners, params, "")
}

// mcpOwnerParamsAlias is like mcpOwnerParams but with a table alias prefix (eg. "m.").
func mcpOwnerParamsAlias(owners []string, params dbx.Params, alias string) string {
	private := []string{}
	shared := false
	for i, o := range owners {
		if o == "" {
			shared = true
			continue
		}
		key := "owner" + strconv.Itoa(i)
		params[key] = o
		private = append(private, "{:"+key+"}")
	}

	conds := []string{}
	if len(private) > 0 {
		conds = append(conds, alias+"[[owner]] IN ("+strings.Join(private, ",")+")")
	}
	if shared {
		conds = append(conds, "("+alias+"[[owner]] = '' AND "+alias+"[[path]] <> '')")
	}
	if len(conds) == 0 {
		return "FALSE"
	}
	return "(" + strings.Join(conds, " OR ") + ")"
}

func toAnySlice(values []string) []any {
	result := make([]any, len(values))
	for i, v := range values {
		result[i] = v
	}
	return result
}

// -------------------------------------------------------------------
// Embeddings
// -------------------------------------------------------------------

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

// -------------------------------------------------------------------
// Rows
// -------------------------------------------------------------------

const mcpMemoryColumns = `[[id]], [[owner]], [[namespace]], [[path]], [[title]], [[content]],
	[[tags]]::text AS [[tags]], [[metadata]]::text AS [[metadata]], [[properties]]::text AS [[properties]],
	[[links]]::text AS [[links]], [[embeddingModel]], [[pinned]], [[accessCount]], [[author]], [[updatedBy]],
	([[embedding]] IS NOT NULL) AS [[hasEmbedding]], [[created]]::text AS [[created]], [[updated]]::text AS [[updated]]`

type mcpMemoryRow struct {
	Id             string  `db:"id"`
	Owner          string  `db:"owner"`
	Namespace      string  `db:"namespace"`
	Path           string  `db:"path"`
	Properties     string  `db:"properties"`
	Links          string  `db:"links"`
	Title          string  `db:"title"`
	Content        string  `db:"content"`
	Tags           string  `db:"tags"`
	Metadata       string  `db:"metadata"`
	EmbeddingModel string  `db:"embeddingModel"`
	Pinned         bool    `db:"pinned"`
	AccessCount    int     `db:"accessCount"`
	Author         string  `db:"author"`
	UpdatedBy      string  `db:"updatedBy"`
	HasEmbedding   bool    `db:"hasEmbedding"`
	Created        string  `db:"created"`
	Updated        string  `db:"updated"`
	Score          float64 `db:"score"`
}

func (r *mcpMemoryRow) tags() []string {
	tags := []string{}
	_ = json.Unmarshal([]byte(r.Tags), &tags)
	return tags
}

func (r *mcpMemoryRow) properties() map[string]any {
	props := map[string]any{}
	_ = json.Unmarshal([]byte(r.Properties), &props)
	return props
}

func (r *mcpMemoryRow) metadata() map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(r.Metadata), &metadata)
	return metadata
}

func (r *mcpMemoryRow) links() []string {
	links := []string{}
	_ = json.Unmarshal([]byte(r.Links), &links)
	return links
}

func (r *mcpMemoryRow) toMap() map[string]any {
	tags := []string{}
	_ = json.Unmarshal([]byte(r.Tags), &tags)
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(r.Metadata), &metadata)

	result := map[string]any{
		"id":           r.Id,
		"owner":        r.Owner,
		"shared":       r.Owner == "",
		"project":      r.Namespace,
		"title":        r.Title,
		"content":      r.Content,
		"tags":         tags,
		"metadata":     metadata,
		"hasEmbedding": r.HasEmbedding,
		"created":      r.Created,
		"updated":      r.Updated,
	}
	if r.Pinned {
		result["pinned"] = true
	}
	if r.Author != "" {
		result["author"] = r.Author
	}
	if r.UpdatedBy != "" && r.UpdatedBy != r.Author {
		result["updatedBy"] = r.UpdatedBy
	}
	if spaceId := mcpSpaceIdFromOwner(r.Owner); spaceId != "" {
		result["space"] = spaceId
	}
	if r.Path != "" {
		result["path"] = r.Path
	}
	if props := r.properties(); len(props) > 0 {
		result["properties"] = props
	}
	if links := r.links(); len(links) > 0 {
		result["links"] = links
	}

	return result
}

type mcpEdgeRow struct {
	Id       string  `db:"id"`
	Owner    string  `db:"owner"`
	Source   string  `db:"source"`
	Target   string  `db:"target"`
	Relation string  `db:"relation"`
	Weight   float64 `db:"weight"`
	Metadata string  `db:"metadata"`
	Created  string  `db:"created"`
}

func (r *mcpEdgeRow) toMap() map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(r.Metadata), &metadata)
	return map[string]any{
		"id":       r.Id,
		"source":   r.Source,
		"target":   r.Target,
		"relation": r.Relation,
		"weight":   r.Weight,
		"shared":   r.Owner == "",
		"metadata": metadata,
		"created":  r.Created,
	}
}

func mcpEdgeSelect(app core.App) *dbx.SelectQuery {
	return app.DB().Select(
		"id", "owner", "source", "target", "relation", "weight",
		"[[metadata]]::text AS metadata", "[[created]]::text AS created",
	).From(mcpEdgesTable)
}

func mcpLoadMemories(app core.App, ids []string, owners []string) ([]*mcpMemoryRow, error) {
	rows := []*mcpMemoryRow{}
	if len(ids) == 0 {
		return rows, nil
	}

	params := dbx.Params{}
	ownerExp := mcpOwnerParams(owners, params)
	idPlaceholders := make([]string, len(ids))
	for i, id := range ids {
		key := "id" + strconv.Itoa(i)
		params[key] = id
		idPlaceholders[i] = "{:" + key + "}"
	}

	err := app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s AND [[id]] IN (%s)",
		mcpMemoryColumns, mcpMemoriesTable, ownerExp, strings.Join(idPlaceholders, ","),
	)).Bind(params).All(&rows)

	return rows, err
}

func mcpLoadEdgesBetween(app core.App, ids []string, owners []string) ([]*mcpEdgeRow, error) {
	edges := []*mcpEdgeRow{}
	if len(ids) == 0 {
		return edges, nil
	}
	idVals := toAnySlice(ids)
	err := mcpEdgeSelect(app).Where(dbx.And(
		dbx.In("owner", toAnySlice(owners)...),
		dbx.Or(dbx.In("source", idVals...), dbx.In("target", idVals...)),
	)).OrderBy("created ASC").All(&edges)
	return edges, err
}

func normalizeTags(tags []string) []string {
	result := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		result = append(result, t)
	}
	return result
}

func mcpBindArgs(args json.RawMessage, dst any) error {
	if err := json.Unmarshal(args, dst); err != nil {
		return mcpToolErr("Invalid arguments: " + err.Error())
	}
	return nil
}

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// -------------------------------------------------------------------
// Handlers
// -------------------------------------------------------------------

type mcpRelationArg struct {
	Target   string   `json:"target"`
	Relation string   `json:"relation"`
	Weight   *float64 `json:"weight"`
	Incoming bool     `json:"incoming"`
}

func mcpMemoryStore(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Content   string           `json:"content"`
		Title     string           `json:"title"`
		Namespace string           `json:"namespace"`
		Tags      []string         `json:"tags"`
		Metadata  map[string]any   `json:"metadata"`
		Embedding []float64        `json:"embedding"`
		Relations []mcpRelationArg `json:"relations"`
		Pinned    *bool            `json:"pinned"`
		Dedupe    bool             `json:"dedupe"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	if payload.Metadata == nil {
		payload.Metadata = map[string]any{}
	}
	tags := payload.Tags
	if tags == nil {
		tags = []string{}
	}

	if payload.Dedupe && len(payload.Relations) == 0 {
		fact, err := mcpRememberFact(e, scope, payload.Namespace, mcpFactInput{
			Title:    payload.Title,
			Content:  payload.Content,
			Tags:     tags,
			Pinned:   payload.Pinned,
			Metadata: payload.Metadata,
		}, mcpDedupeThreshold)
		if err != nil {
			return nil, err
		}
		return fact, nil
	}

	saved, warning, err := mcpSaveMemory(e, scope, nil, &mcpSaveInput{
		Namespace: payload.Namespace,
		Title:     &payload.Title,
		Content:   &payload.Content,
		Tags:      &tags,
		Metadata:  &payload.Metadata,
		Embedding: payload.Embedding,
		Pinned:    payload.Pinned,
	}, func(txApp core.App, id string) error {
		for _, rel := range payload.Relations {
			source, target := id, rel.Target
			if rel.Incoming {
				source, target = target, source
			}
			weight := 1.0
			if rel.Weight != nil {
				weight = *rel.Weight
			}
			if _, err := mcpUpsertEdge(txApp, scope, source, target, rel.Relation, weight, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"id":           saved.Id,
		"project":      saved.Namespace,
		"title":        saved.Title,
		"tags":         saved.tags(),
		"shared":       scope.writeOwner == "",
		"hasEmbedding": saved.HasEmbedding,
		"relations":    len(payload.Relations),
		"pinned":       saved.Pinned,
		"author":       saved.Author,
	}
	if scope.isSpace() {
		result["space"] = scope.space.SpaceId
		result["spaceName"] = scope.space.SpaceName
	}
	if links := saved.links(); len(links) > 0 {
		result["links"] = links
	}
	if warning != "" {
		result["warning"] = warning
	}

	return result, nil
}

func mcpMemorySearch(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Query         string    `json:"query"`
		Namespace     string    `json:"namespace"`
		Tags          []string  `json:"tags"`
		Limit         int       `json:"limit"`
		MinScore      *float64  `json:"minScore"`
		Mode          string    `json:"mode"`
		Embedding     []float64 `json:"embedding"`
		IncludeShared *bool     `json:"includeShared"`
		ExpandDepth   int       `json:"expandDepth"`
		Folder        string    `json:"folder"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	payload.Query = strings.TrimSpace(payload.Query)
	if payload.Query == "" && len(payload.Embedding) == 0 {
		return nil, mcpToolErr("query is required.")
	}
	limit := clampInt(payload.Limit, mcpDefaultSearchLimit, mcpMaxSearchLimit)
	includeShared := payload.IncludeShared == nil || *payload.IncludeShared
	owners := scope.readOwners(includeShared)

	params := dbx.Params{"limit": limit}
	filters := []string{mcpOwnerParams(owners, params)}
	if ns := strings.TrimSpace(payload.Namespace); ns != "" {
		params["namespace"] = ns
		filters = append(filters, "[[namespace]] = {:namespace}")
	}
	if tags := normalizeTags(payload.Tags); len(tags) > 0 {
		tagsJSON, _ := json.Marshal(tags)
		params["tags"] = string(tagsJSON)
		filters = append(filters, "[[tags]] @> {:tags}::jsonb")
	}
	if folder := strings.Trim(strings.TrimSpace(payload.Folder), "/"); folder != "" {
		params["folder"] = escapeLike(strings.ToLower(folder)) + "/%"
		filters = append(filters, "LOWER([[path]]) LIKE {:folder}")
	}

	mode := strings.ToLower(strings.TrimSpace(payload.Mode))
	usedMode := "keyword"
	var semanticCond, embeddingModel string
	var warning string
	rows := []*mcpMemoryRow{}

	if mode != "keyword" {
		vector, queryModel, embedErr := mcpEmbed(e.Request.Context(), e.App, payload.Query, payload.Embedding)
		switch {
		case embedErr != nil:
			warning = "Embedding failed, fell back to keyword search: " + embedErr.Error()
		case vector == nil:
			if mode == "semantic" {
				return nil, mcpToolErr("No embedding provider is configured; pass \"embedding\" or use mode \"keyword\".")
			}
		default:
			usedMode = "semantic"
			params["vector"] = formatVectorForPostgres(vector)
			params["dims"] = len(vector)
			// vectors of different models are not comparable
			semanticCond = "[[embedding]] IS NOT NULL AND vector_dims([[embedding]]) = {:dims}"
			if queryModel != "client" {
				params["queryModel"] = queryModel
				semanticCond += " AND [[embeddingModel]] = {:queryModel}"
				embeddingModel = queryModel
			}
			vectorFilters := append([]string{semanticCond}, filters...)
			if payload.MinScore != nil {
				params["minScore"] = *payload.MinScore
				vectorFilters = append(vectorFilters, "1 - ([[embedding]] <=> {:vector}::vector) >= {:minScore}")
			}

			err = e.App.DB().NewQuery(fmt.Sprintf(
				`SELECT %s, 1 - ([[embedding]] <=> {:vector}::vector) AS [[score]]
				FROM {{%s}} WHERE %s
				ORDER BY [[embedding]] <=> {:vector}::vector
				LIMIT {:limit}`,
				mcpMemoryColumns, mcpMemoriesTable, strings.Join(vectorFilters, " AND "),
			)).Bind(params).All(&rows)
			if err != nil {
				return nil, err
			}
		}
	}

	// keyword search (also used to complete semantic results for memories without embeddings)
	if usedMode == "keyword" || (len(rows) < limit && payload.Query != "") {
		if payload.Query == "" {
			return nil, mcpToolErr("query is required for keyword search.")
		}
		params["q"] = payload.Query
		params["like"] = "%" + escapeLike(payload.Query) + "%"
		keywordFilters := append([]string{
			`(to_tsvector('simple', [[title]] || ' ' || [[content]]) @@ plainto_tsquery('simple', {:q})
				OR [[content]] ILIKE {:like} OR [[title]] ILIKE {:like})`,
		}, filters...)
		if usedMode == "semantic" {
			// complete with the memories not covered by the semantic search
			// (no embedding or embedded with another model)
			keywordFilters = append(keywordFilters, "NOT ("+semanticCond+")")
		}

		keywordRows := []*mcpMemoryRow{}
		err = e.App.DB().NewQuery(fmt.Sprintf(
			`SELECT %s, ts_rank(to_tsvector('simple', [[title]] || ' ' || [[content]]), plainto_tsquery('simple', {:q}))
				+ (CASE WHEN [[title]] ILIKE {:like} THEN 0.2 ELSE 0 END)
				+ (CASE WHEN [[content]] ILIKE {:like} THEN 0.1 ELSE 0 END) AS [[score]]
			FROM {{%s}} WHERE %s
			ORDER BY [[score]] DESC, [[updated]] DESC
			LIMIT {:limit}`,
			mcpMemoryColumns, mcpMemoriesTable, strings.Join(keywordFilters, " AND "),
		)).Bind(params).All(&keywordRows)
		if err != nil {
			return nil, err
		}
		for _, r := range keywordRows {
			if len(rows) >= limit {
				break
			}
			rows = append(rows, r)
		}
	}

	items := make([]map[string]any, len(rows))
	ids := make([]string, len(rows))
	for i, r := range rows {
		item := r.toMap()
		item["score"] = r.Score
		items[i] = item
		ids[i] = r.Id
	}
	mcpTouchMemories(e.App, scope.writeOwner, ids)
	mcpAnnotateAuthors(e.App, items)

	result := map[string]any{
		"mode":    usedMode,
		"count":   len(items),
		"results": items,
	}
	if embeddingModel != "" {
		result["embeddingModel"] = embeddingModel
	}
	if warning != "" {
		result["warning"] = warning
	}

	if payload.ExpandDepth > 0 && len(ids) > 0 {
		graph, err := mcpTraverse(e.App, ids, owners, min(payload.ExpandDepth, 2), nil, "both", mcpDefaultGraphNodes+len(ids))
		if err != nil {
			return nil, err
		}
		result["graph"] = graph
	}

	return result, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func mcpMemoryGet(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Ids []string `json:"ids"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if len(payload.Ids) == 0 {
		return nil, mcpToolErr("ids is required.")
	}
	if len(payload.Ids) > mcpMaxListLimit {
		return nil, mcpToolErr(fmt.Sprintf("too many ids (max %d).", mcpMaxListLimit))
	}

	owners := scope.readOwners(true)
	rows, err := mcpLoadMemories(e.App, payload.Ids, owners)
	if err != nil {
		return nil, err
	}
	mcpTouchMemories(e.App, scope.writeOwner, payload.Ids)
	edges, err := mcpLoadEdgesBetween(e.App, payload.Ids, owners)
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, len(rows))
	for i, r := range rows {
		item := r.toMap()
		out := []map[string]any{}
		in := []map[string]any{}
		for _, edge := range edges {
			if edge.Source == r.Id {
				out = append(out, edge.toMap())
			}
			if edge.Target == r.Id {
				in = append(in, edge.toMap())
			}
		}
		item["outgoing"] = out
		item["incoming"] = in
		items[i] = item
	}
	mcpAnnotateAuthors(e.App, items)

	found := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		found[r.Id] = struct{}{}
	}
	missing := []string{}
	for _, id := range payload.Ids {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}

	return map[string]any{"items": items, "missing": missing}, nil
}

func mcpMemoryList(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Namespace     string   `json:"namespace"`
		Tags          []string `json:"tags"`
		Limit         int      `json:"limit"`
		Offset        int      `json:"offset"`
		IncludeShared bool     `json:"includeShared"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	limit := clampInt(payload.Limit, mcpDefaultListLimit, mcpMaxListLimit)
	params := dbx.Params{"limit": limit, "offset": max(payload.Offset, 0)}
	filters := []string{mcpOwnerParams(scope.readOwners(payload.IncludeShared), params)}
	if ns := strings.TrimSpace(payload.Namespace); ns != "" {
		params["namespace"] = ns
		filters = append(filters, "[[namespace]] = {:namespace}")
	}
	if tags := normalizeTags(payload.Tags); len(tags) > 0 {
		tagsJSON, _ := json.Marshal(tags)
		params["tags"] = string(tagsJSON)
		filters = append(filters, "[[tags]] @> {:tags}::jsonb")
	}
	where := strings.Join(filters, " AND ")

	var total int
	if err := e.App.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE %s", mcpMemoriesTable, where)).
		Bind(params).Row(&total); err != nil {
		return nil, err
	}

	rows := []*mcpMemoryRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY [[updated]] DESC, [[id]] DESC LIMIT {:limit} OFFSET {:offset}",
		mcpMemoryColumns, mcpMemoriesTable, where,
	)).Bind(params).All(&rows); err != nil {
		return nil, err
	}

	items := make([]map[string]any, len(rows))
	for i, r := range rows {
		items[i] = r.toMap()
	}
	mcpAnnotateAuthors(e.App, items)

	return map[string]any{"total": total, "limit": limit, "offset": params["offset"], "items": items}, nil
}

func mcpMemoryUpdate(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Id        string          `json:"id"`
		Content   *string         `json:"content"`
		Title     *string         `json:"title"`
		Namespace *string         `json:"namespace"`
		Tags      *[]string       `json:"tags"`
		Metadata  *map[string]any `json:"metadata"`
		Embedding []float64       `json:"embedding"`
		Pinned    *bool           `json:"pinned"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	if scope, err = mcpScopeForMemoryId(e, scope, payload.Id); err != nil {
		return nil, err
	}
	current, err := mcpLoadWritableMemory(e.App, scope, payload.Id)
	if err != nil {
		return nil, err
	}

	in := &mcpSaveInput{
		Title:     payload.Title,
		Content:   payload.Content,
		Tags:      payload.Tags,
		Metadata:  payload.Metadata,
		Embedding: payload.Embedding,
		Pinned:    payload.Pinned,
	}
	if payload.Namespace != nil {
		in.Namespace = *payload.Namespace
	}

	saved, warning, err := mcpSaveMemory(e, scope, current, in, nil)
	if err != nil {
		return nil, err
	}

	result := saved.toMap()
	if warning != "" {
		result["warning"] = warning
	}
	return result, nil
}

func mcpMemoryDelete(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Ids []string `json:"ids"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if len(payload.Ids) == 0 {
		return nil, mcpToolErr("ids is required.")
	}

	// rows of the scope, plus the rows of the member spaces (deleting there
	// requires the author or a space moderator)
	owners := []string{scope.writeOwner}
	if !scope.isSpace() {
		owners = append(owners, scope.memberSpaces...)
	}
	rows, err := mcpLoadMemories(e.App, payload.Ids, owners)
	if err != nil {
		return nil, err
	}

	deletable := []any{}
	denied := []string{}
	for _, r := range rows {
		rowScope := scope
		if r.Owner != scope.writeOwner {
			if rowScope, err = mcpSpaceScopeForRow(e, scope, r); err != nil {
				denied = append(denied, r.Id)
				continue
			}
		}
		if !rowScope.canModify(r, true) {
			denied = append(denied, r.Id)
			continue
		}
		deletable = append(deletable, r.Id)
	}

	var deleted int64
	if len(deletable) > 0 {
		res, err := e.App.DB().Delete(mcpMemoriesTable, dbx.In("id", deletable...)).Execute()
		if err != nil {
			return nil, err
		}
		deleted, _ = res.RowsAffected()
	}

	result := map[string]any{"deleted": deleted}
	if len(denied) > 0 {
		result["denied"] = denied
		result["deniedReason"] = "Only the author or a space admin can delete these memories."
	}
	return result, nil
}

// mcpUpsertEdge creates (or updates the weight/metadata of) an edge owned by the scope.
func mcpUpsertEdge(app core.App, scope *mcpScope, source, target, relation string, weight float64, metadata map[string]any) (string, error) {
	relation = strings.TrimSpace(relation)
	if relation == "" {
		return "", mcpToolErr("relation is required.")
	}
	if source == "" || target == "" {
		return "", mcpToolErr("source and target are required.")
	}
	if source == target {
		return "", mcpToolErr("source and target must be different memories.")
	}

	// both endpoints must be visible to the edge owner
	// (shared edges can only connect shared memories)
	owners := scope.readOwners(true)
	nodes, err := mcpLoadMemories(app, []string{source, target}, owners)
	if err != nil {
		return "", err
	}
	if len(nodes) != 2 {
		return "", mcpToolErr("source or target memory was not found or is not accessible.")
	}

	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, _ := json.Marshal(metadata)

	var id string
	err = app.DB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} ([[owner]], [[source]], [[target]], [[relation]], [[weight]], [[metadata]])
		VALUES ({:owner}, {:source}, {:target}, {:relation}, {:weight}, {:metadata}::jsonb)
		ON CONFLICT ([[owner]], [[source]], [[target]], [[relation]])
		DO UPDATE SET [[weight]] = EXCLUDED.[[weight]], [[metadata]] = EXCLUDED.[[metadata]], [[updated]] = NOW()
		RETURNING [[id]]`, mcpEdgesTable)).Bind(dbx.Params{
		"owner":    scope.writeOwner,
		"source":   source,
		"target":   target,
		"relation": relation,
		"weight":   weight,
		"metadata": string(metadataJSON),
	}).Row(&id)

	return id, err
}

func mcpMemoryLink(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Source   string         `json:"source"`
		Target   string         `json:"target"`
		Relation string         `json:"relation"`
		Weight   *float64       `json:"weight"`
		Metadata map[string]any `json:"metadata"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	weight := 1.0
	if payload.Weight != nil {
		weight = *payload.Weight
	}

	id, err := mcpUpsertEdge(e.App, scope, payload.Source, payload.Target, payload.Relation, weight, payload.Metadata)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"id":       id,
		"source":   payload.Source,
		"target":   payload.Target,
		"relation": strings.TrimSpace(payload.Relation),
		"weight":   weight,
		"shared":   scope.writeOwner == "",
	}, nil
}

func mcpMemoryUnlink(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Source   string `json:"source"`
		Target   string `json:"target"`
		Relation string `json:"relation"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if payload.Source == "" || payload.Target == "" {
		return nil, mcpToolErr("source and target are required.")
	}

	cond := dbx.HashExp{"owner": scope.writeOwner, "source": payload.Source, "target": payload.Target}
	if rel := strings.TrimSpace(payload.Relation); rel != "" {
		cond["relation"] = rel
	}

	res, err := e.App.DB().Delete(mcpEdgesTable, cond).Execute()
	if err != nil {
		return nil, err
	}
	deleted, _ := res.RowsAffected()

	return map[string]any{"deleted": deleted}, nil
}

// mcpEdgeFilter builds the edge filter for one BFS step from the frontier nodes.
func mcpEdgeFilter(frontier []string, owners []string, relations []string, direction string) dbx.Expression {
	f := toAnySlice(frontier)

	var dirExp dbx.Expression
	switch direction {
	case "out":
		dirExp = dbx.In("source", f...)
	case "in":
		dirExp = dbx.In("target", f...)
	default:
		dirExp = dbx.Or(dbx.In("source", f...), dbx.In("target", f...))
	}

	exps := []dbx.Expression{dbx.In("owner", toAnySlice(owners)...), dirExp}
	if len(relations) > 0 {
		exps = append(exps, dbx.In("relation", toAnySlice(relations)...))
	}

	return dbx.And(exps...)
}

func normalizeDirection(direction string) (string, error) {
	switch d := strings.ToLower(strings.TrimSpace(direction)); d {
	case "":
		return "both", nil
	case "out", "in", "both":
		return d, nil
	default:
		return "", mcpToolErr("direction must be one of out, in, both.")
	}
}

// mcpTraverse runs a bounded breadth-first traversal and returns the visited nodes and edges.
func mcpTraverse(app core.App, startIds []string, owners []string, depth int, relations []string, direction string, maxNodes int) (map[string]any, error) {
	start, err := mcpLoadMemories(app, startIds, owners)
	if err != nil {
		return nil, err
	}

	depthOf := map[string]int{}
	order := []string{}
	frontier := []string{}
	for _, n := range start {
		if _, ok := depthOf[n.Id]; ok {
			continue
		}
		depthOf[n.Id] = 0
		order = append(order, n.Id)
		frontier = append(frontier, n.Id)
	}

	edgeSeen := map[string]*mcpEdgeRow{}
	edgeOrder := []string{}
	truncated := false

	for level := 1; level <= depth && len(frontier) > 0 && !truncated; level++ {
		edges := []*mcpEdgeRow{}
		if err := mcpEdgeSelect(app).
			Where(mcpEdgeFilter(frontier, owners, relations, direction)).
			OrderBy("weight DESC", "created ASC").
			Limit(mcpMaxEdgesPerLevel).
			All(&edges); err != nil {
			return nil, err
		}
		if len(edges) == mcpMaxEdgesPerLevel {
			truncated = true
		}

		inFrontier := make(map[string]struct{}, len(frontier))
		for _, id := range frontier {
			inFrontier[id] = struct{}{}
		}

		candidates := []string{}
		for _, edge := range edges {
			for _, next := range []string{edge.Target, edge.Source} {
				if _, ok := inFrontier[next]; ok {
					continue
				}
				if _, ok := depthOf[next]; ok {
					continue
				}
				if len(order)+len(candidates) >= maxNodes {
					truncated = true
					break
				}
				depthOf[next] = level
				candidates = append(candidates, next)
			}
		}

		// keep only the nodes that are actually visible
		visible, err := mcpLoadMemories(app, candidates, owners)
		if err != nil {
			return nil, err
		}
		visibleSet := make(map[string]struct{}, len(visible))
		for _, v := range visible {
			visibleSet[v.Id] = struct{}{}
		}

		frontier = frontier[:0:0]
		for _, id := range candidates {
			if _, ok := visibleSet[id]; ok {
				order = append(order, id)
				frontier = append(frontier, id)
			} else {
				delete(depthOf, id)
			}
		}

		for _, edge := range edges {
			_, srcOk := depthOf[edge.Source]
			_, tgtOk := depthOf[edge.Target]
			if srcOk && tgtOk {
				if _, ok := edgeSeen[edge.Id]; !ok {
					edgeSeen[edge.Id] = edge
					edgeOrder = append(edgeOrder, edge.Id)
				}
			}
		}
	}

	rows, err := mcpLoadMemories(app, order, owners)
	if err != nil {
		return nil, err
	}
	rowsById := make(map[string]*mcpMemoryRow, len(rows))
	for _, r := range rows {
		rowsById[r.Id] = r
	}

	nodes := make([]map[string]any, 0, len(order))
	for _, id := range order {
		if r, ok := rowsById[id]; ok {
			n := r.toMap()
			n["depth"] = depthOf[id]
			nodes = append(nodes, n)
		}
	}
	edgesOut := make([]map[string]any, 0, len(edgeOrder))
	for _, id := range edgeOrder {
		edgesOut = append(edgesOut, edgeSeen[id].toMap())
	}

	return map[string]any{
		"nodes":     nodes,
		"edges":     edgesOut,
		"truncated": truncated,
	}, nil
}

func mcpMemoryGraph(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Ids       []string `json:"ids"`
		Depth     int      `json:"depth"`
		Relations []string `json:"relations"`
		Direction string   `json:"direction"`
		Limit     int      `json:"limit"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if len(payload.Ids) == 0 {
		return nil, mcpToolErr("ids is required.")
	}
	direction, err := normalizeDirection(payload.Direction)
	if err != nil {
		return nil, err
	}

	return mcpTraverse(
		e.App,
		payload.Ids,
		scope.readOwners(true),
		clampInt(payload.Depth, mcpDefaultGraphDepth, mcpMaxGraphDepth),
		normalizeTags(payload.Relations),
		direction,
		clampInt(payload.Limit, mcpDefaultGraphNodes, mcpMaxGraphNodes),
	)
}

func mcpMemoryPath(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Source    string   `json:"source"`
		Target    string   `json:"target"`
		MaxDepth  int      `json:"maxDepth"`
		Relations []string `json:"relations"`
		Direction string   `json:"direction"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if payload.Source == "" || payload.Target == "" {
		return nil, mcpToolErr("source and target are required.")
	}
	direction, err := normalizeDirection(payload.Direction)
	if err != nil {
		return nil, err
	}

	owners := scope.readOwners(true)
	relations := normalizeTags(payload.Relations)
	maxDepth := clampInt(payload.MaxDepth, 4, mcpMaxGraphDepth)

	endpoints, err := mcpLoadMemories(e.App, []string{payload.Source, payload.Target}, owners)
	if err != nil {
		return nil, err
	}
	if len(endpoints) != 2 && payload.Source != payload.Target {
		return nil, mcpToolErr("source or target memory was not found or is not accessible.")
	}

	// BFS keeping the edge used to reach each node
	parentEdge := map[string]*mcpEdgeRow{payload.Source: nil}
	frontier := []string{payload.Source}
	found := payload.Source == payload.Target

	for level := 1; level <= maxDepth && len(frontier) > 0 && !found; level++ {
		edges := []*mcpEdgeRow{}
		if err := mcpEdgeSelect(e.App).
			Where(mcpEdgeFilter(frontier, owners, relations, direction)).
			OrderBy("weight DESC").
			Limit(mcpMaxEdgesPerLevel).
			All(&edges); err != nil {
			return nil, err
		}

		inFrontier := make(map[string]struct{}, len(frontier))
		for _, id := range frontier {
			inFrontier[id] = struct{}{}
		}

		next := []string{}
		for _, edge := range edges {
			var to string
			if _, ok := inFrontier[edge.Source]; ok && direction != "in" {
				to = edge.Target
			} else if _, ok := inFrontier[edge.Target]; ok && direction != "out" {
				to = edge.Source
			} else {
				continue
			}
			if _, seen := parentEdge[to]; seen {
				continue
			}
			parentEdge[to] = edge
			next = append(next, to)
			if to == payload.Target {
				found = true
				break
			}
		}

		// drop nodes that are not visible (eg. edges pointing to memories of other spaces)
		visible, err := mcpLoadMemories(e.App, next, owners)
		if err != nil {
			return nil, err
		}
		visibleSet := make(map[string]struct{}, len(visible))
		for _, v := range visible {
			visibleSet[v.Id] = struct{}{}
		}
		frontier = frontier[:0:0]
		for _, id := range next {
			if _, ok := visibleSet[id]; ok {
				frontier = append(frontier, id)
			} else {
				delete(parentEdge, id)
				if id == payload.Target {
					found = false
				}
			}
		}
	}

	if !found {
		return map[string]any{"found": false, "maxDepth": maxDepth}, nil
	}

	// rebuild the path from target to source
	pathIds := []string{payload.Target}
	pathEdges := []map[string]any{}
	for cur := payload.Target; cur != payload.Source; {
		edge := parentEdge[cur]
		pathEdges = append([]map[string]any{edge.toMap()}, pathEdges...)
		if edge.Source == cur {
			cur = edge.Target
		} else {
			cur = edge.Source
		}
		pathIds = append([]string{cur}, pathIds...)
	}

	rows, err := mcpLoadMemories(e.App, pathIds, owners)
	if err != nil {
		return nil, err
	}
	rowsById := make(map[string]*mcpMemoryRow, len(rows))
	for _, r := range rows {
		rowsById[r.Id] = r
	}
	nodes := make([]map[string]any, 0, len(pathIds))
	for _, id := range pathIds {
		if r, ok := rowsById[id]; ok {
			nodes = append(nodes, r.toMap())
		}
	}

	return map[string]any{
		"found":  true,
		"length": len(pathEdges),
		"nodes":  nodes,
		"edges":  pathEdges,
	}, nil
}

func mcpMemoryStats(e *core.RequestEvent, args json.RawMessage) (any, error) {
	payload := struct {
		OwnerId *string `json:"ownerId"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	params := dbx.Params{}
	where := "TRUE"
	if payload.OwnerId != nil {
		params["owner"] = *payload.OwnerId
		where = "[[owner]] = {:owner}"
	}

	memoryStats := []struct {
		Owner           string `db:"owner" json:"owner"`
		OwnerCollection string `db:"ownerCollection" json:"ownerCollection"`
		Namespace       string `db:"namespace" json:"project"`
		Memories        int    `db:"memories" json:"memories"`
		Embedded        int    `db:"embedded" json:"embedded"`
	}{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT [[owner]], MAX([[ownerCollection]]) AS [[ownerCollection]], [[namespace]],
			COUNT(*) AS [[memories]], COUNT([[embedding]]) AS [[embedded]]
		FROM {{%s}} WHERE %s
		GROUP BY [[owner]], [[namespace]]
		ORDER BY [[owner]], [[namespace]]`, mcpMemoriesTable, where)).Bind(params).All(&memoryStats); err != nil {
		return nil, err
	}

	edgeStats := []struct {
		Owner    string `db:"owner" json:"owner"`
		Relation string `db:"relation" json:"relation"`
		Edges    int    `db:"edges" json:"edges"`
	}{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT [[owner]], [[relation]], COUNT(*) AS [[edges]]
		FROM {{%s}} WHERE %s
		GROUP BY [[owner]], [[relation]]
		ORDER BY [[owner]], [[relation]]`, mcpEdgesTable, where)).Bind(params).All(&edgeStats); err != nil {
		return nil, err
	}

	embeddingModel := ""
	if primary, _ := embeddingPrimary(e.App); primary != nil {
		embeddingModel = primary.Model
	}

	return map[string]any{
		"memories":       memoryStats,
		"relations":      edgeStats,
		"embeddingModel": embeddingModel,
	}, nil
}

func mcpMemoryProjects(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		IncludeShared bool `json:"includeShared"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	params := dbx.Params{}
	ownerExp := mcpOwnerParamsAlias(scope.readOwners(payload.IncludeShared), params, "m.")

	projects := []struct {
		Project   string `db:"project" json:"project"`
		Shared    bool   `db:"shared" json:"shared"`
		Space     string `db:"space" json:"space,omitempty"`
		Memories  int    `db:"memories" json:"memories"`
		Relations int    `db:"relations" json:"relations"`
		Updated   string `db:"updated" json:"updated"`
	}{}
	err = e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT m.[[namespace]] AS [[project]], (m.[[owner]] = '') AS [[shared]],
			(CASE WHEN m.[[owner]] LIKE 'space:%%' THEN substr(m.[[owner]], 7) ELSE '' END) AS [[space]],
			COUNT(*) AS [[memories]],
			COALESCE(SUM((SELECT COUNT(*) FROM {{%s}} e WHERE e.[[source]] = m.[[id]])), 0) AS [[relations]],
			MAX(m.[[updated]])::text AS [[updated]]
		FROM {{%s}} m WHERE %s
		GROUP BY m.[[namespace]], (m.[[owner]] = ''), (CASE WHEN m.[[owner]] LIKE 'space:%%' THEN substr(m.[[owner]], 7) ELSE '' END)
		ORDER BY MAX(m.[[updated]]) DESC`, mcpEdgesTable, mcpMemoriesTable, ownerExp)).Bind(params).All(&projects)
	if err != nil {
		return nil, err
	}

	return map[string]any{"projects": projects}, nil
}

func mcpMemoryProjectRename(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	if scope.isSpace() && !scope.canModerate() {
		return nil, mcpToolErr("Only the space owner or an admin can rename a project of the space.")
	}

	payload := struct {
		Namespace string `json:"namespace"`
		NewName   string `json:"newName"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	oldName := strings.TrimSpace(payload.Namespace)
	newName := strings.TrimSpace(payload.NewName)
	if oldName == "" || newName == "" {
		return nil, mcpToolErr("project and newName are required.")
	}

	res, err := e.App.DB().Update(
		mcpMemoriesTable,
		dbx.Params{"namespace": newName, "updated": time.Now()},
		dbx.HashExp{"owner": scope.writeOwner, "namespace": oldName},
	).Execute()
	if err != nil {
		return nil, err
	}
	updated, _ := res.RowsAffected()
	if updated == 0 {
		return nil, mcpToolErr("Project " + strconv.Quote(oldName) + " was not found or is not writable.")
	}

	return map[string]any{"project": newName, "memories": updated}, nil
}

func mcpMemoryProjectDelete(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	if scope.isSpace() && !scope.canModerate() {
		return nil, mcpToolErr("Only the space owner or an admin can delete a project of the space.")
	}

	payload := struct {
		Namespace string `json:"namespace"`
		Confirm   bool   `json:"confirm"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	project := strings.TrimSpace(payload.Namespace)
	if project == "" {
		return nil, mcpToolErr("project is required.")
	}
	if !payload.Confirm {
		return nil, mcpToolErr("Set confirm to true to delete the project.")
	}

	res, err := e.App.DB().Delete(mcpMemoriesTable, dbx.HashExp{"owner": scope.writeOwner, "namespace": project}).Execute()
	if err != nil {
		return nil, err
	}
	deleted, _ := res.RowsAffected()

	return map[string]any{"project": project, "deletedMemories": deleted}, nil
}
