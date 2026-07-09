# Vector, LLM Documents, and LangChainGo

This document covers vector storage/search, LLM document storage, and LangChainGo helper endpoints.

## Vector Collection Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Internal vector collection id. | `vec_123` |
| `name` | string | Vector collection table/name. | `docs_vectors` |
| `dimension` | integer | Vector dimension. | `384` |
| `distance` | string | Distance metric: `cosine`, `l2`, `inner_product`, or `l1`. | `cosine` |
| `count` | integer | Number of stored documents, included by list endpoint. | `120` |
| `options` | object | Optional implementation-specific settings. | `{}` |

## Vector Document Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Document id. Optional for insert; generated if omitted. | `doc_1` |
| `vector` | array of numbers | Embedding vector. Length must match collection dimension. | `[0.1, 0.2]` |
| `metadata` | object | Arbitrary JSON metadata. | `{ "source": "manual" }` |
| `content` | string | Optional original text/content. | `Hello world` |

## Vector Collections

All `/api/vectors` endpoints require superuser authentication.

### GET /api/vectors/collections

#### Function

Lists vector collections and document counts.

#### Response Example

```json
[
  {
    "id": "vec_123",
    "name": "docs_vectors",
    "dimension": 384,
    "distance": "cosine",
    "count": 120
  }
]
```

### POST /api/vectors/collections/{name}

#### Function

Creates a vector collection/table. Requires superuser authentication.

#### Path Parameters

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `name` | string | New vector collection name. | `docs_vectors` |

#### Request Body Fields

| Field | Type | Required | Default | Meaning | Example |
| --- | --- | --- | --- | --- | --- |
| `dimension` | integer | No | `384` | Embedding dimension. | `1536` |
| `distance` | string | No | `cosine` | Distance metric. | `cosine` |
| `options` | object | No | `{}` | Optional collection settings. | `{}` |

#### Request Example

```json
{
  "dimension": 1536,
  "distance": "cosine",
  "options": {}
}
```

#### Response

Created vector collection object.

### PATCH /api/vectors/collections/{name}

#### Function

Updates vector collection metadata/configuration. Requires superuser authentication.

#### Request Body Fields

Same fields as create; include only fields to change.

#### Response

Updated vector collection object.

### DELETE /api/vectors/collections/{name}

#### Function

Deletes a vector collection/table. Requires superuser authentication.

#### Response

`204 No Content`.

## Vector Documents

### POST /api/vectors/{collection}

#### Function

Inserts a vector document into a vector collection. Requires superuser authentication.

#### Request Body Fields

Use [Vector Document Object](#vector-document-object). `vector` is required.

#### Request Example

```json
{
  "id": "doc_1",
  "vector": [0.12, 0.25, 0.33],
  "metadata": { "source": "guide" },
  "content": "Vector search guide"
}
```

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Inserted document id. | `doc_1` |
| `success` | boolean | Whether insert succeeded. | `true` |

### POST /api/vectors/{collection}/documents/batch

#### Function

Inserts multiple vector documents. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `documents` | array | Yes | Vector documents to insert. | `[{ "id": "doc_1", "vector": [0.1] }]` |
| `skipDuplicates` | boolean | No | Skip duplicate ids instead of failing them. | `true` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `insertedCount` | integer | Number of inserted documents. | `10` |
| `failedCount` | integer | Number of failed documents. | `1` |
| `ids` | array of strings | Inserted document ids. | `["doc_1"]` |
| `errors` | array of strings | Per-document error messages. | `["dimension mismatch"]` |

### GET /api/vectors/{collection}

#### Function

Lists vector documents. Requires superuser authentication.

#### Query Parameters

| Field | Type | Required | Default | Meaning | Example |
| --- | --- | --- | --- | --- | --- |
| `page` | integer | No | `1` | Page number. | `1` |
| `perPage` | integer | No | `100` | Items per page, max `1000`. | `100` |

#### Response Fields

Paginated response with vector documents in `items`.

### POST /api/vectors/{collection}/documents/search

#### Function

Searches vector documents by embedding similarity. Requires superuser authentication.

#### Request Body Fields

| Field | Type | Required | Default | Meaning | Example |
| --- | --- | --- | --- | --- | --- |
| `queryVector` | array of numbers | Yes | none | Query embedding. | `[0.1, 0.2]` |
| `limit` | integer | No | `10` | Result limit, capped at `100`. | `5` |
| `filter` | object | No | none | Metadata filter object. | `{ "source": "guide" }` |
| `minScore` | number | No | none | Minimum score threshold. | `0.75` |
| `maxDistance` | number | No | none | Maximum distance threshold. | `0.3` |
| `includeDistance` | boolean | No | `false` | Include raw distance. | `true` |
| `includeContent` | boolean | No | `false` | Include document content. | `true` |

#### Request Example

```json
{
  "queryVector": [0.12, 0.25, 0.33],
  "limit": 5,
  "filter": { "source": "guide" },
  "includeDistance": true,
  "includeContent": true
}
```

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `results` | array | Search results. | `[]` |
| `results[].document` | object | Vector document. | `{ "id": "doc_1" }` |
| `results[].score` | number | Similarity score. | `0.91` |
| `results[].distance` | number | Raw distance, when requested. | `0.09` |
| `totalMatches` | integer | Optional total matches. | `5` |
| `queryTime` | integer | Optional query time in milliseconds. | `12` |

#### Response Example

```json
{
  "results": [
    {
      "document": {
        "id": "doc_1",
        "metadata": { "source": "guide" },
        "content": "Vector search guide"
      },
      "score": 0.91,
      "distance": 0.09
    }
  ],
  "totalMatches": 1,
  "queryTime": 12
}
```

### GET /api/vectors/{collection}/{id}

#### Function

Returns one vector document by id. Requires superuser authentication.

#### Response

Vector document object.

### PATCH /api/vectors/{collection}/{id}

#### Function

Updates one vector document. Requires superuser authentication.

#### Request Body Fields

Partial [Vector Document Object](#vector-document-object).

#### Response

`{ "id": "...", "success": true }`.

### DELETE /api/vectors/{collection}/{id}

#### Function

Deletes one vector document. Requires superuser authentication.

#### Response

`204 No Content`.

## LLM Documents

All `/api/llm-documents` endpoints require superuser authentication. These APIs use the app LLM document store.

### LLM Document Object

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Document id. Generated on insert if omitted. | `doc_1` |
| `content` | string | Text content. | `The API supports vectors.` |
| `metadata` | object of strings | String metadata. | `{ "source": "docs" }` |
| `embedding` | array of numbers | Optional embedding. | `[0.1, 0.2]` |

### GET /api/llm-documents/collections

#### Function

Lists LLM document collections.

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `id` | string | Collection id. | `docs` |
| `name` | string | Collection name. | `docs` |
| `metadata` | object | Collection metadata. | `{ "tenant": "default" }` |
| `count` | integer | Number of documents. | `10` |

### POST /api/llm-documents/collections/{name}

#### Function

Creates an LLM document collection.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `metadata` | object of strings | No | Collection metadata. | `{ "tenant": "default" }` |

#### Response Example

```json
{ "id": "docs", "name": "docs" }
```

### DELETE /api/llm-documents/collections/{name}

#### Function

Deletes an LLM document collection.

#### Response

`204 No Content`.

### GET /api/llm-documents/{collection}

#### Function

Lists documents in an LLM collection.

#### Query Parameters

| Field | Type | Required | Default | Meaning | Example |
| --- | --- | --- | --- | --- | --- |
| `page` | integer | No | `1` | Page number. | `1` |
| `perPage` | integer | No | `50` | Items per page, max `500`. | `100` |

#### Response Example

```json
{
  "items": [
    { "id": "doc_1", "content": "Text", "metadata": {}, "embedding": [0.1] }
  ],
  "page": 1,
  "perPage": 50,
  "totalItems": 1
}
```

### POST /api/llm-documents/{collection}

#### Function

Inserts an LLM document. Either `content` or `embedding` is required.

#### Request Body Fields

Use [LLM Document Object](#llm-document-object).

#### Response

`{ "id": "doc_1", "success": true }`.

### GET /api/llm-documents/{collection}/{id}

#### Function

Returns an LLM document by id.

#### Response

LLM document object.

### PATCH /api/llm-documents/{collection}/{id}

#### Function

Updates an LLM document. If `content` changes and `embedding` is omitted, embedding is cleared so it can be recalculated by the store.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `content` | string | No | New content. | `Updated text` |
| `metadata` | object of strings | No | New metadata. | `{ "source": "updated" }` |
| `embedding` | array of numbers | No | New embedding. | `[0.2, 0.3]` |

#### Response

`{ "success": true }`.

### DELETE /api/llm-documents/{collection}/{id}

#### Function

Deletes an LLM document.

#### Response

`204 No Content`.

### POST /api/llm-documents/{collection}/documents/query

#### Function

Queries documents by text and/or embedding, with optional negative query options.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `queryText` | string | No | Text query. | `How do vectors work?` |
| `queryEmbedding` | array of numbers | No | Embedding query. | `[0.1, 0.2]` |
| `limit` | integer | No | Number of results. Defaults to `5`. | `5` |
| `where` | object of strings | No | Metadata filter. | `{ "source": "docs" }` |
| `negative` | object | No | Negative query options. | `{ "text": "obsolete" }` |
| `negative.text` | string | No | Negative text. | `obsolete` |
| `negative.embedding` | array of numbers | No | Negative embedding. | `[0.9]` |
| `negative.mode` | string | No | Negative mode; defaults to subtract behavior. | `subtract` |
| `negative.filterThreshold` | number | No | Threshold for negative filtering. | `0.2` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `results` | array | Matching documents. | `[]` |
| `results[].id` | string | Document id. | `doc_1` |
| `results[].content` | string | Document content. | `Text` |
| `results[].metadata` | object | Metadata. | `{}` |
| `results[].similarity` | number | Similarity score. | `0.87` |

## LangChainGo Helpers

All `/api/langchaingo` endpoints require any valid auth token.

### Model Config Object

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `provider` | string | No | LLM provider. Supported by code: `openai`, `ollama`. Defaults to `openai`. | `openai` |
| `model` | string | No | Model name. Defaults to `gpt-4o-mini`. | `gpt-4o-mini` |
| `apiKey` | string | No | Provider API key. May also come from environment/config. | `sk-...` |
| `baseUrl` | string | No | Provider base URL. | `http://localhost:11434` |

### POST /api/langchaingo/completions

#### Function

Runs an LLM completion/chat request.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `model` | object | No | Model config. | `{ "provider": "openai" }` |
| `prompt` | string | No | Prompt text when `messages` is omitted. | `Write a summary` |
| `messages` | array | No | Chat messages. Required if `prompt` is empty. | `[{ "role": "human", "content": "Hi" }]` |
| `messages[].role` | string | Yes | Message role. | `human` |
| `messages[].content` | string | Yes | Message content. | `Hello` |
| `temperature` | number | No | Sampling temperature. | `0.7` |
| `maxTokens` | integer | No | Max generated tokens. | `500` |
| `topP` | number | No | Top-p sampling. | `1` |
| `candidateCount` | integer | No | Number of candidates. | `1` |
| `stop` | array of strings | No | Stop sequences. | `["END"]` |
| `json` | boolean | No | Request JSON output mode. | `true` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `content` | string | Generated content. | `Hello!` |
| `stopReason` | string | Provider stop reason. | `stop` |
| `generationInfo` | object | Provider generation metadata. | `{}` |
| `functionCall` | object | Function call information if returned. | `{ "name": "tool", "arguments": "{}" }` |
| `toolCalls` | array | Tool calls if returned. | `[]` |

### POST /api/langchaingo/rag

#### Function

Runs retrieval-augmented generation over an LLM document collection.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `model` | object | No | Model config. | `{}` |
| `collection` | string | Yes | LLM document collection name. | `docs` |
| `question` | string | Yes | User question. | `How do I authenticate?` |
| `topK` | integer | No | Retrieval count. Default `4`, max `20`. | `4` |
| `scoreThreshold` | number | No | Minimum similarity threshold. | `0.5` |
| `filters.where` | object | No | Metadata filter. | `{ "tenant": "default" }` |
| `filters.whereDocument` | object | No | Document/content filter. | `{}` |
| `promptTemplate` | string | No | Template using context/question variables. | `Context: {{.context}}` |
| `returnSources` | boolean | No | Include retrieved sources. | `true` |

#### Response

RAG response containing generated answer and optional sources.

### POST /api/langchaingo/documents/query

#### Function

Queries documents through LangChainGo and optionally returns generated answer/sources.

#### Request Body Fields

Same as RAG, except use `query` instead of `question`.

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `query` | string | Yes | Query text. | `authentication` |

### POST /api/langchaingo/sql

#### Function

Uses an LLM to generate and execute SQL over selected database tables. Requires auth.

#### Request Body Fields

| Field | Type | Required | Meaning | Example |
| --- | --- | --- | --- | --- |
| `model` | object | No | Model config. | `{}` |
| `query` | string | Yes | Natural-language SQL request. | `Show the latest 10 users` |
| `tables` | array of strings | No | Tables the model may use. If omitted, implementation chooses accessible schema context. | `["users"]` |
| `topK` | integer | No | Row/result limit. Default `10`, max `100`. | `10` |

#### Response Fields

| Field | Type | Meaning | Example |
| --- | --- | --- | --- |
| `sql` | string | Generated SQL. | `SELECT * FROM users LIMIT 10` |
| `answer` | string | Natural-language answer from the model. | `Here are the latest users.` |
| `columns` | array of strings | Result columns. | `["id", "email"]` |
| `rows` | array of string arrays | Result rows. | `[["u1", "a@example.com"]]` |
| `rawResult` | string | Execution summary. | `completed with 1 returned row(s)` |
