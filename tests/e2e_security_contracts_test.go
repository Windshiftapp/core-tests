package tests

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"windshift/internal/services"
)

// These tests are the HTTP-layer home for authorization and data-isolation
// contracts that used to be implemented as request-only Playwright specs.
// They deliberately do not use a browser: the contract is the authenticated
// HTTP response and its observable side effects, not a particular frontend
// implementation.

type uploadedAttachment struct {
	ID    int
	Bytes []byte
}

func newSecurityTestServer(t *testing.T) *TestServer {
	t.Helper()

	server, cleanup := StartTestServer(t, GetDBType())
	t.Cleanup(cleanup)
	CreateBearerToken(t, server)
	return server
}

func validTestPNG(t *testing.T) []byte {
	t.Helper()
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGL+/x8AAQUBAQ0KLbQAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatalf("decode test PNG: %v", err)
	}
	return png
}

func makeMultipartSessionRequest(
	t *testing.T,
	server *TestServer,
	method string,
	endpoint string,
	fields map[string]string,
	filename string,
	mimeType string,
	fileBytes []byte,
	sessionCookie string,
) *http.Response {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write multipart field %q: %v", name, err)
		}
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	partHeader.Set("Content-Type", mimeType)
	file, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write(fileBytes); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(method, server.APIBase+endpoint, &body)
	if err != nil {
		t.Fatalf("create multipart request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if sessionCookie != "" {
		req.Header.Set("Cookie", sessionCookie)
	}

	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("send multipart request: %v", err)
	}
	return resp
}

func uploadItemAttachment(t *testing.T, server *TestServer, itemID int, suffix string) uploadedAttachment {
	t.Helper()

	fileBytes := []byte(fmt.Sprintf("attachment-%s\n", suffix))
	resp := makeMultipartSessionRequest(
		t,
		server,
		http.MethodPost,
		"/attachments/upload",
		map[string]string{"item_id": fmt.Sprint(itemID)},
		"attachment-"+suffix+".txt",
		"text/plain",
		fileBytes,
		server.SessionCookie,
	)
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload attachment: got %d body=%s", resp.StatusCode, body)
	}

	var payload struct {
		ID           int `json:"id"`
		AttachmentID int `json:"attachment_id"`
		Attachment   *struct {
			ID int `json:"id"`
		} `json:"attachment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode attachment upload response: %v", err)
	}
	attachmentID := payload.ID
	if attachmentID == 0 {
		attachmentID = payload.AttachmentID
	}
	if attachmentID == 0 && payload.Attachment != nil {
		attachmentID = payload.Attachment.ID
	}
	if attachmentID == 0 {
		t.Fatal("attachment upload response did not contain an attachment id")
	}

	return uploadedAttachment{ID: attachmentID, Bytes: fileBytes}
}

func assertResponseStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if resp.StatusCode != want {
		t.Fatalf("expected HTTP %d, got %d body=%s", want, resp.StatusCode, body)
	}
}

func assertResponseStatusForBody(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected HTTP %d, got %d body=%s", want, resp.StatusCode, body)
	}
}

func responseBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return body
}

func TestAPITokenAuthenticationHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)

	createResponse := MakeAuthRequest(t, server, http.MethodPost, "/api-tokens", map[string]interface{}{
		"name":        "HTTP authentication contract",
		"permissions": []string{"workspaces:read"},
	})
	if createResponse.StatusCode != http.StatusOK && createResponse.StatusCode != http.StatusCreated {
		body := responseBody(t, createResponse)
		t.Fatalf("create API token: expected 200 or 201, got %d body=%s", createResponse.StatusCode, body)
	}
	var tokenPayload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(createResponse.Body).Decode(&tokenPayload); err != nil {
		createResponse.Body.Close()
		t.Fatalf("decode API token response: %v", err)
	}
	createResponse.Body.Close()
	if tokenPayload.Token == "" {
		t.Fatal("create API token response did not contain a token")
	}
	if !bytes.HasPrefix([]byte(tokenPayload.Token), []byte("crw_")) {
		t.Fatalf("API token has unexpected prefix: %q", tokenPayload.Token)
	}

	v1Response := MakeBearerRequestWithToken(t, server, tokenPayload.Token, http.MethodGet,
		"/rest/api/v1/workspaces", nil)
	assertResponseStatus(t, v1Response, http.StatusOK)

	legacyResponse := MakeBearerRequestWithToken(t, server, tokenPayload.Token, http.MethodGet,
		"/api/workspaces", nil)
	if legacyResponse.StatusCode != http.StatusUnauthorized {
		body := responseBody(t, legacyResponse)
		t.Fatalf("legacy bearer request: expected 401, got %d body=%s", legacyResponse.StatusCode, body)
	}
	if body := responseBody(t, legacyResponse); !bytes.Contains(body, []byte("/rest/api/v1")) {
		t.Fatalf("legacy bearer rejection did not mention /rest/api/v1: %s", body)
	}
}

func TestAPITokenExpiryDateHTTPContract(t *testing.T) {
	server := newSecurityTestServer(t)

	t.Run("uses UTC by default", func(t *testing.T) {
		response := MakeAuthRequest(t, server, http.MethodPost, "/api-tokens", map[string]interface{}{
			"name":        "Calendar expiry",
			"permissions": []string{"workspaces:read"},
			"expires_on":  "2030-06-15",
		})
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
			body := responseBody(t, response)
			t.Fatalf("create API token: expected 200 or 201, got %d body=%s", response.StatusCode, body)
		}

		var payload struct {
			APIToken struct {
				ExpiresAt string `json:"expires_at"`
			} `json:"api_token"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatalf("decode API token response: %v", err)
		}
		response.Body.Close()
		if payload.APIToken.ExpiresAt != "2030-06-16T00:00:00Z" {
			t.Fatalf("expires_at = %q, want start of next UTC date", payload.APIToken.ExpiresAt)
		}
	})

	t.Run("uses the caller's configured timezone", func(t *testing.T) {
		update := MakeAuthRequest(t, server, http.MethodPut, "/users/1/regional-settings", map[string]interface{}{
			"timezone": "Europe/Zurich",
			"language": "en",
		})
		if update.StatusCode != http.StatusOK {
			body := responseBody(t, update)
			t.Fatalf("set caller timezone: expected 200, got %d body=%s", update.StatusCode, body)
		}
		update.Body.Close()

		response := MakeAuthRequest(t, server, http.MethodPost, "/api-tokens", map[string]interface{}{
			"name":        "Zurich calendar expiry",
			"permissions": []string{"workspaces:read"},
			"expires_on":  "2030-03-31",
		})
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
			body := responseBody(t, response)
			t.Fatalf("create API token: expected 200 or 201, got %d body=%s", response.StatusCode, body)
		}

		var payload struct {
			APIToken struct {
				ExpiresAt string `json:"expires_at"`
			} `json:"api_token"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatalf("decode API token response: %v", err)
		}
		response.Body.Close()
		if payload.APIToken.ExpiresAt != "2030-03-31T22:00:00Z" {
			t.Fatalf("expires_at = %q, want start of next Europe/Zurich date in UTC", payload.APIToken.ExpiresAt)
		}
	})

	t.Run("falls back to UTC for an invalid stored timezone", func(t *testing.T) {
		// The settings API intentionally rejects this value. Write it directly to
		// reproduce legacy or corrupted stored data at the defensive read boundary.
		if _, err := server.server.DB().ExecWrite(`UPDATE users SET timezone = 'Not/AZone' WHERE id = 1`); err != nil {
			t.Fatalf("seed invalid stored timezone: %v", err)
		}

		response := MakeAuthRequest(t, server, http.MethodPost, "/api-tokens", map[string]interface{}{
			"name": "Legacy timezone expiry", "permissions": []string{"workspaces:read"}, "expires_on": "2030-06-15",
		})
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
			body := responseBody(t, response)
			t.Fatalf("create token with invalid stored timezone: status=%d body=%s", response.StatusCode, body)
		}
		var payload struct {
			APIToken struct {
				ExpiresAt string `json:"expires_at"`
			} `json:"api_token"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatalf("decode token response: %v", err)
		}
		response.Body.Close()
		if payload.APIToken.ExpiresAt != "2030-06-16T00:00:00Z" {
			t.Fatalf("expires_at = %q, want UTC fallback", payload.APIToken.ExpiresAt)
		}
	})

	t.Run("accepts null as no expiration", func(t *testing.T) {
		response := MakeAuthRequest(t, server, http.MethodPost, "/api-tokens", map[string]interface{}{
			"name":        "No expiry",
			"permissions": []string{"workspaces:read"},
			"expires_on":  nil,
		})
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
			body := responseBody(t, response)
			t.Fatalf("create API token: expected 200 or 201, got %d body=%s", response.StatusCode, body)
		}

		var payload struct {
			APIToken struct {
				ExpiresAt *string `json:"expires_at"`
			} `json:"api_token"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatalf("decode API token response: %v", err)
		}
		response.Body.Close()
		if payload.APIToken.ExpiresAt != nil {
			t.Fatalf("expires_at = %q, want no expiration", *payload.APIToken.ExpiresAt)
		}
	})

	for _, test := range []struct {
		name      string
		expiresOn string
	}{
		{name: "empty", expiresOn: ""},
		{name: "whitespace", expiresOn: "   "},
		{name: "timestamp", expiresOn: "2030-06-15T12:00:00Z"},
		{name: "invalid date", expiresOn: "2030-02-30"},
	} {
		t.Run("rejects "+test.name, func(t *testing.T) {
			response := MakeAuthRequest(t, server, http.MethodPost, "/api-tokens", map[string]interface{}{
				"name":        "Invalid calendar expiry",
				"permissions": []string{"workspaces:read"},
				"expires_on":  test.expiresOn,
			})
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("expected 400, got %d body=%s", response.StatusCode, body)
			}
			var payload struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode validation response: %v", err)
			}
			if payload.Code != "VALIDATION_FAILED" || payload.Error != "expires_on must use YYYY-MM-DD format" {
				t.Fatalf("validation response = %+v", payload)
			}
		})
	}

	t.Run("rejects the old timestamp field", func(t *testing.T) {
		response := MakeAuthRequest(t, server, http.MethodPost, "/api-tokens", map[string]interface{}{
			"name":        "Old timestamp expiry",
			"permissions": []string{"workspaces:read"},
			"expires_at":  "2030-06-15T12:00:00Z",
		})
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("expected 400, got %d body=%s", response.StatusCode, body)
		}
		var payload struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatalf("decode validation response: %v", err)
		}
		if payload.Code != "VALIDATION_FAILED" || payload.Error != "expires_at is not accepted; use expires_on in YYYY-MM-DD format" {
			t.Fatalf("validation response = %+v", payload)
		}
	})
}

func TestMarkdownSourceAndRenderHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)
	workspaceID, _ := CreateTestWorkspace(t, server, "Rich text contract", shortKey("RTX"))
	itemID := CreateTestItem(t, server, workspaceID, "Rich text target")

	t.Run("comment_preserves_source_and_returns_safe_html", func(t *testing.T) {
		content := "Hello <script>alert(\"xss\")</script> world\n\n" +
			"iframe attempt: <iframe src=\"evil.example\"></iframe>\n\n" +
			"dangerous link [click me](javascript:alert(1))\n\n" +
			"image XSS ![alt](data:text/html,<script>alert(1)</script>)\n\n" +
			"safe link [docs](https://example.com)"
		payload := map[string]interface{}{
			"content":    content,
			"is_private": false,
		}
		createResponse := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/items/%d/comments", itemID), payload)
		if createResponse.StatusCode != http.StatusCreated && createResponse.StatusCode != http.StatusOK {
			body := responseBody(t, createResponse)
			t.Fatalf("create comment: expected 200 or 201, got %d body=%s", createResponse.StatusCode, body)
		}
		var created map[string]interface{}
		DecodeJSON(t, createResponse, &created)
		createResponse.Body.Close()
		commentID := ExtractIDFromResponse(t, created)

		listResponse := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/items/%d/comments", itemID), nil)
		AssertStatusCode(t, listResponse, http.StatusOK)
		var listed struct {
			Comments []struct {
				ID          int    `json:"id"`
				Content     string `json:"content"`
				ContentHTML string `json:"content_html"`
			} `json:"comments"`
		}
		DecodeJSON(t, listResponse, &listed)
		listResponse.Body.Close()

		var stored, rendered string
		for _, comment := range listed.Comments {
			if comment.ID == commentID {
				stored = comment.Content
				rendered = comment.ContentHTML
				break
			}
		}
		if stored == "" {
			t.Fatalf("created comment %d missing from comment feed", commentID)
		}
		if stored != content {
			t.Fatalf("comment source = %q, want exact %q", stored, content)
		}
		for _, unsafe := range [][]byte{
			[]byte("<script>"), []byte("<iframe"), []byte("javascript:"), []byte("data:text/html"),
		} {
			if bytes.Contains(bytes.ToLower([]byte(rendered)), bytes.ToLower(unsafe)) {
				t.Fatalf("comment content_html contains %q: %q", unsafe, rendered)
			}
		}
		for _, expected := range []string{
			"&lt;script&gt;", "&lt;iframe", "<a href=\"https://example.com\"",
		} {
			if !bytes.Contains([]byte(rendered), []byte(expected)) {
				t.Fatalf("comment content_html missing %q: %q", expected, rendered)
			}
		}
	})

	t.Run("description_preserves_source_and_returns_safe_html", func(t *testing.T) {
		description := "first line<br />second line\n\n" +
			"tag attempt: <b>bold</b> and <p>para</p>\n\n" +
			"script attempt: <script>alert(1)</script>\n\n" +
			"safe link [docs](https://example.com)\n\n" +
			"bad link [click](javascript:alert(1))"
		updateResponse := MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/items/%d", itemID), map[string]interface{}{"description": description})
		AssertStatusCode(t, updateResponse, http.StatusOK)
		updateResponse.Body.Close()

		getResponse := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/items/%d", itemID), nil)
		AssertStatusCode(t, getResponse, http.StatusOK)
		var item map[string]interface{}
		DecodeJSON(t, getResponse, &item)
		getResponse.Body.Close()
		stored, _ := item["description"].(string)
		rendered, _ := item["description_html"].(string)
		if stored != description {
			t.Fatalf("description source = %q, want exact %q", stored, description)
		}
		for _, forbidden := range []string{"<script", "<b>", "</b>", "javascript:"} {
			if bytes.Contains([]byte(rendered), []byte(forbidden)) {
				t.Fatalf("description_html contains %q: %q", forbidden, rendered)
			}
		}
		for _, expected := range []string{
			"<br>", "&lt;script&gt;", "&lt;b&gt;", "<a href=\"https://example.com\"",
		} {
			if !bytes.Contains([]byte(rendered), []byte(expected)) {
				t.Fatalf("description_html missing %q: %q", expected, rendered)
			}
		}
	})
}

func TestActionEditorPayloadHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)
	workspaceID, _ := CreateTestWorkspace(t, server, "Action editor contract", shortKey("AEC"))

	var statuses []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	statusResponse := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/workspaces/%d/statuses", workspaceID), nil)
	AssertStatusCode(t, statusResponse, http.StatusOK)
	DecodeJSON(t, statusResponse, &statuses)
	statusResponse.Body.Close()
	if len(statuses) == 0 {
		t.Fatal("workspace has no statuses for the trigger configuration")
	}
	doneStatusID := statuses[len(statuses)-1].ID
	for _, status := range statuses {
		if bytes.Equal(bytes.ToLower([]byte(status.Name)), []byte("done")) {
			doneStatusID = status.ID
			break
		}
	}

	t.Run("status_transition_trigger_config_round_trips", func(t *testing.T) {
		createResponse := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/workspaces/%d/actions", workspaceID), map[string]interface{}{
				"name":           "Trigger node To Status",
				"description":    "Trigger node configuration contract",
				"trigger_type":   "status_transition",
				"trigger_config": "{}",
				"nodes": []map[string]interface{}{{
					"id": -1, "node_type": "trigger", "node_config": "{}",
					"position_x": 100, "position_y": 200,
				}},
				"edges": []interface{}{},
			})
		AssertStatusCode(t, createResponse, http.StatusCreated)
		var created map[string]interface{}
		DecodeJSON(t, createResponse, &created)
		createResponse.Body.Close()
		actionID := ExtractIDFromResponse(t, created)

		triggerConfig := fmt.Sprintf(`{"to_status_id":%d}`, doneStatusID)
		updateResponse := MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/workspaces/%d/actions/%d", workspaceID, actionID), map[string]interface{}{
				"name":           created["name"],
				"description":    created["description"],
				"trigger_type":   "status_transition",
				"trigger_config": triggerConfig,
				"nodes": []map[string]interface{}{{
					"id": -1, "node_type": "trigger", "node_config": triggerConfig,
					"position_x": 100, "position_y": 200,
				}},
				"edges": []interface{}{},
			})
		AssertStatusCode(t, updateResponse, http.StatusOK)
		var updated map[string]interface{}
		DecodeJSON(t, updateResponse, &updated)
		updateResponse.Body.Close()

		var storedTriggerConfig map[string]interface{}
		if err := json.Unmarshal([]byte(updated["trigger_config"].(string)), &storedTriggerConfig); err != nil {
			t.Fatalf("decode stored trigger_config: %v", err)
		}
		if storedTriggerConfig["to_status_id"] != float64(doneStatusID) {
			t.Fatalf("stored trigger_config = %v, want to_status_id %d", storedTriggerConfig, doneStatusID)
		}
		nodes, ok := updated["nodes"].([]interface{})
		if !ok || len(nodes) != 1 {
			t.Fatalf("updated nodes = %v, want one trigger node", updated["nodes"])
		}
		node, ok := nodes[0].(map[string]interface{})
		if !ok {
			t.Fatalf("updated node has unexpected shape: %v", nodes[0])
		}
		var storedNodeConfig map[string]interface{}
		if err := json.Unmarshal([]byte(node["node_config"].(string)), &storedNodeConfig); err != nil {
			t.Fatalf("decode stored node_config: %v", err)
		}
		if storedNodeConfig["to_status_id"] != float64(doneStatusID) {
			t.Fatalf("stored node_config = %v, want to_status_id %d", storedNodeConfig, doneStatusID)
		}
	})

	t.Run("editor_node_config_shapes_are_accepted", func(t *testing.T) {
		cases := []struct {
			name        string
			triggerType string
			nodeType    string
			config      map[string]interface{}
		}{
			{
				name: "set_field_display_props", triggerType: "manual", nodeType: "set_field",
				config: map[string]interface{}{
					"field_name": "description", "field_display_name": "Description",
					"value": "from editor", "value_display_name": "From editor",
				},
			},
			{
				name: "notify_user_recipient_type", triggerType: "manual", nodeType: "notify_user",
				config: map[string]interface{}{
					"recipient_type": "assignee", "recipients": []interface{}{},
					"message": "hello", "include_link": true,
				},
			},
			{
				name: "related_items_default", triggerType: "manual", nodeType: "related_items",
				config: map[string]interface{}{"relation": "descendants", "cross_workspace": false},
			},
			{
				name: "transition_item_default", triggerType: "manual", nodeType: "transition_item",
				config: map[string]interface{}{
					"target":                   map[string]interface{}{"mode": "matching_terminal"},
					"skip_if_already_matching": true,
				},
			},
			{
				name: "round_robin_runtime_config", triggerType: "manual", nodeType: "round_robin_assign",
				config: map[string]interface{}{
					"team_id": 1, "skip_on_leave_members": true, "use_leave_substitutes": true,
				},
			},
			{
				name: "create_milestone_scm_template_config", triggerType: "scm_tag_created", nodeType: "create_milestone",
				config: map[string]interface{}{
					"name_template": "Release {{ref.short}}", "upsert_key_template": "{{ref.short}}",
					"status_on_branch": "planning", "status_on_tag": "in-progress",
					"attach_release_on_tag": true, "attach_commit_issues": true,
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				nodeConfig, err := json.Marshal(tc.config)
				if err != nil {
					t.Fatalf("marshal node config: %v", err)
				}
				triggerConfig := "{}"
				if tc.triggerType == "manual" {
					triggerConfig = `{"respond_to_cascades":true}`
				}
				response := MakeAuthRequest(t, server, http.MethodPost,
					fmt.Sprintf("/workspaces/%d/actions", workspaceID), map[string]interface{}{
						"name": fmt.Sprintf("%s action", tc.name), "trigger_type": tc.triggerType,
						"trigger_config": triggerConfig,
						"nodes": []map[string]interface{}{
							{"id": -1, "node_type": "trigger", "node_config": "{}", "position_x": 0, "position_y": 0},
							{"id": -2, "node_type": tc.nodeType, "node_config": string(nodeConfig), "position_x": 200, "position_y": 0},
						},
						"edges": []map[string]interface{}{{"source_node_id": -1, "target_node_id": -2, "edge_type": "default"}},
					})
				AssertStatusCode(t, response, http.StatusCreated)
				response.Body.Close()
			})
		}
	})
}

func TestItemTransitionAuthorizationHTTPContracts(t *testing.T) {
	t.Run("unknown_status_is_rejected_without_mutation", func(t *testing.T) {
		server := newSecurityTestServer(t)
		workspaceID, _ := CreateTestWorkspace(t, server, "Transition validation", shortKey("TRV"))
		itemID := CreateTestItem(t, server, workspaceID, "Transition target")

		beforeResponse := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/items/%d", itemID), nil)
		AssertStatusCode(t, beforeResponse, http.StatusOK)
		var before map[string]interface{}
		DecodeJSON(t, beforeResponse, &before)
		beforeResponse.Body.Close()

		response := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/items/%d/transition", itemID), map[string]interface{}{
				"to_status_id": 2_147_483_647,
			})
		if response.StatusCode < http.StatusBadRequest || response.StatusCode >= http.StatusInternalServerError {
			body := responseBody(t, response)
			t.Fatalf("unknown status transition: expected 4xx, got %d body=%s", response.StatusCode, body)
		}
		response.Body.Close()

		afterResponse := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/items/%d", itemID), nil)
		AssertStatusCode(t, afterResponse, http.StatusOK)
		var after map[string]interface{}
		DecodeJSON(t, afterResponse, &after)
		afterResponse.Body.Close()
		if after["status_id"] != before["status_id"] {
			t.Fatalf("rejected transition mutated status from %v to %v", before["status_id"], after["status_id"])
		}
	})

	t.Run("viewer_cannot_transition_item", func(t *testing.T) {
		server := newSecurityTestServer(t)
		workspaceID, _ := CreateTestWorkspace(t, server, "Transition permissions", shortKey("TRP"))
		LockDownWorkspace(t, server, workspaceID)
		itemID := CreateTestItem(t, server, workspaceID, "Protected transition target")

		viewerID, username, password := CreateTestUserWithCredentials(t, server,
			"transition_viewer", "transition_viewer@test.com")
		AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
		viewerSession := CreateBearerTokenForUser(t, server, username, password)

		beforeResponse := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/items/%d", itemID), nil)
		AssertStatusCode(t, beforeResponse, http.StatusOK)
		var before map[string]interface{}
		DecodeJSON(t, beforeResponse, &before)
		beforeResponse.Body.Close()

		response := MakeAuthRequestWithToken(t, server, viewerSession, http.MethodPost,
			fmt.Sprintf("/items/%d/transition", itemID), map[string]interface{}{
				"to_status_id": 2,
			})
		AssertStatusCode(t, response, http.StatusNotFound)
		response.Body.Close()

		afterResponse := MakeAuthRequest(t, server, http.MethodGet,
			fmt.Sprintf("/items/%d", itemID), nil)
		AssertStatusCode(t, afterResponse, http.StatusOK)
		var after map[string]interface{}
		DecodeJSON(t, afterResponse, &after)
		afterResponse.Body.Close()
		if after["status_id"] != before["status_id"] {
			t.Fatalf("forbidden transition mutated status from %v to %v", before["status_id"], after["status_id"])
		}
	})
}

type createStatusOverrideFixture struct {
	server          *TestServer
	workspaceID     int
	configSetID     int
	workflowID      int
	triageID        int
	readyID         int
	blockedID       int
	triageReadyID   int
	itemTypeConfigs []map[string]interface{}
}

func newCreateStatusOverrideFixture(t *testing.T) *createStatusOverrideFixture {
	t.Helper()
	server := newSecurityTestServer(t)
	fixture := &createStatusOverrideFixture{server: server}

	categoryResponse := MakeAuthRequest(t, server, http.MethodGet, "/status-categories", nil)
	AssertStatusCode(t, categoryResponse, http.StatusOK)
	var categories []map[string]interface{}
	DecodeJSON(t, categoryResponse, &categories)
	categoryResponse.Body.Close()
	if len(categories) == 0 {
		t.Fatal("no status categories seeded")
	}
	categoryID := int(categories[0]["id"].(float64))

	createStatus := func(name string) int {
		t.Helper()
		response := MakeAuthRequest(t, server, http.MethodPost, "/statuses", map[string]interface{}{
			"name": name, "category_id": categoryID,
		})
		AssertStatusCode(t, response, http.StatusCreated)
		var status map[string]interface{}
		DecodeJSON(t, response, &status)
		response.Body.Close()
		return ExtractIDFromResponse(t, status)
	}
	fixture.triageID = createStatus("Create override Triage")
	fixture.readyID = createStatus("Create override Ready")
	fixture.blockedID = createStatus("Create override Blocked")

	workflowResponse := MakeAuthRequest(t, server, http.MethodPost, "/workflows", map[string]interface{}{
		"name": "Create override workflow", "description": "create-status contract",
	})
	AssertStatusCode(t, workflowResponse, http.StatusCreated)
	var workflow map[string]interface{}
	DecodeJSON(t, workflowResponse, &workflow)
	workflowResponse.Body.Close()
	fixture.workflowID = ExtractIDFromResponse(t, workflow)

	transitionResponse := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/workflows/%d/transitions", fixture.workflowID), []map[string]interface{}{
			{"from_status_id": nil, "to_status_id": fixture.triageID},
			{"from_status_id": fixture.triageID, "to_status_id": fixture.readyID},
			{"from_status_id": fixture.readyID, "to_status_id": fixture.blockedID},
		})
	AssertStatusCode(t, transitionResponse, http.StatusOK)
	var transitions []map[string]interface{}
	DecodeJSON(t, transitionResponse, &transitions)
	transitionResponse.Body.Close()
	for _, transition := range transitions {
		from, fromSet := transition["from_status_id"].(float64)
		to, toSet := transition["to_status_id"].(float64)
		if fromSet && int(from) == fixture.triageID && toSet && int(to) == fixture.readyID {
			fixture.triageReadyID = int(transition["id"].(float64))
		}
	}
	if fixture.triageReadyID == 0 {
		t.Fatal("workflow did not return the Triage→Ready transition")
	}

	workspaceID, _ := CreateTestWorkspace(t, server, "Create override workspace", shortKey("CSO"))
	fixture.workspaceID = workspaceID
	itemTypesResponse := MakeAuthRequest(t, server, http.MethodGet, "/item-types", nil)
	AssertStatusCode(t, itemTypesResponse, http.StatusOK)
	var itemTypes []map[string]interface{}
	DecodeJSON(t, itemTypesResponse, &itemTypes)
	itemTypesResponse.Body.Close()
	for _, itemType := range itemTypes {
		fixture.itemTypeConfigs = append(fixture.itemTypeConfigs, map[string]interface{}{
			"item_type_id": ExtractIDFromResponse(t, itemType),
		})
	}
	if len(fixture.itemTypeConfigs) == 0 {
		t.Fatal("no item types seeded")
	}

	configResponse := MakeAuthRequest(t, server, http.MethodPost, "/configuration-sets", fixture.configPayload(nil))
	AssertStatusCode(t, configResponse, http.StatusCreated)
	var configSet map[string]interface{}
	DecodeJSON(t, configResponse, &configSet)
	configResponse.Body.Close()
	fixture.configSetID = ExtractIDFromResponse(t, configSet)
	t.Cleanup(func() {
		response := MakeAuthRequest(t, server, http.MethodDelete,
			fmt.Sprintf("/configuration-sets/%d", fixture.configSetID), nil)
		response.Body.Close()
	})
	return fixture
}

func (f *createStatusOverrideFixture) configPayload(conditionSetID *int) map[string]interface{} {
	payload := map[string]interface{}{
		"name":              "Create override configuration",
		"description":       "create-status contract",
		"workflow_id":       f.workflowID,
		"workspace_ids":     []int{f.workspaceID},
		"item_type_configs": f.itemTypeConfigs,
	}
	if conditionSetID != nil {
		payload["condition_set_id"] = *conditionSetID
	}
	return payload
}

func createStatusOverrideItem(t *testing.T, fixture *createStatusOverrideFixture, title string, statusID *int) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{"workspace_id": fixture.workspaceID, "title": title}
	if statusID != nil {
		body["status_id"] = *statusID
	}
	response := MakeAuthRequest(t, fixture.server, http.MethodPost, "/items", body)
	AssertStatusCode(t, response, http.StatusCreated)
	var item map[string]interface{}
	DecodeJSON(t, response, &item)
	response.Body.Close()
	return item
}

func TestCreateStatusOverrideHTTPContracts(t *testing.T) {
	fixture := newCreateStatusOverrideFixture(t)

	t.Run("omitted_status_uses_workflow_initial", func(t *testing.T) {
		item := createStatusOverrideItem(t, fixture, "default status", nil)
		if item["status_id"] != float64(fixture.triageID) {
			t.Fatalf("omitted status_id = %v, want initial status %d", item["status_id"], fixture.triageID)
		}
	})

	t.Run("initial_status_override_is_accepted", func(t *testing.T) {
		item := createStatusOverrideItem(t, fixture, "explicit initial", &fixture.triageID)
		if item["status_id"] != float64(fixture.triageID) {
			t.Fatalf("explicit initial status_id = %v, want %d", item["status_id"], fixture.triageID)
		}
	})

	t.Run("directly_reachable_status_override_is_accepted", func(t *testing.T) {
		item := createStatusOverrideItem(t, fixture, "quick add ready", &fixture.readyID)
		if item["status_id"] != float64(fixture.readyID) {
			t.Fatalf("reachable status_id = %v, want %d", item["status_id"], fixture.readyID)
		}
	})

	t.Run("unreachable_status_is_rejected_without_persistence", func(t *testing.T) {
		title := "unreachable blocked"
		response := MakeAuthRequest(t, fixture.server, http.MethodPost, "/items", map[string]interface{}{
			"workspace_id": fixture.workspaceID, "title": title, "status_id": fixture.blockedID,
		})
		AssertStatusCode(t, response, http.StatusBadRequest)
		var payload map[string]interface{}
		DecodeJSON(t, response, &payload)
		response.Body.Close()
		if !strings.Contains(fmt.Sprint(payload["error"]), "not reachable") {
			t.Fatalf("unreachable status error = %v, want reachability explanation", payload["error"])
		}

		listResponse := MakeAuthRequest(t, fixture.server, http.MethodGet,
			fmt.Sprintf("/items?workspace_id=%d", fixture.workspaceID), nil)
		AssertStatusCode(t, listResponse, http.StatusOK)
		var raw json.RawMessage
		DecodeJSON(t, listResponse, &raw)
		listResponse.Body.Close()
		var items []map[string]interface{}
		if len(raw) > 0 && raw[0] == '[' {
			if err := json.Unmarshal(raw, &items); err != nil {
				t.Fatalf("decode item list: %v", err)
			}
		} else {
			var envelope struct {
				Data  []map[string]interface{} `json:"data"`
				Items []map[string]interface{} `json:"items"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("decode item list envelope: %v", err)
			}
			items = envelope.Data
			if items == nil {
				items = envelope.Items
			}
		}
		for _, item := range items {
			if item["title"] == title {
				t.Fatalf("rejected item %q was persisted", title)
			}
		}
	})

	t.Run("condition_gated_first_hop_is_rejected", func(t *testing.T) {
		conditionResponse := MakeAuthRequest(t, fixture.server, http.MethodPost, "/condition-sets", map[string]interface{}{
			"name":        "Create override gate",
			"description": "create-status contract gate",
			"workflow_id": fixture.workflowID,
			"transition_conditions": []map[string]interface{}{{
				"transition_id": fixture.triageReadyID,
				"logic_mode":    "and",
				"conditions": []map[string]interface{}{{
					"condition_type": "field_value",
					"mode":           "condition",
					"display_order":  0,
					"config": map[string]interface{}{
						"field_identifier": "title", "pattern": "^never-matches$",
					},
				}},
			}},
		})
		AssertStatusCode(t, conditionResponse, http.StatusCreated)
		var conditionSet map[string]interface{}
		DecodeJSON(t, conditionResponse, &conditionSet)
		conditionResponse.Body.Close()
		conditionSetID := ExtractIDFromResponse(t, conditionSet)

		updateResponse := MakeAuthRequest(t, fixture.server, http.MethodPut,
			fmt.Sprintf("/configuration-sets/%d", fixture.configSetID), fixture.configPayload(&conditionSetID))
		AssertStatusCode(t, updateResponse, http.StatusOK)
		updateResponse.Body.Close()
		defer func() {
			clearResponse := MakeAuthRequest(t, fixture.server, http.MethodPut,
				fmt.Sprintf("/configuration-sets/%d", fixture.configSetID), fixture.configPayload(nil))
			clearResponse.Body.Close()
			deleteResponse := MakeAuthRequest(t, fixture.server, http.MethodDelete,
				fmt.Sprintf("/condition-sets/%d", conditionSetID), nil)
			deleteResponse.Body.Close()
		}()

		response := MakeAuthRequest(t, fixture.server, http.MethodPost, "/items", map[string]interface{}{
			"workspace_id": fixture.workspaceID, "title": "gated ready", "status_id": fixture.readyID,
		})
		AssertStatusCode(t, response, http.StatusBadRequest)
		var payload map[string]interface{}
		DecodeJSON(t, response, &payload)
		response.Body.Close()
		if !strings.Contains(fmt.Sprint(payload["error"]), "gated") {
			t.Fatalf("gated status error = %v, want gate explanation", payload["error"])
		}

		createStatusOverrideItem(t, fixture, "gated initial remains allowed", &fixture.triageID)
	})
}

func TestMandatoryItemTemplateHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)
	workspaceID, _ := CreateTestWorkspace(t, server, "Item template contract", shortKey("ITC"))

	typeResponse := MakeAuthRequest(t, server, http.MethodGet, "/item-types", nil)
	AssertStatusCode(t, typeResponse, http.StatusOK)
	var itemTypes []map[string]interface{}
	DecodeJSON(t, typeResponse, &itemTypes)
	typeResponse.Body.Close()
	if len(itemTypes) == 0 {
		t.Fatal("no item types seeded")
	}
	itemTypeID := ExtractIDFromResponse(t, itemTypes[0])
	for _, itemType := range itemTypes {
		if isDefault, _ := itemType["is_default"].(bool); isDefault {
			itemTypeID = ExtractIDFromResponse(t, itemType)
			break
		}
	}

	templateResponse := MakeAuthRequest(t, server, http.MethodPost, "/item-templates", map[string]interface{}{
		"workspace_id":     workspaceID,
		"name":             "Mandatory REST template",
		"description_body": "REST_BODY_MARKER scaffold",
		"mode":             "mandatory",
		"is_active":        true,
		"item_type_ids":    []int{itemTypeID},
	})
	AssertStatusCode(t, templateResponse, http.StatusCreated)
	templateResponse.Body.Close()

	createItem := func(title, description string) map[string]interface{} {
		t.Helper()
		response := MakeAuthRequest(t, server, http.MethodPost, "/items", map[string]interface{}{
			"workspace_id": workspaceID,
			"title":        title,
			"item_type_id": itemTypeID,
			"description":  description,
		})
		AssertStatusCode(t, response, http.StatusCreated)
		var item map[string]interface{}
		DecodeJSON(t, response, &item)
		response.Body.Close()
		return item
	}

	emptyDescriptionItem := createItem("empty description", "")
	if !strings.Contains(fmt.Sprint(emptyDescriptionItem["description"]), "REST_BODY_MARKER") {
		t.Fatalf("mandatory template was not applied to an empty description: %v", emptyDescriptionItem["description"])
	}

	suppliedDescriptionItem := createItem("supplied description", "my own description")
	if suppliedDescriptionItem["description"] != "my own description" {
		t.Fatalf("mandatory template overwrote supplied description: %v", suppliedDescriptionItem["description"])
	}
}

func TestPageLinkWorkspaceIsolationHTTPContract(t *testing.T) {
	server := newSecurityTestServer(t)
	workspaceID, _ := CreateTestWorkspace(t, server, "Page link source", shortKey("PLS"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Page link target", shortKey("PLT"))
	itemID := CreateTestItem(t, server, workspaceID, "Page link item")

	pageResponse := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/pages", otherWorkspaceID), map[string]interface{}{
			"title": "Foreign page", "content": "", "parent_id": nil, "is_home": false,
		})
	AssertStatusCode(t, pageResponse, http.StatusCreated)
	var page map[string]interface{}
	DecodeJSON(t, pageResponse, &page)
	pageResponse.Body.Close()
	pageID := ExtractIDFromResponse(t, page)

	typeResponse := MakeAuthRequest(t, server, http.MethodGet, "/link-types", nil)
	AssertStatusCode(t, typeResponse, http.StatusOK)
	var linkTypes []map[string]interface{}
	DecodeJSON(t, typeResponse, &linkTypes)
	typeResponse.Body.Close()
	pageLinkTypeID := 0
	for _, linkType := range linkTypes {
		if linkType["name"] == "Page" {
			pageLinkTypeID = ExtractIDFromResponse(t, linkType)
			break
		}
	}
	if pageLinkTypeID == 0 {
		t.Fatal("Page link type was not seeded")
	}

	response := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
		"link_type_id": pageLinkTypeID,
		"source_type":  "item",
		"source_id":    itemID,
		"target_type":  "page",
		"target_id":    pageID,
	})
	AssertStatusCode(t, response, http.StatusNotFound)
	response.Body.Close()
}

func adminUserID(t *testing.T, server *TestServer) int {
	t.Helper()

	resp := MakeAuthRequest(t, server, http.MethodGet, "/auth/me", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var payload struct {
		ID   int `json:"id"`
		User *struct {
			ID int `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode auth/me response: %v", err)
	}
	if payload.User != nil && payload.User.ID != 0 {
		return payload.User.ID
	}
	if payload.ID != 0 {
		return payload.ID
	}
	t.Fatal("auth/me response did not contain the admin user id")
	return 0
}

func attachmentIDs(t *testing.T, resp *http.Response) map[int]bool {
	t.Helper()

	var payload struct {
		Attachments []struct {
			ID int `json:"id"`
		} `json:"attachments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode attachment list: %v", err)
	}
	ids := make(map[int]bool, len(payload.Attachments))
	for _, attachment := range payload.Attachments {
		ids[attachment.ID] = true
	}
	return ids
}

func commentIDs(t *testing.T, resp *http.Response) map[int]bool {
	t.Helper()

	var payload struct {
		Comments []struct {
			ID int `json:"id"`
		} `json:"comments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode comment list: %v", err)
	}
	ids := make(map[int]bool, len(payload.Comments))
	for _, comment := range payload.Comments {
		ids[comment.ID] = true
	}
	return ids
}

func linkIDs(t *testing.T, resp *http.Response) map[int]bool {
	t.Helper()

	var payload struct {
		Outgoing []struct {
			ID int `json:"id"`
		} `json:"outgoing"`
		Incoming []struct {
			ID int `json:"id"`
		} `json:"incoming"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode link list: %v", err)
	}
	ids := make(map[int]bool, len(payload.Outgoing)+len(payload.Incoming))
	for _, link := range payload.Outgoing {
		ids[link.ID] = true
	}
	for _, link := range payload.Incoming {
		ids[link.ID] = true
	}
	return ids
}

func TestAttachmentAuthorizationHTTPContracts(t *testing.T) {
	t.Run("revoking_viewer_locks_the_same_download_url", func(t *testing.T) {
		server := newSecurityTestServer(t)
		workspaceID, _ := CreateTestWorkspace(t, server, "Attachment revoke", "ATREV")
		LockDownWorkspace(t, server, workspaceID)
		itemID := CreateTestItem(t, server, workspaceID, "Attachment revoke item")

		gateID, _, _ := CreateTestUserWithCredentials(t, server, "attachment_gate", "attachment_gate@test.com")
		memberID, memberName, memberPassword := CreateTestUserWithCredentials(t, server, "attachment_member", "attachment_member@test.com")
		AssignWorkspaceRole(t, server, gateID, workspaceID, "Viewer")
		AssignWorkspaceRole(t, server, memberID, workspaceID, "Viewer")
		memberCookie := CreateBearerTokenForUser(t, server, memberName, memberPassword)
		attachment := uploadItemAttachment(t, server, itemID, "revoke")

		pre := MakeAuthRequestWithToken(t, server, memberCookie, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatusForBody(t, pre, http.StatusOK)
		if got := responseBody(t, pre); !bytes.Equal(got, attachment.Bytes) {
			t.Fatalf("viewer download body changed before revocation: got %q want %q", got, attachment.Bytes)
		}

		roles := GetWorkspaceRoles(t, server)
		RevokeWorkspaceRole(t, server, memberID, workspaceID, roles["Viewer"])

		post := MakeAuthRequestWithToken(t, server, memberCookie, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatus(t, post, http.StatusNotFound)

		gateID2, gateName, gatePassword := CreateTestUserWithCredentials(t, server, "attachment_gate_two", "attachment_gate_two@test.com")
		AssignWorkspaceRole(t, server, gateID2, workspaceID, "Viewer")
		gateCookie := CreateBearerTokenForUser(t, server, gateName, gatePassword)
		gate := MakeAuthRequestWithToken(t, server, gateCookie, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatusForBody(t, gate, http.StatusOK)
		if got := responseBody(t, gate); !bytes.Equal(got, attachment.Bytes) {
			t.Fatalf("unrelated viewer download body changed: got %q want %q", got, attachment.Bytes)
		}
	})

	t.Run("cross_workspace_user_cannot_access_attachment_by_id", func(t *testing.T) {
		server := newSecurityTestServer(t)
		targetWorkspaceID, _ := CreateTestWorkspace(t, server, "Attachment target", "ATTGT")
		otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Attachment other", "ATOTH")
		LockDownWorkspace(t, server, targetWorkspaceID)
		LockDownWorkspace(t, server, otherWorkspaceID)
		itemID := CreateTestItem(t, server, targetWorkspaceID, "Private attachment item")
		attachment := uploadItemAttachment(t, server, itemID, "cross")

		memberID, memberName, memberPassword := CreateTestUserWithCredentials(t, server, "attachment_cross_member", "attachment_cross_member@test.com")
		AssignWorkspaceRole(t, server, memberID, otherWorkspaceID, "Editor")
		memberCookie := CreateBearerTokenForUser(t, server, memberName, memberPassword)

		ownWorkspace := MakeAuthRequestWithToken(t, server, memberCookie, http.MethodGet, fmt.Sprintf("/items/search?workspace_id=%d", otherWorkspaceID), nil)
		assertResponseStatus(t, ownWorkspace, http.StatusOK)

		for _, endpoint := range []string{
			fmt.Sprintf("/attachments/%d/download", attachment.ID),
			fmt.Sprintf("/attachments/%d/thumbnail", attachment.ID),
			fmt.Sprintf("/items/%d/attachments", itemID),
			fmt.Sprintf("/attachments/%d", attachment.ID),
		} {
			resp := MakeAuthRequestWithToken(t, server, memberCookie, map[string]string{
				fmt.Sprintf("/attachments/%d/download", attachment.ID):  http.MethodGet,
				fmt.Sprintf("/attachments/%d/thumbnail", attachment.ID): http.MethodGet,
				fmt.Sprintf("/items/%d/attachments", itemID):            http.MethodGet,
				fmt.Sprintf("/attachments/%d", attachment.ID):           http.MethodDelete,
			}[endpoint], endpoint, nil)
			assertResponseStatus(t, resp, http.StatusNotFound)
		}

		adminDownload := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatusForBody(t, adminDownload, http.StatusOK)
		if got := responseBody(t, adminDownload); !bytes.Equal(got, attachment.Bytes) {
			t.Fatalf("cross-workspace probe changed the attachment: got %q want %q", got, attachment.Bytes)
		}
	})

	t.Run("viewer_cannot_delete_admin_uploaded_attachment", func(t *testing.T) {
		server := newSecurityTestServer(t)
		workspaceID, _ := CreateTestWorkspace(t, server, "Attachment viewer", "ATVWR")
		LockDownWorkspace(t, server, workspaceID)
		itemID := CreateTestItem(t, server, workspaceID, "Viewer attachment item")
		attachment := uploadItemAttachment(t, server, itemID, "viewer")

		gateID, _, _ := CreateTestUserWithCredentials(t, server, "attachment_viewer_gate", "attachment_viewer_gate@test.com")
		viewerID, viewerName, viewerPassword := CreateTestUserWithCredentials(t, server, "attachment_viewer", "attachment_viewer@test.com")
		AssignWorkspaceRole(t, server, gateID, workspaceID, "Viewer")
		AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
		viewerCookie := CreateBearerTokenForUser(t, server, viewerName, viewerPassword)

		read := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatusForBody(t, read, http.StatusOK)
		_ = responseBody(t, read)

		deleteResp := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodDelete, fmt.Sprintf("/attachments/%d", attachment.ID), nil)
		assertResponseStatus(t, deleteResp, http.StatusNotFound)

		adminRead := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatusForBody(t, adminRead, http.StatusOK)
		if got := responseBody(t, adminRead); !bytes.Equal(got, attachment.Bytes) {
			t.Fatalf("rejected delete changed attachment body: got %q want %q", got, attachment.Bytes)
		}
	})

	t.Run("deleted_attachment_is_not_downloadable_or_listed", func(t *testing.T) {
		server := newSecurityTestServer(t)
		workspaceID, _ := CreateTestWorkspace(t, server, "Attachment delete", "ATDEL")
		itemID := CreateTestItem(t, server, workspaceID, "Deleted attachment item")
		attachment := uploadItemAttachment(t, server, itemID, "deleted")

		pre := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatusForBody(t, pre, http.StatusOK)
		_ = responseBody(t, pre)

		deleteResp := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/attachments/%d", attachment.ID), nil)
		if deleteResp.StatusCode != http.StatusOK && deleteResp.StatusCode != http.StatusNoContent {
			body := responseBody(t, deleteResp)
			t.Fatalf("delete attachment: got %d body=%s", deleteResp.StatusCode, body)
		}
		deleteResp.Body.Close()

		post := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/attachments/%d/download", attachment.ID), nil)
		assertResponseStatus(t, post, http.StatusNotFound)

		list := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/attachments", itemID), nil)
		assertResponseStatusForBody(t, list, http.StatusOK)
		if attachmentIDs(t, list)[attachment.ID] {
			t.Fatalf("deleted attachment %d remained in the item attachment list", attachment.ID)
		}
	})
}

func TestWorkspaceBackgroundHTTPContracts(t *testing.T) {
	t.Run("background_is_not_an_item_attachment", func(t *testing.T) {
		server := newSecurityTestServer(t)
		workspaceID, _ := CreateTestWorkspace(t, server, "Background isolation", "BGISO")
		itemID := CreateTestItem(t, server, workspaceID, "Background collision item")
		png := validTestPNG(t)

		resp := makeMultipartSessionRequest(t, server, http.MethodPost, "/attachments/upload", map[string]string{
			"entity_id":   fmt.Sprint(workspaceID),
			"entity_type": "workspace_background",
			"category":    "workspace_background",
		}, "background.png", "image/png", png, server.SessionCookie)
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			body := responseBody(t, resp)
			t.Fatalf("upload background: got %d body=%s", resp.StatusCode, body)
		}
		var payload struct {
			AttachmentID int `json:"attachment_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode background upload: %v", err)
		}
		resp.Body.Close()
		if payload.AttachmentID == 0 {
			t.Fatal("background upload did not return an attachment id")
		}

		list := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/attachments", itemID), nil)
		assertResponseStatusForBody(t, list, http.StatusOK)
		if attachmentIDs(t, list)[payload.AttachmentID] {
			t.Fatalf("workspace background %d leaked into item %d attachments", payload.AttachmentID, itemID)
		}
	})

	t.Run("non_admin_cannot_upload_background", func(t *testing.T) {
		server := newSecurityTestServer(t)
		workspaceID, _ := CreateTestWorkspace(t, server, "Background authorization", "BGAUTH")
		LockDownWorkspace(t, server, workspaceID)
		_, username, password := CreateTestUserWithCredentials(t, server, "background_non_admin", "background_non_admin@test.com")
		cookie := CreateBearerTokenForUser(t, server, username, password)

		resp := makeMultipartSessionRequest(t, server, http.MethodPost, "/attachments/upload", map[string]string{
			"entity_id":   fmt.Sprint(workspaceID),
			"entity_type": "workspace_background",
			"category":    "workspace_background",
		}, "unauthorized.png", "image/png", validTestPNG(t), cookie)
		assertResponseStatus(t, resp, http.StatusNotFound)
	})
}

func TestSearchPickerIsolationHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)
	targetWorkspaceID, _ := CreateTestWorkspace(t, server, "Search private", "SRCHP")
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Search other", "SRCHO")
	LockDownWorkspace(t, server, targetWorkspaceID)
	LockDownWorkspace(t, server, otherWorkspaceID)

	const secret = "SEARCH_PRIVATE_TOKEN_12345"
	CreateTestItem(t, server, targetWorkspaceID, secret+" private item")
	memberID, memberName, memberPassword := CreateTestUserWithCredentials(t, server, "search_outsider", "search_outsider@test.com")
	AssignWorkspaceRole(t, server, memberID, otherWorkspaceID, "Editor")
	memberCookie := CreateBearerTokenForUser(t, server, memberName, memberPassword)

	for _, endpoint := range []string{
		"/items/search?q=" + secret,
		fmt.Sprintf("/items/search?q=%s&workspace_id=%d", secret, targetWorkspaceID),
		"/links/search?q=" + secret + "&type=item&limit=50",
	} {
		resp := MakeAuthRequestWithToken(t, server, memberCookie, http.MethodGet, endpoint, nil)
		assertResponseStatusForBody(t, resp, http.StatusOK)
		if bytes.Contains(responseBody(t, resp), []byte(secret)) {
			t.Fatalf("private workspace title leaked from %s", endpoint)
		}
	}
}

func TestDirectIDAuthorizationHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)
	targetWorkspaceID, _ := CreateTestWorkspace(t, server, "Direct ID target", "DIDTG")
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Direct ID other", "DIDOT")
	LockDownWorkspace(t, server, targetWorkspaceID)
	LockDownWorkspace(t, server, otherWorkspaceID)

	targetItemID := CreateTestItem(t, server, targetWorkspaceID, "Direct ID item")
	linkedItemID := CreateTestItem(t, server, targetWorkspaceID, "Direct ID linked item")
	memberID, memberName, memberPassword := CreateTestUserWithCredentials(t, server, "direct_id_outsider", "direct_id_outsider@test.com")
	AssignWorkspaceRole(t, server, memberID, otherWorkspaceID, "Editor")
	memberCookie := CreateBearerTokenForUser(t, server, memberName, memberPassword)

	commentResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", targetItemID), map[string]interface{}{
		"content":    "private comment",
		"is_private": false,
	})
	AssertStatusCode(t, commentResp, http.StatusCreated)
	var commentPayload struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(commentResp.Body).Decode(&commentPayload); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	commentResp.Body.Close()

	linkResp := MakeAuthRequest(t, server, http.MethodPost, "/links", map[string]interface{}{
		"link_type_id": 4,
		"source_type":  "item",
		"source_id":    targetItemID,
		"target_type":  "item",
		"target_id":    linkedItemID,
	})
	AssertStatusCode(t, linkResp, http.StatusCreated)
	var linkPayload struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(linkResp.Body).Decode(&linkPayload); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	linkResp.Body.Close()

	adminID := adminUserID(t, server)
	labelResp := MakeAuthRequest(t, server, http.MethodPost, "/personal-labels", map[string]interface{}{
		"name":    "private-label",
		"color":   "#ff00aa",
		"user_id": adminID,
	})
	AssertStatusCode(t, labelResp, http.StatusCreated)
	var labelPayload struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(labelResp.Body).Decode(&labelPayload); err != nil {
		t.Fatalf("decode personal label: %v", err)
	}
	labelResp.Body.Close()

	denied := []struct {
		name   string
		method string
		path   string
		body   interface{}
	}{
		{"comment update", http.MethodPut, fmt.Sprintf("/comments/%d", commentPayload.ID), map[string]string{"content": "hijack"}},
		{"comment delete", http.MethodDelete, fmt.Sprintf("/comments/%d", commentPayload.ID), nil},
		{"link delete", http.MethodDelete, fmt.Sprintf("/links/%d", linkPayload.ID), nil},
		{"watch read", http.MethodGet, fmt.Sprintf("/items/%d/watch", targetItemID), nil},
		{"watch add", http.MethodPost, fmt.Sprintf("/items/%d/watch", targetItemID), map[string]interface{}{}},
		{"watch remove", http.MethodDelete, fmt.Sprintf("/items/%d/watch", targetItemID), nil},
		{"personal label update", http.MethodPut, fmt.Sprintf("/personal-labels/%d", labelPayload.ID), map[string]string{"name": "hijack", "color": "#000000"}},
		{"personal label delete", http.MethodDelete, fmt.Sprintf("/personal-labels/%d", labelPayload.ID), nil},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeAuthRequestWithToken(t, server, memberCookie, tc.method, tc.path, tc.body)
			assertResponseStatus(t, resp, http.StatusNotFound)
		})
	}

	comments := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/comments", targetItemID), nil)
	assertResponseStatusForBody(t, comments, http.StatusOK)
	if !commentIDs(t, comments)[commentPayload.ID] {
		t.Fatalf("denied comment operations removed or changed comment %d", commentPayload.ID)
	}

	links := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/links", targetItemID), nil)
	assertResponseStatusForBody(t, links, http.StatusOK)
	if !linkIDs(t, links)[linkPayload.ID] {
		t.Fatalf("denied link deletion removed link %d", linkPayload.ID)
	}

	label := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/personal-labels/%d", labelPayload.ID), nil)
	assertResponseStatusForBody(t, label, http.StatusOK)
	var labelState struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(label.Body).Decode(&labelState); err != nil {
		t.Fatalf("decode label after denied mutation: %v", err)
	}
	label.Body.Close()
	if labelState.Name != "private-label" {
		t.Fatalf("denied label mutation changed name to %q", labelState.Name)
	}
}

func TestPersonalLabelHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)
	ownerID := adminUserID(t, server)

	type labelPayload struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}

	created := make([]labelPayload, 0, 3)
	for _, tc := range []struct {
		name          string
		color         string
		expectedColor string
	}{
		{name: "chosen-colour", color: "#FF0000", expectedColor: "#FF0000"},
		{name: "default-colour", expectedColor: "#3B82F6"},
		{name: "invalid-colour", color: "banana", expectedColor: "#3B82F6"},
	} {
		body := map[string]interface{}{
			"name":    tc.name,
			"user_id": ownerID,
		}
		if tc.color != "" {
			body["color"] = tc.color
		}

		resp := MakeAuthRequest(t, server, http.MethodPost, "/personal-labels", body)
		assertResponseStatusForBody(t, resp, http.StatusCreated)
		var createdLabel labelPayload
		if err := json.NewDecoder(resp.Body).Decode(&createdLabel); err != nil {
			t.Fatalf("decode created personal label %q: %v", tc.name, err)
		}
		resp.Body.Close()
		if createdLabel.Name != tc.name {
			t.Fatalf("created label name = %q, want %q", createdLabel.Name, tc.name)
		}
		if createdLabel.Color != tc.expectedColor {
			t.Fatalf("created label %q color = %q, want %q", tc.name, createdLabel.Color, tc.expectedColor)
		}
		created = append(created, createdLabel)
	}

	listResp := MakeAuthRequest(t, server, http.MethodGet, "/personal-labels", nil)
	assertResponseStatusForBody(t, listResp, http.StatusOK)
	var labels []labelPayload
	if err := json.NewDecoder(listResp.Body).Decode(&labels); err != nil {
		t.Fatalf("decode personal label list: %v", err)
	}
	listResp.Body.Close()
	byID := make(map[int]labelPayload, len(labels))
	for _, label := range labels {
		byID[label.ID] = label
	}
	for _, want := range created {
		got, ok := byID[want.ID]
		if !ok {
			t.Fatalf("personal label list omitted created label %d (%q)", want.ID, want.Name)
		}
		if got.Name != want.Name || got.Color != want.Color {
			t.Fatalf("listed label %d = %#v, want name %q color %q", want.ID, got, want.Name, want.Color)
		}
	}

	workspaceID, _ := CreateTestWorkspace(t, server, "Personal label contracts", "PLCTR")
	itemID := CreateTestItem(t, server, workspaceID, "Personal label target")
	setResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/items/%d/personal-labels", itemID), map[string]interface{}{
		"label_ids": []int{created[0].ID},
	})
	assertResponseStatusForBody(t, setResp, http.StatusOK)
	var attached []labelPayload
	if err := json.NewDecoder(setResp.Body).Decode(&attached); err != nil {
		t.Fatalf("decode labels after assignment: %v", err)
	}
	setResp.Body.Close()
	if len(attached) != 1 || attached[0].ID != created[0].ID || attached[0].Name != created[0].Name || attached[0].Color != created[0].Color {
		t.Fatalf("labels after assignment = %#v, want only %#v", attached, created[0])
	}

	getItemResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/personal-labels", itemID), nil)
	assertResponseStatusForBody(t, getItemResp, http.StatusOK)
	var listedAttached []labelPayload
	if err := json.NewDecoder(getItemResp.Body).Decode(&listedAttached); err != nil {
		t.Fatalf("decode labels on item: %v", err)
	}
	getItemResp.Body.Close()
	if len(listedAttached) != 1 || listedAttached[0].ID != created[0].ID {
		t.Fatalf("labels on item = %#v, want label %d", listedAttached, created[0].ID)
	}
}

func TestKnowledgePagePermissionHTTPContracts(t *testing.T) {
	server := newSecurityTestServer(t)
	workspaceID, _ := CreateTestWorkspace(t, server, "Knowledge page contracts", "PGCTR")
	LockDownWorkspace(t, server, workspaceID)

	viewerID, viewerName, viewerPassword := CreateTestUserWithCredentials(t, server, "page_contract_viewer", "page_contract_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	viewerCookie := CreateBearerTokenForUser(t, server, viewerName, viewerPassword)

	type pagePayload struct {
		ID      int    `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	createPage := func(title, content string) pagePayload {
		t.Helper()
		resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/pages", workspaceID), map[string]interface{}{
			"title":   title,
			"content": content,
		})
		assertResponseStatusForBody(t, resp, http.StatusCreated)
		var page pagePayload
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatalf("decode created page %q: %v", title, err)
		}
		resp.Body.Close()
		if page.ID == 0 {
			t.Fatalf("created page %q has no id", title)
		}
		return page
	}

	openPage := createPage("Public runbook", "open page knowledge contract keyword")
	restrictedPage := createPage("Confidential", "restricted page knowledge contract keyword")
	inheritanceResp := MakeAuthRequest(t, server, http.MethodPatch, fmt.Sprintf("/workspaces/%d/pages/%d/inheritance", workspaceID, restrictedPage.ID), map[string]interface{}{
		"inherit_permissions": false,
	})
	assertResponseStatusForBody(t, inheritanceResp, http.StatusOK)

	t.Run("viewer tree includes open page and omits restricted page", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/pages/tree", workspaceID), nil)
		assertResponseStatusForBody(t, resp, http.StatusOK)
		var body struct {
			Pages []pagePayload `json:"pages"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode viewer page tree: %v", err)
		}
		resp.Body.Close()
		seen := make(map[int]bool, len(body.Pages))
		for _, page := range body.Pages {
			seen[page.ID] = true
		}
		if !seen[openPage.ID] {
			t.Fatalf("viewer tree omitted open page %d", openPage.ID)
		}
		if seen[restrictedPage.ID] {
			t.Fatalf("viewer tree exposed restricted page %d", restrictedPage.ID)
		}
	})

	t.Run("viewer can read open page but restricted page is indistinguishable from missing", func(t *testing.T) {
		openResp := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, openPage.ID), nil)
		assertResponseStatusForBody(t, openResp, http.StatusOK)
		var openState pagePayload
		if err := json.NewDecoder(openResp.Body).Decode(&openState); err != nil {
			t.Fatalf("decode open page: %v", err)
		}
		openResp.Body.Close()
		if openState.ID != openPage.ID || openState.Title != openPage.Title || openState.Content != openPage.Content {
			t.Fatalf("viewer open page = %#v, want %#v", openState, openPage)
		}

		restrictedResp := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, restrictedPage.ID), nil)
		assertResponseStatus(t, restrictedResp, http.StatusNotFound)
	})

	t.Run("viewer search excludes restricted page content", func(t *testing.T) {
		resp := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodGet, fmt.Sprintf("/workspaces/%d/knowledge/search?q=knowledge%%20contract%%20keyword", workspaceID), nil)
		assertResponseStatusForBody(t, resp, http.StatusOK)
		var body struct {
			Results []struct {
				PageID int `json:"page_id"`
			} `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode knowledge search: %v", err)
		}
		resp.Body.Close()
		seen := make(map[int]bool, len(body.Results))
		for _, result := range body.Results {
			seen[result.PageID] = true
		}
		if !seen[openPage.ID] {
			t.Fatalf("viewer search omitted open page %d", openPage.ID)
		}
		if seen[restrictedPage.ID] {
			t.Fatalf("viewer search exposed restricted page %d", restrictedPage.ID)
		}
	})

	t.Run("viewer cannot mutate restricted page and denied mutations do not persist", func(t *testing.T) {
		updateResp := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodPut, fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, restrictedPage.ID), map[string]interface{}{
			"title":   "hacked",
			"content": "hacked",
		})
		assertResponseStatus(t, updateResp, http.StatusNotFound)

		archiveResp := MakeAuthRequestWithToken(t, server, viewerCookie, http.MethodDelete, fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, restrictedPage.ID), nil)
		assertResponseStatus(t, archiveResp, http.StatusNotFound)

		adminResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/pages/%d", workspaceID, restrictedPage.ID), nil)
		assertResponseStatusForBody(t, adminResp, http.StatusOK)
		var persisted pagePayload
		if err := json.NewDecoder(adminResp.Body).Decode(&persisted); err != nil {
			t.Fatalf("decode restricted page after denied mutations: %v", err)
		}
		adminResp.Body.Close()
		if persisted.Title != restrictedPage.Title || persisted.Content != restrictedPage.Content {
			t.Fatalf("restricted page changed after denied mutations = %#v, want %#v", persisted, restrictedPage)
		}
	})
}

func TestPortalMagicLinkUsedHTTPContract(t *testing.T) {
	server := newSecurityTestServer(t)
	workspaceID, _ := CreateTestWorkspace(t, server, "Portal magic-link contracts", "MLCTR")
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)

	magicLinks := services.NewMagicLinkService(server.DB(), nil, server.BaseURL)
	customerEmail := "magic-link-contract@example.test"
	customerID, err := magicLinks.FindOrCreatePortalCustomer(customerEmail, "", channelID)
	if err != nil {
		t.Fatalf("create portal customer for magic-link contract: %v", err)
	}
	token, err := magicLinks.GenerateMagicLink(customerID, &channelID)
	if err != nil {
		t.Fatalf("generate magic-link contract token: %v", err)
	}

	firstResp := MakeUnauthenticatedRequest(t, server, http.MethodGet, fmt.Sprintf("/portal/%s/auth/verify?token=%s", portalSlug, url.QueryEscape(token)), nil)
	assertResponseStatusForBody(t, firstResp, http.StatusOK)

	secondResp := MakeUnauthenticatedRequest(t, server, http.MethodGet, fmt.Sprintf("/portal/%s/auth/verify?token=%s", portalSlug, url.QueryEscape(token)), nil)
	assertResponseStatusForBody(t, secondResp, http.StatusUnauthorized)
	var body struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Email   string `json:"email"`
	}
	if err := json.NewDecoder(secondResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode reused magic-link response: %v", err)
	}
	secondResp.Body.Close()
	if body.Success || body.Code != "used" || body.Email != customerEmail {
		t.Fatalf("reused magic-link body = %#v, want success=false code=used email=%q", body, customerEmail)
	}
}
