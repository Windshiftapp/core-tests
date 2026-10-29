package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAssetValidationErrorsReturnBadRequest(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	setID, typeID := seedAssetSetAndType(t, server, "validation-target")
	otherSetID, otherTypeID := seedAssetSetAndType(t, server, "validation-other")
	createTaxonomy := func(kind string) int {
		response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/asset-sets/%d/%s", otherSetID, kind), map[string]any{"name": "Other set taxonomy"})
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var data map[string]any
		DecodeJSON(t, response, &data)
		return ExtractIDFromResponse(t, data)
	}
	categoryID, statusID := createTaxonomy("categories"), createTaxonomy("statuses")
	base := fmt.Sprintf("/v2/asset-sets/%d/assets", setID)
	created := MakeAuthRequest(t, server, http.MethodPost, base, map[string]any{"title": "Unchanged", "asset_type_id": typeID})
	AssertStatusCode(t, created, http.StatusCreated)
	var asset map[string]any
	DecodeJSON(t, created, &asset)
	created.Body.Close()
	assetID := ExtractIDFromResponse(t, asset)
	cases := []struct {
		name, field string
		value       any
	}{
		{"missing type", "asset_type_id", nil},
		{"negative type", "asset_type_id", -1},
		{"unknown type", "asset_type_id", 99999999},
		{"cross-set type", "asset_type_id", otherTypeID},
		{"cross-set category", "category_id", categoryID},
		{"cross-set status", "status_id", statusID},
	}
	check := func(t *testing.T, response *http.Response, context string) {
		t.Helper()
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusBadRequest)
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		DecodeJSON(t, response, &body)
		if body.Error.Code != "invalid_request" || !strings.Contains(body.Error.Message, context) {
			t.Fatalf("validation response = %+v", body)
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"title": "Must not persist", "asset_type_id": typeID}
			if tc.value == nil {
				delete(body, tc.field)
			} else {
				body[tc.field] = tc.value
			}
			check(t, MakeAuthRequest(t, server, http.MethodPost, base, body), tc.field)
			if tc.value != nil {
				check(t, MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/assets/%d", assetID), map[string]any{tc.field: tc.value, "title": "Must not update"}), tc.field)
			}
		})
	}
	t.Run("malformed CQL", func(t *testing.T) {
		check(t, MakeAuthRequest(t, server, http.MethodGet, base+"?ql="+url.QueryEscape("title = ("), nil), "CQL")
	})
	for _, kind := range []string{"types", "categories", "statuses"} {
		t.Run("missing "+kind+" name", func(t *testing.T) {
			check(t, MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/asset-sets/%d/%s", setID, kind), map[string]any{}), "name")
		})
	}
	var count int
	var title string
	if err := server.DB().QueryRow("SELECT COUNT(*) FROM assets WHERE set_id = ?", setID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := server.DB().QueryRow("SELECT title FROM assets WHERE id = ?", assetID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if count != 1 || title != "Unchanged" {
		t.Fatalf("persisted state: count=%d title=%q", count, title)
	}
	var events int
	if err := server.DB().QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = 'asset.updated.v1' AND aggregate_id = ?", assetID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("invalid requests emitted %d update events", events)
	}
}
