package tests

import (
	"net/http"
	"strconv"
	"testing"
)

// TestCreateSSOProviderDefaultsOmittedBooleans keeps the create path honest:
// the pointer fields must still fall back to false when omitted.
func TestCreateSSOProviderDefaultsOmittedBooleans(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/sso/providers", map[string]any{
		"slug":                 "saml-defaults",
		"name":                 "SAML defaults",
		"provider_type":        "saml",
		"saml_idp_sso_url":     "https://idp.example.com/sso",
		"saml_idp_certificate": "cert",
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)

	var created struct {
		Enabled            bool `json:"enabled"`
		IsDefault          bool `json:"is_default"`
		AutoProvisionUsers bool `json:"auto_provision_users"`
		SAMLSignRequests   bool `json:"saml_sign_requests"`
	}
	DecodeJSON(t, createResp, &created)
	if created.Enabled || created.AutoProvisionUsers || created.SAMLSignRequests {
		t.Errorf("omitted booleans = enabled:%v auto:%v saml_sign:%v, want all false",
			created.Enabled, created.AutoProvisionUsers, created.SAMLSignRequests)
	}
	if !created.IsDefault {
		t.Error("first provider is_default = false, want true")
	}
}

// TestUpdateSSOProviderPreservesOmittedFields pins the partial-update contract:
// a payload that omits booleans leaves the stored values alone, while an
// explicit value still wins. The admin edit form does not carry the SAML
// fields, so a metadata save used to clear saml_sign_requests.
func TestUpdateSSOProviderPreservesOmittedFields(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/sso/providers", map[string]any{
		"slug":                 "saml-partial",
		"name":                 "SAML partial",
		"provider_type":        "saml",
		"enabled":              true,
		"auto_provision_users": true,
		"saml_idp_sso_url":     "https://idp.example.com/sso",
		"saml_idp_certificate": "cert",
		"saml_sign_requests":   true,
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)

	var created struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, createResp, &created)

	// The admin edit form omits the SAML fields; a rename must not clear them.
	updateResp := MakeAuthRequest(t, server, http.MethodPut, "/sso/providers/"+strconv.Itoa(created.ID), map[string]any{
		"name": "Renamed",
	})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)

	getResp := MakeAuthRequest(t, server, http.MethodGet, "/sso/providers/"+strconv.Itoa(created.ID), nil)
	defer getResp.Body.Close()
	AssertStatusCode(t, getResp, http.StatusOK)
	var got struct {
		Name               string `json:"name"`
		Enabled            bool   `json:"enabled"`
		AutoProvisionUsers bool   `json:"auto_provision_users"`
		SAMLSignRequests   bool   `json:"saml_sign_requests"`
	}
	DecodeJSON(t, getResp, &got)
	if got.Name != "Renamed" {
		t.Errorf("name = %q, want Renamed", got.Name)
	}
	if !got.SAMLSignRequests {
		t.Error("saml_sign_requests was cleared by a payload that omitted it")
	}
	if !got.Enabled {
		t.Error("enabled was cleared by a payload that omitted it")
	}
	if !got.AutoProvisionUsers {
		t.Error("auto_provision_users was cleared by a payload that omitted it")
	}

	// An explicit false still clears.
	clearResp := MakeAuthRequest(t, server, http.MethodPut, "/sso/providers/"+strconv.Itoa(created.ID), map[string]any{
		"saml_sign_requests": false,
	})
	defer clearResp.Body.Close()
	AssertStatusCode(t, clearResp, http.StatusOK)

	var cleared struct {
		SAMLSignRequests bool `json:"saml_sign_requests"`
	}
	DecodeJSON(t, clearResp, &cleared)
	if cleared.SAMLSignRequests {
		t.Error("saml_sign_requests = true, want false after an explicit clear")
	}
}
