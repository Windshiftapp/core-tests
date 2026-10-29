package tests

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"windshift/internal/logger"
)

// decodableTestPNG returns a real PNG that the thumbnail generator can decode.
// validTestPNG only carries a valid header, so it never produces a thumbnail.
func decodableTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	img.Set(2, 2, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return encoded.Bytes()
}

func makeBearerMultipartRequest(t *testing.T, server *TestServer, path string, fields map[string]string, filename string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write multipart field: %v", err)
		}
	}
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
	req, err := http.NewRequest(http.MethodPost, server.BaseURL+path, &body)
	if err != nil {
		t.Fatalf("create multipart request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+server.BearerToken)
	response, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("send multipart request: %v", err)
	}
	return response
}

func TestItemAttachmentLifecycleConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Attachment convergence", shortKey("ATC"))
	itemID := CreateTestItem(t, server, workspaceID, "Attachment item")
	fileData := validTestPNG(t)

	response := makeBearerMultipartRequest(t, server,
		fmt.Sprintf("/rest/api/v2/items/%d/attachments", itemID), nil, "shared.png", fileData)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusCreated)
	var upload struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, response, &upload)

	download := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/attachments/%d/content", upload.Data.ID), nil)
	defer download.Body.Close()
	AssertStatusCode(t, download, http.StatusOK)
	sessionBytes, err := io.ReadAll(download.Body)
	if err != nil || !bytes.Equal(sessionBytes, fileData) {
		t.Fatalf("session download = %q, err=%v", sessionBytes, err)
	}

	bearerDownload := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/attachments/%d/content", upload.Data.ID), nil)
	defer bearerDownload.Body.Close()
	AssertStatusCode(t, bearerDownload, http.StatusOK)
	bearerBytes, err := io.ReadAll(bearerDownload.Body)
	if err != nil || !bytes.Equal(bearerBytes, fileData) {
		t.Fatalf("bearer download = %q, err=%v", bearerBytes, err)
	}

	sessionThumbnail := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/attachments/%d/thumbnail", upload.Data.ID), nil)
	defer sessionThumbnail.Body.Close()
	bearerThumbnail := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/attachments/%d/thumbnail", upload.Data.ID), nil)
	defer bearerThumbnail.Body.Close()
	if sessionThumbnail.StatusCode != bearerThumbnail.StatusCode {
		t.Fatalf("thumbnail status: session=%d bearer=%d", sessionThumbnail.StatusCode, bearerThumbnail.StatusCode)
	}

	remove := MakeAuthRequest(t, server, http.MethodDelete,
		fmt.Sprintf("/v2/attachments/%d", upload.Data.ID), nil)
	defer remove.Body.Close()
	AssertStatusCode(t, remove, http.StatusNoContent)

	// WI-1893: the v2 delete path must emit the same attachment.delete audit
	// row as the legacy handler.
	var auditCount int
	if err := server.server.DB().QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE action_type = ? AND resource_id = ?`,
		logger.ActionAttachmentDelete, upload.Data.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query attachment delete audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("attachment delete audit rows = %d, want 1", auditCount)
	}

	deleted := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/attachments/%d/content", upload.Data.ID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNotFound)
}

func TestPageAttachmentBinaryEndpointsServeUploadedImage(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Page attachment binaries", shortKey("PAB"))
	createPage := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/pages", workspaceID), map[string]any{"title": "Image page", "content": "body"})
	defer createPage.Body.Close()
	AssertStatusCode(t, createPage, http.StatusCreated)
	var page map[string]any
	DecodeJSON(t, createPage, &page)
	pageID := ExtractIDFromResponse(t, page)

	fileData := decodableTestPNG(t)
	upload := makeBearerMultipartRequest(t, server,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/pages/%d/attachments", workspaceID, pageID), nil,
		"photo.png", fileData)
	defer upload.Body.Close()
	AssertStatusCode(t, upload, http.StatusCreated)
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, upload, &created)
	if created.Data.ID == 0 {
		t.Fatalf("upload response missing attachment id")
	}

	// The shared binary endpoints must serve page-scoped rows, not just item
	// attachments. Before the fix they returned 404 because OpenItemAttachment
	// only resolves entity_type='item' rows.
	content := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/attachments/%d/content", created.Data.ID), nil)
	defer content.Body.Close()
	AssertStatusCode(t, content, http.StatusOK)
	contentBytes, err := io.ReadAll(content.Body)
	if err != nil || !bytes.Equal(contentBytes, fileData) {
		t.Fatalf("page attachment content = %q, err=%v", contentBytes, err)
	}

	download := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/attachments/%d/download", created.Data.ID), nil)
	defer download.Body.Close()
	AssertStatusCode(t, download, http.StatusOK)

	thumbnail := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/attachments/%d/thumbnail", created.Data.ID), nil)
	defer thumbnail.Body.Close()
	AssertStatusCode(t, thumbnail, http.StatusOK)
	thumbnailBytes, err := io.ReadAll(thumbnail.Body)
	if err != nil || len(thumbnailBytes) == 0 {
		t.Fatalf("page attachment thumbnail empty, err=%v", err)
	}

	// The shared binary route accepts either the item or the page read scope so
	// a page-scoped token can fetch a page attachment binary. Before the fix the
	// route required items:read and rejected a pages:read token with 403.
	pageScoped := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"pages:read"})
	pageContent := MakeBearerRequestWithToken(t, server, pageScoped, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/attachments/%d/content", created.Data.ID), nil)
	defer pageContent.Body.Close()
	AssertStatusCode(t, pageContent, http.StatusOK)

	itemScoped := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"items:read"})
	itemContent := MakeBearerRequestWithToken(t, server, itemScoped, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/attachments/%d/content", created.Data.ID), nil)
	defer itemContent.Body.Close()
	AssertStatusCode(t, itemContent, http.StatusOK)

	unrelated := createTokenWithScopesAsUser(t, server, "admin", "testpass123", []string{"assets:read"})
	forbidden := MakeBearerRequestWithToken(t, server, unrelated, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/attachments/%d/content", created.Data.ID), nil)
	defer forbidden.Body.Close()
	AssertStatusCode(t, forbidden, http.StatusForbidden)

	// A user outside the workspace must not be able to read the page image. The
	// page is restricted first so the open-workspace "everyone" grant does not
	// mask the denial.
	restrict := MakeAuthRequest(t, server, http.MethodPatch,
		fmt.Sprintf("/workspaces/%d/pages/%d/inheritance", workspaceID, pageID),
		map[string]any{"inherit_permissions": false})
	defer restrict.Body.Close()
	AssertStatusCode(t, restrict, http.StatusOK)

	_, strangerName, strangerPassword := CreateTestUserWithCredentials(t, server,
		"page-attachment-stranger", "page-attachment-stranger@example.com")
	_, strangerToken := CreateAuthCredentialsForUser(t, server, strangerName, strangerPassword)
	denied := MakeBearerRequestWithToken(t, server, strangerToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/attachments/%d/content", created.Data.ID), nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusNotFound)
	var deniedBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	DecodeJSON(t, denied, &deniedBody)
	if deniedBody.Error.Code != "not_found" || deniedBody.Error.Message != "Attachment was not found" {
		t.Fatalf("denial body = %+v, want not_found/Attachment was not found", deniedBody.Error)
	}
}

func TestPageAttachmentUploadsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Page attachment convergence", shortKey("PAC"))
	createPage := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/pages", workspaceID), map[string]any{"title": "Attachment page", "content": "body"})
	defer createPage.Body.Close()
	AssertStatusCode(t, createPage, http.StatusCreated)
	var page map[string]any
	DecodeJSON(t, createPage, &page)
	pageID := ExtractIDFromResponse(t, page)

	upload := makeBearerMultipartRequest(t, server,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/pages/%d/attachments", workspaceID, pageID), nil,
		"diagram.json", []byte(`{"elements":[],"appState":{},"files":{}}`))
	defer upload.Body.Close()
	AssertStatusCode(t, upload, http.StatusCreated)
	var result struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, upload, &result)

	download := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/attachments/%d/download", result.Data.ID), nil)
	defer download.Body.Close()
	AssertStatusCode(t, download, http.StatusOK)
}
