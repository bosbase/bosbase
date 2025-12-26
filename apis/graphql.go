package apis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/forms"
	"github.com/bosbase/bosbase-enterprise/tools/inflector"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/search"
	"github.com/bosbase/bosbase-enterprise/tools/security"
	"github.com/spf13/cast"
	gql "graphql-go"
	gqlerrors "graphql-go/errors"
)

const graphqlSchemaStoreKey = "api_graphql_schema"

type graphqlContextKey struct{}
type graphqlVariablesContextKey struct{}

type graphQLRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

func bindGraphqlApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	rg.POST("/graphql", graphqlExecute(app))
}

func graphqlExecute(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		schema, err := graphqlSchema(app)
		if err != nil {
			return e.InternalServerError("Failed to prepare GraphQL schema.", err)
		}

		req := &graphQLRequest{}
		if err := e.BindBody(req); err != nil {
			resp := &gql.Response{
				Errors: []*gqlerrors.QueryError{
					gqlerrors.Errorf("failed to parse request body: %v", err),
				},
			}
			return e.JSON(http.StatusOK, resp)
		}

		requestInfo, err := e.RequestInfo()
		if err != nil {
			return firstApiError(err, e.BadRequestError("", err))
		}
		if requestInfo.Auth == nil || !requestInfo.Auth.IsSuperuser() {
			return e.ForbiddenError("Only superusers can access the GraphQL API.", nil)
		}

		variables := req.Variables
		if variables == nil {
			variables = map[string]any{}
		}

		ctx := context.WithValue(e.Request.Context(), graphqlContextKey{}, e)
		ctx = context.WithValue(ctx, graphqlVariablesContextKey{}, variables)

		resp := schema.Exec(ctx, req.Query, req.OperationName, variables)

		return e.JSON(http.StatusOK, resp)
	}
}

func graphqlSchema(app core.App) (*gql.Schema, error) {
	if cached, ok := app.Store().Get(graphqlSchemaStoreKey).(*gql.Schema); ok && cached != nil {
		return cached, nil
	}

	schema, err := gql.ParseSchema(graphqlSchemaString, &graphQLResolver{app: app}, gql.UseFieldResolvers())
	if err != nil {
		return nil, err
	}

	app.Store().Set(graphqlSchemaStoreKey, schema)

	return schema, nil
}

const graphqlSchemaString = `
scalar JSON

type Record {
    id: ID!
    collectionId: String!
    collectionName: String!
    created: String!
    updated: String!
    data: JSON!
}

type RecordList {
    page: Int!
    perPage: Int!
    totalItems: Int!
    totalPages: Int!
    items: [Record!]!
}

type Query {
    records(
        collection: String!
        page: Int
        perPage: Int
        sort: String
        filter: String
        expand: [String!]
        skipTotal: Boolean
    ): RecordList!
}

type Mutation {
    createRecord(collection: String!, data: JSON!, expand: [String!]): Record!
    updateRecord(collection: String!, id: ID!, data: JSON!, expand: [String!]): Record!
    deleteRecord(collection: String!, id: ID!): Boolean!
}
`

type jsonMap map[string]any

func (jsonMap) ImplementsGraphQLType(name string) bool { return name == "JSON" }

func (m *jsonMap) UnmarshalGraphQL(input interface{}) error {
	if input == nil {
		*m = jsonMap{}
		return nil
	}

	v, ok := input.(map[string]any)
	if !ok {
		return fmt.Errorf("unsupported JSON input type %T", input)
	}

	*m = v

	return nil
}

type graphQLResolver struct {
	app core.App
}

// Query satisfies the root Query resolver.
func (r *graphQLResolver) Query() *graphQLResolver {
	return r
}

// Mutation satisfies the root Mutation resolver.
func (r *graphQLResolver) Mutation() *graphQLResolver {
	return r
}

type recordsArgs struct {
	Collection string
	Page       *int32
	PerPage    *int32
	Sort       *string
	Filter     *string
	Expand     *[]string
	SkipTotal  *bool
}

type createRecordArgs struct {
	Collection string
	Data       jsonMap
	Expand     *[]string
}

type updateRecordArgs struct {
	Collection string
	Id         string
	Data       jsonMap
	Expand     *[]string
}

type deleteRecordArgs struct {
	Collection string
	Id         string
}

func (r *graphQLResolver) Records(ctx context.Context, args recordsArgs) (*recordListResolver, error) {
	e, err := requestEventFromContext(ctx)
	if err != nil {
		return nil, err
	}

	collection, err := e.App.FindCachedCollectionByNameOrId(args.Collection)
	if err != nil || collection == nil {
		return nil, e.NotFoundError("Missing collection context.", err)
	}

	if err := checkCollectionRateLimit(e, collection, "list"); err != nil {
		return nil, err
	}

	requestInfo, err := e.RequestInfo()
	if err != nil {
		return nil, err
	}

	variables := graphqlVariablesFromContext(ctx)

	filter := ""
	if args.Filter != nil {
		filter = applyFilterVariables(*args.Filter, variables)
	}

	sort := ""
	if args.Sort != nil {
		sort = *args.Sort
	}

	expand := normalizeExpand(args.Expand)

	applyGraphQLSearchArgs(requestInfo, filter, sort, expand, args.Page, args.PerPage)

	if collection.ListRule == nil && !requestInfo.HasSuperuserAuth() {
		return nil, e.ForbiddenError("Only superusers can perform this action.", nil)
	}

	if err := checkForSuperuserOnlyRuleFields(requestInfo); err != nil {
		return nil, err
	}

	query := e.App.RecordQuery(collection)

	fieldsResolver := core.NewRecordFieldResolver(e.App, collection, requestInfo, true)

	if !requestInfo.HasSuperuserAuth() && collection.ListRule != nil && *collection.ListRule != "" {
		expr, err := search.FilterData(*collection.ListRule).BuildExpr(fieldsResolver)
		if err != nil {
			return nil, err
		}
		query.AndWhere(expr)
	}

	fieldsResolver.SetAllowHiddenFields(requestInfo.HasSuperuserAuth())

	searchProvider := search.NewProvider(fieldsResolver).Query(query)

	if !collection.IsView() {
		driver := core.BuilderDriverName(e.App.NonconcurrentDB())
		if driver == "sqlite" || driver == "sqlite3" {
			searchProvider.CountCol("_rowid_")
		}
	}

	if args.Page != nil {
		searchProvider.Page(int(*args.Page))
	}
	if args.PerPage != nil {
		searchProvider.PerPage(int(*args.PerPage))
	}
	if sort != "" {
		for _, sort := range search.ParseSortFromString(sort) {
			searchProvider.AddSort(sort)
		}
	}
	if filter != "" {
		searchProvider.AddFilter(search.FilterData(filter))
	}
	if args.SkipTotal != nil {
		searchProvider.SkipTotal(*args.SkipTotal)
	}

	records := []*core.Record{}
	result, err := searchProvider.Exec(&records)
	if err != nil {
		return nil, err
	}

	event := new(core.RecordsListRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Records = records
	event.Result = result

	if err := e.App.OnRecordsListRequest().Trigger(event, func(e *core.RecordsListRequestEvent) error {
		if err := EnrichRecords(e.RequestEvent, e.Records); err != nil {
			return err
		}

		if !e.HasSuperuserAuth() &&
			collection.ListRule != nil &&
			*collection.ListRule != "" &&
			requestInfo.Query[search.FilterQueryParam] != "" &&
			len(e.Records) == 0 &&
			checkRateLimit(e.RequestEvent, "@pb_list_timing_check_"+collection.Id, listTimingRateLimitRule) != nil {
			e.App.Logger().Debug("Randomized throttle because of too many failed searches", "collectionId", collection.Id)
			randomizedThrottle(150)
		}

		return execAfterSuccessTx(true, e.App, func() error {
			return nil
		})
	}); err != nil {
		return nil, err
	}

	return &recordListResolver{result: result, records: records}, nil
}

func (r *graphQLResolver) CreateRecord(ctx context.Context, args createRecordArgs) (*recordResolver, error) {
	e, err := requestEventFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if args.Data == nil {
		args.Data = jsonMap{}
	}

	collection, err := e.App.FindCachedCollectionByNameOrId(args.Collection)
	if err != nil || collection == nil {
		return nil, e.NotFoundError("Missing collection context.", err)
	}

	if collection.IsView() {
		return nil, e.BadRequestError("Unsupported collection type.", nil)
	}

	if err := checkCollectionRateLimit(e, collection, "create"); err != nil {
		return nil, err
	}

	requestInfo, err := e.RequestInfo()
	if err != nil {
		return nil, err
	}

	hasSuperuserAuth := requestInfo.HasSuperuserAuth()
	if !hasSuperuserAuth && collection.CreateRule == nil {
		return nil, e.ForbiddenError("Only superusers can perform this action.", nil)
	}

	record := core.NewRecord(collection)

	data := map[string]any(args.Data)
	requestInfo.Body = data

	form := forms.NewRecordUpsert(e.App, record)
	if hasSuperuserAuth {
		form.GrantSuperuserAccess()
	}
	form.Load(data)

	applyCreateAuditFields(record, requestInfo)

	manageAccess, err := ensureCreateRuleAccess(e, requestInfo, collection, record, form)
	if err != nil {
		return nil, err
	}
	if manageAccess {
		form.GrantManagerAccess()
	}

	event := new(core.RecordRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Record = record

	if err := e.App.OnRecordCreateRequest().Trigger(event, func(e *core.RecordRequestEvent) error {
		form.SetApp(e.App)
		form.SetRecord(e.Record)

		if err := form.Submit(); err != nil {
			return firstApiError(err, e.BadRequestError("Failed to create record", err))
		}

		return nil
	}); err != nil {
		return nil, err
	}

	expand := normalizeExpand(args.Expand)

	applyGraphQLSearchArgs(requestInfo, "", "", expand, nil, nil)

	if err := EnrichRecord(e, record, expand...); err != nil {
		return nil, firstApiError(err, e.InternalServerError("Failed to enrich record", err))
	}

	return &recordResolver{record: record}, nil
}

func (r *graphQLResolver) UpdateRecord(ctx context.Context, args updateRecordArgs) (*recordResolver, error) {
	e, err := requestEventFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if args.Data == nil {
		args.Data = jsonMap{}
	}

	collection, err := e.App.FindCachedCollectionByNameOrId(args.Collection)
	if err != nil || collection == nil {
		return nil, e.NotFoundError("Missing collection context.", err)
	}

	if collection.IsView() {
		return nil, e.BadRequestError("Unsupported collection type.", nil)
	}

	if err := checkCollectionRateLimit(e, collection, "update"); err != nil {
		return nil, err
	}

	if args.Id == "" {
		return nil, e.NotFoundError("", nil)
	}

	requestInfo, err := e.RequestInfo()
	if err != nil {
		return nil, err
	}

	hasSuperuserAuth := requestInfo.HasSuperuserAuth()
	if !hasSuperuserAuth && collection.UpdateRule == nil {
		return nil, e.ForbiddenError("Only superusers can perform this action.", nil)
	}

	record, err := e.App.FindRecordById(collection, args.Id)
	if err != nil {
		return nil, firstApiError(err, e.NotFoundError("", err))
	}

	data := map[string]any(args.Data)
	requestInfo.Body = data

	ruleFunc := func(q *dbx.SelectQuery) error {
		if !hasSuperuserAuth && collection.UpdateRule != nil && *collection.UpdateRule != "" {
			resolver := core.NewRecordFieldResolver(e.App, collection, requestInfo, true)
			expr, err := search.FilterData(*collection.UpdateRule).BuildExpr(resolver)
			if err != nil {
				return err
			}
			resolver.UpdateQuery(q)
			q.AndWhere(expr)
		}
		return nil
	}

	record, err = e.App.FindRecordById(collection, args.Id, ruleFunc)
	if err != nil {
		return nil, firstApiError(err, e.NotFoundError("", err))
	}

	form := forms.NewRecordUpsert(e.App, record)
	if hasSuperuserAuth {
		form.GrantSuperuserAccess()
	}
	form.Load(data)

	applyUpdateAuditFields(record, requestInfo)

	manageRuleQuery := e.App.ConcurrentDB().Select("(1)").From(collection.Name).AndWhere(dbx.HashExp{
		collection.Name + ".id": record.Id,
	})
	if !form.HasManageAccess() && hasAuthManageAccess(e.App, requestInfo, collection, manageRuleQuery) {
		form.GrantManagerAccess()
	}

	event := new(core.RecordRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Record = record

	if err := e.App.OnRecordUpdateRequest().Trigger(event, func(e *core.RecordRequestEvent) error {
		form.SetApp(e.App)
		form.SetRecord(e.Record)

		if err := form.Submit(); err != nil {
			return firstApiError(err, e.BadRequestError("Failed to update record", err))
		}

		return nil
	}); err != nil {
		return nil, err
	}

	expand := normalizeExpand(args.Expand)

	applyGraphQLSearchArgs(requestInfo, "", "", expand, nil, nil)

	if err := EnrichRecord(e, record, expand...); err != nil {
		return nil, firstApiError(err, e.InternalServerError("Failed to enrich record", err))
	}

	return &recordResolver{record: record}, nil
}

func (r *graphQLResolver) DeleteRecord(ctx context.Context, args deleteRecordArgs) (bool, error) {
	e, err := requestEventFromContext(ctx)
	if err != nil {
		return false, err
	}

	collection, err := e.App.FindCachedCollectionByNameOrId(args.Collection)
	if err != nil || collection == nil {
		return false, e.NotFoundError("Missing collection context.", err)
	}

	if collection.IsView() {
		return false, e.BadRequestError("Unsupported collection type.", nil)
	}

	if err := checkCollectionRateLimit(e, collection, "delete"); err != nil {
		return false, err
	}

	if args.Id == "" {
		return false, e.NotFoundError("", nil)
	}

	requestInfo, err := e.RequestInfo()
	if err != nil {
		return false, err
	}

	hasSuperuserAuth := requestInfo.HasSuperuserAuth()
	if !hasSuperuserAuth && collection.DeleteRule == nil {
		return false, e.ForbiddenError("Only superusers can perform this action.", nil)
	}

	ruleFunc := func(q *dbx.SelectQuery) error {
		if !hasSuperuserAuth && collection.DeleteRule != nil && *collection.DeleteRule != "" {
			resolver := core.NewRecordFieldResolver(e.App, collection, requestInfo, true)
			expr, err := search.FilterData(*collection.DeleteRule).BuildExpr(resolver)
			if err != nil {
				return err
			}
			resolver.UpdateQuery(q)
			q.AndWhere(expr)
		}
		return nil
	}

	record, err := e.App.FindRecordById(collection, args.Id, ruleFunc)
	if err != nil {
		return false, firstApiError(err, e.NotFoundError("", err))
	}

	event := new(core.RecordRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Record = record

	if err := e.App.OnRecordDeleteRequest().Trigger(event, func(e *core.RecordRequestEvent) error {
		return e.App.Delete(e.Record)
	}); err != nil {
		return false, err
	}

	return true, nil
}

type recordListResolver struct {
	result  *search.Result
	records []*core.Record
}

func (r *recordListResolver) Page() int32 {
	if r == nil || r.result == nil {
		return 0
	}
	return int32(r.result.Page)
}

func (r *recordListResolver) PerPage() int32 {
	if r == nil || r.result == nil {
		return 0
	}
	return int32(r.result.PerPage)
}

func (r *recordListResolver) TotalItems() int32 {
	if r == nil || r.result == nil || r.result.TotalItems < 0 {
		return 0
	}
	return int32(r.result.TotalItems)
}

func (r *recordListResolver) TotalPages() int32 {
	if r == nil || r.result == nil || r.result.TotalPages < 0 {
		return 0
	}
	return int32(r.result.TotalPages)
}

func (r *recordListResolver) Items() []*recordResolver {
	resolvers := make([]*recordResolver, len(r.records))
	for i, record := range r.records {
		resolvers[i] = &recordResolver{record: record}
	}
	return resolvers
}

type recordResolver struct {
	record *core.Record
}

func (r *recordResolver) ID() gql.ID {
	if r == nil || r.record == nil {
		return gql.ID("")
	}
	return gql.ID(r.record.Id)
}

func (r *recordResolver) CollectionId() string {
	if r == nil || r.record == nil || r.record.Collection() == nil {
		return ""
	}

	return r.record.Collection().Id
}

func (r *recordResolver) CollectionName() string {
	if r == nil || r.record == nil || r.record.Collection() == nil {
		return ""
	}

	return r.record.Collection().Name
}

func (r *recordResolver) Created() string {
	if r == nil || r.record == nil {
		return ""
	}
	return r.record.GetDateTime(core.FieldNameCreated).String()
}

func (r *recordResolver) Updated() string {
	if r == nil || r.record == nil {
		return ""
	}
	return r.record.GetDateTime(core.FieldNameUpdated).String()
}

func (r *recordResolver) Data() jsonMap {
	if r == nil || r.record == nil {
		return jsonMap{}
	}

	export := r.record.PublicExport()
	data := jsonMap{}

	for k, v := range export {
		switch k {
		case core.FieldNameId,
			core.FieldNameCollectionId,
			core.FieldNameCollectionName,
			core.FieldNameCreated,
			core.FieldNameUpdated:
			continue
		default:
			data[k] = v
		}
	}

	return data
}

func graphqlVariablesFromContext(ctx context.Context) map[string]any {
	vars, _ := ctx.Value(graphqlVariablesContextKey{}).(map[string]any)
	return vars
}

func requestEventFromContext(ctx context.Context) (*core.RequestEvent, error) {
	e, _ := ctx.Value(graphqlContextKey{}).(*core.RequestEvent)
	if e == nil {
		return nil, errors.New("missing request context")
	}

	return e, nil
}

func applyGraphQLSearchArgs(info *core.RequestInfo, filter string, sort string, expands []string, page *int32, perPage *int32) {
	if info == nil {
		return
	}

	if info.Query == nil {
		info.Query = map[string]string{}
	}

	if filter != "" {
		info.Query[search.FilterQueryParam] = filter
	}
	if sort != "" {
		info.Query[search.SortQueryParam] = sort
	}
	if len(expands) > 0 {
		info.Query[expandQueryParam] = strings.Join(expands, ",")
	}
	if page != nil {
		info.Query[search.PageQueryParam] = strconv.Itoa(int(*page))
	}
	if perPage != nil {
		info.Query[search.PerPageQueryParam] = strconv.Itoa(int(*perPage))
	}
}

func normalizeExpand(expand *[]string) []string {
	if expand == nil {
		return nil
	}
	return *expand
}

var gqlFilterVarPattern = regexp.MustCompile(`\$(\w+)`)

func applyFilterVariables(filter string, variables map[string]any) string {
	if filter == "" || len(variables) == 0 {
		return filter
	}

	return gqlFilterVarPattern.ReplaceAllStringFunc(filter, func(token string) string {
		key := strings.TrimPrefix(token, "$")

		value, hasValue := variables[key]
		if !hasValue {
			return token
		}

		switch v := value.(type) {
		case nil:
			return "null"
		case bool, float64, float32, int, int64, int32, int16, int8, uint, uint64, uint32, uint16, uint8:
			return cast.ToString(v)
		default:
			replacement := cast.ToString(v)
			if replacement == "" {
				raw, _ := json.Marshal(v)
				replacement = string(raw)
			}

			return strconv.Quote(replacement)
		}
	})
}

func ensureCreateRuleAccess(
	e *core.RequestEvent,
	requestInfo *core.RequestInfo,
	collection *core.Collection,
	record *core.Record,
	form *forms.RecordUpsert,
) (bool, error) {
	hasSuperuserAuth := requestInfo.HasSuperuserAuth()

	if !hasSuperuserAuth && collection.CreateRule != nil {
		dummyRecord := record.Clone()

		dummyRandomPart := "__pb_create__" + security.PseudorandomString(6)

		if dummyRecord.Id == "" {
			dummyRecord.Id = "__temp_id__" + dummyRandomPart
		}

		dummyRecord.SetVerified(false)

		dummyExport, err := dummyRecord.DBExport(e.App)
		if err != nil {
			return false, e.BadRequestError("Failed to create record", fmt.Errorf("dummy DBExport error: %w", err))
		}

		dummyParams, selects := buildDummyParamsAndSelects(collection, dummyExport)

		dummyCollection := *collection
		dummyCollection.Id += dummyRandomPart
		dummyCollection.Name += inflector.Columnify(dummyRandomPart)

		withFrom := fmt.Sprintf("WITH {{%s}} as (SELECT %s)", dummyCollection.Name, strings.Join(selects, ","))

		if *dummyCollection.CreateRule != "" {
			ruleQuery := e.App.ConcurrentDB().Select("(1)").PreFragment(withFrom).From(dummyCollection.Name).AndBind(dummyParams)

			resolver := core.NewRecordFieldResolver(e.App, &dummyCollection, requestInfo, true)

			expr, err := search.FilterData(*dummyCollection.CreateRule).BuildExpr(resolver)
			if err != nil {
				return false, e.BadRequestError("Failed to create record", fmt.Errorf("create rule build expression failure: %w", err))
			}
			ruleQuery.AndWhere(expr)

			resolver.UpdateQuery(ruleQuery)

			var exists int
			err = ruleQuery.Limit(1).Row(&exists)
			if err != nil || exists == 0 {
				return false, e.BadRequestError("Failed to create record", fmt.Errorf("create rule failure: %w", err))
			}
		}

		manageRuleQuery := e.App.ConcurrentDB().Select("(1)").PreFragment(withFrom).From(dummyCollection.Name).AndBind(dummyParams)
		if !form.HasManageAccess() && hasAuthManageAccess(e.App, requestInfo, &dummyCollection, manageRuleQuery) {
			return true, nil
		}
	}

	if form.HasManageAccess() {
		return true, nil
	}

	return false, nil
}
