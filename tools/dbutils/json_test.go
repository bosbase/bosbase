package dbutils_test

import (
	"testing"

	"github.com/bosbase/bosbase-enterprise/tools/dbutils"
)

func TestJSONEach(t *testing.T) {
	result := dbutils.JSONEach("a.b")

	expected := "(SELECT jsonb_array_elements_text(CASE WHEN [[a.b]] IS NULL OR [[a.b]]::text = 'null' THEN '[]'::jsonb WHEN jsonb_typeof([[a.b]]::jsonb) = 'array' THEN [[a.b]]::jsonb ELSE jsonb_build_array([[a.b]]::jsonb) END) AS value)"

	if result != expected {
		t.Fatalf("Expected\n%v\ngot\n%v", expected, result)
	}
}

func TestJSONArrayLength(t *testing.T) {
	result := dbutils.JSONArrayLength("a.b")

	expected := "jsonb_array_length(CASE WHEN [[a.b]] IS NULL OR [[a.b]]::text = 'null' THEN '[]'::jsonb WHEN jsonb_typeof([[a.b]]::jsonb) = 'array' THEN [[a.b]]::jsonb ELSE jsonb_build_array([[a.b]]::jsonb) END)"

	if result != expected {
		t.Fatalf("Expected\n%v\ngot\n%v", expected, result)
	}
}

func TestJSONExtract(t *testing.T) {
	scenarios := []struct {
		name     string
		column   string
		path     string
		expected string
	}{
		{
			"empty path",
			"a.b",
			"",
			"jsonb_path_query_first(COALESCE([[a.b]]::jsonb, 'null'::jsonb), '$')",
		},
		{
			"starting with array index",
			"a.b",
			"[1].a[2]",
			"jsonb_path_query_first(COALESCE([[a.b]]::jsonb, 'null'::jsonb), '$[1].a[2]')",
		},
		{
			"starting with key",
			"a.b",
			"a.b[2].c",
			"jsonb_path_query_first(COALESCE([[a.b]]::jsonb, 'null'::jsonb), '$.a.b[2].c')",
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			result := dbutils.JSONExtract(s.column, s.path)

			if result != s.expected {
				t.Fatalf("Expected\n%v\ngot\n%v", s.expected, result)
			}
		})
	}
}
