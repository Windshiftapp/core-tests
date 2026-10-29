package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestCannedResponsesHTTP covers the WI-1138 canned-response surface through
// the production API: round-trips, validation, cross-workspace denial, and
// preview rendering.
func TestCannedResponsesHTTP(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Canned HTTP", shortKey("CHT"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Canned Other", shortKey("COT"))

	base := fmt.Sprintf("/v2/workspaces/%d/canned-responses", workspaceID)

	var responseID int

	t.Run("create returns the active response with actor attribution", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, base, map[string]any{
			"name": "greeting", "body": "Hello {{requester.name}}, re {{ticket.key}}",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var doc struct {
			Data struct {
				ID        int    `json:"id"`
				Name      string `json:"name"`
				IsActive  bool   `json:"is_active"`
				IsPrivate bool   `json:"is_private"`
				CreatedBy *int   `json:"created_by"`
				UsedCount int    `json:"used_count"`
			} `json:"data"`
		}
		DecodeJSON(t, resp, &doc)
		if doc.Data.ID == 0 || !doc.Data.IsActive || doc.Data.IsPrivate {
			t.Fatalf("created = %+v, want an active public response", doc.Data)
		}
		if doc.Data.CreatedBy == nil {
			t.Fatal("created_by missing on create")
		}
		responseID = doc.Data.ID
	})

	t.Run("duplicate names are rejected case-insensitively", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPost, base, map[string]any{
			"name": "GREETING", "body": "other",
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusConflict)
	})

	t.Run("blank fields are rejected", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"name": "  ", "body": "x"},
			{"name": "unique-name", "body": ""},
		} {
			resp := MakeAuthRequest(t, server, http.MethodPost, base, body)
			resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusBadRequest)
		}
	})

	t.Run("get and list round-trip", func(t *testing.T) {
		get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("%s/%d", base, responseID), nil)
		defer get.Body.Close()
		AssertStatusCode(t, get, http.StatusOK)

		list := MakeAuthRequest(t, server, http.MethodGet, base, nil)
		defer list.Body.Close()
		AssertStatusCode(t, list, http.StatusOK)
		var docs struct {
			Data []struct {
				ID     int `json:"id"`
				Body   string
				IsActive bool `json:"is_active"`
			} `json:"data"`
		}
		DecodeJSON(t, list, &docs)
		if len(docs.Data) != 1 {
			t.Fatalf("list = %d rows, want 1", len(docs.Data))
		}
	})

	t.Run("preview renders sample values without an item", func(t *testing.T) {
		preview := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("%s/%d/preview", base, responseID), map[string]any{})
		defer preview.Body.Close()
		AssertStatusCode(t, preview, http.StatusOK)
		var doc struct {
			Data struct {
				Rendered string `json:"rendered"`
			} `json:"data"`
		}
		DecodeJSON(t, preview, &doc)
		if !strings.Contains(doc.Data.Rendered, "Sample Requester") {
			t.Fatalf("preview = %q, want sample substitution", doc.Data.Rendered)
		}
	})

	t.Run("patch archives and the default list hides it", func(t *testing.T) {
		patch := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("%s/%d", base, responseID), map[string]any{
			"is_active": false,
		})
		defer patch.Body.Close()
		AssertStatusCode(t, patch, http.StatusOK)

		list := MakeAuthRequest(t, server, http.MethodGet, base, nil)
		var docs struct {
			Data []struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		DecodeJSON(t, list, &docs)
		list.Body.Close()
		if len(docs.Data) != 0 {
			t.Fatalf("active list = %d rows, want 0 after archive", len(docs.Data))
		}

		includeArchived := MakeAuthRequest(t, server, http.MethodGet, base+"?include_archived=true", nil)
		defer includeArchived.Body.Close()
		DecodeJSON(t, includeArchived, &docs)
		if len(docs.Data) != 1 {
			t.Fatalf("include_archived list = %d rows, want 1", len(docs.Data))
		}
	})

	t.Run("other workspace cannot read, update, or delete", func(t *testing.T) {
		otherBase := fmt.Sprintf("/v2/workspaces/%d/canned-responses", otherWorkspaceID)

		get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("%s/%d", otherBase, responseID), nil)
		get.Body.Close()
		AssertStatusCode(t, get, http.StatusNotFound)

		patch := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("%s/%d", otherBase, responseID), map[string]any{
			"name": "hijacked",
		})
		patch.Body.Close()
		AssertStatusCode(t, patch, http.StatusNotFound)

		del := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("%s/%d", otherBase, responseID), nil)
		del.Body.Close()
		AssertStatusCode(t, del, http.StatusNotFound)
	})

	t.Run("delete removes the response", func(t *testing.T) {
		del := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("%s/%d", base, responseID), nil)
		defer del.Body.Close()
		AssertStatusCode(t, del, http.StatusNoContent)

		get := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("%s/%d", base, responseID), nil)
		get.Body.Close()
		AssertStatusCode(t, get, http.StatusNotFound)
	})
}
