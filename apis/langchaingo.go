package apis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/template"
	"time"

	chromem "chromem-go"
	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"langchaingo/llms"
	"langchaingo/llms/ollama"
	"langchaingo/llms/openai"
)

const (
	defaultLangchaingoProvider = "openai"
	defaultLangchaingoModel    = "gpt-4o-mini"

	defaultRAGTopK = 4
	maxRAGTopK     = 20

	defaultSQLTopK    = 10
	maxSQLTopK        = 100
	sqlSampleRows     = 5
	sqlMinSampleRows  = 1
	sqlSchemaFetchErr = "The SQL assistant could not load the requested tables."

	defaultRAGPromptTemplate = `You are a helpful assistant. Use only the provided context to answer the question. If the context is empty, reply that you don't know.

Context:
{{.context}}

Question: {{.question}}

Answer:`
)

func bindLangchaingoApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/langchaingo").Bind(RequireAuth())
	sub.POST("/completions", langchaingoCompletions)
	sub.POST("/rag", langchaingoRAG)
	sub.POST("/documents/query", langchaingoQueryDocuments)
	sub.POST("/sql", langchaingoSQL)
}

type langchaingoModelConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseUrl"`
}

type langchaingoCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type langchaingoCompletionRequest struct {
	Model          *langchaingoModelConfig        `json:"model"`
	Prompt         string                         `json:"prompt"`
	Messages       []langchaingoCompletionMessage `json:"messages"`
	Temperature    *float64                       `json:"temperature"`
	MaxTokens      *int                           `json:"maxTokens"`
	TopP           *float64                       `json:"topP"`
	CandidateCount *int                           `json:"candidateCount"`
	Stop           []string                       `json:"stop"`
	JSON           bool                           `json:"json"`
}

type langchaingoFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type langchaingoToolCall struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"`
	FunctionCall *langchaingoFunctionCall `json:"functionCall,omitempty"`
}

type langchaingoCompletionResponse struct {
	Content        string                   `json:"content"`
	StopReason     string                   `json:"stopReason,omitempty"`
	GenerationInfo map[string]any           `json:"generationInfo,omitempty"`
	FunctionCall   *langchaingoFunctionCall `json:"functionCall,omitempty"`
	ToolCalls      []langchaingoToolCall    `json:"toolCalls,omitempty"`
}

type langchaingoRAGFilters struct {
	Where         map[string]string `json:"where"`
	WhereDocument map[string]string `json:"whereDocument"`
}

type langchaingoRAGRequest struct {
	Model          *langchaingoModelConfig `json:"model"`
	Collection     string                  `json:"collection"`
	Question       string                  `json:"question"`
	TopK           *int                    `json:"topK"`
	ScoreThreshold *float64                `json:"scoreThreshold"`
	Filters        *langchaingoRAGFilters  `json:"filters"`
	PromptTemplate string                  `json:"promptTemplate"`
	ReturnSources  bool                    `json:"returnSources"`
}

type langchaingoDocumentQueryRequest struct {
	Model          *langchaingoModelConfig `json:"model"`
	Collection     string                  `json:"collection"`
	Query          string                  `json:"query"`
	TopK           *int                    `json:"topK"`
	ScoreThreshold *float64                `json:"scoreThreshold"`
	Filters        *langchaingoRAGFilters  `json:"filters"`
	PromptTemplate string                  `json:"promptTemplate"`
	ReturnSources  bool                    `json:"returnSources"`
}

type langchaingoSQLRequest struct {
	Model  *langchaingoModelConfig `json:"model"`
	Query  string                  `json:"query"`
	Tables []string                `json:"tables"`
	TopK   *int                    `json:"topK"`
}

type langchaingoSQLResponse struct {
	SQL       string     `json:"sql"`
	Answer    string     `json:"answer"`
	Columns   []string   `json:"columns,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	RawResult string     `json:"rawResult,omitempty"`
}

type langchaingoSQLColumn struct {
	Name     string
	Type     string
	Nullable bool
}

type langchaingoSQLTableSchema struct {
	Name       string
	Columns    []langchaingoSQLColumn
	SampleRows []map[string]string
}

type langchaingoSourceDocument struct {
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Score    *float64       `json:"score,omitempty"`
}

type langchaingoRAGResponse struct {
	Answer  string                      `json:"answer"`
	Sources []langchaingoSourceDocument `json:"sources,omitempty"`
}

type langchaingoResolvedModel struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
}

type langchaingoLLM struct {
	instance llms.Model
	config   langchaingoResolvedModel
}

func langchaingoCompletions(e *core.RequestEvent) error {
	payload := new(langchaingoCompletionRequest)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	messages, err := buildLangchaingoMessages(payload)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	llm, err := newLangchaingoLLM(payload.Model)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	callOptions := buildCompletionCallOptions(payload, llm.config.Model)

	resp, err := llm.instance.GenerateContent(e.Request.Context(), messages, callOptions...)
	if err != nil {
		return e.InternalServerError("Failed to generate completion.", err)
	}

	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return e.InternalServerError("Completion response is empty.", errors.New("missing completion choice"))
	}

	choice := resp.Choices[0]
	result := langchaingoCompletionResponse{
		Content:        choice.Content,
		StopReason:     choice.StopReason,
		GenerationInfo: choice.GenerationInfo,
		FunctionCall:   convertFunctionCall(choice.FuncCall),
		ToolCalls:      convertToolCalls(choice.ToolCalls),
	}

	return e.JSON(http.StatusOK, result)
}

func langchaingoRAG(e *core.RequestEvent) error {
	payload := new(langchaingoRAGRequest)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	payload.Collection = strings.TrimSpace(payload.Collection)
	if payload.Collection == "" {
		return e.BadRequestError("Collection is required.", nil)
	}

	payload.Question = strings.TrimSpace(payload.Question)
	if payload.Question == "" {
		return e.BadRequestError("Question is required.", nil)
	}

	return processLangchaingoRetrieval(e, payload)
}

func langchaingoQueryDocuments(e *core.RequestEvent) error {
	payload := new(langchaingoDocumentQueryRequest)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	payload.Collection = strings.TrimSpace(payload.Collection)
	if payload.Collection == "" {
		return e.BadRequestError("Collection is required.", nil)
	}

	payload.Query = strings.TrimSpace(payload.Query)
	if payload.Query == "" {
		return e.BadRequestError("Query is required.", nil)
	}

	ragPayload := &langchaingoRAGRequest{
		Model:          payload.Model,
		Collection:     payload.Collection,
		Question:       payload.Query,
		TopK:           payload.TopK,
		ScoreThreshold: payload.ScoreThreshold,
		Filters:        payload.Filters,
		PromptTemplate: payload.PromptTemplate,
		ReturnSources:  payload.ReturnSources,
	}

	return processLangchaingoRetrieval(e, ragPayload)
}

func langchaingoSQL(e *core.RequestEvent) error {
	if e.Auth == nil || !e.Auth.IsSuperuser() {
		return e.UnauthorizedError("The request requires a superuser auth record.", nil)
	}

	payload := new(langchaingoSQLRequest)
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	payload.Query = strings.TrimSpace(payload.Query)
	if payload.Query == "" {
		return e.BadRequestError("Query is required.", nil)
	}

	topK := defaultSQLTopK
	if payload.TopK != nil && *payload.TopK > 0 {
		topK = *payload.TopK
	}
	if topK > maxSQLTopK {
		topK = maxSQLTopK
	}

	tableNames, err := resolveLangchaingoSQLTables(e, payload.Tables)
	if err != nil {
		return e.BadRequestError(err.Error(), err)
	}
	if len(tableNames) == 0 {
		return e.BadRequestError("No tables available for SQL generation.", nil)
	}

	tableSchemas, err := loadLangchaingoSQLTables(e.Request.Context(), e.App.DB(), tableNames, topK)
	if err != nil {
		return e.InternalServerError(sqlSchemaFetchErr, err)
	}

	llm, err := newLangchaingoLLM(payload.Model)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	prompt := buildLangchaingoSQLPrompt(tableSchemas, topK)
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, prompt),
		llms.TextParts(llms.ChatMessageTypeHuman, payload.Query),
	}

	resp, err := llm.instance.GenerateContent(e.Request.Context(), messages, llms.WithModel(llm.config.Model))
	if err != nil {
		return e.InternalServerError("Failed to generate SQL.", err)
	}

	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return e.InternalServerError("SQL generation response is empty.", errors.New("missing completion choice"))
	}

	parsed, err := parseLangchaingoSQLModelOutput(resp.Choices[0].Content)
	if err != nil {
		return e.InternalServerError("Failed to parse the generated SQL.", err)
	}

	execution, err := executeLangchaingoSQL(e.Request.Context(), e.App.DB(), parsed.SQL, topK)
	if err != nil {
		return e.InternalServerError("Failed to execute generated SQL.", err)
	}

	result := langchaingoSQLResponse{
		SQL:    parsed.SQL,
		Answer: parsed.Answer,
	}

	if execution != nil {
		result.Columns = execution.Columns
		result.Rows = execution.Rows
		result.RawResult = execution.RawResult
	}

	return e.JSON(http.StatusOK, result)
}

func processLangchaingoRetrieval(e *core.RequestEvent, payload *langchaingoRAGRequest) error {
	store := e.App.LLMStore()
	if store == nil {
		return e.InternalServerError("LLM store is not initialized.", nil)
	}

	collection, err := store.GetCollectionContext(e.Request.Context(), payload.Collection, nil)
	if err != nil {
		return e.InternalServerError("Failed to load collection.", err)
	}
	if collection == nil {
		return e.NotFoundError("Collection not found.", errors.New("missing collection"))
	}

	topK := defaultRAGTopK
	if payload.TopK != nil && *payload.TopK > 0 {
		topK = *payload.TopK
	}
	if topK > maxRAGTopK {
		topK = maxRAGTopK
	}

	queryOptions := chromem.QueryOptions{
		QueryText: payload.Question,
	}

	if payload.Filters != nil {
		if len(payload.Filters.Where) > 0 {
			queryOptions.Where = payload.Filters.Where
		}
		if len(payload.Filters.WhereDocument) > 0 {
			queryOptions.WhereDocument = payload.Filters.WhereDocument
		}
	}

	var results []chromem.Result
	totalDocs := collection.Count()
	if totalDocs == 0 {
		results = []chromem.Result{}
	} else {
		if topK > totalDocs {
			topK = totalDocs
		}
		if topK <= 0 {
			topK = 1
		}
		queryOptions.NResults = topK

		results, err = collection.QueryWithOptions(e.Request.Context(), queryOptions)
		if err != nil {
			return e.InternalServerError("Failed to query collection.", err)
		}
	}

	minScore := float32(-1)
	if payload.ScoreThreshold != nil {
		minScore = float32(*payload.ScoreThreshold)
	}

	filtered := filterRAGResults(results, minScore)
	contextBlock := buildRAGContext(filtered)

	prompt, err := executeRAGTemplate(payload.PromptTemplate, contextBlock, payload.Question)
	if err != nil {
		return e.BadRequestError("Invalid prompt template.", err)
	}

	llm, err := newLangchaingoLLM(payload.Model)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a knowledgeable assistant that only answers using the supplied context."),
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}

	resp, err := llm.instance.GenerateContent(e.Request.Context(), messages, llms.WithModel(llm.config.Model))
	if err != nil {
		return e.InternalServerError("Failed to generate answer.", err)
	}

	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return e.InternalServerError("RAG response is empty.", errors.New("missing completion choice"))
	}

	answer := resp.Choices[0].Content
	result := langchaingoRAGResponse{
		Answer: answer,
	}

	if payload.ReturnSources {
		result.Sources = buildRAGSources(filtered)
	}

	return e.JSON(http.StatusOK, result)
}

func resolveLangchaingoSQLTables(e *core.RequestEvent, requested []string) ([]string, error) {
	collections, err := e.App.FindAllCollections()
	if err != nil {
		return nil, err
	}

	available := make(map[string]string, len(collections))
	for _, collection := range collections {
		available[strings.ToLower(collection.Name)] = collection.Name
	}

	if len(requested) == 0 {
		result := make([]string, 0, len(collections))
		for _, collection := range collections {
			result = append(result, collection.Name)
		}
		return result, nil
	}

	seen := make(map[string]struct{})
	names := make([]string, 0, len(requested))
	for _, tbl := range requested {
		name := strings.TrimSpace(tbl)
		if name == "" {
			continue
		}

		normalized, ok := available[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("table %q not found", name)
		}
		if _, exists := seen[normalized]; exists {
			continue
		}

		seen[normalized] = struct{}{}
		names = append(names, normalized)
	}

	return names, nil
}

func loadLangchaingoSQLTables(ctx context.Context, builder dbx.Builder, tables []string, topK int) ([]langchaingoSQLTableSchema, error) {
	result := make([]langchaingoSQLTableSchema, 0, len(tables))
	for _, table := range tables {
		schema, err := loadLangchaingoSQLTable(ctx, builder, table, topK)
		if err != nil {
			return nil, err
		}
		result = append(result, schema)
	}
	return result, nil
}

func loadLangchaingoSQLTable(ctx context.Context, builder dbx.Builder, table string, topK int) (langchaingoSQLTableSchema, error) {
	columns, err := loadLangchaingoSQLColumns(ctx, builder, table)
	if err != nil {
		return langchaingoSQLTableSchema{}, err
	}

	samples, err := loadLangchaingoSQLSampleRows(ctx, builder, table, topK)
	if err != nil {
		return langchaingoSQLTableSchema{}, err
	}

	return langchaingoSQLTableSchema{
		Name:       table,
		Columns:    columns,
		SampleRows: samples,
	}, nil
}

func loadLangchaingoSQLColumns(ctx context.Context, builder dbx.Builder, table string) ([]langchaingoSQLColumn, error) {
	rows := []struct {
		Name     string `db:"column_name"`
		DataType string `db:"data_type"`
		Nullable string `db:"is_nullable"`
	}{}

	err := builder.Select("column_name", "data_type", "is_nullable").
		From("information_schema.columns").
		AndWhere(dbx.NewExp("table_schema = ANY (current_schemas(false)) AND table_name={:table}", dbx.Params{"table": table})).
		OrderBy("ordinal_position ASC").
		WithContext(ctx).
		All(&rows)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("no columns found for table %q", table)
	}

	result := make([]langchaingoSQLColumn, 0, len(rows))
	for _, row := range rows {
		result = append(result, langchaingoSQLColumn{
			Name:     row.Name,
			Type:     row.DataType,
			Nullable: strings.EqualFold(row.Nullable, "YES"),
		})
	}

	return result, nil
}

func loadLangchaingoSQLSampleRows(ctx context.Context, builder dbx.Builder, table string, topK int) ([]map[string]string, error) {
	limit := sqlSampleRows
	if topK > 0 && topK < limit {
		limit = topK
	}
	if limit < sqlMinSampleRows {
		limit = sqlMinSampleRows
	}

	query := fmt.Sprintf("SELECT * FROM %s LIMIT %d", builder.QuoteSimpleTableName(table), limit)
	rows, err := builder.NewQuery(query).WithContext(ctx).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	samples := make([]map[string]string, 0, limit)
	for rows.Next() {
		values, err := scanSQLRowValues(rows, len(columns))
		if err != nil {
			return nil, err
		}

		row := make(map[string]string, len(columns))
		for i, col := range columns {
			row[col] = values[i]
		}
		samples = append(samples, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return samples, nil
}

func buildLangchaingoSQLPrompt(tables []langchaingoSQLTableSchema, topK int) string {
	var builder strings.Builder

	builder.WriteString("You are a PostgreSQL expert. Given a request, write a single SQL statement that satisfies it. ")
	builder.WriteString("Use only the listed tables, keep the final SELECT limited to the requested row count, and prefer CTEs for any writes before selecting. ")
	builder.WriteString("Respond ONLY with a JSON object {\"sql\": \"...\", \"answer\": \"...\"} without Markdown fences.\n\n")
	builder.WriteString("Available tables and schemas:\n")

	for _, table := range tables {
		builder.WriteString(fmt.Sprintf("- %s columns:\n", table.Name))
		for _, col := range table.Columns {
			nullInfo := "required"
			if col.Nullable {
				nullInfo = "nullable"
			}
			builder.WriteString(fmt.Sprintf("  - %s: %s (%s)\n", col.Name, col.Type, nullInfo))
		}

		if len(table.SampleRows) > 0 {
			builder.WriteString("  sample rows:\n")
			for _, row := range table.SampleRows {
				encoded, err := json.Marshal(row)
				if err != nil {
					continue
				}
				builder.WriteString(fmt.Sprintf("  - %s\n", encoded))
			}
		} else {
			builder.WriteString("  sample rows: none available\n")
		}
	}

	builder.WriteString(fmt.Sprintf("\nAlways cap result sets at %d rows using LIMIT and rely only on the provided schema.", topK))
	return builder.String()
}

type langchaingoGeneratedSQL struct {
	SQL    string `json:"sql"`
	Answer string `json:"answer"`
}

func parseLangchaingoSQLModelOutput(raw string) (*langchaingoGeneratedSQL, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")

	if start := strings.Index(trimmed, "{"); start >= 0 {
		if end := strings.LastIndex(trimmed, "}"); end > start {
			trimmed = trimmed[start : end+1]
		}
	}

	result := &langchaingoGeneratedSQL{}
	if err := json.Unmarshal([]byte(trimmed), result); err != nil {
		return nil, err
	}

	result.SQL = strings.TrimSpace(result.SQL)
	result.Answer = strings.TrimSpace(result.Answer)

	if result.SQL == "" {
		return nil, errors.New("model response did not include sql")
	}

	return result, nil
}

type langchaingoSQLExecution struct {
	Columns   []string
	Rows      [][]string
	RawResult string
}

func executeLangchaingoSQL(ctx context.Context, builder dbx.Builder, sql string, limit int) (*langchaingoSQLExecution, error) {
	rows, err := builder.NewQuery(sql).WithContext(ctx).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	resultRows := make([][]string, 0)
	for rows.Next() {
		values, err := scanSQLRowValues(rows, len(columns))
		if err != nil {
			return nil, err
		}
		resultRows = append(resultRows, values)

		if limit > 0 && len(resultRows) >= limit {
			break
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	summary := fmt.Sprintf("completed with %d returned row(s)", len(resultRows))
	if len(columns) == 0 {
		summary = "statement executed with no result set"
	}

	return &langchaingoSQLExecution{
		Columns:   columns,
		Rows:      resultRows,
		RawResult: summary,
	}, nil
}

type sqlRowScanner interface {
	Scan(dest ...any) error
}

func scanSQLRowValues(scanner sqlRowScanner, columnCount int) ([]string, error) {
	if columnCount <= 0 {
		return []string{}, nil
	}

	raw := make([]any, columnCount)
	dest := make([]any, columnCount)
	for i := range raw {
		dest[i] = &raw[i]
	}

	if err := scanner.Scan(dest...); err != nil {
		return nil, err
	}

	values := make([]string, columnCount)
	for i, v := range raw {
		values[i] = formatSQLValue(v)
	}

	return values, nil
}

func formatSQLValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case *time.Time:
		if v == nil {
			return "NULL"
		}
		return v.Format(time.RFC3339Nano)
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func buildLangchaingoMessages(payload *langchaingoCompletionRequest) ([]llms.MessageContent, error) {
	if len(payload.Messages) == 0 {
		text := strings.TrimSpace(payload.Prompt)
		if text == "" {
			return nil, errors.New("prompt or messages is required")
		}
		return []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, text),
		}, nil
	}

	messages := make([]llms.MessageContent, 0, len(payload.Messages))
	for _, msg := range payload.Messages {
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			return nil, errors.New("message content cannot be empty")
		}

		role := normalizeLangchaingoRole(msg.Role)
		messages = append(messages, llms.MessageContent{
			Role:  role,
			Parts: []llms.ContentPart{llms.TextContent{Text: text}},
		})
	}

	return messages, nil
}

func normalizeLangchaingoRole(role string) llms.ChatMessageType {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return llms.ChatMessageTypeSystem
	case "assistant", "ai":
		return llms.ChatMessageTypeAI
	case "user", "human":
		return llms.ChatMessageTypeHuman
	default:
		return llms.ChatMessageTypeHuman
	}
}

func buildCompletionCallOptions(payload *langchaingoCompletionRequest, model string) []llms.CallOption {
	opts := []llms.CallOption{}

	if model != "" {
		opts = append(opts, llms.WithModel(model))
	}
	if payload.MaxTokens != nil && *payload.MaxTokens > 0 {
		opts = append(opts, llms.WithMaxTokens(*payload.MaxTokens))
	}
	if payload.Temperature != nil {
		opts = append(opts, llms.WithTemperature(*payload.Temperature))
	}
	if payload.TopP != nil {
		opts = append(opts, llms.WithTopP(*payload.TopP))
	}
	if payload.CandidateCount != nil && *payload.CandidateCount > 0 {
		opts = append(opts, llms.WithCandidateCount(*payload.CandidateCount))
	}
	if len(payload.Stop) > 0 {
		opts = append(opts, llms.WithStopWords(payload.Stop))
	}
	if payload.JSON {
		opts = append(opts, llms.WithJSONMode())
	}

	return opts
}

func convertFunctionCall(call *llms.FunctionCall) *langchaingoFunctionCall {
	if call == nil {
		return nil
	}
	return &langchaingoFunctionCall{
		Name:      call.Name,
		Arguments: call.Arguments,
	}
}

func convertToolCalls(calls []llms.ToolCall) []langchaingoToolCall {
	if len(calls) == 0 {
		return nil
	}

	result := make([]langchaingoToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, langchaingoToolCall{
			ID:           call.ID,
			Type:         call.Type,
			FunctionCall: convertFunctionCall(call.FunctionCall),
		})
	}

	return result
}

func newLangchaingoLLM(cfg *langchaingoModelConfig) (*langchaingoLLM, error) {
	resolved := resolveLangchaingoConfig(cfg)

	switch resolved.Provider {
	case "openai":
		if resolved.APIKey == "" {
			return nil, errors.New("OpenAI API key is not configured")
		}
		opts := []openai.Option{
			openai.WithToken(resolved.APIKey),
		}
		if resolved.Model != "" {
			opts = append(opts, openai.WithModel(resolved.Model))
		}
		if resolved.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(resolved.BaseURL))
		}

		model, err := openai.New(opts...)
		if err != nil {
			return nil, err
		}

		return &langchaingoLLM{
			instance: model,
			config:   resolved,
		}, nil
	case "ollama":
		if resolved.Model == "" {
			return nil, errors.New("Ollama model is required")
		}

		opts := []ollama.Option{
			ollama.WithModel(resolved.Model),
		}
		if resolved.BaseURL != "" {
			opts = append(opts, ollama.WithServerURL(resolved.BaseURL))
		}

		model, err := ollama.New(opts...)
		if err != nil {
			return nil, err
		}

		return &langchaingoLLM{
			instance: model,
			config:   resolved,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", resolved.Provider)
	}
}

func resolveLangchaingoConfig(cfg *langchaingoModelConfig) langchaingoResolvedModel {
	result := langchaingoResolvedModel{
		Provider: defaultLangchaingoProvider,
	}

	if cfg != nil {
		if provider := strings.TrimSpace(cfg.Provider); provider != "" {
			result.Provider = strings.ToLower(provider)
		}

		if model := strings.TrimSpace(cfg.Model); model != "" {
			result.Model = model
		}

		result.APIKey = strings.TrimSpace(cfg.APIKey)
		if base := strings.TrimSpace(cfg.BaseURL); base != "" {
			result.BaseURL = base
		}
	}

	if result.Provider == "openai" {
		if result.APIKey == "" {
			result.APIKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		}
		if result.BaseURL == "" {
			result.BaseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
		}
		if result.Model == "" {
			if envModel := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); envModel != "" {
				result.Model = envModel
			} else {
				result.Model = defaultLangchaingoModel
			}
		}
	}

	return result
}

func filterRAGResults(results []chromem.Result, minScore float32) []chromem.Result {
	if minScore <= -1 {
		return results
	}

	filtered := make([]chromem.Result, 0, len(results))
	for _, res := range results {
		if res.Similarity >= minScore {
			filtered = append(filtered, res)
		}
	}

	return filtered
}

func buildRAGContext(results []chromem.Result) string {
	if len(results) == 0 {
		return "No relevant documents were retrieved."
	}

	var builder strings.Builder
	for idx, res := range results {
		builder.WriteString(fmt.Sprintf("Source %d (score %.3f):\n", idx+1, res.Similarity))

		if len(res.Metadata) > 0 {
			builder.WriteString("Metadata:\n")
			for _, key := range sortedKeys(res.Metadata) {
				builder.WriteString(fmt.Sprintf("- %s: %s\n", key, res.Metadata[key]))
			}
		}

		if res.Content != "" {
			builder.WriteString(res.Content)
			builder.WriteString("\n")
		}

		builder.WriteString("\n")
	}

	return strings.TrimSpace(builder.String())
}

func executeRAGTemplate(customTemplate, contextBlock, question string) (string, error) {
	tpl := defaultRAGPromptTemplate
	if strings.TrimSpace(customTemplate) != "" {
		tpl = customTemplate
	}

	tmpl, err := template.New("rag").Parse(tpl)
	if err != nil {
		return "", err
	}

	data := map[string]string{
		"context":  contextBlock,
		"question": question,
	}

	buf := bytes.NewBuffer(nil)
	if err := tmpl.Execute(buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func buildRAGSources(results []chromem.Result) []langchaingoSourceDocument {
	if len(results) == 0 {
		return []langchaingoSourceDocument{}
	}

	sources := make([]langchaingoSourceDocument, 0, len(results))
	for _, res := range results {
		score := float64(res.Similarity)
		sources = append(sources, langchaingoSourceDocument{
			Content:  res.Content,
			Metadata: copyMetadata(res.Metadata),
			Score:    &score,
		})
	}

	return sources
}

func copyMetadata(metadata map[string]string) map[string]any {
	if len(metadata) == 0 {
		return nil
	}

	result := make(map[string]any, len(metadata))
	for k, v := range metadata {
		result[k] = v
	}
	return result
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
