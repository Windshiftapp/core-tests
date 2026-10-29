//go:build test

package v2

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	apispec "windshift/api"
)

type itemOpenAPIOperation struct {
	Parameters  []itemOpenAPIParameter `json:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema itemOpenAPISchema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]itemOpenAPIResponse `json:"responses"`
}

type itemOpenAPIParameter struct {
	Ref  string `json:"$ref"`
	Name string `json:"name"`
	In   string `json:"in"`
}

type itemOpenAPISchema struct {
	Ref                  string                       `json:"$ref"`
	AdditionalProperties json.RawMessage              `json:"additionalProperties"`
	Properties           map[string]itemOpenAPISchema `json:"properties"`
	Description          string                       `json:"description"`
}

type itemOpenAPIResponse struct {
	Ref     string `json:"$ref"`
	Content map[string]struct {
		Schema itemOpenAPISchema `json:"schema"`
	} `json:"content"`
}

type itemOpenAPISpec struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Parameters map[string]itemOpenAPIParameter `json:"parameters"`
		Schemas    map[string]itemOpenAPISchema    `json:"schemas"`
	} `json:"components"`
}

func TestItemOpenAPIQueryParametersMatchHandlerContract(t *testing.T) {
	t.Parallel()
	spec := decodeItemOpenAPISpec(t)
	tests := []struct {
		method string
		path   string
		want   []string
	}{
		{method: "get", path: "/items", want: []string{"page", "page_size", "workspace_id", "collection_id", "ql", "sub_ql", "search", "status_id", "status_id_not", "priority_id", "assignee_id", "item_type_id", "iteration_id", "milestone_id", "id", "parent_id", "level", "max_level", "created_since", "completed_since", "sort", "cursor", "fields", "include_watermark", "completed_activity_days", "exclude_personal"}},
		{method: "post", path: "/items/batch", want: nil},
		{path: "/items/{item_id}/detail-summary", want: []string{"surface", "exclude_personal"}},
		{path: "/workspaces/{workspace_key}/items/{item_number}/detail-summary", want: []string{"surface", "exclude_personal"}},
		{path: "/items/{item_id}/descendants", want: []string{"max_depth"}},
		{path: "/items/{item_id}/time-rollup", want: []string{"max_depth"}},
		{path: "/items/backlog", want: []string{"page", "page_size", "workspace_id", "collection_id", "ql", "sub_ql", "fields", "include_watermark", "exclude_personal"}},
		{path: "/items/changes", want: []string{"workspace_id", "collection_id", "since", "sub_ql", "through", "limit", "exclude_personal"}},
		{path: "/items/{item_id}/type-change-analysis", want: []string{"target_item_type_id"}},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = "get"
			}
			op := itemOperation(t, spec, test.path, method)
			got := make([]string, 0, len(op.Parameters))
			for _, candidate := range op.Parameters {
				parameter := candidate
				if candidate.Ref != "" {
					const prefix = "#/components/parameters/"
					if !strings.HasPrefix(candidate.Ref, prefix) {
						t.Fatalf("unsupported parameter reference %q", candidate.Ref)
					}
					var ok bool
					parameter, ok = spec.Components.Parameters[strings.TrimPrefix(candidate.Ref, prefix)]
					if !ok {
						t.Fatalf("missing parameter reference %q", candidate.Ref)
					}
				}
				if parameter.In == "query" {
					got = append(got, parameter.Name)
				}
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("query parameters = %v, want %v", got, test.want)
			}
		})
	}
}

func TestItemOpenAPIRequestBodiesAreStrictAndTyped(t *testing.T) {
	t.Parallel()
	spec := decodeItemOpenAPISpec(t)
	tests := []struct {
		method string
		path   string
		media  string
	}{
		{method: "post", path: "/items", media: "application/json"},
		{method: "post", path: "/items/batch", media: "application/json"},
		{method: "patch", path: "/items/{item_id}", media: "application/merge-patch+json"},
		{method: "put", path: "/items/{item_id}/watch", media: "application/json"},
		{method: "post", path: "/items/bulk-update", media: "application/json"},
		{method: "post", path: "/items/bulk-patch", media: "application/json"},
		{method: "post", path: "/items/bulk-transition", media: "application/json"},
		{method: "post", path: "/items/bulk-delete", media: "application/json"},
		{method: "post", path: "/items/roadmap-hierarchy-dates", media: "application/json"},
		{method: "post", path: "/items/{item_id}/reparent-children", media: "application/json"},
		{method: "post", path: "/items/{item_id}/move-workspace/preview", media: "application/json"},
		{method: "post", path: "/items/{item_id}/move-workspace", media: "application/json"},
		{method: "patch", path: "/items/{item_id}/rank", media: "application/merge-patch+json"},
		{method: "post", path: "/items/{item_id}/transition", media: "application/json"},
		{method: "post", path: "/items/{item_id}/change-type", media: "application/json"},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			op := itemOperation(t, spec, test.path, test.method)
			if op.RequestBody == nil {
				t.Fatal("request body is missing")
			}
			media, ok := op.RequestBody.Content[test.media]
			if !ok {
				t.Fatalf("request media types = %v, want %q", op.RequestBody.Content, test.media)
			}
			const schemaPrefix = "#/components/schemas/"
			if !strings.HasPrefix(media.Schema.Ref, schemaPrefix) {
				t.Fatalf("request schema reference = %q", media.Schema.Ref)
			}
			schemaName := strings.TrimPrefix(media.Schema.Ref, schemaPrefix)
			schema, ok := spec.Components.Schemas[schemaName]
			if !ok {
				t.Fatalf("request schema %q is missing", schemaName)
			}
			if string(schema.AdditionalProperties) != "false" {
				t.Fatalf("request schema %q must reject unknown fields", schemaName)
			}
			for name, property := range schema.Properties {
				if property.Ref == "" && strings.TrimSpace(property.Description) == "" {
					t.Errorf("request schema %q property %q has no description", schemaName, name)
				}
			}
		})
	}
}

func TestItemOpenAPISuccessResponsesAreTyped(t *testing.T) {
	t.Parallel()
	spec := decodeItemOpenAPISpec(t)
	tests := []struct {
		method string
		path   string
		status string
	}{
		{method: "get", path: "/items", status: "200"},
		{method: "post", path: "/items", status: "201"},
		{method: "get", path: "/items/{item_id}", status: "200"},
		{method: "patch", path: "/items/{item_id}", status: "200"},
		{method: "get", path: "/items/{item_id}/detail-summary", status: "200"},
		{method: "get", path: "/items/{item_id}/time-rollup", status: "200"},
		{method: "get", path: "/items/{item_id}/history", status: "200"},
		{method: "get", path: "/items/{item_id}/status-durations", status: "200"},
		{method: "post", path: "/items/bulk-update", status: "200"},
		{method: "post", path: "/items/bulk-patch", status: "200"},
		{method: "post", path: "/items/bulk-transition", status: "200"},
		{method: "post", path: "/items/bulk-delete", status: "200"},
		{method: "post", path: "/items/{item_id}/move-workspace/preview", status: "200"},
		{method: "post", path: "/items/{item_id}/move-workspace", status: "200"},
		{method: "get", path: "/items/{item_id}/available-transitions", status: "200"},
		{method: "post", path: "/items/{item_id}/transition", status: "200"},
		{method: "get", path: "/items/{item_id}/type-change-analysis", status: "200"},
		{method: "post", path: "/items/{item_id}/change-type", status: "200"},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			op := itemOperation(t, spec, test.path, test.method)
			response, ok := op.Responses[test.status]
			if !ok {
				t.Fatalf("success status %s is missing", test.status)
			}
			media, ok := response.Content["application/json"]
			if !ok || !strings.HasPrefix(media.Schema.Ref, "#/components/schemas/") {
				t.Fatalf("success response is not typed: ref=%q content=%v", response.Ref, response.Content)
			}
		})
	}
}

func decodeItemOpenAPISpec(t *testing.T) itemOpenAPISpec {
	t.Helper()
	var spec itemOpenAPISpec
	if err := json.Unmarshal(apispec.V2SpecJSON, &spec); err != nil {
		t.Fatalf("decode OpenAPI: %v", err)
	}
	return spec
}

func itemOperation(t *testing.T, spec itemOpenAPISpec, path, method string) itemOpenAPIOperation {
	t.Helper()
	raw, ok := spec.Paths[path][method]
	if !ok {
		t.Fatalf("operation %s %s is missing", method, path)
	}
	var operation itemOpenAPIOperation
	if err := json.Unmarshal(raw, &operation); err != nil {
		t.Fatalf("decode operation %s %s: %v", method, path, err)
	}
	return operation
}
