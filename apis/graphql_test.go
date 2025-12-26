package apis_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bosbase/bosbase-enterprise/apis"
	"github.com/bosbase/bosbase-enterprise/tests"
)

const superuserToken = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY"

func TestGraphQLRecordsQuery(t *testing.T) {
	t.Parallel()

	query := `
		query RecordById($id: String!) {
			records(collection: "demo1", filter: "id = $id", expand: ["rel_many"], perPage: 2) {
				totalItems
				items { id data }
			}
		}`

	scenario := tests.ApiScenario{
		Name:   "GraphQL records filtered with expand",
		Method: http.MethodPost,
		URL:    "/api/graphql",
		Body:   newGraphqlBody(t, query, map[string]any{"id": "al1h9ijdeojtsjy"}),
		Headers: map[string]string{
			"Content-Type":  "application/json",
			"Authorization": superuserToken,
		},
		ExpectedStatus: 200,
		ExpectedContent: []string{
			`"totalItems":1`,
			`"id":"al1h9ijdeojtsjy"`,
			`"text":"test2"`,
			`"rel_many":[{"id":"bgs820n361vj1qd"`,
		},
	}

	scenario.Test(t)
}

func TestGraphQLTypenameQuery(t *testing.T) {
	t.Parallel()

	payload := `{"query":"query { __typename }"}`

	scenario := tests.ApiScenario{
		Name:           "GraphQL root typename",
		Method:         http.MethodPost,
		URL:            "/api/graphql",
		Body:           strings.NewReader(payload),
		ExpectedStatus: 200,
		ExpectedContent: []string{
			`"__typename":"Query"`,
		},
		Headers: map[string]string{
			"Content-Type":  "application/json",
			"Authorization": superuserToken,
		},
	}

	scenario.Test(t)
}

func TestGraphQLRecordsForbidden(t *testing.T) {
	t.Parallel()

	query := `
		query Forbidden {
			records(collection: "clients") {
				totalItems
			}
		}`

	scenario := tests.ApiScenario{
		Name:   "GraphQL records forbidden collection",
		Method: http.MethodPost,
		URL:    "/api/graphql",
		Body:   newGraphqlBody(t, query, nil),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		ExpectedStatus: 403,
		ExpectedContent: []string{
			`"message":"Only superusers can access the GraphQL API."`,
		},
		ExpectedEvents: map[string]int{
			"*": 0,
		},
	}

	scenario.Test(t)
}

func TestGraphQLMutations(t *testing.T) {
	t.Parallel()

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}

	send := func(t *testing.T, query string, variables map[string]any) map[string]any {
		t.Helper()

		payload := map[string]any{
			"query": query,
		}
		if variables != nil {
			payload["variables"] = variables
		}

		body, _ := json.Marshal(payload)

		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", superuserToken)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status %d, body %s", rec.Code, rec.Body.String())
		}

		res := map[string]any{}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if errs, ok := res["errors"]; ok && errs != nil {
			t.Fatalf("graphql errors: %v", errs)
		}

		data, _ := res["data"].(map[string]any)
		return data
	}

	createMutation := `
		mutation CreateDemo($data: JSON!) {
			createRecord(collection: "demo2", data: $data) {
				id
				data
			}
		}`

	createRes := send(t, createMutation, map[string]any{"data": map[string]any{"title": "graphql-created"}})
	createNode, ok := createRes["createRecord"].(map[string]any)
	if !ok {
		t.Fatalf("missing createRecord data: %v", createRes)
	}
	createdID, _ := createNode["id"].(string)
	if createdID == "" {
		t.Fatalf("expected created id, got %v", createNode)
	}

	updateMutation := `
		mutation UpdateDemo($id: ID!, $data: JSON!) {
			updateRecord(collection: "demo2", id: $id, data: $data) {
				id
				data
			}
		}`

	updateRes := send(t, updateMutation, map[string]any{"id": createdID, "data": map[string]any{"active": true}})
	updateNode, _ := updateRes["updateRecord"].(map[string]any)
	if updateNode["id"] != createdID {
		t.Fatalf("update returned unexpected id: %v", updateNode)
	}

	deleteMutation := `
		mutation DeleteDemo($id: ID!) {
			deleteRecord(collection: "demo2", id: $id)
		}`
	deleteRes := send(t, deleteMutation, map[string]any{"id": createdID})
	if okFlag, _ := deleteRes["deleteRecord"].(bool); !okFlag {
		t.Fatalf("expected deleteRecord true, got %v", deleteRes)
	}
}

func newGraphqlBody(t testing.TB, query string, variables map[string]any) *bytes.Reader {
	t.Helper()

	payload := map[string]any{
		"query": query,
	}

	if variables != nil {
		payload["variables"] = variables
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal graphql payload: %v", err)
	}

	return bytes.NewReader(raw)
}
