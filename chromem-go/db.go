package chromem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sync"
)

// EmbeddingFunc is a function that creates embeddings for a given text.
// chromem-go will use OpenAI`s "text-embedding-3-small" model by default,
// but you can provide your own function, using any model you like.
// The function must return a *normalized* vector, i.e. the length of the vector
// must be 1. OpenAI's and Mistral's embedding models do this by default. Some
// others like Nomic's "nomic-embed-text-v1.5" don't.
type EmbeddingFunc func(ctx context.Context, text string) ([]float32, error)

// DB is the chromem-go database. It holds collections, which hold documents.
//
//	+----+    1-n    +------------+    n-n    +----------+
//	| DB |-----------| Collection |-----------| Document |
//	+----+           +------------+           +----------+
type DB struct {
	collections     map[string]*Collection
	collectionsLock sync.RWMutex

	pgStore *pgStore

	// ⚠️ When adding fields here, consider adding them to the persistence struct
	// versions in [DB.Export] and [DB.Import] as well!
}

// NewDB creates a new in-memory chromem-go DB.
// While it doesn't write files when you add collections and documents, you can
// still use [DB.Export] and [DB.Import] to export and import the entire DB
// from a file.
func NewDB() *DB {
	return &DB{
		collections: make(map[string]*Collection),
	}
}

// NewPostgresDB creates a new chromem-go DB backed by PostgreSQL with pgvector.
// The provided SQL DB must point to a database where the `_llm_collections`
// metadata table (and the pgvector extension) already exist. The function will
// hydrate all persisted collections and their documents into memory.
func NewPostgresDB(ctx context.Context, sqlDB *sql.DB) (*DB, error) {
	if sqlDB == nil {
		return nil, errors.New("sql DB is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	store := newPGStore(sqlDB)
	persisted, err := store.loadCollections(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't load collections from postgres: %w", err)
	}

	db := &DB{
		collections: make(map[string]*Collection),
		pgStore:     store,
	}

	for _, pc := range persisted {
		collection := &Collection{
			id:              pc.ID,
			Name:            pc.Name,
			metadata:        pc.Metadata,
			documents:       pc.Documents,
			pgStore:         store,
			pgTableName:     pc.TableName,
			vectorDimension: pc.Dimension,
		}
		db.collections[collection.Name] = collection
	}

	return db, nil
}

// Import imports the DB from a file at the given path. The file must be encoded
// as gob and can optionally be compressed with flate (as gzip) and encrypted
// with AES-GCM.
// This works for both the in-memory and persistent DBs.
// Existing collections are overwritten.
//
// - filePath: Mandatory, must not be empty
// - encryptionKey: Optional, must be 32 bytes long if provided
//
// Deprecated: Use [DB.ImportFromFile] instead.
func (db *DB) Import(filePath string, encryptionKey string) error {
	return db.ImportFromFile(filePath, encryptionKey)
}

// ImportFromFile imports the DB from a file at the given path. The file must be
// encoded as gob and can optionally be compressed with flate (as gzip) and encrypted
// with AES-GCM.
// This works for both the in-memory and persistent DBs.
// Existing collections are overwritten.
//
//   - filePath: Mandatory, must not be empty
//   - encryptionKey: Optional, must be 32 bytes long if provided
//   - collections: Optional. If provided, only the collections with the given names
//     are imported. Non-existing collections are ignored.
//     If not provided, all collections are imported.
func (db *DB) ImportFromFile(filePath string, encryptionKey string, collections ...string) error {
	if filePath == "" {
		return fmt.Errorf("file path is empty")
	}
	if encryptionKey != "" {
		// AES 256 requires a 32 byte key
		if len(encryptionKey) != 32 {
			return errors.New("encryption key must be 32 bytes long")
		}
	}

	// If the file doesn't exist or is a directory, return an error.
	fi, err := os.Stat(filePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("file doesn't exist: %s", filePath)
		}
		return fmt.Errorf("couldn't get info about the file: %w", err)
	} else if fi.IsDir() {
		return fmt.Errorf("path is a directory: %s", filePath)
	}

	// Create persistence structs with exported fields so that they can be decoded
	// from gob.
	type persistenceCollection struct {
		ID        string
		Name      string
		Metadata  map[string]string
		Documents map[string]*Document
	}
	persistenceDB := struct {
		Collections map[string]*persistenceCollection
	}{
		Collections: make(map[string]*persistenceCollection, len(db.collections)),
	}

	db.collectionsLock.Lock()
	defer db.collectionsLock.Unlock()

	err = readFromFile(filePath, &persistenceDB, encryptionKey)
	if err != nil {
		return fmt.Errorf("couldn't read file: %w", err)
	}

	for _, pc := range persistenceDB.Collections {
		if len(collections) > 0 && !slices.Contains(collections, pc.Name) {
			continue
		}
		collectionID := pc.ID
		if collectionID == "" {
			collectionID = generateCollectionID()
		}

		c := &Collection{
			id:   collectionID,
			Name: pc.Name,

			metadata:  pc.Metadata,
			documents: pc.Documents,
			pgStore:   db.pgStore,
		}
		if db.pgStore != nil {
			c.pgTableName = db.pgStore.tableNameForCollection(c.Name)
			if err := c.persistMetadata(); err != nil {
				return fmt.Errorf("couldn't persist collection metadata: %w", err)
			}
			for _, doc := range c.documents {
				if err := c.persistDocument(doc); err != nil {
					return err
				}
			}
		}
		db.collections[c.Name] = c
	}

	return nil
}

// ImportFromReader imports the DB from a reader. The stream must be encoded as
// gob and can optionally be compressed with flate (as gzip) and encrypted with
// AES-GCM.
// This works for both the in-memory and persistent DBs.
// Existing collections are overwritten.
// If the writer has to be closed, it's the caller's responsibility.
// This can be used to import DBs from object storage like S3. See
// https://github.com/philippgille/chromem-go/tree/main/examples/s3-export-import
// for an example.
//
//   - reader: An implementation of [io.ReadSeeker]
//   - encryptionKey: Optional, must be 32 bytes long if provided
//   - collections: Optional. If provided, only the collections with the given names
//     are imported. Non-existing collections are ignored.
//     If not provided, all collections are imported.
func (db *DB) ImportFromReader(reader io.ReadSeeker, encryptionKey string, collections ...string) error {
	if encryptionKey != "" {
		// AES 256 requires a 32 byte key
		if len(encryptionKey) != 32 {
			return errors.New("encryption key must be 32 bytes long")
		}
	}

	// Create persistence structs with exported fields so that they can be decoded
	// from gob.
	type persistenceCollection struct {
		ID        string
		Name      string
		Metadata  map[string]string
		Documents map[string]*Document
	}
	persistenceDB := struct {
		Collections map[string]*persistenceCollection
	}{
		Collections: make(map[string]*persistenceCollection, len(db.collections)),
	}

	db.collectionsLock.Lock()
	defer db.collectionsLock.Unlock()

	err := readFromReader(reader, &persistenceDB, encryptionKey)
	if err != nil {
		return fmt.Errorf("couldn't read stream: %w", err)
	}

	for _, pc := range persistenceDB.Collections {
		if len(collections) > 0 && !slices.Contains(collections, pc.Name) {
			continue
		}
		collectionID := pc.ID
		if collectionID == "" {
			collectionID = generateCollectionID()
		}

		c := &Collection{
			id:   collectionID,
			Name: pc.Name,

			metadata:  pc.Metadata,
			documents: pc.Documents,
			pgStore:   db.pgStore,
		}
		if db.pgStore != nil {
			c.pgTableName = db.pgStore.tableNameForCollection(c.Name)
			if err := c.persistMetadata(); err != nil {
				return fmt.Errorf("couldn't persist collection metadata: %w", err)
			}
			for _, doc := range c.documents {
				if err := c.persistDocument(doc); err != nil {
					return err
				}
			}
		}
		db.collections[c.Name] = c
	}

	return nil
}

// Export exports the DB to a file at the given path. The file is encoded as gob,
// optionally compressed with flate (as gzip) and optionally encrypted with AES-GCM.
// This works for both the in-memory and persistent DBs.
// If the file exists, it's overwritten, otherwise created.
//
//   - filePath: If empty, it defaults to "./chromem-go.gob" (+ ".gz" + ".enc")
//   - compress: Optional. Compresses as gzip if true.
//   - encryptionKey: Optional. Encrypts with AES-GCM if provided. Must be 32 bytes
//     long if provided.
//
// Deprecated: Use [DB.ExportToFile] instead.
func (db *DB) Export(filePath string, compress bool, encryptionKey string) error {
	return db.ExportToFile(filePath, compress, encryptionKey)
}

// ExportToFile exports the DB to a file at the given path. The file is encoded as gob,
// optionally compressed with flate (as gzip) and optionally encrypted with AES-GCM.
// This works for both the in-memory and persistent DBs.
// If the file exists, it's overwritten, otherwise created.
//
//   - filePath: If empty, it defaults to "./chromem-go.gob" (+ ".gz" + ".enc")
//   - compress: Optional. Compresses as gzip if true.
//   - encryptionKey: Optional. Encrypts with AES-GCM if provided. Must be 32 bytes
//     long if provided.
//   - collections: Optional. If provided, only the collections with the given names
//     are exported. Non-existing collections are ignored.
//     If not provided, all collections are exported.
func (db *DB) ExportToFile(filePath string, compress bool, encryptionKey string, collections ...string) error {
	if filePath == "" {
		filePath = "./chromem-go.gob"
		if compress {
			filePath += ".gz"
		}
		if encryptionKey != "" {
			filePath += ".enc"
		}
	}
	if encryptionKey != "" {
		// AES 256 requires a 32 byte key
		if len(encryptionKey) != 32 {
			return errors.New("encryption key must be 32 bytes long")
		}
	}

	// Create persistence structs with exported fields so that they can be encoded
	// as gob.
	type persistenceCollection struct {
		ID        string
		Name      string
		Metadata  map[string]string
		Documents map[string]*Document
	}
	persistenceDB := struct {
		Collections map[string]*persistenceCollection
	}{
		Collections: make(map[string]*persistenceCollection, len(db.collections)),
	}

	db.collectionsLock.RLock()
	defer db.collectionsLock.RUnlock()

	for k, v := range db.collections {
		if len(collections) == 0 || slices.Contains(collections, k) {
			if err := v.refreshDocumentsFromStore(context.Background()); err != nil {
				return fmt.Errorf("couldn't refresh collection '%s' from postgres: %w", v.Name, err)
			}
			persistenceDB.Collections[k] = &persistenceCollection{
				ID:        v.ID(),
				Name:      v.Name,
				Metadata:  v.metadata,
				Documents: v.documents,
			}
		}
	}

	err := persistToFile(filePath, persistenceDB, compress, encryptionKey)
	if err != nil {
		return fmt.Errorf("couldn't export DB: %w", err)
	}

	return nil
}

// ExportToWriter exports the DB to a writer. The stream is encoded as gob,
// optionally compressed with flate (as gzip) and optionally encrypted with AES-GCM.
// This works for both the in-memory and persistent DBs.
// If the writer has to be closed, it's the caller's responsibility.
// This can be used to export DBs to object storage like S3. See
// https://github.com/philippgille/chromem-go/tree/main/examples/s3-export-import
// for an example.
//
//   - writer: An implementation of [io.Writer]
//   - compress: Optional. Compresses as gzip if true.
//   - encryptionKey: Optional. Encrypts with AES-GCM if provided. Must be 32 bytes
//     long if provided.
//   - collections: Optional. If provided, only the collections with the given names
//     are exported. Non-existing collections are ignored.
//     If not provided, all collections are exported.
func (db *DB) ExportToWriter(writer io.Writer, compress bool, encryptionKey string, collections ...string) error {
	if encryptionKey != "" {
		// AES 256 requires a 32 byte key
		if len(encryptionKey) != 32 {
			return errors.New("encryption key must be 32 bytes long")
		}
	}

	// Create persistence structs with exported fields so that they can be encoded
	// as gob.
	type persistenceCollection struct {
		ID        string
		Name      string
		Metadata  map[string]string
		Documents map[string]*Document
	}
	persistenceDB := struct {
		Collections map[string]*persistenceCollection
	}{
		Collections: make(map[string]*persistenceCollection, len(db.collections)),
	}

	db.collectionsLock.RLock()
	defer db.collectionsLock.RUnlock()

	for k, v := range db.collections {
		if len(collections) == 0 || slices.Contains(collections, k) {
			if err := v.refreshDocumentsFromStore(context.Background()); err != nil {
				return fmt.Errorf("couldn't refresh collection '%s' from postgres: %w", v.Name, err)
			}
			persistenceDB.Collections[k] = &persistenceCollection{
				ID:        v.ID(),
				Name:      v.Name,
				Metadata:  v.metadata,
				Documents: v.documents,
			}
		}
	}

	err := persistToWriter(writer, persistenceDB, compress, encryptionKey)
	if err != nil {
		return fmt.Errorf("couldn't export DB: %w", err)
	}

	return nil
}

// CreateCollection creates a new collection with the given name and metadata.
//
//   - name: The name of the collection to create.
//   - metadata: Optional metadata to associate with the collection.
//   - embeddingFunc: Optional function to use to embed documents.
//     Uses the default embedding function if not provided.
func (db *DB) CreateCollection(name string, metadata map[string]string, embeddingFunc EmbeddingFunc) (*Collection, error) {
	if name == "" {
		return nil, errors.New("collection name is empty")
	}
	if embeddingFunc == nil {
		embeddingFunc = NewEmbeddingFuncDefault()
	}
	collection, err := newCollection(name, metadata, embeddingFunc, db.pgStore)
	if err != nil {
		return nil, fmt.Errorf("couldn't create collection: %w", err)
	}

	db.collectionsLock.Lock()
	defer db.collectionsLock.Unlock()
	db.collections[name] = collection
	return collection, nil
}

// ListCollections returns all collections in the DB, mapping name->Collection.
// The returned map is a copy of the internal map, so it's safe to directly modify
// the map itself. Direct modifications of the map won't reflect on the DB's map.
// To do that use the DB's methods like [DB.CreateCollection] and [DB.DeleteCollection].
// The map is not an entirely deep clone, so the collections themselves are still
// the original ones. Any methods on the collections like Add() for adding documents
// will be reflected on the DB's collections and are concurrency-safe.
func (db *DB) ListCollections() map[string]*Collection {
	_ = db.refreshCollectionsCache(context.Background())

	db.collectionsLock.RLock()
	defer db.collectionsLock.RUnlock()

	res := make(map[string]*Collection, len(db.collections))
	for k, v := range db.collections {
		res[k] = v
	}

	return res
}

// GetCollection returns the collection with the given name.
// The embeddingFunc param is only used if the DB is persistent and was just loaded
// from storage, in which case no embedding func is set yet (funcs are not (de-)serializable).
// It can be nil, in which case the default one will be used.
// The returned collection is a reference to the original collection, so any methods
// on the collection like Add() will be reflected on the DB's collection. Those
// operations are concurrency-safe.
// If the collection doesn't exist, this returns nil.
func (db *DB) GetCollection(name string, embeddingFunc EmbeddingFunc) *Collection {
	c, _ := db.getCollectionInternal(context.Background(), name, embeddingFunc)
	return c
}

// GetCollectionContext is like GetCollection but allows callers to control the context
// used when hydrating the collection from Postgres.
func (db *DB) GetCollectionContext(ctx context.Context, name string, embeddingFunc EmbeddingFunc) (*Collection, error) {
	return db.getCollectionInternal(ctx, name, embeddingFunc)
}

func (db *DB) getCollectionInternal(ctx context.Context, name string, embeddingFunc EmbeddingFunc) (*Collection, error) {
	if name == "" {
		return nil, nil
	}

	db.collectionsLock.RLock()
	c, ok := db.collections[name]
	db.collectionsLock.RUnlock()

	if !ok && db.pgStore != nil {
		pc, err := db.pgStore.loadCollectionMetadata(ctx, name)
		if err != nil {
			return nil, err
		}
		if pc != nil {
			c = db.addOrUpdateCollection(pc)
		}
	}

	if c == nil {
		return nil, nil
	}

	db.ensureCollectionEmbed(c, embeddingFunc)
	return c, nil
}

// GetOrCreateCollection returns the collection with the given name if it exists
// in the DB, or otherwise creates it. When creating:
//
//   - name: The name of the collection to create.
//   - metadata: Optional metadata to associate with the collection.
//   - embeddingFunc: Optional function to use to embed documents.
//     Uses the default embedding function if not provided.
func (db *DB) GetOrCreateCollection(name string, metadata map[string]string, embeddingFunc EmbeddingFunc) (*Collection, error) {
	// No need to lock here, because the methods we call do that.
	collection := db.GetCollection(name, embeddingFunc)
	if collection == nil {
		var err error
		collection, err = db.CreateCollection(name, metadata, embeddingFunc)
		if err != nil {
			return nil, fmt.Errorf("couldn't create collection: %w", err)
		}
	}
	return collection, nil
}

// DeleteCollection deletes the collection with the given name.
// If the collection doesn't exist, this is a no-op.
// If the DB is persistent, it also removes the collection's directory.
// You shouldn't hold any references to the collection after calling this method.
func (db *DB) DeleteCollection(name string) error {
	db.collectionsLock.Lock()
	defer db.collectionsLock.Unlock()

	col, ok := db.collections[name]
	if !ok {
		return nil
	}

	if db.pgStore != nil {
		if err := db.pgStore.deleteCollection(col); err != nil {
			return fmt.Errorf("couldn't delete collection in postgres: %w", err)
		}
	}

	delete(db.collections, name)
	return nil
}

// Reset removes all collections from the DB.
// For Postgres-backed DBs, it also removes all persisted state.
// You shouldn't hold any references to old collections after calling this method.
func (db *DB) Reset() error {
	db.collectionsLock.Lock()
	defer db.collectionsLock.Unlock()

	if db.pgStore != nil {
		if err := db.pgStore.reset(); err != nil {
			return fmt.Errorf("couldn't reset postgres persistence: %w", err)
		}
	}

	// Just assign a new map, the GC will take care of the rest.
	db.collections = make(map[string]*Collection)
	return nil
}

func (db *DB) addOrUpdateCollection(pc *persistedCollection) *Collection {
	if pc.Metadata == nil {
		pc.Metadata = map[string]string{}
	}

	db.collectionsLock.Lock()
	defer db.collectionsLock.Unlock()

	if existing, ok := db.collections[pc.Name]; ok {
		if existing.id == "" && pc.ID != "" {
			existing.id = pc.ID
		}
		existing.metadata = pc.Metadata
		if pc.TableName != "" {
			existing.pgTableName = pc.TableName
		}
		if pc.Dimension > 0 {
			existing.vectorDimension = pc.Dimension
		}
		return existing
	}

	c := &Collection{
		id:              pc.ID,
		Name:            pc.Name,
		metadata:        pc.Metadata,
		documents:       make(map[string]*Document),
		pgStore:         db.pgStore,
		pgTableName:     pc.TableName,
		vectorDimension: pc.Dimension,
	}
	db.collections[pc.Name] = c
	return c
}

func (db *DB) ensureCollectionEmbed(c *Collection, embeddingFunc EmbeddingFunc) {
	if c.embed != nil {
		return
	}
	if embeddingFunc == nil {
		c.embed = NewEmbeddingFuncDefault()
		return
	}
	c.embed = embeddingFunc
}

func (db *DB) refreshCollectionsCache(ctx context.Context) error {
	if db.pgStore == nil {
		return nil
	}

	persisted, err := db.pgStore.loadCollectionsMetadata(ctx)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(persisted))

	db.collectionsLock.Lock()
	defer db.collectionsLock.Unlock()

	for _, pc := range persisted {
		seen[pc.Name] = struct{}{}
		if existing, ok := db.collections[pc.Name]; ok {
			if existing.id == "" && pc.ID != "" {
				existing.id = pc.ID
			}
			if pc.Metadata != nil {
				existing.metadata = pc.Metadata
			}
			if pc.TableName != "" {
				existing.pgTableName = pc.TableName
			}
			if pc.Dimension > 0 {
				existing.vectorDimension = pc.Dimension
			}
			continue
		}

		meta := pc.Metadata
		if meta == nil {
			meta = map[string]string{}
		}

		db.collections[pc.Name] = &Collection{
			id:              pc.ID,
			Name:            pc.Name,
			metadata:        meta,
			documents:       make(map[string]*Document),
			pgStore:         db.pgStore,
			pgTableName:     pc.TableName,
			vectorDimension: pc.Dimension,
		}
	}

	for name := range db.collections {
		if _, ok := seen[name]; !ok {
			delete(db.collections, name)
		}
	}

	return nil
}
