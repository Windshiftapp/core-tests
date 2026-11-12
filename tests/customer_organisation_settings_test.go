package tests

import (
	"net/http"
	"strconv"
	"testing"
)

// TestUpdateCustomerOrganisationPreservesSettings pins the partial-update
// contract for the structured settings blob: the Time > Customers form does not
// carry settings, so a metadata save must not wipe the portal request-sharing
// configuration stored there.
func TestUpdateCustomerOrganisationPreservesSettings(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/customer-organisations", map[string]any{
		"name":   "Acme",
		"active": true,
		"settings": map[string]any{
			"request_sharing":          "automatic",
			"request_sharing_audience": "organisation",
		},
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)

	var created struct {
		ID       int            `json:"id"`
		Settings map[string]any `json:"settings"`
	}
	DecodeJSON(t, createResp, &created)
	if created.Settings["request_sharing"] != "automatic" {
		t.Fatalf("create settings = %#v, want request_sharing automatic", created.Settings)
	}

	updateResp := MakeAuthRequest(t, server, http.MethodPut, "/customer-organisations/"+strconv.Itoa(created.ID), map[string]any{
		"name":   "Acme Renamed",
		"active": true,
	})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)

	getResp := MakeAuthRequest(t, server, http.MethodGet, "/customer-organisations/"+strconv.Itoa(created.ID), nil)
	defer getResp.Body.Close()
	AssertStatusCode(t, getResp, http.StatusOK)
	var got struct {
		Name     string         `json:"name"`
		Settings map[string]any `json:"settings"`
	}
	DecodeJSON(t, getResp, &got)
	if got.Name != "Acme Renamed" {
		t.Errorf("name = %q, want Acme Renamed", got.Name)
	}
	if got.Settings["request_sharing"] != "automatic" {
		t.Errorf("settings = %#v, want request_sharing automatic preserved", got.Settings)
	}
	if got.Settings["request_sharing_audience"] != "organisation" {
		t.Errorf("settings = %#v, want request_sharing_audience organisation preserved", got.Settings)
	}
}
