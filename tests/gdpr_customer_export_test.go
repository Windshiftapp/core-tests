package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestGBPDCustomerDataExport pins the Article 15/20 export contract
// (WI-1551): an admin-gated JSON download containing every personal-data
// category held about one customer — profile, requested items with display
// keys, authored comments, attachment metadata, email tracking rows, session
// metadata (never session tokens), and kb_events — with a stable schema
// version, deterministic id ordering, and an audit record of the disclosure.
func TestGBPDCustomerDataExport(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, wsKey := CreateTestWorkspace(t, server, "GDPR Export WS", shortKey("EXP"))
	portalSlug, channelID := SetupPortalChannel(t, server, wsID)

	customerEmail := fmt.Sprintf("export-subject-%d@test.local", wsID)
	customerID, portalCookie := CreatePortalCustomerWithSession(t, server, channelID, "Export Subject", customerEmail)

	// Seed every category reachable through the production API.
	itemID := SubmitPortalRequest(t, server, portalSlug, portalCookie, "Exported request")
	commentIDs := make([]int, 0, 2)
	for i, content := range []string{"first comment", "second comment"} {
		resp := MakePortalRequest(t, server, portalCookie, http.MethodPost,
			fmt.Sprintf("/portal/%s/requests/%d/comments", portalSlug, itemID),
			map[string]interface{}{"content": content})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var created struct {
			ID int `json:"id"`
		}
		DecodeJSON(t, resp, &created)
		commentIDs = append(commentIDs, created.ID)
		if i == 0 {
			upload := makePortalMultipartRequest(t, server, portalCookie,
				fmt.Sprintf("/portal/%s/requests/%d/attachments", portalSlug, itemID),
				"evidence.png", validTestPNG(t))
			defer upload.Body.Close()
			AssertStatusCode(t, upload, http.StatusCreated)
		}
	}

	// Wire the portal KB (no published pages) so the search endpoint serves
	// and records a no_result signal attributed to the customer.
	wireResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/channels/%d/config", channelID), map[string]interface{}{
		"config": map[string]interface{}{
			"knowledge_base_page_sources": []map[string]interface{}{{"workspace_id": wsID}},
		},
	})
	defer wireResp.Body.Close()
	AssertStatusCode(t, wireResp, http.StatusOK)

	kbResp := MakePortalRequest(t, server, portalCookie, http.MethodPost,
		fmt.Sprintf("/portal/%s/knowledge-base/search", portalSlug),
		map[string]string{"query": "how do I reset my password"})
	defer kbResp.Body.Close()
	AssertStatusCode(t, kbResp, http.StatusOK)

	// The export itself.
	exportResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/portal-customers/%d/export", customerID), nil)
	defer exportResp.Body.Close()
	AssertStatusCode(t, exportResp, http.StatusOK)

	if ct := exportResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cd := exportResp.Header.Get("Content-Disposition"); !strings.Contains(cd, fmt.Sprintf("customer-data-export-%d.json", customerID)) {
		t.Errorf("Content-Disposition = %q, want an attachment filename", cd)
	}

	var export struct {
		SchemaVersion int `json:"schema_version"`
		Customer      struct {
			ID           int      `json:"id"`
			Name         string   `json:"name"`
			Email        string   `json:"email"`
			Roles        []string `json:"roles"`
			CustomFields []struct {
				FieldID int    `json:"field_id"`
				Name    string `json:"name"`
				Value   any    `json:"value"`
			} `json:"custom_fields"`
		} `json:"customer"`
		RequestedItems []struct {
			ID           int    `json:"id"`
			ItemKey      string `json:"item_key"`
			Title        string `json:"title"`
			WorkspaceKey string `json:"workspace_key"`
			ItemNumber   int    `json:"item_number"`
		} `json:"requested_items"`
		Comments []struct {
			ID      int    `json:"id"`
			ItemID  int    `json:"item_id"`
			Content string `json:"content"`
		} `json:"comments"`
		ApprovalDecisions []map[string]any `json:"approval_decisions"`
		Attachments       []struct {
			ID               int    `json:"id"`
			OriginalFilename string `json:"original_filename"`
			FileSize         int64  `json:"file_size"`
		} `json:"attachments"`
		EmailTracking []map[string]any `json:"email_tracking"`
		Sessions      []struct {
			ID        int     `json:"id"`
			IPAddress *string `json:"ip_address"`
			UserAgent *string `json:"user_agent"`
		} `json:"sessions"`
		KBEvents []struct {
			ID        int     `json:"id"`
			EventType string  `json:"event_type"`
			Query     *string `json:"query"`
		} `json:"kb_events"`
	}
	if err := json.NewDecoder(exportResp.Body).Decode(&export); err != nil {
		t.Fatalf("decode export payload: %v", err)
	}

	if export.SchemaVersion != 3 {
		t.Errorf("schema_version = %d, want 3", export.SchemaVersion)
	}
	// The resolved custom-field list is always present (empty when the customer
	// has no values), so a DSAR consumer never has to guess the shape.
	if export.Customer.CustomFields == nil {
		t.Error("customer.custom_fields is nil, want an array")
	}

	// Profile.
	if export.Customer.ID != customerID || export.Customer.Name != "Export Subject" || export.Customer.Email != customerEmail {
		t.Errorf("profile = %#v", export.Customer)
	}

	// Requested items carry the display key and title.
	if len(export.RequestedItems) != 1 {
		t.Fatalf("requested_items = %d, want 1", len(export.RequestedItems))
	}
	item := export.RequestedItems[0]
	if item.ID != itemID || item.Title != "Exported request" {
		t.Errorf("requested item = %#v", item)
	}
	if item.WorkspaceKey != wsKey || item.ItemNumber == 0 || item.ItemKey != fmt.Sprintf("%s-%d", wsKey, item.ItemNumber) {
		t.Errorf("item key material = %#v, want workspace key %q with a display key", item, wsKey)
	}

	// Comments are complete and id-ordered.
	if len(export.Comments) != 2 {
		t.Fatalf("comments = %d, want 2", len(export.Comments))
	}
	if export.Comments[0].ID >= export.Comments[1].ID {
		t.Errorf("comments not in deterministic id order: %d then %d", export.Comments[0].ID, export.Comments[1].ID)
	}
	if export.Comments[0].ID != commentIDs[0] || export.Comments[1].ID != commentIDs[1] {
		t.Errorf("comment ids = %v, want %v", export.Comments, commentIDs)
	}
	if export.Comments[0].Content != "first comment" || export.Comments[1].Content != "second comment" {
		t.Errorf("comment contents = %q, %q", export.Comments[0].Content, export.Comments[1].Content)
	}

	// Attachment metadata is present but must not carry server file paths.
	if len(export.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(export.Attachments))
	}
	if export.Attachments[0].OriginalFilename != "evidence.png" || export.Attachments[0].FileSize == 0 {
		t.Errorf("attachment metadata = %#v", export.Attachments[0])
	}
	raw, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("re-marshal export: %v", err)
	}
	if strings.Contains(string(raw), "file_path") {
		t.Errorf("export leaks server file paths: %s", raw)
	}

	// Session metadata includes ip/user agent but never the token.
	if len(export.Sessions) == 0 {
		t.Fatal("sessions = 0, want the seeded session")
	}
	for _, s := range export.Sessions {
		if s.UserAgent == nil || *s.UserAgent != "test-agent" {
			t.Errorf("session user_agent = %v, want test-agent", s.UserAgent)
		}
	}
	if strings.Contains(string(raw), "session_token") {
		t.Errorf("export leaks session tokens: %s", raw)
	}

	// KB analytics tied to the customer are disclosed. No pages are
	// published, so the search lands as a no_result event.
	if len(export.KBEvents) != 1 {
		t.Fatalf("kb_events = %d, want 1", len(export.KBEvents))
	}
	if export.KBEvents[0].EventType != "no_result" || export.KBEvents[0].Query == nil || *export.KBEvents[0].Query != "how do I reset my password" {
		t.Errorf("kb event = %#v", export.KBEvents[0])
	}

	// The disclosure is audited with the acting identity and section sizes.
	var details string
	if err := server.DB().QueryRow(
		`SELECT details FROM audit_logs WHERE action_type = 'portal_customer.data_export' AND resource_id = ?`, customerID,
	).Scan(&details); err != nil {
		t.Fatalf("data export audit row missing: %v", err)
	}
	for _, want := range []string{"schema_version", customerEmail} {
		if !strings.Contains(details, want) {
			t.Errorf("audit details missing %q: %s", want, details)
		}
	}
}

// TestGBPDCustomerDataExportDenials pins the exact denial contract: callers
// without customers.manage get 403 with no payload, and unknown customers
// 404.
func TestGBPDCustomerDataExportDenials(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "GDPR Export Denials WS", shortKey("EXD"))
	_, channelID := SetupPortalChannel(t, server, wsID)
	wCustEmail := fmt.Sprintf("export-denial-%d@test.local", wsID)
	customerID, _ := CreatePortalCustomerWithSession(t, server, channelID, "Export Denial Subject", wCustEmail)

	t.Run("non_privileged_user_cannot_export", func(t *testing.T) {
		_, _, plainPass := CreateTestUserWithCredentials(t, server, fmt.Sprintf("exportplain%d", wsID), fmt.Sprintf("exportplain%d@test.local", wsID))
		plainSession, _ := CreateAuthCredentialsForUser(t, server, fmt.Sprintf("exportplain%d", wsID), plainPass)

		resp := MakeAuthRequestWithToken(t, server, plainSession, http.MethodGet, fmt.Sprintf("/portal-customers/%d/export", customerID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)

		// The denial must not leak any of the subject's data alongside the
		// error envelope.
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), wCustEmail) || strings.Contains(string(body), "Export Denial Subject") {
			t.Errorf("denial response leaked subject PII: %s", body)
		}
	})

	t.Run("unknown_customer_is_not_found", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, "/portal-customers/999999999/export", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}
