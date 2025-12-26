package apis

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	chromem "chromem-go"
	"github.com/gofrs/uuid/v5"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/security"
	"github.com/spf13/cast"
)

func bindLLMDocumentApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	subGroup := rg.Group("/llm-documents").Bind(RequireSuperuserAuth())

	collectionsGroup := subGroup.Group("/collections")
	collectionsGroup.GET("", llmListCollections)
	collectionsGroup.POST("/{name}", llmCreateCollection)
	collectionsGroup.DELETE("/{name}", llmDeleteCollection)

	collectionGroup := subGroup.Group("/{collection}")
	collectionGroup.GET("", llmListDocuments)
	collectionGroup.POST("", llmInsertDocument)
	collectionGroup.GET("/{id}", llmGetDocument)
	collectionGroup.PATCH("/{id}", llmUpdateDocument)
	collectionGroup.DELETE("/{id}", llmDeleteDocument)
	collectionGroup.POST("/documents/query", llmQueryDocuments)
}

type llmCollectionPayload struct {
	Metadata map[string]string `json:"metadata"`
}

type llmDocumentPayload struct {
	ID        string            `json:"id"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata"`
	Embedding []float64         `json:"embedding"`
}

type llmDocumentUpdatePayload struct {
	Content   *string            `json:"content"`
	Metadata  *map[string]string `json:"metadata"`
	Embedding *[]float64         `json:"embedding"`
}

type llmQueryNegativePayload struct {
	Text            string    `json:"text"`
	Embedding       []float64 `json:"embedding"`
	Mode            string    `json:"mode"`
	FilterThreshold *float32  `json:"filterThreshold"`
}

type llmQueryPayload struct {
	QueryText      string                   `json:"queryText"`
	QueryEmbedding []float64                `json:"queryEmbedding"`
	Limit          int                      `json:"limit"`
	Where          map[string]string        `json:"where"`
	Negative       *llmQueryNegativePayload `json:"negative"`
}

func llmListCollections(e *core.RequestEvent) error {
	store := e.App.LLMStore()
	if store == nil {
		return e.InternalServerError("LLM store is not initialized.", nil)
	}

	collections := store.ListCollections()
	names := make([]string, 0, len(collections))
	for name := range collections {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		c := collections[name]
		result = append(result, map[string]any{
			"id":       c.ID(),
			"name":     name,
			"metadata": c.Metadata(),
			"count":    c.Count(),
		})
	}

	return e.JSON(http.StatusOK, result)
}

func llmCreateCollection(e *core.RequestEvent) error {
	store := e.App.LLMStore()
	if store == nil {
		return e.InternalServerError("LLM store is not initialized.", nil)
	}

	name := e.Request.PathValue("name")
	if name == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	if existing, err := store.GetCollectionContext(e.Request.Context(), name, nil); err != nil {
		return e.InternalServerError("Failed to check collection existence.", err)
	} else if existing != nil {
		return e.BadRequestError("Collection already exists.", nil)
	}

	payload := &llmCollectionPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	created, err := store.CreateCollection(name, payload.Metadata, nil)
	if err != nil {
		return e.InternalServerError("Failed to create collection.", err)
	}

	return e.JSON(http.StatusCreated, map[string]any{
		"id":   created.ID(),
		"name": name,
	})
}

func llmDeleteCollection(e *core.RequestEvent) error {
	store := e.App.LLMStore()
	if store == nil {
		return e.InternalServerError("LLM store is not initialized.", nil)
	}

	name := e.Request.PathValue("name")
	if name == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	if err := store.DeleteCollection(name); err != nil {
		return e.InternalServerError("Failed to delete collection.", err)
	}

	return e.NoContent(http.StatusNoContent)
}

func llmListDocuments(e *core.RequestEvent) error {
	collection, err := llmResolveCollection(e)
	if err != nil {
		return err
	}

	page := cast.ToInt(e.Request.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}

	perPage := cast.ToInt(e.Request.URL.Query().Get("perPage"))
	if perPage <= 0 {
		perPage = 50
	}
	if perPage > 500 {
		perPage = 500
	}

	docs, err := collection.ListDocuments(e.Request.Context())
	if err != nil {
		return e.InternalServerError("Failed to list documents.", err)
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].ID < docs[j].ID
	})

	totalItems := len(docs)
	start := (page - 1) * perPage
	if start > totalItems {
		start = totalItems
	}
	end := start + perPage
	if end > totalItems {
		end = totalItems
	}

	items := make([]map[string]any, 0, end-start)
	for _, doc := range docs[start:end] {
		items = append(items, serializeDocument(doc))
	}

	return e.JSON(http.StatusOK, map[string]any{
		"items":      items,
		"page":       page,
		"perPage":    perPage,
		"totalItems": totalItems,
	})
}

func llmInsertDocument(e *core.RequestEvent) error {
	collection, err := llmResolveCollection(e)
	if err != nil {
		return err
	}

	payload := &llmDocumentPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	if payload.ID == "" {
		if generatedID, err := uuid.NewV7(); err == nil {
			payload.ID = strings.ReplaceAll(generatedID.String(), "-", "")
		} else {
			payload.ID = security.RandomString(15)
		}
	}

	vector, err := convertEmbedding(payload.Embedding)
	if err != nil {
		return e.BadRequestError("Invalid embedding payload.", err)
	}

	if payload.Content == "" && len(vector) == 0 {
		return e.BadRequestError("Either content or embedding is required.", nil)
	}

	doc := chromem.Document{
		ID:        payload.ID,
		Content:   payload.Content,
		Metadata:  payload.Metadata,
		Embedding: vector,
	}

	if err := collection.AddDocument(e.Request.Context(), doc); err != nil {
		return e.InternalServerError("Failed to insert document.", err)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"id":      payload.ID,
		"success": true,
	})
}

func llmGetDocument(e *core.RequestEvent) error {
	collection, err := llmResolveCollection(e)
	if err != nil {
		return err
	}

	id := e.Request.PathValue("id")
	if id == "" {
		return e.BadRequestError("Document id is required.", nil)
	}

	doc, err := collection.GetByID(e.Request.Context(), id)
	if err != nil {
		return e.NotFoundError("Document not found.", err)
	}

	return e.JSON(http.StatusOK, serializeDocument(&doc))
}

func llmUpdateDocument(e *core.RequestEvent) error {
	collection, err := llmResolveCollection(e)
	if err != nil {
		return err
	}

	id := e.Request.PathValue("id")
	if id == "" {
		return e.BadRequestError("Document id is required.", nil)
	}

	payload := &llmDocumentUpdatePayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	existing, err := collection.GetByID(e.Request.Context(), id)
	if err != nil {
		return e.NotFoundError("Document not found.", err)
	}

	contentModified := false

	if payload.Content != nil {
		existing.Content = *payload.Content
		contentModified = true
	}

	if payload.Metadata != nil {
		existing.Metadata = *payload.Metadata
	}

	if payload.Embedding != nil {
		vector, convErr := convertEmbedding(*payload.Embedding)
		if convErr != nil {
			return e.BadRequestError("Invalid embedding payload.", convErr)
		}
		existing.Embedding = vector
	} else if contentModified {
		existing.Embedding = nil
	}

	if err := collection.AddDocument(e.Request.Context(), existing); err != nil {
		return e.InternalServerError("Failed to update document.", err)
	}

	return e.JSON(http.StatusOK, map[string]any{"success": true})
}

func llmDeleteDocument(e *core.RequestEvent) error {
	collection, err := llmResolveCollection(e)
	if err != nil {
		return err
	}

	id := e.Request.PathValue("id")
	if id == "" {
		return e.BadRequestError("Document id is required.", nil)
	}

	if _, err := collection.GetByID(e.Request.Context(), id); err != nil {
		return e.NotFoundError("Document not found.", err)
	}

	if err := collection.Delete(e.Request.Context(), nil, nil, id); err != nil {
		return e.InternalServerError("Failed to delete document.", err)
	}

	return e.NoContent(http.StatusNoContent)
}

func llmQueryDocuments(e *core.RequestEvent) error {
	collection, err := llmResolveCollection(e)
	if err != nil {
		return err
	}

	payload := &llmQueryPayload{}
	if err := e.BindBody(payload); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	totalDocs := collection.Count()
	if totalDocs == 0 {
		return e.JSON(http.StatusOK, map[string]any{"results": []any{}})
	}

	limit := payload.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > totalDocs {
		limit = totalDocs
	}

	queryOptions := chromem.QueryOptions{
		QueryText:      payload.QueryText,
		QueryEmbedding: convertEmbeddingOrNil(payload.QueryEmbedding),
		NResults:       limit,
		Where:          payload.Where,
	}

	if payload.Negative != nil {
		mode := payload.Negative.Mode
		if mode == "" {
			mode = string(chromem.NEGATIVE_MODE_SUBTRACT)
		}
		queryOptions.Negative = chromem.NegativeQueryOptions{
			Text:      payload.Negative.Text,
			Embedding: convertEmbeddingOrNil(payload.Negative.Embedding),
			Mode:      chromem.NegativeMode(mode),
		}
		if payload.Negative.FilterThreshold != nil {
			queryOptions.Negative.FilterThreshold = *payload.Negative.FilterThreshold
		}
	}

	results, err := collection.QueryWithOptions(e.Request.Context(), queryOptions)
	if err != nil {
		return e.InternalServerError("Failed to execute query.", err)
	}

	apiResults := make([]map[string]any, 0, len(results))
	for _, res := range results {
		apiResults = append(apiResults, map[string]any{
			"id":         res.ID,
			"content":    res.Content,
			"metadata":   safeMetadata(res.Metadata),
			"similarity": res.Similarity,
		})
	}

	return e.JSON(http.StatusOK, map[string]any{"results": apiResults})
}

func llmResolveCollection(e *core.RequestEvent) (*chromem.Collection, error) {
	store := e.App.LLMStore()
	if store == nil {
		return nil, e.InternalServerError("LLM store is not initialized.", nil)
	}

	name := e.Request.PathValue("collection")
	if name == "" {
		return nil, e.BadRequestError("Collection name is required.", nil)
	}

	collection, err := store.GetCollectionContext(e.Request.Context(), name, nil)
	if err != nil {
		return nil, e.InternalServerError("Failed to load collection.", err)
	}
	if collection == nil {
		return nil, e.NotFoundError("Collection not found.", errors.New("missing collection"))
	}

	return collection, nil
}

func serializeDocument(doc *chromem.Document) map[string]any {
	return map[string]any{
		"id":        doc.ID,
		"content":   doc.Content,
		"metadata":  safeMetadata(doc.Metadata),
		"embedding": doc.Embedding,
	}
}

func safeMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(metadata))
	for k, v := range metadata {
		cloned[k] = v
	}
	return cloned
}

func convertEmbedding(values []float64) ([]float32, error) {
	if len(values) == 0 {
		return nil, nil
	}
	res := make([]float32, len(values))
	for i, v := range values {
		res[i] = float32(v)
	}
	return res, nil
}

func convertEmbeddingOrNil(values []float64) []float32 {
	vec, _ := convertEmbedding(values)
	return vec
}
