package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestAttachmentVisibilityToggleControlsPortalAccess covers the v2
// PATCH /attachments/{attachment_id} endpoint added in WI-2032: an editor can
// hide an attachment from portal customers and reveal it again, and the
// portal list/download paths honor the flag.
func TestAttachmentVisibilityToggleControlsPortalAccess(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Attachment Visibility Toggle", shortKey("AVT"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	_, ownerCookie := CreatePortalCustomerWithSession(t, server, channelID, "Visibility Owner",
		fmt.Sprintf("visibility-owner-%d@example.com", time.Now().UnixNano()))

	itemID := SubmitPortalRequest(t, server, portalSlug, ownerCookie, "Request with a togglable file")
	uploadPath := fmt.Sprintf("/portal/%s/requests/%d/attachments", portalSlug, itemID)

	upload := makePortalMultipartRequest(t, server, ownerCookie, uploadPath, "evidence.png", validTestPNG(t))
	defer upload.Body.Close()
	AssertStatusCode(t, upload, http.StatusCreated)
	var uploaded struct {
		Attachment struct {
			ID int `json:"id"`
		} `json:"attachment"`
	}
	DecodeJSON(t, upload, &uploaded)
	if uploaded.Attachment.ID == 0 {
		t.Fatal("upload response has no attachment id")
	}
	attachmentID := uploaded.Attachment.ID

	visibilityPath := fmt.Sprintf("/v2/attachments/%d", attachmentID)

	// A freshly uploaded file is visible to the customer.
	portalList := MakePortalRequest(t, server, ownerCookie, http.MethodGet, uploadPath, nil)
	defer portalList.Body.Close()
	AssertStatusCode(t, portalList, http.StatusOK)
	var before []map[string]any
	DecodeJSON(t, portalList, &before)
	if len(before) != 1 {
		t.Fatalf("portal attachment count before hide = %d, want 1", len(before))
	}

	// The editor hides it.
	hide := MakeAuthRequest(t, server, http.MethodPatch, visibilityPath, map[string]any{"is_internal": true})
	defer hide.Body.Close()
	AssertStatusCode(t, hide, http.StatusOK)
	var hidden struct {
		Data struct {
			ID         int  `json:"id"`
			IsInternal bool `json:"is_internal"`
		} `json:"data"`
	}
	DecodeJSON(t, hide, &hidden)
	if hidden.Data.ID != attachmentID || !hidden.Data.IsInternal {
		t.Fatalf("hide response = %+v, want id %d with is_internal true", hidden.Data, attachmentID)
	}

	// The portal no longer lists or serves the file.
	portalList = MakePortalRequest(t, server, ownerCookie, http.MethodGet, uploadPath, nil)
	defer portalList.Body.Close()
	AssertStatusCode(t, portalList, http.StatusOK)
	var after []map[string]any
	DecodeJSON(t, portalList, &after)
	if len(after) != 0 {
		t.Fatalf("portal attachment count after hide = %d, want 0", len(after))
	}
	denied := MakePortalRequest(t, server, ownerCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/requests/%d/attachments/%d/download", portalSlug, itemID, attachmentID), nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusNotFound)

	// Workspace members still see it, flagged as internal.
	memberList := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/attachments", itemID), nil)
	defer memberList.Body.Close()
	AssertStatusCode(t, memberList, http.StatusOK)
	var memberPage struct {
		Data []struct {
			ID         int  `json:"id"`
			IsInternal bool `json:"is_internal"`
		} `json:"data"`
	}
	if err := json.NewDecoder(memberList.Body).Decode(&memberPage); err != nil {
		t.Fatalf("decode member attachment list: %v", err)
	}
	if len(memberPage.Data) != 1 || !memberPage.Data[0].IsInternal {
		t.Fatalf("member attachments = %+v, want one with is_internal true", memberPage.Data)
	}

	// Revealing it restores portal access.
	reveal := MakeAuthRequest(t, server, http.MethodPatch, visibilityPath, map[string]any{"is_internal": false})
	defer reveal.Body.Close()
	AssertStatusCode(t, reveal, http.StatusOK)
	portalList = MakePortalRequest(t, server, ownerCookie, http.MethodGet, uploadPath, nil)
	defer portalList.Body.Close()
	AssertStatusCode(t, portalList, http.StatusOK)
	var revealed []map[string]any
	DecodeJSON(t, portalList, &revealed)
	if len(revealed) != 1 || int(revealed[0]["id"].(float64)) != attachmentID {
		t.Fatalf("portal attachments after reveal = %+v, want attachment %d", revealed, attachmentID)
	}

	t.Run("missing is_internal is a validation error", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPatch, visibilityPath, map[string]any{})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusBadRequest)
	})

	t.Run("missing attachment is not found", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodPatch, "/v2/attachments/99999999", map[string]any{"is_internal": true})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("anonymous caller is unauthorized", func(t *testing.T) {
		resp := makeSessionRequest(t, http.MethodPatch, server.APIBase+visibilityPath, "",
			map[string]any{"is_internal": true}, map[string]string{"Content-Type": "application/merge-patch+json"})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusUnauthorized)
	})

	t.Run("bearer token without items write scope is forbidden", func(t *testing.T) {
		readOnly := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"items:read"})
		resp := MakeBearerRequestWithToken(t, server, readOnly, http.MethodPatch,
			fmt.Sprintf("/rest/api/v2/attachments/%d", attachmentID), map[string]any{"is_internal": true})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("viewer without item edit permission gets not found", func(t *testing.T) {
		viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "visibility_viewer", fmt.Sprintf("visibility-viewer-%d@example.com", time.Now().UnixNano()))
		AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
		viewerCookie, _ := CreateAuthCredentialsForUser(t, server, viewerUsername, viewerPassword)
		resp := makeSessionRequest(t, http.MethodPatch, server.APIBase+visibilityPath, viewerCookie,
			map[string]any{"is_internal": true}, map[string]string{"Content-Type": "application/merge-patch+json"})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})

	t.Run("page attachment cannot be toggled", func(t *testing.T) {
		createPage := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/pages", workspaceID), map[string]any{
			"title": "Attachment page", "content": "body",
		})
		defer createPage.Body.Close()
		AssertStatusCode(t, createPage, http.StatusCreated)
		var page map[string]any
		DecodeJSON(t, createPage, &page)
		pageID := ExtractIDFromResponse(t, page)

		pageUpload := makeBearerMultipartRequest(t, server,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/pages/%d/attachments", workspaceID, pageID), nil,
			"diagram.json", []byte(`{"elements":[],"appState":{},"files":{}}`))
		defer pageUpload.Body.Close()
		AssertStatusCode(t, pageUpload, http.StatusCreated)
		var pageAttachment struct {
			Data struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		DecodeJSON(t, pageUpload, &pageAttachment)

		resp := MakeAuthRequest(t, server, http.MethodPatch,
			fmt.Sprintf("/v2/attachments/%d", pageAttachment.Data.ID), map[string]any{"is_internal": true})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}
