package apis

import (
	"net/http"
	"strings"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/search"
)

// bindLogsApi registers the request logs api endpoints.
func bindLogsApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/logs").Bind(RequireSuperuserAuth(), SkipSuccessActivityLog())
	sub.GET("", logsList)
	sub.GET("/stats", logsStats)
	sub.GET("/{id}", logsView)
}

const levelTextField = "levelText"

var logFilterFields = []string{
	"id", "created", "level", "message", "data",
	`^data\.[\w\.\:]*\w+$`,
}

type logsFieldResolver struct {
	base *search.SimpleFieldResolver
}

func newLogsFieldResolver(fields ...string) *logsFieldResolver {
	extended := append([]string{}, fields...)
	extended = append(extended, levelTextField)

	return &logsFieldResolver{
		base: search.NewSimpleFieldResolver(extended...),
	}
}

func (r *logsFieldResolver) UpdateQuery(query *dbx.SelectQuery) error {
	return r.base.UpdateQuery(query)
}

func (r *logsFieldResolver) Resolve(field string) (*search.ResolverResult, error) {
	if field == levelTextField {
		return &search.ResolverResult{
			Identifier: "CAST([[level]] AS TEXT)",
			NoCoalesce: true,
		}, nil
	}

	return r.base.Resolve(field)
}

func logsList(e *core.RequestEvent) error {
	fieldResolver := newLogsFieldResolver(logFilterFields...)

	provider := search.NewProvider(fieldResolver).
		Query(e.App.AuxModelQuery(&core.Log{}))

	values := e.Request.URL.Query()

	if filter := values.Get(search.FilterQueryParam); filter != "" {
		replaced := strings.ReplaceAll(filter, "level~", levelTextField+"~")
		replaced = strings.ReplaceAll(replaced, "level!~", levelTextField+"!~")
		if replaced != filter {
			values.Set(search.FilterQueryParam, replaced)
		}
	}

	if err := provider.Parse(values.Encode()); err != nil {
		return e.BadRequestError("", err)
	}

	if strings.TrimSpace(values.Get(search.SortQueryParam)) == "" {
		provider.AddSort(search.SortField{
			Name:      "created",
			Direction: search.SortDesc,
		})
	}

	result, err := provider.Exec(&[]*core.Log{})

	if err != nil {
		return e.BadRequestError("", err)
	}

	return e.JSON(http.StatusOK, result)
}

func logsStats(e *core.RequestEvent) error {
	fieldResolver := newLogsFieldResolver(logFilterFields...)

	filter := e.Request.URL.Query().Get(search.FilterQueryParam)

	var expr dbx.Expression
	if filter != "" {
		var err error
		expr, err = search.FilterData(filter).BuildExpr(fieldResolver)
		if err != nil {
			return e.BadRequestError("Invalid filter format.", err)
		}
	}

	stats, err := e.App.LogsStats(expr)
	if err != nil {
		return e.BadRequestError("Failed to generate logs stats.", err)
	}

	return e.JSON(http.StatusOK, stats)
}

func logsView(e *core.RequestEvent) error {
	id := e.Request.PathValue("id")
	if id == "" {
		return e.NotFoundError("", nil)
	}

	log, err := e.App.FindLogById(id)
	if err != nil || log == nil {
		return e.NotFoundError("", err)
	}

	return e.JSON(http.StatusOK, log)
}
