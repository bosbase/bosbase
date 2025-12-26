package apis

import (
	"errors"
	"net/http"
	"strings"

	"encoding/json"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/search"
	"github.com/bosbase/bosbase-enterprise/tools/security"
)

// bindCollectionApi registers the collection api endpoints and the corresponding handlers.
func bindCollectionApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	subGroup := rg.Group("/collections").Bind(RequireSuperuserAuth())
	subGroup.GET("", collectionsList)
	subGroup.POST("", collectionCreate)
	subGroup.POST("/sql", collectionsImportFromSQLTables) // legacy alias
	subGroup.POST("/sql/import", collectionsImportFromSQLTables)
	subGroup.POST("/sql/tables", collectionsRegisterSQLTables)
	subGroup.GET("/{collection}", collectionView)
	subGroup.PATCH("/{collection}", collectionUpdate)
	subGroup.DELETE("/{collection}", collectionDelete)
	subGroup.DELETE("/{collection}/truncate", collectionTruncate)
	subGroup.PUT("/import", collectionsImport)
	subGroup.GET("/meta/scaffolds", collectionScaffolds)
	subGroup.GET("/{collection}/schema", collectionSchema)
	subGroup.GET("/schemas", collectionsSchemas)
}

func collectionsList(e *core.RequestEvent) error {
	fieldResolver := search.NewSimpleFieldResolver(
		"id", "created", "updated", "name", "system", "type",
	)

	collections := []*core.Collection{}

	result, err := search.NewProvider(fieldResolver).
		Query(e.App.CollectionQuery()).
		ParseAndExec(e.Request.URL.Query().Encode(), &collections)

	if err != nil {
		return e.BadRequestError("", err)
	}

	collections = ensureCollectionsAuditFields(collections)
	result.Items = collections

	event := new(core.CollectionsListRequestEvent)
	event.RequestEvent = e
	event.Collections = collections
	event.Result = result

	return event.App.OnCollectionsListRequest().Trigger(event, func(e *core.CollectionsListRequestEvent) error {
		return execAfterSuccessTx(true, e.App, func() error {
			return e.JSON(http.StatusOK, e.Result)
		})
	})
}

func collectionView(e *core.RequestEvent) error {
	collection, err := e.App.FindCachedCollectionByNameOrId(e.Request.PathValue("collection"))
	if err != nil || collection == nil {
		return e.NotFoundError("", err)
	}

	collection = ensureCollectionAuditFields(collection)

	event := new(core.CollectionRequestEvent)
	event.RequestEvent = e
	event.Collection = collection

	return e.App.OnCollectionViewRequest().Trigger(event, func(e *core.CollectionRequestEvent) error {
		return execAfterSuccessTx(true, e.App, func() error {
			return e.JSON(http.StatusOK, e.Collection)
		})
	})
}

func collectionCreate(e *core.RequestEvent) error {
	// populate the minimal required factory collection data (if any)
	factoryExtract := struct {
		Type string `form:"type" json:"type"`
		Name string `form:"name" json:"name"`
	}{}
	if err := e.BindBody(&factoryExtract); err != nil {
		return e.BadRequestError("Failed to load the collection type data due to invalid formatting.", err)
	}

	// create scaffold
	collection := core.NewCollection(factoryExtract.Type, factoryExtract.Name)

	// merge the scaffold with the submitted request data
	if err := e.BindBody(collection); err != nil {
		return e.BadRequestError("Failed to load the submitted data due to invalid formatting.", err)
	}

	event := new(core.CollectionRequestEvent)
	event.RequestEvent = e
	event.Collection = collection

	return e.App.OnCollectionCreateRequest().Trigger(event, func(e *core.CollectionRequestEvent) error {
		if err := e.App.Save(e.Collection); err != nil {
			// validation failure
			var validationErrors validation.Errors
			if errors.As(err, &validationErrors) {
				return e.BadRequestError("Failed to create collection.", validationErrors)
			}

			// other generic db error
			return e.BadRequestError("Failed to create collection. Raw error: \n"+err.Error(), nil)
		}

		return execAfterSuccessTx(true, e.App, func() error {
			return e.JSON(http.StatusOK, ensureCollectionAuditFields(e.Collection))
		})
	})
}

func collectionUpdate(e *core.RequestEvent) error {
	collection, err := e.App.FindCollectionByNameOrId(e.Request.PathValue("collection"))
	if err != nil || collection == nil {
		return e.NotFoundError("", err)
	}

	if err := e.BindBody(collection); err != nil {
		return e.BadRequestError("Failed to load the submitted data due to invalid formatting.", err)
	}

	event := new(core.CollectionRequestEvent)
	event.RequestEvent = e
	event.Collection = collection

	return event.App.OnCollectionUpdateRequest().Trigger(event, func(e *core.CollectionRequestEvent) error {
		if err := e.App.Save(e.Collection); err != nil {
			// validation failure
			var validationErrors validation.Errors
			if errors.As(err, &validationErrors) {
				return e.BadRequestError("Failed to update collection.", validationErrors)
			}

			// other generic db error
			return e.BadRequestError("Failed to update collection. Raw error: \n"+err.Error(), nil)
		}

		return execAfterSuccessTx(true, e.App, func() error {
			return e.JSON(http.StatusOK, ensureCollectionAuditFields(e.Collection))
		})
	})
}

func collectionDelete(e *core.RequestEvent) error {
	collection, err := e.App.FindCachedCollectionByNameOrId(e.Request.PathValue("collection"))
	if err != nil || collection == nil {
		return e.NotFoundError("", err)
	}

	event := new(core.CollectionRequestEvent)
	event.RequestEvent = e
	event.Collection = collection

	return e.App.OnCollectionDeleteRequest().Trigger(event, func(e *core.CollectionRequestEvent) error {
		if err := e.App.Delete(e.Collection); err != nil {
			msg := "Failed to delete collection"

			// check fo references
			refs, _ := e.App.FindCollectionReferences(e.Collection, e.Collection.Id)
			if len(refs) > 0 {
				names := make([]string, 0, len(refs))
				for ref := range refs {
					names = append(names, ref.Name)
				}
				msg += " probably due to existing reference in " + strings.Join(names, ", ")
			}

			return e.BadRequestError(msg, err)
		}

		return execAfterSuccessTx(true, e.App, func() error {
			return e.NoContent(http.StatusNoContent)
		})
	})
}

func collectionTruncate(e *core.RequestEvent) error {
	collection, err := e.App.FindCachedCollectionByNameOrId(e.Request.PathValue("collection"))
	if err != nil || collection == nil {
		return e.NotFoundError("", err)
	}

	if collection.IsView() {
		return e.BadRequestError("View collections cannot be truncated since they don't store their own records.", nil)
	}

	err = e.App.TruncateCollection(collection)
	if err != nil {
		return e.BadRequestError("Failed to truncate collection (most likely due to required cascade delete record references).", err)
	}

	return e.NoContent(http.StatusNoContent)
}

func collectionScaffolds(e *core.RequestEvent) error {
	randomId := security.RandomStringWithAlphabet(10, core.DefaultIdAlphabet) // could be used as part of the default indexes name

	collections := map[string]*core.Collection{
		core.CollectionTypeBase: core.NewBaseCollection("", randomId),
		core.CollectionTypeAuth: core.NewAuthCollection("", randomId),
		core.CollectionTypeView: core.NewViewCollection("", randomId),
	}

	for _, c := range collections {
		c.Id = "" // clear random id
	}

	return e.JSON(http.StatusOK, collections)
}

// CollectionFieldSchemaInfo represents simplified schema information for a single field.
type CollectionFieldSchemaInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required *bool  `json:"required,omitempty"`
	System   *bool  `json:"system,omitempty"`
	Hidden   *bool  `json:"hidden,omitempty"`
}

// CollectionSchemaInfo represents simplified schema information for a collection.
type CollectionSchemaInfo struct {
	Name   string                      `json:"name"`
	Type   string                      `json:"type"`
	Fields []CollectionFieldSchemaInfo `json:"fields"`
}

// convertFieldToSchemaInfo converts a Field to CollectionFieldSchemaInfo.
func convertFieldToSchemaInfo(field core.Field) CollectionFieldSchemaInfo {
	schemaInfo := CollectionFieldSchemaInfo{
		Name: field.GetName(),
		Type: field.Type(),
	}

	if required := getFieldRequired(field); required != nil {
		schemaInfo.Required = required
	}

	if field.GetSystem() {
		schemaInfo.System = boolPtr(true)
	}

	if field.GetHidden() {
		schemaInfo.Hidden = boolPtr(true)
	}

	return schemaInfo
}

// getFieldRequired extracts the required flag from a field.
// This is a helper function to check if a field is required based on its type.
func getFieldRequired(field core.Field) *bool {
	// Use JSON marshaling/unmarshaling as a generic way to extract the Required property
	// since different field types have different struct definitions but all serialize to JSON
	fieldMap := make(map[string]interface{})
	if jsonBytes, err := json.Marshal(field); err == nil {
		if err := json.Unmarshal(jsonBytes, &fieldMap); err == nil {
			if required, ok := fieldMap["required"].(bool); ok {
				// Only return the pointer if the value is true (omit false values)
				if required {
					return &required
				}
			}
		}
	}
	return nil
}

// convertCollectionToSchemaInfo converts a Collection to CollectionSchemaInfo.
func convertCollectionToSchemaInfo(collection *core.Collection) CollectionSchemaInfo {
	fields := make([]CollectionFieldSchemaInfo, 0, len(collection.Fields))
	hasCreatedBy := false
	hasUpdatedBy := false

	for _, field := range collection.Fields {
		info := convertFieldToSchemaInfo(field)
		switch info.Name {
		case core.FieldNameCreatedBy:
			hasCreatedBy = true
		case core.FieldNameUpdatedBy:
			hasUpdatedBy = true
		}

		fields = append(fields, info)
	}

	if collection.Type == core.CollectionTypeBase {
		if !hasCreatedBy {
			fields = append(fields, systemHiddenTextSchemaField(core.FieldNameCreatedBy))
		}
		if !hasUpdatedBy {
			fields = append(fields, systemHiddenTextSchemaField(core.FieldNameUpdatedBy))
		}
	}

	return CollectionSchemaInfo{
		Name:   collection.Name,
		Type:   collection.Type,
		Fields: fields,
	}
}

func collectionSchema(e *core.RequestEvent) error {
	collection, err := e.App.FindCachedCollectionByNameOrId(e.Request.PathValue("collection"))
	if err != nil || collection == nil {
		return e.NotFoundError("", err)
	}

	schemaInfo := convertCollectionToSchemaInfo(collection)

	return e.JSON(http.StatusOK, schemaInfo)
}

func collectionsSchemas(e *core.RequestEvent) error {
	collections, err := e.App.FindAllCollections()
	if err != nil {
		return e.InternalServerError("Failed to retrieve collections.", err)
	}

	schemas := make([]CollectionSchemaInfo, 0, len(collections))
	for _, collection := range collections {
		schemas = append(schemas, convertCollectionToSchemaInfo(collection))
	}

	return e.JSON(http.StatusOK, map[string]interface{}{
		"collections": schemas,
	})
}

func systemHiddenTextSchemaField(name string) CollectionFieldSchemaInfo {
	return CollectionFieldSchemaInfo{
		Name:   name,
		Type:   core.FieldTypeText,
		System: boolPtr(true),
		Hidden: boolPtr(false),
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func ensureCollectionsAuditFields(collections []*core.Collection) []*core.Collection {
	for i, collection := range collections {
		collections[i] = ensureCollectionAuditFields(collection)
	}

	return collections
}

func ensureCollectionAuditFields(collection *core.Collection) *core.Collection {
	if collection == nil || !collection.IsBase() || collection.ExternalTable {
		return collection
	}

	hasCreatedBy := collection.Fields.GetByName(core.FieldNameCreatedBy) != nil
	hasUpdatedBy := collection.Fields.GetByName(core.FieldNameUpdatedBy) != nil

	if hasCreatedBy && hasUpdatedBy {
		return collection
	}

	cloned := cloneCollection(collection)

	if !hasCreatedBy {
		cloned.Fields.Add(&core.TextField{
			Name:   core.FieldNameCreatedBy,
			System: true,
			Hidden: false,
		})
	} else if field := cloned.Fields.GetByName(core.FieldNameCreatedBy); field != nil {
		field.SetSystem(true)
		field.SetHidden(false)
	}

	if !hasUpdatedBy {
		cloned.Fields.Add(&core.TextField{
			Name:   core.FieldNameUpdatedBy,
			System: true,
			Hidden: false,
		})
	} else if field := cloned.Fields.GetByName(core.FieldNameUpdatedBy); field != nil {
		field.SetSystem(true)
		field.SetHidden(false)
	}

	return cloned
}

func cloneCollection(original *core.Collection) *core.Collection {
	if original == nil {
		return nil
	}

	cloned := *original

	if fieldsClone, err := original.Fields.Clone(); err == nil {
		cloned.Fields = fieldsClone
	}

	return &cloned
}
