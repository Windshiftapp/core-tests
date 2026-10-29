package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// WI-1383 (reopened): the v2 PATCH path must enforce the same workspace key
// contract as v1 — reject invalid raw keys with 400 instead of silently
// sanitizing them into valid-looking keys ("bad key!" -> "BADKEY").
func TestV2WorkspaceKeyUpdateRejectsInvalidRawKeys(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	personal := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/personal", nil)
	defer personal.Body.Close()
	AssertStatusCode(t, personal, http.StatusCreated)
	var ws struct {
		ID  int    `json:"id"`
		Key string `json:"key"`
	}
	DecodeJSON(t, personal, &ws)

	cases := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "single character", key: "A"},
		{name: "space inside", key: "BAD KEY"},
		{name: "dash inside", key: "BAD-KEY"},
		{name: "punctuation", key: "BAD.KEY!"},
		{name: "too long", key: "ABCDEFGHIJ1"},
		// Sanitize strips HTML tags, so without a raw-value check the v2 path
		// would silently rewrite this to the valid key "ABC" where v1 rejects it.
		{name: "html tags", key: "A<b>BC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", ws.ID), map[string]any{"key": tc.key})
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusBadRequest)
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			DecodeJSON(t, resp, &body)
			if body.Error.Code != "invalid_request" || !strings.Contains(body.Error.Message, "2 to 10 alphanumeric") {
				t.Fatalf("invalid key %q response = %+v, want 400 invalid_request about the key contract", tc.key, body)
			}
		})
	}

	// The rejected attempts must not have touched the stored key.
	current := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/workspaces/%d", ws.ID), nil)
	defer current.Body.Close()
	AssertStatusCode(t, current, http.StatusOK)
	var after struct {
		Key string `json:"key"`
	}
	DecodeJSON(t, current, &after)
	if after.Key != ws.Key {
		t.Fatalf("workspace key = %q after rejected updates, want unchanged %q", after.Key, ws.Key)
	}
}

// A valid raw key is accepted and stored uppercase, matching the v1 contract.
func TestV2WorkspaceKeyUpdateAcceptsValidKeyOnPersonalWorkspace(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	personal := MakeAuthRequest(t, server, http.MethodGet, "/workspaces/personal", nil)
	defer personal.Body.Close()
	AssertStatusCode(t, personal, http.StatusCreated)
	var ws struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, personal, &ws)

	resp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", ws.ID), map[string]any{"key": "mytasks"})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var updated struct {
		Key string `json:"key"`
	}
	DecodeJSON(t, resp, &updated)
	if updated.Key != "MYTASKS" {
		t.Fatalf("updated key = %q, want MYTASKS", updated.Key)
	}

	current := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/workspaces/%d", ws.ID), nil)
	defer current.Body.Close()
	AssertStatusCode(t, current, http.StatusOK)
	var after struct {
		Key string `json:"key"`
	}
	DecodeJSON(t, current, &after)
	if after.Key != "MYTASKS" {
		t.Fatalf("persisted key = %q, want MYTASKS", after.Key)
	}
}

// Key changes stay personal-only: a regular workspace rename is denied with
// the unprocessable-entity contract from the WI-1383 service guard.
func TestV2WorkspaceKeyUpdateDeniedForRegularWorkspace(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Key Immutability", shortKey("KIM"))

	resp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/v2/workspaces/%d", workspaceID), map[string]any{"key": "NEWKEY1"})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusUnprocessableEntity)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	DecodeJSON(t, resp, &body)
	if body.Error.Code != "unprocessable_entity" {
		t.Fatalf("regular-workspace key change error = %+v, want unprocessable_entity", body)
	}
}
