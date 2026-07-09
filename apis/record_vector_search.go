package apis

import (
	"fmt"
	"net/http"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/search"
)

// defaultVectorSearchLimit is the number of nearest-neighbour records returned
// when the request does not explicitly provide a limit.
const defaultVectorSearchLimit = 20

// maxVectorSearchLimit caps how many nearest-neighbour records a single
// request may ask for (protects against expensive top-k scans).
const maxVectorSearchLimit = 200

// recordVectorSearchRequest is the JSON body accepted by the record vector
// similarity search endpoint (POST /api/collections/{collection}/records/search).
type recordVectorSearchRequest struct {
	// Field is the name of the target "vector" type field to search against.
	Field string `json:"field"`

	// QueryVector is the embedding to compare stored vectors with.
	// Its length must match the field's configured dimension.
	QueryVector []float64 `json:"queryVector"`

	// Limit is the maximum number of nearest records to return.
	// Defaults to defaultVectorSearchLimit and is capped at maxVectorSearchLimit.
	Limit int `json:"limit"`

	// Distance optionally overrides the pgvector metric used for ordering.
	// Valid values for the float vector(N) type: "cosine", "l2", "inner_product", "l1".
	// When empty it falls back to the field's configured distance (or "cosine").
	Distance string `json:"distance"`

	// Filter is an optional standard record filter expression that is applied
	// before the nearest-neighbour ordering. Use it to combine vector similarity
	// with other collection fields, for example:
	// `status = "active" && category = "docs" && tenantId = "tenant_123"`.
	Filter string `json:"filter"`

	// IncludeDistance controls whether each returned record is augmented with
	// the computed "_distance" (raw metric value) and "_score" (0-1 similarity)
	// custom fields. Defaults to true when omitted.
	IncludeDistance *bool `json:"includeDistance"`
}

// vectorDistanceExpr builds the pgvector distance SQL expression used for the
// ORDER BY clause. The expression evaluates to the RAW distance so that
// `ORDER BY <expr> ASC` always returns the nearest neighbours first.
//
//   - cosine:        identifier <=> {:param}::vector
//   - l2:            identifier <-> {:param}::vector
//   - inner_product: identifier <#> {:param}::vector
//   - l1:            identifier <+> {:param}::vector
//
// The identifier is expected to already be a safely quoted column reference
// (e.g. "[[table.field]]") and param is the name of a bound query parameter
// holding the pgvector literal (e.g. "qvec").
func vectorDistanceExpr(identifier string, metric string, param string) string {
	var op string
	switch metric {
	case "l2":
		op = "<->"
	case "inner_product":
		op = "<#>"
	case "l1":
		op = "<+>"
	default: // "cosine"
		op = "<=>"
	}

	return fmt.Sprintf("%s %s {:%s}::vector", identifier, op, param)
}

// vectorSimilarityScore converts a raw pgvector distance into a normalized
// similarity score. Higher is more similar; cosine/l2/l1 land in the 0-1 range.
func vectorSimilarityScore(metric string, distance float64) float64 {
	switch metric {
	case "l2", "l1":
		// Map the (unbounded, >=0) L2/L1 distance into a 0-1 range.
		return 1.0 / (1.0 + distance)
	case "inner_product":
		// pgvector's <#> operator returns the negative inner product, so the
		// actual inner product (higher = more similar) is its negation.
		return -distance
	default: // "cosine"
		// pgvector's <=> operator returns the cosine distance, so similarity
		// is simply 1 - distance.
		return 1.0 - distance
	}
}

// recordVectorSearch performs a pgvector nearest-neighbour search against a
// regular collection's "vector" type field and returns the matching records
// ordered from most to least similar.
//
// It mirrors the standard recordsList pipeline (list-rule enforcement, filters,
// enrich/expand, hidden-field rules) so the result is a normal record list
// response, optionally augmented with per-record "_distance"/"_score" fields.
func recordVectorSearch(e *core.RequestEvent) error {
	collection, err := e.App.FindCachedCollectionByNameOrId(e.Request.PathValue("collection"))
	if err != nil || collection == nil {
		return e.NotFoundError("Missing collection context.", err)
	}

	// vector similarity search relies on pgvector operators and is Postgres-only.
	if !core.IsPostgresDriver(core.BuilderDriverName(e.App.NonconcurrentDB())) {
		return e.BadRequestError("Vector search requires PostgreSQL.", nil)
	}

	err = checkCollectionRateLimit(e, collection, "list")
	if err != nil {
		return err
	}

	requestInfo, err := e.RequestInfo()
	if err != nil {
		return firstApiError(err, e.BadRequestError("", err))
	}

	// same access guard as the list endpoint: a locked list rule (nil) is
	// reserved for superusers only.
	if collection.ListRule == nil && !requestInfo.HasSuperuserAuth() {
		return e.ForbiddenError("Only superusers can perform this action.", nil)
	}

	// forbid users and guests to query special filter/sort fields
	err = checkForSuperuserOnlyRuleFields(requestInfo)
	if err != nil {
		return err
	}

	// parse and validate the request body
	req := &recordVectorSearchRequest{}
	if err = e.BindBody(req); err != nil {
		return e.BadRequestError("Failed to read the request body.", err)
	}

	if req.Field == "" {
		return e.BadRequestError("The 'field' parameter is required.", nil)
	}

	field := collection.Fields.GetByName(req.Field)
	if field == nil {
		return e.BadRequestError(fmt.Sprintf("Missing or unknown field %q.", req.Field), nil)
	}

	vectorField, ok := field.(*core.VectorField)
	if !ok || field.Type() != core.FieldTypeVector {
		return e.BadRequestError(fmt.Sprintf("Field %q is not a vector field.", req.Field), nil)
	}

	if len(req.QueryVector) == 0 {
		return e.BadRequestError("The 'queryVector' parameter is required.", nil)
	}

	// resolve the field's effective dimension (0 or negative means the default).
	dimension := vectorField.Dimension
	if dimension <= 0 {
		dimension = 1536
	}
	if len(req.QueryVector) != dimension {
		return e.BadRequestError(
			fmt.Sprintf("Query vector dimension mismatch. Expected %d, got %d.", dimension, len(req.QueryVector)),
			nil,
		)
	}

	// resolve the distance metric (request override -> field config -> cosine).
	metric := req.Distance
	if metric == "" {
		metric = vectorField.Distance
	}
	if metric == "" {
		metric = "cosine"
	}
	if metric != "cosine" && metric != "l2" && metric != "inner_product" && metric != "l1" {
		return e.BadRequestError(fmt.Sprintf("Invalid distance metric %q.", metric), nil)
	}

	// normalize the limit.
	limit := req.Limit
	if limit <= 0 {
		limit = defaultVectorSearchLimit
	}
	if limit > maxVectorSearchLimit {
		limit = maxVectorSearchLimit
	}

	query := e.App.RecordQuery(collection)

	fieldsResolver := core.NewRecordFieldResolver(e.App, collection, requestInfo, true)

	// enforce the collection list rule for non-superusers (same as recordsList).
	if !requestInfo.HasSuperuserAuth() && collection.ListRule != nil && *collection.ListRule != "" {
		expr, err := search.FilterData(*collection.ListRule).BuildExpr(fieldsResolver)
		if err != nil {
			return err
		}
		query.AndWhere(expr)
	}

	// hidden fields are searchable only by superusers
	fieldsResolver.SetAllowHiddenFields(requestInfo.HasSuperuserAuth())

	// resolve the target field to a safe, quoted column identifier.
	resolved, err := fieldsResolver.Resolve(req.Field)
	if err != nil || resolved.Identifier == "" || len(resolved.Params) > 0 {
		return e.BadRequestError(fmt.Sprintf("Unable to resolve vector field %q.", req.Field), err)
	}

	// bind the query vector as a real SQL parameter (never string-concatenated)
	// and order by the raw distance ascending (nearest neighbours first).
	queryVectorLiteral := formatVectorForPostgres(req.QueryVector)
	query.AndBind(dbx.Params{"qvec": queryVectorLiteral})
	query.AndOrderBy(vectorDistanceExpr(resolved.Identifier, metric, "qvec") + " ASC")

	records := []*core.Record{}
	searchProvider := search.NewProvider(fieldsResolver).
		Query(query).
		SkipTotal(true). // a total count is meaningless for a top-k NN query
		Page(1).
		PerPage(limit)

	// Compose the optional client filter with the nearest-neighbour ordering.
	// The filter is resolved by the same provider used by the normal list API,
	// so vector search can be restricted by any searchable non-vector fields
	// (status, category, tenantId, relation fields, etc.) before distance sorting.
	if req.Filter != "" {
		searchProvider.AddFilter(search.FilterData(req.Filter))
	}

	result, err := searchProvider.Exec(&records)
	if err != nil {
		return firstApiError(err, e.BadRequestError("", err))
	}

	// distances are attached (opt-out via includeDistance=false) after the main
	// query because the record scanner drops non-schema columns such as _distance.
	includeDistance := req.IncludeDistance == nil || *req.IncludeDistance
	if includeDistance && len(records) > 0 {
		if err = attachVectorDistances(e, collection, req.Field, metric, queryVectorLiteral, records); err != nil {
			return firstApiError(err, e.InternalServerError("Failed to compute vector distances.", err))
		}
	}

	event := new(core.RecordsListRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Records = records
	event.Result = result

	return e.App.OnRecordsListRequest().Trigger(event, func(e *core.RecordsListRequestEvent) error {
		if err := EnrichRecords(e.RequestEvent, e.Records); err != nil {
			return firstApiError(err, e.InternalServerError("Failed to enrich records", err))
		}

		return execAfterSuccessTx(true, e.App, func() error {
			return e.JSON(http.StatusOK, e.Result)
		})
	})
}

// attachVectorDistances runs a lightweight follow-up query to compute the raw
// distance for each already-fetched record and stores it (together with a
// normalized "_score") as custom data on the record so it is serialized in the
// JSON response.
func attachVectorDistances(
	e *core.RequestEvent,
	collection *core.Collection,
	fieldName string,
	metric string,
	queryVectorLiteral string,
	records []*core.Record,
) error {
	ids := make([]any, len(records))
	for i, r := range records {
		ids[i] = r.Id
	}

	// order-independent lookup keyed by record id; the column name of a base
	// field matches the field name, so "[[field]]" is safe here.
	distanceExpr := vectorDistanceExpr("[["+fieldName+"]]", metric, "qvec")

	rows := []struct {
		Id       string  `db:"id"`
		Distance float64 `db:"distance"`
	}{}

	err := e.App.ConcurrentDB().
		Select("[[id]] AS id", "("+distanceExpr+") AS distance").
		From(collection.Name).
		Where(dbx.In("id", ids...)).
		Bind(dbx.Params{"qvec": queryVectorLiteral}).
		All(&rows)
	if err != nil {
		return err
	}

	distanceById := make(map[string]float64, len(rows))
	for _, row := range rows {
		distanceById[row.Id] = row.Distance
	}

	for _, record := range records {
		distance, found := distanceById[record.Id]
		if !found {
			continue
		}

		// custom (non-schema) data must be explicitly enabled to be serialized.
		record.WithCustomData(true)
		record.Set("_distance", distance)
		record.Set("_score", vectorSimilarityScore(metric, distance))
	}

	return nil
}
