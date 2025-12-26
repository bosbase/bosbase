package apis

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dbx"
	"github.com/gofrs/uuid/v5"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/security"
	"github.com/spf13/cast"
)

// bindVectorApi registers the vector api endpoints.
func bindVectorApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	subGroup := rg.Group("/vectors").Bind(RequireSuperuserAuth())

	// Collection management endpoints
	collectionsGroup := subGroup.Group("/collections")
	collectionsGroup.GET("", vectorListCollections)
	collectionsGroup.POST("/{name}", vectorCreateCollection)
	collectionsGroup.PATCH("/{name}", vectorUpdateCollection)
	collectionsGroup.DELETE("/{name}", vectorDeleteCollection)

	// Collection-specific endpoints
	collectionGroup := subGroup.Group("/{collection}")
	collectionGroup.POST("", vectorInsert)
	collectionGroup.POST("/documents/batch", vectorBatchInsert)
	collectionGroup.GET("", vectorList)
	collectionGroup.POST("/documents/search", vectorSearch)
	collectionGroup.GET("/{id}", vectorGet)
	collectionGroup.PATCH("/{id}", vectorUpdate)
	collectionGroup.DELETE("/{id}", vectorDelete)
}

// VectorDocument represents a vector document with embedding, metadata, and content
type VectorDocument struct {
	ID       string                 `json:"id,omitempty"`
	Vector   []float64              `json:"vector"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Content  string                 `json:"content,omitempty"`
}

// VectorCollection represents a vector collection configuration
type VectorCollection struct {
	Name      string                 `json:"name"`
	Dimension int                    `json:"dimension,omitempty"`
	Distance  string                 `json:"distance,omitempty"`
	Options   map[string]interface{} `json:"options,omitempty"`
}

// VectorSearchOptions represents search parameters
type VectorSearchOptions struct {
	QueryVector     []float64              `json:"queryVector"`
	Limit           int                    `json:"limit,omitempty"`
	Filter          map[string]interface{} `json:"filter,omitempty"`
	MinScore        *float64               `json:"minScore,omitempty"`
	MaxDistance     *float64               `json:"maxDistance,omitempty"`
	IncludeDistance bool                   `json:"includeDistance,omitempty"`
	IncludeContent  bool                   `json:"includeContent,omitempty"`
}

// VectorSearchResult represents a single search result
type VectorSearchResult struct {
	Document VectorDocument `json:"document"`
	Score    float64        `json:"score"`
	Distance *float64       `json:"distance,omitempty"`
}

// VectorSearchResponse represents the search response
type VectorSearchResponse struct {
	Results      []VectorSearchResult `json:"results"`
	TotalMatches *int                 `json:"totalMatches,omitempty"`
	QueryTime    *int64               `json:"queryTime,omitempty"`
}

// VectorBatchInsertOptions represents batch insert parameters
type VectorBatchInsertOptions struct {
	Documents      []VectorDocument `json:"documents"`
	SkipDuplicates bool             `json:"skipDuplicates,omitempty"`
}

// VectorBatchInsertResponse represents batch insert response
type VectorBatchInsertResponse struct {
	InsertedCount int      `json:"insertedCount"`
	FailedCount   int      `json:"failedCount"`
	IDs           []string `json:"ids"`
	Errors        []string `json:"errors,omitempty"`
}

// VectorInsertResponse represents insert/update response
type VectorInsertResponse struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
}

// vectorListCollections lists all vector collections
func vectorListCollections(e *core.RequestEvent) error {
	var collections []struct {
		ID        string `db:"id"`
		Name      string `db:"name"`
		Dimension int    `db:"dimension"`
		Distance  string `db:"distance"`
	}

	err := e.App.DB().Select("id", "name", "dimension", "distance").
		From("_vector_collections").
		OrderBy("name ASC").
		All(&collections)
	if err != nil {
		return e.InternalServerError("Failed to list collections.", err)
	}

	// Format response with counts
	result := make([]map[string]interface{}, len(collections))
	for i, col := range collections {
		// Get count for each collection
		var count int
		countSQL := fmt.Sprintf(`SELECT COUNT(*) FROM {{%s}};`, col.Name)
		countErr := e.App.DB().NewQuery(countSQL).Row(&count)
		if countErr != nil {
			count = 0 // If table doesn't exist or error, set to 0
		}

		result[i] = map[string]interface{}{
			"id":        col.ID,
			"name":      col.Name,
			"dimension": col.Dimension,
			"distance":  col.Distance,
			"count":     count,
		}
	}

	return e.JSON(http.StatusOK, result)
}

// vectorCreateCollection creates a new vector collection
func vectorCreateCollection(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("name")
	if collectionName == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	var config VectorCollection
	if err := e.BindBody(&config); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	// Set defaults
	dimension := 384
	if config.Dimension > 0 {
		dimension = config.Dimension
	}

	distance := "cosine"
	if config.Distance != "" {
		distance = config.Distance
	}

	// Validate distance metric
	validDistances := []string{"cosine", "l2", "inner_product"}
	isValid := false
	for _, d := range validDistances {
		if distance == d {
			isValid = true
			break
		}
	}
	if !isValid {
		return e.BadRequestError(fmt.Sprintf("Invalid distance metric. Must be one of: %v", validDistances), nil)
	}

	// Check if collection already exists
	var exists int
	err := e.App.DB().Select("COUNT(*)").
		From("_vector_collections").
		Where(dbx.HashExp{"name": collectionName}).
		Row(&exists)
	if err != nil {
		return e.InternalServerError("Failed to check collection existence.", err)
	}

	if exists > 0 {
		return e.BadRequestError("Collection already exists.", nil)
	}

	// Generate ID
	id := security.RandomString(15)
	if generatedID, err := uuid.NewV7(); err == nil {
		id = strings.ReplaceAll(generatedID.String(), "-", "")
	}

	// Insert into metadata table
	optionsJSON, _ := json.Marshal(config.Options)
	_, err = e.App.DB().Insert("_vector_collections", dbx.Params{
		"id":        id,
		"name":      collectionName,
		"dimension": dimension,
		"distance":  distance,
		"options":   string(optionsJSON),
		"created":   time.Now(),
		"updated":   time.Now(),
	}).Execute()
	if err != nil {
		return e.InternalServerError("Failed to create collection metadata.", err)
	}

	// Create the actual vector table
	// Table name will be the collection name, with vector column type based on dimension
	tableName := collectionName
	vectorType := fmt.Sprintf("vector(%d)", dimension)

	createTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS {{%s}} (
			[[id]] TEXT PRIMARY KEY DEFAULT %s NOT NULL,
			[[vector]] %s NOT NULL,
			[[metadata]] JSONB DEFAULT '{}'::jsonb,
			[[content]] TEXT DEFAULT '',
			%s,
			%s
		);
	`,
		tableName,
		core.RandomIDExpr(core.BuilderDriverName(e.App.NonconcurrentDB())),
		vectorType,
		core.TimestampColumnDefinition(core.BuilderDriverName(e.App.NonconcurrentDB()), "created"),
		core.TimestampColumnDefinition(core.BuilderDriverName(e.App.NonconcurrentDB()), "updated"),
	)

	// Create index based on distance metric
	var indexSQL string
	switch distance {
	case "cosine":
		indexSQL = fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_vector_cosine ON {{%s}} USING ivfflat (vector vector_cosine_ops);`, tableName, tableName)
	case "l2":
		indexSQL = fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_vector_l2 ON {{%s}} USING ivfflat (vector vector_l2_ops);`, tableName, tableName)
	case "inner_product":
		indexSQL = fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_vector_ip ON {{%s}} USING ivfflat (vector vector_ip_ops);`, tableName, tableName)
	}

	_, err = e.App.DB().NewQuery(createTableSQL + indexSQL).Execute()
	if err != nil {
		// Rollback metadata insertion
		e.App.DB().Delete("_vector_collections", dbx.HashExp{"id": id}).Execute()
		return e.InternalServerError("Failed to create vector table.", err)
	}

	return e.NoContent(http.StatusCreated)
}

// vectorUpdateCollection updates a vector collection configuration
func vectorUpdateCollection(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("name")
	if collectionName == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	// Check if collection exists
	var existingCollection struct {
		ID        string
		Name      string
		Dimension int
		Distance  string
	}
	err := e.App.DB().Select("id", "name", "dimension", "distance").
		From("_vector_collections").
		Where(dbx.HashExp{"name": collectionName}).
		Row(&existingCollection.ID, &existingCollection.Name, &existingCollection.Dimension, &existingCollection.Distance)
	if err != nil {
		return e.NotFoundError("Collection not found.", nil)
	}

	var config VectorCollection
	if err := e.BindBody(&config); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	// Update only distance metric (dimension cannot be changed after creation)
	updates := dbx.Params{}

	if config.Distance != "" && config.Distance != existingCollection.Distance {
		// Validate distance metric
		validDistances := []string{"cosine", "l2", "inner_product"}
		isValid := false
		for _, d := range validDistances {
			if config.Distance == d {
				isValid = true
				break
			}
		}
		if !isValid {
			return e.BadRequestError(fmt.Sprintf("Invalid distance metric. Must be one of: %v", validDistances), nil)
		}
		updates["distance"] = config.Distance

		// Drop old index and create new one based on new distance metric
		dropIndexSQL := fmt.Sprintf(`DROP INDEX IF EXISTS idx_%s_vector_cosine, idx_%s_vector_l2, idx_%s_vector_ip;`, collectionName, collectionName, collectionName)
		var indexSQL string
		switch config.Distance {
		case "cosine":
			indexSQL = fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_vector_cosine ON {{%s}} USING ivfflat (vector vector_cosine_ops);`, collectionName, collectionName)
		case "l2":
			indexSQL = fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_vector_l2 ON {{%s}} USING ivfflat (vector vector_l2_ops);`, collectionName, collectionName)
		case "inner_product":
			indexSQL = fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_vector_ip ON {{%s}} USING ivfflat (vector vector_ip_ops);`, collectionName, collectionName)
		}
		_, err = e.App.DB().NewQuery(dropIndexSQL + indexSQL).Execute()
		if err != nil {
			return e.InternalServerError("Failed to update vector index.", err)
		}
	}

	// Update options if provided
	if config.Options != nil {
		optionsJSON, _ := json.Marshal(config.Options)
		updates["options"] = string(optionsJSON)
	}

	if len(updates) == 0 {
		return e.BadRequestError("No fields to update.", nil)
	}

	updates["updated"] = time.Now()

	// Update metadata table
	_, err = e.App.DB().Update("_vector_collections", updates, dbx.HashExp{"name": collectionName}).Execute()
	if err != nil {
		return e.InternalServerError("Failed to update collection.", err)
	}

	return e.NoContent(http.StatusOK)
}

// vectorDeleteCollection deletes a vector collection
func vectorDeleteCollection(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("name")
	if collectionName == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	// Check if collection exists
	var exists int
	err := e.App.DB().Select("COUNT(*)").
		From("_vector_collections").
		Where(dbx.HashExp{"name": collectionName}).
		Row(&exists)
	if err != nil {
		return e.InternalServerError("Failed to check collection existence.", err)
	}

	if exists == 0 {
		return e.NotFoundError("Collection not found.", nil)
	}

	// Drop the vector table
	dropTableSQL := fmt.Sprintf(`DROP TABLE IF EXISTS {{%s}};`, collectionName)
	_, err = e.App.DB().NewQuery(dropTableSQL).Execute()
	if err != nil {
		return e.InternalServerError("Failed to drop vector table.", err)
	}

	// Delete from metadata table
	_, err = e.App.DB().Delete("_vector_collections", dbx.HashExp{"name": collectionName}).Execute()
	if err != nil {
		return e.InternalServerError("Failed to delete collection metadata.", err)
	}

	return e.NoContent(http.StatusNoContent)
}

// vectorInsert inserts a single vector document
func vectorInsert(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("collection")
	if collectionName == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	var doc VectorDocument
	if err := e.BindBody(&doc); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	// Validate vector
	if len(doc.Vector) == 0 {
		return e.BadRequestError("Vector is required.", nil)
	}

	// Check collection exists and get dimension
	var dimension int
	err := e.App.DB().Select("dimension").
		From("_vector_collections").
		Where(dbx.HashExp{"name": collectionName}).
		Row(&dimension)
	if err != nil {
		return e.NotFoundError("Collection not found.", nil)
	}

	// Validate dimension
	if len(doc.Vector) != dimension {
		return e.BadRequestError(fmt.Sprintf("Vector dimension mismatch. Expected %d, got %d", dimension, len(doc.Vector)), nil)
	}

	// Generate ID if not provided
	id := doc.ID
	if id == "" {
		id = security.RandomString(15)
	}

	// Prepare metadata
	metadataJSON := "{}"
	if doc.Metadata != nil {
		metadataBytes, _ := json.Marshal(doc.Metadata)
		metadataJSON = string(metadataBytes)
	}

	// Insert vector
	vectorStr := formatVectorForPostgres(doc.Vector)
	insertSQL := fmt.Sprintf(`
		INSERT INTO {{%s}} ([[id]], [[vector]], [[metadata]], [[content]], [[created]], [[updated]])
		VALUES ({:id}, {:vector}::vector, {:metadata}::jsonb, {:content}, NOW(), NOW())
		ON CONFLICT ([[id]]) DO UPDATE SET
			[[vector]] = {:vector}::vector,
			[[metadata]] = {:metadata}::jsonb,
			[[content]] = {:content},
			[[updated]] = NOW()
		RETURNING [[id]];
	`, collectionName)

	var insertedID string
	err = e.App.DB().NewQuery(insertSQL).
		Bind(dbx.Params{
			"id":       id,
			"vector":   vectorStr,
			"metadata": metadataJSON,
			"content":  doc.Content,
		}).
		Row(&insertedID)
	if err != nil {
		return e.InternalServerError("Failed to insert vector.", err)
	}

	return e.JSON(http.StatusOK, VectorInsertResponse{
		ID:      insertedID,
		Success: true,
	})
}

// vectorBatchInsert inserts multiple vector documents
func vectorBatchInsert(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("collection")
	if collectionName == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	var options VectorBatchInsertOptions
	if err := e.BindBody(&options); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	if len(options.Documents) == 0 {
		return e.BadRequestError("Documents array is required.", nil)
	}

	// Check collection exists and get dimension
	var dimension int
	err := e.App.DB().Select("dimension").
		From("_vector_collections").
		Where(dbx.HashExp{"name": collectionName}).
		Row(&dimension)
	if err != nil {
		return e.NotFoundError("Collection not found.", nil)
	}

	var insertedIDs []string
	var errors []string
	insertedCount := 0
	failedCount := 0

	for i, doc := range options.Documents {
		// Validate vector
		if len(doc.Vector) == 0 {
			errors = append(errors, fmt.Sprintf("Document %d: vector is required", i))
			failedCount++
			continue
		}

		// Validate dimension
		if len(doc.Vector) != dimension {
			errors = append(errors, fmt.Sprintf("Document %d: vector dimension mismatch. Expected %d, got %d", i, dimension, len(doc.Vector)))
			failedCount++
			continue
		}

		// Generate ID if not provided
		id := doc.ID
		if id == "" {
			id = security.RandomString(15)
		}

		// Prepare metadata
		metadataJSON := "{}"
		if doc.Metadata != nil {
			metadataBytes, _ := json.Marshal(doc.Metadata)
			metadataJSON = string(metadataBytes)
		}

		// Insert vector
		vectorStr := formatVectorForPostgres(doc.Vector)
		insertSQL := fmt.Sprintf(`
			INSERT INTO {{%s}} ([[id]], [[vector]], [[metadata]], [[content]], [[created]], [[updated]])
			VALUES ({:id}, {:vector}::vector, {:metadata}::jsonb, {:content}, NOW(), NOW())
		`, collectionName)

		if options.SkipDuplicates {
			insertSQL += ` ON CONFLICT ([[id]]) DO NOTHING`
		} else {
			insertSQL += ` ON CONFLICT ([[id]]) DO UPDATE SET
				[[vector]] = {:vector}::vector,
				[[metadata]] = {:metadata}::jsonb,
				[[content]] = {:content},
				[[updated]] = NOW()`
		}

		_, err := e.App.DB().NewQuery(insertSQL).
			Bind(dbx.Params{
				"id":       id,
				"vector":   vectorStr,
				"metadata": metadataJSON,
				"content":  doc.Content,
			}).
			Execute()
		if err != nil {
			errors = append(errors, fmt.Sprintf("Document %d: %v", i, err))
			failedCount++
			continue
		}

		insertedIDs = append(insertedIDs, id)
		insertedCount++
	}

	response := VectorBatchInsertResponse{
		InsertedCount: insertedCount,
		FailedCount:   failedCount,
		IDs:           insertedIDs,
	}
	if len(errors) > 0 {
		response.Errors = errors
	}

	return e.JSON(http.StatusOK, response)
}

// vectorUpdate updates a vector document
func vectorUpdate(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("collection")
	id := e.Request.PathValue("id")
	if collectionName == "" || id == "" {
		return e.BadRequestError("Collection name and ID are required.", nil)
	}

	var doc VectorDocument
	if err := e.BindBody(&doc); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	// Check collection exists and get dimension
	var dimension int
	err := e.App.DB().Select("dimension").
		From("_vector_collections").
		Where(dbx.HashExp{"name": collectionName}).
		Row(&dimension)
	if err != nil {
		return e.NotFoundError("Collection not found.", nil)
	}

	// Build update query dynamically
	updates := []string{}
	params := dbx.Params{}

	if len(doc.Vector) > 0 {
		if len(doc.Vector) != dimension {
			return e.BadRequestError(fmt.Sprintf("Vector dimension mismatch. Expected %d, got %d", dimension, len(doc.Vector)), nil)
		}
		vectorStr := formatVectorForPostgres(doc.Vector)
		updates = append(updates, "[[vector]] = {:vector}::vector")
		params["vector"] = vectorStr
	}

	if doc.Metadata != nil {
		metadataBytes, _ := json.Marshal(doc.Metadata)
		updates = append(updates, "[[metadata]] = {:metadata}::jsonb")
		params["metadata"] = string(metadataBytes)
	}

	if doc.Content != "" {
		updates = append(updates, "[[content]] = {:content}")
		params["content"] = doc.Content
	}

	if len(updates) == 0 {
		return e.BadRequestError("No fields to update.", nil)
	}

	updates = append(updates, "[[updated]] = NOW()")
	params["id"] = id

	updateSQL := fmt.Sprintf(`
		UPDATE {{%s}}
		SET %s
		WHERE [[id]] = {:id}
		RETURNING [[id]];
	`, collectionName, strings.Join(updates, ", "))

	var updatedID string
	err = e.App.DB().NewQuery(updateSQL).Bind(params).Row(&updatedID)
	if err != nil {
		return e.NotFoundError("Vector document not found.", err)
	}

	return e.JSON(http.StatusOK, VectorInsertResponse{
		ID:      updatedID,
		Success: true,
	})
}

// vectorDelete deletes a vector document
func vectorDelete(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("collection")
	id := e.Request.PathValue("id")
	if collectionName == "" || id == "" {
		return e.BadRequestError("Collection name and ID are required.", nil)
	}

	deleteSQL := fmt.Sprintf(`DELETE FROM {{%s}} WHERE [[id]] = {:id};`, collectionName)
	_, err := e.App.DB().NewQuery(deleteSQL).Bind(dbx.Params{"id": id}).Execute()
	if err != nil {
		return e.InternalServerError("Failed to delete vector.", err)
	}

	return e.NoContent(http.StatusNoContent)
}

// vectorGet retrieves a vector document by ID
func vectorGet(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("collection")
	id := e.Request.PathValue("id")
	if collectionName == "" || id == "" {
		return e.BadRequestError("Collection name and ID are required.", nil)
	}

	var result struct {
		ID       string
		Vector   string // Will be parsed from PostgreSQL vector format
		Metadata string
		Content  string
	}

	selectSQL := fmt.Sprintf(`
		SELECT [[id]], [[vector]]::text, COALESCE([[metadata]]::text, '{}'), COALESCE([[content]], '')
		FROM {{%s}}
		WHERE [[id]] = {:id};
	`, collectionName)

	err := e.App.DB().NewQuery(selectSQL).Bind(dbx.Params{"id": id}).Row(&result)
	if err != nil {
		return e.NotFoundError("Vector document not found.", err)
	}

	// Parse vector from PostgreSQL format
	vector, err := parseVectorFromPostgres(result.Vector)
	if err != nil {
		return e.InternalServerError("Failed to parse vector.", err)
	}

	// Parse metadata
	var metadata map[string]interface{}
	if result.Metadata != "" {
		if err := json.Unmarshal([]byte(result.Metadata), &metadata); err != nil {
			metadata = make(map[string]interface{})
		}
	} else {
		metadata = make(map[string]interface{})
	}

	return e.JSON(http.StatusOK, VectorDocument{
		ID:       result.ID,
		Vector:   vector,
		Metadata: metadata,
		Content:  result.Content,
	})
}

// vectorList lists vector documents with pagination
func vectorList(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("collection")
	if collectionName == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	// Get pagination params
	page := cast.ToInt(e.Request.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}

	perPage := cast.ToInt(e.Request.URL.Query().Get("perPage"))
	if perPage <= 0 {
		perPage = 100
	}
	if perPage > 1000 {
		perPage = 1000
	}

	// Get total count
	var totalItems int
	err := e.App.DB().Select("COUNT(*)").
		From(collectionName).
		Row(&totalItems)
	if err != nil {
		return e.InternalServerError("Failed to count documents.", err)
	}

	// Get documents
	var results []struct {
		ID       string
		Vector   string
		Metadata string
		Content  string
	}

	selectSQL := fmt.Sprintf(`
		SELECT [[id]], [[vector]]::text, COALESCE([[metadata]]::text, '{}'), COALESCE([[content]], '')
		FROM {{%s}}
		ORDER BY [[created]] DESC
		LIMIT {:limit} OFFSET {:offset};
	`, collectionName)

	offset := (page - 1) * perPage
	err = e.App.DB().NewQuery(selectSQL).
		Bind(dbx.Params{
			"limit":  perPage,
			"offset": offset,
		}).
		All(&results)
	if err != nil {
		return e.InternalServerError("Failed to list documents.", err)
	}

	// Parse results
	items := make([]VectorDocument, len(results))
	for i, r := range results {
		vector, _ := parseVectorFromPostgres(r.Vector)
		var metadata map[string]interface{}
		if r.Metadata != "" {
			json.Unmarshal([]byte(r.Metadata), &metadata)
		}
		if metadata == nil {
			metadata = make(map[string]interface{})
		}

		items[i] = VectorDocument{
			ID:       r.ID,
			Vector:   vector,
			Metadata: metadata,
			Content:  r.Content,
		}
	}

	totalPages := (totalItems + perPage - 1) / perPage

	return e.JSON(http.StatusOK, map[string]interface{}{
		"items":      items,
		"page":       page,
		"perPage":    perPage,
		"totalItems": totalItems,
		"totalPages": totalPages,
	})
}

// vectorSearch performs similarity search
func vectorSearch(e *core.RequestEvent) error {
	collectionName := e.Request.PathValue("collection")
	if collectionName == "" {
		return e.BadRequestError("Collection name is required.", nil)
	}

	var options VectorSearchOptions
	if err := e.BindBody(&options); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	if len(options.QueryVector) == 0 {
		return e.BadRequestError("Query vector is required.", nil)
	}

	// Check collection exists and get distance metric
	var distance string
	var dimension int
	err := e.App.DB().Select("distance", "dimension").
		From("_vector_collections").
		Where(dbx.HashExp{"name": collectionName}).
		Row(&distance, &dimension)
	if err != nil {
		return e.NotFoundError("Collection not found.", err)
	}

	// Validate dimension
	if len(options.QueryVector) != dimension {
		return e.BadRequestError(fmt.Sprintf("Query vector dimension mismatch. Expected %d, got %d", dimension, len(options.QueryVector)), nil)
	}

	// Set defaults
	limit := 10
	if options.Limit > 0 {
		limit = options.Limit
	}
	if limit > 100 {
		limit = 100
	}

	// Get distance metric SQL function
	var distanceSQL string
	switch distance {
	case "cosine":
		distanceSQL = "1 - (vector <=> {:queryVector}::vector)"
	case "l2":
		distanceSQL = "vector <-> {:queryVector}::vector"
	case "inner_product":
		distanceSQL = "vector <#> {:queryVector}::vector"
	default:
		distanceSQL = "1 - (vector <=> {:queryVector}::vector)" // default to cosine
	}

	startTime := time.Now()

	// Build query
	queryVectorStr := formatVectorForPostgres(options.QueryVector)

	// Build WHERE clause for metadata filtering
	whereClauses := []string{}
	params := dbx.Params{
		"queryVector": queryVectorStr,
		"limit":       limit,
	}

	paramIndex := 0
	if options.Filter != nil && len(options.Filter) > 0 {
		for key, value := range options.Filter {
			paramName := fmt.Sprintf("filter_%d", paramIndex)
			keyParamName := fmt.Sprintf("key_%d", paramIndex)
			// Use JSONB text operator for filtering - need to quote the key
			whereClauses = append(whereClauses, fmt.Sprintf("[[metadata]]->>{:%s} = {:%s}", keyParamName, paramName))
			params[keyParamName] = key
			// Convert value to string for JSONB text comparison
			if strVal, ok := value.(string); ok {
				params[paramName] = strVal
			} else {
				params[paramName] = fmt.Sprintf("%v", value)
			}
			paramIndex++
		}
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Build SELECT based on what's needed
	selectFields := "[[id]], [[vector]]::text, COALESCE([[metadata]]::text, '{}'), COALESCE([[content]], ''), " + distanceSQL + " as distance"
	if !options.IncludeContent {
		selectFields = "[[id]], [[vector]]::text, COALESCE([[metadata]]::text, '{}'), " + distanceSQL + " as distance"
	}

	searchSQL := fmt.Sprintf(`
		SELECT %s
		FROM {{%s}}
		%s
		ORDER BY vector <=> {:queryVector}::vector
		LIMIT {:limit};
	`, selectFields, collectionName, whereSQL)

	// For cosine similarity, score = 1 - distance
	// For L2, score = 1 / (1 + distance) to normalize to 0-1 range
	// For inner product, we'll use it directly but may need normalization

	var results []struct {
		ID       string
		Vector   string
		Metadata string
		Content  string
		Distance float64
	}

	err = e.App.DB().NewQuery(searchSQL).Bind(params).All(&results)
	if err != nil {
		return e.InternalServerError("Failed to search vectors.", err)
	}

	queryTime := time.Since(startTime).Milliseconds()

	// Convert to response format
	searchResults := make([]VectorSearchResult, 0, len(results))
	for _, r := range results {
		// Calculate score based on distance metric
		var score float64
		if distance == "cosine" {
			score = r.Distance // Already 1 - distance
		} else if distance == "l2" {
			score = 1.0 / (1.0 + r.Distance) // Normalize L2 to 0-1
		} else {
			score = r.Distance // inner_product - may need adjustment
		}

		// Apply filters
		if options.MinScore != nil && score < *options.MinScore {
			continue
		}
		if options.MaxDistance != nil && r.Distance > *options.MaxDistance {
			continue
		}

		vector, _ := parseVectorFromPostgres(r.Vector)
		var metadata map[string]interface{}
		if r.Metadata != "" {
			json.Unmarshal([]byte(r.Metadata), &metadata)
		}
		if metadata == nil {
			metadata = make(map[string]interface{})
		}

		doc := VectorDocument{
			ID:       r.ID,
			Vector:   vector,
			Metadata: metadata,
		}

		if options.IncludeContent {
			doc.Content = r.Content
		}

		result := VectorSearchResult{
			Document: doc,
			Score:    score,
		}

		if options.IncludeDistance {
			result.Distance = &r.Distance
		}

		searchResults = append(searchResults, result)
	}

	response := VectorSearchResponse{
		Results:   searchResults,
		QueryTime: &queryTime,
	}

	return e.JSON(http.StatusOK, response)
}

// formatVectorForPostgres formats a float64 slice as PostgreSQL vector literal
func formatVectorForPostgres(vector []float64) string {
	parts := make([]string, len(vector))
	for i, v := range vector {
		parts[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// parseVectorFromPostgres parses a PostgreSQL vector string into float64 slice
func parseVectorFromPostgres(vectorStr string) ([]float64, error) {
	// PostgreSQL vector format: [0.1,0.2,0.3] or [0.1, 0.2, 0.3]
	vectorStr = strings.TrimSpace(vectorStr)
	vectorStr = strings.TrimPrefix(vectorStr, "[")
	vectorStr = strings.TrimSuffix(vectorStr, "]")

	if vectorStr == "" {
		return []float64{}, nil
	}

	parts := strings.Split(vectorStr, ",")
	vector := make([]float64, len(parts))
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse vector component %d: %w", i, err)
		}
		vector[i] = v
	}

	return vector, nil
}
