package apis

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
)

// Knowledge graph association.
//
// knowledge_associate retrieves the knowledge related to a question by spreading
// activation over the knowledge graph: starting from the best search hits (or the
// provided notes) it follows the explicit links (wikilinks, typed relations,
// backlinks), the implicit semantic neighbours (close embeddings) and the shared
// tags, with a decay per hop, and explains the association path of each result.
//
// note_graph returns the graph of a vault (Obsidian graph view) and
// knowledge_suggest_links finds semantically close notes that aren't linked yet.

const (
	mcpAssocDefaultLimit   = 15
	mcpAssocMaxLimit       = 50
	mcpAssocDefaultDepth   = 2
	mcpAssocMaxDepth       = 3
	mcpAssocSeedLimit      = 5
	mcpAssocFrontierLimit  = 20
	mcpAssocHopDecay       = 0.7
	mcpAssocSemanticMin    = 0.5
	mcpAssocSemanticPerHop = 5
	mcpAssocTagPerNode     = 8
	mcpAssocMinActivation  = 0.05
	mcpGraphDefaultNodes   = 300
	mcpGraphMaxNodes       = 2000
)

// tags describing the memory kind rather than its topic
var mcpAssocIgnoredTags = map[string]bool{
	mcpSessionTag: true, mcpTodoTag: true, mcpDoneTag: true, mcpDecisionTag: true, mcpProjectSummaryTag: true,
}

var mcpKnowledgeTools []mcpTool

func init() {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	number := func(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
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

	mcpKnowledgeTools = []mcpTool{
		{
			Name: "knowledge_associate",
			Description: "Associative retrieval over the knowledge graph: finds the knowledge related to a question (or to some notes/memories) even when it doesn't match the words, " +
				"by spreading from the best hits along wikilinks, typed relations, backlinks, semantic neighbours and shared tags. Each result explains its association path. " +
				"Use it to explore a topic, gather background before answering, or find connected ideas.",
			InputSchema: obj(map[string]any{
				"query":         str("Question or topic (seeds are the best search hits)."),
				"ids":           strArr("Seed note/memory ids (instead of or in addition to query)."),
				"project":       str("Restrict to a project (knowledge base)."),
				"depth":         integer("Association hops (default 2, max 3)."),
				"limit":         integer("Max associated results (default 15, max 50)."),
				"includeShared": boolean("Include the public knowledge bases (default true)."),
				"semantic":      boolean("Follow implicit semantic neighbours (default true)."),
				"tags":          boolean("Follow shared tags (default true)."),
				"relations":     strArr("Only follow these explicit relation types (default all)."),
			}),
			ReadOnly: true,
			Handler:  mcpKnowledgeAssociate,
		},
		{
			Name:        "note_graph",
			Description: "Graph view of a knowledge base (like the Obsidian graph): notes as nodes, links/relations as edges, with the hubs (most connected), the orphans (unlinked) and the connected clusters.",
			InputSchema: obj(map[string]any{
				"project":       str("Knowledge base (project)."),
				"folder":        str("Only the notes of this folder."),
				"includeShared": boolean("Include the public knowledge bases (default false)."),
				"memories":      boolean("Also include the memories without a note path (default false)."),
				"limit":         integer("Max nodes (default 300, max 2000)."),
			}),
			ReadOnly: true,
			Handler:  mcpNoteGraph,
		},
		{
			Name: "knowledge_suggest_links",
			Description: "Find notes/memories of a knowledge base that are semantically close but not linked yet (link suggestions). " +
				"With apply: true the suggested relations are created, enriching the knowledge graph for knowledge_associate.",
			InputSchema: obj(map[string]any{
				"project":  str("Knowledge base (project)."),
				"id":       str("Only suggestions for this note/memory."),
				"minScore": number("Minimum cosine similarity (default 0.75)."),
				"limit":    integer("Max suggestions (default 20, max 200)."),
				"apply":    boolean("Create the suggested relations (default false)."),
				"relation": str("Relation type used with apply (default \"related_to\")."),
			}),
			Handler: mcpKnowledgeSuggestLinks,
		},
	}
}

// -------------------------------------------------------------------
// Association
// -------------------------------------------------------------------

type mcpAssocOptions struct {
	Query     string
	Ids       []string
	Namespace string
	Depth     int
	Limit     int
	Semantic  bool
	Tags      bool
	Relations []string
	// SeedArgs are passed to memory_search (eg. the superuser ownerId).
	SeedArgs map[string]any
}

type mcpAssocNode struct {
	row        *mcpMemoryRow
	activation float64
	hops       int
	seed       bool
	via        string // id of the node it was reached from
	reason     string
}

func mcpAssocReason(relation string, incoming bool, weight float64) string {
	switch {
	case relation == mcpLinkRelation && incoming:
		return "backlink"
	case relation == mcpLinkRelation:
		return "links to"
	case incoming:
		return "← " + relation
	default:
		return relation
	}
}

// mcpAssociate runs the spreading activation and returns the seeds, the associated nodes and the edges between them.
func mcpAssociate(e *core.RequestEvent, owners []string, opts mcpAssocOptions) ([]*mcpAssocNode, []*mcpAssocNode, []*mcpEdgeRow, error) {
	depth := clampInt(opts.Depth, mcpAssocDefaultDepth, mcpAssocMaxDepth)
	limit := clampInt(opts.Limit, mcpAssocDefaultLimit, mcpAssocMaxLimit)
	nodes := map[string]*mcpAssocNode{}

	// seeds
	if len(opts.Ids) > 0 {
		rows, err := mcpLoadMemories(e.App, opts.Ids, owners)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, r := range rows {
			nodes[r.Id] = &mcpAssocNode{row: r, activation: 1, seed: true}
		}
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		searchArgs := map[string]any{}
		for k, v := range opts.SeedArgs {
			searchArgs[k] = v
		}
		searchArgs["query"] = q
		searchArgs["limit"] = mcpAssocSeedLimit
		if opts.Namespace != "" {
			searchArgs["namespace"] = opts.Namespace
		}
		rawArgs, _ := json.Marshal(searchArgs)
		out, err := mcpMemorySearch(e, rawArgs)
		if err != nil {
			return nil, nil, nil, err
		}
		res, _ := out.(map[string]any)
		items, _ := res["results"].([]map[string]any)
		ids := []string{}
		scores := map[string]float64{}
		for _, item := range items {
			id, _ := item["id"].(string)
			score, _ := item["score"].(float64)
			ids = append(ids, id)
			scores[id] = score
		}
		rows, err := mcpLoadMemories(e.App, ids, owners)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, r := range rows {
			if _, ok := nodes[r.Id]; ok {
				continue
			}
			// semantic scores are cosine similarities, keyword scores are ranks: keep them in 0.3..1
			act := min(max(scores[r.Id], 0.3), 1)
			nodes[r.Id] = &mcpAssocNode{row: r, activation: act, seed: true}
		}
	}
	if len(nodes) == 0 {
		return nil, nil, nil, nil
	}

	// spreading
	activate := func(row *mcpMemoryRow, from *mcpAssocNode, strength float64, reason string, hop int) {
		contribution := from.activation * strength * mcpAssocHopDecay
		if contribution < mcpAssocMinActivation {
			return
		}
		n, ok := nodes[row.Id]
		if !ok {
			nodes[row.Id] = &mcpAssocNode{row: row, activation: contribution, hops: hop, via: from.row.Id, reason: reason}
			return
		}
		if n.seed {
			return
		}
		// probabilistic OR: several association paths reinforce each other
		if contribution > n.activation {
			n.via, n.reason, n.hops = from.row.Id, reason, hop
		}
		n.activation = 1 - (1-n.activation)*(1-contribution)
	}

	frontier := []*mcpAssocNode{}
	for _, n := range nodes {
		frontier = append(frontier, n)
	}

	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		sort.Slice(frontier, func(i, j int) bool { return frontier[i].activation > frontier[j].activation })
		if len(frontier) > mcpAssocFrontierLimit {
			frontier = frontier[:mcpAssocFrontierLimit]
		}
		frontierIds := make([]string, len(frontier))
		byId := map[string]*mcpAssocNode{}
		for i, n := range frontier {
			frontierIds[i] = n.row.Id
			byId[n.row.Id] = n
		}
		before := map[string]bool{}
		for id := range nodes {
			before[id] = true
		}

		// explicit relations (both directions)
		edges := []*mcpEdgeRow{}
		if err := mcpEdgeSelect(e.App).Where(mcpEdgeFilter(frontierIds, owners, opts.Relations, "both")).Limit(mcpMaxEdgesPerLevel).All(&edges); err != nil {
			return nil, nil, nil, err
		}
		neighbourIds := []string{}
		for _, edge := range edges {
			if byId[edge.Source] != nil {
				neighbourIds = append(neighbourIds, edge.Target)
			}
			if byId[edge.Target] != nil {
				neighbourIds = append(neighbourIds, edge.Source)
			}
		}
		neighbours, err := mcpLoadMemories(e.App, neighbourIds, owners) // visibility check
		if err != nil {
			return nil, nil, nil, err
		}
		visible := map[string]*mcpMemoryRow{}
		for _, r := range neighbours {
			visible[r.Id] = r
		}
		for _, edge := range edges {
			weight := edge.Weight
			if weight <= 0 || weight > 1 {
				weight = 1
			}
			strength := 0.9 * weight
			if edge.Relation == mcpLinkRelation {
				strength = 0.85
			}
			if from := byId[edge.Source]; from != nil && visible[edge.Target] != nil {
				activate(visible[edge.Target], from, strength, mcpAssocReason(edge.Relation, false, weight), hop)
			}
			if from := byId[edge.Target]; from != nil && visible[edge.Source] != nil {
				activate(visible[edge.Source], from, strength*0.9, mcpAssocReason(edge.Relation, true, weight), hop)
			}
		}

		// implicit semantic neighbours
		if opts.Semantic {
			for _, from := range frontier {
				if !from.row.HasEmbedding {
					continue
				}
				params := dbx.Params{"id": from.row.Id, "limit": mcpAssocSemanticPerHop, "min": mcpAssocSemanticMin}
				filters := []string{mcpOwnerParamsAlias(owners, params, "m."), "m.[[id]] <> {:id}", "m.[[embedding]] IS NOT NULL",
					"m.[[embeddingModel]] = s.[[embeddingModel]]", "vector_dims(m.[[embedding]]) = vector_dims(s.[[embedding]])"}
				if opts.Namespace != "" {
					params["namespace"] = opts.Namespace
					filters = append(filters, "(m.[[namespace]] = {:namespace} OR m.[[owner]] = '')")
				}
				rows := []*mcpMemoryRow{}
				query := fmt.Sprintf(`
					SELECT %s, 1 - (m.[[embedding]] <=> s.[[embedding]]) AS [[score]]
					FROM {{%s}} m, (SELECT [[embedding]], [[embeddingModel]] FROM {{%s}} WHERE [[id]] = {:id}) s
					WHERE %s AND 1 - (m.[[embedding]] <=> s.[[embedding]]) >= {:min}
					ORDER BY m.[[embedding]] <=> s.[[embedding]] LIMIT {:limit}`,
					mcpPrefixedMemoryColumns("m."), mcpMemoriesTable, mcpMemoriesTable, strings.Join(filters, " AND "))
				if err := e.App.DB().NewQuery(query).Bind(params).All(&rows); err != nil {
					return nil, nil, nil, err
				}
				for _, r := range rows {
					activate(r, from, r.Score*0.75, fmt.Sprintf("semantically similar (%.2f)", r.Score), hop)
				}
			}
		}

		// shared topical tags
		if opts.Tags {
			for _, from := range frontier {
				topical := []string{}
				for _, t := range from.row.tags() {
					if !mcpAssocIgnoredTags[strings.ToLower(t)] {
						topical = append(topical, t)
					}
				}
				if len(topical) == 0 {
					continue
				}
				params := dbx.Params{"id": from.row.Id, "limit": mcpAssocTagPerNode}
				tagConds := make([]string, len(topical))
				for i, t := range topical {
					raw, _ := json.Marshal([]string{t})
					key := "tag" + strconv.Itoa(i)
					params[key] = string(raw)
					tagConds[i] = "(CASE WHEN [[tags]] @> {:" + key + "}::jsonb THEN 1 ELSE 0 END)"
				}
				shared := strings.Join(tagConds, " + ")
				filters := []string{mcpOwnerParams(owners, params), "[[id]] <> {:id}", "(" + shared + ") > 0"}
				if opts.Namespace != "" {
					params["namespace"] = opts.Namespace
					filters = append(filters, "([[namespace]] = {:namespace} OR [[owner]] = '')")
				}
				rows := []*mcpMemoryRow{}
				if err := e.App.DB().NewQuery(fmt.Sprintf(`
					SELECT %s, (%s)::float AS [[score]] FROM {{%s}} WHERE %s
					ORDER BY [[score]] DESC, [[updated]] DESC LIMIT {:limit}`,
					mcpMemoryColumns, shared, mcpMemoriesTable, strings.Join(filters, " AND "),
				)).Bind(params).All(&rows); err != nil {
					return nil, nil, nil, err
				}
				for _, r := range rows {
					common := []string{}
					rowTags := map[string]bool{}
					for _, t := range r.tags() {
						rowTags[strings.ToLower(t)] = true
					}
					for _, t := range topical {
						if rowTags[strings.ToLower(t)] {
							common = append(common, "#"+t)
						}
					}
					strength := 0.35 + 0.15*min(float64(len(common)-1), 2)
					activate(r, from, strength, "shared tags "+strings.Join(common, " "), hop)
				}
			}
		}

		next := []*mcpAssocNode{}
		for id, n := range nodes {
			if !before[id] {
				next = append(next, n)
			}
		}
		frontier = next
	}

	seeds := []*mcpAssocNode{}
	associated := []*mcpAssocNode{}
	for _, n := range nodes {
		if n.seed {
			seeds = append(seeds, n)
		} else {
			associated = append(associated, n)
		}
	}
	sort.Slice(seeds, func(i, j int) bool { return seeds[i].activation > seeds[j].activation })
	sort.Slice(associated, func(i, j int) bool { return associated[i].activation > associated[j].activation })
	if len(associated) > limit {
		associated = associated[:limit]
	}

	ids := []string{}
	for _, n := range append(append([]*mcpAssocNode{}, seeds...), associated...) {
		ids = append(ids, n.row.Id)
	}
	allEdges, err := mcpLoadEdgesBetween(e.App, ids, owners)
	if err != nil {
		return nil, nil, nil, err
	}
	inResult := map[string]bool{}
	for _, id := range ids {
		inResult[id] = true
	}
	edges := []*mcpEdgeRow{}
	for _, edge := range allEdges {
		if inResult[edge.Source] && inResult[edge.Target] {
			edges = append(edges, edge)
		}
	}

	return seeds, associated, edges, nil
}

// mcpPrefixedMemoryColumns returns mcpMemoryColumns with a table alias.
func mcpPrefixedMemoryColumns(alias string) string {
	cols := mcpMemoryColumns
	for _, c := range []string{"id", "owner", "namespace", "path", "title", "content", "tags", "metadata", "properties", "links",
		"embeddingModel", "pinned", "accessCount", "embedding", "created", "updated"} {
		cols = strings.ReplaceAll(cols, "[["+c+"]]", alias+"[["+c+"]]")
	}
	// keep the output column names unprefixed
	cols = strings.ReplaceAll(cols, "AS "+alias+"[[", "AS [[")
	return cols
}

func (n *mcpAssocNode) toMap(titles map[string]string) map[string]any {
	item := map[string]any{
		"id":         n.row.Id,
		"title":      n.row.Title,
		"project":    n.row.Namespace,
		"tags":       n.row.tags(),
		"content":    mcpTruncate(n.row.Content, 500),
		"activation": float64(int(n.activation*1000)) / 1000,
	}
	if n.row.Path != "" {
		item["path"] = n.row.Path
	}
	if n.row.Owner == "" {
		item["shared"] = true
	}
	if !n.seed {
		item["hops"] = n.hops
		item["via"] = map[string]any{"id": n.via, "title": titles[n.via], "relation": n.reason}
	}
	return item
}

func mcpKnowledgeAssociate(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Query         string   `json:"query"`
		Ids           []string `json:"ids"`
		Namespace     string   `json:"namespace"`
		Depth         int      `json:"depth"`
		Limit         int      `json:"limit"`
		IncludeShared *bool    `json:"includeShared"`
		Semantic      *bool    `json:"semantic"`
		Tags          *bool    `json:"tags"`
		Relations     []string `json:"relations"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.Query) == "" && len(payload.Ids) == 0 {
		return nil, mcpToolErr("query or ids is required.")
	}

	includeShared := payload.IncludeShared == nil || *payload.IncludeShared
	seedArgs := map[string]any{"includeShared": includeShared}
	raw := map[string]any{}
	_ = json.Unmarshal(args, &raw)
	for _, k := range []string{"ownerId", "shared"} {
		if v, ok := raw[k]; ok {
			seedArgs[k] = v
		}
	}

	seeds, associated, edges, err := mcpAssociate(e, scope.readOwners(includeShared), mcpAssocOptions{
		Query:     payload.Query,
		Ids:       payload.Ids,
		Namespace: payload.Namespace,
		Depth:     payload.Depth,
		Limit:     payload.Limit,
		Semantic:  payload.Semantic == nil || *payload.Semantic,
		Tags:      payload.Tags == nil || *payload.Tags,
		Relations: normalizeTags(payload.Relations),
		SeedArgs:  seedArgs,
	})
	if err != nil {
		return nil, err
	}

	titles := map[string]string{}
	for _, n := range append(append([]*mcpAssocNode{}, seeds...), associated...) {
		titles[n.row.Id] = n.row.Title
	}
	seedItems := make([]map[string]any, len(seeds))
	for i, n := range seeds {
		seedItems[i] = n.toMap(titles)
	}
	assocItems := make([]map[string]any, len(associated))
	for i, n := range associated {
		assocItems[i] = n.toMap(titles)
	}
	edgeItems := make([]map[string]any, len(edges))
	for i, edge := range edges {
		edgeItems[i] = map[string]any{"source": edge.Source, "target": edge.Target, "relation": edge.Relation}
	}

	ids := []string{}
	for id := range titles {
		ids = append(ids, id)
	}
	mcpTouchMemories(e.App, scope.writeOwner, ids)

	return map[string]any{
		"seeds":      seedItems,
		"associated": assocItems,
		"edges":      edgeItems,
		"count":      len(assocItems),
	}, nil
}

// -------------------------------------------------------------------
// Graph view
// -------------------------------------------------------------------

func mcpNoteGraph(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Namespace     string `json:"namespace"`
		Folder        string `json:"folder"`
		IncludeShared bool   `json:"includeShared"`
		Memories      bool   `json:"memories"`
		Limit         int    `json:"limit"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}

	owners := scope.readOwners(payload.IncludeShared)
	params := dbx.Params{"limit": clampInt(payload.Limit, mcpGraphDefaultNodes, mcpGraphMaxNodes)}
	filters := []string{mcpOwnerParams(owners, params)}
	if !payload.Memories {
		filters = append(filters, "[[path]] <> ''")
	}
	if ns := strings.TrimSpace(payload.Namespace); ns != "" {
		params["namespace"] = ns
		filters = append(filters, "[[namespace]] = {:namespace}")
	}
	if folder := strings.Trim(strings.TrimSpace(payload.Folder), "/"); folder != "" {
		params["folder"] = escapeLike(strings.ToLower(folder)) + "/%"
		filters = append(filters, "LOWER([[path]]) LIKE {:folder}")
	}

	rows := []*mcpMemoryRow{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(
		"SELECT %s FROM {{%s}} WHERE %s ORDER BY [[updated]] DESC LIMIT {:limit}", mcpMemoryColumns, mcpMemoriesTable, strings.Join(filters, " AND "),
	)).Bind(params).All(&rows); err != nil {
		return nil, err
	}

	ids := make([]string, len(rows))
	index := map[string]int{}
	for i, r := range rows {
		ids[i] = r.Id
		index[r.Id] = i
	}
	allEdges, err := mcpLoadEdgesBetween(e.App, ids, owners)
	if err != nil {
		return nil, err
	}

	// union-find for the connected clusters
	parent := make([]int, len(rows))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}

	in := make([]int, len(rows))
	out := make([]int, len(rows))
	edges := []map[string]any{}
	for _, edge := range allEdges {
		si, sok := index[edge.Source]
		ti, tok := index[edge.Target]
		if !sok || !tok {
			continue
		}
		out[si]++
		in[ti]++
		parent[find(si)] = find(ti)
		edges = append(edges, map[string]any{"source": edge.Source, "target": edge.Target, "relation": edge.Relation})
	}

	nodes := make([]map[string]any, len(rows))
	clusters := map[int][]int{}
	orphans := []map[string]any{}
	for i, r := range rows {
		root := find(i)
		clusters[root] = append(clusters[root], i)
		node := map[string]any{
			"id":      r.Id,
			"title":   r.Title,
			"project": r.Namespace,
			"tags":    r.tags(),
			"in":      in[i],
			"out":     out[i],
			"degree":  in[i] + out[i],
		}
		if r.Path != "" {
			node["path"] = r.Path
		}
		if r.Owner == "" {
			node["shared"] = true
		}
		nodes[i] = node
		if in[i]+out[i] == 0 {
			orphans = append(orphans, map[string]any{"id": r.Id, "title": r.Title, "path": r.Path})
		}
	}

	hubIdx := make([]int, len(rows))
	for i := range hubIdx {
		hubIdx[i] = i
	}
	sort.SliceStable(hubIdx, func(a, b int) bool { return in[hubIdx[a]]+out[hubIdx[a]] > in[hubIdx[b]]+out[hubIdx[b]] })
	hubs := []map[string]any{}
	for _, i := range hubIdx {
		if len(hubs) >= 10 || in[i]+out[i] == 0 {
			break
		}
		hubs = append(hubs, map[string]any{"id": rows[i].Id, "title": rows[i].Title, "degree": in[i] + out[i], "backlinks": in[i]})
	}

	clusterList := []map[string]any{}
	for _, members := range clusters {
		if len(members) < 2 {
			continue
		}
		best := members[0]
		for _, m := range members {
			if in[m]+out[m] > in[best]+out[best] {
				best = m
			}
		}
		clusterList = append(clusterList, map[string]any{"size": len(members), "hub": rows[best].Title, "hubId": rows[best].Id})
	}
	sort.Slice(clusterList, func(a, b int) bool { return clusterList[a]["size"].(int) > clusterList[b]["size"].(int) })

	return map[string]any{
		"nodes":    nodes,
		"edges":    edges,
		"hubs":     hubs,
		"orphans":  orphans,
		"clusters": clusterList,
		"stats": map[string]any{
			"nodes":    len(nodes),
			"edges":    len(edges),
			"orphans":  len(orphans),
			"clusters": len(clusterList),
		},
	}, nil
}

// -------------------------------------------------------------------
// Link suggestions
// -------------------------------------------------------------------

func mcpKnowledgeSuggestLinks(e *core.RequestEvent, args json.RawMessage) (any, error) {
	scope, err := mcpResolveScope(e, args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Namespace string   `json:"namespace"`
		Id        string   `json:"id"`
		MinScore  *float64 `json:"minScore"`
		Limit     int      `json:"limit"`
		Apply     bool     `json:"apply"`
		Relation  string   `json:"relation"`
	}{}
	if err := mcpBindArgs(args, &payload); err != nil {
		return nil, err
	}
	minScore := 0.75
	if payload.MinScore != nil {
		minScore = *payload.MinScore
	}
	limit := clampInt(payload.Limit, 20, 200)
	relation := strings.TrimSpace(payload.Relation)
	if relation == "" {
		relation = "related_to"
	}
	if relation == mcpLinkRelation {
		return nil, mcpToolErr("links_to is reserved for the wikilinks.")
	}

	namespace := strings.TrimSpace(payload.Namespace)
	if namespace == "" && strings.TrimSpace(payload.Id) == "" {
		namespace = mcpDefaultNamespace
	}

	ignored, _ := json.Marshal([]string{mcpSessionTag})
	ignoredSummary, _ := json.Marshal([]string{mcpProjectSummaryTag})
	params := dbx.Params{"owner": scope.writeOwner, "min": minScore, "ignored": string(ignored), "ignoredSummary": string(ignoredSummary)}
	filters := []string{"a.[[owner]] = {:owner}", "a.[[embedding]] IS NOT NULL",
		"NOT a.[[tags]] @> {:ignored}::jsonb", "NOT a.[[tags]] @> {:ignoredSummary}::jsonb"}
	if namespace != "" {
		params["namespace"] = namespace
		filters = append(filters, "a.[[namespace]] = {:namespace}")
	}
	if id := strings.TrimSpace(payload.Id); id != "" {
		params["id"] = id
		filters = append(filters, "a.[[id]] = {:id}")
	}

	suggestions := []struct {
		Source      string  `db:"source"`
		SourceTitle string  `db:"sourceTitle"`
		Target      string  `db:"target"`
		TargetTitle string  `db:"targetTitle"`
		Score       float64 `db:"score"`
	}{}
	if err := e.App.DB().NewQuery(fmt.Sprintf(`
		SELECT a.[[id]] AS [[source]], a.[[title]] AS [[sourceTitle]], b.[[id]] AS [[target]], b.[[title]] AS [[targetTitle]], b.[[score]]
		FROM (SELECT * FROM {{%s}} a WHERE %s ORDER BY a.[[updated]] DESC LIMIT 500) a
		CROSS JOIN LATERAL (
			SELECT x.[[id]], x.[[title]], 1 - (x.[[embedding]] <=> a.[[embedding]]) AS [[score]]
			FROM {{%s}} x
			WHERE x.[[owner]] = a.[[owner]] AND x.[[namespace]] = a.[[namespace]] AND x.[[id]] <> a.[[id]]
				AND x.[[embedding]] IS NOT NULL AND x.[[embeddingModel]] = a.[[embeddingModel]]
				AND vector_dims(x.[[embedding]]) = vector_dims(a.[[embedding]])
				AND NOT x.[[tags]] @> {:ignored}::jsonb AND NOT x.[[tags]] @> {:ignoredSummary}::jsonb
			ORDER BY x.[[embedding]] <=> a.[[embedding]] LIMIT 3
		) b
		WHERE b.[[score]] >= {:min}
			AND NOT EXISTS (
				SELECT 1 FROM {{%s}} ed WHERE ed.[[owner]] = a.[[owner]]
				AND ((ed.[[source]] = a.[[id]] AND ed.[[target]] = b.[[id]]) OR (ed.[[source]] = b.[[id]] AND ed.[[target]] = a.[[id]]))
			)
		ORDER BY b.[[score]] DESC`,
		mcpMemoriesTable, strings.Join(filters, " AND "), mcpMemoriesTable, mcpEdgesTable,
	)).Bind(params).All(&suggestions); err != nil {
		return nil, err
	}

	// each pair once
	seen := map[string]bool{}
	items := []map[string]any{}
	pairs := [][2]string{}
	for _, s := range suggestions {
		key := s.Source + "|" + s.Target
		if s.Target < s.Source {
			key = s.Target + "|" + s.Source
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, map[string]any{
			"source": s.Source, "sourceTitle": s.SourceTitle,
			"target": s.Target, "targetTitle": s.TargetTitle,
			"score": float64(int(s.Score*1000)) / 1000,
		})
		pairs = append(pairs, [2]string{s.Source, s.Target})
		if len(items) >= limit {
			break
		}
	}

	applied := 0
	if payload.Apply && len(pairs) > 0 {
		err := e.App.RunInTransaction(func(txApp core.App) error {
			for i, p := range pairs {
				score, _ := items[i]["score"].(float64)
				if _, err := mcpUpsertEdge(txApp, scope, p[0], p[1], relation, score, map[string]any{"suggested": true}); err != nil {
					return err
				}
				applied++
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	result := map[string]any{"suggestions": items, "count": len(items)}
	if payload.Apply {
		result["applied"] = applied
		result["relation"] = relation
	}
	return result, nil
}

func mcpAssocItems(nodes []*mcpAssocNode, titles map[string]string) []map[string]any {
	items := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		items[i] = n.toMap(titles)
	}
	return items
}
