package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// makePortalMultipartRequest sends a multipart form request authenticated by a
// portal customer session cookie.
func makePortalMultipartRequest(t *testing.T, server *TestServer, portalSessionCookie, path, filename string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.APIBase+path, &body)
	if err != nil {
		t.Fatalf("create multipart request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", portalSessionCookie)
	response, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("send multipart request: %v", err)
	}
	return response
}

func TestPortalRequestAttachments(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Portal Attachments Workspace", shortKey("PAT"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)

	customerID, ownerCookie := CreatePortalCustomerWithSession(t, server, channelID, "Attachment Owner", fmt.Sprintf("attach-owner-%d@example.com", time.Now().UnixNano()))
	_, otherCookie := CreatePortalCustomerWithSession(t, server, channelID, "Other Customer", fmt.Sprintf("attach-other-%d@example.com", time.Now().UnixNano()))

	itemID := SubmitPortalRequest(t, server, portalSlug, ownerCookie, "Request with attachment")
	otherItemID := SubmitPortalRequest(t, server, portalSlug, otherCookie, "Other request")

	uploadPath := fmt.Sprintf("/portal/%s/requests/%d/attachments", portalSlug, itemID)
	png := validTestPNG(t)

	t.Run("owner can upload, list, and download", func(t *testing.T) {
		upload := makePortalMultipartRequest(t, server, ownerCookie, uploadPath, "screenshot.png", png)
		defer upload.Body.Close()
		AssertStatusCode(t, upload, http.StatusCreated)

		var uploaded struct {
			Attachment struct {
				ID               int    `json:"id"`
				OriginalFilename string `json:"original_filename"`
				MimeType         string `json:"mime_type"`
				FileSize         int64  `json:"file_size"`
			} `json:"attachment"`
		}
		DecodeJSON(t, upload, &uploaded)
		if uploaded.Attachment.ID == 0 {
			t.Fatal("upload response has no attachment id")
		}
		if uploaded.Attachment.OriginalFilename != "screenshot.png" {
			t.Fatalf("original_filename = %q, want screenshot.png", uploaded.Attachment.OriginalFilename)
		}
		if uploaded.Attachment.FileSize != int64(len(png)) {
			t.Fatalf("file_size = %d, want %d", uploaded.Attachment.FileSize, len(png))
		}
		attachmentID := uploaded.Attachment.ID

		list := MakePortalRequest(t, server, ownerCookie, http.MethodGet, uploadPath, nil)
		defer list.Body.Close()
		AssertStatusCode(t, list, http.StatusOK)

		var attachments []map[string]any
		if err := json.NewDecoder(list.Body).Decode(&attachments); err != nil {
			t.Fatalf("decode attachment list: %v", err)
		}
		if len(attachments) != 1 {
			t.Fatalf("attachment list length = %d, want 1", len(attachments))
		}
		if got := int(attachments[0]["id"].(float64)); got != attachmentID {
			t.Fatalf("listed attachment id = %d, want %d", got, attachmentID)
		}

		download := MakePortalRequest(t, server, ownerCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/attachments/%d/download", portalSlug, itemID, attachmentID), nil)
		defer download.Body.Close()
		AssertStatusCode(t, download, http.StatusOK)
		got, err := io.ReadAll(download.Body)
		if err != nil {
			t.Fatalf("read download body: %v", err)
		}
		if !bytes.Equal(got, png) {
			t.Fatalf("downloaded bytes differ from uploaded content")
		}
		if ct := download.Header.Get("Content-Type"); ct != "image/png" {
			t.Fatalf("download content type = %q, want image/png", ct)
		}
	})

	t.Run("unauthenticated requests are rejected", func(t *testing.T) {
		upload := makePortalMultipartRequest(t, server, "", uploadPath, "nope.png", png)
		defer upload.Body.Close()
		AssertStatusCode(t, upload, http.StatusUnauthorized)

		list := MakePortalRequest(t, server, "", http.MethodGet, uploadPath, nil)
		defer list.Body.Close()
		AssertStatusCode(t, list, http.StatusUnauthorized)

		download := MakePortalRequest(t, server, "", http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/attachments/1/download", portalSlug, itemID), nil)
		defer download.Body.Close()
		AssertStatusCode(t, download, http.StatusUnauthorized)
	})

	t.Run("another customer gets not-found on upload, list, and download", func(t *testing.T) {
		upload := makePortalMultipartRequest(t, server, otherCookie, uploadPath, "intruder.png", png)
		defer upload.Body.Close()
		AssertStatusCode(t, upload, http.StatusNotFound)

		list := MakePortalRequest(t, server, otherCookie, http.MethodGet, uploadPath, nil)
		defer list.Body.Close()
		AssertStatusCode(t, list, http.StatusNotFound)
	})

	t.Run("attachments are bound to their request", func(t *testing.T) {
		// Upload onto the owner's request, then replay the attachment ID
		// against a second request owned by the same customer: the download
		// must 404 because the attachment belongs to a different item.
		upload := makePortalMultipartRequest(t, server, ownerCookie, uploadPath, "bound.png", png)
		defer upload.Body.Close()
		AssertStatusCode(t, upload, http.StatusCreated)

		var uploaded struct {
			Attachment struct {
				ID int `json:"id"`
			} `json:"attachment"`
		}
		DecodeJSON(t, upload, &uploaded)

		replay := MakePortalRequest(t, server, ownerCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/attachments/%d/download", portalSlug, otherItemID, uploaded.Attachment.ID), nil)
		defer replay.Body.Close()
		AssertStatusCode(t, replay, http.StatusNotFound)
	})

	t.Run("internal private comments never appear to customers", func(t *testing.T) {
		public := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", itemID), map[string]any{
			"content": "public agent reply",
		})
		defer public.Body.Close()
		AssertStatusCode(t, public, http.StatusCreated)

		private := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", itemID), map[string]any{
			"content":	"internal note",
			"is_private": true,
		})
		defer private.Body.Close()
		AssertStatusCode(t, private, http.StatusCreated)

		list := MakePortalRequest(t, server, ownerCookie, http.MethodGet,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), nil)
		defer list.Body.Close()
		AssertStatusCode(t, list, http.StatusOK)

		var comments []map[string]any
		DecodeJSON(t, list, &comments)
		if len(comments) != 1 {
			t.Fatalf("portal comment count = %d, want 1 (private comment must be excluded)", len(comments))
		}
		if got, _ := comments[0]["content"].(string); got != "public agent reply" {
			t.Fatalf("portal comment content = %q, want the public reply", got)
		}
	})

	t.Run("upload without a file part is a validation error", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.Close(); err != nil {
			t.Fatalf("close multipart body: %v", err)
		}
		req, err := http.NewRequest(http.MethodPost, server.APIBase+uploadPath, &body)
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Cookie", ownerCookie)
		response, err := testHTTPClient.Do(req)
		if err != nil {
			t.Fatalf("send request: %v", err)
		}
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusBadRequest)
	})

	_ = customerID
}

func TestPortalCustomerReplyNotifiesAssignee(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Portal Notify Workspace", shortKey("PNW"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)

	assigneeID := adminUserID(t, server)
	customerName := "Replying Customer"
	_, customerCookie := CreatePortalCustomerWithSession(t, server, channelID, customerName, fmt.Sprintf("reply-notify-%d@example.com", time.Now().UnixNano()))

	itemID := SubmitPortalRequest(t, server, portalSlug, customerCookie, "Notify me when replied")

	// Assign the admin session user so the default comment.created rule
	// (assignee + creator + watchers) has a staff recipient.
	update := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/items/%d", itemID), map[string]any{
		"assignee_id": assigneeID,
	})
	update.Body.Close()
	AssertStatusCode(t, update, http.StatusOK)

	commentResp := MakePortalRequest(t, server, customerCookie, http.MethodPost,
		fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID), map[string]any{
			"content": "Any update on this request?",
		})
	commentResp.Body.Close()
	AssertStatusCode(t, commentResp, http.StatusCreated)

	// The notification pipeline is asynchronous; poll until the assignee's
	// comment notification shows up.
	deadline := time.Now().Add(10 * time.Second)
	for {
		notificationsResp := MakeAuthRequest(t, server, http.MethodGet, "/notifications", nil)
		var notifications []map[string]any
		decodeErr := json.NewDecoder(notificationsResp.Body).Decode(&notifications)
		notificationsResp.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode notifications: %v", decodeErr)
		}

		found := false
		for _, n := range notifications {
			if n["type"] != "comment" {
				continue
			}
			message, _ := n["message"].(string)
			if strings.Contains(message, customerName) && strings.Contains(message, "New comment added by") {
				found = true
				break
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			summary := make([]string, 0, len(notifications))
			for _, n := range notifications {
				summary = append(summary, fmt.Sprintf("type=%v title=%v message=%v", n["type"], n["title"], n["message"]))
			}
			t.Fatalf("no comment notification mentioning %q arrived for the assignee within 10s; got %d notifications: %v", customerName, len(notifications), summary)
		}
		<-time.After(200 * time.Millisecond)
	}
}

// TestPortalCustomerUploadWritesAttributableHistory verifies WI-1538: an
// attachment uploaded by a portal customer on a customer-only request lands
// in item history attributed to that customer, and the attachment record
// carries the portal-customer uploader. Internal uploads keep user
// attribution.
func TestPortalCustomerUploadWritesAttributableHistory(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Portal History Workspace", shortKey("PHW"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)

	customerName := "History Customer"
	customerID, ownerCookie := CreatePortalCustomerWithSession(t, server, channelID, customerName,
		fmt.Sprintf("history-customer-%d@example.com", time.Now().UnixNano()))

	itemID := SubmitPortalRequest(t, server, portalSlug, ownerCookie, "Customer-only evidence request")

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

	// The attachment record names the portal customer as uploader.
	list := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/attachments", itemID), nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var page struct {
		Data []struct {
			ID                         int    `json:"id"`
			UploadedBy                 *int   `json:"uploaded_by"`
			UploadedByPortalCustomerID *int   `json:"uploaded_by_portal_customer_id"`
			UploaderPortalCustomerName string `json:"uploader_portal_customer_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(list.Body).Decode(&page); err != nil {
		t.Fatalf("decode attachment list: %v", err)
	}
	attachments := page.Data
	if len(attachments) != 1 {
		t.Fatalf("attachment count = %d, want 1", len(attachments))
	}
	if attachments[0].ID != uploaded.Attachment.ID {
		t.Fatalf("listed attachment id = %d, want %d", attachments[0].ID, uploaded.Attachment.ID)
	}
	if attachments[0].UploadedBy != nil {
		t.Fatalf("uploaded_by = %d, want nil for a portal upload", *attachments[0].UploadedBy)
	}
	if attachments[0].UploadedByPortalCustomerID == nil || *attachments[0].UploadedByPortalCustomerID != customerID {
		t.Fatalf("uploaded_by_portal_customer_id = %v, want %d", attachments[0].UploadedByPortalCustomerID, customerID)
	}
	if attachments[0].UploaderPortalCustomerName != customerName {
		t.Fatalf("uploader_portal_customer_name = %q, want %q", attachments[0].UploaderPortalCustomerName, customerName)
	}

	// The upload survives in item history attributed to the customer, even
	// though no internal user ever touched the request.
	history := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/history", itemID), nil)
	defer history.Body.Close()
	AssertStatusCode(t, history, http.StatusOK)

	var historyDoc struct {
		Data []struct {
			FieldName          string `json:"field_name"`
			UserID             int    `json:"user_id"`
			ActorKind          string `json:"actor_kind"`
			PortalCustomerID   *int   `json:"portal_customer_id"`
			PortalCustomerName string `json:"portal_customer_name"`
			NewValue           string `json:"new_value"`
		} `json:"data"`
	}
	if err := json.NewDecoder(history.Body).Decode(&historyDoc); err != nil {
		t.Fatalf("decode item history: %v", err)
	}
	entries := historyDoc.Data
	uploadEntries := 0
	for _, entry := range entries {
		if entry.FieldName != "attachment_uploaded" {
			continue
		}
		uploadEntries++
		if entry.ActorKind != "portal_customer" {
			t.Fatalf("actor_kind = %q, want portal_customer", entry.ActorKind)
		}
		if entry.UserID != 0 {
			t.Fatalf("user_id = %d, want 0 for a portal actor", entry.UserID)
		}
		if entry.PortalCustomerID == nil || *entry.PortalCustomerID != customerID {
			t.Fatalf("portal_customer_id = %v, want %d", entry.PortalCustomerID, customerID)
		}
		if entry.PortalCustomerName != customerName {
			t.Fatalf("portal_customer_name = %q, want %q", entry.PortalCustomerName, customerName)
		}
		wantValue := fmt.Sprintf("attachment:%d:evidence.png", uploaded.Attachment.ID)
		if entry.NewValue != wantValue {
			t.Fatalf("new_value = %q, want %q", entry.NewValue, wantValue)
		}
	}
	if uploadEntries != 1 {
		t.Fatalf("attachment_uploaded history entries = %d, want 1", uploadEntries)
	}
}

// TestPortalHidesInternalAttachments verifies WI-1700: a file attached to an
// agent-only (internal) source must not be listable or downloadable through
// the customer portal, while workspace members still see it.
func TestPortalHidesInternalAttachments(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Portal Internal Attachments Workspace", shortKey("PIA"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	_, ownerCookie := CreatePortalCustomerWithSession(t, server, channelID, "Internal File Owner", fmt.Sprintf("internal-file-owner-%d@example.com", time.Now().UnixNano()))

	itemID := SubmitPortalRequest(t, server, portalSlug, ownerCookie, "Request with a public and an internal file")

	uploadPath := fmt.Sprintf("/portal/%s/requests/%d/attachments", portalSlug, itemID)
	publicUpload := makePortalMultipartRequest(t, server, ownerCookie, uploadPath, "customer-evidence.png", validTestPNG(t))
	defer publicUpload.Body.Close()
	AssertStatusCode(t, publicUpload, http.StatusCreated)

	var publicUploaded struct {
		Attachment struct {
			ID int `json:"id"`
		} `json:"attachment"`
	}
	DecodeJSON(t, publicUpload, &publicUploaded)

	// Direct DB fixture: the Jira importer is the only production path that
	// creates internal attachments, and it is not reachable from this HTTP
	// test, so insert the internal row exactly as that importer would.
	var internalID int
	if err := server.DB().QueryRow(`
		INSERT INTO attachments (item_id, entity_type, filename, original_filename, file_path, mime_type, file_size, is_internal)
		VALUES (?, 'item', 'internal.png', 'agent-note.png', '/tmp/internal.png', 'image/png', 1, true)
		RETURNING id
	`, itemID).Scan(&internalID); err != nil {
		t.Fatalf("insert internal attachment fixture: %v", err)
	}

	list := MakePortalRequest(t, server, ownerCookie, http.MethodGet, uploadPath, nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var listed []map[string]any
	DecodeJSON(t, list, &listed)
	if len(listed) != 1 {
		t.Fatalf("portal attachment count = %d, want 1 (internal file must be hidden)", len(listed))
	}
	if got := int(listed[0]["id"].(float64)); got != publicUploaded.Attachment.ID {
		t.Fatalf("portal listed attachment id = %d, want public %d", got, publicUploaded.Attachment.ID)
	}

	denied := MakePortalRequest(t, server, ownerCookie, http.MethodGet,
		fmt.Sprintf("/portal/%s/requests/%d/attachments/%d/download", portalSlug, itemID, internalID), nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusNotFound)

	memberList := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/items/%d/attachments", itemID), nil)
	defer memberList.Body.Close()
	AssertStatusCode(t, memberList, http.StatusOK)
	var memberPage struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(memberList.Body).Decode(&memberPage); err != nil {
		t.Fatalf("decode member attachment list: %v", err)
	}
	found := false
	for _, attachment := range memberPage.Data {
		if attachment.ID == internalID {
			found = true
		}
	}
	if !found {
		t.Fatalf("internal attachment %d missing from the workspace list %v", internalID, memberPage.Data)
	}
}
