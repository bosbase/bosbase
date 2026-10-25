package apis

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
)

// Obsidian-like notes on top of the MCP memories:
//   - a project is a vault and every note has a unique vault relative path (eg. "ideas/bosbase.md")
//   - [[wikilinks]] / [text](note.md) links are parsed and kept in sync as "links_to" graph edges
//   - #tags, frontmatter properties, aliases, backlinks, daily notes and version history are supported
//
// Plain memories (memory_store) share the same storage and the same markdown features,
// they simply don't have a path.

const (
	mcpVersionsTable = "_mcp_memory_versions"

	// mcpLinkRelation is the relation of the edges automatically generated from the note links.
	mcpLinkRelation = "links_to"

	mcpMaxVersions = 50
)

var mcpNoteTools []mcpTool

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

	projectProp := str("Vault/project name. Defaults to \"default\".")
	pathProp := str("Vault relative note path, eg. \"ideas/bosbase.md\" (the .md extension is optional).")
	idProp := str("Note id (alternative to path).")

	mcpNoteTools = []mcpTool{
		{
			Name: "note_write",
			Description: "Create or modify a markdown note (Obsidian style). Supports YAML frontmatter properties, #tags, [[wikilinks]] (kept in sync as graph links), " +
				"and keeps the previous content in the version history. Modes: overwrite (default, creates when missing), create (fails if it exists), append, prepend.",
			InputSchema: obj(map[string]any{
				"project":  projectProp,
				"path":     pathProp,
				"content":  str("Markdown content."),
				"mode":     map[string]any{"type": "string", "enum": []string{"overwrite", "create", "append", "prepend"}},
				"title":    str("Optional title (defaults to the frontmatter title, the first # heading or the file name)."),
				"tags":     strArr("Extra tags (inline #tags and frontmatter tags are detected automatically)."),
				"metadata": map[string]any{"type": "object", "description": "Optional hidden JSON metadata."},
			}, "path", "content"),
			Handler: mcpNoteWrite,
		},
		{
			Name:        "note_read",
			Description: "Read a note with its properties, tags, outgoing links (resolved/unresolved) and backlinks.",
			InputSchema: obj(map[string]any{
				"project": projectProp,
				"path":    pathProp,
				"id":      idProp,
			}),
			ReadOnly: true,
			Handler:  mcpNoteRead,
		},
		{
			Name:        "note_list",
			Description: "Browse the vault: list the notes and sub folders of a folder, optionally filtered by tags or frontmatter properties.",
			InputSchema: obj(map[string]any{
				"project":       projectProp,
				"folder":        str("Folder to list (vault root when empty)."),
				"recursive":     boolean("Include the notes of the sub folders. Defaults to true."),
				"tags":          strArr("Only notes having all of these tags."),
				"properties":    map[string]any{"type": "object", "description": "Only notes whose frontmatter contains these values, eg. {\"status\": \"done\"}."},
				"sort":          map[string]any{"type": "string", "enum": []string{"updated", "created", "path", "title"}},
				"limit":         integer("Max notes (default 100, max 500)."),
				"offset":        integer("Pagination offset."),
				"includeShared": boolean("Include the public knowledge base notes. Defaults to false."),
			}),
			ReadOnly: true,
			Handler:  mcpNoteList,
		},
		{
			Name:        "note_move",
			Description: "Rename/move a note. Links pointing to it from other notes are updated automatically (like Obsidian).",
			InputSchema: obj(map[string]any{
				"project":     projectProp,
				"path":        pathProp,
				"id":          idProp,
				"newPath":     str("New vault relative path."),
				"updateLinks": boolean("Rewrite the links in the other notes. Defaults to true."),
			}, "newPath"),
			Handler: mcpNoteMove,
		},
		{
			Name:        "note_delete",
			Description: "Delete a note (links to it become unresolved).",
			InputSchema: obj(map[string]any{
				"project": projectProp,
				"path":    pathProp,
				"id":      idProp,
			}),
			Destructive: true,
			Handler:     mcpNoteDelete,
		},
		{
			Name:        "note_backlinks",
			Description: "Find the notes linking to a note, plus unlinked mentions of its title/aliases.",
			InputSchema: obj(map[string]any{
				"project":         projectProp,
				"path":            pathProp,
				"id":              idProp,
				"includeUnlinked": boolean("Also search unlinked mentions. Defaults to true."),
			}),
			ReadOnly: true,
			Handler:  mcpNoteBacklinks,
		},
		{
			Name:        "note_daily",
			Description: "Get (and create if missing) the daily note of a date; optionally append a timestamped entry. Useful as a work journal.",
			InputSchema: obj(map[string]any{
				"project": projectProp,
				"date":    str("Date in YYYY-MM-DD format. Defaults to today (UTC)."),
				"folder":  str("Daily notes folder. Defaults to \"daily\"."),
				"append":  str("Text to append as a \"- HH:MM text\" list item."),
			}),
			Handler: mcpNoteDaily,
		},
		{
			Name:        "note_tags",
			Description: "List the tags used in a project with their usage counts.",
			InputSchema: obj(map[string]any{
				"project":       str("Vault/project name (all projects when empty)."),
				"includeShared": boolean("Include the public knowledge bases. Defaults to false."),
			}),
			ReadOnly: true,
			Handler:  mcpNoteTags,
		},
		{
			Name:        "note_history",
			Description: "List the saved versions of a note or memory, or return the content of a specific version.",
			InputSchema: obj(map[string]any{
				"project": projectProp,
				"path":    pathProp,
				"id":      idProp,
				"version": integer("Version number to return the content of."),
			}),
			ReadOnly: true,
			Handler:  mcpNoteHistory,
		},
		{
			Name:        "note_restore",
			Description: "Restore a note or memory to a previous version (the current content is kept as a new version).",
			InputSchema: obj(map[string]any{
				"project": projectProp,
				"path":    pathProp,
				"id":      idProp,
				"version": integer("Version number to restore."),
			}, "version"),
			Handler: mcpNoteRestore,
		},
	}
}

// -------------------------------------------------------------------
// Save pipeline (shared by memories and notes)
// -------------------------------------------------------------------

type mcpSaveInput struct {
	// Namespace is the target project ("" keeps the current one or uses the default for new rows).
	Namespace string
	Path      *string
	Title     *string
	Content   *string
	// Tags are the explicit tags (nil keeps the current explicit tags);
	// the inline/frontmatter tags are always merged.
	Tags      *[]string
	Metadata  *map[string]any
	Embedding []float64
	// EmbeddingModel is the model of a server computed Embedding
	// (empty for client provided embeddings).
	EmbeddingModel string
	// Pinned pins/unpins the memory (nil keeps the current state).
	Pinned *bool
}

// mcpLoadWritableMemory loads a memory owned by the scope write owner.
func mcpLoadWritableMemory(app core.App, scope *mcpScope, id string) (*mcpMemoryRow, error) {
	if strings.TrimSpace(id) == "" {
		return nil, mcpToolErr("id is required.")
	}
	rows, err := mcpLoadMemories(app, []string{id}, []string{scope.writeOwner})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, mcpToolErr("Memory " + strconv.Quote(id) + " was not found or is not writable (pass \"space\" for the memories of a shared space).")
	}
	if !scope.canModify(rows[0], false) {
		return nil, mcpToolErr("Only the author or a space admin can modify this memory.")
	}
	return rows[0], nil
}

// mcpSaveMemory inserts (current == nil) or updates a memory/note, keeping the
// derived data (tags, properties, links, link edges, embedding, versions) in sync.
func mcpSaveMemory(
	e *core.RequestEvent,
	scope *mcpScope,
	current *mcpMemoryRow,
	in *mcpSaveInput,
	afterSave func(txApp core.App, id string) error,
) (*mcpMemoryRow, string, error) {
	namespace := strings.TrimSpace(in.Namespace)
	if namespace == "" {
		if current != nil {
			namespace = current.Namespace
		} else {
			namespace = mcpDefaultNamespace
		}
	}

	notePath := ""
	if current != nil {
		notePath = current.Path
	}
	if in.Path != nil {
		notePath = *in.Path
	}

	content := ""
	if current != nil {
		content = current.Content
	}
	if in.Content != nil {
		content = strings.TrimSpace(*in.Content)
	}
	if content == "" && notePath == "" {
		return nil, "", mcpToolErr("content is required.")
	}
	if len(content) > mcpMaxContentLength {
		return nil, "", mcpToolErr(fmt.Sprintf("content is too long (max %d bytes).", mcpMaxContentLength))
	}

	// memories are always private: the shared space only holds public knowledge base notes
	if scope.writeOwner == "" && strings.TrimSpace(notePath) == "" {
		return nil, "", mcpToolErr("Memories are always private and can't be shared. To publish knowledge use a public knowledge base note: note_write with shared: true and a path.")
	}

	parsed := mcpParseMarkdown(content)

	// explicit tags + inline/frontmatter tags
	var explicitTags []string
	switch {
	case in.Tags != nil:
		explicitTags = *in.Tags
	case current != nil:
		oldInline := map[string]struct{}{}
		for _, t := range mcpParseMarkdown(current.Content).Tags {
			oldInline[strings.ToLower(t)] = struct{}{}
		}
		for _, t := range current.tags() {
			if _, ok := oldInline[strings.ToLower(t)]; !ok {
				explicitTags = append(explicitTags, t)
			}
		}
	}
	tags := normalizeTags(append(append([]string{}, explicitTags...), parsed.Tags...))

	title := ""
	switch {
	case in.Title != nil && strings.TrimSpace(*in.Title) != "":
		title = strings.TrimSpace(*in.Title)
	case parsed.Title != "":
		title = parsed.Title
	case current != nil && in.Title == nil && current.Title != "" && (current.Path == "" || current.Title != mcpNoteBaseName(current.Path)):
		title = current.Title
	case notePath != "":
		title = mcpNoteBaseName(notePath)
	}

	metadata := map[string]any{}
	if current != nil {
		metadata = current.metadata()
	}
	if in.Metadata != nil && *in.Metadata != nil {
		metadata = *in.Metadata
	}

	contentChanged := current == nil || content != current.Content || title != current.Title
	pathChanged := current != nil && (notePath != current.Path || namespace != current.Namespace)
	needEmbed := contentChanged || len(in.Embedding) > 0

	var warning string
	var vector []float64
	var model string
	if needEmbed {
		var embedErr error
		if len(in.Embedding) > 0 && in.EmbeddingModel != "" {
			// already computed by the server (eg. during the duplicates check)
			vector, model = in.Embedding, in.EmbeddingModel
		} else {
			vector, model, embedErr = mcpEmbed(e.Request.Context(), e.App, strings.TrimSpace(title+"\n"+content), in.Embedding)
		}
		if embedErr != nil {
			e.App.Logger().Warn("MCP embedding failure", "error", embedErr)
			warning = "Embedding failed, the memory is only searchable by keywords: " + embedErr.Error()
		}
	}

	tagsJSON, _ := json.Marshal(tags)
	metadataJSON, _ := json.Marshal(metadata)
	propertiesJSON, _ := json.Marshal(parsed.Properties)
	linksJSON, _ := json.Marshal(parsed.Links)

	params := dbx.Params{
		"owner":            scope.writeOwner,
		"ownerCollection":  scope.ownerCollection,
		"namespace":        namespace,
		"path":             notePath,
		"title":            title,
		"content":          content,
		"tags":             string(tagsJSON),
		"metadata":         string(metadataJSON),
		"properties":       string(propertiesJSON),
		"links":            string(linksJSON),
		"embedding":        nil,
		"embeddingModel":   model,
		"pinned":           in.Pinned != nil && *in.Pinned,
		"author":           scope.actor,
		"authorCollection": scope.actorCollection,
	}
	if vector != nil {
		params["embedding"] = formatVectorForPostgres(vector)
	}

	var id string
	err := e.App.RunInTransaction(func(txApp core.App) error {
		if current == nil {
			if err := txApp.DB().NewQuery(fmt.Sprintf(`
				INSERT INTO {{%s}} ([[owner]], [[ownerCollection]], [[namespace]], [[path]], [[title]], [[content]], [[tags]], [[metadata]], [[properties]], [[links]], [[embedding]], [[embeddingModel]], [[pinned]], [[author]], [[authorCollection]])
				VALUES ({:owner}, {:ownerCollection}, {:namespace}, {:path}, {:title}, {:content}, {:tags}::jsonb, {:metadata}::jsonb, {:properties}::jsonb, {:links}::jsonb, {:embedding}::vector, {:embeddingModel}, {:pinned}, {:author}, {:authorCollection})
				RETURNING [[id]]`, mcpMemoriesTable)).Bind(params).Row(&id); err != nil {
				return err
			}
		} else {
			id = current.Id
			params["id"] = id

			if contentChanged || pathChanged {
				if err := mcpSaveVersion(txApp, current); err != nil {
					return err
				}
			}

			sets := []string{
				"[[namespace]] = {:namespace}", "[[path]] = {:path}", "[[title]] = {:title}", "[[content]] = {:content}",
				"[[tags]] = {:tags}::jsonb", "[[metadata]] = {:metadata}::jsonb", "[[properties]] = {:properties}::jsonb",
				"[[links]] = {:links}::jsonb", "[[updated]] = NOW()", "[[updatedBy]] = {:author}",
			}
			if needEmbed {
				sets = append(sets, "[[embedding]] = {:embedding}::vector", "[[embeddingModel]] = {:embeddingModel}")
			}
			if in.Pinned != nil {
				sets = append(sets, "[[pinned]] = {:pinned}")
			}
			if _, err := txApp.DB().NewQuery(fmt.Sprintf(
				"UPDATE {{%s}} SET %s WHERE [[id]] = {:id} AND [[owner]] = {:owner}",
				mcpMemoriesTable, strings.Join(sets, ", "),
			)).Bind(params).Execute(); err != nil {
				return err
			}
		}

		if err := mcpSyncLinks(txApp, scope.writeOwner, namespace, id, parsed.Links); err != nil {
			return err
		}

		if current == nil || pathChanged || title != current.Title || contentChanged {
			names := mcpNoteNames(notePath, title, parsed.Aliases)
			if err := mcpBackfillLinks(txApp, scope.writeOwner, namespace, id, names); err != nil {
				return err
			}
		}

		if afterSave != nil {
			return afterSave(txApp, id)
		}
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "idx__mcp_memories_path") {
			return nil, "", mcpToolErr("A note with path " + strconv.Quote(notePath) + " already exists in project " + strconv.Quote(namespace) + ".")
		}
		return nil, "", err
	}

	rows, err := mcpLoadMemories(e.App, []string{id}, []string{scope.writeOwner})
	if err != nil {
		return nil, "", err
	}
	if len(rows) == 0 {
		return nil, "", mcpToolErr("Failed to load the saved memory.")
	}

	return rows[0], warning, nil
}

func mcpSaveVersion(txApp core.App, current *mcpMemoryRow) error {
	_, err := txApp.DB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} ([[memory]], [[version]], [[path]], [[title]], [[content]], [[tags]], [[metadata]])
		SELECT {:memory}, COALESCE(MAX([[version]]), 0) + 1, {:path}, {:title}, {:content}, {:tags}::jsonb, {:metadata}::jsonb
		FROM {{%s}} WHERE [[memory]] = {:memory}`, mcpVersionsTable, mcpVersionsTable)).Bind(dbx.Params{
		"memory":   current.Id,
		"path":     current.Path,
		"title":    current.Title,
		"content":  current.Content,
		"tags":     current.Tags,
		"metadata": current.Metadata,
	}).Execute()
	if err != nil {
		return err
	}

	// keep only the latest versions
	_, err = txApp.DB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}} WHERE [[memory]] = {:memory} AND [[version]] <= (
			SELECT MAX([[version]]) - {:keep} FROM {{%s}} WHERE [[memory]] = {:memory}
		)`, mcpVersionsTable, mcpVersionsTable)).Bind(dbx.Params{"memory": current.Id, "keep": mcpMaxVersions}).Execute()
	return err
}

// mcpNoteNames returns the lowercased names a note can be linked by.
func mcpNoteNames(notePath, title string, aliases []string) []string {
	names := []string{}
	if notePath != "" {
		names = append(names, strings.TrimSuffix(notePath, path.Ext(notePath)), mcpNoteBaseName(notePath))
	}
	if title != "" {
		names = append(names, title)
	}
	names = append(names, aliases...)
	for i, n := range names {
		names[i] = strings.ToLower(strings.TrimSpace(n))
	}
	return mcpUniqueFold(names)
}

type mcpLinkCandidate struct {
	Id         string `db:"id"`
	Path       string `db:"path"`
	Title      string `db:"title"`
	Properties string `db:"properties"`
	Updated    string `db:"updated"`
}

// mcpResolveLinks resolves link targets (case-insensitive) to the memories of the same owner and project.
//
// Resolution priority: full path > file name > title > alias.
func mcpResolveLinks(app core.App, owner, namespace string, targets []string) (map[string]*mcpLinkCandidate, error) {
	result := map[string]*mcpLinkCandidate{}
	if len(targets) == 0 {
		return result, nil
	}

	lowered := make([]string, len(targets))
	for i, t := range targets {
		lowered[i] = strings.ToLower(t)
	}
	targetsJSON, _ := json.Marshal(lowered)

	candidates := []*mcpLinkCandidate{}
	err := app.DB().NewQuery(fmt.Sprintf(`
		WITH t AS (SELECT jsonb_array_elements_text({:targets}::jsonb) AS name)
		SELECT [[id]], [[path]], [[title]], [[properties]]::text AS [[properties]], [[updated]]::text AS [[updated]]
		FROM {{%s}}
		WHERE [[owner]] = {:owner} AND [[namespace]] = {:namespace} AND (
			LOWER(regexp_replace([[path]], '\.md$', '', 'i')) IN (SELECT name FROM t)
			OR LOWER(regexp_replace(regexp_replace([[path]], '^.*/', ''), '\.md$', '', 'i')) IN (SELECT name FROM t)
			OR LOWER([[title]]) IN (SELECT name FROM t)
			OR EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(
					CASE WHEN jsonb_typeof([[properties]]->'aliases') = 'array' THEN [[properties]]->'aliases' ELSE '[]'::jsonb END
				) a WHERE LOWER(a) IN (SELECT name FROM t)
			)
		)
		ORDER BY [[updated]] DESC
		LIMIT 1000`, mcpMemoriesTable)).Bind(dbx.Params{
		"targets":   string(targetsJSON),
		"owner":     owner,
		"namespace": namespace,
	}).All(&candidates)
	if err != nil {
		return nil, err
	}

	for _, target := range targets {
		key := strings.ToLower(target)
		var best *mcpLinkCandidate
		bestRank := 99
		for _, c := range candidates {
			rank := 99
			switch {
			case c.Path != "" && strings.ToLower(strings.TrimSuffix(c.Path, path.Ext(c.Path))) == key:
				rank = 0
			case c.Path != "" && strings.ToLower(mcpNoteBaseName(c.Path)) == key:
				rank = 1
			case strings.ToLower(c.Title) == key:
				rank = 2
			default:
				props := map[string]any{}
				_ = json.Unmarshal([]byte(c.Properties), &props)
				for _, a := range mcpPropertyStrings(props["aliases"]) {
					if strings.ToLower(a) == key {
						rank = 3
						break
					}
				}
			}
			if rank < bestRank {
				best, bestRank = c, rank
			}
		}
		if best != nil {
			result[key] = best
		}
	}

	return result, nil
}

// mcpSyncLinks replaces the auto generated "links_to" edges of a memory.
func mcpSyncLinks(txApp core.App, owner, namespace, id string, links []string) error {
	if _, err := txApp.DB().Delete(mcpEdgesTable, dbx.HashExp{
		"owner":    owner,
		"source":   id,
		"relation": mcpLinkRelation,
	}).Execute(); err != nil {
		return err
	}

	resolved, err := mcpResolveLinks(txApp, owner, namespace, links)
	if err != nil {
		return err
	}

	for _, link := range links {
		target, ok := resolved[strings.ToLower(link)]
		if !ok || target.Id == id {
			continue
		}
		if err := mcpInsertLinkEdge(txApp, owner, id, target.Id, link); err != nil {
			return err
		}
	}

	return nil
}

func mcpInsertLinkEdge(txApp core.App, owner, source, target, link string) error {
	metadataJSON, _ := json.Marshal(map[string]any{"auto": true, "link": link})
	_, err := txApp.DB().NewQuery(fmt.Sprintf(`
		INSERT INTO {{%s}} ([[owner]], [[source]], [[target]], [[relation]], [[weight]], [[metadata]])
		VALUES ({:owner}, {:source}, {:target}, {:relation}, 1, {:metadata}::jsonb)
		ON CONFLICT ([[owner]], [[source]], [[target]], [[relation]]) DO NOTHING`, mcpEdgesTable)).Bind(dbx.Params{
		"owner":    owner,
		"source":   source,
		"target":   target,
		"relation": mcpLinkRelation,
		"metadata": string(metadataJSON),
	}).Execute()
	return err
}

// mcpBackfillLinks re-resolves the links of the memories referencing one of the
// provided names (eg. after a note was created or renamed).
func mcpBackfillLinks(txApp core.App, owner, namespace, id string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	namesJSON, _ := json.Marshal(names)

	sources := []*mcpMemoryRow{}
	err := txApp.DB().NewQuery(fmt.Sprintf(`
		SELECT [[id]], [[links]]::text AS [[links]] FROM {{%s}}
		WHERE [[owner]] = {:owner} AND [[namespace]] = {:namespace} AND [[id]] <> {:id}
		AND EXISTS (
			SELECT 1 FROM jsonb_array_elements_text([[links]]) l
			WHERE LOWER(l) IN (SELECT jsonb_array_elements_text({:names}::jsonb))
		)
		LIMIT 5000`, mcpMemoriesTable)).Bind(dbx.Params{
		"owner":     owner,
		"namespace": namespace,
		"id":        id,
		"names":     string(namesJSON),
	}).All(&sources)
	if err != nil {
		return err
	}

	for _, src := range sources {
		if err := mcpSyncLinks(txApp, owner, namespace, src.Id, src.links()); err != nil {
			return err
		}
	}

	return nil
}

// -------------------------------------------------------------------
// Note helpers
// -------------------------------------------------------------------

type mcpNoteRef struct {
	Namespace string `json:"namespace"`
	Path      string `json:"path"`
	Id        string `json:"id"`
}

// mcpFindNote finds a note by id or path among the provided owners (the first owner wins).
func mcpFindNote(app core.App, owners []string, ref *mcpNoteRef) (*mcpMemoryRow, error) {
	if strings.TrimSpace(ref.Id) != "" {
		rows, err := mcpLoadMemories(app, []string{ref.Id}, owners)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, mcpToolErr("Note " + strconv.Quote(ref.Id) + " was not found.")
		}
		return rows[0], nil
	}

	notePath, err := mcpNormalizeNotePath(ref.Path)
	if err != nil {
		return nil, mcpToolErr("path or id is required.")
	}
	namespace := strings.TrimSpace(ref.Namespace)
	if namespace == "" {
		namespace = mcpDefaultNamespace
	}

	params := dbx.Params{"namespace": namespace, "path": notePath}
	ownerExp := mcpOwnerParams(owners, params)
	rows := []*mcpMemoryRow{}
	err = app.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s AND [[namespace]] = {:namespace} AND LOWER([[path]]) = LOWER({:path})",
		mcpMemoryColumns, mcpMemoriesTable, ownerExp,
	)).Bind(params).All(&rows)
	if err != nil {
		return nil, err
	}
	for _, owner := range owners {
		for _, r := range rows {
			if r.Owner == owner {
				return r, nil
			}
		}
	}

	return nil, mcpToolErr("Note " + strconv.Quote(notePath) + " was not found in project " + strconv.Quote(namespace) + ".")
}

func mcpNoteSummary(r *mcpMemoryRow) map[string]any {
	m := map[string]any{
		"id":      r.Id,
		"title":   r.Title,
		"project": r.Namespace,
		"tags":    r.tags(),
		"updated": r.Updated,
		"shared":  r.Owner == "",
	}
	if r.Path != "" {
		m["path"] = r.Path
	}
	return m
}

// -------------------------------------------------------------------
// Handlers
// -------------------------------------------------------------------

func mcpNoteWrite(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Namespace string          `json:"namespace"`
		Path      string          `json:"path"`
		Content   *string         `json:"content"`
		Mode      string          `json:"mode"`
		Title     *string         `json:"title"`
		Tags      *[]string       `json:"tags"`
		Metadata  *map[string]any `json:"metadata"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if payload.Content == nil {
		return nil, mcpToolErr("content is required.")
	}

	notePath, err := mcpNormalizeNotePath(payload.Path)
	if err != nil {
		return nil, err
	}
	namespace := strings.TrimSpace(payload.Namespace)
	if namespace == "" {
		namespace = mcpDefaultNamespace
	}

	current, err := mcpFindNote(e.App, []string{scope.writeOwner}, &mcpNoteRef{Namespace: namespace, Path: notePath})
	if err != nil {
		if _, ok := err.(*mcpToolError); !ok {
			return nil, err
		}
		current = nil
	}

	mode := strings.ToLower(strings.TrimSpace(payload.Mode))
	content := *payload.Content
	switch mode {
	case "", "overwrite":
	case "create":
		if current != nil {
			return nil, mcpToolErr("Note " + strconv.Quote(notePath) + " already exists. Use mode overwrite/append/prepend to modify it.")
		}
	case "append":
		if current != nil && current.Content != "" {
			content = strings.TrimRight(current.Content, "\n") + "\n" + strings.TrimLeft(content, "\n")
		}
	case "prepend":
		if current != nil && current.Content != "" {
			props, body := mcpParseFrontmatter(current.Content)
			if len(props) > 0 {
				frontmatter := strings.TrimSuffix(current.Content, body)
				content = strings.TrimRight(frontmatter, "\n") + "\n" + strings.TrimRight(content, "\n") + "\n" + body
			} else {
				content = strings.TrimRight(content, "\n") + "\n" + current.Content
			}
		}
	default:
		return nil, mcpToolErr("mode must be one of overwrite, create, append, prepend.")
	}

	in := &mcpSaveInput{
		Namespace: namespace,
		Path:      &notePath,
		Title:     payload.Title,
		Content:   &content,
		Tags:      payload.Tags,
		Metadata:  payload.Metadata,
	}
	if current == nil && in.Tags == nil {
		in.Tags = &[]string{}
	}

	saved, warning, err := mcpSaveMemory(e, scope, current, in, nil)
	if err != nil {
		return nil, err
	}

	result := mcpNoteSummary(saved)
	result["created"] = current == nil
	result["links"] = saved.links()
	result["properties"] = saved.properties()
	result["hasEmbedding"] = saved.HasEmbedding
	if warning != "" {
		result["warning"] = warning
	}
	return result, nil
}

func mcpNoteRead(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	ref := &mcpNoteRef{}
	if err := mcpBindArgs(args, ref); err != nil {
		return nil, err
	}

	owners := scope.readOwners(true)
	note, err := mcpFindNote(e.App, owners, ref)
	if err != nil {
		return nil, err
	}

	result := note.toMap()
	mcpAnnotateAuthors(e.App, []map[string]any{result})

	// outgoing links
	resolved, err := mcpResolveLinks(e.App, note.Owner, note.Namespace, note.links())
	if err != nil {
		return nil, err
	}
	links := []map[string]any{}
	for _, l := range note.links() {
		item := map[string]any{"link": l, "resolved": false}
		if c, ok := resolved[strings.ToLower(l)]; ok {
			item["resolved"] = true
			item["id"] = c.Id
			item["title"] = c.Title
			if c.Path != "" {
				item["path"] = c.Path
			}
		}
		links = append(links, item)
	}
	result["links"] = links

	backlinks, err := mcpLoadBacklinks(e.App, owners, note.Id)
	if err != nil {
		return nil, err
	}
	result["backlinks"] = backlinks

	// other (manual) relations
	edges, err := mcpLoadEdgesBetween(e.App, []string{note.Id}, owners)
	if err != nil {
		return nil, err
	}
	relations := []map[string]any{}
	for _, edge := range edges {
		if edge.Relation != mcpLinkRelation {
			relations = append(relations, edge.toMap())
		}
	}
	result["relations"] = relations

	return result, nil
}

func mcpLoadBacklinks(app core.App, owners []string, id string) ([]map[string]any, error) {
	params := dbx.Params{"id": id, "relation": mcpLinkRelation}
	ownerExp := mcpOwnerParams(owners, params)
	rows := []*mcpMemoryRow{}
	err := app.DB().NewQuery(fmt.Sprintf(`
		SELECT %s FROM {{%s}} WHERE %s AND [[id]] IN (
			SELECT [[source]] FROM {{%s}} WHERE [[target]] = {:id} AND [[relation]] = {:relation}
		) ORDER BY [[updated]] DESC LIMIT 500`,
		mcpMemoryColumns, mcpMemoriesTable, ownerExp, mcpEdgesTable,
	)).Bind(params).All(&rows)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, len(rows))
	for i, r := range rows {
		result[i] = mcpNoteSummary(r)
	}
	return result, nil
}

func mcpNoteList(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Namespace     string         `json:"namespace"`
		Folder        string         `json:"folder"`
		Recursive     *bool          `json:"recursive"`
		Tags          []string       `json:"tags"`
		Properties    map[string]any `json:"properties"`
		Sort          string         `json:"sort"`
		Limit         int            `json:"limit"`
		Offset        int            `json:"offset"`
		IncludeShared bool           `json:"includeShared"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	namespace := strings.TrimSpace(payload.Namespace)
	if namespace == "" {
		namespace = mcpDefaultNamespace
	}
	folder := strings.Trim(strings.TrimSpace(strings.ReplaceAll(payload.Folder, "\\", "/")), "/")
	recursive := payload.Recursive == nil || *payload.Recursive
	limit := clampInt(payload.Limit, 100, mcpMaxListLimit)

	params := dbx.Params{"namespace": namespace, "limit": limit, "offset": max(payload.Offset, 0)}
	filters := []string{
		mcpOwnerParams(scope.readOwners(payload.IncludeShared), params),
		"[[namespace]] = {:namespace}",
		"[[path]] <> ''",
	}
	folderFilters := []string{}
	if folder != "" {
		params["folderLike"] = escapeLike(strings.ToLower(folder)) + "/%"
		folderFilters = append(folderFilters, "LOWER([[path]]) LIKE {:folderLike}")
	}
	if tags := normalizeTags(payload.Tags); len(tags) > 0 {
		tagsJSON, _ := json.Marshal(tags)
		params["tags"] = string(tagsJSON)
		filters = append(filters, "[[tags]] @> {:tags}::jsonb")
	}
	if len(payload.Properties) > 0 {
		propsJSON, _ := json.Marshal(payload.Properties)
		params["properties"] = string(propsJSON)
		filters = append(filters, "[[properties]] @> {:properties}::jsonb")
	}

	notesFilters := append(append([]string{}, filters...), folderFilters...)
	if !recursive {
		prefix := ""
		if folder != "" {
			prefix = escapeLike(strings.ToLower(folder)) + "/"
		}
		params["nestedLike"] = prefix + "%/%"
		notesFilters = append(notesFilters, "LOWER([[path]]) NOT LIKE {:nestedLike}")
	}

	orderBy := "[[updated]] DESC"
	switch payload.Sort {
	case "created":
		orderBy = "[[created]] DESC"
	case "path":
		orderBy = "LOWER([[path]]) ASC"
	case "title":
		orderBy = "LOWER([[title]]) ASC"
	}

	where := strings.Join(notesFilters, " AND ")

	var total int
	if err := e.App.DB().NewQuery(fmt.Sprintf("SELECT COUNT(*) FROM {{%s}} WHERE %s", mcpMemoriesTable, where)).
		Bind(params).Row(&total); err != nil {
		return nil, err
	}

	rows := []*mcpMemoryRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY %s, [[id]] LIMIT {:limit} OFFSET {:offset}",
		mcpMemoryColumns, mcpMemoriesTable, where, orderBy,
	)).Bind(params).All(&rows); err != nil {
		return nil, err
	}

	notes := make([]map[string]any, len(rows))
	for i, r := range rows {
		notes[i] = mcpNoteSummary(r)
	}

	// direct sub folders of the folder
	paths := []string{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT DISTINCT [[path]] FROM {{%s}} WHERE %s LIMIT 20000",
		mcpMemoriesTable, strings.Join(append(append([]string{}, filters...), folderFilters...), " AND "),
	)).Bind(params).Column(&paths); err != nil {
		return nil, err
	}
	folderSet := map[string]struct{}{}
	for _, p := range paths {
		rest := p
		if folder != "" {
			rest = p[len(folder)+1:]
		}
		if i := strings.Index(rest, "/"); i > 0 {
			sub := rest[:i]
			if folder != "" {
				sub = folder + "/" + sub
			}
			folderSet[sub] = struct{}{}
		}
	}
	folders := make([]string, 0, len(folderSet))
	for f := range folderSet {
		folders = append(folders, f)
	}
	sort.Strings(folders)

	return map[string]any{
		"project": namespace,
		"folder":  folder,
		"folders": folders,
		"total":   total,
		"notes":   notes,
	}, nil
}

func mcpNoteMove(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		mcpNoteRef
		NewPath     string `json:"newPath"`
		UpdateLinks *bool  `json:"updateLinks"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	note, err := mcpFindNote(e.App, []string{scope.writeOwner}, &payload.mcpNoteRef)
	if err != nil {
		return nil, err
	}
	newPath, err := mcpNormalizeNotePath(payload.NewPath)
	if err != nil {
		return nil, err
	}
	if newPath == note.Path {
		return mcpNoteSummary(note), nil
	}

	// collect the notes linking to it before the move
	linkingIds := []string{}
	if payload.UpdateLinks == nil || *payload.UpdateLinks {
		if err := e.App.DB().Select("source").From(mcpEdgesTable).Where(dbx.HashExp{
			"owner":    scope.writeOwner,
			"target":   note.Id,
			"relation": mcpLinkRelation,
		}).Column(&linkingIds); err != nil {
			return nil, err
		}
	}

	oldPath := note.Path
	oldTitle := note.Title
	moved, warning, err := mcpSaveMemory(e, scope, note, &mcpSaveInput{Path: &newPath}, nil)
	if err != nil {
		return nil, err
	}

	updated := []string{}
	if len(linkingIds) > 0 {
		sources, err := mcpLoadMemories(e.App, linkingIds, []string{scope.writeOwner})
		if err != nil {
			return nil, err
		}

		oldFull := []string{}
		oldShort := []string{}
		if oldPath != "" {
			oldFull = append(oldFull, strings.TrimSuffix(oldPath, path.Ext(oldPath)))
			oldShort = append(oldShort, mcpNoteBaseName(oldPath))
		}
		if oldTitle != moved.Title && oldTitle != "" {
			oldShort = append(oldShort, oldTitle)
		}
		newFull := strings.TrimSuffix(newPath, path.Ext(newPath))
		newShort := mcpNoteBaseName(newPath)

		for _, src := range sources {
			content, changedFull := mcpRewriteLinks(src.Content, oldFull, newFull)
			content, changedShort := mcpRewriteLinks(content, oldShort, newShort)
			if !changedFull && !changedShort {
				continue
			}
			if _, _, err := mcpSaveMemory(e, scope, src, &mcpSaveInput{Content: &content}, nil); err != nil {
				return nil, err
			}
			label := src.Path
			if label == "" {
				label = src.Id
			}
			updated = append(updated, label)
		}
	}

	result := mcpNoteSummary(moved)
	result["oldPath"] = oldPath
	result["updatedLinksIn"] = updated
	if warning != "" {
		result["warning"] = warning
	}
	return result, nil
}

func mcpNoteDelete(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	ref := &mcpNoteRef{}
	if err := mcpBindArgs(args, ref); err != nil {
		return nil, err
	}

	note, err := mcpFindNote(e.App, []string{scope.writeOwner}, ref)
	if err != nil {
		return nil, err
	}
	if !scope.canModify(note, true) {
		return nil, mcpToolErr("Only the author or a space admin can delete this note.")
	}

	if _, err := e.App.DB().Delete(mcpMemoriesTable, dbx.HashExp{"id": note.Id, "owner": scope.writeOwner}).Execute(); err != nil {
		return nil, err
	}

	return map[string]any{"deleted": true, "id": note.Id, "path": note.Path, "project": note.Namespace}, nil
}

func mcpNoteBacklinks(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		mcpNoteRef
		IncludeUnlinked *bool `json:"includeUnlinked"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	owners := scope.readOwners(true)
	note, err := mcpFindNote(e.App, owners, &payload.mcpNoteRef)
	if err != nil {
		return nil, err
	}

	backlinks, err := mcpLoadBacklinks(e.App, owners, note.Id)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"note":      mcpNoteSummary(note),
		"backlinks": backlinks,
	}

	if payload.IncludeUnlinked == nil || *payload.IncludeUnlinked {
		linked := map[string]struct{}{note.Id: {}}
		for _, b := range backlinks {
			linked[b["id"].(string)] = struct{}{}
		}

		names := []string{}
		if note.Title != "" {
			names = append(names, note.Title)
		}
		names = append(names, mcpPropertyStrings(note.properties()["aliases"])...)

		mentions := []map[string]any{}
		if len(names) > 0 {
			params := dbx.Params{"namespace": note.Namespace}
			conds := []string{}
			for i, n := range names {
				if len([]rune(n)) < 2 {
					continue
				}
				key := "n" + strconv.Itoa(i)
				params[key] = "%" + escapeLike(n) + "%"
				conds = append(conds, "[[content]] ILIKE {:"+key+"}")
			}
			if len(conds) > 0 {
				rows := []*mcpMemoryRow{}
				err := e.App.DB().NewQuery(fmt.Sprintf(
					"SELECT %s FROM {{%s}} WHERE %s AND [[namespace]] = {:namespace} AND (%s) ORDER BY [[updated]] DESC LIMIT 50",
					mcpMemoryColumns, mcpMemoriesTable, mcpOwnerParams(owners, params), strings.Join(conds, " OR "),
				)).Bind(params).All(&rows)
				if err != nil {
					return nil, err
				}
				for _, r := range rows {
					if _, ok := linked[r.Id]; ok {
						continue
					}
					mentions = append(mentions, mcpNoteSummary(r))
				}
			}
		}
		result["unlinkedMentions"] = mentions
	}

	return result, nil
}

func mcpNoteDaily(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Namespace string `json:"namespace"`
		Date      string `json:"date"`
		Folder    string `json:"folder"`
		Append    string `json:"append"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	date := strings.TrimSpace(payload.Date)
	if date == "" {
		date = now.Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, mcpToolErr("date must be in YYYY-MM-DD format.")
	}
	folder := strings.Trim(strings.TrimSpace(payload.Folder), "/")
	if folder == "" {
		folder = "daily"
	}
	namespace := strings.TrimSpace(payload.Namespace)
	if namespace == "" {
		namespace = mcpDefaultNamespace
	}

	notePath := folder + "/" + date + ".md"
	current, err := mcpFindNote(e.App, []string{scope.writeOwner}, &mcpNoteRef{Namespace: namespace, Path: notePath})
	if err != nil {
		if _, ok := err.(*mcpToolError); !ok {
			return nil, err
		}
		current = nil
	}

	appendText := strings.TrimSpace(payload.Append)
	if current != nil && appendText == "" {
		return current.toMap(), nil
	}

	content := "# " + date + "\n"
	if current != nil {
		content = strings.TrimRight(current.Content, "\n") + "\n"
	}
	if appendText != "" {
		content += "- " + now.Format("15:04") + " " + appendText + "\n"
	}

	in := &mcpSaveInput{Namespace: namespace, Path: &notePath, Content: &content}
	if current == nil {
		in.Tags = &[]string{"daily"}
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

func mcpNoteTags(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		Namespace     string `json:"namespace"`
		IncludeShared bool   `json:"includeShared"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	params := dbx.Params{}
	filters := []string{mcpOwnerParamsAlias(scope.readOwners(payload.IncludeShared), params, "m.")}
	if ns := strings.TrimSpace(payload.Namespace); ns != "" {
		params["namespace"] = ns
		filters = append(filters, "m.[[namespace]] = {:namespace}")
	}

	tags := []struct {
		Tag   string `db:"tag" json:"tag"`
		Count int    `db:"count" json:"count"`
	}{}
	err = e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT t AS [[tag]], COUNT(*) AS [[count]]
		FROM {{%s}} m, jsonb_array_elements_text(m.[[tags]]) t
		WHERE %s
		GROUP BY t ORDER BY COUNT(*) DESC, t ASC LIMIT 1000`, mcpMemoriesTable, strings.Join(filters, " AND "))).
		Bind(params).All(&tags)
	if err != nil {
		return nil, err
	}

	return map[string]any{"tags": tags}, nil
}

type mcpVersionRow struct {
	Version int    `db:"version" json:"version"`
	Path    string `db:"path" json:"path,omitempty"`
	Title   string `db:"title" json:"title"`
	Content string `db:"content" json:"content,omitempty"`
	Size    int    `db:"size" json:"size"`
	Created string `db:"created" json:"created"`
}

func mcpNoteHistory(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		mcpNoteRef
		Version int `json:"version"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	note, err := mcpFindNote(e.App, scope.readOwners(true), &payload.mcpNoteRef)
	if err != nil {
		return nil, err
	}

	if payload.Version > 0 {
		version, err := mcpLoadVersion(e.App, note.Id, payload.Version)
		if err != nil {
			return nil, err
		}
		return map[string]any{"note": mcpNoteSummary(note), "version": version}, nil
	}

	versions := []*mcpVersionRow{}
	err = e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT [[version]], [[path]], [[title]], LENGTH([[content]]) AS [[size]], [[created]]::text AS [[created]]
		FROM {{%s}} WHERE [[memory]] = {:memory} ORDER BY [[version]] DESC`, mcpVersionsTable)).
		Bind(dbx.Params{"memory": note.Id}).All(&versions)
	if err != nil {
		return nil, err
	}

	return map[string]any{"note": mcpNoteSummary(note), "versions": versions}, nil
}

func mcpLoadVersion(app core.App, memoryId string, version int) (*mcpVersionRow, error) {
	row := &mcpVersionRow{}
	err := app.DB().NewQuery(fmt.Sprintf(`
		SELECT [[version]], [[path]], [[title]], [[content]], LENGTH([[content]]) AS [[size]], [[created]]::text AS [[created]]
		FROM {{%s}} WHERE [[memory]] = {:memory} AND [[version]] = {:version}`, mcpVersionsTable)).
		Bind(dbx.Params{"memory": memoryId, "version": version}).One(row)
	if err != nil {
		return nil, mcpToolErr("Version " + strconv.Itoa(version) + " was not found.")
	}
	return row, nil
}

func mcpNoteRestore(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}

	payload := struct {
		mcpNoteRef
		Version int `json:"version"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	note, err := mcpFindNote(e.App, []string{scope.writeOwner}, &payload.mcpNoteRef)
	if err != nil {
		return nil, err
	}
	version, err := mcpLoadVersion(e.App, note.Id, payload.Version)
	if err != nil {
		return nil, err
	}

	in := &mcpSaveInput{Content: &version.Content}
	if note.Path == "" {
		in.Title = &version.Title
	}
	saved, warning, err := mcpSaveMemory(e, scope, note, in, nil)
	if err != nil {
		return nil, err
	}

	result := saved.toMap()
	result["restoredVersion"] = version.Version
	if warning != "" {
		result["warning"] = warning
	}
	return result, nil
}
