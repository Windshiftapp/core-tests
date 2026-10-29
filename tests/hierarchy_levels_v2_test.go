package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestHierarchyLevelsV2Provisioning exercises the public v2 hierarchy-level
// surface (WI-1306): reads with the read scope, admin-gated writes with the
// write scope, invalid input rejection, and the exact denial contract for
// non-admins.
func TestHierarchyLevelsV2Provisioning(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	t.Run("create_requires_system_admin", func(t *testing.T) {
		// Non-admin users cannot request admin scopes at token creation, so
		// this token holds only non-admin scopes — exercising both the scope
		// and the system-admin denial path.
		_, _, editorPass := CreateTestUserWithCredentials(t, server, "hlv2editor", "hlv2editor@test.local")
		_, editorToken := CreateAuthCredentialsForUser(t, server, "hlv2editor", editorPass)

		resp := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost, "/rest/api/v2/hierarchy-levels", map[string]interface{}{
			"level": 5, "name": "Editor Level",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("create_rejects_invalid_input", func(t *testing.T) {
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/hierarchy-levels", map[string]interface{}{
			"level": -1, "name": "Invalid Level",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("create_list_get_update_delete_roundtrip", func(t *testing.T) {
		createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/hierarchy-levels", map[string]interface{}{
			"level": 5, "name": "Portfolio v2", "description": "v2 provisioning level",
		})
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created struct {
			Data struct {
				ID    int    `json:"id"`
				Level int    `json:"level"`
				Name  string `json:"name"`
			} `json:"data"`
		}
		DecodeJSON(t, createResp, &created)
		if created.Data.ID == 0 || created.Data.Name != "Portfolio v2" || created.Data.Level != 5 {
			t.Fatalf("created = %#v", created.Data)
		}

		listResp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/hierarchy-levels", nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)

		getResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/hierarchy-levels/%d", created.Data.ID), nil)
		defer getResp.Body.Close()
		AssertStatusCode(t, getResp, http.StatusOK)

		updateResp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/hierarchy-levels/%d", created.Data.ID), map[string]interface{}{
			"level": 6, "name": "Portfolio v2 Renamed", "description": "",
		})
		defer updateResp.Body.Close()
		AssertStatusCode(t, updateResp, http.StatusOK)
		var updated struct {
			Data struct {
				Name  string `json:"name"`
				Level int    `json:"level"`
			} `json:"data"`
		}
		DecodeJSON(t, updateResp, &updated)
		if updated.Data.Name != "Portfolio v2 Renamed" || updated.Data.Level != 6 {
			t.Fatalf("updated = %#v", updated.Data)
		}

		deleteResp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/hierarchy-levels/%d", created.Data.ID), nil)
		defer deleteResp.Body.Close()
		AssertStatusCode(t, deleteResp, http.StatusNoContent)
	})
}
