package apis

import (
	"testing"

	gql "graphql-go"
)

// Ensures the GraphQL schema compiles so runtime requests don't fail with schema errors.
func TestGraphQLSchemaParses(t *testing.T) {
	if _, err := gql.ParseSchema(graphqlSchemaString, &graphQLResolver{app: nil}, gql.UseFieldResolvers()); err != nil {
		t.Fatalf("unexpected graphql schema parse error: %v", err)
	}
}
