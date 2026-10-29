package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceConfigurationCatalogEntry struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	FieldType   string `json:"field_type"`
	SortOrder   int    `json:"sort_order"`
}

func TestConfigurationCatalogReadsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	tests := []struct {
		name       string
		path       string
		createPath string
		createBody map[string]any
		want       surfaceConfigurationCatalogEntry
	}{
		{
			name: "item type", path: "item-types", createPath: "/item-types",
			createBody: map[string]any{"name": "Shared catalog type", "description": "Type body", "icon": "circle", "color": "#112233", "sort_order": 41},
			want:       surfaceConfigurationCatalogEntry{Name: "Shared catalog type", Description: "Type body", Icon: "circle", Color: "#112233", SortOrder: 41},
		},
		{
			name: "priority", path: "priorities", createPath: "/priorities",
			createBody: map[string]any{"name": "Shared catalog priority", "description": "Priority body", "icon": "arrow-up", "color": "#223344", "sort_order": 42},
			want:       surfaceConfigurationCatalogEntry{Name: "Shared catalog priority", Description: "Priority body", Icon: "arrow-up", Color: "#223344", SortOrder: 42},
		},
		{
			name: "custom field", path: "custom-fields", createPath: "/admin/custom-fields",
			createBody: map[string]any{"name": "Shared catalog field", "field_type": "text", "description": "Field body", "display_order": 43},
			want:       surfaceConfigurationCatalogEntry{Name: "Shared catalog field", Description: "Field body", FieldType: "text"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			create := MakeAuthRequest(t, server, http.MethodPost, tc.createPath, tc.createBody)
			defer create.Body.Close()
			AssertStatusCode(t, create, http.StatusCreated)
			var session surfaceConfigurationCatalogEntry
			DecodeJSON(t, create, &session)

			get := MakeBearerRequest(t, server, http.MethodGet,
				fmt.Sprintf("/rest/api/v2/%s/%d", tc.path, session.ID), nil)
			defer get.Body.Close()
			AssertStatusCode(t, get, http.StatusOK)
			var bearer surfaceConfigurationCatalogEntry
			DecodeJSON(t, get, &bearer)

			tc.want.ID = session.ID
			if bearer != tc.want {
				t.Fatalf("bearer projection = %+v, want %+v", bearer, tc.want)
			}
		})
	}
}
