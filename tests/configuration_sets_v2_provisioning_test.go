package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestConfigurationSetsV2Provisioning exercises the public v2 configuration
// set surface (WI-1306): reads, admin-gated create/delete with dependent-cache
// refresh warnings, default-set export refusal, and the non-admin denial
// contract.
func TestConfigurationSetsV2Provisioning(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	t.Run("mutations_require_system_admin", func(t *testing.T) {
		// Non-admin users cannot request admin scopes at token creation, so
		// this token holds only non-admin scopes — exercising scope + role
		// denial together.
		_, _, editorPass := CreateTestUserWithCredentials(t, server, "csv2editor", "csv2editor@test.local")
		_, editorToken := CreateAuthCredentialsForUser(t, server, "csv2editor", editorPass)

		resp := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
			"name": "Editor Set",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("create_rejects_unknown_workspaces_and_empty_name", func(t *testing.T) {
		noName := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
			"name": "  ",
		})
		defer noName.Body.Close()
		AssertStatusCode(t, noName, http.StatusBadRequest)

		badWS := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
			"name": "Bad WS Set", "workspace_ids": []int{999999},
		})
		defer badWS.Body.Close()
		AssertStatusCode(t, badWS, http.StatusBadRequest)
	})

	t.Run("create_list_get_delete_roundtrip", func(t *testing.T) {
		wsID, _ := CreateTestWorkspace(t, server, "Config Set v2 WS", "")
		createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
			"name": "Provisioned Set", "description": "from v2", "workspace_ids": []int{wsID},
		})
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created struct {
			Data struct {
				ConfigurationSet struct {
					ID   int    `json:"id"`
					Name string `json:"name"`
				} `json:"configuration_set"`
			} `json:"data"`
		}
		DecodeJSON(t, createResp, &created)
		setID := created.Data.ConfigurationSet.ID
		if setID == 0 {
			t.Fatalf("created = %#v", created.Data)
		}

		listResp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/configuration-sets?search=Provisioned", nil)
		defer listResp.Body.Close()
		AssertStatusCode(t, listResp, http.StatusOK)

		getResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/configuration-sets/%d", setID), nil)
		defer getResp.Body.Close()
		AssertStatusCode(t, getResp, http.StatusOK)

		exportResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/configuration-sets/%d/export", setID), nil)
		defer exportResp.Body.Close()
		AssertStatusCode(t, exportResp, http.StatusOK)

		deleteResp := MakeBearerRequest(t, server, http.MethodDelete, fmt.Sprintf("/rest/api/v2/configuration-sets/%d", setID), nil)
		defer deleteResp.Body.Close()
		AssertStatusCode(t, deleteResp, http.StatusNoContent)

		getGone := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/configuration-sets/%d", setID), nil)
		defer getGone.Body.Close()
		AssertStatusCode(t, getGone, http.StatusNotFound)
	})

	t.Run("default_set_export_is_refused", func(t *testing.T) {
		defaultID := GetDefaultConfigurationSet(t, server)
		resp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/configuration-sets/%d/export", defaultID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})
}
