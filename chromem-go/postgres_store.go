package chromem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	llmCollectionsTable = "_llm_collections"
	llmDocsTablePrefix  = "_llm_docs_"
)

type pgStore struct {
	db *sql.DB
}

type persistedCollection struct {
	ID        string
	Name      string
	Metadata  map[string]string
	TableName string
	Dimension int
	Documents map[string]*Document
}

func newPGStore(db *sql.DB) *pgStore {
	return &pgStore{db: db}
}

func (s *pgStore) tableNameForCollection(name string) string {
	return llmDocsTablePrefix + hash2hex(name)
}

func (s *pgStore) loadCollections(ctx context.Context) ([]*persistedCollection, error) {
	rows, err := s.loadCollectionsRows(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	collections, pendingDocs, err := s.scanCollectionsRows(rows, true)
	if err != nil {
		return nil, err
	}

	// close rows early so the underlying connection can be reused
	if err := rows.Close(); err != nil {
		return nil, err
	}

	if err := s.ensureCollectionIDs(ctx, collections); err != nil {
		return nil, err
	}

	for _, pc := range pendingDocs {
		docs, err := s.loadDocumentsForCollection(ctx, pc.TableName)
		if err != nil {
			return nil, err
		}
		pc.Documents = docs
	}

	return collections, nil
}

func (s *pgStore) loadCollectionsMetadata(ctx context.Context) ([]*persistedCollection, error) {
	rows, err := s.loadCollectionsRows(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	collections, _, err := s.scanCollectionsRows(rows, false)
	if err != nil {
		return nil, err
	}

	if err := rows.Close(); err != nil {
		return nil, err
	}

	if err := s.ensureCollectionIDs(ctx, collections); err != nil {
		return nil, err
	}

	return collections, nil
}

func (s *pgStore) loadCollectionMetadata(ctx context.Context, name string) (*persistedCollection, error) {
	query := fmt.Sprintf(
		`SELECT id, name, COALESCE(metadata::text, '{}'), COALESCE(table_name, ''), COALESCE(dimension, 0)
		 FROM %s WHERE name = $1`,
		quoteIdentifier(llmCollectionsTable),
	)

	row := s.db.QueryRowContext(ctx, query, name)

	var (
		id        string
		colName   string
		metadata  string
		tableName string
		dimension int
	)

	if err := row.Scan(&id, &colName, &metadata, &tableName, &dimension); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	pc := &persistedCollection{
		ID:        id,
		Name:      colName,
		Metadata:  parseMetadataJSON(metadata),
		TableName: tableName,
		Dimension: dimension,
		Documents: make(map[string]*Document),
	}

	if err := s.ensureCollectionIDs(ctx, []*persistedCollection{pc}); err != nil {
		return nil, err
	}

	return pc, nil
}

func (s *pgStore) loadCollectionsRows(ctx context.Context) (*sql.Rows, error) {
	query := fmt.Sprintf(
		`SELECT id, name, COALESCE(metadata::text, '{}'), COALESCE(table_name, ''), COALESCE(dimension, 0)
		 FROM %s
		 ORDER BY name ASC`,
		quoteIdentifier(llmCollectionsTable),
	)

	return s.db.QueryContext(ctx, query)
}

func (s *pgStore) scanCollectionsRows(rows *sql.Rows, loadDocs bool) ([]*persistedCollection, []*persistedCollection, error) {
	var (
		collections []*persistedCollection
		pendingDocs []*persistedCollection
	)

	for rows.Next() {
		var (
			id        string
			name      string
			metadata  string
			tableName string
			dimension int
		)

		if err := rows.Scan(&id, &name, &metadata, &tableName, &dimension); err != nil {
			return nil, nil, err
		}

		pc := &persistedCollection{
			ID:        id,
			Name:      name,
			Metadata:  parseMetadataJSON(metadata),
			TableName: tableName,
			Dimension: dimension,
			Documents: make(map[string]*Document),
		}

		if loadDocs && tableName != "" {
			pendingDocs = append(pendingDocs, pc)
		}

		collections = append(collections, pc)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return collections, pendingDocs, nil
}

func (s *pgStore) loadDocumentsForCollection(ctx context.Context, tableName string) (map[string]*Document, error) {
	exists, err := s.tableExists(ctx, tableName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]*Document{}, nil
	}

	query := fmt.Sprintf(
		`SELECT id, COALESCE(metadata::text, '{}'), COALESCE(content, ''), embedding::text
		 FROM %s`,
		quoteIdentifier(tableName),
	)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	docs := make(map[string]*Document)

	for rows.Next() {
		var (
			id        string
			metadata  string
			content   string
			embedding string
		)

		if err := rows.Scan(&id, &metadata, &content, &embedding); err != nil {
			return nil, err
		}

		meta := make(map[string]string)
		if err := json.Unmarshal([]byte(metadata), &meta); err != nil {
			meta = make(map[string]string)
		}

		vector, err := parseVectorLiteral(embedding)
		if err != nil {
			return nil, err
		}

		docs[id] = &Document{
			ID:        id,
			Metadata:  meta,
			Content:   content,
			Embedding: vector,
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return docs, nil
}

func (s *pgStore) persistCollectionMetadata(c *Collection) error {
	ctx := context.Background()
	metadataJSON := metadataToJSON(c.metadata)
	if c.pgTableName == "" {
		c.pgTableName = s.tableNameForCollection(c.Name)
	}
	if c.id == "" {
		c.id = generateCollectionID()
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (id, name, metadata, table_name, dimension, created, updated)
		VALUES ($1, $2, $3::jsonb, $4, $5, NOW(), NOW())
		ON CONFLICT (name) DO UPDATE SET
			metadata = EXCLUDED.metadata,
			table_name = EXCLUDED.table_name,
			dimension = CASE
				WHEN EXCLUDED.dimension > 0 THEN EXCLUDED.dimension
				ELSE %s.dimension
			END,
			updated = NOW();
	`, quoteIdentifier(llmCollectionsTable), quoteIdentifier(llmCollectionsTable))

	_, err := s.db.ExecContext(ctx, query, c.id, c.Name, metadataJSON, c.pgTableName, c.vectorDimension)
	return err
}

func (s *pgStore) ensureCollectionTable(c *Collection) error {
	if c.vectorDimension <= 0 {
		return errors.New("collection vector dimension is not set")
	}
	if c.pgTableName == "" {
		c.pgTableName = s.tableNameForCollection(c.Name)
	}

	ctx := context.Background()

	createTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			content TEXT DEFAULT '' NOT NULL,
			metadata JSONB DEFAULT '{}'::jsonb NOT NULL,
			embedding vector(%d) NOT NULL,
			created TIMESTAMPTZ DEFAULT NOW() NOT NULL,
			updated TIMESTAMPTZ DEFAULT NOW() NOT NULL
		);
	`, quoteIdentifier(c.pgTableName), c.vectorDimension)

	if _, err := s.db.ExecContext(ctx, createTableSQL); err != nil {
		return err
	}

	indexSQL := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s ON %s USING ivfflat (embedding vector_cosine_ops);
	`, quoteIdentifier(fmt.Sprintf("idx_%s_embedding", c.pgTableName)), quoteIdentifier(c.pgTableName))

	if _, err := s.db.ExecContext(ctx, indexSQL); err != nil {
		return err
	}

	updateSQL := fmt.Sprintf(`
		UPDATE %s
		SET table_name = $1, dimension = $2, updated = NOW()
		WHERE name = $3;
	`, quoteIdentifier(llmCollectionsTable))

	_, err := s.db.ExecContext(ctx, updateSQL, c.pgTableName, c.vectorDimension, c.Name)
	return err
}

func (s *pgStore) upsertDocument(c *Collection, doc *Document) error {
	if c.pgTableName == "" {
		c.pgTableName = s.tableNameForCollection(c.Name)
	}
	if len(doc.Embedding) == 0 {
		return errors.New("document embedding is empty")
	}
	if c.vectorDimension == 0 {
		c.vectorDimension = len(doc.Embedding)
		if err := s.ensureCollectionTable(c); err != nil {
			return err
		}
	} else if len(doc.Embedding) != c.vectorDimension {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d", c.vectorDimension, len(doc.Embedding))
	}

	vectorLiteral := formatVectorLiteral(doc.Embedding)
	metadataJSON := metadataToJSON(doc.Metadata)

	query := fmt.Sprintf(`
		INSERT INTO %s (id, content, metadata, embedding, created, updated)
		VALUES ($1, $2, $3::jsonb, $4::vector, NOW(), NOW())
		ON CONFLICT (id) DO UPDATE SET
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			embedding = EXCLUDED.embedding,
			updated = NOW();
	`, quoteIdentifier(c.pgTableName))

	ctx := context.Background()
	_, err := s.db.ExecContext(ctx, query, doc.ID, doc.Content, metadataJSON, vectorLiteral)
	return err
}

func (s *pgStore) deleteDocument(c *Collection, docID string) error {
	if c.pgTableName == "" {
		return nil
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, quoteIdentifier(c.pgTableName))
	_, err := s.db.ExecContext(context.Background(), query, docID)
	return err
}

func (s *pgStore) deleteCollection(c *Collection) error {
	ctx := context.Background()
	if c.pgTableName != "" {
		dropSQL := fmt.Sprintf(`DROP TABLE IF EXISTS %s`, quoteIdentifier(c.pgTableName))
		if _, err := s.db.ExecContext(ctx, dropSQL); err != nil {
			return err
		}
	}

	deleteSQL := fmt.Sprintf(`DELETE FROM %s WHERE name = $1`, quoteIdentifier(llmCollectionsTable))
	_, err := s.db.ExecContext(ctx, deleteSQL, c.Name)
	return err
}

func (s *pgStore) reset() error {
	ctx := context.Background()

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT table_name FROM %s`, quoteIdentifier(llmCollectionsTable)))
	if err != nil {
		return err
	}
	defer rows.Close()

	var tableNames []string
	for rows.Next() {
		var tableName sql.NullString
		if err := rows.Scan(&tableName); err != nil {
			return err
		}
		if tableName.Valid && tableName.String != "" {
			tableNames = append(tableNames, tableName.String)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, tableName := range tableNames {
		dropSQL := fmt.Sprintf(`DROP TABLE IF EXISTS %s`, quoteIdentifier(tableName))
		if _, err := s.db.ExecContext(ctx, dropSQL); err != nil {
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s`, quoteIdentifier(llmCollectionsTable)))
	return err
}

func metadataToJSON(meta map[string]string) string {
	if meta == nil {
		meta = map[string]string{}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func formatVectorLiteral(vec []float32) string {
	if len(vec) == 0 {
		return "[]"
	}

	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')

	return b.String()
}

func parseVectorLiteral(lit string) ([]float32, error) {
	lit = strings.TrimSpace(lit)
	lit = strings.TrimPrefix(lit, "[")
	lit = strings.TrimSuffix(lit, "]")
	if strings.TrimSpace(lit) == "" {
		return []float32{}, nil
	}

	parts := strings.Split(lit, ",")
	res := make([]float32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		val, err := strconv.ParseFloat(p, 32)
		if err != nil {
			return nil, err
		}
		res = append(res, float32(val))
	}

	return res, nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func parseMetadataJSON(metadata string) map[string]string {
	metaMap := make(map[string]string)
	if err := json.Unmarshal([]byte(metadata), &metaMap); err != nil {
		return make(map[string]string)
	}
	return metaMap
}

func isValidCollectionID(id string) bool {
	if len(id) != 32 {
		return false
	}

	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	// UUIDv7 has version nibble '7' at the 13th hex character (0-indexed 12) in the hyphenless form.
	return id[12] == '7'
}

func (s *pgStore) ensureCollectionIDs(ctx context.Context, collections []*persistedCollection) error {
	type update struct {
		name string
		id   string
	}

	var pending []update

	for _, pc := range collections {
		if pc == nil || isValidCollectionID(pc.ID) {
			continue
		}

		newID := generateCollectionID()
		pc.ID = newID

		pending = append(pending, update{
			name: pc.Name,
			id:   newID,
		})
	}

	if len(pending) == 0 {
		return nil
	}

	query := fmt.Sprintf(`UPDATE %s SET id = $1 WHERE name = $2`, quoteIdentifier(llmCollectionsTable))
	for _, upd := range pending {
		if _, err := s.db.ExecContext(ctx, query, upd.id, upd.name); err != nil {
			return err
		}
	}

	return nil
}

func (s *pgStore) tableExists(ctx context.Context, tableName string) (bool, error) {
	if tableName == "" {
		return false, nil
	}

	var exists bool
	query := `SELECT to_regclass($1) IS NOT NULL`
	if err := s.db.QueryRowContext(ctx, query, tableName).Scan(&exists); err != nil {
		return false, err
	}

	return exists, nil
}
