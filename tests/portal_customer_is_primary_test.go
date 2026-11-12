package tests

import (
	"net/http"
	"strconv"
	"testing"
)

// TestUpdatePortalCustomerPreservesOmittedIsPrimary pins the partial-update
// contract for the primary-contact flag: the detail edit form does not carry
// is_primary, so a metadata save must not demote the contact, while an explicit
// value still wins.
func TestUpdatePortalCustomerPreservesOmittedIsPrimary(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/portal-customers", map[string]any{
		"name":       "Primary Contact",
		"email":      "primary-contact@test.local",
		"is_primary": true,
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)

	var created struct {
		ID        int  `json:"id"`
		IsPrimary bool `json:"is_primary"`
	}
	DecodeJSON(t, createResp, &created)
	if !created.IsPrimary {
		t.Fatal("create did not persist is_primary")
	}

	updateResp := MakeAuthRequest(t, server, http.MethodPut, "/portal-customers/"+strconv.Itoa(created.ID), map[string]any{
		"name":  "Renamed Contact",
		"email": "primary-contact@test.local",
	})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)

	getResp := MakeAuthRequest(t, server, http.MethodGet, "/portal-customers/"+strconv.Itoa(created.ID), nil)
	defer getResp.Body.Close()
	AssertStatusCode(t, getResp, http.StatusOK)
	var got struct {
		IsPrimary bool `json:"is_primary"`
	}
	DecodeJSON(t, getResp, &got)
	if !got.IsPrimary {
		t.Error("is_primary was cleared by a payload that omitted it")
	}

	// An explicit false still clears.
	clearResp := MakeAuthRequest(t, server, http.MethodPut, "/portal-customers/"+strconv.Itoa(created.ID), map[string]any{
		"name":       "Renamed Contact",
		"email":      "primary-contact@test.local",
		"is_primary": false,
	})
	defer clearResp.Body.Close()
	AssertStatusCode(t, clearResp, http.StatusOK)

	var cleared struct {
		IsPrimary bool `json:"is_primary"`
	}
	DecodeJSON(t, clearResp, &cleared)
	if cleared.IsPrimary {
		t.Error("is_primary = true, want false after an explicit clear")
	}
}
